package handler

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/problemevolution"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const problemEvolutionReapBatch = 50

// ReapProblemEvolutionRuns enforces the two bounds a stuck run cannot enforce
// for itself: a `stopping` run is cancelled once the cancellation deadline
// passes, and a claimed run whose daemon stopped heartbeating is requeued.
//
// Both are server-side on purpose. If the deadline depended on the daemon
// acknowledging the stop, a crashed daemon would leave the run in `stopping`
// forever and the user's stop button would be a lie.
func (h *Handler) ReapProblemEvolutionRuns(ctx context.Context, now time.Time) error {
	if err := h.cancelOverdueProblemEvolutionRuns(ctx, now); err != nil {
		return err
	}
	return h.requeueAbandonedProblemEvolutionRuns(ctx, now)
}

func (h *Handler) cancelOverdueProblemEvolutionRuns(ctx context.Context, now time.Time) error {
	deadline := now.Add(-problemevolution.RunCancellationDeadline)
	overdue, err := h.Queries.ListStopRequestedProblemEvolutionRuns(ctx, db.ListStopRequestedProblemEvolutionRunsParams{
		Deadline:    pgtype.Timestamptz{Time: deadline, Valid: true},
		ResultLimit: problemEvolutionReapBatch,
	})
	if err != nil {
		return err
	}
	for _, run := range overdue {
		reason := run.StopReason
		if reason == "" {
			reason = problemevolution.StopReasonUser
		}
		cancelled, err := h.Queries.ForceCancelProblemEvolutionRun(ctx, db.ForceCancelProblemEvolutionRunParams{
			ID:         run.ID,
			StopReason: reason,
		})
		if err != nil {
			// Another writer settled the run between the list and the update;
			// that is the desired end state either way.
			continue
		}
		h.publishProblemEvolutionRunChanged(uuidToString(cancelled.WorkspaceID), cancelled)
		h.publishProblemEvolutionRunCompleted(uuidToString(cancelled.WorkspaceID), cancelled)
	}
	return nil
}

func (h *Handler) requeueAbandonedProblemEvolutionRuns(ctx context.Context, now time.Time) error {
	staleBefore := now.Add(-problemevolution.HeartbeatStaleAfter)
	stale, err := h.Queries.ListStaleProblemEvolutionRuns(ctx, db.ListStaleProblemEvolutionRunsParams{
		StaleBefore: pgtype.Timestamptz{Time: staleBefore, Valid: true},
		ResultLimit: problemEvolutionReapBatch,
	})
	if err != nil {
		return err
	}
	for _, run := range stale {
		// A `stopping` run is handled by the cancellation deadline instead;
		// requeueing it would restart work the user asked to end.
		if run.Status == "stopping" {
			continue
		}
		requeued, err := h.Queries.ForceReleaseProblemEvolutionRun(ctx, db.ForceReleaseProblemEvolutionRunParams{
			ID:            run.ID,
			FailureReason: problemevolution.StopReasonHeartbeatLost,
		})
		if err != nil {
			continue
		}
		h.publishProblemEvolutionRunChanged(uuidToString(requeued.WorkspaceID), requeued)
	}
	return nil
}
