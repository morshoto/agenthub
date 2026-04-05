package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/term"
)

type createProgressRenderer struct {
	out   io.Writer
	tty   bool
	lock  sync.Mutex
	start time.Time
	tasks []createProgressTask
}

type createProgressTask struct {
	title      string
	startedAt  time.Time
	finishedAt time.Time
	running    bool
	err        error
}

func newCreateProgressRenderer(out io.Writer) *createProgressRenderer {
	return &createProgressRenderer{
		out: out,
		tty: isTerminalWriter(out),
	}
}

func (r *createProgressRenderer) Run(ctx context.Context, title string, fn func(context.Context) error) error {
	if r == nil {
		return fn(ctx)
	}

	title = strings.TrimSpace(title)
	if title == "" {
		return fn(ctx)
	}

	if !r.tty {
		fmt.Fprintf(r.out, "%s ...\n", title)
		err := fn(ctx)
		if err != nil {
			fmt.Fprintf(r.out, "failed: %s: %v\n", title, err)
			return err
		}
		fmt.Fprintf(r.out, "done: %s\n", title)
		return nil
	}

	task := r.startTask(title)

	errCh := make(chan error, 1)
	go func() {
		errCh <- fn(ctx)
	}()

	ticker := time.NewTicker(120 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			r.finishTask(task, ctx.Err())
			r.render()
			return ctx.Err()
		case err := <-errCh:
			r.finishTask(task, err)
			r.render()
			if err != nil {
				return err
			}
			return nil
		case <-ticker.C:
			r.render()
		}
	}
}

func (r *createProgressRenderer) startTask(title string) *createProgressTask {
	r.lock.Lock()
	defer r.lock.Unlock()

	if r.start.IsZero() {
		r.start = time.Now()
	}
	task := createProgressTask{
		title:     title,
		startedAt: time.Now(),
		running:   true,
	}
	r.tasks = append(r.tasks, task)
	return &r.tasks[len(r.tasks)-1]
}

func (r *createProgressRenderer) finishTask(task *createProgressTask, err error) {
	if r == nil || task == nil {
		return
	}
	r.lock.Lock()
	defer r.lock.Unlock()

	task.running = false
	task.finishedAt = time.Now()
	task.err = err
}

func (r *createProgressRenderer) render() {
	r.lock.Lock()
	defer r.lock.Unlock()

	if !r.tty {
		return
	}

	fmt.Fprint(r.out, "\r\033[2J\033[H")

	elapsed := time.Since(r.start)
	if r.start.IsZero() {
		elapsed = 0
	}
	total := len(r.tasks)
	done := 0
	running := 0
	for _, task := range r.tasks {
		if !task.running && task.err == nil && !task.finishedAt.IsZero() {
			done++
		}
		if task.running {
			running++
		}
	}

	fmt.Fprintf(r.out, "Provisioning %s (%d/%d) • %d running\n\n", formatProgressDuration(elapsed), done, total, running)

	groups := []string{"Infrastructure", "Access", "Runtime", "Verification"}
	for i, task := range r.tasks {
		group := groups[minInt(i, len(groups)-1)]
		if i > 0 {
			fmt.Fprintln(r.out)
		}
		fmt.Fprintln(r.out, group)
		fmt.Fprintln(r.out, renderProgressTaskLine(task))
	}
}

func renderProgressTaskLine(task createProgressTask) string {
	title := strings.TrimSpace(task.title)
	if title == "" {
		title = "task"
	}
	switch {
	case task.err != nil:
		return fmt.Sprintf("  ✕ %-32s %s", title, formatProgressDuration(taskDuration(task)))
	case task.running:
		return fmt.Sprintf("  %s %-32s %s", spinnerFrame(task.startedAt), title, formatProgressDuration(taskDuration(task)))
	case !task.finishedAt.IsZero():
		return fmt.Sprintf("  ✓ %-32s %s", title, formatProgressDuration(taskDuration(task)))
	default:
		return fmt.Sprintf("  • %-32s", title)
	}
}

func taskDuration(task createProgressTask) time.Duration {
	if task.startedAt.IsZero() {
		return 0
	}
	if task.running {
		return time.Since(task.startedAt)
	}
	if !task.finishedAt.IsZero() {
		return task.finishedAt.Sub(task.startedAt)
	}
	return 0
}

func formatProgressDuration(d time.Duration) string {
	if d <= 0 {
		return "0.0s"
	}
	return fmt.Sprintf("%.1fs", d.Seconds())
}

func spinnerFrame(start time.Time) string {
	frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	if start.IsZero() {
		return frames[0]
	}
	idx := int(time.Since(start) / (120 * time.Millisecond))
	return frames[idx%len(frames)]
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func isTerminalWriter(w io.Writer) bool {
	file, ok := w.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(file.Fd()))
}
