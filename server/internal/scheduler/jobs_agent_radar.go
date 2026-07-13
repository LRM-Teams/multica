package scheduler

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/multica-ai/multica/server/internal/memorycuration"
	"github.com/multica-ai/multica/server/internal/radar"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const JobNameAgentRadarSchedule = "agent_radar_schedule"

const (
	agentRadarCadence      = 10 * time.Minute
	agentRadarHourlyBudget = 6
	agentRadarBatchLimit   = 200
	agentRadarCooldownKey  = "periodic_project_radar"
)

type radarCandidate struct {
	WorkspaceID pgtype.UUID
	AgentID     pgtype.UUID
	DisplayName string
}

func AgentRadarScheduleJob(pool *pgxpool.Pool, taskSvc *service.TaskService) JobSpec {
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
		Handler:           makeAgentRadarScheduleHandler(pool, taskSvc),
	}
}

func makeAgentRadarScheduleHandler(pool *pgxpool.Pool, taskSvc *service.TaskService) Handler {
	return func(ctx context.Context, in HandlerInput) (HandlerResult, error) {
		if pool == nil || taskSvc == nil {
			return HandlerResult{Result: map[string]any{"skipped": true, "reason": "db_unavailable"}}, nil
		}
		repaired, err := reconcileTerminalRadarRuns(ctx, pool)
		if err != nil {
			return HandlerResult{}, err
		}
		candidates, err := listRadarCandidates(ctx, pool)
		if err != nil {
			return HandlerResult{}, err
		}
		q := taskSvc.Queries
		created := int64(0)
		failed := 0
		skippedActive := 0
		skippedNotReady := 0
		skippedBudget := 0
		hourStart := in.PlanTime.Add(-time.Hour)
		for _, candidate := range candidates {
			recent, err := q.CountRecentAgentRadarRuns(ctx, db.CountRecentAgentRadarRunsParams{
				WorkspaceID: candidate.WorkspaceID,
				AgentID:     candidate.AgentID,
				CreatedAt:   pgtype.Timestamptz{Time: hourStart, Valid: true},
			})
			if err != nil {
				return HandlerResult{}, err
			}
			if recent >= agentRadarHourlyBudget {
				skippedBudget++
				continue
			}
			radarCtx, err := radar.NewContextBuilder(pool, memorycuration.DefaultWorkspacesRoot()).Build(ctx, uuidString(candidate.WorkspaceID), uuidString(candidate.AgentID))
			if err != nil {
				failed++
				slog.Warn("agent radar: context build failed", "agent_id", uuidString(candidate.AgentID), "error", err)
				continue
			}
			_, _, err = taskSvc.EnqueueAgentRadarRun(ctx, service.EnqueueAgentRadarRunParams{
				WorkspaceID:    candidate.WorkspaceID,
				AgentID:        candidate.AgentID,
				TriggerKind:    "scheduled",
				TriggerRef:     in.PlanTime.UTC().Format(time.RFC3339),
				CooldownKey:    agentRadarCooldownKey,
				ContextSummary: "Scheduled project radar check for " + candidate.DisplayName,
				ScheduledFor:   in.PlanTime,
				Prompt:         radar.BuildPrompt(radarCtx),
			})
			switch {
			case errors.Is(err, service.ErrAgentRadarRunActive):
				skippedActive++
				continue
			case errors.Is(err, service.ErrAgentRadarNotReady):
				skippedNotReady++
				continue
			case err != nil:
				failed++
				slog.Warn("agent radar: enqueue failed", "agent_id", uuidString(candidate.AgentID), "error", err)
				continue
			}
			created++
		}
		return HandlerResult{
			RowsAffected: created,
			Result: map[string]any{
				"candidates":      len(candidates),
				"created":         created,
				"failed":          failed,
				"repaired":        repaired,
				"skipped_active":  skippedActive,
				"skipped_offline": skippedNotReady,
				"skipped_budget":  skippedBudget,
				"hourly_budget":   agentRadarHourlyBudget,
				"cooldown_key":    agentRadarCooldownKey,
			},
		}, nil
	}
}

// reconcileTerminalRadarRuns closes the crash window between a task becoming
// terminal and its Radar completion hook updating the run. A completed task is
// marked failed here rather than replayed because action execution may already
// have produced external side effects before the process stopped; the next
// scheduled observation can safely try again without duplicating those effects.
func reconcileTerminalRadarRuns(ctx context.Context, pool *pgxpool.Pool) (int64, error) {
	result, err := pool.Exec(ctx, `
		UPDATE agent_radar_run rr
		SET status = CASE WHEN task.status = 'cancelled' THEN 'cancelled' ELSE 'failed' END,
		    error = CASE
		      WHEN rr.error <> '' THEN rr.error
		      WHEN task.status = 'completed' THEN 'Radar completion processing was interrupted'
		      ELSE COALESCE(task.error, task.failure_reason, 'Radar task terminated')
		    END,
		    finished_at = COALESCE(rr.finished_at, now()),
		    updated_at = now()
		FROM agent_task_queue task
		WHERE rr.task_id = task.id
		  AND rr.status IN ('planned', 'queued', 'running')
		  AND task.status IN ('completed', 'failed', 'cancelled')
	`)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected(), nil
}

func listRadarCandidates(ctx context.Context, pool *pgxpool.Pool) ([]radarCandidate, error) {
	rows, err := pool.Query(ctx, `
		SELECT a.workspace_id, a.id, a.display_name
		FROM agent a
		JOIN agent_runtime ar
		  ON ar.id = a.runtime_id
		 AND ar.workspace_id = a.workspace_id
		 AND ar.status = 'online'
		WHERE a.archived_at IS NULL
		  AND a.runtime_id IS NOT NULL
		  AND NOT EXISTS (
		    SELECT 1
		    FROM agent_radar_run rr
		    WHERE rr.workspace_id = a.workspace_id
		      AND rr.agent_id = a.id
		      AND rr.status IN ('planned', 'queued', 'running')
		  )
		  AND EXISTS (
		    SELECT 1
		    FROM channel_member cm
		    JOIN channel c ON c.id = cm.channel_id AND c.workspace_id = cm.workspace_id
		    WHERE cm.workspace_id = a.workspace_id
		      AND cm.member_type = 'agent'
		      AND cm.member_id = a.id
		      AND c.archived_at IS NULL
		  )
		ORDER BY (
		  SELECT max(history.created_at)
		  FROM agent_radar_run history
		  WHERE history.workspace_id = a.workspace_id
		    AND history.agent_id = a.id
		) ASC NULLS FIRST,
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
		if err := rows.Scan(&candidate.WorkspaceID, &candidate.AgentID, &candidate.DisplayName); err != nil {
			return nil, err
		}
		out = append(out, candidate)
	}
	return out, rows.Err()
}

func uuidString(id pgtype.UUID) string {
	if !id.Valid {
		return ""
	}
	return uuid.UUID(id.Bytes).String()
}
