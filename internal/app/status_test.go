package app

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agenthub/internal/provider"
)

func TestStatusCommandReportsNoAgentsFound(t *testing.T) {
	agentsDir := filepath.Join(t.TempDir(), "agents")

	stdout, err := runStatusCommand(t, agentsDir)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	for _, fragment := range []string{
		"agent status",
		"no agents found under " + agentsDir,
	} {
		if !strings.Contains(stdout, fragment) {
			t.Fatalf("stdout = %q, want %q", stdout, fragment)
		}
	}
}

func TestStatusCommandReportsMergedAgentConfigs(t *testing.T) {
	agentsDir := filepath.Join(t.TempDir(), "agents")
	alphaDir := filepath.Join(agentsDir, "alpha")
	betaDir := filepath.Join(agentsDir, "beta")
	if err := os.MkdirAll(alphaDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(alpha) error = %v", err)
	}
	if err := os.MkdirAll(betaDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(beta) error = %v", err)
	}

	writeConfig(t, filepath.Join(alphaDir, "01-base.yaml"), `
platform:
  name: aws
compute:
  class: gpu
region:
  name: us-east-1
instance:
  type: g5.xlarge
  disk_size_gb: 40
image:
  name: ubuntu-24.04
runtime:
  endpoint: http://localhost:11434
  model: llama3.2
sandbox:
  enabled: true
  network_mode: public
  use_nemoclaw: false
`)
	writeConfig(t, filepath.Join(alphaDir, "99-override.yaml"), `
runtime:
  provider: aws-bedrock
  port: 8080
ssh:
  user: ubuntu
  key_name: demo-key
sandbox:
  use_nemoclaw: true
`)
	writeConfig(t, filepath.Join(betaDir, "config.yaml"), `
platform:
  name: aws
region:
  name: us-west-2
instance:
  type: t3.medium
  disk_size_gb: 20
image:
  name: ubuntu-24.04
runtime:
  endpoint: http://localhost:11434
  model: llama3.2
sandbox:
  enabled: false
`)

	stdout, err := runStatusCommand(t, agentsDir)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	for _, fragment := range []string{
		"agent status",
		"agent: alpha",
		"agent: beta",
		"status: valid",
		"runtime: provider=aws-bedrock endpoint=http://localhost:11434 model=llama3.2 port=8080",
		"ssh: user=ubuntu key_name=demo-key",
		"sandbox: enabled=true network=public use_nemoclaw=true",
		"instance: g5.xlarge (40 GB)",
		"instance: t3.medium (20 GB)",
	} {
		if !strings.Contains(stdout, fragment) {
			t.Fatalf("stdout = %q, want %q", stdout, fragment)
		}
	}

	alphaIndex := strings.Index(stdout, "agent: alpha")
	betaIndex := strings.Index(stdout, "agent: beta")
	if alphaIndex == -1 || betaIndex == -1 || alphaIndex > betaIndex {
		t.Fatalf("stdout order = %q, want alpha before beta", stdout)
	}
}

func TestStatusCommandReportsInvalidAgentConfig(t *testing.T) {
	agentsDir := filepath.Join(t.TempDir(), "agents")
	alphaDir := filepath.Join(agentsDir, "alpha")
	betaDir := filepath.Join(agentsDir, "beta")
	if err := os.MkdirAll(alphaDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(alpha) error = %v", err)
	}
	if err := os.MkdirAll(betaDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(beta) error = %v", err)
	}

	writeConfig(t, filepath.Join(alphaDir, "config.yaml"), `
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
  endpoint: http://localhost:11434
  model: llama3.2
sandbox:
  enabled: true
`)
	writeConfig(t, filepath.Join(betaDir, "broken.yaml"), `
platform:
  name: aws
runtime:
  endpoint: http://localhost:11434
  model: llama3.2
  port: [
`)

	stdout, err := runStatusCommand(t, agentsDir)
	if err == nil {
		t.Fatal("Execute() error = nil, want failure for invalid agent config")
	}

	for _, fragment := range []string{
		"agent: beta",
		"status: invalid",
		"parse config",
		"broken.yaml",
	} {
		if !strings.Contains(stdout, fragment) {
			t.Fatalf("stdout = %q, want %q", stdout, fragment)
		}
	}
}

func TestStatusCommandReportsEC2InstanceState(t *testing.T) {
	agentsDir := filepath.Join(t.TempDir(), "agents")
	alphaDir := filepath.Join(agentsDir, "alpha")
	betaDir := filepath.Join(agentsDir, "beta")
	if err := os.MkdirAll(alphaDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(alpha) error = %v", err)
	}
	if err := os.MkdirAll(betaDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(beta) error = %v", err)
	}

	writeConfig(t, filepath.Join(alphaDir, "config.yaml"), `
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
  key_name: demo-key
`)
	writeConfig(t, filepath.Join(betaDir, "config.yaml"), `
platform:
  name: aws
region:
  name: us-west-2
instance:
  type: t3.medium
  disk_size_gb: 20
image:
  name: ubuntu-24.04
runtime:
  endpoint: http://localhost:11434
  model: llama3.2
`)

	oldFactory := newAWSProvider
	t.Cleanup(func() { newAWSProvider = oldFactory })
	newAWSProvider = func(profile, computeClass string) provider.CloudProvider {
		return fakeStatusCloudProvider{}
	}

	stdout, err := runStatusCommand(t, agentsDir)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	for _, fragment := range []string{
		"agent: alpha",
		"ec2:",
		"instance-id: i-0123456789abcdef0",
		"state: running",
		"instance-type: g5.xlarge",
		"availability-zone: us-west-2a",
		"public-ip: 203.0.113.10",
		"private-ip: 10.0.0.10",
		"launch-time: 2026-04-06T12:00:00Z",
		"key-name: demo-key",
		"agent: beta",
		"ec2: not provisioned",
	} {
		if !strings.Contains(stdout, fragment) {
			t.Fatalf("stdout = %q, want %q", stdout, fragment)
		}
	}
}

func TestStatusCommandOutputsJSON(t *testing.T) {
	agentsDir := filepath.Join(t.TempDir(), "agents")
	alphaDir := filepath.Join(agentsDir, "alpha")
	betaDir := filepath.Join(agentsDir, "beta")
	if err := os.MkdirAll(alphaDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(alpha) error = %v", err)
	}
	if err := os.MkdirAll(betaDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(beta) error = %v", err)
	}

	writeConfig(t, filepath.Join(alphaDir, "01-base.yaml"), `
platform:
  name: aws
compute:
  class: gpu
region:
  name: us-east-1
instance:
  type: g5.xlarge
  disk_size_gb: 40
image:
  name: ubuntu-24.04
runtime:
  endpoint: http://localhost:11434
  model: llama3.2
sandbox:
  enabled: true
  network_mode: public
  use_nemoclaw: true
  filesystem_allow:
    - /tmp
ssh:
  user: ubuntu
infra:
  instance_id: i-0123456789abcdef0
`)
	writeConfig(t, filepath.Join(alphaDir, "99-override.yaml"), `
runtime:
  provider: aws-bedrock
  port: 8080
  public_cidr: 203.0.113.0/24
  codex:
    secret_id: codex-secret
`)
	writeConfig(t, filepath.Join(betaDir, "config.yaml"), `
platform:
  name: aws
region:
  name: us-west-2
instance:
  type: t3.medium
  disk_size_gb: 20
image:
  name: ubuntu-24.04
runtime:
  endpoint: http://localhost:11434
  model: llama3.2
sandbox:
  enabled: false
`)

	oldFactory := newAWSProvider
	t.Cleanup(func() { newAWSProvider = oldFactory })
	newAWSProvider = func(profile, computeClass string) provider.CloudProvider {
		return fakeStatusCloudProvider{}
	}

	stdout, err := runStatusCommandWithArgs(t, agentsDir, "--output", "json")
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	var payload statusJSONResponse
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("Unmarshal() error = %v, stdout = %q", err, stdout)
	}
	if payload.Root != agentsDir {
		t.Fatalf("root = %q, want %q", payload.Root, agentsDir)
	}
	if len(payload.Agents) != 2 {
		t.Fatalf("len(agents) = %d, want 2", len(payload.Agents))
	}

	alpha := payload.Agents[0]
	if alpha.Name != "alpha" {
		t.Fatalf("alpha name = %q, want alpha", alpha.Name)
	}
	if alpha.Status != "valid" {
		t.Fatalf("alpha status = %q, want valid", alpha.Status)
	}
	if alpha.Config == nil || alpha.Config.Runtime == nil || alpha.Config.Runtime.Provider != "aws-bedrock" || alpha.Config.Runtime.Port != 8080 {
		t.Fatalf("alpha runtime = %#v, want provider/port populated", alpha.Config)
	}
	if alpha.Config.Runtime.Codex == nil || alpha.Config.Runtime.Codex.SecretID != "codex-secret" {
		t.Fatalf("alpha runtime codex = %#v, want secret_id", alpha.Config.Runtime.Codex)
	}
	if alpha.Config.Sandbox == nil || !alpha.Config.Sandbox.Enabled || alpha.Config.Sandbox.NetworkMode != "public" || !alpha.Config.Sandbox.UseNemoClaw {
		t.Fatalf("alpha sandbox = %#v, want structured sandbox fields", alpha.Config.Sandbox)
	}
	if alpha.Live.State != "available" || alpha.Live.Instance == nil || alpha.Live.Instance.ID != "i-0123456789abcdef0" || alpha.Live.Instance.LaunchTime != "2026-04-06T12:00:00Z" {
		t.Fatalf("alpha live = %#v, want populated live state", alpha.Live)
	}

	beta := payload.Agents[1]
	if beta.Name != "beta" {
		t.Fatalf("beta name = %q, want beta", beta.Name)
	}
	if beta.Live.State != "not_provisioned" {
		t.Fatalf("beta live state = %q, want not_provisioned", beta.Live.State)
	}
}

func TestStatusCommandOutputsJSONForInvalidAgentAndReturnsError(t *testing.T) {
	agentsDir := filepath.Join(t.TempDir(), "agents")
	alphaDir := filepath.Join(agentsDir, "alpha")
	if err := os.MkdirAll(alphaDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(alpha) error = %v", err)
	}

	writeConfig(t, filepath.Join(alphaDir, "broken.yaml"), `
platform:
  name: aws
runtime:
  endpoint: http://localhost:11434
  model: llama3.2
  port: [
`)

	stdout, err := runStatusCommandWithArgs(t, agentsDir, "--output", "json")
	if err == nil {
		t.Fatal("Execute() error = nil, want failure for invalid agent config")
	}

	var payload statusJSONResponse
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("Unmarshal() error = %v, stdout = %q", err, stdout)
	}
	if len(payload.Agents) != 1 {
		t.Fatalf("len(agents) = %d, want 1", len(payload.Agents))
	}
	if payload.Agents[0].Status != "invalid" {
		t.Fatalf("status = %q, want invalid", payload.Agents[0].Status)
	}
	if !strings.Contains(payload.Agents[0].Error, "parse config") {
		t.Fatalf("error = %q, want parse config", payload.Agents[0].Error)
	}
}

func TestStatusCommandRejectsUnsupportedOutputFormat(t *testing.T) {
	agentsDir := filepath.Join(t.TempDir(), "agents")

	stdout, err := runStatusCommandWithArgs(t, agentsDir, "--output", "xml")
	if err == nil {
		t.Fatal("Execute() error = nil, want unsupported output format error")
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty output for unsupported format", stdout)
	}
	if !strings.Contains(err.Error(), "unsupported output format") {
		t.Fatalf("err = %v, want unsupported output format", err)
	}
}

func runStatusCommand(t *testing.T, agentsDir string) (string, error) {
	t.Helper()
	return runStatusCommandWithArgs(t, agentsDir)
}

func runStatusCommandWithArgs(t *testing.T, agentsDir string, extraArgs ...string) (string, error) {
	t.Helper()

	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()
	os.Args = append([]string{"agenthub", "status", "--agents-dir", agentsDir}, extraArgs...)

	app := New()
	cmd := newRootCommand(app)
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)

	err := cmd.Execute()
	return stdout.String(), err
}

type fakeStatusCloudProvider struct{}

func (fakeStatusCloudProvider) CheckAuth(ctx context.Context) (provider.AuthStatus, error) {
	return provider.AuthStatus{}, nil
}

func (fakeStatusCloudProvider) ListRegions(ctx context.Context) ([]string, error) {
	return nil, nil
}

func (fakeStatusCloudProvider) CheckGPUQuota(ctx context.Context, region, instanceFamily string) (provider.GPUQuotaReport, error) {
	return provider.GPUQuotaReport{}, nil
}

func (fakeStatusCloudProvider) RecommendInstanceTypes(ctx context.Context, region, computeClass string) ([]provider.InstanceType, error) {
	return nil, nil
}

func (fakeStatusCloudProvider) RecommendBaseImages(ctx context.Context, region, computeClass string) ([]provider.BaseImage, error) {
	return nil, nil
}

func (fakeStatusCloudProvider) GetInstance(ctx context.Context, region, instanceID string) (*provider.Instance, error) {
	return &provider.Instance{
		ID:               instanceID,
		Name:             "agenthub-demo",
		Region:           region,
		State:            "running",
		InstanceType:     "g5.xlarge",
		AvailabilityZone: "us-west-2a",
		LaunchTime:       time.Date(2026, 4, 6, 12, 0, 0, 0, time.UTC),
		KeyName:          "demo-key",
		PublicIP:         "203.0.113.10",
		PrivateIP:        "10.0.0.10",
	}, nil
}

func (fakeStatusCloudProvider) DeleteInstance(ctx context.Context, region, instanceID string) error {
	return nil
}
