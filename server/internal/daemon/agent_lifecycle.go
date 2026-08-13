package daemon

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/daemon/execenv"
)

const agentLifecycleLeaseReleaseTimeout = 5 * time.Second

type agentLifecycleActionKind string

const (
	agentLifecycleActionRestart             agentLifecycleActionKind = "restart"
	agentLifecycleActionResetSessionRestart agentLifecycleActionKind = "reset_session_restart"
	agentLifecycleActionFullResetRestart    agentLifecycleActionKind = "full_reset_restart"
)

type agentLifecycleExecutionRequest struct {
	OperationID string
	WorkspaceID string
	AgentID     string
	RuntimeID   string
	ActionKind  agentLifecycleActionKind
}

type agentLifecycleSessionResetter interface {
	ResetAgentRuntimeSession(ctx context.Context, operationID, agentID, runtimeID string) error
}

type agentLifecycleExecutor struct {
	workspacesRoot string
	runtimes       *canonicalAgentRuntimePool
	sessionReset   agentLifecycleSessionResetter
	sessions       *agentRuntimeSessionStore
	commands       *agentLifecycleCommandLedger
	starter        agentLifecycleStarter
	logger         *slog.Logger
}

type agentLifecycleExecutionError struct {
	Step string
	Err  error
}

func (e *agentLifecycleExecutionError) Error() string {
	return "agent lifecycle " + e.Step + ": " + e.Err.Error()
}

func (e *agentLifecycleExecutionError) Unwrap() error { return e.Err }

// Execute performs one restart command: stop (force if busy) → optional
// session invalidate → optional workspace reset → start. A start against a
// still-running process is refused (not a silent rebind). Destructive steps
// are idempotent on the same command id.
func (e *agentLifecycleExecutor) Execute(ctx context.Context, request agentLifecycleExecutionRequest) error {
	if err := validateAgentLifecycleExecutionRequest(request); err != nil {
		return lifecycleStepError("validate", err)
	}
	if e == nil {
		return lifecycleStepError("validate", errors.New("executor is not configured"))
	}
	if err := e.validateDependencies(request); err != nil {
		return lifecycleStepError("validate", err)
	}
	replay, err := e.beginCommand(request)
	if err != nil {
		return lifecycleStepError("validate", err)
	}
	if replay {
		return nil
	}

	// #112 / #62: all lifecycle restart kinds force-interrupt a busy turn.
	if err := e.stopRuntime(ctx, request); err != nil {
		return err
	}

	switch request.ActionKind {
	case agentLifecycleActionRestart:
	case agentLifecycleActionResetSessionRestart:
		if err := e.resetSession(ctx, request); err != nil {
			return err
		}
	case agentLifecycleActionFullResetRestart:
		if err := e.resetSession(ctx, request); err != nil {
			return err
		}
		if err := e.resetWorkspace(request); err != nil {
			return err
		}
	default:
		return lifecycleStepError("validate", fmt.Errorf("unsupported action_kind %q", request.ActionKind))
	}

	if err := e.startAfterStop(ctx, request); err != nil {
		return err
	}
	if err := e.commitCommand(request); err != nil {
		return lifecycleStepError("validate", err)
	}
	return nil
}

func (e *agentLifecycleExecutor) validateDependencies(request agentLifecycleExecutionRequest) error {
	if e.runtimes == nil {
		return errors.New("canonical runtime pool is not configured")
	}
	switch request.ActionKind {
	case agentLifecycleActionResetSessionRestart, agentLifecycleActionFullResetRestart:
		if e.sessions == nil && e.sessionReset == nil {
			return errors.New("session reset client is not configured")
		}
	}
	if request.ActionKind == agentLifecycleActionFullResetRestart {
		if _, err := execenv.ResolveAgentWorkspaceLayout(
			e.workspacesRoot, request.WorkspaceID, request.AgentID,
		); err != nil {
			return err
		}
	}
	return nil
}

func (e *agentLifecycleExecutor) resetSession(ctx context.Context, request agentLifecycleExecutionRequest) error {
	if e.sessions == nil && e.sessionReset == nil {
		return lifecycleStepError("reset_session", errors.New("session reset client is not configured"))
	}
	if e.sessions != nil {
		if err := e.sessions.Invalidate(request.OperationID, request.AgentID, request.RuntimeID); err != nil {
			return lifecycleStepError("reset_session", err)
		}
	}
	if e.sessionReset == nil {
		return nil
	}
	if err := e.sessionReset.ResetAgentRuntimeSession(
		ctx, request.OperationID, request.AgentID, request.RuntimeID,
	); err != nil {
		return lifecycleStepError("reset_session", err)
	}
	return nil
}

func (e *agentLifecycleExecutor) beginCommand(request agentLifecycleExecutionRequest) (bool, error) {
	if e == nil || e.commands == nil {
		return false, nil
	}
	return e.commands.Begin(request.OperationID, string(request.ActionKind))
}

func (e *agentLifecycleExecutor) commitCommand(request agentLifecycleExecutionRequest) error {
	if e == nil || e.commands == nil {
		return nil
	}
	return e.commands.Commit(request.OperationID, string(request.ActionKind))
}

func (e *agentLifecycleExecutor) stopRuntime(ctx context.Context, request agentLifecycleExecutionRequest) error {
	// Raft hasStarting: a start still in flight has no live process. Killing
	// the factory slot there makes the spawn callback stale and leaves
	// Starting with nothing to recover.
	if e.runtimes != nil && !e.runtimes.hasLiveLease(request.AgentID, request.RuntimeID) && !e.runtimes.residentProcessAlive(request.AgentID, request.RuntimeID) {
		return nil
	}
	if err := e.forceInvalidateRuntime(request); err != nil {
		return err
	}
	if err := e.waitForLeaseRelease(ctx, request); err != nil {
		return err
	}
	if e.runtimes == nil {
		return nil
	}
	if err := e.runtimes.invalidateSession(request.AgentID, request.RuntimeID); err != nil && !errors.Is(err, ErrCanonicalAgentRuntimeBusy) {
		return lifecycleStepError("restart_runtime", err)
	}
	return nil
}

func (e *agentLifecycleExecutor) waitForLeaseRelease(ctx context.Context, request agentLifecycleExecutionRequest) error {
	if e == nil || e.runtimes == nil || !e.runtimes.hasLiveLease(request.AgentID, request.RuntimeID) {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	deadline := time.Now().Add(agentLifecycleLeaseReleaseTimeout)
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		if !e.runtimes.hasLiveLease(request.AgentID, request.RuntimeID) {
			return nil
		}
		if time.Now().After(deadline) {
			return lifecycleStepError("restart_runtime", errors.New("provider lease still held after stop"))
		}
		select {
		case <-ctx.Done():
			return lifecycleStepError("restart_runtime", ctx.Err())
		case <-ticker.C:
		}
	}
}

func (e *agentLifecycleExecutor) startAfterStop(ctx context.Context, request agentLifecycleExecutionRequest) error {
	if e.runtimes != nil && e.runtimes.hasLiveLease(request.AgentID, request.RuntimeID) {
		return lifecycleStepError("start", errors.New("refusing start while process still held"))
	}
	if e.starter == nil {
		return nil
	}
	sessionID := ""
	if e.sessions != nil {
		got, err := e.sessions.Get(request.AgentID, request.RuntimeID)
		if err != nil {
			return lifecycleStepError("start", err)
		}
		sessionID = got
	}
	if err := e.starter.Start(ctx, agentLifecycleStartRequest{
		CommandID:   request.OperationID,
		WorkspaceID: request.WorkspaceID,
		AgentID:     request.AgentID,
		RuntimeID:   request.RuntimeID,
		SessionID:   sessionID,
	}); err != nil {
		return lifecycleStepError("start", err)
	}
	return nil
}

func (e *agentLifecycleExecutor) invalidateRuntime(request agentLifecycleExecutionRequest) error {
	if e.runtimes == nil {
		return lifecycleStepError("restart_runtime", errors.New("canonical runtime pool is not configured"))
	}
	if err := e.runtimes.invalidateSession(request.AgentID, request.RuntimeID); err != nil {
		return lifecycleStepError("restart_runtime", err)
	}
	return nil
}

// forceInvalidateRuntime is invalidateRuntime's counterpart for plain restart
// (task #62): it interrupts a busy slot instead of refusing it, via
// canonicalAgentRuntimePool.forceInvalidateSession. See that function's doc
// comment for why this is not simply "call closeBackend() anyway".
func (e *agentLifecycleExecutor) forceInvalidateRuntime(request agentLifecycleExecutionRequest) error {
	if e.runtimes == nil {
		return lifecycleStepError("restart_runtime", errors.New("canonical runtime pool is not configured"))
	}
	if err := e.runtimes.forceInvalidateSession(request.AgentID, request.RuntimeID); err != nil {
		return lifecycleStepError("restart_runtime", err)
	}
	return nil
}

func (e *agentLifecycleExecutor) resetWorkspace(request agentLifecycleExecutionRequest) error {
	if e.runtimes != nil && e.runtimes.hasLiveLease(request.AgentID, request.RuntimeID) {
		return lifecycleStepError("remove_workspace", errors.New("refusing workspace removal while provider lease is held"))
	}
	layout, err := execenv.ResolveAgentWorkspaceLayout(
		e.workspacesRoot, request.WorkspaceID, request.AgentID,
	)
	if err != nil {
		return lifecycleStepError("resolve_workspace", err)
	}
	if err := execenv.RemoveAgentWorkspace(execenv.RemoveAgentWorkspaceParams{
		WorkspacesRoot: e.workspacesRoot,
		WorkspaceID:    request.WorkspaceID,
		AgentID:        request.AgentID,
		AgentRoot:      layout.AgentRoot,
		Reason:         execenv.AgentWorkspaceRemovalFullReset,
	}); err != nil {
		return lifecycleStepError("remove_workspace", err)
	}
	if _, err := execenv.ProvisionAgentWorkspace(
		e.workspacesRoot, request.WorkspaceID, request.AgentID, e.logger,
	); err != nil {
		return lifecycleStepError("provision_workspace", err)
	}
	return nil
}

func validateAgentLifecycleExecutionRequest(request agentLifecycleExecutionRequest) error {
	for name, value := range map[string]string{
		"operation_id": request.OperationID,
		"workspace_id": request.WorkspaceID,
		"agent_id":     request.AgentID,
		"runtime_id":   request.RuntimeID,
	} {
		if !isCanonicalRuntimeUUID(value) {
			return fmt.Errorf("%s must be a canonical full UUID", name)
		}
	}
	switch request.ActionKind {
	case agentLifecycleActionRestart,
		agentLifecycleActionResetSessionRestart,
		agentLifecycleActionFullResetRestart:
		return nil
	default:
		return fmt.Errorf("unsupported action_kind %q", strings.TrimSpace(string(request.ActionKind)))
	}
}

// agentLifecycleResumeStarter is the production start step: after stop it
// refuses a still-held process (that would be a silent rebind) and otherwise
// leaves the stored session identity for the next acquire.
type agentLifecycleResumeStarter struct {
	runtimes *canonicalAgentRuntimePool
	sessions *agentRuntimeSessionStore
	start    func(ctx context.Context, req agentLifecycleStartRequest) error
}

func (d *Daemon) recordProviderSession(agentID, runtimeID, sessionID string) {
	if d == nil || d.agentLifecycleExecutor == nil || d.agentLifecycleExecutor.sessions == nil {
		return
	}
	agentID = strings.TrimSpace(agentID)
	runtimeID = strings.TrimSpace(runtimeID)
	sessionID = strings.TrimSpace(sessionID)
	if agentID == "" || runtimeID == "" || sessionID == "" {
		return
	}
	if err := d.agentLifecycleExecutor.sessions.Put(agentID, runtimeID, sessionID); err != nil && d.logger != nil {
		d.logger.Warn("record provider session failed", "agent_id", agentID, "error", err)
	}
}

func (s agentLifecycleResumeStarter) Start(ctx context.Context, req agentLifecycleStartRequest) error {
	if s.runtimes != nil && s.runtimes.hasLiveLease(req.AgentID, req.RuntimeID) {
		return errors.New("refusing start while process still held")
	}
	if s.sessions != nil {
		if err := s.sessions.Put(req.AgentID, req.RuntimeID, req.SessionID); err != nil {
			return err
		}
	}
	if s.runtimes != nil {
		s.runtimes.setNextResumeSession(req.AgentID, req.RuntimeID, req.SessionID)
	}
	if s.start != nil {
		return s.start(ctx, req)
	}
	return nil
}

func lifecycleStepError(step string, err error) error {
	if err == nil {
		return nil
	}
	return &agentLifecycleExecutionError{Step: step, Err: err}
}

func isCanonicalRuntimeUUID(value string) bool {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || value != strings.ToLower(trimmed) {
		return false
	}
	parsed, err := uuid.Parse(value)
	return err == nil && parsed != uuid.Nil && parsed.String() == value
}
