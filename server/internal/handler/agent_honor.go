package handler

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type patchAgentHonorShowcaseRequest struct {
	AchievementIDs []string `json:"achievement_ids"`
	EquippedID     string   `json:"equipped_id"`
}

type agentHonorGrantRequest struct {
	Kind          string `json:"kind"`
	XP            int32  `json:"xp"`
	AchievementID string `json:"achievement_id"`
	Reason        string `json:"reason"`
	GrantID       string `json:"grant_id"`
}

type revokeAgentAchievementRequest struct {
	Reason string `json:"reason"`
}

func (h *Handler) GetAgentHonorRules(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	if _, ok := h.workspaceMember(w, r, workspaceID); !ok {
		return
	}
	view, err := h.AgentHonorService.GetRules(r.Context(), parseUUID(workspaceID))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load agent honor rules")
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (h *Handler) PutAgentHonorRules(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	member, ok := h.requireWorkspaceRole(
		w, r, workspaceID, "workspace not found", "owner", "admin",
	)
	if !ok {
		return
	}
	var rules service.AgentHonorRules
	if err := json.NewDecoder(r.Body).Decode(&rules); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	view, err := h.AgentHonorService.UpdateRules(
		r.Context(), parseUUID(workspaceID), member.UserID, rules,
	)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if h.AgentFleetRankService != nil {
		if _, err := h.AgentFleetRankService.RefreshWorkspace(
			r.Context(), parseUUID(workspaceID), "rules_updated",
		); err != nil {
			slog.Warn("refresh fleet after honor rules update failed", "workspace_id", workspaceID, "error", err)
		}
	}
	writeJSON(w, http.StatusOK, view)
}

func (h *Handler) GetAgentHonorAdminAudit(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	if _, ok := h.requireWorkspaceRole(
		w, r, workspaceID, "workspace not found", "owner", "admin",
	); !ok {
		return
	}
	var agentID pgtype.UUID
	if raw := strings.TrimSpace(r.URL.Query().Get("agent_id")); raw != "" {
		var ok bool
		agentID, ok = parseUUIDOrBadRequest(w, raw, "agent_id")
		if !ok {
			return
		}
	}
	rows, err := h.AgentHonorService.ListAdminAudit(r.Context(), parseUUID(workspaceID), agentID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load agent honor audit")
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

func (h *Handler) GetAgentHonor(w http.ResponseWriter, r *http.Request) {
	agent, ok := h.loadAgentForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	view, err := h.AgentHonorService.GetDashboard(r.Context(), agent.WorkspaceID, agent.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load agent honor")
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (h *Handler) PatchAgentHonorShowcase(w http.ResponseWriter, r *http.Request) {
	agent, ok := h.loadAgentForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	if !h.canManageAgent(w, r, agent) {
		return
	}
	var req patchAgentHonorShowcaseRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := h.AgentHonorService.SetShowcase(
		r.Context(), agent.WorkspaceID, agent.ID, req.AchievementIDs, req.EquippedID,
	); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	view, err := h.AgentHonorService.GetDashboard(r.Context(), agent.WorkspaceID, agent.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load agent honor")
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (h *Handler) PostAgentHonorGrant(w http.ResponseWriter, r *http.Request) {
	agent, ok := h.loadAgentForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	member, ok := h.workspaceMember(w, r, util.UUIDToString(agent.WorkspaceID))
	if !ok {
		return
	}
	if !roleAllowed(member.Role, "owner", "admin") {
		writeError(w, http.StatusForbidden, "insufficient permissions")
		return
	}
	var req agentHonorGrantRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.Reason = strings.TrimSpace(req.Reason)
	if req.Reason == "" {
		writeError(w, http.StatusBadRequest, "reason is required")
		return
	}
	var err error
	switch req.Kind {
	case "xp":
		grantID := strings.TrimSpace(req.GrantID)
		if grantID == "" {
			grantID = "manual:" + util.UUIDToString(member.UserID) + ":" + time.Now().UTC().Format("20060102T150405.000000000")
		}
		err = h.AgentHonorService.GrantXP(
			r.Context(), agent.WorkspaceID, agent.ID, member.UserID,
			req.XP, req.Reason, grantID,
		)
	case "achievement":
		err = h.AgentHonorService.GrantAchievement(
			r.Context(), agent.WorkspaceID, agent.ID, member.UserID,
			req.AchievementID, req.Reason,
		)
	default:
		writeError(w, http.StatusBadRequest, "kind must be xp or achievement")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	view, err := h.AgentHonorService.GetDashboard(r.Context(), agent.WorkspaceID, agent.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load agent honor")
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (h *Handler) DeleteAgentHonorAchievement(w http.ResponseWriter, r *http.Request) {
	agent, ok := h.loadAgentForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	member, ok := h.workspaceMember(w, r, util.UUIDToString(agent.WorkspaceID))
	if !ok {
		return
	}
	if !roleAllowed(member.Role, "owner", "admin") {
		writeError(w, http.StatusForbidden, "insufficient permissions")
		return
	}
	var req revokeAgentAchievementRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.Reason = strings.TrimSpace(req.Reason)
	if req.Reason == "" {
		writeError(w, http.StatusBadRequest, "reason is required")
		return
	}
	if err := h.AgentHonorService.RevokeAchievement(
		r.Context(), agent.WorkspaceID, agent.ID, member.UserID,
		chi.URLParam(r, "achievementId"), req.Reason,
	); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) wireAgentHonorEvents() {
	if h.AgentHonorService == nil {
		return
	}
	audience := func(ctx context.Context, agentID pgtype.UUID) ([]string, string, bool) {
		agent, err := h.Queries.GetAgent(ctx, agentID)
		return agentHonorAudience(agent, err)
	}
	h.AgentHonorService.OnAchievementUnlocked = func(ctx context.Context, evt service.AgentHonorUnlockEvent) {
		recipients, agentName, ok := audience(ctx, evt.AgentID)
		if !ok {
			slog.Warn("skip agent honor achievement event without a named owner audience", "agent_id", util.UUIDToString(evt.AgentID))
			return
		}
		h.publishToUsers(
			protocol.EventAgentHonorUnlocked,
			util.UUIDToString(evt.WorkspaceID),
			"system",
			"",
			recipients,
			agentHonorUnlockedPayload(evt, agentName),
		)
	}
	h.AgentHonorService.OnLevelChanged = func(ctx context.Context, evt service.AgentHonorLevelEvent) {
		recipients, agentName, ok := audience(ctx, evt.AgentID)
		if !ok {
			slog.Warn("skip agent honor level event without a named owner audience", "agent_id", util.UUIDToString(evt.AgentID))
			return
		}
		h.publishToUsers(
			protocol.EventAgentHonorLevelChanged,
			util.UUIDToString(evt.WorkspaceID),
			"system",
			"",
			recipients,
			agentHonorLevelChangedPayload(evt, agentName),
		)
	}
	h.AgentHonorService.OnFleetClassChanged = func(ctx context.Context, evt service.AgentFleetClassEvent) {
		recipients, agentName, ok := audience(ctx, evt.AgentID)
		if !ok {
			slog.Warn("skip agent fleet promotion event without a named owner audience", "agent_id", util.UUIDToString(evt.AgentID))
			return
		}
		h.publishToUsers(
			protocol.EventAgentFleetClassChanged,
			util.UUIDToString(evt.WorkspaceID),
			"system",
			"",
			recipients,
			agentFleetClassChangedPayload(evt, agentName),
		)
	}
}

func agentHonorAudience(agent db.Agent, queryErr error) ([]string, string, bool) {
	if queryErr != nil || !agent.OwnerID.Valid {
		return nil, "", false
	}
	agentName := firstNonEmpty(agent.DisplayName, agent.Name)
	if agentName == "" {
		return nil, "", false
	}
	return []string{util.UUIDToString(agent.OwnerID)}, agentName, true
}

func agentFleetClassChangedPayload(evt service.AgentFleetClassEvent, agentName string) map[string]any {
	return map[string]any{
		"agent_id":          util.UUIDToString(evt.AgentID),
		"agent_name":        agentName,
		"previous_class_id": evt.Previous,
		"class_id":          evt.Current,
		"fleet_score":       evt.FleetScore,
	}
}

func agentHonorUnlockedPayload(evt service.AgentHonorUnlockEvent, agentName string) map[string]any {
	return map[string]any{
		"agent_id":    util.UUIDToString(evt.AgentID),
		"agent_name":  agentName,
		"achievement": evt.Achievement,
	}
}

func agentHonorLevelChangedPayload(evt service.AgentHonorLevelEvent, agentName string) map[string]any {
	return map[string]any{
		"agent_id":       util.UUIDToString(evt.AgentID),
		"agent_name":     agentName,
		"previous_level": evt.Previous,
		"level":          evt.Current,
	}
}

func (h *Handler) refreshAgentHonor(
	ctx context.Context,
	workspaceID, agentID pgtype.UUID,
	reason string,
) {
	if h.AgentHonorService == nil || !workspaceID.Valid || !agentID.Valid {
		return
	}
	go func() {
		refreshCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
		defer cancel()
		if err := h.AgentHonorService.RefreshAgent(refreshCtx, workspaceID, agentID, reason); err != nil {
			slog.Warn(
				"refresh agent honor failed",
				"workspace_id", util.UUIDToString(workspaceID),
				"agent_id", util.UUIDToString(agentID),
				"reason", reason,
				"error", err,
			)
		}
	}()
}
