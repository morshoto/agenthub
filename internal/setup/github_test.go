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

func TestBootstrapGitHubSSHCloneStoresKeyAndRegistersDeployKey(t *testing.T) {
	originalEnsure := ensureGitHubSSHPrivateKeyFunc
	originalDerive := deriveGitHubSSHPublicKeyFunc
	originalRegister := runGitHubAPIRepoDeployKeyFunc
	originalStore := storeGitHubSSHKeyFunc
	defer func() {
		ensureGitHubSSHPrivateKeyFunc = originalEnsure
		deriveGitHubSSHPublicKeyFunc = originalDerive
		runGitHubAPIRepoDeployKeyFunc = originalRegister
		storeGitHubSSHKeyFunc = originalStore
	}()

	ensureGitHubSSHPrivateKeyFunc = func(ctx context.Context, privateKeyPath string) (string, error) {
		if privateKeyPath != "~/.ssh/custom-deploy-key" {
			t.Fatalf("privateKeyPath = %q, want custom path", privateKeyPath)
		}
		return "/tmp/custom-deploy-key", nil
	}
	deriveGitHubSSHPublicKeyFunc = func(ctx context.Context, privateKeyPath string) (string, error) {
		if privateKeyPath != "/tmp/custom-deploy-key" {
			t.Fatalf("derive privateKeyPath = %q, want resolved path", privateKeyPath)
		}
		return "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITestDeployKey agenthub", nil
	}
	var gotRepoSlug, gotTitle, gotPublicKey string
	runGitHubAPIRepoDeployKeyFunc = func(ctx context.Context, repoSlug, title, publicKey string) error {
		gotRepoSlug, gotTitle, gotPublicKey = repoSlug, title, publicKey
		return nil
	}
	var gotProfile, gotRegion, gotSecretName, gotPrivateKey string
	storeGitHubSSHKeyFunc = func(ctx context.Context, profile, region, secretName, privateKey string) (string, error) {
		gotProfile, gotRegion, gotSecretName, gotPrivateKey = profile, region, secretName, privateKey
		return "arn:aws:secretsmanager:ap-northeast-1:123456789012:secret:agenthub/github-ssh-key/owner/repo", nil
	}

	cfg, resolvedPath, err := bootstrapGitHubSSHClone(context.Background(), "dev-profile", "ap-northeast-1", "owner/repo", "~/.ssh/custom-deploy-key")
	if err != nil {
		t.Fatalf("bootstrapGitHubSSHClone() error = %v", err)
	}
	if resolvedPath != "/tmp/custom-deploy-key" {
		t.Fatalf("resolvedPath = %q, want /tmp/custom-deploy-key", resolvedPath)
	}
	if cfg.SSHKeySecretARN == "" {
		t.Fatal("SSHKeySecretARN = empty, want value")
	}
	if gotRepoSlug != "owner/repo" {
		t.Fatalf("repoSlug = %q, want owner/repo", gotRepoSlug)
	}
	if gotTitle != "agenthub deploy key for owner/repo" {
		t.Fatalf("title = %q, want deploy key title", gotTitle)
	}
	if gotPublicKey != "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITestDeployKey agenthub" {
		t.Fatalf("public key = %q, want deploy key", gotPublicKey)
	}
	if gotProfile != "dev-profile" || gotRegion != "ap-northeast-1" {
		t.Fatalf("store profile/region = %q/%q, want dev-profile/ap-northeast-1", gotProfile, gotRegion)
	}
	if gotSecretName != "agenthub/github-ssh-key/owner/repo" {
		t.Fatalf("secretName = %q, want %q", gotSecretName, "agenthub/github-ssh-key/owner/repo")
	}
	if gotPrivateKey != "" {
		t.Fatalf("privateKey = %q, want empty string from fixture path", gotPrivateKey)
	}
}

func TestBootstrapGitHubSSHCloneReusesExistingDeployKeyOn422(t *testing.T) {
	originalEnsure := ensureGitHubSSHPrivateKeyFunc
	originalDerive := deriveGitHubSSHPublicKeyFunc
	originalRegister := runGitHubAPIRepoDeployKeyFunc
	originalLookup := runGitHubAPIRepoDeployKeysFunc
	originalStore := storeGitHubSSHKeyFunc
	defer func() {
		ensureGitHubSSHPrivateKeyFunc = originalEnsure
		deriveGitHubSSHPublicKeyFunc = originalDerive
		runGitHubAPIRepoDeployKeyFunc = originalRegister
		runGitHubAPIRepoDeployKeysFunc = originalLookup
		storeGitHubSSHKeyFunc = originalStore
	}()

	ensureGitHubSSHPrivateKeyFunc = func(ctx context.Context, privateKeyPath string) (string, error) {
		return "/tmp/custom-deploy-key", nil
	}
	deriveGitHubSSHPublicKeyFunc = func(ctx context.Context, privateKeyPath string) (string, error) {
		return "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITestDeployKey agenthub", nil
	}
	runGitHubAPIRepoDeployKeyFunc = func(ctx context.Context, repoSlug, title, publicKey string) error {
		return errGitHubDeployKeyAlreadyInUse
	}
	lookupCalls := 0
	runGitHubAPIRepoDeployKeysFunc = func(ctx context.Context, repoSlug string) ([]githubDeployKey, error) {
		lookupCalls++
		return []githubDeployKey{
			{ID: 1, Title: "unrelated key", Key: "ssh-ed25519 AAAAother"},
			{ID: 2, Title: "agenthub deploy key for owner/repo", Key: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITestDeployKey agenthub"},
		}, nil
	}
	var gotProfile, gotRegion, gotSecretName, gotPrivateKey string
	storeGitHubSSHKeyFunc = func(ctx context.Context, profile, region, secretName, privateKey string) (string, error) {
		gotProfile, gotRegion, gotSecretName, gotPrivateKey = profile, region, secretName, privateKey
		return "arn:aws:secretsmanager:ap-northeast-1:123456789012:secret:agenthub/github-ssh-key/owner/repo", nil
	}

	cfg, resolvedPath, err := bootstrapGitHubSSHClone(context.Background(), "dev-profile", "ap-northeast-1", "owner/repo", "~/.ssh/custom-deploy-key")
	if err != nil {
		t.Fatalf("bootstrapGitHubSSHClone() error = %v", err)
	}
	if resolvedPath != "/tmp/custom-deploy-key" {
		t.Fatalf("resolvedPath = %q, want /tmp/custom-deploy-key", resolvedPath)
	}
	if cfg.SSHKeySecretARN == "" {
		t.Fatal("SSHKeySecretARN = empty, want value")
	}
	if lookupCalls != 1 {
		t.Fatalf("lookupCalls = %d, want 1", lookupCalls)
	}
	if gotSecretName != "agenthub/github-ssh-key/owner/repo" {
		t.Fatalf("secretName = %q, want %q", gotSecretName, "agenthub/github-ssh-key/owner/repo")
	}
	if gotPrivateKey != "" {
		t.Fatalf("privateKey = %q, want empty string from fixture path", gotPrivateKey)
	}
	if gotProfile != "dev-profile" || gotRegion != "ap-northeast-1" {
		t.Fatalf("store profile/region = %q/%q, want dev-profile/ap-northeast-1", gotProfile, gotRegion)
	}
}

func TestMatchGitHubDeployKey(t *testing.T) {
	keys := []githubDeployKey{
		{ID: 1, Title: "other", Key: "ssh-ed25519 AAAAother"},
		{ID: 2, Title: "agenthub deploy key for owner/repo", Key: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITestDeployKey agenthub"},
	}

	if got := matchGitHubDeployKey(keys, "agenthub deploy key for owner/repo", "nope"); got == nil || got.ID != 2 {
		t.Fatalf("match by title = %+v, want id 2", got)
	}
	if got := matchGitHubDeployKey(keys, "nope", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITestDeployKey agenthub"); got == nil || got.ID != 2 {
		t.Fatalf("match by key = %+v, want id 2", got)
	}
	if got := matchGitHubDeployKey(keys, "missing", "missing"); got != nil {
		t.Fatalf("matchGitHubDeployKey() = %+v, want nil", got)
	}
}

func TestIsGitHubDeployKeyAlreadyInUseError(t *testing.T) {
	if !isGitHubDeployKeyAlreadyInUseError("Validation Failed: key is already in use") {
		t.Fatal("expected 422 error matcher to recognize GitHub response")
	}
	if isGitHubDeployKeyAlreadyInUseError("Validation Failed: title is invalid") {
		t.Fatal("unexpected match for unrelated validation error")
	}
}

func TestLookupGitIdentityUsesGitHubCLI(t *testing.T) {
	originalUser := runGitHubAPIUserFunc
	originalEmails := runGitHubAPIUserEmailsFunc
	defer func() {
		runGitHubAPIUserFunc = originalUser
		runGitHubAPIUserEmailsFunc = originalEmails
	}()

	runGitHubAPIUserFunc = func(ctx context.Context) (GitIdentity, error) {
		return GitIdentity{Name: "Test User"}, nil
	}
	runGitHubAPIUserEmailsFunc = func(ctx context.Context) (string, error) {
		return "test@example.com", nil
	}

	got, err := lookupGitIdentity(context.Background())
	if err != nil {
		t.Fatalf("lookupGitIdentity() error = %v", err)
	}
	if got.Name != "Test User" || got.Email != "test@example.com" {
		t.Fatalf("lookupGitIdentity() = %+v, want Test User/test@example.com", got)
	}
}

func TestLookupGitIdentityFallsBackWhenEmailMissing(t *testing.T) {
	originalUser := runGitHubAPIUserFunc
	originalEmails := runGitHubAPIUserEmailsFunc
	defer func() {
		runGitHubAPIUserFunc = originalUser
		runGitHubAPIUserEmailsFunc = originalEmails
	}()

	runGitHubAPIUserFunc = func(ctx context.Context) (GitIdentity, error) {
		return GitIdentity{Name: "Test User"}, nil
	}
	runGitHubAPIUserEmailsFunc = func(ctx context.Context) (string, error) {
		return "", errors.New("no email")
	}

	got, err := lookupGitIdentity(context.Background())
	if err != nil {
		t.Fatalf("lookupGitIdentity() error = %v", err)
	}
	if got.Name != "Test User" || got.Email != "" {
		t.Fatalf("lookupGitIdentity() = %+v, want Test User/empty email", got)
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
