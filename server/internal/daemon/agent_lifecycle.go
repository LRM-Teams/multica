package daemon

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
)

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
	turns          *agentRuntimeTurnCoordinator
	runtimes       *canonicalAgentRuntimePool
	sessionReset   agentLifecycleSessionResetter
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

// Execute performs only the D1/D2/D4 lifecycle mutation after D5/D6 has
// acquired the per-agent claim barrier. It owns no queue state and advertises
// no capability by itself.
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
	// #112 / #62: all lifecycle restart kinds force-interrupt a busy turn.
	// hasActiveTurn permanently true is the stuck-agent case. Never refuse
	// with ErrCanonicalAgentRuntimeBusy — that recreated "scheduled forever"
	// hangs for reset_session/full_reset (阿泰 2026-08-03).

	switch request.ActionKind {
	case agentLifecycleActionRestart:
		return e.forceInvalidateRuntime(request)
	case agentLifecycleActionResetSessionRestart:
		if err := e.resetSession(ctx, request); err != nil {
			return err
		}
		return e.forceInvalidateRuntime(request)
	case agentLifecycleActionFullResetRestart:
		if err := e.resetSession(ctx, request); err != nil {
			return err
		}
		if err := e.forceInvalidateRuntime(request); err != nil {
			return err
		}
		return e.resetWorkspace(request)
	default:
		return lifecycleStepError("validate", fmt.Errorf("unsupported action_kind %q", request.ActionKind))
	}
}

func (e *agentLifecycleExecutor) validateDependencies(request agentLifecycleExecutionRequest) error {
	if e.turns == nil {
		return errors.New("canonical turn coordinator is not configured")
	}
	if e.runtimes == nil {
		return errors.New("canonical runtime pool is not configured")
	}
	switch request.ActionKind {
	case agentLifecycleActionResetSessionRestart, agentLifecycleActionFullResetRestart:
		if e.sessionReset == nil {
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
	if e.sessionReset == nil {
		return lifecycleStepError("reset_session", errors.New("session reset client is not configured"))
	}
	if err := e.sessionReset.ResetAgentRuntimeSession(
		ctx, request.OperationID, request.AgentID, request.RuntimeID,
	); err != nil {
		return lifecycleStepError("reset_session", err)
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

func lifecycleStepError(step string, err error) error {
	if err == nil {
		return nil
	}
	return &agentLifecycleExecutionError{Step: step, Err: err}
}
