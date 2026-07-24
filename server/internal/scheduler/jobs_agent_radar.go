package scheduler

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/multica-ai/multica/server/internal/radar"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const JobNameAgentRadarSchedule = "agent_radar_schedule"

const (
	agentRadarCadence            = time.Minute
	workspaceRadarCheckInterval  = 30 * time.Minute
	agentRadarHourlyBudget       = 1
	agentRadarDailyBudget        = 24
	agentRadarCreatesPerTick     = 2
	agentRadarBatchLimit         = 100
	agentRadarBindingRepairLimit = 200
	agentRadarCooldownKey        = radar.WorkspaceSupervisorCooldownKey
	agentRadarFullReviewPeriod   = 6 * time.Hour
	// A claim should move dispatched -> running in seconds. One hour is an
	// intentionally conservative absolute-age backstop for claims whose
	// dispatched_at keeps being refreshed by claim recovery.
	agentRadarStaleDispatchAge  = time.Hour
	agentRadarStaleRepairLimit  = 200
	agentRadarUnauthorizedLimit = 200
	agentRadarCompletionLease   = 5 * time.Minute
	agentRadarCompletionLimit   = 20
)

type radarCandidate struct {
	WorkspaceID      pgtype.UUID
	AgentID          pgtype.UUID
	DisplayName      string
	LastSuccessAt    pgtype.Timestamptz
	LastFullReviewAt pgtype.Timestamptz
}

type AgentRadarCompletionReplayer interface {
	ReplayCompletedAgentRadarTask(context.Context, db.AgentInboxEvent, db.AgentRadarRun) error
}

func AgentRadarScheduleJob(pool *pgxpool.Pool, taskSvc *service.TaskService, replayers ...AgentRadarCompletionReplayer) JobSpec {
	var replayer AgentRadarCompletionReplayer
	if len(replayers) > 0 {
		replayer = replayers[0]
	}
	return JobSpec{
		Name:              JobNameAgentRadarSchedule,
		Cadence:           agentRadarCadence,
		ScheduleDelay:     time.Minute,
		CatchUpMode:       CatchUpLatestOnly,
		CatchUpWindow:     agentRadarCadence,
		MaxPlansPerTick:   1,
		RunTimeout:        2 * time.Minute,
		StaleTimeout:      5 * time.Minute,
		HeartbeatInterval: 30 * time.Second,
		AllowStaleReentry: true,
		MaxAttempts:       3,
		RetryBackoff:      []time.Duration{time.Minute, 5 * time.Minute, 10 * time.Minute},
		Scopes:            StaticScopes(ScopeGlobal),
		Handler:           makeAgentRadarScheduleHandler(pool, taskSvc, replayer),
	}
}

func makeAgentRadarScheduleHandler(pool *pgxpool.Pool, taskSvc *service.TaskService, replayers ...AgentRadarCompletionReplayer) Handler {
	var replayer AgentRadarCompletionReplayer
	if len(replayers) > 0 {
		replayer = replayers[0]
	}
	return func(ctx context.Context, in HandlerInput) (HandlerResult, error) {
		if pool == nil || taskSvc == nil {
			return HandlerResult{Result: map[string]any{"skipped": true, "reason": "db_unavailable"}}, nil
		}
		unauthorizedTasks, unauthorizedRuns, err := cancelUnauthorizedWorkspaceRadar(ctx, pool)
		if err != nil {
			return HandlerResult{}, err
		}
		if len(unauthorizedTasks) > 0 {
			taskSvc.BroadcastCancelledTasks(ctx, unauthorizedTasks)
		}
		repairedBindings, err := repairWorkspaceRadarSupervisorBindings(ctx, pool)
		if err != nil {
			return HandlerResult{}, err
		}
		replayedCompleted, replayFailed, err := recoverStaleCompletedRadarRunsWithStats(ctx, pool, replayer)
		if err != nil {
			return HandlerResult{}, err
		}
		terminalRepaired, err := reconcileTerminalRadarRuns(ctx, pool)
		if err != nil {
			return HandlerResult{}, err
		}
		staleDispatchRepaired, err := repairStaleDispatchedRadarTasks(ctx, taskSvc)
		if err != nil {
			return HandlerResult{}, err
		}
		// Work-graph handoffs are the workspace supervisor's speech path.
		// Keep identity binding and repair work above, but never spend tokens on
		// the legacy whole-workspace scheduled Radar prompt.
		slog.Info("workspace radar: skipping scheduled LLM enqueue; workgraph handoffs are active")
		return HandlerResult{
			Result: map[string]any{
				"skipped":                      true,
				"reason":                       "workgraph_handoffs",
				"repaired":                     unauthorizedRuns + repairedBindings + terminalRepaired + staleDispatchRepaired,
				"repaired_unauthorized":        unauthorizedRuns,
				"repaired_bindings":            repairedBindings,
				"cancelled_unauthorized_tasks": len(unauthorizedTasks),
				"replayed_completed":           replayedCompleted,
				"replay_failed":                replayFailed,
				"repaired_terminal":            terminalRepaired,
				"repaired_stale_dispatched":    staleDispatchRepaired,
			},
		}, nil
	}
}

type radarBudget struct {
	Limited  bool
	ResumeAt time.Time
}

func workspaceRadarBudget(ctx context.Context, pool *pgxpool.Pool, workspaceID pgtype.UUID, planTime time.Time) (radarBudget, error) {
	var hourlyCount, dailyCount int64
	var hourlyResume, dailyResume pgtype.Timestamptz
	err := pool.QueryRow(ctx, `
		WITH attempts AS MATERIALIZED (
		  SELECT run.created_at
		  FROM agent_radar_run run
		  LEFT JOIN agent_inbox_event task ON task.id = run.task_id
		  WHERE run.workspace_id = $1
		    AND run.trigger_kind = 'scheduled'
		    AND run.cooldown_key = 'workspace_supervisor_radar'
		    AND run.created_at > $2::timestamptz - interval '24 hours'
		    AND COALESCE(task.failure_reason, '') NOT IN (
		      'radar_active_run_repair',
		      'radar_stale_dispatch_repair'
		    )
		    AND NOT (
		      run.status = 'failed'
		      AND (
		        COALESCE(run.error, '') LIKE 'migration:%'
		        OR COALESCE(run.error, '') = 'radar_stale_dispatch_repair'
		      )
		    )
		)
		SELECT
		  count(*) FILTER (WHERE created_at > $2::timestamptz - interval '1 hour')::bigint,
		  count(*)::bigint,
		  min(created_at) FILTER (WHERE created_at > $2::timestamptz - interval '1 hour') + interval '1 hour',
		  min(created_at) + interval '24 hours'
		FROM attempts
	`, workspaceID, planTime).Scan(&hourlyCount, &dailyCount, &hourlyResume, &dailyResume)
	if err != nil {
		return radarBudget{}, err
	}
	budget := radarBudget{}
	if hourlyCount >= agentRadarHourlyBudget && hourlyResume.Valid {
		budget.Limited = true
		budget.ResumeAt = hourlyResume.Time
	}
	if dailyCount >= agentRadarDailyBudget && dailyResume.Valid {
		budget.Limited = true
		// Both rolling windows must permit another run. If both are exhausted,
		// the later reset is the earliest time at which scheduling can resume.
		if budget.ResumeAt.IsZero() || dailyResume.Time.After(budget.ResumeAt) {
			budget.ResumeAt = dailyResume.Time
		}
	}
	return budget, nil
}

func deferWorkspaceRadarForBudget(ctx context.Context, pool *pgxpool.Pool, candidate radarCandidate, resumeAt time.Time) error {
	_, err := pool.Exec(ctx, `
		UPDATE workspace_radar_state
		SET next_due_at = GREATEST(next_due_at, $3),
		    updated_at = now()
		WHERE workspace_id = $1
		  AND supervisor_agent_id = $2
		  AND enabled
	`, candidate.WorkspaceID, candidate.AgentID, resumeAt)
	return err
}

// repairWorkspaceRadarSupervisorBindings fills only missing or invalid active
// bindings. It never replaces an authorized supervisor merely because another
// owner has a newer Wendy. The workspace lock serializes this selection with
// EnsureWindy and scheduled enqueue/execution, while the conditional upsert
// rechecks the invariant for concurrent scheduler replicas.
func repairWorkspaceRadarSupervisorBindings(ctx context.Context, pool *pgxpool.Pool) (int64, error) {
	var repaired int64
	if err := pool.QueryRow(ctx, `
		WITH repairable_workspaces AS MATERIALIZED (
		  SELECT workspace.id AS workspace_id
		  FROM workspace
		  LEFT JOIN workspace_radar_state state
		    ON state.workspace_id = workspace.id
		  WHERE (
		    state.workspace_id IS NULL
		    OR (
		      state.enabled
		      AND NOT EXISTS (
		        SELECT 1
		        FROM agent current_supervisor
		        JOIN member current_owner
		          ON current_owner.workspace_id = current_supervisor.workspace_id
		         AND current_owner.user_id = current_supervisor.owner_id
		         AND current_owner.role = 'owner'
		        WHERE current_supervisor.workspace_id = workspace.id
		          AND current_supervisor.id = state.supervisor_agent_id
		          AND current_supervisor.archived_at IS NULL
		      )
		    )
		  )
		    AND EXISTS (
		      SELECT 1
		      FROM agent candidate
		      JOIN member candidate_owner
		        ON candidate_owner.workspace_id = candidate.workspace_id
		       AND candidate_owner.user_id = candidate.owner_id
		       AND candidate_owner.role = 'owner'
		      WHERE candidate.workspace_id = workspace.id
		        AND candidate.archived_at IS NULL
		        AND COALESCE(NULLIF(candidate.display_name, ''), candidate.name) IN ('Wendy', 'Windy', 'Joe')
		    )
		  ORDER BY workspace.id
		  LIMIT $1
		  FOR UPDATE OF workspace SKIP LOCKED
		), candidate_pool AS MATERIALIZED (
		  SELECT
		    repairable.workspace_id,
		    candidate.id AS agent_id,
		    (runtime.status = 'online') AS runtime_online,
		    (COALESCE(NULLIF(candidate.display_name, ''), candidate.name) = 'Wendy') AS canonical_wendy,
		    (candidate.visibility = 'private') AS private_wendy,
		    candidate.updated_at,
		    candidate.created_at
		  FROM repairable_workspaces repairable
		  JOIN agent candidate
		    ON candidate.workspace_id = repairable.workspace_id
		   AND candidate.archived_at IS NULL
		  JOIN member candidate_owner
		    ON candidate_owner.workspace_id = candidate.workspace_id
		   AND candidate_owner.user_id = candidate.owner_id
		   AND candidate_owner.role = 'owner'
		  LEFT JOIN agent_runtime runtime
		    ON runtime.id = candidate.runtime_id
		   AND runtime.workspace_id = candidate.workspace_id
		  WHERE COALESCE(NULLIF(candidate.display_name, ''), candidate.name) IN ('Wendy', 'Windy', 'Joe')
		  FOR SHARE OF candidate, candidate_owner
		), ranked_candidates AS MATERIALIZED (
		  SELECT
		    candidate_pool.*,
		    row_number() OVER (
		      PARTITION BY candidate_pool.workspace_id
		      ORDER BY
		        candidate_pool.runtime_online DESC NULLS LAST,
		        candidate_pool.canonical_wendy DESC,
		        candidate_pool.private_wendy DESC,
		        candidate_pool.updated_at DESC,
		        candidate_pool.created_at DESC,
		        candidate_pool.agent_id ASC
		    ) AS candidate_rank
		  FROM candidate_pool
		), selected AS MATERIALIZED (
		  SELECT workspace_id, agent_id
		  FROM ranked_candidates
		  WHERE candidate_rank = 1
		), repaired AS (
		  INSERT INTO workspace_radar_state (
		    workspace_id,
		    supervisor_agent_id,
		    enabled,
		    next_due_at
		  )
		  SELECT selected.workspace_id, selected.agent_id, true, now()
		  FROM selected
		  ON CONFLICT (workspace_id) DO UPDATE
		  SET supervisor_agent_id = EXCLUDED.supervisor_agent_id,
		      next_due_at = LEAST(workspace_radar_state.next_due_at, now()),
		      consecutive_failures = 0,
		      updated_at = now()
		  WHERE workspace_radar_state.enabled
		    AND workspace_radar_state.supervisor_agent_id IS DISTINCT FROM EXCLUDED.supervisor_agent_id
		    AND NOT EXISTS (
		      SELECT 1
		      FROM agent current_supervisor
		      JOIN member current_owner
		        ON current_owner.workspace_id = current_supervisor.workspace_id
		       AND current_owner.user_id = current_supervisor.owner_id
		       AND current_owner.role = 'owner'
		      WHERE current_supervisor.workspace_id = workspace_radar_state.workspace_id
		        AND current_supervisor.id = workspace_radar_state.supervisor_agent_id
		        AND current_supervisor.archived_at IS NULL
		    )
		  RETURNING workspace_id
		)
		SELECT count(*) FROM repaired
	`, agentRadarBindingRepairLimit).Scan(&repaired); err != nil {
		return 0, err
	}
	return repaired, nil
}

// cancelUnauthorizedWorkspaceRadar is the repair arm of the database dispatch
// gate.  The trigger prevents old replicas from handing an unauthorized prompt
// to a daemon; this pass terminalizes the stranded pair and returns committed
// task rows so the normal cancellation event/status path remains visible.
func cancelUnauthorizedWorkspaceRadar(ctx context.Context, pool *pgxpool.Pool) ([]db.AgentInboxEvent, int64, error) {
	var taskIDs []pgtype.UUID
	var cancelledRuns int64
	err := pool.QueryRow(ctx, `
		WITH victims AS MATERIALIZED (
		  SELECT run.id, run.task_id, run.agent_id
		  FROM agent_radar_run run
		  WHERE run.trigger_kind = 'scheduled'
		    AND run.cooldown_key = 'workspace_supervisor_radar'
		    AND run.status IN ('planned', 'queued', 'running', 'executing')
		    AND NOT EXISTS (
		      SELECT 1
		      FROM workspace_radar_state state
		      JOIN agent supervisor
		        ON supervisor.workspace_id = state.workspace_id
		       AND supervisor.id = state.supervisor_agent_id
		      JOIN member owner_member
		        ON owner_member.workspace_id = state.workspace_id
		       AND owner_member.user_id = supervisor.owner_id
		       AND owner_member.role = 'owner'
		      WHERE state.workspace_id = run.workspace_id
		        AND state.supervisor_agent_id = run.agent_id
		        AND state.enabled
		        AND supervisor.archived_at IS NULL
		    )
		  ORDER BY run.created_at ASC, run.id ASC
		  LIMIT $1
		), cancelled_tasks AS MATERIALIZED (
		  UPDATE agent_inbox_event task
		  SET status = 'suppressed',
		      terminal_outcome = 'cancelled',
		      terminal_at = COALESCE(task.terminal_at, now()),
		      acked_at = COALESCE(task.acked_at, now()),
		      completed_at = COALESCE(task.completed_at, now()),
		      error = COALESCE(NULLIF(task.error, ''), 'Workspace Radar supervisor is no longer authorized'),
		      failure_reason = 'radar_supervisor_unauthorized'
		  FROM victims
		  WHERE task.id = victims.task_id
		    AND task.agent_id = victims.agent_id
		    AND task.context->>'type' = 'agent_radar'
		    AND task.context->>'radar_run_id' = victims.id::text
		    AND task.status IN ('pending', 'draining', 'failed')
		  RETURNING task.id
		), cancelled_runs AS MATERIALIZED (
		  UPDATE agent_radar_run run
		  SET status = 'cancelled',
		      error = CASE
		        WHEN run.error = '' THEN 'Workspace Radar supervisor is no longer authorized'
		        ELSE run.error
		      END,
		      finished_at = COALESCE(run.finished_at, now()),
		      updated_at = now()
		  -- StartTask locks task then run in one transaction.  Referencing the
		  -- task phase here makes cleanup use the same order and avoids a
		  -- run->task / task->run deadlock.
		  FROM victims
		  CROSS JOIN (
		    SELECT count(*) AS task_phase_applied FROM cancelled_tasks
		  ) task_phase
		  WHERE run.id = victims.id
		    AND run.status IN ('planned', 'queued', 'running', 'executing')
		    AND task_phase.task_phase_applied >= 0
		  RETURNING run.id
		)
		SELECT
		  COALESCE((SELECT array_agg(id) FROM cancelled_tasks), '{}'::uuid[]),
		  (SELECT count(*)::bigint FROM cancelled_runs)
	`, agentRadarUnauthorizedLimit).Scan(&taskIDs, &cancelledRuns)
	if err != nil {
		return nil, 0, err
	}

	q := db.New(pool)
	cancelledTasks := make([]db.AgentInboxEvent, 0, len(taskIDs))
	for _, taskID := range taskIDs {
		task, err := q.GetAgentTask(ctx, taskID)
		if err != nil {
			return nil, 0, err
		}
		cancelledTasks = append(cancelledTasks, task)
	}
	return cancelledTasks, cancelledRuns, nil
}

func repairStaleDispatchedRadarTasks(ctx context.Context, taskSvc *service.TaskService) (int64, error) {
	failedTasks, err := taskSvc.Queries.FailStaleDispatchedAgentRadarTasks(ctx, db.FailStaleDispatchedAgentRadarTasksParams{
		StaleAgeSecs: agentRadarStaleDispatchAge.Seconds(),
		MaxPerTick:   agentRadarStaleRepairLimit,
	})
	if err != nil {
		return 0, err
	}
	if len(failedTasks) == 0 {
		return 0, nil
	}

	// This also terminalizes the linked Radar run and publishes the normal
	// task failure/status events. The queue row has no automatic retries;
	// the workspace scheduler and its persisted backoff own the retry boundary.
	taskSvc.HandleFailedTasks(ctx, failedTasks)
	slog.Info("agent radar: repaired stale dispatched tasks", "count", len(failedTasks))
	return int64(len(failedTasks)), nil
}

func recoverStaleCompletedRadarRuns(ctx context.Context, pool *pgxpool.Pool, replayer AgentRadarCompletionReplayer) (int64, error) {
	replayed, _, err := recoverStaleCompletedRadarRunsWithStats(ctx, pool, replayer)
	return replayed, err
}

func recoverStaleCompletedRadarRunsWithStats(ctx context.Context, pool *pgxpool.Pool, replayer AgentRadarCompletionReplayer) (int64, int, error) {
	if pool == nil || replayer == nil {
		return 0, 0, nil
	}
	rows, err := pool.Query(ctx, `
		WITH candidates AS MATERIALIZED (
		  SELECT run.id
		  FROM agent_radar_run run
		  JOIN agent_inbox_event task
		    ON task.id = run.task_id
		   AND task.agent_id = run.agent_id
		   AND task.context->>'type' = 'agent_radar'
		   AND task.context->>'radar_run_id' = run.id::text
		  JOIN workspace_radar_state state
		    ON state.workspace_id = run.workspace_id
		   AND state.supervisor_agent_id = run.agent_id
		   AND state.enabled
		  JOIN agent supervisor
		    ON supervisor.id = state.supervisor_agent_id
		   AND supervisor.workspace_id = state.workspace_id
		   AND supervisor.archived_at IS NULL
		  JOIN member owner_member
		    ON owner_member.workspace_id = state.workspace_id
		   AND owner_member.user_id = supervisor.owner_id
		   AND owner_member.role = 'owner'
		  WHERE run.trigger_kind = 'scheduled'
		    AND run.cooldown_key = 'workspace_supervisor_radar'
		    AND task.status = 'completed'
		    AND (
		      (run.status = 'executing' AND run.updated_at < now() - ($2 * interval '1 second'))
		      OR
		      (run.status IN ('queued', 'running') AND COALESCE(task.completed_at, task.created_at) < now() - ($2 * interval '1 second'))
		    )
		  ORDER BY COALESCE(task.completed_at, run.updated_at) ASC, run.id ASC
		  LIMIT $1
		  FOR UPDATE OF run SKIP LOCKED
		)
		UPDATE agent_radar_run run
		SET status = 'executing',
		    started_at = COALESCE(run.started_at, now()),
		    finished_at = NULL,
		    updated_at = now()
		FROM candidates
		WHERE run.id = candidates.id
		  AND (
		    (run.status = 'executing' AND run.updated_at < now() - ($2 * interval '1 second'))
		    OR run.status IN ('queued', 'running')
		  )
		RETURNING run.id, run.task_id
	`, agentRadarCompletionLimit, agentRadarCompletionLease.Seconds())
	if err != nil {
		return 0, 0, err
	}
	type leasedRun struct {
		runID  pgtype.UUID
		taskID pgtype.UUID
	}
	leased := make([]leasedRun, 0, agentRadarCompletionLimit)
	for rows.Next() {
		var pair leasedRun
		if err := rows.Scan(&pair.runID, &pair.taskID); err != nil {
			rows.Close()
			return 0, 0, err
		}
		leased = append(leased, pair)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, 0, err
	}
	// Exhausting and closing the autocommit query releases the row locks before
	// the replayer starts its own transactions and refreshes the run lease.
	rows.Close()

	q := db.New(pool)
	failed := 0
	for _, pair := range leased {
		task, taskErr := q.GetAgentTask(ctx, pair.taskID)
		if taskErr != nil {
			failed++
			slog.Warn("workspace radar: load replay task failed", "task_id", uuidString(pair.taskID), "error", taskErr)
			continue
		}
		run, runErr := q.GetAgentRadarRun(ctx, pair.runID)
		if runErr != nil {
			failed++
			slog.Warn("workspace radar: load replay run failed", "run_id", uuidString(pair.runID), "error", runErr)
			continue
		}
		if replayErr := replayer.ReplayCompletedAgentRadarTask(ctx, task, run); replayErr != nil {
			failed++
			slog.Warn("workspace radar: replay persisted completion failed", "run_id", uuidString(run.ID), "task_id", uuidString(task.ID), "error", replayErr)
		}
	}
	return int64(len(leased)), failed, nil
}

func reconcileTerminalRadarRuns(ctx context.Context, pool *pgxpool.Pool) (int64, error) {
	result, err := db.New(pool).ReconcileTerminalWorkspaceRadarRuns(ctx)
	if err != nil {
		return 0, err
	}
	return result.TerminalizedCount, nil
}

func listRadarCandidates(ctx context.Context, pool *pgxpool.Pool) ([]radarCandidate, error) {
	rows, err := pool.Query(ctx, `
		SELECT
		  a.workspace_id,
		  a.id,
		  COALESCE(NULLIF(a.display_name, ''), a.name),
		  state.last_success_at,
		  state.last_full_review_at
		FROM workspace_radar_state state
		JOIN agent a
		  ON a.workspace_id = state.workspace_id
		 AND a.id = state.supervisor_agent_id
		JOIN member owner_member
		  ON owner_member.workspace_id = a.workspace_id
		 AND owner_member.user_id = a.owner_id
		 AND owner_member.role = 'owner'
		JOIN agent_runtime ar
		  ON ar.id = a.runtime_id
		 AND ar.workspace_id = a.workspace_id
		 AND ar.status = 'online'
		WHERE state.enabled
		  AND state.next_due_at <= now()
		  AND a.archived_at IS NULL
		  AND a.runtime_id IS NOT NULL
		  AND NOT EXISTS (
		    SELECT 1
		    FROM agent_radar_run rr
		    WHERE rr.workspace_id = a.workspace_id
		      AND rr.status IN ('planned', 'queued', 'running', 'executing')
		      AND (
		        rr.agent_id = a.id
		        OR (
		          rr.trigger_kind = 'scheduled'
		          AND rr.cooldown_key = 'workspace_supervisor_radar'
		        )
		      )
		  )
		ORDER BY (
		  SELECT max(history.created_at)
		  FROM agent_radar_run history
		  WHERE history.workspace_id = a.workspace_id
		    AND history.agent_id = a.id
		) ASC NULLS FIRST,
		state.next_due_at ASC,
		a.created_at ASC
		LIMIT $1
	`, agentRadarBatchLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []radarCandidate
	for rows.Next() {
		var candidate radarCandidate
		if err := rows.Scan(
			&candidate.WorkspaceID,
			&candidate.AgentID,
			&candidate.DisplayName,
			&candidate.LastSuccessAt,
			&candidate.LastFullReviewAt,
		); err != nil {
			return nil, err
		}
		out = append(out, candidate)
	}
	return out, rows.Err()
}

func workspaceRadarNeedsReview(ctx context.Context, pool *pgxpool.Pool, candidate radarCandidate, planTime time.Time) (bool, bool, error) {
	if !candidate.LastSuccessAt.Valid || !candidate.LastFullReviewAt.Valid {
		return true, true, nil
	}
	if planTime.Sub(candidate.LastFullReviewAt.Time) >= agentRadarFullReviewPeriod {
		return true, true, nil
	}
	if _, err := pool.Exec(ctx, `SELECT refresh_workspace_radar_time_signals($1, $2)`, candidate.WorkspaceID, planTime); err != nil {
		return false, false, err
	}
	var changed bool
	err := pool.QueryRow(ctx, `
		SELECT change_version > change_cursor_version
		FROM workspace_radar_state
		WHERE workspace_id = $1
		  AND supervisor_agent_id = $2
		  AND enabled
	`, candidate.WorkspaceID, candidate.AgentID).Scan(&changed)
	return changed, false, err
}

func deferUnchangedWorkspaceRadar(ctx context.Context, pool *pgxpool.Pool, candidate radarCandidate, planTime time.Time) error {
	_, err := pool.Exec(ctx, `
		UPDATE workspace_radar_state
		SET next_due_at = $3, updated_at = now()
		WHERE workspace_id = $1
		  AND supervisor_agent_id = $2
	`, candidate.WorkspaceID, candidate.AgentID, planTime.Add(workspaceRadarCheckInterval))
	return err
}

func uuidString(id pgtype.UUID) string {
	if !id.Valid {
		return ""
	}
	return uuid.UUID(id.Bytes).String()
}
