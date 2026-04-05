package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"openclaw/internal/config"
	"openclaw/internal/host"
	"openclaw/internal/prompt"
)

type slackDeployOptions struct {
	ConfigPath string
	Target     string
	SSHUser    string
	SSHKey     string
	SSHPort    int
	WorkingDir string
}

var resolveSlackDeployTarget = resolveHostTarget

func newSlackDeployCommand(app *App) *cobra.Command {
	var target string
	var sshUser string
	var sshKey string
	var sshPort int
	var workingDir string
	var agentsDir string

	cmd := &cobra.Command{
		Use:     "deploy",
		Short:   "Deploy the Slack adapter to a remote EC2 host",
		GroupID: "integrations",
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
			agentCfg, err := config.Load(configPath)
			if err != nil {
				return err
			}
			if err := config.Validate(agentCfg); err != nil {
				return err
			}
			if !strings.EqualFold(strings.TrimSpace(agentCfg.Runtime.Provider), "codex") {
				return fmt.Errorf("slack deploy currently supports codex agents only (provider=%q)", agentCfg.Runtime.Provider)
			}
			targetValue := strings.TrimSpace(target)
			if targetValue == "" {
				targetValue = strings.TrimSpace(agentCfg.Infra.InstanceID)
			}
			if targetValue == "" {
				return errors.New("target is required: pass --target or run openclaw create first so infra.instance_id is recorded")
			}
			agentEnvPath := agentEnvPathFromConfigPath(configPath)
			agentEnv, err := loadAgentEnvFile(agentEnvPath)
			if err != nil {
				return err
			}

			logger := loggerFromContext(cmd.Context())
			logger.Info("starting slack deploy workflow")
			fmt.Fprintln(cmd.OutOrStdout(), "running slack deploy workflow...")
			resolvedTarget, err := runSlackDeployWorkflow(cmd.Context(), app.opts.Profile, agentCfg, agentEnv, slackDeployOptions{
				ConfigPath: configPath,
				Target:     targetValue,
				SSHUser:    sshUser,
				SSHKey:     sshKey,
				SSHPort:    sshPort,
				WorkingDir: workingDir,
			})
			if err != nil {
				return wrapUserFacingError(
					"slack deploy failed",
					err,
					"the SSH target is unreachable, Codex is not installed on the host, or Slack tokens are missing",
					"SSH into the EC2 host and run "+commandRef(cmd.OutOrStdout(), "openclaw", "onboard", "--auth-choice", "openai-codex")+" once to authenticate Codex",
					"verify the agent .env contains SLACK_BOT_TOKEN and SLACK_APP_TOKEN",
				)
			}
			agentName := agentNameFromConfigPath(configPath)
			fmt.Fprintf(cmd.OutOrStdout(), "slack adapter deployed to %s\n", resolvedTarget)
			fmt.Fprintf(cmd.OutOrStdout(), "service: %s\n", slackServiceNameForAgent(agentName))
			fmt.Fprintf(cmd.OutOrStdout(), "next step: check %s on the host if you want to confirm the service status\n", slackServiceUnitPathForAgent(agentName))
			return nil
		},
	}

	cmd.Flags().StringVar(&target, "target", "", "SSH target host or EC2 instance id; defaults to infra.instance_id")
	cmd.Flags().StringVar(&sshUser, "ssh-user", "", "SSH username for the target host")
	cmd.Flags().StringVar(&sshKey, "ssh-key", "", "path to the SSH private key")
	cmd.Flags().IntVar(&sshPort, "ssh-port", 22, "SSH port")
	cmd.Flags().StringVar(&workingDir, "working-dir", "/opt/openclaw", "remote working directory")
	cmd.Flags().StringVar(&agentsDir, "agents-dir", "agents", "path to the agents directory")
	return cmd
}

func runSlackDeployWorkflow(ctx context.Context, profile string, cfg *config.Config, agentEnv map[string]string, opts slackDeployOptions) (string, error) {
	if cfg == nil {
		return "", errors.New("config is required")
	}

	botToken := strings.TrimSpace(agentEnv["SLACK_BOT_TOKEN"])
	appToken := strings.TrimSpace(agentEnv["SLACK_APP_TOKEN"])
	if botToken == "" || appToken == "" {
		return "", errors.New("agent env must contain SLACK_BOT_TOKEN and SLACK_APP_TOKEN")
	}
	targetValue := strings.TrimSpace(opts.Target)
	if targetValue == "" {
		targetValue = strings.TrimSpace(cfg.Infra.InstanceID)
	}
	if targetValue == "" {
		return "", errors.New("target is required")
	}

	agentName := agentNameFromConfigPath(opts.ConfigPath)
	if agentName == "" {
		agentName = "default"
	}
	remoteAgentDir := remoteSlackAgentDir(opts.WorkingDir, agentName)
	remoteConfigPath := pathJoin(remoteAgentDir, "config.yaml")
	remoteEnvPath := pathJoin(remoteAgentDir, ".env")
	stagedServicePath := pathJoin(remoteAgentDir, "openclaw-slack.service")
	remoteServicePath := slackServiceUnitPathForAgent(agentName)
	if remoteServicePath == "" {
		return "", errors.New("failed to build slack service unit path")
	}

	resolvedTarget, err := resolveSlackDeployTarget(ctx, profile, cfg, targetValue)
	if err != nil {
		return "", err
	}
	user, keyPath, err := resolveInstallSSH(cfg, opts.SSHUser, opts.SSHKey)
	if err != nil {
		return "", err
	}
	exec := newSSHExecutor(host.SSHConfig{
		Host:           resolvedTarget,
		Port:           opts.SSHPort,
		User:           user,
		IdentityFile:   keyPath,
		ConnectTimeout: 15 * time.Second,
	})
	if err := waitForSSHReady(ctx, exec, resolvedTarget); err != nil {
		return "", err
	}

	if err := ensureCodexCLI(ctx, exec); err != nil {
		return "", err
	}

	if _, err := exec.Run(ctx, "sudo", "mkdir", "-p", remoteAgentDir); err != nil {
		return "", fmt.Errorf("prepare slack agent directory: %w", err)
	}
	if _, err := exec.Run(ctx, "sudo", "chown", "-R", "ubuntu:ubuntu", remoteAgentDir); err != nil {
		return "", fmt.Errorf("prepare slack agent directory ownership: %w", err)
	}

	tmpDir, err := os.MkdirTemp("", "openclaw-slack-*")
	if err != nil {
		return "", fmt.Errorf("create temporary slack deployment workspace: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	localConfigPath := filepath.Join(tmpDir, "config.yaml")
	if err := config.Save(localConfigPath, cfg); err != nil {
		return "", fmt.Errorf("write slack agent config: %w", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, ".env"), []byte(strings.Join([]string{
		"SLACK_BOT_TOKEN=" + botToken,
		"SLACK_APP_TOKEN=" + appToken,
		"",
	}, "\n")), 0o600); err != nil {
		return "", fmt.Errorf("write slack agent env: %w", err)
	}

	if err := exec.Upload(ctx, localConfigPath, remoteConfigPath); err != nil {
		return "", fmt.Errorf("upload slack agent config: %w", err)
	}
	if err := exec.Upload(ctx, filepath.Join(tmpDir, ".env"), remoteEnvPath); err != nil {
		return "", fmt.Errorf("upload slack agent env: %w", err)
	}

	localUnitPath := filepath.Join(tmpDir, "openclaw-slack.service")
	if err := os.WriteFile(localUnitPath, []byte(renderSlackSystemdUnit(pathJoin(opts.WorkingDir, "bin", "openclaw"), remoteConfigPath, remoteEnvPath, remoteAgentDir, agentName)), 0o600); err != nil {
		return "", fmt.Errorf("write slack systemd unit: %w", err)
	}
	if err := exec.Upload(ctx, localUnitPath, stagedServicePath); err != nil {
		return "", fmt.Errorf("upload slack systemd unit: %w", err)
	}
	if _, err := exec.Run(ctx, "sudo", "mv", stagedServicePath, remoteServicePath); err != nil {
		return "", fmt.Errorf("install slack systemd unit: %w", err)
	}
	if _, err := exec.Run(ctx, "sudo", "chmod", "600", remoteEnvPath); err != nil {
		return "", fmt.Errorf("secure slack env file: %w", err)
	}
	if _, err := exec.Run(ctx, "sudo", "systemctl", "daemon-reload"); err != nil {
		return "", fmt.Errorf("reload systemd after slack deploy: %w", err)
	}
	if _, err := exec.Run(ctx, "sudo", "systemctl", "enable", "--now", filepath.Base(remoteServicePath)); err != nil {
		return "", fmt.Errorf("enable slack service: %w", err)
	}

	return resolvedTarget, nil
}

func renderSlackSystemdUnit(binaryPath, configPath, envFilePath, workingDir, agentName string) string {
	return fmt.Sprintf(`[Unit]
Description=OpenClaw Slack adapter (%s)
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=ubuntu
Environment=HOME=/home/ubuntu
Environment=PATH=/home/ubuntu/.nix-profile/bin:/home/ubuntu/.local/bin:/home/ubuntu/.npm-global/bin:/home/ubuntu/.local/share/npm/bin:/usr/local/bin:/usr/bin:/bin
WorkingDirectory=%s
EnvironmentFile=-%s
ExecStart=%s slack serve --config %s
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
`, strings.TrimSpace(agentName), strings.TrimSpace(workingDir), strings.TrimSpace(envFilePath), strings.TrimSpace(binaryPath), strings.TrimSpace(configPath))
}

func agentNameFromConfigPath(configPath string) string {
	configPath = strings.TrimSpace(configPath)
	if configPath == "" {
		return ""
	}
	return filepath.Base(filepath.Dir(configPath))
}

func pathJoin(elem ...string) string {
	parts := make([]string, 0, len(elem))
	for _, part := range elem {
		parts = append(parts, strings.TrimRight(strings.TrimSpace(part), "/"))
	}
	return strings.Join(parts, "/")
}

func remoteSlackAgentDir(workdir, agentName string) string {
	agentName = strings.TrimSpace(agentName)
	if agentName == "" {
		agentName = "default"
	}
	return pathJoin(workdir, "agents", agentName)
}

func slackServiceNameForAgent(agentName string) string {
	return slackServiceName(agentName)
}

func slackServiceUnitPathForAgent(agentName string) string {
	name := slackServiceNameForAgent(agentName)
	if name == "" {
		return ""
	}
	return pathJoin("/etc/systemd/system", name+".service")
}

func slackServiceName(agentName string) string {
	agentName = strings.ToLower(strings.TrimSpace(agentName))
	if agentName == "" {
		agentName = "default"
	}
	var b strings.Builder
	for _, r := range agentName {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	return "openclaw-slack-" + strings.Trim(b.String(), "-")
}

func ensureCodexCLI(ctx context.Context, exec host.Executor) error {
	if _, err := exec.Run(ctx, "sh", "-lc", "command -v codex >/dev/null 2>&1"); err == nil {
		return nil
	}

	if _, err := exec.Run(ctx, "sh", "-lc", "command -v npm >/dev/null 2>&1"); err == nil {
		if _, installErr := exec.Run(ctx, "npm", "install", "-g", "@openai/codex"); installErr == nil {
			if _, err := exec.Run(ctx, "sh", "-lc", "command -v codex >/dev/null 2>&1"); err == nil {
				return nil
			}
		}
	}

	if _, err := exec.Run(ctx, "sh", "-lc", "command -v brew >/dev/null 2>&1"); err == nil {
		if _, installErr := exec.Run(ctx, "brew", "install", "--cask", "codex"); installErr == nil {
			if _, err := exec.Run(ctx, "sh", "-lc", "command -v codex >/dev/null 2>&1"); err == nil {
				return nil
			}
		}
	}

	return errors.New("codex CLI is required on the EC2 host; install it with `npm install -g @openai/codex` or `brew install --cask codex`, then rerun openclaw slack deploy")
}
