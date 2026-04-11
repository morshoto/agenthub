package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"agenthub/internal/codexauth"
	"agenthub/internal/config"
	"agenthub/internal/host"
	"agenthub/internal/prompt"
)

type slackDeployOptions struct {
	ConfigPath string
	Target     string
	SSHUser    string
	SSHKey     string
	SSHPort    int
	WorkingDir string
}

var resolveSlackDeployTarget = resolveHostTarget
var downloadCodexBinaryArchiveFunc = downloadCodexBinaryArchive
var extractCodexBinaryFunc = extractCodexBinary

const (
	codexBinaryArchiveURL = "https://github.com/openai/codex/releases/latest/download/codex-x86_64-unknown-linux-musl.tar.gz"
	codexBinaryName       = "codex-x86_64-unknown-linux-musl"
)

func newSlackDeployCommand(app *App) *cobra.Command {
	var target string
	var sshUser string
	var sshKey string
	var sshPort int
	var workingDir string
	var agentsDir string

	cmd := &cobra.Command{
		Use:     "deploy",
		Short:   "Deploy the Slack adapter to a remote EC2 host",
		GroupID: "integrations",
		RunE: func(cmd *cobra.Command, args []string) error {
			configPath := strings.TrimSpace(app.opts.ConfigPath)
			if configPath == "" {
				session := prompt.NewSession(cmd.InOrStdin(), cmd.OutOrStdout())
				selectedConfigPath, err := selectAgentConfigPath(session, agentsDir)
				if err != nil {
					return err
				}
				configPath = selectedConfigPath
			}
			agentCfg, err := config.Load(configPath)
			if err != nil {
				return err
			}
			if err := config.Validate(agentCfg); err != nil {
				return err
			}
			if !strings.EqualFold(strings.TrimSpace(agentCfg.Runtime.Provider), "codex") {
				return fmt.Errorf("slack deploy currently supports codex agents only (provider=%q)", agentCfg.Runtime.Provider)
			}
			targetValue := strings.TrimSpace(target)
			if targetValue == "" {
				targetValue = strings.TrimSpace(agentCfg.Infra.InstanceID)
			}
			if targetValue == "" {
				return errors.New("target is required: pass --target or run agenthub create first so infra.instance_id is recorded")
			}
			agentEnvPath := agentEnvPathFromConfigPath(configPath)
			agentEnv, err := loadAgentEnvFile(agentEnvPath)
			if err != nil {
				return err
			}
			hasCodexSecret := strings.TrimSpace(agentCfg.Runtime.Codex.SecretID) != ""

			logger := loggerFromContext(cmd.Context())
			logger.Info("starting slack deploy workflow")
			fmt.Fprintln(cmd.OutOrStdout(), "running slack deploy workflow...")
			progress := newProgressRenderer(cmd.OutOrStdout())
			resolvedTarget, err := runSlackDeployWorkflow(cmd.Context(), app.opts.Profile, agentCfg, agentEnv, slackDeployOptions{
				ConfigPath: configPath,
				Target:     targetValue,
				SSHUser:    sshUser,
				SSHKey:     sshKey,
				SSHPort:    sshPort,
				WorkingDir: workingDir,
			}, progress)
			if err != nil {
				details := "the SSH target is unreachable, the host AgentHub binary is missing, or Slack tokens are missing"
				nextStep := "confirm the EC2 host was prepared with agenthub create, then rerun " + commandRef(cmd.OutOrStdout(), "agenthub", "slack", "deploy")
				if hasCodexSecret {
					details = "the SSH target is unreachable, the host AgentHub binary is missing, or Slack tokens are missing"
					nextStep = "confirm runtime.codex.secret_id points to a readable AWS secret, then rerun " + commandRef(cmd.OutOrStdout(), "agenthub", "slack", "deploy")
				}
				return wrapUserFacingError(
					"slack deploy failed",
					err,
					details,
					nextStep,
					"verify the agent .env contains SLACK_BOT_TOKEN and SLACK_APP_TOKEN",
				)
			}
			agentName := agentNameFromConfigPath(configPath)
			fmt.Fprintf(cmd.OutOrStdout(), "slack adapter deployed to %s\n", resolvedTarget)
			fmt.Fprintf(cmd.OutOrStdout(), "service: %s\n", slackServiceNameForAgent(agentName))
			fmt.Fprintf(cmd.OutOrStdout(), "next step: check %s on the host if you want to confirm the service status\n", slackServiceUnitPathForAgent(agentName))
			return nil
		},
	}

	cmd.Flags().StringVar(&target, "target", "", "SSH target host or EC2 instance id; defaults to infra.instance_id")
	cmd.Flags().StringVar(&sshUser, "ssh-user", "", "SSH username for the target host")
	cmd.Flags().StringVar(&sshKey, "ssh-key", "", "path to the SSH private key")
	cmd.Flags().IntVar(&sshPort, "ssh-port", 22, "SSH port")
	cmd.Flags().StringVar(&workingDir, "working-dir", "/opt/agenthub", "remote working directory")
	cmd.Flags().StringVar(&agentsDir, "agents-dir", "agents", "path to the agents directory")
	return cmd
}

func runSlackDeployWorkflow(ctx context.Context, profile string, cfg *config.Config, agentEnv map[string]string, opts slackDeployOptions, progress stageRunner) (string, error) {
	if cfg == nil {
		return "", errors.New("config is required")
	}
	if progress == nil {
		progress = newProgressRenderer(os.Stdout)
	}

	botToken := strings.TrimSpace(agentEnv["SLACK_BOT_TOKEN"])
	appToken := strings.TrimSpace(agentEnv["SLACK_APP_TOKEN"])
	if botToken == "" || appToken == "" {
		return "", errors.New("agent env must contain SLACK_BOT_TOKEN and SLACK_APP_TOKEN")
	}
	codexAPIKey, err := resolveSlackDeployCodexAPIKey(ctx, profile, cfg, agentEnv)
	if err != nil {
		return "", err
	}
	targetValue := strings.TrimSpace(opts.Target)
	if targetValue == "" {
		targetValue = strings.TrimSpace(cfg.Infra.InstanceID)
	}
	if targetValue == "" {
		return "", errors.New("target is required")
	}

	agentName := agentNameFromConfigPath(opts.ConfigPath)
	if agentName == "" {
		agentName = "default"
	}
	remoteAgentDir := remoteSlackAgentDir(opts.WorkingDir, agentName)
	remoteConfigPath := pathJoin(remoteAgentDir, "config.yaml")
	remoteEnvPath := pathJoin(remoteAgentDir, ".env")
	stagedServicePath := pathJoin(remoteAgentDir, "agenthub-slack.service")
	remoteServicePath := slackServiceUnitPathForAgent(agentName)
	if remoteServicePath == "" {
		return "", errors.New("failed to build slack service unit path")
	}

	var resolvedTarget string
	if err := progress.Run(ctx, "resolving slack deploy target", func(runCtx context.Context) error {
		var err error
		resolvedTarget, err = resolveSlackDeployTarget(runCtx, profile, cfg, targetValue)
		return err
	}); err != nil {
		return "", err
	}
	user, keyPath, err := resolveInstallSSH(cfg, opts.SSHUser, opts.SSHKey)
	if err != nil {
		return "", err
	}
	exec := newSSHExecutor(host.SSHConfig{
		Host:           resolvedTarget,
		Port:           opts.SSHPort,
		User:           user,
		IdentityFile:   keyPath,
		ConnectTimeout: 15 * time.Second,
	})
	if err := progress.Run(ctx, "waiting for slack host ssh", func(runCtx context.Context) error {
		return waitForSSHReady(runCtx, exec, resolvedTarget)
	}); err != nil {
		return "", err
	}

	remoteBinaryPath := pathJoin(opts.WorkingDir, "bin", "agenthub")
	if err := progress.Run(ctx, "checking host agenthub binary", func(runCtx context.Context) error {
		if _, err := exec.Run(runCtx, "test", "-x", remoteBinaryPath); err != nil {
			return fmt.Errorf("check host agenthub binary %q: %w", remoteBinaryPath, err)
		}
		return nil
	}); err != nil {
		return "", err
	}

	if err := progress.Run(ctx, "ensuring codex on host", func(runCtx context.Context) error {
		return ensureCodexAvailable(runCtx, exec, remoteAgentDir)
	}); err != nil {
		return "", err
	}

	if strings.TrimSpace(codexAPIKey) == "" {
		if err := progress.Run(ctx, "syncing codex auth state", func(runCtx context.Context) error {
			return syncLocalCodexAuthState(runCtx, exec, remoteAgentDir)
		}); err != nil {
			return "", err
		}
	}

	if err := progress.Run(ctx, "preparing slack workspace", func(runCtx context.Context) error {
		if _, err := exec.Run(runCtx, "sudo", "mkdir", "-p", remoteAgentDir); err != nil {
			return fmt.Errorf("prepare slack agent directory: %w", err)
		}
		if _, err := exec.Run(runCtx, "sudo", "chown", "-R", "ubuntu:ubuntu", remoteAgentDir); err != nil {
			return fmt.Errorf("prepare slack agent directory ownership: %w", err)
		}
		return nil
	}); err != nil {
		return "", err
	}

	tmpDir, err := os.MkdirTemp("", "agenthub-slack-*")
	if err != nil {
		return "", fmt.Errorf("create temporary slack deployment workspace: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	localConfigPath := filepath.Join(tmpDir, "config.yaml")
	if err := config.Save(localConfigPath, cfg); err != nil {
		return "", fmt.Errorf("write slack agent config: %w", err)
	}
	localEnvPath := filepath.Join(tmpDir, ".env")
	if err := os.WriteFile(localEnvPath, []byte(strings.Join([]string{
		"SLACK_BOT_TOKEN=" + botToken,
		"SLACK_APP_TOKEN=" + appToken,
	}, "\n")+"\n"), 0o600); err != nil {
		return "", fmt.Errorf("write slack agent env: %w", err)
	}
	if strings.TrimSpace(codexAPIKey) != "" {
		if err := appendEnvFile(localEnvPath, map[string]string{
			"OPENAI_API_KEY": strings.TrimSpace(codexAPIKey),
		}); err != nil {
			return "", fmt.Errorf("write slack agent codex env: %w", err)
		}
	}
	if err := progress.Run(ctx, "uploading slack files", func(runCtx context.Context) error {
		if err := exec.Upload(runCtx, localConfigPath, remoteConfigPath); err != nil {
			return fmt.Errorf("upload slack agent config: %w", err)
		}
		if err := exec.Upload(runCtx, localEnvPath, remoteEnvPath); err != nil {
			return fmt.Errorf("upload slack agent env: %w", err)
		}
		return nil
	}); err != nil {
		return "", err
	}

	localUnitPath := filepath.Join(tmpDir, "agenthub-slack.service")
	if err := os.WriteFile(localUnitPath, []byte(renderSlackSystemdUnit(remoteBinaryPath, remoteConfigPath, remoteEnvPath, remoteAgentDir, agentName)), 0o600); err != nil {
		return "", fmt.Errorf("write slack systemd unit: %w", err)
	}
	if err := progress.Run(ctx, "installing slack service", func(runCtx context.Context) error {
		if err := exec.Upload(runCtx, localUnitPath, stagedServicePath); err != nil {
			return fmt.Errorf("upload slack systemd unit: %w", err)
		}
		if _, err := exec.Run(runCtx, "sudo", "mv", stagedServicePath, remoteServicePath); err != nil {
			return fmt.Errorf("install slack systemd unit: %w", err)
		}
		if _, err := exec.Run(runCtx, "sudo", "chmod", "600", remoteEnvPath); err != nil {
			return fmt.Errorf("secure slack env file: %w", err)
		}
		if _, err := exec.Run(runCtx, "sudo", "systemctl", "daemon-reload"); err != nil {
			return fmt.Errorf("reload systemd after slack deploy: %w", err)
		}
		if _, err := exec.Run(runCtx, "sudo", "systemctl", "enable", "--now", filepath.Base(remoteServicePath)); err != nil {
			return fmt.Errorf("enable slack service: %w", err)
		}
		return nil
	}); err != nil {
		return "", err
	}

	return resolvedTarget, nil
}

func resolveSlackDeployCodexAPIKey(ctx context.Context, profile string, cfg *config.Config, agentEnv map[string]string) (string, error) {
	if cfg == nil {
		return "", errors.New("config is required")
	}

	if secretID := strings.TrimSpace(cfg.Runtime.Codex.SecretID); secretID != "" {
		key, err := codexauth.LoadAPIKeyFunc(ctx, profile, cfg.Region.Name, secretID)
		if err != nil {
			return "", fmt.Errorf("load codex api key from secret %q: %w", secretID, err)
		}
		if strings.TrimSpace(key) == "" {
			return "", fmt.Errorf("codex secret %q did not return an api key", secretID)
		}
		return strings.TrimSpace(key), nil
	}

	return strings.TrimSpace(agentEnv["OPENAI_API_KEY"]), nil
}

func appendEnvFile(path string, values map[string]string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			data = nil
		} else {
			return fmt.Errorf("read env file %q: %w", path, err)
		}
	}
	lines := []string{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		if strings.HasPrefix(line, "#") {
			lines = append(lines, line)
			continue
		}
		key, _, ok := strings.Cut(line, "=")
		if !ok {
			lines = append(lines, line)
			continue
		}
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if _, exists := values[key]; exists {
			continue
		}
		lines = append(lines, line)
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		lines = append(lines, fmt.Sprintf("%s=%s", key, values[key]))
	}
	lines = append(lines, "")
	return os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o600)
}

func ensureCodexAvailable(ctx context.Context, exec host.Executor, remoteAgentDir string) error {
	if exec == nil {
		return errors.New("codex check requires a host executor")
	}
	if _, err := exec.Run(ctx, "command", "-v", "codex"); err == nil {
		return nil
	}

	archivePath, err := downloadCodexBinaryArchiveFunc(ctx)
	if err != nil {
		return fmt.Errorf("download codex binary: %w", err)
	}
	binaryPath, err := extractCodexBinaryFunc(ctx, archivePath)
	if err != nil {
		return fmt.Errorf("extract codex binary: %w", err)
	}

	stagedBinaryPath := pathJoin(remoteAgentDir, "codex.upload")
	if err := exec.Upload(ctx, binaryPath, stagedBinaryPath); err != nil {
		return fmt.Errorf("upload codex binary: %w", err)
	}
	if _, err := exec.Run(ctx, "sudo", "mv", stagedBinaryPath, "/usr/local/bin/codex"); err != nil {
		return fmt.Errorf("install codex on target host: %w", err)
	}
	if _, err := exec.Run(ctx, "sudo", "chmod", "755", "/usr/local/bin/codex"); err != nil {
		return fmt.Errorf("prepare codex on target host: %w", err)
	}
	return nil
}

func syncLocalCodexAuthState(ctx context.Context, exec host.Executor, remoteAgentDir string) error {
	if exec == nil {
		return errors.New("codex auth sync requires a host executor")
	}
	localHome, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("locate local home directory: %w", err)
	}

	localCodexDir := filepath.Join(localHome, ".codex")
	remoteCodexDir := pathJoin(remoteAgentDir, ".codex")
	remoteCodexTargetDir := "/home/ubuntu/.codex"
	authFiles := []string{"auth.json", "config.toml"}

	foundAny := false
	for _, name := range authFiles {
		localPath := filepath.Join(localCodexDir, name)
		if _, err := os.Stat(localPath); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return fmt.Errorf("stat local codex auth file %q: %w", localPath, err)
		}
		foundAny = true
		remotePath := pathJoin(remoteCodexDir, name)
		if err := exec.Upload(ctx, localPath, remotePath); err != nil {
			return fmt.Errorf("upload codex auth file %q: %w", name, err)
		}
	}
	if !foundAny {
		return errors.New("codex authentication is missing on the local machine; run `agenthub onboard --auth-choice openai-codex` or set runtime.codex.secret_id")
	}
	if _, err := exec.Run(ctx, "sudo", "rm", "-rf", remoteCodexTargetDir); err != nil {
		return fmt.Errorf("prepare codex auth directory: %w", err)
	}
	if _, err := exec.Run(ctx, "sudo", "mv", remoteCodexDir, remoteCodexTargetDir); err != nil {
		return fmt.Errorf("install codex auth state: %w", err)
	}
	if _, err := exec.Run(ctx, "sudo", "chown", "-R", "ubuntu:ubuntu", remoteCodexTargetDir); err != nil {
		return fmt.Errorf("prepare codex auth ownership: %w", err)
	}
	if _, err := exec.Run(ctx, "sudo", "chmod", "700", remoteCodexTargetDir); err != nil {
		return fmt.Errorf("prepare codex auth directory permissions: %w", err)
	}
	for _, name := range authFiles {
		targetPath := pathJoin(remoteCodexTargetDir, name)
		if _, err := exec.Run(ctx, "sudo", "chmod", "600", targetPath); err != nil {
			return fmt.Errorf("secure codex auth file %q: %w", name, err)
		}
	}
	return nil
}

func downloadCodexBinaryArchive(ctx context.Context) (string, error) {
	tmpDir, err := os.MkdirTemp("", "agenthub-codex-archive-*")
	if err != nil {
		return "", fmt.Errorf("create temporary codex archive workspace: %w", err)
	}

	archivePath := filepath.Join(tmpDir, "codex.tgz")
	cmd := exec.CommandContext(ctx, "curl", "-fsSL", "-o", archivePath, codexBinaryArchiveURL)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg != "" {
			return "", fmt.Errorf("download codex archive: %s: %w", msg, err)
		}
		return "", fmt.Errorf("download codex archive: %w", err)
	}
	return archivePath, nil
}

func extractCodexBinary(ctx context.Context, archivePath string) (string, error) {
	archivePath = strings.TrimSpace(archivePath)
	if archivePath == "" {
		return "", errors.New("codex archive path is required")
	}
	tmpDir, err := os.MkdirTemp("", "agenthub-codex-bin-*")
	if err != nil {
		return "", fmt.Errorf("create temporary codex binary workspace: %w", err)
	}
	cmd := exec.CommandContext(ctx, "tar", "-xzf", archivePath, "-C", tmpDir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg != "" {
			return "", fmt.Errorf("extract codex archive: %s: %w", msg, err)
		}
		return "", fmt.Errorf("extract codex archive: %w", err)
	}
	return filepath.Join(tmpDir, codexBinaryName), nil
}

func renderSlackSystemdUnit(binaryPath, configPath, envFilePath, agentDir, agentName string) string {
	return fmt.Sprintf(`[Unit]
Description=AgentHub Slack adapter (%s)
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=ubuntu
Group=ubuntu
WorkingDirectory=%s
EnvironmentFile=-%s
ExecStart=%s slack serve --config %s
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
`, strings.TrimSpace(agentName), strings.TrimSpace(agentDir), strings.TrimSpace(envFilePath), strings.TrimSpace(binaryPath), strings.TrimSpace(configPath))
}

func agentNameFromConfigPath(configPath string) string {
	configPath = strings.TrimSpace(configPath)
	if configPath == "" {
		return ""
	}
	return filepath.Base(filepath.Dir(configPath))
}

func pathJoin(elem ...string) string {
	parts := make([]string, 0, len(elem))
	for _, part := range elem {
		parts = append(parts, strings.TrimRight(strings.TrimSpace(part), "/"))
	}
	return strings.Join(parts, "/")
}

func remoteSlackAgentDir(workdir, agentName string) string {
	agentName = strings.TrimSpace(agentName)
	if agentName == "" {
		agentName = "default"
	}
	return pathJoin(workdir, "agents", agentName)
}

func slackServiceNameForAgent(agentName string) string {
	return slackServiceName(agentName)
}

func slackServiceUnitPathForAgent(agentName string) string {
	name := slackServiceNameForAgent(agentName)
	if name == "" {
		return ""
	}
	return pathJoin("/etc/systemd/system", name+".service")
}

func slackServiceName(agentName string) string {
	agentName = strings.ToLower(strings.TrimSpace(agentName))
	if agentName == "" {
		agentName = "default"
	}
	var b strings.Builder
	for _, r := range agentName {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	return "agenthub-slack-" + strings.Trim(b.String(), "-")
}
