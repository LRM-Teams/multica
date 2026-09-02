// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/google/uuid"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// retractionHarness: publisher harness (454+464+466+467) plus helpers.
type retractionHarness struct {
	t   *testing.T
	ctx context.Context
	*universalDAGPublisherHarness
}

func newRetractionHarness(t *testing.T) *retractionHarness {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)
	return &retractionHarness{t: t, ctx: ctx, universalDAGPublisherHarness: newUniversalDAGPublisherHarness(t)}
}

// publishAndRetract publishes a graph segment (guard + provenance land in the
// publish tx), then fences the segment's task_output source on a caller-style
// transaction. Returns the segment's task id and atom count.
func (h *retractionHarness) publishAndRetract(t *testing.T) (taskRef MemorySourceRef, atoms int) {
	t.Helper()
	task := h.createTask(t, h.ctx, 1)
	setTaskMessageContent(t, h.universalDAGPublisherHarness, task, "NIMBUS codename fact for retraction", `{"a":1}`, "")
	segmentID := h.recordMessageSegment(task, 1, "retract-fence")
	published, err := NewInteractionDAGPublisher(h.pubPool).PublishClaim(h.ctx, 10)
	require.NoError(t, err)
	require.Equal(t, 1, published)

	require.NoError(t, h.conn.QueryRow(h.ctx,
		`SELECT count(*) FROM graph_memory_atom WHERE segment_id=$1`, segmentID).Scan(&atoms))
	require.Positive(t, atoms, "fixture must produce atoms")

	taskRef = MemorySourceRefForTask(h.workspace, task.ID)
	tx, err := h.pubPool.Begin(h.ctx)
	require.NoError(t, err)
	defer tx.Rollback(h.ctx)
	require.NoError(t, NewMemoryRetractionService().RetractSourcesTx(h.ctx, tx,
		[]MemorySourceRef{taskRef}, "user:1", "comment deleted"))
	require.NoError(t, tx.Commit(h.ctx))
	return taskRef, atoms
}

func (h *retractionHarness) countRows(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	require.NoError(t, h.conn.QueryRow(h.ctx, query, args...).Scan(&n))
	return n
}

func TestMemoryRetraction_FencesGuardsQuarantineAndAuditAtomically(t *testing.T) {
	h := newRetractionHarness(t)
	defer h.Close()
	_, atoms := h.publishAndRetract(t)

	assert.Equal(t, 1, h.countRows(t,
		`SELECT count(*) FROM memory_source_guard WHERE retracted_at IS NOT NULL`))
	assert.Equal(t, 1, h.countRows(t, `SELECT count(*) FROM retraction_registry`),
		"one attributable retraction event")
	assert.Equal(t, 1, h.countRows(t, `SELECT count(*) FROM memory_deletion_audit`))
	audit := h.countRows(t,
		`SELECT quarantined_count FROM memory_deletion_audit WHERE quarantined_count=$1`, atoms)
	assert.Equal(t, 1, audit, "the audit row records the full atom closure size")
	assert.Equal(t, atoms, h.countRows(t, `SELECT count(*) FROM quarantined_pending_recompute`),
		"every published atom of the source is quarantined")
	// Provenance was written by the publish transaction, not the retraction.
	assert.Equal(t, atoms, h.countRows(t, `SELECT count(*) FROM memory_source_provenance`))
}

func TestMemoryRetraction_RegistersUnknownSourcesOnFence(t *testing.T) {
	h := newRetractionHarness(t)
	defer h.Close()
	// A business delete of a source that never published anything (no guard
	// row from a publish or the backfill) must still fence it.
	unknown := MemorySourceRef{WorkspaceID: h.workspace, Kind: MemorySourceComment,
		ID: pgtype.UUID{Bytes: uuid.New(), Valid: true}}
	tx, err := h.pubPool.Begin(h.ctx)
	require.NoError(t, err)
	require.NoError(t, NewMemoryRetractionService().RetractSourcesTx(h.ctx, tx,
		[]MemorySourceRef{unknown}, "user:1", "comment deleted"))
	require.NoError(t, tx.Commit(h.ctx))
	assert.Equal(t, 1, h.countRows(t,
		`SELECT count(*) FROM memory_source_guard WHERE retracted_at IS NOT NULL AND source_kind='comment'`),
		"unregistered sources are registered and fenced by the retraction itself")
}

func TestMemoryRetraction_RollbackUndoesTheFence(t *testing.T) {
	h := newRetractionHarness(t)
	defer h.Close()
	task := h.createTask(t, h.ctx, 1)
	setTaskMessageContent(t, h.universalDAGPublisherHarness, task, "rollback fence fact NIMBUS", `{"a":1}`, "")
	h.recordMessageSegment(task, 1, "retract-rollback")
	published, err := NewInteractionDAGPublisher(h.pubPool).PublishClaim(h.ctx, 10)
	require.NoError(t, err)
	require.Equal(t, 1, published)

	tx, err := h.pubPool.Begin(h.ctx)
	require.NoError(t, err)
	require.NoError(t, NewMemoryRetractionService().RetractSourcesTx(h.ctx, tx,
		[]MemorySourceRef{MemorySourceRefForTask(h.workspace, task.ID)}, "user:1", "business tombstone"))
	// The business deletion "fails" after fencing: everything rolls back.
	require.NoError(t, tx.Rollback(h.ctx))

	assert.Zero(t, h.countRows(t, `SELECT count(*) FROM memory_source_guard WHERE retracted_at IS NOT NULL`),
		"tombstone, fence, and quarantine commit together or not at all")
	assert.Zero(t, h.countRows(t, `SELECT count(*) FROM retraction_registry`))
	assert.Zero(t, h.countRows(t, `SELECT count(*) FROM quarantined_pending_recompute`))
}

func TestMemoryRetraction_IsIdempotentForAlreadyFencedSources(t *testing.T) {
	h := newRetractionHarness(t)
	defer h.Close()
	taskRef, _ := h.publishAndRetract(t)

	tx, err := h.pubPool.Begin(h.ctx)
	require.NoError(t, err)
	defer tx.Rollback(h.ctx)
	require.NoError(t, NewMemoryRetractionService().RetractSourcesTx(h.ctx, tx,
		[]MemorySourceRef{taskRef}, "user:2", "second deletion wave"))
	require.NoError(t, tx.Commit(h.ctx))

	assert.Equal(t, 2, h.countRows(t, `SELECT count(*) FROM retraction_registry`),
		"each fence event is recorded, but the guard stays single")
	assert.Equal(t, 1, h.countRows(t,
		`SELECT count(*) FROM memory_source_guard WHERE retracted_at IS NOT NULL`))
}

func TestMemoryReadGate_FailsClosedForRetractedSources(t *testing.T) {
	h := newRetractionHarness(t)
	defer h.Close()
	taskRef, _ := h.publishAndRetract(t)
	gate := NewMemoryReadGate(db.New(h.pubPool))

	require.NoError(t, gate.AuthorizeResolve(h.ctx, h.workspace, nil))
	live := MemorySourceRef{WorkspaceID: h.workspace, Kind: MemorySourceComment,
		ID: pgtype.UUID{Bytes: uuid.New(), Valid: true}}
	require.NoError(t, gate.AuthorizeResolve(h.ctx, h.workspace, []MemorySourceRef{live}),
		"unknown-but-unfenced sources are readable (guards fence only registered sources)")

	err := gate.AuthorizeResolve(h.ctx, h.workspace, []MemorySourceRef{live, taskRef})
	require.ErrorIs(t, err, ErrMemorySourceRetracted, "a fenced ref anywhere in the batch fails closed")
}

func TestMemoryReadGate_EveryRouteDisabledByDefault(t *testing.T) {
	h := newRetractionHarness(t)
	defer h.Close()
	gate := NewMemoryReadGate(db.New(h.pubPool))
	for _, route := range []MemoryReadRoute{MemoryRouteAtoms, MemoryRouteSearchV2, MemoryRouteExplore, MemoryRouteCitations} {
		enabled, err := gate.RouteEnabled(h.ctx, h.workspace, route)
		require.NoError(t, err, route)
		assert.False(t, enabled, "%s must be DB-default disabled", route)
		require.Error(t, gate.RequireRouteEnabled(h.ctx, h.workspace, route),
			"%s external behavior stays unreachable without a gate row", route)
	}
}

func TestMemoryReadGate_CanaryRequiredBeforeRouteEnablement(t *testing.T) {
	h := newRetractionHarness(t)
	defer h.Close()
	// An operator inserts a gate row without the retraction canary: the CHECK
	// must reject enabling any route.
	_, err := h.conn.Exec(h.ctx, `
		INSERT INTO memory_read_phase_gate (workspace_id) VALUES ($1)`, h.workspace)
	require.NoError(t, err, "all-off gate row is insertable")
	_, err = h.conn.Exec(h.ctx, `
		INSERT INTO memory_read_phase_gate (workspace_id, atoms_enabled)
		VALUES ($1, true)`, h.workspace)
	require.Error(t, err, "enabling a route without a green retraction canary is rejected by the DB")
}

// ensureIssueClosureTables creates the minimal comment/attachment projections
// the issue-closure query reads (the boundary harness legacy schema has none).
func (h *retractionHarness) ensureIssueClosureTables(t *testing.T) {
	t.Helper()
	_, err := h.conn.Exec(h.ctx, `
CREATE TABLE IF NOT EXISTS comment (
  id uuid PRIMARY KEY,
  workspace_id uuid NOT NULL,
  issue_id uuid
);
CREATE TABLE IF NOT EXISTS attachment (
  id uuid PRIMARY KEY,
  workspace_id uuid NOT NULL,
  issue_id uuid,
  comment_id uuid
);`)
	require.NoError(t, err, "create issue closure tables")
}

// publishForFence publishes one atom-bearing segment without fencing it.
func (h *retractionHarness) publishForFence(t *testing.T, label string) (taskRef MemorySourceRef, atoms int) {
	t.Helper()
	task := h.createTask(t, h.ctx, 1)
	setTaskMessageContent(t, h.universalDAGPublisherHarness, task, "NIMBUS "+label+" fact", `{"a":1}`, "")
	segmentID := h.recordMessageSegment(task, 1, label)
	published, err := NewInteractionDAGPublisher(h.pubPool).PublishClaim(h.ctx, 10)
	require.NoError(t, err)
	require.Equal(t, 1, published)
	require.NoError(t, h.conn.QueryRow(h.ctx,
		`SELECT count(*) FROM graph_memory_atom WHERE segment_id=$1`, segmentID).Scan(&atoms))
	require.Positive(t, atoms, "fixture must produce atoms")
	return MemorySourceRefForTask(h.workspace, task.ID), atoms
}

func TestMemoryRetraction_IssueClosureFencesEveryCanonicalSource(t *testing.T) {
	h := newRetractionHarness(t)
	defer h.Close()
	h.ensureIssueClosureTables(t)

	taskRef, atoms := h.publishForFence(t, "issue-closure")
	issueID := pgtype.UUID{Bytes: uuid.New(), Valid: true}
	_, err := h.conn.Exec(h.ctx,
		`UPDATE agent_inbox_event SET issue_id=$1 WHERE id=$2`, issueID, taskRef.ID)
	require.NoError(t, err)

	commentID := pgtype.UUID{Bytes: uuid.New(), Valid: true}
	_, err = h.conn.Exec(h.ctx,
		`INSERT INTO comment(id,workspace_id,issue_id) VALUES ($1,$2,$3)`, commentID, h.workspace, issueID)
	require.NoError(t, err)
	attachmentID := pgtype.UUID{Bytes: uuid.New(), Valid: true}
	_, err = h.conn.Exec(h.ctx,
		`INSERT INTO attachment(id,workspace_id,comment_id) VALUES ($1,$2,$3)`, attachmentID, h.workspace, commentID)
	require.NoError(t, err)

	tx, err := h.pubPool.Begin(h.ctx)
	require.NoError(t, err)
	defer tx.Rollback(h.ctx)
	require.NoError(t, NewMemoryRetractionService().RetractIssueSourcesTx(
		h.ctx, tx, h.workspace, issueID, "member:u1", "issue deleted"))
	require.NoError(t, tx.Commit(h.ctx))

	for kind, id := range map[string]pgtype.UUID{
		MemorySourceIssue:      issueID,
		MemorySourceTaskOutput: taskRef.ID,
		MemorySourceComment:    commentID,
		MemorySourceAttachment: attachmentID,
	} {
		var fenced bool
		require.NoError(t, h.conn.QueryRow(h.ctx, `
			SELECT retracted_at IS NOT NULL FROM memory_source_guard
			WHERE workspace_id=$1 AND source_kind=$2 AND source_id=$3`,
			h.workspace, kind, id.String()).Scan(&fenced), "guard for %s", kind)
		assert.True(t, fenced, "source kind %s must be fenced by the issue closure", kind)
	}
	assert.Equal(t, atoms, h.countRows(t, `SELECT count(*) FROM quarantined_pending_recompute`))
	assert.Equal(t, 4, h.countRows(t, `SELECT count(*) FROM memory_deletion_audit`))
	assert.Equal(t, 1, h.countRows(t, `SELECT count(*) FROM retraction_registry WHERE source_count=4`))
}

func TestMemoryRetraction_WorkspaceFenceIsSetBasedAndIdempotent(t *testing.T) {
	h := newRetractionHarness(t)
	defer h.Close()

	taskRef, atoms := h.publishForFence(t, "workspace-fence")

	fence := func() {
		tx, err := h.pubPool.Begin(h.ctx)
		require.NoError(t, err)
		defer tx.Rollback(h.ctx)
		require.NoError(t, NewMemoryRetractionService().RetractWorkspaceSourcesTx(
			h.ctx, tx, h.workspace, "member:owner", "workspace deleted"))
		require.NoError(t, tx.Commit(h.ctx))
	}
	fence()

	// The workspace's own guard plus every registered source row is fenced.
	assert.Equal(t, 2, h.countRows(t, `
		SELECT count(*) FROM memory_source_guard
		WHERE workspace_id=$1 AND retracted_at IS NOT NULL`, h.workspace))
	assert.Equal(t, atoms, h.countRows(t, `SELECT count(*) FROM quarantined_pending_recompute`))
	assert.Equal(t, 1, h.countRows(t, `SELECT count(*) FROM memory_deletion_audit`))
	assert.Equal(t, 1, h.countRows(t, `SELECT count(*) FROM retraction_registry`))
	_ = taskRef

	// A second sweep fences nothing new and writes no duplicate event.
	fence()
	assert.Equal(t, 1, h.countRows(t, `SELECT count(*) FROM retraction_registry`))
	assert.Equal(t, 1, h.countRows(t, `SELECT count(*) FROM memory_deletion_audit`))
}

// publishRunSegment records and publishes a segment already linked to a
// Mixed-RL run (run identity is frozen at event time; published segments are
// immutable, so the linkage must be part of the boundary input).
func (h *retractionHarness) publishRunSegment(t *testing.T, label string) (taskRef MemorySourceRef, runID pgtype.UUID) {
	t.Helper()
	task := h.createTask(t, h.ctx, 1)
	setTaskMessageContent(t, h.universalDAGPublisherHarness, task, "NIMBUS "+label+" fact", `{"a":1}`, "")
	runID = pgtype.UUID{Bytes: uuid.New(), Valid: true}
	runAgentID := pgtype.UUID{Bytes: uuid.New(), Valid: true}
	// The 454 run-ownership trigger requires a matching env_dispatch_run /
	// env_dispatch_run_agent pair; env_dispatch_run is keyed by project, so
	// each fixture run gets its own project row.
	proj := pgtype.UUID{Bytes: uuid.New(), Valid: true}
	for _, stmt := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO project(id, workspace_id) VALUES ($1, $2)`, []any{proj, h.workspace}},
		{`INSERT INTO env_dispatch_run(project_id, workspace_id, run_id) VALUES ($1, $2, $3)`, []any{proj, h.workspace, runID}},
		{`INSERT INTO env_dispatch_run_agent(run_agent_id, run_id) VALUES ($1, $2)`, []any{runAgentID, runID}},
	} {
		_, err := h.conn.Exec(h.ctx, stmt.sql, stmt.args...)
		require.NoError(t, err, "seed run ownership rows")
	}
	input := h.boundaryInput(task, universalDAGBoundaryFixture{
		kind: DAGBoundaryVisible, closeKind: DAGCloseMessage, endSeq: 1, actionKey: label,
	})
	input.RunID = runID
	input.RunAgentID = runAgentID
	result, err := h.recordBoundary(h.ctx, input)
	require.NoError(t, err, "record run-linked boundary for %s", label)
	_ = result
	published, err := NewInteractionDAGPublisher(h.pubPool).PublishClaim(h.ctx, 10)
	require.NoError(t, err)
	require.Equal(t, 1, published)
	return MemorySourceRefForTask(h.workspace, task.ID), runID
}

func TestAuthorizeRunSources_FailsClosedForFencedRunSource(t *testing.T) {
	h := newRetractionHarness(t)
	defer h.Close()

	taskRef, runID := h.publishRunSegment(t, "run-gate")
	queries := db.New(h.pubPool)
	require.NoError(t, AuthorizeRunSources(h.ctx, queries, h.workspace, runID))

	// Fencing the run's task_output source flips the shared gate closed.
	tx, err := h.pubPool.Begin(h.ctx)
	require.NoError(t, err)
	require.NoError(t, NewMemoryRetractionService().RetractSourcesTx(h.ctx, tx,
		[]MemorySourceRef{taskRef}, "member:u1", "task output deleted"))
	require.NoError(t, tx.Commit(h.ctx))
	require.ErrorIs(t, AuthorizeRunSources(h.ctx, queries, h.workspace, runID), ErrMemorySourceRetracted)

	// An unrelated run stays readable.
	otherTask, otherRun := h.publishRunSegment(t, "run-gate-unrelated")
	_ = otherTask
	require.NoError(t, AuthorizeRunSources(h.ctx, queries, h.workspace, otherRun))
}
