package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"agenthub/internal/config"
	"agenthub/internal/host"
	"agenthub/internal/provider"
	"agenthub/internal/runtimeinstall"
)

type inspectOptions struct {
	AgentName         string
	AgentsDir         string
	Target            string
	SSHUser           string
	SSHKey            string
	SSHPort           int
	RuntimeConfigPath string
}

type inspectReport struct {
	AgentName         string
	Path              string
	Files             []string
	Config            config.Config
	RuntimeConfigPath string
	ResolvedTarget    string
	Cloud             *provider.Instance
	CloudErr          error
	RuntimeConfig     *runtimeinstall.RuntimeConfig
	RuntimeConfigErr  error
	RuntimeService    inspectServiceState
	RuntimeServiceErr error
	SlackService      inspectServiceState
	SlackServiceErr   error
	Health            map[string]any
	HealthErr         error
	StatusPayload     map[string]any
	Status            *runtimeStatusResponse
	StatusErr         error
	Warnings          []string
}

type inspectServiceState struct {
	Unit          string
	Installed     bool
	LoadState     string
	ActiveState   string
	SubState      string
	UnitFileState string
	FragmentPath  string
}

type inspectOutputFormat string

const (
	inspectOutputText inspectOutputFormat = "text"
	inspectOutputJSON inspectOutputFormat = "json"
)

type inspectJSONResponse struct {
	Agent            string                      `json:"agent"`
	Path             string                      `json:"path,omitempty"`
	Files            []string                    `json:"files,omitempty"`
	LocalConfig      *inspectJSONLocalConfig     `json:"local_config,omitempty"`
	Cloud            inspectJSONCloudState       `json:"cloud"`
	RemoteDeployment inspectJSONRemoteDeployment `json:"remote_deployment"`
	RuntimeState     inspectJSONRuntimeState     `json:"runtime_state"`
	Warnings         []string                    `json:"warnings,omitempty"`
}

type inspectJSONLocalConfig struct {
	Config          *statusJSONConfig `json:"config,omitempty"`
	InfraInstanceID string            `json:"infra_instance_id,omitempty"`
	SlackRuntimeURL string            `json:"slack_runtime_url,omitempty"`
}

type inspectJSONCloudState struct {
	State    string                  `json:"state"`
	Error    string                  `json:"error,omitempty"`
	Instance *statusJSONLiveInstance `json:"instance,omitempty"`
}

type inspectJSONRemoteDeployment struct {
	SSHTarget          string                    `json:"ssh_target,omitempty"`
	RuntimeConfigPath  string                    `json:"runtime_config_path,omitempty"`
	RuntimeConfig      *inspectJSONRuntimeConfig `json:"runtime_config,omitempty"`
	RuntimeService     inspectJSONServiceState   `json:"runtime_service"`
	IntegrationService inspectJSONServiceState   `json:"integration_service"`
}

type inspectJSONRuntimeConfig struct {
	Provider    string              `json:"provider,omitempty"`
	NIMEndpoint string              `json:"nim_endpoint,omitempty"`
	Model       string              `json:"model,omitempty"`
	Port        int                 `json:"port,omitempty"`
	Region      string              `json:"region,omitempty"`
	UseNemoClaw bool                `json:"use_nemoclaw"`
	GitHub      *statusJSONGitHub   `json:"github,omitempty"`
	Sandbox     *inspectJSONSandbox `json:"sandbox,omitempty"`
}

type inspectJSONSandbox struct {
	Enabled         bool     `json:"enabled"`
	NetworkMode     string   `json:"network_mode,omitempty"`
	FilesystemAllow []string `json:"filesystem_allow,omitempty"`
}

type inspectJSONServiceState struct {
	Unit          string `json:"unit,omitempty"`
	Installed     bool   `json:"installed"`
	LoadState     string `json:"load_state,omitempty"`
	ActiveState   string `json:"active_state,omitempty"`
	SubState      string `json:"sub_state,omitempty"`
	UnitFileState string `json:"unit_file_state,omitempty"`
	FragmentPath  string `json:"fragment_path,omitempty"`
	Error         string `json:"error,omitempty"`
}

type inspectJSONRuntimeState struct {
	Health inspectJSONEndpointState `json:"health"`
	Status inspectJSONEndpointState `json:"status"`
}

type inspectJSONEndpointState struct {
	Available bool                   `json:"available"`
	Error     string                 `json:"error,omitempty"`
	Payload   map[string]any         `json:"payload,omitempty"`
	Summary   *runtimeStatusResponse `json:"summary,omitempty"`
}

func newInspectCommand(app *App) *cobra.Command {
	var agentsDir string
	var target string
	var sshUser string
	var sshKey string
	var sshPort int
	var runtimeConfigPath string
	var output string

	cmd := &cobra.Command{
		Use:           "inspect <agent>",
		Short:         "Inspect one agent in detail",
		GroupID:       "inspect",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			outputFormat, parseErr := parseInspectOutputFormat(output)
			if parseErr != nil {
				return parseErr
			}
			report, err := runInspectWorkflow(cmd.Context(), app.opts.Profile, inspectOptions{
				AgentName:         strings.TrimSpace(args[0]),
				AgentsDir:         agentsDir,
				Target:            target,
				SSHUser:           sshUser,
				SSHKey:            sshKey,
				SSHPort:           sshPort,
				RuntimeConfigPath: runtimeConfigPath,
			})
			switch outputFormat {
			case inspectOutputJSON:
				if encodeErr := json.NewEncoder(cmd.OutOrStdout()).Encode(buildInspectJSONResponse(report)); encodeErr != nil {
					return encodeErr
				}
			default:
				printInspectReport(cmd.OutOrStdout(), report)
			}
			if err != nil {
				return wrapUserFacingError(
					"inspect failed",
					err,
					"one or more remote inspection stages could not be completed",
					"confirm the target host is reachable and rerun "+commandRef(cmd.OutOrStdout(), "agenthub", "inspect", report.AgentName),
				)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&agentsDir, "agents-dir", "agents", "path to the agents directory")
	cmd.Flags().StringVar(&target, "target", "", "SSH target host or EC2 instance id; defaults to infra.instance_id")
	cmd.Flags().StringVar(&sshUser, "ssh-user", "", "SSH username for the target host")
	cmd.Flags().StringVar(&sshKey, "ssh-key", "", "path to the SSH private key")
	cmd.Flags().IntVar(&sshPort, "ssh-port", 22, "SSH port")
	cmd.Flags().StringVar(&runtimeConfigPath, "runtime-config", "/opt/agenthub/runtime.yaml", "path to the runtime config on the target host")
	cmd.Flags().StringVar(&output, "output", string(inspectOutputText), "output format: text or json")
	return cmd
}

func parseInspectOutputFormat(value string) (inspectOutputFormat, error) {
	switch inspectOutputFormat(strings.ToLower(strings.TrimSpace(value))) {
	case "", inspectOutputText:
		return inspectOutputText, nil
	case inspectOutputJSON:
		return inspectOutputJSON, nil
	default:
		return "", fmt.Errorf("unsupported output format %q: expected text or json", value)
	}
}

func runInspectWorkflow(ctx context.Context, profile string, opts inspectOptions) (inspectReport, error) {
	report, err := loadInspectReport(opts.AgentsDir, opts.AgentName)
	if err != nil {
		return report, err
	}
	if report.RuntimeConfigPath == "" {
		report.RuntimeConfigPath = strings.TrimSpace(opts.RuntimeConfigPath)
	}
	if report.RuntimeConfigPath == "" {
		report.RuntimeConfigPath = "/opt/agenthub/runtime.yaml"
	}

	report.Cloud, report.CloudErr = inspectCloudState(ctx, profile, report.Config)

	targetValue := strings.TrimSpace(opts.Target)
	if targetValue == "" {
		targetValue = strings.TrimSpace(report.Config.Infra.InstanceID)
	}
	if targetValue == "" {
		return report, errors.New("deployment target is required: pass --target or run agenthub create first so infra.instance_id is recorded")
	}

	resolvedTarget, targetErr := resolveHostTarget(ctx, profile, &report.Config, targetValue)
	report.ResolvedTarget = strings.TrimSpace(resolvedTarget)
	if targetErr != nil {
		return report, fmt.Errorf("resolve target: %w", targetErr)
	}

	user, keyPath, sshErr := resolveInstallSSH(&report.Config, opts.SSHUser, opts.SSHKey)
	if sshErr != nil {
		return report, sshErr
	}
	exec := newSSHExecutor(host.SSHConfig{
		Host:           report.ResolvedTarget,
		Port:           opts.SSHPort,
		User:           user,
		IdentityFile:   keyPath,
		ConnectTimeout: 15 * time.Second,
	})
	if err := waitForSSHReady(ctx, exec, report.ResolvedTarget); err != nil {
		return report, err
	}

	var errs []error

	report.RuntimeConfig, report.RuntimeConfigErr = readRemoteRuntimeConfig(ctx, exec, report.RuntimeConfigPath)
	if report.RuntimeConfigErr != nil {
		errs = append(errs, fmt.Errorf("remote runtime config: %w", report.RuntimeConfigErr))
	}

	report.RuntimeService, report.RuntimeServiceErr = inspectServiceUnit(ctx, exec, "agenthub.service")
	if report.RuntimeServiceErr != nil {
		errs = append(errs, fmt.Errorf("runtime service: %w", report.RuntimeServiceErr))
	} else if !report.RuntimeService.Installed {
		errs = append(errs, errors.New("runtime service: agenthub.service is not installed on the target host"))
	}

	report.SlackService, err = inspectServiceUnit(ctx, exec, slackServiceNameForAgent(report.AgentName)+".service")
	if err != nil {
		report.SlackServiceErr = err
		report.Warnings = append(report.Warnings, fmt.Sprintf("integration service probe failed: %v", err))
	}

	port := inspectRuntimePort(report.Config, report.RuntimeConfig)

	report.Health, report.HealthErr = queryRemoteJSON(ctx, exec, port, "/healthz")
	if report.HealthErr != nil {
		errs = append(errs, fmt.Errorf("runtime health: %w", report.HealthErr))
	}

	statusPayload, statusErr := queryRemoteJSON(ctx, exec, port, "/status")
	if statusErr != nil {
		report.StatusErr = statusErr
		errs = append(errs, fmt.Errorf("runtime status: %w", statusErr))
	} else {
		report.StatusPayload = cloneJSONMap(statusPayload)
		var decoded runtimeStatusResponse
		data, marshalErr := json.Marshal(statusPayload)
		if marshalErr != nil {
			report.StatusErr = marshalErr
			errs = append(errs, fmt.Errorf("runtime status: marshal payload: %w", marshalErr))
		} else if unmarshalErr := json.Unmarshal(data, &decoded); unmarshalErr != nil {
			report.StatusErr = unmarshalErr
			errs = append(errs, fmt.Errorf("runtime status: parse payload: %w", unmarshalErr))
		} else {
			report.Status = &decoded
		}
	}

	return report, errors.Join(errs...)
}

func loadInspectReport(root, agentName string) (inspectReport, error) {
	agentPath, err := localAgentPath(root, agentName)
	if err != nil {
		return inspectReport{AgentName: strings.TrimSpace(agentName)}, err
	}
	entry, err := loadAgentStatusEntry(agentPath)
	report := inspectReport{
		AgentName: strings.TrimSpace(entry.Name),
		Path:      entry.Path,
		Files:     append([]string(nil), entry.Files...),
		Config:    entry.Config,
	}
	if report.AgentName == "" {
		report.AgentName = strings.TrimSpace(agentName)
	}
	return report, err
}

func inspectCloudState(ctx context.Context, profile string, cfg config.Config) (*provider.Instance, error) {
	instanceID := strings.TrimSpace(cfg.Infra.InstanceID)
	if instanceID == "" {
		return nil, nil
	}
	region := strings.TrimSpace(cfg.Region.Name)
	if region == "" {
		return nil, errors.New("region is required")
	}
	prov := newAWSProvider(firstNonEmpty(profile, cfg.Infra.AWSProfile), "")
	return prov.GetInstance(ctx, region, instanceID)
}

func readRemoteRuntimeConfig(ctx context.Context, exec host.Executor, path string) (*runtimeinstall.RuntimeConfig, error) {
	result, err := exec.Run(ctx, "cat", path)
	if err != nil {
		msg := strings.TrimSpace(firstNonEmpty(result.Stderr, result.Stdout))
		if msg == "" {
			msg = err.Error()
		}
		return nil, errors.New(msg)
	}

	var cfg runtimeinstall.RuntimeConfig
	if err := yaml.Unmarshal([]byte(result.Stdout), &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &cfg, nil
}

func inspectServiceUnit(ctx context.Context, exec host.Executor, unit string) (inspectServiceState, error) {
	unit = strings.TrimSpace(unit)
	result, err := exec.Run(ctx, "sh", "-lc", strings.Join([]string{
		"set -eu",
		"unit=" + strconv.Quote(unit),
		"if ! systemctl list-unit-files --full --no-legend \"$unit\" 2>/dev/null | awk '{print $1}' | grep -Fxq \"$unit\"; then",
		"  echo installed=false",
		"  exit 0",
		"fi",
		"echo installed=true",
		"systemctl show \"$unit\" --no-pager --property=LoadState --property=ActiveState --property=SubState --property=UnitFileState --property=FragmentPath",
	}, "\n"))
	if err != nil {
		msg := strings.TrimSpace(firstNonEmpty(result.Stderr, result.Stdout))
		if msg == "" {
			msg = err.Error()
		}
		return inspectServiceState{Unit: unit}, errors.New(msg)
	}

	state := inspectServiceState{Unit: unit}
	for _, line := range strings.Split(result.Stdout, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if line == "installed=false" {
			state.Installed = false
			return state, nil
		}
		if line == "installed=true" {
			state.Installed = true
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch strings.TrimSpace(key) {
		case "LoadState":
			state.LoadState = strings.TrimSpace(value)
		case "ActiveState":
			state.ActiveState = strings.TrimSpace(value)
		case "SubState":
			state.SubState = strings.TrimSpace(value)
		case "UnitFileState":
			state.UnitFileState = strings.TrimSpace(value)
		case "FragmentPath":
			state.FragmentPath = strings.TrimSpace(value)
		}
	}
	return state, nil
}

func queryRemoteJSON(ctx context.Context, exec host.Executor, port int, path string) (map[string]any, error) {
	endpoint := fmt.Sprintf("http://127.0.0.1:%d%s", port, path)
	script := fmt.Sprintf(`
endpoint=%s
if command -v curl >/dev/null 2>&1; then
  curl --max-time 5 -fsS "$endpoint"
  exit $?
fi
if command -v python3 >/dev/null 2>&1; then
  ENDPOINT="$endpoint" python3 - <<'PY'
import os
import urllib.request
print(urllib.request.urlopen(os.environ["ENDPOINT"], timeout=5).read().decode("utf-8"))
PY
  exit $?
fi
exit 127
`, strconv.Quote(endpoint))
	result, err := exec.Run(ctx, "sh", "-lc", script)
	if err != nil {
		msg := strings.TrimSpace(firstNonEmpty(result.Stderr, result.Stdout))
		if msg == "" {
			msg = err.Error()
		}
		return nil, errors.New(msg)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(result.Stdout)), &payload); err != nil {
		return nil, fmt.Errorf("parse %s payload: %w", path, err)
	}
	return payload, nil
}

func inspectRuntimePort(local config.Config, remote *runtimeinstall.RuntimeConfig) int {
	if remote != nil && remote.Port > 0 {
		return remote.Port
	}
	if local.Runtime.Port > 0 {
		return local.Runtime.Port
	}
	return 8080
}

func printInspectReport(out io.Writer, report inspectReport) {
	fmt.Fprintln(out, "agent inspect")
	fmt.Fprintf(out, "agent: %s\n", report.AgentName)
	if strings.TrimSpace(report.Path) != "" {
		fmt.Fprintf(out, "path: %s\n", report.Path)
	}
	if len(report.Files) > 0 {
		fmt.Fprintf(out, "files: %s\n", strings.Join(report.Files, ", "))
	}

	fmt.Fprintln(out, "local config")
	for _, line := range formatAgentConfigSummary(report.Config) {
		fmt.Fprintf(out, "- %s\n", line)
	}
	if value := strings.TrimSpace(report.Config.Infra.InstanceID); value != "" {
		fmt.Fprintf(out, "- infra.instance_id: %s\n", value)
	}
	if value := strings.TrimSpace(report.Config.Slack.RuntimeURL); value != "" {
		fmt.Fprintf(out, "- slack.runtime_url: %s\n", value)
	}

	fmt.Fprintln(out, "cloud state")
	switch {
	case report.CloudErr != nil:
		fmt.Fprintf(out, "- ec2: unavailable: %v\n", report.CloudErr)
	case report.Cloud == nil:
		fmt.Fprintln(out, "- ec2: not provisioned")
	default:
		for _, line := range formatAgentLiveSummary(agentStatusEntry{
			Config: report.Config,
			Live: agentLiveStatus{
				Instance: report.Cloud,
			},
		}) {
			fmt.Fprintf(out, "- %s\n", line)
		}
	}

	fmt.Fprintln(out, "remote deployment")
	if strings.TrimSpace(report.ResolvedTarget) != "" {
		fmt.Fprintf(out, "- ssh target: %s\n", report.ResolvedTarget)
	}
	if strings.TrimSpace(report.RuntimeConfigPath) != "" {
		fmt.Fprintf(out, "- runtime config path: %s\n", report.RuntimeConfigPath)
	}
	if report.RuntimeConfigErr != nil {
		fmt.Fprintf(out, "- remote runtime config: unavailable: %v\n", report.RuntimeConfigErr)
	} else if report.RuntimeConfig != nil {
		fmt.Fprintf(out, "- remote runtime config: %s\n", formatRemoteRuntimeConfigSummary(report.RuntimeConfig))
	}
	printInspectService(out, "runtime service", report.RuntimeService, report.RuntimeServiceErr)
	printInspectService(out, "integration service", report.SlackService, report.SlackServiceErr)

	fmt.Fprintln(out, "runtime state")
	if report.HealthErr != nil {
		fmt.Fprintf(out, "- health: unavailable: %v\n", report.HealthErr)
	} else if len(report.Health) > 0 {
		fmt.Fprintf(out, "- health: %s\n", formatHealthSummary(report.Health))
	}
	if report.StatusErr != nil {
		fmt.Fprintf(out, "- status: unavailable: %v\n", report.StatusErr)
	} else if report.Status != nil {
		fmt.Fprintf(out, "- status: active=%t active_count=%d\n", report.Status.Active, report.Status.ActiveCount)
		for _, active := range report.Status.ActiveAgents {
			fmt.Fprintf(out, "  active-agent: id=%s task=%s\n", active.ID, active.Task)
		}
	}
	for _, warning := range report.Warnings {
		fmt.Fprintf(out, "warning: %s\n", warning)
	}
}

func buildInspectJSONResponse(report inspectReport) inspectJSONResponse {
	return inspectJSONResponse{
		Agent: report.AgentName,
		Path:  report.Path,
		Files: append([]string(nil), report.Files...),
		LocalConfig: &inspectJSONLocalConfig{
			Config:          buildStatusJSONConfig(report.Config),
			InfraInstanceID: strings.TrimSpace(report.Config.Infra.InstanceID),
			SlackRuntimeURL: strings.TrimSpace(report.Config.Slack.RuntimeURL),
		},
		Cloud: inspectJSONCloudState{
			State:    buildStatusJSONLiveStatus(agentStatusEntry{Config: report.Config, Live: agentLiveStatus{Instance: report.Cloud, Err: report.CloudErr}}).State,
			Error:    buildStatusJSONLiveStatus(agentStatusEntry{Config: report.Config, Live: agentLiveStatus{Instance: report.Cloud, Err: report.CloudErr}}).Error,
			Instance: buildStatusJSONLiveStatus(agentStatusEntry{Config: report.Config, Live: agentLiveStatus{Instance: report.Cloud, Err: report.CloudErr}}).Instance,
		},
		RemoteDeployment: inspectJSONRemoteDeployment{
			SSHTarget:          strings.TrimSpace(report.ResolvedTarget),
			RuntimeConfigPath:  strings.TrimSpace(report.RuntimeConfigPath),
			RuntimeConfig:      buildInspectJSONRuntimeConfig(report.RuntimeConfig),
			RuntimeService:     buildInspectJSONServiceState(report.RuntimeService, report.RuntimeServiceErr),
			IntegrationService: buildInspectJSONServiceState(report.SlackService, report.SlackServiceErr),
		},
		RuntimeState: inspectJSONRuntimeState{
			Health: buildInspectJSONHealthState(report),
			Status: buildInspectJSONStatusState(report),
		},
		Warnings: append([]string(nil), report.Warnings...),
	}
}

func buildInspectJSONRuntimeConfig(cfg *runtimeinstall.RuntimeConfig) *inspectJSONRuntimeConfig {
	if cfg == nil {
		return nil
	}
	out := &inspectJSONRuntimeConfig{
		Provider:    strings.TrimSpace(cfg.Provider),
		NIMEndpoint: strings.TrimSpace(cfg.NIMEndpoint),
		Model:       strings.TrimSpace(cfg.Model),
		Port:        cfg.Port,
		Region:      strings.TrimSpace(cfg.Region),
		UseNemoClaw: cfg.UseNemoClaw,
		Sandbox: &inspectJSONSandbox{
			Enabled:         cfg.Sandbox.Enabled,
			NetworkMode:     strings.TrimSpace(cfg.Sandbox.NetworkMode),
			FilesystemAllow: append([]string(nil), cfg.Sandbox.FilesystemAllow...),
		},
	}
	if github := buildStatusJSONGitHub(cfg.GitHub); github != nil {
		out.GitHub = github
	}
	return out
}

func buildInspectJSONServiceState(state inspectServiceState, err error) inspectJSONServiceState {
	out := inspectJSONServiceState{
		Unit:          strings.TrimSpace(state.Unit),
		Installed:     state.Installed,
		LoadState:     strings.TrimSpace(state.LoadState),
		ActiveState:   strings.TrimSpace(state.ActiveState),
		SubState:      strings.TrimSpace(state.SubState),
		UnitFileState: strings.TrimSpace(state.UnitFileState),
		FragmentPath:  strings.TrimSpace(state.FragmentPath),
	}
	if err != nil {
		out.Error = err.Error()
	}
	return out
}

func buildInspectJSONHealthState(report inspectReport) inspectJSONEndpointState {
	out := inspectJSONEndpointState{
		Available: report.HealthErr == nil && len(report.Health) > 0,
		Payload:   cloneJSONMap(report.Health),
	}
	if report.HealthErr != nil {
		out.Error = report.HealthErr.Error()
	}
	return out
}

func buildInspectJSONStatusState(report inspectReport) inspectJSONEndpointState {
	out := inspectJSONEndpointState{
		Available: report.StatusErr == nil && len(report.StatusPayload) > 0,
		Payload:   cloneJSONMap(report.StatusPayload),
	}
	if report.StatusErr != nil {
		out.Error = report.StatusErr.Error()
	}
	if report.Status != nil {
		summary := *report.Status
		out.Summary = &summary
	}
	return out
}

func cloneJSONMap(src map[string]any) map[string]any {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]any, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

func printInspectService(out io.Writer, label string, state inspectServiceState, err error) {
	if err != nil {
		fmt.Fprintf(out, "- %s: unavailable: %v\n", label, err)
		return
	}
	summary := formatInspectServiceSummary(state)
	if summary == "" {
		return
	}
	fmt.Fprintf(out, "- %s: %s\n", label, summary)
}

func formatInspectServiceSummary(state inspectServiceState) string {
	if strings.TrimSpace(state.Unit) == "" {
		return ""
	}
	if !state.Installed {
		return fmt.Sprintf("not installed (%s)", state.Unit)
	}
	parts := []string{state.Unit}
	if value := strings.TrimSpace(state.ActiveState); value != "" {
		parts = append(parts, "active="+value)
	}
	if value := strings.TrimSpace(state.SubState); value != "" {
		parts = append(parts, "sub="+value)
	}
	if value := strings.TrimSpace(state.UnitFileState); value != "" {
		parts = append(parts, "enabled="+value)
	}
	if value := strings.TrimSpace(state.FragmentPath); value != "" {
		parts = append(parts, "path="+value)
	}
	return strings.Join(parts, " ")
}

func formatRemoteRuntimeConfigSummary(cfg *runtimeinstall.RuntimeConfig) string {
	if cfg == nil {
		return ""
	}
	parts := make([]string, 0, 8)
	if value := strings.TrimSpace(cfg.Provider); value != "" {
		parts = append(parts, "provider="+value)
	}
	if value := strings.TrimSpace(cfg.NIMEndpoint); value != "" {
		parts = append(parts, "endpoint="+value)
	}
	if value := strings.TrimSpace(cfg.Model); value != "" {
		parts = append(parts, "model="+value)
	}
	if cfg.Port > 0 {
		parts = append(parts, fmt.Sprintf("port=%d", cfg.Port))
	}
	if value := strings.TrimSpace(cfg.Region); value != "" {
		parts = append(parts, "region="+value)
	}
	parts = append(parts, fmt.Sprintf("sandbox.enabled=%t", cfg.Sandbox.Enabled))
	if value := strings.TrimSpace(cfg.Sandbox.NetworkMode); value != "" {
		parts = append(parts, "sandbox.network="+value)
	}
	if mode := strings.TrimSpace(config.GitHubAuthModeFor(cfg.GitHub)); mode != "" {
		parts = append(parts, "github.auth_mode="+mode)
	}
	return strings.Join(parts, " ")
}

func formatHealthSummary(payload map[string]any) string {
	keys := []string{"status", "provider", "model", "configured_port", "workspace_root", "runtime_config"}
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		value, ok := payload[key]
		if !ok {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s=%v", key, value))
	}
	return strings.Join(parts, " ")
}
