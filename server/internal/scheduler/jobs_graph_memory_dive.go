// SPDX-License-Identifier: Apache-2.0

package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/multica-ai/multica/server/internal/arealrl"
	"github.com/multica-ai/multica/server/internal/service"
)

const graphMemoryDiveJobsPerTick = 32

// GraphMemoryDiveJobs returns the global durable Dive executor and reward
// outbox flusher. A Manager RunnerID is a stable random identifier for the
// process; the hostname/pid fallback only supports direct handler invocation.
func GraphMemoryDiveJobs(_ *pgxpool.Pool, worker *service.GraphMemoryDiveWorker, sink *arealrl.RewardSink, rl *service.GraphMemoryRLSessionService) []JobSpec {
	return []JobSpec{
		{
			Name: "graph_memory_dive_worker", Cadence: time.Minute,
			CatchUpMode: CatchUpLatestOnly, CatchUpWindow: 24 * time.Hour,
			RunTimeout: 45 * time.Minute, StaleTimeout: 60 * time.Minute,
			HeartbeatInterval: time.Minute, AllowStaleReentry: true, MaxAttempts: 2,
			RetryBackoff: []time.Duration{time.Minute}, Scopes: StaticScopes(ScopeGlobal),
			Handler: func(ctx context.Context, in HandlerInput) (HandlerResult, error) {
				if worker == nil {
					return HandlerResult{}, nil
				}
				workerID := in.RunnerID
				if workerID == "" {
					host, err := os.Hostname()
					if err != nil {
						host = "unknown"
					}
					workerID = fmt.Sprintf("%s-%d", host, os.Getpid())
				}
				processed := 0
				for processed < graphMemoryDiveJobsPerTick {
					did, err := worker.RunOnce(ctx, workerID)
					if err != nil {
						return HandlerResult{RowsAffected: int64(processed)}, err
					}
					if !did {
						break
					}
					processed++
					if in.Heartbeat != nil {
						if err := in.Heartbeat(ctx); err != nil {
							return HandlerResult{RowsAffected: int64(processed)}, err
						}
					}
				}
				return HandlerResult{RowsAffected: int64(processed)}, nil
			},
		},
		{
			Name: "graph_memory_reward_outbox", Cadence: time.Minute,
			CatchUpMode: CatchUpLatestOnly, CatchUpWindow: 24 * time.Hour,
			RunTimeout: 5 * time.Minute, StaleTimeout: 10 * time.Minute,
			HeartbeatInterval: time.Minute, AllowStaleReentry: true, MaxAttempts: 2,
			RetryBackoff: []time.Duration{time.Minute}, Scopes: StaticScopes(ScopeGlobal),
			Handler: func(ctx context.Context, _ HandlerInput) (HandlerResult, error) {
				var delivered, reaped int
				if sink != nil {
					var err error
					delivered, err = sink.DeliverOnce(ctx, 100)
					if err != nil {
						return HandlerResult{}, err
					}
					slog.Info("graph memory reward outbox delivered", "count", delivered)
				}
				if rl != nil {
					var err error
					reaped, err = rl.ReapStaleSessions(ctx, 24*time.Hour, 100)
					if err != nil {
						slog.Warn("graph memory rl session reap failed", "error", err)
					}
				}
				return HandlerResult{RowsAffected: int64(delivered + reaped)}, nil
			},
		},
	}
}
