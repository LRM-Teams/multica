package handler

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/multica-ai/multica/server/internal/daemonws"
	"github.com/multica-ai/multica/server/internal/util"
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
	stopLaunchID   string
	startSessionID string
}

// beginAgentRestartOperation starts the server-owned product operation at
// Raft's first discrete boundary. An observed launch must produce inactive
// before session/workspace mutation or replacement start can advance.
func (h *Handler) beginAgentRestartOperation(ctx context.Context, state activeAgentRestartState) error {
	if h.TxStarter == nil {
		return errors.New("Agent restart transaction store is unavailable")
	}
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	locked, err := lockActiveAgentRestartState(ctx, tx, state.operationID)
	if err != nil || locked == nil || locked.step != agentRestartStepStopping {
		return err
	}
	state = *locked
	if state.stopLaunchID == "" {
		if obs, ok := h.observations().get(state.workspaceID, state.agentID); ok &&
			obs.runtimeID == state.runtimeID && obs.daemonID == state.computerID &&
			(obs.status == "accepted" || obs.status == protocol.AgentStatusActive) {
			state.stopLaunchID = obs.launchID
			if state.storageKind == agentRestartStorageRestart {
				state.startSessionID = obs.sessionID
			}
		} else {
			err = tx.QueryRow(ctx, `
				SELECT launch_id::text FROM agent_runner_launch_projection
				WHERE workspace_id = $1 AND agent_id = $2 AND runtime_id = $3
			`, state.workspaceID, state.agentID, state.runtimeID).Scan(&state.stopLaunchID)
			if err != nil {
				return fmt.Errorf("resolve Agent stop launch fence: %w", err)
			}
		}
		if _, err := tx.Exec(ctx, `
			UPDATE agent_restart_operation SET stop_launch_id = $2, start_session_id = $3, updated_at = now()
			WHERE id = $1 AND status = 'running' AND step = $4
		`, state.operationID, state.stopLaunchID, state.startSessionID, agentRestartStepStopping); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	return h.sendAgentRestartCommand(state, protocol.EventDaemonAgentStop,
		protocol.WorkspaceRunnerAgentStopPayload{AgentID: state.agentID, LaunchID: state.stopLaunchID})
}

func (h *Handler) advanceAgentRestartAfterStop(ctx context.Context, state activeAgentRestartState) error {
	if h.TxStarter == nil {
		return errors.New("Agent restart transaction store is unavailable")
	}
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	locked, err := lockActiveAgentRestartState(ctx, tx, state.operationID)
	if err != nil {
		return err
	}
	if locked == nil || locked.step != agentRestartStepStopping {
		return nil
	}
	state = *locked
	if state.storageKind == agentRestartStorageSession || state.storageKind == agentRestartStorageFull {
		if err := clearAgentRuntimeSessionState(ctx, tx, state.agentID, state.runtimeID); err != nil {
			return err
		}
	}
	if state.storageKind == agentRestartStorageFull {
		if _, err := tx.Exec(ctx, `
			UPDATE agent_restart_operation SET step = $2, updated_at = now()
			WHERE id = $1 AND status = 'running'
		`, state.operationID, agentRestartStepResettingWorkspace); err != nil {
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		state.step = agentRestartStepResettingWorkspace
		return h.sendAgentRestartCommand(state, protocol.EventDaemonAgentResetWorkspace,
			protocol.WorkspaceRunnerAgentResetWorkspacePayload{OperationID: state.operationID, AgentID: state.agentID})
	}

	start, err := prepareAgentRestartStart(ctx, tx, state)
	if err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	return h.sendAgentRestartCommand(state, protocol.EventDaemonAgentStart, start)
}

func (h *Handler) advanceAgentRestartAfterWorkspaceReset(ctx context.Context, state activeAgentRestartState) error {
	if h.TxStarter == nil {
		return errors.New("Agent restart transaction store is unavailable")
	}
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	locked, err := lockActiveAgentRestartState(ctx, tx, state.operationID)
	if err != nil {
		return err
	}
	if locked == nil || locked.step != agentRestartStepResettingWorkspace {
		return nil
	}
	state = *locked
	start, err := prepareAgentRestartStart(ctx, tx, state)
	if err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	return h.sendAgentRestartCommand(state, protocol.EventDaemonAgentStart, start)
}

func prepareAgentRestartStart(ctx context.Context, tx pgx.Tx, state activeAgentRestartState) (protocol.WorkspaceRunnerAgentStartPayload, error) {
	sessionID := ""
	if state.storageKind == agentRestartStorageRestart {
		sessionID = state.startSessionID
	}
	launchID := uuid.NewString()
	command, err := tx.Exec(ctx, `
		UPDATE agent_runner_launch_projection
		SET launch_id = $1, start_dispatch_id = $2, updated_at = now()
		WHERE workspace_id = $3 AND agent_id = $4 AND runtime_id = $5
	`, launchID, state.operationID, state.workspaceID, state.agentID, state.runtimeID)
	if err != nil {
		return protocol.WorkspaceRunnerAgentStartPayload{}, err
	}
	if command.RowsAffected() != 1 {
		return protocol.WorkspaceRunnerAgentStartPayload{}, errors.New("desired Agent launch is unavailable")
	}
	if _, err := tx.Exec(ctx, `
		UPDATE agent_restart_operation
		SET step = $2, start_session_id = $3, updated_at = now()
		WHERE id = $1 AND status = 'running'
	`, state.operationID, agentRestartStepStarting, sessionID); err != nil {
		return protocol.WorkspaceRunnerAgentStartPayload{}, err
	}
	start := protocol.WorkspaceRunnerAgentStartPayload{
		AgentID: state.agentID, RuntimeID: state.runtimeID,
		LaunchID: launchID, StartDispatchID: state.operationID,
		Config: protocol.WorkspaceRunnerAgentStartConfig{SessionID: sessionID},
	}
	return start, nil
}

func clearAgentRuntimeSessionState(ctx context.Context, tx pgx.Tx, agentID, runtimeID string) error {
	if _, err := tx.Exec(ctx, `UPDATE chat_session SET session_id = NULL, updated_at = now() WHERE agent_id = $1 AND runtime_id = $2`, agentID, runtimeID); err != nil {
		return fmt.Errorf("clear chat provider sessions: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE agent_inbox_event SET session_id = NULL, updated_at = now() WHERE agent_id = $1 AND runtime_id = $2 AND session_id IS NOT NULL`, agentID, runtimeID); err != nil {
		return fmt.Errorf("clear inbox provider sessions: %w", err)
	}
	return nil
}

func lockActiveAgentRestartState(ctx context.Context, tx pgx.Tx, operationID string) (*activeAgentRestartState, error) {
	var state activeAgentRestartState
	err := tx.QueryRow(ctx, `
		SELECT operation.id::text, operation.workspace_id::text, operation.agent_id::text,
		       operation.runtime_id::text, runtime.daemon_id::text,
		       operation.action_kind, operation.step, COALESCE(operation.stop_launch_id::text, ''),
		       COALESCE(operation.start_session_id, '')
		FROM agent_restart_operation operation
		JOIN agent_runtime runtime ON runtime.id = operation.runtime_id
		WHERE operation.id = $1 AND operation.status = 'running'
		FOR UPDATE OF operation
	`, operationID).Scan(&state.operationID, &state.workspaceID, &state.agentID, &state.runtimeID, &state.computerID, &state.storageKind, &state.step, &state.stopLaunchID, &state.startSessionID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return &state, err
}

func (h *Handler) sendAgentRestartCommand(state activeAgentRestartState, eventType string, payload any) error {
	if h.AgentRestartNotifier == nil || !h.AgentRestartNotifier.NotifyAgentRestartCommand(
		state.workspaceID, state.computerID, eventType, state.operationID, payload,
	) {
		return errors.New("current Workspace Runner unavailable during Agent restart operation")
	}
	return nil
}

// advanceAgentRestartFromStatus consumes Raft's process facts. It returns
// true when the status belonged to an active restart operation, allowing the
// caller to avoid a parallel generic reconcile in the same frame.
func (h *Handler) advanceAgentRestartFromStatus(ctx context.Context, identity daemonws.ClientIdentity, status protocol.AgentStatusPayload) (bool, error) {
	state, err := h.activeAgentRestartForRunner(ctx, identity, status.AgentID)
	if err != nil || state == nil {
		return false, err
	}
	switch {
	case state.step == agentRestartStepStopping && status.Status == protocol.AgentStatusInactive && status.LaunchID == state.stopLaunchID:
		return true, h.advanceAgentRestartAfterStop(ctx, *state)
	case state.step == agentRestartStepStarting:
		var desiredLaunchID string
		if err := h.DB.QueryRow(ctx, `SELECT launch_id::text FROM agent_runner_launch_projection WHERE agent_id = $1 AND runtime_id = $2`, state.agentID, state.runtimeID).Scan(&desiredLaunchID); err != nil {
			return true, err
		}
		if desiredLaunchID != status.LaunchID {
			return false, nil
		}
		if status.Status == protocol.AgentStatusActive {
			_, err = h.DB.Exec(ctx, `UPDATE agent_restart_operation SET status = 'succeeded', step = '', reason_code = '', finished_at = now(), updated_at = now() WHERE id = $1 AND status = 'running' AND step = $2`, state.operationID, agentRestartStepStarting)
		} else {
			_, err = h.DB.Exec(ctx, `UPDATE agent_restart_operation SET status = 'failed', step = 'start', reason_code = 'replacement Agent failed to become active', finished_at = now(), updated_at = now() WHERE id = $1 AND status = 'running' AND step = $2`, state.operationID, agentRestartStepStarting)
		}
		return true, err
	default:
		return false, nil
	}
}

func (h *Handler) recordAgentWorkspaceResetResult(ctx context.Context, identity daemonws.ClientIdentity, result protocol.WorkspaceRunnerAgentResetWorkspaceResultPayload) error {
	if err := result.Validate(); err != nil {
		return err
	}
	state, err := h.activeAgentRestartForRunner(ctx, identity, result.AgentID)
	if err != nil || state == nil {
		return err
	}
	if state.operationID != result.OperationID || state.step != agentRestartStepResettingWorkspace {
		return nil
	}
	if result.Status == protocol.AgentResetWorkspaceFailed {
		_, err := h.DB.Exec(ctx, `UPDATE agent_restart_operation SET status = 'failed', step = 'reset_workspace', reason_code = $2, finished_at = now(), updated_at = now() WHERE id = $1 AND status = 'running' AND step = $3`, state.operationID, result.ReasonCode, agentRestartStepResettingWorkspace)
		return err
	}
	return h.advanceAgentRestartAfterWorkspaceReset(ctx, *state)
}

func (h *Handler) activeAgentRestartForRunner(ctx context.Context, identity daemonws.ClientIdentity, agentID string) (*activeAgentRestartState, error) {
	workspaceID, err := util.ParseUUID(identity.WorkspaceID)
	if err != nil {
		return nil, errors.New("invalid Runner workspace identity")
	}
	agentUUID, err := util.ParseUUID(agentID)
	if err != nil {
		return nil, errors.New("invalid Agent restart agent_id")
	}
	var state activeAgentRestartState
	err = h.DB.QueryRow(ctx, `
		SELECT operation.id::text, operation.workspace_id::text, operation.agent_id::text,
		       operation.runtime_id::text, runtime.daemon_id::text,
		       operation.action_kind, operation.step, COALESCE(operation.stop_launch_id::text, ''),
		       COALESCE(operation.start_session_id, '')
		FROM agent_restart_operation operation
		JOIN agent_runtime runtime ON runtime.id = operation.runtime_id
		WHERE operation.workspace_id = $1 AND operation.agent_id = $2
		  AND operation.status = 'running' AND runtime.daemon_id = $3
	`, workspaceID, agentUUID, identity.DaemonID).Scan(&state.operationID, &state.workspaceID, &state.agentID, &state.runtimeID, &state.computerID, &state.storageKind, &state.step, &state.stopLaunchID, &state.startSessionID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return &state, err
}

func restartStateFromOperation(op *AgentRestartOperation, workspaceID, computerID string) activeAgentRestartState {
	state := activeAgentRestartState{
		operationID: op.ID, workspaceID: workspaceID, agentID: op.AgentID,
		computerID: computerID, storageKind: op.storageKind, step: op.Step,
	}
	if op.RuntimeID != nil {
		state.runtimeID = *op.RuntimeID
	}
	return state
}
