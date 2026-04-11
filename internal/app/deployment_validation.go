package app

import (
	"io"

	"agenthub/internal/config"
)

func validateDeploymentConfig(out io.Writer, cfg *config.Config) error {
	if err := config.ValidateDeployment(cfg); err != nil {
		return wrapUserFacingError(
			"deployment validation failed",
			err,
			"GitHub connectivity is required for deployment",
			"configure GitHub App auth and rerun "+commandRef(out, "agenthub", "init"),
			"use "+commandRef(out, "agenthub", "config", "secret", "update")+" if you need to add or repair github.* settings in an existing config",
			"use github.auth_mode=user only for personal or development environments",
		)
	}
	return nil
}
