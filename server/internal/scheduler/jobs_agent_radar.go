package scheduler

import (
	"context"
	"encoding/json"
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
	RuntimeID   pgtype.UUID
	DisplayName string
}

func AgentRadarScheduleJob(pool *pgxpool.Pool) JobSpec {
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
		Handler:           makeAgentRadarScheduleHandler(pool),
	}
}

func makeAgentRadarScheduleHandler(pool *pgxpool.Pool) Handler {
	return func(ctx context.Context, in HandlerInput) (HandlerResult, error) {
		if pool == nil {
			return HandlerResult{Result: map[string]any{"skipped": true, "reason": "db_unavailable"}}, nil
		}
		candidates, err := listRadarCandidates(ctx, pool)
		if err != nil {
			return HandlerResult{}, err
		}
		q := db.New(pool)
		created := int64(0)
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
			run, err := q.CreateAgentRadarRun(ctx, db.CreateAgentRadarRunParams{
				WorkspaceID:    candidate.WorkspaceID,
				AgentID:        candidate.AgentID,
				RuntimeID:      candidate.RuntimeID,
				TriggerKind:    "scheduled",
				TriggerRef:     in.PlanTime.UTC().Format(time.RFC3339),
				CooldownKey:    agentRadarCooldownKey,
				ContextSummary: "Scheduled project radar check for " + candidate.DisplayName,
				Status:         "planned",
				ScheduledFor:   pgtype.Timestamptz{Time: in.PlanTime, Valid: true},
			})
			if err != nil {
				return HandlerResult{}, err
			}
			radarCtx, err := radar.NewContextBuilder(pool, memorycuration.DefaultWorkspacesRoot()).Build(ctx, uuidString(candidate.WorkspaceID), uuidString(candidate.AgentID))
			if err != nil {
				return HandlerResult{}, err
			}
			prompt := radar.BuildPrompt(radarCtx)
			taskContext, _ := json.Marshal(service.AgentRadarContext{
				Type:       service.AgentRadarContextType,
				RadarRunID: uuidString(run.ID),
				Prompt:     prompt,
			})
			task, err := q.CreateQuickCreateTask(ctx, db.CreateQuickCreateTaskParams{
				AgentID:   candidate.AgentID,
				RuntimeID: candidate.RuntimeID,
				Priority:  1,
				Context:   taskContext,
			})
			if err != nil {
				return HandlerResult{}, err
			}
			if _, err := q.UpdateAgentRadarRunStatus(ctx, db.UpdateAgentRadarRunStatusParams{
				ID:     run.ID,
				Status: "queued",
				TaskID: task.ID,
			}); err != nil {
				return HandlerResult{}, err
			}
			created++
		}
		return HandlerResult{
			RowsAffected: created,
			Result: map[string]any{
				"candidates":     len(candidates),
				"created":        created,
				"skipped_budget": skippedBudget,
				"hourly_budget":  agentRadarHourlyBudget,
				"cooldown_key":   agentRadarCooldownKey,
			},
		}, nil
	}
}

func listRadarCandidates(ctx context.Context, pool *pgxpool.Pool) ([]radarCandidate, error) {
	rows, err := pool.Query(ctx, `
		SELECT a.workspace_id, a.id, a.runtime_id, a.display_name
		FROM agent a
		WHERE a.archived_at IS NULL
		  AND a.runtime_id IS NOT NULL
		  AND EXISTS (
		    SELECT 1
		    FROM channel_member cm
		    JOIN channel c ON c.id = cm.channel_id AND c.workspace_id = cm.workspace_id
		    WHERE cm.workspace_id = a.workspace_id
		      AND cm.member_type = 'agent'
		      AND cm.member_id = a.id
		      AND c.archived_at IS NULL
		  )
		ORDER BY a.created_at ASC
		LIMIT $1
	`, agentRadarBatchLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []radarCandidate
	for rows.Next() {
		var candidate radarCandidate
		if err := rows.Scan(&candidate.WorkspaceID, &candidate.AgentID, &candidate.RuntimeID, &candidate.DisplayName); err != nil {
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
