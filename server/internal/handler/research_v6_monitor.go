package handler

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/researchrun"
)

type researchMonitorRow struct {
	ID                   string    `json:"id"`
	WorkspaceID          string    `json:"workspace_id"`
	SessionID            string    `json:"session_id"`
	QuestionID           string    `json:"question_id,omitempty"`
	SearchPlanID         string    `json:"search_plan_id,omitempty"`
	SearchPlanVersion    int       `json:"search_plan_version"`
	BaselineReportID     string    `json:"baseline_report_id,omitempty"`
	Status               string    `json:"status"`
	IntervalSeconds      int       `json:"interval_seconds"`
	NextRunAt            time.Time `json:"next_run_at"`
	MaterialityThreshold float64   `json:"materiality_threshold"`
	RemainingBudget      float64   `json:"remaining_budget"`
	LastCycleStatus      string    `json:"last_cycle_status,omitempty"`
	LastCycleReason      string    `json:"last_cycle_reason,omitempty"`
}

func (h *Handler) ListResearchMonitors(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	if _, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id"); !ok {
		return
	}
	monitors, err := h.listResearchMonitors(r.Context(), workspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list research monitors")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"monitors": monitors})
}

type createResearchMonitorRequest struct {
	SessionID            string  `json:"session_id"`
	QuestionID           string  `json:"question_id"`
	SearchPlanID         string  `json:"search_plan_id"`
	SearchPlanVersion    int     `json:"search_plan_version"`
	BaselineReportID     string  `json:"baseline_report_id"`
	IntervalSeconds      int     `json:"interval_seconds"`
	MaterialityThreshold float64 `json:"materiality_threshold"`
}

func (h *Handler) CreateResearchMonitor(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	if _, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id"); !ok {
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	if !h.isWorkspaceOwnerOrAdmin(r.Context(), workspaceID, parseUUID(userID)) {
		writeError(w, http.StatusForbidden, "only workspace owners or admins can create research monitors")
		return
	}
	var req createResearchMonitorRequest
	if !decodeResearchJSON(w, r, &req) {
		return
	}
	if _, ok = parseUUIDOrBadRequest(w, req.SessionID, "session_id"); !ok {
		return
	}
	if req.IntervalSeconds < 60 {
		writeError(w, http.StatusBadRequest, "interval_seconds must be at least 60")
		return
	}
	if req.SearchPlanVersion < 1 || req.MaterialityThreshold < 0 || req.MaterialityThreshold > 1 ||
		strings.TrimSpace(req.QuestionID) == "" || strings.TrimSpace(req.SearchPlanID) == "" || strings.TrimSpace(req.BaselineReportID) == "" {
		writeError(w, http.StatusBadRequest, "monitor contract is incomplete")
		return
	}
	id := uuid.NewString()
	now := time.Now().UTC()
	_, err := h.DB.Exec(r.Context(), `
		INSERT INTO research_monitor (
		  id, workspace_id, session_id, question_id, search_plan_id, search_plan_version,
		  baseline_report_id, status, interval_seconds, next_run_at, materiality_threshold,
		  remaining_budget, created_by
		) VALUES ($1::uuid,$2::uuid,$3::uuid,NULLIF($4,'')::uuid,NULLIF($5,'')::uuid,$6,
		          NULLIF($7,'')::uuid,'active',$8,$9,$10,1,$11::uuid)
	`, id, workspaceID, req.SessionID, req.QuestionID, req.SearchPlanID, req.SearchPlanVersion,
		req.BaselineReportID, req.IntervalSeconds, now.Add(time.Duration(req.IntervalSeconds)*time.Second),
		req.MaterialityThreshold, userID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to create research monitor")
		return
	}
	writeJSON(w, http.StatusCreated, researchMonitorRow{
		ID: id, WorkspaceID: workspaceID, SessionID: req.SessionID, QuestionID: req.QuestionID,
		SearchPlanID: req.SearchPlanID, SearchPlanVersion: req.SearchPlanVersion, BaselineReportID: req.BaselineReportID,
		Status: "active", IntervalSeconds: req.IntervalSeconds,
		NextRunAt:            now.Add(time.Duration(req.IntervalSeconds) * time.Second),
		MaterialityThreshold: req.MaterialityThreshold, RemainingBudget: 1,
	})
}

type patchResearchMonitorRequest struct {
	Status string `json:"status"`
}

func (h *Handler) PatchResearchMonitor(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	if _, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id"); !ok {
		return
	}
	monitorID := strings.TrimSpace(chi.URLParam(r, "monitorId"))
	if _, ok := parseUUIDOrBadRequest(w, monitorID, "monitorId"); !ok {
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	if !h.isWorkspaceOwnerOrAdmin(r.Context(), workspaceID, parseUUID(userID)) {
		writeError(w, http.StatusForbidden, "only workspace owners or admins can update research monitors")
		return
	}
	var req patchResearchMonitorRequest
	if !decodeResearchJSON(w, r, &req) {
		return
	}
	if req.Status != "paused" && req.Status != "cancelled" && req.Status != "active" {
		writeError(w, http.StatusBadRequest, "status must be active, paused, or cancelled")
		return
	}
	tag, err := h.DB.Exec(r.Context(), `
		UPDATE research_monitor SET status = $3, updated_at = now()
		WHERE workspace_id = $1::uuid AND id = $2::uuid
	`, workspaceID, monitorID, req.Status)
	if err != nil || tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "research monitor not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": monitorID, "status": req.Status})
}

func (h *Handler) listResearchMonitors(ctx context.Context, workspaceID string) ([]researchMonitorRow, error) {
	if h.DB == nil {
		return []researchMonitorRow{}, nil
	}
	rows, err := h.DB.Query(ctx, `
		SELECT id::text, workspace_id::text, session_id::text, COALESCE(question_id::text,''),
		       COALESCE(search_plan_id::text,''), search_plan_version, COALESCE(baseline_report_id::text,''),
		       status, interval_seconds, next_run_at, materiality_threshold, remaining_budget,
		       last_cycle_status, last_cycle_reason
		FROM research_monitor WHERE workspace_id = $1::uuid
		ORDER BY created_at DESC
	`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]researchMonitorRow, 0)
	for rows.Next() {
		var row researchMonitorRow
		if err = rows.Scan(&row.ID, &row.WorkspaceID, &row.SessionID, &row.QuestionID, &row.SearchPlanID,
			&row.SearchPlanVersion, &row.BaselineReportID, &row.Status, &row.IntervalSeconds, &row.NextRunAt,
			&row.MaterialityThreshold, &row.RemainingBudget, &row.LastCycleStatus, &row.LastCycleReason); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (h *Handler) ProcessDueResearchMonitors(ctx context.Context, limit int) (int, error) {
	if h.DB == nil || limit <= 0 {
		return 0, nil
	}
	rows, err := h.DB.Query(ctx, `
		SELECT id::text, workspace_id::text, session_id::text, COALESCE(question_id::text,''),
		       COALESCE(search_plan_id::text,''), search_plan_version, COALESCE(baseline_report_id::text,''),
		       status, interval_seconds, next_run_at, materiality_threshold, remaining_budget,
		       credentials_valid, source_reachable
		FROM research_monitor
		WHERE status = 'active' AND next_run_at <= now()
		ORDER BY next_run_at
		LIMIT $1
	`, limit)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	type due struct {
		row              researchMonitorRow
		credentialsValid bool
		sourceReachable  bool
	}
	pending := make([]due, 0)
	for rows.Next() {
		var item due
		if err = rows.Scan(&item.row.ID, &item.row.WorkspaceID, &item.row.SessionID, &item.row.QuestionID,
			&item.row.SearchPlanID, &item.row.SearchPlanVersion, &item.row.BaselineReportID, &item.row.Status,
			&item.row.IntervalSeconds, &item.row.NextRunAt, &item.row.MaterialityThreshold, &item.row.RemainingBudget,
			&item.credentialsValid, &item.sourceReachable); err != nil {
			return 0, err
		}
		pending = append(pending, item)
	}
	if err = rows.Err(); err != nil {
		return 0, err
	}
	processed := 0
	now := time.Now().UTC()
	for _, item := range pending {
		monitor := researchrun.ResearchMonitor{
			MonitorID: item.row.ID, Status: researchrun.ResearchMonitorStatus(item.row.Status),
			QuestionID: item.row.QuestionID, SearchPlanID: item.row.SearchPlanID,
			SearchPlanVersion: item.row.SearchPlanVersion, BaselineReportID: item.row.BaselineReportID,
			Interval: time.Duration(item.row.IntervalSeconds) * time.Second, NextRunAt: item.row.NextRunAt,
			MaterialityThreshold: item.row.MaterialityThreshold, CredentialsValid: item.credentialsValid,
			SourceReachable: item.sourceReachable, RemainingBudget: item.row.RemainingBudget,
		}
		cycle := researchrun.MonitoringCycleInput{CycleID: now.Format(time.RFC3339Nano), Now: now}
		if item.row.SearchPlanID != "" {
			cycle.SearchPlanID = item.row.SearchPlanID
			cycle.SearchPlanVersion = item.row.SearchPlanVersion
		}
		decision, evalErr := researchrun.EvaluateMonitoringCycle(monitor, cycle)
		status, reason := "not_eligible", "monitor_cycle_incomplete"
		next := item.row.NextRunAt.Add(time.Duration(item.row.IntervalSeconds) * time.Second)
		persistStatus := item.row.Status
		if evalErr != nil {
			status, reason = "blocked", evalErr.Error()
			if strings.Contains(evalErr.Error(), "pinned Search Plan") || strings.Contains(evalErr.Error(), "incomplete") {
				persistStatus = "blocked"
			}
		} else {
			status, reason, next = decision.Status, decision.Reason, decision.NextRunAt
			if decision.Status == "blocked" || decision.Status == "budget_exhausted" {
				persistStatus = decision.Status
			}
		}
		if _, err = h.DB.Exec(ctx, `
			INSERT INTO research_monitor_cycle (workspace_id, monitor_id, cycle_key, status, reason, decided_at)
			VALUES ($1::uuid,$2::uuid,$3,$4,$5,$6)
			ON CONFLICT (workspace_id, monitor_id, cycle_key) DO NOTHING
		`, item.row.WorkspaceID, item.row.ID, cycle.CycleID, status, truncateString(reason, 1024), now); err != nil {
			return processed, err
		}
		if _, err = h.DB.Exec(ctx, `
			UPDATE research_monitor
			SET status = $3, next_run_at = $4, last_cycle_status = $5, last_cycle_reason = $6, updated_at = now()
			WHERE workspace_id = $1::uuid AND id = $2::uuid
		`, item.row.WorkspaceID, item.row.ID, persistStatus, next, status, truncateString(reason, 1024)); err != nil {
			return processed, err
		}
		processed++
	}
	return processed, nil
}
