package scheduler

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/multica-ai/multica/server/internal/service"
)

const JobNameGraphMemoryAgentReconcile = "graph_memory_agent_reconcile"

// GraphMemoryAgentReconcileJob repairs managed identity/runtime state and
// checkpoints runs whose activity lease expired. The global scheduler lease
// makes the sweep safe across server replicas; ReconcileChannel adds a
// per-channel advisory lock for API/scheduler races.
func GraphMemoryAgentReconcileJob(pool *pgxpool.Pool, control service.GraphMemoryAgentControlPlane) JobSpec {
	return JobSpec{
		Name:              JobNameGraphMemoryAgentReconcile,
		Cadence:           time.Minute,
		ScheduleDelay:     0,
		CatchUpMode:       CatchUpLatestOnly,
		CatchUpWindow:     5 * time.Minute,
		MaxPlansPerTick:   1,
		RunTimeout:        45 * time.Second,
		StaleTimeout:      2 * time.Minute,
		HeartbeatInterval: 15 * time.Second,
		AllowStaleReentry: true,
		MaxAttempts:       2,
		RetryBackoff:      []time.Duration{15 * time.Second},
		Scopes:            StaticScopes(ScopeGlobal),
		Handler: func(ctx context.Context, _ HandlerInput) (HandlerResult, error) {
			if pool == nil || control == nil {
				return HandlerResult{Result: map[string]any{"skipped": true, "reason": "control_plane_unavailable"}}, nil
			}
			checkpointed, err := checkpointExpiredGraphMemoryAgentRuns(ctx, pool)
			if err != nil {
				return HandlerResult{}, err
			}
			rows, err := pool.Query(ctx, `
				SELECT channel.id::text,channel.workspace_id::text
				FROM channel
				LEFT JOIN graph_memory_profile profile ON profile.workspace_id=channel.workspace_id
				LEFT JOIN graph_memory_channel_agent managed ON managed.channel_id=channel.id
				WHERE channel.kind='group' AND channel.system_key IS NULL
				  AND (managed.channel_id IS NOT NULL OR profile.memory_type='graph')
				ORDER BY channel.workspace_id,channel.id`)
			if err != nil {
				return HandlerResult{}, err
			}
			defer rows.Close()
			type channelRef struct{ channelID, workspaceID string }
			refs := make([]channelRef, 0)
			for rows.Next() {
				var ref channelRef
				if err := rows.Scan(&ref.channelID, &ref.workspaceID); err != nil {
					return HandlerResult{}, err
				}
				refs = append(refs, ref)
			}
			if err := rows.Err(); err != nil {
				return HandlerResult{}, err
			}
			reconciled, failed := 0, 0
			for _, ref := range refs {
				if _, err := control.ReconcileChannel(ctx, ref.workspaceID, ref.channelID); err != nil {
					failed++
					slog.Warn("graph memory agent background reconciliation failed", "workspace_id", ref.workspaceID, "channel_id", ref.channelID, "error", err)
					continue
				}
				reconciled++
			}
			return HandlerResult{Result: map[string]any{"reconciled": reconciled, "failed": failed, "checkpointed": checkpointed}}, nil
		},
	}
}

func checkpointExpiredGraphMemoryAgentRuns(ctx context.Context, pool *pgxpool.Pool) (int64, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `
		SELECT run.id::text,state.channel_id::text,
		       jsonb_build_object(
		         'objective',state.objective,'observations',state.observations,
		         'rejected_branches',state.rejected_branches,'open_questions',state.open_questions,
		         'candidate_node_ids',state.candidate_node_ids,'viewed_node_ids',state.viewed_node_ids,
		         'pending_targets',state.pending_targets,'next_hint',state.next_hint
		       )
		FROM graph_memory_agent_state state
		JOIN graph_memory_agent_run run ON run.id=state.active_run_id AND run.status='running'
		WHERE state.lease_expires_at IS NOT NULL AND state.lease_expires_at <= now()
		FOR UPDATE OF state,run`)
	if err != nil {
		return 0, err
	}
	type expired struct {
		runID, channelID string
		patch            json.RawMessage
	}
	items := make([]expired, 0)
	for rows.Next() {
		var item expired
		if err := rows.Scan(&item.runID, &item.channelID, &item.patch); err != nil {
			rows.Close()
			return 0, err
		}
		items = append(items, item)
	}
	rows.Close()
	for _, item := range items {
		if _, err := tx.Exec(ctx, `UPDATE graph_memory_agent_trajectory SET status='checkpointed',state_patch=$2::jsonb,finished_at=now() WHERE run_id=$1::uuid AND status='active'`, item.runID, item.patch); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(ctx, `UPDATE graph_memory_agent_run SET status='checkpointed',finished_at=now() WHERE id=$1::uuid AND status='running'`, item.runID); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(ctx, `UPDATE graph_memory_agent_state SET active_run_id=NULL,state_version=state_version+1,updated_at=now() WHERE channel_id=$1::uuid AND active_run_id=$2::uuid`, item.channelID, item.runID); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return int64(len(items)), nil
}
