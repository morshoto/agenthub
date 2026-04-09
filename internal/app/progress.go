package app

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
)

type stageRunner interface {
	// fn must respect ctx so cancellation can stop the underlying work.
	Run(ctx context.Context, title string, fn func(context.Context) error) error
}

type progressRenderer struct {
	out  io.Writer
	tty  bool
	lock sync.Mutex
}

const (
	progressAnsiReset = "\x1b[0m"
	progressAnsiCyan  = "\x1b[36m"
)

func newProgressRenderer(out io.Writer) *progressRenderer {
	return &progressRenderer{
		out: out,
		tty: isTerminalWriter(out),
	}
}

func (p *progressRenderer) Run(ctx context.Context, title string, fn func(context.Context) error) error {
	if p == nil {
		return fn(ctx)
	}

	title = strings.TrimSpace(title)
	if title == "" {
		return fn(ctx)
	}

	if !p.tty {
		fmt.Fprintf(p.out, "%s ...\n", title)
		err := fn(ctx)
		if err != nil {
			fmt.Fprintf(p.out, "failed: %s: %v\n", title, err)
			return err
		}
		fmt.Fprintf(p.out, "done: %s\n", title)
		return nil
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- fn(ctx)
	}()
	fmt.Fprintf(p.out, "%s ...\n", title)

	for {
		select {
		case <-ctx.Done():
			fmt.Fprintf(p.out, "failed: %s: %v\n", title, ctx.Err())
			return ctx.Err()
		case err := <-errCh:
			if err != nil {
				fmt.Fprintf(p.out, "x %s: %v\n", title, err)
				return err
			}
			fmt.Fprintf(p.out, "done: %s\n", title)
			return nil
		case <-ticker.C:
			p.lock.Lock()
			fmt.Fprintf(p.out, "\r[%s%s%s] %s", progressAnsiCyan, frames[frame%len(frames)], progressAnsiReset, title)
			p.lock.Unlock()
			frame++
		}
	}
}
