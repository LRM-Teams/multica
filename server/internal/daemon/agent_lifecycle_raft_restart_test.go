package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/daemon/execenv"
	"github.com/multica-ai/multica/server/pkg/agent"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// recordingLifecycleStarter is a narrow I/O stand-in for "start the agent
// again". It records the session the composer handed to the next start so
// tests can observe isolation without a live provider CLI.
type recordingLifecycleStarter struct {
	mu    sync.Mutex
	calls []agentLifecycleStartRequest
	err   error
}

func (s *recordingLifecycleStarter) Start(_ context.Context, req agentLifecycleStartRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, req)
	return s.err
}

func (s *recordingLifecycleStarter) lastSession() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.calls) == 0 {
		return "<no-start>"
	}
	return s.calls[len(s.calls)-1].SessionID
}

func (s *recordingLifecycleStarter) startCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.calls)
}

type raftRestartFixture struct {
	root        string
	workspaceID string
	agentID     string
	runtimeID   string
	memoryPath  string
	sessionID   string
	sessions    *agentRuntimeSessionStore
	starter     *recordingLifecycleStarter
	pool        *canonicalAgentRuntimePool
	probe       *canonicalRuntimeFactoryProbe
	lease       *canonicalAgentRuntimeLease
	backend     *canonicalRuntimeTestBackend
	executor    *agentLifecycleExecutor
}

func newRaftRestartFixture(t *testing.T) *raftRestartFixture {
	t.Helper()
	root := t.TempDir()
	fx := &raftRestartFixture{
		root:        root,
		workspaceID: uuid.NewString(),
		agentID:     uuid.NewString(),
		runtimeID:   uuid.NewString(),
		sessionID:   "provider-session-keep-me",
		sessions:    newAgentRuntimeSessionStore(root),
		starter:     &recordingLifecycleStarter{},
		pool:        newCanonicalAgentRuntimePool(),
		probe:       &canonicalRuntimeFactoryProbe{},
	}
	layout, err := execenv.ProvisionAgentWorkspace(root, fx.workspaceID, fx.agentID, nil)
	if err != nil {
		t.Fatalf("provision workspace: %v", err)
	}
	fx.memoryPath = filepath.Join(layout.AgentRoot, "MEMORY.md")
	if err := os.WriteFile(fx.memoryPath, []byte("keep me"), 0o600); err != nil {
		t.Fatalf("write memory fixture: %v", err)
	}
	if err := fx.sessions.Put(fx.agentID, fx.runtimeID, fx.sessionID); err != nil {
		t.Fatalf("seed session identity: %v", err)
	}
	fx.executor = &agentLifecycleExecutor{
		workspacesRoot: root,
		runtimes:       fx.pool,
		sessions:       fx.sessions,
		starter:        fx.starter,
	}
	return fx
}

func (fx *raftRestartFixture) request(kind agentLifecycleActionKind) agentLifecycleExecutionRequest {
	return agentLifecycleExecutionRequest{
		OperationID: uuid.NewString(),
		WorkspaceID: fx.workspaceID,
		AgentID:     fx.agentID,
		RuntimeID:   fx.runtimeID,
		ActionKind:  kind,
	}
}

func (fx *raftRestartFixture) acquireBusy(t *testing.T) {
	t.Helper()
	stable, _, err := splitAgentProcessEnvironment(map[string]string{
		"MULTICA_SERVER_URL":   "https://multica.example",
		"MULTICA_WORKSPACE_ID": fx.workspaceID,
		"MULTICA_AGENT_ID":     fx.agentID,
		"MULTICA_TASK_ID":      "turn-a",
	})
	if err != nil {
		t.Fatalf("splitAgentProcessEnvironment: %v", err)
	}
	identity, err := newCanonicalAgentRuntimeIdentity(canonicalAgentRuntimeIdentityParams{
		AgentID:     fx.agentID,
		RuntimeID:   fx.runtimeID,
		Provider:    "pi",
		Executable:  "/usr/local/bin/pi",
		Model:       "model-a",
		WorkDir:     "/var/lib/multica/agent-a/workspace",
		Environment: stable,
		WorkspaceID: fx.workspaceID,
	})
	if err != nil {
		t.Fatalf("newCanonicalAgentRuntimeIdentity: %v", err)
	}
	lease, err := fx.pool.acquire(canonicalAgentRuntimeAcquireRequest{
		Identity:           identity,
		CanonicalSessionID: fx.sessionID,
		Factory:            fx.probe.factory,
	})
	if err != nil {
		t.Fatalf("acquire busy resident: %v", err)
	}
	fx.lease = lease
	fx.backend = fx.probe.backends[0]
}

func (fx *raftRestartFixture) storedSession(t *testing.T) string {
	t.Helper()
	got, err := fx.sessions.Get(fx.agentID, fx.runtimeID)
	if err != nil {
		t.Fatalf("read session identity: %v", err)
	}
	return got
}

func TestRaftRestartModesIsolateSessionAndWorkspace(t *testing.T) {
	t.Run("restart keeps session and workspace then starts", func(t *testing.T) {
		fx := newRaftRestartFixture(t)
		if err := fx.executor.Execute(context.Background(), fx.request(agentLifecycleActionRestart)); err != nil {
			t.Fatalf("restart: %v", err)
		}
		if _, err := os.Stat(fx.memoryPath); err != nil {
			t.Fatalf("restart changed workspace: %v", err)
		}
		if got := fx.storedSession(t); got != fx.sessionID {
			t.Fatalf("restart session = %q, want %q", got, fx.sessionID)
		}
		if fx.starter.startCount() != 1 {
			t.Fatalf("restart start count = %d, want 1", fx.starter.startCount())
		}
		if got := fx.starter.lastSession(); got != fx.sessionID {
			t.Fatalf("restart next start session = %q, want %q", got, fx.sessionID)
		}
	})

	t.Run("reset_session_restart keeps workspace and starts without old session", func(t *testing.T) {
		fx := newRaftRestartFixture(t)
		if err := fx.executor.Execute(context.Background(), fx.request(agentLifecycleActionResetSessionRestart)); err != nil {
			t.Fatalf("reset_session_restart: %v", err)
		}
		if _, err := os.Stat(fx.memoryPath); err != nil {
			t.Fatalf("reset_session_restart changed workspace: %v", err)
		}
		if got := fx.storedSession(t); got != "" {
			t.Fatalf("reset_session_restart left session %q, want empty", got)
		}
		if got := fx.starter.lastSession(); got != "" {
			t.Fatalf("reset_session_restart next start session = %q, want empty", got)
		}
	})

	t.Run("full_reset_restart wipes workspace, reprovisions, and starts fresh", func(t *testing.T) {
		fx := newRaftRestartFixture(t)
		if err := fx.executor.Execute(context.Background(), fx.request(agentLifecycleActionFullResetRestart)); err != nil {
			t.Fatalf("full_reset_restart: %v", err)
		}
		if _, err := os.Stat(fx.memoryPath); !os.IsNotExist(err) {
			t.Fatalf("full_reset_restart retained workspace file: %v", err)
		}
		layout, err := execenv.ResolveAgentWorkspaceLayout(fx.root, fx.workspaceID, fx.agentID)
		if err != nil {
			t.Fatalf("resolve workspace: %v", err)
		}
		if _, err := os.Stat(layout.AgentRoot); err != nil {
			t.Fatalf("full_reset_restart did not reprovision workspace: %v", err)
		}
		if got := fx.storedSession(t); got != "" {
			t.Fatalf("full_reset_restart left session %q, want empty", got)
		}
		if got := fx.starter.lastSession(); got != "" {
			t.Fatalf("full_reset_restart next start session = %q, want empty", got)
		}
	})
}

func TestRaftRestartModesPublishStoppedStartingIdleActivity(t *testing.T) {
	kinds := []agentLifecycleActionKind{
		agentLifecycleActionRestart,
		agentLifecycleActionResetSessionRestart,
		agentLifecycleActionFullResetRestart,
	}
	for _, kind := range kinds {
		t.Run(string(kind), func(t *testing.T) {
			root := t.TempDir()
			workspaceID, agentID, runtimeID := uuid.NewString(), uuid.NewString(), uuid.NewString()
			d := New(Config{WorkspacesRoot: root}, nil)
			d.mu.Lock()
			d.runtimeIndex[runtimeID] = Runtime{ID: runtimeID, WorkspaceID: workspaceID}
			d.mu.Unlock()
			runner, _ := attachTestWorkspaceRunner(t, d, workspaceID, nil)
			runner.ensureResidentRuntime = func(context.Context, string, string, *agent.PiRunIdentity) error { return nil }
			if _, err := execenv.ProvisionAgentWorkspace(root, workspaceID, agentID, nil); err != nil {
				t.Fatalf("provision workspace: %v", err)
			}

			var activities []protocol.AgentActivityPayload
			runner.activity.AttachTransport(func(payload protocol.AgentActivityPayload) {
				activities = append(activities, payload)
			})
			if _, _, _, err := runner.startManagedAgent(context.Background(), protocol.WorkspaceRunnerAgentStartPayload{
				AgentID: agentID, RuntimeID: runtimeID, LaunchID: uuid.NewString(), StartDispatchID: uuid.NewString(),
			}); err != nil {
				t.Fatalf("seed managed launch: %v", err)
			}
			activities = nil
			d.agentLifecycleExecutor.sessionReset = nil
			d.agentLifecycleExecutor.starter = &recordingLifecycleStarter{}

			if err := d.agentLifecycleExecutor.Execute(context.Background(), agentLifecycleExecutionRequest{
				OperationID: uuid.NewString(), WorkspaceID: workspaceID, AgentID: agentID,
				RuntimeID: runtimeID, ActionKind: kind,
			}); err != nil {
				t.Fatalf("%s: %v", kind, err)
			}

			want := []struct{ activity, detail string }{
				{protocol.ActivityKindOffline, "stopped"},
				{protocol.ActivityKindWorking, "starting"},
				{protocol.ActivityKindOnline, "idle"},
			}
			if len(activities) != len(want) {
				t.Fatalf("%s Activity count = %d, want %d: %+v", kind, len(activities), len(want), activities)
			}
			for i, expected := range want {
				got := activities[i].Snapshot
				if got.ActivityKind != expected.activity || got.DetailKind != expected.detail {
					t.Fatalf("%s Activity[%d] = %s/%s, want %s/%s", kind, i, got.ActivityKind, got.DetailKind, expected.activity, expected.detail)
				}
			}
		})
	}
}

func TestRaftRestartModesForceInterruptBusyTurn(t *testing.T) {
	kinds := []agentLifecycleActionKind{
		agentLifecycleActionRestart,
		agentLifecycleActionResetSessionRestart,
		agentLifecycleActionFullResetRestart,
	}
	for _, kind := range kinds {
		t.Run(string(kind), func(t *testing.T) {
			fx := newRaftRestartFixture(t)
			fx.acquireBusy(t)

			done := make(chan error, 1)
			go func() {
				done <- fx.executor.Execute(context.Background(), fx.request(kind))
			}()

			deadline := time.Now().Add(2 * time.Second)
			for fx.backend.forceKillCount() == 0 {
				if time.Now().After(deadline) {
					t.Fatal("ForceKill was never called")
				}
				time.Sleep(5 * time.Millisecond)
			}

			if kind == agentLifecycleActionFullResetRestart {
				if _, err := os.Stat(fx.memoryPath); err != nil {
					t.Fatalf("full_reset deleted workspace while lease still held: %v", err)
				}
			}

			fx.lease.release(false)
			var execErr error
			select {
			case execErr = <-done:
			case <-time.After(2 * time.Second):
				t.Fatalf("%s did not finish after lease release", kind)
			}
			if execErr != nil {
				t.Fatalf("%s on busy turn: %v", kind, execErr)
			}
			if fx.backend.forceKillCount() < 1 {
				t.Fatalf("%s ForceKill count = %d, want >= 1", kind, fx.backend.forceKillCount())
			}
			if fx.pool.hasLiveLease(fx.agentID, fx.runtimeID) {
				t.Fatalf("%s left the provider lease held", kind)
			}
			if fx.starter.startCount() != 1 {
				t.Fatalf("%s start count = %d, want 1 (start after stop, not rebind)", kind, fx.starter.startCount())
			}
			if kind == agentLifecycleActionFullResetRestart {
				if _, err := os.Stat(fx.memoryPath); !os.IsNotExist(err) {
					t.Fatalf("full_reset did not delete workspace after stop+release: %v", err)
				}
			}
		})
	}
}

func TestLifecycleForceRestartFencesInterruptedTurnCompletion(t *testing.T) {
	fx := newRaftRestartFixture(t)
	fx.acquireBusy(t)

	const interruptedGeneration = uint64(17)
	key := fx.agentID + "\x00" + fx.runtimeID
	fx.pool.mu.Lock()
	slot := fx.pool.slots[key]
	if slot != nil {
		slot.mu.Lock()
	}
	fx.pool.mu.Unlock()
	if slot == nil {
		t.Fatal("busy runtime slot missing")
	}
	slot.messageInputGeneration = interruptedGeneration
	slot.messageInputDone = make(chan error)
	slot.mu.Unlock()

	done := make(chan error, 1)
	go func() {
		done <- fx.executor.Execute(context.Background(), fx.request(agentLifecycleActionResetSessionRestart))
	}()

	deadline := time.Now().Add(2 * time.Second)
	for fx.backend.forceKillCount() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("ForceKill was never called")
		}
		time.Sleep(5 * time.Millisecond)
	}
	fx.lease.release(false)
	if err := <-done; err != nil {
		t.Fatalf("reset_session_restart: %v", err)
	}
	slot.mu.Lock()
	slot.messageInputDone = nil
	slot.mu.Unlock()

	published := false
	if fx.pool.publishIfMessageTurnStillIdle(fx.agentID, fx.runtimeID, interruptedGeneration, func() {
		published = true
	}) || published {
		t.Fatal("interrupted old turn published a terminal failure after replacement start")
	}
}

func TestProductionStartAppliesComposerSessionToAcquire(t *testing.T) {
	fx := newRaftRestartFixture(t)
	var lastResume string
	fx.executor.starter = agentLifecycleResumeStarter{
		runtimes: fx.pool,
		sessions: fx.sessions,
		start: func(_ context.Context, req agentLifecycleStartRequest) error {
			identity, err := newCanonicalAgentRuntimeIdentity(canonicalAgentRuntimeIdentityParams{
				AgentID:     req.AgentID,
				RuntimeID:   req.RuntimeID,
				Provider:    "pi",
				Executable:  "/usr/local/bin/pi",
				Model:       "model-a",
				WorkDir:     "/var/lib/multica/agent-a/workspace",
				Environment: map[string]string{"MULTICA_SERVER_URL": "https://multica.example"},
				WorkspaceID: req.WorkspaceID,
			})
			if err != nil {
				return err
			}
			lease, err := fx.pool.acquire(canonicalAgentRuntimeAcquireRequest{
				Identity: identity,
				Factory:  fx.probe.factory,
			})
			if err != nil {
				return err
			}
			if _, err := lease.backend.Execute(context.Background(), "hi", agent.ExecOptions{}); err != nil {
				lease.release(true)
				return err
			}
			lastResume = fx.probe.backends[len(fx.probe.backends)-1].lastResumeSessionID()
			lease.release(true)
			return nil
		},
	}

	if err := fx.executor.Execute(context.Background(), fx.request(agentLifecycleActionRestart)); err != nil {
		t.Fatalf("restart: %v", err)
	}
	if lastResume != fx.sessionID {
		t.Fatalf("production start/acquire resume = %q, want stored session %q", lastResume, fx.sessionID)
	}

	if err := fx.executor.Execute(context.Background(), fx.request(agentLifecycleActionResetSessionRestart)); err != nil {
		t.Fatalf("reset_session_restart: %v", err)
	}
	if lastResume != "" {
		t.Fatalf("production start/acquire after session reset resumed %q, want empty", lastResume)
	}
}

func TestRemoteSessionResetFailureIsNotSwallowed(t *testing.T) {
	fx := newRaftRestartFixture(t)
	fx.executor.sessionReset = &lifecycleResetRecorder{err: errors.New("server prior session still set")}
	err := fx.executor.Execute(context.Background(), fx.request(agentLifecycleActionResetSessionRestart))
	if err == nil {
		t.Fatal("reset_session_restart succeeded while remote session reset failed")
	}
	if !strings.Contains(err.Error(), "server prior session still set") {
		t.Fatalf("error = %v, want remote reset failure", err)
	}
}

func TestProductionDaemonWiresSessionInvalidateForAllRestartModes(t *testing.T) {
	root := t.TempDir()
	d := New(Config{WorkspacesRoot: root}, nil)
	if d.agentLifecycleExecutor == nil || d.agentLifecycleExecutor.sessions == nil {
		t.Fatal("production New() left lifecycle session store unwired")
	}
	workspaceID := uuid.NewString()
	agentID := uuid.NewString()
	runtimeID := uuid.NewString()
	if _, err := execenv.ProvisionAgentWorkspace(root, workspaceID, agentID, nil); err != nil {
		t.Fatalf("provision workspace: %v", err)
	}
	if err := d.agentLifecycleExecutor.sessions.Put(agentID, runtimeID, "stale-prior"); err != nil {
		t.Fatalf("seed session: %v", err)
	}

	err := d.agentLifecycleExecutor.Execute(context.Background(), agentLifecycleExecutionRequest{
		OperationID: uuid.NewString(),
		WorkspaceID: workspaceID,
		AgentID:     agentID,
		RuntimeID:   runtimeID,
		ActionKind:  agentLifecycleActionResetSessionRestart,
	})
	if err != nil && strings.Contains(err.Error(), "session reset client is not configured") {
		t.Fatalf("production reset_session_restart failed closed as unwired: %v", err)
	}
	if err == nil {
		t.Fatal("production reset_session_restart swallowed the remote session-reset failure")
	}
	got, getErr := d.agentLifecycleExecutor.sessions.Get(agentID, runtimeID)
	if getErr != nil {
		t.Fatalf("read local session: %v", getErr)
	}
	if got != "" {
		t.Fatalf("local session after failed remote reset = %q, want empty", got)
	}
}
