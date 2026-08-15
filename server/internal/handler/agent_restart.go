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
	runtime, supported, reason, err := h.agentRestartRuntimeSupport(r.Context(), agent)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to start Agent restart")
		return
	}
	if !supported {
		writeError(w, http.StatusConflict, reason)
		return
	}
	if req.Mode == agentRestartModeFull &&
		!workspaceRunnerResetCapabilityPresent(runtimeCapabilities(runtimeMetadata(runtime))) {
		writeError(w, http.StatusConflict, "unsupported_runtime_capability")
		return
	}
	if !runtime.DaemonID.Valid {
		writeError(w, http.StatusConflict, "current Workspace Runner unavailable during Agent restart operation")
		return
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
		writeError(w, http.StatusConflict, "an Agent restart is already active")
		return
	}
	if err := h.beginAgentRestartOperation(r.Context(), state); err != nil {
		slog.Warn(
			"Agent Restart command not yet delivered",
			"workspace_id", state.workspaceID,
			"computer_id", state.computerID,
			"runtime_id", state.runtimeID,
			"agent_id", state.agentID,
			"operation_id", state.operationID,
			"mode", req.Mode,
			"step", state.step,
			"error", err,
		)
	}
	startedAt := now.Format(time.RFC3339Nano)
	writeJSON(w, http.StatusAccepted, AgentRestartOperation{
		ID:        state.operationID,
		AgentID:   state.agentID,
		RuntimeID: uuidToPtr(runtime.ID),
		Mode:      req.Mode,
		Status:    agentRestartRunning,
		Step:      state.step,
		CreatedAt: startedAt,
		StartedAt: &startedAt,
	})
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
	resetWorkspaceSupported := supported && workspaceRunnerResetCapabilityPresent(runtimeCapabilities(runtimeMetadata(runtime)))
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
	if !workspaceRunnerAgentProcessCapabilityPresent(runtimeCapabilities(runtimeMetadata(runtime))) {
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

func workspaceRunnerAgentProcessCapabilityPresent(capabilities []string) bool {
	for _, capability := range capabilities {
		if capability == protocol.DaemonCapabilityWorkspaceRunnerAgentProcess {
			return true
		}
	}
	return false
}

func workspaceRunnerResetCapabilityPresent(capabilities []string) bool {
	for _, capability := range capabilities {
		if capability == protocol.DaemonCapabilityWorkspaceRunnerAgentReset {
			return true
		}
	}
	return false
}
