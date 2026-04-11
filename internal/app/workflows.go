package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"agenthub/internal/codexauth"
	"agenthub/internal/config"
	"agenthub/internal/host"
	infratf "agenthub/internal/infra/terraform"
	"agenthub/internal/provider"
	"agenthub/internal/runtimeinstall"
	"agenthub/internal/verify"
)

var newLocalExecutor = func() host.Executor {
	return host.NewLocalExecutor()
}

var newSSHExecutor = func(cfg host.SSHConfig) host.Executor {
	return host.NewSSHExecutor(cfg)
}

var (
	defaultSSHReadyTimeout     = 5 * time.Minute
	defaultSSHReadyInitialWait = 2 * time.Second
	defaultSSHReadyMaxWait     = 10 * time.Second
)

type installOptions struct {
	Target            string
	SSHUser           string
	SSHKey            string
	SSHPort           int
	WorkingDir        string
	Port              int
	UseNemoClaw       bool
	DisableNemoClaw   bool
	RuntimeConfigPath string
}

type createOptions struct {
	SSHKeyName      string
	SSHCIDR         string
	SSHUser         string
	SSHKey          string
	SSHPort         int
	WorkingDir      string
	Port            int
	UseNemoClaw     bool
	DisableNemoClaw bool
	ConfigPath      string
	AgentName       string
}

type terraformVars struct {
	AWSProfile                string `json:"aws_profile"`
	Region                    string `json:"region"`
	ComputeClass              string `json:"compute_class"`
	Owner                     string `json:"owner"`
	AgentName                 string `json:"agent_name"`
	Environment               string `json:"environment"`
	InstanceType              string `json:"instance_type"`
	DiskSizeGB                int    `json:"disk_size_gb"`
	NetworkMode               string `json:"network_mode"`
	ImageName                 string `json:"image_name"`
	ImageID                   string `json:"image_id"`
	RuntimePort               int    `json:"runtime_port"`
	RuntimeCIDR               string `json:"runtime_cidr"`
	RuntimeProvider           string `json:"runtime_provider"`
	SSHKeyName                string `json:"ssh_key_name"`
	SSHPublicKey              string `json:"ssh_public_key"`
	GitHubPrivateKeySecretARN string `json:"github_private_key_secret_arn"`
	GitHubSSHKeySecretARN     string `json:"github_ssh_key_secret_arn"`
	GitHubTokenSecretARN      string `json:"github_token_secret_arn"`
	SSHCIDR                   string `json:"ssh_cidr"`
	SSHUser                   string `json:"ssh_user"`
	NamePrefix                string `json:"name_prefix"`
	SecurityGroupName         string `json:"security_group_name"`
	UseNemoClaw               bool   `json:"use_nemoclaw"`
	NIMEndpoint               string `json:"nim_endpoint"`
	Model                     string `json:"model"`
	SourceURL                 string `json:"source_archive_url"`
}

type terraformInputs struct {
	NetworkMode               string
	RuntimePort               int
	RuntimeCIDR               string
	RuntimeProvider           string
	SSHKeyName                string
	SSHPublicKey              string
	GitHubPrivateKeySecretARN string
	GitHubSSHKeySecretARN     string
	GitHubTokenSecretARN      string
	SSHCIDR                   string
	SSHUser                   string
	SourceURL                 string
	Owner                     string
	AgentName                 string
	Environment               string
}

type verifyOptions struct {
	Target            string
	SSHUser           string
	SSHKey            string
	SSHPort           int
	RuntimeConfigPath string
}

type createGroupedStageRunner interface {
	stageRunner
	RunGroup(ctx context.Context, group, title string, fn func(context.Context) error) error
}

type awsKeyPairInspector interface {
	KeyPairExists(ctx context.Context, region, keyName string) (bool, error)
}

type awsSecurityGroupInspector interface {
	FindSecurityGroupByName(ctx context.Context, region, groupName, owner, agentName, environment string) (string, error)
}

func runCreateStage(progress stageRunner, ctx context.Context, group, title string, fn func(context.Context) error) error {
	if progress == nil {
		return fn(ctx)
	}
	if grouped, ok := progress.(createGroupedStageRunner); ok {
		return grouped.RunGroup(ctx, group, title, fn)
	}
	return progress.Run(ctx, title, fn)
}

func runInfraCreate(ctx context.Context, profile string, cfg *config.Config, opts createOptions, progress stageRunner) (*provider.Instance, error) {
	if cfg == nil {
		return nil, errors.New("config is required")
	}

	var inputs terraformInputs
	var adviser provider.CloudProvider
	var image provider.BaseImage
	var instanceType string
	if err := runCreateStage(progress, ctx, "Infrastructure", "resolve provisioning inputs", func(runCtx context.Context) error {
		var err error
		inputs, err = buildTerraformInputs(runCtx, profile, cfg, opts)
		if err != nil {
			return err
		}
		adviser = newAWSProvider(profile, cfg.Compute.Class)
		if _, err := adviser.CheckAuth(runCtx); err != nil {
			return fmt.Errorf("aws auth check failed: %w", err)
		}
		image, err = resolveInfraImage(runCtx, adviser, cfg)
		if err != nil {
			return err
		}
		instanceType = strings.TrimSpace(cfg.Instance.Type)
		if instanceType == "" {
			recs, recErr := adviser.RecommendInstanceTypes(runCtx, cfg.Region.Name, cfg.Compute.Class)
			if recErr != nil {
				return recErr
			}
			if len(recs) == 0 {
				return errors.New("no recommended instance types available")
			}
			instanceType = recs[0].Name
		}
		return nil
	}); err != nil {
		return nil, err
	}

	workdir := ""
	var varsPath string
	var backend infratf.InfraBackend
	var output *infratf.InfraOutput
	if err := runCreateStage(progress, ctx, "Infrastructure", "prepare terraform", func(runCtx context.Context) error {
		var err error
		workdir, err = prepareTerraformWorkdir(opts.ConfigPath)
		if err != nil {
			return err
		}
		backend, err = newTerraformBackend(profile, cfg)
		if err != nil {
			return err
		}
		if err := backend.Init(runCtx, workdir); err != nil {
			return err
		}
		varsPath, err = writeTerraformVars(workdir, terraformVars{
			Region:                    cfg.Region.Name,
			ComputeClass:              config.EffectiveComputeClass(cfg.Compute.Class),
			Owner:                     inputs.Owner,
			AgentName:                 inputs.AgentName,
			Environment:               inputs.Environment,
			InstanceType:              instanceType,
			DiskSizeGB:                cfg.Instance.DiskSizeGB,
			NetworkMode:               inputs.NetworkMode,
			ImageID:                   image.ID,
			RuntimePort:               inputs.RuntimePort,
			RuntimeCIDR:               inputs.RuntimeCIDR,
			RuntimeProvider:           inputs.RuntimeProvider,
			SSHKeyName:                inputs.SSHKeyName,
			SSHPublicKey:              inputs.SSHPublicKey,
			GitHubPrivateKeySecretARN: inputs.GitHubPrivateKeySecretARN,
			GitHubSSHKeySecretARN:     inputs.GitHubSSHKeySecretARN,
			GitHubTokenSecretARN:      inputs.GitHubTokenSecretARN,
			SSHCIDR:                   inputs.SSHCIDR,
			SSHUser:                   inputs.SSHUser,
			NamePrefix:                "agenthub",
			SecurityGroupName:         securityGroupIdentityName("agenthub", inputs.Owner, inputs.AgentName, inputs.Environment),
			UseNemoClaw:               cfg.Sandbox.UseNemoClaw,
			NIMEndpoint:               cfg.Runtime.Endpoint,
			Model:                     cfg.Runtime.Model,
			SourceURL:                 inputs.SourceURL,
		})
		if err != nil {
			return err
		}
		if strings.EqualFold(cfg.Platform.Name, config.PlatformAWS) {
			inspector, ok := adviser.(awsKeyPairInspector)
			if ok {
				keyName := strings.TrimSpace(inputs.SSHKeyName)
				if keyName != "" {
					exists, err := inspector.KeyPairExists(runCtx, cfg.Region.Name, keyName)
					if err != nil {
						return err
					}
					if exists {
						if err := backend.Import(runCtx, workdir, "aws_key_pair.this", keyName); err != nil {
							return err
						}
					}
				}
			}
			sgInspector, ok := adviser.(awsSecurityGroupInspector)
			if ok && !terraformStateExists(workdir) {
				securityGroupName := securityGroupIdentityName("agenthub", inputs.Owner, inputs.AgentName, inputs.Environment)
				if securityGroupName != "" {
					securityGroupID, err := sgInspector.FindSecurityGroupByName(runCtx, cfg.Region.Name, securityGroupName, inputs.Owner, inputs.AgentName, inputs.Environment)
					if err != nil {
						return err
					}
					if securityGroupID != "" {
						if err := backend.Import(runCtx, workdir, "aws_security_group.this", securityGroupID); err != nil {
							return err
						}
					}
				}
			}
		}
		if err := backend.Plan(runCtx, workdir, varsPath); err != nil {
			return err
		}
		return nil
	}); err != nil {
		if workdir != "" {
			if workdir != "" && isEphemeralTerraformWorkdir(workdir) {
				defer os.RemoveAll(workdir)
			}
		}
		return nil, err
	}
	if isEphemeralTerraformWorkdir(workdir) {
		defer os.RemoveAll(workdir)
	}

	if err := runCreateStage(progress, ctx, "Infrastructure", "apply terraform", func(runCtx context.Context) error {
		if err := backend.Apply(runCtx, workdir, varsPath); err != nil {
			return err
		}
		var outErr error
		output, outErr = backend.Output(runCtx, workdir)
		if outErr != nil {
			return outErr
		}
		return nil
	}); err != nil {
		return nil, err
	}

	return infraOutputToInstance(output, inputs.NetworkMode, inputs.SSHUser, image), nil
}

func runInstallWorkflow(ctx context.Context, profile string, cfg *config.Config, opts installOptions) (runtimeinstall.Result, string, error) {
	if cfg == nil {
		return runtimeinstall.Result{}, "", errors.New("config is required")
	}
	if strings.TrimSpace(opts.Target) == "" {
		return runtimeinstall.Result{}, "", errors.New("target is required")
	}

	networkMode := effectiveNetworkMode(cfg)
	if networkMode == "private" {
		return runtimeinstall.Result{}, "", errors.New("private networking is not supported yet; install requires SSH access to the instance")
	}
	if !config.IsValidNetworkMode(networkMode) && networkMode != "" {
		return runtimeinstall.Result{}, "", fmt.Errorf("unsupported network mode %q", networkMode)
	}

	resolvedTarget, err := resolveHostTarget(ctx, profile, cfg, opts.Target)
	if err != nil {
		return runtimeinstall.Result{}, "", err
	}

	user, keyPath, err := resolveInstallSSH(cfg, opts.SSHUser, opts.SSHKey)
	if err != nil {
		return runtimeinstall.Result{}, "", err
	}
	exec := newSSHExecutor(host.SSHConfig{
		Host:           resolvedTarget,
		Port:           opts.SSHPort,
		User:           user,
		IdentityFile:   keyPath,
		ConnectTimeout: 15 * time.Second,
	})
	if err := waitForSSHReady(ctx, exec, resolvedTarget); err != nil {
		return runtimeinstall.Result{}, "", err
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
		return runtimeinstall.Result{}, "", err
	}

	inst := runtimeinstall.Installer{Host: exec}
	result, err := inst.Install(ctx, runtimeinstall.Request{
		Config:       cfg,
		UseNemoClaw:  &useNemo,
		Port:         opts.Port,
		WorkingDir:   opts.WorkingDir,
		ComputeClass: cfg.Compute.Class,
		CodexAPIKey:  codexAPIKey,
	})
	return result, resolvedTarget, err
}

func runRedeployWorkflow(ctx context.Context, profile string, cfg *config.Config, opts installOptions) (runtimeinstall.Result, verify.Report, string, error) {
	if cfg == nil {
		return runtimeinstall.Result{}, verify.Report{}, "", errors.New("config is required")
	}
	if strings.TrimSpace(opts.Target) == "" {
		return runtimeinstall.Result{}, verify.Report{}, "", errors.New("target is required")
	}

	installResult, resolvedTarget, err := runInstallWorkflow(ctx, profile, cfg, opts)
	if err != nil {
		return installResult, verify.Report{}, resolvedTarget, err
	}

	verifyReport, _, err := runVerifyWorkflow(ctx, profile, cfg, verifyOptions{
		Target:            resolvedTarget,
		SSHUser:           opts.SSHUser,
		SSHKey:            opts.SSHKey,
		SSHPort:           opts.SSHPort,
		RuntimeConfigPath: installResult.ConfigPath,
	})
	if err != nil {
		return installResult, verifyReport, resolvedTarget, err
	}
	if verifyReport.Failed() {
		return installResult, verifyReport, resolvedTarget, errors.New(fmt.Sprintf("%d required checks failed", verifyReport.RequiredFailures()))
	}
	return installResult, verifyReport, resolvedTarget, nil
}

func buildTerraformInputs(ctx context.Context, profile string, cfg *config.Config, opts createOptions) (terraformInputs, error) {
	if cfg == nil {
		return terraformInputs{}, errors.New("config is required")
	}
	switch strings.ToLower(strings.TrimSpace(cfg.Platform.Name)) {
	case config.PlatformAWS:
		// AWS remains the only platform wired for provisioning in this build.
	default:
		return terraformInputs{}, fmt.Errorf("provisioning is only wired for aws right now; platform %q is supported for configuration scaffolding only", cfg.Platform.Name)
	}

	networkMode := effectiveNetworkMode(cfg)
	if networkMode == "" {
		networkMode = "public"
	}
	if networkMode == "private" {
		return terraformInputs{}, errors.New("private networking is not supported yet; use public networking or add an SSM/bastion executor")
	}
	if !config.IsValidNetworkMode(networkMode) {
		return terraformInputs{}, fmt.Errorf("unsupported network mode %q", networkMode)
	}

	sshKeyName, sshCIDR, sshUser, sshKeyPath, err := resolveProvisioningSSH(ctx, cfg, opts)
	if err != nil {
		return terraformInputs{}, err
	}
	if strings.TrimSpace(sshKeyPath) == "" {
		return terraformInputs{}, errors.New("ssh private key path is required for public networking")
	}
	sshPublicKey, err := deriveSSHPublicKeyFunc(ctx, sshKeyPath)
	if err != nil {
		return terraformInputs{}, err
	}
	runtimePort := cfg.Runtime.Port
	if runtimePort <= 0 {
		runtimePort = 8080
	}
	owner := strings.TrimSpace(profile)
	if cfg != nil && strings.TrimSpace(cfg.Infra.AWSProfile) != "" {
		owner = strings.TrimSpace(cfg.Infra.AWSProfile)
	}
	if owner == "" {
		owner = "unknown"
	}
	agentName := strings.TrimSpace(opts.AgentName)
	if agentName == "" {
		agentName = "default"
	}
	environment := strings.TrimSpace(cfg.Infra.Environment)
	if environment == "" {
		environment = "default"
	}

	return terraformInputs{
		NetworkMode:               networkMode,
		RuntimePort:               runtimePort,
		RuntimeCIDR:               resolveRuntimeCIDR(cfg),
		RuntimeProvider:           strings.TrimSpace(cfg.Runtime.Provider),
		SSHKeyName:                sshKeyName,
		SSHPublicKey:              sshPublicKey,
		GitHubPrivateKeySecretARN: strings.TrimSpace(cfg.GitHub.PrivateKeySecretARN),
		GitHubSSHKeySecretARN:     strings.TrimSpace(cfg.GitHub.SSHKeySecretARN),
		GitHubTokenSecretARN:      strings.TrimSpace(cfg.GitHub.TokenSecretARN),
		SSHCIDR:                   sshCIDR,
		SSHUser:                   sshUser,
		SourceURL:                 "",
		Owner:                     owner,
		AgentName:                 agentName,
		Environment:               environment,
	}, nil
}

func runVerifyWorkflow(ctx context.Context, profile string, cfg *config.Config, opts verifyOptions) (verify.Report, string, error) {
	runtimeConfigPath := strings.TrimSpace(opts.RuntimeConfigPath)
	if runtimeConfigPath == "" {
		runtimeConfigPath = "/opt/agenthub/runtime.yaml"
	}

	if strings.TrimSpace(opts.Target) == "" {
		report, err := verify.Verifier{Host: newLocalExecutor()}.Verify(ctx, verify.Request{
			Config:            cfg,
			RuntimeConfigPath: runtimeConfigPath,
			TargetDescription: "local host",
		})
		return report, "local host", err
	}

	resolvedTarget, err := resolveVerifyTarget(ctx, profile, cfg, opts.Target)
	if err != nil {
		return verify.Report{}, "", err
	}

	user, keyPath, err := resolveInstallSSH(cfg, opts.SSHUser, opts.SSHKey)
	if err != nil {
		return verify.Report{}, "", err
	}
	exec := newSSHExecutor(host.SSHConfig{
		Host:           resolvedTarget,
		Port:           opts.SSHPort,
		User:           user,
		IdentityFile:   keyPath,
		ConnectTimeout: 15 * time.Second,
	})
	if err := waitForSSHReady(ctx, exec, resolvedTarget); err != nil {
		return verify.Report{}, "", err
	}

	report, err := verify.Verifier{Host: exec}.Verify(ctx, verify.Request{
		Config:            cfg,
		RuntimeConfigPath: runtimeConfigPath,
		TargetDescription: resolvedTarget,
	})
	return report, resolvedTarget, err
}

func runCreateWorkflow(ctx context.Context, profile string, cfg *config.Config, opts createOptions, progress stageRunner) (_ *provider.Instance, _ runtimeinstall.Result, _ verify.Report, err error) {
	if progress == nil {
		progress = newProgressRenderer(io.Discard)
	}

	var instance *provider.Instance
	if instance, err = runInfraCreate(ctx, profile, cfg, opts, progress); err != nil {
		return instance, runtimeinstall.Result{}, verify.Report{}, err
	}
	if instance != nil {
		defer func() {
			if err == nil {
				return
			}
			if cleanupErr := cleanupCreatedInstance(context.Background(), profile, cfg, instance); cleanupErr != nil {
				err = errors.Join(err, cleanupErr)
			}
		}()
	}

	target := instanceTarget(instance)
	installResult := runtimeinstall.Result{
		WorkingDir: "/opt/agenthub",
		ConfigPath: "/opt/agenthub/runtime.yaml",
	}
	if err = runCreateStage(progress, ctx, "Access", "waiting for bootstrap", func(runCtx context.Context) error {
		return waitForBootstrapReady(runCtx, cfg, target, opts.SSHUser, opts.SSHKey, opts.SSHPort, os.Stdout)
	}); err != nil {
		return instance, installResult, verify.Report{}, err
	}

	var resolvedTarget string
	if err = runCreateStage(progress, ctx, "Runtime", "installing runtime", func(runCtx context.Context) error {
		var err error
		installResult, resolvedTarget, err = runInstallWorkflow(runCtx, profile, cfg, installOptions{
			Target:            target,
			SSHUser:           opts.SSHUser,
			SSHKey:            opts.SSHKey,
			SSHPort:           opts.SSHPort,
			WorkingDir:        opts.WorkingDir,
			Port:              opts.Port,
			UseNemoClaw:       opts.UseNemoClaw,
			DisableNemoClaw:   opts.DisableNemoClaw,
			RuntimeConfigPath: installResult.ConfigPath,
		})
		return err
	}); err != nil {
		return instance, installResult, verify.Report{}, err
	}
	if strings.TrimSpace(resolvedTarget) == "" {
		resolvedTarget = target
	}

	var verifyReport verify.Report
	if err = runCreateStage(progress, ctx, "Runtime", "verifying runtime", func(runCtx context.Context) error {
		var err error
		verifyReport, _, err = runVerifyWorkflow(runCtx, profile, cfg, verifyOptions{
			Target:            resolvedTarget,
			SSHUser:           opts.SSHUser,
			SSHKey:            opts.SSHKey,
			SSHPort:           opts.SSHPort,
			RuntimeConfigPath: installResult.ConfigPath,
		})
		return err
	}); err != nil {
		return instance, installResult, verify.Report{}, err
	}
	return instance, installResult, verifyReport, nil
}

func cleanupCreatedInstance(ctx context.Context, profile string, cfg *config.Config, instance *provider.Instance) error {
	if cfg == nil || instance == nil {
		return nil
	}
	region := strings.TrimSpace(instance.Region)
	if region == "" {
		region = strings.TrimSpace(cfg.Region.Name)
	}
	if region == "" || strings.TrimSpace(instance.ID) == "" {
		return nil
	}
	cleanupCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	provider := newAWSProvider(profile, cfg.Compute.Class)
	if err := provider.DeleteInstance(cleanupCtx, region, instance.ID); err != nil {
		return fmt.Errorf("cleanup created instance %s: %w", instance.ID, err)
	}
	return nil
}

func waitForBootstrapReady(ctx context.Context, cfg *config.Config, target, sshUser, sshKey string, sshPort int, out io.Writer) error {
	if strings.TrimSpace(target) == "" {
		return errors.New("target is required")
	}
	user, keyPath, err := resolveInstallSSH(cfg, sshUser, sshKey)
	if err != nil {
		return err
	}
	exec := newSSHExecutor(host.SSHConfig{
		Host:           target,
		Port:           sshPort,
		User:           user,
		IdentityFile:   keyPath,
		ConnectTimeout: 15 * time.Second,
	})
	if err := waitForSSHReady(ctx, exec, target); err != nil {
		return err
	}

	waitCtx := ctx
	var cancel context.CancelFunc
	if _, ok := ctx.Deadline(); !ok {
		waitCtx, cancel = context.WithTimeout(ctx, 15*time.Minute)
		defer cancel()
	}
	delay := 2 * time.Second
	attempt := 0
	for {
		attempt++
		result, err := exec.Run(waitCtx, "test", "-f", "/opt/agenthub/bootstrap.done")
		if err == nil {
			break
		}
		msg := strings.ToLower(strings.TrimSpace(result.Stderr + " " + err.Error()))
		if attempt == 1 || attempt%3 == 0 {
			if status, statusErr := probeBootstrapStatus(waitCtx, exec); statusErr == nil {
				fmt.Fprintf(out, "bootstrap still running on %s: %s\n", target, summarizeBootstrapStatus(status))
			} else {
				fmt.Fprintf(out, "bootstrap still running on %s (status unavailable)\n", target)
			}
		}
		if waitCtx.Err() != nil {
			return fmt.Errorf("wait for bootstrap on %s: %w", target, waitCtx.Err())
		}
		if isTransientSSHError(err) {
			timer := time.NewTimer(delay)
			select {
			case <-waitCtx.Done():
				timer.Stop()
				return fmt.Errorf("wait for bootstrap on %s: %w", target, waitCtx.Err())
			case <-timer.C:
			}
			if delay < 30*time.Second {
				delay *= 2
			}
			continue
		}
		if result.ExitCode == 1 || strings.Contains(msg, "permission denied") || strings.Contains(msg, "no such file") || strings.Contains(msg, "exit status 1") || strings.Contains(msg, "exit code 1") {
			timer := time.NewTimer(delay)
			select {
			case <-waitCtx.Done():
				timer.Stop()
				return fmt.Errorf("wait for bootstrap on %s: %w", target, waitCtx.Err())
			case <-timer.C:
			}
			if delay < 30*time.Second {
				delay *= 2
			}
			continue
		}
		return fmt.Errorf("wait for bootstrap on %s: %w", target, err)
	}
	return nil
}

func probeBootstrapStatus(ctx context.Context, exec host.Executor) (string, error) {
	status, statusErr := exec.Run(ctx, "sh", "-lc", `set +e
printf 'cloud-init status:\n'
cloud-init status --long 2>&1 || cloud-init status 2>&1 || true
printf '\nbootstrap log tail:\n'
tail -n 20 /var/log/agenthub-bootstrap.log 2>&1 || true
`)
	if statusErr != nil {
		return "", statusErr
	}
	text := strings.TrimSpace(status.Stdout)
	if text == "" {
		text = strings.TrimSpace(status.Stderr)
	}
	if text == "" {
		text = "no bootstrap status available yet"
	}
	return text, nil
}

func summarizeBootstrapStatus(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return "no bootstrap status available yet"
	}

	lines := strings.Split(text, "\n")
	parts := make([]string, 0, 3)
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		switch {
		case strings.HasPrefix(line, "cloud-init status:"):
			continue
		case strings.HasPrefix(line, "bootstrap log tail:"):
			continue
		case strings.HasPrefix(line, "tail: cannot open"):
			parts = append(parts, "bootstrap log unavailable")
		default:
			parts = append(parts, line)
		}
		if len(parts) >= 3 {
			break
		}
	}
	if len(parts) == 0 {
		return "no bootstrap status available yet"
	}
	summary := strings.Join(parts, "; ")
	const maxLen = 180
	if len(summary) > maxLen {
		return summary[:maxLen-1] + "…"
	}
	return summary
}

func resolveCodexAPIKey(ctx context.Context, profile string, cfg *config.Config) (string, error) {
	if cfg == nil || strings.ToLower(strings.TrimSpace(cfg.Runtime.Provider)) != "codex" {
		return "", nil
	}
	secretID := strings.TrimSpace(cfg.Runtime.Codex.SecretID)
	if secretID == "" {
		return "", nil
	}
	return codexauth.LoadAPIKeyFunc(ctx, profile, cfg.Region.Name, secretID)
}

func resolveHostTarget(ctx context.Context, profile string, cfg *config.Config, target string) (string, error) {
	if strings.HasPrefix(strings.TrimSpace(target), "i-") {
		prov := newAWSProvider(profile, "")
		regions := []string{}
		if cfg != nil && strings.TrimSpace(cfg.Region.Name) != "" {
			regions = append(regions, strings.TrimSpace(cfg.Region.Name))
		} else {
			listedRegions, err := prov.ListRegions(ctx)
			if err != nil {
				return "", err
			}
			regions = append(regions, listedRegions...)
		}
		for _, region := range regions {
			instance, err := prov.GetInstance(ctx, region, target)
			if err != nil {
				continue
			}
			if strings.TrimSpace(instance.PublicIP) != "" {
				return instance.PublicIP, nil
			}
			if strings.TrimSpace(instance.PrivateIP) != "" {
				return instance.PrivateIP, nil
			}
		}
		if fallback := hostFromRuntimeURL(cfg); fallback != "" {
			return fallback, nil
		}
		return "", fmt.Errorf("instance %s does not expose an SSH-reachable address", target)
	}
	return target, nil
}

func hostFromRuntimeURL(cfg *config.Config) string {
	if cfg == nil {
		return ""
	}
	raw := strings.TrimSpace(cfg.Slack.RuntimeURL)
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	host := strings.TrimSpace(parsed.Hostname())
	if host != "" {
		return host
	}
	return strings.TrimSpace(parsed.Host)
}

func resolveVerifyTarget(ctx context.Context, profile string, cfg *config.Config, target string) (string, error) {
	return resolveHostTarget(ctx, profile, cfg, target)
}

func effectiveNetworkMode(cfg *config.Config) string {
	if cfg == nil {
		return ""
	}
	if mode := strings.TrimSpace(cfg.Instance.NetworkMode); mode != "" {
		return strings.ToLower(mode)
	}
	return strings.ToLower(strings.TrimSpace(cfg.Sandbox.NetworkMode))
}

func resolveProvisioningSSH(ctx context.Context, cfg *config.Config, opts createOptions) (string, string, string, string, error) {
	if cfg == nil {
		return "", "", "", "", errors.New("config is required")
	}

	sshKeyName := strings.TrimSpace(opts.SSHKeyName)
	if sshKeyName == "" {
		sshKeyName = strings.TrimSpace(cfg.SSH.KeyName)
	}
	if sshKeyName == "" {
		sshKeyName = defaultSSHKeyName()
	}
	sshCIDR := strings.TrimSpace(opts.SSHCIDR)
	if sshCIDR == "" {
		sshCIDR = strings.TrimSpace(cfg.SSH.CIDR)
	}
	if sshCIDR == "" {
		return "", "", "", "", errors.New("ssh cidr is required for public networking; run `agenthub init` or pass --ssh-cidr")
	}
	sshUser := strings.TrimSpace(opts.SSHUser)
	if sshUser == "" {
		sshUser = strings.TrimSpace(cfg.SSH.User)
	}
	if sshUser == "" {
		sshUser = sshUsernameForImage(cfg.Image.Name, cfg.Image.ID)
	}
	sshKeyPath := strings.TrimSpace(opts.SSHKey)
	if sshKeyPath == "" {
		sshKeyPath = strings.TrimSpace(cfg.SSH.PrivateKeyPath)
	}
	if sshKeyPath == "" {
		sshKeyPath = defaultSSHPrivateKeyPath()
	}

	return sshKeyName, sshCIDR, sshUser, sshKeyPath, nil
}

func resolveInstallSSH(cfg *config.Config, userFlag, keyFlag string) (string, string, error) {
	if cfg == nil {
		return "", "", errors.New("config is required")
	}
	user := strings.TrimSpace(userFlag)
	if user == "" {
		user = strings.TrimSpace(cfg.SSH.User)
	}
	if user == "" {
		user = sshUsernameForImage(cfg.Image.Name, cfg.Image.ID)
	}
	keyPath := strings.TrimSpace(keyFlag)
	if keyPath == "" {
		keyPath = strings.TrimSpace(cfg.SSH.PrivateKeyPath)
	}
	if keyPath == "" {
		keyPath = defaultSSHPrivateKeyPath()
	}
	resolved, err := resolveSSHPrivateKeyPath(keyPath)
	if err != nil {
		return "", "", err
	}
	if _, err := os.Stat(resolved); err != nil {
		return "", "", fmt.Errorf("ssh private key %q does not exist; pass --ssh-key or update ssh.private_key_path", resolved)
	}
	return user, resolved, nil
}

func waitForSSHReady(ctx context.Context, exec host.Executor, target string) error {
	waitCtx := ctx
	var cancel context.CancelFunc
	if _, ok := ctx.Deadline(); !ok {
		waitCtx, cancel = context.WithTimeout(ctx, defaultSSHReadyTimeout)
		defer cancel()
	}

	startedAt := time.Now()
	delay := defaultSSHReadyInitialWait
	attempts := 0
	var lastErr error
	for {
		attempts++
		_, err := exec.Run(waitCtx, "true")
		if err == nil {
			return nil
		}
		lastErr = err
		if !isTransientSSHError(err) {
			return fmt.Errorf("wait for ssh readiness on %s: %w", target, err)
		}
		if waitCtx.Err() != nil {
			return formatSSHReadyTimeoutError(target, startedAt, attempts, lastErr, waitCtx.Err())
		}

		timer := time.NewTimer(delay)
		select {
		case <-waitCtx.Done():
			timer.Stop()
			return formatSSHReadyTimeoutError(target, startedAt, attempts, lastErr, waitCtx.Err())
		case <-timer.C:
		}

		delay *= 2
		if delay > defaultSSHReadyMaxWait {
			delay = defaultSSHReadyMaxWait
		}
	}
}

func formatSSHReadyTimeoutError(target string, startedAt time.Time, attempts int, lastErr, ctxErr error) error {
	elapsed := time.Since(startedAt).Round(time.Second)
	if elapsed <= 0 {
		elapsed = time.Second
	}
	base := fmt.Sprintf("wait for ssh readiness on %s after %s (%d attempts)", target, elapsed, attempts)
	switch {
	case lastErr != nil && ctxErr != nil:
		return fmt.Errorf("%s: %w", base, errors.Join(ctxErr, lastErr))
	case lastErr != nil:
		return fmt.Errorf("%s: %w", base, lastErr)
	case ctxErr != nil:
		return fmt.Errorf("%s: %w", base, ctxErr)
	default:
		return fmt.Errorf("%s", base)
	}
}

func isTransientSSHError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, fragment := range []string{
		"connection refused",
		"connection timed out",
		"operation timed out",
		"no route to host",
		"network is unreachable",
	} {
		if strings.Contains(msg, fragment) {
			return true
		}
	}
	return false
}

func prepareTerraformWorkdir(configPath string) (string, error) {
	if path := terraformWorkspacePath(configPath); path != "" {
		if err := os.MkdirAll(path, 0o755); err != nil {
			return "", fmt.Errorf("prepare terraform workspace %q: %w", path, err)
		}
		return path, nil
	}
	workdir, err := os.MkdirTemp("", "agenthub-terraform-*")
	if err != nil {
		return "", fmt.Errorf("create terraform workspace: %w", err)
	}
	return workdir, nil
}

func terraformWorkspacePath(configPath string) string {
	configPath = strings.TrimSpace(configPath)
	if configPath == "" {
		return ""
	}
	dir := filepath.Dir(configPath)
	if strings.TrimSpace(dir) == "" || dir == "." {
		return ""
	}
	return filepath.Join(dir, ".agenthub", "terraform")
}

func isEphemeralTerraformWorkdir(workdir string) bool {
	workdir = strings.TrimSpace(workdir)
	return workdir != "" && strings.Contains(filepath.Base(workdir), "agenthub-terraform-")
}

func terraformStateExists(workdir string) bool {
	workdir = strings.TrimSpace(workdir)
	if workdir == "" {
		return false
	}
	info, err := os.Stat(filepath.Join(workdir, "terraform.tfstate"))
	return err == nil && !info.IsDir()
}

func resolveTerraformModuleDir(cfg *config.Config) (string, error) {
	if cfg == nil {
		return "", errors.New("config is required")
	}
	moduleDir := strings.TrimSpace(cfg.Infra.ModuleDir)
	if moduleDir == "" {
		moduleDir = defaultTerraformModuleDirForPlatform(cfg.Platform.Name)
		if moduleDir == "" {
			return "", fmt.Errorf("unsupported platform %q", cfg.Platform.Name)
		}
	}
	if !filepath.IsAbs(moduleDir) {
		abs, err := filepath.Abs(moduleDir)
		if err != nil {
			return "", fmt.Errorf("resolve terraform module dir %q: %w", moduleDir, err)
		}
		moduleDir = abs
	}
	return moduleDir, nil
}

func defaultTerraformModuleDirForPlatform(platform string) string {
	switch strings.ToLower(strings.TrimSpace(platform)) {
	case config.PlatformAWS:
		return filepath.Join("infra", "aws", "ec2")
	case config.PlatformGCP:
		return filepath.Join("infra", "gcp", "vm")
	case config.PlatformAzure:
		return filepath.Join("infra", "azure", "vm")
	default:
		return ""
	}
}

func writeTerraformVars(workdir string, vars terraformVars) (string, error) {
	data, err := json.MarshalIndent(vars, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal terraform vars: %w", err)
	}
	path := filepath.Join(workdir, "agenthub.auto.tfvars.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", fmt.Errorf("write terraform vars: %w", err)
	}
	return path, nil
}

func securityGroupIdentityName(prefix, owner, agentName, environment string) string {
	parts := []string{
		normalizeIdentitySegment(prefix),
		normalizeIdentitySegment(owner),
		normalizeIdentitySegment(agentName),
		normalizeIdentitySegment(environment),
		"sg",
	}
	filtered := make([]string, 0, len(parts))
	for _, part := range parts {
		if strings.TrimSpace(part) != "" {
			filtered = append(filtered, part)
		}
	}
	return strings.Join(filtered, "-")
}

var newTerraformBackend = func(profile string, cfg *config.Config) (infratf.InfraBackend, error) {
	if cfg == nil {
		return nil, errors.New("config is required")
	}
	moduleDir, err := resolveTerraformModuleDir(cfg)
	if err != nil {
		return nil, err
	}
	backend := infratf.New(moduleDir)
	backend.Profile = strings.TrimSpace(profile)
	backend.Region = strings.TrimSpace(cfg.Region.Name)
	return backend, nil
}

func infraOutputToInstance(output *infratf.InfraOutput, networkMode, sshUser string, image provider.BaseImage) *provider.Instance {
	if output == nil {
		return nil
	}
	instance := &provider.Instance{
		ID:                 strings.TrimSpace(output.InstanceID),
		Name:               strings.TrimSpace(output.InstanceName),
		Owner:              strings.TrimSpace(output.Owner),
		AgentName:          strings.TrimSpace(output.AgentName),
		Environment:        strings.TrimSpace(output.Environment),
		TrackingID:         strings.TrimSpace(output.TrackingID),
		Region:             strings.TrimSpace(output.Region),
		PublicIP:           strings.TrimSpace(output.PublicIP),
		PrivateIP:          strings.TrimSpace(output.PrivateIP),
		ConnectionInfo:     strings.TrimSpace(output.ConnectionInfo),
		SecurityGroupID:    strings.TrimSpace(output.SecurityGroupID),
		SecurityGroupRules: append([]string(nil), output.SecurityGroupRules...),
	}
	if instance.ConnectionInfo == "" {
		if instance.PublicIP != "" && strings.EqualFold(networkMode, "public") {
			instance.ConnectionInfo = fmt.Sprintf("ssh -i <your-key>.pem %s@%s", sshUser, instance.PublicIP)
		} else if instance.PrivateIP != "" {
			instance.ConnectionInfo = fmt.Sprintf("private IP access: %s", instance.PrivateIP)
		}
	}
	if instance.ConnectionInfo == "" && image.ID != "" {
		instance.ConnectionInfo = fmt.Sprintf("instance ready for %s", image.Name)
	}
	if instance.Name == "" {
		instance.Name = instanceIdentityName(instance.Owner, instance.AgentName, instance.Environment, instance.TrackingID)
	}
	if instance.Name == "" {
		instance.Name = instance.ID
	}
	return instance
}

func instanceTarget(instance *provider.Instance) string {
	if instance == nil {
		return ""
	}
	if strings.TrimSpace(instance.PublicIP) != "" {
		return strings.TrimSpace(instance.PublicIP)
	}
	return strings.TrimSpace(instance.PrivateIP)
}

func printWorkflowSuccess(out io.Writer, instance *provider.Instance, installResult runtimeinstall.Result, verifyReport verify.Report, cfgPath string, cfg *config.Config, target string, elapsed time.Duration, createMode bool) {
	if elapsed > 0 {
		fmt.Fprintf(out, "Provisioning complete in %s\n\n", formatProgressDuration(elapsed))
	}
	fmt.Fprintln(out, "Created")
	if instance != nil {
		printCreatedInstance(out, instance)
	} else {
		fmt.Fprintln(out, "- instance: not available")
	}
	if strings.TrimSpace(target) != "" {
		fmt.Fprintf(out, "- connection target: %s\n", target)
	}
	if url := runtimeHealthURL(instance, cfg); strings.TrimSpace(url) != "" {
		fmt.Fprintf(out, "- health url: %s\n", url)
	}
	if url := runtimeInvokeURL(instance, cfg); strings.TrimSpace(url) != "" {
		fmt.Fprintf(out, "- invoke url: %s\n", url)
	}
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Configured")
	if strings.TrimSpace(installResult.WorkingDir) != "" {
		fmt.Fprintf(out, "- working directory: %s\n", installResult.WorkingDir)
	}
	if strings.TrimSpace(installResult.ConfigPath) != "" {
		fmt.Fprintf(out, "- runtime config: %s\n", installResult.ConfigPath)
	}
	if len(verifyReport.Checks) > 0 {
		fmt.Fprintln(out, "- verification summary:")
		printVerificationReport(out, verifyReport)
	}
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Next")
	if strings.TrimSpace(cfgPath) != "" && strings.TrimSpace(target) != "" {
		fmt.Fprintf(out, "- verify: %s\n", commandRef(out, "agenthub", "verify", "--config", cfgPath, "--target", target))
	}
	if createMode && strings.TrimSpace(cfgPath) != "" && strings.TrimSpace(target) != "" && strings.TrimSpace(installResult.ServicePath) != "" {
		fmt.Fprintf(out, "- install: %s\n", commandRef(out, "agenthub", "install", "--config", cfgPath, "--target", target))
	}
	if createMode && cfg != nil && strings.EqualFold(strings.TrimSpace(cfg.Runtime.Provider), "codex") && strings.TrimSpace(cfgPath) != "" {
		if strings.TrimSpace(cfg.Infra.InstanceID) != "" {
			fmt.Fprintf(out, "- slack deploy: %s\n", commandRef(out, "agenthub", "slack", "deploy", "--config", cfgPath))
		} else if strings.TrimSpace(target) != "" {
			fmt.Fprintf(out, "- slack deploy: %s\n", commandRef(out, "agenthub", "slack", "deploy", "--config", cfgPath, "--target", target))
		}
	}
	if createMode {
		fmt.Fprintf(out, "- destroy: %s\n", commandRef(out, "agenthub", "infra", "destroy", "--config", cfgPath))
	}
	fmt.Fprintln(out, "- keep the runtime config and SSH target handy for future verify runs")
}

func instanceIdentityName(owner, agentName, environment, trackingID string) string {
	parts := []string{
		normalizeIdentitySegment("agenthub"),
		normalizeIdentitySegment(owner),
		normalizeIdentitySegment(agentName),
		normalizeIdentitySegment(environment),
		normalizeIdentitySegment(trackingID),
	}
	filtered := make([]string, 0, len(parts))
	for _, part := range parts {
		if strings.TrimSpace(part) != "" {
			filtered = append(filtered, part)
		}
	}
	return strings.Join(filtered, "-")
}

func normalizeIdentitySegment(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return ""
	}
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		case r == '-' || r == '_':
			if !lastDash && b.Len() > 0 {
				b.WriteByte('-')
				lastDash = true
			}
		default:
			if !lastDash && b.Len() > 0 {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	for strings.Contains(out, "--") {
		out = strings.ReplaceAll(out, "--", "-")
	}
	return out
}

func runtimeBaseURL(instance *provider.Instance, cfg *config.Config) string {
	if instance == nil {
		return ""
	}
	host := strings.TrimSpace(instance.PublicIP)
	if host == "" {
		return ""
	}
	port := 8080
	if cfg != nil && cfg.Runtime.Port > 0 {
		port = cfg.Runtime.Port
	}
	return fmt.Sprintf("http://%s:%d", host, port)
}

func runtimeHealthURL(instance *provider.Instance, cfg *config.Config) string {
	if instance == nil {
		return ""
	}
	host := strings.TrimSpace(instance.PublicIP)
	if host == "" {
		return ""
	}
	port := 8080
	if cfg != nil && cfg.Runtime.Port > 0 {
		port = cfg.Runtime.Port
	}
	return fmt.Sprintf("http://%s:%d/healthz", host, port)
}

func runtimeInvokeURL(instance *provider.Instance, cfg *config.Config) string {
	if instance == nil || cfg == nil {
		return ""
	}
	if !strings.EqualFold(strings.TrimSpace(cfg.Runtime.Provider), "aws-bedrock") {
		return ""
	}
	host := strings.TrimSpace(instance.PublicIP)
	if host == "" {
		return ""
	}
	port := 8080
	if cfg.Runtime.Port > 0 {
		port = cfg.Runtime.Port
	}
	return fmt.Sprintf("http://%s:%d/v1/generate", host, port)
}

func printVerificationReport(out io.Writer, report verify.Report) {
	fmt.Fprintln(out, "verification summary")
	for _, check := range report.Checks {
		status := "PASS"
		switch {
		case check.Skipped:
			status = "SKIP"
		case !check.Passed:
			status = "FAIL"
		}
		fmt.Fprintf(out, "- %s: %s\n", check.Name, status)
		if strings.TrimSpace(check.Message) != "" {
			fmt.Fprintf(out, "  %s\n", check.Message)
		}
		if !check.Passed && strings.TrimSpace(check.Remediation) != "" {
			fmt.Fprintf(out, "  remediation: %s\n", check.Remediation)
		}
	}
	if report.Failed() {
		fmt.Fprintf(out, "required checks failed: %d\n", report.RequiredFailures())
	} else {
		fmt.Fprintln(out, "all required checks passed")
	}
}

func printSuccessNextSteps(out io.Writer, cfgPath, target string, includeInstall bool) {
	fmt.Fprintln(out, "next steps")
	if strings.TrimSpace(target) != "" && strings.TrimSpace(cfgPath) != "" {
		fmt.Fprintf(out, "- verify: %s\n", commandRef(out, "agenthub", "verify", "--config", cfgPath, "--target", target))
	}
	if includeInstall && strings.TrimSpace(target) != "" && strings.TrimSpace(cfgPath) != "" {
		fmt.Fprintf(out, "- install: %s\n", commandRef(out, "agenthub", "install", "--config", cfgPath, "--target", target))
	}
	if strings.TrimSpace(cfgPath) != "" {
		fmt.Fprintf(out, "- destroy: %s\n", commandRef(out, "agenthub", "infra", "destroy", "--config", cfgPath))
	}
}

func wrapUserFacingError(action string, err error, likelyCause string, nextSteps ...string) error {
	if err == nil {
		return nil
	}
	return &userFacingError{
		Action:      action,
		Cause:       err,
		LikelyCause: likelyCause,
		NextSteps:   append([]string(nil), nextSteps...),
	}
}

type userFacingError struct {
	Action      string
	Cause       error
	LikelyCause string
	NextSteps   []string
}

func (e *userFacingError) Error() string {
	if e == nil {
		return ""
	}
	var b strings.Builder
	if strings.TrimSpace(e.Action) != "" {
		b.WriteString(e.Action)
	}
	if e.Cause != nil {
		if b.Len() > 0 {
			b.WriteString(": ")
		}
		b.WriteString(e.Cause.Error())
	}
	if strings.TrimSpace(e.LikelyCause) != "" {
		b.WriteString("\nlikely cause: ")
		b.WriteString(strings.TrimSpace(e.LikelyCause))
	}
	if len(e.NextSteps) > 0 {
		b.WriteString("\nnext steps:")
		for _, step := range e.NextSteps {
			if strings.TrimSpace(step) == "" {
				continue
			}
			b.WriteString("\n- ")
			b.WriteString(strings.TrimSpace(step))
		}
	}
	return b.String()
}

func (e *userFacingError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}
