package handler

import (
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type agentResearchFleetOverviewSession struct {
	ID           string `json:"id"`
	Status       string `json:"status"`
	CurrentStage string `json:"current_stage"`
}

type agentResearchFleetOverview struct {
	Session agentResearchFleetOverviewSession `json:"session"`
	Fleet   []ResearchFleetPreviewMember      `json:"fleet"`
}

const agentResearchAttemptAuthorizationQuery = `
	SELECT a.task_id::text, COALESCE(a.inbox_task_id::text, '')
	FROM research_task_attempt a
	JOIN research_session s
	  ON s.workspace_id = a.workspace_id
	 AND s.id = a.session_id
	JOIN research_fleet_member fm
	  ON fm.workspace_id = s.workspace_id
	 AND fm.fleet_id = s.fleet_id
	 AND fm.agent_id = a.assigned_agent_id
	 AND fm.status = 'active'
	WHERE a.workspace_id = $1::uuid
	  AND a.session_id = $2::uuid
	  AND a.id = $3::uuid
	  AND a.assigned_agent_id = $4::uuid
`

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
		h.getAgentResearchFleetOverview(w, r, workspaceID)
		return
	}
	if _, valid = parseUUIDOrBadRequest(w, attemptID, "attempt_id"); !valid {
		return
	}
	sessionID := strings.TrimSpace(chi.URLParam(r, "id"))
	if _, valid = parseUUIDOrBadRequest(w, sessionID, "id"); !valid {
		return
	}
	var taskID, expectedInboxTaskID string
	if err := h.DB.QueryRow(
		r.Context(), agentResearchAttemptAuthorizationQuery,
		principal.WorkspaceID, sessionID, attemptID, principal.AgentID,
	).Scan(&taskID, &expectedInboxTaskID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "research attempt not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to authorize research attempt")
		return
	}
	boundInboxTaskID, bound := h.resolveResearchAttemptInboxTaskID(w, r, principal, sessionID, taskID, attemptID)
	if !bound {
		return
	}
	if !researchAttemptCredentialMatches(expectedInboxTaskID, boundInboxTaskID) {
		writeError(w, http.StatusNotFound, "research attempt not found")
		return
	}
	h.getResearchSessionSnapshot(w, r, true)
}

func researchAttemptCredentialMatches(expectedInboxTaskID, boundInboxTaskID string) bool {
	expectedInboxTaskID = strings.TrimSpace(expectedInboxTaskID)
	boundInboxTaskID = strings.TrimSpace(boundInboxTaskID)
	return expectedInboxTaskID != "" && expectedInboxTaskID == boundInboxTaskID
}

func (h *Handler) getAgentResearchFleetOverview(w http.ResponseWriter, r *http.Request, workspaceID pgtype.UUID) {
	sessionID, ok := parseUUIDOrBadRequest(w, strings.TrimSpace(chi.URLParam(r, "id")), "id")
	if !ok {
		return
	}
	session, err := h.Queries.GetResearchSession(r.Context(), db.GetResearchSessionParams{
		ID: sessionID, WorkspaceID: workspaceID,
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "research session not found")
		return
	}
	fleet, err := h.Queries.GetResearchFleetByWorkspace(r.Context(), workspaceID)
	if err != nil || fleet.ID != session.FleetID {
		writeError(w, http.StatusNotFound, "research session not found")
		return
	}
	members, err := h.Queries.ListResearchFleetMembers(r.Context(), db.ListResearchFleetMembersParams{
		FleetID: fleet.ID, WorkspaceID: workspaceID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load research fleet overview")
		return
	}
	writeJSON(w, http.StatusOK, agentResearchFleetOverview{
		Session: agentResearchFleetOverviewSession{
			ID: uuidToString(session.ID), Status: session.Status, CurrentStage: session.CurrentStage,
		},
		Fleet: h.researchFleetPreview(r.Context(), fleet, members, 8),
	})
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
