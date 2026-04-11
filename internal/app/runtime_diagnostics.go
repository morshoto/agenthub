package app

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"agenthub/internal/config"
	"agenthub/internal/host"
)

const defaultDiagnosticsLogLines = 100

var diagnosticsNowFunc = func() time.Time {
	return time.Now().UTC()
}

type runtimeDiagnosticsOptions struct {
	ConfigPath        string
	Target            string
	SSHUser           string
	SSHKey            string
	SSHPort           int
	Lines             int
	OutputPath        string
	RuntimeConfigPath string
	Version           string
}

type runtimeDiagnosticsResult struct {
	AgentName   string
	Target      string
	OutputPath  string
	Warnings    []string
	Collected   []string
	GeneratedAt time.Time
}

type diagnosticsManifest struct {
	Agent       string   `json:"agent"`
	Target      string   `json:"target,omitempty"`
	Version     string   `json:"version,omitempty"`
	GeneratedAt string   `json:"generated_at"`
	Collected   []string `json:"collected,omitempty"`
	Warnings    []string `json:"warnings,omitempty"`
}

type diagnosticsCloudPayload struct {
	State    string                  `json:"state"`
	Error    string                  `json:"error,omitempty"`
	Instance *statusJSONLiveInstance `json:"instance,omitempty"`
}

type diagnosticsServicesPayload struct {
	Runtime     inspectJSONServiceState `json:"runtime"`
	Integration inspectJSONServiceState `json:"integration"`
}

func newRuntimeDiagnosticsCommand(app *App) *cobra.Command {
	var target string
	var sshUser string
	var sshKey string
	var sshPort int
	var lines int
	var outputPath string
	var runtimeConfigPath string

	cmd := &cobra.Command{
		Use:          "diagnostics",
		Short:        "Collect a diagnostics bundle for a deployed agent",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(app.opts.ConfigPath) == "" {
				return errors.New("config file is required: pass --config <path>")
			}
			fmt.Fprintln(cmd.OutOrStdout(), "collecting diagnostics bundle...")

			result, err := runRuntimeDiagnosticsWorkflow(cmd.Context(), app.opts.Profile, runtimeDiagnosticsOptions{
				ConfigPath:        app.opts.ConfigPath,
				Target:            target,
				SSHUser:           sshUser,
				SSHKey:            sshKey,
				SSHPort:           sshPort,
				Lines:             lines,
				OutputPath:        outputPath,
				RuntimeConfigPath: runtimeConfigPath,
				Version:           Version,
			})
			printRuntimeDiagnosticsResult(cmd.OutOrStdout(), result)
			if err != nil {
				return wrapUserFacingError(
					"runtime diagnostics failed",
					err,
					"the diagnostics bundle could not be collected from the target host",
					"confirm the host is reachable over SSH and rerun "+commandRef(cmd.OutOrStdout(), "agenthub", "runtime", "diagnostics", "--config", app.opts.ConfigPath),
					"run "+commandRef(cmd.OutOrStdout(), "agenthub", "inspect", agentNameFromConfigPath(app.opts.ConfigPath))+" to inspect the current host state",
				)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&target, "target", "", "SSH target host or EC2 instance id; defaults to infra.instance_id")
	cmd.Flags().StringVar(&sshUser, "ssh-user", "", "SSH username for the target host")
	cmd.Flags().StringVar(&sshKey, "ssh-key", "", "path to the SSH private key")
	cmd.Flags().IntVar(&sshPort, "ssh-port", 22, "SSH port")
	cmd.Flags().IntVar(&lines, "lines", defaultDiagnosticsLogLines, "number of recent log lines to fetch per service")
	cmd.Flags().StringVar(&outputPath, "output", "", "destination path for the diagnostics archive")
	cmd.Flags().StringVar(&runtimeConfigPath, "runtime-config", "/opt/agenthub/runtime.yaml", "path to the runtime config on the target host")
	return cmd
}

func runRuntimeDiagnosticsWorkflow(ctx context.Context, profile string, opts runtimeDiagnosticsOptions) (runtimeDiagnosticsResult, error) {
	if strings.TrimSpace(opts.ConfigPath) == "" {
		return runtimeDiagnosticsResult{}, errors.New("config path is required")
	}
	if opts.Lines <= 0 {
		return runtimeDiagnosticsResult{}, errors.New("lines must be greater than 0")
	}

	cfg, err := config.Load(opts.ConfigPath)
	if err != nil {
		return runtimeDiagnosticsResult{}, err
	}
	if err := config.Validate(cfg); err != nil {
		return runtimeDiagnosticsResult{}, err
	}

	agentName := agentNameFromConfigPath(opts.ConfigPath)
	if agentName == "" {
		agentName = "default"
	}

	targetValue := strings.TrimSpace(opts.Target)
	if targetValue == "" {
		targetValue = strings.TrimSpace(cfg.Infra.InstanceID)
	}
	if targetValue == "" {
		return runtimeDiagnosticsResult{}, errors.New("target is required: pass --target or run agenthub create first so infra.instance_id is recorded")
	}

	resolvedTarget, err := resolveHostTarget(ctx, profile, cfg, targetValue)
	if err != nil {
		return runtimeDiagnosticsResult{}, err
	}
	user, keyPath, err := resolveInstallSSH(cfg, opts.SSHUser, opts.SSHKey)
	if err != nil {
		return runtimeDiagnosticsResult{}, err
	}

	exec := newSSHExecutor(host.SSHConfig{
		Host:           resolvedTarget,
		Port:           opts.SSHPort,
		User:           user,
		IdentityFile:   keyPath,
		ConnectTimeout: 15 * time.Second,
	})
	if err := waitForSSHReady(ctx, exec, resolvedTarget); err != nil {
		return runtimeDiagnosticsResult{}, err
	}

	generatedAt := diagnosticsNowFunc().UTC()
	result := runtimeDiagnosticsResult{
		AgentName:   agentName,
		Target:      resolvedTarget,
		GeneratedAt: generatedAt,
	}
	outputPath := strings.TrimSpace(opts.OutputPath)
	if outputPath == "" {
		outputPath = defaultDiagnosticsOutputPath(agentName, generatedAt)
	}
	absOutputPath, err := filepath.Abs(outputPath)
	if err != nil {
		return result, fmt.Errorf("resolve output path: %w", err)
	}
	result.OutputPath = absOutputPath

	tmpDir, err := os.MkdirTemp("", "agenthub-diagnostics-*")
	if err != nil {
		return result, fmt.Errorf("create temporary diagnostics workspace: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	cloudPayload := diagnosticsCloudPayload{}
	cloud, cloudErr := inspectCloudState(ctx, profile, *cfg)
	liveStatus := buildStatusJSONLiveStatus(agentStatusEntry{
		Config: *cfg,
		Live: agentLiveStatus{
			Instance: cloud,
			Err:      cloudErr,
		},
	})
	cloudPayload.State = liveStatus.State
	cloudPayload.Error = strings.TrimSpace(liveStatus.Error)
	cloudPayload.Instance = liveStatus.Instance
	if err := writeJSONFile(filepath.Join(tmpDir, "cloud.json"), cloudPayload); err != nil {
		return result, err
	}
	result.Collected = append(result.Collected, "cloud")
	if cloudErr != nil {
		result.Warnings = append(result.Warnings, fmt.Sprintf("cloud state unavailable: %v", cloudErr))
	}

	if err := writeYAMLFile(filepath.Join(tmpDir, "local-config.yaml"), cfg); err != nil {
		return result, err
	}
	result.Collected = append(result.Collected, "local-config")

	runtimeConfigPath := strings.TrimSpace(opts.RuntimeConfigPath)
	if runtimeConfigPath == "" {
		runtimeConfigPath = "/opt/agenthub/runtime.yaml"
	}

	runtimeCfg, runtimeCfgErr := readRemoteRuntimeConfig(ctx, exec, runtimeConfigPath)
	if runtimeCfgErr != nil {
		result.Warnings = append(result.Warnings, fmt.Sprintf("remote runtime config unavailable: %v", runtimeCfgErr))
	} else {
		if err := writeYAMLFile(filepath.Join(tmpDir, "remote", "runtime-config.yaml"), runtimeCfg); err != nil {
			return result, err
		}
		result.Collected = append(result.Collected, "remote-runtime-config")
	}

	runtimeService, runtimeServiceErr := inspectServiceUnit(ctx, exec, "agenthub.service")
	if runtimeServiceErr != nil {
		return result, fmt.Errorf("runtime service: %w", runtimeServiceErr)
	}
	if !runtimeService.Installed {
		return result, errors.New("runtime service: agenthub.service is not installed on the target host")
	}

	integrationService, integrationServiceErr := inspectServiceUnit(ctx, exec, slackServiceNameForAgent(agentName)+".service")
	if integrationServiceErr != nil {
		result.Warnings = append(result.Warnings, fmt.Sprintf("integration service probe failed: %v", integrationServiceErr))
	}
	if err := writeJSONFile(filepath.Join(tmpDir, "remote", "services.json"), diagnosticsServicesPayload{
		Runtime:     buildInspectJSONServiceState(runtimeService, runtimeServiceErr),
		Integration: buildInspectJSONServiceState(integrationService, integrationServiceErr),
	}); err != nil {
		return result, err
	}
	result.Collected = append(result.Collected, "services")

	port := inspectRuntimePort(*cfg, runtimeCfg)

	healthPayload, healthErr := queryRemoteJSON(ctx, exec, port, "/healthz")
	if healthErr != nil {
		result.Warnings = append(result.Warnings, fmt.Sprintf("runtime health unavailable: %v", healthErr))
	} else {
		if err := writeJSONFile(filepath.Join(tmpDir, "remote", "runtime-health.json"), healthPayload); err != nil {
			return result, err
		}
		result.Collected = append(result.Collected, "runtime-health")
	}

	statusPayload, statusErr := queryRemoteJSON(ctx, exec, port, "/status")
	if statusErr != nil {
		result.Warnings = append(result.Warnings, fmt.Sprintf("runtime status unavailable: %v", statusErr))
	} else {
		if err := writeJSONFile(filepath.Join(tmpDir, "remote", "runtime-status.json"), statusPayload); err != nil {
			return result, err
		}
		result.Collected = append(result.Collected, "runtime-status")
	}

	runtimeLogs, err := fetchServiceLogs(ctx, exec, "agenthub.service", opts.Lines)
	if err != nil {
		return result, fmt.Errorf("runtime logs: %w", err)
	}
	if err := writeTextFile(filepath.Join(tmpDir, "logs", "runtime.log"), runtimeLogs+"\n"); err != nil {
		return result, err
	}
	result.Collected = append(result.Collected, "runtime-logs")

	integrationLogs, err := fetchServiceLogs(ctx, exec, slackServiceNameForAgent(agentName)+".service", opts.Lines)
	if err != nil {
		if isMissingServiceUnitError(err) {
			result.Warnings = append(result.Warnings, fmt.Sprintf("integration logs unavailable: %v", err))
		} else {
			return result, fmt.Errorf("integration logs: %w", err)
		}
	} else {
		if err := writeTextFile(filepath.Join(tmpDir, "logs", "integration.log"), integrationLogs+"\n"); err != nil {
			return result, err
		}
		result.Collected = append(result.Collected, "integration-logs")
	}

	sort.Strings(result.Collected)

	manifest := diagnosticsManifest{
		Agent:       result.AgentName,
		Target:      result.Target,
		Version:     strings.TrimSpace(opts.Version),
		GeneratedAt: result.GeneratedAt.Format(time.RFC3339),
		Collected:   append([]string(nil), result.Collected...),
		Warnings:    append([]string(nil), result.Warnings...),
	}
	if err := writeJSONFile(filepath.Join(tmpDir, "manifest.json"), manifest); err != nil {
		return result, err
	}
	if err := writeDiagnosticsREADME(filepath.Join(tmpDir, "README.txt"), manifest); err != nil {
		return result, err
	}

	if err := os.MkdirAll(filepath.Dir(absOutputPath), 0o755); err != nil {
		return result, fmt.Errorf("create output directory: %w", err)
	}
	if err := createTarGzFromDir(absOutputPath, tmpDir); err != nil {
		return result, err
	}

	return result, nil
}

func printRuntimeDiagnosticsResult(out io.Writer, result runtimeDiagnosticsResult) {
	if strings.TrimSpace(result.AgentName) != "" {
		fmt.Fprintf(out, "agent: %s\n", result.AgentName)
	}
	if strings.TrimSpace(result.Target) != "" {
		fmt.Fprintf(out, "target: %s\n", result.Target)
	}
	if strings.TrimSpace(result.OutputPath) != "" {
		fmt.Fprintf(out, "bundle: %s\n", result.OutputPath)
	}
	if len(result.Collected) > 0 {
		fmt.Fprintf(out, "collected: %s\n", strings.Join(result.Collected, ", "))
	}
	for _, warning := range result.Warnings {
		fmt.Fprintf(out, "warning: %s\n", warning)
	}
}

func defaultDiagnosticsOutputPath(agentName string, now time.Time) string {
	name := strings.ToLower(strings.TrimSpace(agentName))
	if name == "" {
		name = "default"
	}
	return fmt.Sprintf("diagnostics-%s-%s.tar.gz", name, now.UTC().Format("20060102-150405"))
}

func writeDiagnosticsREADME(path string, manifest diagnosticsManifest) error {
	lines := []string{
		"AgentHub diagnostics bundle",
		"",
		fmt.Sprintf("Agent: %s", manifest.Agent),
		fmt.Sprintf("Generated at: %s", manifest.GeneratedAt),
		"",
		"Files in this archive capture local config, remote deployment state, runtime endpoints, and recent service logs.",
	}
	if len(manifest.Warnings) > 0 {
		lines = append(lines, "", "Warnings:")
		for _, warning := range manifest.Warnings {
			lines = append(lines, "- "+warning)
		}
	}
	return writeTextFile(path, strings.Join(lines, "\n")+"\n")
}

func writeTextFile(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create directory for %s: %w", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func writeJSONFile(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal %s: %w", path, err)
	}
	return writeTextFile(path, string(data)+"\n")
}

func writeYAMLFile(path string, value any) error {
	data, err := yaml.Marshal(value)
	if err != nil {
		return fmt.Errorf("marshal %s: %w", path, err)
	}
	return writeTextFile(path, string(data))
}

func createTarGzFromDir(outputPath, root string) error {
	file, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("create archive %s: %w", outputPath, err)
	}
	defer file.Close()

	gzw := gzip.NewWriter(file)
	defer gzw.Close()

	tw := tar.NewWriter(gzw)
	defer tw.Close()

	err = filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			return nil
		}

		relPath, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		relPath = filepath.ToSlash(relPath)

		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		header.Name = relPath
		header.ModTime = time.Unix(0, 0)
		header.AccessTime = time.Unix(0, 0)
		header.ChangeTime = time.Unix(0, 0)
		if err := tw.WriteHeader(header); err != nil {
			return err
		}

		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()

		if _, err := io.Copy(tw, f); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("write archive %s: %w", outputPath, err)
	}

	if err := tw.Close(); err != nil {
		return fmt.Errorf("close archive writer %s: %w", outputPath, err)
	}
	if err := gzw.Close(); err != nil {
		return fmt.Errorf("close gzip writer %s: %w", outputPath, err)
	}
	return nil
}
