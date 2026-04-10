package app

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agenthub/internal/host"
	"agenthub/internal/provider"
)

func TestRuntimeStartCommandStartsInactiveService(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agents", "alpha", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	writeConfig(t, path, `
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

	var startCalls int
	var inspectCalls int
	oldExecutor := newSSHExecutor
	newSSHExecutor = func(cfg host.SSHConfig) host.Executor {
		return flexibleExecutor{
			run: func(command string, args ...string) (host.CommandResult, error) {
				key := strings.TrimSpace(command + " " + strings.Join(args, " "))
				switch {
				case command == "sh" && len(args) >= 2 && args[0] == "-lc" && strings.Contains(args[1], `unit="agenthub.service"`):
					inspectCalls++
					if inspectCalls == 1 {
						return host.CommandResult{Stdout: "installed=true\nLoadState=loaded\nActiveState=inactive\nSubState=dead\nUnitFileState=enabled\nFragmentPath=/etc/systemd/system/agenthub.service\n"}, nil
					}
					return host.CommandResult{Stdout: "installed=true\nLoadState=loaded\nActiveState=active\nSubState=running\nUnitFileState=enabled\nFragmentPath=/etc/systemd/system/agenthub.service\n"}, nil
				case key == "sudo systemctl start agenthub.service":
					startCalls++
					return host.CommandResult{}, nil
				}
				if result, ok := defaultFlexibleCommand(command, args...); ok {
					return result, nil
				}
				return host.CommandResult{}, errors.New("unexpected command: " + key)
			},
		}
	}
	t.Cleanup(func() { newSSHExecutor = oldExecutor })

	stdout, err := runRuntimeStartCommand(t, []string{"--config", path, "--ssh-user", "ubuntu", "--ssh-key", keyPath})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if startCalls != 1 {
		t.Fatalf("startCalls = %d, want 1", startCalls)
	}
	for _, fragment := range []string{
		"starting runtime service...",
		"agent: alpha",
		"target: 203.0.113.10",
		"result: runtime service started",
		"runtime service: agenthub.service active=active sub=running enabled=enabled path=/etc/systemd/system/agenthub.service",
	} {
		if !strings.Contains(stdout, fragment) {
			t.Fatalf("stdout = %q, want %q", stdout, fragment)
		}
	}
}

func TestRuntimeStartCommandReportsNoOpWhenServiceAlreadyRunning(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agents", "alpha", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	writeConfig(t, path, `
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

	var startCalls int
	oldExecutor := newSSHExecutor
	newSSHExecutor = func(cfg host.SSHConfig) host.Executor {
		return flexibleExecutor{
			run: func(command string, args ...string) (host.CommandResult, error) {
				key := strings.TrimSpace(command + " " + strings.Join(args, " "))
				switch {
				case command == "sh" && len(args) >= 2 && args[0] == "-lc" && strings.Contains(args[1], `unit="agenthub.service"`):
					return host.CommandResult{Stdout: "installed=true\nLoadState=loaded\nActiveState=active\nSubState=running\nUnitFileState=enabled\nFragmentPath=/etc/systemd/system/agenthub.service\n"}, nil
				case key == "sudo systemctl start agenthub.service":
					startCalls++
					return host.CommandResult{}, nil
				}
				if result, ok := defaultFlexibleCommand(command, args...); ok {
					return result, nil
				}
				return host.CommandResult{}, errors.New("unexpected command: " + key)
			},
		}
	}
	t.Cleanup(func() { newSSHExecutor = oldExecutor })

	stdout, err := runRuntimeStartCommand(t, []string{"--config", path, "--ssh-user", "ubuntu", "--ssh-key", keyPath})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if startCalls != 0 {
		t.Fatalf("startCalls = %d, want 0", startCalls)
	}
	if !strings.Contains(stdout, "result: runtime service is already running") {
		t.Fatalf("stdout = %q, want already-running message", stdout)
	}
}

func TestRuntimeStopCommandStopsActiveService(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agents", "alpha", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	writeConfig(t, path, `
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

	var stopCalls int
	var inspectCalls int
	oldExecutor := newSSHExecutor
	newSSHExecutor = func(cfg host.SSHConfig) host.Executor {
		return flexibleExecutor{
			run: func(command string, args ...string) (host.CommandResult, error) {
				key := strings.TrimSpace(command + " " + strings.Join(args, " "))
				switch {
				case command == "sh" && len(args) >= 2 && args[0] == "-lc" && strings.Contains(args[1], `unit="agenthub.service"`):
					inspectCalls++
					if inspectCalls == 1 {
						return host.CommandResult{Stdout: "installed=true\nLoadState=loaded\nActiveState=active\nSubState=running\nUnitFileState=enabled\nFragmentPath=/etc/systemd/system/agenthub.service\n"}, nil
					}
					return host.CommandResult{Stdout: "installed=true\nLoadState=loaded\nActiveState=inactive\nSubState=dead\nUnitFileState=enabled\nFragmentPath=/etc/systemd/system/agenthub.service\n"}, nil
				case key == "sudo systemctl stop agenthub.service":
					stopCalls++
					return host.CommandResult{}, nil
				}
				if result, ok := defaultFlexibleCommand(command, args...); ok {
					return result, nil
				}
				return host.CommandResult{}, errors.New("unexpected command: " + key)
			},
		}
	}
	t.Cleanup(func() { newSSHExecutor = oldExecutor })

	stdout, err := runRuntimeStopCommand(t, []string{"--config", path, "--ssh-user", "ubuntu", "--ssh-key", keyPath})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if stopCalls != 1 {
		t.Fatalf("stopCalls = %d, want 1", stopCalls)
	}
	for _, fragment := range []string{
		"stopping runtime service...",
		"agent: alpha",
		"target: 203.0.113.10",
		"result: runtime service stopped",
		"runtime service: agenthub.service active=inactive sub=dead enabled=enabled path=/etc/systemd/system/agenthub.service",
	} {
		if !strings.Contains(stdout, fragment) {
			t.Fatalf("stdout = %q, want %q", stdout, fragment)
		}
	}
}

func TestRuntimeStopCommandReportsNoOpWhenServiceAlreadyStopped(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agents", "alpha", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	writeConfig(t, path, `
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

	var stopCalls int
	oldExecutor := newSSHExecutor
	newSSHExecutor = func(cfg host.SSHConfig) host.Executor {
		return flexibleExecutor{
			run: func(command string, args ...string) (host.CommandResult, error) {
				key := strings.TrimSpace(command + " " + strings.Join(args, " "))
				switch {
				case command == "sh" && len(args) >= 2 && args[0] == "-lc" && strings.Contains(args[1], `unit="agenthub.service"`):
					return host.CommandResult{Stdout: "installed=true\nLoadState=loaded\nActiveState=inactive\nSubState=dead\nUnitFileState=enabled\nFragmentPath=/etc/systemd/system/agenthub.service\n"}, nil
				case key == "sudo systemctl stop agenthub.service":
					stopCalls++
					return host.CommandResult{}, nil
				}
				if result, ok := defaultFlexibleCommand(command, args...); ok {
					return result, nil
				}
				return host.CommandResult{}, errors.New("unexpected command: " + key)
			},
		}
	}
	t.Cleanup(func() { newSSHExecutor = oldExecutor })

	stdout, err := runRuntimeStopCommand(t, []string{"--config", path, "--ssh-user", "ubuntu", "--ssh-key", keyPath})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if stopCalls != 0 {
		t.Fatalf("stopCalls = %d, want 0", stopCalls)
	}
	if !strings.Contains(stdout, "result: runtime service is already stopped") {
		t.Fatalf("stdout = %q, want already-stopped message", stdout)
	}
}

func TestRuntimeStopCommandFailsWhenServiceMissing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agents", "alpha", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	writeConfig(t, path, `
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
				if command == "sh" && len(args) >= 2 && args[0] == "-lc" && strings.Contains(args[1], `unit="agenthub.service"`) {
					return host.CommandResult{Stdout: "installed=false\n"}, nil
				}
				if result, ok := defaultFlexibleCommand(command, args...); ok {
					return result, nil
				}
				return host.CommandResult{}, errors.New("unexpected command: " + key)
			},
		}
	}
	t.Cleanup(func() { newSSHExecutor = oldExecutor })

	_, err := runRuntimeStopCommand(t, []string{"--config", path, "--ssh-user", "ubuntu", "--ssh-key", keyPath})
	if err == nil {
		t.Fatal("Execute() error = nil")
	}
	if !strings.Contains(err.Error(), "agenthub.service is not installed") {
		t.Fatalf("error = %q, want missing service error", err)
	}
}

func TestRuntimeStartCommandFailsWhenServiceMissing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agents", "alpha", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	writeConfig(t, path, `
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
				if command == "sh" && len(args) >= 2 && args[0] == "-lc" && strings.Contains(args[1], `unit="agenthub.service"`) {
					return host.CommandResult{Stdout: "installed=false\n"}, nil
				}
				if result, ok := defaultFlexibleCommand(command, args...); ok {
					return result, nil
				}
				return host.CommandResult{}, errors.New("unexpected command: " + key)
			},
		}
	}
	t.Cleanup(func() { newSSHExecutor = oldExecutor })

	_, err := runRuntimeStartCommand(t, []string{"--config", path, "--ssh-user", "ubuntu", "--ssh-key", keyPath})
	if err == nil {
		t.Fatal("Execute() error = nil")
	}
	if !strings.Contains(err.Error(), "agenthub.service is not installed") {
		t.Fatalf("error = %q, want missing service error", err)
	}
}

func TestRunRuntimeServiceWorkflowFailsWhenServiceDoesNotBecomeActive(t *testing.T) {
	cfg := mustLoadConfigFromString(t, `
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
ssh:
  user: ubuntu
sandbox:
  enabled: false
`)

	keyPath := filepath.Join(t.TempDir(), "id_ed25519")
	if err := os.WriteFile(keyPath, []byte("test-key"), 0o600); err != nil {
		t.Fatalf("WriteFile(key) error = %v", err)
	}
	cfg.SSH.PrivateKeyPath = keyPath

	oldProvider := newAWSProvider
	newAWSProvider = func(profile, computeClass string) provider.CloudProvider {
		return fakeStatusCloudProvider{}
	}
	t.Cleanup(func() { newAWSProvider = oldProvider })

	var inspectCalls int
	oldExecutor := newSSHExecutor
	newSSHExecutor = func(cfg host.SSHConfig) host.Executor {
		return flexibleExecutor{
			run: func(command string, args ...string) (host.CommandResult, error) {
				key := strings.TrimSpace(command + " " + strings.Join(args, " "))
				switch {
				case command == "sh" && len(args) >= 2 && args[0] == "-lc" && strings.Contains(args[1], `unit="agenthub.service"`):
					inspectCalls++
					if inspectCalls == 1 {
						return host.CommandResult{Stdout: "installed=true\nLoadState=loaded\nActiveState=inactive\nSubState=dead\nUnitFileState=enabled\nFragmentPath=/etc/systemd/system/agenthub.service\n"}, nil
					}
					return host.CommandResult{Stdout: "installed=true\nLoadState=loaded\nActiveState=failed\nSubState=failed\nUnitFileState=enabled\nFragmentPath=/etc/systemd/system/agenthub.service\n"}, nil
				case key == "sudo systemctl start agenthub.service":
					return host.CommandResult{}, nil
				}
				if result, ok := defaultFlexibleCommand(command, args...); ok {
					return result, nil
				}
				return host.CommandResult{}, errors.New("unexpected command: " + key)
			},
		}
	}
	t.Cleanup(func() { newSSHExecutor = oldExecutor })

	_, err := runRuntimeServiceWorkflow(context.Background(), "", cfg, runtimeServiceOptions{
		ConfigPath: filepath.Join("agents", "alpha", "config.yaml"),
		Action:     "start",
	})
	if err == nil {
		t.Fatal("runRuntimeServiceWorkflow() error = nil")
	}
	if !strings.Contains(err.Error(), "did not reach expected state after start") {
		t.Fatalf("error = %q, want post-start verification failure", err)
	}
}

func TestRunRuntimeServiceWorkflowFailsWhenServiceDoesNotStop(t *testing.T) {
	cfg := mustLoadConfigFromString(t, `
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
ssh:
  user: ubuntu
sandbox:
  enabled: false
`)

	keyPath := filepath.Join(t.TempDir(), "id_ed25519")
	if err := os.WriteFile(keyPath, []byte("test-key"), 0o600); err != nil {
		t.Fatalf("WriteFile(key) error = %v", err)
	}
	cfg.SSH.PrivateKeyPath = keyPath

	oldProvider := newAWSProvider
	newAWSProvider = func(profile, computeClass string) provider.CloudProvider {
		return fakeStatusCloudProvider{}
	}
	t.Cleanup(func() { newAWSProvider = oldProvider })

	var inspectCalls int
	oldExecutor := newSSHExecutor
	newSSHExecutor = func(cfg host.SSHConfig) host.Executor {
		return flexibleExecutor{
			run: func(command string, args ...string) (host.CommandResult, error) {
				key := strings.TrimSpace(command + " " + strings.Join(args, " "))
				switch {
				case command == "sh" && len(args) >= 2 && args[0] == "-lc" && strings.Contains(args[1], `unit="agenthub.service"`):
					inspectCalls++
					if inspectCalls == 1 {
						return host.CommandResult{Stdout: "installed=true\nLoadState=loaded\nActiveState=active\nSubState=running\nUnitFileState=enabled\nFragmentPath=/etc/systemd/system/agenthub.service\n"}, nil
					}
					return host.CommandResult{Stdout: "installed=true\nLoadState=loaded\nActiveState=active\nSubState=running\nUnitFileState=enabled\nFragmentPath=/etc/systemd/system/agenthub.service\n"}, nil
				case key == "sudo systemctl stop agenthub.service":
					return host.CommandResult{}, nil
				}
				if result, ok := defaultFlexibleCommand(command, args...); ok {
					return result, nil
				}
				return host.CommandResult{}, errors.New("unexpected command: " + key)
			},
		}
	}
	t.Cleanup(func() { newSSHExecutor = oldExecutor })

	_, err := runRuntimeServiceWorkflow(context.Background(), "", cfg, runtimeServiceOptions{
		ConfigPath: filepath.Join("agents", "alpha", "config.yaml"),
		Action:     "stop",
	})
	if err == nil {
		t.Fatal("runRuntimeServiceWorkflow() error = nil")
	}
	if !strings.Contains(err.Error(), "did not reach expected state after stop") {
		t.Fatalf("error = %q, want post-stop verification failure", err)
	}
}

func runRuntimeStartCommand(t *testing.T, args []string) (string, error) {
	t.Helper()

	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()
	os.Args = append([]string{"agenthub", "runtime", "start"}, args...)

	return runRuntimeCommand(t)
}

func runRuntimeStopCommand(t *testing.T, args []string) (string, error) {
	t.Helper()

	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()
	os.Args = append([]string{"agenthub", "runtime", "stop"}, args...)

	return runRuntimeCommand(t)
}

func runRuntimeCommand(t *testing.T) (string, error) {
	t.Helper()

	app := New()
	cmd := newRootCommand(app)
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)

	err := cmd.Execute()
	return stdout.String(), err
}
