package app

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestProgressRendererClearLineUsesANSIEraseSequence(t *testing.T) {
	var out bytes.Buffer
	r := &progressRenderer{out: &out, tty: true}

	r.clearLine()

	if got := out.String(); got != "\r\033[2K" {
		t.Fatalf("clearLine() output = %q, want ANSI erase line sequence", got)
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

	if got := out.String(); !strings.Contains(got, "\033[2K") {
		t.Fatalf("output %q does not clear the line", got)
	}
}
