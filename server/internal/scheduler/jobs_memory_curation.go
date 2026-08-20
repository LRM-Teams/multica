package scheduler

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/multica-ai/multica/server/internal/service"
)

const (
	JobNameAgentMemorySelfReview = "agent_memory_self_review"
	JobNameTeamMemoryCuration    = "team_memory_curation"

	defaultMemoryCurationTimezone      = "Asia/Shanghai"
	defaultAgentSelfReviewScheduleHour = 1
	defaultAgentSelfReviewMode         = "auto_safe"
	defaultAgentSelfReviewConfidence   = 0.8

	// Backward-compatible names for older scheduler tests and deployments.
	JobNameMemoryL1DailyRecord   = JobNameAgentMemorySelfReview
	JobNameMemoryL2ReviewExtract = JobNameAgentMemorySelfReview
	JobNameMemoryL3Promote       = JobNameTeamMemoryCuration
	JobNameMemoryL4Curator       = JobNameTeamMemoryCuration
)

func MemoryCurationJobs(pool *pgxpool.Pool) []JobSpec {
	return []JobSpec{
		memoryCurationJob(pool, JobNameAgentMemorySelfReview, "agent_self_review", 0),
		memoryCurationJob(pool, JobNameTeamMemoryCuration, "team_curation", 1),
	}
}

func memoryCurationJob(pool *pgxpool.Pool, name, stage string, hourOffset int) JobSpec {
	return JobSpec{
		Name:              name,
		Cadence:           time.Hour,
		ScheduleDelay:     5 * time.Minute,
		CatchUpMode:       CatchUpEveryPlan,
		CatchUpWindow:     48 * time.Hour,
		MaxPlansPerTick:   8,
		RunTimeout:        10 * time.Minute,
		StaleTimeout:      15 * time.Minute,
		HeartbeatInterval: 30 * time.Second,
		AllowStaleReentry: true,
		MaxAttempts:       3,
		RetryBackoff:      []time.Duration{5 * time.Minute, 15 * time.Minute, 30 * time.Minute},
		Scopes:            StaticScopes(ScopeGlobal),
		Handler:           makeMemoryCurationIntentHandler(pool, stage, hourOffset),
	}
}

// makeMemoryCurationIntentHandler schedules self-review by default for active
// agents. Team curation remains profile-backed so a workspace can choose one
// curator runtime/agent for shared governance.
func makeMemoryCurationIntentHandler(pool *pgxpool.Pool, stage any, hourOffset int) Handler {
	stageName := normalizeScheduledMemoryCurationStage(stage)
	return func(ctx context.Context, in HandlerInput) (HandlerResult, error) {
		if pool == nil {
			return HandlerResult{Result: map[string]any{"skipped": true, "reason": "database_unavailable", "stage": stageName}}, nil
		}
		agentRunTableAvailable, err := memoryCurationAgentRunTableExists(ctx, pool)
		if err != nil {
			return HandlerResult{}, err
		}
		if stageName == "agent_self_review" && agentRunTableAvailable {
			return scheduleDefaultAgentSelfReviewRuns(ctx, pool, in.PlanTime, hourOffset, in.Heartbeat)
		}
		enabledColumn := "team_curation_enabled"
		if stageName == "agent_self_review" {
			enabledColumn = "self_review_enabled"
		}
		rows, err := pool.Query(ctx, `
			SELECT p.id::text, p.workspace_id::text, p.user_id::text,
			       p.runtime_id::text, p.curator_agent_id::text, p.model_override,
			       p.mode, p.confidence_threshold, p.target_scope, p.timezone,
			       p.schedule_hour, p.catch_up_enabled, p.config_version, rt.last_seen_at
			  FROM memory_curator_profile p
			  JOIN agent_runtime rt ON rt.id = p.runtime_id
			  JOIN agent curator ON curator.id = p.curator_agent_id
			 WHERE CASE WHEN $1 = 'self_review_enabled' THEN p.self_review_enabled ELSE p.team_curation_enabled END = true
			   AND p.runtime_id IS NOT NULL
			   AND p.curator_agent_id IS NOT NULL
			   AND curator.archived_at IS NULL
			   AND NOT EXISTS (
			     SELECT 1 FROM graph_memory_profile gmp
			     WHERE gmp.workspace_id = p.workspace_id
			       AND gmp.memory_type = 'graph'
			   )
		`, enabledColumn)
		if err != nil {
			return HandlerResult{}, err
		}
		// Drain every candidate profile into memory and close the cursor
		// BEFORE any per-row processing below: activeMemoryCurationAgentIDs,
		// the INSERT QueryRow, and the INSERT Exec all acquire their own
		// pool connection, and doing so while this cursor is still open can
		// deadlock a bounded pool under concurrent scheduler ticks (same
		// shape as the #1803 attachAgentRuntimeNames bug / task #90).
		type curatorProfileCandidate struct {
			profileID, workspaceID, userID, runtimeID, curatorAgentID, model, mode, targetScope, timezone string
			runtimeLastSeenAt                                                                             time.Time
			confidenceThreshold                                                                           float64
			scheduleHour                                                                                  int
			catchUp                                                                                       bool
			configVersion                                                                                 int64
		}
		var candidates []curatorProfileCandidate
		for rows.Next() {
			var c curatorProfileCandidate
			if err := rows.Scan(&c.profileID, &c.workspaceID, &c.userID, &c.runtimeID, &c.curatorAgentID, &c.model, &c.mode, &c.confidenceThreshold, &c.targetScope, &c.timezone, &c.scheduleHour, &c.catchUp, &c.configVersion, &c.runtimeLastSeenAt); err != nil {
				rows.Close()
				return HandlerResult{}, err
			}
			candidates = append(candidates, c)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return HandlerResult{}, err
		}
		rows.Close()

		created := int64(0)
		for _, c := range candidates {
			profileID, workspaceID, userID, runtimeID, curatorAgentID, model, mode, targetScope, timezone :=
				c.profileID, c.workspaceID, c.userID, c.runtimeID, c.curatorAgentID, c.model, c.mode, c.targetScope, c.timezone
			runtimeLastSeenAt := c.runtimeLastSeenAt
			confidenceThreshold := c.confidenceThreshold
			scheduleHour := c.scheduleHour
			catchUp := c.catchUp
			configVersion := c.configVersion
			loc, err := time.LoadLocation(timezone)
			if err != nil {
				continue
			}
			localPlan := in.PlanTime.In(loc)
			cycleLocal := localPlan.Add(-time.Duration(hourOffset) * time.Hour)
			if cycleLocal.Hour() != scheduleHour {
				continue
			}
			if !catchUp {
				currentLocal := in.PlanTime.In(loc)
				if cycleLocal.Year() != currentLocal.Year() || cycleLocal.YearDay() != currentLocal.YearDay() {
					continue
				}
			}
			planDate, windowStart, windowEnd := priorLocalCurationDay(cycleLocal)
			agentIDs, err := activeMemoryCurationAgentIDs(ctx, pool, workspaceID, userID, targetScope, profileID, windowStart, windowEnd)
			if err != nil {
				return HandlerResult{}, err
			}
			if len(agentIDs) == 0 {
				continue
			}
			runStatus := "queued"
			if time.Since(runtimeLastSeenAt) > service.AgentHealthStaleThreshold {
				runStatus = "waiting_runtime"
			}
			if stageName == "agent_self_review" && agentRunTableAvailable {
				var inserted int64
				err = pool.QueryRow(ctx, `
					WITH inserted AS (
						INSERT INTO memory_curation_run (
						  workspace_id, stage, trigger_kind, status, date_from, date_to,
						  profile_id, owner_user_id, runtime_id, curator_agent_id, curator_model,
						  curator_mode, confidence_threshold, config_version, target_agent_ids,
						  execution_owner
						) VALUES ($1,$2,'scheduled',$3,$4,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13::uuid[],'daemon')
						ON CONFLICT DO NOTHING
						RETURNING id, workspace_id, runtime_id
					), child_runs AS (
						INSERT INTO memory_curation_agent_run (
						  parent_run_id, workspace_id, agent_id, runtime_id, stage, status, error, finished_at
						)
						SELECT i.id, i.workspace_id, a.id, COALESCE(a.runtime_id, i.runtime_id), 'agent_self_review',
						       CASE WHEN rt.last_seen_at >= now() - make_interval(secs => $14::double precision) THEN 'queued' ELSE 'skipped' END,
						       CASE WHEN rt.last_seen_at >= now() - make_interval(secs => $14::double precision) THEN '' ELSE 'runtime offline; skipped' END,
						       CASE WHEN rt.last_seen_at >= now() - make_interval(secs => $14::double precision) THEN NULL ELSE now() END
						  FROM inserted i
						  JOIN agent a ON a.workspace_id = i.workspace_id AND a.id = ANY($13::uuid[])
						  LEFT JOIN agent_runtime rt ON rt.id = COALESCE(a.runtime_id, i.runtime_id)
						ON CONFLICT (parent_run_id, agent_id, stage) DO NOTHING
						RETURNING 1
					)
					SELECT count(*) FROM inserted
				`, workspaceID, stageName, runStatus, planDate, profileID, userID, runtimeID, curatorAgentID, model, mode, confidenceThreshold, configVersion, agentIDs, service.AgentHealthStaleThreshold.Seconds()).Scan(&inserted)
				if err != nil {
					return HandlerResult{}, err
				}
				created += inserted
				continue
			}
			tag, err := pool.Exec(ctx, `
				INSERT INTO memory_curation_run (
				  workspace_id, stage, trigger_kind, status, date_from, date_to,
				  profile_id, owner_user_id, runtime_id, curator_agent_id, curator_model,
				  curator_mode, confidence_threshold, config_version, target_agent_ids,
				  execution_owner
				) VALUES ($1,$2,'scheduled',$3,$4,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13::uuid[],'daemon')
				ON CONFLICT DO NOTHING
			`, workspaceID, stageName, runStatus, planDate, profileID, userID, runtimeID, curatorAgentID, model, mode, confidenceThreshold, configVersion, agentIDs)
			if err != nil {
				return HandlerResult{}, err
			}
			created += tag.RowsAffected()
		}
		if in.Heartbeat != nil {
			_ = in.Heartbeat(ctx)
		}
		return HandlerResult{RowsAffected: created, Result: map[string]any{"stage": stageName, "run_intents_created": created}}, nil
	}
}

func scheduleDefaultAgentSelfReviewRuns(ctx context.Context, pool *pgxpool.Pool, planTime time.Time, hourOffset int, heartbeat func(context.Context) error) (HandlerResult, error) {
	loc, err := time.LoadLocation(defaultMemoryCurationTimezone)
	if err != nil {
		return HandlerResult{}, err
	}
	localPlan := planTime.In(loc)
	cycleLocal := localPlan.Add(-time.Duration(hourOffset) * time.Hour)
	if cycleLocal.Hour() != defaultAgentSelfReviewScheduleHour {
		return HandlerResult{Result: map[string]any{"stage": "agent_self_review", "run_intents_created": int64(0), "reason": "not_default_schedule_hour"}}, nil
	}
	planDate, windowStart, windowEnd := priorLocalCurationDay(cycleLocal)
	activeSources, err := defaultSelfReviewActiveSources(ctx, pool)
	if err != nil {
		return HandlerResult{}, err
	}
	var created int64
	err = pool.QueryRow(ctx, `
		WITH active_agents AS MATERIALIZED (
		  SELECT DISTINCT a.workspace_id, a.id AS agent_id, a.runtime_id,
		         rt.last_seen_at >= now() - make_interval(secs => $4::double precision) AS runtime_fresh
		    FROM agent a
		    JOIN agent_runtime rt ON rt.id = a.runtime_id
		    JOIN (`+activeSources+`
		    ) active ON active.agent_id = a.id
		   WHERE a.archived_at IS NULL
		     AND a.runtime_id IS NOT NULL
		     AND NOT EXISTS (
		       SELECT 1 FROM graph_memory_profile gmp
		       WHERE gmp.workspace_id = a.workspace_id
		         AND gmp.memory_type = 'graph'
		     )
		), workspace_targets AS (
		  SELECT workspace_id,
		         array_agg(agent_id ORDER BY agent_id) AS target_agent_ids,
		         bool_or(runtime_fresh) AS has_online_runtime
		    FROM active_agents
		   GROUP BY workspace_id
		), inserted AS (
		  INSERT INTO memory_curation_run (
		    workspace_id, stage, trigger_kind, status, date_from, date_to,
		    curator_mode, confidence_threshold, target_agent_ids, execution_owner
		  )
		  SELECT wt.workspace_id, 'agent_self_review', 'scheduled',
		         CASE WHEN wt.has_online_runtime THEN 'queued' ELSE 'waiting_runtime' END,
		         $1::date, $1::date, $2, $3, wt.target_agent_ids, 'daemon'
		    FROM workspace_targets wt
		   WHERE NOT EXISTS (
		     SELECT 1
		       FROM memory_curation_run existing
		      WHERE existing.workspace_id = wt.workspace_id
		        AND existing.stage = 'agent_self_review'
		        AND existing.trigger_kind = 'scheduled'
		        AND existing.date_from = $1::date
		        AND existing.profile_id IS NULL
		   )
		  RETURNING id, workspace_id
		), child_runs AS (
		  INSERT INTO memory_curation_agent_run (
		    parent_run_id, workspace_id, agent_id, runtime_id, stage, status, error, finished_at
		  )
		  SELECT i.id, i.workspace_id, a.agent_id, a.runtime_id, 'agent_self_review',
		         CASE WHEN a.runtime_fresh THEN 'queued' ELSE 'skipped' END,
		         CASE WHEN a.runtime_fresh THEN '' ELSE 'runtime offline; skipped' END,
		         CASE WHEN a.runtime_fresh THEN NULL ELSE now() END
		    FROM inserted i
		    JOIN active_agents a ON a.workspace_id = i.workspace_id
		  ON CONFLICT (parent_run_id, agent_id, stage) DO NOTHING
		  RETURNING 1
		)
		SELECT count(*) FROM inserted
	`, planDate, defaultAgentSelfReviewMode, defaultAgentSelfReviewConfidence, service.AgentHealthStaleThreshold.Seconds(), windowStart, windowEnd).Scan(&created)
	if err != nil {
		return HandlerResult{}, err
	}
	if heartbeat != nil {
		_ = heartbeat(ctx)
	}
	return HandlerResult{RowsAffected: created, Result: map[string]any{"stage": "agent_self_review", "run_intents_created": created, "schedule": "default_active_agents"}}, nil
}

func defaultSelfReviewActiveSources(ctx context.Context, pool *pgxpool.Pool) (string, error) {
	return memoryCurationActiveSources(ctx, pool, "$5", "$6")
}

func memoryCurationActiveSources(ctx context.Context, pool *pgxpool.Pool, windowStartArg, windowEndArg string) (string, error) {
	sources := `
		      SELECT agent_id
		        FROM agent_inbox_event
		       WHERE COALESCE(completed_at, started_at, dispatched_at, created_at) >= ` + windowStartArg + `::timestamptz
		         AND COALESCE(completed_at, started_at, dispatched_at, created_at) < ` + windowEndArg + `::timestamptz
		      UNION
		      SELECT agent_id
		        FROM agent_inbox_event
		       WHERE created_at >= ` + windowStartArg + `::timestamptz
		         AND created_at < ` + windowEndArg + `::timestamptz`
	optionalSources := []struct {
		relation string
		query    string
	}{
		{
			relation: "public.agent_task_queue",
			query: `
		      UNION
		      SELECT q.agent_id
		        FROM agent_task_queue q
		       WHERE COALESCE(q.completed_at, q.started_at, q.dispatched_at, q.created_at) >= ` + windowStartArg + `::timestamptz
		         AND COALESCE(q.completed_at, q.started_at, q.dispatched_at, q.created_at) < ` + windowEndArg + `::timestamptz`,
		},
		{
			relation: "public.agent_memory_write_event",
			query: `
		      UNION
		      SELECT w.agent_id
		        FROM agent_memory_write_event w
		       WHERE w.created_at >= ` + windowStartArg + `::timestamptz
		         AND w.created_at < ` + windowEndArg + `::timestamptz`,
		},
		{
			relation: "public.agent_memory_curation_candidate",
			query: `
		      UNION
		      SELECT c.source_agent_id AS agent_id
		        FROM agent_memory_curation_candidate c
		       WHERE c.source_agent_id IS NOT NULL
		         AND c.created_at >= ` + windowStartArg + `::timestamptz
		         AND c.created_at < ` + windowEndArg + `::timestamptz`,
		},
		{
			relation: "public.agent_memory_sync_entry",
			query: `
		      UNION
		      SELECT s.agent_id
		        FROM agent_memory_sync_entry s
		       WHERE s.updated_at >= ` + windowStartArg + `::timestamptz
		         AND s.updated_at < ` + windowEndArg + `::timestamptz`,
		},
		{
			relation: "public.agent_skill_suggestion",
			query: `
		      UNION
		      SELECT ss.agent_id
		        FROM agent_skill_suggestion ss
		       WHERE ss.updated_at >= ` + windowStartArg + `::timestamptz
		         AND ss.updated_at < ` + windowEndArg + `::timestamptz`,
		},
	}
	for _, source := range optionalSources {
		available, err := relationExists(ctx, pool, source.relation)
		if err != nil {
			return "", err
		}
		if available {
			sources += source.query
		}
	}
	return sources, nil
}

func priorLocalCurationDay(cycleLocal time.Time) (planDate, windowStartUTC, windowEndUTC time.Time) {
	localMidnight := time.Date(cycleLocal.Year(), cycleLocal.Month(), cycleLocal.Day(), 0, 0, 0, 0, cycleLocal.Location()).AddDate(0, 0, -1)
	planDate = time.Date(localMidnight.Year(), localMidnight.Month(), localMidnight.Day(), 0, 0, 0, 0, time.UTC)
	return planDate, localMidnight.UTC(), localMidnight.AddDate(0, 0, 1).UTC()
}

func memoryCurationAgentRunTableExists(ctx context.Context, pool *pgxpool.Pool) (bool, error) {
	return relationExists(ctx, pool, "public.memory_curation_agent_run")
}

func relationExists(ctx context.Context, pool *pgxpool.Pool, name string) (bool, error) {
	var exists bool
	err := pool.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, name).Scan(&exists)
	return exists, err
}

func normalizeScheduledMemoryCurationStage(stage any) string {
	switch fmt.Sprint(stage) {
	case "l1", "l2", "l1_daily", "l2_review", "agent_self_review":
		return "agent_self_review"
	case "l3", "l4", "l3_promote", "l4_curator", "team_curation":
		return "team_curation"
	default:
		return "team_curation"
	}
}

func activeMemoryCurationAgentIDs(ctx context.Context, pool *pgxpool.Pool, workspaceID, userID, targetScope, profileID string, windowStart, windowEnd time.Time) ([]string, error) {
	activeSources, err := memoryCurationActiveSources(ctx, pool, "$3", "$4")
	if err != nil {
		return nil, err
	}
	rows, err := pool.Query(ctx, `
		WITH targets AS (
		  SELECT a.id, a.runtime_id
		    FROM agent a
		   WHERE a.workspace_id = $1
		     AND a.archived_at IS NULL
		     AND (($5 <> 'selected' AND a.owner_id = $2) OR ($5 = 'selected' AND EXISTS (
		       SELECT 1 FROM memory_curator_target t WHERE t.profile_id = $6 AND t.agent_id = a.id
		     )))
		), active AS (
		  `+activeSources+`
		)
		SELECT t.id::text
		  FROM targets t
		  JOIN active act ON act.agent_id = t.id
		  JOIN agent_runtime rt ON rt.id = t.runtime_id
		   AND rt.last_seen_at >= now() - make_interval(secs => $7::double precision)
		 ORDER BY t.id
	`, workspaceID, userID, windowStart, windowEnd, targetScope, profileID, service.AgentHealthStaleThreshold.Seconds())
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
