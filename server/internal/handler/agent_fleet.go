package handler

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/multica-ai/multica/server/internal/service"
)

func (h *Handler) GetAgentFleetRankRules(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	if _, ok := h.workspaceMember(w, r, workspaceID); !ok {
		return
	}
	rules, err := h.AgentFleetRankService.GetRulesDocument(r.Context(), parseUUID(workspaceID))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load fleet rank rules")
		return
	}
	writeJSON(w, http.StatusOK, rules)
}

func (h *Handler) GetAgentFleetRankings(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	member, ok := h.workspaceMember(w, r, workspaceID)
	if !ok {
		return
	}

	rows, err := h.AgentFleetRankService.ListRankings(r.Context(), parseUUID(workspaceID))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list fleet rankings")
		return
	}

	actorType, actorID := h.resolveActor(r, requestUserID(r), workspaceID)
	allowed, ok := h.accessibleAgentIDs(r.Context(), workspaceID, actorType, actorID, member.Role)
	if !ok {
		writeError(w, http.StatusInternalServerError, "failed to resolve agent access")
		return
	}

	resp := make([]serviceAgentFleetRankResponse, 0, len(rows))
	for _, row := range rows {
		if _, ok := allowed[row.AgentID]; !ok {
			continue
		}
		resp = append(resp, toAgentFleetRankResponse(row))
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) GetAgentFleetRank(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	agent, ok := h.loadAgentForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}

	row, err := h.AgentFleetRankService.GetAgentRank(r.Context(), agent.WorkspaceID, agent.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get fleet rank")
		return
	}
	_ = workspaceID
	writeJSON(w, http.StatusOK, toAgentFleetRankResponse(row))
}

type serviceAgentFleetRankResponse struct {
	AgentID          string                     `json:"agent_id"`
	FleetScore       float64                    `json:"fleet_score"`
	ClassID          string                     `json:"class_id"`
	ClassLabel       string                     `json:"class_label"`
	FleetRank        int                        `json:"fleet_rank"`
	FleetSize        int                        `json:"fleet_size"`
	SampleTasks      int                        `json:"sample_tasks"`
	MinSampleTasks   int                        `json:"min_sample_tasks"`
	SampleSufficient bool                       `json:"sample_sufficient"`
	Frozen           bool                       `json:"frozen"`
	Pillars          serviceFleetPillarResponse `json:"pillars"`
}

type serviceFleetPillarResponse struct {
	Delivery   float64 `json:"delivery"`
	Evolution  float64 `json:"evolution"`
	Growth     float64 `json:"growth"`
	Efficiency float64 `json:"efficiency"`
}

func toAgentFleetRankResponse(row service.AgentFleetRankView) serviceAgentFleetRankResponse {
	return serviceAgentFleetRankResponse{
		AgentID:          row.AgentID,
		FleetScore:       row.FleetScore,
		ClassID:          row.ClassID,
		ClassLabel:       row.ClassLabel,
		FleetRank:        row.FleetRank,
		FleetSize:        row.FleetSize,
		SampleTasks:      row.SampleTasks,
		MinSampleTasks:   row.MinSampleTasks,
		SampleSufficient: row.SampleSufficient,
		Frozen:           row.Frozen,
		Pillars: serviceFleetPillarResponse{
			Delivery:   row.Pillars.Delivery,
			Evolution:  row.Pillars.Evolution,
			Growth:     row.Pillars.Growth,
			Efficiency: row.Pillars.Efficiency,
		},
	}
}
