// SPDX-License-Identifier: Apache-2.0

package skillevolution

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeLedgerStore is the in-memory LedgerStore port double: single-active
// key admission, CAS transitions, linear pattern revisions.
type fakeLedgerStore struct {
	mu        sync.Mutex
	runs      map[string]EvolutionRunRecord // workspace|runID
	patterns  map[string][]PatternRecord    // workspace|patternID, ascending revision
	activeKey map[string]string             // workspace|keyBody -> active runID
}

func newFakeLedgerStore() *fakeLedgerStore {
	return &fakeLedgerStore{
		runs:      map[string]EvolutionRunRecord{},
		patterns:  map[string][]PatternRecord{},
		activeKey: map[string]string{},
	}
}

func (f *fakeLedgerStore) InsertRun(_ context.Context, run EvolutionRunRecord) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := run.WorkspaceID + "|" + EvolutionKey{
		TargetAgentID: run.TargetAgentID, TaskType: run.TaskType,
		EnvironmentMajorVersion: run.EnvironmentMajorVersion,
	}.Body()
	if active, ok := f.activeKey[key]; ok {
		return fmt.Errorf("%w: active run %s", ErrActiveRunExists, active)
	}
	f.runs[run.WorkspaceID+"|"+run.RunID] = run
	f.activeKey[key] = run.RunID
	return nil
}

func (f *fakeLedgerStore) GetRun(_ context.Context, workspaceID, runID string) (EvolutionRunRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	run, ok := f.runs[workspaceID+"|"+runID]
	if !ok {
		return EvolutionRunRecord{}, fmt.Errorf("%w: run %s", ErrLedgerNotFound, runID)
	}
	return run, nil
}

func (f *fakeLedgerStore) TransitionRun(_ context.Context, workspaceID, runID string, from, to EvolutionRunStatus) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := workspaceID + "|" + runID
	run, ok := f.runs[key]
	if !ok {
		return fmt.Errorf("%w: run %s", ErrLedgerNotFound, runID)
	}
	if run.Status != from {
		return fmt.Errorf("%w: CAS miss %s != %s", ErrLedgerConflict, run.Status, from)
	}
	wasTerminal := run.Status.Terminal()
	run.Status = to
	run.UpdatedAt = time.Now()
	if to.Terminal() && !wasTerminal {
		now := time.Now()
		run.TerminalAt = &now
	}
	f.runs[key] = run
	if to.Terminal() {
		lane := workspaceID + "|" + EvolutionKey{
			TargetAgentID: run.TargetAgentID, TaskType: run.TaskType,
			EnvironmentMajorVersion: run.EnvironmentMajorVersion,
		}.Body()
		if f.activeKey[lane] == runID {
			delete(f.activeKey, lane)
		}
	}
	return nil
}

func (f *fakeLedgerStore) InsertPatternRevision(_ context.Context, record PatternRecord) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := record.WorkspaceID + "|" + record.PatternID
	revisions := f.patterns[key]
	if len(revisions) > 0 && record.Revision != revisions[len(revisions)-1].Revision+1 {
		return fmt.Errorf("%w: revision %d is not linear", ErrLedgerConflict, record.Revision)
	}
	f.patterns[key] = append(revisions, record)
	return nil
}

func (f *fakeLedgerStore) LatestPatternRevision(_ context.Context, workspaceID, patternID string) (PatternRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	revisions := f.patterns[workspaceID+"|"+patternID]
	if len(revisions) == 0 {
		return PatternRecord{}, fmt.Errorf("%w: pattern %s", ErrLedgerNotFound, patternID)
	}
	return revisions[len(revisions)-1], nil
}

func coordinatorRunFixture(runID string) EvolutionRunRecord {
	return EvolutionRunRecord{
		RunID: runID, WorkspaceID: "ws-1", TargetAgentID: "agent-1",
		TaskType: "spreadsheet", EnvironmentMajorVersion: "v1",
		PinnedInputs: []byte(`{}`), CreatedByActor: "member:u1",
	}
}

// The happy path walks queued -> ... -> completed and every illegal edge
// (including terminal revival) fails closed.
func TestRunCoordinatorTransitionsFollowStateMachine(t *testing.T) {
	coord := NewRunCoordinator(newFakeLedgerStore())
	run, err := coord.StartRun(context.Background(), coordinatorRunFixture("run-1"))
	require.NoError(t, err)
	assert.Equal(t, EvolutionRunQueued, run.Status)

	happy := []EvolutionRunStatus{
		EvolutionRunSnapshotting, EvolutionRunConsolidatingPatterns,
		EvolutionRunProposingCandidate, EvolutionRunAwaitingReview,
		EvolutionRunEvaluating, EvolutionRunAwaitingApproval, EvolutionRunCompleted,
	}
	for _, next := range happy {
		run, err = coord.Transition(context.Background(), "ws-1", "run-1", next)
		require.NoError(t, err, "transition to %s", next)
	}
	require.NotNil(t, run.TerminalAt, "the terminal transition stamps terminal_at")

	_, err = coord.Transition(context.Background(), "ws-1", "run-1", EvolutionRunQueued)
	require.ErrorIs(t, err, ErrLedgerConflict, "a completed run cannot be revived")
}

// Interruption terminals are reachable from every live status, and a
// stale/fenced run releases its evolution key for a fresh run.
func TestRunCoordinatorInterruptionTerminalsReleaseTheKey(t *testing.T) {
	coord := NewRunCoordinator(newFakeLedgerStore())
	_, err := coord.StartRun(context.Background(), coordinatorRunFixture("run-2"))
	require.NoError(t, err)

	for _, from := range []EvolutionRunStatus{
		EvolutionRunQueued, EvolutionRunSnapshotting, EvolutionRunConsolidatingPatterns,
		EvolutionRunProposingCandidate, EvolutionRunAwaitingReview, EvolutionRunEvaluating,
		EvolutionRunAwaitingApproval,
	} {
		assert.True(t, from.CanTransition(EvolutionRunFenced),
			"%s must reach the safety-fence terminal", from)
	}
	_, err = coord.Transition(context.Background(), "ws-1", "run-2", EvolutionRunFenced)
	require.NoError(t, err)

	_, err = coord.StartRun(context.Background(), coordinatorRunFixture("run-3"))
	require.NoError(t, err, "a fenced run releases the evolution key")
}

// One mutation lane, one active run: a second start on the same key
// refuses, and the refusal disappears once the lane goes terminal.
func TestRunCoordinatorStartRunAdmitsOneActiveRunPerKey(t *testing.T) {
	coord := NewRunCoordinator(newFakeLedgerStore())
	_, err := coord.StartRun(context.Background(), coordinatorRunFixture("run-a"))
	require.NoError(t, err)

	_, err = coord.StartRun(context.Background(), coordinatorRunFixture("run-b"))
	require.ErrorIs(t, err, ErrActiveRunExists)

	// A different lane is unaffected.
	other := coordinatorRunFixture("run-c")
	other.TaskType = "coding"
	_, err = coord.StartRun(context.Background(), other)
	require.NoError(t, err)

	_, err = coord.Transition(context.Background(), "ws-1", "run-a", EvolutionRunCancelled)
	require.NoError(t, err)
	_, err = coord.StartRun(context.Background(), coordinatorRunFixture("run-d"))
	require.NoError(t, err, "cancelling the active run frees the key")
}

// A new run must start queued; anything else is refused before the store.
func TestRunCoordinatorStartRunRequiresQueued(t *testing.T) {
	coord := NewRunCoordinator(newFakeLedgerStore())
	notQueued := coordinatorRunFixture("run-x")
	notQueued.Status = EvolutionRunEvaluating
	_, err := coord.StartRun(context.Background(), notQueued)
	require.Error(t, err)
}

// The evolution key body matches the migration 493 generated column shape
// byte for byte: agent uuid, task type, environment major version.
func TestRunCoordinatorEvolutionKeyBodyShape(t *testing.T) {
	key := EvolutionKey{TargetAgentID: "d8c6d824-0000-0000-0000-000000000000", TaskType: "spreadsheet", EnvironmentMajorVersion: "v1"}
	assert.Equal(t, "d8c6d824-0000-0000-0000-000000000000:spreadsheet:v1", key.Body())
}
