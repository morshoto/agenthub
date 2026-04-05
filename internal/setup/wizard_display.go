package setup

import (
	"fmt"
	"io"
	"strings"
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
	Status wizardProgressStatus
}

func renderWizardProgress(out io.Writer, title string, step, total int, current string, items []wizardProgressItem) {
	if out == nil {
		return
	}

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

	fmt.Fprintln(out)
	fmt.Fprintf(out, "%s  Step %d/%d\n\n", title, step, total)
	for _, item := range items {
		label := strings.TrimSpace(item.Label)
		if label == "" {
			continue
		}
		value := strings.TrimSpace(item.Value)
		switch item.Status {
		case wizardStatusDone:
			if value == "" {
				value = "done"
			}
			fmt.Fprintf(out, "✓ %-18s %s\n", label, value)
		case wizardStatusSkipped:
			if value == "" {
				value = "n/a"
			}
			fmt.Fprintf(out, "- %-18s %s\n", label, value)
		case wizardStatusCurrent:
			if value == "" {
				value = "-"
			}
			fmt.Fprintf(out, "→ %-18s %s\n", label, value)
		default:
			if value == "" {
				value = "-"
			}
			fmt.Fprintf(out, "  %-18s %s\n", label, value)
		}
	}
	if current != "" {
		fmt.Fprintf(out, "\nCurrent: %s\n", current)
	}
}

func wizardDecisionLine(out io.Writer, label, value string) {
	if out == nil {
		return
	}
	label = strings.TrimSpace(label)
	value = strings.TrimSpace(value)
	if label == "" {
		return
	}
	if value == "" {
		value = "done"
	}
	fmt.Fprintf(out, "✓ %s: %s\n", label, value)
}

func valueOrDash(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "-"
	}
	return value
}
