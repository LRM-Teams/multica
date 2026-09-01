// SPDX-License-Identifier: Apache-2.0

package service

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// ---------------------------------------------------------------------------
// Harness
// ---------------------------------------------------------------------------

// universalDAGBackfillHarness layers the Task 22 backfill surface onto the
// shadow-gate harness: migration 475 (boundary_quality marker + training
// selection exclusion) applied verbatim, the terminal-completion columns the
// candidate scan reads, and the final rollout gate seeded to a chosen phase.
type universalDAGBackfillHarness struct {
	t *testing.T
	*universalDAGShadowGateHarness
}

func newUniversalDAGBackfillHarness(t *testing.T) *universalDAGBackfillHarness {
	t.Helper()
	shadow := newUniversalDAGShadowGateHarness(t)
	// The pre-454 legacy schema stub carries only the columns the boundary
	// state machine needs; the backfill candidate scan additionally reads the
	// terminal-completion columns of the real 160+ shape.
	_, err := shadow.conn.Exec(shadow.ctx, `
		ALTER TABLE agent_inbox_event
		ADD COLUMN status text,
		ADD COLUMN acked_at timestamptz`)
	require.NoError(t, err, "extend stub agent_inbox_event for backfill")
	applyUniversalDAGLegacyBackfillMarker(t, shadow.ctx, shadow.conn)
	return &universalDAGBackfillHarness{t: t, universalDAGShadowGateHarness: shadow}
}

// seedFinalGate writes the global pooled_training gate row at the given
// phase. The backfill worker is the last rollout step (spec §19.11) and runs
// only behind the final gate.
func (h *universalDAGBackfillHarness) seedFinalGate(t *testing.T, phase string) {
	t.Helper()
	_, err := h.conn.Exec(h.ctx, `
		INSERT INTO universal_dag_shadow_gate
			(scope, workspace_id, gate_name, phase, gate_version, policy_version, evidence)
		VALUES ('global', '00000000-0000-0000-0000-000000000000', 'pooled_training', $1, 1, 1, '{}'::jsonb)
		ON CONFLICT DO NOTHING`, phase)
	require.NoError(t, err, "seed final gate at %s", phase)
}

// insertCompletedTask inserts one terminal-completed task (status acked at
// ackedAt) carrying task_message rows 1-2, the range a backfill Segment must
// cover exactly.
func (h *universalDAGBackfillHarness) insertCompletedTask(t *testing.T, ackedAt time.Time) pgtype.UUID {
	t.Helper()
	taskID := pgtype.UUID{Bytes: uuid.New(), Valid: true}
	_, err := h.conn.Exec(h.ctx,
		`INSERT INTO agent_inbox_event(id, workspace_id, channel_id, status, acked_at) VALUES ($1, $2, $3, 'acked', $4)`,
		taskID, h.workspace, h.channel, ackedAt)
	require.NoError(t, err, "insert completed task")
	for seq := 1; seq <= 2; seq++ {
		_, err = h.conn.Exec(h.ctx,
			`INSERT INTO task_message(task_id, seq, content) VALUES ($1, $2, '')`, taskID, seq)
		require.NoError(t, err, "insert task message %d", seq)
	}
	return taskID
}

// backfill runs one bounded pass over the harness workspace.
func (h *universalDAGBackfillHarness) backfill(t *testing.T, opts LegacyBackfillOptions) LegacyBackfillReport {
	t.Helper()
	svc := NewLegacyBackfillService(h.gatePool(), NewShadowGateService(h.gatePool()))
	report, err := svc.BackfillWorkspace(h.ctx, h.workspace, opts)
	require.NoError(t, err, "run legacy backfill pass")
	return report
}

func backfillTestOptions() LegacyBackfillOptions {
	return LegacyBackfillOptions{WindowDays: LegacyBackfillWindowDays, MaxTasks: LegacyBackfillTasksPerPass}
}

// segmentRow loads one segment row by id for assertions.
func (h *universalDAGBackfillHarness) segmentRow(t *testing.T, segmentID string) db.InteractionDagSegment {
	t.Helper()
	row, err := db.New(h.gatePool()).GetUniversalDAGSegment(h.ctx, db.GetUniversalDAGSegmentParams{
		WorkspaceID: h.workspace, SegmentID: segmentID,
	})
	require.NoError(t, err, "load segment %s", segmentID)
	return row
}

// segmentCountForTask counts every segment row of one task.
func (h *universalDAGBackfillHarness) segmentCountForTask(t *testing.T, taskID pgtype.UUID) int {
	t.Helper()
	var count int
	require.NoError(t, h.conn.QueryRow(h.ctx,
		`SELECT count(*) FROM interaction_dag_segment WHERE workspace_id=$1 AND agent_run_id=$2`,
		h.workspace, taskID).Scan(&count), "count segments for task")
	return count
}

// insertLiveSegmentForTask inserts one already-published canonical Segment
// (with its outbox pair) owned by the given task, the "existing live
// Segment" a backfill pass must skip.
func (h *universalDAGBackfillHarness) insertLiveSegmentForTask(t *testing.T, taskID pgtype.UUID) {
	t.Helper()
	segmentID := universalDAGSegmentID(h.workspace, taskID, 1)
	tx, err := h.conn.Begin(h.ctx)
	require.NoError(t, err, "begin live segment pair tx")
	defer tx.Rollback(h.ctx)
	_, err = tx.Exec(h.ctx, `
		INSERT INTO interaction_dag_publish_outbox (workspace_id, segment_id, request_hash, status)
		VALUES ($1, $2, 'req-live', 'pending')`, h.workspace, segmentID)
	require.NoError(t, err, "insert live outbox row")
	_, err = tx.Exec(h.ctx, `
		INSERT INTO interaction_dag_segment (
			workspace_id, segment_id, agent_run_id, generation,
			channel_id_at_event, start_seq, end_seq,
			close_action_kind, canonical_action_id, visible_action_key,
			memory_type_at_event, graph_projection_eligible_at_event,
			trajectory_source, derivative, trainable_eligible,
			publish_status, content_status, provider_capture_status
		) VALUES (
			$1, $2, $3, 1,
			$4, 1, 2,
			'terminal', NULL, $5,
			'graph', true,
			'task_messages', false, true,
			'pending', 'pending', 'not_expected'
		)`,
		h.workspace, segmentID, taskID, h.channel, segmentID+":close")
	require.NoError(t, err, "insert live segment")
	require.NoError(t, tx.Commit(h.ctx), "commit live segment pair")
	// Walk the pair through the real lifecycle triggers to published.
	_, err = h.conn.Exec(h.ctx, `
		UPDATE interaction_dag_segment SET publish_status='processing'
		WHERE workspace_id=$1 AND segment_id=$2`, h.workspace, segmentID)
	require.NoError(t, err, "process live segment")
	_, err = h.conn.Exec(h.ctx, `
		UPDATE interaction_dag_segment
		SET publish_status='published', content_status='published', publish_seq=1, published_at=now()
		WHERE workspace_id=$1 AND segment_id=$2`, h.workspace, segmentID)
	require.NoError(t, err, "publish live segment")
	h.walkOutboxToTerminal(t, segmentID, "published")
}

// ---------------------------------------------------------------------------
// Step 1 RED: rate-limited 90-day legacy_backfill (spec §8.2, §19.11, AC54/55/58)
// ---------------------------------------------------------------------------

func TestLegacyBackfillCreatesOneApproximateSegmentPerCompletedTask(t *testing.T) {
	h := newUniversalDAGBackfillHarness(t)
	defer h.Close()
	h.seedFinalGate(t, "enabled")

	taskID := h.insertCompletedTask(t, time.Now().UTC().Add(-time.Hour))
	report := h.backfill(t, backfillTestOptions())

	assert.Equal(t, 1, report.SegmentsCreated, "one Segment per completed Task")
	assert.True(t, report.GateOpen, "final gate open")
	assert.False(t, report.DeferredRealtime, "no realtime backlog defers the pass")
	assert.Equal(t, 1, h.segmentCountForTask(t, taskID), "exactly one segment row for the task")

	segmentID := universalDAGSegmentID(h.workspace, taskID, 1)
	seg := h.segmentRow(t, segmentID)
	assert.True(t, seg.BoundaryQuality.Valid, "boundary quality marker recorded")
	assert.Equal(t, "approximate", seg.BoundaryQuality.String, "boundary marked approximate (AC55)")
	assert.False(t, seg.TrainableEligible, "backfill segment excluded from training selection by default")
	assert.Equal(t, int32(1), seg.StartSeq, "segment covers the task's full message range")
	assert.Equal(t, int32(2), seg.EndSeq, "segment covers the task's full message range")
	assert.Equal(t, int64(1), seg.Generation, "generation allocated from the real sequence, not guessed")
	assert.Equal(t, "pending", seg.PublishStatus.String, "segment enters the durable publish pipeline")
	assert.Equal(t, "pending", seg.ContentStatus, "sanitization re-done by the pipeline at execution time")

	// No guessed causal edges: the approximate boundary fabricates nothing.
	var edgeCount int
	require.NoError(t, h.conn.QueryRow(h.ctx,
		`SELECT count(*) FROM interaction_dag_edge WHERE workspace_id=$1 AND (src_segment_id=$2 OR dst_segment_id=$2)`,
		h.workspace, segmentID).Scan(&edgeCount), "count edges touching the backfill segment")
	assert.Zero(t, edgeCount, "no guessed edge (AC55)")

	// The publish outbox row exists so the durable pipeline sanitizes and
	// atomizes the backfilled segment like any realtime one.
	var outboxCount int
	require.NoError(t, h.conn.QueryRow(h.ctx,
		`SELECT count(*) FROM interaction_dag_publish_outbox WHERE workspace_id=$1 AND segment_id=$2`,
		h.workspace, segmentID).Scan(&outboxCount), "count outbox rows")
	assert.Equal(t, 1, outboxCount, "atomic publish outbox pair")
}

func TestLegacyBackfillHonorsNinetyDayWindow(t *testing.T) {
	h := newUniversalDAGBackfillHarness(t)
	defer h.Close()
	h.seedFinalGate(t, "enabled")

	oldTask := h.insertCompletedTask(t, time.Now().UTC().Add(-91*24*time.Hour))
	recentTask := h.insertCompletedTask(t, time.Now().UTC().Add(-24*time.Hour))

	report := h.backfill(t, backfillTestOptions())

	assert.Equal(t, 1, report.SegmentsCreated, "only the in-window task is backfilled")
	assert.Zero(t, h.segmentCountForTask(t, oldTask), "task completed outside the window stays untouched")
	assert.Equal(t, 1, h.segmentCountForTask(t, recentTask), "in-window task backfilled")
}

func TestLegacyBackfillSkipsTasksWithExistingLiveSegmentsAndReplaysIdempotently(t *testing.T) {
	h := newUniversalDAGBackfillHarness(t)
	defer h.Close()
	h.seedFinalGate(t, "enabled")

	liveTask := h.insertCompletedTask(t, time.Now().UTC().Add(-time.Hour))
	h.insertLiveSegmentForTask(t, liveTask)
	freshTask := h.insertCompletedTask(t, time.Now().UTC().Add(-2*time.Hour))

	first := h.backfill(t, backfillTestOptions())
	assert.Equal(t, 1, first.SegmentsCreated, "only the segment-less task is backfilled")
	assert.Equal(t, 1, first.Candidates, "live-segment task never becomes a candidate")
	assert.Equal(t, 1, h.segmentCountForTask(t, liveTask), "live segment untouched")

	// Replay: a second pass finds no candidates at all (both tasks carry
	// segments) and creates nothing new anywhere.
	second := h.backfill(t, backfillTestOptions())
	assert.Zero(t, second.SegmentsCreated, "replay is idempotent")
	assert.Zero(t, second.Candidates, "segment-carrying tasks never re-enter the candidate scan")
	assert.Equal(t, 1, h.segmentCountForTask(t, freshTask), "no duplicate backfill segment")
	assert.Equal(t, 1, h.segmentCountForTask(t, liveTask), "live segment still the only one")
}

func TestLegacyBackfillDefersToRealtimeQuota(t *testing.T) {
	h := newUniversalDAGBackfillHarness(t)
	defer h.Close()
	h.seedFinalGate(t, "enabled")
	h.seedCleanPublishedState(t)

	h.insertCompletedTask(t, time.Now().UTC().Add(-time.Hour))
	// Realtime work pending: an unpublished canonical pair outranks backfill.
	h.insertPublishedSegment(t, universalDAGSegmentID(h.workspace, h.insertShadowTask(t), 1), 0, "pending", "pending", h.channel)

	report := h.backfill(t, backfillTestOptions())
	assert.True(t, report.DeferredRealtime, "pass defers while realtime publish work is outstanding")
	assert.Zero(t, report.SegmentsCreated, "no backfill segment while realtime quota is busy")
}

func TestLegacyBackfillRequiresFinalGateEnabled(t *testing.T) {
	h := newUniversalDAGBackfillHarness(t)
	defer h.Close()
	h.seedFinalGate(t, "shadow")

	h.insertCompletedTask(t, time.Now().UTC().Add(-time.Hour))
	report := h.backfill(t, backfillTestOptions())

	assert.False(t, report.GateOpen, "backfill stays closed until the final gate is enabled")
	assert.Zero(t, report.SegmentsCreated, "no segment written behind a closed gate")
}

func TestLegacyBackfillExcludedFromTrainingSelectionByDefault(t *testing.T) {
	h := newUniversalDAGBackfillHarness(t)
	defer h.Close()
	h.seedFinalGate(t, "enabled")

	// Control: an exact published+rewarded segment stays selectable.
	h.insertPublishedSegment(t, "seg-exact-control", 1, "published", "published", h.channel)
	_, err := h.conn.Exec(h.ctx, `
		INSERT INTO interaction_dag_step_reward (segment_id, seq, score)
		VALUES ('seg-exact-control', 1, 1)`)
	require.NoError(t, err, "seed control reward")

	taskID := h.insertCompletedTask(t, time.Now().UTC().Add(-time.Hour))
	h.backfill(t, backfillTestOptions())
	backfillSegmentID := universalDAGSegmentID(h.workspace, taskID, 1)
	// Walk the backfilled pair through the real lifecycle to published so
	// only the approximate marker differentiates it from the control.
	for _, stmt := range []string{
		`UPDATE interaction_dag_segment SET publish_status='processing' WHERE workspace_id='` + h.workspace.String() + `' AND segment_id='` + backfillSegmentID + `'`,
		`UPDATE interaction_dag_segment SET publish_status='published', content_status='published', publish_seq=2, published_at=now() WHERE workspace_id='` + h.workspace.String() + `' AND segment_id='` + backfillSegmentID + `'`,
	} {
		_, err = h.conn.Exec(h.ctx, stmt)
		require.NoError(t, err, "walk backfill segment to published: %s", stmt)
	}
	h.walkOutboxToTerminal(t, backfillSegmentID, "published")
	_, err = h.conn.Exec(h.ctx, `
		INSERT INTO interaction_dag_step_reward (segment_id, seq, score)
		VALUES ($1, 1, 1)`, backfillSegmentID)
	require.NoError(t, err, "seed backfill reward (isolation: marker alone must exclude)")

	candidates, err := db.New(h.gatePool()).ListTrainingSegmentCandidates(h.ctx, db.ListTrainingSegmentCandidatesParams{
		WorkspaceID: h.workspace, LimitCount: 10,
	})
	require.NoError(t, err, "list training candidates")
	ids := map[string]bool{}
	for _, c := range candidates {
		ids[c.ItemKey] = true
	}
	assert.True(t, ids["seg-exact-control"], "exact segment remains selectable")
	assert.False(t, ids[backfillSegmentID], "approximate backfill segment excluded from training selection (AC54)")
}

func TestLegacyBackfillRevalidatesScopeAtExecutionTime(t *testing.T) {
	t.Setenv("MULTICA_MEMORY_TYPE", "graph")
	h := newUniversalDAGBackfillHarness(t)
	defer h.Close()
	h.seedFinalGate(t, "enabled")

	taskID := h.insertCompletedTask(t, time.Now().UTC().Add(-time.Hour))
	h.backfill(t, backfillTestOptions())

	seg := h.segmentRow(t, universalDAGSegmentID(h.workspace, taskID, 1))
	// The task row predates every canonical column; eligibility is re-derived
	// from the workspace's CURRENT route, not flipped from stale row fields.
	assert.Equal(t, "graph", seg.MemoryTypeAtEvent, "memory type re-resolved at execution time (spec §8.2)")
	assert.True(t, seg.ChannelIDAtEvent.Valid, "channel scope re-resolved at execution time")
	assert.Equal(t, h.channel, seg.ChannelIDAtEvent, "channel scope re-resolved at execution time")
	assert.True(t, seg.GraphProjectionEligibleAtEvent, "graph eligibility re-derived from current route")
}
