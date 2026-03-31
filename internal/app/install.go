package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"openclaw/internal/config"
	"openclaw/internal/host"
	"openclaw/internal/runtimeinstall"
)

var newSSHExecutor = func(cfg host.SSHConfig) host.Executor {
	return host.NewSSHExecutor(cfg)
}

func newInstallCommand(app *App) *cobra.Command {
	var target string
	var sshUser string
	var sshKey string
	var sshPort int
	var workingDir string
	var port int
	var useNemoClaw bool
	var disableNemoClaw bool

	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install the OpenClaw runtime on a prepared host",
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(app.opts.ConfigPath) == "" {
				return errors.New("config file is required: pass --config <path>")
			}
			cfg, err := config.Load(app.opts.ConfigPath)
			if err != nil {
				return err
			}
			if err := config.Validate(cfg); err != nil {
				return err
			}
			if strings.TrimSpace(target) == "" {
				return errors.New("target is required: pass --target <instance-id-or-host>")
			}

			resolvedTarget, err := resolveInstallTarget(cmd.Context(), app.opts.Profile, cfg, target)
			if err != nil {
				return err
			}

			user := strings.TrimSpace(sshUser)
			if user == "" {
				user = sshUsernameForImage(cfg.Image.Name, cfg.Image.ID)
			}
			exec := newSSHExecutor(host.SSHConfig{
				Host:           resolvedTarget,
				Port:           sshPort,
				User:           user,
				IdentityFile:   strings.TrimSpace(sshKey),
				ConnectTimeout: 15 * time.Second,
			})

			useNemo := cfg.Sandbox.UseNemoClaw
			if useNemoClaw {
				useNemo = true
			}
			if disableNemoClaw {
				useNemo = false
			}

			inst := runtimeinstall.Installer{Host: exec}
			result, err := inst.Install(cmd.Context(), runtimeinstall.Request{
				Config:      cfg,
				UseNemoClaw: &useNemo,
				Port:        port,
				WorkingDir:  workingDir,
			})
			printInstallResult(cmd.OutOrStdout(), result)
			if err != nil {
				return err
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&target, "target", "", "SSH target host or EC2 instance id")
	cmd.Flags().StringVar(&sshUser, "ssh-user", "", "SSH username for the target host")
	cmd.Flags().StringVar(&sshKey, "ssh-key", "", "path to the SSH private key")
	cmd.Flags().IntVar(&sshPort, "ssh-port", 22, "SSH port")
	cmd.Flags().StringVar(&workingDir, "working-dir", "/opt/openclaw", "remote working directory")
	cmd.Flags().IntVar(&port, "port", 0, "runtime port override")
	cmd.Flags().BoolVar(&useNemoClaw, "use-nemoclaw", false, "enable NemoClaw settings for the generated runtime config")
	cmd.Flags().BoolVar(&disableNemoClaw, "disable-nemoclaw", false, "disable NemoClaw settings for the generated runtime config")
	return cmd
}

func resolveInstallTarget(ctx context.Context, profile string, cfg *config.Config, target string) (string, error) {
	if strings.HasPrefix(strings.TrimSpace(target), "i-") {
		prov := newAWSProvider(profile)
		instance, err := prov.GetInstance(ctx, cfg.Region.Name, target)
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(instance.PublicIP) != "" {
			return instance.PublicIP, nil
		}
		if strings.TrimSpace(instance.PrivateIP) != "" {
			return instance.PrivateIP, nil
		}
		return "", fmt.Errorf("instance %s does not expose an SSH-reachable address", target)
	}
	return target, nil
}

func sshUsernameForImage(imageName, imageID string) string {
	lower := strings.ToLower(strings.TrimSpace(imageName) + " " + strings.TrimSpace(imageID))
	if strings.Contains(lower, "ubuntu") {
		return "ubuntu"
	}
	return "ec2-user"
}

func printInstallResult(out io.Writer, result runtimeinstall.Result) {
	fmt.Fprintln(out, "install workflow completed")
	if strings.TrimSpace(result.WorkingDir) != "" {
		fmt.Fprintf(out, "working directory: %s\n", result.WorkingDir)
	}
	if strings.TrimSpace(result.ConfigPath) != "" {
		fmt.Fprintf(out, "runtime config: %s\n", result.ConfigPath)
	}
	if strings.TrimSpace(result.ScriptPath) != "" {
		fmt.Fprintf(out, "install script: %s\n", result.ScriptPath)
	}
	if len(result.Prerequisites.Checks) > 0 {
		fmt.Fprintln(out, "prerequisites:")
		for _, check := range result.Prerequisites.Checks {
			status := "passed"
			if check.Skipped {
				status = "skipped"
			}
			if !check.Passed && !check.Skipped {
				status = "failed"
			}
			fmt.Fprintf(out, "- %s: %s\n", check.Name, status)
			if strings.TrimSpace(check.Message) != "" {
				fmt.Fprintf(out, "  %s\n", check.Message)
			}
			if strings.TrimSpace(check.Remediation) != "" && !check.Passed {
				fmt.Fprintf(out, "  remediation: %s\n", check.Remediation)
			}
		}
	}
	if len(result.CommandResults) > 0 {
		fmt.Fprintln(out, "backend output:")
		for _, r := range result.CommandResults {
			if strings.TrimSpace(r.Stdout) != "" {
				fmt.Fprint(out, r.Stdout)
				if !strings.HasSuffix(r.Stdout, "\n") {
					fmt.Fprintln(out)
				}
			}
			if strings.TrimSpace(r.Stderr) != "" {
				fmt.Fprint(out, r.Stderr)
				if !strings.HasSuffix(r.Stderr, "\n") {
					fmt.Fprintln(out)
				}
			}
		}
	}
}
