package app

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelectAWSProfileUsesSingleDiscoveredProfile(t *testing.T) {
	originalList := listAWSProfilesFunc
	listAWSProfilesFunc = func(ctx context.Context) ([]string, error) {
		return []string{"sso-dev"}, nil
	}
	defer func() { listAWSProfilesFunc = originalList }()

	profile, err := selectAWSProfile(context.Background(), strings.NewReader(""), io.Discard, "")
	if err != nil {
		t.Fatalf("selectAWSProfile() error = %v", err)
	}
	if profile != "sso-dev" {
		t.Fatalf("selectAWSProfile() profile = %q, want sso-dev", profile)
	}
}

func TestInfraTFVarsCommandWritesTerraformVars(t *testing.T) {
	originalResolveSourceArchiveURL := resolveSourceArchiveURLFunc
	resolveSourceArchiveURLFunc = func(ctx context.Context, profile, region string) (string, string, error) {
		return "https://example.com/agenthub-bootstrap.tar.gz", "test-sha", nil
	}
	defer func() { resolveSourceArchiveURLFunc = originalResolveSourceArchiveURL }()

	originalDeriveSSHPublicKey := deriveSSHPublicKeyFunc
	deriveSSHPublicKeyFunc = func(ctx context.Context, privateKeyPath string) (string, error) {
		return "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITfvarsTestKey agenthub", nil
	}
	defer func() { deriveSSHPublicKeyFunc = originalDeriveSSHPublicKey }()

	dir := t.TempDir()
	output := filepath.Join(dir, "terraform.tfvars")
	keyPath := filepath.Join(dir, "id_ed25519")
	if err := os.WriteFile(keyPath, []byte("dummy"), 0o600); err != nil {
		t.Fatalf("WriteFile(keyPath) error = %v", err)
	}
	configPath := filepath.Join(dir, "agenthub.yaml")
	writeConfig(t, configPath, `
platform:
  name: aws
compute:
  class: gpu
region:
  name: ap-northeast-1
instance:
  type: g5.xlarge
  disk_size_gb: 40
  network_mode: public
image:
  name: Ubuntu 22.04 LTS
runtime:
  endpoint: http://localhost:11434
  model: llama3.2
  public_cidr: 0.0.0.0/0
sandbox:
  enabled: true
  network_mode: public
  use_nemoclaw: true
github:
  auth_mode: app
  app_id: "123456"
  installation_id: "789012"
  private_key_secret_arn: arn:aws:secretsmanager:ap-northeast-1:123456789012:secret:agenthub/github-app-private-key
ssh:
  key_name: demo-key
  private_key_path: `+keyPath+`
  cidr: 203.0.113.0/24
  user: ubuntu
`)

	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()
	os.Args = []string{"agenthub", "--profile", "sso-dev", "--config", configPath, "infra", "tfvars", "--output", output}

	app := New()
	cmd := newRootCommand(app)
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	body := string(data)
	agentName := agentNameFromConfigPath(configPath)
	mustContainTerraformAssignment(t, body, "region", `"ap-northeast-1"`)
	mustContainTerraformAssignment(t, body, "compute_class", `"gpu"`)
	mustContainTerraformAssignment(t, body, "instance_type", `"g5.xlarge"`)
	mustContainTerraformAssignment(t, body, "disk_size_gb", `40`)
	mustContainTerraformAssignment(t, body, "network_mode", `"public"`)
	mustContainTerraformAssignment(t, body, "image_name", `"Ubuntu 22.04 LTS"`)
	mustContainTerraformAssignment(t, body, "image_id", `""`)
	mustContainTerraformAssignment(t, body, "runtime_port", `8080`)
	mustContainTerraformAssignment(t, body, "runtime_cidr", `"0.0.0.0/0"`)
	mustContainTerraformAssignment(t, body, "runtime_provider", `""`)
	mustContainTerraformAssignment(t, body, "ssh_key_name", `"demo-key"`)
	mustContainTerraformAssignment(t, body, "ssh_public_key", `"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITfvarsTestKey agenthub"`)
	mustContainTerraformAssignment(t, body, "github_private_key_secret_arn", `"arn:aws:secretsmanager:ap-northeast-1:123456789012:secret:agenthub/github-app-private-key"`)
	mustContainTerraformAssignment(t, body, "github_ssh_key_secret_arn", `""`)
	mustContainTerraformAssignment(t, body, "github_token_secret_arn", `""`)
	mustContainTerraformAssignment(t, body, "ssh_cidr", `"203.0.113.0/24"`)
	mustContainTerraformAssignment(t, body, "ssh_user", `"ubuntu"`)
	mustContainTerraformAssignment(t, body, "name_prefix", `"agenthub"`)
	mustContainTerraformAssignment(t, body, "security_group_name", `"`+securityGroupIdentityName("agenthub", "sso-dev", agentName, "default")+`"`)
	mustContainTerraformAssignment(t, body, "use_nemoclaw", `true`)
	mustContainTerraformAssignment(t, body, "nim_endpoint", `"http://localhost:11434"`)
	mustContainTerraformAssignment(t, body, "model", `"llama3.2"`)
	mustContainTerraformAssignment(t, body, "source_archive_url", `""`)
	mustContainTerraformAssignment(t, body, "aws_profile", `"sso-dev"`)
	if !strings.Contains(stdout.String(), "terraform variables written to") {
		t.Fatalf("stdout = %q, want success message", stdout.String())
	}
}

func TestInfraTFVarsCommandWritesGitHubTokenSecretARN(t *testing.T) {
	originalResolveSourceArchiveURL := resolveSourceArchiveURLFunc
	resolveSourceArchiveURLFunc = func(ctx context.Context, profile, region string) (string, string, error) {
		return "https://example.com/agenthub-bootstrap.tar.gz", "test-sha", nil
	}
	defer func() { resolveSourceArchiveURLFunc = originalResolveSourceArchiveURL }()

	originalDeriveSSHPublicKey := deriveSSHPublicKeyFunc
	deriveSSHPublicKeyFunc = func(ctx context.Context, privateKeyPath string) (string, error) {
		return "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITfvarsTestKey agenthub", nil
	}
	defer func() { deriveSSHPublicKeyFunc = originalDeriveSSHPublicKey }()

	dir := t.TempDir()
	output := filepath.Join(dir, "terraform.tfvars")
	keyPath := filepath.Join(dir, "id_ed25519")
	if err := os.WriteFile(keyPath, []byte("dummy"), 0o600); err != nil {
		t.Fatalf("WriteFile(keyPath) error = %v", err)
	}
	configPath := filepath.Join(dir, "agenthub.yaml")
	writeConfig(t, configPath, `
platform:
  name: aws
region:
  name: ap-northeast-1
instance:
  type: g5.xlarge
  disk_size_gb: 40
  network_mode: public
image:
  name: Ubuntu 22.04 LTS
runtime:
  endpoint: http://localhost:11434
  model: llama3.2
sandbox:
  enabled: true
  network_mode: public
github:
  auth_mode: user
  token_secret_arn: arn:aws:secretsmanager:ap-northeast-1:123456789012:secret:agenthub/github-token
ssh:
  key_name: demo-key
  private_key_path: `+keyPath+`
  cidr: 203.0.113.0/24
  user: ubuntu
`)

	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()
	os.Args = []string{"agenthub", "--profile", "sso-dev", "--config", configPath, "infra", "tfvars", "--output", output}

	app := New()
	cmd := newRootCommand(app)
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	body := string(data)
	mustContainTerraformAssignment(t, body, "github_private_key_secret_arn", `""`)
	mustContainTerraformAssignment(t, body, "github_ssh_key_secret_arn", `""`)
	mustContainTerraformAssignment(t, body, "github_token_secret_arn", `"arn:aws:secretsmanager:ap-northeast-1:123456789012:secret:agenthub/github-token"`)
}

func TestInfraTFVarsCommandWritesGitHubSSHKeySecretARN(t *testing.T) {
	originalResolveSourceArchiveURL := resolveSourceArchiveURLFunc
	resolveSourceArchiveURLFunc = func(ctx context.Context, profile, region string) (string, string, error) {
		return "https://example.com/agenthub-bootstrap.tar.gz", "test-sha", nil
	}
	defer func() { resolveSourceArchiveURLFunc = originalResolveSourceArchiveURL }()

	originalDeriveSSHPublicKey := deriveSSHPublicKeyFunc
	deriveSSHPublicKeyFunc = func(ctx context.Context, privateKeyPath string) (string, error) {
		return "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITfvarsTestKey agenthub", nil
	}
	defer func() { deriveSSHPublicKeyFunc = originalDeriveSSHPublicKey }()

	dir := t.TempDir()
	output := filepath.Join(dir, "terraform.tfvars")
	keyPath := filepath.Join(dir, "id_ed25519")
	if err := os.WriteFile(keyPath, []byte("dummy"), 0o600); err != nil {
		t.Fatalf("WriteFile(keyPath) error = %v", err)
	}
	configPath := filepath.Join(dir, "agenthub.yaml")
	writeConfig(t, configPath, `
platform:
  name: aws
region:
  name: ap-northeast-1
instance:
  type: g5.xlarge
  disk_size_gb: 40
  network_mode: public
image:
  name: Ubuntu 22.04 LTS
runtime:
  endpoint: http://localhost:11434
  model: llama3.2
sandbox:
  enabled: true
  network_mode: public
github:
  ssh_key_secret_arn: arn:aws:secretsmanager:ap-northeast-1:123456789012:secret:agenthub/github-ssh-key
ssh:
  key_name: demo-key
  private_key_path: `+keyPath+`
  cidr: 203.0.113.0/24
  user: ubuntu
`)

	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()
	os.Args = []string{"agenthub", "--profile", "sso-dev", "--config", configPath, "infra", "tfvars", "--output", output}

	app := New()
	cmd := newRootCommand(app)
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	body := string(data)
	mustContainTerraformAssignment(t, body, "github_private_key_secret_arn", `""`)
	mustContainTerraformAssignment(t, body, "github_ssh_key_secret_arn", `"arn:aws:secretsmanager:ap-northeast-1:123456789012:secret:agenthub/github-ssh-key"`)
	mustContainTerraformAssignment(t, body, "github_token_secret_arn", `""`)
}

func TestInfraTFVarsCommandWritesAWSProfile(t *testing.T) {
	originalResolveSourceArchiveURL := resolveSourceArchiveURLFunc
	resolveSourceArchiveURLFunc = func(ctx context.Context, profile, region string) (string, string, error) {
		return "https://example.com/agenthub-bootstrap.tar.gz", "test-sha", nil
	}
	defer func() { resolveSourceArchiveURLFunc = originalResolveSourceArchiveURL }()

	originalDeriveSSHPublicKey := deriveSSHPublicKeyFunc
	deriveSSHPublicKeyFunc = func(ctx context.Context, privateKeyPath string) (string, error) {
		return "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITfvarsTestKey agenthub", nil
	}
	defer func() { deriveSSHPublicKeyFunc = originalDeriveSSHPublicKey }()

	dir := t.TempDir()
	output := filepath.Join(dir, "terraform.tfvars")
	keyPath := filepath.Join(dir, "id_ed25519")
	if err := os.WriteFile(keyPath, []byte("dummy"), 0o600); err != nil {
		t.Fatalf("WriteFile(keyPath) error = %v", err)
	}
	configPath := filepath.Join(dir, "agenthub.yaml")
	writeConfig(t, configPath, `
platform:
  name: aws
region:
  name: ap-northeast-1
instance:
  type: g5.xlarge
  disk_size_gb: 40
  network_mode: public
image:
  name: Ubuntu 22.04 LTS
runtime:
  endpoint: http://localhost:11434
  model: llama3.2
sandbox:
  enabled: true
  network_mode: public
ssh:
  key_name: demo-key
  private_key_path: `+keyPath+`
  cidr: 203.0.113.0/24
  user: ubuntu
`)

	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()
	os.Args = []string{"agenthub", "--profile", "sso-dev", "--config", configPath, "infra", "tfvars", "--output", output}

	app := New()
	cmd := newRootCommand(app)
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	body := string(data)
	mustContainTerraformAssignment(t, body, "aws_profile", `"sso-dev"`)
}

func TestSecurityGroupIdentityNameNormalizesInputs(t *testing.T) {
	got := securityGroupIdentityName("AgentHub", "Sso Dev", "Alpha_Bot", "Prod")
	if got != "agenthub-sso-dev-alpha-bot-prod-sg" {
		t.Fatalf("securityGroupIdentityName() = %q, want agenthub-sso-dev-alpha-bot-prod-sg", got)
	}
}

func mustContainTerraformAssignment(t *testing.T, body, key, value string) {
	t.Helper()
	if !strings.Contains(body, key) || !strings.Contains(body, value) {
		t.Fatalf("terraform vars file %q missing %s = %s", body, key, value)
	}
}
