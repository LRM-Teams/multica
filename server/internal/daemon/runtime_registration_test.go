package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// TestRegisterRuntimesForWorkspace_DoesNotCallStarting pins the Computer
// V1 / Raft alignment: machine liveness is the DaemonCore Workspace Runner
// socket, not a pre-register HTTP starting signal. Version probing may
// still delay register; that must not resurrect /api/daemon/starting.
func TestRegisterRuntimesForWorkspace_DoesNotCallStarting(t *testing.T) {
	oldDetect := detectAgentVersion
	oldCheck := checkAgentMinVersion
	detectAgentVersion = func(context.Context, string) (string, error) { return "9.9.9", nil }
	checkAgentMinVersion = func(string, string) error { return nil }
	t.Cleanup(func() {
		detectAgentVersion = oldDetect
		checkAgentMinVersion = oldCheck
	})

	var startingCalls, registerCalls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/daemon/starting":
			startingCalls.Add(1)
			t.Errorf("retired /api/daemon/starting must not be called")
			w.WriteHeader(http.StatusNotFound)
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
	if got := startingCalls.Load(); got != 0 {
		t.Fatalf("starting calls = %d, want 0", got)
	}
	if got := registerCalls.Load(); got != 1 {
		t.Fatalf("register calls = %d, want 1", got)
	}
}

// TestRegisterRuntimesForWorkspace_ZeroRegisterableAgentsKeepsComputerConnected
// is the Computer V1 contract: a Binding that detects agent CLIs but cannot
// register any of them (too old / version probe failed) must stay connected
// with an empty runtime set. Setup success is Computer connectivity, not
// runtime count.
func TestRegisterRuntimesForWorkspace_ZeroRegisterableAgentsKeepsComputerConnected(t *testing.T) {
	oldDetect := detectAgentVersion
	oldCheck := checkAgentMinVersion
	detectAgentVersion = func(context.Context, string) (string, error) { return "0.0.1", nil }
	checkAgentMinVersion = func(string, string) error { return fmt.Errorf("below minimum") }
	t.Cleanup(func() {
		detectAgentVersion = oldDetect
		checkAgentMinVersion = oldCheck
	})

	var registerCalls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/daemon/register":
			registerCalls.Add(1)
			t.Errorf("empty runtime set must not POST /api/daemon/register")
			w.WriteHeader(http.StatusBadRequest)
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
				"claude": {Path: "claude"},
			},
		},
		client:        c,
		logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		agentVersions: make(map[string]string),
	}

	resp, err := d.registerRuntimesForWorkspace(context.Background(), "ws-1")
	if err != nil {
		t.Fatalf("zero registerable agents must keep the Computer connected: %v", err)
	}
	if resp == nil {
		t.Fatal("expected an empty RegisterResponse, got nil")
	}
	if len(resp.Runtimes) != 0 {
		t.Fatalf("runtimes = %#v, want none", resp.Runtimes)
	}
	if got := registerCalls.Load(); got != 0 {
		t.Fatalf("register calls = %d, want 0", got)
	}
}
