package daemon

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// TestRegisterRuntimesForWorkspace_CallsMarkStartingBeforeRegister pins the
// ordering this feature depends on: MarkStarting must fire before the
// (possibly ~20s on a cold cache) agent-CLI version-probe loop that precedes
// the real register call, so the server has a "starting" fact to show during
// exactly the window a machine coming back from a crash otherwise has none.
func TestRegisterRuntimesForWorkspace_CallsMarkStartingBeforeRegister(t *testing.T) {
	oldDetect := detectAgentVersion
	oldCheck := checkAgentMinVersion
	detectAgentVersion = func(context.Context, string) (string, error) { return "9.9.9", nil }
	checkAgentMinVersion = func(string, string) error { return nil }
	t.Cleanup(func() {
		detectAgentVersion = oldDetect
		checkAgentMinVersion = oldCheck
	})

	var startingCalls, registerCalls atomic.Int32
	var startingHappenedBeforeRegister atomic.Bool
	startingHappenedBeforeRegister.Store(true)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/daemon/starting":
			startingCalls.Add(1)
			if registerCalls.Load() != 0 {
				startingHappenedBeforeRegister.Store(false)
			}
			var req struct {
				WorkspaceID string `json:"workspace_id"`
				DaemonID    string `json:"daemon_id"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			if req.WorkspaceID != "ws-1" || req.DaemonID != "daemon-1" {
				t.Errorf("starting call body = %+v, want ws-1/daemon-1", req)
			}
			w.WriteHeader(http.StatusOK)
		case "/api/daemon/register":
			registerCalls.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"runtimes": []map[string]string{{
					"id":           "rt-1",
					"workspace_id": "ws-1",
					"provider":     "pi",
				}},
			})
		default:
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	c.SetToken("mul-profile")

	d := &Daemon{
		cfg: Config{
			DaemonID: "daemon-1",
			Agents: map[string]AgentEntry{
				"pi": {Path: "pi"},
			},
		},
		client:        c,
		logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		agentVersions: make(map[string]string),
	}

	if _, err := d.registerRuntimesForWorkspace(context.Background(), "ws-1"); err != nil {
		t.Fatalf("registerRuntimesForWorkspace: %v", err)
	}
	if got := startingCalls.Load(); got != 1 {
		t.Fatalf("starting calls = %d, want 1", got)
	}
	if got := registerCalls.Load(); got != 1 {
		t.Fatalf("register calls = %d, want 1", got)
	}
	if !startingHappenedBeforeRegister.Load() {
		t.Fatal("MarkStarting must be called before register, not after")
	}
}

// TestRegisterRuntimesForWorkspace_MarkStartingFailureDoesNotBlockRegister
// proves MarkStarting is genuinely best-effort: a server error on
// /api/daemon/starting must not prevent registerRuntimesForWorkspace from
// completing normally. Losing the "starting" display for one cycle is an
// acceptable cost; failing startup over it is not.
func TestRegisterRuntimesForWorkspace_MarkStartingFailureDoesNotBlockRegister(t *testing.T) {
	oldDetect := detectAgentVersion
	oldCheck := checkAgentMinVersion
	detectAgentVersion = func(context.Context, string) (string, error) { return "9.9.9", nil }
	checkAgentMinVersion = func(string, string) error { return nil }
	t.Cleanup(func() {
		detectAgentVersion = oldDetect
		checkAgentMinVersion = oldCheck
	})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/daemon/starting":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"boom"}`))
		case "/api/daemon/register":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"runtimes": []map[string]string{{
					"id":           "rt-1",
					"workspace_id": "ws-1",
					"provider":     "pi",
				}},
			})
		default:
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	c.SetToken("mul-profile")

	d := &Daemon{
		cfg: Config{
			DaemonID: "daemon-1",
			Agents: map[string]AgentEntry{
				"pi": {Path: "pi"},
			},
		},
		client:        c,
		logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		agentVersions: make(map[string]string),
	}

	done := make(chan error, 1)
	go func() {
		_, err := d.registerRuntimesForWorkspace(context.Background(), "ws-1")
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("registerRuntimesForWorkspace failed after a MarkStarting error: %v, want success (best-effort)", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("registerRuntimesForWorkspace did not return — a MarkStarting failure must not block it")
	}
}
