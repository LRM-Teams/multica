package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/turntransport"
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
	if err := d.ensureResidentMessageRuntime(context.Background(), agentID, runtimeID, nil); err != nil {
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
		AgentProxyTokenFileEnv,
		"MULTICA_AGENT_ACTIVE_CAPABILITIES",
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
	wrapperDir := strings.Split(config.Env["PATH"], string(os.PathListSeparator))[0]
	wrapperPath := filepath.Join(wrapperDir, turntransport.CliWrapperFilename())
	wrapper, err := os.ReadFile(wrapperPath)
	if err != nil {
		t.Fatalf("read resident Agent Proxy wrapper: %v", err)
	}
	for _, expected := range []string{"MULTICA_AGENT_ID", "MULTICA_WORKSPACE_ID", AgentProxyURLEnv, AgentProxyTokenFileEnv} {
		if !strings.Contains(string(wrapper), expected) {
			t.Fatalf("resident Agent Proxy wrapper omitted %q: %s", expected, wrapper)
		}
	}
	if strings.Contains(string(wrapper), "MULTICA_AGENT_ACTIVE_CAPABILITIES") {
		t.Fatal("resident Agent Proxy wrapper prematurely restricted the existing CLI command surface")
	}
	if config.ResidentOptions.Cwd != config.Env["MULTICA_AGENT_ROOT"] || config.ResidentOptions.Model != "codex-test" {
		t.Fatalf("resident startup options = %+v", config.ResidentOptions)
	}

	if err := d.ensureResidentMessageRuntime(context.Background(), agentID, runtimeID, nil); err != nil {
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
	if err := d.canonicalRuntimes.closeAll(); err != nil {
		t.Fatalf("close resident runtimes: %v", err)
	}
	if _, err := os.Stat(wrapperDir); !os.IsNotExist(err) {
		t.Fatalf("resident Agent Proxy transport survived backend cleanup: %v", err)
	}
}

func TestEnsureResidentMessageRuntimeRotatesPiSessionBetweenRuns(t *testing.T) {
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
	var (
		factoryMu sync.Mutex
		backends  []agent.PiRPCBackend
		closed    int
	)
	d := &Daemon{
		cfg: Config{
			WorkspacesRoot: t.TempDir(),
			HealthPort:     19514,
			Agents:         map[string]AgentEntry{"pi": {Path: "/usr/bin/true"}},
		},
		client:            client,
		logger:            slog.New(slog.NewTextHandler(io.Discard, nil)),
		runtimeIndex:      map[string]Runtime{runtimeID: {ID: runtimeID, WorkspaceID: workspaceID, Provider: "pi"}},
		agentVersions:     make(map[string]string),
		canonicalRuntimes: newCanonicalAgentRuntimePool(),
		canonicalResidentFactoryOverride: func(config agent.Config) (agent.Backend, func(), error) {
			backend := agent.NewPiRPCBackend(config)
			factoryMu.Lock()
			backends = append(backends, backend)
			factoryMu.Unlock()
			return backend, func() {
				backend.Close()
				factoryMu.Lock()
				closed++
				factoryMu.Unlock()
			}, nil
		},
	}

	firstIdentity := &agent.PiRunIdentity{RunID: "run-1", RunAgentID: "run-agent-1"}
	if err := d.ensureResidentMessageRuntime(context.Background(), agentID, runtimeID, firstIdentity); err != nil {
		t.Fatalf("create first run resident: %v", err)
	}
	factoryMu.Lock()
	firstBackend := backends[0]
	factoryMu.Unlock()
	firstBinding, err := firstBackend.BindRunIdentity(*firstIdentity)
	if err != nil {
		t.Fatalf("inspect first run binding: %v", err)
	}

	if err := d.ensureResidentMessageRuntime(context.Background(), agentID, runtimeID, firstIdentity); err != nil {
		t.Fatalf("reuse first run resident: %v", err)
	}
	factoryMu.Lock()
	createdAfterReuse := len(backends)
	factoryMu.Unlock()
	if createdAfterReuse != 1 {
		t.Fatalf("same run created %d resident backends, want 1", createdAfterReuse)
	}

	secondIdentity := &agent.PiRunIdentity{RunID: "run-2", RunAgentID: "run-agent-2"}
	if err := d.ensureResidentMessageRuntime(context.Background(), agentID, runtimeID, secondIdentity); err != nil {
		t.Fatalf("create second run resident: %v", err)
	}
	factoryMu.Lock()
	if len(backends) != 2 {
		factoryMu.Unlock()
		t.Fatalf("successive runs created %d resident backends, want 2", len(backends))
	}
	secondBackend := backends[1]
	closedCount := closed
	factoryMu.Unlock()
	secondBinding, err := secondBackend.BindRunIdentity(*secondIdentity)
	if err != nil {
		t.Fatalf("inspect second run binding: %v", err)
	}
	if closedCount != 1 {
		t.Fatalf("prior run resident closes = %d, want 1", closedCount)
	}
	if secondBinding.SessionID == firstBinding.SessionID || secondBinding.CaptureBoundary == firstBinding.CaptureBoundary {
		t.Fatalf("successive run-agents reused Pi identity: first=%+v second=%+v", firstBinding, secondBinding)
	}
}

func TestMessageRecoveryMergesTerminalSnapshotBeforeCreatingResidentRuntime(t *testing.T) {
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
	readyAtFactory := make(chan bool, 1)
	var coordinator *MessageCoordinator
	d := &Daemon{
		cfg:    Config{WorkspacesRoot: t.TempDir(), HealthPort: 19514, Agents: map[string]AgentEntry{"codex": {Path: "/usr/bin/true"}}},
		client: client, logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		runtimeIndex:      map[string]Runtime{runtimeID: {ID: runtimeID, WorkspaceID: workspaceID, Provider: "codex"}},
		agentVersions:     make(map[string]string),
		canonicalRuntimes: newCanonicalAgentRuntimePool(),
		canonicalResidentFactoryOverride: func(agent.Config) (agent.Backend, func(), error) {
			readyAtFactory <- coordinator.FreshnessKnown()
			return &blockingResidentMessageRuntime{done: done}, func() {}, nil
		},
	}
	coordinator, err := newTestMessageCoordinator(t, t.TempDir(), func(ctx context.Context, messages []protocol.AgentMessageProjection) error {
		return d.canonicalRuntimes.handoffIdleMessages(ctx, agentID, runtimeID, messages, nil, nil, nil, nil)
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	runner := registerTestInbox(t, d, InboxKey{WorkspaceID: workspaceID, AgentID: agentID}, runtimeID, coordinator)
	request := coordinator.BeginRecovery(agentID, 100)
	err = runner.mergeMessageRecoveryPage(protocol.AgentRecoveryPage{
		AgentID: agentID, RecoveryID: request.RecoveryID, SnapshotID: "snapshot-1", HighWatermark: "snapshot-1",
		Messages: []protocol.AgentMessageProjection{{ID: "message-1", Target: "dm:user-1", Seq: 1, Content: "hello"}},
	}, func(protocol.AgentRecoveryRequest) error { return nil })
	if err != nil {
		t.Fatalf("recover resident Message: %v", err)
	}
	if !coordinator.FreshnessKnown() {
		t.Fatal("terminal snapshot was not ready when recovery handler returned")
	}
	select {
	case ready := <-readyAtFactory:
		if !ready {
			t.Fatal("resident Runtime creation started before terminal snapshot merge")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("recovery did not create the resident Runtime")
	}
	deadline := time.Now().Add(5 * time.Second)
	for !d.canonicalRuntimes.hasResidentBackend(agentID, runtimeID) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !d.canonicalRuntimes.hasResidentBackend(agentID, runtimeID) {
		t.Fatal("recovery did not create the resident runtime")
	}
	done <- nil
	close(done)
}

func TestMessageRecoveryCompletesWithoutResidentRuntime(t *testing.T) {
	const agentID = "33333333-3333-3333-3333-333333333333"
	const workspaceID = "11111111-1111-1111-1111-111111111111"
	d := &Daemon{
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	coordinator, err := newTestMessageCoordinator(t, t.TempDir(), func(context.Context, []protocol.AgentMessageProjection) error {
		return errors.New("resident Runtime is unavailable")
	}, nil)
	if err != nil {
		t.Fatalf("NewMessageCoordinator: %v", err)
	}
	runner := registerTestInbox(t, d, InboxKey{WorkspaceID: workspaceID, AgentID: agentID}, "missing-runtime", coordinator)
	request := coordinator.BeginRecovery(agentID, 100)
	err = runner.mergeMessageRecoveryPage(protocol.AgentRecoveryPage{
		AgentID: agentID, RecoveryID: request.RecoveryID, SnapshotID: "snapshot-1", HighWatermark: "snapshot-1",
		Messages: []protocol.AgentMessageProjection{{ID: "message-1", Target: "dm:user-1", Seq: 1, Content: "hello"}},
	}, func(protocol.AgentRecoveryRequest) error { return nil })
	if err != nil {
		t.Fatalf("terminal recovery without Runtime: %v", err)
	}
	if !coordinator.FreshnessKnown() {
		t.Fatal("missing resident Runtime prevented terminal recovery readiness")
	}
	if got := coordinator.Boundaries()["dm:user-1"]; got != 0 {
		t.Fatalf("missing Runtime advanced Context Boundary to %d", got)
	}
}

func TestResidentMessageRuntimeReportsMixedRunTurnCaptureAndToolLifecycle(t *testing.T) {
	const agentID, runtimeID = "agent-1", "runtime-1"
	backend := &activityResidentMessageRuntime{done: make(chan error, 1), messages: make(chan agent.Message, 2)}
	pool := newCanonicalAgentRuntimePool()
	pool.slots[agentID+"\x00"+runtimeID] = &canonicalAgentRuntimeSlot{mode: canonicalRuntimeResident, backend: backend}
	reports := make(chan protocol.MixedRunActivityTransitionPayload, 8)
	d := &Daemon{
		canonicalRuntimes: pool,
		runtimeIndex:      map[string]Runtime{runtimeID: {ID: runtimeID, WorkspaceID: "workspace-1"}},
		mixedRunActivityReporter: func(payload protocol.MixedRunActivityTransitionPayload) bool {
			reports <- payload
			return true
		},
	}
	messages := []protocol.AgentMessageProjection{{
		ID: "message-1", Target: "channel:one", Seq: 1, Content: "hello",
		RunID: "run-1", RunAgentID: "run-agent-1", DeliveryID: "delivery-1",
	}}
	if err := d.handoffIdleMessageBatch(context.Background(), agentID, runtimeID, messages); err != nil {
		t.Fatalf("handoff mixed-run message: %v", err)
	}
	backend.messages <- agent.Message{Type: agent.MessageToolUse, Tool: "bash", CallID: "call-1"}
	backend.messages <- agent.Message{Type: agent.MessageToolResult, Tool: "bash", CallID: "call-1"}
	close(backend.messages)
	backend.done <- nil
	close(backend.done)

	counts := map[string]int{}
	deadline := time.After(2 * time.Second)
	// Without a daemon client/credential, the capture cannot be acknowledged.
	// The unfinished-capture counter must therefore remain open rather than
	// being decremented merely because the local runtime finished.
	for len(counts) < 5 {
		select {
		case report := <-reports:
			counts[report.Dimension+fmt.Sprint(report.Delta)]++
		case <-deadline:
			t.Fatalf("timed out waiting for lifecycle transitions: %v", counts)
		}
	}
	for _, key := range []string{
		protocol.MixedRunActivityActiveTurn + "1", protocol.MixedRunActivityActiveTurn + "-1",
		protocol.MixedRunActivityUnfinishedCaptureBatch + "1",
		protocol.MixedRunActivityInflightTool + "1", protocol.MixedRunActivityInflightTool + "-1",
	} {
		if counts[key] != 1 {
			t.Fatalf("lifecycle transition %s count = %d, all=%v", key, counts[key], counts)
		}
	}
}
