package handler

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
)

// Agent research data-plane (LRM-904 / #801).
//
// Research Fleet CLI runs under mat_* in daemon tasks. Human /api/research/*
// is fail-closed by RejectAgentOnHumanAPI; these dedicated routes remount the
// same handlers under /api/agent/research/* after RequireAgentPrincipal.

func (h *Handler) GetAgentResearchFleet(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAgentPrincipal(w, r); !ok {
		return
	}
	h.GetResearchFleet(w, r)
}

func (h *Handler) HireAgentResearchFleetMember(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAgentPrincipal(w, r); !ok {
		return
	}
	h.HireResearchFleetMember(w, r)
}

func (h *Handler) OptimizeAgentResearchFleetMember(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAgentPrincipal(w, r); !ok {
		return
	}
	h.OptimizeResearchFleetMember(w, r)
}

func (h *Handler) ArchiveAgentResearchFleetMember(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAgentPrincipal(w, r); !ok {
		return
	}
	h.ArchiveResearchFleetMemberHandler(w, r)
}

func (h *Handler) GetAgentResearchSessionSnapshot(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.requireAgentPrincipal(w, r)
	if !ok {
		return
	}
	workspaceID, valid := parseUUIDOrBadRequest(w, principal.WorkspaceID, "workspace_id")
	if !valid {
		return
	}
	if _, active := h.requireActiveFleetMember(w, r, workspaceID); !active {
		return
	}
	attemptID := strings.TrimSpace(r.URL.Query().Get("attempt_id"))
	if attemptID == "" {
		writeError(w, http.StatusBadRequest, "attempt_id is required")
		return
	}
	if _, valid = parseUUIDOrBadRequest(w, attemptID, "attempt_id"); !valid {
		return
	}
	sessionID := strings.TrimSpace(chi.URLParam(r, "id"))
	if _, valid = parseUUIDOrBadRequest(w, sessionID, "id"); !valid {
		return
	}
	var authorized bool
	if err := h.DB.QueryRow(r.Context(), `
		SELECT EXISTS (
		  SELECT 1
		  FROM research_task_attempt
		  WHERE workspace_id = $1::uuid
		    AND session_id = $2::uuid
		    AND id = $3::uuid
		    AND assigned_agent_id = $4::uuid
		)
	`, principal.WorkspaceID, sessionID, attemptID, principal.AgentID).Scan(&authorized); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to authorize research attempt")
		return
	}
	if !authorized {
		writeError(w, http.StatusNotFound, "research attempt not found")
		return
	}
	h.getResearchSessionSnapshot(w, r, true)
}

func (h *Handler) AppendAgentResearchGraphNode(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAgentPrincipal(w, r); !ok {
		return
	}
	h.AppendResearchGraphNode(w, r)
}

func (h *Handler) UpsertAgentResearchSource(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAgentPrincipal(w, r); !ok {
		return
	}
	h.UpsertResearchSourceHandler(w, r)
}

func (h *Handler) PatchAgentResearchReport(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAgentPrincipal(w, r); !ok {
		return
	}
	h.PatchResearchReport(w, r)
}

func (h *Handler) PostAgentResearchPresence(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAgentPrincipal(w, r); !ok {
		return
	}
	h.PostResearchPresence(w, r)
}

func (h *Handler) RequestAgentResearchStageEval(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAgentPrincipal(w, r); !ok {
		return
	}
	h.RequestResearchStageEval(w, r)
}

func (h *Handler) PostAgentResearchMessage(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAgentPrincipal(w, r); !ok {
		return
	}
	h.PostResearchMessage(w, r)
}
