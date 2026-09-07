// SPDX-License-Identifier: Apache-2.0

package skillevolution

import (
	"context"
	"fmt"
)

// RunCoordinator drives the orchestrator run lifecycle over the ledger
// port (spec §12.6): every transition is validated against the Phase 0
// state machine and then CAS-applied, so a terminal run can never be
// revived even under racing writers (the DB trigger is the floor, this is
// the authority).
type RunCoordinator struct {
	store LedgerStore
}

func NewRunCoordinator(store LedgerStore) *RunCoordinator {
	return &RunCoordinator{store: store}
}

// StartRun admits one new run under its evolution key. A key with a live
// non-terminal run refuses with ErrActiveRunExists — one mutation lane,
// one active run (ADR 0021 D4).
func (c *RunCoordinator) StartRun(ctx context.Context, run EvolutionRunRecord) (EvolutionRunRecord, error) {
	if run.Status != EvolutionRunQueued && run.Status != "" {
		return EvolutionRunRecord{}, fmt.Errorf("a new run must start queued, got %q", run.Status)
	}
	run.Status = EvolutionRunQueued
	if err := c.store.InsertRun(ctx, run); err != nil {
		return EvolutionRunRecord{}, err
	}
	return c.store.GetRun(ctx, run.WorkspaceID, run.RunID)
}

// Transition moves a run to next after validating the state machine. A
// same-status call is an idempotent no-op; an illegal edge fails closed
// with ErrLedgerConflict wrapping the reason; a CAS miss (concurrent
// writer) surfaces ErrLedgerConflict untouched.
func (c *RunCoordinator) Transition(ctx context.Context, workspaceID, runID string, next EvolutionRunStatus) (EvolutionRunRecord, error) {
	run, err := c.store.GetRun(ctx, workspaceID, runID)
	if err != nil {
		return EvolutionRunRecord{}, err
	}
	if run.Status == next {
		return run, nil
	}
	if !run.Status.CanTransition(next) {
		return EvolutionRunRecord{}, fmt.Errorf("%w: run %s cannot move %s -> %s",
			ErrLedgerConflict, runID, run.Status, next)
	}
	if err := c.store.TransitionRun(ctx, workspaceID, runID, run.Status, next); err != nil {
		return EvolutionRunRecord{}, err
	}
	return c.store.GetRun(ctx, workspaceID, runID)
}
