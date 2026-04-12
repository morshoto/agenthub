package app

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agenthub/internal/config"
	infratf "agenthub/internal/infra/terraform"
)

func TestInfraDestroyCommandDestroysInfrastructureAndClearsDeployState(t *testing.T) {
	originalBackend := newTerraformBackend
	originalDerive := deriveSSHPublicKeyFunc
	originalEnsure := ensureSSHPrivateKeyFunc
	originalInteractive := detectInteractiveInput
	t.Cleanup(func() {
		newTerraformBackend = originalBackend
		deriveSSHPublicKeyFunc = originalDerive
		ensureSSHPrivateKeyFunc = originalEnsure
		detectInteractiveInput = originalInteractive
	})

	deriveSSHPublicKeyFunc = func(ctx context.Context, privateKeyPath string) (string, error) {
		return "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITestPublicKey agenthub", nil
	}
	ensureSSHPrivateKeyFunc = func(ctx context.Context, privateKeyPath string) (string, error) {
		return privateKeyPath, nil
	}
	detectInteractiveInput = func(io.Reader) bool { return true }

	destroyCalled := false
	initCalled := false
	newTerraformBackend = func(profile string, cfg *config.Config) (infratf.InfraBackend, error) {
		if profile != "sso-dev" {
			t.Fatalf("profile = %q, want sso-dev", profile)
		}
		return destroyTrackingTerraformBackend{
			onInit: func(workdir string) { initCalled = true },
			onDestroy: func(workdir, varsFile string) {
				destroyCalled = true
				if varsFile != "agenthub.auto.tfvars.json" {
					t.Fatalf("vars file = %q, want terraform-readable basename", varsFile)
				}
			},
		}, nil
	}

	configPath := writeDestroyConfig(t)
	agentName := agentNameFromConfigPath(configPath)
	if agentName == "" {
		agentName = "default"
	}
	stdout, err := runInfraDestroyCommand(t, []string{"agenthub", "--config", configPath, "infra", "destroy"}, "y\n")
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	for _, fragment := range []string{
		"will destroy",
		"- agent: " + agentName,
		"- recorded instance id: i-0123456789abcdef0",
		"- recorded runtime url: http://203.0.113.10:8080",
		"destroying infrastructure with Terraform...",
		"destroyed infrastructure",
	} {
		if !strings.Contains(stdout, fragment) {
			t.Fatalf("stdout = %q, want %q", stdout, fragment)
		}
	}
	if !initCalled {
		t.Fatal("terraform init was not called")
	}
	if !destroyCalled {
		t.Fatal("terraform destroy was not called")
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatalf("Load(config) error = %v", err)
	}
	if cfg.Infra.InstanceID != "" {
		t.Fatalf("infra.instance_id = %q, want empty", cfg.Infra.InstanceID)
	}
	if cfg.Slack.RuntimeURL != "" {
		t.Fatalf("slack.runtime_url = %q, want empty", cfg.Slack.RuntimeURL)
	}
	if cfg.Infra.AWSProfile != "sso-dev" {
		t.Fatalf("infra.aws_profile = %q, want sso-dev", cfg.Infra.AWSProfile)
	}
}

func TestInfraDestroyCommandCancelsWithoutDestroying(t *testing.T) {
	originalBackend := newTerraformBackend
	originalInteractive := detectInteractiveInput
	t.Cleanup(func() {
		newTerraformBackend = originalBackend
		detectInteractiveInput = originalInteractive
	})

	detectInteractiveInput = func(io.Reader) bool { return true }
	newTerraformBackend = func(profile string, cfg *config.Config) (infratf.InfraBackend, error) {
		t.Fatal("newTerraformBackend should not be called when confirmation is declined")
		return nil, nil
	}

	configPath := writeDestroyConfig(t)
	stdout, err := runInfraDestroyCommand(t, []string{"agenthub", "--config", configPath, "infra", "destroy"}, "n\n")
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !strings.Contains(stdout, "destroy cancelled") {
		t.Fatalf("stdout = %q, want cancellation message", stdout)
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatalf("Load(config) error = %v", err)
	}
	if cfg.Infra.InstanceID == "" {
		t.Fatal("infra.instance_id was cleared on cancellation")
	}
}

func TestInfraDestroyCommandRequiresForceInNonInteractiveMode(t *testing.T) {
	configPath := writeDestroyConfig(t)
	_, err := runInfraDestroyCommandWithInput(t, []string{"agenthub", "--config", configPath, "infra", "destroy"}, bytes.NewBuffer(nil))
	if err == nil {
		t.Fatal("Execute() error = nil, want non-interactive confirmation failure")
	}
	if got := err.Error(); !strings.Contains(got, "rerun with --force in non-interactive mode") {
		t.Fatalf("error = %q, want --force guidance", got)
	}
}

func TestInfraDestroyCommandPreservesDeployStateWhenDestroyFails(t *testing.T) {
	originalBackend := newTerraformBackend
	originalDerive := deriveSSHPublicKeyFunc
	originalEnsure := ensureSSHPrivateKeyFunc
	t.Cleanup(func() {
		newTerraformBackend = originalBackend
		deriveSSHPublicKeyFunc = originalDerive
		ensureSSHPrivateKeyFunc = originalEnsure
	})

	deriveSSHPublicKeyFunc = func(ctx context.Context, privateKeyPath string) (string, error) {
		return "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITestPublicKey agenthub", nil
	}
	ensureSSHPrivateKeyFunc = func(ctx context.Context, privateKeyPath string) (string, error) {
		return privateKeyPath, nil
	}
	newTerraformBackend = func(profile string, cfg *config.Config) (infratf.InfraBackend, error) {
		return destroyTrackingTerraformBackend{
			destroyErr: errors.New("boom"),
		}, nil
	}

	configPath := writeDestroyConfig(t)
	_, err := runInfraDestroyCommand(t, []string{"agenthub", "--config", configPath, "infra", "destroy", "--force"}, "")
	if err == nil {
		t.Fatal("Execute() error = nil, want destroy failure")
	}
	if got := err.Error(); !strings.Contains(got, "infra destroy failed") || !strings.Contains(got, "boom") {
		t.Fatalf("error = %q, want wrapped destroy failure", got)
	}

	cfg, loadErr := config.Load(configPath)
	if loadErr != nil {
		t.Fatalf("Load(config) error = %v", loadErr)
	}
	if cfg.Infra.InstanceID == "" {
		t.Fatal("infra.instance_id was cleared after failed destroy")
	}
	if cfg.Slack.RuntimeURL == "" {
		t.Fatal("slack.runtime_url was cleared after failed destroy")
	}
}

type destroyTrackingTerraformBackend struct {
	onInit     func(workdir string)
	onDestroy  func(workdir, varsFile string)
	destroyErr error
}

func (b destroyTrackingTerraformBackend) Init(ctx context.Context, workdir string) error {
	if b.onInit != nil {
		b.onInit(workdir)
	}
	return nil
}

func (b destroyTrackingTerraformBackend) Import(ctx context.Context, workdir string, address string, id string) error {
	return nil
}

func (b destroyTrackingTerraformBackend) Plan(ctx context.Context, workdir string, varsFile string) error {
	return nil
}

func (b destroyTrackingTerraformBackend) Apply(ctx context.Context, workdir string, varsFile string) error {
	return nil
}

func (b destroyTrackingTerraformBackend) Destroy(ctx context.Context, workdir string, varsFile string) error {
	if b.onDestroy != nil {
		b.onDestroy(workdir, varsFile)
	}
	return b.destroyErr
}

func (b destroyTrackingTerraformBackend) Output(ctx context.Context, workdir string) (*infratf.InfraOutput, error) {
	return &infratf.InfraOutput{}, nil
}

func writeDestroyConfig(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	keyPath := filepath.Join(dir, "demo.pem")
	if err := os.WriteFile(keyPath, []byte("dummy"), 0o600); err != nil {
		t.Fatalf("WriteFile(key) error = %v", err)
	}

	configPath := filepath.Join(dir, "agenthub.yaml")
	writeConfig(t, configPath, `
platform:
  name: aws
region:
  name: us-east-1
infra:
  aws_profile: sso-dev
  instance_id: i-0123456789abcdef0
ssh:
  key_name: demo-key
  private_key_path: `+keyPath+`
  cidr: 203.0.113.0/24
  user: ubuntu
instance:
  type: g5.xlarge
  disk_size_gb: 40
  network_mode: public
image:
  id: ami-0123456789abcdef0
runtime:
  endpoint: http://localhost:11434
  model: llama3.2
slack:
  runtime_url: http://203.0.113.10:8080
sandbox:
  enabled: true
  network_mode: public
`)
	return configPath
}

func runInfraDestroyCommand(t *testing.T, args []string, input string) (string, error) {
	t.Helper()
	return runInfraDestroyCommandWithInput(t, args, strings.NewReader(input))
}

func runInfraDestroyCommandWithInput(t *testing.T, args []string, input io.Reader) (string, error) {
	t.Helper()

	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()
	os.Args = args

	app := New()
	cmd := newRootCommand(app)
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetIn(input)

	err := cmd.Execute()
	return stdout.String(), err
}
