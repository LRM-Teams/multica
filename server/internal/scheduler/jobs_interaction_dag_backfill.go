// SPDX-License-Identifier: Apache-2.0

package scheduler

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/multica-ai/multica/server/internal/service"
)

// JobNameLegacyBackfill is the canonical scheduler name of the Task 22
// approximate historical backfill. Stable across releases — do not rename
// without a migration.
const JobNameLegacyBackfill = "interaction_dag_legacy_backfill"

// legacyBackfillRunner is the subset of the backfill service the job depends
// on; the narrow interface keeps scheduler tests runnable without
// PostgreSQL.
type legacyBackfillRunner interface {
	BackfillWorkspace(ctx context.Context, workspaceID pgtype.UUID, opts service.LegacyBackfillOptions) (service.LegacyBackfillReport, error)
}

// LegacyBackfillJob projects completed historical Tasks into the Universal
// DAG as approximate Segments (spec §8.2, §19.11). It runs behind the final
// shadow gate with its own rate budget and defers to the realtime publish
// quota; the hourly cadence bounds the approximate channel's throughput on
// top of the per-pass task cap.
func LegacyBackfillJob(pool *pgxpool.Pool, backfill legacyBackfillRunner) JobSpec {
	return JobSpec{
		Name:              JobNameLegacyBackfill,
		Cadence:           time.Hour,
		ScheduleDelay:     0,
		CatchUpMode:       CatchUpLatestOnly,
		CatchUpWindow:     24 * time.Hour,
		MaxPlansPerTick:   1,
		RunTimeout:        30 * time.Minute,
		StaleTimeout:      45 * time.Minute,
		HeartbeatInterval: time.Minute,
		AllowStaleReentry: true,
		MaxAttempts:       2,
		RetryBackoff:      []time.Duration{10 * time.Minute},
		Scopes:            StaticScopes(ScopeGlobal),
		Handler: func(ctx context.Context, in HandlerInput) (HandlerResult, error) {
			if backfill == nil {
				return HandlerResult{}, nil
			}
			root, err := graphMemoryWorkspacesRoot()
			if err != nil {
				return HandlerResult{}, err
			}
			dirs, err := findMemoryGraphDirs(root)
			if err != nil {
				return HandlerResult{}, err
			}
			var created int64
			for _, workspaceID := range shadowGateSweepTargets(dirs) {
				ws, err := serviceWorkspaceUUID(workspaceID)
				if err != nil {
					continue
				}
				report, err := backfill.BackfillWorkspace(ctx, ws, service.LegacyBackfillOptions{})
				if err != nil {
					// One workspace never aborts the sweep; the next tick
					// retries it.
					continue
				}
				created += int64(report.SegmentsCreated)
			}
			return HandlerResult{RowsAffected: created}, nil
		},
	}
}
