package app

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

func resolveSourceArchiveURL(ctx context.Context) (string, string, error) {
	remoteURL, err := gitOutput(ctx, "git", "remote", "get-url", "origin")
	if err != nil {
		return "", "", err
	}
	remoteURL = strings.TrimSpace(remoteURL)
	if remoteURL == "" {
		return "", "", errors.New("git remote origin is required to bootstrap the Docker image")
	}

	repoURL, err := normalizeGitHubRepoURL(remoteURL)
	if err != nil {
		return "", "", err
	}

	ref, err := gitOutput(ctx, "git", "rev-parse", "HEAD")
	if err != nil {
		return "", "", err
	}
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", "", errors.New("git HEAD revision is required to bootstrap the Docker image")
	}

	archiveURL := strings.TrimSuffix(repoURL, ".git")
	archiveURL = fmt.Sprintf("%s/archive/%s.tar.gz", archiveURL, ref)
	return archiveURL, ref, nil
}

func normalizeGitHubRepoURL(remoteURL string) (string, error) {
	remoteURL = strings.TrimSpace(remoteURL)
	if remoteURL == "" {
		return "", errors.New("repository URL is required")
	}

	switch {
	case strings.HasPrefix(remoteURL, "git@github.com:"):
		path := strings.TrimPrefix(remoteURL, "git@github.com:")
		path = strings.TrimSuffix(path, ".git")
		return "https://github.com/" + path, nil
	case strings.HasPrefix(remoteURL, "https://github.com/"):
		return strings.TrimSuffix(remoteURL, ".git"), nil
	default:
		return "", fmt.Errorf("unsupported repository URL %q: use a GitHub origin", remoteURL)
	}
}

func gitOutput(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return string(out), nil
}
