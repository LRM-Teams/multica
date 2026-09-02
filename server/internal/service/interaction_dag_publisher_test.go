package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// universalDAGPublisherHarness wraps the Task 2 boundary harness so publisher
// tests always start from real atomically-created (segment, outbox) pairs.
// The publisher drains through its own schema-scoped pool: pool connections
// do not inherit the boundary harness connection's search_path.
type universalDAGPublisherHarness struct {
	t       *testing.T
	ctx     context.Context
	pubPool *pgxpool.Pool
	*universalDAGBoundaryHarness
}

func newUniversalDAGPublisherHarness(t *testing.T) *universalDAGPublisherHarness {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)
	boundary := newUniversalDAGBoundaryHarness(t, ctx)
	applyUniversalDAGAtomProjectionMigration(t, ctx, boundary.conn)
	applyUniversalDAGRetractionGateMigration(t, ctx, boundary.conn)
	applyUniversalDAGPublicationMigration(t, ctx, boundary.conn)

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		databaseURL = "postgres://multica:multica@localhost:5432/multica?sslmode=disable"
	}
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatalf("parse publisher pool URL: %v", err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = boundary.schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("open publisher pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return &universalDAGPublisherHarness{
		t: t, ctx: ctx, pubPool: pool,
		universalDAGBoundaryHarness: boundary,
	}
}

// applyUniversalDAGAtomProjectionMigration applies migration 466 in the
// harness's private schema so publish-transaction tests cover the atom and
// projection-request tables.
func applyUniversalDAGAtomProjectionMigration(t *testing.T, ctx context.Context, conn *pgxpool.Conn) {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate universal DAG publisher test")
	}
	path := filepath.Join(filepath.Dir(filename), "..", "..", "migrations", "481_graph_memory_atom_projection.up.sql")
	migration, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read migration 466: %v", err)
	}
	if _, err := conn.Exec(ctx, string(migration)); err != nil {
		t.Fatalf("apply migration 466 in private schema: %v", err)
	}
}

// applyUniversalDAGRetractionGateMigration applies migration 467 (retraction
// fence tables) in the harness's private schema; the publish transaction
// maintains guard and provenance rows in it.
func applyUniversalDAGRetractionGateMigration(t *testing.T, ctx context.Context, conn *pgxpool.Conn) {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate universal DAG publisher test")
	}
	path := filepath.Join(filepath.Dir(filename), "..", "..", "migrations", "482_memory_retraction_gate.up.sql")
	migration, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read migration 467: %v", err)
	}
	if _, err := conn.Exec(ctx, string(migration)); err != nil {
		t.Fatalf("apply migration 467 in private schema: %v", err)
	}
}

// applyUniversalDAGPublicationMigration applies migration 469 (DB
// publication ledger + atom_consolidation gate column) in the harness's
// private schema. The ALTER is not naturally idempotent, so the gate column
// doubling as the applied marker keeps repeat harness layers safe.
func applyUniversalDAGPublicationMigration(t *testing.T, ctx context.Context, conn *pgxpool.Conn) {
	t.Helper()
	var applied bool
	if err := conn.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.columns
			WHERE table_schema = current_schema()
			  AND table_name = 'memory_read_phase_gate'
			  AND column_name = 'atom_consolidation_enabled')`).Scan(&applied); err != nil {
		t.Fatalf("probe migration 469 marker: %v", err)
	}
	if applied {
		return
	}
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate universal DAG publisher test")
	}
	path := filepath.Join(filepath.Dir(filename), "..", "..", "migrations", "484_graph_memory_publication_coverage.up.sql")
	migration, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read migration 469: %v", err)
	}
	if _, err := conn.Exec(ctx, string(migration)); err != nil {
		t.Fatalf("apply migration 469 in private schema: %v", err)
	}
}

// recordMessageSegment closes one message-kind boundary on the task.
func (h *universalDAGPublisherHarness) recordMessageSegment(task db.AgentInboxEvent, endSeq int32, actionKey string) string {
	h.t.Helper()
	result, err := h.recordBoundary(h.ctx, h.boundaryInput(task, universalDAGBoundaryFixture{
		kind: DAGBoundaryVisible, closeKind: DAGCloseMessage, endSeq: endSeq, actionKey: actionKey,
	}))
	require.NoError(h.t, err, "record boundary for %s", actionKey)
	return result.SegmentID
}

func (h *universalDAGPublisherHarness) outboxRow(segmentID string) (status string, attempts int32, leaseOwner string, lastError string, hasNext bool, completed bool) {
	h.t.Helper()
	err := h.conn.QueryRow(h.ctx, `
		SELECT status, attempts, COALESCE(lease_owner,''), COALESCE(last_error,''),
		       (next_attempt_at IS NOT NULL), (completed_at IS NOT NULL)
		FROM interaction_dag_publish_outbox WHERE segment_id=$1`, segmentID).
		Scan(&status, &attempts, &leaseOwner, &lastError, &hasNext, &completed)
	require.NoError(h.t, err, "read outbox row %s", segmentID)
	return
}

type universalDAGSegmentRow struct {
	publishStatus string
	contentStatus string
	publishSeq    int64
	hasPublished  bool
}

func (h *universalDAGPublisherHarness) segmentRow(segmentID string) universalDAGSegmentRow {
	h.t.Helper()
	var row universalDAGSegmentRow
	err := h.conn.QueryRow(h.ctx, `
		SELECT publish_status, content_status, COALESCE(publish_seq,0), (published_at IS NOT NULL)
		FROM interaction_dag_segment WHERE segment_id=$1`, segmentID).
		Scan(&row.publishStatus, &row.contentStatus, &row.publishSeq, &row.hasPublished)
	require.NoError(h.t, err, "read segment row %s", segmentID)
	return row
}

func (h *universalDAGPublisherHarness) nextAttemptAt(segmentID string) time.Time {
	h.t.Helper()
	var at pgtype.Timestamptz
	require.NoError(h.t, h.conn.QueryRow(h.ctx,
		`SELECT next_attempt_at FROM interaction_dag_publish_outbox WHERE segment_id=$1`, segmentID).Scan(&at))
	require.True(h.t, at.Valid, "next_attempt_at must be set on retry rows")
	return at.Time
}

// forceAttemptDue fast-forwards the retry backoff so the next claim can pick
// the row up without waiting for wall-clock time.
func (h *universalDAGPublisherHarness) forceAttemptDue(segmentID string) {
	h.t.Helper()
	_, err := h.conn.Exec(h.ctx,
		`UPDATE interaction_dag_publish_outbox SET next_attempt_at = now() - interval '1 second' WHERE segment_id=$1`,
		segmentID)
	require.NoError(h.t, err, "fast-forward retry backoff for %s", segmentID)
}

// simulateCrashedClaim leaves a retrying row mid-flight: a worker leased it
// (retry -> processing) and vanished before applying an outcome, so only an
// expired lease remains.
func (h *universalDAGPublisherHarness) simulateCrashedClaim(segmentID string) {
	h.t.Helper()
	_, err := h.conn.Exec(h.ctx, `
		UPDATE interaction_dag_publish_outbox
		SET status='processing', lease_owner='crashed-worker',
		    lease_expires_at = now() - interval '1 second', next_attempt_at=NULL
		WHERE segment_id=$1 AND status='retry'`, segmentID)
	require.NoError(h.t, err, "simulate crashed claim for %s", segmentID)
	_, err = h.conn.Exec(h.ctx, `
		UPDATE interaction_dag_segment SET publish_status='processing'
		WHERE segment_id=$1 AND publish_status='retry'`, segmentID)
	require.NoError(h.t, err, "mirror crashed claim onto segment %s", segmentID)
}

// classifyingSink lets each test drive the publish outcome per segment.
type classifyingSink struct {
	mu     sync.Mutex
	errFor map[string]error
	calls  []string
}

func (s *classifyingSink) PublishSegment(_ context.Context, _ *db.Queries, claim InteractionDAGPublishClaim, _ SanitizedTrajectory) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, claim.SegmentID)
	if err, ok := s.errFor[claim.SegmentID]; ok {
		return err
	}
	return nil
}

func newPublisherWithSink(t *testing.T, h *universalDAGPublisherHarness, sink PublishSink) *InteractionDAGPublisher {
	t.Helper()
	return NewInteractionDAGPublisher(h.pubPool, WithInteractionDAGPublishSink(sink))
}

func TestInteractionDAGPublisher_ClaimsPendingSegmentAndPublishesWithSequence(t *testing.T) {
	h := newUniversalDAGPublisherHarness(t)
	defer h.Close()
	task := h.createTask(t, h.ctx, 1)
	segmentID := h.recordMessageSegment(task, 1, "publish-claim")

	published, err := NewInteractionDAGPublisher(h.pubPool).PublishClaim(h.ctx, 10)
	require.NoError(t, err)
	assert.Equal(t, 1, published)

	status, attempts, leaseOwner, _, hasNext, completed := h.outboxRow(segmentID)
	assert.Equal(t, string(SegmentPublished), status)
	assert.Zero(t, attempts)
	assert.Empty(t, leaseOwner, "terminal rows must release the lease")
	assert.False(t, hasNext)
	assert.True(t, completed, "published rows must record completion")

	segment := h.segmentRow(segmentID)
	assert.Equal(t, string(SegmentPublished), segment.publishStatus)
	assert.Equal(t, "published", segment.contentStatus)
	assert.Positive(t, segment.publishSeq, "publish_seq is allocated only in the publish transaction")
	assert.True(t, segment.hasPublished)

	// A drained outbox must not be claimed twice.
	again, err := NewInteractionDAGPublisher(h.pubPool).PublishClaim(h.ctx, 10)
	require.NoError(t, err)
	assert.Zero(t, again)
}

func TestInteractionDAGPublisher_PublishesMetadataOnlySegmentWithEmptyContent(t *testing.T) {
	h := newUniversalDAGPublisherHarness(t)
	defer h.Close()
	taskID := pgtype.UUID{Bytes: uuid.New(), Valid: true}
	_, err := h.conn.Exec(h.ctx, `INSERT INTO agent_inbox_event(id,workspace_id) VALUES ($1,$2)`, taskID, h.workspace)
	require.NoError(t, err, "insert unscoped task")
	task := db.AgentInboxEvent{ID: taskID, WorkspaceID: h.workspace}
	NewTaskService(db.New(h.conn), h.conn, nil, nil).FinalizeTerminalTaskSideEffects(h.ctx, task)

	published, err := NewInteractionDAGPublisher(h.pubPool).PublishClaim(h.ctx, 10)
	require.NoError(t, err)
	assert.Equal(t, 1, published)

	var segmentID string
	require.NoError(t, h.conn.QueryRow(h.ctx,
		`SELECT segment_id FROM interaction_dag_publish_outbox WHERE status='published'`).Scan(&segmentID))
	segment := h.segmentRow(segmentID)
	assert.Equal(t, string(SegmentPublished), segment.publishStatus)
	assert.Equal(t, "empty", segment.contentStatus, "metadata-only content stays empty after publish")
	assert.Positive(t, segment.publishSeq)
}

func TestInteractionDAGPublisher_TransientFailureRetriesWithExponentialBackoff(t *testing.T) {
	h := newUniversalDAGPublisherHarness(t)
	defer h.Close()
	task := h.createTask(t, h.ctx, 1)
	segmentID := h.recordMessageSegment(task, 1, "transient-backoff")

	sink := &classifyingSink{errFor: map[string]error{segmentID: fmt.Errorf("provider unavailable: %w", ErrDAGPublishTransient)}}
	publisher := newPublisherWithSink(t, h, sink)

	processed, err := publisher.PublishClaim(h.ctx, 10)
	require.NoError(t, err)
	assert.Equal(t, 1, processed)

	status, attempts, leaseOwner, lastError, hasNext, completed := h.outboxRow(segmentID)
	assert.Equal(t, string(SegmentRetry), status)
	assert.Equal(t, int32(1), attempts, "processing->retry must increment attempts exactly once")
	assert.Empty(t, leaseOwner, "retry rows must release the lease")
	assert.Contains(t, lastError, "provider unavailable")
	assert.True(t, hasNext, "retry rows must schedule the next attempt")
	assert.False(t, completed)

	segment := h.segmentRow(segmentID)
	assert.Equal(t, string(SegmentRetry), segment.publishStatus)
	assert.Equal(t, "pending", segment.contentStatus)
	assert.Zero(t, segment.publishSeq)

	// First backoff interval is one minute (exponential base).
	firstDelay := h.nextAttemptAt(segmentID).Sub(time.Now().UTC())
	assert.Greater(t, firstDelay, 30*time.Second, "first backoff must back off, not hammer")
	assert.LessOrEqual(t, firstDelay, 90*time.Second)

	// Backoff gates the next claim until the interval elapses.
	processed, err = publisher.PublishClaim(h.ctx, 10)
	require.NoError(t, err)
	assert.Zero(t, processed, "retry row must not be claimable before next_attempt_at")

	h.forceAttemptDue(segmentID)
	processed, err = publisher.PublishClaim(h.ctx, 10)
	require.NoError(t, err)
	assert.Equal(t, 1, processed)

	status, attempts, _, _, _, _ = h.outboxRow(segmentID)
	assert.Equal(t, string(SegmentRetry), status)
	assert.Equal(t, int32(2), attempts)
	secondDelay := h.nextAttemptAt(segmentID).Sub(time.Now().UTC())
	assert.Greater(t, secondDelay, 90*time.Second, "second backoff doubles the interval")
	assert.LessOrEqual(t, secondDelay, 3*time.Minute)
}

func TestInteractionDAGPublisher_StaleLeaseReclaimDoesNotConsumeAttempts(t *testing.T) {
	h := newUniversalDAGPublisherHarness(t)
	defer h.Close()
	task := h.createTask(t, h.ctx, 1)
	segmentID := h.recordMessageSegment(task, 1, "stale-lease")

	sink := &classifyingSink{errFor: map[string]error{segmentID: fmt.Errorf("crash mid publish: %w", ErrDAGPublishTransient)}}
	crashed := newPublisherWithSink(t, h, sink)
	_, err := crashed.PublishClaim(h.ctx, 10)
	require.NoError(t, err)

	h.forceAttemptDue(segmentID)
	_, err = crashed.PublishClaim(h.ctx, 10)
	require.NoError(t, err)
	_, attempts, _, _, _, _ := h.outboxRow(segmentID)
	require.Equal(t, int32(2), attempts)

	// Simulate the crashed worker: the row stays processing under an expired lease.
	h.simulateCrashedClaim(segmentID)
	recovered := NewInteractionDAGPublisher(h.pubPool)
	processed, err := recovered.PublishClaim(h.ctx, 10)
	require.NoError(t, err)
	assert.Equal(t, 1, processed, "an expired lease must be reclaimable")

	segment := h.segmentRow(segmentID)
	assert.Equal(t, string(SegmentPublished), segment.publishStatus)
	_, attempts, _, _, _, completed := h.outboxRow(segmentID)
	assert.Equal(t, int32(2), attempts, "lease reclaim itself must not consume an attempt")
	assert.True(t, completed)
}

func TestInteractionDAGPublisher_DeadLettersAfterTenTransientAttempts(t *testing.T) {
	h := newUniversalDAGPublisherHarness(t)
	defer h.Close()
	task := h.createTask(t, h.ctx, 1)
	segmentID := h.recordMessageSegment(task, 1, "dead-letter")

	sink := &classifyingSink{errFor: map[string]error{segmentID: fmt.Errorf("storage flake: %w", ErrDAGPublishTransient)}}
	publisher := newPublisherWithSink(t, h, sink)

	for attempt := 1; attempt <= 10; attempt++ {
		if attempt > 1 {
			h.forceAttemptDue(segmentID)
		}
		processed, err := publisher.PublishClaim(h.ctx, 10)
		require.NoError(t, err)
		require.Equal(t, 1, processed, "attempt %d must be processed", attempt)
		status, attempts, _, _, _, _ := h.outboxRow(segmentID)
		require.Equal(t, string(SegmentRetry), status, "attempt %d stays in retry", attempt)
		require.Equal(t, int32(attempt), attempts)
	}

	// The 11th transient failure exhausts the retry policy and dead-letters.
	h.forceAttemptDue(segmentID)
	processed, err := publisher.PublishClaim(h.ctx, 10)
	require.NoError(t, err)
	require.Equal(t, 1, processed)

	status, attempts, leaseOwner, lastError, _, completed := h.outboxRow(segmentID)
	assert.Equal(t, string(SegmentDeadLetter), status)
	assert.Equal(t, int32(10), attempts, "dead-letter keeps the attempt ledger at the cap")
	assert.Empty(t, leaseOwner)
	assert.True(t, completed, "terminal rows record completion")
	assert.NotEmpty(t, lastError)

	segment := h.segmentRow(segmentID)
	assert.Equal(t, string(SegmentDeadLetter), segment.publishStatus)
	assert.Equal(t, "dead_letter", segment.contentStatus)
	assert.Zero(t, segment.publishSeq, "nothing was published, so no sequence is allocated")
	assert.False(t, segment.hasPublished)

	// Dead-letter rows are never re-claimed.
	h.forceAttemptDue(segmentID)
	processed, err = publisher.PublishClaim(h.ctx, 10)
	require.NoError(t, err)
	assert.Zero(t, processed)
}

func TestInteractionDAGPublisher_RedactionFailureIsTerminalWithoutRetry(t *testing.T) {
	h := newUniversalDAGPublisherHarness(t)
	defer h.Close()
	task := h.createTask(t, h.ctx, 1)
	segmentID := h.recordMessageSegment(task, 1, "redaction-terminal")

	sink := &classifyingSink{errFor: map[string]error{segmentID: fmt.Errorf("sanitizer schema: %w", ErrDAGPublishRedaction)}}
	publisher := newPublisherWithSink(t, h, sink)

	processed, err := publisher.PublishClaim(h.ctx, 10)
	require.NoError(t, err)
	assert.Equal(t, 1, processed)

	status, attempts, _, lastError, hasNext, completed := h.outboxRow(segmentID)
	assert.Equal(t, string(SegmentRedactionFailed), status)
	assert.Zero(t, attempts, "deterministic failures never consume a retry")
	assert.False(t, hasNext)
	assert.True(t, completed)
	assert.Contains(t, lastError, "sanitizer schema")

	segment := h.segmentRow(segmentID)
	assert.Equal(t, string(SegmentRedactionFailed), segment.publishStatus)
	assert.Equal(t, "redaction_failed", segment.contentStatus)
	assert.Zero(t, segment.publishSeq)

	// No retry is scheduled, so nothing is claimable afterwards.
	processed, err = publisher.PublishClaim(h.ctx, 10)
	require.NoError(t, err)
	assert.Zero(t, processed)
}

func TestInteractionDAGPublisher_ScopeViolationIsRejectedWithoutRetry(t *testing.T) {
	h := newUniversalDAGPublisherHarness(t)
	defer h.Close()
	task := h.createTask(t, h.ctx, 1)
	segmentID := h.recordMessageSegment(task, 1, "scope-reject")

	sink := &classifyingSink{errFor: map[string]error{segmentID: fmt.Errorf("workspace policy: %w", ErrDAGPublishScope)}}
	publisher := newPublisherWithSink(t, h, sink)

	processed, err := publisher.PublishClaim(h.ctx, 10)
	require.NoError(t, err)
	assert.Equal(t, 1, processed)

	status, attempts, _, _, _, completed := h.outboxRow(segmentID)
	assert.Equal(t, string(SegmentRejectedScope), status)
	assert.Zero(t, attempts)
	assert.True(t, completed)

	segment := h.segmentRow(segmentID)
	assert.Equal(t, string(SegmentRejectedScope), segment.publishStatus)
	assert.Equal(t, "rejected_scope", segment.contentStatus)
	assert.Zero(t, segment.publishSeq)
}

func TestInteractionDAGPublisher_ReplayRequeuesRetryRowsIdempotently(t *testing.T) {
	h := newUniversalDAGPublisherHarness(t)
	defer h.Close()
	task := h.createTask(t, h.ctx, 1)
	segmentID := h.recordMessageSegment(task, 1, "replay-requeue")

	sink := &classifyingSink{errFor: map[string]error{segmentID: fmt.Errorf("flake: %w", ErrDAGPublishTransient)}}
	publisher := newPublisherWithSink(t, h, sink)
	_, err := publisher.PublishClaim(h.ctx, 10)
	require.NoError(t, err)
	status, _, _, _, _, _ := h.outboxRow(segmentID)
	require.Equal(t, string(SegmentRetry), status)

	// The scheduled backoff would delay the next attempt beyond the test window.
	require.Greater(t, h.nextAttemptAt(segmentID).Sub(time.Now().UTC()), 30*time.Second)
	processed, err := publisher.PublishClaim(h.ctx, 10)
	require.NoError(t, err)
	assert.Zero(t, processed)

	// Operator replay requeues the row for immediate processing and is
	// idempotent: repeated requests converge on the same claimable state.
	require.NoError(t, publisher.ReplayDeadLetter(h.ctx, h.workspace.String(), segmentID))
	require.NoError(t, publisher.ReplayDeadLetter(h.ctx, h.workspace.String(), segmentID))
	require.LessOrEqual(t, h.nextAttemptAt(segmentID).Sub(time.Now().UTC()), 5*time.Second)

	healthy := &classifyingSink{}
	recovered := newPublisherWithSink(t, h, healthy)
	processed, err = recovered.PublishClaim(h.ctx, 10)
	require.NoError(t, err)
	assert.Equal(t, 1, processed)
	assert.Equal(t, []string{segmentID}, healthy.calls)

	status, _, _, _, _, _ = h.outboxRow(segmentID)
	assert.Equal(t, string(SegmentPublished), status)
}

func TestInteractionDAGPublisher_ReplayOfTerminalDeadLetterFailsClosed(t *testing.T) {
	h := newUniversalDAGPublisherHarness(t)
	defer h.Close()
	task := h.createTask(t, h.ctx, 2)
	first := h.recordMessageSegment(task, 1, "dlq-terminal-a")
	second := h.recordMessageSegment(task, 2, "dlq-terminal-b")

	sink := &classifyingSink{errFor: map[string]error{
		first:  fmt.Errorf("exhausted: %w", ErrDAGPublishTransient),
		second: fmt.Errorf("schema: %w", ErrDAGPublishRedaction),
	}}
	publisher := newPublisherWithSink(t, h, sink)
	for i := 0; i < 11; i++ {
		if i > 0 {
			h.forceAttemptDue(first)
		}
		_, err := publisher.PublishClaim(h.ctx, 10)
		require.NoError(t, err)
	}
	status, _, _, _, _, _ := h.outboxRow(first)
	require.Equal(t, string(SegmentDeadLetter), status)

	// Migration 454 makes terminal (segment, outbox) pairs immutable: there is
	// no legal transition out of dead_letter on either row, so replay must
	// fail closed instead of silently doing nothing.
	err := publisher.ReplayDeadLetter(h.ctx, h.workspace.String(), first)
	require.ErrorIs(t, err, ErrDAGPublishReplayTerminal)

	err = publisher.ReplayDeadLetter(h.ctx, h.workspace.String(), second)
	require.ErrorIs(t, err, ErrDAGPublishReplayTerminal, "redaction_failed is terminal too")

	status, _, _, _, _, completed := h.outboxRow(first)
	assert.Equal(t, string(SegmentDeadLetter), status, "failed replay must leave the terminal row untouched")
	assert.True(t, completed)

	// A row actively leased by another worker is not replayable either.
	thirdTask := h.createTask(t, h.ctx, 1)
	third := h.recordMessageSegment(thirdTask, 1, "dlq-terminal-c")
	_, err = h.conn.Exec(h.ctx, `
		UPDATE interaction_dag_publish_outbox
		SET status='processing', lease_owner='other-worker', lease_expires_at=now()+interval '5 min', next_attempt_at=NULL
		WHERE segment_id=$1`, third)
	require.NoError(t, err)
	err = publisher.ReplayDeadLetter(h.ctx, h.workspace.String(), third)
	require.ErrorIs(t, err, ErrDAGPublishReplayLeased)
}

// The synthetic legacy schema omits the business status column, so this test
// runs on the full-migration schema where agent_inbox_event carries the real
// lifecycle states. Counts are tolerant because the shared schema may hold
// leftover pending rows from earlier tests.
func TestInteractionDAGPublisher_PublishNeverBlocksBusinessTaskTerminal(t *testing.T) {
	h := newUniversalDAGProjectionHarness(t)

	terminalTask := h.createTask("mention")
	h.addTaskMessage(terminalTask, 1)
	h.exec(`UPDATE agent_inbox_event SET status='failed' WHERE id=$1`, terminalTask.ID)
	firstAction := pgtype.UUID{Bytes: uuid.New(), Valid: true}
	publishedBoundary := h.recordBoundary(DAGBoundaryInput{
		WorkspaceID: h.workspace, Task: terminalTask, BoundaryKind: DAGBoundaryVisible,
		CloseActionKind: DAGCloseMessage, EndSeq: 1,
		ActionID: firstAction, ActionKey: "message:" + firstAction.String(),
		MemoryTypeAtEvent: "graph", ProjectID: h.project,
	})

	// The business task is already terminal before the pipeline runs. Earlier
	// sharers of this schema leave pending outbox rows behind, so the drain
	// keeps claiming batches until this segment's own outcome is visible.
	publisher := NewInteractionDAGPublisher(h.pool)
	processed := 0
	for range 100 {
		claimed, err := publisher.PublishClaim(h.ctx, 10)
		require.NoError(t, err)
		processed += claimed
		var probe string
		require.NoError(t, h.pool.QueryRow(h.ctx,
			`SELECT publish_status FROM interaction_dag_segment WHERE segment_id=$1`,
			publishedBoundary.SegmentID).Scan(&probe))
		if probe == string(SegmentPublished) || claimed == 0 {
			break
		}
	}
	assert.GreaterOrEqual(t, processed, 1, "a terminal business task must still be publishable")

	var taskStatus, publishStatus string
	require.NoError(t, h.pool.QueryRow(h.ctx,
		`SELECT publish_status FROM interaction_dag_segment WHERE segment_id=$1`,
		publishedBoundary.SegmentID).Scan(&publishStatus))
	assert.Equal(t, string(SegmentPublished), publishStatus)
	require.NoError(t, h.pool.QueryRow(h.ctx,
		`SELECT status FROM agent_inbox_event WHERE id=$1`, terminalTask.ID).Scan(&taskStatus))
	assert.Equal(t, "failed", taskStatus, "publishing must not mutate business task state")

	// Conversely, a failing publish must not drag the task out of terminal.
	failingTask := h.createTask("mention")
	h.addTaskMessage(failingTask, 1)
	h.exec(`UPDATE agent_inbox_event SET status='acked' WHERE id=$1`, failingTask.ID)
	secondAction := pgtype.UUID{Bytes: uuid.New(), Valid: true}
	failingBoundary := h.recordBoundary(DAGBoundaryInput{
		WorkspaceID: h.workspace, Task: failingTask, BoundaryKind: DAGBoundaryVisible,
		CloseActionKind: DAGCloseMessage, EndSeq: 1,
		ActionID: secondAction, ActionKey: "message:" + secondAction.String(),
		MemoryTypeAtEvent: "graph", ProjectID: h.project,
	})

	sink := &classifyingSink{errFor: map[string]error{
		failingBoundary.SegmentID: fmt.Errorf("schema: %w", ErrDAGPublishRedaction),
	}}
	failingPublisher := NewInteractionDAGPublisher(h.pool, WithInteractionDAGPublishSink(sink))
	for range 100 {
		var probe string
		require.NoError(t, h.pool.QueryRow(h.ctx,
			`SELECT status FROM interaction_dag_publish_outbox WHERE segment_id=$1`,
			failingBoundary.SegmentID).Scan(&probe))
		if probe != "" && probe != "pending" {
			break
		}
		claimed, err := failingPublisher.PublishClaim(h.ctx, 10)
		require.NoError(t, err)
		if claimed == 0 {
			break
		}
	}

	var outboxStatus string
	require.NoError(t, h.pool.QueryRow(h.ctx,
		`SELECT status FROM interaction_dag_publish_outbox WHERE segment_id=$1`,
		failingBoundary.SegmentID).Scan(&outboxStatus))
	assert.Equal(t, string(SegmentRedactionFailed), outboxStatus, "the failure lands on the pipeline rows only")
	require.NoError(t, h.pool.QueryRow(h.ctx,
		`SELECT status FROM agent_inbox_event WHERE id=$1`, failingTask.ID).Scan(&taskStatus))
	assert.Equal(t, "acked", taskStatus, "publish failures must not touch the business task status")
}

func TestInteractionDAGPublisher_ConcurrentClaimsAreExclusiveAndSequencesUnique(t *testing.T) {
	h := newUniversalDAGPublisherHarness(t)
	defer h.Close()
	task := h.createTask(t, h.ctx, 2)
	first := h.recordMessageSegment(task, 1, "concurrent-a")
	second := h.recordMessageSegment(task, 2, "concurrent-b")

	var wg sync.WaitGroup
	results := make([]int, 2)
	errs := make([]error, 2)
	for i := range 2 {
		wg.Add(1)
		go func(slot int) {
			defer wg.Done()
			processed, err := NewInteractionDAGPublisher(h.pubPool, WithInteractionDAGWorkerID(fmt.Sprintf("worker-%d", slot))).PublishClaim(h.ctx, 1)
			results[slot], errs[slot] = processed, err
		}(i)
	}
	wg.Wait()
	require.NoError(t, errs[0])
	require.NoError(t, errs[1])
	assert.Equal(t, 1, results[0])
	assert.Equal(t, 1, results[1])

	// Drain anything a race left behind, then require both published with
	// distinct per-workspace sequences.
	for range 4 {
		if processed, err := NewInteractionDAGPublisher(h.pubPool).PublishClaim(h.ctx, 2); err != nil || processed == 0 {
			break
		}
	}
	for _, segmentID := range []string{first, second} {
		status, _, leaseOwner, _, _, completed := h.outboxRow(segmentID)
		assert.Equal(t, string(SegmentPublished), status, "segment %s", segmentID)
		assert.Empty(t, leaseOwner)
		assert.True(t, completed)
	}
	seqs := map[int64]bool{}
	for _, segmentID := range []string{first, second} {
		row := h.segmentRow(segmentID)
		assert.Positive(t, row.publishSeq)
		assert.False(t, seqs[row.publishSeq], "publish_seq must be unique per workspace")
		seqs[row.publishSeq] = true
	}
}

func TestInteractionDAGPublishHealthCounters_AggregateOutboxStates(t *testing.T) {
	h := newUniversalDAGPublisherHarness(t)
	defer h.Close()
	task := h.createTask(t, h.ctx, 3)
	publishedSegment := h.recordMessageSegment(task, 1, "health-published")
	redactedSegment := h.recordMessageSegment(task, 2, "health-redacted")

	sink := &classifyingSink{errFor: map[string]error{redactedSegment: fmt.Errorf("schema: %w", ErrDAGPublishRedaction)}}
	processed, err := newPublisherWithSink(t, h, sink).PublishClaim(h.ctx, 10)
	require.NoError(t, err)
	assert.Equal(t, 2, processed)

	// Anything closed after the pass stays pending for the next tick.
	pendingSegment := h.recordMessageSegment(task, 3, "health-pending")

	status, _, _, _, _, _ := h.outboxRow(publishedSegment)
	assert.Equal(t, string(SegmentPublished), status)
	status, _, _, _, _, _ = h.outboxRow(redactedSegment)
	assert.Equal(t, string(SegmentRedactionFailed), status)
	status, _, _, _, _, _ = h.outboxRow(pendingSegment)
	assert.Equal(t, string(SegmentPending), status)

	snapshot, err := NewInteractionDAGPublisher(h.pubPool).PublishHealth(h.ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(1), snapshot.Published)
	assert.Equal(t, int64(1), snapshot.RedactionFailed)
	assert.Equal(t, int64(1), snapshot.Pending)
	assert.Zero(t, snapshot.DeadLetter)
	assert.Equal(t, int64(1), snapshot.Backlog, "backlog counts non-terminal in-flight work")
}
