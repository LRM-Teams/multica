package daemon

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestEnsureResidentMessageRuntimeUsesOnlyStableAgentConfiguration(t *testing.T) {
	const (
		workspaceID = "11111111-1111-1111-1111-111111111111"
		runtimeID   = "22222222-2222-2222-2222-222222222222"
		agentID     = "33333333-3333-3333-3333-333333333333"
	)
	var (
		mu       sync.Mutex
		requests []string
	)
	expiresAt := time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339Nano)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, r.Method+" "+r.URL.Path)
		mu.Unlock()
		if got := r.Header.Get("Authorization"); got != "Bearer daemon-token" {
			t.Errorf("Authorization = %q, want durable daemon credential", got)
		}
		switch r.Method + " " + r.URL.Path {
		case "GET /api/daemon/runtimes/" + runtimeID + "/agents/" + agentID + "/runtime-config":
			_ = json.NewEncoder(w).Encode(ResidentAgentRuntimeConfig{
				WorkspaceID: workspaceID, RuntimeID: runtimeID, WorkspaceContext: "Workspace context", RuntimeStateGeneration: 1,
				Agent: &AgentData{
					ID: agentID, Name: "message-agent", Instructions: "Follow the workspace rules.",
					CustomEnv: map[string]string{"MESSAGE_RUNTIME_SETTING": "enabled", "MULTICA_TASK_ID": "must-not-override"},
				},
			})
		case "POST /api/daemon/runtimes/" + runtimeID + "/agents/" + agentID + "/credential":
			_ = json.NewEncoder(w).Encode(AgentCredentialResponse{
				ID: "credential-1", AgentID: agentID, Prefix: "mat_test", Token: "durable-agent-token", ExpiresAt: &expiresAt,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	root := t.TempDir()
	client := NewClient(upstream.URL)
	client.SetToken("daemon-token")
	probe := &canonicalRuntimeFactoryProbe{}
	d := &Daemon{
		cfg: Config{
			ServerBaseURL:  upstream.URL,
			WorkspacesRoot: root,
			HealthPort:     19514,
			Agents: map[string]AgentEntry{
				"codex": {Path: "/usr/bin/true", Model: "codex-test"},
			},
		},
		client:                           client,
		logger:                           slog.New(slog.NewTextHandler(io.Discard, nil)),
		runtimeIndex:                     map[string]Runtime{runtimeID: {ID: runtimeID, WorkspaceID: workspaceID, Provider: "codex"}},
		agentVersions:                    make(map[string]string),
		canonicalRuntimes:                newCanonicalAgentRuntimePool(),
		canonicalResidentFactoryOverride: probe.factory,
	}
	if err := d.ensureResidentMessageRuntime(context.Background(), agentID, runtimeID); err != nil {
		t.Fatalf("ensureResidentMessageRuntime: %v", err)
	}
	if !d.canonicalRuntimes.hasResidentBackend(agentID, runtimeID) {
		t.Fatal("resident Message backend was not created")
	}
	created, _ := probe.counts()
	if created != 1 {
		t.Fatalf("resident factory calls = %d, want 1", created)
	}
	config := probe.latestConfig()
	for _, forbidden := range []string{
		"MULTICA_TASK_ID",
		"MULTICA_EXECUTION_ID",
		"MULTICA_AGENT_INBOX_EVENT_ID",
		"MULTICA_AGENT_INBOX_DELIVERY_ID",
		"MULTICA_AGENT_INBOX_LEASE_TOKEN",
		"MULTICA_TOKEN",
		"MULTICA_TOKEN_FILE",
	} {
		if _, exists := config.Env[forbidden]; exists {
			t.Fatalf("resident Message runtime leaked %s into provider environment", forbidden)
		}
	}
	if config.Env["MULTICA_WORKSPACE_ID"] != workspaceID || config.Env["MULTICA_AGENT_ID"] != agentID {
		t.Fatalf("resident Message runtime identity environment = %#v", config.Env)
	}
	if config.Env["MESSAGE_RUNTIME_SETTING"] != "enabled" {
		t.Fatalf("resident Message runtime omitted agent custom environment: %#v", config.Env)
	}

	if err := d.ensureResidentMessageRuntime(context.Background(), agentID, runtimeID); err != nil {
		t.Fatalf("reuse resident Message runtime: %v", err)
	}
	created, _ = probe.counts()
	if created != 1 {
		t.Fatalf("resident factory calls after reuse = %d, want 1", created)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 2 {
		t.Fatalf("stable-config and credential requests = %v, want exactly two", requests)
	}
}
