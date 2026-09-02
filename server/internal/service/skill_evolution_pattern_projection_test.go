// SPDX-License-Identifier: Apache-2.0

package service

// Slice 3.1 projection behavior against the real ledger, retraction
// registry, and pattern plane store: the graph node is the pattern's
// scope-safe projection, hidden entirely (no aggregates) when a source
// retracts, conflicts link without overwriting, and the outbox dispatcher
// converges on the latest revision.

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/multica-ai/multica/server/internal/memorygraph"
	"github.com/multica-ai/multica/server/internal/skillevolution"
	"github.com/multica-ai/multica/server/internal/util"
)

type patternProjectionFixture struct {
	*trajectoryFixture
	projection *SkillPatternProjectionService
	ledger     *PostgresSkillEvolutionLedger
	dispatcher *SkillEvolutionOutboxDispatcher
}

func newPatternProjectionFixture(t *testing.T) *patternProjectionFixture {
	t.Helper()
	t.Setenv("MULTICA_WORKSPACES_ROOT", t.TempDir())
	f := newTrajectoryFixture(t)
	gates := SkillEvolutionFeatureGates{PatternConsolidation: true}
	projection := NewSkillPatternProjectionService(f.pool, NewGraphMutationCoordinator(f.pool), gates)
	ledger := NewPostgresSkillEvolutionLedger(f.pool)
	return &patternProjectionFixture{
		trajectoryFixture: f,
		projection:        projection,
		ledger:            ledger,
		dispatcher:        NewSkillEvolutionOutboxDispatcher(f.pool, ledger, projection),
	}
}

func (f *patternProjectionFixture) draftPattern(t *testing.T, taskIDs ...string) skillevolution.PatternRecord {
	t.Helper()
	evidence := make([]skillevolution.SkillEvolutionRef, 0, len(taskIDs))
	for _, taskID := range taskIDs {
		evidence = append(evidence, skillevolution.SkillEvolutionRef{
			Kind: skillevolution.RefEvaluationRun, ID: taskID, WorkspaceID: f.workspaceID,
		})
	}
	record, err := skillevolution.DraftTentativePattern(skillevolution.PatternDraftInput{
		PatternID:         "pattern-" + uuid.NewString()[:8],
		WorkspaceID:       f.workspaceID,
		EvolutionKey:      "agent-1:spreadsheet:env-3",
		Kind:              skillevolution.PatternKindFailure,
		Problem:           "Sheet export omits hidden rows",
		Applicability:     "spreadsheet export tasks with filtered rows",
		RootCauseSummary:  "export reads the visible range instead of the full row set",
		RecommendedAction: "iterate the full row set before formatting",
		TaskType:          "spreadsheet",
		EnvironmentKey:    "env-3",
		ToolCapabilityID:  "xlsx-writer",
		GeneratorVersion:  "maintainer-1",
		PolicyVersion:     skillevolution.DefaultPatternConsolidationPolicy().PolicyVersion,
		PositiveEvidence:  evidence,
		CreatedByActor:    "maintainer:run-1",
		CreatedAt:         time.Now().UTC().Truncate(time.Microsecond),
	})
	require.NoError(t, err)
	return record
}

func (f *patternProjectionFixture) planeGraph(t *testing.T) *memorygraph.Graph {
	t.Helper()
	root, err := graphMemoryWorkspacesRoot()
	require.NoError(t, err)
	store, err := PatternPlaneStore(root, f.workspaceID)
	require.NoError(t, err)
	version, err := store.CurrentVersion()
	require.NoError(t, err)
	graph, err := memorygraph.LoadGraph(store, version)
	require.NoError(t, err)
	return graph
}

// The projection carries semantics and never aggregates: evidence counts,
// scores, and vote tallies cannot leak through the node body — visible or
// hidden.
func TestSkillPatternProjectionIsAggregateFree(t *testing.T) {
	f := newPatternProjectionFixture(t)
	ctx := context.Background()
	taskA := f.createTask(t, "completed", "")
	taskB := f.createTask(t, "completed", "")
	record := f.draftPattern(t, taskA, taskB)

	require.NoError(t, f.projection.ProjectPattern(ctx, PatternProjectionRequest{
		WorkspaceID: f.workspaceID, Pattern: record,
		SourceTaskIDs: []string{taskA, taskB},
	}))

	node := f.planeGraph(t).Node(record.PatternID)
	require.NotNil(t, node, "the projection node id equals the pattern id")
	assert.Equal(t, memorygraph.NodeRolePattern, memorygraph.EffectiveNodeRole(node.Role))
	assert.Contains(t, node.Body, "problem: Sheet export omits hidden rows")
	assert.Contains(t, node.Body, "status: tentative")
	assert.Nil(t, node.ValidTo)
	assert.Equal(t, []string{taskA, taskB}, node.SourceTaskIDs)

	// Aggregate-free by construction: two pieces of evidence exist, but
	// no count, score, or tally may appear anywhere in the body.
	assert.NotContains(t, node.Body, "evidence", "no evidence counts in the projection")
	assert.NotContains(t, node.Body, "score")
	assert.NotContains(t, node.Body, "count")

	// Idempotent re-projection of the same revision converges to the
	// same node without duplicating versions beyond the store's base.
	require.NoError(t, f.projection.ProjectPattern(ctx, PatternProjectionRequest{
		WorkspaceID: f.workspaceID, Pattern: record,
		SourceTaskIDs: []string{taskA, taskB},
	}))
	assert.Equal(t, node.Body, f.planeGraph(t).Node(record.PatternID).Body)
}

// Retracting a pattern's source hides the projection completely: the
// node stays as a tombstone with neutral text, no semantic content, and
// nothing aggregate.
func TestSkillPatternProjectionHidesOnRetractedSource(t *testing.T) {
	f := newPatternProjectionFixture(t)
	ctx := context.Background()
	taskID := f.createTask(t, "completed", "")
	record := f.draftPattern(t, taskID)

	require.NoError(t, f.projection.ProjectPattern(ctx, PatternProjectionRequest{
		WorkspaceID: f.workspaceID, Pattern: record, SourceTaskIDs: []string{taskID},
	}))
	visible := f.planeGraph(t).Node(record.PatternID)
	require.NotNil(t, visible)
	require.Nil(t, visible.ValidTo)

	retracted := retractPatternSourceTask(t, f, taskID)

	require.NoError(t, f.projection.ProjectPattern(ctx, PatternProjectionRequest{
		WorkspaceID: f.workspaceID, Pattern: record, SourceTaskIDs: []string{taskID},
	}))
	hidden := f.planeGraph(t).Node(record.PatternID)
	require.NotNil(t, hidden, "the node remains as an auditable tombstone")
	require.NotNil(t, hidden.ValidTo, "ValidTo closes the node")
	assert.Equal(t, "hidden: source retracted", hidden.Body)
	assert.NotContains(t, hidden.Body, "Sheet export", "no semantic content survives hiding")
	assert.Empty(t, hidden.SourceTaskIDs, "provenance refs drop with the content")

	// The retracted source really is the cause.
	assert.True(t, retracted)
}

func retractPatternSourceTask(t *testing.T, f *patternProjectionFixture, taskID string) bool {
	t.Helper()
	ctx := context.Background()
	taskUUID, err := util.ParseUUID(taskID)
	require.NoError(t, err)
	wsUUID, err := util.ParseUUID(f.workspaceID)
	require.NoError(t, err)
	tx, err := f.pool.Begin(ctx)
	require.NoError(t, err)
	defer tx.Rollback(ctx)
	require.NoError(t, NewMemoryRetractionService().RetractSourcesTx(ctx, tx, []MemorySourceRef{{
		WorkspaceID: wsUUID, Kind: MemorySourceTaskOutput, ID: taskUUID,
	}}, "admin:ops", "gdpr erasure"))
	require.NoError(t, tx.Commit(ctx))
	return true
}

// Conflicts link patterns with a conflicts_with edge and never overwrite
// either side; dropping the conflict removes the edge again.
func TestSkillPatternProjectionConflictEdges(t *testing.T) {
	f := newPatternProjectionFixture(t)
	ctx := context.Background()
	a := f.draftPattern(t)
	b := f.draftPattern(t)

	require.NoError(t, f.projection.ProjectPattern(ctx, PatternProjectionRequest{
		WorkspaceID: f.workspaceID, Pattern: b,
	}))
	require.NoError(t, f.projection.ProjectPattern(ctx, PatternProjectionRequest{
		WorkspaceID: f.workspaceID, Pattern: a, ConflictsWith: []string{b.PatternID},
	}))

	store := patternPlaneStoreFor(t, f)
	version, err := store.CurrentVersion()
	require.NoError(t, err)
	_, rel, err := store.LoadEdges(version)
	require.NoError(t, err)
	require.Len(t, rel, 1)
	assert.Equal(t, memorygraph.EdgeTypeConflictsWith, rel[0].Type)
	assert.Equal(t, a.PatternID, rel[0].From)
	assert.Equal(t, b.PatternID, rel[0].To)

	// A later revision without the conflict drops the edge (latest
	// revision wins; no stale edges survive).
	require.NoError(t, f.projection.ProjectPattern(ctx, PatternProjectionRequest{
		WorkspaceID: f.workspaceID, Pattern: a,
	}))
	version, err = store.CurrentVersion()
	require.NoError(t, err)
	_, rel, err = store.LoadEdges(version)
	require.NoError(t, err)
	assert.Empty(t, rel)
}

func patternPlaneStoreFor(t *testing.T, f *patternProjectionFixture) *memorygraph.Store {
	t.Helper()
	root, err := graphMemoryWorkspacesRoot()
	require.NoError(t, err)
	store, err := PatternPlaneStore(root, f.workspaceID)
	require.NoError(t, err)
	return store
}

// The dispatcher projects the LATEST ledger revision per aggregate,
// marks the event dispatched exactly once, and leaves unknown aggregate
// kinds pending for their future handlers.
func TestSkillEvolutionOutboxDispatcherProjectsLatestRevision(t *testing.T) {
	f := newPatternProjectionFixture(t)
	ctx := context.Background()
	taskID := f.createTask(t, "completed", "")
	draft := f.draftPattern(t, taskID)
	require.NoError(t, f.ledger.InsertPatternRevision(ctx, draft))

	require.NoError(t, f.ledger.InsertOutboxEvent(ctx, skillevolution.OutboxEvent{
		WorkspaceID:   f.workspaceID,
		AggregateKind: SkillPatternAggregateKind,
		AggregateID:   draft.PatternID,
		EventType:     "pattern_revision",
		Payload:       json.RawMessage(`{}`),
	}))
	// An aggregate no handler owns yet stays pending, untouched.
	require.NoError(t, f.ledger.InsertOutboxEvent(ctx, skillevolution.OutboxEvent{
		WorkspaceID:   f.workspaceID,
		AggregateKind: "skill_candidate",
		AggregateID:   "cand-1",
		EventType:     "candidate_created",
		Payload:       json.RawMessage(`{}`),
	}))

	handled, err := f.dispatcher.DispatchClaim(ctx, f.workspaceID, 16)
	require.NoError(t, err)
	assert.Equal(t, 1, handled)

	node := f.planeGraph(t).Node(draft.PatternID)
	require.NotNil(t, node)
	assert.Contains(t, node.Body, "status: tentative")

	// The pattern event is dispatched; the unknown-kind event remains.
	pending, err := f.ledger.ListPendingOutboxEvents(ctx, f.workspaceID, 16)
	require.NoError(t, err)
	require.Len(t, pending, 1)
	assert.Equal(t, "skill_candidate", pending[0].AggregateKind)

	handled, err = f.dispatcher.DispatchClaim(ctx, f.workspaceID, 16)
	require.NoError(t, err)
	assert.Zero(t, handled, "dispatched events never re-handle")

	// A newer revision wins even if a stale event is still queued: the
	// dispatcher reads the ledger, not the event payload.
	upgraded, _, err := skillevolution.ReevaluatePattern(draft, []skillevolution.PatternEvidenceObservation{
		{Ref: skillevolution.SkillEvolutionRef{Kind: skillevolution.RefEvaluationRun, ID: uuid.NewString(), WorkspaceID: f.workspaceID},
			Polarity: skillevolution.EvidencePositive, LineageID: "workbook-a", RecordedAt: time.Now().UTC()},
		{Ref: skillevolution.SkillEvolutionRef{Kind: skillevolution.RefEvaluationRun, ID: uuid.NewString(), WorkspaceID: f.workspaceID},
			Polarity: skillevolution.EvidencePositive, LineageID: "workbook-b", RecordedAt: time.Now().UTC()},
	}, skillevolution.DefaultPatternConsolidationPolicy(), "maintainer:run-2", time.Now().UTC())
	require.NoError(t, err)
	require.NoError(t, f.ledger.InsertPatternRevision(ctx, upgraded))
	require.NoError(t, f.ledger.InsertOutboxEvent(ctx, skillevolution.OutboxEvent{
		WorkspaceID:   f.workspaceID,
		AggregateKind: SkillPatternAggregateKind,
		AggregateID:   draft.PatternID,
		EventType:     "pattern_revision",
		Payload:       json.RawMessage(`{}`),
	}))
	handled, err = f.dispatcher.DispatchClaim(ctx, f.workspaceID, 16)
	require.NoError(t, err)
	assert.Equal(t, 1, handled)
	assert.Contains(t, f.planeGraph(t).Node(draft.PatternID).Body, "status: supported",
		"the latest revision is authoritative")
}

// The gate fails closed: with consolidation disabled the projection
// refuses to write anything.
func TestSkillPatternProjectionRespectsFeatureGate(t *testing.T) {
	f := newTrajectoryFixture(t)
	t.Setenv("MULTICA_WORKSPACES_ROOT", t.TempDir())
	gated := NewSkillPatternProjectionService(f.pool, NewGraphMutationCoordinator(f.pool), SkillEvolutionFeatureGates{})
	record := skillevolution.PatternRecord{PatternID: "pattern-gated"}
	err := gated.ProjectPattern(context.Background(), PatternProjectionRequest{
		WorkspaceID: f.workspaceID, Pattern: record,
	})
	require.Error(t, err, "disabled gates refuse projections")
	assert.Contains(t, err.Error(), "feature gates")
}
