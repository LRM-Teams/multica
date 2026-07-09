package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

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
	if len(agentIDs) == 0 && !req.AllAgents {
		writeError(w, http.StatusBadRequest, "agent_id, agent_ids, or all_agents is required")
		return
	}
	now := time.Now().UTC()
	until := now.AddDate(0, 0, -1)
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
	root := memorycuration.DefaultWorkspacesRoot()
	if root == "" {
		writeError(w, http.StatusInternalServerError, "workspaces root is not configured")
		return
	}
	dbStage := dbStageName(stage)
	trigger := "manual"
	if req.IncludeHistory {
		trigger = "backfill"
	}
	var agentForRun any
	if len(agentIDs) == 1 && !req.AllAgents {
		agentForRun = agentIDs[0]
	}
	var requestedBy any
	if m, ok := ctxMember(r.Context()); ok {
		requestedBy = uuidToString(m.ID)
	}
	var runID string
	if err := h.DB.QueryRow(r.Context(), `
		INSERT INTO memory_curation_run (workspace_id, agent_id, stage, trigger_kind, status, date_from, date_to, dry_run, force, requested_by, started_at)
		VALUES ($1, $2, $3, $4, 'running', $5, $6, $7, $8, $9, now())
		RETURNING id
	`, workspaceID, agentForRun, dbStage, trigger, since, until, req.DryRun, req.Force, requestedBy).Scan(&runID); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("create curation run: %v", err))
		return
	}
	res, runErr := memorycuration.NewEngine().Run(memorycuration.Options{
		Context:        r.Context(),
		DB:             h.DB,
		WorkspacesRoot: root,
		WorkspaceID:    workspaceID,
		AgentIDs:       agentIDs,
		AllAgents:      req.AllAgents,
		Stage:          stage,
		Since:          since,
		Until:          until,
		IncludeHistory: req.IncludeHistory,
		DryRun:         req.DryRun,
		Force:          req.Force,
		Now:            now,
		Timezone:       memorycuration.DefaultTimezone,
	})
	statsJSON, _ := json.Marshal(res)
	status := "succeeded"
	errText := ""
	if runErr != nil || len(res.Errors) > 0 {
		status = "failed"
		if runErr != nil {
			errText = runErr.Error()
		} else {
			errText = "one or more agents failed"
		}
	}
	if _, err := h.DB.Exec(r.Context(), `
		UPDATE memory_curation_run
		   SET status = $2, stats = $3::jsonb, error = $4, finished_at = now()
		 WHERE id = $1
	`, runID, status, string(statsJSON), errText); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("finish curation run: %v", err))
		return
	}
	if runErr != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"id": runID, "result": res, "error": runErr.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": runID, "result": res})
}

func (h *Handler) GetMemoryCurationRun(w http.ResponseWriter, r *http.Request) {
	workspaceID := workspaceIDFromURL(r, "id")
	runID := chi.URLParam(r, "runId")
	row, err := h.loadMemoryCurationRun(r, workspaceID, runID)
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

func (h *Handler) GetAgentMemoryCurationStatus(w http.ResponseWriter, r *http.Request) {
	agentID := chi.URLParam(r, "id")
	var workspaceID string
	var pending int
	var lastRun json.RawMessage
	err := h.DB.QueryRow(r.Context(), `
		SELECT a.workspace_id::text,
		       COALESCE((SELECT count(*) FROM memory_curation_run r WHERE r.agent_id = a.id AND r.status IN ('queued','running')), 0),
		       COALESCE((
		         SELECT jsonb_build_object('id', r.id, 'stage', r.stage, 'status', r.status, 'created_at', r.created_at, 'finished_at', r.finished_at)
		           FROM memory_curation_run r
		          WHERE r.agent_id = a.id OR (r.agent_id IS NULL AND r.workspace_id = a.workspace_id)
		          ORDER BY r.created_at DESC
		          LIMIT 1
		       ), '{}'::jsonb)
		  FROM agent a
		 WHERE a.id = $1
	`, agentID).Scan(&workspaceID, &pending, &lastRun)
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

func (h *Handler) loadMemoryCurationRun(r *http.Request, workspaceID, runID string) (memoryCurationRunResponse, error) {
	var resp memoryCurationRunResponse
	var stats []byte
	var agentID, dateFrom, dateTo string
	var startedAt, finishedAt *time.Time
	err := h.DB.QueryRow(r.Context(), `
		SELECT id::text, workspace_id::text, COALESCE(agent_id::text, ''), stage, trigger_kind, status,
		       COALESCE(date_from::text, ''), COALESCE(date_to::text, ''), dry_run, force, stats, error, created_at, started_at, finished_at
		  FROM memory_curation_run
		 WHERE workspace_id = $1 AND id = $2
	`, workspaceID, runID).Scan(&resp.ID, &resp.WorkspaceID, &agentID, &resp.Stage, &resp.TriggerKind, &resp.Status, &dateFrom, &dateTo, &resp.DryRun, &resp.Force, &stats, &resp.Error, &resp.CreatedAt, &startedAt, &finishedAt)
	if err != nil {
		return resp, err
	}
	resp.Stats = json.RawMessage(stats)
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

func dbStageName(stage memorycuration.Stage) string {
	switch stage {
	case memorycuration.StageL1:
		return "l1_daily"
	case memorycuration.StageL2:
		return "l2_review"
	case memorycuration.StageL3:
		return "l3_promote"
	case memorycuration.StageL4:
		return "l4_curator"
	default:
		return "all"
	}
}
