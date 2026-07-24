package handler

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type AgentRadarActionResponse struct {
	ID         string `json:"id"`
	Type       string `json:"type"`
	Status     string `json:"status"`
	RiskLevel  string `json:"risk_level"`
	Confidence string `json:"confidence"`
	DedupeKey  string `json:"dedupe_key"`
	Reason     string `json:"reason"`
	CreatedAt  string `json:"created_at"`
	UpdatedAt  string `json:"updated_at"`
}

type AgentRadarRunResponse struct {
	ID             string                     `json:"id"`
	AgentID        string                     `json:"agent_id"`
	Status         string                     `json:"status"`
	TriggerKind    string                     `json:"trigger_kind"`
	TriggerRef     string                     `json:"trigger_ref"`
	ContextSummary string                     `json:"context_summary"`
	Error          string                     `json:"error"`
	ScheduledFor   string                     `json:"scheduled_for"`
	StartedAt      *string                    `json:"started_at"`
	FinishedAt     *string                    `json:"finished_at"`
	CreatedAt      string                     `json:"created_at"`
	Actions        []AgentRadarActionResponse `json:"actions"`
}

type ListAgentRadarRunsResponse struct {
	Runs []AgentRadarRunResponse `json:"runs"`
}

func (h *Handler) ListAgentRadarRuns(w http.ResponseWriter, r *http.Request) {
	agent, ok := h.loadAgentForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	runs, err := h.Queries.ListAgentRadarRunsByAgent(r.Context(), db.ListAgentRadarRunsByAgentParams{
		WorkspaceID: agent.WorkspaceID,
		AgentID:     agent.ID,
		Limit:       20,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list radar runs")
		return
	}
	runIDs := make([]pgtype.UUID, len(runs))
	for i, run := range runs {
		runIDs[i] = run.ID
	}
	actionsByRun := map[string][]db.AgentRadarAction{}
	if len(runIDs) > 0 {
		actions, err := h.Queries.ListAgentRadarActionsByRuns(r.Context(), runIDs)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list radar actions")
			return
		}
		for _, action := range actions {
			key := uuidToString(action.RadarRunID)
			actionsByRun[key] = append(actionsByRun[key], action)
		}
	}
	out := make([]AgentRadarRunResponse, 0, len(runs))
	for _, run := range runs {
		out = append(out, agentRadarRunToResponse(run, actionsByRun[uuidToString(run.ID)]))
	}
	writeJSON(w, http.StatusOK, ListAgentRadarRunsResponse{Runs: out})
}

func agentRadarRunToResponse(run db.AgentRadarRun, actions []db.AgentRadarAction) AgentRadarRunResponse {
	resp := AgentRadarRunResponse{
		ID:             uuidToString(run.ID),
		AgentID:        uuidToString(run.AgentID),
		Status:         run.Status,
		TriggerKind:    run.TriggerKind,
		TriggerRef:     run.TriggerRef,
		ContextSummary: run.ContextSummary,
		Error:          run.Error,
		ScheduledFor:   timestampToString(run.ScheduledFor),
		StartedAt:      timestampToPtr(run.StartedAt),
		FinishedAt:     timestampToPtr(run.FinishedAt),
		CreatedAt:      timestampToString(run.CreatedAt),
		Actions:        make([]AgentRadarActionResponse, 0, len(actions)),
	}
	for _, action := range actions {
		resp.Actions = append(resp.Actions, AgentRadarActionResponse{
			ID:         uuidToString(action.ID),
			Type:       action.ActionType,
			Status:     action.Status,
			RiskLevel:  action.RiskLevel,
			Confidence: action.Confidence,
			DedupeKey:  action.DedupeKey,
			Reason:     action.Reason,
			CreatedAt:  timestampToString(action.CreatedAt),
			UpdatedAt:  timestampToString(action.UpdatedAt),
		})
	}
	return resp
}
