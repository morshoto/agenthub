package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"agenthub/internal/runtime"
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

const latestReleaseURL = "https://api.github.com/repos/morshoto/agenthub/releases/latest"

var fetchLatestReleaseVersion = defaultFetchLatestReleaseVersion

func New() *App {
	return &App{}
}

func (a *App) Execute() error {
	return newRootCommand(a).Execute()
}

func (a *App) applyRuntime(ctx context.Context) context.Context {
	logger := runtime.NewLogger(a.opts.Verbose, a.opts.Debug)
	slog.SetDefault(logger)
	logger.Debug("runtime initialized")

	ctx = runtime.WithLogger(ctx, logger)
	return ctx
}

func (a *App) versionString() string {
	return fmt.Sprintf("agenthub %s\ncommit: %s\nbuild date: %s", Version, CommitSHA, BuildDate)
}

func (a *App) maybePrintUpdateNotice(ctx context.Context, out io.Writer) {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()

	current, ok := parseVersionTriplet(Version)
	if !ok {
		return
	}

	latest, err := fetchLatestReleaseVersion(ctx)
	if err != nil {
		return
	}
	latestVersion, ok := parseVersionTriplet(latest)
	if !ok {
		return
	}

	if compareVersionTriplets(latestVersion, current) <= 0 {
		return
	}

	fmt.Fprintf(out, "Update available: agenthub %s is out of date. Latest is %s.\n", Version, latest)
	fmt.Fprintf(out, "Update with %s, %s, or download the latest release from %s.\n",
		commandRef(out, "brew", "upgrade", "agenthub"),
		commandRef(out, "nix", "profile", "upgrade", "github:morshoto/agenthub"),
		"https://github.com/morshoto/agenthub/releases/latest",
	)
}

type githubRelease struct {
	TagName string `json:"tag_name"`
}

func defaultFetchLatestReleaseVersion(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, latestReleaseURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "agenthub")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		if len(body) > 0 {
			return "", fmt.Errorf("latest release request failed: %s: %s", resp.Status, strings.TrimSpace(string(body)))
		}
		return "", fmt.Errorf("latest release request failed: %s", resp.Status)
	}

	var release githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return "", err
	}
	return release.TagName, nil
}

func parseVersionTriplet(raw string) ([3]int, bool) {
	var parsed [3]int
	raw = strings.TrimSpace(strings.TrimPrefix(raw, "v"))
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return parsed, false
	}
	for i, part := range parts {
		value, err := strconv.Atoi(part)
		if err != nil || value < 0 {
			return [3]int{}, false
		}
		parsed[i] = value
	}
	return parsed, true
}

func compareVersionTriplets(a, b [3]int) int {
	for i := 0; i < len(a); i++ {
		if a[i] > b[i] {
			return 1
		}
		if a[i] < b[i] {
			return -1
		}
	}
	return 0
}
