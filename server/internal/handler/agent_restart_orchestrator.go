package handler

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/multica-ai/multica/server/internal/daemonws"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

const (
	agentRestartStepStopping           = "stopping"
	agentRestartStepResettingWorkspace = "resetting_workspace"
	agentRestartStepStarting           = "starting"
)

type activeAgentRestartState struct {
	operationID    string
	workspaceID    string
	agentID        string
	runtimeID      string
	computerID     string
	storageKind    agentRestartStorageKind
	step           string
	startSessionID string
}

// beginAgentRestartOperation starts stop on the current Runner socket.
// An observed launch must produce inactive before session/workspace
// mutation or replacement start can advance.
func (h *Handler) beginAgentRestartOperation(ctx context.Context, state activeAgentRestartState) error {
	_ = ctx
	updated, ok := h.restarts().update(state.agentID, func(current *activeAgentRestartState) bool {
		if current.operationID != state.operationID || current.step != agentRestartStepStopping {
			return false
		}
		if obs, found := h.observations().get(current.workspaceID, current.agentID); found &&
			obs.runtimeID == current.runtimeID && obs.daemonID == current.computerID &&
			(obs.status == "accepted" || obs.status == protocol.AgentStatusActive) {
			if current.storageKind == agentRestartStorageRestart {
				current.startSessionID = obs.sessionID
			}
			return true
		}
		return true
	})
	if !ok {
		return nil
	}
	return h.sendAgentRestartCommand(updated, protocol.EventDaemonAgentStop,
		protocol.AgentStopPayload{AgentID: updated.agentID})
}

func (h *Handler) advanceAgentRestartAfterStop(ctx context.Context, state activeAgentRestartState) error {
	restarts := h.restarts()
	restarts.lifecycleMu.Lock()
	current, ok := h.restarts().get(state.agentID)
	if !ok || current.operationID != state.operationID || current.step != agentRestartStepStopping {
		restarts.lifecycleMu.Unlock()
		return nil
	}
	if current.storageKind == agentRestartStorageSession || current.storageKind == agentRestartStorageFull {
		if h.TxStarter == nil {
			restarts.lifecycleMu.Unlock()
			return errors.New("Agent restart transaction store is unavailable")
		}
		tx, err := h.TxStarter.Begin(ctx)
		if err != nil {
			restarts.lifecycleMu.Unlock()
			return err
		}
		if err := clearAgentRuntimeSessionState(ctx, tx, current.agentID, current.runtimeID); err != nil {
			_ = tx.Rollback(ctx)
			restarts.lifecycleMu.Unlock()
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			restarts.lifecycleMu.Unlock()
			return err
		}
	}
	if current.storageKind == agentRestartStorageFull {
		updated, ok := h.restarts().update(current.agentID, func(next *activeAgentRestartState) bool {
			if next.operationID != current.operationID || next.step != agentRestartStepStopping {
				return false
			}
			next.step = agentRestartStepResettingWorkspace
			return true
		})
		if !ok {
			restarts.lifecycleMu.Unlock()
			return nil
		}
		err := h.sendAgentRestartCommand(updated, protocol.EventDaemonAgentResetWorkspace,
			protocol.AgentWorkspaceResetPayload{OperationID: updated.operationID, AgentID: updated.agentID})
		restarts.lifecycleMu.Unlock()
		return err
	}
	restarts.lifecycleMu.Unlock()
	return h.dispatchAgentRestartStart(ctx, current)
}

func (h *Handler) advanceAgentRestartAfterWorkspaceReset(ctx context.Context, state activeAgentRestartState) error {
	current, ok := h.restarts().get(state.agentID)
	if !ok || current.operationID != state.operationID || current.step != agentRestartStepResettingWorkspace {
		return nil
	}
	return h.dispatchAgentRestartStart(ctx, current)
}

func (h *Handler) dispatchAgentRestartStart(ctx context.Context, state activeAgentRestartState) error {
	_ = ctx
	restarts := h.restarts()
	restarts.lifecycleMu.Lock()
	defer restarts.lifecycleMu.Unlock()
	current, active := h.restarts().get(state.agentID)
	if !active || current.operationID != state.operationID || current.step != state.step {
		return nil
	}
	if _, ok := h.restarts().update(state.agentID, func(current *activeAgentRestartState) bool {
		if current.operationID != state.operationID {
			return false
		}
		current.step = agentRestartStepStarting
		return true
	}); !ok {
		return nil
	}
	state.step = agentRestartStepStarting
	start := prepareAgentRestartStart(state)
	return h.sendAgentRestartCommand(state, protocol.EventDaemonAgentStart, start)
}

func prepareAgentRestartStart(state activeAgentRestartState) protocol.AgentStartPayload {
	sessionID := ""
	if state.storageKind == agentRestartStorageRestart {
		sessionID = state.startSessionID
	}
	return protocol.AgentStartPayload{
		AgentID: state.agentID, RuntimeID: state.runtimeID,
		Config: protocol.AgentStartConfig{SessionID: sessionID},
	}
}

func clearAgentRuntimeSessionState(ctx context.Context, tx pgx.Tx, agentID, runtimeID string) error {
	if _, err := tx.Exec(ctx, `
		UPDATE agent
		SET provider_session_id = NULL, updated_at = now()
		WHERE id = $1 AND runtime_id = $2
	`, agentID, runtimeID); err != nil {
		return fmt.Errorf("clear desired Runner provider session: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE chat_session SET session_id = NULL, updated_at = now() WHERE agent_id = $1 AND runtime_id = $2`, agentID, runtimeID); err != nil {
		return fmt.Errorf("clear chat provider sessions: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE agent_inbox_event SET session_id = NULL, updated_at = now() WHERE agent_id = $1 AND runtime_id = $2 AND session_id IS NOT NULL`, agentID, runtimeID); err != nil {
		return fmt.Errorf("clear inbox provider sessions: %w", err)
	}
	return nil
}

func (h *Handler) sendAgentRestartCommand(state activeAgentRestartState, eventType string, payload any) error {
	if h.AgentRestartNotifier == nil || !h.AgentRestartNotifier.NotifyAgentRestartCommand(
		state.workspaceID, state.computerID, eventType, state.operationID, payload,
	) {
		return errors.New("current WorkspaceDaemon unavailable during Agent restart operation")
	}
	return nil
}

// advanceAgentRestartFromStatus consumes Raft's process facts. It returns
// true when the status belonged to an active restart, allowing the caller
// to avoid a parallel generic reconcile in the same frame.
func (h *Handler) advanceAgentRestartFromStatus(ctx context.Context, identity daemonws.ClientIdentity, status protocol.AgentStatusPayload) (bool, error) {
	state, ok := h.activeAgentRestartForRunner(identity, status.AgentID)
	if !ok {
		return false, nil
	}
	observed, ok := h.observations().get(state.workspaceID, state.agentID)
	if !ok || observed.daemonID != state.computerID || observed.runtimeID != state.runtimeID || observed.status != status.Status {
		return false, nil
	}
	switch {
	case state.step == agentRestartStepStopping && status.Status == protocol.AgentStatusInactive:
		return true, h.advanceAgentRestartAfterStop(ctx, state)
	case state.step == agentRestartStepStarting:
		restarts := h.restarts()
		restarts.lifecycleMu.Lock()
		defer restarts.lifecycleMu.Unlock()
		current, active := h.activeAgentRestartForRunner(identity, status.AgentID)
		if !active || current.operationID != state.operationID || current.step != agentRestartStepStarting {
			return false, nil
		}
		state = current
		if status.Status == protocol.AgentStatusInactive {
			h.restarts().finish(state.agentID)
			return true, nil
		}
		if status.Status == protocol.AgentStatusActive {
			h.restarts().finish(state.agentID)
		}
		return true, nil
	default:
		return false, nil
	}
}

func (h *Handler) recordAgentWorkspaceResetResult(ctx context.Context, identity daemonws.ClientIdentity, result protocol.AgentWorkspaceResetResultPayload) error {
	if err := result.Validate(); err != nil {
		return err
	}
	state, ok := h.activeAgentRestartForRunner(identity, result.AgentID)
	if !ok {
		return nil
	}
	if state.operationID != result.OperationID || state.step != agentRestartStepResettingWorkspace {
		return nil
	}
	if result.Status == protocol.AgentResetWorkspaceFailed {
		h.restarts().finish(state.agentID)
		return nil
	}
	return h.advanceAgentRestartAfterWorkspaceReset(ctx, state)
}

func (h *Handler) activeAgentRestartForRunner(identity daemonws.ClientIdentity, agentID string) (activeAgentRestartState, bool) {
	state, ok := h.restarts().get(agentID)
	if !ok || state.workspaceID != identity.WorkspaceID || state.computerID != identity.DaemonID {
		return activeAgentRestartState{}, false
	}
	return state, true
}
