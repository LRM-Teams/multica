package handler

import (
	"net/http"
)

// Agent issue data-plane (#801).
//
// Dedicated /api/agent/issues/* routes require AgentPrincipal. Shared issue
// loaders called from these paths use principal.WorkspaceID (never owner
// channel/workspace membership for surface ACL). Human URLs remain fail-closed
// via rejectAgentOnHumanRoute.

func (h *Handler) GetAgentIssue(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAgentPrincipal(w, r); !ok {
		return
	}
	h.GetIssue(w, r)
}

func (h *Handler) CreateAgentIssue(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAgentPrincipal(w, r); !ok {
		return
	}
	// CreateIssue uses resolveActor → agent creator under mat_* auth.
	h.CreateIssue(w, r)
}

func (h *Handler) UpdateAgentIssue(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAgentPrincipal(w, r); !ok {
		return
	}
	h.UpdateIssue(w, r)
}

func (h *Handler) ListAgentIssueComments(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAgentPrincipal(w, r); !ok {
		return
	}
	h.ListComments(w, r)
}

func (h *Handler) CreateAgentIssueComment(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAgentPrincipal(w, r); !ok {
		return
	}
	h.CreateComment(w, r)
}

func (h *Handler) ListAgentIssues(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAgentPrincipal(w, r); !ok {
		return
	}
	h.ListIssues(w, r)
}

func (h *Handler) ListAgentIssueMetadata(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAgentPrincipal(w, r); !ok {
		return
	}
	h.ListIssueMetadata(w, r)
}

func (h *Handler) SetAgentIssueMetadataKey(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAgentPrincipal(w, r); !ok {
		return
	}
	h.SetIssueMetadataKey(w, r)
}

func (h *Handler) DeleteAgentIssueMetadataKey(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAgentPrincipal(w, r); !ok {
		return
	}
	h.DeleteIssueMetadataKey(w, r)
}
