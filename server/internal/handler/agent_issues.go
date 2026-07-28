package handler

import (
	"net/http"
)

// Agent issue data-plane (#801). Require AgentPrincipal, then reuse human
// handlers (workspace-scoped issue model). Transitional dual-path still stamps
// X-User-ID=owner for creator fields.

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
