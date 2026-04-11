package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agenthub/internal/host"
	"agenthub/internal/provider"
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
				if result, ok := defaultFlexibleCommand(command, args...); ok {
					return result, nil
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
				if result, ok := defaultFlexibleCommand(command, args...); ok {
					return result, nil
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

func TestInspectCommandOutputsJSON(t *testing.T) {
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
				key := strings.TrimSpace(command + " " + strings.Join(args, " "))
				switch {
				case key == "cat /opt/agenthub/runtime.yaml":
					return host.CommandResult{Stdout: strings.TrimSpace(`
provider: codex
nim_endpoint: http://localhost:11434
model: llama3.2
port: 9090
region: us-west-2
use_nemoclaw: true
github:
  auth_mode: user
  token_secret_arn: arn:aws:secretsmanager:us-west-2:123456789012:secret:agenthub/github-token
sandbox:
  enabled: true
  network_mode: public
  filesystem_allow:
    - /tmp
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
				if result, ok := defaultFlexibleCommand(command, args...); ok {
					return result, nil
				}
				return host.CommandResult{}, errors.New("unexpected command: " + key)
			},
		}
	}
	t.Cleanup(func() { newSSHExecutor = oldExecutor })

	stdout, err := runInspectCommand(t, []string{"alpha", "--agents-dir", agentsDir, "--ssh-user", "ubuntu", "--ssh-key", keyPath, "--output", "json"})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	var payload inspectJSONResponse
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("Unmarshal() error = %v, stdout = %q", err, stdout)
	}
	if payload.Agent != "alpha" {
		t.Fatalf("agent = %q, want alpha", payload.Agent)
	}
	if payload.LocalConfig == nil || payload.LocalConfig.Config == nil || payload.LocalConfig.Config.Runtime == nil {
		t.Fatalf("local_config = %#v, want structured config", payload.LocalConfig)
	}
	if payload.LocalConfig.InfraInstanceID != "i-0123456789abcdef0" {
		t.Fatalf("infra_instance_id = %q, want populated value", payload.LocalConfig.InfraInstanceID)
	}
	if payload.Cloud.State != "available" || payload.Cloud.Instance == nil || payload.Cloud.Instance.ID != "i-0123456789abcdef0" {
		t.Fatalf("cloud = %#v, want available instance", payload.Cloud)
	}
	if payload.RemoteDeployment.SSHTarget != "203.0.113.10" {
		t.Fatalf("ssh_target = %q, want 203.0.113.10", payload.RemoteDeployment.SSHTarget)
	}
	if payload.RemoteDeployment.RuntimeConfig == nil || payload.RemoteDeployment.RuntimeConfig.UseNemoClaw != true || payload.RemoteDeployment.RuntimeConfig.Sandbox == nil || len(payload.RemoteDeployment.RuntimeConfig.Sandbox.FilesystemAllow) != 1 {
		t.Fatalf("runtime_config = %#v, want structured remote runtime config", payload.RemoteDeployment.RuntimeConfig)
	}
	if payload.RemoteDeployment.RuntimeService.Unit != "agenthub.service" || !payload.RemoteDeployment.RuntimeService.Installed {
		t.Fatalf("runtime_service = %#v, want installed runtime service", payload.RemoteDeployment.RuntimeService)
	}
	if payload.RemoteDeployment.IntegrationService.Unit != "agenthub-slack-alpha.service" || payload.RemoteDeployment.IntegrationService.Installed {
		t.Fatalf("integration_service = %#v, want not installed integration service", payload.RemoteDeployment.IntegrationService)
	}
	if !payload.RuntimeState.Health.Available || payload.RuntimeState.Health.Payload["status"] != "ok" {
		t.Fatalf("health = %#v, want available raw payload", payload.RuntimeState.Health)
	}
	if !payload.RuntimeState.Status.Available || payload.RuntimeState.Status.Payload["active_count"] != float64(1) || payload.RuntimeState.Status.Summary == nil || payload.RuntimeState.Status.Summary.ActiveCount != 1 {
		t.Fatalf("status = %#v, want raw payload and summary", payload.RuntimeState.Status)
	}
}

func TestInspectCommandOutputsJSONForPartialFailureAndReturnsError(t *testing.T) {
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
						return host.CommandResult{Stderr: "permission denied"}, errors.New("permission denied")
					case strings.Contains(script, "http://127.0.0.1:8080/healthz"):
						return host.CommandResult{Stderr: "connection refused"}, errors.New("connection refused")
					case strings.Contains(script, "http://127.0.0.1:8080/status"):
						return host.CommandResult{Stdout: `{"status":"ok","active":false,"active_count":0}`}, nil
					}
				}
				if result, ok := defaultFlexibleCommand(command, args...); ok {
					return result, nil
				}
				return host.CommandResult{}, errors.New("unexpected command: " + key)
			},
		}
	}
	t.Cleanup(func() { newSSHExecutor = oldExecutor })

	stdout, err := runInspectCommand(t, []string{"alpha", "--agents-dir", agentsDir, "--ssh-user", "ubuntu", "--ssh-key", keyPath, "--output", "json"})
	if err == nil {
		t.Fatal("Execute() error = nil, want partial remote failure")
	}
	if !strings.Contains(err.Error(), "runtime health") {
		t.Fatalf("error = %q, want runtime health failure", err)
	}

	var payload inspectJSONResponse
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("Unmarshal() error = %v, stdout = %q", err, stdout)
	}
	if payload.RuntimeState.Health.Available {
		t.Fatalf("health.available = true, want false")
	}
	if !strings.Contains(payload.RuntimeState.Health.Error, "connection refused") {
		t.Fatalf("health.error = %q, want connection refused", payload.RuntimeState.Health.Error)
	}
	if payload.RuntimeState.Status.Summary == nil || payload.RuntimeState.Status.Summary.ActiveCount != 0 {
		t.Fatalf("status.summary = %#v, want active_count=0", payload.RuntimeState.Status.Summary)
	}
	if !strings.Contains(strings.Join(payload.Warnings, "\n"), "integration service probe failed") {
		t.Fatalf("warnings = %#v, want integration service warning", payload.Warnings)
	}
	if !strings.Contains(payload.RemoteDeployment.IntegrationService.Error, "permission denied") {
		t.Fatalf("integration_service.error = %q, want permission denied", payload.RemoteDeployment.IntegrationService.Error)
	}
}

func TestInspectCommandRejectsUnsupportedOutputFormat(t *testing.T) {
	stdout, err := runInspectCommand(t, []string{"alpha", "--output", "xml"})
	if err == nil {
		t.Fatal("Execute() error = nil, want unsupported output format error")
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty output", stdout)
	}
	if !strings.Contains(err.Error(), "unsupported output format") {
		t.Fatalf("err = %q, want unsupported output format", err)
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
