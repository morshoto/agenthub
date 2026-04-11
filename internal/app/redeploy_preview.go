package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"agenthub/internal/config"
	"agenthub/internal/host"
	"agenthub/internal/runtimeinstall"
)

type redeployPreviewFile struct {
	Label  string
	Path   string
	Status string
	Diff   string
}

type redeployPreviewResult struct {
	Files      []redeployPreviewFile
	BinaryPath string
}

func runRedeployPreviewWorkflow(ctx context.Context, profile string, cfg *config.Config, opts installOptions) (redeployPreviewResult, string, error) {
	if cfg == nil {
		return redeployPreviewResult{}, "", errors.New("config is required")
	}
	if strings.TrimSpace(opts.Target) == "" {
		return redeployPreviewResult{}, "", errors.New("target is required")
	}

	networkMode := effectiveNetworkMode(cfg)
	if networkMode == "private" {
		return redeployPreviewResult{}, "", errors.New("private networking is not supported yet; redeploy requires SSH access to the instance")
	}
	if !config.IsValidNetworkMode(networkMode) && networkMode != "" {
		return redeployPreviewResult{}, "", fmt.Errorf("unsupported network mode %q", networkMode)
	}

	resolvedTarget, err := resolveHostTarget(ctx, profile, cfg, opts.Target)
	if err != nil {
		return redeployPreviewResult{}, "", err
	}

	user, keyPath, err := resolveInstallSSH(cfg, opts.SSHUser, opts.SSHKey)
	if err != nil {
		return redeployPreviewResult{}, "", err
	}
	exec := newSSHExecutor(host.SSHConfig{
		Host:           resolvedTarget,
		Port:           opts.SSHPort,
		User:           user,
		IdentityFile:   keyPath,
		ConnectTimeout: 15 * time.Second,
	})
	if err := waitForSSHReady(ctx, exec, resolvedTarget); err != nil {
		return redeployPreviewResult{}, "", err
	}

	useNemo := cfg.Sandbox.UseNemoClaw
	if opts.UseNemoClaw {
		useNemo = true
	}
	if opts.DisableNemoClaw {
		useNemo = false
	}
	codexAPIKey, err := resolveCodexAPIKey(ctx, profile, cfg)
	if err != nil {
		return redeployPreviewResult{}, "", err
	}

	artifacts, err := runtimeinstall.PreviewManagedArtifacts(runtimeinstall.Request{
		Config:       cfg,
		UseNemoClaw:  &useNemo,
		Port:         opts.Port,
		WorkingDir:   opts.WorkingDir,
		ComputeClass: cfg.Compute.Class,
		CodexAPIKey:  codexAPIKey,
	})
	if err != nil {
		return redeployPreviewResult{}, "", err
	}

	files := []struct {
		label   string
		path    string
		content string
	}{
		{label: "runtime config", path: artifacts.RuntimeConfigPath, content: string(artifacts.RuntimeConfig)},
		{label: "systemd unit", path: artifacts.ServicePath, content: string(artifacts.ServiceUnit)},
	}
	if strings.TrimSpace(artifacts.ProviderEnvPath) != "" {
		files = append(files, struct {
			label   string
			path    string
			content string
		}{
			label:   "provider environment",
			path:    artifacts.ProviderEnvPath,
			content: string(artifacts.ProviderEnv),
		})
	}

	result := redeployPreviewResult{BinaryPath: artifacts.BinaryPath}
	for _, file := range files {
		current, exists, err := readRemoteManagedFile(ctx, exec, file.path)
		if err != nil {
			return redeployPreviewResult{}, resolvedTarget, err
		}
		status, diff := previewFileStatus(current, file.content, exists, file.path)
		result.Files = append(result.Files, redeployPreviewFile{
			Label:  file.label,
			Path:   file.path,
			Status: status,
			Diff:   diff,
		})
	}

	return result, resolvedTarget, nil
}

func readRemoteManagedFile(ctx context.Context, exec host.Executor, path string) (string, bool, error) {
	if _, err := exec.Run(ctx, "sudo", "test", "-f", path); err != nil {
		var remoteErr *host.RemoteCommandError
		if errors.As(err, &remoteErr) && remoteErr.ExitCode == 1 {
			return "", false, nil
		}
		return "", false, fmt.Errorf("check remote file %q: %w", path, err)
	}
	result, err := exec.Run(ctx, "sudo", "cat", path)
	if err != nil {
		return "", true, fmt.Errorf("read remote file %q: %w", path, err)
	}
	return result.Stdout, true, nil
}

func previewFileStatus(current, proposed string, exists bool, path string) (string, string) {
	if !exists {
		return "would create", buildUnifiedLineDiff("", proposed, path)
	}
	if normalizeTrailingNewline(current) == normalizeTrailingNewline(proposed) {
		return "unchanged", ""
	}
	return "would update", buildUnifiedLineDiff(current, proposed, path)
}

func printRedeployPreview(out io.Writer, preview redeployPreviewResult) {
	fmt.Fprintln(out, "dry-run preview")
	for _, file := range preview.Files {
		fmt.Fprintf(out, "- %s: %s\n", file.Label, file.Status)
		fmt.Fprintf(out, "  path: %s\n", file.Path)
		if strings.TrimSpace(file.Diff) != "" {
			fmt.Fprintln(out, file.Diff)
		}
	}
	if strings.TrimSpace(preview.BinaryPath) != "" {
		fmt.Fprintf(out, "- runtime binary: would replace\n")
		fmt.Fprintf(out, "  path: %s\n", preview.BinaryPath)
	}
}

func buildUnifiedLineDiff(before, after, path string) string {
	beforeLines := splitPreviewLines(before)
	afterLines := splitPreviewLines(after)
	ops := diffLines(beforeLines, afterLines)

	var b strings.Builder
	fmt.Fprintf(&b, "  --- current %s\n", path)
	fmt.Fprintf(&b, "  +++ proposed %s\n", path)
	for _, op := range ops {
		prefix := " "
		switch op.kind {
		case '-':
			prefix = "-"
		case '+':
			prefix = "+"
		}
		fmt.Fprintf(&b, "  %s%s\n", prefix, op.line)
	}
	return strings.TrimRight(b.String(), "\n")
}

type diffLineOp struct {
	kind rune
	line string
}

func diffLines(before, after []string) []diffLineOp {
	dp := make([][]int, len(before)+1)
	for i := range dp {
		dp[i] = make([]int, len(after)+1)
	}
	for i := len(before) - 1; i >= 0; i-- {
		for j := len(after) - 1; j >= 0; j-- {
			if before[i] == after[j] {
				dp[i][j] = dp[i+1][j+1] + 1
				continue
			}
			if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}

	ops := make([]diffLineOp, 0, len(before)+len(after))
	i, j := 0, 0
	for i < len(before) && j < len(after) {
		if before[i] == after[j] {
			ops = append(ops, diffLineOp{kind: ' ', line: before[i]})
			i++
			j++
			continue
		}
		if dp[i+1][j] >= dp[i][j+1] {
			ops = append(ops, diffLineOp{kind: '-', line: before[i]})
			i++
			continue
		}
		ops = append(ops, diffLineOp{kind: '+', line: after[j]})
		j++
	}
	for ; i < len(before); i++ {
		ops = append(ops, diffLineOp{kind: '-', line: before[i]})
	}
	for ; j < len(after); j++ {
		ops = append(ops, diffLineOp{kind: '+', line: after[j]})
	}
	return ops
}

func splitPreviewLines(value string) []string {
	value = normalizeTrailingNewline(value)
	if value == "" {
		return nil
	}
	return strings.Split(value, "\n")
}

func normalizeTrailingNewline(value string) string {
	return strings.TrimRight(value, "\n")
}
