// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/multica-ai/multica/server/internal/memorygraph"
)

// graphMemoryProjectionHarness wraps the publisher harness (boundary + 466)
// with a throwaway workspaces root the projector writes graphs into.
type graphMemoryProjectionHarness struct {
	t    *testing.T
	ctx  context.Context
	root string
	*universalDAGPublisherHarness
}

func newGraphMemoryProjectionHarness(t *testing.T) *graphMemoryProjectionHarness {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)
	h := &graphMemoryProjectionHarness{
		t: t, ctx: ctx, root: t.TempDir(),
		universalDAGPublisherHarness: newUniversalDAGPublisherHarness(t),
	}
	ensureGraphMemoryLineageTable(t, ctx, h.conn, h.schema)
	return h
}

// ensureGraphMemoryLineageTable creates the channel lineage table in the
// harness's private schema, schema-qualified so nothing ever resolves (or
// locks) through public. The projector reads event-time lineage from it.
func ensureGraphMemoryLineageTable(t *testing.T, ctx context.Context, conn *pgxpool.Conn, schema string) {
	t.Helper()
	qualified := pgx.Identifier{schema, "graph_memory_channel_lineage"}.Sanitize()
	if _, err := conn.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS `+qualified+` (
		  workspace_id   uuid        NOT NULL,
		  channel_id     uuid        NOT NULL,
		  generation     bigint      NOT NULL,
		  graph_kind     text        NOT NULL CHECK (graph_kind IN ('project', 'channel')),
		  graph_owner_id uuid        NOT NULL,
		  valid_from     timestamptz NOT NULL DEFAULT now(),
		  valid_to       timestamptz,
		  PRIMARY KEY (channel_id, generation)
		)`); err != nil {
		t.Fatalf("create lineage table in private schema: %v", err)
	}
}

// seedChannelLineage installs the event-time lineage row for the harness
// channel at the given generation (the route registry normally creates it;
// the projector must resolve it read-only).
func (h *graphMemoryProjectionHarness) seedChannelLineage(t *testing.T, generation int64, kind, ownerID string) {
	t.Helper()
	_, err := h.conn.Exec(h.ctx, `
		INSERT INTO graph_memory_channel_lineage (workspace_id, channel_id, generation, graph_kind, graph_owner_id)
		VALUES ($1, $2, $3, $4, $5::uuid)
		ON CONFLICT DO NOTHING`, h.workspace, h.channel, generation, kind, ownerID)
	require.NoError(t, err, "seed channel lineage")
}

// publishGraphSegment publishes one segment with a real fact message and
// returns its id. Default: graph-eligible, channel+project scoped.
func (h *graphMemoryProjectionHarness) publishGraphSegment(t *testing.T, projectOnly, derivative bool) string {
	t.Helper()
	if projectOnly {
		return h.publishSegmentWithFacts(t, "graph", false, true, derivative)
	}
	return h.publishSegmentWithFacts(t, "graph", true, true, derivative)
}

// publishSegmentWithFacts freezes event-time facts the boundary fixtures
// cannot express (memory type, scope presence) by building the boundary
// input directly, then publishes through the real publisher.
func (h *graphMemoryProjectionHarness) publishSegmentWithFacts(t *testing.T, memoryType string, withChannel, withProject, derivative bool) string {
	t.Helper()
	task := h.createTask(t, h.ctx, 1)
	setTaskMessageContent(t, h.universalDAGPublisherHarness, task, "My project codename is NIMBUS and the launch date is March 3rd.", `{"a":1}`, "")
	input := DAGBoundaryInput{
		WorkspaceID:       h.workspace,
		Task:              task,
		BoundaryKind:      DAGBoundaryVisible,
		CloseActionKind:   DAGCloseMessage,
		EndSeq:            1,
		ActionKey:         task.ID.String() + ":projection-facts",
		ActionID:          pgtype.UUID{Bytes: uuid.New(), Valid: true},
		RouteGeneration:   1,
		MemoryTypeAtEvent: memoryType,
		Derivative:        derivative,
	}
	if withChannel {
		input.ChannelID = h.channel
	}
	if withProject {
		input.ProjectID = h.project
	}
	result, err := h.recordBoundary(h.ctx, input)
	require.NoError(t, err, "record boundary with explicit facts")
	published, err := NewInteractionDAGPublisher(h.pubPool).PublishClaim(h.ctx, 10)
	require.NoError(t, err)
	require.Equal(t, 1, published, "publish the segment")
	return result.SegmentID
}

// projectionRow reads the outbox row state for one segment.
func (h *graphMemoryProjectionHarness) projectionRow(t *testing.T, segmentID string) (status string, attempts int32, lastError string, found bool) {
	t.Helper()
	err := h.conn.QueryRow(h.ctx, `
		SELECT status, attempts, COALESCE(last_error,'') FROM graph_memory_projection_outbox
		WHERE segment_id=$1`, segmentID).Scan(&status, &attempts, &lastError)
	if err != nil {
		require.ErrorIs(t, err, pgx.ErrNoRows, "projection row read")
		return "", 0, "", false
	}
	return status, attempts, lastError, true
}

// enqueueProjectionRequest simulates an operator backfill: an explicit
// request for an already-published segment.
func (h *graphMemoryProjectionHarness) enqueueProjectionRequest(t *testing.T, segmentID string) {
	t.Helper()
	_, err := h.conn.Exec(h.ctx, `
		INSERT INTO graph_memory_projection_outbox (workspace_id, segment_id, request_hash, route_generation)
		VALUES ($1, $2, 'sha256:backfill', 1)
		ON CONFLICT (workspace_id, segment_id) DO NOTHING`, h.workspace, segmentID)
	require.NoError(t, err, "enqueue backfill projection request")
}

// stagingPaths returns the graph staging files for one segment under the
// given scope.
func stagingPaths(root, workspaceID string, kind memorygraph.GraphDirKind, ownerID, segmentID string) (body, meta string) {
	dir, err := memorygraph.DirForScope(root, workspaceID, kind, ownerID)
	if err != nil {
		return "", ""
	}
	return filepath.Join(dir, "staging", "segments", segmentID+".md"),
		filepath.Join(dir, "staging", "segments", segmentID+".scope.json")
}

func readStagingBody(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoError(t, err, "staging body must exist")
	var doc map[string]any
	require.NoError(t, json.Unmarshal(raw, &doc))
	return doc
}

func newTestProjector(h *graphMemoryProjectionHarness) *GraphMemoryProjector {
	return NewGraphMemoryProjector(h.pubPool, WithGraphMemoryProjectionRoot(h.root))
}

// --- Step 1: eligibility matrix ---

func TestGraphMemoryProjection_ProjectsChannelAtomsIntoEventTimeRoute(t *testing.T) {
	h := newGraphMemoryProjectionHarness(t)
	defer h.Close()
	segmentID := h.publishGraphSegment(t, false, false)
	h.seedChannelLineage(t, 1, "channel", h.channel.String())

	claimed, err := newTestProjector(h).ProjectClaim(h.ctx, 10)
	require.NoError(t, err)
	assert.Equal(t, 1, claimed)

	status, attempts, _, found := h.projectionRow(t, segmentID)
	require.True(t, found, "Task 7 wrote the durable request in the publish tx")
	assert.Equal(t, "completed", status)
	assert.Zero(t, attempts)

	ws := h.workspace.String()
	bodyPath, metaPath := stagingPaths(h.root, ws, memorygraph.GraphDirKindChannel, h.channel.String(), segmentID)
	doc := readStagingBody(t, bodyPath)
	assert.Equal(t, segmentID, doc["segment_id"], "projection document is keyed by segment")
	assert.Equal(t, "universal_dag_atoms", doc["source"])
	raw, _ := json.Marshal(doc["atoms"])
	assert.Contains(t, string(raw), "NIMBUS")

	metaRaw, err := os.ReadFile(metaPath)
	require.NoError(t, err, "scope sidecar must exist")
	var meta memorygraph.SegmentMeta
	require.NoError(t, json.Unmarshal(metaRaw, &meta))
	assert.Equal(t, ws, meta.WorkspaceID)
	assert.Equal(t, "channel", meta.Visibility)
	assert.Equal(t, h.channel.String(), meta.ChannelID)
	assert.Equal(t, int64(1), meta.LineageGeneration, "meta carries the event-time lineage generation")

	// Exact-channel: the harness segment also froze a project scope, but the
	// channel atoms must not leak into the project graph.
	projectOwner := h.project.String()
	pBody, _ := stagingPaths(h.root, ws, memorygraph.GraphDirKindProject, projectOwner, segmentID)
	_, err = os.Stat(pBody)
	assert.True(t, os.IsNotExist(err), "channel atoms never land in the project graph")
}

func TestGraphMemoryProjection_ProjectsProjectOnlyAtomsIntoProjectGraph(t *testing.T) {
	h := newGraphMemoryProjectionHarness(t)
	defer h.Close()
	segmentID := h.publishGraphSegment(t, true, false)

	claimed, err := newTestProjector(h).ProjectClaim(h.ctx, 10)
	require.NoError(t, err)
	assert.Equal(t, 1, claimed)

	bodyPath, metaPath := stagingPaths(h.root, h.workspace.String(), memorygraph.GraphDirKindProject, h.project.String(), segmentID)
	doc := readStagingBody(t, bodyPath)
	assert.Equal(t, "universal_dag_atoms", doc["source"])
	metaRaw, err := os.ReadFile(metaPath)
	require.NoError(t, err)
	var meta memorygraph.SegmentMeta
	require.NoError(t, json.Unmarshal(metaRaw, &meta))
	assert.Equal(t, "project", meta.Visibility)
	assert.Equal(t, h.project.String(), meta.ProjectID)
}

func TestGraphMemoryProjection_ZeroProjectionForIneligibleSegments(t *testing.T) {
	h := newGraphMemoryProjectionHarness(t)
	defer h.Close()

	// Derivative segments publish without atoms or a request row at all.
	derivativeID := h.publishGraphSegment(t, false, true)
	claimed, err := newTestProjector(h).ProjectClaim(h.ctx, 10)
	require.NoError(t, err)
	assert.Zero(t, claimed, "no request row exists for derivative segments")
	_, _, _, found := h.projectionRow(t, derivativeID)
	assert.False(t, found)

	// Legacy-frozen and unscoped segments: explicit requests complete as
	// skips. The projector re-validates frozen facts, never current state.
	for name, segmentID := range map[string]string{
		"legacy memory type": h.publishSegmentWithFacts(t, "legacy", true, true, false),
		"unscoped":           h.publishSegmentWithFacts(t, "graph", false, false, false),
	} {
		h.enqueueProjectionRequest(t, segmentID)
		claimed, err := newTestProjector(h).ProjectClaim(h.ctx, 10)
		require.NoError(t, err, name)
		assert.Equal(t, 1, claimed, name)
		status, _, _, found := h.projectionRow(t, segmentID)
		require.True(t, found, name)
		assert.Equal(t, "completed", status, name+": ineligible requests skip idempotently")
		bodyPath, _ := stagingPaths(h.root, h.workspace.String(), memorygraph.GraphDirKindChannel, h.channel.String(), segmentID)
		_, statErr := os.Stat(bodyPath)
		assert.True(t, os.IsNotExist(statErr), name+": no graph write for ineligible segments")
	}
}

func TestGraphMemoryProjection_LegacyToGraphSwitchUsesEventTimeValuesOnly(t *testing.T) {
	h := newGraphMemoryProjectionHarness(t)
	defer h.Close()

	// A legacy-frozen segment stays legacy even after the workspace switches
	// to graph memory (event-time facts win over current state).
	legacyID := h.publishSegmentWithFacts(t, "legacy", true, true, false)
	h.seedChannelLineage(t, 1, "channel", h.channel.String())
	h.enqueueProjectionRequest(t, legacyID)

	// A graph-frozen segment still projects even if the workspace runs legacy
	// now: the projector never consults current workspace state.
	graphID := h.publishGraphSegment(t, false, false)

	claimed, err := newTestProjector(h).ProjectClaim(h.ctx, 10)
	require.NoError(t, err)
	assert.Equal(t, 2, claimed)

	legacyStatus, _, _, legacyFound := h.projectionRow(t, legacyID)
	require.True(t, legacyFound)
	assert.Equal(t, "completed", legacyStatus)
	legacyBody, _ := stagingPaths(h.root, h.workspace.String(), memorygraph.GraphDirKindChannel, h.channel.String(), legacyID)
	_, err = os.Stat(legacyBody)
	assert.True(t, os.IsNotExist(err), "legacy-frozen segment is never projected")

	graphStatus, _, _, graphFound := h.projectionRow(t, graphID)
	require.True(t, graphFound)
	assert.Equal(t, "completed", graphStatus, "graph-frozen segment projects regardless of current workspace state")
	graphBody, _ := stagingPaths(h.root, h.workspace.String(), memorygraph.GraphDirKindChannel, h.channel.String(), graphID)
	_, err = os.Stat(graphBody)
	require.NoError(t, err, "graph-frozen segment reaches the graph")
}

func TestGraphMemoryProjection_ExplicitBackfillRequestIsProjected(t *testing.T) {
	h := newGraphMemoryProjectionHarness(t)
	defer h.Close()
	segmentID := h.publishGraphSegment(t, false, false)
	h.seedChannelLineage(t, 1, "channel", h.channel.String())
	// Simulate a pre-outbox era segment: the request row is lost, an operator
	// enqueues one explicitly.
	_, err := h.conn.Exec(h.ctx, `DELETE FROM graph_memory_projection_outbox WHERE segment_id=$1`, segmentID)
	require.NoError(t, err)
	h.enqueueProjectionRequest(t, segmentID)

	claimed, err := newTestProjector(h).ProjectClaim(h.ctx, 10)
	require.NoError(t, err)
	assert.Equal(t, 1, claimed)
	status, _, _, found := h.projectionRow(t, segmentID)
	require.True(t, found)
	assert.Equal(t, "completed", status)
	bodyPath, _ := stagingPaths(h.root, h.workspace.String(), memorygraph.GraphDirKindChannel, h.channel.String(), segmentID)
	_, err = os.Stat(bodyPath)
	require.NoError(t, err, "backfill request projects the segment")
}

// --- Step 2: consumer behavior ---

func TestGraphMemoryProjection_MissingEventTimeLineageFailsTerminal(t *testing.T) {
	h := newGraphMemoryProjectionHarness(t)
	defer h.Close()
	segmentID := h.publishGraphSegment(t, false, false)
	// No lineage row at the event-time generation: route identity cannot be
	// validated, so the request dead-letters instead of guessing a target.

	claimed, err := newTestProjector(h).ProjectClaim(h.ctx, 10)
	require.NoError(t, err)
	assert.Equal(t, 1, claimed)
	status, attempts, lastError, found := h.projectionRow(t, segmentID)
	require.True(t, found)
	assert.Equal(t, "dead_letter", status)
	assert.Zero(t, attempts, "terminal route failures never consume retries")
	assert.Contains(t, lastError, "route")

	bodyPath, _ := stagingPaths(h.root, h.workspace.String(), memorygraph.GraphDirKindChannel, h.channel.String(), segmentID)
	_, statErr := os.Stat(bodyPath)
	assert.True(t, os.IsNotExist(statErr), "no write without a validated route")
}

func TestGraphMemoryProjection_IdempotentCompletion(t *testing.T) {
	h := newGraphMemoryProjectionHarness(t)
	defer h.Close()
	segmentID := h.publishGraphSegment(t, false, false)
	h.seedChannelLineage(t, 1, "channel", h.channel.String())

	_, err := newTestProjector(h).ProjectClaim(h.ctx, 10)
	require.NoError(t, err)
	// Second pass: nothing left to claim, nothing rewritten.
	claimed, err := newTestProjector(h).ProjectClaim(h.ctx, 10)
	require.NoError(t, err)
	assert.Zero(t, claimed)
	status, _, _, found := h.projectionRow(t, segmentID)
	require.True(t, found)
	assert.Equal(t, "completed", status)
}

func TestGraphMemoryProjection_StoreFailureRetriesThenDeadLetters(t *testing.T) {
	h := newGraphMemoryProjectionHarness(t)
	defer h.Close()
	segmentID := h.publishGraphSegment(t, false, false)
	h.seedChannelLineage(t, 1, "channel", h.channel.String())

	// The root is a regular file: every scoped-dir ensure fails, exercising
	// the transient retry path with real filesystem errors.
	badRoot := filepath.Join(t.TempDir(), "not-a-dir")
	require.NoError(t, os.WriteFile(badRoot, []byte("x"), 0o644))
	projector := NewGraphMemoryProjector(h.pubPool,
		WithGraphMemoryProjectionRoot(badRoot),
		WithGraphMemoryProjectionBackoff(time.Millisecond),
		WithGraphMemoryProjectionMaxAttempts(2))

	claimed, err := projector.ProjectClaim(h.ctx, 10)
	require.NoError(t, err)
	assert.Equal(t, 1, claimed)
	status, attempts, _, found := h.projectionRow(t, segmentID)
	require.True(t, found)
	assert.Equal(t, "retry", status)
	assert.Equal(t, int32(1), attempts)

	// Fast-forward the backoff and fail once more: the attempt cap
	// dead-letters the request.
	_, err = h.conn.Exec(h.ctx, `
		UPDATE graph_memory_projection_outbox SET next_attempt_at = now() - interval '1 second'
		WHERE segment_id=$1`, segmentID)
	require.NoError(t, err)
	claimed, err = projector.ProjectClaim(h.ctx, 10)
	require.NoError(t, err)
	assert.Equal(t, 1, claimed)
	status, attempts, _, _ = h.projectionRow(t, segmentID)
	assert.Equal(t, "dead_letter", status)
	assert.Equal(t, int32(2), attempts)
}

func TestGraphMemoryProjection_ClaimsAreExclusive(t *testing.T) {
	h := newGraphMemoryProjectionHarness(t)
	defer h.Close()
	h.publishGraphSegment(t, false, false)
	h.seedChannelLineage(t, 1, "channel", h.channel.String())

	first := NewGraphMemoryProjector(h.pubPool,
		WithGraphMemoryProjectionRoot(h.root),
		WithGraphMemoryProjectionWorkerID("projector-a"),
		WithGraphMemoryProjectionClock(func() time.Time {
			return time.Now().Add(-2 * time.Minute) // both workers see a live lease
		}))
	claimed, err := first.ProjectClaim(h.ctx, 10)
	require.NoError(t, err)
	assert.Equal(t, 1, claimed)

	// A second projector cannot steal a live lease: nothing is claimable.
	second := newTestProjector(h)
	claimed, err = second.ProjectClaim(h.ctx, 10)
	require.NoError(t, err)
	assert.Zero(t, claimed, "a live lease is not claimable")
}
