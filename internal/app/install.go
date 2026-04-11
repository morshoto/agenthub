package app

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"agenthub/internal/config"
	"agenthub/internal/runtimeinstall"
)

func newInstallCommand(app *App) *cobra.Command {
	var target string
	var sshUser string
	var sshKey string
	var sshPort int
	var workingDir string
	var port int
	var useNemoClaw bool
	var disableNemoClaw bool
	var agentName string
	var agentsDir string

	cmd := &cobra.Command{
		Use:     "install",
		Short:   "Install the AgentHub runtime on a prepared host",
		GroupID: "provision",
		RunE: func(cmd *cobra.Command, args []string) error {
			configPath, err := resolveAgentConfigPath(agentConfigResolutionOptions{
				ConfigPath:    app.opts.ConfigPath,
				AgentName:     agentName,
				AgentsDir:     agentsDir,
				RequireConfig: true,
			})
			if err != nil {
				return err
			}
			app.opts.ConfigPath = configPath
			cfg, err := config.Load(configPath)
			if err != nil {
				return err
			}
			if err := config.Validate(cfg); err != nil {
				return err
			}
			if strings.TrimSpace(target) == "" {
				return errors.New("target is required: pass --target <instance-id-or-host>")
			}

			logger := loggerFromContext(cmd.Context())
			logger.Info("starting install workflow")
			fmt.Fprintln(cmd.OutOrStdout(), "running install workflow...")
			result, resolvedTarget, err := runInstallWorkflow(cmd.Context(), app.opts.Profile, cfg, installOptions{
				Target:          target,
				SSHUser:         sshUser,
				SSHKey:          sshKey,
				SSHPort:         sshPort,
				WorkingDir:      workingDir,
				Port:            port,
				UseNemoClaw:     useNemoClaw,
				DisableNemoClaw: disableNemoClaw,
			})
			printInstallResult(cmd.OutOrStdout(), result)
			printSuccessNextSteps(cmd.OutOrStdout(), configPath, resolvedTarget, false)
			if err != nil {
				return wrapUserFacingError(
					"install failed",
					err,
					"the SSH target is unreachable or the host prerequisites are missing",
					"run "+commandRef(cmd.OutOrStdout(), "agenthub", "verify", "--config", configPath, "--target", resolvedTarget)+" after fixing the host",
					"check Docker, GPU drivers, and SSH access on the target host",
				)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&target, "target", "", "SSH target host or EC2 instance id")
	cmd.Flags().StringVar(&sshUser, "ssh-user", "", "SSH username for the target host")
	cmd.Flags().StringVar(&sshKey, "ssh-key", "", "path to the SSH private key")
	cmd.Flags().IntVar(&sshPort, "ssh-port", 22, "SSH port")
	cmd.Flags().StringVar(&workingDir, "working-dir", "/opt/agenthub", "remote working directory")
	cmd.Flags().IntVar(&port, "port", 0, "runtime port override")
	cmd.Flags().BoolVar(&useNemoClaw, "use-nemoclaw", false, "enable NemoClaw settings for the generated runtime config")
	cmd.Flags().BoolVar(&disableNemoClaw, "disable-nemoclaw", false, "disable NemoClaw settings for the generated runtime config")
	cmd.Flags().StringVar(&agentName, "agent", "", "agent name to resolve under the agents directory")
	cmd.Flags().StringVar(&agentsDir, "agents-dir", "agents", "path to the agents directory")
	return cmd
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
	if strings.TrimSpace(result.BinaryPath) != "" {
		fmt.Fprintf(out, "runtime binary: %s\n", result.BinaryPath)
	}
	if strings.TrimSpace(result.ServicePath) != "" {
		fmt.Fprintf(out, "systemd unit: %s\n", result.ServicePath)
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
