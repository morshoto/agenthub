package app

import (
	"bytes"
	"context"
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

func runStatusCommand(t *testing.T, agentsDir string) (string, error) {
	t.Helper()

	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()
	os.Args = []string{"agenthub", "status", "--agents-dir", agentsDir}

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
