package app

import (
	"context"
	"os"
	"testing"

	"agenthub/internal/config"
	"agenthub/internal/host"
	"agenthub/internal/setup"
)

func TestMain(m *testing.M) {
	originalOrigin := gitRemoteOriginURLFunc
	originalAppToken := loadGitHubInstallationToken
	originalUserToken := loadGitHubUserToken
	originalGitIdentity := setup.LookupGitIdentityFunc
	originalRemoteVerify := verifyRemoteGitHubAccessFunc
	gitRemoteOriginURLFunc = func(ctx context.Context) (string, error) {
		return "git@github.com:owner/repo.git", nil
	}
	loadGitHubInstallationToken = func(ctx context.Context, profile, region string, cfg config.GitHubConfig) (string, error) {
		return "test-app-token", nil
	}
	loadGitHubUserToken = func(ctx context.Context, profile, region, secretID string) (string, error) {
		return "test-user-token", nil
	}
	setup.LookupGitIdentityFunc = func(ctx context.Context) (setup.GitIdentity, error) {
		return setup.GitIdentity{Name: "Test User", Email: "test@example.com"}, nil
	}
	verifyRemoteGitHubAccessFunc = func(ctx context.Context, exec host.Executor, repoURL string) error {
		return nil
	}

	code := m.Run()

	gitRemoteOriginURLFunc = originalOrigin
	loadGitHubInstallationToken = originalAppToken
	loadGitHubUserToken = originalUserToken
	setup.LookupGitIdentityFunc = originalGitIdentity
	verifyRemoteGitHubAccessFunc = originalRemoteVerify
	os.Exit(code)
}
