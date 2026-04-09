package setup

import (
	"fmt"
	"io"
	"os"
	"reflect"
	"strings"
	"sync"
	"time"

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

const (
	wizardAnsiReset  = "\033[0m"
	wizardAnsiDim    = "\033[2m"
	wizardAnsiGreen  = "\033[32m"
	wizardAnsiCyan   = "\033[36m"
	wizardAnsiYellow = "\033[33m"
)

var wizardProgressLineCount sync.Map

// renderWizardProgress redraws the step progress block in-place on a TTY,
// or appends it line-by-line on non-TTY writers.
func renderWizardProgress(out io.Writer, phase, title string, step, total int, current string, items []wizardProgressItem) {
	if out == nil {
		return
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

	tty := isTerminalWriter(out)
	lines := buildWizardProgressLines(tty, phase, title, step, total, currentIndex, items)
	if tty {
		if prev := wizardProgressPreviousLines(out); prev > 0 {
			fmt.Fprintf(out, "\033[%dA", prev)
		}
	}
	for _, line := range lines {
		fmt.Fprintf(out, "\r\033[2K%s\n", line)
	}
	if tty {
		wizardProgressLineCount.Store(wizardProgressKey(out), len(lines))
	}
}

func buildWizardProgressLines(tty bool, phase, title string, step, total, currentIndex int, items []wizardProgressItem) []string {
	lines := make([]string, 0, 4+len(items))
	if phase != "" {
		lines = append(lines, wizardStyle(tty, wizardAnsiDim, phase))
	}
	lines = append(lines, "")
	// [+] mirrors Docker's active-task prefix so the header reads at a glance.
	lines = append(lines, wizardStyle(tty, wizardAnsiCyan, fmt.Sprintf("[+] %s  Step %d/%d", title, step, total)))
	lines = append(lines, "")
	for i, item := range items {
		label := strings.TrimSpace(item.Label)
		if label == "" {
			continue
		}
		value := strings.TrimSpace(item.Value)
		switch {
		case i < currentIndex:
			if value == "" {
				value = wizardStyle(tty, wizardAnsiGreen, "done")
			} else {
				value = wizardStyle(tty, wizardAnsiGreen, value)
			}
			lines = append(lines, fmt.Sprintf("%s %-18s %s", wizardStyle(tty, wizardAnsiGreen, "✓"), label, value))
		case i == currentIndex:
			lines = append(lines, fmt.Sprintf("%s %-18s %s", wizardStyle(tty, wizardAnsiCyan, "→"), label, wizardStyle(tty, wizardAnsiDim, "-")))
		default:
			lines = append(lines, fmt.Sprintf("  %-18s %s", label, wizardStyle(tty, wizardAnsiDim, "-")))
		}
	}
	return lines
}

// wizardLogLine writes a single scroll-safe log line.
//
// Format (TTY):
//
//	[phase] ⇒ message                              1.2s
//
// The elapsed time is right-aligned to the terminal width so it mirrors the
// timing column in `docker compose up` output. On non-TTY writers the
// elapsed suffix is omitted.
//
// Pass elapsed < 0 to suppress the elapsed column entirely.
func wizardLogLine(out io.Writer, phase, symbol, message string, elapsed time.Duration) {
	if out == nil {
		return
	}
	tty := isTerminalWriter(out)

	if symbol == "" {
		symbol = "⇒"
	}

	// Build the left-hand portion: "[phase] ⇒ message"
	var left string
	if phase != "" {
		phaseTag := "[" + phase + "]"
		if tty {
			phaseTag = wizardAnsiDim + phaseTag + wizardAnsiReset
		}
		left = fmt.Sprintf("%s %s %s", phaseTag, symbol, message)
	} else {
		left = fmt.Sprintf("%s %s", symbol, message)
	}

	if tty && elapsed >= 0 {
		elapsedStr := formatWizardElapsed(elapsed)
		width := termWidth(out)
		visLen := wizardVisibleLen(left)
		padding := width - visLen - len(elapsedStr) - 1
		if padding > 1 {
			fmt.Fprintf(out, "%s%s%s\n", left, strings.Repeat(" ", padding), wizardAnsiDim+elapsedStr+wizardAnsiReset)
			return
		}
	}
	fmt.Fprintln(out, left)
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

// termWidth returns the terminal column width for right-aligning output.
// Falls back to 80 for non-TTY writers or when the size cannot be determined.
func termWidth(out io.Writer) int {
	if f, ok := out.(*os.File); ok {
		if w, _, err := term.GetSize(int(f.Fd())); err == nil && w > 0 {
			return w
		}
	}
	return 80
}

// wizardVisibleLen returns the printable rune count of s, ignoring ANSI
// escape sequences. Used to compute right-aligned padding.
func wizardVisibleLen(s string) int {
	inEsc := false
	n := 0
	for _, r := range s {
		switch {
		case r == '\033':
			inEsc = true
		case inEsc && r == 'm':
			inEsc = false
		case !inEsc:
			n++
		}
	}
	return n
}

// formatWizardElapsed formats a duration as "0.0s" (sub-second) or "12s".
// Matches the compact style in docker compose up output.
func formatWizardElapsed(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	return fmt.Sprintf("%.0fs", d.Seconds())
}

func isTerminalWriter(w io.Writer) bool {
	file, ok := w.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(file.Fd()))
}

func wizardProgressKey(out io.Writer) uintptr {
	value := reflect.ValueOf(out)
	if !value.IsValid() {
		return 0
	}
	switch value.Kind() {
	case reflect.Pointer, reflect.UnsafePointer:
		return value.Pointer()
	default:
		return 0
	}
}

func wizardProgressPreviousLines(out io.Writer) int {
	key := wizardProgressKey(out)
	if key == 0 {
		return 0
	}
	value, ok := wizardProgressLineCount.Load(key)
	if !ok {
		return 0
	}
	lines, ok := value.(int)
	if !ok {
		return 0
	}
	return lines
}

func wizardStyle(enabled bool, code, text string) string {
	if !enabled || text == "" {
		return text
	}
	return code + text + wizardAnsiReset
}
