package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"agenthub/internal/prompt"
)

func newDeleteCommand(app *App) *cobra.Command {
	var agentsDir string
	var force bool

	cmd := &cobra.Command{
		Use:     "delete <agent>",
		Short:   "Delete a locally managed agent",
		GroupID: "runtime",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			agentName := strings.TrimSpace(args[0])
			if agentName == "" {
				return errors.New("agent name is required")
			}

			agentPath, err := localAgentPath(agentsDir, agentName)
			if err != nil {
				return err
			}

			fmt.Fprintf(cmd.OutOrStdout(), "removing local agent state: %s\n", agentPath)

			session := prompt.NewSession(cmd.InOrStdin(), cmd.OutOrStdout())
			session.Interactive = detectInteractiveInput(cmd.InOrStdin())
			if !force {
				if !session.Interactive {
					return errors.New("deletion requires confirmation; rerun with --force in non-interactive mode")
				}
				confirmed, err := session.Confirm("Delete this local agent", false)
				if err != nil {
					return err
				}
				if !confirmed {
					fmt.Fprintln(cmd.OutOrStdout(), "deletion cancelled")
					return nil
				}
			}

			if err := os.RemoveAll(agentPath); err != nil {
				return fmt.Errorf("remove agent directory %q: %w", agentPath, err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "deleted local agent: %s\n", agentName)
			return nil
		},
	}

	cmd.Flags().StringVar(&agentsDir, "agents-dir", "agents", "path to the agents directory")
	cmd.Flags().BoolVar(&force, "force", false, "delete without interactive confirmation")
	return cmd
}

func localAgentPath(root, agentName string) (string, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		root = "agents"
	}

	agentName = strings.TrimSpace(agentName)
	if agentName == "" {
		return "", errors.New("agent name is required")
	}

	path := filepath.Join(root, agentName)
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("agent %q does not exist under %q", agentName, root)
		}
		return "", fmt.Errorf("stat agent directory %q: %w", path, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("agent %q does not map to a directory under %q", agentName, root)
	}
	return path, nil
}
