package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"openclaw/internal/config"
)

type runtimeStatusClient interface {
	Do(req *http.Request) (*http.Response, error)
}

var resolveRuntimeStatusURL = runtimeStatusURL

var newRuntimeStatusClient = func() runtimeStatusClient {
	return &http.Client{Timeout: 5 * time.Second}
}

func newStatusCommand(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show runtime agent activity",
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(app.opts.ConfigPath) == "" {
				return errors.New("config file is required: pass --config <path>")
			}

			cfg, err := config.Load(app.opts.ConfigPath)
			if err != nil {
				return err
			}
			if err := config.Validate(cfg); err != nil {
				return err
			}

			url, err := resolveRuntimeStatusURL(cfg)
			if err != nil {
				return err
			}

			status, err := fetchRuntimeStatus(cmd.Context(), url, newRuntimeStatusClient())
			if err != nil {
				return err
			}

			printRuntimeStatus(cmd.OutOrStdout(), status)
			return nil
		},
	}

	return cmd
}

func runtimeStatusURL(cfg *config.Config) (string, error) {
	if cfg == nil {
		return "", errors.New("runtime status requires a config")
	}
	port := cfg.Runtime.Port
	if port <= 0 {
		port = 8080
	}
	return fmt.Sprintf("http://127.0.0.1:%d/status", port), nil
}

func fetchRuntimeStatus(ctx context.Context, url string, client runtimeStatusClient) (runtimeStatusResponse, error) {
	if client == nil {
		client = newRuntimeStatusClient()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return runtimeStatusResponse{}, fmt.Errorf("create status request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return runtimeStatusResponse{}, fmt.Errorf("query runtime status at %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		msg := strings.TrimSpace(string(body))
		if msg != "" {
			return runtimeStatusResponse{}, fmt.Errorf("query runtime status at %s: %s", url, msg)
		}
		return runtimeStatusResponse{}, fmt.Errorf("query runtime status at %s: unexpected http status %s", url, resp.Status)
	}

	var status runtimeStatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return runtimeStatusResponse{}, fmt.Errorf("decode runtime status from %s: %w", url, err)
	}
	return status, nil
}

func printRuntimeStatus(out io.Writer, status runtimeStatusResponse) {
	fmt.Fprintln(out, "runtime status")
	if !status.Active || len(status.ActiveAgents) == 0 {
		fmt.Fprintln(out, "no active agents")
		return
	}

	fmt.Fprintf(out, "active agents: %d\n", len(status.ActiveAgents))
	for _, agent := range status.ActiveAgents {
		task := strings.TrimSpace(agent.Task)
		if task == "" {
			task = "working"
		}
		line := fmt.Sprintf("- %s: %s", agent.ID, task)
		parts := make([]string, 0, 2)
		if strings.TrimSpace(agent.Model) != "" {
			parts = append(parts, fmt.Sprintf("model: %s", agent.Model))
		}
		parts = append(parts, fmt.Sprintf("running %s", formatRuntimeDuration(agent.RunningForSeconds)))
		if len(parts) > 0 {
			line += " (" + strings.Join(parts, ", ") + ")"
		}
		fmt.Fprintln(out, line)
	}
}

func formatRuntimeDuration(seconds int64) string {
	if seconds <= 0 {
		return "0s"
	}
	return (time.Duration(seconds) * time.Second).String()
}
