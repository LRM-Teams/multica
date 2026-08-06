package handler

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type AgentSkillSuggestionResponse struct {
	ID               string          `json:"id"`
	WorkspaceID      string          `json:"workspace_id"`
	AgentID          string          `json:"agent_id"`
	SkillID          string          `json:"skill_id"`
	Action           string          `json:"action"`
	Reason           string          `json:"reason"`
	MatcherScore     float64         `json:"matcher_score"`
	MatcherDetails   json.RawMessage `json:"matcher_details,omitempty"`
	Status           string          `json:"status"`
	SkillName        string          `json:"skill_name"`
	SkillDescription string          `json:"skill_description"`
	CreatedAt        string          `json:"created_at"`
	UpdatedAt        string          `json:"updated_at"`
}

type agentSkillSuggestionDecisionRequest struct {
	Decision string `json:"decision"`
}

func (h *Handler) ListAgentSkillSuggestions(w http.ResponseWriter, r *http.Request) {
	agent, ok := h.loadAgentForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	if !h.canManageAgent(w, r, agent) {
		return
	}
	rows, err := h.Queries.ListPendingAgentSkillSuggestionsByAgent(r.Context(), db.ListPendingAgentSkillSuggestionsByAgentParams{
		AgentID:     agent.ID,
		WorkspaceID: agent.WorkspaceID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list skill suggestions")
		return
	}
	items := make([]AgentSkillSuggestionResponse, 0, len(rows))
	for _, row := range rows {
		items = append(items, agentSkillSuggestionResponse(row))
	}
	writeJSON(w, http.StatusOK, map[string]any{"suggestions": items})
}

func (h *Handler) DecideAgentSkillSuggestion(w http.ResponseWriter, r *http.Request) {
	agent, ok := h.loadAgentForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	if !h.canManageAgent(w, r, agent) {
		return
	}
	var req agentSkillSuggestionDecisionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	decision := strings.TrimSpace(req.Decision)
	if decision != "accept" && decision != "dismiss" {
		writeError(w, http.StatusBadRequest, "invalid decision")
		return
	}
	suggestionID, ok := parseSuggestionIDOrBadRequest(w, chi.URLParam(r, "suggestionId"))
	if !ok {
		return
	}
	svc := service.NewEvolutionService(h.Queries)
	var err error
	switch decision {
	case "accept":
		err = svc.AcceptAgentSkillSuggestion(r.Context(), agent.WorkspaceID, agent.ID, suggestionID)
	case "dismiss":
		err = svc.DismissAgentSkillSuggestion(r.Context(), agent.WorkspaceID, agent.ID, suggestionID)
	}
	if err != nil {
		if err == pgx.ErrNoRows {
			writeError(w, http.StatusNotFound, "suggestion not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to update skill suggestion")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": decision})
}

func (h *Handler) refreshAgentSkillSuggestions(ctx context.Context, agent db.Agent) {
	if err := service.NewEvolutionService(h.Queries).RefreshAgentSkillSuggestions(ctx, agent); err != nil {
		slog.Warn("refresh agent skill suggestions failed",
			"agent_id", uuidToString(agent.ID),
			"workspace_id", uuidToString(agent.WorkspaceID),
			"error", err,
		)
	}
}

func agentSkillSuggestionResponse(row db.ListPendingAgentSkillSuggestionsByAgentRow) AgentSkillSuggestionResponse {
	return AgentSkillSuggestionResponse{
		ID:               uuidToString(row.ID),
		WorkspaceID:      uuidToString(row.WorkspaceID),
		AgentID:          uuidToString(row.AgentID),
		SkillID:          uuidToString(row.SkillID),
		Action:           row.Action,
		Reason:           row.Reason,
		MatcherScore:     row.MatcherScore,
		MatcherDetails:   json.RawMessage(row.MatcherDetails),
		Status:           row.Status,
		SkillName:        row.SkillName,
		SkillDescription: row.SkillDescription,
		CreatedAt:        timestampToString(row.CreatedAt),
		UpdatedAt:        timestampToString(row.UpdatedAt),
	}
}

func parseSuggestionIDOrBadRequest(w http.ResponseWriter, raw string) (pgtype.UUID, bool) {
	id, err := util.ParseUUID(strings.TrimSpace(raw))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid suggestion id")
		return pgtype.UUID{}, false
	}
	return id, true
}
