package aws

import (
	"context"
	"errors"
	"testing"

	awsbase "github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/aws/smithy-go"
)

func TestAuthCheckReturnsProfileNotFound(t *testing.T) {
	p := &Provider{
		Config: Config{Profile: "missing"},
		loadDefaultConfig: func(ctx context.Context, optFns ...func(*awsconfig.LoadOptions) error) (awsbase.Config, error) {
			return awsbase.Config{}, errors.New("failed to load shared config profile, missing")
		},
	}

	_, err := p.AuthCheck(context.Background())
	if err == nil {
		t.Fatal("AuthCheck() error = nil")
	}
	authErr := mustAuthError(t, err)
	if authErr.Kind != "profile_not_found" {
		t.Fatalf("AuthError.Kind = %q, want profile_not_found", authErr.Kind)
	}
}

func TestAuthCheckReturnsNoCredentials(t *testing.T) {
	p := &Provider{
		Config: Config{Profile: "test-profile"},
		loadDefaultConfig: func(ctx context.Context, optFns ...func(*awsconfig.LoadOptions) error) (awsbase.Config, error) {
			return awsbase.Config{
				Region:      "us-east-1",
				Credentials: awsbase.NewCredentialsCache(failingCredentialsProvider{}),
			}, nil
		},
		newSTSClient: func(cfg awsbase.Config) stsClient {
			t.Fatal("STS client should not be called when credentials cannot be retrieved")
			return nil
		},
	}

	_, err := p.AuthCheck(context.Background())
	if err == nil {
		t.Fatal("AuthCheck() error = nil")
	}
	authErr := mustAuthError(t, err)
	if authErr.Kind != "no_credentials" {
		t.Fatalf("AuthError.Kind = %q, want no_credentials", authErr.Kind)
	}
}

func TestAuthCheckReturnsPermissionDenied(t *testing.T) {
	p := &Provider{
		Config: Config{Profile: "test-profile"},
		loadDefaultConfig: func(ctx context.Context, optFns ...func(*awsconfig.LoadOptions) error) (awsbase.Config, error) {
			return awsbase.Config{
				Region:      "us-east-1",
				Credentials: awsbase.NewCredentialsCache(staticCredentialsProvider{}),
			}, nil
		},
		newSTSClient: func(cfg awsbase.Config) stsClient {
			return failingSTSClient{err: accessDeniedError{}}
		},
	}

	_, err := p.AuthCheck(context.Background())
	if err == nil {
		t.Fatal("AuthCheck() error = nil")
	}
	authErr := mustAuthError(t, err)
	if authErr.Kind != "permission_denied" {
		t.Fatalf("AuthError.Kind = %q, want permission_denied", authErr.Kind)
	}
}

func TestAuthCheckReturnsCallerIdentity(t *testing.T) {
	p := &Provider{
		Config: Config{Profile: "test-profile"},
		loadDefaultConfig: func(ctx context.Context, optFns ...func(*awsconfig.LoadOptions) error) (awsbase.Config, error) {
			return awsbase.Config{
				Region:      "us-east-1",
				Credentials: awsbase.NewCredentialsCache(staticCredentialsProvider{}),
			}, nil
		},
		newSTSClient: func(cfg awsbase.Config) stsClient {
			return failingSTSClient{
				out: &sts.GetCallerIdentityOutput{
					Account: awsbase.String("123456789012"),
					Arn:     awsbase.String("arn:aws:sts::123456789012:assumed-role/test-role/test-session"),
					UserId:  awsbase.String("test-session"),
				},
			}
		},
	}

	status, err := p.AuthCheck(context.Background())
	if err != nil {
		t.Fatalf("AuthCheck() error = %v", err)
	}
	if status.Profile != "test-profile" || status.Account != "123456789012" || status.Arn == "" {
		t.Fatalf("AuthCheck() status = %#v", status)
	}
}

func TestListBaseImagesResolvesRegionSpecificAMI(t *testing.T) {
	p := &Provider{
		Config: Config{Profile: "test-profile"},
		loadDefaultConfig: func(ctx context.Context, optFns ...func(*awsconfig.LoadOptions) error) (awsbase.Config, error) {
			return awsbase.Config{Region: "us-east-1"}, nil
		},
		newSSMClient: func(cfg awsbase.Config) ssmClient {
			if cfg.Region != "ap-northeast-1" {
				t.Fatalf("cfg.Region = %q, want ap-northeast-1", cfg.Region)
			}
			return fakeSSMClient{
				value: "ami-0123456789abcdef0",
			}
		},
	}

	images, err := p.ListBaseImages(context.Background(), "ap-northeast-1")
	if err != nil {
		t.Fatalf("ListBaseImages() error = %v", err)
	}
	if len(images) != 1 {
		t.Fatalf("ListBaseImages() len = %d, want 1", len(images))
	}
	got := images[0]
	if got.Name != "AWS Deep Learning AMI GPU Ubuntu 22.04" {
		t.Fatalf("image name = %q", got.Name)
	}
	if got.ID != "ami-0123456789abcdef0" {
		t.Fatalf("image ID = %q", got.ID)
	}
	if got.Region != "ap-northeast-1" || got.SSMParameter == "" || got.Source != "aws-ssm-public-parameter" {
		t.Fatalf("image metadata = %#v", got)
	}
}

func mustAuthError(t *testing.T, err error) *AuthError {
	t.Helper()
	var authErr *AuthError
	if !errors.As(err, &authErr) {
		t.Fatalf("error %v is not *AuthError", err)
	}
	return authErr
}

type failingCredentialsProvider struct{}

func (failingCredentialsProvider) Retrieve(ctx context.Context) (awsbase.Credentials, error) {
	return awsbase.Credentials{}, errors.New("no valid credential sources")
}

type staticCredentialsProvider struct{}

func (staticCredentialsProvider) Retrieve(ctx context.Context) (awsbase.Credentials, error) {
	return awsbase.Credentials{
		AccessKeyID:     "AKIA1234567890TEST",
		SecretAccessKey: "secret",
		SessionToken:    "token",
		Source:          "test",
	}, nil
}

type failingSTSClient struct {
	out *sts.GetCallerIdentityOutput
	err error
}

func (f failingSTSClient) GetCallerIdentity(ctx context.Context, params *sts.GetCallerIdentityInput, optFns ...func(*sts.Options)) (*sts.GetCallerIdentityOutput, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.out, nil
}

type accessDeniedError struct{}

func (accessDeniedError) Error() string {
	return "AccessDenied: denied"
}

func (accessDeniedError) ErrorCode() string {
	return "AccessDenied"
}

func (accessDeniedError) ErrorMessage() string {
	return "denied"
}

func (accessDeniedError) ErrorFault() smithy.ErrorFault {
	return smithy.FaultClient
}

type fakeSSMClient struct {
	value string
	err   error
}

func (f fakeSSMClient) GetParameter(ctx context.Context, params *ssm.GetParameterInput, optFns ...func(*ssm.Options)) (*ssm.GetParameterOutput, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &ssm.GetParameterOutput{
		Parameter: &ssmtypes.Parameter{
			Value: awsbase.String(f.value),
		},
	}, nil
}
