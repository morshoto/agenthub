package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"agenthub/internal/config"
	"agenthub/internal/host"
	"agenthub/internal/prompt"
)

const (
	logServiceRuntime     = "runtime"
	logServiceIntegration = "integration"
	logServiceAll         = "all"
	defaultLogLines       = 100
)

type logsOptions struct {
	ConfigPath string
	Target     string
	SSHUser    string
	SSHKey     string
	SSHPort    int
	Service    string
	Lines      int
}

type serviceLogTarget struct {
	Name string
	Unit string
}

type serviceLogResult struct {
	Name   string
	Unit   string
	Output string
}

type serviceLogError struct {
	Name string
	Unit string
	Err  error
}

type logsResult struct {
	AgentName string
	Target    string
	Lines     int
	Service   string
	Sections  []serviceLogResult
	Warnings  []serviceLogError
}

func newLogsCommand(app *App) *cobra.Command {
	var target string
	var sshUser string
	var sshKey string
	var sshPort int
	var service string
	var lines int
	var agentsDir string

	cmd := &cobra.Command{
		Use:          "logs",
		Short:        "Fetch recent logs for a deployed agent",
		GroupID:      "runtime",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			configPath := strings.TrimSpace(app.opts.ConfigPath)
			if configPath == "" {
				session := prompt.NewSession(cmd.InOrStdin(), cmd.OutOrStdout())
				selectedConfigPath, err := selectAgentConfigPath(session, agentsDir)
				if err != nil {
					return err
				}
				configPath = selectedConfigPath
			}

			cfg, err := config.Load(configPath)
			if err != nil {
				return err
			}
			if err := config.Validate(cfg); err != nil {
				return err
			}

			logger := loggerFromContext(cmd.Context())
			logger.Info("starting logs workflow")
			fmt.Fprintln(cmd.OutOrStdout(), "fetching service logs...")

			result, err := runLogsWorkflow(cmd.Context(), app.opts.Profile, cfg, logsOptions{
				ConfigPath: configPath,
				Target:     target,
				SSHUser:    sshUser,
				SSHKey:     sshKey,
				SSHPort:    sshPort,
				Service:    service,
				Lines:      lines,
			})
			printLogsResult(cmd.OutOrStdout(), result)
			if err != nil {
				return wrapUserFacingError(
					"logs retrieval failed",
					err,
					"the target host is unreachable or one of the requested services is not installed",
					"confirm the host is reachable over SSH and rerun "+commandRef(cmd.OutOrStdout(), "agenthub", "logs", "--config", configPath),
					"adjust "+commandRef(cmd.OutOrStdout(), "--service", "runtime")+" or "+commandRef(cmd.OutOrStdout(), "--service", "integration")+" if only one service is expected on the host",
				)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&target, "target", "", "SSH target host or EC2 instance id; defaults to infra.instance_id")
	cmd.Flags().StringVar(&sshUser, "ssh-user", "", "SSH username for the target host")
	cmd.Flags().StringVar(&sshKey, "ssh-key", "", "path to the SSH private key")
	cmd.Flags().IntVar(&sshPort, "ssh-port", 22, "SSH port")
	cmd.Flags().StringVar(&service, "service", logServiceAll, "service logs to fetch: runtime, integration, or all")
	cmd.Flags().IntVar(&lines, "lines", defaultLogLines, "number of recent log lines to fetch per service")
	cmd.Flags().StringVar(&agentsDir, "agents-dir", "agents", "path to the agents directory")
	return cmd
}

func runLogsWorkflow(ctx context.Context, profile string, cfg *config.Config, opts logsOptions) (logsResult, error) {
	if cfg == nil {
		return logsResult{}, errors.New("config is required")
	}

	service := strings.ToLower(strings.TrimSpace(opts.Service))
	if service == "" {
		service = logServiceAll
	}
	if opts.Lines <= 0 {
		return logsResult{}, errors.New("lines must be greater than 0")
	}

	targetValue := strings.TrimSpace(opts.Target)
	if targetValue == "" {
		targetValue = strings.TrimSpace(cfg.Infra.InstanceID)
	}
	if targetValue == "" {
		return logsResult{}, errors.New("target is required: pass --target or run agenthub create first so infra.instance_id is recorded")
	}

	agentName := agentNameFromConfigPath(opts.ConfigPath)
	if agentName == "" {
		agentName = "default"
	}

	resolvedTarget, err := resolveHostTarget(ctx, profile, cfg, targetValue)
	if err != nil {
		return logsResult{}, err
	}
	user, keyPath, err := resolveInstallSSH(cfg, opts.SSHUser, opts.SSHKey)
	if err != nil {
		return logsResult{}, err
	}

	exec := newSSHExecutor(host.SSHConfig{
		Host:           resolvedTarget,
		Port:           opts.SSHPort,
		User:           user,
		IdentityFile:   keyPath,
		ConnectTimeout: 15 * time.Second,
	})
	if err := waitForSSHReady(ctx, exec, resolvedTarget); err != nil {
		return logsResult{}, err
	}

	targets, err := resolveLogTargets(service, agentName)
	if err != nil {
		return logsResult{}, err
	}

	result := logsResult{
		AgentName: agentName,
		Target:    resolvedTarget,
		Lines:     opts.Lines,
		Service:   service,
	}

	var errs []error
	for _, target := range targets {
		output, fetchErr := fetchServiceLogs(ctx, exec, target.Unit, opts.Lines)
		if fetchErr != nil {
			serviceErr := serviceLogError{Name: target.Name, Unit: target.Unit, Err: fetchErr}
			if service == logServiceAll && target.Name == logServiceIntegration && isMissingServiceUnitError(fetchErr) {
				result.Warnings = append(result.Warnings, serviceErr)
				continue
			}
			errs = append(errs, fmt.Errorf("%s logs (%s): %w", target.Name, target.Unit, fetchErr))
			continue
		}
		result.Sections = append(result.Sections, serviceLogResult{
			Name:   target.Name,
			Unit:   target.Unit,
			Output: output,
		})
	}

	return result, errors.Join(errs...)
}

func resolveLogTargets(service, agentName string) ([]serviceLogTarget, error) {
	switch strings.ToLower(strings.TrimSpace(service)) {
	case logServiceRuntime:
		return []serviceLogTarget{{Name: logServiceRuntime, Unit: "agenthub.service"}}, nil
	case logServiceIntegration:
		return []serviceLogTarget{{Name: logServiceIntegration, Unit: slackServiceNameForAgent(agentName) + ".service"}}, nil
	case logServiceAll:
		return []serviceLogTarget{
			{Name: logServiceRuntime, Unit: "agenthub.service"},
			{Name: logServiceIntegration, Unit: slackServiceNameForAgent(agentName) + ".service"},
		}, nil
	default:
		return nil, fmt.Errorf("unsupported service %q: expected runtime, integration, or all", service)
	}
}

func fetchServiceLogs(ctx context.Context, exec host.Executor, unit string, lines int) (string, error) {
	if exec == nil {
		return "", errors.New("executor is required")
	}
	if strings.TrimSpace(unit) == "" {
		return "", errors.New("service unit is required")
	}
	if lines <= 0 {
		return "", errors.New("lines must be greater than 0")
	}

	script := strings.Join([]string{
		"set -eu",
		"unit=" + strconv.Quote(strings.TrimSpace(unit)),
		"if ! systemctl list-unit-files --full --no-legend \"$unit\" 2>/dev/null | awk '{print $1}' | grep -Fxq \"$unit\"; then",
		"  echo \"service unit $unit is not installed on the target host\" >&2",
		"  exit 1",
		"fi",
		"journalctl --no-pager --output short-iso -n " + strconv.Itoa(lines) + " -u \"$unit\"",
	}, "\n")

	result, err := exec.Run(ctx, "sudo", "sh", "-lc", script)
	if err != nil {
		msg := strings.TrimSpace(firstNonEmpty(result.Stderr, result.Stdout))
		if msg != "" {
			return "", errors.New(msg)
		}
		return "", err
	}

	output := strings.TrimSpace(result.Stdout)
	if output == "" {
		return "no log entries returned", nil
	}
	return output, nil
}

func isMissingServiceUnitError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "service unit") &&
		strings.Contains(strings.ToLower(err.Error()), "is not installed")
}

func printLogsResult(out io.Writer, result logsResult) {
	if strings.TrimSpace(result.AgentName) != "" {
		fmt.Fprintf(out, "agent: %s\n", result.AgentName)
	}
	if strings.TrimSpace(result.Target) != "" {
		fmt.Fprintf(out, "target: %s\n", result.Target)
	}
	if result.Lines > 0 {
		fmt.Fprintf(out, "lines: %d\n", result.Lines)
	}
	if len(result.Sections) == 0 {
		return
	}

	for i, section := range result.Sections {
		if i == 0 {
			fmt.Fprintln(out)
		} else {
			fmt.Fprintln(out)
			fmt.Fprintln(out)
		}
		fmt.Fprintf(out, "%s logs (%s)\n", section.Name, section.Unit)
		fmt.Fprintln(out, strings.Repeat("-", len(section.Name)+len(section.Unit)+8))
		fmt.Fprint(out, strings.TrimRight(section.Output, "\n"))
	}
	if len(result.Sections) > 0 {
		fmt.Fprintln(out)
	}
	for _, warning := range result.Warnings {
		fmt.Fprintf(out, "warning: %s logs (%s): %s\n", warning.Name, warning.Unit, warning.Err)
	}
}
