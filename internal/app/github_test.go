package app

import (
	"testing"

	"agenthub/internal/config"
	"agenthub/internal/runtimeinstall"
)

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
