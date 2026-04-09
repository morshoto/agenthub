package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agenthub/internal/runtimeinstall"
)

type fakeRuntimeExecutor struct {
	execute func(ctx context.Context, req runtimeExecuteRequest) (runtimeExecuteResponse, error)
}

func (f fakeRuntimeExecutor) Execute(ctx context.Context, req runtimeExecuteRequest) (runtimeExecuteResponse, error) {
	return f.execute(ctx, req)
}

func TestRuntimeExecuteEndpointRunsCommand(t *testing.T) {
	runtimeConfigPath := filepath.Join(t.TempDir(), "runtime.yaml")
	executor := newLocalRuntimeCommandExecutor(runtimeConfigPath, &runtimeinstall.RuntimeConfig{
		Sandbox: runtimeinstall.Sandbox{
			FilesystemAllow: []string{filepath.Dir(runtimeConfigPath)},
		},
	})
	if err := executor.ensureWorkspace(); err != nil {
		t.Fatalf("ensureWorkspace() error = %v", err)
	}

	mux := newRuntimeServerMux(newRuntimeServerState(runtimeConfigPath, "127.0.0.1:8080", &runtimeinstall.RuntimeConfig{}, nil, executor))
	body := bytes.NewBufferString(`{"command":"pwd"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/execute", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body = %q", rec.Code, http.StatusOK, rec.Body.String())
	}

	var response runtimeExecuteResponse
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if response.Status != "ok" {
		t.Fatalf("status = %q, want ok", response.Status)
	}
	if response.ExitCode != 0 {
		t.Fatalf("exit_code = %d, want 0", response.ExitCode)
	}
	wantDir := defaultRuntimeWorkspace(runtimeConfigPath)
	if response.Cwd != wantDir {
		t.Fatalf("cwd = %q, want %q", response.Cwd, wantDir)
	}
	if strings.TrimSpace(response.Stdout) != wantDir {
		t.Fatalf("stdout = %q, want %q", response.Stdout, wantDir)
	}
}

func TestRuntimeExecuteEndpointRejectsDisallowedCWD(t *testing.T) {
	runtimeConfigPath := filepath.Join(t.TempDir(), "runtime.yaml")
	executor := newLocalRuntimeCommandExecutor(runtimeConfigPath, &runtimeinstall.RuntimeConfig{})
	mux := newRuntimeServerMux(newRuntimeServerState(runtimeConfigPath, "127.0.0.1:8080", &runtimeinstall.RuntimeConfig{}, nil, executor))

	req := httptest.NewRequest(http.MethodPost, "/v1/execute", strings.NewReader(`{"command":"pwd","cwd":"/tmp"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	if !strings.Contains(rec.Body.String(), "outside the allowed workspace roots") {
		t.Fatalf("body = %q, want cwd validation error", rec.Body.String())
	}
}

func TestRuntimeExecuteEndpointReportsNonZeroExit(t *testing.T) {
	runtimeConfigPath := filepath.Join(t.TempDir(), "runtime.yaml")
	executor := newLocalRuntimeCommandExecutor(runtimeConfigPath, &runtimeinstall.RuntimeConfig{})
	if err := executor.ensureWorkspace(); err != nil {
		t.Fatalf("ensureWorkspace() error = %v", err)
	}
	mux := newRuntimeServerMux(newRuntimeServerState(runtimeConfigPath, "127.0.0.1:8080", &runtimeinstall.RuntimeConfig{}, nil, executor))

	req := httptest.NewRequest(http.MethodPost, "/v1/execute", strings.NewReader(`{"command":"sh","args":["-lc","echo nope >&2; exit 7"]}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%q", rec.Code, http.StatusOK, rec.Body.String())
	}

	var response runtimeExecuteResponse
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if response.ExitCode != 7 {
		t.Fatalf("exit_code = %d, want 7", response.ExitCode)
	}
	if !strings.Contains(response.Stderr, "nope") {
		t.Fatalf("stderr = %q, want command stderr", response.Stderr)
	}
}

func TestRuntimeStatusReportsActiveExecution(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	executor := fakeRuntimeExecutor{
		execute: func(ctx context.Context, req runtimeExecuteRequest) (runtimeExecuteResponse, error) {
			close(started)
			<-release
			return runtimeExecuteResponse{Status: "ok", Command: req.Command, Cwd: "/opt/agenthub/workspace"}, nil
		},
	}
	state := newRuntimeServerState("/opt/agenthub/runtime.yaml", "127.0.0.1:8080", &runtimeinstall.RuntimeConfig{}, nil, executor)
	mux := newRuntimeServerMux(state)

	done := make(chan struct{})
	go func() {
		defer close(done)
		req := httptest.NewRequest(http.MethodPost, "/v1/execute", strings.NewReader(`{"command":"sleep"}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
	}()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("execution did not start")
	}

	statusReq := httptest.NewRequest(http.MethodGet, "/status", nil)
	statusRec := httptest.NewRecorder()
	mux.ServeHTTP(statusRec, statusReq)
	if statusRec.Code != http.StatusOK {
		t.Fatalf("status code = %d, want %d", statusRec.Code, http.StatusOK)
	}

	var payload runtimeStatusResponse
	if err := json.NewDecoder(statusRec.Body).Decode(&payload); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if !payload.Active || payload.ActiveCount != 1 {
		t.Fatalf("payload = %#v, want one active execution", payload)
	}
	if len(payload.ActiveAgents) != 1 || payload.ActiveAgents[0].Task != "executing sleep" {
		t.Fatalf("active_agents = %#v, want executing sleep", payload.ActiveAgents)
	}

	close(release)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("execution did not finish")
	}
}
