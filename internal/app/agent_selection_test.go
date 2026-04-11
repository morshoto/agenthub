package app

import (
	"strings"
	"testing"
)

func TestResolveAgentConfigPathResolvesAgentName(t *testing.T) {
	dir := t.TempDir()
	configPath := agentConfigPath(dir, "alpha")
	if err := writeMinimalAgentConfig(configPath); err != nil {
		t.Fatalf("writeMinimalAgentConfig() error = %v", err)
	}

	got, err := resolveAgentConfigPath(agentConfigResolutionOptions{
		AgentName:     "alpha",
		AgentsDir:     dir,
		RequireConfig: true,
	})
	if err != nil {
		t.Fatalf("resolveAgentConfigPath() error = %v", err)
	}
	if got != configPath {
		t.Fatalf("resolveAgentConfigPath() = %q, want %q", got, configPath)
	}
}

func TestResolveAgentConfigPathRejectsConfigAndAgent(t *testing.T) {
	_, err := resolveAgentConfigPath(agentConfigResolutionOptions{
		ConfigPath: "agents/alpha/config.yaml",
		AgentName:  "alpha",
	})
	if err == nil {
		t.Fatal("resolveAgentConfigPath() error = nil")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("error = %v, want mutually exclusive message", err)
	}
}

func TestResolveAgentConfigPathReportsMissingAgentConfig(t *testing.T) {
	dir := t.TempDir()

	_, err := resolveAgentConfigPath(agentConfigResolutionOptions{
		AgentName:     "missing",
		AgentsDir:     dir,
		RequireConfig: true,
	})
	if err == nil {
		t.Fatal("resolveAgentConfigPath() error = nil")
	}
	if !strings.Contains(err.Error(), agentConfigPath(dir, "missing")) {
		t.Fatalf("error = %v, want expected path", err)
	}
}

func TestResolveAgentConfigPathAllowsEmptyWhenConfigOptional(t *testing.T) {
	got, err := resolveAgentConfigPath(agentConfigResolutionOptions{})
	if err != nil {
		t.Fatalf("resolveAgentConfigPath() error = %v", err)
	}
	if got != "" {
		t.Fatalf("resolveAgentConfigPath() = %q, want empty path", got)
	}
}
