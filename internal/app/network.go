package app

import (
	"strings"

	"agenthub/internal/config"
)

func resolveRuntimeCIDR(cfg *config.Config) string {
	if cfg == nil {
		return "0.0.0.0/0"
	}
	if value := strings.TrimSpace(cfg.Runtime.PublicCIDR); value != "" {
		return value
	}
	if config.EffectiveNetworkMode(cfg) == "public" {
		return "0.0.0.0/0"
	}
	return ""
}
