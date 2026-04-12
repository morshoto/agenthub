package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"agenthub/internal/config"
	"agenthub/internal/prompt"
)

func newInfraDestroyCommand(app *App) *cobra.Command {
	var force bool

	cmd := &cobra.Command{
		Use:   "destroy",
		Short: "Destroy cloud infrastructure with Terraform",
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(app.opts.ConfigPath) == "" {
				return errors.New("config file is required: pass --config <path>")
			}

			cfg, err := config.Load(app.opts.ConfigPath)
			if err != nil {
				return err
			}
			if err := validateInfraConfig(cfg); err != nil {
				return err
			}

			profile, err := selectAWSProfile(cmd.Context(), cmd.InOrStdin(), cmd.OutOrStdout(), firstNonEmpty(app.opts.Profile, cfg.Infra.AWSProfile))
			if err != nil {
				return err
			}

			agentName := agentNameFromConfigPath(app.opts.ConfigPath)
			if agentName == "" {
				agentName = "default"
			}

			printInfraDestroySummary(cmd.OutOrStdout(), app.opts.ConfigPath, cfg, agentName)

			session := prompt.NewSession(cmd.InOrStdin(), cmd.OutOrStdout())
			session.Interactive = detectInteractiveInput(cmd.InOrStdin())
			if !force {
				if !session.Interactive {
					return errors.New("destroy requires confirmation; rerun with --force in non-interactive mode")
				}
				confirmed, err := session.Confirm("Destroy this cloud infrastructure", false)
				if err != nil {
					return err
				}
				if !confirmed {
					fmt.Fprintln(cmd.OutOrStdout(), "destroy cancelled")
					return nil
				}
			}

			logger := loggerFromContext(cmd.Context())
			logger.Info("starting infra destroy")
			fmt.Fprintln(cmd.OutOrStdout(), "destroying infrastructure with Terraform...")
			if err := runInfraDestroy(cmd.Context(), profile, cfg, createOptions{ConfigPath: app.opts.ConfigPath, AgentName: agentName}); err != nil {
				return wrapUserFacingError(
					"infra destroy failed",
					err,
					"the selected cloud provider rejected the destroy request, the Terraform inputs no longer match the deployed stack, or credentials are unavailable",
					"confirm the selected config and AWS profile still point at the deployed environment",
					"if the profile uses AWS SSO, run "+commandRef(cmd.OutOrStdout(), "aws", "sso", "login", "--profile", profile)+" from a local terminal with browser access and retry",
				)
			}

			clearDestroyedInfraState(cfg)
			if err := config.Save(app.opts.ConfigPath, cfg); err != nil {
				return fmt.Errorf("destroyed infrastructure but failed to update config %q: %w", app.opts.ConfigPath, err)
			}

			fmt.Fprintln(cmd.OutOrStdout(), "destroyed infrastructure")
			fmt.Fprintf(cmd.OutOrStdout(), "config updated: %s\n", app.opts.ConfigPath)
			return nil
		},
	}

	cmd.Flags().BoolVar(&force, "force", false, "destroy without interactive confirmation")
	return cmd
}

func runInfraDestroy(ctx context.Context, profile string, cfg *config.Config, opts createOptions) error {
	if cfg == nil {
		return errors.New("config is required")
	}

	inputs, err := buildTerraformInputs(ctx, profile, cfg, opts)
	if err != nil {
		return err
	}

	workdir, err := prepareTerraformWorkdir(opts.ConfigPath)
	if err != nil {
		return err
	}

	backend, err := newTerraformBackend(profile, cfg)
	if err != nil {
		return err
	}
	if err := backend.Init(ctx, workdir); err != nil {
		return err
	}
	varsPath, err := writeTerraformVars(workdir, terraformVars{
		Region:                    cfg.Region.Name,
		ComputeClass:              config.EffectiveComputeClass(cfg.Compute.Class),
		Owner:                     inputs.Owner,
		AgentName:                 inputs.AgentName,
		Environment:               inputs.Environment,
		InstanceType:              strings.TrimSpace(cfg.Instance.Type),
		DiskSizeGB:                cfg.Instance.DiskSizeGB,
		NetworkMode:               inputs.NetworkMode,
		ImageName:                 strings.TrimSpace(cfg.Image.Name),
		ImageID:                   strings.TrimSpace(cfg.Image.ID),
		RuntimePort:               inputs.RuntimePort,
		RuntimeCIDR:               inputs.RuntimeCIDR,
		RuntimeProvider:           inputs.RuntimeProvider,
		SSHKeyName:                inputs.SSHKeyName,
		SSHPublicKey:              inputs.SSHPublicKey,
		GitHubPrivateKeySecretARN: inputs.GitHubPrivateKeySecretARN,
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
	if err := backend.Destroy(ctx, workdir, filepath.Base(varsPath)); err != nil {
		return err
	}
	if !isEphemeralTerraformWorkdir(workdir) {
		if err := os.RemoveAll(workdir); err != nil {
			return fmt.Errorf("remove terraform workspace %q: %w", workdir, err)
		}
	}
	return nil
}

func printInfraDestroySummary(out io.Writer, cfgPath string, cfg *config.Config, agentName string) {
	fmt.Fprintln(out, "will destroy")
	fmt.Fprintf(out, "- agent: %s\n", firstNonEmpty(strings.TrimSpace(agentName), "default"))
	if strings.TrimSpace(cfgPath) != "" {
		fmt.Fprintf(out, "- config: %s\n", cfgPath)
	}
	if cfg == nil {
		return
	}
	if platform := strings.TrimSpace(cfg.Platform.Name); platform != "" {
		fmt.Fprintf(out, "- platform: %s\n", platform)
	}
	if moduleDir, err := resolveTerraformModuleDir(cfg); err == nil && strings.TrimSpace(moduleDir) != "" {
		fmt.Fprintf(out, "- terraform module: %s\n", moduleDir)
	}
	if region := strings.TrimSpace(cfg.Region.Name); region != "" {
		fmt.Fprintf(out, "- region: %s\n", region)
	}
	if instanceID := strings.TrimSpace(cfg.Infra.InstanceID); instanceID != "" {
		fmt.Fprintf(out, "- recorded instance id: %s\n", instanceID)
	}
	if runtimeURL := strings.TrimSpace(cfg.Slack.RuntimeURL); runtimeURL != "" {
		fmt.Fprintf(out, "- recorded runtime url: %s\n", runtimeURL)
	}
}

func clearDestroyedInfraState(cfg *config.Config) {
	if cfg == nil {
		return
	}
	cfg.Infra.InstanceID = ""
	cfg.Slack.RuntimeURL = ""
}
