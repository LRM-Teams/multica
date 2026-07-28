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

// --- necessary batch: labels / subscribers / runs / channel ---

func (h *Handler) ListAgentIssueLabels(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAgentPrincipal(w, r); !ok {
		return
	}
	h.ListLabelsForIssue(w, r)
}

func (h *Handler) AttachAgentIssueLabel(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAgentPrincipal(w, r); !ok {
		return
	}
	h.AttachLabel(w, r)
}

func (h *Handler) DetachAgentIssueLabel(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAgentPrincipal(w, r); !ok {
		return
	}
	h.DetachLabel(w, r)
}

func (h *Handler) ListAgentIssueSubscribers(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAgentPrincipal(w, r); !ok {
		return
	}
	h.ListIssueSubscribers(w, r)
}

func (h *Handler) SubscribeAgentToIssue(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAgentPrincipal(w, r); !ok {
		return
	}
	h.SubscribeToIssue(w, r)
}

func (h *Handler) UnsubscribeAgentFromIssue(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAgentPrincipal(w, r); !ok {
		return
	}
	h.UnsubscribeFromIssue(w, r)
}

func (h *Handler) ListAgentIssueTaskRuns(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAgentPrincipal(w, r); !ok {
		return
	}
	h.ListTasksByIssue(w, r)
}

func (h *Handler) RerunAgentIssue(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAgentPrincipal(w, r); !ok {
		return
	}
	h.RerunIssue(w, r)
}

func (h *Handler) SetAgentIssueSourceChannel(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAgentPrincipal(w, r); !ok {
		return
	}
	h.SetIssueSourceChannel(w, r)
}

// ListAgentDirectoryAgents — GET /api/agent/agents
// Directory surface for CLI @-resolve: agents visible in principal workspace.
func (h *Handler) ListAgentDirectoryAgents(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAgentPrincipal(w, r); !ok {
		return
	}
	// ListAgents is workspace-scoped; under /api/agent/* principal workspace applies.
	h.ListAgents(w, r)
}
