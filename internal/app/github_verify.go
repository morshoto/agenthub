package app

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"agenthub/internal/config"
	"agenthub/internal/githubauth"
	"agenthub/internal/host"
)

var gitRemoteOriginURLFunc = func(ctx context.Context) (string, error) {
	remoteURL, err := gitOutput(ctx, "git", "remote", "get-url", "origin")
	if err != nil {
		return "", err
	}
	remoteURL = strings.TrimSpace(remoteURL)
	if remoteURL == "" {
		return "", errors.New("git remote origin is required to verify GitHub deployment dependencies")
	}
	return remoteURL, nil
}

var loadGitHubInstallationToken = githubauth.InstallationToken
var loadGitHubUserToken = githubauth.LoadToken
var verifyRemoteGitHubAccessFunc = verifyRemoteGitHubAccess
var verifyGitHubCredentialHelperFunc = verifyGitHubCredentialHelperConfigured

type githubVerificationTarget struct {
	RemoteURL string
	HTTPSURL  string
}

func resolveGitHubVerificationTarget(ctx context.Context) (githubVerificationTarget, error) {
	remoteURL, err := gitRemoteOriginURLFunc(ctx)
	if err != nil {
		return githubVerificationTarget{}, err
	}
	normalized, err := normalizeGitHubRemoteURL(remoteURL)
	if err != nil {
		return githubVerificationTarget{}, err
	}
	return githubVerificationTarget{
		RemoteURL: remoteURL,
		HTTPSURL:  normalized,
	}, nil
}

func normalizeGitHubRemoteURL(remoteURL string) (string, error) {
	trimmed := strings.TrimSpace(remoteURL)
	if trimmed == "" {
		return "", errors.New("git remote origin is required to verify GitHub deployment dependencies")
	}

	if strings.HasPrefix(trimmed, "git@github.com:") {
		return normalizeGitHubPath("https://github.com/", strings.TrimPrefix(trimmed, "git@github.com:"))
	}
	if strings.HasPrefix(trimmed, "ssh://git@github.com/") {
		return normalizeGitHubPath("https://github.com/", strings.TrimPrefix(trimmed, "ssh://git@github.com/"))
	}

	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("unsupported GitHub remote %q", remoteURL)
	}
	if !strings.EqualFold(parsed.Host, "github.com") {
		return "", fmt.Errorf("GitHub deployment verification requires a github.com remote, got %q", remoteURL)
	}
	return normalizeGitHubPath("https://github.com/", strings.TrimPrefix(parsed.Path, "/"))
}

func normalizeGitHubPath(prefix, path string) (string, error) {
	path = strings.TrimSpace(path)
	path = strings.TrimSuffix(path, ".git")
	path = strings.Trim(path, "/")
	if path == "" {
		return "", errors.New("git remote origin must include an owner/repo path")
	}
	parts := strings.Split(path, "/")
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
		return "", fmt.Errorf("git remote origin must resolve to owner/repo, got %q", path)
	}
	return prefix + parts[0] + "/" + parts[1], nil
}

func validateCreateGitHubDeployment(cfg *config.Config) error {
	if cfg == nil {
		return errors.New("config is required")
	}
	if mode := config.GitHubAuthModeFor(cfg.GitHub); mode == "" {
		return errors.New("GitHub connectivity is required for deployment; configure github.auth_mode=app (recommended) or github.auth_mode=user")
	}
	return nil
}

func verifyLocalGitHubAuth(ctx context.Context, profile string, cfg *config.Config) error {
	if cfg == nil {
		return errors.New("config is required")
	}
	switch config.GitHubAuthModeFor(cfg.GitHub) {
	case config.GitHubAuthModeApp:
		token, err := loadGitHubInstallationToken(ctx, strings.TrimSpace(profile), strings.TrimSpace(cfg.Region.Name), cfg.GitHub)
		if err != nil {
			return err
		}
		if strings.TrimSpace(token) == "" {
			return errors.New("github installation token response did not contain a token")
		}
	case config.GitHubAuthModeUser:
		token, err := loadGitHubUserToken(ctx, strings.TrimSpace(profile), strings.TrimSpace(cfg.Region.Name), cfg.GitHub.TokenSecretARN)
		if err != nil {
			return err
		}
		if strings.TrimSpace(token) == "" {
			return errors.New("github token secret did not contain a token")
		}
	default:
		return errors.New("GitHub connectivity is required for deployment; configure github.auth_mode=app (recommended) or github.auth_mode=user")
	}
	return nil
}

func verifyRemoteGitHubAccess(ctx context.Context, exec host.Executor, repoURL string) error {
	if exec == nil {
		return errors.New("remote verification requires a host executor")
	}
	repoURL = strings.TrimSpace(repoURL)
	if repoURL == "" {
		return errors.New("GitHub deployment verification requires a GitHub repository target")
	}
	if err := verifyGitHubCredentialHelperFunc(ctx, exec); err != nil {
		return err
	}
	result, err := exec.Run(ctx, "git", "ls-remote", repoURL)
	if err != nil {
		return fmt.Errorf("verify GitHub access with git ls-remote %q: %w", repoURL, err)
	}
	if !hasGitRemoteRefs(result.Stdout) {
		return fmt.Errorf("verify GitHub access with git ls-remote %q: expected at least one ref in stdout", repoURL)
	}
	return nil
}

func hasGitRemoteRefs(stdout string) bool {
	for _, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.Contains(line, "\t") {
			return true
		}
	}
	return false
}

func verifyGitHubCredentialHelperConfigured(ctx context.Context, exec host.Executor) error {
	if exec == nil {
		return errors.New("remote verification requires a host executor")
	}
	checks := [][]string{
		{"git", "config", "--global", "--get", "credential.helper"},
		{"git", "config", "--global", "--get", "url.https://github.com/.insteadof"},
	}
	for _, args := range checks {
		result, err := exec.Run(ctx, args[0], args[1:]...)
		if err != nil {
			return fmt.Errorf("verify git credential helper setup with %q: %w", strings.Join(args, " "), err)
		}
		if strings.TrimSpace(result.Stdout) == "" {
			return fmt.Errorf("verify git credential helper setup with %q: expected a configured value", strings.Join(args, " "))
		}
	}
	return nil
}
