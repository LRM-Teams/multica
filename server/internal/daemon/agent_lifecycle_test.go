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
	memoryPath := filepath.Join(layout.WorkDir, "MEMORY.md")
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
	if _, err := os.Stat(layout.WorkDir); err != nil {
		t.Fatalf("full reset did not reprovision workspace: %v", err)
	}
	if len(resetter.calls) != 2 {
		t.Fatalf("session reset calls=%d want=2", len(resetter.calls))
	}
}

func TestAgentLifecycleExecutorFailsClosedOnActiveTurn(t *testing.T) {
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
		ActionKind:  agentLifecycleActionFullResetRestart,
	})
	var stepErr *agentLifecycleExecutionError
	if !errors.As(err, &stepErr) || stepErr.Step != "drain" ||
		!errors.Is(err, ErrCanonicalAgentRuntimeBusy) {
		t.Fatalf("active turn error=%v", err)
	}
	if len(resetter.calls) != 0 {
		t.Fatal("active turn reached session reset")
	}
}

func TestAgentLifecycleExecutorReportsPartialFailureStep(t *testing.T) {
	resetter := &lifecycleResetRecorder{err: errors.New("reset unavailable")}
	executor := &agentLifecycleExecutor{sessionReset: resetter}
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
