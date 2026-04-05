package prompt

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestSessionSelectUsesDefaultInNonInteractiveMode(t *testing.T) {
	session := NewSession(strings.NewReader(""), &bytes.Buffer{})
	session.Interactive = false

	got, err := session.Select("platform", []string{"aws", "gcp"}, "aws")
	if err != nil {
		t.Fatalf("Select() error = %v", err)
	}
	if got != "aws" {
		t.Fatalf("Select() = %q, want aws", got)
	}
}

func TestSessionConfirmAndTextAndInt(t *testing.T) {
	in := strings.NewReader("y\nhello\n42\n")
	out := &bytes.Buffer{}
	session := NewSession(in, out)

	confirmed, err := session.Confirm("Continue", false)
	if err != nil {
		t.Fatalf("Confirm() error = %v", err)
	}
	if !confirmed {
		t.Fatal("Confirm() = false, want true")
	}

	text, err := session.Text("Name", "")
	if err != nil {
		t.Fatalf("Text() error = %v", err)
	}
	if text != "hello" {
		t.Fatalf("Text() = %q, want hello", text)
	}

	value, err := session.Int("Count", 0)
	if err != nil {
		t.Fatalf("Int() error = %v", err)
	}
	if value != 42 {
		t.Fatalf("Int() = %d, want 42", value)
	}

	if out.Len() == 0 {
		t.Fatal("expected prompts to be written")
	}
}

func TestSessionSecretUsesTextFallbackWhenNotTerminal(t *testing.T) {
	in := strings.NewReader("secret-value\n")
	out := &bytes.Buffer{}
	session := NewSession(in, out)

	got, err := session.Secret("API key", "")
	if err != nil {
		t.Fatalf("Secret() error = %v", err)
	}
	if got != "secret-value" {
		t.Fatalf("Secret() = %q, want secret-value", got)
	}
}

func TestSessionReadMenuKeyParsesArrowsAndDigits(t *testing.T) {
	session := NewSession(strings.NewReader("\x1b[A\x1b[B2"), &bytes.Buffer{})

	key, err := session.readMenuKey()
	if err != nil {
		t.Fatalf("readMenuKey() error = %v", err)
	}
	if key.kind != menuKeyUp {
		t.Fatalf("first key kind = %d, want up", key.kind)
	}

	key, err = session.readMenuKey()
	if err != nil {
		t.Fatalf("readMenuKey() error = %v", err)
	}
	if key.kind != menuKeyDown {
		t.Fatalf("second key kind = %d, want down", key.kind)
	}

	key, err = session.readMenuKey()
	if err != nil {
		t.Fatalf("readMenuKey() error = %v", err)
	}
	if key.kind != menuKeyDigit || key.index != 1 {
		t.Fatalf("third key = %+v, want digit index 1", key)
	}
}

func TestFilterSearchOptionsMatchesInstanceFamilyBoundaries(t *testing.T) {
	options := []string{"g5.xlarge", "g5g.xlarge", "t3.medium", "t3a.medium", "t3.xlarge", "t4g.large"}
	got := filterSearchOptions(options, "t3")
	want := []string{"t3.medium", "t3.xlarge"}
	if len(got) != len(want) {
		t.Fatalf("filterSearchOptions() len = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("filterSearchOptions()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestRenderMenuClearsPreviousLinesBeforeRedraw(t *testing.T) {
	out := &bytes.Buffer{}
	session := NewSession(strings.NewReader(""), out)

	lines := session.renderMenu("Select platform", []string{"aws", "gcp"}, "aws", 0, 0)
	session.renderMenu("Select platform", []string{"aws", "gcp"}, "aws", 1, lines)

	got := out.String()
	if count := strings.Count(got, "\033[2K"); count != 6 {
		t.Fatalf("rendered output contains %d clear-line sequences, want 6; output=%q", count, got)
	}
	if !strings.Contains(got, "> gcp") {
		t.Fatalf("rendered output = %q, want selected option marker", got)
	}
}

func TestCanUseCursorMenuFallsBackWhenSearchHelpWouldWrap(t *testing.T) {
	oldIsTerminalFile := isTerminalFileFunc
	oldTerminalSize := terminalSizeFunc
	isTerminalFileFunc = func(*os.File) bool { return true }
	terminalSizeFunc = func(*os.File) (int, int, error) { return 40, 24, nil }
	t.Cleanup(func() {
		isTerminalFileFunc = oldIsTerminalFile
		terminalSizeFunc = oldTerminalSize
	})

	inFile, err := os.CreateTemp(t.TempDir(), "prompt-in-*")
	if err != nil {
		t.Fatalf("CreateTemp(in) error = %v", err)
	}
	defer inFile.Close()
	outFile, err := os.CreateTemp(t.TempDir(), "prompt-out-*")
	if err != nil {
		t.Fatalf("CreateTemp(out) error = %v", err)
	}
	defer outFile.Close()

	session := NewSession(strings.NewReader(""), &bytes.Buffer{})
	session.inFile = inFile
	session.outFile = outFile

	if got := session.canUseCursorMenu("Select instance type", []string{"t3.small", "t3.xlarge"}, "t3.xlarge", true); got {
		t.Fatal("canUseCursorMenu() = true, want false when the search help line would wrap")
	}
}

func TestCanUseCursorMenuAllowsWideMenus(t *testing.T) {
	oldIsTerminalFile := isTerminalFileFunc
	oldTerminalSize := terminalSizeFunc
	isTerminalFileFunc = func(*os.File) bool { return true }
	terminalSizeFunc = func(*os.File) (int, int, error) { return 120, 24, nil }
	t.Cleanup(func() {
		isTerminalFileFunc = oldIsTerminalFile
		terminalSizeFunc = oldTerminalSize
	})

	inFile, err := os.CreateTemp(t.TempDir(), "prompt-in-*")
	if err != nil {
		t.Fatalf("CreateTemp(in) error = %v", err)
	}
	defer inFile.Close()
	outFile, err := os.CreateTemp(t.TempDir(), "prompt-out-*")
	if err != nil {
		t.Fatalf("CreateTemp(out) error = %v", err)
	}
	defer outFile.Close()

	session := NewSession(strings.NewReader(""), &bytes.Buffer{})
	session.inFile = inFile
	session.outFile = outFile

	if got := session.canUseCursorMenu("Select instance type", []string{"t3.small", "t3.xlarge"}, "t3.xlarge", true); !got {
		t.Fatal("canUseCursorMenu() = false, want true on a wide terminal")
	}
}
