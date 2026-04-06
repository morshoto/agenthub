package app

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agenthub/internal/config"
	"agenthub/internal/host"
)

func TestProgressRendererRunWritesHistoryInsteadOfOverwriting(t *testing.T) {
	var out bytes.Buffer
	r := &progressRenderer{out: &out, tty: true}

	if err := r.Run(context.Background(), "long-running task", func(context.Context) error { return nil }); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	got := out.String()
	if strings.Contains(got, "\033[2K") || strings.Contains(got, "\033[2J") {
		t.Fatalf("Run() output = %q, want no screen-clearing escape sequences", got)
	}
	if !strings.Contains(got, "long-running task ...") {
		t.Fatalf("Run() output = %q, want start line", got)
	}
	if !strings.Contains(got, "done: long-running task") {
		t.Fatalf("Run() output = %q, want completion line", got)
	}
}

func TestProgressRendererRunPassesContextToWorker(t *testing.T) {
	var out bytes.Buffer
	r := &progressRenderer{out: &out, tty: true}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	entered := make(chan struct{}, 1)
	done := make(chan error, 1)

	go func() {
		done <- r.Run(ctx, "long-running task", func(workerCtx context.Context) error {
			select {
			case entered <- struct{}{}:
			default:
			}
			<-workerCtx.Done()
			return workerCtx.Err()
		})
	}()

	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not start")
	}

	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run() error = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run() did not return after cancellation")
	}

	if got := out.String(); !strings.Contains(got, "failed: long-running task: context canceled") {
		t.Fatalf("output %q does not contain cancellation summary", got)
	}
}

func TestCreateProgressRendererRunGroupWritesHistoryInsteadOfRedrawing(t *testing.T) {
	var out bytes.Buffer
	r := &createProgressRenderer{out: &out, tty: true}

	if err := r.RunGroup(context.Background(), "Runtime", "installing runtime", func(context.Context) error { return nil }); err != nil {
		t.Fatalf("RunGroup() error = %v", err)
	}

	got := out.String()
	if strings.Contains(got, "\033[2J") || strings.Contains(got, "\033[2K") || strings.Contains(got, "\033[H") {
		t.Fatalf("RunGroup() output = %q, want no screen-clearing escape sequences", got)
	}
	if !strings.Contains(got, "Runtime: installing runtime ...") {
		t.Fatalf("RunGroup() output = %q, want start line", got)
	}
	if !strings.Contains(got, "done: Runtime: installing runtime") {
		t.Fatalf("RunGroup() output = %q, want completion line", got)
	}
}

func TestSummarizeBootstrapStatusCondensesMultilineOutput(t *testing.T) {
	got := summarizeBootstrapStatus(`cloud-init status:
status: running
extended_status: running
detail: DataSourceEc2Local

bootstrap log tail:
tail: cannot open '/var/log/agenthub-bootstrap.log' for reading: No such file or directory
`)
	want := "status: running; extended_status: running; detail: DataSourceEc2Local"
	if got != want {
		t.Fatalf("summarizeBootstrapStatus() = %q, want %q", got, want)
	}
}

func TestWaitForBootstrapReadyRetriesUntilDeadlineOnTransientSSHErrors(t *testing.T) {
	originalTimeout := defaultSSHReadyTimeout
	originalInitialWait := defaultSSHReadyInitialWait
	originalMaxWait := defaultSSHReadyMaxWait
	defaultSSHReadyTimeout = 10 * time.Second
	defaultSSHReadyInitialWait = time.Millisecond
	defaultSSHReadyMaxWait = time.Millisecond
	defer func() {
		defaultSSHReadyTimeout = originalTimeout
		defaultSSHReadyInitialWait = originalInitialWait
		defaultSSHReadyMaxWait = originalMaxWait
	}()

	original := newSSHExecutor
	attempts := 0
	newSSHExecutor = func(cfg host.SSHConfig) host.Executor {
		return flexibleExecutor{
			run: func(command string, args ...string) (host.CommandResult, error) {
				key := command + " " + strings.Join(args, " ")
				switch {
				case strings.TrimSpace(key) == "true":
					return host.CommandResult{}, nil
				case key == "test -f /opt/agenthub/bootstrap.done":
					attempts++
					return host.CommandResult{}, errors.New("ssh connection timed out: verify the host address, network path, and security groups: exit status 255")
				default:
					return host.CommandResult{}, errors.New("unexpected command: " + key)
				}
			},
		}
	}
	defer func() { newSSHExecutor = original }()

	dir := t.TempDir()
	keyPath := filepath.Join(dir, "demo.pem")
	if err := os.WriteFile(keyPath, []byte("dummy"), 0o600); err != nil {
		t.Fatalf("WriteFile(key) error = %v", err)
	}

	cfg := &config.Config{
		Platform: config.PlatformConfig{Name: config.PlatformAWS},
		Region:   config.RegionConfig{Name: "us-east-1"},
		Instance: config.InstanceConfig{NetworkMode: "public"},
		Image:    config.ImageConfig{Name: "ubuntu-24.04"},
		SSH: config.SSHConfig{
			KeyName:        "demo-key",
			PrivateKeyPath: keyPath,
			CIDR:           "203.0.113.0/24",
			User:           "ubuntu",
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var stdout bytes.Buffer
	err := waitForBootstrapReady(ctx, cfg, "203.0.113.10", "", "", 22, &stdout)
	if err == nil {
		t.Fatal("waitForBootstrapReady() error = nil")
	}
	if !strings.Contains(err.Error(), "context deadline exceeded") {
		t.Fatalf("error = %v, want deadline-based SSH retry failure", err)
	}
	if attempts != 3 {
		t.Fatalf("attempts = %d, want 3 retries before deadline", attempts)
	}
}
