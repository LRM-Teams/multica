package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/memorycuration"
)

// writeLegacyCurationNotApplicable is the stable graph-mode response for
// legacy-only curation endpoints (spec §10): graph workspaces never run
// legacy L1-L4 pipelines, and callers get an explicit machine-readable
// answer instead of a silently frozen queue.
func writeLegacyCurationNotApplicable(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusConflict)
	_, _ = w.Write([]byte(`{"error":"legacy_curation_not_applicable","memory_type":"graph"}`))
}

// graphMemoryTypeForWorkspace resolves the effective memory_type for one
// workspace (design §1/A4): a valid graph_memory_profile row wins over the
// process env default (MULTICA_MEMORY_TYPE), then "legacy".
func (h *Handler) graphMemoryTypeForWorkspace(ctx context.Context, workspaceID pgtype.UUID) string {
	if profile := h.graphMemoryProfileForWorkspace(ctx, workspaceID); profile.memoryType != "" {
		return profile.memoryType
	}
	envType := strings.ToLower(strings.TrimSpace(os.Getenv("MULTICA_MEMORY_TYPE")))
	if envType == "graph" || envType == "legacy" {
		return envType
	}
	return "legacy"
}

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
	ID                  string                           `json:"id"`
	WorkspaceID         string                           `json:"workspace_id"`
	AgentID             *string                          `json:"agent_id,omitempty"`
	Stage               string                           `json:"stage"`
	TriggerKind         string                           `json:"trigger_kind"`
	Status              string                           `json:"status"`
	DateFrom            *string                          `json:"date_from,omitempty"`
	DateTo              *string                          `json:"date_to,omitempty"`
	DryRun              bool                             `json:"dry_run"`
	Force               bool                             `json:"force"`
	Stats               json.RawMessage                  `json:"stats"`
	StatsSummary        memoryCurationRunStatsResponse   `json:"stats_summary"`
	Error               string                           `json:"error,omitempty"`
	Diagnostics         []memoryCurationRunDiagnostic    `json:"diagnostics,omitempty"`
	RuntimeID           string                           `json:"runtime_id,omitempty"`
	RuntimeName         string                           `json:"runtime_name,omitempty"`
	RuntimeDeviceInfo   string                           `json:"runtime_device_info,omitempty"`
	RuntimeLastSeenAt   *string                          `json:"runtime_last_seen_at,omitempty"`
	Attempt             int                              `json:"attempt,omitempty"`
	ClaimedAt           *string                          `json:"claimed_at,omitempty"`
	ClaimedAgeSeconds   int                              `json:"claimed_age_seconds,omitempty"`
	CuratorAgentID      string                           `json:"curator_agent_id,omitempty"`
	CuratorAgentName    string                           `json:"curator_agent_name,omitempty"`
	CuratorModel        string                           `json:"curator_model,omitempty"`
	CuratorMode         string                           `json:"curator_mode,omitempty"`
	ConfidenceThreshold float64                          `json:"confidence_threshold,omitempty"`
	TargetAgentIDs      []string                         `json:"target_agent_ids"`
	TargetAgents        []memoryCurationTargetAgent      `json:"target_agents"`
	Timeline            []memoryCurationRunTimelineItem  `json:"timeline"`
	AgentResults        []memoryCurationAgentRunResponse `json:"agent_results"`
	ChildRuns           []memoryCurationChildRunResponse `json:"child_runs"`
	Artifacts           []memoryCurationRunArtifact      `json:"artifacts"`
	CreatedAt           string                           `json:"created_at"`
	StartedAt           *string                          `json:"started_at,omitempty"`
	FinishedAt          *string                          `json:"finished_at,omitempty"`
}

type memoryCurationRunDiagnostic struct {
	Severity string `json:"severity"`
	Code     string `json:"code"`
	Message  string `json:"message"`
	Action   string `json:"action,omitempty"`
}

type memoryCurationTargetAgent struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type memoryCurationRunTimelineItem struct {
	Key       string `json:"key"`
	AgentID   string `json:"agent_id,omitempty"`
	Label     string `json:"label"`
	Status    string `json:"status"`
	Timestamp string `json:"timestamp,omitempty"`
	Detail    string `json:"detail,omitempty"`
}

type memoryCurationAgentRunResponse struct {
	WorkspaceID           string `json:"workspace_id"`
	AgentID               string `json:"agent_id"`
	AgentName             string `json:"agent_name,omitempty"`
	Root                  string `json:"root"`
	Changed               bool   `json:"changed"`
	DailyFilesWritten     int    `json:"daily_files_written"`
	ReviewCandidatesAdded int    `json:"review_candidates_added"`
	SkillCandidatesAdded  int    `json:"skill_candidates_added"`
	EvidenceCollected     int    `json:"evidence_collected"`
	ConflictsFound        int    `json:"conflicts_found"`
	Error                 string `json:"error,omitempty"`
	CuratorOutputExcerpt  string `json:"curator_output_excerpt,omitempty"`
}

type memoryCurationChildRunResponse struct {
	ID                    string  `json:"id"`
	ParentRunID           string  `json:"parent_run_id"`
	WorkspaceID           string  `json:"workspace_id"`
	AgentID               string  `json:"agent_id"`
	AgentName             string  `json:"agent_name,omitempty"`
	RuntimeID             string  `json:"runtime_id,omitempty"`
	RuntimeName           string  `json:"runtime_name,omitempty"`
	Stage                 string  `json:"stage"`
	Status                string  `json:"status"`
	Attempt               int     `json:"attempt"`
	StartedAt             *string `json:"started_at,omitempty"`
	FinishedAt            *string `json:"finished_at,omitempty"`
	Error                 string  `json:"error,omitempty"`
	Changed               bool    `json:"changed"`
	DailyFilesWritten     int     `json:"daily_files_written"`
	ReviewCandidatesAdded int     `json:"review_candidates_added"`
	SkillCandidatesAdded  int     `json:"skill_candidates_added"`
	EvidenceCollected     int     `json:"evidence_collected"`
	ConflictsFound        int     `json:"conflicts_found"`
	OutputExcerpt         string  `json:"output_excerpt,omitempty"`
}

type memoryCurationRunArtifact struct {
	Kind    string `json:"kind"`
	Title   string `json:"title"`
	AgentID string `json:"agent_id,omitempty"`
	Detail  string `json:"detail,omitempty"`
	Content string `json:"content,omitempty"`
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
	Error       string                         `json:"error,omitempty"`
	CreatedAt   string                         `json:"created_at"`
	StartedAt   *string                        `json:"started_at,omitempty"`
	FinishedAt  *string                        `json:"finished_at,omitempty"`
}

type workspaceMemoryCurationStatusResponse struct {
	WorkspaceID   string                              `json:"workspace_id"`
	PendingRuns   int                                 `json:"pending_runs"`
	FailedRuns24h int                                 `json:"failed_runs_24h"`
	Stages        []memoryCurationStageStatusResponse `json:"stages"`
	// Funnel registry counts — workspace-scoped, independent of the last run's
	// evolution-review submissions. The Evolution Center memory card uses these
	// so "awaiting shared review" reflects DB pending candidates, not an empty
	// cross-product with skill/memory review queues.
	LocalProposals     int `json:"local_proposals"`
	PendingCandidates  int `json:"pending_candidates"`
	PendingSkills      int `json:"pending_skills"`
	PromotedCandidates int `json:"promoted_candidates"`
	TeamKnowledgeItems int `json:"team_knowledge_items"`
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
	member, ok := h.workspaceMember(w, r, workspaceID)
	if !ok {
		return
	}
	if !roleAllowed(member.Role, "owner", "admin") {
		writeError(w, http.StatusForbidden, "only workspace owner/admin can run team curation")
		return
	}
	if h.graphMemoryTypeForWorkspace(r.Context(), parseUUID(workspaceID)) == "graph" {
		writeLegacyCurationNotApplicable(w)
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
	// Match the scheduled curator: default to yesterday in the profile timezone.
	// Explicit since/until (or the backfill API) still overrides this.
	since, until := defaultMemoryCurationPlanDay(profile.Timezone, time.Now().UTC())
	if req.Until != "" {
		until, err = time.Parse("2006-01-02", req.Until)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid until date")
			return
		}
		if req.Since == "" {
			since = until
		}
	}
	if req.Since != "" {
		since, err = time.Parse("2006-01-02", req.Since)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid since date")
			return
		}
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
	trigger := "manual"
	if req.IncludeHistory {
		trigger = "backfill"
	}
	runID, status, err := h.enqueueMemoryCurationRun(r.Context(), enqueueMemoryCurationRunParams{
		WorkspaceID: workspaceID,
		MemberID:    uuidToString(member.ID),
		Profile:     profile,
		Stage:       stage,
		TriggerKind: trigger,
		RunStatus:   runStatus,
		Since:       since,
		Until:       until,
		AgentIDs:    agentIDs,
		DryRun:      req.DryRun,
		Force:       req.Force,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create curation run")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"id": runID, "status": status})
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

	// Status polling is the evolution UI's heartbeat. Sweep zombie running
	// rows here so a stuck claim cannot leave the page spinning forever when
	// daemon WS delivery fails or skips the claim/fail path.
	if err := h.failExpiredMemoryCurationRunsForWorkspace(r.Context(), workspaceID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to sweep expired memory curation runs")
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
		       id::text, stage, trigger_kind, status, stats, error, created_at, started_at, finished_at
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
		if err := rows.Scan(&stage.ID, &stage.Stage, &stage.TriggerKind, &stage.Status, &stats, &stage.Error, &createdAt, &startedAt, &finishedAt); err != nil {
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

	// Prefer the latest successful self-review candidate count so the top
	// funnel step matches what operators just saw in the stage card.
	for _, stage := range response.Stages {
		if stage.Stage == "agent_self_review" || stage.Stage == "all" {
			if stage.Stats.ReviewCandidatesAdded > response.LocalProposals {
				response.LocalProposals = stage.Stats.ReviewCandidatesAdded
			}
		}
	}

	if err := h.DB.QueryRow(r.Context(), `
		SELECT
		  COALESCE(count(*) FILTER (WHERE status = 'pending'), 0),
		  COALESCE(count(*) FILTER (WHERE status = 'pending' AND candidate_type IN ('skill', 'team_skill')), 0),
		  COALESCE(count(*) FILTER (WHERE status = 'promoted'), 0)
		  FROM agent_memory_curation_candidate
		 WHERE workspace_id = $1
	`, workspaceID).Scan(&response.PendingCandidates, &response.PendingSkills, &response.PromotedCandidates); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load memory curation candidate funnel")
		return
	}
	if err := h.DB.QueryRow(r.Context(), `
		SELECT count(*) FROM team_knowledge_item WHERE workspace_id = $1
	`, workspaceID).Scan(&response.TeamKnowledgeItems); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load team knowledge funnel")
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
	var startedAt, finishedAt, claimedAt, runtimeLastSeenAt *time.Time
	err := h.DB.QueryRow(r.Context(), `
		SELECT r.id::text, r.workspace_id::text, COALESCE(r.agent_id::text, ''), r.stage, r.trigger_kind, r.status,
		       COALESCE(r.date_from::text, ''), COALESCE(r.date_to::text, ''), r.dry_run, r.force, r.stats, r.error,
		       COALESCE(r.runtime_id::text, ''), COALESCE(rt.name, ''), COALESCE(rt.device_info, ''), rt.last_seen_at,
		       r.attempt, r.claimed_at,
		       COALESCE(r.curator_agent_id::text, ''), COALESCE(curator.name, ''), COALESCE(r.curator_model, ''),
		       COALESCE(r.curator_mode, ''), r.confidence_threshold,
		       COALESCE((SELECT array_agg(t.id::text ORDER BY t.id::text) FROM unnest(r.target_agent_ids) AS t(id)), '{}'::text[]),
		       r.created_at, r.started_at, r.finished_at
		  FROM memory_curation_run r
		  LEFT JOIN agent_runtime rt ON rt.id = r.runtime_id
		  LEFT JOIN agent curator ON curator.id = r.curator_agent_id
		 WHERE r.workspace_id = $1 AND r.id = $2
	`, workspaceID, runID).Scan(&resp.ID, &resp.WorkspaceID, &agentID, &resp.Stage, &resp.TriggerKind, &resp.Status, &dateFrom, &dateTo, &resp.DryRun, &resp.Force, &stats, &resp.Error, &resp.RuntimeID, &resp.RuntimeName, &resp.RuntimeDeviceInfo, &runtimeLastSeenAt, &resp.Attempt, &claimedAt, &resp.CuratorAgentID, &resp.CuratorAgentName, &resp.CuratorModel, &resp.CuratorMode, &resp.ConfidenceThreshold, &resp.TargetAgentIDs, &createdAt, &startedAt, &finishedAt)
	if err != nil {
		return resp, err
	}
	resp.Stats = json.RawMessage(stats)
	resp.StatsSummary = publicMemoryCurationStats(stats)
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
	if runtimeLastSeenAt != nil {
		s := runtimeLastSeenAt.UTC().Format(time.RFC3339)
		resp.RuntimeLastSeenAt = &s
	}
	if claimedAt != nil {
		s := claimedAt.UTC().Format(time.RFC3339)
		resp.ClaimedAt = &s
		resp.ClaimedAgeSeconds = int(time.Since(*claimedAt).Seconds())
		if resp.ClaimedAgeSeconds < 0 {
			resp.ClaimedAgeSeconds = 0
		}
	}
	agentNames, _ := h.memoryCurationAgentNames(r.Context(), workspaceID, resp.TargetAgentIDs)
	for _, id := range resp.TargetAgentIDs {
		resp.TargetAgents = append(resp.TargetAgents, memoryCurationTargetAgent{ID: id, Name: agentNames[id]})
	}
	resp.Diagnostics = memoryCurationDiagnostics(resp.Error)
	resp.Timeline = buildMemoryCurationTimeline(resp, stats, createdAt, startedAt, finishedAt)
	resp.AgentResults, resp.Artifacts = buildMemoryCurationArtifacts(stats, agentNames)
	resp.ChildRuns = []memoryCurationChildRunResponse{}
	if childRuns, err := h.loadMemoryCurationChildRuns(r.Context(), workspaceID, resp.ID); err == nil {
		resp.ChildRuns = childRuns
	}
	return resp, nil
}

func (h *Handler) loadMemoryCurationChildRuns(ctx context.Context, workspaceID, parentRunID string) ([]memoryCurationChildRunResponse, error) {
	rows, err := h.DB.Query(ctx, `
		SELECT cr.id::text, cr.parent_run_id::text, cr.workspace_id::text, cr.agent_id::text,
		       COALESCE(a.name, ''), COALESCE(cr.runtime_id::text, ''), COALESCE(rt.name, ''),
		       cr.stage, cr.status, cr.attempt, cr.started_at, cr.finished_at, cr.error,
		       COALESCE((cr.stats->>'changed')::boolean, false),
		       COALESCE((cr.stats->>'daily_files_written')::int, 0),
		       COALESCE((cr.stats->>'review_candidates_added')::int, 0),
		       COALESCE((cr.stats->>'skill_candidates_added')::int, 0),
		       COALESCE((cr.stats->>'evidence_collected')::int, 0),
		       COALESCE((cr.stats->>'conflicts_found')::int, 0),
		       left(COALESCE(cr.output->>'curator_output', ''), 1200)
		  FROM memory_curation_agent_run cr
		  LEFT JOIN agent a ON a.id = cr.agent_id
		  LEFT JOIN agent_runtime rt ON rt.id = cr.runtime_id
		 WHERE cr.workspace_id = $1::uuid AND cr.parent_run_id = $2::uuid
		 ORDER BY cr.created_at, cr.agent_id
	`, workspaceID, parentRunID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []memoryCurationChildRunResponse{}
	for rows.Next() {
		var item memoryCurationChildRunResponse
		var startedAt, finishedAt *time.Time
		if err := rows.Scan(&item.ID, &item.ParentRunID, &item.WorkspaceID, &item.AgentID, &item.AgentName, &item.RuntimeID, &item.RuntimeName, &item.Stage, &item.Status, &item.Attempt, &startedAt, &finishedAt, &item.Error, &item.Changed, &item.DailyFilesWritten, &item.ReviewCandidatesAdded, &item.SkillCandidatesAdded, &item.EvidenceCollected, &item.ConflictsFound, &item.OutputExcerpt); err != nil {
			return nil, err
		}
		if startedAt != nil {
			value := startedAt.UTC().Format(time.RFC3339)
			item.StartedAt = &value
		}
		if finishedAt != nil {
			value := finishedAt.UTC().Format(time.RFC3339)
			item.FinishedAt = &value
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (h *Handler) memoryCurationAgentNames(ctx context.Context, workspaceID string, agentIDs []string) (map[string]string, error) {
	out := map[string]string{}
	if len(agentIDs) == 0 {
		return out, nil
	}
	rows, err := h.DB.Query(ctx, `
		SELECT id::text, name
		  FROM agent
		 WHERE workspace_id = $1 AND id::text = ANY($2)
	`, workspaceID, agentIDs)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			return out, err
		}
		out[id] = name
	}
	return out, rows.Err()
}

func memoryCurationDiagnostics(errText string) []memoryCurationRunDiagnostic {
	errText = strings.TrimSpace(errText)
	if errText == "" {
		return nil
	}
	if strings.Contains(errText, "unknown memory curation stage") {
		return []memoryCurationRunDiagnostic{{
			Severity: "error",
			Code:     "daemon_stage_unsupported",
			Message:  "The selected daemon does not understand the curation stage sent by the server.",
			Action:   "Update or rebuild the multica daemon, then restart it and rerun curation.",
		}}
	}
	if strings.Contains(strings.ToLower(errText), "rate limit") || strings.Contains(errText, "429") {
		return []memoryCurationRunDiagnostic{{Severity: "warning", Code: "provider_rate_limit", Message: "The curator model provider rate-limited the run.", Action: "Retry after the quota window resets or choose another model/runtime."}}
	}
	return []memoryCurationRunDiagnostic{{Severity: "error", Code: "run_failed", Message: errText}}
}

func buildMemoryCurationTimeline(resp memoryCurationRunResponse, raw []byte, createdAt time.Time, startedAt, finishedAt *time.Time) []memoryCurationRunTimelineItem {
	created := createdAt.UTC().Format(time.RFC3339)
	items := []memoryCurationRunTimelineItem{{Key: "queued", Label: "Queued", Status: "done", Timestamp: created, Detail: resp.TriggerKind}}
	var result memorycuration.Result
	if err := json.Unmarshal(raw, &result); err == nil && len(result.Events) > 0 {
		if startedAt != nil {
			items = append(items, memoryCurationRunTimelineItem{Key: "claimed", Label: "Claimed by runtime", Status: "done", Timestamp: startedAt.UTC().Format(time.RFC3339), Detail: resp.RuntimeName})
		}
		for _, ev := range result.Events {
			items = append(items, memoryCurationRunTimelineItem{Key: ev.Key, AgentID: ev.AgentID, Label: curationEventLabel(ev.Key), Status: ev.Status, Timestamp: ev.CreatedAt, Detail: ev.Message})
		}
		if finishedAt != nil {
			finalStatus := "done"
			if resp.Status == "failed" || resp.Status == "invalid_config" {
				finalStatus = "failed"
			}
			items = append(items, memoryCurationRunTimelineItem{Key: "completed", Label: "Completed", Status: finalStatus, Timestamp: finishedAt.UTC().Format(time.RFC3339), Detail: resp.Error})
		}
		return items
	}
	if startedAt == nil {
		items = append(items, memoryCurationRunTimelineItem{Key: "claimed", Label: "Claimed by runtime", Status: "pending", Detail: resp.RuntimeName})
		return items
	}
	started := startedAt.UTC().Format(time.RFC3339)
	items = append(items, memoryCurationRunTimelineItem{Key: "claimed", Label: "Claimed by runtime", Status: "done", Timestamp: started, Detail: resp.RuntimeName})
	if finishedAt == nil {
		items = append(items, memoryCurationRunTimelineItem{Key: "invoked_curator", Label: "Invoked curator agent", Status: "running", Timestamp: started, Detail: resp.CuratorAgentName})
		return items
	}
	items = append(items,
		memoryCurationRunTimelineItem{Key: "validated_profile", Label: "Validated profile", Status: "done", Timestamp: started, Detail: resp.CuratorMode},
		memoryCurationRunTimelineItem{Key: "resolved_targets", Label: "Resolved target agents", Status: "done", Timestamp: started, Detail: strings.Join(resp.TargetAgentIDs, ", ")},
	)
	if resp.StatsSummary.EvidenceCollected > 0 {
		evidenceLabel := "Collected DB evidence"
		evidenceDetail := plural(resp.StatsSummary.EvidenceCollected, "evidence item")
		if resp.Stage == string(memorycuration.StageTeamCuration) {
			evidenceLabel = "Loaded self-review artifacts"
			evidenceDetail = plural(resp.StatsSummary.EvidenceCollected, "artifact")
		}
		items = append(items, memoryCurationRunTimelineItem{Key: "collected_evidence", Label: evidenceLabel, Status: "done", Timestamp: started, Detail: evidenceDetail})
	} else {
		evidenceLabel := "Collected DB evidence"
		evidenceDetail := "0 evidence items"
		if resp.Stage == string(memorycuration.StageTeamCuration) {
			evidenceLabel = "Loaded self-review artifacts"
			evidenceDetail = "0 artifacts"
		}
		items = append(items, memoryCurationRunTimelineItem{Key: "collected_evidence", Label: evidenceLabel, Status: "skipped", Timestamp: started, Detail: evidenceDetail})
	}
	if resp.StatsSummary.AgentsScanned > 0 {
		items = append(items, memoryCurationRunTimelineItem{Key: "read_local_files", Label: "Read local memory files", Status: "done", Timestamp: started, Detail: plural(resp.StatsSummary.AgentsScanned, "agent")})
	}
	invokedStatus := "done"
	if resp.Error != "" && resp.StatsSummary.AgentsScanned == 0 {
		invokedStatus = "failed"
	}
	items = append(items, memoryCurationRunTimelineItem{Key: "invoked_curator", Label: "Invoked curator agent", Status: invokedStatus, Timestamp: started, Detail: resp.CuratorAgentName})
	if finishedAt != nil {
		finished := finishedAt.UTC().Format(time.RFC3339)
		finalStatus := "done"
		if resp.Status == "failed" || resp.Status == "invalid_config" {
			finalStatus = "failed"
		}
		items = append(items, memoryCurationRunTimelineItem{Key: "completed", Label: "Completed", Status: finalStatus, Timestamp: finished, Detail: resp.Error})
	}
	return items
}

func curationEventLabel(key string) string {
	switch key {
	case "validated_profile":
		return "Validated profile"
	case "resolved_targets":
		return "Resolved target agents"
	case "read_local_files":
		return "Read local memory files"
	case "invoked_curator":
		return "Invoked curator agent"
	case "parsed_output":
		return "Parsed curator output"
	case "persisted_candidates":
		return "Persisted candidates"
	default:
		return key
	}
}

func buildMemoryCurationArtifacts(raw []byte, agentNames map[string]string) ([]memoryCurationAgentRunResponse, []memoryCurationRunArtifact) {
	var result memorycuration.Result
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, nil
	}
	errs := map[string]string{}
	for _, item := range result.Errors {
		errs[item.AgentID] = item.Error
	}
	agentResults := make([]memoryCurationAgentRunResponse, 0, len(result.AgentResults))
	artifacts := []memoryCurationRunArtifact{}
	for _, ar := range result.AgentResults {
		excerpt := truncateForAPI(strings.TrimSpace(ar.CuratorOutput), 1200)
		agentResults = append(agentResults, memoryCurationAgentRunResponse{
			WorkspaceID: ar.WorkspaceID, AgentID: ar.AgentID, AgentName: agentNames[ar.AgentID], Root: ar.Root, Changed: ar.Changed,
			DailyFilesWritten: ar.DailyFilesWritten, ReviewCandidatesAdded: ar.ReviewCandidatesAdded, SkillCandidatesAdded: ar.SkillCandidatesAdded,
			EvidenceCollected: ar.EvidenceCollected, ConflictsFound: ar.ConflictsFound, Error: errs[ar.AgentID], CuratorOutputExcerpt: excerpt,
		})
		if excerpt != "" {
			artifacts = append(artifacts, memoryCurationRunArtifact{Kind: "curator_output", Title: "Curator raw output", AgentID: ar.AgentID, Content: excerpt})
		}
		if ar.DailyFilesWritten > 0 {
			artifacts = append(artifacts, memoryCurationRunArtifact{Kind: "daily", Title: "Daily memory file", AgentID: ar.AgentID, Detail: plural(ar.DailyFilesWritten, "file written")})
		}
		if ar.ReviewCandidatesAdded > 0 || ar.SkillCandidatesAdded > 0 {
			artifacts = append(artifacts, memoryCurationRunArtifact{Kind: "proposal", Title: "Review/proposal candidates", AgentID: ar.AgentID, Detail: plural(ar.ReviewCandidatesAdded, "memory candidate") + ", " + plural(ar.SkillCandidatesAdded, "skill candidate")})
		}
	}
	if result.SharedCandidatesAdded > 0 {
		artifacts = append(artifacts, memoryCurationRunArtifact{Kind: "team_knowledge", Title: "Team knowledge candidates", Detail: plural(result.SharedCandidatesAdded, "item")})
	}
	return agentResults, artifacts
}

func plural(n int, unit string) string {
	if n == 1 {
		return "1 " + unit
	}
	return strconv.Itoa(n) + " " + unit + "s"
}

func truncateForAPI(v string, max int) string {
	if max <= 0 || len(v) <= max {
		return v
	}
	return v[:max] + "..."
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
