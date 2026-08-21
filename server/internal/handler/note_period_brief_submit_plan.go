package handler

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
)

type submitNotePeriodBriefCollectPlanResponse struct {
	DraftPageID string `json:"draft_page_id"`
	Summary     string `json:"summary"`
	Assigned    int    `json:"assigned"`
	Skipped     int    `json:"skipped"`
	Message     string `json:"message"`
}

// SubmitAgentNotePeriodBriefCollectPlan stores the Notes Assistant collect plan
// on the run. Only the synthesizer (planner) for this draft may submit.
// POST /api/agent/notes/period-briefs/{draftPageId}/submit-collect-plan
func (h *Handler) SubmitAgentNotePeriodBriefCollectPlan(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.requireAgentPrincipal(w, r)
	if !ok {
		return
	}
	draftID := chi.URLParam(r, "draftPageId")
	draftUUID, ok := parseUUIDOrBadRequest(w, draftID, "draftPageId")
	if !ok {
		return
	}
	workspaceUUID, ok := parseUUIDOrBadRequest(w, principal.WorkspaceID, "workspace id")
	if !ok {
		return
	}
	agentUUID, ok := parseUUIDOrBadRequest(w, principal.AgentID, "agent id")
	if !ok {
		return
	}

	run, err := h.loadNotePeriodBriefRunByDraft(r.Context(), workspaceUUID, draftUUID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "period brief run not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load period brief run")
		return
	}
	if uuidToString(run.SynthesizerAgentID) != uuidToString(agentUUID) {
		writeError(w, http.StatusForbidden, "only the Period Brief synthesizer for this draft may submit a collect plan")
		return
	}
	if run.Status != "planning" {
		writeError(w, http.StatusConflict, "collect plan can only be submitted while the run is planning")
		return
	}

	plan, ok := readPeriodBriefCollectPlan(w, r)
	if !ok {
		return
	}
	selected := make([]string, 0, len(run.Collectors))
	for _, ref := range run.Collectors {
		if id := strings.TrimSpace(ref.AgentID); id != "" {
			selected = append(selected, id)
		}
	}
	applied := applyNotePeriodBriefCollectPlan(selected, &plan)
	stored := plan
	if applied.Fallback {
		assignments := make([]notePeriodBriefCollectAssignment, 0, len(applied.DispatchIDs))
		for _, id := range applied.DispatchIDs {
			assignments = append(assignments, notePeriodBriefCollectAssignment{CollectorAgentID: id})
		}
		stored = notePeriodBriefCollectPlan{Summary: applied.Summary, Assignments: assignments}
	} else {
		stored.Summary = applied.Summary
	}

	if err := h.updateNotePeriodBriefRunPlan(r.Context(), run.ID, &stored, run.Collectors, "planning"); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to store collect plan")
		return
	}

	assigned := 0
	skipped := 0
	if !applied.Fallback {
		assigned = len(applied.DispatchIDs)
		skipped = len(selected) - assigned
	} else {
		assigned = len(applied.DispatchIDs)
	}

	writeJSON(w, http.StatusOK, submitNotePeriodBriefCollectPlanResponse{
		DraftPageID: draftID,
		Summary:     stored.Summary,
		Assigned:    assigned,
		Skipped:     skipped,
		Message:     "Collect plan stored. Stop and wait — the platform will dispatch collectors.",
	})
}

func readPeriodBriefCollectPlan(w http.ResponseWriter, r *http.Request) (notePeriodBriefCollectPlan, bool) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read body")
		return notePeriodBriefCollectPlan{}, false
	}
	text := strings.TrimSpace(string(body))
	if text == "" {
		writeError(w, http.StatusBadRequest, "collect plan JSON is required")
		return notePeriodBriefCollectPlan{}, false
	}
	var plan notePeriodBriefCollectPlan
	if err := json.Unmarshal(body, &plan); err != nil {
		writeError(w, http.StatusBadRequest, "invalid collect plan JSON")
		return notePeriodBriefCollectPlan{}, false
	}
	return plan, true
}
