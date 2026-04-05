package setup

import (
	"context"
	"errors"
	"testing"

	"agenthub/internal/config"
)

func TestParseGitHubRepoSlug(t *testing.T) {
	for remote, want := range map[string]string{
		"git@github.com:owner/repo.git":       "owner/repo",
		"ssh://git@github.com/owner/repo.git": "owner/repo",
		"https://github.com/owner/repo.git":   "owner/repo",
		"http://github.com/owner/repo":        "owner/repo",
		"git@github.com:owner/sub/repo.git":   "",
		"git@github.com:owner":                "",
		"https://example.com/owner/repo.git":  "",
		"":                                    "",
		"   ":                                 "",
	} {
		if got := parseGitHubRepoSlug(remote); got != want {
			t.Fatalf("parseGitHubRepoSlug(%q) = %q, want %q", remote, got, want)
		}
	}
}

func TestDefaultGitHubUserSecretName(t *testing.T) {
	if got, want := defaultGitHubUserSecretName("owner/repo"), "agenthub/github-token/owner/repo"; got != want {
		t.Fatalf("defaultGitHubUserSecretName() = %q, want %q", got, want)
	}
	if got, want := defaultGitHubUserSecretName(""), "agenthub/github-token"; got != want {
		t.Fatalf("defaultGitHubUserSecretName() = %q, want %q", got, want)
	}
}

func TestBootstrapGitHubUserAuthStoresTokenSecret(t *testing.T) {
	originalToken := runGitHubAuthTokenFunc
	originalLogin := runGitHubAuthLoginFunc
	originalStore := storeGitHubTokenFunc
	defer func() {
		runGitHubAuthTokenFunc = originalToken
		runGitHubAuthLoginFunc = originalLogin
		storeGitHubTokenFunc = originalStore
	}()

	tokenCalls := 0
	runGitHubAuthTokenFunc = func(ctx context.Context) (string, error) {
		tokenCalls++
		if tokenCalls == 1 {
			return "", errors.New("not logged in")
		}
		return "gho_test_token", nil
	}
	loginCalls := 0
	runGitHubAuthLoginFunc = func(ctx context.Context) error {
		loginCalls++
		return nil
	}
	var gotProfile, gotRegion, gotSecretName, gotToken string
	storeGitHubTokenFunc = func(ctx context.Context, profile, region, secretName, token string) (string, error) {
		gotProfile, gotRegion, gotSecretName, gotToken = profile, region, secretName, token
		return "arn:aws:secretsmanager:ap-northeast-1:123456789012:secret:agenthub/github-token/owner/repo", nil
	}

	cfg, err := bootstrapGitHubUserAuth(context.Background(), "dev-profile", "ap-northeast-1", "owner/repo")
	if err != nil {
		t.Fatalf("bootstrapGitHubUserAuth() error = %v", err)
	}
	if cfg.AuthMode != config.GitHubAuthModeUser {
		t.Fatalf("AuthMode = %q, want %q", cfg.AuthMode, config.GitHubAuthModeUser)
	}
	if cfg.TokenSecretARN == "" {
		t.Fatal("TokenSecretARN = empty, want value")
	}
	if tokenCalls != 2 {
		t.Fatalf("tokenCalls = %d, want 2", tokenCalls)
	}
	if loginCalls != 1 {
		t.Fatalf("loginCalls = %d, want 1", loginCalls)
	}
	if gotProfile != "dev-profile" || gotRegion != "ap-northeast-1" {
		t.Fatalf("store call profile/region = %q/%q, want dev-profile/ap-northeast-1", gotProfile, gotRegion)
	}
	if gotSecretName != "agenthub/github-token/owner/repo" {
		t.Fatalf("store call secretName = %q, want %q", gotSecretName, "agenthub/github-token/owner/repo")
	}
	if gotToken != "gho_test_token" {
		t.Fatalf("store call token = %q, want %q", gotToken, "gho_test_token")
	}
}

func TestDetectGitHubRepoSlugFromRemoteURL(t *testing.T) {
	original := gitRemoteOriginURLFunc
	defer func() { gitRemoteOriginURLFunc = original }()
	gitRemoteOriginURLFunc = func(ctx context.Context) (string, error) {
		return "git@github.com:owner/repo.git", nil
	}

	got, err := detectGitHubRepoSlug(context.Background())
	if err != nil {
		t.Fatalf("detectGitHubRepoSlug() error = %v", err)
	}
	if got != "owner/repo" {
		t.Fatalf("detectGitHubRepoSlug() = %q, want owner/repo", got)
	}
}

func TestDetectGitHubRepoSlugReturnsEmptyForNonGitHubRemote(t *testing.T) {
	original := gitRemoteOriginURLFunc
	defer func() { gitRemoteOriginURLFunc = original }()
	gitRemoteOriginURLFunc = func(ctx context.Context) (string, error) {
		return "https://example.com/owner/repo.git", nil
	}

	got, err := detectGitHubRepoSlug(context.Background())
	if err != nil {
		t.Fatalf("detectGitHubRepoSlug() error = %v", err)
	}
	if got != "" {
		t.Fatalf("detectGitHubRepoSlug() = %q, want empty", got)
	}
}
