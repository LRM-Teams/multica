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
				WorkspaceID: workspaceID, RuntimeID: runtimeID, WorkspaceContext: "Workspace context",
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
	client.SetRuntimeDaemonToken(runtimeID, "daemon-token", time.Now().Add(time.Hour))
	probe := &canonicalRuntimeFactoryProbe{}
	starter := &residentProcessStartProbe{}
	d := &Daemon{
		cfg: Config{
			ServerBaseURL:  upstream.URL,
			WorkspacesRoot: root,
			HealthPort:     19514,
			Agents: map[string]AgentEntry{
				"codex": {Path: "/usr/bin/true", Model: "codex-test"},
			},
		},
		client:            client,
		logger:            slog.New(slog.NewTextHandler(io.Discard, nil)),
		runtimeIndex:      map[string]Runtime{runtimeID: {ID: runtimeID, WorkspaceID: workspaceID, Provider: "codex"}},
		agentVersions:     make(map[string]string),
		canonicalRuntimes: newCanonicalAgentRuntimePool(),
		canonicalResidentFactoryOverride: func(config agent.Config) (agent.Backend, func(), error) {
			_, closer, err := probe.factory(config)
			if err != nil {
				return nil, nil, err
			}
			return starter, closer, nil
		},
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
	if got := config.Env[AgentProxyCLIWrapperEnv]; got != wrapperPath {
		t.Fatalf("resident Agent Proxy wrapper forward path = %q, want %q", got, wrapperPath)
	}
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

// residentProcessStartProbe is a Factory adapter that records the Raft spawn
// seam: creating the backend is not residency; EnsureResidentProcess is.
type residentProcessStartProbe struct {
	canonicalRuntimeTestBackend
	starts int
	err    error
}

func (p *residentProcessStartProbe) EnsureResidentProcess(context.Context) error {
	p.starts++
	return p.err
}

func (p *residentProcessStartProbe) RuntimeAlive() (bool, bool) {
	return p.err == nil && p.starts > 0, true
}

type busyResidentProcessStartProbe struct {
	residentProcessStartProbe
	done chan error
}

func (p *busyResidentProcessStartProbe) AcceptMessageBatch(context.Context, []agent.ResidentMessage) (agent.ResidentMessageAcceptance, error) {
	messages := make(chan agent.Message)
	close(messages)
	return agent.ResidentMessageAcceptance{Done: p.done, Messages: messages}, nil
}

func TestEnsureResidentMessageRuntimeSpawnFailureRetiresBusyBackend(t *testing.T) {
	const agentID, runtimeID = "agent-1", "runtime-1"
	backend := &busyResidentProcessStartProbe{
		residentProcessStartProbe: residentProcessStartProbe{err: errors.New("codex app-server did not start")},
		done:                      make(chan error, 1),
	}
	pool := newCanonicalAgentRuntimePool()
	pool.slots[agentID+"\x00"+runtimeID] = &canonicalAgentRuntimeSlot{backend: backend}
	d := &Daemon{canonicalRuntimes: pool}

	if err := pool.deliverIdleMessages(context.Background(), agentID, runtimeID, []protocol.AgentMessageProjection{{
		ID: "message-1", Target: "dm:one", Seq: 1, Content: "hello",
	}}, nil, nil, nil, nil); err != nil {
		t.Fatalf("start resident turn: %v", err)
	}
	if err := d.ensureResidentMessageRuntime(context.Background(), agentID, runtimeID, nil); err == nil {
		t.Fatal("spawn failure was accepted while the prior resident turn was active")
	}
	if got := backend.forceKillCount(); got != 1 {
		t.Fatalf("failed resident cleanup force-kill calls = %d, want 1", got)
	}

	backend.done <- errors.New(agent.AgentForceKilledMarker)
	deadline := time.Now().Add(time.Second)
	for pool.hasResidentBackend(agentID, runtimeID) && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if pool.hasResidentBackend(agentID, runtimeID) {
		t.Fatal("failed resident backend remained registered after the active turn drained")
	}
}

func TestEnsureResidentMessageRuntimeSpawnsProviderProcess(t *testing.T) {
	const (
		workspaceID = "11111111-1111-1111-1111-111111111111"
		runtimeID   = "22222222-2222-2222-2222-222222222222"
		agentID     = "33333333-3333-3333-3333-333333333333"
	)
	expiresAt := time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339Nano)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /api/daemon/runtimes/" + runtimeID + "/agents/" + agentID + "/runtime-config":
			_ = json.NewEncoder(w).Encode(ResidentAgentRuntimeConfig{
				WorkspaceID: workspaceID, RuntimeID: runtimeID,
				Agent: &AgentData{ID: agentID, Name: "message-agent"},
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

	starter := &residentProcessStartProbe{}
	client := NewClient(upstream.URL)
	client.SetRuntimeDaemonToken(runtimeID, "daemon-token", time.Now().Add(time.Hour))
	d := &Daemon{
		cfg: Config{
			ServerBaseURL:  upstream.URL,
			WorkspacesRoot: t.TempDir(),
			HealthPort:     19514,
			Agents:         map[string]AgentEntry{"codex": {Path: "/usr/bin/true", Model: "codex-test"}},
		},
		client:            client,
		logger:            slog.New(slog.NewTextHandler(io.Discard, nil)),
		runtimeIndex:      map[string]Runtime{runtimeID: {ID: runtimeID, WorkspaceID: workspaceID, Provider: "codex"}},
		canonicalRuntimes: newCanonicalAgentRuntimePool(),
		canonicalResidentFactoryOverride: func(agent.Config) (agent.Backend, func(), error) {
			return starter, func() {}, nil
		},
	}
	if err := d.ensureResidentMessageRuntime(context.Background(), agentID, runtimeID, nil); err != nil {
		t.Fatalf("ensureResidentMessageRuntime: %v", err)
	}
	if starter.starts != 1 {
		t.Fatalf("provider process starts = %d, want 1 (Factory is not spawn)", starter.starts)
	}
	if err := d.ensureResidentMessageRuntime(context.Background(), agentID, runtimeID, nil); err != nil {
		t.Fatalf("reuse resident Message runtime: %v", err)
	}
	if starter.starts != 1 {
		t.Fatalf("provider process starts after reuse = %d, want still 1", starter.starts)
	}
}

func TestEnsureResidentMessageRuntimeResumesStoredProviderSessionDuringPrestart(t *testing.T) {
	const (
		workspaceID = "11111111-1111-1111-1111-111111111111"
		runtimeID   = "22222222-2222-2222-2222-222222222222"
		agentID     = "33333333-3333-3333-3333-333333333333"
		sessionID   = "provider-session-before-restart"
	)
	var captured agent.Config
	d := newResidentStartTestDaemon(t, workspaceID, runtimeID, agentID, func(config agent.Config) (agent.Backend, func(), error) {
		captured = config
		return &residentProcessStartProbe{}, func() {}, nil
	})
	sessions := newAgentRuntimeSessionStore(d.cfg.WorkspacesRoot)
	if err := sessions.Put(agentID, runtimeID, sessionID); err != nil {
		t.Fatal(err)
	}
	d.agentRuntimeSessions = sessions

	if err := d.ensureResidentMessageRuntime(context.Background(), agentID, runtimeID, nil); err != nil {
		t.Fatalf("ensure resident Message runtime: %v", err)
	}
	if captured.ResidentOptions.ResumeSessionID != sessionID {
		t.Fatalf("provider prestart ResumeSessionID = %q, want %q", captured.ResidentOptions.ResumeSessionID, sessionID)
	}
}

func TestEnsureResidentMessageRuntimeSpawnFailureDoesNotKeepBackend(t *testing.T) {
	const (
		workspaceID = "11111111-1111-1111-1111-111111111111"
		runtimeID   = "22222222-2222-2222-2222-222222222222"
		agentID     = "33333333-3333-3333-3333-333333333333"
	)
	expiresAt := time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339Nano)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /api/daemon/runtimes/" + runtimeID + "/agents/" + agentID + "/runtime-config":
			_ = json.NewEncoder(w).Encode(ResidentAgentRuntimeConfig{
				WorkspaceID: workspaceID, RuntimeID: runtimeID,
				Agent: &AgentData{ID: agentID, Name: "message-agent"},
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

	starter := &residentProcessStartProbe{err: errors.New("codex app-server did not start")}
	client := NewClient(upstream.URL)
	client.SetRuntimeDaemonToken(runtimeID, "daemon-token", time.Now().Add(time.Hour))
	d := &Daemon{
		cfg: Config{
			ServerBaseURL:  upstream.URL,
			WorkspacesRoot: t.TempDir(),
			HealthPort:     19514,
			Agents:         map[string]AgentEntry{"codex": {Path: "/usr/bin/true", Model: "codex-test"}},
		},
		client:            client,
		logger:            slog.New(slog.NewTextHandler(io.Discard, nil)),
		runtimeIndex:      map[string]Runtime{runtimeID: {ID: runtimeID, WorkspaceID: workspaceID, Provider: "codex"}},
		canonicalRuntimes: newCanonicalAgentRuntimePool(),
		canonicalResidentFactoryOverride: func(agent.Config) (agent.Backend, func(), error) {
			return starter, func() {}, nil
		},
	}
	if err := d.ensureResidentMessageRuntime(context.Background(), agentID, runtimeID, nil); err == nil {
		t.Fatal("spawn failure was accepted as residency")
	}
	if d.canonicalRuntimes.hasResidentBackend(agentID, runtimeID) {
		t.Fatal("failed spawn left a resident backend (would report fake active)")
	}
}

func TestEnsureResidentMessageRuntimeNonStarterDoesNotKeepBackend(t *testing.T) {
	const (
		workspaceID = "11111111-1111-1111-1111-111111111111"
		runtimeID   = "22222222-2222-2222-2222-222222222222"
		agentID     = "33333333-3333-3333-3333-333333333333"
	)
	expiresAt := time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339Nano)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /api/daemon/runtimes/" + runtimeID + "/agents/" + agentID + "/runtime-config":
			_ = json.NewEncoder(w).Encode(ResidentAgentRuntimeConfig{
				WorkspaceID: workspaceID, RuntimeID: runtimeID,
				Agent: &AgentData{ID: agentID, Name: "message-agent"},
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

	client := NewClient(upstream.URL)
	client.SetRuntimeDaemonToken(runtimeID, "daemon-token", time.Now().Add(time.Hour))
	d := &Daemon{
		cfg: Config{
			ServerBaseURL:  upstream.URL,
			WorkspacesRoot: t.TempDir(),
			HealthPort:     19514,
			Agents:         map[string]AgentEntry{"codex": {Path: "/usr/bin/true", Model: "codex-test"}},
		},
		client:            client,
		logger:            slog.New(slog.NewTextHandler(io.Discard, nil)),
		runtimeIndex:      map[string]Runtime{runtimeID: {ID: runtimeID, WorkspaceID: workspaceID, Provider: "codex"}},
		canonicalRuntimes: newCanonicalAgentRuntimePool(),
		canonicalResidentFactoryOverride: func(agent.Config) (agent.Backend, func(), error) {
			return &canonicalRuntimeTestBackend{}, func() {}, nil
		},
	}
	if err := d.ensureResidentMessageRuntime(context.Background(), agentID, runtimeID, nil); err == nil {
		t.Fatal("non-starter backend was accepted as residency")
	}
	if d.canonicalRuntimes.hasResidentBackend(agentID, runtimeID) {
		t.Fatal("non-starter left a resident backend")
	}
}

func TestWorkspaceRunnerStartFailsClosedWhenResidentCannotStart(t *testing.T) {
	const (
		workspaceID = "11111111-1111-1111-1111-111111111111"
		runtimeID   = "22222222-2222-2222-2222-222222222222"
		agentID     = "33333333-3333-3333-3333-333333333333"
	)
	cases := []struct {
		name    string
		factory canonicalRuntimeBackendFactory
	}{
		{
			name: "non-starter",
			factory: func(agent.Config) (agent.Backend, func(), error) {
				return &canonicalRuntimeTestBackend{}, func() {}, nil
			},
		},
		{
			name: "starter error",
			factory: func(agent.Config) (agent.Backend, func(), error) {
				return &residentProcessStartProbe{err: errors.New("codex app-server did not start")}, func() {}, nil
			},
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			d := newResidentStartTestDaemon(t, workspaceID, runtimeID, agentID, test.factory)
			runner, _ := attachTestWorkspaceRunner(t, d, workspaceID, nil)
			var activities []protocol.AgentActivityPayload
			runner.activity.AttachTransport(func(payload protocol.AgentActivityPayload) { activities = append(activities, payload) })
			_, status, _, err := runner.startManagedAgent(context.Background(), protocol.WorkspaceRunnerAgentStartPayload{
				AgentID: agentID, RuntimeID: runtimeID, LaunchID: "launch-1", StartDispatchID: "dispatch-1",
			})
			if err == nil {
				t.Fatal("managed start succeeded without a resident process")
			}
			if status.Status != protocol.AgentStatusInactive {
				t.Fatalf("status = %+v, want inactive", status)
			}
			for _, payload := range activities {
				if payload.Snapshot.ActivityKind == protocol.ActivityKindWorking && payload.Snapshot.DetailKind == "starting" {
					t.Fatalf("Starting Activity after failed start: %+v", payload)
				}
			}
			if d.canonicalRuntimes.hasResidentBackend(agentID, runtimeID) {
				t.Fatal("failed start left a resident backend")
			}
			if _, found := runner.processes.Snapshot(agentID); found {
				t.Fatal("failed start left an APM launch Running")
			}
		})
	}
}

func TestWorkspaceRunnerStartBecomesActiveAfterResidentProcess(t *testing.T) {
	const (
		workspaceID = "11111111-1111-1111-1111-111111111111"
		runtimeID   = "22222222-2222-2222-2222-222222222222"
		agentID     = "33333333-3333-3333-3333-333333333333"
	)
	starter := &residentProcessStartProbe{}
	d := newResidentStartTestDaemon(t, workspaceID, runtimeID, agentID, func(agent.Config) (agent.Backend, func(), error) {
		return starter, func() {}, nil
	})
	runner, _ := attachTestWorkspaceRunner(t, d, workspaceID, nil)
	var activities []protocol.AgentActivityPayload
	runner.activity.AttachTransport(func(payload protocol.AgentActivityPayload) { activities = append(activities, payload) })
	_, status, _, err := runner.startManagedAgent(context.Background(), protocol.WorkspaceRunnerAgentStartPayload{
		AgentID: agentID, RuntimeID: runtimeID, LaunchID: "launch-1", StartDispatchID: "dispatch-1",
	})
	if err != nil {
		t.Fatalf("managed start: %v", err)
	}
	if status.Status != protocol.AgentStatusActive {
		t.Fatalf("status = %+v, want active", status)
	}
	if starter.starts != 1 {
		t.Fatalf("provider process starts = %d, want 1", starter.starts)
	}
	launch, ok := runner.processes.Snapshot(agentID)
	if !ok || launch.QueueState != protocol.AgentStartQueueRunning || launch.ProcessInstanceID == "" {
		t.Fatalf("launch after spawn = %+v managed=%v, want running with process", launch, ok)
	}
	starting := 0
	for _, payload := range activities {
		if payload.Snapshot.ActivityKind == protocol.ActivityKindWorking && payload.Snapshot.DetailKind == "starting" {
			starting++
		}
	}
	if starting != 1 {
		t.Fatalf("Starting Activity = %d, want 1 after admission", starting)
	}
	last := activities[len(activities)-1].Snapshot
	if last.ActivityKind != protocol.ActivityKindOnline || last.DetailKind != "idle" {
		t.Fatalf("Activity after resident readiness = %q/%q, want online/idle", last.ActivityKind, last.DetailKind)
	}
}

func TestWorkspaceRunnerStartUsesExplicitProviderSession(t *testing.T) {
	const (
		workspaceID = "11111111-1111-1111-1111-111111111111"
		runtimeID   = "22222222-2222-2222-2222-222222222222"
		agentID     = "33333333-3333-3333-3333-333333333333"
	)
	for _, test := range []struct {
		name      string
		sessionID string
	}{
		{name: "resume", sessionID: "provider-session-before-restart"},
		{name: "fresh", sessionID: ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			var captured agent.Config
			d := newResidentStartTestDaemon(t, workspaceID, runtimeID, agentID, func(config agent.Config) (agent.Backend, func(), error) {
				captured = config
				return &residentProcessStartProbe{}, func() {}, nil
			})
			sessions := newAgentRuntimeSessionStore(d.cfg.WorkspacesRoot)
			if err := sessions.Put(agentID, runtimeID, "stale-provider-session"); err != nil {
				t.Fatal(err)
			}
			d.agentRuntimeSessions = sessions
			runner, _ := attachTestWorkspaceRunner(t, d, workspaceID, nil)

			_, status, _, err := runner.startManagedAgent(context.Background(), protocol.WorkspaceRunnerAgentStartPayload{
				AgentID: agentID, RuntimeID: runtimeID, LaunchID: "launch-1", StartDispatchID: "dispatch-1",
				Config: protocol.WorkspaceRunnerAgentStartConfig{SessionID: test.sessionID},
			})
			if err != nil {
				t.Fatalf("managed start: %v", err)
			}
			if status.Status != protocol.AgentStatusActive {
				t.Fatalf("status = %+v, want active", status)
			}
			if captured.ResidentOptions.ResumeSessionID != test.sessionID {
				t.Fatalf("provider ResumeSessionID = %q, want %q", captured.ResidentOptions.ResumeSessionID, test.sessionID)
			}
		})
	}
}

func newResidentStartTestDaemon(t *testing.T, workspaceID, runtimeID, agentID string, factory canonicalRuntimeBackendFactory) *Daemon {
	t.Helper()
	expiresAt := time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339Nano)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /api/daemon/runtimes/" + runtimeID + "/agents/" + agentID + "/runtime-config":
			_ = json.NewEncoder(w).Encode(ResidentAgentRuntimeConfig{
				WorkspaceID: workspaceID, RuntimeID: runtimeID,
				Agent: &AgentData{ID: agentID, Name: "message-agent"},
			})
		case "POST /api/daemon/runtimes/" + runtimeID + "/agents/" + agentID + "/credential":
			_ = json.NewEncoder(w).Encode(AgentCredentialResponse{
				ID: "credential-1", AgentID: agentID, Prefix: "mat_test", Token: "durable-agent-token", ExpiresAt: &expiresAt,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(upstream.Close)
	client := NewClient(upstream.URL)
	client.SetRuntimeDaemonToken(runtimeID, "daemon-token", time.Now().Add(time.Hour))
	workspacesRoot := t.TempDir()
	return &Daemon{
		cfg: Config{
			ServerBaseURL:  upstream.URL,
			WorkspacesRoot: workspacesRoot,
			HealthPort:     19514,
			Agents:         map[string]AgentEntry{"codex": {Path: "/usr/bin/true", Model: "codex-test"}},
		},
		client:                           client,
		logger:                           slog.New(slog.NewTextHandler(io.Discard, nil)),
		runtimeIndex:                     map[string]Runtime{runtimeID: {ID: runtimeID, WorkspaceID: workspaceID, Provider: "codex"}},
		canonicalRuntimes:                newCanonicalAgentRuntimePool(),
		canonicalResidentFactoryOverride: factory,
		agentRuntimeSessions:             newAgentRuntimeSessionStore(workspacesRoot),
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
				WorkspaceID: workspaceID, RuntimeID: runtimeID,
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
	client.SetRuntimeDaemonToken(runtimeID, "daemon-token", time.Now().Add(time.Hour))
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

func TestResidentMessageRuntimeReportsMixedRunTurnCaptureAndToolLifecycle(t *testing.T) {
	const agentID, runtimeID = "agent-1", "runtime-1"
	backend := &activityResidentMessageRuntime{done: make(chan error, 1), messages: make(chan agent.Message, 2)}
	pool := newCanonicalAgentRuntimePool()
	pool.slots[agentID+"\x00"+runtimeID] = &canonicalAgentRuntimeSlot{backend: backend}
	reports := make(chan protocol.MixedRunActivityTransitionPayload, 8)
	d := &Daemon{
		cfg:               Config{WorkspacesRoot: t.TempDir()},
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
	if err := d.deliverIdleMessageBatch(context.Background(), agentID, runtimeID, messages); err != nil {
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
