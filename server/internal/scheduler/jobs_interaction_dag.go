// SPDX-License-Identifier: Apache-2.0

package scheduler

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/multica-ai/multica/server/internal/service"
)

// JobNameInteractionDAGPublish is the canonical name used in audit rows.
// Stable across releases — do not rename without a migration.
const JobNameInteractionDAGPublish = "interaction_dag_publish"

// interactionDAGPublishBatchSize bounds one claim inside a handler tick.
const interactionDAGPublishBatchSize = 32

// interactionDAGPublisher is the subset of the publisher the job depends on;
// keeping it an interface lets the scheduler tests run without PostgreSQL.
type interactionDAGPublisher interface {
	PublishClaim(ctx context.Context, limit int) (int, error)
	PublishHealth(ctx context.Context) (service.InteractionDAGPublishHealth, error)
}

// InteractionDAGPublishJob drains the canonical migration 454 publish outbox
// (pending, due retry, and stale-lease rows) once per minute and reports the
// aggregate health counters on every tick. The job never enables Graph read
// paths; it only advances the durable publish lifecycle.
func InteractionDAGPublishJob(pool *pgxpool.Pool, publisher interactionDAGPublisher) JobSpec {
	return JobSpec{
		Name:              JobNameInteractionDAGPublish,
		Cadence:           time.Minute,
		CatchUpMode:       CatchUpLatestOnly,
		CatchUpWindow:     24 * time.Hour,
		RunTimeout:        10 * time.Minute,
		StaleTimeout:      15 * time.Minute,
		HeartbeatInterval: time.Minute,
		AllowStaleReentry: true,
		MaxAttempts:       2,
		RetryBackoff:      []time.Duration{time.Minute},
		Scopes:            StaticScopes(ScopeGlobal),
		Handler: func(ctx context.Context, in HandlerInput) (HandlerResult, error) {
			if publisher == nil {
				return HandlerResult{}, nil
			}
			processed := 0
			for {
				claimed, err := publisher.PublishClaim(ctx, interactionDAGPublishBatchSize)
				processed += claimed
				if err != nil {
					return HandlerResult{RowsAffected: int64(processed)}, err
				}
				if claimed < interactionDAGPublishBatchSize {
					break
				}
				if in.Heartbeat != nil {
					if err := in.Heartbeat(ctx); err != nil {
						return HandlerResult{RowsAffected: int64(processed)}, err
					}
				}
			}
			health, err := publisher.PublishHealth(ctx)
			if err != nil {
				return HandlerResult{RowsAffected: int64(processed)}, err
			}
			// Light heartbeat at the end keeps stale_after fresh for ticks
			// that drained faster than HeartbeatInterval.
			if in.Heartbeat != nil {
				_ = in.Heartbeat(ctx)
			}
			return HandlerResult{
				RowsAffected: int64(processed),
				Result:       map[string]any{"publish_health": health},
			}, nil
		},
	}
}

// JobNameGraphMemoryProjection is the canonical name used in audit rows.
// Stable across releases — do not rename without a migration.
const JobNameGraphMemoryProjection = "graph_memory_projection"

// graphMemoryProjectionBatchSize bounds one claim inside a handler tick.
const graphMemoryProjectionBatchSize = 32

// graphMemoryProjector is the subset of the projector the job depends on.
type graphMemoryProjector interface {
	ProjectClaim(ctx context.Context, limit int) (int, error)
}

// GraphMemoryProjectionJob drains the Task 7 graph projection outbox through
// leases. This job is the ONLY production availability path for graph
// projection: the retired best-effort segment-ingest hook no longer writes,
// and nothing scans segments, atoms, or graph directories for work. Event-time
// facts and route identity govern every write; no Graph read path is enabled.
func GraphMemoryProjectionJob(pool *pgxpool.Pool, projector graphMemoryProjector) JobSpec {
	return JobSpec{
		Name:              JobNameGraphMemoryProjection,
		Cadence:           time.Minute,
		CatchUpMode:       CatchUpLatestOnly,
		CatchUpWindow:     24 * time.Hour,
		RunTimeout:        10 * time.Minute,
		StaleTimeout:      15 * time.Minute,
		HeartbeatInterval: time.Minute,
		AllowStaleReentry: true,
		MaxAttempts:       2,
		RetryBackoff:      []time.Duration{time.Minute},
		Scopes:            StaticScopes(ScopeGlobal),
		Handler: func(ctx context.Context, in HandlerInput) (HandlerResult, error) {
			if projector == nil {
				return HandlerResult{}, nil
			}
			processed := 0
			for {
				claimed, err := projector.ProjectClaim(ctx, graphMemoryProjectionBatchSize)
				processed += claimed
				if err != nil {
					return HandlerResult{RowsAffected: int64(processed)}, err
				}
				if claimed < graphMemoryProjectionBatchSize {
					break
				}
				if in.Heartbeat != nil {
					if err := in.Heartbeat(ctx); err != nil {
						return HandlerResult{RowsAffected: int64(processed)}, err
					}
				}
			}
			if in.Heartbeat != nil {
				_ = in.Heartbeat(ctx)
			}
			return HandlerResult{RowsAffected: int64(processed)}, nil
		},
	}
}
