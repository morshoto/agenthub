package app

import (
	"context"
	"fmt"
	"io"
	"strings"
)

type stageRunner interface {
	// fn must respect ctx so cancellation can stop the underlying work.
	Run(ctx context.Context, title string, fn func(context.Context) error) error
}

type progressRenderer struct {
	out io.Writer
	tty bool
}

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

	fmt.Fprintf(p.out, "%s ...\n", title)
	err := fn(ctx)
	if err != nil {
		fmt.Fprintf(p.out, "failed: %s: %v\n", title, err)
		return err
	}
	fmt.Fprintf(p.out, "done: %s\n", title)
	return nil
}
