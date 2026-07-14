package scheduler

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/multica-ai/multica/server/internal/memorycuration"
)

const (
	JobNameMemoryL1DailyRecord   = "memory_l1_daily_record"
	JobNameMemoryL2ReviewExtract = "memory_l2_review_extract"
	JobNameMemoryL3Promote       = "memory_l3_promote"
	JobNameMemoryL4Curator       = "memory_l4_curator"
)

func MemoryCurationJobs(pool *pgxpool.Pool) []JobSpec {
	return []JobSpec{
		memoryCurationJob(pool, JobNameMemoryL1DailyRecord, memorycuration.StageL1, 0),
		memoryCurationJob(pool, JobNameMemoryL2ReviewExtract, memorycuration.StageL2, 1),
		memoryCurationJob(pool, JobNameMemoryL3Promote, memorycuration.StageL3, 2),
		memoryCurationJob(pool, JobNameMemoryL4Curator, memorycuration.StageL4, 3),
	}
}

func memoryCurationJob(pool *pgxpool.Pool, name string, stage memorycuration.Stage, hourOffset int) JobSpec {
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

// makeMemoryCurationIntentHandler creates durable per-user run intents. The
// selected daemon owns all filesystem and reviewer execution; the server never
// assumes it can see a user's local agent roots.
func makeMemoryCurationIntentHandler(pool *pgxpool.Pool, stage memorycuration.Stage, hourOffset int) Handler {
	return func(ctx context.Context, in HandlerInput) (HandlerResult, error) {
		if pool == nil {
			return HandlerResult{Result: map[string]any{"skipped": true, "reason": "database_unavailable", "stage": stage}}, nil
		}
		rows, err := pool.Query(ctx, `
			SELECT p.id::text, p.workspace_id::text, p.user_id::text,
			       p.runtime_id::text, p.curator_agent_id::text, p.model_override,
			       p.mode, p.confidence_threshold, p.target_scope, p.timezone,
			       p.schedule_hour, p.catch_up_enabled, p.config_version, rt.status
			  FROM memory_curator_profile p
			  JOIN agent_runtime rt ON rt.id = p.runtime_id
			  JOIN agent curator ON curator.id = p.curator_agent_id
			 WHERE p.enabled = true
			   AND curator.archived_at IS NULL
		`)
		if err != nil {
			return HandlerResult{}, err
		}
		defer rows.Close()
		created := int64(0)
		for rows.Next() {
			var profileID, workspaceID, userID, runtimeID, curatorAgentID, model, mode, targetScope, timezone, runtimeStatus string
			var confidenceThreshold float64
			var scheduleHour int
			var catchUp bool
			var configVersion int64
			if err := rows.Scan(&profileID, &workspaceID, &userID, &runtimeID, &curatorAgentID, &model, &mode, &confidenceThreshold, &targetScope, &timezone, &scheduleHour, &catchUp, &configVersion, &runtimeStatus); err != nil {
				return HandlerResult{}, err
			}
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
			planDate := time.Date(cycleLocal.Year(), cycleLocal.Month(), cycleLocal.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -1)
			var agentIDs []string
			if targetScope == "selected" {
				targetRows, err := pool.Query(ctx, `SELECT agent_id::text FROM memory_curator_target WHERE profile_id = $1 ORDER BY agent_id`, profileID)
				if err != nil {
					return HandlerResult{}, err
				}
				for targetRows.Next() {
					var agentID string
					if err := targetRows.Scan(&agentID); err != nil {
						targetRows.Close()
						return HandlerResult{}, err
					}
					agentIDs = append(agentIDs, agentID)
				}
				if err := targetRows.Err(); err != nil {
					targetRows.Close()
					return HandlerResult{}, err
				}
				targetRows.Close()
			} else {
				targetRows, err := pool.Query(ctx, `SELECT id::text FROM agent WHERE workspace_id = $1 AND owner_id = $2 AND archived_at IS NULL ORDER BY created_at, id`, workspaceID, userID)
				if err != nil {
					return HandlerResult{}, err
				}
				for targetRows.Next() {
					var agentID string
					if err := targetRows.Scan(&agentID); err != nil {
						targetRows.Close()
						return HandlerResult{}, err
					}
					agentIDs = append(agentIDs, agentID)
				}
				if err := targetRows.Err(); err != nil {
					targetRows.Close()
					return HandlerResult{}, err
				}
				targetRows.Close()
			}
			if len(agentIDs) == 0 {
				continue
			}
			runStatus := "queued"
			if runtimeStatus != "online" {
				runStatus = "waiting_runtime"
			}
			tag, err := pool.Exec(ctx, `
				INSERT INTO memory_curation_run (
				  workspace_id, stage, trigger_kind, status, date_from, date_to,
				  profile_id, owner_user_id, runtime_id, curator_agent_id, curator_model,
				  curator_mode, confidence_threshold, config_version, target_agent_ids,
				  execution_owner
				) VALUES ($1,$2,'scheduled',$3,$4,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13::uuid[],'daemon')
				ON CONFLICT DO NOTHING
			`, workspaceID, memorycuration.DBStageName(stage), runStatus, planDate, profileID, userID, runtimeID, curatorAgentID, model, mode, confidenceThreshold, configVersion, agentIDs)
			if err != nil {
				return HandlerResult{}, err
			}
			created += tag.RowsAffected()
		}
		if err := rows.Err(); err != nil {
			return HandlerResult{}, err
		}
		if in.Heartbeat != nil {
			_ = in.Heartbeat(ctx)
		}
		return HandlerResult{RowsAffected: created, Result: map[string]any{"stage": stage, "run_intents_created": created}}, nil
	}
}
