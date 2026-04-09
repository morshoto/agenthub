package app

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
)

type createProgressRenderer struct {
	out  io.Writer
	tty  bool
	lock sync.Mutex
}

func newCreateProgressRenderer(out io.Writer) *createProgressRenderer {
	return &createProgressRenderer{
		out: out,
		tty: isTerminalWriter(out),
	}
}

func (r *createProgressRenderer) Run(ctx context.Context, title string, fn func(context.Context) error) error {
	return r.RunGroup(ctx, "", title, fn)
}

func (r *createProgressRenderer) RunGroup(ctx context.Context, group, title string, fn func(context.Context) error) error {
	if r == nil {
		return fn(ctx)
	}

	group = strings.TrimSpace(group)
	title = strings.TrimSpace(title)
	if title == "" {
		return fn(ctx)
	}

	label := title
	if group != "" {
		label = group + ": " + title
	}

	r.writeLine(fmt.Sprintf("%s ...", label))
	err := fn(ctx)
	if err != nil {
		r.writeLine(fmt.Sprintf("failed: %s: %v", label, err))
		return err
	}
	r.writeLine(fmt.Sprintf("done: %s", label))
	return nil
}

func (r *createProgressRenderer) writeLine(line string) {
	if r == nil || strings.TrimSpace(line) == "" {
		return
	}
	r.lock.Lock()
	defer r.lock.Unlock()
	fmt.Fprintln(r.out, line)
}
