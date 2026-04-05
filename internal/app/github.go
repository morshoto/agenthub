package app

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"agenthub/internal/githubauth"
	"agenthub/internal/runtimeinstall"
)

func newGitHubCommand(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "github",
		Short:   "GitHub integration commands",
		GroupID: "integrations",
	}
	cmd.AddCommand(newGitHubCredentialCommand())
	return cmd
}

func newGitHubCredentialCommand() *cobra.Command {
	var runtimeConfigPath string

	cmd := &cobra.Command{
		Use:    "credential",
		Hidden: true,
		Short:  "Git credential helper for GitHub App auth",
		Args:   cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			request, err := readGitCredentialRequest(cmd.InOrStdin())
			if err != nil {
				return err
			}
			if !shouldServeGitHubCredential(request) {
				return nil
			}

			runtimeCfg, err := loadRuntimeConfig(runtimeConfigPath)
			if err != nil {
				return err
			}
			if !hasGitHubRuntimeConfig(runtimeCfg) {
				return errors.New("github auth is not configured on this host")
			}

			creds, err := githubauth.CredentialForGit(cmd.Context(), strings.TrimSpace(runtimeCfg.Region), runtimeCfg.GitHub)
			if err != nil {
				return err
			}
			return writeGitCredentialResponse(cmd.OutOrStdout(), creds)
		},
	}

	cmd.Flags().StringVar(&runtimeConfigPath, "runtime-config", "/opt/agenthub/runtime.yaml", "path to the runtime config")
	return cmd
}

type gitCredentialRequest struct {
	Protocol string
	Host     string
	Path     string
	Username string
}

func readGitCredentialRequest(r io.Reader) (gitCredentialRequest, error) {
	scanner := bufio.NewScanner(r)
	req := gitCredentialRequest{}
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			break
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "protocol":
			req.Protocol = strings.TrimSpace(value)
		case "host":
			req.Host = strings.TrimSpace(value)
		case "path":
			req.Path = strings.TrimSpace(value)
		case "username":
			req.Username = strings.TrimSpace(value)
		}
	}
	if err := scanner.Err(); err != nil {
		return gitCredentialRequest{}, err
	}
	return req, nil
}

func shouldServeGitHubCredential(req gitCredentialRequest) bool {
	return strings.EqualFold(strings.TrimSpace(req.Host), "github.com")
}

func writeGitCredentialResponse(out io.Writer, creds githubauth.Credential) error {
	if strings.TrimSpace(creds.Username) == "" || strings.TrimSpace(creds.Password) == "" {
		return errors.New("git credential response is incomplete")
	}
	if _, err := fmt.Fprintf(out, "username=%s\npassword=%s\n", strings.TrimSpace(creds.Username), strings.TrimSpace(creds.Password)); err != nil {
		return err
	}
	return nil
}

func hasGitHubRuntimeConfig(cfg *runtimeinstall.RuntimeConfig) bool {
	if cfg == nil {
		return false
	}
	return strings.TrimSpace(cfg.GitHub.AppID) != "" && strings.TrimSpace(cfg.GitHub.InstallationID) != "" && strings.TrimSpace(cfg.GitHub.PrivateKeySecretARN) != ""
}
