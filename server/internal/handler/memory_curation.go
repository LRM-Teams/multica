package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/memorycuration"
)

type startMemoryCurationRequest struct {
	AgentID        string   `json:"agent_id"`
	AgentIDs       []string `json:"agent_ids"`
	AllAgents      bool     `json:"all_agents"`
	Stage          string   `json:"stage"`
	Since          string   `json:"since"`
	Until          string   `json:"until"`
	IncludeHistory bool     `json:"include_history"`
	DryRun         bool     `json:"dry_run"`
	Force          bool     `json:"force"`
}

type memoryCurationRunResponse struct {
	ID          string          `json:"id"`
	WorkspaceID string          `json:"workspace_id"`
	AgentID     *string         `json:"agent_id,omitempty"`
	Stage       string          `json:"stage"`
	TriggerKind string          `json:"trigger_kind"`
	Status      string          `json:"status"`
	DateFrom    *string         `json:"date_from,omitempty"`
	DateTo      *string         `json:"date_to,omitempty"`
	DryRun      bool            `json:"dry_run"`
	Force       bool            `json:"force"`
	Stats       json.RawMessage `json:"stats"`
	Error       string          `json:"error,omitempty"`
	CreatedAt   string          `json:"created_at"`
	StartedAt   *string         `json:"started_at,omitempty"`
	FinishedAt  *string         `json:"finished_at,omitempty"`
}

type memoryCurationRunStatsResponse struct {
	AgentsScanned          int `json:"agents_scanned"`
	AgentsChanged          int `json:"agents_changed"`
	DailyFilesWritten      int `json:"daily_files_written"`
	ReviewCandidatesAdded  int `json:"review_candidates_added"`
	EntriesReviewed        int `json:"entries_reviewed"`
	MemoryRoutes           int `json:"memory_routes"`
	SkillRoutes            int `json:"skill_routes"`
	SplitRoutes            int `json:"split_routes"`
	DiscardRoutes          int `json:"discard_routes"`
	ReviewDeferred         int `json:"review_deferred"`
	EntriesPromoted        int `json:"entries_promoted"`
	SkillCandidatesAdded   int `json:"skill_candidates_added"`
	SharedCandidatesAdded  int `json:"shared_candidates_added"`
	SharedCandidatesSynced int `json:"shared_candidates_synced"`
	EntriesArchived        int `json:"entries_archived"`
	DuplicatesMerged       int `json:"duplicates_merged"`
	ConflictsFound         int `json:"conflicts_found"`
	EvidenceCollected      int `json:"evidence_collected"`
	ErrorCount             int `json:"error_count"`
}

type memoryCurationStageStatusResponse struct {
	ID          string                         `json:"id"`
	Stage       string                         `json:"stage"`
	TriggerKind string                         `json:"trigger_kind"`
	Status      string                         `json:"status"`
	Stats       memoryCurationRunStatsResponse `json:"stats"`
	CreatedAt   string                         `json:"created_at"`
	StartedAt   *string                        `json:"started_at,omitempty"`
	FinishedAt  *string                        `json:"finished_at,omitempty"`
}

type workspaceMemoryCurationStatusResponse struct {
	WorkspaceID   string                              `json:"workspace_id"`
	PendingRuns   int                                 `json:"pending_runs"`
	FailedRuns24h int                                 `json:"failed_runs_24h"`
	Stages        []memoryCurationStageStatusResponse `json:"stages"`
}

func (h *Handler) StartMemoryCurationRun(w http.ResponseWriter, r *http.Request) {
	workspaceID := workspaceIDFromURL(r, "id")
	if workspaceID == "" {
		writeError(w, http.StatusBadRequest, "workspace id is required")
		return
	}
	var req startMemoryCurationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	stage, err := memorycuration.NormalizeStage(req.Stage)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	agentIDs := append([]string(nil), req.AgentIDs...)
	if req.AgentID != "" {
		agentIDs = append(agentIDs, req.AgentID)
	}
	agentIDs, ok := parseUniqueAgentIDsOrBadRequest(w, agentIDs)
	if !ok {
		return
	}
	if len(agentIDs) == 0 && !req.AllAgents {
		writeError(w, http.StatusBadRequest, "agent_id, agent_ids, or all_agents is required")
		return
	}
	if len(agentIDs) > 0 {
		valid, err := h.agentIDsBelongToWorkspace(r.Context(), workspaceID, agentIDs)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to validate agent scope")
			return
		}
		if !valid {
			writeError(w, http.StatusNotFound, "agent not found")
			return
		}
	}
	now := time.Now().UTC()
	until := now
	if req.Until != "" {
		until, err = time.Parse("2006-01-02", req.Until)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid until date")
			return
		}
	}
	since := until
	if req.Since != "" {
		since, err = time.Parse("2006-01-02", req.Since)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid since date")
			return
		}
	}
	member, ok := h.workspaceMember(w, r, workspaceID)
	if !ok {
		return
	}
	if !roleAllowed(member.Role, "owner", "admin") {
		writeError(w, http.StatusForbidden, "only workspace owner/admin can run team curation")
		return
	}
	profile, err := h.loadMemoryCuratorProfile(r, workspaceID, uuidToString(member.UserID))
	if err != nil {
		if err == pgx.ErrNoRows {
			writeError(w, http.StatusConflict, "configure a memory curator profile before running curation")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load memory curator profile")
		return
	}
	runStatus, err := h.memoryCuratorRunStatus(r.Context(), profile)
	if err != nil {
		if errors.Is(err, errInvalidMemoryCuratorProfile) {
			writeError(w, http.StatusConflict, "memory curator profile is no longer valid; choose a runtime and curator agent again")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to validate memory curator profile")
		return
	}
	if (stage == memorycuration.StageAgentSelfReview || stage == memorycuration.StageAll) && !profile.SelfReviewEnabled {
		writeError(w, http.StatusConflict, "agent self-review is disabled for this workspace")
		return
	}
	if (stage == memorycuration.StageTeamCuration || stage == memorycuration.StageAll) && !profile.TeamCurationEnabled {
		writeError(w, http.StatusConflict, "team curation is disabled for this workspace")
		return
	}
	profileAgentIDs, err := h.resolveActiveMemoryCurationTargetAgentIDs(r.Context(), profile, since)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to resolve active curator targets")
		return
	}
	if req.AllAgents {
		agentIDs = profileAgentIDs
	} else if !agentIDsSubset(agentIDs, profileAgentIDs) {
		writeError(w, http.StatusForbidden, "selected agents are outside the active online curator targets")
		return
	}
	if len(agentIDs) == 0 {
		writeError(w, http.StatusConflict, "no active online target agents for this date")
		return
	}
	dbStage := memorycuration.DBStageName(stage)
	trigger := "manual"
	if req.IncludeHistory {
		trigger = "backfill"
	}
	var agentForRun any
	if len(agentIDs) == 1 {
		agentForRun = parseUUID(agentIDs[0])
	}
	var runID string
	if err := h.DB.QueryRow(r.Context(), `
		INSERT INTO memory_curation_run (
		  workspace_id, agent_id, stage, trigger_kind, status, date_from, date_to,
		  dry_run, force, requested_by, profile_id, owner_user_id, runtime_id,
		  curator_agent_id, curator_model, curator_mode, confidence_threshold,
		  config_version, target_agent_ids, execution_owner
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19::uuid[],'daemon')
		RETURNING id::text
	`, workspaceID, agentForRun, dbStage, trigger, runStatus, since, until, req.DryRun, req.Force,
		uuidToString(member.ID), profile.ID, profile.UserID, profile.RuntimeID,
		profile.CuratorAgentID, profile.ModelOverride, profile.Mode, profile.ConfidenceThreshold,
		profile.ConfigVersion, agentIDs).Scan(&runID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create curation run")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"id": runID, "status": runStatus})
}

func (h *Handler) GetMemoryCurationRun(w http.ResponseWriter, r *http.Request) {
	workspaceID := workspaceIDFromURL(r, "id")
	runUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "runId"), "run id")
	if !ok {
		return
	}
	row, err := h.loadMemoryCurationRun(r, workspaceID, runUUID)
	if err != nil {
		if err == pgx.ErrNoRows {
			writeError(w, http.StatusNotFound, "memory curation run not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, row)
}

func (h *Handler) GetWorkspaceMemoryCurationStatus(w http.ResponseWriter, r *http.Request) {
	workspaceID := workspaceIDFromURL(r, "id")
	if workspaceID == "" {
		writeError(w, http.StatusBadRequest, "workspace id is required")
		return
	}

	response := workspaceMemoryCurationStatusResponse{
		WorkspaceID: workspaceID,
		Stages:      []memoryCurationStageStatusResponse{},
	}
	if err := h.DB.QueryRow(r.Context(), `
		SELECT count(*) FILTER (WHERE status IN ('queued', 'waiting_runtime', 'running')),
		       count(*) FILTER (WHERE status = 'failed' AND created_at >= now() - interval '24 hours')
		  FROM memory_curation_run
		 WHERE workspace_id = $1
	`, workspaceID).Scan(&response.PendingRuns, &response.FailedRuns24h); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load memory curation status")
		return
	}

	rows, err := h.DB.Query(r.Context(), `
		SELECT DISTINCT ON (stage)
		       id::text, stage, trigger_kind, status, stats, created_at, started_at, finished_at
		  FROM memory_curation_run
		 WHERE workspace_id = $1
		   AND stage IN ('agent_self_review', 'team_curation', 'all')
		 ORDER BY stage, created_at DESC
	`, workspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load memory curation stages")
		return
	}
	defer rows.Close()

	for rows.Next() {
		var stage memoryCurationStageStatusResponse
		var stats []byte
		var createdAt time.Time
		var startedAt, finishedAt *time.Time
		if err := rows.Scan(&stage.ID, &stage.Stage, &stage.TriggerKind, &stage.Status, &stats, &createdAt, &startedAt, &finishedAt); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to read memory curation stage")
			return
		}
		stage.Stats = publicMemoryCurationStats(stats)
		stage.CreatedAt = createdAt.UTC().Format(time.RFC3339)
		if startedAt != nil {
			value := startedAt.UTC().Format(time.RFC3339)
			stage.StartedAt = &value
		}
		if finishedAt != nil {
			value := finishedAt.UTC().Format(time.RFC3339)
			stage.FinishedAt = &value
		}
		response.Stages = append(response.Stages, stage)
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load memory curation stages")
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func publicMemoryCurationStats(raw []byte) memoryCurationRunStatsResponse {
	var result memorycuration.Result
	if err := json.Unmarshal(raw, &result); err != nil {
		return memoryCurationRunStatsResponse{}
	}
	return memoryCurationRunStatsResponse{
		AgentsScanned:          result.AgentsScanned,
		AgentsChanged:          result.AgentsChanged,
		DailyFilesWritten:      result.DailyFilesWritten,
		ReviewCandidatesAdded:  result.ReviewCandidatesAdded,
		EntriesReviewed:        result.EntriesReviewed,
		MemoryRoutes:           result.MemoryRoutes,
		SkillRoutes:            result.SkillRoutes,
		SplitRoutes:            result.SplitRoutes,
		DiscardRoutes:          result.DiscardRoutes,
		ReviewDeferred:         result.ReviewDeferred,
		EntriesPromoted:        result.EntriesPromoted,
		SkillCandidatesAdded:   result.SkillCandidatesAdded,
		SharedCandidatesAdded:  result.SharedCandidatesAdded,
		SharedCandidatesSynced: result.SharedCandidatesSynced,
		EntriesArchived:        result.EntriesArchived,
		DuplicatesMerged:       result.DuplicatesMerged,
		ConflictsFound:         result.ConflictsFound,
		EvidenceCollected:      result.EvidenceCollected,
		ErrorCount:             len(result.Errors),
	}
}

func (h *Handler) GetAgentMemoryCurationStatus(w http.ResponseWriter, r *http.Request) {
	agentUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "agent id")
	if !ok {
		return
	}
	agentID := uuidToString(agentUUID)
	var workspaceID string
	var pending int
	var lastRun json.RawMessage
	err := h.DB.QueryRow(r.Context(), `
		SELECT a.workspace_id::text,
		       COALESCE((
		         SELECT count(*)
		           FROM memory_curation_run r
		          WHERE (r.agent_id = a.id OR (r.agent_id IS NULL AND r.workspace_id = a.workspace_id))
		            AND r.status IN ('queued','running')
		       ), 0),
		       COALESCE((
		         SELECT jsonb_build_object('id', r.id, 'stage', r.stage, 'status', r.status, 'created_at', r.created_at, 'finished_at', r.finished_at)
		           FROM memory_curation_run r
		          WHERE r.agent_id = a.id OR (r.agent_id IS NULL AND r.workspace_id = a.workspace_id)
		          ORDER BY r.created_at DESC
		          LIMIT 1
		       ), '{}'::jsonb)
		  FROM agent a
		 WHERE a.id = $1
	`, agentUUID).Scan(&workspaceID, &pending, &lastRun)
	if err != nil {
		if err == pgx.ErrNoRows {
			writeError(w, http.StatusNotFound, "agent not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if ctxWS := ctxWorkspaceID(r.Context()); ctxWS != "" && ctxWS != workspaceID {
		writeError(w, http.StatusNotFound, "agent not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"agent_id": agentID, "workspace_id": workspaceID, "pending_runs": pending, "last_run": lastRun})
}

func (h *Handler) loadMemoryCurationRun(r *http.Request, workspaceID string, runID pgtype.UUID) (memoryCurationRunResponse, error) {
	var resp memoryCurationRunResponse
	var stats []byte
	var agentID, dateFrom, dateTo string
	var createdAt time.Time
	var startedAt, finishedAt *time.Time
	err := h.DB.QueryRow(r.Context(), `
		SELECT id::text, workspace_id::text, COALESCE(agent_id::text, ''), stage, trigger_kind, status,
		       COALESCE(date_from::text, ''), COALESCE(date_to::text, ''), dry_run, force, stats, error, created_at, started_at, finished_at
		  FROM memory_curation_run
		 WHERE workspace_id = $1 AND id = $2
	`, workspaceID, runID).Scan(&resp.ID, &resp.WorkspaceID, &agentID, &resp.Stage, &resp.TriggerKind, &resp.Status, &dateFrom, &dateTo, &resp.DryRun, &resp.Force, &stats, &resp.Error, &createdAt, &startedAt, &finishedAt)
	if err != nil {
		return resp, err
	}
	resp.Stats = json.RawMessage(stats)
	resp.CreatedAt = createdAt.UTC().Format(time.RFC3339)
	if agentID != "" {
		resp.AgentID = &agentID
	}
	if dateFrom != "" {
		resp.DateFrom = &dateFrom
	}
	if dateTo != "" {
		resp.DateTo = &dateTo
	}
	if startedAt != nil {
		s := startedAt.UTC().Format(time.RFC3339)
		resp.StartedAt = &s
	}
	if finishedAt != nil {
		s := finishedAt.UTC().Format(time.RFC3339)
		resp.FinishedAt = &s
	}
	return resp, nil
}

func parseUniqueAgentIDsOrBadRequest(w http.ResponseWriter, rawIDs []string) ([]string, bool) {
	seen := make(map[string]struct{}, len(rawIDs))
	agentIDs := make([]string, 0, len(rawIDs))
	for _, raw := range rawIDs {
		if raw == "" {
			continue
		}
		agentUUID, ok := parseUUIDOrBadRequest(w, raw, "agent_id")
		if !ok {
			return nil, false
		}
		agentID := uuidToString(agentUUID)
		if _, exists := seen[agentID]; exists {
			continue
		}
		seen[agentID] = struct{}{}
		agentIDs = append(agentIDs, agentID)
	}
	return agentIDs, true
}

func (h *Handler) agentIDsBelongToWorkspace(ctx context.Context, workspaceID string, agentIDs []string) (bool, error) {
	if len(agentIDs) == 0 {
		return true, nil
	}
	var count int
	if err := h.DB.QueryRow(ctx, `
		SELECT count(*)
		  FROM agent
		 WHERE workspace_id = $1 AND id::text = ANY($2)
	`, workspaceID, agentIDs).Scan(&count); err != nil {
		return false, err
	}
	return count == len(agentIDs), nil
}
