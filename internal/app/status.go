package app

import (
	"bytes"
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
	"agenthub/internal/provider"
)

type agentStatusReport struct {
	Root   string
	Agents []agentStatusEntry
}

type agentStatusEntry struct {
	Name   string
	Path   string
	Files  []string
	Config config.Config
	Err    error
	Live   agentLiveStatus
}

type agentLiveStatus struct {
	Instance *provider.Instance
	Err      error
}

type statusOutputFormat string

const (
	statusOutputText statusOutputFormat = "text"
	statusOutputJSON statusOutputFormat = "json"
)

type statusJSONResponse struct {
	Root   string                 `json:"root"`
	Agents []statusJSONAgentEntry `json:"agents"`
}

type statusJSONAgentEntry struct {
	Name   string               `json:"name"`
	Path   string               `json:"path"`
	Files  []string             `json:"files,omitempty"`
	Status string               `json:"status"`
	Error  string               `json:"error,omitempty"`
	Config *statusJSONConfig    `json:"config,omitempty"`
	Live   statusJSONLiveStatus `json:"live"`
}

type statusJSONConfig struct {
	Platform *statusJSONNameField  `json:"platform,omitempty"`
	Compute  *statusJSONClassField `json:"compute,omitempty"`
	Region   *statusJSONNameField  `json:"region,omitempty"`
	Instance *statusJSONInstance   `json:"instance,omitempty"`
	Image    *statusJSONImage      `json:"image,omitempty"`
	Runtime  *statusJSONRuntime    `json:"runtime,omitempty"`
	Sandbox  *statusJSONSandbox    `json:"sandbox,omitempty"`
	SSH      *statusJSONSSH        `json:"ssh,omitempty"`
	GitHub   *statusJSONGitHub     `json:"github,omitempty"`
	Infra    *statusJSONInfra      `json:"infra,omitempty"`
}

type statusJSONNameField struct {
	Name string `json:"name"`
}

type statusJSONClassField struct {
	Class string `json:"class"`
}

type statusJSONInstance struct {
	Type        string `json:"type,omitempty"`
	DiskSizeGB  int    `json:"disk_size_gb,omitempty"`
	NetworkMode string `json:"network_mode,omitempty"`
}

type statusJSONImage struct {
	Name string `json:"name,omitempty"`
	ID   string `json:"id,omitempty"`
}

type statusJSONRuntime struct {
	Endpoint   string                  `json:"endpoint,omitempty"`
	Model      string                  `json:"model,omitempty"`
	Port       int                     `json:"port,omitempty"`
	Provider   string                  `json:"provider,omitempty"`
	PublicCIDR string                  `json:"public_cidr,omitempty"`
	Codex      *statusJSONRuntimeCodex `json:"codex,omitempty"`
}

type statusJSONRuntimeCodex struct {
	SecretID string `json:"secret_id,omitempty"`
}

type statusJSONSandbox struct {
	Enabled         bool     `json:"enabled"`
	NetworkMode     string   `json:"network_mode,omitempty"`
	UseNemoClaw     bool     `json:"use_nemoclaw"`
	FilesystemAllow []string `json:"filesystem_allow,omitempty"`
}

type statusJSONSSH struct {
	KeyName              string `json:"key_name,omitempty"`
	PrivateKeyPath       string `json:"private_key_path,omitempty"`
	GitHubPrivateKeyPath string `json:"github_private_key_path,omitempty"`
	CIDR                 string `json:"cidr,omitempty"`
	User                 string `json:"user,omitempty"`
}

type statusJSONGitHub struct {
	AuthMode            string `json:"auth_mode,omitempty"`
	AppID               string `json:"app_id,omitempty"`
	InstallationID      string `json:"installation_id,omitempty"`
	PrivateKeySecretARN string `json:"private_key_secret_arn,omitempty"`
	SSHKeySecretARN     string `json:"ssh_key_secret_arn,omitempty"`
	TokenSecretARN      string `json:"token_secret_arn,omitempty"`
}

type statusJSONInfra struct {
	Backend     string `json:"backend,omitempty"`
	ModuleDir   string `json:"module_dir,omitempty"`
	AWSProfile  string `json:"aws_profile,omitempty"`
	Environment string `json:"environment,omitempty"`
	InstanceID  string `json:"instance_id,omitempty"`
}

type statusJSONLiveStatus struct {
	State    string                  `json:"state"`
	Error    string                  `json:"error,omitempty"`
	Instance *statusJSONLiveInstance `json:"instance,omitempty"`
}

type statusJSONLiveInstance struct {
	ID               string `json:"id,omitempty"`
	Name             string `json:"name,omitempty"`
	State            string `json:"state,omitempty"`
	InstanceType     string `json:"instance_type,omitempty"`
	AvailabilityZone string `json:"availability_zone,omitempty"`
	PublicIP         string `json:"public_ip,omitempty"`
	PrivateIP        string `json:"private_ip,omitempty"`
	LaunchTime       string `json:"launch_time,omitempty"`
	KeyName          string `json:"key_name,omitempty"`
}

func newStatusCommand(app *App) *cobra.Command {
	var agentsDir string
	var output string

	cmd := &cobra.Command{
		Use:           "status",
		Short:         "Show formatted agent configuration status",
		GroupID:       "runtime",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			outputFormat, err := parseStatusOutputFormat(output)
			if err != nil {
				return err
			}
			report, err := loadAgentStatusReport(agentsDir)
			attachAgentLiveStatus(cmd.Context(), app.opts.Profile, &report)
			switch outputFormat {
			case statusOutputJSON:
				if encodeErr := json.NewEncoder(cmd.OutOrStdout()).Encode(buildStatusJSONResponse(report)); encodeErr != nil {
					return encodeErr
				}
			default:
				printAgentStatusReport(cmd.OutOrStdout(), report)
			}
			return err
		},
	}

	cmd.Flags().StringVar(&agentsDir, "agents-dir", "agents", "path to the agents directory")
	cmd.Flags().StringVar(&output, "output", string(statusOutputText), "output format: text or json")
	return cmd
}

func parseStatusOutputFormat(value string) (statusOutputFormat, error) {
	switch statusOutputFormat(strings.ToLower(strings.TrimSpace(value))) {
	case "", statusOutputText:
		return statusOutputText, nil
	case statusOutputJSON:
		return statusOutputJSON, nil
	default:
		return "", fmt.Errorf("unsupported output format %q: expected text or json", value)
	}
}

func loadAgentStatusReport(root string) (agentStatusReport, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		root = "agents"
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return agentStatusReport{Root: root}, nil
		}
		return agentStatusReport{}, fmt.Errorf("read agents directory %q: %w", root, err)
	}

	var report agentStatusReport
	report.Root = root

	var errs []error
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		agent, agentErr := loadAgentStatusEntry(filepath.Join(root, entry.Name()))
		report.Agents = append(report.Agents, agent)
		if agentErr != nil {
			errs = append(errs, agentErr)
		}
	}

	sort.Slice(report.Agents, func(i, j int) bool {
		return report.Agents[i].Name < report.Agents[j].Name
	})

	return report, errors.Join(errs...)
}

func attachAgentLiveStatus(ctx context.Context, profile string, report *agentStatusReport) {
	if report == nil {
		return
	}
	for i := range report.Agents {
		agent := &report.Agents[i]
		if agent.Err != nil {
			continue
		}
		instanceID := strings.TrimSpace(agent.Config.Infra.InstanceID)
		if instanceID == "" {
			continue
		}
		region := strings.TrimSpace(agent.Config.Region.Name)
		if region == "" {
			agent.Live.Err = errors.New("region is required")
			continue
		}
		prov := newAWSProvider(firstNonEmpty(profile, agent.Config.Infra.AWSProfile), "")
		instance, err := prov.GetInstance(ctx, region, instanceID)
		if err != nil {
			agent.Live.Err = err
			continue
		}
		agent.Live.Instance = instance
	}
}

func loadAgentStatusEntry(path string) (agentStatusEntry, error) {
	name := filepath.Base(path)
	entries, err := os.ReadDir(path)
	if err != nil {
		return agentStatusEntry{Name: name, Path: path, Err: fmt.Errorf("read agent directory %q: %w", path, err)}, fmt.Errorf("%s: %w", name, err)
	}

	files := make([]string, 0, len(entries))
	merged := map[string]any{}
	var errs []error

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if !isYAMLConfigFile(entry.Name()) {
			continue
		}
		files = append(files, entry.Name())
		fragment, fragmentErr := loadYAMLDocument(filepath.Join(path, entry.Name()))
		if fragmentErr != nil {
			errs = append(errs, fmt.Errorf("%s/%s: %w", name, entry.Name(), fragmentErr))
			continue
		}
		mergeYAMLDocuments(merged, fragment)
	}

	if len(files) == 0 {
		err := fmt.Errorf("no YAML config files found in %q", path)
		return agentStatusEntry{Name: name, Path: path, Err: err}, err
	}
	if len(errs) > 0 {
		err := errors.Join(errs...)
		return agentStatusEntry{Name: name, Path: path, Files: append([]string(nil), files...), Err: err}, err
	}

	cfg, decodeErr := decodeMergedAgentConfig(merged)
	if decodeErr != nil {
		err := fmt.Errorf("decode merged config: %w", decodeErr)
		return agentStatusEntry{Name: name, Path: path, Files: append([]string(nil), files...), Err: err}, err
	}
	if validateErr := config.Validate(&cfg); validateErr != nil {
		err := fmt.Errorf("validate merged config: %w", validateErr)
		return agentStatusEntry{Name: name, Path: path, Files: append([]string(nil), files...), Config: cfg, Err: err}, err
	}

	return agentStatusEntry{Name: name, Path: path, Files: append([]string(nil), files...), Config: cfg}, nil
}

func loadYAMLDocument(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %q: %w", path, err)
	}
	if strings.TrimSpace(string(data)) == "" {
		return map[string]any{}, nil
	}

	var raw any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse config %q: %w", path, err)
	}

	doc, ok := normalizeYAMLMap(raw).(map[string]any)
	if !ok {
		return nil, fmt.Errorf("config %q must contain a YAML mapping at the top level", path)
	}
	return doc, nil
}

func decodeMergedAgentConfig(doc map[string]any) (config.Config, error) {
	data, err := yaml.Marshal(doc)
	if err != nil {
		return config.Config{}, fmt.Errorf("marshal merged config: %w", err)
	}

	var cfg config.Config
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		return config.Config{}, fmt.Errorf("decode merged config: %w", err)
	}
	return cfg, nil
}

func mergeYAMLDocuments(dst, src map[string]any) {
	for key, srcValue := range src {
		if dstValue, ok := dst[key]; ok {
			dstMap, dstOK := dstValue.(map[string]any)
			srcMap, srcOK := srcValue.(map[string]any)
			if dstOK && srcOK {
				mergeYAMLDocuments(dstMap, srcMap)
				continue
			}
		}
		dst[key] = srcValue
	}
}

func normalizeYAMLMap(value any) any {
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, item := range v {
			out[key] = normalizeYAMLMap(item)
		}
		return out
	case map[interface{}]interface{}:
		out := make(map[string]any, len(v))
		for key, item := range v {
			out[fmt.Sprint(key)] = normalizeYAMLMap(item)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = normalizeYAMLMap(item)
		}
		return out
	default:
		return value
	}
}

func isYAMLConfigFile(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".yaml", ".yml":
		return true
	default:
		return false
	}
}

func printAgentStatusReport(out io.Writer, report agentStatusReport) {
	fmt.Fprintln(out, "agent status")
	if len(report.Agents) == 0 {
		fmt.Fprintf(out, "no agents found under %s\n", report.Root)
		return
	}

	for _, agent := range report.Agents {
		fmt.Fprintf(out, "agent: %s\n", agent.Name)
		fmt.Fprintf(out, "  path: %s\n", agent.Path)
		if len(agent.Files) > 0 {
			fmt.Fprintf(out, "  files: %s\n", strings.Join(agent.Files, ", "))
		}
		if agent.Err != nil {
			fmt.Fprintln(out, "  status: invalid")
			fmt.Fprintf(out, "  error: %v\n", agent.Err)
			continue
		}
		fmt.Fprintln(out, "  status: valid")
		for _, line := range formatAgentConfigSummary(agent.Config) {
			fmt.Fprintf(out, "  %s\n", line)
		}
		for _, line := range formatAgentLiveSummary(agent) {
			fmt.Fprintf(out, "  %s\n", line)
		}
	}
}

func buildStatusJSONResponse(report agentStatusReport) statusJSONResponse {
	response := statusJSONResponse{
		Root:   report.Root,
		Agents: make([]statusJSONAgentEntry, 0, len(report.Agents)),
	}
	for _, agent := range report.Agents {
		entry := statusJSONAgentEntry{
			Name:   agent.Name,
			Path:   agent.Path,
			Files:  append([]string(nil), agent.Files...),
			Status: "valid",
			Live:   buildStatusJSONLiveStatus(agent),
		}
		if agent.Err != nil {
			entry.Status = "invalid"
			entry.Error = agent.Err.Error()
		} else {
			entry.Config = buildStatusJSONConfig(agent.Config)
		}
		response.Agents = append(response.Agents, entry)
	}
	return response
}

func buildStatusJSONConfig(cfg config.Config) *statusJSONConfig {
	out := &statusJSONConfig{}
	if value := strings.TrimSpace(cfg.Platform.Name); value != "" {
		out.Platform = &statusJSONNameField{Name: value}
	}
	if value := strings.TrimSpace(cfg.Compute.Class); value != "" {
		out.Compute = &statusJSONClassField{Class: value}
	}
	if value := strings.TrimSpace(cfg.Region.Name); value != "" {
		out.Region = &statusJSONNameField{Name: value}
	}
	if instance := buildStatusJSONInstance(cfg); instance != nil {
		out.Instance = instance
	}
	if image := buildStatusJSONImage(cfg.Image); image != nil {
		out.Image = image
	}
	if runtime := buildStatusJSONRuntime(cfg.Runtime); runtime != nil {
		out.Runtime = runtime
	}
	out.Sandbox = &statusJSONSandbox{
		Enabled:         cfg.Sandbox.Enabled,
		NetworkMode:     strings.TrimSpace(cfg.Sandbox.NetworkMode),
		UseNemoClaw:     cfg.Sandbox.UseNemoClaw,
		FilesystemAllow: append([]string(nil), cfg.Sandbox.FilesystemAllow...),
	}
	if ssh := buildStatusJSONSSH(cfg.SSH); ssh != nil {
		out.SSH = ssh
	}
	if github := buildStatusJSONGitHub(cfg.GitHub); github != nil {
		out.GitHub = github
	}
	if infra := buildStatusJSONInfra(cfg.Infra); infra != nil {
		out.Infra = infra
	}
	return out
}

func buildStatusJSONInstance(cfg config.Config) *statusJSONInstance {
	instance := &statusJSONInstance{
		Type:        strings.TrimSpace(cfg.Instance.Type),
		DiskSizeGB:  cfg.Instance.DiskSizeGB,
		NetworkMode: strings.TrimSpace(cfg.Instance.NetworkMode),
	}
	if instance.Type == "" && instance.DiskSizeGB == 0 && instance.NetworkMode == "" {
		return nil
	}
	return instance
}

func buildStatusJSONImage(cfg config.ImageConfig) *statusJSONImage {
	image := &statusJSONImage{
		Name: strings.TrimSpace(cfg.Name),
		ID:   strings.TrimSpace(cfg.ID),
	}
	if image.Name == "" && image.ID == "" {
		return nil
	}
	return image
}

func buildStatusJSONRuntime(cfg config.RuntimeConfig) *statusJSONRuntime {
	runtime := &statusJSONRuntime{
		Endpoint:   strings.TrimSpace(cfg.Endpoint),
		Model:      strings.TrimSpace(cfg.Model),
		Port:       cfg.Port,
		Provider:   strings.TrimSpace(cfg.Provider),
		PublicCIDR: strings.TrimSpace(cfg.PublicCIDR),
	}
	if secretID := strings.TrimSpace(cfg.Codex.SecretID); secretID != "" {
		runtime.Codex = &statusJSONRuntimeCodex{SecretID: secretID}
	}
	if runtime.Endpoint == "" && runtime.Model == "" && runtime.Port == 0 && runtime.Provider == "" && runtime.PublicCIDR == "" && runtime.Codex == nil {
		return nil
	}
	return runtime
}

func buildStatusJSONSSH(cfg config.SSHConfig) *statusJSONSSH {
	ssh := &statusJSONSSH{
		KeyName:              strings.TrimSpace(cfg.KeyName),
		PrivateKeyPath:       strings.TrimSpace(cfg.PrivateKeyPath),
		GitHubPrivateKeyPath: strings.TrimSpace(cfg.GitHubPrivateKeyPath),
		CIDR:                 strings.TrimSpace(cfg.CIDR),
		User:                 strings.TrimSpace(cfg.User),
	}
	if ssh.KeyName == "" && ssh.PrivateKeyPath == "" && ssh.GitHubPrivateKeyPath == "" && ssh.CIDR == "" && ssh.User == "" {
		return nil
	}
	return ssh
}

func buildStatusJSONGitHub(cfg config.GitHubConfig) *statusJSONGitHub {
	github := &statusJSONGitHub{
		AuthMode:            strings.TrimSpace(cfg.AuthMode),
		AppID:               strings.TrimSpace(cfg.AppID),
		InstallationID:      strings.TrimSpace(cfg.InstallationID),
		PrivateKeySecretARN: strings.TrimSpace(cfg.PrivateKeySecretARN),
		SSHKeySecretARN:     strings.TrimSpace(cfg.SSHKeySecretARN),
		TokenSecretARN:      strings.TrimSpace(cfg.TokenSecretARN),
	}
	if github.AuthMode == "" && github.AppID == "" && github.InstallationID == "" && github.PrivateKeySecretARN == "" && github.SSHKeySecretARN == "" && github.TokenSecretARN == "" {
		return nil
	}
	return github
}

func buildStatusJSONInfra(cfg config.InfraConfig) *statusJSONInfra {
	infra := &statusJSONInfra{
		Backend:     strings.TrimSpace(cfg.Backend),
		ModuleDir:   strings.TrimSpace(cfg.ModuleDir),
		AWSProfile:  strings.TrimSpace(cfg.AWSProfile),
		Environment: strings.TrimSpace(cfg.Environment),
		InstanceID:  strings.TrimSpace(cfg.InstanceID),
	}
	if infra.Backend == "" && infra.ModuleDir == "" && infra.AWSProfile == "" && infra.Environment == "" && infra.InstanceID == "" {
		return nil
	}
	return infra
}

func buildStatusJSONLiveStatus(agent agentStatusEntry) statusJSONLiveStatus {
	instanceID := strings.TrimSpace(agent.Config.Infra.InstanceID)
	if instanceID == "" {
		return statusJSONLiveStatus{State: "not_provisioned"}
	}
	if agent.Live.Err != nil {
		return statusJSONLiveStatus{
			State: "unavailable",
			Error: agent.Live.Err.Error(),
		}
	}
	if agent.Live.Instance == nil {
		return statusJSONLiveStatus{State: "unavailable"}
	}

	instance := &statusJSONLiveInstance{
		ID:               strings.TrimSpace(agent.Live.Instance.ID),
		Name:             strings.TrimSpace(agent.Live.Instance.Name),
		State:            strings.TrimSpace(agent.Live.Instance.State),
		InstanceType:     strings.TrimSpace(agent.Live.Instance.InstanceType),
		AvailabilityZone: strings.TrimSpace(agent.Live.Instance.AvailabilityZone),
		PublicIP:         strings.TrimSpace(agent.Live.Instance.PublicIP),
		PrivateIP:        strings.TrimSpace(agent.Live.Instance.PrivateIP),
		KeyName:          strings.TrimSpace(agent.Live.Instance.KeyName),
	}
	if !agent.Live.Instance.LaunchTime.IsZero() {
		instance.LaunchTime = agent.Live.Instance.LaunchTime.UTC().Format(time.RFC3339)
	}
	return statusJSONLiveStatus{
		State:    "available",
		Instance: instance,
	}
}

func formatAgentLiveSummary(agent agentStatusEntry) []string {
	instanceID := strings.TrimSpace(agent.Config.Infra.InstanceID)
	if instanceID == "" {
		return []string{"ec2: not provisioned"}
	}
	if agent.Live.Err != nil {
		return []string{fmt.Sprintf("ec2: unavailable: %v", agent.Live.Err)}
	}
	if agent.Live.Instance == nil {
		return []string{"ec2: unavailable"}
	}

	instance := agent.Live.Instance
	lines := []string{"ec2:"}
	if value := strings.TrimSpace(instance.ID); value != "" {
		lines = append(lines, "  instance-id: "+value)
	}
	if value := strings.TrimSpace(instance.State); value != "" {
		lines = append(lines, "  state: "+value)
	}
	if value := strings.TrimSpace(instance.InstanceType); value != "" {
		lines = append(lines, "  instance-type: "+value)
	}
	if value := strings.TrimSpace(instance.AvailabilityZone); value != "" {
		lines = append(lines, "  availability-zone: "+value)
	}
	if value := strings.TrimSpace(instance.PublicIP); value != "" {
		lines = append(lines, "  public-ip: "+value)
	}
	if value := strings.TrimSpace(instance.PrivateIP); value != "" {
		lines = append(lines, "  private-ip: "+value)
	}
	if !instance.LaunchTime.IsZero() {
		lines = append(lines, "  launch-time: "+instance.LaunchTime.UTC().Format(time.RFC3339))
	}
	if value := strings.TrimSpace(instance.KeyName); value != "" {
		lines = append(lines, "  key-name: "+value)
	}
	if value := strings.TrimSpace(instance.Name); value != "" {
		lines = append(lines, "  name: "+value)
	}
	return lines
}

func formatAgentConfigSummary(cfg config.Config) []string {
	lines := make([]string, 0, 8)

	if value := strings.TrimSpace(cfg.Platform.Name); value != "" {
		lines = append(lines, fmt.Sprintf("platform: %s", value))
	}
	if value := strings.TrimSpace(cfg.Compute.Class); value != "" {
		lines = append(lines, fmt.Sprintf("compute: %s", value))
	}
	if value := strings.TrimSpace(cfg.Region.Name); value != "" {
		lines = append(lines, fmt.Sprintf("region: %s", value))
	}
	if value := strings.TrimSpace(cfg.Instance.Type); value != "" {
		if cfg.Instance.DiskSizeGB > 0 {
			lines = append(lines, fmt.Sprintf("instance: %s (%d GB)", value, cfg.Instance.DiskSizeGB))
		} else {
			lines = append(lines, fmt.Sprintf("instance: %s", value))
		}
	}
	if value := strings.TrimSpace(cfg.Image.Name); value != "" {
		if imageID := strings.TrimSpace(cfg.Image.ID); imageID != "" {
			lines = append(lines, fmt.Sprintf("image: %s (%s)", value, imageID))
		} else {
			lines = append(lines, fmt.Sprintf("image: %s", value))
		}
	}
	if value := formatRuntimeSummary(cfg.Runtime); value != "" {
		lines = append(lines, "runtime: "+value)
	}
	if value := formatSandboxSummary(cfg.Sandbox); value != "" {
		lines = append(lines, "sandbox: "+value)
	}
	if value := formatSSHSummary(cfg.SSH); value != "" {
		lines = append(lines, "ssh: "+value)
	}
	if value := formatGitHubSummary(cfg.GitHub); value != "" {
		lines = append(lines, "github: "+value)
	}
	if value := formatInfraSummary(cfg.Infra); value != "" {
		lines = append(lines, "infra: "+value)
	}

	return lines
}

func formatRuntimeSummary(cfg config.RuntimeConfig) string {
	parts := make([]string, 0, 5)
	if value := strings.TrimSpace(cfg.Provider); value != "" {
		parts = append(parts, "provider="+value)
	}
	if value := strings.TrimSpace(cfg.Endpoint); value != "" {
		parts = append(parts, "endpoint="+value)
	}
	if value := strings.TrimSpace(cfg.Model); value != "" {
		parts = append(parts, "model="+value)
	}
	if cfg.Port > 0 {
		parts = append(parts, fmt.Sprintf("port=%d", cfg.Port))
	}
	if value := strings.TrimSpace(cfg.PublicCIDR); value != "" {
		parts = append(parts, "public_cidr="+value)
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " ")
}

func formatSandboxSummary(cfg config.SandboxConfig) string {
	parts := make([]string, 0, 3)
	parts = append(parts, fmt.Sprintf("enabled=%t", cfg.Enabled))
	if value := strings.TrimSpace(cfg.NetworkMode); value != "" {
		parts = append(parts, "network="+value)
	}
	parts = append(parts, fmt.Sprintf("use_nemoclaw=%t", cfg.UseNemoClaw))
	if len(cfg.FilesystemAllow) > 0 {
		parts = append(parts, "filesystem_allow="+strings.Join(cfg.FilesystemAllow, ","))
	}
	return strings.Join(parts, " ")
}

func formatSSHSummary(cfg config.SSHConfig) string {
	parts := make([]string, 0, 4)
	if value := strings.TrimSpace(cfg.User); value != "" {
		parts = append(parts, "user="+value)
	}
	if value := strings.TrimSpace(cfg.KeyName); value != "" {
		parts = append(parts, "key_name="+value)
	}
	if value := strings.TrimSpace(cfg.CIDR); value != "" {
		parts = append(parts, "cidr="+value)
	}
	if value := strings.TrimSpace(cfg.PrivateKeyPath); value != "" {
		parts = append(parts, "private_key_path="+value)
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " ")
}

func formatGitHubSummary(cfg config.GitHubConfig) string {
	parts := make([]string, 0, 5)
	if value := strings.TrimSpace(cfg.AuthMode); value != "" {
		parts = append(parts, "auth_mode="+value)
	}
	if value := strings.TrimSpace(cfg.AppID); value != "" {
		parts = append(parts, "app_id="+value)
	}
	if value := strings.TrimSpace(cfg.InstallationID); value != "" {
		parts = append(parts, "installation_id="+value)
	}
	if value := strings.TrimSpace(cfg.PrivateKeySecretARN); value != "" {
		parts = append(parts, "private_key_secret_arn="+value)
	}
	if value := strings.TrimSpace(cfg.TokenSecretARN); value != "" {
		parts = append(parts, "token_secret_arn="+value)
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " ")
}

func formatInfraSummary(cfg config.InfraConfig) string {
	parts := make([]string, 0, 2)
	if value := strings.TrimSpace(cfg.Backend); value != "" {
		parts = append(parts, "backend="+value)
	}
	if value := strings.TrimSpace(cfg.ModuleDir); value != "" {
		parts = append(parts, "module_dir="+value)
	}
	if value := strings.TrimSpace(cfg.AWSProfile); value != "" {
		parts = append(parts, "aws_profile="+value)
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " ")
}
