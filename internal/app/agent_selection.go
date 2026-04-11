package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"agenthub/internal/prompt"
)

type agentConfigResolutionOptions struct {
	ConfigPath       string
	AgentName        string
	AgentsDir        string
	Session          *prompt.Session
	AllowInteractive bool
	RequireConfig    bool
}

func resolveAgentConfigPath(opts agentConfigResolutionOptions) (string, error) {
	configPath := strings.TrimSpace(opts.ConfigPath)
	agentName := strings.TrimSpace(opts.AgentName)

	if configPath != "" && agentName != "" {
		return "", errors.New("--config and --agent are mutually exclusive; pass only one")
	}
	if configPath != "" {
		return configPath, nil
	}
	if agentName != "" {
		resolvedPath := agentConfigPath(opts.AgentsDir, agentName)
		if _, err := os.Stat(resolvedPath); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return "", fmt.Errorf("agent config file not found for --agent %q: expected %s", agentName, resolvedPath)
			}
			return "", fmt.Errorf("stat agent config %q: %w", resolvedPath, err)
		}
		return resolvedPath, nil
	}
	if opts.AllowInteractive {
		return selectAgentConfigPath(opts.Session, opts.AgentsDir)
	}
	if opts.RequireConfig {
		return "", errors.New("config file is required: pass --config <path> or --agent <name>")
	}
	return "", nil
}

func agentConfigPath(agentsDir, agentName string) string {
	root := strings.TrimSpace(agentsDir)
	if root == "" {
		root = "agents"
	}
	return filepath.Join(root, strings.TrimSpace(agentName), "config.yaml")
}
