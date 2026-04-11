package app

import (
	"context"
	"testing"

	"agenthub/internal/config"
	"agenthub/internal/host"
	"agenthub/internal/runtimeinstall"
)

type testHostExecutor struct {
	runFn func(ctx context.Context, command string, args ...string) (host.CommandResult, error)
}

func (e *testHostExecutor) Run(ctx context.Context, command string, args ...string) (host.CommandResult, error) {
	return e.runFn(ctx, command, args...)
}

func (e *testHostExecutor) Upload(ctx context.Context, localPath, remotePath string) error {
	return nil
}

func TestHasGitHubRuntimeConfigAcceptsUserAuth(t *testing.T) {
	cfg := &runtimeinstall.RuntimeConfig{
		GitHub: config.GitHubConfig{
			AuthMode:       config.GitHubAuthModeUser,
			TokenSecretARN: "arn:aws:secretsmanager:ap-northeast-1:123456789012:secret:agenthub/github-token",
		},
	}

	if !hasGitHubRuntimeConfig(cfg) {
		t.Fatal("hasGitHubRuntimeConfig() = false, want true for user auth")
	}
}

func TestHasGitHubRuntimeConfigRejectsIncompleteAppAuth(t *testing.T) {
	cfg := &runtimeinstall.RuntimeConfig{
		GitHub: config.GitHubConfig{
			AuthMode:            config.GitHubAuthModeApp,
			AppID:               "123456",
			PrivateKeySecretARN: "arn:aws:secretsmanager:ap-northeast-1:123456789012:secret:agenthub/github-app-private-key",
		},
	}

	if hasGitHubRuntimeConfig(cfg) {
		t.Fatal("hasGitHubRuntimeConfig() = true, want false for incomplete app auth")
	}
}

func TestNormalizeGitHubRemoteURL(t *testing.T) {
	cases := map[string]string{
		"https://github.com/owner/repo.git":   "https://github.com/owner/repo",
		"git@github.com:owner/repo.git":       "https://github.com/owner/repo",
		"ssh://git@github.com/owner/repo.git": "https://github.com/owner/repo",
		"https://github.com/owner/repo":       "https://github.com/owner/repo",
	}
	for input, want := range cases {
		got, err := normalizeGitHubRemoteURL(input)
		if err != nil {
			t.Fatalf("normalizeGitHubRemoteURL(%q) error = %v", input, err)
		}
		if got != want {
			t.Fatalf("normalizeGitHubRemoteURL(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestNormalizeGitHubRemoteURLRejectsNonGitHub(t *testing.T) {
	for _, input := range []string{
		"git@bitbucket.org:owner/repo.git",
		"https://example.com/owner/repo.git",
		"not-a-remote",
		"https://github.com/owner",
	} {
		if _, err := normalizeGitHubRemoteURL(input); err == nil {
			t.Fatalf("normalizeGitHubRemoteURL(%q) error = nil, want rejection", input)
		}
	}
}

func TestVerifyLocalGitHubAuthUsesAppInstallationToken(t *testing.T) {
	original := loadGitHubInstallationToken
	defer func() { loadGitHubInstallationToken = original }()
	called := false
	loadGitHubInstallationToken = func(ctx context.Context, region string, cfg config.GitHubConfig) (string, error) {
		called = true
		if region != "us-east-1" {
			t.Fatalf("region = %q, want us-east-1", region)
		}
		return "token", nil
	}
	cfg := &config.Config{
		Region: config.RegionConfig{Name: "us-east-1"},
		GitHub: config.GitHubConfig{AuthMode: config.GitHubAuthModeApp},
	}
	if err := verifyLocalGitHubAuth(context.Background(), cfg); err != nil {
		t.Fatalf("verifyLocalGitHubAuth() error = %v", err)
	}
	if !called {
		t.Fatal("verifyLocalGitHubAuth() did not mint an installation token")
	}
}

func TestVerifyLocalGitHubAuthUsesUserToken(t *testing.T) {
	original := loadGitHubUserToken
	defer func() { loadGitHubUserToken = original }()
	called := false
	loadGitHubUserToken = func(ctx context.Context, region, secretID string) (string, error) {
		called = true
		if secretID != "arn:aws:secretsmanager:us-east-1:123456789012:secret:agenthub/github-token" {
			t.Fatalf("secretID = %q, want configured token secret", secretID)
		}
		return "token", nil
	}
	cfg := &config.Config{
		Region: config.RegionConfig{Name: "us-east-1"},
		GitHub: config.GitHubConfig{AuthMode: config.GitHubAuthModeUser, TokenSecretARN: "arn:aws:secretsmanager:us-east-1:123456789012:secret:agenthub/github-token"},
	}
	if err := verifyLocalGitHubAuth(context.Background(), cfg); err != nil {
		t.Fatalf("verifyLocalGitHubAuth() error = %v", err)
	}
	if !called {
		t.Fatal("verifyLocalGitHubAuth() did not load the user token")
	}
}

func TestVerifyRemoteGitHubAccess(t *testing.T) {
	exec := &testHostExecutor{
		runFn: func(ctx context.Context, command string, args ...string) (host.CommandResult, error) {
			switch {
			case command != "git":
				t.Fatalf("unexpected command: %s %v", command, args)
			case len(args) == 4 && args[0] == "config" && args[1] == "--global" && args[2] == "--get" && args[3] == "credential.helper":
				return host.CommandResult{Stdout: "!/opt/agenthub/bin/agenthub github credential --runtime-config /opt/agenthub/runtime.yaml"}, nil
			case len(args) == 4 && args[0] == "config" && args[1] == "--global" && args[2] == "--get" && args[3] == "url.https://github.com/.insteadof":
				return host.CommandResult{Stdout: "git@github.com:"}, nil
			case len(args) == 2 && args[0] == "ls-remote" && args[1] == "https://github.com/owner/repo":
				return host.CommandResult{Stdout: "deadbeef\tHEAD"}, nil
			default:
				t.Fatalf("unexpected command: %s %v", command, args)
			}
			return host.CommandResult{}, nil
		},
	}
	if err := verifyRemoteGitHubAccess(context.Background(), exec, "https://github.com/owner/repo"); err != nil {
		t.Fatalf("verifyRemoteGitHubAccess() error = %v", err)
	}
}

func TestVerifyRemoteGitHubAccessRejectsEmptyLsRemoteOutput(t *testing.T) {
	exec := &testHostExecutor{
		runFn: func(ctx context.Context, command string, args ...string) (host.CommandResult, error) {
			switch {
			case command != "git":
				t.Fatalf("unexpected command: %s %v", command, args)
			case len(args) == 4 && args[0] == "config" && args[1] == "--global" && args[2] == "--get" && args[3] == "credential.helper":
				return host.CommandResult{Stdout: "!/opt/agenthub/bin/agenthub github credential --runtime-config /opt/agenthub/runtime.yaml"}, nil
			case len(args) == 4 && args[0] == "config" && args[1] == "--global" && args[2] == "--get" && args[3] == "url.https://github.com/.insteadof":
				return host.CommandResult{Stdout: "git@github.com:"}, nil
			case len(args) == 2 && args[0] == "ls-remote" && args[1] == "https://github.com/owner/repo":
				return host.CommandResult{Stdout: ""}, nil
			default:
				t.Fatalf("unexpected command: %s %v", command, args)
			}
			return host.CommandResult{}, nil
		},
	}
	if err := verifyRemoteGitHubAccess(context.Background(), exec, "https://github.com/owner/repo"); err == nil {
		t.Fatal("verifyRemoteGitHubAccess() error = nil, want failure for empty ls-remote output")
	}
}

func TestVerifyGitHubCredentialHelperConfigured(t *testing.T) {
	exec := &testHostExecutor{
		runFn: func(ctx context.Context, command string, args ...string) (host.CommandResult, error) {
			switch {
			case command != "git":
				t.Fatalf("unexpected command: %s %v", command, args)
			case len(args) == 4 && args[0] == "config" && args[1] == "--global" && args[2] == "--get" && args[3] == "credential.helper":
				return host.CommandResult{Stdout: "!/opt/agenthub/bin/agenthub github credential --runtime-config /opt/agenthub/runtime.yaml"}, nil
			case len(args) == 4 && args[0] == "config" && args[1] == "--global" && args[2] == "--get" && args[3] == "url.https://github.com/.insteadof":
				return host.CommandResult{Stdout: "git@github.com:"}, nil
			default:
				t.Fatalf("unexpected command: %s %v", command, args)
			}
			return host.CommandResult{}, nil
		},
	}
	if err := verifyGitHubCredentialHelperConfigured(context.Background(), exec); err != nil {
		t.Fatalf("verifyGitHubCredentialHelperConfigured() error = %v", err)
	}
}
