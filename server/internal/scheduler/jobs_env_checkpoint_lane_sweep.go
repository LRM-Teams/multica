package scheduler

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const JobNameEnvCheckpointLaneSweep = "env_checkpoint_lane_sweep"

// envCheckpointLaneStaleAfter is how long a lane may sit in `provisioning`
// before the sweeper declares it abandoned. Lane materialization is a handful
// of sandbox jobs; 15 minutes is well past the sandboxd create timeout, so a
// lane still provisioning after that lost its owner to a crash.
const envCheckpointLaneStaleAfter = 15 * time.Minute

// envCheckpointLaneSweepBatch bounds one tick's work. Sweeping is not urgent
// (the lanes have already been abandoned for a quarter of an hour) and the
// cadence is 5 minutes, so a backlog drains over several ticks rather than
// holding one long transaction open.
const envCheckpointLaneSweepBatch = 200

// envCheckpointLaneSweptError is what a swept lane records. It names the sweep
// as the cause and says the lane's resources may outlive it, so whoever reads
// the row is not misled into thinking the failure was clean.
const envCheckpointLaneSweptError = "lane materialization abandoned; swept. Any sandbox, project or runtime recorded on this row may still exist."

// envCheckpointLaneStaleCutoff derives the cutoff from the scheduler's plan
// time rather than the process clock. Plan time is floored from the database's
// own now(), so the cutoff cannot drift with app/database clock skew, and it
// trails db_now by at most one cadence — which errs toward sweeping late. That
// is the only safe direction: sweeping a lane that is still being materialized
// would fail a live lane and orphan what it had already built.
func envCheckpointLaneStaleCutoff(planTime time.Time) time.Time {
	return planTime.Add(-envCheckpointLaneStaleAfter)
}

// EnvCheckpointLaneSweepJob fails lanes abandoned mid-materialization so their
// sandboxes stop being invisible and checkpoint deletion is not blocked forever
// by a dead owner.
//
// Failing the row is the whole job: it does not delete the lane's sandboxes.
// The lane's per-step ids stay on the row precisely so the resources remain
// attributable after the sweep, and reclaiming them belongs to checkpoint
// deletion, which is the only place that knows whether the savepoint is still
// wanted.
func EnvCheckpointLaneSweepJob(pool *pgxpool.Pool) JobSpec {
	return JobSpec{
		Name:              JobNameEnvCheckpointLaneSweep,
		Cadence:           5 * time.Minute,
		CatchUpMode:       CatchUpLatestOnly,
		MaxPlansPerTick:   1,
		RunTimeout:        2 * time.Minute,
		StaleTimeout:      5 * time.Minute,
		HeartbeatInterval: 30 * time.Second,
		AllowStaleReentry: true,
		MaxAttempts:       3,
		RetryBackoff:      []time.Duration{time.Minute, 2 * time.Minute, 5 * time.Minute},
		Scopes:            StaticScopes(ScopeGlobal),
		Handler:           makeEnvCheckpointLaneSweepHandler(pool),
	}
}

func makeEnvCheckpointLaneSweepHandler(pool *pgxpool.Pool) Handler {
	return func(ctx context.Context, in HandlerInput) (HandlerResult, error) {
		if pool == nil {
			return HandlerResult{Result: map[string]any{"skipped": true, "reason": "database_unavailable"}}, nil
		}
		// One statement, so the staleness test and the write cannot come apart:
		// a lane its owner drove to `ready` between a hypothetical list and a
		// later write would otherwise be failed while healthy.
		swept, err := db.New(pool).SweepStaleProvisioningEnvCheckpointLanes(ctx, db.SweepStaleProvisioningEnvCheckpointLanesParams{
			StaleBefore: pgtype.Timestamptz{Time: envCheckpointLaneStaleCutoff(in.PlanTime), Valid: true},
			RowLimit:    envCheckpointLaneSweepBatch,
			Error: pgtype.Text{
				String: envCheckpointLaneSweptError,
				Valid:  true,
			},
		})
		if err != nil {
			return HandlerResult{}, err
		}
		if in.Heartbeat != nil {
			_ = in.Heartbeat(ctx)
		}
		return HandlerResult{
			RowsAffected: int64(len(swept)),
			Result: map[string]any{
				"lanes_swept": len(swept),
				"batch_limit": envCheckpointLaneSweepBatch,
				// A full batch means there is more to do; the next tick continues.
				"batch_full": len(swept) == envCheckpointLaneSweepBatch,
			},
		}, nil
	}
}
