package app

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"agenthub/internal/config"
)

func newRedeployCommand(app *App) *cobra.Command {
	var target string
	var sshUser string
	var sshKey string
	var sshPort int
	var workingDir string
	var port int
	var useNemoClaw bool
	var disableNemoClaw bool

	cmd := &cobra.Command{
		Use:     "redeploy",
		Short:   "Re-apply the runtime deployment to an existing host",
		GroupID: "provision",
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

			targetValue := strings.TrimSpace(target)
			if targetValue == "" {
				targetValue = strings.TrimSpace(cfg.Infra.InstanceID)
			}
			if targetValue == "" {
				return errors.New("target is required: pass --target or record infra.instance_id in the config")
			}

			agentName := agentNameFromConfigPath(app.opts.ConfigPath)
			if agentName == "" {
				agentName = "default"
			}

			resolvedTarget, err := resolveHostTarget(cmd.Context(), app.opts.Profile, cfg, targetValue)
			if err != nil {
				return wrapUserFacingError(
					"redeploy failed",
					err,
					"the target deployment could not be resolved to a reachable host",
					"confirm infra.instance_id or pass "+commandRef(cmd.OutOrStdout(), "--target", "<instance-id-or-host>"),
				)
			}

			fmt.Fprintln(cmd.OutOrStdout(), "running redeploy workflow...")
			fmt.Fprintf(cmd.OutOrStdout(), "agent: %s\n", agentName)
			fmt.Fprintf(cmd.OutOrStdout(), "deployment target: %s\n", resolvedTarget)
			printRedeployUpdateSummary(cmd.OutOrStdout(), cfg, workingDir)

			logger := loggerFromContext(cmd.Context())
			logger.Info("starting redeploy workflow")

			installResult, verifyReport, resolvedTarget, err := runRedeployWorkflow(cmd.Context(), app.opts.Profile, cfg, installOptions{
				Target:          targetValue,
				SSHUser:         sshUser,
				SSHKey:          sshKey,
				SSHPort:         sshPort,
				WorkingDir:      workingDir,
				Port:            port,
				UseNemoClaw:     useNemoClaw,
				DisableNemoClaw: disableNemoClaw,
			})
			printInstallResult(cmd.OutOrStdout(), installResult)
			printVerificationReport(cmd.OutOrStdout(), verifyReport)
			if strings.TrimSpace(resolvedTarget) != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "target: %s\n", resolvedTarget)
			}
			if err != nil {
				return wrapUserFacingError(
					"redeploy failed",
					err,
					"the target host is unreachable, the host runtime is missing prerequisites, or verification failed after apply",
					"run "+commandRef(cmd.OutOrStdout(), "agenthub", "verify", "--config", app.opts.ConfigPath, "--target", resolvedTarget)+" after fixing the host",
					"check the host service status and runtime config on the target machine",
				)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&target, "target", "", "SSH target host or EC2 instance id; defaults to infra.instance_id")
	cmd.Flags().StringVar(&sshUser, "ssh-user", "", "SSH username for the target host")
	cmd.Flags().StringVar(&sshKey, "ssh-key", "", "path to the SSH private key")
	cmd.Flags().IntVar(&sshPort, "ssh-port", 22, "SSH port")
	cmd.Flags().StringVar(&workingDir, "working-dir", "/opt/agenthub", "remote working directory")
	cmd.Flags().IntVar(&port, "port", 0, "runtime port override")
	cmd.Flags().BoolVar(&useNemoClaw, "use-nemoclaw", false, "enable NemoClaw settings for the generated runtime config")
	cmd.Flags().BoolVar(&disableNemoClaw, "disable-nemoclaw", false, "disable NemoClaw settings for the generated runtime config")
	return cmd
}

func printRedeployUpdateSummary(out io.Writer, cfg *config.Config, workingDir string) {
	if strings.TrimSpace(workingDir) == "" {
		workingDir = "/opt/agenthub"
	}
	fmt.Fprintln(out, "will update")
	fmt.Fprintf(out, "- runtime binary: %s\n", pathJoin(workingDir, "bin", "agenthub"))
	fmt.Fprintf(out, "- runtime config: %s\n", pathJoin(workingDir, "runtime.yaml"))
	fmt.Fprintf(out, "- systemd unit: %s\n", "/etc/systemd/system/agenthub.service")
	providerName := strings.ToLower(strings.TrimSpace(cfg.Runtime.Provider))
	if providerName == "codex" || providerName == "aws-bedrock" {
		fmt.Fprintf(out, "- provider environment: %s\n", pathJoin(workingDir, "agenthub.env"))
	}
}
