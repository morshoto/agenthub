package setup

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"unicode"

	"agenthub/internal/config"
	"agenthub/internal/githubauth"
)

var gitRemoteOriginURLFunc = defaultGitRemoteOriginURL
var runGitHubAuthLoginFunc = defaultRunGitHubAuthLogin
var runGitHubAuthTokenFunc = defaultRunGitHubAuthToken
var storeGitHubTokenFunc = defaultStoreGitHubToken

func detectGitHubRepoSlug(ctx context.Context) (string, error) {
	remoteURL, err := gitRemoteOriginURLFunc(ctx)
	if err != nil {
		return "", err
	}
	return parseGitHubRepoSlug(remoteURL), nil
}

func defaultGitRemoteOriginURL(ctx context.Context) (string, error) {
	out, err := exec.CommandContext(ctx, "git", "remote", "get-url", "origin").Output()
	if err != nil {
		return "", fmt.Errorf("git remote get-url origin: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

func parseGitHubRepoSlug(remoteURL string) string {
	remoteURL = strings.TrimSpace(remoteURL)
	if remoteURL == "" {
		return ""
	}

	const sshPrefix = "git@github.com:"
	const sshURLPrefix = "ssh://git@github.com/"

	var path string
	switch {
	case strings.HasPrefix(remoteURL, sshPrefix):
		path = strings.TrimPrefix(remoteURL, sshPrefix)
	case strings.HasPrefix(remoteURL, sshURLPrefix):
		path = strings.TrimPrefix(remoteURL, sshURLPrefix)
	default:
		parsed, err := url.Parse(remoteURL)
		if err != nil {
			return ""
		}
		if !strings.EqualFold(parsed.Host, "github.com") {
			return ""
		}
		path = parsed.Path
	}

	path = strings.TrimSpace(path)
	path = strings.TrimPrefix(path, "/")
	path = strings.TrimSuffix(path, ".git")
	parts := strings.Split(path, "/")
	if len(parts) != 2 {
		return ""
	}
	owner := strings.TrimSpace(parts[0])
	repo := strings.TrimSpace(parts[1])
	if owner == "" || repo == "" {
		return ""
	}
	return owner + "/" + repo
}

func defaultRunGitHubAuthLogin(ctx context.Context) error {
	if _, err := exec.LookPath("gh"); err != nil {
		return errors.New("gh CLI is required for GitHub user auth")
	}
	cmd := exec.CommandContext(ctx, "gh", "auth", "login", "--web", "--git-protocol", "https", "--hostname", "github.com")
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("run gh auth login: %w", err)
	}
	return nil
}

func defaultRunGitHubAuthToken(ctx context.Context) (string, error) {
	if _, err := exec.LookPath("gh"); err != nil {
		return "", errors.New("gh CLI is required for GitHub user auth")
	}
	cmd := exec.CommandContext(ctx, "gh", "auth", "token", "--hostname", "github.com")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("run gh auth token: %w", err)
	}
	token := strings.TrimSpace(string(out))
	if token == "" {
		return "", errors.New("gh auth token returned an empty token")
	}
	return token, nil
}

func defaultStoreGitHubToken(ctx context.Context, profile, region, secretName, token string) (string, error) {
	return githubauth.StoreToken(ctx, profile, region, secretName, token)
}

func bootstrapGitHubUserAuth(ctx context.Context, profile, region, repoSlug string) (config.GitHubConfig, error) {
	token, err := runGitHubAuthTokenFunc(ctx)
	if err != nil {
		if loginErr := runGitHubAuthLoginFunc(ctx); loginErr != nil {
			return config.GitHubConfig{}, loginErr
		}
		token, err = runGitHubAuthTokenFunc(ctx)
		if err != nil {
			return config.GitHubConfig{}, err
		}
	}

	secretName := defaultGitHubUserSecretName(repoSlug)
	arn, err := storeGitHubTokenFunc(ctx, profile, region, secretName, token)
	if err != nil {
		return config.GitHubConfig{}, err
	}
	return config.GitHubConfig{
		AuthMode:       config.GitHubAuthModeUser,
		TokenSecretARN: arn,
	}, nil
}

func defaultGitHubUserSecretName(repoSlug string) string {
	repoSlug = strings.TrimSpace(repoSlug)
	if repoSlug == "" {
		return "agenthub/github-token"
	}
	if safe := sanitizeSecretName(repoSlug); safe != "" {
		return "agenthub/github-token/" + safe
	}
	return "agenthub/github-token"
}

func sanitizeSecretName(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("-_./+=@", r) {
			b.WriteRune(r)
			continue
		}
		b.WriteRune('-')
	}
	return strings.Trim(b.String(), "-/")
}
