package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"agenthub/internal/config"
	"agenthub/internal/host"
)

type slackUndeployOptions struct {
	ConfigPath string
	Target     string
	SSHUser    string
	SSHKey     string
	SSHPort    int
	WorkingDir string
}

type slackUndeployResult struct {
	AgentName      string
	ResolvedTarget string
	Service        inspectServiceState
	Message        string
}

var resolveSlackUndeployTarget = resolveHostTarget

func newSlackUndeployCommand(app *App) *cobra.Command {
	var target string
	var sshUser string
	var sshKey string
	var sshPort int
	var workingDir string

	cmd := &cobra.Command{
		Use:          "undeploy",
		Short:        "Remove the deployed Slack adapter from a remote host",
		GroupID:      "integrations",
		SilenceUsage: true,
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

			fmt.Fprintln(cmd.OutOrStdout(), "running slack undeploy workflow...")
			result, err := runSlackUndeployWorkflow(cmd.Context(), app.opts.Profile, cfg, slackUndeployOptions{
				ConfigPath: app.opts.ConfigPath,
				Target:     target,
				SSHUser:    sshUser,
				SSHKey:     sshKey,
				SSHPort:    sshPort,
				WorkingDir: workingDir,
			})
			printSlackUndeployResult(cmd.OutOrStdout(), result)
			if err != nil {
				return wrapUserFacingError(
					"slack undeploy failed",
					err,
					"the target host is unreachable, the Slack service is missing, or systemd could not remove the unit",
					"confirm the host is reachable over SSH and rerun "+commandRef(cmd.OutOrStdout(), "agenthub", "slack", "undeploy", "--config", app.opts.ConfigPath),
					"run "+commandRef(cmd.OutOrStdout(), "agenthub", "inspect", agentNameFromConfigPath(app.opts.ConfigPath))+" if the host state looks inconsistent",
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
	return cmd
}

func runSlackUndeployWorkflow(ctx context.Context, profile string, cfg *config.Config, opts slackUndeployOptions) (slackUndeployResult, error) {
	if cfg == nil {
		return slackUndeployResult{}, errors.New("config is required")
	}

	targetValue := strings.TrimSpace(opts.Target)
	if targetValue == "" {
		targetValue = strings.TrimSpace(cfg.Infra.InstanceID)
	}
	if targetValue == "" {
		return slackUndeployResult{}, errors.New("target is required: pass --target or run agenthub create first so infra.instance_id is recorded")
	}

	agentName := agentNameFromConfigPath(opts.ConfigPath)
	if agentName == "" {
		agentName = "default"
	}

	resolvedTarget, err := resolveSlackUndeployTarget(ctx, profile, cfg, targetValue)
	if err != nil {
		return slackUndeployResult{}, err
	}
	user, keyPath, err := resolveInstallSSH(cfg, opts.SSHUser, opts.SSHKey)
	if err != nil {
		return slackUndeployResult{}, err
	}

	exec := newSSHExecutor(host.SSHConfig{
		Host:           resolvedTarget,
		Port:           opts.SSHPort,
		User:           user,
		IdentityFile:   keyPath,
		ConnectTimeout: 15 * time.Second,
	})
	if err := waitForSSHReady(ctx, exec, resolvedTarget); err != nil {
		return slackUndeployResult{}, err
	}

	result := slackUndeployResult{
		AgentName:      agentName,
		ResolvedTarget: resolvedTarget,
	}

	serviceUnit := slackServiceNameForAgent(agentName) + ".service"
	state, err := inspectServiceUnit(ctx, exec, serviceUnit)
	if err != nil {
		return result, fmt.Errorf("inspect slack service: %w", err)
	}
	result.Service = state

	remoteServicePath := slackServiceUnitPathForAgent(agentName)
	if remoteServicePath == "" {
		return result, errors.New("failed to build slack service unit path")
	}
	remoteAgentDir := remoteSlackAgentDir(opts.WorkingDir, agentName)

	if state.Installed {
		if _, err := exec.Run(ctx, "sudo", "systemctl", "stop", serviceUnit); err != nil {
			commandErr := formatRemoteCommandError("stop slack service", err)
			if !isMissingServiceUnitError(commandErr) {
				return result, commandErr
			}
		}
		if _, err := exec.Run(ctx, "sudo", "systemctl", "disable", serviceUnit); err != nil {
			commandErr := formatRemoteCommandError("disable slack service", err)
			if !isMissingServiceUnitError(commandErr) {
				return result, commandErr
			}
		}
	}

	if _, err := exec.Run(ctx, "sudo", "rm", "-f", remoteServicePath); err != nil {
		return result, formatRemoteCommandError("remove slack service unit", err)
	}
	if _, err := exec.Run(ctx, "sudo", "rm", "-rf", remoteAgentDir); err != nil {
		return result, formatRemoteCommandError("remove slack workspace", err)
	}
	if _, err := exec.Run(ctx, "sudo", "systemctl", "daemon-reload"); err != nil {
		return result, formatRemoteCommandError("reload systemd after slack undeploy", err)
	}

	if result.Service.Installed {
		result.Message = "slack integration removed"
	} else {
		result.Message = "slack integration was already absent"
	}
	result.Service = inspectServiceState{Unit: serviceUnit}
	return result, nil
}

func printSlackUndeployResult(out io.Writer, result slackUndeployResult) {
	fmt.Fprintf(out, "agent: %s\n", result.AgentName)
	if strings.TrimSpace(result.ResolvedTarget) != "" {
		fmt.Fprintf(out, "target: %s\n", result.ResolvedTarget)
	}
	if strings.TrimSpace(result.Message) != "" {
		fmt.Fprintf(out, "result: %s\n", result.Message)
	}
	if strings.TrimSpace(result.Service.Unit) != "" {
		fmt.Fprintf(out, "service: %s\n", strings.TrimSpace(result.Service.Unit))
	}
}

func formatRemoteCommandError(action string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", action, err)
}
