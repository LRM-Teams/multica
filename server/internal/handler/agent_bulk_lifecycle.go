package handler

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/middleware"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type bulkAgentLifecycleAction string

const (
	bulkAgentLifecycleStart bulkAgentLifecycleAction = "start"
	bulkAgentLifecycleStop  bulkAgentLifecycleAction = "stop"
	bulkAgentLifecycleReset bulkAgentLifecycleAction = "reset"
)

type bulkAgentLifecycleRequest struct {
	AgentIDs []string                 `json:"agent_ids"`
	Action   bulkAgentLifecycleAction `json:"action"`
	Mode     AgentRestartMode         `json:"mode,omitempty"`
}

type bulkAgentLifecycleResult struct {
	AgentID   string                 `json:"agent_id"`
	Accepted  bool                   `json:"accepted"`
	Status    string                 `json:"status,omitempty"`
	Error     string                 `json:"error,omitempty"`
	Operation *AgentRestartOperation `json:"operation,omitempty"`
}

type bulkAgentLifecycleResponse struct {
	Results []bulkAgentLifecycleResult `json:"results"`
}

// BulkAgentLifecycle applies one lifecycle action to multiple Agents through
// one API request. Identity, workspace membership, and role checks complete
// before any command is dispatched. Per-Agent delivery is explicit in the
// response because daemon commands cannot be rolled back after acceptance.
func (h *Handler) BulkAgentLifecycle(w http.ResponseWriter, r *http.Request) {
	if _, ok := middleware.AgentPrincipalFromContext(r.Context()); ok {
		writeError(w, http.StatusForbidden, "agent principals cannot manage agent lifecycle")
		return
	}
	var req bulkAgentLifecycleRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(req.AgentIDs) == 0 || len(req.AgentIDs) > maxBulkAgentTargets {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("agent_ids must contain between 1 and %d agents", maxBulkAgentTargets))
		return
	}
	storageKind, validMode := agentRestartStorageForMode(req.Mode)
	switch req.Action {
	case bulkAgentLifecycleStart, bulkAgentLifecycleStop:
		if req.Mode != "" {
			writeError(w, http.StatusBadRequest, "mode is only valid for reset actions")
			return
		}
	case bulkAgentLifecycleReset:
		if !validMode {
			writeError(w, http.StatusBadRequest, "invalid mode")
			return
		}
	default:
		writeError(w, http.StatusBadRequest, "invalid action")
		return
	}

	workspaceID := h.resolveWorkspaceID(r)
	workspaceUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
	if !ok {
		return
	}
	if _, ok := h.requireWorkspaceRole(
		w, r, workspaceID, "one or more agents were not found", "owner", "admin",
	); !ok {
		return
	}
	targets, ok := h.loadBulkAgentLifecycleTargets(w, r, workspaceUUID, req.AgentIDs)
	if !ok {
		return
	}

	restarts := h.restarts()
	restarts.lifecycleMu.Lock()
	defer restarts.lifecycleMu.Unlock()
	results := make([]bulkAgentLifecycleResult, 0, len(targets))
	for _, target := range targets {
		result := bulkAgentLifecycleResult{AgentID: uuidToString(target.ID)}
		switch req.Action {
		case bulkAgentLifecycleStart:
			if requestErr := h.startAgent(r.Context(), target); requestErr != nil {
				result.Error = requestErr.message
			} else {
				result.Accepted = true
				result.Status = "starting"
			}
		case bulkAgentLifecycleStop:
			if requestErr := h.stopAgent(r.Context(), target); requestErr != nil {
				result.Error = requestErr.message
			} else {
				result.Accepted = true
				result.Status = "stopping"
			}
		case bulkAgentLifecycleReset:
			operation, requestErr := h.resetAgent(r.Context(), target, req.Mode, storageKind)
			if requestErr != nil {
				result.Error = requestErr.message
			} else {
				result.Accepted = true
				result.Status = operation.Status
				result.Operation = &operation
			}
		}
		results = append(results, result)
	}
	writeJSON(w, http.StatusAccepted, bulkAgentLifecycleResponse{Results: results})
}

func (h *Handler) loadBulkAgentLifecycleTargets(w http.ResponseWriter, r *http.Request, workspaceID pgtype.UUID, rawIDs []string) ([]db.Agent, bool) {
	ids := make([]string, 0, len(rawIDs))
	seen := make(map[string]struct{}, len(rawIDs))
	for _, rawID := range rawIDs {
		id, ok := parseUUIDOrBadRequest(w, rawID, "agent_id")
		if !ok {
			return nil, false
		}
		canonicalID := uuidToString(id)
		if _, duplicate := seen[canonicalID]; duplicate {
			writeError(w, http.StatusBadRequest, "agent_ids must not contain duplicates")
			return nil, false
		}
		seen[canonicalID] = struct{}{}
		ids = append(ids, canonicalID)
	}
	agents, err := h.Queries.ListAgents(r.Context(), workspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load agents")
		return nil, false
	}
	byID := make(map[string]db.Agent, len(agents))
	for _, candidate := range agents {
		if !candidate.ArchivedAt.Valid {
			byID[uuidToString(candidate.ID)] = candidate
		}
	}
	targets := make([]db.Agent, 0, len(ids))
	for _, id := range ids {
		target, found := byID[id]
		if !found {
			writeError(w, http.StatusNotFound, "one or more agents were not found")
			return nil, false
		}
		targets = append(targets, target)
	}
	return targets, true
}
