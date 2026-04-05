package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"agenthub/internal/bedrock"
	"agenthub/internal/runtimeinstall"
)

type runtimeGenerator interface {
	Generate(ctx context.Context, prompt string) (string, error)
}

var newBedrockGenerator = bedrock.New

func newServeCommand(app *App) *cobra.Command {
	var runtimeConfigPath string
	var listenAddr string
	var idleTimeout time.Duration
	var idleShutdownCommand string

	cmd := &cobra.Command{
		Use:     "serve",
		Short:   "Run the AgentHub runtime daemon",
		GroupID: "runtime",
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(runtimeConfigPath) == "" {
				return errors.New("runtime config path is required")
			}

			runtimeCfg, err := loadRuntimeConfig(runtimeConfigPath)
			if err != nil {
				return err
			}
			addr := strings.TrimSpace(listenAddr)
			if addr == "" {
				addr = listenAddressForRuntime(runtimeCfg)
			}
			generator, err := runtimeGeneratorForConfig(cmd.Context(), runtimeCfg)
			if err != nil {
				return err
			}

			logger := loggerFromContext(cmd.Context())
			logger.Info("starting runtime server", "listen", addr, "runtimeConfig", runtimeConfigPath, "provider", runtimeCfg.Provider)
			fmt.Fprintf(cmd.OutOrStdout(), "runtime server listening on %s\n", addr)
			return runRuntimeServer(cmd.Context(), addr, runtimeConfigPath, runtimeCfg, generator, idleTimeout, idleShutdownCommand)
		},
	}

	cmd.Flags().StringVar(&runtimeConfigPath, "runtime-config", "/opt/agenthub/runtime.yaml", "path to the runtime config")
	cmd.Flags().StringVar(&listenAddr, "listen", "", "address to listen on; defaults to the runtime config port")
	cmd.Flags().DurationVar(&idleTimeout, "idle-timeout", 0, "shutdown after this period with no requests")
	cmd.Flags().StringVar(&idleShutdownCommand, "idle-shutdown-command", "", "shell command to run before exiting on idle timeout")
	return cmd
}

func loadRuntimeConfig(path string) (*runtimeinstall.RuntimeConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read runtime config %q: %w", path, err)
	}
	var cfg runtimeinstall.RuntimeConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse runtime config %q: %w", path, err)
	}
	return &cfg, nil
}

func listenAddressForRuntime(cfg *runtimeinstall.RuntimeConfig) string {
	port := cfg.Port
	if port <= 0 {
		port = 8080
	}
	return fmt.Sprintf("0.0.0.0:%d", port)
}

func runtimeGeneratorForConfig(ctx context.Context, runtimeCfg *runtimeinstall.RuntimeConfig) (runtimeGenerator, error) {
	if runtimeCfg == nil {
		return nil, nil
	}
	switch strings.ToLower(strings.TrimSpace(runtimeCfg.Provider)) {
	case "aws-bedrock":
		return newBedrockGenerator(ctx, runtimeCfg.Region, runtimeCfg.Model)
	default:
		return nil, nil
	}
}

func runRuntimeServer(ctx context.Context, addr, runtimeConfigPath string, runtimeCfg *runtimeinstall.RuntimeConfig, generator runtimeGenerator, idleTimeout time.Duration, idleShutdownCommand string) error {
	state := newRuntimeServerState(runtimeConfigPath, addr, runtimeCfg, generator)
	mux := newRuntimeServerMux(state)
	server := &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	if idleTimeout > 0 {
		go func() {
			ticker := time.NewTicker(minDuration(idleTimeout/2, time.Minute))
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					if time.Since(state.readLastActivity()) < idleTimeout {
						continue
					}
					if strings.TrimSpace(idleShutdownCommand) != "" {
						_ = runShellCommand(ctx, idleShutdownCommand)
					}
					shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
					_ = server.Shutdown(shutdownCtx)
					cancel()
					return
				}
			}
		}()
	}

	errCh := make(chan error, 1)
	go func() {
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return server.Shutdown(shutdownCtx)
	case err := <-errCh:
		return err
	}
}

func runShellCommand(ctx context.Context, command string) error {
	if strings.TrimSpace(command) == "" {
		return nil
	}
	return execShell(ctx, command)
}

var execShell = func(ctx context.Context, command string) error {
	cmd := osExecCommandContext(ctx, "sh", "-lc", command)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg != "" {
			return fmt.Errorf("run idle shutdown command: %s: %w", msg, err)
		}
		return fmt.Errorf("run idle shutdown command: %w", err)
	}
	return nil
}

// osExecCommandContext is extracted for tests.
var osExecCommandContext = func(ctx context.Context, name string, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, name, args...)
}

func minDuration(a, b time.Duration) time.Duration {
	if a <= 0 || (b > 0 && a > b) {
		return b
	}
	return a
}
