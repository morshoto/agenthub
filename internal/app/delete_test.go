package app

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDeleteCommandRemovesAgentDirectoryAfterConfirmation(t *testing.T) {
	agentsDir := filepath.Join(t.TempDir(), "agents")
	agentDir := filepath.Join(agentsDir, "alpha")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(agent) error = %v", err)
	}
	writeConfig(t, filepath.Join(agentDir, "config.yaml"), `
platform:
  name: aws
region:
  name: us-east-1
instance:
  type: t3.medium
  disk_size_gb: 20
image:
  name: ubuntu-24.04
runtime:
  endpoint: http://localhost:11434
  model: llama3.2
sandbox:
  enabled: false
`)
	if err := os.WriteFile(filepath.Join(agentDir, ".env"), []byte("SLACK_BOT_TOKEN=xoxb-test\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(.env) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "override.yaml"), []byte("runtime:\n  port: 8080\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(override) error = %v", err)
	}

	stdout, err := runDeleteCommand(t, []string{"agenthub", "delete", "alpha", "--agents-dir", agentsDir}, "y\n")
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	for _, fragment := range []string{
		"removing local agent state: " + agentDir,
		"deleted local agent: alpha",
	} {
		if !strings.Contains(stdout, fragment) {
			t.Fatalf("stdout = %q, want %q", stdout, fragment)
		}
	}
	if _, err := os.Stat(agentDir); !os.IsNotExist(err) {
		t.Fatalf("agent dir still exists or unexpected error: %v", err)
	}
}

func TestDeleteCommandSupportsForceWithoutPrompt(t *testing.T) {
	agentsDir := filepath.Join(t.TempDir(), "agents")
	agentDir := filepath.Join(agentsDir, "alpha")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(agent) error = %v", err)
	}

	stdout, err := runDeleteCommand(t, []string{"agenthub", "delete", "alpha", "--agents-dir", agentsDir, "--force"}, "")
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !strings.Contains(stdout, "deleted local agent: alpha") {
		t.Fatalf("stdout = %q, want success message", stdout)
	}
	if _, err := os.Stat(agentDir); !os.IsNotExist(err) {
		t.Fatalf("agent dir still exists or unexpected error: %v", err)
	}
}

func TestDeleteCommandFailsWhenAgentDoesNotExist(t *testing.T) {
	agentsDir := filepath.Join(t.TempDir(), "agents")
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(agents) error = %v", err)
	}

	_, err := runDeleteCommand(t, []string{"agenthub", "delete", "missing", "--agents-dir", agentsDir, "--force"}, "")
	if err == nil {
		t.Fatal("Execute() error = nil, want missing agent failure")
	}
	if got := err.Error(); !strings.Contains(got, `agent "missing" does not exist under`) {
		t.Fatalf("error = %q, want missing agent message", got)
	}
}

func TestDeleteCommandFailsWithoutForceInNonInteractiveMode(t *testing.T) {
	agentsDir := filepath.Join(t.TempDir(), "agents")
	agentDir := filepath.Join(agentsDir, "alpha")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(agent) error = %v", err)
	}

	_, err := runDeleteCommandWithInput(t, []string{"agenthub", "delete", "alpha", "--agents-dir", agentsDir}, bytes.NewBuffer(nil))
	if err == nil {
		t.Fatal("Execute() error = nil, want non-interactive confirmation failure")
	}
	if got := err.Error(); !strings.Contains(got, "rerun with --force in non-interactive mode") {
		t.Fatalf("error = %q, want --force guidance", got)
	}
	if _, statErr := os.Stat(agentDir); statErr != nil {
		t.Fatalf("agent dir stat error = %v, want directory preserved", statErr)
	}
}

func TestDeleteCommandCancelsWhenConfirmationDeclined(t *testing.T) {
	agentsDir := filepath.Join(t.TempDir(), "agents")
	agentDir := filepath.Join(agentsDir, "alpha")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(agent) error = %v", err)
	}

	stdout, err := runDeleteCommand(t, []string{"agenthub", "delete", "alpha", "--agents-dir", agentsDir}, "n\n")
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !strings.Contains(stdout, "deletion cancelled") {
		t.Fatalf("stdout = %q, want cancellation message", stdout)
	}
	if _, statErr := os.Stat(agentDir); statErr != nil {
		t.Fatalf("agent dir stat error = %v, want directory preserved", statErr)
	}
}

func TestDeleteCommandRequiresAgentArgument(t *testing.T) {
	_, err := runDeleteCommand(t, []string{"agenthub", "delete"}, "")
	if err == nil {
		t.Fatal("Execute() error = nil, want missing argument failure")
	}
	if got := err.Error(); !strings.Contains(got, "accepts 1 arg(s), received 0") {
		t.Fatalf("error = %q, want cobra arg failure", got)
	}
}

func runDeleteCommand(t *testing.T, args []string, input string) (string, error) {
	t.Helper()
	return runDeleteCommandWithInput(t, args, strings.NewReader(input))
}

func runDeleteCommandWithInput(t *testing.T, args []string, input io.Reader) (string, error) {
	t.Helper()

	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()
	os.Args = args

	app := New()
	cmd := newRootCommand(app)
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetIn(input)

	err := cmd.Execute()
	return stdout.String(), err
}
