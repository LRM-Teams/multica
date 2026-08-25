package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/multica-ai/multica/server/internal/service"
)

var errInvalidMemoryCuratorProfile = errors.New("invalid memory curator profile")

const (
	defaultCuratorMode                = "review"
	defaultCuratorTargetScope         = "owned_all"
	defaultCuratorTimezone            = "Asia/Shanghai"
	defaultCuratorScheduleHour        = 1
	defaultCuratorConfidenceThreshold = 0.8
)

type memoryCuratorProfileResponse struct {
	ID                  string   `json:"id"`
	WorkspaceID         string   `json:"workspace_id"`
	UserID              string   `json:"user_id"`
	Enabled             bool     `json:"enabled"`
	SelfReviewEnabled   bool     `json:"self_review_enabled"`
	TeamCurationEnabled bool     `json:"team_curation_enabled"`
	Mode                string   `json:"mode"`
	RuntimeID           string   `json:"runtime_id"`
	CuratorAgentID      string   `json:"curator_agent_id"`
	ModelOverride       string   `json:"model_override"`
	TargetScope         string   `json:"target_scope"`
	TargetAgentIDs      []string `json:"target_agent_ids"`
	Timezone            string   `json:"timezone"`
	ScheduleHour        int      `json:"schedule_hour"`
	CatchUpEnabled      bool     `json:"catch_up_enabled"`
	ConfidenceThreshold float64  `json:"confidence_threshold"`
	ConfigVersion       int64    `json:"config_version"`
	CreatedAt           string   `json:"created_at"`
	UpdatedAt           string   `json:"updated_at"`
}

type updateMemoryCuratorProfileRequest struct {
	Enabled             bool     `json:"enabled"`
	SelfReviewEnabled   bool     `json:"self_review_enabled"`
	TeamCurationEnabled bool     `json:"team_curation_enabled"`
	Mode                string   `json:"mode"`
	RuntimeID           string   `json:"runtime_id"`
	CuratorAgentID      string   `json:"curator_agent_id"`
	ModelOverride       string   `json:"model_override"`
	TargetScope         string   `json:"target_scope"`
	TargetAgentIDs      []string `json:"target_agent_ids"`
	Timezone            string   `json:"timezone"`
	ScheduleHour        int      `json:"schedule_hour"`
	CatchUpEnabled      bool     `json:"catch_up_enabled"`
	ConfidenceThreshold float64  `json:"confidence_threshold"`
}

func (h *Handler) GetMemoryCuratorProfile(w http.ResponseWriter, r *http.Request) {
	workspaceID := workspaceIDFromURL(r, "id")
	member, ok := h.workspaceMember(w, r, workspaceID)
	if !ok {
		return
	}
	profile, err := h.loadMemoryCuratorProfile(r, workspaceID, uuidToString(member.UserID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeJSON(w, http.StatusOK, defaultMemoryCuratorProfile(workspaceID, uuidToString(member.UserID)))
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load memory curator profile")
		return
	}
	writeJSON(w, http.StatusOK, profile)
}

func (h *Handler) UpdateMemoryCuratorProfile(w http.ResponseWriter, r *http.Request) {
	workspaceID := workspaceIDFromURL(r, "id")
	member, ok := h.workspaceMember(w, r, workspaceID)
	if !ok {
		return
	}
	if !roleAllowed(member.Role, "owner", "admin") {
		writeError(w, http.StatusForbidden, "only workspace owner/admin can configure team curation")
		return
	}
	userID := uuidToString(member.UserID)

	var req updateMemoryCuratorProfileRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.Mode = strings.ToLower(strings.TrimSpace(req.Mode))
	req.TargetScope = strings.ToLower(strings.TrimSpace(req.TargetScope))
	req.Timezone = strings.TrimSpace(req.Timezone)
	req.ModelOverride = strings.TrimSpace(req.ModelOverride)
	if req.Mode == "" {
		req.Mode = defaultCuratorMode
	}
	if req.TargetScope == "" {
		req.TargetScope = defaultCuratorTargetScope
	}
	if req.Timezone == "" {
		req.Timezone = defaultCuratorTimezone
	}
	if !validCuratorMode(req.Mode) {
		writeError(w, http.StatusBadRequest, "invalid curator mode")
		return
	}
	if req.TargetScope != "owned_all" && req.TargetScope != "selected" {
		writeError(w, http.StatusBadRequest, "invalid target_scope")
		return
	}
	if _, err := time.LoadLocation(req.Timezone); err != nil {
		writeError(w, http.StatusBadRequest, "invalid timezone")
		return
	}
	if req.ScheduleHour < 0 || req.ScheduleHour > 23 {
		writeError(w, http.StatusBadRequest, "schedule_hour must be between 0 and 23")
		return
	}
	if req.ConfidenceThreshold < 0 || req.ConfidenceThreshold > 1 {
		writeError(w, http.StatusBadRequest, "confidence_threshold must be between 0 and 1")
		return
	}
	if req.RuntimeID == "" || req.CuratorAgentID == "" {
		writeError(w, http.StatusBadRequest, "runtime_id and curator_agent_id are required")
		return
	}
	runtimeUUID, ok := parseUUIDOrBadRequest(w, req.RuntimeID, "runtime_id")
	if !ok {
		return
	}
	curatorAgentUUID, ok := parseUUIDOrBadRequest(w, req.CuratorAgentID, "curator_agent_id")
	if !ok {
		return
	}
	targetAgentIDs, ok := parseUniqueAgentIDsOrBadRequest(w, req.TargetAgentIDs)
	if !ok {
		return
	}
	if req.TargetScope == "selected" && len(targetAgentIDs) == 0 {
		writeError(w, http.StatusBadRequest, "target_agent_ids are required for selected scope")
		return
	}

	var runtimeCount int
	if err := h.DB.QueryRow(r.Context(), `
		SELECT count(*)
		  FROM agent_runtime
		 WHERE id = $1 AND workspace_id = $2
		   AND (visibility = 'public' OR EXISTS (
		        SELECT 1 FROM computer_workspace_bindings b
		         WHERE b.daemon_id = agent_runtime.daemon_id
		           AND b.workspace_id = agent_runtime.workspace_id
		           AND b.user_id = $3 AND b.active = TRUE
		   ))
	`, runtimeUUID, workspaceID, userID).Scan(&runtimeCount); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to validate runtime")
		return
	}
	if runtimeCount != 1 {
		writeError(w, http.StatusNotFound, "runtime not found")
		return
	}

	var curatorAgentCount int
	if err := h.DB.QueryRow(r.Context(), `
		SELECT count(*)
		  FROM agent
		 WHERE id = $1 AND workspace_id = $2 AND owner_id = $3
		   AND runtime_id = $4 AND archived_at IS NULL
	`, curatorAgentUUID, workspaceID, userID, runtimeUUID).Scan(&curatorAgentCount); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to validate curator agent")
		return
	}
	if curatorAgentCount != 1 {
		writeError(w, http.StatusNotFound, "curator agent not found")
		return
	}
	if len(targetAgentIDs) > 0 {
		valid, err := h.agentIDsOwnedByUser(r.Context(), workspaceID, userID, targetAgentIDs)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to validate target agents")
			return
		}
		if !valid {
			writeError(w, http.StatusNotFound, "target agent not found")
			return
		}
	}

	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update memory curator profile")
		return
	}
	defer tx.Rollback(r.Context())
	var profileID string
	if err := tx.QueryRow(r.Context(), `
		INSERT INTO memory_curator_profile (
		  workspace_id, user_id, enabled, self_review_enabled, team_curation_enabled,
		  mode, runtime_id, curator_agent_id, model_override, target_scope,
		  timezone, schedule_hour, catch_up_enabled, confidence_threshold
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
		ON CONFLICT (workspace_id, user_id) DO UPDATE SET
		  enabled = EXCLUDED.enabled,
		  self_review_enabled = EXCLUDED.self_review_enabled,
		  team_curation_enabled = EXCLUDED.team_curation_enabled,
		  mode = EXCLUDED.mode,
		  runtime_id = EXCLUDED.runtime_id,
		  curator_agent_id = EXCLUDED.curator_agent_id,
		  model_override = EXCLUDED.model_override,
		  target_scope = EXCLUDED.target_scope,
		  timezone = EXCLUDED.timezone,
		  schedule_hour = EXCLUDED.schedule_hour,
		  catch_up_enabled = EXCLUDED.catch_up_enabled,
		  confidence_threshold = EXCLUDED.confidence_threshold,
		  config_version = memory_curator_profile.config_version + 1,
		  updated_at = now()
		RETURNING id::text
	`, workspaceID, userID, req.TeamCurationEnabled, req.SelfReviewEnabled, req.TeamCurationEnabled, req.Mode, runtimeUUID, curatorAgentUUID, req.ModelOverride, req.TargetScope, req.Timezone, req.ScheduleHour, req.CatchUpEnabled, req.ConfidenceThreshold).Scan(&profileID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save memory curator profile")
		return
	}
	if _, err := tx.Exec(r.Context(), `DELETE FROM memory_curator_target WHERE profile_id = $1`, profileID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save curator targets")
		return
	}
	for _, agentID := range targetAgentIDs {
		if _, err := tx.Exec(r.Context(), `INSERT INTO memory_curator_target (profile_id, agent_id) VALUES ($1, $2)`, profileID, agentID); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to save curator targets")
			return
		}
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update memory curator profile")
		return
	}
	profile, err := h.loadMemoryCuratorProfile(r, workspaceID, userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load memory curator profile")
		return
	}
	writeJSON(w, http.StatusOK, profile)
}

func (h *Handler) loadMemoryCuratorProfile(r *http.Request, workspaceID, userID string) (memoryCuratorProfileResponse, error) {
	var profile memoryCuratorProfileResponse
	var targetIDs []string
	var createdAt, updatedAt time.Time
	err := h.DB.QueryRow(r.Context(), `
		SELECT p.id::text, p.workspace_id::text, p.user_id::text, p.enabled,
		       COALESCE(p.self_review_enabled, false), COALESCE(p.team_curation_enabled, p.enabled), p.mode,
		       COALESCE(p.runtime_id::text, ''), COALESCE(p.curator_agent_id::text, ''),
		       p.model_override, p.target_scope, p.timezone, p.schedule_hour,
		       p.catch_up_enabled, p.confidence_threshold, p.config_version,
		       p.created_at, p.updated_at,
		       COALESCE(array_agg(t.agent_id::text ORDER BY t.agent_id) FILTER (WHERE t.agent_id IS NOT NULL), '{}')
		  FROM memory_curator_profile p
		  LEFT JOIN memory_curator_target t ON t.profile_id = p.id
		 WHERE p.workspace_id = $1 AND p.user_id = $2
		 GROUP BY p.id
	`, workspaceID, userID).Scan(
		&profile.ID, &profile.WorkspaceID, &profile.UserID, &profile.Enabled,
		&profile.SelfReviewEnabled, &profile.TeamCurationEnabled, &profile.Mode,
		&profile.RuntimeID, &profile.CuratorAgentID, &profile.ModelOverride, &profile.TargetScope,
		&profile.Timezone, &profile.ScheduleHour, &profile.CatchUpEnabled,
		&profile.ConfidenceThreshold, &profile.ConfigVersion, &createdAt, &updatedAt, &targetIDs,
	)
	if err != nil {
		return profile, err
	}
	profile.TargetAgentIDs = targetIDs
	profile.CreatedAt = createdAt.UTC().Format(time.RFC3339)
	profile.UpdatedAt = updatedAt.UTC().Format(time.RFC3339)
	return profile, nil
}

func defaultMemoryCuratorProfile(workspaceID, userID string) memoryCuratorProfileResponse {
	return memoryCuratorProfileResponse{
		WorkspaceID:         workspaceID,
		UserID:              userID,
		Mode:                defaultCuratorMode,
		TargetScope:         defaultCuratorTargetScope,
		TargetAgentIDs:      []string{},
		Timezone:            defaultCuratorTimezone,
		ScheduleHour:        defaultCuratorScheduleHour,
		CatchUpEnabled:      true,
		ConfidenceThreshold: defaultCuratorConfidenceThreshold,
	}
}

func validCuratorMode(mode string) bool {
	switch mode {
	case "observe", "review", "auto_safe", "auto":
		return true
	default:
		return false
	}
}

func (h *Handler) agentIDsOwnedByUser(ctx context.Context, workspaceID, userID string, agentIDs []string) (bool, error) {
	var count int
	err := h.DB.QueryRow(ctx, `
		SELECT count(*)
		  FROM agent
		 WHERE workspace_id = $1 AND owner_id = $2 AND archived_at IS NULL
		   AND id::text = ANY($3)
	`, workspaceID, userID, agentIDs).Scan(&count)
	return count == len(agentIDs), err
}

func (h *Handler) memoryCuratorRunStatus(ctx context.Context, profile memoryCuratorProfileResponse) (string, error) {
	if profile.RuntimeID == "" || profile.CuratorAgentID == "" {
		return "", errInvalidMemoryCuratorProfile
	}
	var runtimeLastSeenAt time.Time
	err := h.DB.QueryRow(ctx, `
		SELECT rt.last_seen_at
		  FROM agent_runtime rt
		  JOIN agent curator ON curator.id = $2
		 WHERE rt.id = $1 AND rt.workspace_id = $3
		   AND (rt.visibility = 'public' OR EXISTS (
		        SELECT 1 FROM computer_workspace_bindings b
		         WHERE b.daemon_id = rt.daemon_id
		           AND b.workspace_id = rt.workspace_id
		           AND b.user_id = $4 AND b.active = TRUE
		   ))
		   AND curator.workspace_id = $3 AND curator.owner_id = $4
		   AND curator.runtime_id = rt.id AND curator.archived_at IS NULL
	`, profile.RuntimeID, profile.CuratorAgentID, profile.WorkspaceID, profile.UserID).Scan(&runtimeLastSeenAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", errInvalidMemoryCuratorProfile
	}
	if err != nil {
		return "", err
	}
	// Task #53: this decides the *initial* status shown to the user right
	// after they start a run (StartMemoryCurationRun writes it straight into
	// the response), so it must reflect real-time reachability, not the
	// sweeper-lagged agent_runtime.status column.
	if time.Since(runtimeLastSeenAt) > service.AgentHealthStaleThreshold {
		return "waiting_runtime", nil
	}
	return "queued", nil
}

func (h *Handler) resolveMemoryCuratorTargetAgentIDs(ctx context.Context, profile memoryCuratorProfileResponse) ([]string, error) {
	if profile.TargetScope == "selected" {
		return append([]string(nil), profile.TargetAgentIDs...), nil
	}
	rows, err := h.DB.Query(ctx, `
		SELECT id::text
		  FROM agent
		 WHERE workspace_id = $1 AND owner_id = $2 AND archived_at IS NULL
		 ORDER BY created_at, id
	`, profile.WorkspaceID, profile.UserID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (h *Handler) resolveActiveMemoryCurationTargetAgentIDs(ctx context.Context, profile memoryCuratorProfileResponse, day time.Time) ([]string, error) {
	rows, err := h.DB.Query(ctx, `
		WITH targets AS (
		  SELECT a.id, a.runtime_id
		    FROM agent a
		   WHERE a.workspace_id = $1
		     AND a.archived_at IS NULL
		     AND (($4 <> 'selected' AND a.owner_id = $2) OR ($4 = 'selected' AND a.id::text = ANY($5)))
		), active AS (
		  SELECT DISTINCT agent_id
		    FROM agent_inbox_event
		   WHERE COALESCE(completed_at, started_at, dispatched_at, created_at) >= $3::date
		     AND COALESCE(completed_at, started_at, dispatched_at, created_at) < ($3::date + interval '1 day')
		  UNION
		  SELECT DISTINCT agent_id
		    FROM agent_inbox_event
		   WHERE created_at >= $3::date AND created_at < ($3::date + interval '1 day')
		)
		SELECT t.id::text
		  FROM targets t
		  JOIN active act ON act.agent_id = t.id
		  JOIN agent_runtime rt ON rt.id = t.runtime_id
		   AND rt.last_seen_at >= now() - make_interval(secs => $6::double precision)
		 ORDER BY t.id
	`, profile.WorkspaceID, profile.UserID, day, profile.TargetScope, profile.TargetAgentIDs, service.AgentHealthStaleThreshold.Seconds())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func agentIDsSubset(selected, allowed []string) bool {
	set := make(map[string]struct{}, len(allowed))
	for _, id := range allowed {
		set[id] = struct{}{}
	}
	for _, id := range selected {
		if _, ok := set[id]; !ok {
			return false
		}
	}
	return true
}
