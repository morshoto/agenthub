package app

import (
	"context"
	"fmt"
	"log/slog"

	"openclaw/internal/config"
	"openclaw/internal/runtime"
)

// version information is injected at build time.
var (
	Version   = "dev"
	CommitSHA = "none"
	BuildDate = "unknown"
)

type Options struct {
	ConfigPath string
	Profile    string
	Verbose    bool
	Debug      bool
}

type App struct {
	opts Options
}

func New() *App {
	return &App{}
}

func (a *App) Execute() error {
	return newRootCommand(a).Execute()
}

func (a *App) applyRuntime(ctx context.Context) (context.Context, *config.Config, error) {
	cfg, err := config.Load(a.opts.ConfigPath, a.opts.Profile)
	if err != nil {
		return ctx, nil, err
	}

	logger := runtime.NewLogger(a.opts.Verbose, a.opts.Debug)
	slog.SetDefault(logger)
	logger.Debug("runtime initialized")

	ctx = runtime.WithLogger(ctx, logger)
	ctx = runtime.WithConfig(ctx, cfg)

	return ctx, cfg, nil
}

func (a *App) versionString() string {
	return fmt.Sprintf("openclaw %s\ncommit: %s\nbuild date: %s", Version, CommitSHA, BuildDate)
}
