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

type runtimeServiceOptions struct {
	ConfigPath string
	Target     string
	SSHUser    string
	SSHKey     string
	SSHPort    int
	Action     string
}

type runtimeServiceResult struct {
	AgentName      string
	ResolvedTarget string
	Action         string
	Changed        bool
	Message        string
	Service        inspectServiceState
}

func newRuntimeCommand(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "runtime",
		Short:   "Run deployed runtime service commands",
		GroupID: "runtime",
	}
	cmd.AddCommand(newRuntimeStartCommand(app))
	cmd.AddCommand(newRuntimeStopCommand(app))
	return cmd
}

func newRuntimeStartCommand(app *App) *cobra.Command {
	var target string
	var sshUser string
	var sshKey string
	var sshPort int

	cmd := &cobra.Command{
		Use:          "start",
		Short:        "Start the runtime service for a deployed agent",
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

			fmt.Fprintln(cmd.OutOrStdout(), "starting runtime service...")
			result, err := runRuntimeServiceWorkflow(cmd.Context(), app.opts.Profile, cfg, runtimeServiceOptions{
				ConfigPath: app.opts.ConfigPath,
				Target:     target,
				SSHUser:    sshUser,
				SSHKey:     sshKey,
				SSHPort:    sshPort,
				Action:     "start",
			})
			printRuntimeServiceResult(cmd.OutOrStdout(), result)
			if err != nil {
				return wrapUserFacingError(
					"runtime start failed",
					err,
					"the target host is unreachable, the runtime service is missing, or systemd could not start the unit",
					"confirm the host is reachable over SSH and rerun "+commandRef(cmd.OutOrStdout(), "agenthub", "runtime", "start", "--config", app.opts.ConfigPath),
					"run "+commandRef(cmd.OutOrStdout(), "agenthub", "inspect", agentNameFromConfigPath(app.opts.ConfigPath))+" after fixing the host state",
				)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&target, "target", "", "SSH target host or EC2 instance id; defaults to infra.instance_id")
	cmd.Flags().StringVar(&sshUser, "ssh-user", "", "SSH username for the target host")
	cmd.Flags().StringVar(&sshKey, "ssh-key", "", "path to the SSH private key")
	cmd.Flags().IntVar(&sshPort, "ssh-port", 22, "SSH port")
	return cmd
}

func newRuntimeStopCommand(app *App) *cobra.Command {
	var target string
	var sshUser string
	var sshKey string
	var sshPort int

	cmd := &cobra.Command{
		Use:          "stop",
		Short:        "Stop the runtime service for a deployed agent",
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

			fmt.Fprintln(cmd.OutOrStdout(), "stopping runtime service...")
			result, err := runRuntimeServiceWorkflow(cmd.Context(), app.opts.Profile, cfg, runtimeServiceOptions{
				ConfigPath: app.opts.ConfigPath,
				Target:     target,
				SSHUser:    sshUser,
				SSHKey:     sshKey,
				SSHPort:    sshPort,
				Action:     "stop",
			})
			printRuntimeServiceResult(cmd.OutOrStdout(), result)
			if err != nil {
				return wrapUserFacingError(
					"runtime stop failed",
					err,
					"the target host is unreachable, the runtime service is missing, or systemd could not stop the unit",
					"confirm the host is reachable over SSH and rerun "+commandRef(cmd.OutOrStdout(), "agenthub", "runtime", "stop", "--config", app.opts.ConfigPath),
					"run "+commandRef(cmd.OutOrStdout(), "agenthub", "inspect", agentNameFromConfigPath(app.opts.ConfigPath))+" after fixing the host state",
				)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&target, "target", "", "SSH target host or EC2 instance id; defaults to infra.instance_id")
	cmd.Flags().StringVar(&sshUser, "ssh-user", "", "SSH username for the target host")
	cmd.Flags().StringVar(&sshKey, "ssh-key", "", "path to the SSH private key")
	cmd.Flags().IntVar(&sshPort, "ssh-port", 22, "SSH port")
	return cmd
}

func runRuntimeServiceWorkflow(ctx context.Context, profile string, cfg *config.Config, opts runtimeServiceOptions) (runtimeServiceResult, error) {
	if cfg == nil {
		return runtimeServiceResult{}, errors.New("config is required")
	}

	action := strings.ToLower(strings.TrimSpace(opts.Action))
	if action == "" {
		action = "start"
	}
	if action != "start" && action != "stop" {
		return runtimeServiceResult{}, fmt.Errorf("unsupported runtime service action %q", action)
	}

	targetValue := strings.TrimSpace(opts.Target)
	if targetValue == "" {
		targetValue = strings.TrimSpace(cfg.Infra.InstanceID)
	}
	if targetValue == "" {
		return runtimeServiceResult{}, errors.New("target is required: pass --target or run agenthub create first so infra.instance_id is recorded")
	}

	agentName := agentNameFromConfigPath(opts.ConfigPath)
	if agentName == "" {
		agentName = "default"
	}

	resolvedTarget, err := resolveHostTarget(ctx, profile, cfg, targetValue)
	if err != nil {
		return runtimeServiceResult{}, err
	}
	user, keyPath, err := resolveInstallSSH(cfg, opts.SSHUser, opts.SSHKey)
	if err != nil {
		return runtimeServiceResult{}, err
	}

	exec := newSSHExecutor(host.SSHConfig{
		Host:           resolvedTarget,
		Port:           opts.SSHPort,
		User:           user,
		IdentityFile:   keyPath,
		ConnectTimeout: 15 * time.Second,
	})
	if err := waitForSSHReady(ctx, exec, resolvedTarget); err != nil {
		return runtimeServiceResult{}, err
	}

	result := runtimeServiceResult{
		AgentName:      agentName,
		ResolvedTarget: resolvedTarget,
		Action:         action,
	}

	state, err := inspectServiceUnit(ctx, exec, "agenthub.service")
	if err != nil {
		return result, fmt.Errorf("inspect runtime service: %w", err)
	}
	if !state.Installed {
		return result, errors.New("runtime service: agenthub.service is not installed on the target host")
	}
	if runtimeServiceActionAlreadySatisfied(action, state) {
		result.Service = state
		result.Message = runtimeServiceAlreadySatisfiedMessage(action)
		return result, nil
	}

	commandResult, err := exec.Run(ctx, "sudo", "systemctl", action, "agenthub.service")
	if err != nil {
		msg := strings.TrimSpace(firstNonEmpty(commandResult.Stderr, commandResult.Stdout))
		if msg == "" {
			msg = err.Error()
		}
		return result, fmt.Errorf("%s runtime service: %s", action, msg)
	}

	state, err = inspectServiceUnit(ctx, exec, "agenthub.service")
	if err != nil {
		return result, fmt.Errorf("inspect runtime service after %s: %w", action, err)
	}
	result.Service = state
	if !runtimeServiceActionReachedExpectedState(action, state) {
		return result, fmt.Errorf("runtime service did not reach expected state after %s: %s", action, formatInspectServiceSummary(state))
	}

	result.Changed = true
	result.Message = runtimeServiceChangedMessage(action)
	return result, nil
}

func runtimeServiceActionAlreadySatisfied(action string, state inspectServiceState) bool {
	switch action {
	case "start":
		return strings.EqualFold(strings.TrimSpace(state.ActiveState), "active")
	case "stop":
		return !strings.EqualFold(strings.TrimSpace(state.ActiveState), "active")
	default:
		return false
	}
}

func runtimeServiceAlreadySatisfiedMessage(action string) string {
	switch action {
	case "start":
		return "runtime service is already running"
	case "stop":
		return "runtime service is already stopped"
	default:
		return ""
	}
}

func runtimeServiceChangedMessage(action string) string {
	switch action {
	case "start":
		return "runtime service started"
	case "stop":
		return "runtime service stopped"
	default:
		return ""
	}
}

func runtimeServiceActionReachedExpectedState(action string, state inspectServiceState) bool {
	switch action {
	case "start":
		return strings.EqualFold(strings.TrimSpace(state.ActiveState), "active")
	case "stop":
		return !strings.EqualFold(strings.TrimSpace(state.ActiveState), "active")
	default:
		return false
	}
}

func printRuntimeServiceResult(out io.Writer, result runtimeServiceResult) {
	fmt.Fprintf(out, "agent: %s\n", result.AgentName)
	if strings.TrimSpace(result.ResolvedTarget) != "" {
		fmt.Fprintf(out, "target: %s\n", result.ResolvedTarget)
	}
	if strings.TrimSpace(result.Message) != "" {
		fmt.Fprintf(out, "result: %s\n", result.Message)
	}
	if summary := formatInspectServiceSummary(result.Service); summary != "" {
		fmt.Fprintf(out, "runtime service: %s\n", summary)
	}
}
