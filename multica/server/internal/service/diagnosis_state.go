// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// maxDiagnosisRunErrorBytes bounds the persisted last_error so a pathological
// failure cannot write unbounded text into the run row.
const maxDiagnosisRunErrorBytes = 1024

// DiagnosisRunStatus is the lifecycle state of one diagnosis run.
type DiagnosisRunStatus string

const (
	DiagnosisRunRunning    DiagnosisRunStatus = "running"
	DiagnosisRunCompacting DiagnosisRunStatus = "compacting"
	DiagnosisRunCompleted  DiagnosisRunStatus = "completed"
	DiagnosisRunFailed     DiagnosisRunStatus = "failed"
)

// SegmentDiagnosisStatus is the lifecycle state of one segment checkpoint.
type SegmentDiagnosisStatus string

const (
	SegmentDiagnosisPending    SegmentDiagnosisStatus = "pending"
	SegmentDiagnosisInProgress SegmentDiagnosisStatus = "in_progress"
	SegmentDiagnosisCompleted  SegmentDiagnosisStatus = "completed"
)

// Typed errors returned by DiagnosisStateStore transitions. Callers match with
// errors.Is; the wrapped message carries the offending IDs.
var (
	ErrDiagnosisRunNotFound        = errors.New("diagnosis run not found")
	ErrDiagnosisStaleCursor        = errors.New("diagnosis segment cursor is stale")
	ErrDiagnosisIncompleteMessages = errors.New("diagnosis segment message coverage incomplete")
	ErrDiagnosisIncompleteRewards  = errors.New("diagnosis segment reward coverage incomplete")
	ErrDiagnosisTopologyMismatch   = errors.New("diagnosis topology hash mismatch")
	ErrDiagnosisInvalidTransition  = errors.New("diagnosis invalid state transition")
)

// DiagnosisRunCheckpoint is the service-boundary view of one run row.
type DiagnosisRunCheckpoint struct {
	RunID                 string
	ProjectID             string
	TaskID                string
	TopologyHash          string
	OrderedSegmentIDs     []string
	CurrentSegmentOrdinal int
	Status                DiagnosisRunStatus
	PiSessionID           string
	LastError             string
}

// SegmentDiagnosisCheckpoint is the service-boundary view of one segment row.
type SegmentDiagnosisCheckpoint struct {
	RunID                string
	SegmentID            string
	Ordinal              int
	ExpectedMessageCount int
	FetchedMessageCount  int
	ExpectedRewardCount  int
	ExpectedRewardSeqs   []int32
	RewardCount          int
	NextCursor           string
	Status               SegmentDiagnosisStatus
}

// diagnosisStateQueries is the narrow generated-query surface the store needs.
// *db.Queries satisfies it in production; tests substitute an in-memory fake
// that honors the same compare-and-set predicates.
type diagnosisStateQueries interface {
	CreateInteractionDAGDiagnosisRun(ctx context.Context, arg db.CreateInteractionDAGDiagnosisRunParams) error
	GetInteractionDAGDiagnosisRun(ctx context.Context, runID string) (db.InteractionDagDiagnosisRun, error)
	GetResumableInteractionDAGDiagnosisRun(ctx context.Context, arg db.GetResumableInteractionDAGDiagnosisRunParams) (db.InteractionDagDiagnosisRun, error)
	GetLatestCompletedInteractionDAGDiagnosisRun(ctx context.Context, arg db.GetLatestCompletedInteractionDAGDiagnosisRunParams) (db.InteractionDagDiagnosisRun, error)
	FailInteractionDAGDiagnosisRun(ctx context.Context, arg db.FailInteractionDAGDiagnosisRunParams) (int64, error)
	CompleteInteractionDAGDiagnosisRun(ctx context.Context, runID string) (int64, error)
	CreateInteractionDAGDiagnosisSegment(ctx context.Context, arg db.CreateInteractionDAGDiagnosisSegmentParams) error
	GetInteractionDAGDiagnosisSegment(ctx context.Context, arg db.GetInteractionDAGDiagnosisSegmentParams) (db.GetInteractionDAGDiagnosisSegmentRow, error)
	ListInteractionDAGDiagnosisSegments(ctx context.Context, runID string) ([]db.ListInteractionDAGDiagnosisSegmentsRow, error)
	StartInteractionDAGDiagnosisSegment(ctx context.Context, arg db.StartInteractionDAGDiagnosisSegmentParams) (int64, error)
	AdvanceInteractionDAGDiagnosisSegmentFetch(ctx context.Context, arg db.AdvanceInteractionDAGDiagnosisSegmentFetchParams) (int64, error)
	SetInteractionDAGDiagnosisSegmentRewardCount(ctx context.Context, arg db.SetInteractionDAGDiagnosisSegmentRewardCountParams) (int64, error)
	CompleteInteractionDAGDiagnosisSegment(ctx context.Context, arg db.CompleteInteractionDAGDiagnosisSegmentParams) (int64, error)
}

// Compile-time check that the generated queries satisfy the store surface.
var _ diagnosisStateQueries = (*db.Queries)(nil)

// DiagnosisStateStore persists resumable diagnosis progress (migration 208).
// All mutating transitions go through compare-and-set queries so a crashed
// runner resuming mid-flight cannot double-advance a cursor or mark incomplete
// coverage complete; the database remains the authoritative progress record.
type DiagnosisStateStore struct {
	q diagnosisStateQueries
}

// NewDiagnosisStateStore constructs the store over the generated queries.
func NewDiagnosisStateStore(q diagnosisStateQueries) *DiagnosisStateStore {
	return &DiagnosisStateStore{q: q}
}

func diagnosisRunFromRow(row db.InteractionDagDiagnosisRun) (DiagnosisRunCheckpoint, error) {
	var segmentIDs []string
	if len(row.OrderedSegmentIds) > 0 {
		if err := json.Unmarshal(row.OrderedSegmentIds, &segmentIDs); err != nil {
			return DiagnosisRunCheckpoint{}, fmt.Errorf("decode ordered segment ids for run %s: %w", row.RunID, err)
		}
	}
	return DiagnosisRunCheckpoint{
		RunID:                 row.RunID,
		ProjectID:             row.ProjectID,
		TaskID:                row.TaskID,
		TopologyHash:          row.TopologyHash,
		OrderedSegmentIDs:     segmentIDs,
		CurrentSegmentOrdinal: int(row.CurrentSegmentOrdinal),
		Status:                DiagnosisRunStatus(row.Status),
		PiSessionID:           row.PiSessionID,
		LastError:             row.LastError,
	}, nil
}

func diagnosisSegmentFromRow(row db.InteractionDagDiagnosisSegment) SegmentDiagnosisCheckpoint {
	var expectedRewardSeqs []int32
	if len(row.ExpectedRewardSeqs) > 0 {
		_ = json.Unmarshal(row.ExpectedRewardSeqs, &expectedRewardSeqs)
	}
	return SegmentDiagnosisCheckpoint{
		RunID:                row.RunID,
		SegmentID:            row.SegmentID,
		Ordinal:              int(row.Ordinal),
		ExpectedMessageCount: int(row.ExpectedMessageCount),
		FetchedMessageCount:  int(row.FetchedMessageCount),
		ExpectedRewardCount:  int(row.ExpectedRewardCount),
		ExpectedRewardSeqs:   expectedRewardSeqs,
		RewardCount:          int(row.RewardCount),
		NextCursor:           row.NextCursor,
		Status:               SegmentDiagnosisStatus(row.Status),
	}
}

// CreateRun snapshots a new run with status running plus one pending segment
// checkpoint per ordered segment ID. RunID must be unique; a duplicate is an
// invalid transition rather than an overwrite.
func (s *DiagnosisStateStore) CreateRun(ctx context.Context, ckpt DiagnosisRunCheckpoint) (DiagnosisRunCheckpoint, error) {
	if strings.TrimSpace(ckpt.RunID) == "" || strings.TrimSpace(ckpt.ProjectID) == "" ||
		strings.TrimSpace(ckpt.TaskID) == "" || strings.TrimSpace(ckpt.TopologyHash) == "" {
		return DiagnosisRunCheckpoint{}, fmt.Errorf("%w: run_id, project_id, task_id and topology_hash are required", ErrDiagnosisInvalidTransition)
	}
	if len(ckpt.OrderedSegmentIDs) == 0 {
		return DiagnosisRunCheckpoint{}, fmt.Errorf("%w: run must snapshot at least one segment", ErrDiagnosisInvalidTransition)
	}
	seen := make(map[string]struct{}, len(ckpt.OrderedSegmentIDs))
	for _, id := range ckpt.OrderedSegmentIDs {
		if strings.TrimSpace(id) == "" {
			return DiagnosisRunCheckpoint{}, fmt.Errorf("%w: segment id must be non-empty", ErrDiagnosisInvalidTransition)
		}
		if _, dup := seen[id]; dup {
			return DiagnosisRunCheckpoint{}, fmt.Errorf("%w: duplicate segment id %q", ErrDiagnosisInvalidTransition, id)
		}
		seen[id] = struct{}{}
	}
	if _, err := s.q.GetInteractionDAGDiagnosisRun(ctx, ckpt.RunID); err == nil {
		return DiagnosisRunCheckpoint{}, fmt.Errorf("%w: run %s already exists", ErrDiagnosisInvalidTransition, ckpt.RunID)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return DiagnosisRunCheckpoint{}, err
	}

	encoded, err := json.Marshal(ckpt.OrderedSegmentIDs)
	if err != nil {
		return DiagnosisRunCheckpoint{}, fmt.Errorf("encode ordered segment ids: %w", err)
	}
	if err := s.q.CreateInteractionDAGDiagnosisRun(ctx, db.CreateInteractionDAGDiagnosisRunParams{
		RunID:             ckpt.RunID,
		ProjectID:         ckpt.ProjectID,
		TaskID:            ckpt.TaskID,
		TopologyHash:      ckpt.TopologyHash,
		OrderedSegmentIds: encoded,
	}); err != nil {
		return DiagnosisRunCheckpoint{}, err
	}
	for i, segmentID := range ckpt.OrderedSegmentIDs {
		if err := s.q.CreateInteractionDAGDiagnosisSegment(ctx, db.CreateInteractionDAGDiagnosisSegmentParams{
			RunID:     ckpt.RunID,
			SegmentID: segmentID,
			Ordinal:   int32(i),
		}); err != nil {
			return DiagnosisRunCheckpoint{}, err
		}
	}
	created := ckpt
	created.Status = DiagnosisRunRunning
	created.CurrentSegmentOrdinal = 0
	return created, nil
}

// GetRun loads one run checkpoint by ID.
func (s *DiagnosisStateStore) GetRun(ctx context.Context, runID string) (DiagnosisRunCheckpoint, error) {
	row, err := s.q.GetInteractionDAGDiagnosisRun(ctx, runID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return DiagnosisRunCheckpoint{}, fmt.Errorf("%w: %s", ErrDiagnosisRunNotFound, runID)
		}
		return DiagnosisRunCheckpoint{}, err
	}
	return diagnosisRunFromRow(row)
}

// GetSegment loads one segment checkpoint.
func (s *DiagnosisStateStore) GetSegment(ctx context.Context, runID, segmentID string) (SegmentDiagnosisCheckpoint, error) {
	row, err := s.q.GetInteractionDAGDiagnosisSegment(ctx, db.GetInteractionDAGDiagnosisSegmentParams{RunID: runID, SegmentID: segmentID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return SegmentDiagnosisCheckpoint{}, fmt.Errorf("%w: segment %s for run %s", ErrDiagnosisRunNotFound, segmentID, runID)
		}
		return SegmentDiagnosisCheckpoint{}, err
	}
	return diagnosisSegmentFromRow(db.InteractionDagDiagnosisSegment{
		RunID:                row.RunID,
		SegmentID:            row.SegmentID,
		Ordinal:              row.Ordinal,
		ExpectedMessageCount: row.ExpectedMessageCount,
		FetchedMessageCount:  row.FetchedMessageCount,
		ExpectedRewardCount:  row.ExpectedRewardCount,
		ExpectedRewardSeqs:   row.ExpectedRewardSeqs,
		RewardCount:          row.RewardCount,
		NextCursor:           row.NextCursor,
		Status:               row.Status,
		CreatedAt:            row.CreatedAt,
		UpdatedAt:            row.UpdatedAt,
		CompletedAt:          row.CompletedAt,
	}), nil
}

// ListSegments returns all segment checkpoints for a run in ordinal order.
func (s *DiagnosisStateStore) ListSegments(ctx context.Context, runID string) ([]SegmentDiagnosisCheckpoint, error) {
	rows, err := s.q.ListInteractionDAGDiagnosisSegments(ctx, runID)
	if err != nil {
		return nil, err
	}
	out := make([]SegmentDiagnosisCheckpoint, 0, len(rows))
	for _, row := range rows {
		out = append(out, diagnosisSegmentFromRow(db.InteractionDagDiagnosisSegment{
			RunID:                row.RunID,
			SegmentID:            row.SegmentID,
			Ordinal:              row.Ordinal,
			ExpectedMessageCount: row.ExpectedMessageCount,
			FetchedMessageCount:  row.FetchedMessageCount,
			ExpectedRewardCount:  row.ExpectedRewardCount,
			ExpectedRewardSeqs:   row.ExpectedRewardSeqs,
			RewardCount:          row.RewardCount,
			NextCursor:           row.NextCursor,
			Status:               row.Status,
			CreatedAt:            row.CreatedAt,
			UpdatedAt:            row.UpdatedAt,
			CompletedAt:          row.CompletedAt,
		}))
	}
	return out, nil
}

// StartSegment transitions a pending segment to in_progress and records the
// expected message/reward coverage. It is idempotent: replaying the identical
// expectations returns the current checkpoint; replaying different ones is an
// invalid transition.
func (s *DiagnosisStateStore) StartSegment(ctx context.Context, runID, segmentID string, expectedMessages, expectedRewards int) (SegmentDiagnosisCheckpoint, error) {
	if expectedRewards < 0 {
		return SegmentDiagnosisCheckpoint{}, fmt.Errorf("%w: expected counts must be non-negative", ErrDiagnosisInvalidTransition)
	}
	seqs := make([]int32, expectedRewards)
	for i := range seqs {
		seqs[i] = int32(i + 1)
	}
	return s.StartSegmentWithTargets(ctx, runID, segmentID, expectedMessages, seqs)
}

// StartSegmentWithTargets freezes the exact assistant-message sequence numbers
// whose rewards are required. This is the production entry point: assistant
// message sequences may be sparse and cannot safely be inferred from a count.
func (s *DiagnosisStateStore) StartSegmentWithTargets(ctx context.Context, runID, segmentID string, expectedMessages int, assistantSeqs []int32) (SegmentDiagnosisCheckpoint, error) {
	if expectedMessages < 0 {
		return SegmentDiagnosisCheckpoint{}, fmt.Errorf("%w: expected message count must be non-negative", ErrDiagnosisInvalidTransition)
	}
	seqs := append([]int32(nil), assistantSeqs...)
	seen := make(map[int32]struct{}, len(seqs))
	for _, seq := range seqs {
		if seq < 1 {
			return SegmentDiagnosisCheckpoint{}, fmt.Errorf("%w: assistant sequence must be positive", ErrDiagnosisInvalidTransition)
		}
		if _, duplicate := seen[seq]; duplicate {
			return SegmentDiagnosisCheckpoint{}, fmt.Errorf("%w: duplicate assistant sequence %d", ErrDiagnosisInvalidTransition, seq)
		}
		seen[seq] = struct{}{}
	}
	encodedSeqs, err := json.Marshal(seqs)
	if err != nil {
		return SegmentDiagnosisCheckpoint{}, fmt.Errorf("encode expected reward sequences: %w", err)
	}
	applied, err := s.q.StartInteractionDAGDiagnosisSegment(ctx, db.StartInteractionDAGDiagnosisSegmentParams{
		RunID:                runID,
		SegmentID:            segmentID,
		ExpectedMessageCount: int32(expectedMessages),
		ExpectedRewardCount:  int32(len(seqs)),
		ExpectedRewardSeqs:   encodedSeqs,
	})
	if err != nil {
		return SegmentDiagnosisCheckpoint{}, err
	}
	current, err := s.GetSegment(ctx, runID, segmentID)
	if err != nil {
		return SegmentDiagnosisCheckpoint{}, err
	}
	if applied > 0 {
		return current, nil
	}
	// Already started: identical expectations are an idempotent replay.
	if current.ExpectedMessageCount == expectedMessages && sameInt32s(current.ExpectedRewardSeqs, seqs) {
		return current, nil
	}
	return SegmentDiagnosisCheckpoint{}, fmt.Errorf("%w: segment %s already started with different expectations", ErrDiagnosisInvalidTransition, segmentID)
}

func sameInt32s(left, right []int32) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

// RecordSegmentPage persists the server-issued next cursor and the cumulative
// fetched count after a page was produced. Only the current cursor holder may
// advance (compare-and-set on prevCursor), and counts must move forward.
func (s *DiagnosisStateStore) RecordSegmentPage(ctx context.Context, runID, segmentID, prevCursor, nextCursor string, fetchedTotal int) error {
	current, err := s.GetSegment(ctx, runID, segmentID)
	if err != nil {
		return err
	}
	if current.NextCursor != prevCursor {
		return fmt.Errorf("%w: segment %s", ErrDiagnosisStaleCursor, segmentID)
	}
	if fetchedTotal < current.FetchedMessageCount {
		return fmt.Errorf("%w: fetched count regressed for segment %s", ErrDiagnosisInvalidTransition, segmentID)
	}
	applied, err := s.q.AdvanceInteractionDAGDiagnosisSegmentFetch(ctx, db.AdvanceInteractionDAGDiagnosisSegmentFetchParams{
		RunID: runID,
		SegmentID: segmentID,
		// sqlc numbers params by $-position, not by intent: $3 (WHERE
		// next_cursor = $3, the CAS check against the currently-held
		// cursor) becomes the first-seen "NextCursor" field; $4 (SET
		// next_cursor = $4, the new value) becomes "NextCursor_2". Despite
		// the field names, NextCursor here is the CAS predicate (prev) and
		// NextCursor_2 is the new value being written.
		NextCursor:          prevCursor,
		NextCursor_2:        nextCursor,
		FetchedMessageCount: int32(fetchedTotal),
	})
	if err != nil {
		return err
	}
	if applied == 0 {
		// The row changed between the read and the guarded update.
		return fmt.Errorf("%w: segment %s", ErrDiagnosisStaleCursor, segmentID)
	}
	return nil
}

// RecordSegmentRewards sets the cumulative persisted-reward count for a
// segment. The counter is monotonic; regressive writes are invalid.
func (s *DiagnosisStateStore) RecordSegmentRewards(ctx context.Context, runID, segmentID string, rewardCount int) error {
	current, err := s.GetSegment(ctx, runID, segmentID)
	if err != nil {
		return err
	}
	if rewardCount < current.RewardCount {
		return fmt.Errorf("%w: reward count regressed for segment %s", ErrDiagnosisInvalidTransition, segmentID)
	}
	applied, err := s.q.SetInteractionDAGDiagnosisSegmentRewardCount(ctx, db.SetInteractionDAGDiagnosisSegmentRewardCountParams{
		RunID:       runID,
		SegmentID:   segmentID,
		RewardCount: int32(rewardCount),
	})
	if err != nil {
		return err
	}
	if applied == 0 {
		return fmt.Errorf("%w: segment %s", ErrDiagnosisInvalidTransition, segmentID)
	}
	return nil
}

// CompleteSegment marks a segment completed once the database shows both
// message and reward coverage satisfied. Completing twice is a no-op.
func (s *DiagnosisStateStore) CompleteSegment(ctx context.Context, runID, segmentID string) error {
	current, err := s.GetSegment(ctx, runID, segmentID)
	if err != nil {
		return err
	}
	if current.Status == SegmentDiagnosisCompleted {
		return nil
	}
	if current.FetchedMessageCount < current.ExpectedMessageCount {
		return fmt.Errorf("%w: segment %s fetched %d of %d messages", ErrDiagnosisIncompleteMessages, segmentID, current.FetchedMessageCount, current.ExpectedMessageCount)
	}
	if current.RewardCount < current.ExpectedRewardCount {
		return fmt.Errorf("%w: segment %s recorded %d of %d rewards", ErrDiagnosisIncompleteRewards, segmentID, current.RewardCount, current.ExpectedRewardCount)
	}
	applied, err := s.q.CompleteInteractionDAGDiagnosisSegment(ctx, db.CompleteInteractionDAGDiagnosisSegmentParams{
		RunID:     runID,
		SegmentID: segmentID,
	})
	if err != nil {
		return err
	}
	if applied == 0 {
		return fmt.Errorf("%w: segment %s coverage changed during completion", ErrDiagnosisInvalidTransition, segmentID)
	}
	return nil
}

// LoadResumableRun returns the latest still-active run for a (project, task)
// plus its segment checkpoints in ordinal order; the first non-completed
// checkpoint carries the cursor to resume from. Returns
// ErrDiagnosisRunNotFound when no active run exists.
func (s *DiagnosisStateStore) LoadResumableRun(ctx context.Context, projectID, taskID string) (DiagnosisRunCheckpoint, []SegmentDiagnosisCheckpoint, error) {
	row, err := s.q.GetResumableInteractionDAGDiagnosisRun(ctx, db.GetResumableInteractionDAGDiagnosisRunParams{
		ProjectID: projectID,
		TaskID:    taskID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return DiagnosisRunCheckpoint{}, nil, fmt.Errorf("%w: project %s task %s", ErrDiagnosisRunNotFound, projectID, taskID)
		}
		return DiagnosisRunCheckpoint{}, nil, err
	}
	run, err := diagnosisRunFromRow(row)
	if err != nil {
		return DiagnosisRunCheckpoint{}, nil, err
	}
	segments, err := s.ListSegments(ctx, run.RunID)
	if err != nil {
		return DiagnosisRunCheckpoint{}, nil, err
	}
	return run, segments, nil
}

// LoadCompletedRun returns the newest completed run for a project/task. It is
// used by an idempotent on-demand request after no active run exists.
func (s *DiagnosisStateStore) LoadCompletedRun(ctx context.Context, projectID, taskID string) (DiagnosisRunCheckpoint, []SegmentDiagnosisCheckpoint, error) {
	row, err := s.q.GetLatestCompletedInteractionDAGDiagnosisRun(ctx, db.GetLatestCompletedInteractionDAGDiagnosisRunParams{
		ProjectID: projectID,
		TaskID:    taskID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return DiagnosisRunCheckpoint{}, nil, fmt.Errorf("%w: completed project %s task %s", ErrDiagnosisRunNotFound, projectID, taskID)
		}
		return DiagnosisRunCheckpoint{}, nil, err
	}
	run, err := diagnosisRunFromRow(row)
	if err != nil {
		return DiagnosisRunCheckpoint{}, nil, err
	}
	segments, err := s.ListSegments(ctx, run.RunID)
	if err != nil {
		return DiagnosisRunCheckpoint{}, nil, err
	}
	return run, segments, nil
}

// CompleteRun marks the run completed. It fails while any segment is
// incomplete and when the caller's topology hash does not match the snapshot.
func (s *DiagnosisStateStore) CompleteRun(ctx context.Context, runID, topologyHash string) error {
	run, err := s.GetRun(ctx, runID)
	if err != nil {
		return err
	}
	if run.TopologyHash != topologyHash {
		return fmt.Errorf("%w: run %s", ErrDiagnosisTopologyMismatch, runID)
	}
	segments, err := s.ListSegments(ctx, runID)
	if err != nil {
		return err
	}
	for _, seg := range segments {
		if seg.Status != SegmentDiagnosisCompleted {
			return fmt.Errorf("%w: segment %s is %s", ErrDiagnosisInvalidTransition, seg.SegmentID, seg.Status)
		}
	}
	applied, err := s.q.CompleteInteractionDAGDiagnosisRun(ctx, runID)
	if err != nil {
		return err
	}
	if applied == 0 {
		return fmt.Errorf("%w: run %s is not active", ErrDiagnosisInvalidTransition, runID)
	}
	return nil
}

// FailRun marks an active run failed with a bounded error string. Failing an
// already-terminal run is a no-op.
func (s *DiagnosisStateStore) FailRun(ctx context.Context, runID string, cause error) error {
	if _, err := s.GetRun(ctx, runID); err != nil {
		return err
	}
	message := "unknown failure"
	if cause != nil {
		message = cause.Error()
	}
	message = truncateUTF8Bytes(message, maxDiagnosisRunErrorBytes)
	if _, err := s.q.FailInteractionDAGDiagnosisRun(ctx, db.FailInteractionDAGDiagnosisRunParams{
		RunID:     runID,
		LastError: message,
	}); err != nil {
		return err
	}
	return nil
}
