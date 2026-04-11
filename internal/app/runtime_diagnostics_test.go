package app

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agenthub/internal/host"
	"agenthub/internal/provider"
)

func TestRuntimeDiagnosticsCommandCreatesArchive(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agents", "alpha", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	keyPath := filepath.Join(dir, "id_ed25519")
	if err := os.WriteFile(keyPath, []byte("test-key"), 0o600); err != nil {
		t.Fatalf("WriteFile(key) error = %v", err)
	}
	writeConfig(t, path, `
platform:
  name: aws
region:
  name: us-west-2
ssh:
  user: ubuntu
  private_key_path: `+keyPath+`
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

	oldNow := diagnosticsNowFunc
	diagnosticsNowFunc = func() time.Time {
		return time.Date(2026, 4, 11, 10, 20, 30, 0, time.UTC)
	}
	t.Cleanup(func() { diagnosticsNowFunc = oldNow })

	oldProvider := newAWSProvider
	newAWSProvider = func(profile, computeClass string) provider.CloudProvider {
		return fakeStatusCloudProvider{}
	}
	t.Cleanup(func() { newAWSProvider = oldProvider })

	oldExecutor := newSSHExecutor
	newSSHExecutor = func(cfg host.SSHConfig) host.Executor {
		return flexibleExecutor{
			run: func(command string, args ...string) (host.CommandResult, error) {
				switch {
				case command == "cat" && len(args) == 1 && args[0] == "/opt/agenthub/runtime.yaml":
					return host.CommandResult{Stdout: "provider: openai\nmodel: gpt-5.4\nport: 8080\nsandbox:\n  enabled: false\n"}, nil
				case command == "sh" && len(args) >= 2 && args[0] == "-lc" && strings.Contains(args[1], `unit="agenthub.service"`):
					return host.CommandResult{Stdout: "installed=true\nLoadState=loaded\nActiveState=active\nSubState=running\nUnitFileState=enabled\nFragmentPath=/etc/systemd/system/agenthub.service\n"}, nil
				case command == "sh" && len(args) >= 2 && args[0] == "-lc" && strings.Contains(args[1], `unit="agenthub-slack-alpha.service"`):
					return host.CommandResult{Stdout: "installed=true\nLoadState=loaded\nActiveState=active\nSubState=running\nUnitFileState=enabled\nFragmentPath=/etc/systemd/system/agenthub-slack-alpha.service\n"}, nil
				case command == "sh" && len(args) >= 2 && args[0] == "-lc" && strings.Contains(args[1], "http://127.0.0.1:8080/healthz"):
					return host.CommandResult{Stdout: `{"status":"ok","provider":"openai"}`}, nil
				case command == "sh" && len(args) >= 2 && args[0] == "-lc" && strings.Contains(args[1], "http://127.0.0.1:8080/status"):
					return host.CommandResult{Stdout: `{"status":"ok","active":true,"active_count":1,"active_agents":[{"id":"agent-1","task":"triage"}]}`}, nil
				case command == "sudo" && len(args) >= 3 && args[0] == "sh" && args[1] == "-lc" && strings.Contains(args[2], `unit="agenthub.service"`):
					return host.CommandResult{Stdout: "2026-04-10T12:00:00Z runtime started\n"}, nil
				case command == "sudo" && len(args) >= 3 && args[0] == "sh" && args[1] == "-lc" && strings.Contains(args[2], `unit="agenthub-slack-alpha.service"`):
					return host.CommandResult{Stdout: "2026-04-10T12:00:02Z slack connected\n"}, nil
				}
				if result, ok := defaultFlexibleCommand(command, args...); ok {
					return result, nil
				}
				return host.CommandResult{}, errors.New("unexpected command: " + command + " " + strings.Join(args, " "))
			},
		}
	}
	t.Cleanup(func() { newSSHExecutor = oldExecutor })

	outputPath := filepath.Join(dir, "alpha-diagnostics.tar.gz")
	stdout, err := runRuntimeDiagnosticsCommand(t, []string{"--config", path, "--ssh-user", "ubuntu", "--ssh-key", keyPath, "--output", outputPath})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	for _, fragment := range []string{
		"collecting diagnostics bundle...",
		"agent: alpha",
		"target: 203.0.113.10",
		"bundle: " + outputPath,
		"runtime-health",
		"runtime-logs",
	} {
		if !strings.Contains(stdout, fragment) {
			t.Fatalf("stdout = %q, want %q", stdout, fragment)
		}
	}

	entries := readTarGzEntries(t, outputPath)
	for _, name := range []string{
		"README.txt",
		"cloud.json",
		"local-config.yaml",
		"logs/integration.log",
		"logs/runtime.log",
		"manifest.json",
		"remote/runtime-config.yaml",
		"remote/runtime-health.json",
		"remote/runtime-status.json",
		"remote/services.json",
	} {
		if _, ok := entries[name]; !ok {
			t.Fatalf("archive missing %s", name)
		}
	}

	var manifest diagnosticsManifest
	if err := json.Unmarshal(entries["manifest.json"], &manifest); err != nil {
		t.Fatalf("Unmarshal(manifest) error = %v", err)
	}
	if manifest.Agent != "alpha" {
		t.Fatalf("manifest.Agent = %q, want alpha", manifest.Agent)
	}
	if manifest.GeneratedAt != "2026-04-11T10:20:30Z" {
		t.Fatalf("manifest.GeneratedAt = %q, want fixed timestamp", manifest.GeneratedAt)
	}
	if len(manifest.Warnings) != 0 {
		t.Fatalf("manifest.Warnings = %v, want none", manifest.Warnings)
	}
}

func TestRuntimeDiagnosticsCommandWritesWarningsForOptionalSections(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agents", "alpha", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	keyPath := filepath.Join(dir, "id_ed25519")
	if err := os.WriteFile(keyPath, []byte("test-key"), 0o600); err != nil {
		t.Fatalf("WriteFile(key) error = %v", err)
	}
	writeConfig(t, path, `
platform:
  name: aws
region:
  name: us-west-2
ssh:
  user: ubuntu
  private_key_path: `+keyPath+`
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

	oldNow := diagnosticsNowFunc
	diagnosticsNowFunc = func() time.Time {
		return time.Date(2026, 4, 11, 10, 20, 30, 0, time.UTC)
	}
	t.Cleanup(func() { diagnosticsNowFunc = oldNow })

	oldProvider := newAWSProvider
	newAWSProvider = func(profile, computeClass string) provider.CloudProvider {
		return fakeStatusCloudProvider{}
	}
	t.Cleanup(func() { newAWSProvider = oldProvider })

	oldExecutor := newSSHExecutor
	newSSHExecutor = func(cfg host.SSHConfig) host.Executor {
		return flexibleExecutor{
			run: func(command string, args ...string) (host.CommandResult, error) {
				switch {
				case command == "cat" && len(args) == 1 && args[0] == "/opt/agenthub/runtime.yaml":
					return host.CommandResult{Stdout: "provider: openai\nmodel: gpt-5.4\nport: 8080\nsandbox:\n  enabled: false\n"}, nil
				case command == "sh" && len(args) >= 2 && args[0] == "-lc" && strings.Contains(args[1], `unit="agenthub.service"`):
					return host.CommandResult{Stdout: "installed=true\nLoadState=loaded\nActiveState=active\nSubState=running\nUnitFileState=enabled\nFragmentPath=/etc/systemd/system/agenthub.service\n"}, nil
				case command == "sh" && len(args) >= 2 && args[0] == "-lc" && strings.Contains(args[1], `unit="agenthub-slack-alpha.service"`):
					return host.CommandResult{Stderr: "systemctl unavailable"}, errors.New("probe failed")
				case command == "sh" && len(args) >= 2 && args[0] == "-lc" && strings.Contains(args[1], "http://127.0.0.1:8080/healthz"):
					return host.CommandResult{Stderr: "curl: (7) connection refused"}, errors.New("health failed")
				case command == "sh" && len(args) >= 2 && args[0] == "-lc" && strings.Contains(args[1], "http://127.0.0.1:8080/status"):
					return host.CommandResult{Stdout: `{"status":"ok","active":false,"active_count":0}`}, nil
				case command == "sudo" && len(args) >= 3 && args[0] == "sh" && args[1] == "-lc" && strings.Contains(args[2], `unit="agenthub.service"`):
					return host.CommandResult{Stdout: "2026-04-10T12:00:00Z runtime started\n"}, nil
				case command == "sudo" && len(args) >= 3 && args[0] == "sh" && args[1] == "-lc" && strings.Contains(args[2], `unit="agenthub-slack-alpha.service"`):
					return host.CommandResult{Stderr: "service unit agenthub-slack-alpha.service is not installed on the target host"}, errors.New("missing unit")
				}
				if result, ok := defaultFlexibleCommand(command, args...); ok {
					return result, nil
				}
				return host.CommandResult{}, errors.New("unexpected command: " + command + " " + strings.Join(args, " "))
			},
		}
	}
	t.Cleanup(func() { newSSHExecutor = oldExecutor })

	outputPath := filepath.Join(dir, "alpha-diagnostics.tar.gz")
	stdout, err := runRuntimeDiagnosticsCommand(t, []string{"--config", path, "--ssh-user", "ubuntu", "--ssh-key", keyPath, "--output", outputPath})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	for _, fragment := range []string{
		"warning: integration service probe failed: systemctl unavailable",
		"warning: runtime health unavailable: curl: (7) connection refused",
		"warning: integration logs unavailable: service unit agenthub-slack-alpha.service is not installed on the target host",
	} {
		if !strings.Contains(stdout, fragment) {
			t.Fatalf("stdout = %q, want %q", stdout, fragment)
		}
	}

	entries := readTarGzEntries(t, outputPath)
	if _, ok := entries["remote/runtime-health.json"]; ok {
		t.Fatal("archive unexpectedly contains runtime-health.json")
	}
	if _, ok := entries["logs/integration.log"]; ok {
		t.Fatal("archive unexpectedly contains integration.log")
	}

	var manifest diagnosticsManifest
	if err := json.Unmarshal(entries["manifest.json"], &manifest); err != nil {
		t.Fatalf("Unmarshal(manifest) error = %v", err)
	}
	if len(manifest.Warnings) != 3 {
		t.Fatalf("manifest.Warnings = %v, want 3 warnings", manifest.Warnings)
	}
}

func TestRuntimeDiagnosticsCommandRejectsInvalidLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agents", "alpha", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	keyPath := filepath.Join(dir, "id_ed25519")
	if err := os.WriteFile(keyPath, []byte("test-key"), 0o600); err != nil {
		t.Fatalf("WriteFile(key) error = %v", err)
	}
	writeConfig(t, path, `
platform:
  name: aws
region:
  name: us-west-2
ssh:
  user: ubuntu
  private_key_path: `+keyPath+`
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

	stdout, err := runRuntimeDiagnosticsCommand(t, []string{"--config", path, "--ssh-key", keyPath, "--lines", "0"})
	if err == nil {
		t.Fatal("Execute() error = nil")
	}
	if !strings.Contains(err.Error(), "lines must be greater than 0") {
		t.Fatalf("error = %q, want lines validation", err)
	}
	if strings.Contains(stdout, "Usage:") {
		t.Fatalf("stdout = %q, did not expect usage output", stdout)
	}
}

func runRuntimeDiagnosticsCommand(t *testing.T, args []string) (string, error) {
	t.Helper()

	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()
	os.Args = append([]string{"agenthub", "runtime", "diagnostics"}, args...)

	return runRuntimeCommand(t)
}

func readTarGzEntries(t *testing.T, path string) map[string][]byte {
	t.Helper()

	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("Open(%s) error = %v", path, err)
	}
	defer file.Close()

	gzr, err := gzip.NewReader(file)
	if err != nil {
		t.Fatalf("gzip.NewReader(%s) error = %v", path, err)
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)
	entries := make(map[string][]byte)
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("tar.Next() error = %v", err)
		}
		var buf bytes.Buffer
		if _, err := buf.ReadFrom(tr); err != nil {
			t.Fatalf("ReadFrom(%s) error = %v", header.Name, err)
		}
		entries[header.Name] = buf.Bytes()
	}
	return entries
}
