package runtimeinstall

import (
	"fmt"
	"strings"
)

// ManagedArtifacts describes the managed files a runtime install would place on the target host.
type ManagedArtifacts struct {
	WorkingDir        string
	RuntimeConfigPath string
	RuntimeConfig     []byte
	BinaryPath        string
	ServicePath       string
	ServiceUnit       []byte
	ProviderEnvPath   string
	ProviderEnv       []byte
}

// PreviewManagedArtifacts renders the managed text artifacts for an install request without mutating a host.
func PreviewManagedArtifacts(req Request) (ManagedArtifacts, error) {
	if req.Config == nil {
		return ManagedArtifacts{}, fmt.Errorf("preview managed artifacts: config is nil")
	}

	workingDir := strings.TrimSpace(req.WorkingDir)
	if workingDir == "" {
		workingDir = "/opt/agenthub"
	}

	renderedConfig, err := RenderRuntimeConfig(req.Config, req.UseNemoClaw, req.Port)
	if err != nil {
		return ManagedArtifacts{}, err
	}

	remoteBinaryPath := pathJoin(workingDir, "bin", "agenthub")
	remoteConfigPath := pathJoin(workingDir, "runtime.yaml")
	remoteServicePath := "/etc/systemd/system/agenthub.service"
	remoteEnvPath := pathJoin(workingDir, "agenthub.env")

	providerName := strings.ToLower(strings.TrimSpace(req.Config.Runtime.Provider))
	codexAPIKey := strings.TrimSpace(req.CodexAPIKey)
	providerEnv := []byte(nil)
	providerEnvPath := ""
	switch providerName {
	case "codex":
		if codexAPIKey != "" {
			providerEnvPath = remoteEnvPath
			providerEnv = []byte(fmt.Sprintf("OPENAI_API_KEY=%s\n", codexAPIKey))
		}
	case "aws-bedrock":
		providerEnvPath = remoteEnvPath
		region := strings.TrimSpace(req.Config.Region.Name)
		providerEnv = []byte(fmt.Sprintf("AWS_REGION=%s\nAWS_DEFAULT_REGION=%s\n", region, region))
	}

	listenPort := req.Config.Runtime.Port
	if req.Port > 0 {
		listenPort = req.Port
	}
	if listenPort <= 0 {
		listenPort = 8080
	}

	unitContents := []byte(renderSystemdUnit(
		remoteBinaryPath,
		remoteConfigPath,
		listenPort,
		defaultRuntimeIdleTimeout,
		defaultRuntimeIdleShutdownCommand,
		providerEnvPath,
	))

	return ManagedArtifacts{
		WorkingDir:        workingDir,
		RuntimeConfigPath: remoteConfigPath,
		RuntimeConfig:     renderedConfig,
		BinaryPath:        remoteBinaryPath,
		ServicePath:       remoteServicePath,
		ServiceUnit:       unitContents,
		ProviderEnvPath:   providerEnvPath,
		ProviderEnv:       providerEnv,
	}, nil
}
