package app

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agenthub/internal/host"
)

func TestInspectCommandReportsDetailedDeployedState(t *testing.T) {
	agentsDir := filepath.Join(t.TempDir(), "agents")
	agentDir := filepath.Join(agentsDir, "alpha")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(agent) error = %v", err)
	}

	writeConfig(t, filepath.Join(agentDir, "config.yaml"), `
platform:
  name: aws
compute:
  class: gpu
region:
  name: us-west-2
instance:
  type: g5.xlarge
  disk_size_gb: 40
image:
  name: ubuntu-24.04
runtime:
  endpoint: http://localhost:11434
  model: llama3.2
  provider: codex
  port: 9090
slack:
  runtime_url: http://203.0.113.10:9090
github:
  auth_mode: user
  token_secret_arn: arn:aws:secretsmanager:us-west-2:123456789012:secret:agenthub/github-token
infra:
  instance_id: i-0123456789abcdef0
sandbox:
  enabled: true
  network_mode: public
  use_nemoclaw: false
`)

	keyPath := filepath.Join(t.TempDir(), "id_ed25519")
	if err := os.WriteFile(keyPath, []byte("test-key"), 0o600); err != nil {
		t.Fatalf("WriteFile(key) error = %v", err)
	}

	oldProvider := newAWSProvider
	newAWSProvider = func(profile, computeClass string) provider.CloudProvider {
		return fakeStatusCloudProvider{}
	}
	t.Cleanup(func() { newAWSProvider = oldProvider })

	oldExecutor := newSSHExecutor
	newSSHExecutor = func(cfg host.SSHConfig) host.Executor {
		return flexibleExecutor{
			run: func(command string, args ...string) (host.CommandResult, error) {
				if result, ok := defaultFlexibleCommand(command, args...); ok {
					return result, nil
				}
				key := strings.TrimSpace(command + " " + strings.Join(args, " "))
				switch {
				case key == "cat /opt/agenthub/runtime.yaml":
					return host.CommandResult{Stdout: strings.TrimSpace(`
provider: codex
nim_endpoint: http://localhost:11434
model: llama3.2
port: 9090
region: us-west-2
github:
  auth_mode: user
  token_secret_arn: arn:aws:secretsmanager:us-west-2:123456789012:secret:agenthub/github-token
sandbox:
  enabled: true
  network_mode: public
`)}, nil
				case command == "sh" && len(args) >= 2 && args[0] == "-lc":
					script := args[1]
					switch {
					case strings.Contains(script, `unit="agenthub.service"`):
						return host.CommandResult{Stdout: strings.Join([]string{
							"installed=true",
							"LoadState=loaded",
							"ActiveState=active",
							"SubState=running",
							"UnitFileState=enabled",
							"FragmentPath=/etc/systemd/system/agenthub.service",
						}, "\n")}, nil
					case strings.Contains(script, `unit="agenthub-slack-alpha.service"`):
						return host.CommandResult{Stdout: "installed=false\n"}, nil
					case strings.Contains(script, "http://127.0.0.1:9090/healthz"):
						return host.CommandResult{Stdout: `{"status":"ok","provider":"codex","model":"llama3.2","configured_port":9090,"workspace_root":"/opt/agenthub/workspace","runtime_config":"/opt/agenthub/runtime.yaml"}`}, nil
					case strings.Contains(script, "http://127.0.0.1:9090/status"):
						return host.CommandResult{Stdout: `{"status":"ok","active":true,"active_count":1,"active_agents":[{"id":"agent-1","task":"executing pwd"}]}`}, nil
					}
				}
				return host.CommandResult{}, errors.New("unexpected command: " + key)
			},
		}
	}
	t.Cleanup(func() { newSSHExecutor = oldExecutor })

	stdout, err := runInspectCommand(t, []string{"alpha", "--agents-dir", agentsDir, "--ssh-user", "ubuntu", "--ssh-key", keyPath})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	for _, fragment := range []string{
		"agent inspect",
		"agent: alpha",
		"local config",
		"infra.instance_id: i-0123456789abcdef0",
		"slack.runtime_url: http://203.0.113.10:9090",
		"cloud state",
		"instance-id: i-0123456789abcdef0",
		"remote deployment",
		"ssh target: 203.0.113.10",
		"remote runtime config: provider=codex endpoint=http://localhost:11434 model=llama3.2 port=9090 region=us-west-2 sandbox.enabled=true sandbox.network=public github.auth_mode=user",
		"runtime service: agenthub.service active=active sub=running enabled=enabled path=/etc/systemd/system/agenthub.service",
		"integration service: not installed (agenthub-slack-alpha.service)",
		"runtime state",
		"health: status=ok provider=codex model=llama3.2 configured_port=9090 workspace_root=/opt/agenthub/workspace runtime_config=/opt/agenthub/runtime.yaml",
		"status: active=true active_count=1",
		"active-agent: id=agent-1 task=executing pwd",
	} {
		if !strings.Contains(stdout, fragment) {
			t.Fatalf("stdout = %q, want %q", stdout, fragment)
		}
	}
}

func TestInspectCommandReturnsPartialOutputWhenRemoteHealthFails(t *testing.T) {
	agentsDir := filepath.Join(t.TempDir(), "agents")
	agentDir := filepath.Join(agentsDir, "alpha")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(agent) error = %v", err)
	}

	writeConfig(t, filepath.Join(agentDir, "config.yaml"), `
platform:
  name: aws
region:
  name: us-west-2
instance:
  type: g5.xlarge
  disk_size_gb: 40
image:
  name: ubuntu-24.04
runtime:
  endpoint: http://localhost:11434
  model: llama3.2
infra:
  instance_id: i-0123456789abcdef0
sandbox:
  enabled: false
`)

	keyPath := filepath.Join(t.TempDir(), "id_ed25519")
	if err := os.WriteFile(keyPath, []byte("test-key"), 0o600); err != nil {
		t.Fatalf("WriteFile(key) error = %v", err)
	}

	oldProvider := newAWSProvider
	newAWSProvider = func(profile, computeClass string) provider.CloudProvider {
		return fakeStatusCloudProvider{}
	}
	t.Cleanup(func() { newAWSProvider = oldProvider })

	oldExecutor := newSSHExecutor
	newSSHExecutor = func(cfg host.SSHConfig) host.Executor {
		return flexibleExecutor{
			run: func(command string, args ...string) (host.CommandResult, error) {
				if result, ok := defaultFlexibleCommand(command, args...); ok {
					return result, nil
				}
				key := strings.TrimSpace(command + " " + strings.Join(args, " "))
				switch {
				case key == "cat /opt/agenthub/runtime.yaml":
					return host.CommandResult{Stdout: "provider: ollama\nnim_endpoint: http://localhost:11434\nmodel: llama3.2\nport: 8080\nsandbox:\n  enabled: false\n"}, nil
				case command == "sh" && len(args) >= 2 && args[0] == "-lc":
					script := args[1]
					switch {
					case strings.Contains(script, `unit="agenthub.service"`):
						return host.CommandResult{Stdout: "installed=true\nLoadState=loaded\nActiveState=active\nSubState=running\nUnitFileState=enabled\nFragmentPath=/etc/systemd/system/agenthub.service\n"}, nil
					case strings.Contains(script, `unit="agenthub-slack-alpha.service"`):
						return host.CommandResult{Stdout: "installed=false\n"}, nil
					case strings.Contains(script, "http://127.0.0.1:8080/healthz"):
						return host.CommandResult{Stderr: "connection refused"}, errors.New("connection refused")
					case strings.Contains(script, "http://127.0.0.1:8080/status"):
						return host.CommandResult{Stdout: `{"status":"ok","active":false,"active_count":0}`}, nil
					}
				}
				return host.CommandResult{}, errors.New("unexpected command: " + key)
			},
		}
	}
	t.Cleanup(func() { newSSHExecutor = oldExecutor })

	stdout, err := runInspectCommand(t, []string{"alpha", "--agents-dir", agentsDir, "--ssh-user", "ubuntu", "--ssh-key", keyPath})
	if err == nil {
		t.Fatal("Execute() error = nil, want partial remote failure")
	}
	if !strings.Contains(err.Error(), "runtime health") {
		t.Fatalf("error = %q, want runtime health failure", err)
	}
	for _, fragment := range []string{
		"agent inspect",
		"remote runtime config: provider=ollama endpoint=http://localhost:11434 model=llama3.2 port=8080 sandbox.enabled=false",
		"runtime service: agenthub.service active=active sub=running enabled=enabled path=/etc/systemd/system/agenthub.service",
		"health: unavailable: connection refused",
		"status: active=false active_count=0",
	} {
		if !strings.Contains(stdout, fragment) {
			t.Fatalf("stdout = %q, want %q", stdout, fragment)
		}
	}
}

func runInspectCommand(t *testing.T, args []string) (string, error) {
	t.Helper()

	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()
	os.Args = append([]string{"agenthub", "inspect"}, args...)

	app := New()
	cmd := newRootCommand(app)
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)

	err := cmd.Execute()
	return stdout.String(), err
}
