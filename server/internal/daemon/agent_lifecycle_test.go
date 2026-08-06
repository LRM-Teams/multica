package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/daemon/execenv"
)

type lifecycleResetRecorder struct {
	calls []agentLifecycleExecutionRequest
	err   error
}

func (r *lifecycleResetRecorder) ResetAgentRuntimeSession(_ context.Context, operationID, agentID, runtimeID string) error {
	r.calls = append(r.calls, agentLifecycleExecutionRequest{
		OperationID: operationID,
		AgentID:     agentID,
		RuntimeID:   runtimeID,
	})
	return r.err
}

func TestAgentLifecycleExecutorPreserveAndDeleteBoundaries(t *testing.T) {
	workspaceID := uuid.NewString()
	agentID := uuid.NewString()
	runtimeID := uuid.NewString()
	root := t.TempDir()
	layout, err := execenv.ProvisionAgentWorkspace(root, workspaceID, agentID, nil)
	if err != nil {
		t.Fatalf("provision workspace: %v", err)
	}
	memoryPath := filepath.Join(layout.AgentRoot, "MEMORY.md")
	if err := os.WriteFile(memoryPath, []byte("keep me"), 0o600); err != nil {
		t.Fatalf("write memory fixture: %v", err)
	}

	resetter := &lifecycleResetRecorder{}
	executor := &agentLifecycleExecutor{
		workspacesRoot: root,
		turns:          newAgentRuntimeTurnCoordinator(Config{WorkspacesRoot: root}, nil),
		runtimes:       newCanonicalAgentRuntimePool(),
		sessionReset:   resetter,
	}
	base := agentLifecycleExecutionRequest{
		WorkspaceID: workspaceID,
		AgentID:     agentID,
		RuntimeID:   runtimeID,
	}

	restart := base
	restart.OperationID = uuid.NewString()
	restart.ActionKind = agentLifecycleActionRestart
	if err := executor.Execute(context.Background(), restart); err != nil {
		t.Fatalf("restart: %v", err)
	}
	if _, err := os.Stat(memoryPath); err != nil {
		t.Fatalf("restart changed workspace: %v", err)
	}
	if len(resetter.calls) != 0 {
		t.Fatalf("restart reset session: %#v", resetter.calls)
	}

	reset := base
	reset.OperationID = uuid.NewString()
	reset.ActionKind = agentLifecycleActionResetSessionRestart
	if err := executor.Execute(context.Background(), reset); err != nil {
		t.Fatalf("reset session restart: %v", err)
	}
	if _, err := os.Stat(memoryPath); err != nil {
		t.Fatalf("session reset changed workspace: %v", err)
	}
	if len(resetter.calls) != 1 {
		t.Fatalf("session reset calls=%d want=1", len(resetter.calls))
	}

	full := base
	full.OperationID = uuid.NewString()
	full.ActionKind = agentLifecycleActionFullResetRestart
	if err := executor.Execute(context.Background(), full); err != nil {
		t.Fatalf("full reset restart: %v", err)
	}
	if _, err := os.Stat(memoryPath); !os.IsNotExist(err) {
		t.Fatalf("full reset retained workspace file: %v", err)
	}
	if _, err := os.Stat(layout.AgentRoot); err != nil {
		t.Fatalf("full reset did not reprovision workspace: %v", err)
	}
	if len(resetter.calls) != 2 {
		t.Fatalf("session reset calls=%d want=2", len(resetter.calls))
	}
}

// TestAgentLifecycleExecutorResetSessionInterruptsActiveTurn pins #112:
// reset_session_restart must not refuse a busy turn (old drain guard).
// With an empty pool slot forceInvalidate is a no-op success; session reset
// must still run (no ErrCanonicalAgentRuntimeBusy).
func TestAgentLifecycleExecutorResetSessionInterruptsActiveTurn(t *testing.T) {
	agentID := uuid.NewString()
	runtimeID := uuid.NewString()
	turnID := uuid.NewString()
	turns := newAgentRuntimeTurnCoordinator(Config{}, nil)
	key := agentRuntimeTurnSlotKey{AgentID: agentID, RuntimeID: runtimeID}
	if !turns.reserve(key, turnID) {
		t.Fatal("reserve active turn")
	}
	defer turns.release(key, turnID)

	resetter := &lifecycleResetRecorder{}
	executor := &agentLifecycleExecutor{
		workspacesRoot: t.TempDir(),
		turns:          turns,
		runtimes:       newCanonicalAgentRuntimePool(),
		sessionReset:   resetter,
	}
	err := executor.Execute(context.Background(), agentLifecycleExecutionRequest{
		OperationID: uuid.NewString(),
		WorkspaceID: uuid.NewString(),
		AgentID:     agentID,
		RuntimeID:   runtimeID,
		ActionKind:  agentLifecycleActionResetSessionRestart,
	})
	if err != nil {
		t.Fatalf("reset_session_restart on active turn error=%v, want success (force path)", err)
	}
	if len(resetter.calls) != 1 {
		t.Fatalf("session reset calls=%d, want 1", len(resetter.calls))
	}
}

// TestAgentLifecycleExecutorPlainRestartInterruptsActiveTurn pins #62/#112:
// plain restart force-interrupts a busy pool slot (ForceKill) instead of
// returning ErrCanonicalAgentRuntimeBusy.
func TestAgentLifecycleExecutorPlainRestartInterruptsActiveTurn(t *testing.T) {
	agentID := uuid.NewString()
	runtimeID := uuid.NewString()
	turnID := uuid.NewString()
	turns := newAgentRuntimeTurnCoordinator(Config{}, nil)
	key := agentRuntimeTurnSlotKey{AgentID: agentID, RuntimeID: runtimeID}
	if !turns.reserve(key, turnID) {
		t.Fatal("reserve active turn")
	}
	defer turns.release(key, turnID)

	pool := newCanonicalAgentRuntimePool()
	probe := &canonicalRuntimeFactoryProbe{}
	stable, _, err := splitAgentProcessEnvironment(map[string]string{
		"MULTICA_SERVER_URL":   "https://multica.example",
		"MULTICA_WORKSPACE_ID": "workspace-a",
		"MULTICA_AGENT_ID":     agentID,
		"MULTICA_TASK_ID":      "turn-a",
	})
	if err != nil {
		t.Fatalf("splitAgentProcessEnvironment: %v", err)
	}
	identity, err := newCanonicalAgentRuntimeIdentity(canonicalAgentRuntimeIdentityParams{
		AgentID:     agentID,
		RuntimeID:   runtimeID,
		Provider:    "pi",
		Executable:  "/usr/local/bin/pi",
		Model:       "model-a",
		WorkDir:     "/var/lib/multica/agent-a/workspace",
		Environment: stable,
		WorkspaceID: "workspace-a",
	})
	if err != nil {
		t.Fatalf("newCanonicalAgentRuntimeIdentity: %v", err)
	}
	if _, err := pool.acquire(canonicalAgentRuntimeAcquireRequest{
		Identity: identity,
		Mode:     canonicalRuntimeResident,
		Factory:  probe.factory,
	}); err != nil {
		t.Fatalf("acquire: %v", err)
	}
	backend := probe.backends[0]

	executor := &agentLifecycleExecutor{
		workspacesRoot: t.TempDir(),
		turns:          turns,
		runtimes:       pool,
		sessionReset:   &lifecycleResetRecorder{},
	}
	execErr := executor.Execute(context.Background(), agentLifecycleExecutionRequest{
		OperationID: uuid.NewString(),
		WorkspaceID: uuid.NewString(),
		AgentID:     agentID,
		RuntimeID:   runtimeID,
		ActionKind:  agentLifecycleActionRestart,
	})
	if execErr != nil {
		t.Fatalf("plain restart on active turn should interrupt, not fail: %v", execErr)
	}
	if got := backend.forceKillCount(); got != 1 {
		t.Fatalf("ForceKill called %d times, want 1", got)
	}
}

func TestAgentLifecycleExecutorReportsPartialFailureStep(t *testing.T) {
	resetter := &lifecycleResetRecorder{err: errors.New("reset unavailable")}
	executor := &agentLifecycleExecutor{
		workspacesRoot: t.TempDir(),
		turns:          newAgentRuntimeTurnCoordinator(Config{}, nil),
		runtimes:       newCanonicalAgentRuntimePool(),
		sessionReset:   resetter,
	}
	err := executor.Execute(context.Background(), agentLifecycleExecutionRequest{
		OperationID: uuid.NewString(),
		WorkspaceID: uuid.NewString(),
		AgentID:     uuid.NewString(),
		RuntimeID:   uuid.NewString(),
		ActionKind:  agentLifecycleActionFullResetRestart,
	})
	var stepErr *agentLifecycleExecutionError
	if !errors.As(err, &stepErr) || stepErr.Step != "reset_session" {
		t.Fatalf("partial failure error=%v", err)
	}
}

func TestAgentLifecycleExecutorRequiresSafetyDependenciesBeforeMutation(t *testing.T) {
	root := t.TempDir()
	workspaceID := uuid.NewString()
	agentID := uuid.NewString()
	runtimeID := uuid.NewString()
	layout, err := execenv.ProvisionAgentWorkspace(root, workspaceID, agentID, nil)
	if err != nil {
		t.Fatalf("provision workspace: %v", err)
	}
	sentinelPath := filepath.Join(layout.AgentRoot, "sentinel.txt")
	if err := os.WriteFile(sentinelPath, []byte("keep me"), 0o600); err != nil {
		t.Fatalf("write sentinel: %v", err)
	}
	resetter := &lifecycleResetRecorder{}
	base := agentLifecycleExecutionRequest{
		OperationID: uuid.NewString(),
		WorkspaceID: workspaceID,
		AgentID:     agentID,
		RuntimeID:   runtimeID,
	}

	t.Run("missing turn coordinator", func(t *testing.T) {
		request := base
		request.ActionKind = agentLifecycleActionFullResetRestart
		executor := &agentLifecycleExecutor{
			workspacesRoot: root,
			runtimes:       newCanonicalAgentRuntimePool(),
			sessionReset:   resetter,
		}
		assertLifecycleValidationFailure(t, executor.Execute(context.Background(), request))
		if len(resetter.calls) != 0 {
			t.Fatal("missing coordinator reached session reset")
		}
		if _, err := os.Stat(sentinelPath); err != nil {
			t.Fatalf("missing coordinator changed workspace: %v", err)
		}
	})

	t.Run("missing runtime pool", func(t *testing.T) {
		request := base
		request.ActionKind = agentLifecycleActionRestart
		executor := &agentLifecycleExecutor{
			turns: newAgentRuntimeTurnCoordinator(Config{}, nil),
		}
		assertLifecycleValidationFailure(t, executor.Execute(context.Background(), request))
	})

	t.Run("missing workspace root", func(t *testing.T) {
		request := base
		request.ActionKind = agentLifecycleActionFullResetRestart
		executor := &agentLifecycleExecutor{
			turns:        newAgentRuntimeTurnCoordinator(Config{}, nil),
			runtimes:     newCanonicalAgentRuntimePool(),
			sessionReset: resetter,
		}
		assertLifecycleValidationFailure(t, executor.Execute(context.Background(), request))
		if len(resetter.calls) != 0 {
			t.Fatal("missing workspace root reached session reset")
		}
		if _, err := os.Stat(sentinelPath); err != nil {
			t.Fatalf("missing workspace root changed workspace: %v", err)
		}
	})
}

func TestAgentLifecycleExecutorRejectsBadBinding(t *testing.T) {
	executor := &agentLifecycleExecutor{}
	err := executor.Execute(context.Background(), agentLifecycleExecutionRequest{
		OperationID: uuid.NewString(),
		WorkspaceID: "../workspace",
		AgentID:     uuid.NewString(),
		RuntimeID:   uuid.NewString(),
		ActionKind:  agentLifecycleActionFullResetRestart,
	})
	var stepErr *agentLifecycleExecutionError
	if !errors.As(err, &stepErr) || stepErr.Step != "validate" {
		t.Fatalf("bad binding error=%v", err)
	}
}

func assertLifecycleValidationFailure(t *testing.T, err error) {
	t.Helper()
	var stepErr *agentLifecycleExecutionError
	if !errors.As(err, &stepErr) || stepErr.Step != "validate" {
		t.Fatalf("validation error=%v", err)
	}
}
