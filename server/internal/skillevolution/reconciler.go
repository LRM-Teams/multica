// SPDX-License-Identifier: Apache-2.0

package skillevolution

// Run reconciliation (spec §12.6, plan Slice 3.3): a pure decision
// function that tells the caller what may safely happen to a run — never
// what must happen. The scheduler only ever applies the safety terminals
// (failed/stale); resumption stays an explicit, lease-fenced operation.

import (
	"fmt"
	"time"
)

// DefaultPhaseDeadline bounds how long a phase may sit without progress
// (updated_at) before reconciliation fails the run. A day: manual runs in
// the first milestone are operator-driven; anything idle that long is
// abandoned, not slow.
const DefaultPhaseDeadline = 24 * time.Hour

// ReconciliationAction is one reconciler verdict.
type ReconciliationAction string

const (
	// ReconcileNone: the run is terminal and stable; nothing to do.
	ReconcileNone ReconciliationAction = "none"
	// ReconcileAwaitOwner: a live lease owns the run; reconciliation
	// never interferes with a working owner.
	ReconcileAwaitOwner ReconciliationAction = "await_owner"
	// ReconcileResumeCheckpoint: the lease lapsed and a usable
	// checkpoint exists; the next attempt resumes from it.
	ReconcileResumeCheckpoint ReconciliationAction = "resume_from_checkpoint"
	// ReconcileRequeuePhase: the lease lapsed with no checkpoint, but
	// the phase is within its deadline; it may be re-driven from the
	// run's current status (CAS makes the re-drive safe).
	ReconcileRequeuePhase ReconciliationAction = "requeue_phase"
	// ReconcileMarkFailed: the phase exceeded its deadline with no live
	// lease; a new run would have to start over.
	ReconcileMarkFailed ReconciliationAction = "mark_failed"
	// ReconcileMarkStale: the run's validation surface changed (pin
	// unreadable or a checkpoint from a different pin); spec §12.6 —
	// stale, never auto-rebased.
	ReconcileMarkStale ReconciliationAction = "mark_stale"
)

// ReconcilerInput is everything the decision needs. Lease and Checkpoints
// may be nil/empty — absence is informative (nobody is driving).
type ReconcilerInput struct {
	Now         time.Time
	Run         EvolutionRunRecord
	Lease       *RunLease
	Checkpoints []RunCheckpoint
	// PhaseDeadline bounds phase staleness; <= 0 selects
	// DefaultPhaseDeadline.
	PhaseDeadline time.Duration
}

// ReconciliationDecision is the verdict plus the evidence for audit.
type ReconciliationDecision struct {
	Action     ReconciliationAction
	NextStatus EvolutionRunStatus // set for mark_failed / mark_stale
	Checkpoint *RunCheckpoint     // set for resume_from_checkpoint
	Reason     string
}

// ReconcileRun decides what may safely happen to one run. Order matters:
// terminal stability first, then ownership (a live lease is never
// disturbed), then the deadline (an abandoned phase fails even if a
// checkpoint survives — checkpoints accelerate recovery, they do not
// extend deadlines), then checkpoint resume, then requeue.
func ReconcileRun(input ReconcilerInput) (ReconciliationDecision, error) {
	if input.Now.IsZero() {
		return ReconciliationDecision{}, fmt.Errorf("%w: reconciliation needs a clock", ErrInvalidContract)
	}
	deadline := input.PhaseDeadline
	if deadline <= 0 {
		deadline = DefaultPhaseDeadline
	}
	run := input.Run
	if run.Status.Terminal() {
		return ReconciliationDecision{
			Action: ReconcileNone,
			Reason: fmt.Sprintf("run %s is terminal (%s): terminal runs are stable", run.RunID, run.Status),
		}, nil
	}
	if input.Lease != nil && input.Lease.Validate() == nil && input.Lease.ActiveAt(input.Now) {
		return ReconciliationDecision{
			Action: ReconcileAwaitOwner,
			Reason: fmt.Sprintf("run %s is driven by owner %q (attempt %d, expires %s)",
				run.RunID, input.Lease.OwnerID, input.Lease.Attempt, input.Lease.ExpiresAt.Format(time.RFC3339)),
		}, nil
	}
	if input.Now.Sub(run.UpdatedAt) > deadline {
		return ReconciliationDecision{
			Action:     ReconcileMarkFailed,
			NextStatus: EvolutionRunFailed,
			Reason: fmt.Sprintf("run %s has sat in %s since %s without a live lease (deadline %s)",
				run.RunID, run.Status, run.UpdatedAt.Format(time.RFC3339), deadline),
		}, nil
	}
	checkpoint, ok, err := ResumeFromCheckpoint(run, input.Checkpoints)
	if err != nil {
		// Unreadable pin or a checkpoint from a different pin: the
		// validation surface moved — stale, never auto-rebased.
		return ReconciliationDecision{
			Action:     ReconcileMarkStale,
			NextStatus: EvolutionRunStale,
			Reason:     fmt.Sprintf("run %s cannot revalidate its inputs: %v", run.RunID, err),
		}, nil
	}
	if ok {
		return ReconciliationDecision{
			Action:     ReconcileResumeCheckpoint,
			Checkpoint: &checkpoint,
			Reason: fmt.Sprintf("run %s has a %s checkpoint from attempt %d (lease lapsed)",
				run.RunID, checkpoint.Phase, checkpoint.Attempt),
		}, nil
	}
	return ReconciliationDecision{
		Action: ReconcileRequeuePhase,
		Reason: fmt.Sprintf("run %s may re-drive phase %s under a fresh lease", run.RunID, run.Status),
	}, nil
}
