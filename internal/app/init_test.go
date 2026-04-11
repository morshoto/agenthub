package app

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agenthub/internal/config"
	"agenthub/internal/provider"
	awsprovider "agenthub/internal/provider/aws"
)

func stubGitHubSSHSetup(t *testing.T) {
	t.Helper()

	originalDerive := deriveSSHPublicKeyFunc
	deriveSSHPublicKeyFunc = func(ctx context.Context, privateKeyPath string) (string, error) {
		return "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITestPublicKey agenthub", nil
	}
	t.Cleanup(func() { deriveSSHPublicKeyFunc = originalDerive })
}

func TestInitWritesConfigFile(t *testing.T) {
	restore := stubAWSProviderFactory()
	defer restore()
	stubGitHubSSHSetup(t)

	dir := t.TempDir()
	agentsDir := filepath.Join(dir, "agents")
	configPath := filepath.Join(agentsDir, "alpha", "config.yaml")
	input := strings.Join([]string{
		"alpha",          // agent name
		"1",              // platform aws
		"",               // accept default GPU compute mode
		"2",              // region us-east-1
		"",               // accept default instance g5.xlarge
		"1",              // image ubuntu-24.04
		"20",             // disk size
		"demo-key",       // ssh key pair name
		"/tmp/demo.pem",  // ssh private key
		"203.0.113.0/24", // ssh cidr
		"ubuntu",         // ssh user
		"",               // accept default GitHub App auth
		"123456",         // GitHub App ID
		"789012",         // GitHub installation ID
		"arn:aws:secretsmanager:us-east-1:123456789012:secret:agenthub/github-app-private-key",
		"y",                      // use NemoClaw
		"1",                      // provider codex
		"http://localhost:11434", // endpoint
		"y",                      // confirm summary
	}, "\n") + "\n"

	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()
	os.Args = []string{"agenthub", "--profile", "sso-dev", "init", "--agents-dir", agentsDir}

	app := New()
	cmd := newRootCommand(app)
	cmd.SetIn(strings.NewReader(input))
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	body := string(data)
	for _, fragment := range []string{
		"platform:",
		"name: aws",
		"region:",
		"disk_size_gb: 20",
		"image:",
		"id: ami-0123456789abcdef0",
		"network_mode: public",
		"key_name: demo-key",
		"private_key_path: /tmp/demo.pem",
		"cidr: 203.0.113.0/24",
		"user: ubuntu",
		"backend: terraform",
		"module_dir: infra/aws/ec2",
		"aws_profile: sso-dev",
		"use_nemoclaw: true",
		"provider: codex",
		"endpoint: http://localhost:11434",
		"auth_mode: app",
		`app_id: "123456"`,
		`installation_id: "789012"`,
		"private_key_secret_arn: arn:aws:secretsmanager:us-east-1:123456789012:secret:agenthub/github-app-private-key",
	} {
		if !strings.Contains(body, fragment) {
			t.Fatalf("config file %q missing %q", body, fragment)
		}
	}
	for _, fragment := range []string{"Setup", "Environment check", "Review configuration"} {
		if !strings.Contains(stdout.String(), fragment) {
			t.Fatalf("stdout = %q, want %q", stdout.String(), fragment)
		}
	}
	if strings.Contains(stdout.String(), "Plan:") {
		t.Fatalf("stdout = %q, want review output without duplicated Plan block", stdout.String())
	}

	envPath := filepath.Join(agentsDir, "alpha", ".env")
	envData, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatalf("ReadFile(env) error = %v", err)
	}
	for _, fragment := range []string{
		"SLACK_BOT_TOKEN=",
		"SLACK_APP_TOKEN=",
	} {
		if !strings.Contains(string(envData), fragment) {
			t.Fatalf("env file %q missing %q", string(envData), fragment)
		}
	}
}

func TestInitUsesDefaultAgentNameWhenBlank(t *testing.T) {
	restore := stubAWSProviderFactory()
	defer restore()
	stubGitHubSSHSetup(t)

	dir := t.TempDir()
	agentsDir := filepath.Join(dir, "agents")
	configPath := filepath.Join(agentsDir, "default", "config.yaml")
	input := strings.Join([]string{
		"",               // accept default agent name
		"1",              // platform aws
		"",               // accept default GPU compute mode
		"2",              // region us-east-1
		"",               // accept default instance g5.xlarge
		"1",              // image ubuntu-24.04
		"20",             // disk size
		"demo-key",       // ssh key pair name
		"/tmp/demo.pem",  // ssh private key
		"203.0.113.0/24", // ssh cidr
		"ubuntu",         // ssh user
		"",               // accept default GitHub App auth
		"123456",         // GitHub App ID
		"789012",         // GitHub installation ID
		"arn:aws:secretsmanager:us-east-1:123456789012:secret:agenthub/github-app-private-key",
		"y",                      // use NemoClaw
		"1",                      // provider codex
		"http://localhost:11434", // endpoint
		"y",                      // confirm summary
	}, "\n") + "\n"

	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()
	os.Args = []string{"agenthub", "--profile", "sso-dev", "init", "--agents-dir", agentsDir}

	app := New()
	cmd := newRootCommand(app)
	cmd.SetIn(strings.NewReader(input))
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if _, err := os.Stat(configPath); err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(agentsDir, "default", ".env")); err != nil {
		t.Fatalf("Stat(env) error = %v", err)
	}
}

func TestInitSupportsCPUComputeMode(t *testing.T) {
	original := newAWSProvider
	newAWSProvider = func(profile, computeClass string) provider.CloudProvider {
		if computeClass == config.ComputeClassCPU {
			return cpuInitCloudProvider{stubCloudProvider: stubCloudProvider{profile: profile}}
		}
		return stubCloudProvider{profile: profile}
	}
	defer func() { newAWSProvider = original }()
	stubGitHubSSHSetup(t)

	dir := t.TempDir()
	agentsDir := filepath.Join(dir, "agents")
	configPath := filepath.Join(agentsDir, "alpha", "config.yaml")
	input := strings.Join([]string{
		"alpha",
		"1", // platform aws
		"1", // cpu compute mode
		"2", // region us-east-1
		"",  // accept default instance t3.xlarge
		"1", // Ubuntu 22.04 LTS
		"20",
		"demo-key",
		"/tmp/demo.pem",
		"203.0.113.0/24",
		"ubuntu",
		"", // accept default GitHub App auth
		"123456",
		"789012",
		"arn:aws:secretsmanager:us-east-1:123456789012:secret:agenthub/github-app-private-key",
		"y",                      // use NemoClaw
		"1",                      // provider codex
		"http://localhost:11434", // endpoint
		"y",                      // confirm summary
	}, "\n") + "\n"

	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()
	os.Args = []string{"agenthub", "--profile", "sso-dev", "init", "--agents-dir", agentsDir}

	app := New()
	cmd := newRootCommand(app)
	cmd.SetIn(strings.NewReader(input))
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	loaded, err := config.Load(configPath)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded.Compute.Class != config.ComputeClassCPU {
		t.Fatalf("loaded compute class = %q, want cpu", loaded.Compute.Class)
	}
	if loaded.Instance.Type != "t3.xlarge" {
		t.Fatalf("loaded instance type = %q, want t3.xlarge", loaded.Instance.Type)
	}
	if loaded.Image.Name != "Ubuntu 22.04 LTS" {
		t.Fatalf("loaded image = %q, want Ubuntu 22.04 LTS", loaded.Image.Name)
	}
	if loaded.Sandbox.NetworkMode != "public" {
		t.Fatalf("loaded network mode = %q, want public", loaded.Sandbox.NetworkMode)
	}
	if loaded.SSH.KeyName != "demo-key" || loaded.SSH.PrivateKeyPath != "/tmp/demo.pem" {
		t.Fatalf("loaded ssh config = %#v", loaded.SSH)
	}
}

func TestInitSupportsNonAWSPlatformScaffold(t *testing.T) {
	restore := stubAWSProviderFactory()
	defer restore()
	stubGitHubSSHSetup(t)

	dir := t.TempDir()
	agentsDir := filepath.Join(dir, "agents")
	configPath := filepath.Join(agentsDir, "alpha", "config.yaml")
	input := strings.Join([]string{
		"alpha",          // agent name
		"2",              // gcp
		"",               // accept default GPU compute mode
		"",               // accept default region
		"",               // accept default instance type
		"1",              // image Ubuntu 22.04 LTS
		"20",             // disk size
		"demo-key",       // ssh key pair name
		"/tmp/demo.pem",  // ssh private key
		"203.0.113.0/24", // ssh cidr
		"ubuntu",         // ssh user
		"",               // accept default GitHub App auth
		"123456",         // GitHub App ID
		"789012",         // GitHub installation ID
		"arn:aws:secretsmanager:us-east-1:123456789012:secret:agenthub/github-app-private-key",
		"y",                      // use NemoClaw
		"1",                      // provider codex
		"http://localhost:11434", // endpoint
		"y",                      // confirm summary
	}, "\n") + "\n"

	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()
	os.Args = []string{"agenthub", "--profile", "sso-dev", "init", "--agents-dir", agentsDir}

	app := New()
	cmd := newRootCommand(app)
	cmd.SetIn(strings.NewReader(input))
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	body := string(data)
	for _, fragment := range []string{
		"name: gcp",
		"name: us-central1",
		"instance:",
		"type: a2-highgpu-1g",
		"image:",
		"name: Ubuntu 22.04 LTS",
		"backend: terraform",
		"module_dir: infra/gcp/vm",
	} {
		if !strings.Contains(body, fragment) {
			t.Fatalf("config file %q missing %q", body, fragment)
		}
	}
}

func TestInitUsesPlatformSpecificProviderFactory(t *testing.T) {
	called := false
	original := newCloudProvider
	newCloudProvider = func(platform, profile, computeClass string) provider.CloudProvider {
		called = true
		if platform != config.PlatformGCP {
			t.Fatalf("platform = %q, want %q", platform, config.PlatformGCP)
		}
		if profile != "sso-dev" {
			t.Fatalf("profile = %q, want sso-dev", profile)
		}
		return stubCloudProvider{profile: profile}
	}
	defer func() { newCloudProvider = original }()

	if got := newCloudProvider(config.PlatformGCP, "sso-dev", "gpu"); got == nil {
		t.Fatal("newCloudProvider() returned nil")
	}
	if !called {
		t.Fatal("provider factory should have been called")
	}
}

func TestInitPreselectsRegionFromExistingConfig(t *testing.T) {
	restore := stubAWSProviderFactory()
	defer restore()
	stubGitHubSSHSetup(t)

	dir := t.TempDir()
	existing := filepath.Join(dir, "existing.yaml")
	writeConfig(t, existing, `
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
  network_mode: private
  use_nemoclaw: false
`)
	agentsDir := filepath.Join(dir, "agents")
	configPath := filepath.Join(agentsDir, "alpha", "config.yaml")
	input := strings.Join([]string{
		"alpha",
		"1", // platform aws
		"",  // accept default GPU compute mode
		"",  // accept preselected region from existing config
		"",  // accept default instance g5.xlarge
		"1", // image
		"20",
		"",
		"demo-key",
		"/tmp/demo.pem",
		"203.0.113.0/24",
		"",
		"",
		"123456",
		"789012",
		"arn:aws:secretsmanager:us-east-1:123456789012:secret:agenthub/github-app-private-key",
		"y",
		"1",
		"http://localhost:11434",
		"y",
	}, "\n") + "\n"

	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()
	os.Args = []string{"agenthub", "--profile", "sso-dev", "--config", existing, "init", "--agents-dir", agentsDir}

	app := New()
	cmd := newRootCommand(app)
	cmd.SetIn(strings.NewReader(input))
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	loaded, err := config.Load(configPath)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded.Region.Name != "us-west-2" {
		t.Fatalf("loaded region = %q, want us-west-2", loaded.Region.Name)
	}
}

func TestInitContinuesWhenAWSAuthCheckIsPermissionDenied(t *testing.T) {
	original := newAWSProvider
	newAWSProvider = func(profile, computeClass string) provider.CloudProvider {
		return authFailingCloudProvider{
			stubCloudProvider: stubCloudProvider{profile: profile},
			authErr: &awsprovider.AuthError{
				Kind:    "permission_denied",
				Profile: profile,
				Stage:   "api",
				Cause:   errors.New("AccessDenied: denied"),
			},
		}
	}
	defer func() { newAWSProvider = original }()
	stubGitHubSSHSetup(t)

	dir := t.TempDir()
	agentsDir := filepath.Join(dir, "agents")
	input := strings.Join([]string{
		"alpha",
		"1",              // platform aws
		"",               // accept default GPU compute mode
		"2",              // region us-east-1
		"",               // accept default instance g5.xlarge
		"1",              // image ubuntu-24.04
		"20",             // disk size
		"demo-key",       // ssh key pair name
		"/tmp/demo.pem",  // ssh private key
		"203.0.113.0/24", // ssh cidr
		"ubuntu",         // ssh user
		"",               // accept default GitHub App auth
		"123456",         // GitHub App ID
		"789012",         // GitHub installation ID
		"arn:aws:secretsmanager:us-east-1:123456789012:secret:agenthub/github-app-private-key",
		"y",                      // use NemoClaw
		"1",                      // provider codex
		"http://localhost:11434", // endpoint
		"y",                      // confirm summary
	}, "\n") + "\n"

	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()
	os.Args = []string{"agenthub", "--profile", "sso-dev", "init", "--agents-dir", agentsDir}

	app := New()
	cmd := newRootCommand(app)
	cmd.SetIn(strings.NewReader(input))
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got := stdout.String(); !strings.Contains(got, "Warning: AWS auth check unavailable; continuing.") {
		t.Fatalf("stdout = %q, want permission-denied warning", got)
	}
}

func TestInitContinuesWhenAWSAuthCheckFailsAtSTS(t *testing.T) {
	original := newAWSProvider
	newAWSProvider = func(profile, computeClass string) provider.CloudProvider {
		return authFailingCloudProvider{
			stubCloudProvider: stubCloudProvider{profile: profile},
			authErr: &awsprovider.AuthError{
				Kind:    "api_call_failed",
				Profile: profile,
				Stage:   "api",
				Cause:   errors.New("AWS auth check failed while calling sts:GetCallerIdentity"),
			},
		}
	}
	defer func() { newAWSProvider = original }()
	stubGitHubSSHSetup(t)

	dir := t.TempDir()
	agentsDir := filepath.Join(dir, "agents")
	input := strings.Join([]string{
		"alpha",
		"1",              // platform aws
		"",               // accept default GPU compute mode
		"2",              // region us-east-1
		"",               // accept default instance g5.xlarge
		"1",              // image ubuntu-24.04
		"20",             // disk size
		"demo-key",       // ssh key pair name
		"/tmp/demo.pem",  // ssh private key
		"203.0.113.0/24", // ssh cidr
		"ubuntu",         // ssh user
		"",               // accept default GitHub App auth
		"123456",         // GitHub App ID
		"789012",         // GitHub installation ID
		"arn:aws:secretsmanager:us-east-1:123456789012:secret:agenthub/github-app-private-key",
		"y",                      // use NemoClaw
		"1",                      // provider codex
		"http://localhost:11434", // endpoint
		"y",                      // confirm summary
	}, "\n") + "\n"

	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()
	os.Args = []string{"agenthub", "--profile", "sso-dev", "init", "--agents-dir", agentsDir}

	app := New()
	cmd := newRootCommand(app)
	cmd.SetIn(strings.NewReader(input))
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got := stdout.String(); !strings.Contains(got, "Warning: AWS auth check unavailable; continuing.") {
		t.Fatalf("stdout = %q, want STS warning", got)
	}
}

func TestInitFallsBackWhenAWSImageLookupIsPermissionDenied(t *testing.T) {
	original := newAWSProvider
	newAWSProvider = func(profile, computeClass string) provider.CloudProvider {
		return baseImageFailingCloudProvider{
			stubCloudProvider: stubCloudProvider{profile: profile},
			baseImageErr: &awsprovider.AuthError{
				Kind:    "permission_denied",
				Profile: profile,
				Stage:   "api",
				Cause:   errors.New("UnrecognizedClientException: The security token included in the request is invalid"),
			},
		}
	}
	defer func() { newAWSProvider = original }()
	stubGitHubSSHSetup(t)

	dir := t.TempDir()
	agentsDir := filepath.Join(dir, "agents")
	input := strings.Join([]string{
		"alpha",
		"1",              // platform aws
		"",               // accept default GPU compute mode
		"2",              // region us-east-1
		"",               // accept default instance g5.xlarge
		"1",              // image fallback selection
		"20",             // disk size
		"demo-key",       // ssh key pair name
		"/tmp/demo.pem",  // ssh private key
		"203.0.113.0/24", // ssh cidr
		"ubuntu",         // ssh user
		"",               // accept default GitHub App auth
		"123456",         // GitHub App ID
		"789012",         // GitHub installation ID
		"arn:aws:secretsmanager:us-east-1:123456789012:secret:agenthub/github-app-private-key",
		"y",                      // use NemoClaw
		"1",                      // provider codex
		"http://localhost:11434", // endpoint
		"y",                      // confirm summary
	}, "\n") + "\n"

	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()
	os.Args = []string{"agenthub", "--profile", "sso-dev", "init", "--agents-dir", agentsDir}

	app := New()
	cmd := newRootCommand(app)
	cmd.SetIn(strings.NewReader(input))
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	got := stdout.String()
	for _, fragment := range []string{
		"Warning: AWS image lookup unavailable; using bundled fallback images.",
		"Review configuration",
		"Advanced details",
		"image: AWS Deep Learning AMI GPU Ubuntu 22.04",
	} {
		if !strings.Contains(got, fragment) {
			t.Fatalf("stdout = %q, want %q", got, fragment)
		}
	}
}

type cpuInitCloudProvider struct {
	stubCloudProvider
}

func (cpuInitCloudProvider) ListBaseImages(ctx context.Context, region string) ([]provider.BaseImage, error) {
	return []provider.BaseImage{{
		Name:               "Ubuntu 22.04 LTS",
		ID:                 "ami-0ubuntu1234567890",
		Architecture:       "x86_64",
		Owner:              "canonical",
		VirtualizationType: "hvm",
		RootDeviceType:     "ebs",
		Region:             region,
		Source:             "mock",
		SSMParameter:       "/aws/service/canonical/ubuntu/server/22.04/stable/current/amd64/hvm/ebs-gp2/ami-id",
	}}, nil
}

func (cpuInitCloudProvider) RecommendBaseImages(ctx context.Context, region, computeClass string) ([]provider.BaseImage, error) {
	return []provider.BaseImage{{
		Name:               "Ubuntu 22.04 LTS",
		ID:                 "ami-0ubuntu1234567890",
		Architecture:       "x86_64",
		Owner:              "canonical",
		VirtualizationType: "hvm",
		RootDeviceType:     "ebs",
		Region:             region,
		Source:             "mock",
		SSMParameter:       "/aws/service/canonical/ubuntu/server/22.04/stable/current/amd64/hvm/ebs-gp2/ami-id",
	}}, nil
}

func (cpuInitCloudProvider) ListInstanceTypes(ctx context.Context, region string) ([]provider.InstanceType, error) {
	return []provider.InstanceType{
		{Name: "t3.xlarge", MemoryGB: 16},
		{Name: "t3.2xlarge", MemoryGB: 32},
	}, nil
}

func (cpuInitCloudProvider) RecommendInstanceTypes(ctx context.Context, region, computeClass string) ([]provider.InstanceType, error) {
	return []provider.InstanceType{
		{Name: "t3.xlarge", MemoryGB: 16},
		{Name: "t3.2xlarge", MemoryGB: 32},
	}, nil
}
