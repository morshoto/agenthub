package app

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agenthub/internal/config"
	"agenthub/internal/host"
)

func TestRunSlackUndeployWorkflowRemovesAgentService(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "agents", "alpha", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	writeConfig(t, configPath, `
platform:
  name: aws
region:
  name: us-east-1
instance:
  type: g5.xlarge
  disk_size_gb: 40
image:
  name: ubuntu-24.04
runtime:
  provider: codex
  endpoint: https://nim.example.com
infra:
  instance_id: i-0123456789abcdef0
`)
	keyPath := filepath.Join(dir, "id_ed25519")
	if err := os.WriteFile(keyPath, []byte("ssh-key"), 0o600); err != nil {
		t.Fatalf("WriteFile(key) error = %v", err)
	}

	exec := &slackDeployExecutor{
		results: map[string]host.CommandResult{
			"true": {},
			"sh -lc set -eu\nunit=\"agenthub-slack-alpha.service\"\nif ! systemctl list-unit-files --full --no-legend \"$unit\" 2>/dev/null | awk '{print $1}' | grep -Fxq \"$unit\"; then\n  echo installed=false\n  exit 0\nfi\necho installed=true\nsystemctl show \"$unit\" --no-pager --property=LoadState --property=ActiveState --property=SubState --property=UnitFileState --property=FragmentPath": {
				Stdout: "installed=true\nLoadState=loaded\nActiveState=active\nSubState=running\nUnitFileState=enabled\nFragmentPath=/etc/systemd/system/agenthub-slack-alpha.service\n",
			},
			"sudo systemctl stop agenthub-slack-alpha.service":            {},
			"sudo systemctl disable agenthub-slack-alpha.service":         {},
			"sudo rm -f /etc/systemd/system/agenthub-slack-alpha.service": {},
			"sudo rm -rf /opt/agenthub/agents/alpha":                      {},
			"sudo systemctl daemon-reload":                                {},
		},
	}

	originalNewSSHExecutor := newSSHExecutor
	newSSHExecutor = func(cfg host.SSHConfig) host.Executor { return exec }
	defer func() { newSSHExecutor = originalNewSSHExecutor }()

	originalResolveSlackUndeployTarget := resolveSlackUndeployTarget
	resolveSlackUndeployTarget = func(ctx context.Context, profile string, cfg *config.Config, target string) (string, error) {
		if target != "i-0123456789abcdef0" {
			t.Fatalf("target = %q, want instance id from config", target)
		}
		return "203.0.113.10", nil
	}
	defer func() { resolveSlackUndeployTarget = originalResolveSlackUndeployTarget }()

	result, err := runSlackUndeployWorkflow(context.Background(), "dev", mustLoadConfig(t, configPath), slackUndeployOptions{
		ConfigPath: configPath,
		SSHUser:    "ubuntu",
		SSHKey:     keyPath,
		SSHPort:    22,
		WorkingDir: "/opt/agenthub",
	})
	if err != nil {
		t.Fatalf("runSlackUndeployWorkflow() error = %v", err)
	}
	if result.ResolvedTarget != "203.0.113.10" {
		t.Fatalf("resolvedTarget = %q, want 203.0.113.10", result.ResolvedTarget)
	}
	if result.Message != "slack integration removed" {
		t.Fatalf("result.Message = %q, want slack integration removed", result.Message)
	}
}

func TestRunSlackUndeployWorkflowSucceedsWhenServiceIsAlreadyAbsent(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "agents", "alpha", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	writeConfig(t, configPath, `
platform:
  name: aws
region:
  name: us-east-1
instance:
  type: g5.xlarge
  disk_size_gb: 40
image:
  name: ubuntu-24.04
runtime:
  provider: codex
  endpoint: https://nim.example.com
infra:
  instance_id: i-0123456789abcdef0
`)
	keyPath := filepath.Join(dir, "id_ed25519")
	if err := os.WriteFile(keyPath, []byte("ssh-key"), 0o600); err != nil {
		t.Fatalf("WriteFile(key) error = %v", err)
	}

	exec := &slackDeployExecutor{
		results: map[string]host.CommandResult{
			"true": {},
			"sh -lc set -eu\nunit=\"agenthub-slack-alpha.service\"\nif ! systemctl list-unit-files --full --no-legend \"$unit\" 2>/dev/null | awk '{print $1}' | grep -Fxq \"$unit\"; then\n  echo installed=false\n  exit 0\nfi\necho installed=true\nsystemctl show \"$unit\" --no-pager --property=LoadState --property=ActiveState --property=SubState --property=UnitFileState --property=FragmentPath": {
				Stdout: "installed=false\n",
			},
			"sudo rm -f /etc/systemd/system/agenthub-slack-alpha.service": {},
			"sudo rm -rf /opt/agenthub/agents/alpha":                      {},
			"sudo systemctl daemon-reload":                                {},
		},
	}

	originalNewSSHExecutor := newSSHExecutor
	newSSHExecutor = func(cfg host.SSHConfig) host.Executor { return exec }
	defer func() { newSSHExecutor = originalNewSSHExecutor }()

	originalResolveSlackUndeployTarget := resolveSlackUndeployTarget
	resolveSlackUndeployTarget = func(ctx context.Context, profile string, cfg *config.Config, target string) (string, error) {
		return "203.0.113.10", nil
	}
	defer func() { resolveSlackUndeployTarget = originalResolveSlackUndeployTarget }()

	result, err := runSlackUndeployWorkflow(context.Background(), "dev", mustLoadConfig(t, configPath), slackUndeployOptions{
		ConfigPath: configPath,
		SSHUser:    "ubuntu",
		SSHKey:     keyPath,
		SSHPort:    22,
		WorkingDir: "/opt/agenthub",
	})
	if err != nil {
		t.Fatalf("runSlackUndeployWorkflow() error = %v", err)
	}
	if result.Message != "slack integration was already absent" {
		t.Fatalf("result.Message = %q, want already absent", result.Message)
	}
}

func TestSlackUndeployCommandRunsEndToEndWorkflow(t *testing.T) {
	dir := t.TempDir()
	agentsDir := filepath.Join(dir, "agents")
	configPath := filepath.Join(agentsDir, "alpha", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	writeConfig(t, configPath, `
platform:
  name: aws
region:
  name: us-east-1
instance:
  type: g5.xlarge
  disk_size_gb: 40
image:
  name: ubuntu-24.04
runtime:
  provider: codex
  endpoint: https://nim.example.com
infra:
  instance_id: i-0123456789abcdef0
`)
	keyPath := filepath.Join(dir, "id_ed25519")
	if err := os.WriteFile(keyPath, []byte("ssh-key"), 0o600); err != nil {
		t.Fatalf("WriteFile(key) error = %v", err)
	}

	exec := &slackDeployExecutor{
		results: map[string]host.CommandResult{
			"true": {},
			"sh -lc set -eu\nunit=\"agenthub-slack-alpha.service\"\nif ! systemctl list-unit-files --full --no-legend \"$unit\" 2>/dev/null | awk '{print $1}' | grep -Fxq \"$unit\"; then\n  echo installed=false\n  exit 0\nfi\necho installed=true\nsystemctl show \"$unit\" --no-pager --property=LoadState --property=ActiveState --property=SubState --property=UnitFileState --property=FragmentPath": {
				Stdout: "installed=true\nLoadState=loaded\nActiveState=active\nSubState=running\nUnitFileState=enabled\nFragmentPath=/etc/systemd/system/agenthub-slack-alpha.service\n",
			},
			"sudo systemctl stop agenthub-slack-alpha.service":            {},
			"sudo systemctl disable agenthub-slack-alpha.service":         {},
			"sudo rm -f /etc/systemd/system/agenthub-slack-alpha.service": {},
			"sudo rm -rf /opt/agenthub/agents/alpha":                      {},
			"sudo systemctl daemon-reload":                                {},
		},
	}

	originalNewSSHExecutor := newSSHExecutor
	newSSHExecutor = func(cfg host.SSHConfig) host.Executor { return exec }
	defer func() { newSSHExecutor = originalNewSSHExecutor }()

	originalResolveSlackUndeployTarget := resolveSlackUndeployTarget
	resolveSlackUndeployTarget = func(ctx context.Context, profile string, cfg *config.Config, target string) (string, error) {
		return "203.0.113.10", nil
	}
	defer func() { resolveSlackUndeployTarget = originalResolveSlackUndeployTarget }()

	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()
	os.Args = []string{
		"agenthub",
		"--config", configPath,
		"slack",
		"undeploy",
		"--ssh-user", "ubuntu",
		"--ssh-key", keyPath,
		"--working-dir", "/opt/agenthub",
	}

	app := New()
	cmd := newRootCommand(app)
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	got := stdout.String()
	for _, fragment := range []string{
		"running slack undeploy workflow...",
		"result: slack integration removed",
		"service: agenthub-slack-alpha.service",
	} {
		if !strings.Contains(got, fragment) {
			t.Fatalf("stdout = %q, want %q", got, fragment)
		}
	}
}
