package app

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"openclaw/internal/provider"
	awsprovider "openclaw/internal/provider/aws"
)

func TestConfigValidateCommandAcceptsConfigFlagAfterSubcommand(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "openclaw.yaml")
	writeConfig(t, path, `
platform:
  name: aws
region:
  name: us-east-1
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

	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()
	os.Args = []string{"openclaw", "config", "validate", "--config", path}

	app := New()
	cmd := newRootCommand(app)
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got := stdout.String(); !strings.Contains(got, "configuration is valid") {
		t.Fatalf("stdout = %q, want validation success message", got)
	}
}

func TestConfigValidateCommandReturnsErrorOnInvalidConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "openclaw.yaml")
	writeConfig(t, path, `
platform:
  name: gcp
region:
  name: us-east-1
instance:
  type: t3.medium
  disk_size_gb: 0
image:
  name: ubuntu-24.04
runtime:
  endpoint: not-a-url
  model: ""
`)

	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()
	os.Args = []string{"openclaw", "config", "validate", "--config", path}

	app := New()
	cmd := newRootCommand(app)
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("Execute() error = nil")
	}
	msg := err.Error()
	for _, field := range []string{"platform.name", "instance.disk_size_gb", "runtime.endpoint", "runtime.model"} {
		if !strings.Contains(msg, field) {
			t.Fatalf("error %q does not mention %q", msg, field)
		}
	}
}

func TestQuotaCheckCommandReportsLiveQuotaStatus(t *testing.T) {
	restore := stubAWSProviderFactory()
	defer restore()

	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()
	os.Args = []string{"openclaw", "quota", "check", "--platform", "aws", "--region", "ap-northeast-1", "--instance-family", "g5"}

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
		"Quota report for g5 in ap-northeast-1",
		"Data source: aws-service-quotas",
		"Likely creatable: true",
		"Notes:",
		"quota check complete",
	} {
		if !strings.Contains(got, fragment) {
			t.Fatalf("stdout = %q, want %q", got, fragment)
		}
	}
}

func TestAuthCheckCommandReportsSuccess(t *testing.T) {
	restore := stubAWSProviderFactory()
	defer restore()

	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()
	os.Args = []string{"openclaw", "--profile", "test-profile", "auth", "check"}

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
		"AWS auth check passed",
		"profile: test-profile",
		"caller identity: arn:aws:sts::123456789012:assumed-role/test-role/test-session",
	} {
		if !strings.Contains(got, fragment) {
			t.Fatalf("stdout = %q, want %q", got, fragment)
		}
	}
}

func TestInfraCreateCommandReportsCreatedInstance(t *testing.T) {
	original := newAWSProvider
	newAWSProvider = func(profile string) provider.CloudProvider {
		return infraCreateStubCloudProvider{stubCloudProvider: stubCloudProvider{profile: profile}}
	}
	defer func() { newAWSProvider = original }()

	dir := t.TempDir()
	path := filepath.Join(dir, "openclaw.yaml")
	writeConfig(t, path, `
platform:
  name: aws
region:
  name: us-east-1
instance:
  type: g5.xlarge
  disk_size_gb: 40
image:
  id: ami-0123456789abcdef0
sandbox:
  network_mode: public
`)

	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()
	os.Args = []string{"openclaw", "--config", path, "infra", "create", "--ssh-key-name", "demo-key", "--ssh-cidr", "203.0.113.0/24"}

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
		"instance id: i-0123456789abcdef0",
		"region: us-east-1",
		"public ip: 203.0.113.10",
		"connection: ssh -i <your-key>.pem ubuntu@203.0.113.10",
		"security group rules:",
		"allow tcp/22 from 203.0.113.0/24",
	} {
		if !strings.Contains(got, fragment) {
			t.Fatalf("stdout = %q, want %q", got, fragment)
		}
	}
}

func stubAWSProviderFactory() func() {
	original := newAWSProvider
	newAWSProvider = func(profile string) provider.CloudProvider {
		return stubCloudProvider{profile: profile}
	}
	return func() {
		newAWSProvider = original
	}
}

type stubCloudProvider struct {
	profile string
}

type authFailingCloudProvider struct {
	stubCloudProvider
	authErr error
}

func (s authFailingCloudProvider) AuthCheck(ctx context.Context) (provider.AuthStatus, error) {
	return provider.AuthStatus{}, s.authErr
}

type baseImageFailingCloudProvider struct {
	stubCloudProvider
	baseImageErr error
}

func (s baseImageFailingCloudProvider) ListBaseImages(ctx context.Context, region string) ([]provider.BaseImage, error) {
	return nil, s.baseImageErr
}

func (s stubCloudProvider) AuthCheck(ctx context.Context) (provider.AuthStatus, error) {
	return provider.AuthStatus{
		Profile: s.profile,
		Account: "123456789012",
		Arn:     "arn:aws:sts::123456789012:assumed-role/test-role/test-session",
		UserID:  "test-session",
	}, nil
}

func (s stubCloudProvider) ListRegions(ctx context.Context) ([]string, error) {
	return []string{"ap-northeast-1", "us-east-1", "us-west-2"}, nil
}

func (s stubCloudProvider) CheckGPUQuota(ctx context.Context, region, instanceFamily string) (provider.GPUQuotaReport, error) {
	quotaUsage := 1
	return provider.GPUQuotaReport{
		Source:         awsprovider.QuotaSourceServiceQuotas,
		Region:         region,
		InstanceFamily: instanceFamily,
		Checks: []provider.GPUQuotaCheck{{
			QuotaName:          "Running On-Demand G and VT instances",
			CurrentLimit:       2,
			CurrentUsage:       &quotaUsage,
			EstimatedRemaining: 1,
			UsageIsEstimated:   false,
		}},
		LikelyCreatable: true,
		Notes:           []string{"quota check complete"},
	}, nil
}

func (s stubCloudProvider) ListInstanceTypes(ctx context.Context, region string) ([]provider.InstanceType, error) {
	return []provider.InstanceType{{Name: "g5.xlarge"}}, nil
}

func (s stubCloudProvider) ListBaseImages(ctx context.Context, region string) ([]provider.BaseImage, error) {
	return []provider.BaseImage{{
		Name:               "AWS Deep Learning AMI GPU Ubuntu 22.04",
		ID:                 "ami-0123456789abcdef0",
		Description:        "Deep Learning Base OSS Nvidia Driver GPU AMI (Ubuntu 22.04)",
		Architecture:       "x86_64",
		Owner:              "amazon",
		VirtualizationType: "hvm",
		RootDeviceType:     "ebs",
		Region:             region,
		Source:             "mock",
		SSMParameter:       "/aws/service/deeplearning/ami/x86_64/base-oss-nvidia-driver-gpu-ubuntu-22.04/latest/ami-id",
	}}, nil
}

func (s stubCloudProvider) CreateInstance(ctx context.Context, req provider.CreateInstanceRequest) (*provider.Instance, error) {
	return &provider.Instance{
		ID:                 "i-0123456789abcdef0",
		Name:               "i-0123456789abcdef0",
		Region:             req.Region,
		PublicIP:           "203.0.113.10",
		PrivateIP:          "10.0.0.10",
		ConnectionInfo:     "ssh -i <your-key>.pem ubuntu@203.0.113.10",
		SecurityGroupID:    "sg-0123456789abcdef0",
		SecurityGroupRules: []string{"allow tcp/22 from 203.0.113.0/24"},
	}, nil
}

func (s stubCloudProvider) DeleteInstance(ctx context.Context, instanceID string) error { return nil }

type infraCreateStubCloudProvider struct {
	stubCloudProvider
}

func writeConfig(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(strings.TrimSpace(contents)+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}
