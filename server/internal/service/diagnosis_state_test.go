// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// fakeDiagnosisStateQueries is an in-memory diagnosisStateQueries for unit
// tests. It mirrors the SQL compare-and-set predicates of the hand-written
// generated companions (mirrors fakeInteractionDAGStore in
// interaction_dag_test.go): guarded updates return applied=false exactly when
// the SQL WHERE clause would match no row.
type fakeDiagnosisStateQueries struct {
	mu       sync.Mutex
	runs     map[string]db.InteractionDagDiagnosisRun
	segments map[string]db.InteractionDagDiagnosisSegment // runID+"/"+segmentID
}

func newFakeDiagnosisStateQueries() *fakeDiagnosisStateQueries {
	return &fakeDiagnosisStateQueries{
		runs:     map[string]db.InteractionDagDiagnosisRun{},
		segments: map[string]db.InteractionDagDiagnosisSegment{},
	}
}

func diagSegmentKey(runID, segmentID string) string { return runID + "/" + segmentID }

func (f *fakeDiagnosisStateQueries) CreateInteractionDAGDiagnosisRun(_ context.Context, arg db.CreateInteractionDAGDiagnosisRunParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.runs[arg.RunID] = db.InteractionDagDiagnosisRun{
		RunID:                 arg.RunID,
		ProjectID:             arg.ProjectID,
		TaskID:                arg.TaskID,
		TopologyHash:          arg.TopologyHash,
		OrderedSegmentIds:     arg.OrderedSegmentIds,
		Status:                string(DiagnosisRunRunning),
		CurrentSegmentOrdinal: 0,
	}
	return nil
}

func (f *fakeDiagnosisStateQueries) GetInteractionDAGDiagnosisRun(_ context.Context, runID string) (db.InteractionDagDiagnosisRun, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	row, ok := f.runs[runID]
	if !ok {
		return db.InteractionDagDiagnosisRun{}, pgx.ErrNoRows
	}
	return row, nil
}

func (f *fakeDiagnosisStateQueries) GetResumableInteractionDAGDiagnosisRun(_ context.Context, arg db.GetResumableInteractionDAGDiagnosisRunParams) (db.InteractionDagDiagnosisRun, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, row := range f.runs {
		if row.ProjectID == arg.ProjectID && row.TaskID == arg.TaskID &&
			(row.Status == string(DiagnosisRunProvisioning) || row.Status == string(DiagnosisRunRunning) || row.Status == string(DiagnosisRunCompacting)) {
			return row, nil
		}
	}
	return db.InteractionDagDiagnosisRun{}, pgx.ErrNoRows
}

func (f *fakeDiagnosisStateQueries) GetLatestCompletedInteractionDAGDiagnosisRun(_ context.Context, arg db.GetLatestCompletedInteractionDAGDiagnosisRunParams) (db.InteractionDagDiagnosisRun, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, row := range f.runs {
		if row.ProjectID == arg.ProjectID && row.TaskID == arg.TaskID && row.Status == string(DiagnosisRunCompleted) {
			return row, nil
		}
	}
	return db.InteractionDagDiagnosisRun{}, pgx.ErrNoRows
}

func (f *fakeDiagnosisStateQueries) FailInteractionDAGDiagnosisRun(_ context.Context, arg db.FailInteractionDAGDiagnosisRunParams) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	row, ok := f.runs[arg.RunID]
	if !ok {
		return 0, pgx.ErrNoRows
	}
	if row.Status != string(DiagnosisRunProvisioning) && row.Status != string(DiagnosisRunRunning) && row.Status != string(DiagnosisRunCompacting) {
		return 0, nil
	}
	row.Status = string(DiagnosisRunFailed)
	row.LastError = arg.LastError
	f.runs[arg.RunID] = row
	return 1, nil
}

func (f *fakeDiagnosisStateQueries) GetLatestInteractionDAGDiagnosisRunForProject(_ context.Context, projectID string) (db.InteractionDagDiagnosisRun, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var latest db.InteractionDagDiagnosisRun
	found := false
	for _, row := range f.runs {
		if row.ProjectID != projectID {
			continue
		}
		if !found || row.UpdatedAt.Time.After(latest.UpdatedAt.Time) {
			latest = row
			found = true
		}
	}
	if !found {
		return db.InteractionDagDiagnosisRun{}, pgx.ErrNoRows
	}
	return latest, nil
}

func (f *fakeDiagnosisStateQueries) SetInteractionDAGDiagnosisRunSandbox(_ context.Context, arg db.SetInteractionDAGDiagnosisRunSandboxParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	row, ok := f.runs[arg.RunID]
	if !ok {
		return pgx.ErrNoRows
	}
	row.SandboxInstanceID = arg.SandboxInstanceID
	row.CapabilityTokenHash = arg.CapabilityTokenHash
	row.ExecutionMode = arg.ExecutionMode
	row.SandboxMode = arg.SandboxMode
	f.runs[arg.RunID] = row
	return nil
}

func (f *fakeDiagnosisStateQueries) SetInteractionDAGDiagnosisRunStatus(_ context.Context, arg db.SetInteractionDAGDiagnosisRunStatusParams) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	row, ok := f.runs[arg.RunID]
	if !ok {
		return 0, pgx.ErrNoRows
	}
	// CAS predicate mirrors the SQL: WHERE run_id = $1 AND status = $3.
	if row.Status != arg.Status_2 {
		return 0, nil
	}
	row.Status = arg.Status
	f.runs[arg.RunID] = row
	return 1, nil
}

func (f *fakeDiagnosisStateQueries) CompleteInteractionDAGDiagnosisRun(_ context.Context, runID string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	row, ok := f.runs[runID]
	if !ok {
		return 0, pgx.ErrNoRows
	}
	if row.Status != string(DiagnosisRunRunning) && row.Status != string(DiagnosisRunCompacting) {
		return 0, nil
	}
	for _, seg := range f.segments {
		if seg.RunID == runID && seg.Status != string(SegmentDiagnosisCompleted) {
			return 0, nil
		}
	}
	row.Status = string(DiagnosisRunCompleted)
	row.CompletedAt = pgtype.Timestamptz{Valid: true}
	f.runs[runID] = row
	return 1, nil
}

func (f *fakeDiagnosisStateQueries) CreateInteractionDAGDiagnosisSegment(_ context.Context, arg db.CreateInteractionDAGDiagnosisSegmentParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.segments[diagSegmentKey(arg.RunID, arg.SegmentID)] = db.InteractionDagDiagnosisSegment{
		RunID:      arg.RunID,
		SegmentID:  arg.SegmentID,
		Ordinal:    arg.Ordinal,
		Status:     string(SegmentDiagnosisPending),
		NextCursor: "",
	}
	return nil
}

func diagSegmentToGetRow(seg db.InteractionDagDiagnosisSegment) db.GetInteractionDAGDiagnosisSegmentRow {
	return db.GetInteractionDAGDiagnosisSegmentRow{
		RunID:                seg.RunID,
		SegmentID:            seg.SegmentID,
		Ordinal:              seg.Ordinal,
		ExpectedMessageCount: seg.ExpectedMessageCount,
		FetchedMessageCount:  seg.FetchedMessageCount,
		ExpectedRewardCount:  seg.ExpectedRewardCount,
		ExpectedRewardSeqs:   seg.ExpectedRewardSeqs,
		RewardCount:          seg.RewardCount,
		NextCursor:           seg.NextCursor,
		Status:               seg.Status,
		CreatedAt:            seg.CreatedAt,
		UpdatedAt:            seg.UpdatedAt,
		CompletedAt:          seg.CompletedAt,
	}
}

func diagSegmentToListRow(seg db.InteractionDagDiagnosisSegment) db.ListInteractionDAGDiagnosisSegmentsRow {
	return db.ListInteractionDAGDiagnosisSegmentsRow{
		RunID:                seg.RunID,
		SegmentID:            seg.SegmentID,
		Ordinal:              seg.Ordinal,
		ExpectedMessageCount: seg.ExpectedMessageCount,
		FetchedMessageCount:  seg.FetchedMessageCount,
		ExpectedRewardCount:  seg.ExpectedRewardCount,
		ExpectedRewardSeqs:   seg.ExpectedRewardSeqs,
		RewardCount:          seg.RewardCount,
		NextCursor:           seg.NextCursor,
		Status:               seg.Status,
		CreatedAt:            seg.CreatedAt,
		UpdatedAt:            seg.UpdatedAt,
		CompletedAt:          seg.CompletedAt,
	}
}

func (f *fakeDiagnosisStateQueries) GetInteractionDAGDiagnosisSegment(_ context.Context, arg db.GetInteractionDAGDiagnosisSegmentParams) (db.GetInteractionDAGDiagnosisSegmentRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	row, ok := f.segments[diagSegmentKey(arg.RunID, arg.SegmentID)]
	if !ok {
		return db.GetInteractionDAGDiagnosisSegmentRow{}, pgx.ErrNoRows
	}
	return diagSegmentToGetRow(row), nil
}

func (f *fakeDiagnosisStateQueries) ListInteractionDAGDiagnosisSegments(_ context.Context, runID string) ([]db.ListInteractionDAGDiagnosisSegmentsRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []db.ListInteractionDAGDiagnosisSegmentsRow
	for _, seg := range f.segments {
		if seg.RunID == runID {
			out = append(out, diagSegmentToListRow(seg))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Ordinal < out[j].Ordinal })
	return out, nil
}

func (f *fakeDiagnosisStateQueries) StartInteractionDAGDiagnosisSegment(_ context.Context, arg db.StartInteractionDAGDiagnosisSegmentParams) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := diagSegmentKey(arg.RunID, arg.SegmentID)
	row, ok := f.segments[key]
	if !ok {
		return 0, pgx.ErrNoRows
	}
	if row.Status != string(SegmentDiagnosisPending) {
		return 0, nil
	}
	row.Status = string(SegmentDiagnosisInProgress)
	row.ExpectedMessageCount = arg.ExpectedMessageCount
	row.ExpectedRewardCount = arg.ExpectedRewardCount
	row.ExpectedRewardSeqs = arg.ExpectedRewardSeqs
	f.segments[key] = row
	return 1, nil
}

func (f *fakeDiagnosisStateQueries) AdvanceInteractionDAGDiagnosisSegmentFetch(_ context.Context, arg db.AdvanceInteractionDAGDiagnosisSegmentFetchParams) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := diagSegmentKey(arg.RunID, arg.SegmentID)
	row, ok := f.segments[key]
	if !ok {
		return 0, pgx.ErrNoRows
	}
	// CAS predicate: only the current cursor holder may advance, and fetched
	// counts only move forward. Field names follow sqlc's $-position naming
	// (see AdvanceInteractionDAGDiagnosisSegmentFetch's production call site):
	// arg.NextCursor is the CAS check against the currently-held cursor,
	// arg.NextCursor_2 is the new value being written.
	if row.Status != string(SegmentDiagnosisInProgress) || row.NextCursor != arg.NextCursor ||
		arg.FetchedMessageCount < row.FetchedMessageCount {
		return 0, nil
	}
	row.NextCursor = arg.NextCursor_2
	row.FetchedMessageCount = arg.FetchedMessageCount
	f.segments[key] = row
	return 1, nil
}

func (f *fakeDiagnosisStateQueries) SetInteractionDAGDiagnosisSegmentRewardCount(_ context.Context, arg db.SetInteractionDAGDiagnosisSegmentRewardCountParams) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := diagSegmentKey(arg.RunID, arg.SegmentID)
	row, ok := f.segments[key]
	if !ok {
		return 0, pgx.ErrNoRows
	}
	if arg.RewardCount < row.RewardCount {
		return 0, nil
	}
	row.RewardCount = arg.RewardCount
	f.segments[key] = row
	return 1, nil
}

func (f *fakeDiagnosisStateQueries) CompleteInteractionDAGDiagnosisSegment(_ context.Context, arg db.CompleteInteractionDAGDiagnosisSegmentParams) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := diagSegmentKey(arg.RunID, arg.SegmentID)
	row, ok := f.segments[key]
	if !ok {
		return 0, pgx.ErrNoRows
	}
	if row.Status != string(SegmentDiagnosisInProgress) ||
		row.FetchedMessageCount < row.ExpectedMessageCount ||
		row.RewardCount < row.ExpectedRewardCount {
		return 0, nil
	}
	row.Status = string(SegmentDiagnosisCompleted)
	row.CompletedAt = pgtype.Timestamptz{Valid: true}
	f.segments[key] = row
	return 1, nil
}

// Compile-time interface compliance check.
var _ diagnosisStateQueries = (*fakeDiagnosisStateQueries)(nil)

func newTestDiagnosisStore(t *testing.T) (*DiagnosisStateStore, *fakeDiagnosisStateQueries) {
	t.Helper()
	fake := newFakeDiagnosisStateQueries()
	return NewDiagnosisStateStore(fake), fake
}

func createTestDiagnosisRun(t *testing.T, store *DiagnosisStateStore, runID string, segmentIDs ...string) DiagnosisRunCheckpoint {
	t.Helper()
	ckpt, err := store.CreateRun(context.Background(), DiagnosisRunCheckpoint{
		RunID:             runID,
		ProjectID:         "project-1",
		TaskID:            "task-1",
		TopologyHash:      "topo-hash-1",
		OrderedSegmentIDs: segmentIDs,
	})
	require.NoError(t, err)
	return ckpt
}

func TestDiagnosisStateCreateRun_SnapshotsScopeAndSegments(t *testing.T) {
	store, _ := newTestDiagnosisStore(t)
	ckpt := createTestDiagnosisRun(t, store, "run-1", "seg-a", "seg-b", "seg-c")

	assert.Equal(t, "run-1", ckpt.RunID)
	assert.Equal(t, "project-1", ckpt.ProjectID)
	assert.Equal(t, "task-1", ckpt.TaskID)
	assert.Equal(t, DiagnosisRunRunning, ckpt.Status)
	assert.Equal(t, []string{"seg-a", "seg-b", "seg-c"}, ckpt.OrderedSegmentIDs)

	segments, err := store.ListSegments(context.Background(), "run-1")
	require.NoError(t, err)
	require.Len(t, segments, 3)
	for i, seg := range segments {
		assert.Equal(t, i, seg.Ordinal)
		assert.Equal(t, SegmentDiagnosisPending, seg.Status)
	}
}

func TestDiagnosisStateCreateRun_RejectsDuplicateRun(t *testing.T) {
	store, _ := newTestDiagnosisStore(t)
	createTestDiagnosisRun(t, store, "run-1", "seg-a")
	_, err := store.CreateRun(context.Background(), DiagnosisRunCheckpoint{
		RunID:             "run-1",
		ProjectID:         "project-1",
		TaskID:            "task-1",
		TopologyHash:      "topo-hash-1",
		OrderedSegmentIDs: []string{"seg-a"},
	})
	require.ErrorIs(t, err, ErrDiagnosisInvalidTransition)
}

func TestDiagnosisStateStartSegment_IdempotentAndRecordsExpectedCounts(t *testing.T) {
	store, _ := newTestDiagnosisStore(t)
	createTestDiagnosisRun(t, store, "run-1", "seg-a")

	seg, err := store.StartSegment(context.Background(), "run-1", "seg-a", 42, 7)
	require.NoError(t, err)
	assert.Equal(t, SegmentDiagnosisInProgress, seg.Status)
	assert.Equal(t, 42, seg.ExpectedMessageCount)
	assert.Equal(t, 7, seg.ExpectedRewardCount)

	// Replaying the identical start is a no-op.
	seg, err = store.StartSegment(context.Background(), "run-1", "seg-a", 42, 7)
	require.NoError(t, err)
	assert.Equal(t, SegmentDiagnosisInProgress, seg.Status)

	// A conflicting replay fails rather than silently rewriting expectations.
	_, err = store.StartSegment(context.Background(), "run-1", "seg-a", 43, 7)
	require.ErrorIs(t, err, ErrDiagnosisInvalidTransition)
}

func TestDiagnosisStateRecordSegmentPage_RequiresCurrentCursorAndMonotonicCounts(t *testing.T) {
	store, _ := newTestDiagnosisStore(t)
	createTestDiagnosisRun(t, store, "run-1", "seg-a")
	_, err := store.StartSegment(context.Background(), "run-1", "seg-a", 30, 3)
	require.NoError(t, err)

	// Stale cursor is rejected.
	err = store.RecordSegmentPage(context.Background(), "run-1", "seg-a", "cursor-x", "cursor-1", 10)
	require.ErrorIs(t, err, ErrDiagnosisStaleCursor)

	// Current cursor (empty first page) advances.
	require.NoError(t, store.RecordSegmentPage(context.Background(), "run-1", "seg-a", "", "cursor-1", 10))
	require.NoError(t, store.RecordSegmentPage(context.Background(), "run-1", "seg-a", "cursor-1", "cursor-2", 20))

	// Replaying a previous cursor is stale now.
	err = store.RecordSegmentPage(context.Background(), "run-1", "seg-a", "cursor-1", "cursor-3", 25)
	require.ErrorIs(t, err, ErrDiagnosisStaleCursor)

	// Non-monotonic fetched count is rejected.
	err = store.RecordSegmentPage(context.Background(), "run-1", "seg-a", "cursor-2", "cursor-3", 5)
	require.ErrorIs(t, err, ErrDiagnosisInvalidTransition)
}

func TestDiagnosisStateCompleteSegment_RequiresMessageAndRewardCoverage(t *testing.T) {
	store, _ := newTestDiagnosisStore(t)
	createTestDiagnosisRun(t, store, "run-1", "seg-a")
	_, err := store.StartSegment(context.Background(), "run-1", "seg-a", 20, 2)
	require.NoError(t, err)

	// Neither coverage complete.
	err = store.CompleteSegment(context.Background(), "run-1", "seg-a")
	require.ErrorIs(t, err, ErrDiagnosisIncompleteMessages)

	// Messages done, rewards missing.
	require.NoError(t, store.RecordSegmentPage(context.Background(), "run-1", "seg-a", "", "cursor-1", 20))
	err = store.CompleteSegment(context.Background(), "run-1", "seg-a")
	require.ErrorIs(t, err, ErrDiagnosisIncompleteRewards)

	// Both covered -> completes.
	require.NoError(t, store.RecordSegmentRewards(context.Background(), "run-1", "seg-a", 2))
	require.NoError(t, store.CompleteSegment(context.Background(), "run-1", "seg-a"))

	seg, err := store.GetSegment(context.Background(), "run-1", "seg-a")
	require.NoError(t, err)
	assert.Equal(t, SegmentDiagnosisCompleted, seg.Status)
}

func TestDiagnosisStateCompleteSegment_Idempotent(t *testing.T) {
	store, _ := newTestDiagnosisStore(t)
	createTestDiagnosisRun(t, store, "run-1", "seg-a")
	_, err := store.StartSegment(context.Background(), "run-1", "seg-a", 1, 1)
	require.NoError(t, err)
	require.NoError(t, store.RecordSegmentPage(context.Background(), "run-1", "seg-a", "", "done", 1))
	require.NoError(t, store.RecordSegmentRewards(context.Background(), "run-1", "seg-a", 1))
	require.NoError(t, store.CompleteSegment(context.Background(), "run-1", "seg-a"))
	// Completing again is a no-op, not an error.
	require.NoError(t, store.CompleteSegment(context.Background(), "run-1", "seg-a"))
}

func TestDiagnosisStateLoadResumableRun_ReturnsFirstIncompleteSegmentAndCursor(t *testing.T) {
	store, _ := newTestDiagnosisStore(t)
	createTestDiagnosisRun(t, store, "run-1", "seg-a", "seg-b", "seg-c")

	// Complete seg-a fully.
	_, err := store.StartSegment(context.Background(), "run-1", "seg-a", 1, 1)
	require.NoError(t, err)
	require.NoError(t, store.RecordSegmentPage(context.Background(), "run-1", "seg-a", "", "done", 1))
	require.NoError(t, store.RecordSegmentRewards(context.Background(), "run-1", "seg-a", 1))
	require.NoError(t, store.CompleteSegment(context.Background(), "run-1", "seg-a"))

	// Leave seg-b mid-flight with a cursor.
	_, err = store.StartSegment(context.Background(), "run-1", "seg-b", 40, 4)
	require.NoError(t, err)
	require.NoError(t, store.RecordSegmentPage(context.Background(), "run-1", "seg-b", "", "cursor-b1", 15))

	run, segments, err := store.LoadResumableRun(context.Background(), "project-1", "task-1")
	require.NoError(t, err)
	assert.Equal(t, "run-1", run.RunID)
	require.Len(t, segments, 3)

	var firstIncomplete *SegmentDiagnosisCheckpoint
	for i := range segments {
		if segments[i].Status != SegmentDiagnosisCompleted {
			firstIncomplete = &segments[i]
			break
		}
	}
	require.NotNil(t, firstIncomplete)
	assert.Equal(t, "seg-b", firstIncomplete.SegmentID)
	assert.Equal(t, "cursor-b1", firstIncomplete.NextCursor)
	assert.Equal(t, 15, firstIncomplete.FetchedMessageCount)
}

func TestDiagnosisStateLoadResumableRun_NotFound(t *testing.T) {
	store, _ := newTestDiagnosisStore(t)
	_, _, err := store.LoadResumableRun(context.Background(), "project-x", "task-x")
	require.ErrorIs(t, err, ErrDiagnosisRunNotFound)
}

func TestDiagnosisStateLoadCompletedRun_ReturnsPersistedReport(t *testing.T) {
	store, _ := newTestDiagnosisStore(t)
	createTestDiagnosisRun(t, store, "run-1", "seg-a")
	_, err := store.StartSegment(context.Background(), "run-1", "seg-a", 0, 0)
	require.NoError(t, err)
	require.NoError(t, store.CompleteSegment(context.Background(), "run-1", "seg-a"))
	require.NoError(t, store.CompleteRun(context.Background(), "run-1", "topo-hash-1"))

	run, segments, err := store.LoadCompletedRun(context.Background(), "project-1", "task-1")
	require.NoError(t, err)
	assert.Equal(t, "run-1", run.RunID)
	assert.Equal(t, DiagnosisRunCompleted, run.Status)
	require.Len(t, segments, 1)
	assert.Equal(t, SegmentDiagnosisCompleted, segments[0].Status)
}

func TestDiagnosisStateCompleteRun_RequiresAllSegmentsComplete(t *testing.T) {
	store, _ := newTestDiagnosisStore(t)
	createTestDiagnosisRun(t, store, "run-1", "seg-a", "seg-b")

	err := store.CompleteRun(context.Background(), "run-1", "topo-hash-1")
	require.ErrorIs(t, err, ErrDiagnosisInvalidTransition)

	// A topology mismatch never completes, even when coverage is done.
	_, err = store.StartSegment(context.Background(), "run-1", "seg-a", 0, 0)
	require.NoError(t, err)
	require.NoError(t, store.CompleteSegment(context.Background(), "run-1", "seg-a"))
	_, err = store.StartSegment(context.Background(), "run-1", "seg-b", 0, 0)
	require.NoError(t, err)
	require.NoError(t, store.CompleteSegment(context.Background(), "run-1", "seg-b"))

	err = store.CompleteRun(context.Background(), "run-1", "topo-hash-WRONG")
	require.ErrorIs(t, err, ErrDiagnosisTopologyMismatch)

	require.NoError(t, store.CompleteRun(context.Background(), "run-1", "topo-hash-1"))

	run, err := store.GetRun(context.Background(), "run-1")
	require.NoError(t, err)
	assert.Equal(t, DiagnosisRunCompleted, run.Status)
}

func TestCompleteDiagnosisRun_PropagatesIncompleteCoverage(t *testing.T) {
	store, _ := newTestDiagnosisStore(t)
	createTestDiagnosisRun(t, store, "run-1", "seg-a")

	err := completeDiagnosisRun(context.Background(), store, "run-1", "topo-hash-1")
	require.ErrorIs(t, err, ErrDiagnosisInvalidTransition)
}

func TestDiagnosisStateFailRun_MarksFailedWithBoundedError(t *testing.T) {
	store, _ := newTestDiagnosisStore(t)
	createTestDiagnosisRun(t, store, "run-1", "seg-a")

	huge := errors.New(strings.Repeat("x", 8*1024))
	require.NoError(t, store.FailRun(context.Background(), "run-1", huge))

	run, err := store.GetRun(context.Background(), "run-1")
	require.NoError(t, err)
	assert.Equal(t, DiagnosisRunFailed, run.Status)
	assert.LessOrEqual(t, len(run.LastError), maxDiagnosisRunErrorBytes)
	assert.NotEmpty(t, run.LastError)

	// A failed run is no longer resumable.
	_, _, err = store.LoadResumableRun(context.Background(), "project-1", "task-1")
	require.ErrorIs(t, err, ErrDiagnosisRunNotFound)
}

func TestDiagnosisStateSetRunSandbox_PersistsFields(t *testing.T) {
	store, _ := newTestDiagnosisStore(t)
	createTestDiagnosisRun(t, store, "run-1", "seg-a")

	require.NoError(t, store.SetRunSandbox(context.Background(), "run-1", "sbx-123", "hash-abc", "sandbox", DiagnosisSandboxModeDedicated))

	run, err := store.GetRun(context.Background(), "run-1")
	require.NoError(t, err)
	assert.Equal(t, "sbx-123", run.SandboxInstanceID)
	assert.Equal(t, "hash-abc", run.CapabilityTokenHash)
	assert.Equal(t, "sandbox", run.ExecutionMode)

	// Server-mode runs keep the fields empty.
	createTestDiagnosisRun(t, store, "run-2", "seg-a")
	run, err = store.GetRun(context.Background(), "run-2")
	require.NoError(t, err)
	assert.Empty(t, run.SandboxInstanceID)
	assert.Empty(t, run.CapabilityTokenHash)
	assert.Empty(t, run.ExecutionMode)
}

func TestDiagnosisRunFromRowPreservesSandboxMode(t *testing.T) {
	row := db.InteractionDagDiagnosisRun{
		RunID:             "run-shared",
		SandboxInstanceID: pgtype.Text{String: "sandbox-1", Valid: true},
		ExecutionMode:     pgtype.Text{String: DiagnosisExecutionModeSandbox, Valid: true},
		SandboxMode:       pgtype.Text{String: DiagnosisSandboxModeShared, Valid: true},
	}

	got, err := diagnosisRunFromRow(row)
	require.NoError(t, err)
	assert.Equal(t, DiagnosisSandboxModeShared, got.SandboxMode)
}

func TestDiagnosisStateSetRunSandbox_PersistsSandboxMode(t *testing.T) {
	store, _ := newTestDiagnosisStore(t)
	createTestDiagnosisRun(t, store, "run-shared", "seg-a")

	require.NoError(t, store.SetRunSandbox(
		context.Background(),
		"run-shared",
		"sandbox-1",
		"hash-1",
		DiagnosisExecutionModeSandbox,
		DiagnosisSandboxModeShared,
	))

	run, err := store.GetRun(context.Background(), "run-shared")
	require.NoError(t, err)
	assert.Equal(t, DiagnosisSandboxModeShared, run.SandboxMode)
}

func TestDiagnosisStateSetRunSandbox_UnknownRun(t *testing.T) {
	store, _ := newTestDiagnosisStore(t)
	err := store.SetRunSandbox(context.Background(), "run-unknown", "sbx", "hash", "sandbox", DiagnosisSandboxModeDedicated)
	require.ErrorIs(t, err, ErrDiagnosisRunNotFound)
}

func TestDiagnosisStateProvisioningLifecycle(t *testing.T) {
	store, _ := newTestDiagnosisStore(t)
	createTestDiagnosisRun(t, store, "run-1", "seg-a")

	// Activating a running (non-provisioning) run is a no-op-safe reject...
	require.NoError(t, store.MarkRunProvisioning(context.Background(), "run-1"))
	run, err := store.GetRun(context.Background(), "run-1")
	require.NoError(t, err)
	assert.Equal(t, DiagnosisRunProvisioning, run.Status)

	// ...a provisioning run is resumable so a crashed provisioner can recover.
	resumed, _, err := store.LoadResumableRun(context.Background(), "project-1", "task-1")
	require.NoError(t, err)
	assert.Equal(t, "run-1", resumed.RunID)

	// Marking provisioning twice is idempotent.
	require.NoError(t, store.MarkRunProvisioning(context.Background(), "run-1"))

	require.NoError(t, store.ActivateRun(context.Background(), "run-1"))
	run, err = store.GetRun(context.Background(), "run-1")
	require.NoError(t, err)
	assert.Equal(t, DiagnosisRunRunning, run.Status)

	// Activating from running is a no-op; a terminal run cannot transition.
	require.NoError(t, store.ActivateRun(context.Background(), "run-1"))
	require.NoError(t, store.FailRun(context.Background(), "run-1", errors.New("boom")))
	require.ErrorIs(t, store.MarkRunProvisioning(context.Background(), "run-1"), ErrDiagnosisInvalidTransition)
	require.ErrorIs(t, store.ActivateRun(context.Background(), "run-1"), ErrDiagnosisInvalidTransition)
}

func TestDiagnosisStateClaimRunProvisioningOnlyOneCallerWins(t *testing.T) {
	store, _ := newTestDiagnosisStore(t)
	createTestDiagnosisRun(t, store, "run-claim", "seg-a")

	claimed, err := store.ClaimRunProvisioning(context.Background(), "run-claim")
	require.NoError(t, err)
	assert.True(t, claimed)

	claimed, err = store.ClaimRunProvisioning(context.Background(), "run-claim")
	require.NoError(t, err)
	assert.False(t, claimed, "an already-provisioning run must not launch a second worker")
}

func TestDiagnosisStateLoadLatestRunForProject(t *testing.T) {
	store, fake := newTestDiagnosisStore(t)
	createTestDiagnosisRun(t, store, "run-1", "seg-a")

	_, err := store.LoadLatestRunForProject(context.Background(), "project-unknown")
	require.ErrorIs(t, err, ErrDiagnosisRunNotFound)

	run, err := store.LoadLatestRunForProject(context.Background(), "project-1")
	require.NoError(t, err)
	assert.Equal(t, "run-1", run.RunID)

	// Latest is chosen by updated_at regardless of status.
	older := fake.runs["run-1"]
	older.UpdatedAt = pgtype.Timestamptz{Valid: true}
	fake.runs["run-1"] = older
	createTestDiagnosisRun(t, store, "run-2", "seg-b")
	newer := fake.runs["run-2"]
	newer.UpdatedAt = pgtype.Timestamptz{Valid: true}
	newer.UpdatedAt.Time = older.UpdatedAt.Time.Add(time.Second)
	fake.runs["run-2"] = newer

	run, err = store.LoadLatestRunForProject(context.Background(), "project-1")
	require.NoError(t, err)
	assert.Equal(t, "run-2", run.RunID)
}
