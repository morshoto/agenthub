package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"openclaw/internal/runtimeinstall"
)

type runtimeActivityTracker struct {
	mu     sync.Mutex
	nextID int64
	active map[string]runtimeActiveAgent
}

type runtimeActiveAgent struct {
	ID                string    `json:"id"`
	Task              string    `json:"task"`
	Model             string    `json:"model,omitempty"`
	StartedAt         time.Time `json:"started_at"`
	RunningForSeconds int64     `json:"running_for_seconds"`
}

type runtimeStatusResponse struct {
	Status       string               `json:"status"`
	Active       bool                 `json:"active"`
	ActiveCount  int                  `json:"active_count"`
	ActiveAgents []runtimeActiveAgent `json:"active_agents,omitempty"`
}

type runtimeServerState struct {
	runtimeConfigPath string
	listenAddr        string
	runtimeCfg        *runtimeinstall.RuntimeConfig
	generator         runtimeGenerator
	tracker           *runtimeActivityTracker

	mu           sync.Mutex
	lastActivity time.Time
}

func newRuntimeActivityTracker() *runtimeActivityTracker {
	return &runtimeActivityTracker{
		active: make(map[string]runtimeActiveAgent),
	}
}

func (t *runtimeActivityTracker) Start(task, model string) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.active == nil {
		t.active = make(map[string]runtimeActiveAgent)
	}
	t.nextID++
	id := fmt.Sprintf("agent-%d", t.nextID)
	task = strings.TrimSpace(task)
	if task == "" {
		task = "working"
	}
	t.active[id] = runtimeActiveAgent{
		ID:        id,
		Task:      task,
		Model:     strings.TrimSpace(model),
		StartedAt: time.Now().UTC(),
	}
	return id
}

func (t *runtimeActivityTracker) Finish(id string) {
	id = strings.TrimSpace(id)
	if id == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.active, id)
}

func (t *runtimeActivityTracker) Snapshot() runtimeStatusResponse {
	now := time.Now().UTC()
	t.mu.Lock()
	defer t.mu.Unlock()

	agents := make([]runtimeActiveAgent, 0, len(t.active))
	for _, agent := range t.active {
		copy := agent
		if !copy.StartedAt.IsZero() {
			copy.RunningForSeconds = int64(now.Sub(copy.StartedAt).Seconds())
			if copy.RunningForSeconds < 0 {
				copy.RunningForSeconds = 0
			}
		}
		agents = append(agents, copy)
	}
	sort.Slice(agents, func(i, j int) bool {
		if agents[i].StartedAt.Equal(agents[j].StartedAt) {
			return agents[i].ID < agents[j].ID
		}
		return agents[i].StartedAt.Before(agents[j].StartedAt)
	})

	return runtimeStatusResponse{
		Status:       "ok",
		Active:       len(agents) > 0,
		ActiveCount:  len(agents),
		ActiveAgents: agents,
	}
}

func newRuntimeServerState(runtimeConfigPath, listenAddr string, runtimeCfg *runtimeinstall.RuntimeConfig, generator runtimeGenerator) *runtimeServerState {
	return &runtimeServerState{
		runtimeConfigPath: runtimeConfigPath,
		listenAddr:        listenAddr,
		runtimeCfg:        runtimeCfg,
		generator:         generator,
		tracker:           newRuntimeActivityTracker(),
		lastActivity:      time.Now(),
	}
}

func (s *runtimeServerState) touch() {
	s.mu.Lock()
	s.lastActivity = time.Now()
	s.mu.Unlock()
}

func (s *runtimeServerState) readLastActivity() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastActivity
}

func (s *runtimeServerState) healthPayload() map[string]any {
	cfg := s.runtimeCfg
	payload := map[string]any{
		"status":           "ok",
		"runtime_config":   s.runtimeConfigPath,
		"listen":           s.listenAddr,
		"configured_port":  0,
		"sandbox_enabled":  false,
		"sandbox_network":  "",
		"filesystem_allow": []string(nil),
	}
	if cfg == nil {
		return payload
	}
	payload["use_nemoclaw"] = cfg.UseNemoClaw
	payload["provider"] = cfg.Provider
	payload["region"] = cfg.Region
	payload["nim_endpoint"] = cfg.NIMEndpoint
	payload["model"] = cfg.Model
	payload["configured_port"] = cfg.Port
	payload["sandbox_enabled"] = cfg.Sandbox.Enabled
	payload["sandbox_network"] = cfg.Sandbox.NetworkMode
	payload["filesystem_allow"] = cfg.Sandbox.FilesystemAllow
	return payload
}

func (s *runtimeServerState) statusPayload() runtimeStatusResponse {
	if s == nil || s.tracker == nil {
		return runtimeStatusResponse{Status: "ok"}
	}
	return s.tracker.Snapshot()
}

func newRuntimeServerMux(state *runtimeServerState) *http.ServeMux {
	mux := http.NewServeMux()
	if state == nil {
		state = newRuntimeServerState("", "", nil, nil)
	}

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		state.touch()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(state.healthPayload())
	})
	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		state.touch()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(state.statusPayload())
	})
	mux.HandleFunc("/v1/generate", func(w http.ResponseWriter, r *http.Request) {
		state.touch()
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if state.runtimeCfg == nil || strings.ToLower(strings.TrimSpace(state.runtimeCfg.Provider)) != "aws-bedrock" {
			http.Error(w, "generate is only available for aws-bedrock provider", http.StatusNotImplemented)
			return
		}
		if state.generator == nil {
			http.Error(w, "bedrock generator is not configured", http.StatusServiceUnavailable)
			return
		}
		var req struct {
			Prompt string `json:"prompt"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, fmt.Sprintf("parse request: %v", err), http.StatusBadRequest)
			return
		}

		agentID := state.tracker.Start("generating response", state.runtimeCfg.Model)
		defer state.tracker.Finish(agentID)

		output, err := state.generator.Generate(r.Context(), req.Prompt)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":   "ok",
			"provider": state.runtimeCfg.Provider,
			"model":    state.runtimeCfg.Model,
			"output":   output,
		})
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		state.touch()
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ready"))
	})
	mux.HandleFunc("/config", func(w http.ResponseWriter, r *http.Request) {
		state.touch()
		w.Header().Set("Content-Type", "application/x-yaml")
		data, err := yaml.Marshal(state.runtimeCfg)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_, _ = w.Write(data)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		state.touch()
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("openclaw runtime"))
	})
	return mux
}
