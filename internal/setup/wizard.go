package setup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"agenthub/internal/config"
	"agenthub/internal/prompt"
	"agenthub/internal/provider"
	awsprovider "agenthub/internal/provider/aws"
)

type Wizard struct {
	Prompter        *prompt.Session
	Out             io.Writer
	ProviderFactory func(platform, computeClass string) provider.CloudProvider
	Provider        provider.CloudProvider
	Existing        *config.Config
	AWSProfile      string
	AgentName       string
}

const initAWSLookupTimeout = 5 * time.Second

var detectInitSSHCIDR = defaultDetectInitSSHCIDR

func NewWizard(prompter *prompt.Session, out io.Writer, factory func(platform, computeClass string) provider.CloudProvider, existing *config.Config) *Wizard {
	return &Wizard{Prompter: prompter, Out: out, ProviderFactory: factory, Existing: existing}
}

func (w *Wizard) Run(ctx context.Context) (*config.Config, error) {
	compact := func(parts ...string) string {
		filtered := make([]string, 0, len(parts))
		for _, part := range parts {
			if value := strings.TrimSpace(part); value != "" {
				filtered = append(filtered, value)
			}
		}
		if len(filtered) == 0 {
			return "-"
		}
		return strings.Join(filtered, " / ")
	}
	render := func(phase string, step int, current string, agentName, platform, computeClass, region, instanceSummary, accessSummary, runtimeSummary, reviewSummary string, accessPending, runtimePending bool) {
		items := []wizardProgressItem{
			{Label: "Agent name", Value: agentName},
			{Label: "Platform", Value: platform},
			{Label: "Compute mode", Value: computeClass},
			{Label: "Region", Value: region},
			{Label: "Instance", Value: instanceSummary},
			{Label: "Access", Value: accessSummary},
			{Label: "Runtime", Value: runtimeSummary},
			{Label: "Review", Value: reviewSummary},
		}
		renderWizardProgress(w.Out, phase, "Agent setup", step, 8, current, items)
	}

	render("Setup", 1, "Agent name", "", "", "", "", "", "", "", "", false, false)
	agentName, err := w.Prompter.Text("Agent name", defaultAgentName())
	if err != nil {
		return nil, err
	}
	agentName, err = normalizeAgentName(agentName)
	if err != nil {
		return nil, err
	}
	w.AgentName = agentName

	render("Setup", 2, "Platform", agentName, "", "", "", "", "", "", "", false, false)
	platform, err := w.Prompter.Select("Select platform", []string{"aws", "gcp", "azure"}, config.PlatformAWS)
	if err != nil {
		return nil, err
	}
	if platform != config.PlatformAWS {
		return nil, fmt.Errorf("%s is not implemented yet", platform)
	}

	computeClass := defaultComputeClass(w.Existing)
	render("Setup", 3, "Compute mode", agentName, platform, computeClass, "", "", "", "", "", false, false)
	computeClass, err = w.Prompter.Select("Select compute mode", []string{config.ComputeClassCPU, config.ComputeClassGPU}, computeClass)
	if err != nil {
		return nil, err
	}

	profile, prompted, err := w.selectAWSProfile()
	if err != nil {
		return nil, err
	}
	w.AWSProfile = profile
	if profile == "" {
		return nil, errors.New("AWS profile is required")
	}
	if prompted {
		fmt.Fprintf(w.Out, "! This profile uses AWS SSO\n")
		fmt.Fprintf(w.Out, "Run `aws sso login --profile %s` if needed\n", profile)
		ready, err := w.Prompter.Confirm("Continue after AWS SSO login", true)
		if err != nil {
			return nil, err
		}
		if !ready {
			return nil, errors.New("setup cancelled")
		}
	}

	if w.Provider == nil && w.ProviderFactory != nil {
		w.Provider = w.ProviderFactory(platform, computeClass)
	}
	if w.Provider != nil {
		authCtx, cancel := bestEffortAWSContext(ctx)
		if _, err := w.Provider.CheckAuth(authCtx); err != nil {
			cancel()
			var authErr *awsprovider.AuthError
			if errors.As(err, &authErr) {
				fmt.Fprintln(w.Out, "Warning: AWS auth check unavailable; continuing.")
			} else {
				return nil, err
			}
		}
		cancel()
	}

	regions, err := w.listRegions(ctx)
	if err != nil {
		return nil, err
	}
	regionDefault := "us-east-1"
	if w.Existing != nil && strings.TrimSpace(w.Existing.Region.Name) != "" && slices.Contains(regions, w.Existing.Region.Name) {
		regionDefault = w.Existing.Region.Name
	}
	render("Environment check", 4, "Region", agentName, platform, computeClass, "", "", "", "", "", false, false)
	region, err := w.Prompter.Select("Select AWS region", regions, regionDefault)
	if err != nil {
		return nil, err
	}

	if err := w.warnOnQuota(ctx, region); err != nil {
		return nil, err
	}

	instanceTypes, err := w.listInstanceTypes(ctx, region, computeClass)
	if err != nil {
		return nil, err
	}
	render("Environment check", 5, "Instance", agentName, platform, computeClass, region, "", "", "", "", false, false)
	instanceType, err := w.Prompter.SelectSearch("Select instance type", instanceTypes, defaultInstanceType(computeClass))
	if err != nil {
		return nil, err
	}

	images, err := w.listImages(ctx, region, computeClass)
	if err != nil {
		fmt.Fprintln(w.Out, "Warning: AWS image lookup unavailable; using bundled fallback images.")
		images = fallbackAWSBaseImages(region, computeClass)
	}
	render("Environment check", 5, "Instance", agentName, platform, computeClass, region, compact(instanceType), "", "", "", false, false)
	image, err := selectBaseImage(w.Prompter, images)
	if err != nil {
		return nil, err
	}

	render("Environment check", 5, "Instance", agentName, platform, computeClass, region, compact(instanceType, image.Name), "", "", "", false, false)
	diskSize, err := w.Prompter.Int("Enter disk size (GB)", 20)
	if err != nil {
		return nil, err
	}

	render("Environment check", 6, "Access", agentName, platform, computeClass, region, compact(instanceType, image.Name, fmt.Sprintf("%d GB", diskSize)), "", "", "", false, false)
	networkMode, err := w.Prompter.Select("Select network mode", []string{"private", "public"}, defaultNetworkMode(computeClass))
	if err != nil {
		return nil, err
	}

	sshKeyName := ""
	sshPrivateKeyPath := defaultSSHPrivateKeyPath()
	sshCIDR := ""
	sshUser := ""
	if w.Existing != nil {
		sshKeyName = strings.TrimSpace(w.Existing.SSH.KeyName)
		if existingPath := strings.TrimSpace(w.Existing.SSH.PrivateKeyPath); existingPath != "" {
			sshPrivateKeyPath = existingPath
		}
		sshCIDR = strings.TrimSpace(w.Existing.SSH.CIDR)
		sshUser = strings.TrimSpace(w.Existing.SSH.User)
	}
	if sshKeyName == "" {
		sshKeyName = defaultSSHKeyName()
	}
	accessSummary := networkMode
	render("Environment check", 6, "Access", agentName, platform, computeClass, region, compact(instanceType, image.Name, fmt.Sprintf("%d GB", diskSize)), accessSummary, "", "", networkMode == "public", false)
	if networkMode == "public" {
		sshKeyName, err = w.Prompter.Text("SSH key pair name", sshKeyName)
		if err != nil {
			return nil, err
		}
		if sshCIDR == "" {
			if detected, detectErr := detectInitSSHCIDR(ctx); detectErr == nil {
				sshCIDR = detected
			}
		}
		sshPrivateKeyPath, err = w.Prompter.Text("SSH private key path", sshPrivateKeyPath)
		if err != nil {
			return nil, err
		}
		sshCIDR, err = w.Prompter.Text("SSH CIDR", sshCIDR)
		if err != nil {
			return nil, err
		}
		sshUserDefault := sshUser
		if sshUserDefault == "" {
			sshUserDefault = sshUsernameForImage(image.Name, image.ID)
		}
		sshUser, err = w.Prompter.Text("SSH user", sshUserDefault)
		if err != nil {
			return nil, err
		}
		accessSummary = compact(networkMode, sshKeyName, sshCIDR, sshUser)
	} else {
		sshKeyName = ""
		sshPrivateKeyPath = ""
		sshCIDR = ""
		sshUser = ""
		accessSummary = networkMode
	}

	render("Environment check", 6, "Access", agentName, platform, computeClass, region, compact(instanceType, image.Name, fmt.Sprintf("%d GB", diskSize)), compact(networkMode, sshKeyName, sshCIDR, sshUser), "", "", false, false)
	repoSlug, err := detectGitHubRepoSlug(ctx)
	if err == nil && strings.TrimSpace(repoSlug) != "" {
		fmt.Fprintf(w.Out, "! detected GitHub repo candidate: %s\n", repoSlug)
	}
	githubCfg := config.GitHubConfig{}
	if w.Existing != nil {
		githubCfg = w.Existing.GitHub
	}
	connectGitHub, err := w.Prompter.Confirm("Configure GitHub access?", config.HasGitHubAuth(w.Existing))
	if err != nil {
		return nil, err
	}
	if connectGitHub {
		defaultAuthMode := config.GitHubAuthModeFor(githubCfg)
		if defaultAuthMode == "" {
			if _, lookPathErr := exec.LookPath("gh"); lookPathErr == nil {
				defaultAuthMode = config.GitHubAuthModeUser
			} else {
				defaultAuthMode = config.GitHubAuthModeApp
			}
		}
		authMode, err := w.Prompter.Select("Select GitHub auth mode", []string{config.GitHubAuthModeUser, config.GitHubAuthModeApp}, defaultAuthMode)
		if err != nil {
			return nil, err
		}
		switch authMode {
		case config.GitHubAuthModeUser:
			githubCfg, err = bootstrapGitHubUserAuth(ctx, w.AWSProfile, region, repoSlug)
			if err != nil {
				return nil, err
			}
		case config.GitHubAuthModeApp:
			githubCfg = config.GitHubConfig{AuthMode: config.GitHubAuthModeApp}
			githubCfg.AppID, err = w.Prompter.Text("GitHub App ID", strings.TrimSpace(githubCfg.AppID))
			if err != nil {
				return nil, err
			}
			githubCfg.InstallationID, err = w.Prompter.Text("GitHub installation ID", strings.TrimSpace(githubCfg.InstallationID))
			if err != nil {
				return nil, err
			}
			githubCfg.PrivateKeySecretARN, err = w.Prompter.Text("GitHub private key secret ARN", strings.TrimSpace(githubCfg.PrivateKeySecretARN))
			if err != nil {
				return nil, err
			}
			githubCfg.TokenSecretARN = ""
		}
	}

	useNemoClaw, err := w.Prompter.Confirm("Use NemoClaw", true)
	if err != nil {
		return nil, err
	}

	render("Environment check", 7, "Runtime", agentName, platform, computeClass, region, compact(instanceType, image.Name, fmt.Sprintf("%d GB", diskSize)), compact(networkMode, sshKeyName, sshCIDR, sshUser), "", "", false, false)
	runtimeProvider, err := w.Prompter.Select("Select model provider", runtimeProviderOptions(), defaultRuntimeProvider(w.Existing))
	if err != nil {
		return nil, err
	}

	if runtimeProvider == "codex" {
		fmt.Fprintln(w.Out, "Codex auth uses the local browser login flow or existing signed-in state.")
		fmt.Fprintln(w.Out, "If you are not already authenticated, run `agenthub onboard --auth-choice openai-codex` before provisioning.")
	}

	runtimePublicCIDR := "0.0.0.0/0"
	if w.Existing != nil {
		if existingCIDR := strings.TrimSpace(w.Existing.Runtime.PublicCIDR); existingCIDR != "" {
			runtimePublicCIDR = existingCIDR
		}
	}
	if networkMode != "public" {
		runtimePublicCIDR = ""
	}

	nimEndpoint := ""
	if runtimeProvider != "aws-bedrock" {
		render("Environment check", 7, "Runtime", agentName, platform, computeClass, region, compact(instanceType, image.Name, fmt.Sprintf("%d GB", diskSize)), accessSummary, runtimeProvider, "", false, false)
		nimEndpoint, err = w.Prompter.Text("NIM endpoint", defaultEndpoint(computeClass))
		if err != nil {
			return nil, err
		}
	}

	model := ""
	if runtimeProvider != "codex" {
		model, err = w.Prompter.Text("Model name", defaultRuntimeModel(runtimeProvider))
		if err != nil {
			return nil, err
		}
	}

	cfg := &config.Config{
		Platform: config.PlatformConfig{Name: platform},
		Compute:  config.ComputeConfig{Class: computeClass},
		Region:   config.RegionConfig{Name: region},
		Instance: config.InstanceConfig{Type: instanceType, DiskSizeGB: diskSize, NetworkMode: networkMode},
		Image:    config.ImageConfig{Name: image.Name, ID: image.ID},
		SSH: config.SSHConfig{
			KeyName:        sshKeyName,
			PrivateKeyPath: sshPrivateKeyPath,
			CIDR:           sshCIDR,
			User:           sshUser,
		},
		Infra: config.InfraConfig{
			Backend:   "terraform",
			ModuleDir: filepath.Join("infra", "aws", "ec2"),
		},
		GitHub: githubCfg,
		Sandbox: config.SandboxConfig{
			Enabled:     true,
			NetworkMode: networkMode,
			UseNemoClaw: useNemoClaw,
		},
		Runtime: config.RuntimeConfig{
			Endpoint:   nimEndpoint,
			Model:      model,
			Provider:   runtimeProvider,
			PublicCIDR: runtimePublicCIDR,
		},
	}

	reviewSummary := compact(
		cfg.Platform.Name,
		cfg.Region.Name,
		cfg.Instance.Type,
		cfg.Image.Name,
		fmt.Sprintf("%d GB", cfg.Instance.DiskSizeGB),
		cfg.Sandbox.NetworkMode,
		cfg.Runtime.Provider,
	)
	render("Review configuration", 8, "Review", agentName, platform, computeClass, region, compact(instanceType, image.Name, fmt.Sprintf("%d GB", diskSize)), compact(networkMode, sshKeyName, sshCIDR, sshUser), compact(runtimeProvider, nimEndpoint, model), reviewSummary, false, false)

	renderWizardPhase(w.Out, "Review configuration")
	fmt.Fprintln(w.Out, "Advanced details")
	fmt.Fprintf(w.Out, "- image: %s\n", valueOrDash(cfg.Image.Name))
	fmt.Fprintf(w.Out, "- image id: %s\n", valueOrDash(image.ID))
	fmt.Fprintf(w.Out, "- disk size: %d GB\n", cfg.Instance.DiskSizeGB)
	fmt.Fprintf(w.Out, "- infra backend: %s\n", cfg.Infra.Backend)
	fmt.Fprintf(w.Out, "- terraform module: %s\n", cfg.Infra.ModuleDir)
	fmt.Fprintf(w.Out, "- use NemoClaw: %t\n", cfg.Sandbox.UseNemoClaw)
	if strings.TrimSpace(cfg.SSH.KeyName) != "" {
		fmt.Fprintf(w.Out, "- ssh key pair: %s\n", cfg.SSH.KeyName)
	}
	if strings.TrimSpace(cfg.SSH.PrivateKeyPath) != "" {
		fmt.Fprintf(w.Out, "- ssh private key: %s\n", cfg.SSH.PrivateKeyPath)
	}
	if strings.TrimSpace(cfg.SSH.CIDR) != "" {
		fmt.Fprintf(w.Out, "- ssh cidr: %s\n", cfg.SSH.CIDR)
	}
	if strings.TrimSpace(cfg.SSH.User) != "" {
		fmt.Fprintf(w.Out, "- ssh user: %s\n", cfg.SSH.User)
	}
	if strings.TrimSpace(cfg.Runtime.PublicCIDR) != "" {
		fmt.Fprintf(w.Out, "- runtime cidr: %s\n", cfg.Runtime.PublicCIDR)
	}
	if strings.TrimSpace(cfg.Runtime.Endpoint) != "" {
		fmt.Fprintf(w.Out, "- NIM endpoint: %s\n", cfg.Runtime.Endpoint)
	}
	if strings.TrimSpace(cfg.Runtime.Model) != "" {
		fmt.Fprintf(w.Out, "- model: %s\n", cfg.Runtime.Model)
	}
	if cfg.Runtime.Provider == "codex" {
		fmt.Fprintln(w.Out, "- codex auth: browser login or existing local auth")
	}
	if cfg.Runtime.Provider == "aws-bedrock" {
		fmt.Fprintln(w.Out, "- bedrock auth: uses instance role")
	}
	if config.HasGitHubAuth(cfg) {
		mode := config.GitHubAuthModeFor(cfg.GitHub)
		switch mode {
		case config.GitHubAuthModeUser:
			fmt.Fprintf(w.Out, "- github auth: mode=user / token secret %s\n", valueOrDash(cfg.GitHub.TokenSecretARN))
		case config.GitHubAuthModeApp:
			fmt.Fprintf(w.Out, "- github auth: mode=app / GitHub App %s / installation %s / secret %s\n",
				valueOrDash(cfg.GitHub.AppID),
				valueOrDash(cfg.GitHub.InstallationID),
				valueOrDash(cfg.GitHub.PrivateKeySecretARN),
			)
		default:
			fmt.Fprintf(w.Out, "- github auth: %s\n", valueOrDash(cfg.GitHub.AuthMode))
		}
	}

	confirm, err := w.Prompter.Confirm("Write this configuration", true)
	if err != nil {
		return nil, err
	}
	if !confirm {
		return nil, errors.New("setup cancelled")
	}

	return cfg, nil
}

func runtimeProviderOptions() []string {
	return []string{"codex", "aws-bedrock", "gemini", "claude-code"}
}

func defaultRuntimeProvider(existing *config.Config) string {
	if existing == nil {
		return "codex"
	}
	provider := strings.ToLower(strings.TrimSpace(existing.Runtime.Provider))
	if provider == "" {
		return "codex"
	}
	return provider
}

func defaultAgentName() string {
	return "default"
}

func normalizeAgentName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "default", nil
	}
	if name == "." || name == ".." {
		return "", fmt.Errorf("invalid agent name %q", name)
	}
	if strings.ContainsAny(name, `/\`) {
		return "", fmt.Errorf("invalid agent name %q: path separators are not allowed", name)
	}
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			continue
		}
		return "", fmt.Errorf("invalid agent name %q: use letters, digits, hyphen, or underscore", name)
	}
	return name, nil
}

func defaultRuntimeModel(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "aws-bedrock":
		return "anthropic.claude-3-haiku-20240307-v1:0"
	case "codex":
		return ""
	default:
		return "llama3.2"
	}
}

func (w *Wizard) selectAWSProfile() (string, bool, error) {
	profile := strings.TrimSpace(w.AWSProfile)
	if profile == "" {
		profile = strings.TrimSpace(os.Getenv("AWS_PROFILE"))
	}
	if profile == "" {
		profile = strings.TrimSpace(os.Getenv("AWS_DEFAULT_PROFILE"))
	}
	if profile != "" {
		return profile, false, nil
	}
	if !w.Prompter.Interactive {
		return "", false, errors.New("AWS profile is required: pass --profile, set AWS_PROFILE, or run interactively")
	}
	value, err := w.Prompter.Text("AWS profile", "")
	if err != nil {
		return "", false, err
	}
	return strings.TrimSpace(value), true, nil
}

func (w *Wizard) listRegions(ctx context.Context) ([]string, error) {
	if w.Provider == nil {
		return fallbackAWSRegions(), nil
	}
	regions, err := w.Provider.ListRegions(ctx)
	if err != nil {
		fmt.Fprintln(w.Out, "Warning: AWS region lookup unavailable; using bundled fallback regions.")
		return fallbackAWSRegions(), nil
	}
	if len(regions) == 0 {
		fmt.Fprintln(w.Out, "Warning: AWS region lookup returned no regions; using bundled fallback regions.")
		return fallbackAWSRegions(), nil
	}
	return regions, nil
}

func (w *Wizard) listInstanceTypes(ctx context.Context, region, computeClass string) ([]string, error) {
	if w.Provider == nil {
		return fallbackAWSInstanceTypes(computeClass), nil
	}
	items, err := w.Provider.RecommendInstanceTypes(ctx, region, computeClass)
	if err != nil {
		fmt.Fprintln(w.Out, "Warning: AWS instance type lookup unavailable; using bundled fallback instance types.")
		return fallbackAWSInstanceTypes(computeClass), nil
	}
	options := make([]string, 0, len(items))
	for _, item := range items {
		options = append(options, item.Name)
	}
	if len(options) == 0 {
		fmt.Fprintln(w.Out, "Warning: AWS instance type lookup returned no options; using bundled fallback instance types.")
		return fallbackAWSInstanceTypes(computeClass), nil
	}
	return options, nil
}

func (w *Wizard) listImages(ctx context.Context, region, computeClass string) ([]provider.BaseImage, error) {
	if w.Provider == nil {
		return fallbackAWSBaseImages(region, computeClass), nil
	}
	imageCtx, cancel := bestEffortAWSContext(ctx)
	defer cancel()
	items, err := w.Provider.RecommendBaseImages(imageCtx, region, computeClass)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return fallbackAWSBaseImages(region, computeClass), nil
	}
	return items, nil
}

func (w *Wizard) warnOnQuota(ctx context.Context, region string) error {
	if w.Provider == nil {
		return nil
	}
	quotaCtx, cancel := bestEffortAWSContext(ctx)
	defer cancel()
	report, err := w.Provider.CheckGPUQuota(quotaCtx, region, "g5")
	if err != nil {
		fmt.Fprintln(w.Out, "Warning: GPU quota check unavailable; continuing.")
		return nil
	}
	if report.Source == "mock" {
		fmt.Fprintln(w.Out, "Quota check is a mock report; live AWS Service Quotas access is not wired yet.")
		for _, note := range report.Notes {
			fmt.Fprintf(w.Out, "  - %s\n", note)
		}
		return nil
	}
	if report.LikelyCreatable {
		return nil
	}

	fmt.Fprintf(w.Out, "Warning: GPU quota for %s in %s looks insufficient.\n", report.InstanceFamily, report.Region)
	for _, check := range report.Checks {
		fmt.Fprintf(w.Out, "  %s: limit=%d usage=%s remaining=%d\n", check.QuotaName, check.CurrentLimit, formatUsage(check.CurrentUsage), check.EstimatedRemaining)
	}
	if len(report.Notes) > 0 {
		fmt.Fprintln(w.Out, "Notes:")
		for _, note := range report.Notes {
			fmt.Fprintf(w.Out, "  - %s\n", note)
		}
	}
	confirm, err := w.Prompter.Confirm("Quota looks insufficient. Continue anyway", false)
	if err != nil {
		return err
	}
	if !confirm {
		return errors.New("setup cancelled due to insufficient quota")
	}
	return nil
}

func defaultComputeClass(existing *config.Config) string {
	if existing == nil {
		return config.ComputeClassGPU
	}
	if class := config.EffectiveComputeClass(existing.Compute.Class); class != "" {
		return class
	}
	return config.ComputeClassGPU
}

func defaultInstanceType(computeClass string) string {
	if config.EffectiveComputeClass(computeClass) == config.ComputeClassCPU {
		return "t3.xlarge"
	}
	return "g5.xlarge"
}

func defaultNetworkMode(computeClass string) string {
	return "public"
}

func defaultEndpoint(computeClass string) string {
	if config.EffectiveComputeClass(computeClass) == config.ComputeClassCPU {
		return "https://nim.example.com"
	}
	return "http://localhost:11434"
}

func defaultSSHPrivateKeyPath() string {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return "~/.ssh/id_ed25519"
	}
	return filepath.Join(home, ".ssh", "id_ed25519")
}

func defaultSSHKeyName() string {
	return "agenthub"
}

func defaultDetectInitSSHCIDR(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.ipify.org", nil)
	if err != nil {
		return "", err
	}
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("unexpected status %s", resp.Status)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	value := strings.TrimSpace(string(body))
	if value == "" {
		return "", errors.New("empty public IP")
	}
	if strings.Contains(value, "/") {
		prefix, err := netip.ParsePrefix(value)
		if err != nil {
			return "", err
		}
		return prefix.String(), nil
	}
	addr, err := netip.ParseAddr(value)
	if err != nil {
		return "", err
	}
	if addr.Is4() {
		return addr.String() + "/32", nil
	}
	return addr.String() + "/128", nil
}

func sshUsernameForImage(imageName, imageID string) string {
	lower := strings.ToLower(strings.TrimSpace(imageName) + " " + strings.TrimSpace(imageID))
	if strings.Contains(lower, "ubuntu") {
		return "ubuntu"
	}
	return "ec2-user"
}

func selectBaseImage(prompter *prompt.Session, images []provider.BaseImage) (provider.BaseImage, error) {
	if len(images) == 0 {
		return provider.BaseImage{}, errors.New("no base images available")
	}

	options := make([]string, 0, len(images))
	for _, image := range images {
		options = append(options, image.Name)
	}
	defaultName := images[0].Name
	if preferred := findBaseImage(images, preferredBaseImageName(images)); preferred.Name != "" {
		defaultName = preferred.Name
	}

	selected, err := prompter.Select("Select base image", options, defaultName)
	if err != nil {
		return provider.BaseImage{}, err
	}
	image := findBaseImage(images, selected)
	if image.Name == "" {
		return provider.BaseImage{}, fmt.Errorf("base image %q not found", selected)
	}
	return image, nil
}

func findBaseImage(images []provider.BaseImage, name string) provider.BaseImage {
	for _, image := range images {
		if image.Name == name {
			return image
		}
	}
	return provider.BaseImage{}
}

func preferredBaseImageName(images []provider.BaseImage) string {
	for _, image := range images {
		lower := strings.ToLower(strings.TrimSpace(image.Name))
		if strings.Contains(lower, "ubuntu 22.04") && !strings.Contains(lower, "gpu") {
			return image.Name
		}
	}
	for _, image := range images {
		lower := strings.ToLower(strings.TrimSpace(image.Name))
		if strings.Contains(lower, "deep learning ami gpu ubuntu 22.04") {
			return image.Name
		}
	}
	if len(images) > 0 {
		return images[0].Name
	}
	return ""
}

func formatUsage(value *int) string {
	if value == nil {
		return "n/a"
	}
	return fmt.Sprintf("%d", *value)
}

func fallbackAWSBaseImages(region, computeClass string) []provider.BaseImage {
	if config.EffectiveComputeClass(computeClass) == config.ComputeClassCPU {
		return []provider.BaseImage{{
			Name:               "Ubuntu 22.04 LTS",
			Architecture:       "x86_64",
			Owner:              "canonical",
			VirtualizationType: "hvm",
			RootDeviceType:     "ebs",
			Region:             region,
			Source:             "fallback",
			SSMParameter:       "/aws/service/canonical/ubuntu/server/22.04/stable/current/amd64/hvm/ebs-gp2/ami-id",
		}}
	}
	return []provider.BaseImage{{
		Name:               "AWS Deep Learning AMI GPU Ubuntu 22.04",
		Architecture:       "x86_64",
		Owner:              "amazon",
		VirtualizationType: "hvm",
		RootDeviceType:     "ebs",
		Region:             region,
		Source:             "fallback",
		SSMParameter:       "/aws/service/deeplearning/ami/x86_64/base-oss-nvidia-driver-gpu-ubuntu-22.04/latest/ami-id",
	}}
}

func fallbackAWSRegions() []string {
	return []string{"us-east-1", "us-west-2"}
}

func fallbackAWSInstanceTypes(computeClass string) []string {
	if config.EffectiveComputeClass(computeClass) == config.ComputeClassCPU {
		return []string{"t3.xlarge", "t3.2xlarge", "t3.medium"}
	}
	return []string{"g5.xlarge", "g4dn.xlarge", "g6.xlarge"}
}

func bestEffortAWSContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, initAWSLookupTimeout)
}
