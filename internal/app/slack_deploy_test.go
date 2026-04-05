package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"openclaw/internal/config"
	"openclaw/internal/host"
)

type slackDeployExecutor struct {
	results        map[string]host.CommandResult
	codexAvailable bool
	uploads        []uploadCall
}

type uploadCall struct {
	local  string
	remote string
}

func (f *slackDeployExecutor) Run(ctx context.Context, command string, args ...string) (host.CommandResult, error) {
	key := strings.TrimSpace(command + " " + strings.Join(args, " "))
	if key == "sh -lc command -v codex >/dev/null 2>&1" {
		if !f.codexAvailable {
			return host.CommandResult{}, errors.New("codex not found")
		}
		return host.CommandResult{}, nil
	}
	if key == "npm install -g @openai/codex" {
		f.codexAvailable = true
		return host.CommandResult{}, nil
	}
	if result, ok := f.results[key]; ok {
		return result, nil
	}
	return host.CommandResult{}, errors.New("unexpected command: " + key)
}

func (f *slackDeployExecutor) Upload(ctx context.Context, localPath, remotePath string) error {
	f.uploads = append(f.uploads, uploadCall{local: localPath, remote: remotePath})
	return nil
}

func TestRunSlackDeployWorkflowInstallsAgentService(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "agents", "alpha", "config.yaml")
	envPath := filepath.Join(dir, "agents", "alpha", ".env")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(configPath, []byte(strings.Join([]string{
		"runtime:",
		"  provider: codex",
		"  endpoint: https://nim.example.com",
		"  model: codex-pro",
		"infra:",
		"  instance_id: i-0123456789abcdef0",
		"slack:",
		"  runtime_url: http://203.0.113.10:8080",
		"  bot_user_id: UAGENT",
		"  allowed_channels:",
		"    - C123",
		"",
	}, "\n")), 0o600); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}
	if err := os.WriteFile(envPath, []byte(strings.Join([]string{
		"SLACK_BOT_TOKEN=xoxb-agent-token",
		"SLACK_APP_TOKEN=xapp-agent-token",
		"",
	}, "\n")), 0o600); err != nil {
		t.Fatalf("WriteFile(env) error = %v", err)
	}
	keyPath := filepath.Join(dir, "id_ed25519")
	if err := os.WriteFile(keyPath, []byte("ssh-key"), 0o600); err != nil {
		t.Fatalf("WriteFile(key) error = %v", err)
	}

	exec := &slackDeployExecutor{
		codexAvailable: true,
		results: map[string]host.CommandResult{
			"true": {},
			"sh -lc command -v codex >/dev/null 2>&1":                                                                    {},
			"sudo mkdir -p /opt/openclaw/agents/alpha":                                                                   {},
			"sudo chown -R ubuntu:ubuntu /opt/openclaw/agents/alpha":                                                     {},
			"sudo mv /opt/openclaw/agents/alpha/openclaw-slack.service /etc/systemd/system/openclaw-slack-alpha.service": {},
			"sudo chmod 600 /opt/openclaw/agents/alpha/.env":                                                             {},
			"sudo systemctl daemon-reload":                                                                               {},
			"sudo systemctl enable --now openclaw-slack-alpha.service":                                                   {},
		},
	}

	originalNewSSHExecutor := newSSHExecutor
	newSSHExecutor = func(cfg host.SSHConfig) host.Executor {
		return exec
	}
	defer func() { newSSHExecutor = originalNewSSHExecutor }()

	originalResolveSlackDeployTarget := resolveSlackDeployTarget
	resolveSlackDeployTarget = func(ctx context.Context, profile string, cfg *config.Config, target string) (string, error) {
		if target != "i-0123456789abcdef0" {
			t.Fatalf("target = %q, want instance id from config", target)
		}
		return "203.0.113.10", nil
	}
	defer func() { resolveSlackDeployTarget = originalResolveSlackDeployTarget }()

	resolvedTarget, err := runSlackDeployWorkflow(context.Background(), "dev", mustLoadConfig(t, configPath), mustLoadAgentEnv(t, envPath), slackDeployOptions{
		ConfigPath: configPath,
		SSHUser:    "ubuntu",
		SSHKey:     keyPath,
		SSHPort:    22,
		WorkingDir: "/opt/openclaw",
	})
	if err != nil {
		t.Fatalf("runSlackDeployWorkflow() error = %v", err)
	}
	if resolvedTarget != "203.0.113.10" {
		t.Fatalf("resolvedTarget = %q, want 203.0.113.10", resolvedTarget)
	}
	if len(exec.uploads) != 3 {
		t.Fatalf("uploads = %#v, want 3", exec.uploads)
	}
	if exec.uploads[0].remote != "/opt/openclaw/agents/alpha/config.yaml" {
		t.Fatalf("config upload remote = %q, want agent config path", exec.uploads[0].remote)
	}
	if exec.uploads[1].remote != "/opt/openclaw/agents/alpha/.env" {
		t.Fatalf("env upload remote = %q, want agent env path", exec.uploads[1].remote)
	}
	if exec.uploads[2].remote != "/opt/openclaw/agents/alpha/openclaw-slack.service" {
		t.Fatalf("unit upload remote = %q, want staged service path", exec.uploads[2].remote)
	}
	if _, ok := exec.results["sudo systemctl enable --now openclaw-slack-alpha.service"]; !ok {
		t.Fatal("expected slack service enable command to be executed")
	}
}

func TestRunSlackDeployWorkflowInstallsCodexCLIWhenMissing(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "agents", "alpha", "config.yaml")
	envPath := filepath.Join(dir, "agents", "alpha", ".env")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(configPath, []byte(strings.Join([]string{
		"runtime:",
		"  provider: codex",
		"  endpoint: https://nim.example.com",
		"  model: codex-pro",
		"infra:",
		"  instance_id: i-0123456789abcdef0",
		"slack:",
		"  runtime_url: http://203.0.113.10:8080",
		"",
	}, "\n")), 0o600); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}
	if err := os.WriteFile(envPath, []byte(strings.Join([]string{
		"SLACK_BOT_TOKEN=xoxb-agent-token",
		"SLACK_APP_TOKEN=xapp-agent-token",
		"",
	}, "\n")), 0o600); err != nil {
		t.Fatalf("WriteFile(env) error = %v", err)
	}
	keyPath := filepath.Join(dir, "id_ed25519")
	if err := os.WriteFile(keyPath, []byte("ssh-key"), 0o600); err != nil {
		t.Fatalf("WriteFile(key) error = %v", err)
	}

	exec := &slackDeployExecutor{
		results: map[string]host.CommandResult{
			"true":                                  {},
			"sh -lc command -v npm >/dev/null 2>&1": {},
			"sudo mkdir -p /opt/openclaw/agents/alpha":                                                                   {},
			"sudo chown -R ubuntu:ubuntu /opt/openclaw/agents/alpha":                                                     {},
			"sudo mv /opt/openclaw/agents/alpha/openclaw-slack.service /etc/systemd/system/openclaw-slack-alpha.service": {},
			"sudo chmod 600 /opt/openclaw/agents/alpha/.env":                                                             {},
			"sudo systemctl daemon-reload":                                                                               {},
			"sudo systemctl enable --now openclaw-slack-alpha.service":                                                   {},
		},
		codexAvailable: false,
	}

	originalNewSSHExecutor := newSSHExecutor
	newSSHExecutor = func(cfg host.SSHConfig) host.Executor {
		return exec
	}
	defer func() { newSSHExecutor = originalNewSSHExecutor }()

	originalResolveSlackDeployTarget := resolveSlackDeployTarget
	resolveSlackDeployTarget = func(ctx context.Context, profile string, cfg *config.Config, target string) (string, error) {
		return "203.0.113.10", nil
	}
	defer func() { resolveSlackDeployTarget = originalResolveSlackDeployTarget }()

	resolvedTarget, err := runSlackDeployWorkflow(context.Background(), "dev", mustLoadConfig(t, configPath), mustLoadAgentEnv(t, envPath), slackDeployOptions{
		ConfigPath: configPath,
		SSHUser:    "ubuntu",
		SSHKey:     keyPath,
		SSHPort:    22,
		WorkingDir: "/opt/openclaw",
	})
	if err != nil {
		t.Fatalf("runSlackDeployWorkflow() error = %v", err)
	}
	if resolvedTarget != "203.0.113.10" {
		t.Fatalf("resolvedTarget = %q, want 203.0.113.10", resolvedTarget)
	}
	if !exec.codexAvailable {
		t.Fatal("expected codex to be installed during deploy")
	}
}

func mustLoadConfig(t *testing.T, path string) *config.Config {
	t.Helper()
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("config.Load() error = %v", err)
	}
	return cfg
}

func mustLoadAgentEnv(t *testing.T, path string) map[string]string {
	t.Helper()
	env, err := loadAgentEnvFile(path)
	if err != nil {
		t.Fatalf("loadAgentEnvFile() error = %v", err)
	}
	return env
}
