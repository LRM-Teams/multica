package handler

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type agentRestartStorageKind string

const (
	agentRestartStorageRestart agentRestartStorageKind = "restart"
	agentRestartStorageSession agentRestartStorageKind = "reset_session_restart"
	agentRestartStorageFull    agentRestartStorageKind = "full_reset_restart"
)

type AgentRestartMode string

const (
	agentRestartModeRestart AgentRestartMode = "restart"
	agentRestartModeSession AgentRestartMode = "session"
	agentRestartModeFull    AgentRestartMode = "full"
)

const (
	agentRestartRunning   = "running"
	agentRestartSucceeded = "succeeded"
	agentRestartFailed    = "failed"
)

type AgentRestartOperation struct {
	ID          string                  `json:"id"`
	AgentID     string                  `json:"agent_id"`
	RuntimeID   *string                 `json:"runtime_id"`
	Mode        AgentRestartMode        `json:"mode"`
	storageKind agentRestartStorageKind `json:"-"`
	Status      string                  `json:"status"`
	Step        string                  `json:"step,omitempty"`
	ReasonCode  string                  `json:"reason_code,omitempty"`
	CreatedAt   string                  `json:"created_at"`
	StartedAt   *string                 `json:"started_at"`
	FinishedAt  *string                 `json:"finished_at"`
}

type AgentRestartPreflight struct {
	Actions map[AgentRestartMode]AgentRestartModePreflight `json:"actions"`
	// ProviderCapabilities is the FE-facing projection of
	// agent.ProviderCapabilities for this agent's runtime provider.
	// Gate the restart button on provider_capabilities.force_restart — do
	// not hardcode a provider allow-list. Older servers omit the object;
	// treat missing as all-false.
	ProviderCapabilities ProviderCapabilitiesWire `json:"provider_capabilities"`
}

type AgentRestartModePreflight struct {
	Supported      bool   `json:"supported"`
	DisabledReason string `json:"disabled_reason,omitempty"`
}

type createAgentRestartRequest struct {
	Mode AgentRestartMode `json:"mode"`
}

type agentLifecycleRequestError struct {
	status  int
	message string
}

func writeAgentLifecycleRequestError(w http.ResponseWriter, err *agentLifecycleRequestError) {
	writeError(w, err.status, err.message)
}

// StartAgent allocates a fresh immutable launch and dispatch identity before
// asking the current WorkspaceDaemon to start it. Reusing a failed dispatch
// would only replay Raft's original acceptance receipt without spawning.
func (h *Handler) StartAgent(w http.ResponseWriter, r *http.Request) {
	agent, ok := h.loadAgentRestartTarget(w, r)
	if !ok {
		return
	}
	restarts := h.restarts()
	restarts.lifecycleMu.Lock()
	defer restarts.lifecycleMu.Unlock()
	if err := h.startAgent(r.Context(), agent); err != nil {
		writeAgentLifecycleRequestError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "starting"})
}

func (h *Handler) startAgent(ctx context.Context, agent db.Agent) *agentLifecycleRequestError {
	if !agent.RuntimeID.Valid {
		return &agentLifecycleRequestError{status: http.StatusConflict, message: "agent_runtime_missing"}
	}
	runtime, err := h.Queries.GetAgentRuntimeForWorkspace(ctx, db.GetAgentRuntimeForWorkspaceParams{
		ID: agent.RuntimeID, WorkspaceID: agent.WorkspaceID,
	})
	if err != nil {
		return &agentLifecycleRequestError{status: http.StatusConflict, message: "agent_runtime_missing"}
	}
	if !runtime.DaemonID.Valid {
		return &agentLifecycleRequestError{status: http.StatusConflict, message: "agent_runtime_offline"}
	}
	launchID := uuid.NewString()
	dispatchID := uuid.NewString()
	var sessionID string
	err = h.DB.QueryRow(ctx, `
		UPDATE agent_runner_launch_projection
		SET launch_id = $1, start_dispatch_id = $2, updated_at = now()
		WHERE workspace_id = $3 AND agent_id = $4 AND runtime_id = $5
		RETURNING COALESCE(provider_session_id, '')
	`, launchID, dispatchID, agent.WorkspaceID, agent.ID, runtime.ID).Scan(&sessionID)
	if err != nil {
		return &agentLifecycleRequestError{status: http.StatusConflict, message: "agent_launch_unavailable"}
	}
	h.restarts().finish(uuidToString(agent.ID))
	if h.AgentRestartNotifier == nil || !h.AgentRestartNotifier.NotifyAgentRestartCommand(
		uuidToString(agent.WorkspaceID), runtime.DaemonID.String, protocol.EventDaemonAgentStart, dispatchID,
		protocol.WorkspaceDaemonAgentStartPayload{
			AgentID: uuidToString(agent.ID), RuntimeID: uuidToString(runtime.ID), LaunchID: launchID, StartDispatchID: dispatchID,
			Config: protocol.WorkspaceDaemonAgentStartConfig{SessionID: sessionID},
		},
	) {
		// The desired launch and immutable dispatch are already recorded.
		// Reconnect reconciliation replays that exact Start; it must not turn an
		// unavailable first delivery into manual Stop intent.
		return nil
	}
	return nil
}

func (h *Handler) StopAgent(w http.ResponseWriter, r *http.Request) {
	agent, ok := h.loadAgentRestartTarget(w, r)
	if !ok {
		return
	}
	restarts := h.restarts()
	restarts.lifecycleMu.Lock()
	defer restarts.lifecycleMu.Unlock()
	if err := h.stopAgent(r.Context(), agent); err != nil {
		writeAgentLifecycleRequestError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "stopping"})
}

func (h *Handler) stopAgent(ctx context.Context, agent db.Agent) *agentLifecycleRequestError {
	workspaceID := uuidToString(agent.WorkspaceID)
	agentID := uuidToString(agent.ID)
	if !agent.RuntimeID.Valid {
		return &agentLifecycleRequestError{status: http.StatusConflict, message: "agent_runtime_missing"}
	}
	runtime, err := h.Queries.GetAgentRuntimeForWorkspace(ctx, db.GetAgentRuntimeForWorkspaceParams{
		ID: agent.RuntimeID, WorkspaceID: agent.WorkspaceID,
	})
	if err != nil || !runtime.DaemonID.Valid {
		return &agentLifecycleRequestError{status: http.StatusConflict, message: "agent_runtime_offline"}
	}
	var launchID string
	if restart, active := h.restarts().get(agentID); active && restart.workspaceID == workspaceID &&
		restart.runtimeID == uuidToString(runtime.ID) && restart.computerID == runtime.DaemonID.String {
		if restart.step == agentRestartStepStarting {
			launchID = restart.startLaunchID
		} else {
			launchID = restart.stopLaunchID
		}
		h.restarts().finish(agentID)
	}
	if launchID == "" {
		err = h.DB.QueryRow(ctx, `
			SELECT launch_id::text FROM agent_runner_launch_projection
			WHERE workspace_id = $1 AND agent_id = $2 AND runtime_id = $3
		`, agent.WorkspaceID, agent.ID, runtime.ID).Scan(&launchID)
		if err != nil || launchID == "" {
			return &agentLifecycleRequestError{status: http.StatusConflict, message: "agent_launch_unavailable"}
		}
	}
	if h.AgentRestartNotifier == nil || !h.AgentRestartNotifier.NotifyAgentRestartCommand(
		workspaceID, runtime.DaemonID.String, protocol.EventDaemonAgentStop, launchID,
		protocol.WorkspaceDaemonAgentStopPayload{AgentID: agentID, LaunchID: launchID},
	) {
		return &agentLifecycleRequestError{status: http.StatusConflict, message: "agent_runtime_offline"}
	}
	return nil
}

// GetAgentRestart returns the server-authoritative capability for Raft's
// three restart modes. Clients must not infer it from presentation state.
func (h *Handler) GetAgentRestart(w http.ResponseWriter, r *http.Request) {
	agent, ok := h.loadAgentRestartTarget(w, r)
	if !ok {
		return
	}
	preflight, err := h.agentRestartPreflight(r.Context(), agent)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load Agent restart state")
		return
	}
	writeJSON(w, http.StatusOK, preflight)
}

// ResetAgent starts one live-socket restart/reset. The client supplies no
// runtime or filesystem path. Those bindings are always resolved from the
// current Agent row.
func (h *Handler) ResetAgent(w http.ResponseWriter, r *http.Request) {
	agent, ok := h.loadAgentRestartTarget(w, r)
	if !ok {
		return
	}
	restarts := h.restarts()
	restarts.lifecycleMu.Lock()
	defer restarts.lifecycleMu.Unlock()
	var req createAgentRestartRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	storageKind, validMode := agentRestartStorageForMode(req.Mode)
	if !validMode {
		writeError(w, http.StatusBadRequest, "invalid mode")
		return
	}
	operation, requestErr := h.resetAgent(r.Context(), agent, req.Mode, storageKind)
	if requestErr != nil {
		writeAgentLifecycleRequestError(w, requestErr)
		return
	}
	writeJSON(w, http.StatusAccepted, operation)
}

func (h *Handler) resetAgent(ctx context.Context, agent db.Agent, mode AgentRestartMode, storageKind agentRestartStorageKind) (AgentRestartOperation, *agentLifecycleRequestError) {
	runtime, supported, reason, err := h.agentRestartRuntimeSupport(ctx, agent)
	if err != nil {
		return AgentRestartOperation{}, &agentLifecycleRequestError{status: http.StatusInternalServerError, message: "failed to start Agent restart"}
	}
	if !supported {
		return AgentRestartOperation{}, &agentLifecycleRequestError{status: http.StatusConflict, message: reason}
	}
	if mode == agentRestartModeFull &&
		!workspaceDaemonResetCapabilityPresent(runtimeCapabilities(runtimeMetadata(runtime))) {
		return AgentRestartOperation{}, &agentLifecycleRequestError{status: http.StatusConflict, message: "unsupported_runtime_capability"}
	}
	if !runtime.DaemonID.Valid {
		return AgentRestartOperation{}, &agentLifecycleRequestError{status: http.StatusConflict, message: "current WorkspaceDaemon unavailable during Agent restart operation"}
	}
	now := time.Now().UTC()
	state, accepted := h.restarts().begin(activeAgentRestartState{
		operationID: uuid.NewString(),
		workspaceID: uuidToString(agent.WorkspaceID),
		agentID:     uuidToString(agent.ID),
		runtimeID:   uuidToString(runtime.ID),
		computerID:  runtime.DaemonID.String,
		storageKind: storageKind,
		step:        agentRestartStepStopping,
	})
	if !accepted {
		return AgentRestartOperation{}, &agentLifecycleRequestError{status: http.StatusConflict, message: "an Agent restart is already active"}
	}
	if err := h.beginAgentRestartOperation(ctx, state); err != nil {
		slog.Warn(
			"Agent Restart command not yet delivered",
			"workspace_id", state.workspaceID,
			"computer_id", state.computerID,
			"runtime_id", state.runtimeID,
			"agent_id", state.agentID,
			"operation_id", state.operationID,
			"mode", mode,
			"step", state.step,
			"error", err,
		)
	}
	startedAt := now.Format(time.RFC3339Nano)
	return AgentRestartOperation{
		ID:        state.operationID,
		AgentID:   state.agentID,
		RuntimeID: uuidToPtr(runtime.ID),
		Mode:      mode,
		Status:    agentRestartRunning,
		Step:      state.step,
		CreatedAt: startedAt,
		StartedAt: &startedAt,
	}, nil
}

func (h *Handler) loadAgentRestartTarget(w http.ResponseWriter, r *http.Request) (db.Agent, bool) {
	agentID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "agent_id")
	if !ok {
		return db.Agent{}, false
	}
	agent, err := h.Queries.GetAgent(r.Context(), agentID)
	if err != nil || agent.ArchivedAt.Valid {
		writeError(w, http.StatusNotFound, "agent not found")
		return db.Agent{}, false
	}
	if _, ok := h.requireWorkspaceRole(
		w, r, uuidToString(agent.WorkspaceID), "agent not found", "owner", "admin",
	); !ok {
		return db.Agent{}, false
	}
	return agent, true
}

func (h *Handler) agentRestartPreflight(ctx context.Context, target db.Agent) (AgentRestartPreflight, error) {
	runtime, supported, reason, err := h.agentRestartRuntimeSupport(ctx, target)
	if err != nil {
		return AgentRestartPreflight{}, err
	}
	actions := make(map[AgentRestartMode]AgentRestartModePreflight, 3)
	resetWorkspaceSupported := supported && workspaceDaemonResetCapabilityPresent(runtimeCapabilities(runtimeMetadata(runtime)))
	for _, mode := range []AgentRestartMode{
		agentRestartModeRestart,
		agentRestartModeSession,
		agentRestartModeFull,
	} {
		actionSupported, actionReason := supported, reason
		if mode == agentRestartModeFull && supported && !resetWorkspaceSupported {
			actionSupported = false
			actionReason = "unsupported_runtime_capability"
		}
		actions[mode] = AgentRestartModePreflight{
			Supported:      actionSupported,
			DisabledReason: actionReason,
		}
	}
	return AgentRestartPreflight{
		Actions:              actions,
		ProviderCapabilities: providerCapabilitiesWire(runtime.Provider),
	}, nil
}

func (h *Handler) agentRestartRuntimeSupport(ctx context.Context, agent db.Agent) (db.AgentRuntime, bool, string, error) {
	if !agent.RuntimeID.Valid {
		return db.AgentRuntime{}, false, "agent_runtime_missing", nil
	}
	runtime, err := h.Queries.GetAgentRuntimeForWorkspace(ctx, db.GetAgentRuntimeForWorkspaceParams{
		ID:          agent.RuntimeID,
		WorkspaceID: agent.WorkspaceID,
	})
	if err != nil {
		if isNotFound(err) {
			return db.AgentRuntime{}, false, "agent_runtime_missing", nil
		}
		return db.AgentRuntime{}, false, "", err
	}
	if runtime.Status != "online" ||
		!runtime.LastSeenAt.Valid ||
		time.Since(runtime.LastSeenAt.Time) >= agentHealthStaleThreshold {
		return runtime, false, "agent_runtime_offline", nil
	}
	if !workspaceDaemonAgentProcessCapabilityPresent(runtimeCapabilities(runtimeMetadata(runtime))) {
		return runtime, false, "unsupported_runtime_capability", nil
	}
	return runtime, true, "", nil
}

func agentRestartStorageForMode(mode AgentRestartMode) (agentRestartStorageKind, bool) {
	switch mode {
	case agentRestartModeRestart:
		return agentRestartStorageRestart, true
	case agentRestartModeSession:
		return agentRestartStorageSession, true
	case agentRestartModeFull:
		return agentRestartStorageFull, true
	default:
		return "", false
	}
}

func agentRestartModeForStorage(action agentRestartStorageKind) AgentRestartMode {
	switch action {
	case agentRestartStorageSession:
		return agentRestartModeSession
	case agentRestartStorageFull:
		return agentRestartModeFull
	default:
		return agentRestartModeRestart
	}
}

func workspaceDaemonAgentProcessCapabilityPresent(capabilities []string) bool {
	for _, capability := range capabilities {
		if capability == protocol.DaemonCapabilityWorkspaceDaemonAgentProcess {
			return true
		}
	}
	return false
}

func workspaceDaemonResetCapabilityPresent(capabilities []string) bool {
	for _, capability := range capabilities {
		if capability == protocol.DaemonCapabilityWorkspaceDaemonAgentReset {
			return true
		}
	}
	return false
}
