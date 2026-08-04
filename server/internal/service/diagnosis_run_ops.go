// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"log/slog"
)

// DiagnosisRunAPIStore is the state-store surface shared by the loopback
// tool server and the network diagnosis-run API handlers.
// *DiagnosisStateStore satisfies it; tests substitute in-memory fakes.
type DiagnosisRunAPIStore interface {
	GetSegment(ctx context.Context, runID, segmentID string) (SegmentDiagnosisCheckpoint, error)
	ListSegments(ctx context.Context, runID string) ([]SegmentDiagnosisCheckpoint, error)
	RecordSegmentPage(ctx context.Context, runID, segmentID, prevCursor, nextCursor string, fetchedTotal int) error
	RecordSegmentRewards(ctx context.Context, runID, segmentID string, rewardCount int) error
	CompleteSegment(ctx context.Context, runID, segmentID string) error
	CompleteRun(ctx context.Context, runID, topologyHash string) error
}

var _ DiagnosisRunAPIStore = (*DiagnosisStateStore)(nil)

// SegmentInRun reports whether segmentID belongs to the run snapshot.
func SegmentInRun(run DiagnosisRunCheckpoint, segmentID string) bool {
	for _, id := range run.OrderedSegmentIDs {
		if id == segmentID {
			return true
		}
	}
	return false
}

// FetchDiagnosisSegmentPage produces one page for a frozen segment target and
// CAS-advances the persisted cursor. Errors: the store's not-found error when
// the segment checkpoint is missing, ErrDiagnosisStaleCursor when the
// caller's cursor is not the currently-held one, or the pager failure.
func FetchDiagnosisSegmentPage(
	ctx context.Context,
	store DiagnosisRunAPIStore,
	pager DiagnosisMessagePager,
	cursorKey []byte,
	runID string,
	target DiagnosisSegmentTarget,
	cursor string,
) (SegmentMessagePage, error) {
	if _, err := store.GetSegment(ctx, runID, target.SegmentID); err != nil {
		return SegmentMessagePage{}, err
	}
	page, err := GetSegmentMessagePageWithKey(ctx, pager, cursorKey, target.AgentRunID, target.SegmentID, target.StartSeq, target.EndSeq, cursor)
	if err != nil {
		return SegmentMessagePage{}, err
	}
	if err := store.RecordSegmentPage(ctx, runID, target.SegmentID, cursor, page.NextCursor, page.FetchedCount); err != nil {
		return SegmentMessagePage{}, err
	}
	return page, nil
}

// DiagnosisStepRewardInput is one agent-submitted step reward.
type DiagnosisStepRewardInput struct {
	Seq       int
	Score     int
	Rationale string
}

// DiagnosisStepRewardRejection explains why one entry was not persisted.
type DiagnosisStepRewardRejection struct {
	Seq    int
	Reason string
}

// DiagnosisStepRewardOutcome summarizes one record-step-rewards batch.
type DiagnosisStepRewardOutcome struct {
	PersistedSeqs []int
	MissingSeqs   []int
	Rejected      []DiagnosisStepRewardRejection
}

// RecordDiagnosisStepRewards validates and persists one batch of step rewards
// for a segment: sequences must be frozen assistant targets, scores clamp to
// >= 0, conflicting rewrites are rejected, identical replays are idempotent,
// and the segment reward counter follows the persisted total. Returns the
// store's not-found error when the segment checkpoint is missing.
func RecordDiagnosisStepRewards(
	ctx context.Context,
	store DiagnosisRunAPIStore,
	dagWriter DiagnosisDAGWriter,
	projectID, runID, segmentID string,
	entries []DiagnosisStepRewardInput,
) (DiagnosisStepRewardOutcome, error) {
	segCkpt, err := store.GetSegment(ctx, runID, segmentID)
	if err != nil {
		return DiagnosisStepRewardOutcome{}, err
	}

	var outcome DiagnosisStepRewardOutcome
	for _, entry := range entries {
		if entry.Seq < 1 {
			outcome.Rejected = append(outcome.Rejected, DiagnosisStepRewardRejection{Seq: entry.Seq, Reason: "seq must be positive"})
			continue
		}
		// Clamp score.
		score := entry.Score
		if score < 0 {
			score = 0
		}
		if !containsDiagnosisSeq(segCkpt.ExpectedRewardSeqs, int32(entry.Seq)) {
			outcome.Rejected = append(outcome.Rejected, DiagnosisStepRewardRejection{Seq: entry.Seq, Reason: "seq is not an assistant target"})
			continue
		}
		// Check for conflicting rewrite.
		existingScore, _, exists, err := dagWriter.GetDiagnosisStepReward(ctx, projectID, segmentID, int32(entry.Seq))
		if err != nil {
			slog.Warn("diagnosis tool server: step reward lookup failed", "segment_id", segmentID, "seq", entry.Seq, "error", err)
			outcome.MissingSeqs = append(outcome.MissingSeqs, entry.Seq)
			continue
		}
		if exists && existingScore != score {
			outcome.Rejected = append(outcome.Rejected, DiagnosisStepRewardRejection{Seq: entry.Seq, Reason: "conflicting rewrite: existing score differs"})
			continue
		}
		if exists && existingScore == score {
			// Idempotent replay.
			outcome.PersistedSeqs = append(outcome.PersistedSeqs, entry.Seq)
			continue
		}
		if err := dagWriter.UpsertDiagnosisStepReward(ctx, projectID, segmentID, int32(entry.Seq), score, entry.Rationale); err != nil {
			slog.Warn("diagnosis tool server: step reward upsert failed", "segment_id", segmentID, "seq", entry.Seq, "error", err)
			outcome.MissingSeqs = append(outcome.MissingSeqs, entry.Seq)
			continue
		}
		outcome.PersistedSeqs = append(outcome.PersistedSeqs, entry.Seq)
	}

	// Update reward count on segment checkpoint.
	if totalRewards, err := dagWriter.CountDiagnosisStepRewards(ctx, projectID, segmentID); err == nil {
		_ = store.RecordSegmentRewards(ctx, runID, segmentID, totalRewards)
	}
	return outcome, nil
}

// DiagnosisSegmentProgress is one segment's coverage within a run.
type DiagnosisSegmentProgress struct {
	SegmentID            string `json:"segment_id"`
	Ordinal              int    `json:"ordinal"`
	Status               string `json:"status"`
	FetchedMessageCount  int    `json:"fetched_message_count"`
	ExpectedMessageCount int    `json:"expected_message_count"`
	RewardCount          int    `json:"reward_count"`
	ExpectedRewardCount  int    `json:"expected_reward_count"`
}

// DiagnosisRunProgress is the authoritative run/segment progress served by
// the network run API and the human-facing /diagnosis/latest endpoint.
type DiagnosisRunProgress struct {
	RunID                string                     `json:"run_id"`
	Status               DiagnosisRunStatus         `json:"status"`
	Segments             []DiagnosisSegmentProgress `json:"segments"`
	FetchedMessageCount  int                        `json:"fetched_message_count"`
	ExpectedMessageCount int                        `json:"expected_message_count"`
	RecordedRewardCount  int                        `json:"recorded_reward_count"`
	ExpectedRewardCount  int                        `json:"expected_reward_count"`
}

// BuildDiagnosisRunProgress aggregates segment checkpoints into the progress
// payload; run-level counts sum over all segments.
func BuildDiagnosisRunProgress(run DiagnosisRunCheckpoint, segments []SegmentDiagnosisCheckpoint) DiagnosisRunProgress {
	progress := DiagnosisRunProgress{
		RunID:    run.RunID,
		Status:   run.Status,
		Segments: make([]DiagnosisSegmentProgress, 0, len(segments)),
	}
	for _, seg := range segments {
		progress.Segments = append(progress.Segments, DiagnosisSegmentProgress{
			SegmentID:            seg.SegmentID,
			Ordinal:              seg.Ordinal,
			Status:               string(seg.Status),
			FetchedMessageCount:  seg.FetchedMessageCount,
			ExpectedMessageCount: seg.ExpectedMessageCount,
			RewardCount:          seg.RewardCount,
			ExpectedRewardCount:  seg.ExpectedRewardCount,
		})
		progress.FetchedMessageCount += seg.FetchedMessageCount
		progress.ExpectedMessageCount += seg.ExpectedMessageCount
		progress.RecordedRewardCount += seg.RewardCount
		progress.ExpectedRewardCount += seg.ExpectedRewardCount
	}
	return progress
}

// IncompleteDiagnosisSegment identifies one unfinished segment and why.
type IncompleteDiagnosisSegment struct {
	SegmentID string
	Reason    string
}

// ListIncompleteDiagnosisSegments enumerates the segments blocking run
// completion with a coverage-gap reason for each.
func ListIncompleteDiagnosisSegments(segments []SegmentDiagnosisCheckpoint) []IncompleteDiagnosisSegment {
	var missing []IncompleteDiagnosisSegment
	for _, seg := range segments {
		if seg.Status == SegmentDiagnosisCompleted {
			continue
		}
		reason := "incomplete"
		if seg.FetchedMessageCount < seg.ExpectedMessageCount {
			reason = "message coverage gap"
		} else if seg.RewardCount < seg.ExpectedRewardCount {
			reason = "reward coverage gap"
		}
		missing = append(missing, IncompleteDiagnosisSegment{SegmentID: seg.SegmentID, Reason: reason})
	}
	return missing
}

// TruncateDiagnosisTaskContext applies the one-shot prompt budget
// (maxDiagnosisContextBytes per field) to a task context, reporting whether
// each field was cut.
func TruncateDiagnosisTaskContext(tc TaskContext) (goal string, goalTruncated bool, goldContext string, goldTruncated bool) {
	goal = truncateUTF8Bytes(tc.Goal, maxDiagnosisContextBytes)
	goalTruncated = len(goal) < len(tc.Goal)
	goldContext = truncateUTF8Bytes(tc.GoldContext, maxDiagnosisContextBytes)
	goldTruncated = len(goldContext) < len(tc.GoldContext)
	return goal, goalTruncated, goldContext, goldTruncated
}
