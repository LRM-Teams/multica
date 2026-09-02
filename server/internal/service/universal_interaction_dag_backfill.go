// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Task 22: approximate historical backfill (spec §8.2, §19.11, AC54/55/58).
//
// The backfill projects completed historical Tasks into the Universal DAG as
// one approximate Segment per Task. It is the LAST rollout step and runs only
// behind the final shadow gate (pooled_training): reaching that gate requires
// every canary-supported promotion before it, including the workspace owner's
// tenant-grant acknowledgement and pooled opt-in, which is the explicit owner
// authorization §8.2 demands. The pass is rate-limited independently of the
// realtime pipeline and defers while any realtime publish work is
// outstanding, so the approximate channel never consumes the realtime quota.
const (
	// LegacyBackfillWindowDays bounds how far back completed Tasks are
	// projected (spec §19.11: 90 days).
	LegacyBackfillWindowDays = 90
	// LegacyBackfillTasksPerPass bounds one pass — the separate rate budget
	// of the approximate channel.
	LegacyBackfillTasksPerPass = 25
)

// LegacyBackfillOptions tunes one pass; zero values fall back to the
// production constants. Now exists so tests can pin the window evaluation.
type LegacyBackfillOptions struct {
	WindowDays int
	MaxTasks   int
	Now        time.Time
}

// LegacyBackfillReport summarizes one bounded pass over one workspace.
type LegacyBackfillReport struct {
	// GateOpen reports whether the final rollout gate admitted the pass.
	GateOpen bool `json:"gate_open"`
	// DeferredRealtime reports the pass was skipped because realtime
	// publish work was outstanding (realtime quota priority).
	DeferredRealtime bool `json:"deferred_realtime"`
	// Candidates counts the segment-less completed Tasks the scan found.
	Candidates int `json:"candidates"`
	// SegmentsCreated counts approximate Segments inserted this pass.
	SegmentsCreated int `json:"segments_created"`
	// SkippedExisting counts candidates a concurrent writer or an open
	// generation claimed before the pass reached them.
	SkippedExisting int `json:"skipped_existing"`
}

// LegacyBackfillService projects completed historical Tasks into the
// Universal DAG behind the final shadow gate.
type LegacyBackfillService struct {
	pool  *pgxpool.Pool
	gates *ShadowGateService
}

// NewLegacyBackfillService wires the backfill worker. gates may be nil only
// in tests; a nil gate service keeps the pass closed.
func NewLegacyBackfillService(pool *pgxpool.Pool, gates *ShadowGateService) *LegacyBackfillService {
	return &LegacyBackfillService{pool: pool, gates: gates}
}

// BackfillWorkspace runs one bounded, idempotent pass over the workspace's
// 90-day completion window. Every Task it touches either gains exactly one
// approximate Segment (plus its atomic publish-outbox pair) or is left
// untouched; a later pass skips both classes, so replays converge without
// duplicates.
func (s *LegacyBackfillService) BackfillWorkspace(ctx context.Context, workspaceID pgtype.UUID, opts LegacyBackfillOptions) (LegacyBackfillReport, error) {
	report := LegacyBackfillReport{}
	if s == nil || s.pool == nil {
		return report, errors.New("legacy backfill service not configured")
	}
	if !workspaceID.Valid {
		return report, errors.New("legacy backfill requires a workspace")
	}

	// Final gate: the backfill is rollout step 11 and never precedes the
	// stable realtime ladder (AC54). The gate ladder to this point carries
	// the owner authorization chain of §8.2.
	gateStatus, err := s.gates.Gate(ctx, workspaceID, ShadowGatePooledTraining)
	if err != nil {
		return report, fmt.Errorf("legacy backfill gate read: %w", err)
	}
	if gateStatus.Phase != ShadowPhaseEnabled {
		slog.Info("legacy backfill skipped: final gate not enabled",
			"workspace_id", workspaceID.String(), "phase", string(gateStatus.Phase))
		return report, nil
	}
	report.GateOpen = true

	// Realtime quota priority: any outstanding exact-boundary publish work
	// defers the whole pass.
	backlog, err := db.New(s.pool).CountRealtimeDAGPublishBacklog(ctx, workspaceID)
	if err != nil {
		return report, fmt.Errorf("legacy backfill realtime backlog read: %w", err)
	}
	if backlog > 0 {
		slog.Info("legacy backfill deferred: realtime publish backlog outstanding",
			"workspace_id", workspaceID.String(), "backlog", backlog)
		report.DeferredRealtime = true
		return report, nil
	}

	if opts.WindowDays <= 0 {
		opts.WindowDays = LegacyBackfillWindowDays
	}
	if opts.MaxTasks <= 0 {
		opts.MaxTasks = LegacyBackfillTasksPerPass
	}
	if opts.Now.IsZero() {
		opts.Now = time.Now().UTC()
	}
	windowStart := opts.Now.Add(-time.Duration(opts.WindowDays) * 24 * time.Hour)

	queries := db.New(s.pool)
	candidates, err := queries.ListLegacyBackfillCandidateTasks(ctx, db.ListLegacyBackfillCandidateTasksParams{
		WorkspaceID: workspaceID,
		WindowStart: pgtype.Timestamptz{Time: windowStart, Valid: true},
		LimitCount:  int32(opts.MaxTasks),
	})
	if err != nil {
		return report, fmt.Errorf("legacy backfill candidate scan: %w", err)
	}
	report.Candidates = len(candidates)

	// Scope, sanitizer and eligibility are re-derived at execution time
	// (spec §8.2): the backfill never flips historical row fields. The
	// publish pipeline re-runs sanitization over the enqueued Segment.
	memoryType := resolveGraphMemoryType(ctx, queries, workspaceID, graphMemoryEnvMemoryType())

	for _, candidate := range candidates {
		if candidate.MaxSeq <= 0 {
			// No persisted messages: nothing to project an approximate
			// boundary over.
			continue
		}
		created, err := s.backfillOneTask(ctx, queries, workspaceID, candidate, memoryType)
		if err != nil {
			// One bad Task never aborts the pass; the candidate scan
			// retries it next run.
			slog.Warn("legacy backfill task failed",
				"workspace_id", workspaceID.String(),
				"task_id", candidate.ID.String(), "error", err)
			continue
		}
		if created {
			report.SegmentsCreated++
		} else {
			report.SkippedExisting++
		}
	}
	if report.SegmentsCreated > 0 {
		slog.Info("legacy backfill pass complete",
			"workspace_id", workspaceID.String(),
			"segments_created", report.SegmentsCreated,
			"skipped_existing", report.SkippedExisting,
			"candidates", report.Candidates)
	}
	return report, nil
}

// backfillOneTask inserts the Task's single approximate Segment, its atomic
// publish-outbox pair and the completed task cursor inside one transaction.
// It reports false when a concurrent writer or an open generation claimed
// the Task first (skip, not error).
func (s *LegacyBackfillService) backfillOneTask(
	ctx context.Context,
	queries *db.Queries,
	workspaceID pgtype.UUID,
	candidate db.ListLegacyBackfillCandidateTasksRow,
	memoryType string,
) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin task tx: %w", err)
	}
	defer tx.Rollback(ctx)
	q := queries.WithTx(tx)

	// Lock the task's generation cursor: the canonical writer uses the same
	// row lock, so a live boundary and a backfill pass serialize here.
	cursor, err := q.LockUniversalDAGTaskCursor(ctx, db.LockUniversalDAGTaskCursorParams{
		WorkspaceID: workspaceID, AgentRunID: candidate.ID,
	})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return false, fmt.Errorf("lock task cursor: %w", err)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		cursor = db.InteractionDagTaskCursor{
			WorkspaceID: workspaceID, AgentRunID: candidate.ID, NextGeneration: 1,
		}
	}
	if cursor.OpenGeneration.Valid {
		// A live generation is still open (crash or long-running task):
		// the canonical state machine owns it; an approximate boundary
		// must not interleave.
		return false, nil
	}

	generation := cursor.NextGeneration
	if generation < 1 {
		generation = 1
	}
	segmentID := universalDAGSegmentID(workspaceID, candidate.ID, generation)

	// No guessed edge is ever created: linkage stays the canonical writer's
	// monopoly, and generation numbering comes from the real cursor.
	graphEligible := memoryType == "graph" && (candidate.IssueID.Valid || candidate.ChannelID.Valid)
	_, err = q.InsertUniversalDAGSegment(ctx, db.InsertUniversalDAGSegmentParams{
		SegmentID: segmentID, WorkspaceID: workspaceID, AgentRunID: candidate.ID,
		Generation: generation,
		IssueID:    pgUUIDToText(candidate.IssueID),
		StartSeq:   1, EndSeq: candidate.MaxSeq,
		TrainableEligible:              false, // excluded from training selection by default (AC54)
		ChannelIDAtEvent:               candidate.ChannelID,
		MemoryTypeAtEvent:              memoryType,
		GraphProjectionEligibleAtEvent: graphEligible,
		CloseActionKind:                string(DAGCloseTerminal),
		VisibleActionKey:               "legacy_backfill:" + candidate.ID.String(),
		Derivative:                     false,
		SanitizerVersion:               universalDAGSanitizerVersion,
		PolicyVersion:                  universalDAGPolicyVersion,
		BoundaryQuality:                "approximate",
	})
	if err != nil {
		return false, fmt.Errorf("insert approximate segment: %w", err)
	}
	if _, err := q.InsertUniversalDAGPublishOutbox(ctx, db.InsertUniversalDAGPublishOutboxParams{
		WorkspaceID: workspaceID, SegmentID: segmentID,
		RequestHash: universalDAGOutboxRequestHash(segmentID),
	}); err != nil {
		return false, fmt.Errorf("insert approximate segment outbox: %w", err)
	}
	if _, err := q.UpsertUniversalDAGTaskCursor(ctx, db.UpsertUniversalDAGTaskCursorParams{
		WorkspaceID: workspaceID, AgentRunID: candidate.ID,
		NextGeneration: generation + 1, LastClosedSeq: candidate.MaxSeq,
	}); err != nil {
		return false, fmt.Errorf("complete task cursor: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit task tx: %w", err)
	}
	return true, nil
}

// pgUUIDToText renders an optional uuid for the text-typed issue_id argument.
func pgUUIDToText(v pgtype.UUID) string {
	if v.Valid {
		return v.String()
	}
	return ""
}
