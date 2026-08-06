package handler

import (
	"net/http"
	"strconv"
	"strings"
)

type EvolutionMetricsResponse struct {
	UnitMetrics            []EvolutionUnitMetricResponse        `json:"unit_metrics"`
	DailyMetrics           []EvolutionDailyMetricResponse       `json:"daily_metrics"`
	TaskEfficiency         EvolutionTaskEfficiencyResponse      `json:"task_efficiency"`
	CollaborationEvolution EvolutionCollaborationMetricResponse `json:"collaboration_evolution"`
	ModelEvolution         EvolutionModelMetricResponse         `json:"model_evolution"`
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
	TeamKnowledgeItems     int64  `json:"team_knowledge_items"`
	ArchivedOrDeprecated   int64  `json:"archived_or_deprecated"`
	FeedbackInjected       int64  `json:"feedback_injected"`
	FeedbackUsed           int64  `json:"feedback_used"`
	FeedbackSuccess        int64  `json:"feedback_success"`
	FeedbackFailure        int64  `json:"feedback_failure"`
	MemoryCurationRunCount int64  `json:"memory_curation_run_count"`
	MemoryCurationFailed   int64  `json:"memory_curation_failed"`
}

type EvolutionCollaborationMetricResponse struct {
	UnmentionedMessages            int64   `json:"unmentioned_messages"`
	AttentionRounds                int64   `json:"attention_rounds"`
	AttentionProbes                int64   `json:"attention_probes"`
	AttentionSilentRate            float64 `json:"attention_silent_rate"`
	AutonomousClaims               int64   `json:"autonomous_claims"`
	PeerConverged                  int64   `json:"peer_converged"`
	ManagerFallbacks               int64   `json:"manager_fallbacks"`
	FullExecutionWakes             int64   `json:"full_execution_wakes"`
	FullExecutionReductionRate     float64 `json:"full_execution_reduction_rate"`
	CollaborationSessions          int64   `json:"collaboration_sessions"`
	TurnOrderViolationRate         float64 `json:"turn_order_violation_rate"`
	ContributionOffers             int64   `json:"contribution_offers"`
	ContributionOfferAdoptionRate  float64 `json:"contribution_offer_adoption_rate"`
	ContributionOfferHelpfulRate   float64 `json:"contribution_offer_helpful_rate"`
	UnauthorizedPublicSendsBlocked int64   `json:"unauthorized_public_sends_blocked"`
	PoliciesRetrieved              int64   `json:"policies_retrieved"`
	PoliciesUsed                   int64   `json:"policies_used"`
	PolicySuccessRate              float64 `json:"policy_success_rate"`
	AttentionTokens                int64   `json:"attention_tokens"`
	ExecutionTokens                int64   `json:"execution_tokens"`
	EstimatedTokensSaved           int64   `json:"estimated_tokens_saved"`
	ImmutableDecisionAuditEvents   int64   `json:"immutable_decision_audit_events"`
}

type EvolutionModelMetricResponse struct {
	AttentionStudentVersion string  `json:"attention_student_version"`
	AttentionStudentMode    string  `json:"attention_student_mode"`
	MissedAttentionRate     float64 `json:"missed_attention_rate"`
	LateRescueRate          float64 `json:"late_rescue_rate"`
	ContextFilterVersion    string  `json:"context_filter_version"`
	ContextCompressionRate  float64 `json:"context_compression_rate"`
	CriticalContextRecall   float64 `json:"critical_context_recall"`
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
	if err := h.loadEvolutionCollaborationMetrics(r, workspaceID, days, &resp); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load collaboration evolution metrics")
		return
	}
	if err := h.loadEvolutionModelMetrics(r, workspaceID, &resp); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load model evolution metrics")
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
		), team_knowledge AS (
		  SELECT created_at::date AS day,
		         count(*) AS team_knowledge_items
		    FROM team_knowledge_item
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
		       COALESCE(p.promoted_memory, 0), COALESCE(p.promoted_skill, 0), COALESCE(tk.team_knowledge_items, 0),
		       COALESCE(l.archived_or_deprecated, 0),
		       COALESCE(f.injected, 0), COALESCE(f.used, 0), COALESCE(f.success, 0), COALESCE(f.failure, 0),
		       COALESCE(c.run_count, 0), COALESCE(c.failed_count, 0)
		  FROM days d
		  LEFT JOIN submissions s ON s.day = d.day
		  LEFT JOIN promoted p ON p.day = d.day
		  LEFT JOIN team_knowledge tk ON tk.day = d.day
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
		if err := rows.Scan(&item.Date, &item.MemoryCandidates, &item.SkillCandidates, &item.PromotedMemory, &item.PromotedSkill, &item.TeamKnowledgeItems, &item.ArchivedOrDeprecated, &item.FeedbackInjected, &item.FeedbackUsed, &item.FeedbackSuccess, &item.FeedbackFailure, &item.MemoryCurationRunCount, &item.MemoryCurationFailed); err != nil {
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
		    FROM agent_inbox_event atq
		    JOIN issue i ON i.id = atq.issue_id AND i.workspace_id = $1
		    LEFT JOIN agent_usage tu ON tu.execution_id = atq.id
		    LEFT JOIN evolution_unit_feedback_event f ON f.task_id = atq.id AND f.workspace_id = $1
		   WHERE atq.completed_at >= current_date - (($2::int - 1) * interval '1 day')
		     AND atq.status = 'acked'
		     AND atq.terminal_outcome IN ('completed','failed')
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

// loadEvolutionCollaborationMetrics reports Collaboration session/turn and
// policy-feedback metrics. Channel Attention Round metrics were retired
// alongside the feature and its tables; those response fields remain for API
// stability but are always zero.
func (h *Handler) loadEvolutionCollaborationMetrics(r *http.Request, workspaceID string, days int, resp *EvolutionMetricsResponse) error {
	resp.ModelEvolution.AttentionStudentMode = "off"
	return h.DB.QueryRow(r.Context(), `
		WITH bounds AS (
		  SELECT current_date - (($2::int - 1) * interval '1 day') AS since
		), sessions AS (
		  SELECT count(*) AS collaboration_sessions
		    FROM collaboration_session session
		    CROSS JOIN bounds
		   WHERE session.workspace_id = $1 AND session.created_at >= bounds.since
		), turns AS (
		  SELECT count(*) AS turn_full_wakes
		    FROM collaboration_turn turn
		    CROSS JOIN bounds
		   WHERE turn.workspace_id = $1 AND turn.created_at >= bounds.since
		), turns_consumed AS (
		  SELECT count(*) AS consumed_turns
		    FROM collaboration_turn turn
		    CROSS JOIN bounds
		   WHERE turn.workspace_id = $1 AND turn.updated_at >= bounds.since AND turn.grant_status = 'consumed'
		), audit AS (
		  SELECT count(*) AS audit_events,
		         count(*) FILTER (WHERE event_type = 'unauthorized_public_send_blocked') AS blocked_public_sends
		    FROM channel_decision_audit audit
		    CROSS JOIN bounds
		   WHERE audit.workspace_id = $1 AND audit.created_at >= bounds.since
		), policies AS (
		  SELECT count(*) FILTER (WHERE feedback.event = 'injected') AS policies_retrieved,
		         count(*) FILTER (WHERE feedback.event = 'used') AS policies_used,
		         count(*) FILTER (WHERE feedback.event = 'success' OR feedback.outcome = 'success') AS policy_success,
		         count(*) FILTER (WHERE feedback.event = 'failure' OR feedback.outcome = 'failure') AS policy_failure
		    FROM evolution_unit_feedback_event feedback
		    CROSS JOIN bounds
		   WHERE feedback.workspace_id = $1 AND feedback.created_at >= bounds.since
		     AND feedback.unit_type IN ('workflow', 'tool_pattern')
		), execution_usage AS (
		  SELECT COALESCE(sum(usage.input_tokens + usage.output_tokens), 0) AS execution_tokens
		    FROM agent_usage usage
		    JOIN agent_execution execution ON execution.id = usage.execution_id
		    CROSS JOIN bounds
		   WHERE execution.workspace_id = $1 AND execution.created_at >= bounds.since
		     AND execution.source_kind = 'inbox'
		)
		SELECT
		  0,
		  0,
		  0,
		  0::float8,
		  0,
		  0,
		  0,
		  COALESCE(turns.turn_full_wakes, 0),
		  0::float8,
		  COALESCE(sessions.collaboration_sessions, 0),
		  CASE WHEN (COALESCE(turns_consumed.consumed_turns, 0) + COALESCE(audit.blocked_public_sends, 0)) > 0 THEN audit.blocked_public_sends::float8 / (turns_consumed.consumed_turns + audit.blocked_public_sends) ELSE 0 END,
		  0,
		  0::float8,
		  0::float8,
		  COALESCE(audit.blocked_public_sends, 0),
		  COALESCE(policies.policies_retrieved, 0),
		  COALESCE(policies.policies_used, 0),
		  CASE WHEN (COALESCE(policies.policy_success, 0) + COALESCE(policies.policy_failure, 0)) > 0 THEN policies.policy_success::float8 / (policies.policy_success + policies.policy_failure) ELSE 0 END,
		  0,
		  COALESCE(execution_usage.execution_tokens, 0),
		  0,
		  COALESCE(audit.audit_events, 0)
		FROM sessions
		CROSS JOIN turns
		CROSS JOIN turns_consumed
		CROSS JOIN audit
		CROSS JOIN policies
		CROSS JOIN execution_usage
	`, workspaceID, days).Scan(
		&resp.CollaborationEvolution.UnmentionedMessages,
		&resp.CollaborationEvolution.AttentionRounds,
		&resp.CollaborationEvolution.AttentionProbes,
		&resp.CollaborationEvolution.AttentionSilentRate,
		&resp.CollaborationEvolution.AutonomousClaims,
		&resp.CollaborationEvolution.PeerConverged,
		&resp.CollaborationEvolution.ManagerFallbacks,
		&resp.CollaborationEvolution.FullExecutionWakes,
		&resp.CollaborationEvolution.FullExecutionReductionRate,
		&resp.CollaborationEvolution.CollaborationSessions,
		&resp.CollaborationEvolution.TurnOrderViolationRate,
		&resp.CollaborationEvolution.ContributionOffers,
		&resp.CollaborationEvolution.ContributionOfferAdoptionRate,
		&resp.CollaborationEvolution.ContributionOfferHelpfulRate,
		&resp.CollaborationEvolution.UnauthorizedPublicSendsBlocked,
		&resp.CollaborationEvolution.PoliciesRetrieved,
		&resp.CollaborationEvolution.PoliciesUsed,
		&resp.CollaborationEvolution.PolicySuccessRate,
		&resp.CollaborationEvolution.AttentionTokens,
		&resp.CollaborationEvolution.ExecutionTokens,
		&resp.CollaborationEvolution.EstimatedTokensSaved,
		&resp.CollaborationEvolution.ImmutableDecisionAuditEvents,
	)
}

func (h *Handler) loadEvolutionModelMetrics(r *http.Request, workspaceID string, resp *EvolutionMetricsResponse) error {
	resp.ModelEvolution.AttentionStudentMode = "off"
	var attentionVersion, attentionMode string
	err := h.DB.QueryRow(r.Context(), `
		SELECT COALESCE(NULLIF(candidate_version, ''), active_version), mode
		FROM evolution_model_runtime_config
		WHERE workspace_id = $1 AND model_kind = 'attention_student'`, workspaceID).Scan(&attentionVersion, &attentionMode)
	if err != nil && !errorsIsNoRows(err) {
		return err
	}
	if err == nil {
		resp.ModelEvolution.AttentionStudentVersion = attentionVersion
		resp.ModelEvolution.AttentionStudentMode = attentionMode
	}
	var contextVersion string
	err = h.DB.QueryRow(r.Context(), `
		SELECT COALESCE(NULLIF(candidate_version, ''), active_version)
		FROM evolution_model_runtime_config
		WHERE workspace_id = $1 AND model_kind = 'context_filter'`, workspaceID).Scan(&contextVersion)
	if err != nil && !errorsIsNoRows(err) {
		return err
	}
	if err == nil {
		resp.ModelEvolution.ContextFilterVersion = contextVersion
	}
	_ = h.DB.QueryRow(r.Context(), `
		SELECT COALESCE((metrics->>'missed_attention_rate')::float8, 0),
		       COALESCE((metrics->>'late_rescue_rate')::float8, 0)
		FROM evolution_model_eval_run
		WHERE workspace_id = $1 AND model_kind = 'attention_student' AND status = 'completed'
		ORDER BY created_at DESC
		LIMIT 1`, workspaceID).Scan(&resp.ModelEvolution.MissedAttentionRate, &resp.ModelEvolution.LateRescueRate)
	_ = h.DB.QueryRow(r.Context(), `
		SELECT COALESCE((metrics->>'context_compression_rate')::float8, 0),
		       COALESCE((metrics->>'critical_context_recall')::float8, 0)
		FROM evolution_model_eval_run
		WHERE workspace_id = $1 AND model_kind = 'context_filter' AND status = 'completed'
		ORDER BY created_at DESC
		LIMIT 1`, workspaceID).Scan(&resp.ModelEvolution.ContextCompressionRate, &resp.ModelEvolution.CriticalContextRecall)
	return nil
}
