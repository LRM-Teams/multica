package handler

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// AgentRealtimeContract describes a non-Activity scoped realtime refresh.
type AgentRealtimeContract struct {
	Scope     string `json:"scope"`
	ID        string `json:"id"`
	EventType string `json:"event_type"`
	Payload   string `json:"payload"`
}

type agentInternalRequest struct {
	userID string
	agent  db.Agent
}

func (h *Handler) prepareAgentInternalRequest(w http.ResponseWriter, r *http.Request) (agentInternalRequest, bool) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return agentInternalRequest{}, false
	}
	workspaceID := ctxWorkspaceID(r.Context())
	if workspaceID == "" {
		workspaceID = h.resolveWorkspaceID(r)
	}
	if workspaceID == "" {
		writeError(w, http.StatusBadRequest, "workspace_id is required")
		return agentInternalRequest{}, false
	}
	agent, ok := h.loadAgentForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return agentInternalRequest{}, false
	}
	if _, ok := h.workspaceMember(w, r, workspaceID); !ok {
		return agentInternalRequest{}, false
	}
	actorType, actorID := h.resolveActor(r, userID, workspaceID)
	if !h.canAccessAgentInternals(r.Context(), agent, actorType, actorID, workspaceID) {
		writeError(w, http.StatusForbidden, "you do not have access to this agent")
		return agentInternalRequest{}, false
	}
	return agentInternalRequest{userID: userID, agent: agent}, true
}
