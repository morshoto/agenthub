package setup

import (
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"
)

type wizardProgressStatus string

const (
	wizardStatusPending wizardProgressStatus = "pending"
	wizardStatusCurrent wizardProgressStatus = "current"
	wizardStatusDone    wizardProgressStatus = "done"
	wizardStatusSkipped wizardProgressStatus = "skipped"
)

type wizardProgressItem struct {
	Label string
	Value string
}

func renderWizardProgress(out io.Writer, phase, title string, step, total int, current string, items []wizardProgressItem) {
	if out == nil {
		return
	}

	if isTerminalWriter(out) {
		fmt.Fprint(out, "\r\033[2J\033[H")
	}

	phase = strings.TrimSpace(phase)
	title = strings.TrimSpace(title)
	if title == "" {
		title = "Setup"
	}
	current = strings.TrimSpace(current)
	if total <= 0 {
		total = len(items)
	}
	if step <= 0 {
		step = 1
	}

	current = strings.TrimSpace(current)
	currentIndex := -1
	if current != "" {
		for i, item := range items {
			if strings.EqualFold(strings.TrimSpace(item.Label), current) {
				currentIndex = i
				break
			}
		}
	}
	if currentIndex < 0 {
		currentIndex = step - 1
	}

	if phase != "" {
		fmt.Fprintln(out)
		fmt.Fprintln(out, phase)
	}
	fmt.Fprintln(out)
	fmt.Fprintf(out, "%s  Step %d/%d\n\n", title, step, total)
	for i, item := range items {
		label := strings.TrimSpace(item.Label)
		if label == "" {
			continue
		}
		value := strings.TrimSpace(item.Value)
		switch {
		case i < currentIndex:
			if value == "" {
				value = "done"
			}
			fmt.Fprintf(out, "✓ %-18s %s\n", label, value)
		case i == currentIndex:
			fmt.Fprintf(out, "→ %-18s -\n", label)
		default:
			fmt.Fprintf(out, "  %-18s -\n", label)
		}
	}
}

func renderWizardPhase(out io.Writer, title string, body ...string) {
	if out == nil {
		return
	}
	title = strings.TrimSpace(title)
	if title == "" {
		return
	}
	fmt.Fprintln(out)
	fmt.Fprintln(out, title)
	if len(body) == 0 {
		return
	}
	for _, line := range body {
		if text := strings.TrimSpace(line); text != "" {
			fmt.Fprintln(out, text)
		}
	}
}

func valueOrDash(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "-"
	}
	return value
}

func isTerminalWriter(w io.Writer) bool {
	file, ok := w.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(file.Fd()))
}
