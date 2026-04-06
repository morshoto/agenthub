package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"agenthub/internal/config"
	"agenthub/internal/provider"
)

func newInfraCommand(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "infra",
		Short:   "Provision infrastructure",
		GroupID: "provision",
	}
	cmd.AddCommand(newInfraCreateCommand(app))
	cmd.AddCommand(newInfraTFVarsCommand(app))
	return cmd
}

func newInfraCreateCommand(app *App) *cobra.Command {
	var sshKeyName string
	var sshCIDR string

	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create AWS infrastructure with Terraform",
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
			effectiveSSHKeyName := firstNonEmpty(sshKeyName, cfg.SSH.KeyName)
			effectiveSSHCIDR := firstNonEmpty(sshCIDR, cfg.SSH.CIDR)
			if err := validateInfraCreateFlags(cfg, effectiveSSHKeyName, effectiveSSHCIDR); err != nil {
				return err
			}

			logger := loggerFromContext(cmd.Context())
			logger.Info("starting infra create")
			fmt.Fprintln(cmd.OutOrStdout(), "creating infrastructure with Terraform...")
			agentName := agentNameFromConfigPath(app.opts.ConfigPath)
			if agentName == "" {
				agentName = "default"
			}
			instance, err := runInfraCreate(cmd.Context(), app.opts.Profile, cfg, createOptions{
				SSHKeyName: effectiveSSHKeyName,
				SSHCIDR:    effectiveSSHCIDR,
				AgentName:  agentName,
			}, nil)
			printCreatedInstance(cmd.OutOrStdout(), instance)
			if instance != nil {
				printSuccessNextSteps(cmd.OutOrStdout(), app.opts.ConfigPath, instanceTarget(instance), true)
			}
			if err != nil {
				return wrapUserFacingError(
					"infra create failed",
					err,
					"the AWS provider rejected the request or the selected region lacks capacity",
					"check the AWS error above",
					"run "+commandRef(cmd.OutOrStdout(), "agenthub", "quota", "check", "--platform", "aws", "--region", cfg.Region.Name, "--instance-family", cfg.Instance.Type)+" before retrying",
				)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&sshKeyName, "ssh-key-name", "", "SSH key pair name to attach to the instance")
	cmd.Flags().StringVar(&sshCIDR, "ssh-cidr", "", "CIDR allowed to reach port 22; auto-detected from your public IP when omitted")
	return cmd
}

func validateInfraConfig(cfg *config.Config) error {
	if cfg == nil {
		return errors.New("config validation failed: config is nil")
	}

	var v config.ValidationError
	platform := strings.ToLower(strings.TrimSpace(cfg.Platform.Name))
	if platform == "" {
		v.Add("platform.name", "is required")
	} else if !config.IsSupportedPlatform(platform) {
		v.Add("platform.name", fmt.Sprintf("unsupported platform %q", cfg.Platform.Name))
	}
	if platform == "" {
		platform = config.PlatformAWS
	}
	if strings.TrimSpace(cfg.Region.Name) == "" {
		v.Add("region.name", "is required")
	}
	if class := strings.TrimSpace(cfg.Compute.Class); class != "" && !config.IsValidComputeClass(class) {
		v.Add("compute.class", fmt.Sprintf("unsupported compute class %q", class))
	}
	if strings.TrimSpace(cfg.Instance.Type) == "" {
		v.Add("instance.type", "is required")
	}
	if cfg.Instance.DiskSizeGB <= 0 {
		v.Add("instance.disk_size_gb", "must be greater than 0")
	}
	if strings.TrimSpace(cfg.Image.ID) == "" && strings.TrimSpace(cfg.Image.Name) == "" {
		v.Add("image.name", "or image.id is required")
	}
	if mode := config.EffectiveNetworkMode(cfg); mode != "" && mode != "public" && mode != "private" {
		v.Add("sandbox.network_mode", "must be public or private")
	}
	if cfg.Infra.Backend != "" && strings.ToLower(strings.TrimSpace(cfg.Infra.Backend)) != "terraform" {
		v.Add("infra.backend", "must be terraform")
	}
	return v.OrNil()
}

func validateInfraCreateFlags(cfg *config.Config, sshKeyName, sshCIDR string) error {
	sshKeyName = strings.TrimSpace(sshKeyName)
	sshCIDR = strings.TrimSpace(sshCIDR)
	networkMode := config.EffectiveNetworkMode(cfg)
	switch {
	case networkMode == "private":
		return errors.New("private networking is not supported yet; use public networking or add an SSM/bastion executor")
	default:
		return nil
	}
}

func resolveInfraImage(ctx context.Context, prov provider.CloudProvider, cfg *config.Config) (provider.BaseImage, error) {
	if cfg == nil {
		return provider.BaseImage{}, errors.New("config is nil")
	}
	if imageID := strings.TrimSpace(cfg.Image.ID); imageID != "" {
		return provider.BaseImage{
			ID:   imageID,
			Name: cfg.Image.Name,
		}, nil
	}

	imageName := strings.TrimSpace(cfg.Image.Name)
	if imageName == "" {
		return provider.BaseImage{}, errors.New("image name or image id is required")
	}
	if prov == nil {
		return provider.BaseImage{}, fmt.Errorf("resolve image %q: provider is unavailable", imageName)
	}

	images, err := prov.RecommendBaseImages(ctx, cfg.Region.Name, cfg.Compute.Class)
	if err != nil {
		return provider.BaseImage{}, fmt.Errorf("resolve image %q: %w", imageName, err)
	}
	if len(images) == 0 {
		return provider.BaseImage{}, fmt.Errorf("resolve image %q: no base images available", imageName)
	}
	if len(images) == 1 {
		return images[0], nil
	}

	for _, image := range images {
		if strings.EqualFold(strings.TrimSpace(image.Name), imageName) || strings.EqualFold(strings.TrimSpace(image.ID), imageName) {
			return image, nil
		}
	}
	return provider.BaseImage{}, fmt.Errorf("resolve image %q: no matching base image found", imageName)
}

func printCreatedInstance(out io.Writer, instance *provider.Instance) {
	if instance == nil {
		fmt.Fprintln(out, "instance created")
		return
	}
	if strings.TrimSpace(instance.Name) != "" {
		fmt.Fprintf(out, "instance name: %s\n", instance.Name)
	}
	fmt.Fprintf(out, "instance id: %s\n", instance.ID)
	if strings.TrimSpace(instance.Owner) != "" {
		fmt.Fprintf(out, "owner: %s\n", instance.Owner)
	}
	if strings.TrimSpace(instance.AgentName) != "" {
		fmt.Fprintf(out, "agent: %s\n", instance.AgentName)
	}
	if strings.TrimSpace(instance.Environment) != "" {
		fmt.Fprintf(out, "environment: %s\n", instance.Environment)
	}
	if strings.TrimSpace(instance.TrackingID) != "" {
		fmt.Fprintf(out, "tracking id: %s\n", instance.TrackingID)
	}
	if strings.TrimSpace(instance.Region) != "" {
		fmt.Fprintf(out, "region: %s\n", instance.Region)
	}
	if strings.TrimSpace(instance.PublicIP) != "" {
		fmt.Fprintf(out, "public ip: %s\n", instance.PublicIP)
	}
	if strings.TrimSpace(instance.PrivateIP) != "" {
		fmt.Fprintf(out, "private ip: %s\n", instance.PrivateIP)
	}
	if strings.TrimSpace(instance.ConnectionInfo) != "" {
		fmt.Fprintf(out, "connection: %s\n", instance.ConnectionInfo)
	}
	if strings.TrimSpace(instance.SecurityGroupID) != "" {
		fmt.Fprintf(out, "security group: %s\n", instance.SecurityGroupID)
	}
	if len(instance.SecurityGroupRules) > 0 {
		fmt.Fprintln(out, "security group rules:")
		for _, rule := range instance.SecurityGroupRules {
			fmt.Fprintf(out, "  - %s\n", rule)
		}
	}
}
