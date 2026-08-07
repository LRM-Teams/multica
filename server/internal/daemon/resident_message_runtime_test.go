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

	"github.com/multica-ai/multica/server/pkg/agent"
	"github.com/multica-ai/multica/server/pkg/protocol"
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
		"MULTICA_SERVER_URL",
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
	if config.ResidentOptions.Cwd != config.Env["MULTICA_AGENT_ROOT"] || config.ResidentOptions.Model != "codex-test" {
		t.Fatalf("resident startup options = %+v", config.ResidentOptions)
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

func TestMessageRecoveryCreatesResidentRuntimeBeforeHandoff(t *testing.T) {
	const (
		workspaceID = "11111111-1111-1111-1111-111111111111"
		runtimeID   = "22222222-2222-2222-2222-222222222222"
		agentID     = "33333333-3333-3333-3333-333333333333"
	)
	expiresAt := time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /api/daemon/runtimes/" + runtimeID + "/agents/" + agentID + "/runtime-config":
			_ = json.NewEncoder(w).Encode(ResidentAgentRuntimeConfig{
				WorkspaceID: workspaceID, RuntimeID: runtimeID, RuntimeStateGeneration: 1,
				Agent: &AgentData{ID: agentID, Name: "message-agent"},
			})
		case "POST /api/daemon/runtimes/" + runtimeID + "/agents/" + agentID + "/credential":
			_ = json.NewEncoder(w).Encode(AgentCredentialResponse{ID: "credential-1", AgentID: agentID, Token: "durable-agent-token", ExpiresAt: &expiresAt})
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	client := NewClient(upstream.URL)
	client.SetToken("daemon-token")
	done := make(chan error, 1)
	done <- nil
	close(done)
	d := &Daemon{
		cfg:    Config{WorkspacesRoot: t.TempDir(), Agents: map[string]AgentEntry{"codex": {Path: "/usr/bin/true"}}},
		client: client, logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		runtimeIndex:      map[string]Runtime{runtimeID: {ID: runtimeID, WorkspaceID: workspaceID, Provider: "codex"}},
		agentVersions:     make(map[string]string),
		canonicalRuntimes: newCanonicalAgentRuntimePool(),
		canonicalResidentFactoryOverride: func(agent.Config) (agent.Backend, func(), error) {
			return &blockingResidentMessageRuntime{done: done}, func() {}, nil
		},
		messageCoordinators: make(map[string]*MessageCoordinator),
		messageRuntimeIDs:   map[string]string{agentID: runtimeID},
	}
	coordinator, err := NewMessageCoordinator(t.TempDir(), func(ctx context.Context, messages []protocol.AgentMessageProjection) error {
		return d.canonicalRuntimes.handoffIdleMessages(ctx, agentID, runtimeID, messages, nil, nil, nil, nil)
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	d.messageCoordinators[agentID] = coordinator
	request := coordinator.BeginRecovery(agentID, 100)
	err = d.handleMessageRecoveryPageWithSend(context.Background(), protocol.AgentRecoveryPage{
		AgentID: agentID, RecoveryID: request.RecoveryID, SnapshotID: "snapshot-1", HighWatermark: "snapshot-1",
		Messages: []protocol.AgentMessageProjection{{ID: "message-1", Target: "dm:user-1", Seq: 1, Content: "hello"}},
	}, func(protocol.AgentRecoveryRequest) error { return nil })
	if err != nil {
		t.Fatalf("recover resident Message: %v", err)
	}
	if !d.canonicalRuntimes.hasResidentBackend(agentID, runtimeID) {
		t.Fatal("recovery did not create the resident runtime")
	}
}
