package setup

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"agenthub/internal/provider"
	awsprovider "agenthub/internal/provider/aws"
)

var RunAWSLoginFunc = defaultRunAWSLogin
var AWSProfileUsesSSOFunc = defaultAWSProfileUsesSSO

func RecoverAWSAuth(ctx context.Context, prov provider.CloudProvider, profile string, interactive bool) (provider.AuthStatus, bool, error) {
	if prov == nil {
		return provider.AuthStatus{}, false, errors.New("AWS provider is required")
	}

	status, err := prov.CheckAuth(ctx)
	if err == nil {
		return status, false, nil
	}

	if !interactive {
		return provider.AuthStatus{}, false, err
	}

	var authErr *awsprovider.AuthError
	if !errors.As(err, &authErr) || authErr.Kind != "no_credentials" {
		return provider.AuthStatus{}, false, err
	}

	if !AWSProfileUsesSSOFunc(ctx, profile) {
		return provider.AuthStatus{}, false, err
	}

	if loginErr := RunAWSLoginFunc(ctx, profile); loginErr != nil {
		return provider.AuthStatus{}, true, loginErr
	}

	status, err = prov.CheckAuth(ctx)
	if err != nil {
		return provider.AuthStatus{}, true, err
	}

	return status, true, nil
}

func defaultRunAWSLogin(ctx context.Context, profile string) error {
	profile = strings.TrimSpace(profile)
	if profile == "" {
		return errors.New("AWS profile is required for login")
	}
	if _, err := exec.LookPath("aws"); err != nil {
		return errors.New("aws CLI is required for AWS SSO login")
	}
	// Do not tie the browser login helper to the caller context.
	// AWS SSO may take a while while the user completes the web flow,
	// and canceling the parent command should not kill the login helper
	// once it has launched the browser callback.
	cmd := exec.Command("aws", "sso", "login", "--profile", profile)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("run aws sso login: %w", err)
	}
	return nil
}

func defaultAWSProfileUsesSSO(ctx context.Context, profile string) bool {
	profile = strings.TrimSpace(profile)
	if profile == "" {
		return false
	}
	if value, err := awsConfigureGet(ctx, profile, "sso_start_url"); err == nil && strings.TrimSpace(value) != "" {
		return true
	}
	if value, err := awsConfigureGet(ctx, profile, "sso_session"); err == nil && strings.TrimSpace(value) != "" {
		return true
	}
	return false
}

func awsConfigureGet(ctx context.Context, profile, key string) (string, error) {
	cmd := exec.CommandContext(ctx, "aws", "configure", "get", key, "--profile", profile)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
