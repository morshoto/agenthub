package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"agenthub/internal/runtimeinstall"
)

const (
	defaultRuntimeExecuteTimeout   = 30 * time.Second
	maxRuntimeExecuteOutputBytes   = 64 << 10
	defaultRuntimeWorkspaceDirName = "workspace"
)

type runtimeExecuteRequest struct {
	Command        string   `json:"command"`
	Args           []string `json:"args"`
	Cwd            string   `json:"cwd"`
	TimeoutSeconds int      `json:"timeout_seconds"`
}

type runtimeExecuteResponse struct {
	Status   string   `json:"status"`
	Command  string   `json:"command"`
	Args     []string `json:"args,omitempty"`
	Cwd      string   `json:"cwd"`
	Stdout   string   `json:"stdout,omitempty"`
	Stderr   string   `json:"stderr,omitempty"`
	ExitCode int      `json:"exit_code"`
}

type runtimeCommandExecutor interface {
	Execute(ctx context.Context, req runtimeExecuteRequest) (runtimeExecuteResponse, error)
}

type localRuntimeCommandExecutor struct {
	workspaceRoot string
	allowedRoots  []string
}

type cappedBuffer struct {
	buf       bytes.Buffer
	remaining int
}

func newLocalRuntimeCommandExecutor(runtimeConfigPath string, runtimeCfg *runtimeinstall.RuntimeConfig) *localRuntimeCommandExecutor {
	workspaceRoot := defaultRuntimeWorkspace(runtimeConfigPath)
	allowedRoots := []string{workspaceRoot}
	if runtimeCfg != nil {
		for _, root := range runtimeCfg.Sandbox.FilesystemAllow {
			root = strings.TrimSpace(root)
			if root == "" {
				continue
			}
			allowedRoots = append(allowedRoots, root)
		}
	}
	return &localRuntimeCommandExecutor{
		workspaceRoot: workspaceRoot,
		allowedRoots:  uniqueCleanPaths(allowedRoots),
	}
}

func defaultRuntimeWorkspace(runtimeConfigPath string) string {
	base := strings.TrimSpace(runtimeConfigPath)
	if base == "" {
		return "/opt/agenthub/" + defaultRuntimeWorkspaceDirName
	}
	return filepath.Join(filepath.Dir(base), defaultRuntimeWorkspaceDirName)
}

func uniqueCleanPaths(paths []string) []string {
	seen := make(map[string]struct{}, len(paths))
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		if !filepath.IsAbs(p) {
			continue
		}
		cleaned := filepath.Clean(p)
		if _, ok := seen[cleaned]; ok {
			continue
		}
		seen[cleaned] = struct{}{}
		out = append(out, cleaned)
	}
	return out
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	written := len(p)
	if b.remaining <= 0 {
		return written, nil
	}
	if len(p) > b.remaining {
		p = p[:b.remaining]
	}
	n, err := b.buf.Write(p)
	b.remaining -= n
	return written, err
}

func (b *cappedBuffer) String() string {
	return b.buf.String()
}

func (e *localRuntimeCommandExecutor) Execute(ctx context.Context, req runtimeExecuteRequest) (runtimeExecuteResponse, error) {
	command := strings.TrimSpace(req.Command)
	if command == "" {
		return runtimeExecuteResponse{}, errors.New("command is required")
	}

	cwd, err := e.resolveWorkingDirectory(strings.TrimSpace(req.Cwd))
	if err != nil {
		return runtimeExecuteResponse{}, err
	}

	timeout := defaultRuntimeExecuteTimeout
	if req.TimeoutSeconds > 0 {
		timeout = time.Duration(req.TimeoutSeconds) * time.Second
	}
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(execCtx, command, req.Args...)
	cmd.Dir = cwd

	stdout := &cappedBuffer{remaining: maxRuntimeExecuteOutputBytes}
	stderr := &cappedBuffer{remaining: maxRuntimeExecuteOutputBytes}
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	response := runtimeExecuteResponse{
		Status:  "ok",
		Command: command,
		Args:    append([]string(nil), req.Args...),
		Cwd:     cwd,
	}

	err = cmd.Run()
	response.Stdout = strings.TrimSpace(stdout.String())
	response.Stderr = strings.TrimSpace(stderr.String())
	if err == nil {
		return response, nil
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		response.ExitCode = exitErr.ExitCode()
		return response, nil
	}
	if errors.Is(execCtx.Err(), context.DeadlineExceeded) {
		response.ExitCode = -1
		if response.Stderr == "" {
			response.Stderr = fmt.Sprintf("command timed out after %s", timeout)
		}
		return response, nil
	}
	if errors.Is(err, exec.ErrNotFound) {
		return runtimeExecuteResponse{}, fmt.Errorf("command %q was not found", command)
	}
	if response.Stderr == "" {
		response.Stderr = err.Error()
	}
	return response, nil
}

func (e *localRuntimeCommandExecutor) resolveWorkingDirectory(requested string) (string, error) {
	if strings.TrimSpace(requested) == "" {
		return e.workspaceRoot, nil
	}
	if !filepath.IsAbs(requested) {
		return "", errors.New("cwd must be an absolute path")
	}
	cleaned := filepath.Clean(requested)
	for _, root := range e.allowedRoots {
		if isWithinRoot(cleaned, root) {
			return cleaned, nil
		}
	}
	return "", fmt.Errorf("cwd %q is outside the allowed workspace roots", cleaned)
}

func isWithinRoot(path, root string) bool {
	path = filepath.Clean(path)
	root = filepath.Clean(root)
	if path == root {
		return true
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

var runtimeExecuteMkdirAll = func(path string, perm fs.FileMode) error {
	return os.MkdirAll(path, perm)
}

func (e *localRuntimeCommandExecutor) ensureWorkspace() error {
	return runtimeExecuteMkdirAll(e.workspaceRoot, 0o755)
}
