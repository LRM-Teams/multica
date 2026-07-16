package handler

import (
	"net/http"
	"strconv"
	"strings"
)

type EvolutionMetricsResponse struct {
	UnitMetrics    []EvolutionUnitMetricResponse   `json:"unit_metrics"`
	DailyMetrics   []EvolutionDailyMetricResponse  `json:"daily_metrics"`
	TaskEfficiency EvolutionTaskEfficiencyResponse `json:"task_efficiency"`
}

type EvolutionUnitMetricResponse struct {
	UnitID        *string `json:"unit_id,omitempty"`
	LocalUnitID   string  `json:"local_unit_id"`
	UnitType      string  `json:"unit_type"`
	Title         string  `json:"title"`
	InjectedCount int64   `json:"injected_count"`
	UsedCount     int64   `json:"used_count"`
	SuccessCount  int64   `json:"success_count"`
	FailureCount  int64   `json:"failure_count"`
	IgnoredCount  int64   `json:"ignored_count"`
	ConflictCount int64   `json:"conflict_count"`
	SuccessRate   float64 `json:"success_rate"`
	LastUsedAt    *string `json:"last_used_at,omitempty"`
}

type EvolutionDailyMetricResponse struct {
	Date                   string `json:"date"`
	MemoryCandidates       int64  `json:"memory_candidates"`
	SkillCandidates        int64  `json:"skill_candidates"`
	PromotedMemory         int64  `json:"promoted_memory"`
	PromotedSkill          int64  `json:"promoted_skill"`
	ArchivedOrDeprecated   int64  `json:"archived_or_deprecated"`
	FeedbackInjected       int64  `json:"feedback_injected"`
	FeedbackUsed           int64  `json:"feedback_used"`
	FeedbackSuccess        int64  `json:"feedback_success"`
	FeedbackFailure        int64  `json:"feedback_failure"`
	MemoryCurationRunCount int64  `json:"memory_curation_run_count"`
	MemoryCurationFailed   int64  `json:"memory_curation_failed"`
}

type EvolutionTaskEfficiencyResponse struct {
	IssueCount                    int64   `json:"issue_count"`
	AverageDurationSeconds        float64 `json:"average_duration_seconds"`
	AverageInputTokens            float64 `json:"average_input_tokens"`
	AverageOutputTokens           float64 `json:"average_output_tokens"`
	AverageCacheReadTokens        float64 `json:"average_cache_read_tokens"`
	AverageCacheWriteTokens       float64 `json:"average_cache_write_tokens"`
	AverageEvolvedUnitsUsed       float64 `json:"average_evolved_units_used"`
	WithEvolvedUnitsIssueCount    int64   `json:"with_evolved_units_issue_count"`
	WithoutEvolvedUnitsIssueCount int64   `json:"without_evolved_units_issue_count"`
}

func (h *Handler) GetEvolutionMetrics(w http.ResponseWriter, r *http.Request) {
	workspaceID := ctxWorkspaceID(r.Context())
	if workspaceID == "" {
		workspaceID = strings.TrimSpace(r.URL.Query().Get("workspace_id"))
	}
	if workspaceID == "" {
		writeError(w, http.StatusBadRequest, "workspace_id is required")
		return
	}
	unitType := strings.TrimSpace(r.URL.Query().Get("unit_type"))
	days := 30
	if rawDays := strings.TrimSpace(r.URL.Query().Get("days")); rawDays != "" {
		if parsed, err := strconv.Atoi(rawDays); err == nil && parsed > 0 && parsed <= 365 {
			days = parsed
		}
	}
	rows, err := h.DB.Query(r.Context(), `
		WITH feedback AS (
		  SELECT unit_type, unit_id, local_unit_id,
		         count(*) FILTER (WHERE event = 'injected') AS injected_count,
		         count(*) FILTER (WHERE event = 'used') AS used_count,
		         count(*) FILTER (WHERE event = 'success' OR outcome = 'success') AS success_count,
		         count(*) FILTER (WHERE event = 'failure' OR outcome = 'failure') AS failure_count,
		         count(*) FILTER (WHERE event = 'ignored') AS ignored_count,
		         count(*) FILTER (WHERE event = 'conflict') AS conflict_count,
		         max(created_at) FILTER (WHERE event IN ('used','success','failure')) AS last_used_at
		    FROM evolution_unit_feedback_event
		   WHERE workspace_id = $1
		     AND ($2 = '' OR unit_type = $2)
		   GROUP BY unit_type, unit_id, local_unit_id
		)
		SELECT COALESCE(f.unit_id::text, ''),
		       f.local_unit_id,
		       f.unit_type,
		       COALESCE(am.name, s.name, seu.title, f.local_unit_id, f.unit_type) AS title,
		       f.injected_count,
		       f.used_count,
		       f.success_count,
		       f.failure_count,
		       f.ignored_count,
		       f.conflict_count,
		       CASE WHEN (f.success_count + f.failure_count) > 0 THEN f.success_count::float8 / (f.success_count + f.failure_count) ELSE 0 END AS success_rate,
		       COALESCE(f.last_used_at::text, '')
		  FROM feedback f
		  LEFT JOIN agent_memory am ON am.id = f.unit_id
		  LEFT JOIN skill s ON s.id = f.unit_id
		  LEFT JOIN shared_evolution_unit seu ON seu.id = f.unit_id
		 ORDER BY f.used_count DESC, f.success_count DESC, f.last_used_at DESC NULLS LAST
		 LIMIT 100
	`, workspaceID, unitType)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load evolution metrics")
		return
	}
	defer rows.Close()
	resp := EvolutionMetricsResponse{UnitMetrics: []EvolutionUnitMetricResponse{}, DailyMetrics: []EvolutionDailyMetricResponse{}}
	for rows.Next() {
		var item EvolutionUnitMetricResponse
		var unitID, lastUsedAt string
		if err := rows.Scan(&unitID, &item.LocalUnitID, &item.UnitType, &item.Title, &item.InjectedCount, &item.UsedCount, &item.SuccessCount, &item.FailureCount, &item.IgnoredCount, &item.ConflictCount, &item.SuccessRate, &lastUsedAt); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to scan evolution metrics")
			return
		}
		if unitID != "" {
			item.UnitID = &unitID
		}
		if lastUsedAt != "" {
			item.LastUsedAt = &lastUsedAt
		}
		resp.UnitMetrics = append(resp.UnitMetrics, item)
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load evolution metrics")
		return
	}
	if err := h.loadEvolutionDailyMetrics(r, workspaceID, days, &resp); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load evolution daily metrics")
		return
	}
	if err := h.loadEvolutionTaskEfficiency(r, workspaceID, days, &resp); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load evolution task efficiency")
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) loadEvolutionDailyMetrics(r *http.Request, workspaceID string, days int, resp *EvolutionMetricsResponse) error {
	rows, err := h.DB.Query(r.Context(), `
		WITH days AS (
		  SELECT generate_series((current_date - (($2::int - 1) * interval '1 day'))::date, current_date, interval '1 day')::date AS day
		), submissions AS (
		  SELECT created_at::date AS day,
		         count(*) FILTER (WHERE unit_type IN ('memory','preference')) AS memory_candidates,
		         count(*) FILTER (WHERE unit_type = 'skill') AS skill_candidates
		    FROM evolution_unit_submission
		   WHERE workspace_id = $1 AND created_at >= current_date - (($2::int - 1) * interval '1 day')
		   GROUP BY created_at::date
		), promoted AS (
		  SELECT created_at::date AS day,
		         count(*) FILTER (WHERE unit_type IN ('memory','preference')) AS promoted_memory,
		         count(*) FILTER (WHERE unit_type = 'skill') AS promoted_skill
		    FROM shared_evolution_unit
		   WHERE workspace_id = $1 AND created_at >= current_date - (($2::int - 1) * interval '1 day')
		   GROUP BY created_at::date
		), lifecycle AS (
		  SELECT updated_at::date AS day,
		         count(*) FILTER (WHERE status IN ('archived','deprecated')) AS archived_or_deprecated
		    FROM shared_evolution_unit
		   WHERE workspace_id = $1 AND updated_at >= current_date - (($2::int - 1) * interval '1 day')
		   GROUP BY updated_at::date
		), feedback AS (
		  SELECT created_at::date AS day,
		         count(*) FILTER (WHERE event = 'injected') AS injected,
		         count(*) FILTER (WHERE event = 'used') AS used,
		         count(*) FILTER (WHERE event = 'success' OR outcome = 'success') AS success,
		         count(*) FILTER (WHERE event = 'failure' OR outcome = 'failure') AS failure
		    FROM evolution_unit_feedback_event
		   WHERE workspace_id = $1 AND created_at >= current_date - (($2::int - 1) * interval '1 day')
		   GROUP BY created_at::date
		), curation AS (
		  SELECT created_at::date AS day,
		         count(*) AS run_count,
		         count(*) FILTER (WHERE status = 'failed') AS failed_count
		    FROM memory_curation_run
		   WHERE workspace_id = $1 AND created_at >= current_date - (($2::int - 1) * interval '1 day')
		   GROUP BY created_at::date
		)
		SELECT d.day::text,
		       COALESCE(s.memory_candidates, 0), COALESCE(s.skill_candidates, 0),
		       COALESCE(p.promoted_memory, 0), COALESCE(p.promoted_skill, 0), COALESCE(l.archived_or_deprecated, 0),
		       COALESCE(f.injected, 0), COALESCE(f.used, 0), COALESCE(f.success, 0), COALESCE(f.failure, 0),
		       COALESCE(c.run_count, 0), COALESCE(c.failed_count, 0)
		  FROM days d
		  LEFT JOIN submissions s ON s.day = d.day
		  LEFT JOIN promoted p ON p.day = d.day
		  LEFT JOIN lifecycle l ON l.day = d.day
		  LEFT JOIN feedback f ON f.day = d.day
		  LEFT JOIN curation c ON c.day = d.day
		 ORDER BY d.day
	`, workspaceID, days)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var item EvolutionDailyMetricResponse
		if err := rows.Scan(&item.Date, &item.MemoryCandidates, &item.SkillCandidates, &item.PromotedMemory, &item.PromotedSkill, &item.ArchivedOrDeprecated, &item.FeedbackInjected, &item.FeedbackUsed, &item.FeedbackSuccess, &item.FeedbackFailure, &item.MemoryCurationRunCount, &item.MemoryCurationFailed); err != nil {
			return err
		}
		resp.DailyMetrics = append(resp.DailyMetrics, item)
	}
	return rows.Err()
}

func (h *Handler) loadEvolutionTaskEfficiency(r *http.Request, workspaceID string, days int, resp *EvolutionMetricsResponse) error {
	return h.DB.QueryRow(r.Context(), `
		WITH task_rollup AS (
		  SELECT atq.issue_id,
		         min(atq.started_at) AS started_at,
		         max(atq.completed_at) AS completed_at,
		         sum(COALESCE(tu.input_tokens, 0)) AS input_tokens,
		         sum(COALESCE(tu.output_tokens, 0)) AS output_tokens,
		         sum(COALESCE(tu.cache_read_tokens, 0)) AS cache_read_tokens,
		         sum(COALESCE(tu.cache_write_tokens, 0)) AS cache_write_tokens,
		         count(DISTINCT COALESCE(f.unit_id::text, f.local_unit_id)) FILTER (WHERE f.event IN ('used','success','failure')) AS evolved_units_used
		    FROM agent_task_queue atq
		    JOIN issue i ON i.id = atq.issue_id AND i.workspace_id = $1
		    LEFT JOIN task_usage tu ON tu.task_id = atq.id
		    LEFT JOIN evolution_unit_feedback_event f ON f.task_id = atq.id AND f.workspace_id = $1
		   WHERE atq.completed_at >= current_date - (($2::int - 1) * interval '1 day')
		     AND atq.status IN ('completed','failed')
		   GROUP BY atq.issue_id
		), issue_rollup AS (
		  SELECT issue_id,
		         EXTRACT(EPOCH FROM (completed_at - started_at)) AS duration_seconds,
		         input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, evolved_units_used
		    FROM task_rollup
		   WHERE started_at IS NOT NULL AND completed_at IS NOT NULL
		)
		SELECT count(*),
		       COALESCE(avg(duration_seconds), 0),
		       COALESCE(avg(input_tokens), 0), COALESCE(avg(output_tokens), 0),
		       COALESCE(avg(cache_read_tokens), 0), COALESCE(avg(cache_write_tokens), 0),
		       COALESCE(avg(evolved_units_used), 0),
		       count(*) FILTER (WHERE evolved_units_used > 0),
		       count(*) FILTER (WHERE evolved_units_used = 0)
		  FROM issue_rollup
	`, workspaceID, days).Scan(&resp.TaskEfficiency.IssueCount, &resp.TaskEfficiency.AverageDurationSeconds, &resp.TaskEfficiency.AverageInputTokens, &resp.TaskEfficiency.AverageOutputTokens, &resp.TaskEfficiency.AverageCacheReadTokens, &resp.TaskEfficiency.AverageCacheWriteTokens, &resp.TaskEfficiency.AverageEvolvedUnitsUsed, &resp.TaskEfficiency.WithEvolvedUnitsIssueCount, &resp.TaskEfficiency.WithoutEvolvedUnitsIssueCount)
}
