package setup

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"agenthub/internal/config"
	"agenthub/internal/prompt"
	"agenthub/internal/provider"
	awsprovider "agenthub/internal/provider/aws"
)

func TestRenderWizardProgressShowsCurrentStateWithoutHistoryNoise(t *testing.T) {
	out := &bytes.Buffer{}
	renderWizardProgress(out, "Setup", "Agent setup", 3, 8, "Compute mode", []wizardProgressItem{
		{Label: "Agent name", Value: "default"},
		{Label: "Platform", Value: "aws"},
		{Label: "Compute mode", Value: "gpu"},
		{Label: "Region"},
		{Label: "Instance"},
		{Label: "Access"},
		{Label: "Runtime"},
		{Label: "Review"},
	})

	got := out.String()
	for _, fragment := range []string{
		"Setup",
		// Header now uses Docker-style "[+]" active-task prefix.
		"[+] Agent setup  Step 3/8",
		"✓ Agent name         default",
		"✓ Platform           aws",
		"→ Compute mode       -",
		"  Region             -",
		"  Instance           -",
		"  Access             -",
		"  Runtime            -",
		"  Review             -",
	} {
		if !strings.Contains(got, fragment) {
			t.Fatalf("render output %q missing %q", got, fragment)
		}
	}
	for _, fragment := range []string{
		"✓ Compute mode",
		"... 4 more step(s)",
	} {
		if strings.Contains(got, fragment) {
			t.Fatalf("render output %q unexpectedly contains %q", got, fragment)
		}
	}
	if strings.Contains(got, "\033[2J") {
		t.Fatalf("render output %q unexpectedly clears the full screen", got)
	}
}

type fakeProvider struct {
	regions          []string
	report           provider.GPUQuotaReport
	quotaErr         error
	regionsErr       error
	instanceTypesErr error
}

type authRetryProvider struct {
	checks int
}

func (f fakeProvider) AuthCheck(ctx context.Context) (provider.AuthStatus, error) {
	return provider.AuthStatus{}, nil
}
func (f fakeProvider) CheckAuth(ctx context.Context) (provider.AuthStatus, error) {
	return f.AuthCheck(ctx)
}
func (f fakeProvider) ListRegions(ctx context.Context) ([]string, error) {
	if f.regionsErr != nil {
		return nil, f.regionsErr
	}
	return f.regions, nil
}
func (f fakeProvider) CheckGPUQuota(ctx context.Context, region, instanceFamily string) (provider.GPUQuotaReport, error) {
	if f.quotaErr != nil {
		return provider.GPUQuotaReport{}, f.quotaErr
	}
	return f.report, nil
}
func (f fakeProvider) ListInstanceTypes(ctx context.Context, region string) ([]provider.InstanceType, error) {
	if f.instanceTypesErr != nil {
		return nil, f.instanceTypesErr
	}
	return []provider.InstanceType{{Name: "t3.medium"}, {Name: "g5.xlarge"}}, nil
}
func (f fakeProvider) RecommendInstanceTypes(ctx context.Context, region, computeClass string) ([]provider.InstanceType, error) {
	if f.instanceTypesErr != nil {
		return nil, f.instanceTypesErr
	}
	if config.EffectiveComputeClass(computeClass) == config.ComputeClassCPU {
		return []provider.InstanceType{{Name: "t3.xlarge"}, {Name: "t3.2xlarge"}}, nil
	}
	return []provider.InstanceType{{Name: "g5.xlarge"}, {Name: "g4dn.xlarge"}}, nil
}
func (f fakeProvider) ListBaseImages(ctx context.Context, region string) ([]provider.BaseImage, error) {
	return []provider.BaseImage{{
		Name:               "AWS Deep Learning AMI GPU Ubuntu 22.04",
		ID:                 "ami-0123456789abcdef0",
		Architecture:       "x86_64",
		Owner:              "amazon",
		VirtualizationType: "hvm",
		RootDeviceType:     "ebs",
		Region:             region,
		Source:             "mock",
		SSMParameter:       "/aws/service/deeplearning/ami/x86_64/base-oss-nvidia-driver-gpu-ubuntu-22.04/latest/ami-id",
	}}, nil
}
func (f fakeProvider) RecommendBaseImages(ctx context.Context, region, computeClass string) ([]provider.BaseImage, error) {
	if config.EffectiveComputeClass(computeClass) == config.ComputeClassCPU {
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
	return f.ListBaseImages(ctx, region)
}
func (f fakeProvider) GetInstance(ctx context.Context, region, instanceID string) (*provider.Instance, error) {
	return nil, errors.New("not implemented")
}
func (f fakeProvider) CreateInstance(ctx context.Context, req provider.CreateInstanceRequest) (*provider.Instance, error) {
	return nil, errors.New("not implemented")
}
func (f fakeProvider) DeleteInstance(ctx context.Context, region, instanceID string) error {
	return nil
}

func (p *authRetryProvider) AuthCheck(ctx context.Context) (provider.AuthStatus, error) {
	p.checks++
	if p.checks == 1 {
		return provider.AuthStatus{}, &awsprovider.AuthError{
			Kind:    "no_credentials",
			Profile: "sso-dev",
			Stage:   "credentials",
			Cause:   errors.New("no valid credential sources"),
		}
	}
	return provider.AuthStatus{
		Profile: "sso-dev",
		Account: "123456789012",
		Arn:     "arn:aws:sts::123456789012:assumed-role/test-role/test-session",
		UserID:  "test-session",
	}, nil
}

func (p *authRetryProvider) CheckAuth(ctx context.Context) (provider.AuthStatus, error) {
	return p.AuthCheck(ctx)
}

func (p *authRetryProvider) ListRegions(ctx context.Context) ([]string, error) { return nil, nil }
func (p *authRetryProvider) CheckGPUQuota(ctx context.Context, region, instanceFamily string) (provider.GPUQuotaReport, error) {
	return provider.GPUQuotaReport{}, nil
}
func (p *authRetryProvider) RecommendInstanceTypes(ctx context.Context, region, computeClass string) ([]provider.InstanceType, error) {
	return nil, nil
}
func (p *authRetryProvider) RecommendBaseImages(ctx context.Context, region, computeClass string) ([]provider.BaseImage, error) {
	return nil, nil
}
func (p *authRetryProvider) GetInstance(ctx context.Context, region, instanceID string) (*provider.Instance, error) {
	return nil, nil
}
func (p *authRetryProvider) DeleteInstance(ctx context.Context, region, instanceID string) error {
	return nil
}

type authDeniedProvider struct{}

func (authDeniedProvider) AuthCheck(ctx context.Context) (provider.AuthStatus, error) {
	return provider.AuthStatus{}, &awsprovider.AuthError{
		Kind:    "permission_denied",
		Profile: "sso-dev",
		Stage:   "api",
		Cause:   errors.New("AccessDenied: denied"),
	}
}
func (authDeniedProvider) CheckAuth(ctx context.Context) (provider.AuthStatus, error) {
	return authDeniedProvider{}.AuthCheck(ctx)
}
func (authDeniedProvider) ListRegions(ctx context.Context) ([]string, error) { return nil, nil }
func (authDeniedProvider) CheckGPUQuota(ctx context.Context, region, instanceFamily string) (provider.GPUQuotaReport, error) {
	return provider.GPUQuotaReport{}, nil
}
func (authDeniedProvider) RecommendInstanceTypes(ctx context.Context, region, computeClass string) ([]provider.InstanceType, error) {
	return nil, nil
}
func (authDeniedProvider) RecommendBaseImages(ctx context.Context, region, computeClass string) ([]provider.BaseImage, error) {
	return nil, nil
}
func (authDeniedProvider) GetInstance(ctx context.Context, region, instanceID string) (*provider.Instance, error) {
	return nil, nil
}
func (authDeniedProvider) DeleteInstance(ctx context.Context, region, instanceID string) error {
	return nil
}

type loginRetryProvider struct {
	fakeProvider
	checks int
}

func (p *loginRetryProvider) CheckAuth(ctx context.Context) (provider.AuthStatus, error) {
	p.checks++
	if p.checks == 1 {
		return provider.AuthStatus{}, &awsprovider.AuthError{
			Kind:    "no_credentials",
			Profile: "sso-dev",
			Stage:   "credentials",
			Cause:   errors.New("no valid credential sources"),
		}
	}
	return provider.AuthStatus{
		Profile: "sso-dev",
		Account: "123456789012",
		Arn:     "arn:aws:sts::123456789012:assumed-role/test-role/test-session",
		UserID:  "test-session",
	}, nil
}

func TestRecoverAWSAuthRunsAWSLoginForSSOProfiles(t *testing.T) {
	originalLogin := RunAWSLoginFunc
	originalDetect := AWSProfileUsesSSOFunc
	defer func() {
		RunAWSLoginFunc = originalLogin
		AWSProfileUsesSSOFunc = originalDetect
	}()

	loginCalled := false
	RunAWSLoginFunc = func(ctx context.Context, profile string) error {
		loginCalled = true
		if profile != "sso-dev" {
			t.Fatalf("profile = %q, want sso-dev", profile)
		}
		return nil
	}
	AWSProfileUsesSSOFunc = func(ctx context.Context, profile string) bool {
		return profile == "sso-dev"
	}

	prov := &authRetryProvider{}
	status, recovered, err := RecoverAWSAuth(context.Background(), prov, "sso-dev", true)
	if err != nil {
		t.Fatalf("RecoverAWSAuth() error = %v", err)
	}
	if !recovered {
		t.Fatal("RecoverAWSAuth() recovered = false, want true")
	}
	if !loginCalled {
		t.Fatal("RecoverAWSAuth() did not call AWS login")
	}
	if status.Profile != "sso-dev" || status.Account == "" || status.Arn == "" {
		t.Fatalf("status = %#v, want populated caller identity", status)
	}
	if prov.checks != 2 {
		t.Fatalf("auth checks = %d, want 2", prov.checks)
	}
}

func TestRecoverAWSAuthSkipsLoginForPermissionDenied(t *testing.T) {
	originalLogin := RunAWSLoginFunc
	originalDetect := AWSProfileUsesSSOFunc
	defer func() {
		RunAWSLoginFunc = originalLogin
		AWSProfileUsesSSOFunc = originalDetect
	}()

	loginCalled := false
	RunAWSLoginFunc = func(ctx context.Context, profile string) error {
		loginCalled = true
		return nil
	}
	AWSProfileUsesSSOFunc = func(ctx context.Context, profile string) bool {
		return true
	}

	_, recovered, err := RecoverAWSAuth(context.Background(), authDeniedProvider{}, "sso-dev", true)
	if err == nil {
		t.Fatal("RecoverAWSAuth() error = nil, want auth error")
	}
	if recovered {
		t.Fatal("RecoverAWSAuth() recovered = true, want false")
	}
	if loginCalled {
		t.Fatal("RecoverAWSAuth() unexpectedly called AWS login")
	}
}

func TestRecoverAWSAuthSkipsLoginWhenNoninteractive(t *testing.T) {
	originalLogin := RunAWSLoginFunc
	originalDetect := AWSProfileUsesSSOFunc
	defer func() {
		RunAWSLoginFunc = originalLogin
		AWSProfileUsesSSOFunc = originalDetect
	}()

	loginCalled := false
	RunAWSLoginFunc = func(ctx context.Context, profile string) error {
		loginCalled = true
		return nil
	}
	AWSProfileUsesSSOFunc = func(ctx context.Context, profile string) bool {
		return true
	}

	_, recovered, err := RecoverAWSAuth(context.Background(), &authRetryProvider{}, "sso-dev", false)
	if err == nil {
		t.Fatal("RecoverAWSAuth() error = nil, want auth error")
	}
	if recovered {
		t.Fatal("RecoverAWSAuth() recovered = true, want false")
	}
	if loginCalled {
		t.Fatal("RecoverAWSAuth() unexpectedly called AWS login")
	}
}

func TestWizardSelectAWSProfileUsesSingleDiscoveredProfile(t *testing.T) {
	originalList := listAWSProfilesFunc
	listAWSProfilesFunc = func(ctx context.Context) ([]string, error) {
		return []string{"sso-dev"}, nil
	}
	defer func() { listAWSProfilesFunc = originalList }()

	wizard := NewWizard(prompt.NewSession(strings.NewReader(""), &bytes.Buffer{}), &bytes.Buffer{}, nil, &config.Config{})
	profile, prompted, err := wizard.selectAWSProfile(context.Background())
	if err != nil {
		t.Fatalf("selectAWSProfile() error = %v", err)
	}
	if prompted {
		t.Fatal("selectAWSProfile() prompted = true, want false")
	}
	if profile != "sso-dev" {
		t.Fatalf("selectAWSProfile() profile = %q, want sso-dev", profile)
	}
}

func TestWizardRequiresAWSProfileBeforeOtherPrompts(t *testing.T) {
	originalList := listAWSProfilesFunc
	listAWSProfilesFunc = func(ctx context.Context) ([]string, error) {
		return []string{"dev-a", "dev-b"}, nil
	}
	defer func() { listAWSProfilesFunc = originalList }()
	t.Setenv("AWS_PROFILE", "")
	t.Setenv("AWS_DEFAULT_PROFILE", "")

	wizard := NewWizard(prompt.NewSession(strings.NewReader(""), &bytes.Buffer{}), &bytes.Buffer{}, nil, &config.Config{})
	wizard.Prompter.Interactive = false

	_, _, err := wizard.selectAWSProfile(context.Background())
	if err == nil {
		t.Fatal("selectAWSProfile() error = nil, want AWS profile failure")
	}
	want := "AWS profile is required: pass --profile, set AWS_PROFILE, or run interactively"
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("selectAWSProfile() error = %v, want %q", err, want)
	}
}

func TestWizardRunsAWSLoginForSSOProfiles(t *testing.T) {
	originalLogin := RunAWSLoginFunc
	originalDetect := AWSProfileUsesSSOFunc
	defer func() {
		RunAWSLoginFunc = originalLogin
		AWSProfileUsesSSOFunc = originalDetect
	}()

	loginCalled := false
	RunAWSLoginFunc = func(ctx context.Context, profile string) error {
		loginCalled = true
		return nil
	}
	AWSProfileUsesSSOFunc = func(ctx context.Context, profile string) bool {
		return true
	}

	input := strings.Join([]string{
		"alpha",
		"1", // platform aws
		"",  // accept default GPU compute mode
		"1", // region
		"",  // accept default instance type
		"1", // base image
		"20",
		"",
		"",
		"",
		"",
		"",
		"y", // use NemoClaw
		"1", // provider codex
		"http://localhost:11434",
		"y", // confirm summary
	}, "\n") + "\n"

	out := &bytes.Buffer{}
	wizard := NewWizard(
		prompt.NewSession(strings.NewReader(input), out),
		out,
		func(platform, computeClass string) provider.CloudProvider {
			return &loginRetryProvider{
				fakeProvider: fakeProvider{
					regions: []string{"us-east-1", "us-west-2"},
					report: provider.GPUQuotaReport{
						Region:          "us-east-1",
						InstanceFamily:  "g5",
						LikelyCreatable: true,
					},
				},
			}
		},
		&config.Config{},
	)
	wizard.AWSProfile = "sso-dev"
	wizard.Interactive = true

	cfg, err := wizard.Run(context.Background())
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if cfg.Region.Name != "us-east-1" {
		t.Fatalf("Region.Name = %q, want us-east-1", cfg.Region.Name)
	}
	if !loginCalled {
		t.Fatal("AWS login should have been called")
	}
	if !strings.Contains(out.String(), "AWS SSO login refreshed credentials") {
		t.Fatalf("output = %q, want login refresh message", out.String())
	}
}

func TestWizardWarnsAndContinuesWhenQuotaInsufficient(t *testing.T) {
	input := strings.Join([]string{
		"alpha",
		"1", // platform aws
		"",  // accept default GPU compute mode
		"1", // region
		"y", // continue despite quota warning
		"",  // accept default instance type (g5.xlarge)
		"1", // base image
		"20",
		"1",
		"",
		"",
		"",
		"",
		"",
	}, "\n") + "\n"

	quotaUsage := 1
	wizard := NewWizard(
		prompt.NewSession(strings.NewReader(input), &bytes.Buffer{}),
		&bytes.Buffer{},
		func(platform, computeClass string) provider.CloudProvider {
			return fakeProvider{
				regions: []string{"us-east-1", "us-west-2"},
				report: provider.GPUQuotaReport{
					Region:         "us-east-1",
					InstanceFamily: "g5",
					Checks: []provider.GPUQuotaCheck{{
						QuotaName:          "Running On-Demand G and VT instances",
						CurrentLimit:       0,
						CurrentUsage:       &quotaUsage,
						EstimatedRemaining: 0,
						UsageIsEstimated:   true,
					}},
					LikelyCreatable: false,
					Notes:           []string{"request more quota"},
				},
			}
		},
		&config.Config{Region: config.RegionConfig{Name: "us-west-2"}},
	)
	wizard.AWSProfile = "sso-dev"

	cfg, err := wizard.Run(context.Background())
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if cfg.Region.Name != "us-east-1" {
		t.Fatalf("Region.Name = %q, want us-east-1", cfg.Region.Name)
	}
}

func TestWizardFallsBackToBundledLookupsWhenAWSDataIsUnavailable(t *testing.T) {
	input := strings.Join([]string{
		"alpha",
		"1", // platform aws
		"",  // accept default GPU compute mode
		"1", // fallback region us-east-1
		"",  // accept fallback instance type
		"1", // base image
		"20",
		"1",
		"",
		"",
		"",
		"",
		"",
	}, "\n") + "\n"

	out := &bytes.Buffer{}
	wizard := NewWizard(
		prompt.NewSession(strings.NewReader(input), out),
		out,
		func(platform, computeClass string) provider.CloudProvider {
			return fakeProvider{
				regionsErr:       errors.New("access denied"),
				instanceTypesErr: errors.New("timeout"),
				quotaErr:         errors.New("quota unavailable"),
			}
		},
		&config.Config{},
	)
	wizard.AWSProfile = "sso-dev"

	cfg, err := wizard.Run(context.Background())
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if cfg.Region.Name != "us-east-1" {
		t.Fatalf("Region.Name = %q, want us-east-1", cfg.Region.Name)
	}
	if cfg.Instance.Type != "g5.xlarge" {
		t.Fatalf("Instance.Type = %q, want g5.xlarge", cfg.Instance.Type)
	}
	got := out.String()
	for _, fragment := range []string{
		"Warning: AWS region lookup unavailable; using bundled fallback regions.",
		"Warning: AWS instance type lookup unavailable; using bundled fallback instance types.",
	} {
		if !strings.Contains(got, fragment) {
			t.Fatalf("output = %q, want %q", got, fragment)
		}
	}
}

func TestWizardWarnsAndContinuesWhenQuotaCheckUnavailable(t *testing.T) {
	input := strings.Join([]string{
		"alpha",
		"1", // platform aws
		"",  // accept default GPU compute mode
		"1", // region
		"",  // accept default instance type
		"1", // base image
		"20",
		"1",
		"",
		"",
		"",
		"",
		"",
	}, "\n") + "\n"

	out := &bytes.Buffer{}
	wizard := NewWizard(
		prompt.NewSession(strings.NewReader(input), out),
		out,
		func(platform, computeClass string) provider.CloudProvider {
			return fakeProvider{
				regions:  []string{"us-east-1", "us-west-2"},
				quotaErr: errors.New("security token invalid"),
			}
		},
		&config.Config{},
	)
	wizard.AWSProfile = "sso-dev"

	cfg, err := wizard.Run(context.Background())
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if cfg.Region.Name != "us-east-1" {
		t.Fatalf("Region.Name = %q, want us-east-1", cfg.Region.Name)
	}
	if got := out.String(); !strings.Contains(got, "Warning: GPU quota check unavailable; continuing.") {
		t.Fatalf("output = %q, want quota warning", got)
	}
}

func TestWizardFallsBackToBundledImagesWhenSSMIsUnavailable(t *testing.T) {
	input := strings.Join([]string{
		"alpha",
		"1", // platform aws
		"",  // accept default GPU compute mode
		"1", // region
		"",  // accept default instance type
		"1", // bundled fallback image
		"20",
		"1",
		"",
		"",
		"",
		"",
		"",
	}, "\n") + "\n"

	out := &bytes.Buffer{}
	wizard := NewWizard(
		prompt.NewSession(strings.NewReader(input), out),
		out,
		func(platform, computeClass string) provider.CloudProvider {
			return fakeProvider{
				regions: []string{"us-east-1", "us-west-2"},
				report: provider.GPUQuotaReport{
					Region:          "us-east-1",
					InstanceFamily:  "g5",
					LikelyCreatable: true,
				},
			}
		},
		&config.Config{},
	)
	wizard.AWSProfile = "sso-dev"
	wizard.Provider = failingImageProvider{fakeProvider: fakeProvider{
		regions: []string{"us-east-1", "us-west-2"},
		report: provider.GPUQuotaReport{
			Region:          "us-east-1",
			InstanceFamily:  "g5",
			LikelyCreatable: true,
		},
	}}

	cfg, err := wizard.Run(context.Background())
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if cfg.Image.Name != "AWS Deep Learning AMI GPU Ubuntu 22.04" {
		t.Fatalf("Image.Name = %q, want bundled fallback image", cfg.Image.Name)
	}
	if got := out.String(); !strings.Contains(got, "Warning: AWS image lookup unavailable; using bundled fallback images.") {
		t.Fatalf("output = %q, want image lookup warning", got)
	}
}

func TestWizardFallsBackToBundledImagesWhenImageLookupFails(t *testing.T) {
	input := strings.Join([]string{
		"alpha",
		"1", // platform aws
		"",  // accept default GPU compute mode
		"1", // region
		"",  // accept default instance type
		"1", // bundled fallback image
		"20",
		"1",
		"",
		"",
		"",
		"",
		"",
	}, "\n") + "\n"

	out := &bytes.Buffer{}
	wizard := NewWizard(
		prompt.NewSession(strings.NewReader(input), out),
		out,
		func(platform, computeClass string) provider.CloudProvider {
			return fakeProvider{
				regions: []string{"us-east-1", "us-west-2"},
				report: provider.GPUQuotaReport{
					Region:          "us-east-1",
					InstanceFamily:  "g5",
					LikelyCreatable: true,
				},
			}
		},
		&config.Config{},
	)
	wizard.AWSProfile = "sso-dev"
	wizard.Provider = genericFailingImageProvider{fakeProvider: fakeProvider{
		regions: []string{"us-east-1", "us-west-2"},
		report: provider.GPUQuotaReport{
			Region:          "us-east-1",
			InstanceFamily:  "g5",
			LikelyCreatable: true,
		},
	}}

	cfg, err := wizard.Run(context.Background())
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if cfg.Image.Name != "AWS Deep Learning AMI GPU Ubuntu 22.04" {
		t.Fatalf("Image.Name = %q, want bundled fallback image", cfg.Image.Name)
	}
	if got := out.String(); !strings.Contains(got, "Warning: AWS image lookup unavailable; using bundled fallback images.") {
		t.Fatalf("output = %q, want image lookup warning", got)
	}
}

func TestWizardConfiguresGitHubUserAuthFromRepoOrigin(t *testing.T) {
	originalRemote := gitRemoteOriginURLFunc
	originalToken := runGitHubAuthTokenFunc
	originalLogin := runGitHubAuthLoginFunc
	originalStore := storeGitHubTokenFunc
	defer func() {
		gitRemoteOriginURLFunc = originalRemote
		runGitHubAuthTokenFunc = originalToken
		runGitHubAuthLoginFunc = originalLogin
		storeGitHubTokenFunc = originalStore
	}()

	gitRemoteOriginURLFunc = func(ctx context.Context) (string, error) {
		return "git@github.com:owner/repo.git", nil
	}
	runGitHubAuthTokenFunc = func(ctx context.Context) (string, error) {
		return "gho_test_token", nil
	}
	runGitHubAuthLoginFunc = func(ctx context.Context) error {
		t.Fatal("runGitHubAuthLoginFunc should not be called when a token is already available")
		return nil
	}
	var storedProfile, storedRegion, storedSecretName, storedToken string
	storeGitHubTokenFunc = func(ctx context.Context, profile, region, secretName, token string) (string, error) {
		storedProfile, storedRegion, storedSecretName, storedToken = profile, region, secretName, token
		return "arn:aws:secretsmanager:ap-northeast-1:123456789012:secret:agenthub/github-token/owner/repo", nil
	}

	input := strings.Join([]string{
		"alpha",
		"1", // platform aws
		"",  // accept default GPU compute mode
		"1", // region
		"",  // accept default instance type
		"1", // base image
		"20",
		"",
		"",
		"",
		"",
		"y", // configure GitHub access
		"1", // select user auth
		"y", // use NemoClaw
		"1", // provider codex
		"http://localhost:11434",
		"y", // confirm summary
	}, "\n") + "\n"

	out := &bytes.Buffer{}
	wizard := NewWizard(
		prompt.NewSession(strings.NewReader(input), out),
		out,
		func(platform, computeClass string) provider.CloudProvider {
			return fakeProvider{
				regions: []string{"us-east-1", "us-west-2"},
				report: provider.GPUQuotaReport{
					Region:          "us-east-1",
					InstanceFamily:  "g5",
					LikelyCreatable: true,
				},
			}
		},
		&config.Config{},
	)
	wizard.AWSProfile = "sso-dev"

	cfg, err := wizard.Run(context.Background())
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if cfg.GitHub.AuthMode != config.GitHubAuthModeUser {
		t.Fatalf("GitHub.AuthMode = %q, want %q", cfg.GitHub.AuthMode, config.GitHubAuthModeUser)
	}
	if cfg.GitHub.TokenSecretARN == "" {
		t.Fatal("GitHub.TokenSecretARN = empty, want value")
	}
	if storedProfile != "sso-dev" || storedRegion != "us-east-1" {
		t.Fatalf("store profile/region = %q/%q, want sso-dev/us-east-1", storedProfile, storedRegion)
	}
	if storedSecretName != "agenthub/github-token/owner/repo" {
		t.Fatalf("store secret name = %q, want agenthub/github-token/owner/repo", storedSecretName)
	}
	if storedToken != "gho_test_token" {
		t.Fatalf("store token = %q, want gho_test_token", storedToken)
	}
	// logLine writes "[Access] ! detected GitHub repo candidate: owner/repo"
	if got := out.String(); !strings.Contains(got, "detected GitHub repo candidate: owner/repo") {
		t.Fatalf("output = %q, want repo candidate", got)
	}
}

type failingImageProvider struct {
	fakeProvider
}

func (failingImageProvider) ListBaseImages(ctx context.Context, region string) ([]provider.BaseImage, error) {
	return nil, &awsprovider.AuthError{
		Kind:    "api_call_failed",
		Profile: "test-profile",
		Stage:   "api",
		Cause:   errors.New("security token invalid"),
	}
}
func (failingImageProvider) RecommendBaseImages(ctx context.Context, region, computeClass string) ([]provider.BaseImage, error) {
	return nil, &awsprovider.AuthError{
		Kind:    "api_call_failed",
		Profile: "test-profile",
		Stage:   "api",
		Cause:   errors.New("security token invalid"),
	}
}

type genericFailingImageProvider struct {
	fakeProvider
}

func (genericFailingImageProvider) ListBaseImages(ctx context.Context, region string) ([]provider.BaseImage, error) {
	return nil, errors.New("dial tcp: lookup ssm.ap-northeast-1.amazonaws.com: no such host")
}
func (genericFailingImageProvider) RecommendBaseImages(ctx context.Context, region, computeClass string) ([]provider.BaseImage, error) {
	return nil, errors.New("dial tcp: lookup ssm.ap-northeast-1.amazonaws.com: no such host")
}
