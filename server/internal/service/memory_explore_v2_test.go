// SPDX-License-Identifier: Apache-2.0

package service

import (
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/multica-ai/multica/server/internal/memorygraph"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// exploreV2Harness: plan harness (454+464+466+467+468) with the explore
// route enabled and one published, unfenced channel-scoped segment.
type exploreV2Harness struct {
	*explorePlanHarness
	atomID  string
	segment string
	taskRef MemorySourceRef
}

func newExploreV2Harness(t *testing.T, enabled bool) *exploreV2Harness {
	t.Helper()
	h := &exploreV2Harness{explorePlanHarness: newExplorePlanHarness(t)}
	if enabled {
		h.enableExploreRoute(t)
	}
	h.taskRef, _ = h.publishForFence(t, "explore-v2")
	require.NoError(t, h.conn.QueryRow(h.ctx, `
		SELECT atom_id, segment_id FROM graph_memory_atom
		WHERE workspace_id=$1 ORDER BY atom_id LIMIT 1`, h.workspace).Scan(&h.atomID, &h.segment))
	require.NotEmpty(t, h.atomID)
	return h
}

func (h *exploreV2Harness) svc() *MemoryExploreV2Service {
	return NewMemoryExploreV2Service(h.pubPool)
}

func (h *exploreV2Harness) pool_() *pgxpool.Pool { return h.pubPool }

// Every operation — not only Start — fails closed while the phase gate is
// red, and nothing persists.
func TestMemoryExploreV2_DisabledGateFailsClosedOnEveryOperation(t *testing.T) {
	h := newExploreV2Harness(t, false)
	defer h.Close()
	svc := h.svc()
	ref := memorygraph.MemoryRef{Kind: memorygraph.MemoryRefStagingAtom, AtomID: h.atomID, SegmentID: h.segment}

	_, err := svc.Start(h.ctx, h.workspace, "traj-gate", h.planGraphs())
	require.ErrorIs(t, err, ErrMemoryRouteDisabled)
	_, err = svc.Explore(h.ctx, h.workspace, "traj-gate", ref)
	require.ErrorIs(t, err, ErrMemoryRouteDisabled)
	require.ErrorIs(t, svc.Redirect(h.ctx, h.workspace, "traj-gate", "new focus"), ErrMemoryRouteDisabled)
	require.ErrorIs(t, svc.Submit(h.ctx, h.workspace, "traj-gate"), ErrMemoryRouteDisabled)
	_, err = svc.Checkpoint(h.ctx, h.workspace, "traj-gate")
	require.ErrorIs(t, err, ErrMemoryRouteDisabled)
	_, err = svc.Evidence(h.ctx, h.workspace, "traj-gate", ref)
	require.ErrorIs(t, err, ErrMemoryRouteDisabled)
	_, err = svc.History(h.ctx, h.workspace, "traj-gate")
	require.ErrorIs(t, err, ErrMemoryRouteDisabled)
	assert.Equal(t, 0, h.countRows(t, `SELECT count(*) FROM memory_explore_plan`))
}

// Start seeds from the atom ledger at the frozen watermark; Explore walks
// Atom→Segment, sibling atoms and bidirectional DAG edges, all fenced.
func TestMemoryExploreV2_StartAndExploreWalkTheAuthorizedGraph(t *testing.T) {
	h := newExploreV2Harness(t, true)
	defer h.Close()
	svc := h.svc()

	start, err := svc.Start(h.ctx, h.workspace, "traj-walk", h.planGraphs())
	require.NoError(t, err)
	require.NotEmpty(t, start.Seeds, "channel-scoped atoms at the watermark must seed the walk")
	assert.Equal(t, DefaultExploreBudgets(), start.Plan.Budgets)
	assert.Equal(t, h.segment, start.Seeds[0].SegmentID)

	ref := memorygraph.MemoryRef{Kind: memorygraph.MemoryRefStagingAtom, AtomID: h.atomID, SegmentID: h.segment}
	neighbors, err := svc.Explore(h.ctx, h.workspace, "traj-walk", ref)
	require.NoError(t, err)
	// The explored segment itself resolves, siblings carry their segment,
	// and any recorded edges appear in both directions.
	found := map[string]bool{}
	for _, n := range neighbors.Refs {
		found[n.Ref.Key()] = true
	}
	assert.True(t, found[ref.Key()], "the explored ref resolves")
	for _, sib := range neighbors.SiblingAtoms {
		assert.Equal(t, h.segment, sib.SegmentID)
	}
}

// A retracted source flips every operation to fail-closed — even mid-walk,
// after the plan already exists.
func TestMemoryExploreV2_RetractedSourceFailsClosedMidWalk(t *testing.T) {
	h := newExploreV2Harness(t, true)
	defer h.Close()
	svc := h.svc()
	_, err := svc.Start(h.ctx, h.workspace, "traj-fence", h.planGraphs())
	require.NoError(t, err)

	tx, err := h.pubPool.Begin(h.ctx)
	require.NoError(t, err)
	require.NoError(t, NewMemoryRetractionService().RetractSourcesTx(h.ctx, tx,
		[]MemorySourceRef{h.taskRef}, "member:u1", "task output deleted"))
	require.NoError(t, tx.Commit(h.ctx))

	ref := memorygraph.MemoryRef{Kind: memorygraph.MemoryRefStagingAtom, AtomID: h.atomID, SegmentID: h.segment}
	_, err = svc.Explore(h.ctx, h.workspace, "traj-fence", ref)
	require.ErrorIs(t, err, ErrMemorySourceRetracted)
	_, err = svc.Evidence(h.ctx, h.workspace, "traj-fence", ref)
	require.ErrorIs(t, err, ErrMemorySourceRetracted)
}

// Evidence is summary-first: the closing event and a bounded chunk of the
// sanitized trajectory, never the full payload.
func TestMemoryExploreV2_EvidenceIsSummaryFirstAndBounded(t *testing.T) {
	h := newExploreV2Harness(t, true)
	defer h.Close()
	svc := h.svc()
	_, err := svc.Start(h.ctx, h.workspace, "traj-evidence", h.planGraphs())
	require.NoError(t, err)

	ref := memorygraph.MemoryRef{Kind: memorygraph.MemoryRefStagingAtom, AtomID: h.atomID, SegmentID: h.segment}
	ev, err := svc.Evidence(h.ctx, h.workspace, "traj-evidence", ref)
	require.NoError(t, err)
	assert.Equal(t, h.segment, ev.SegmentID)
	assert.NotEmpty(t, ev.Summary)
	assert.LessOrEqual(t, len(ev.TrajectoryChunk), exploreEvidenceMaxChunkBytes,
		"trajectory chunks are bounded")
}

// Budgets are enforced from the persisted plan: exhausting the distinct
// segment ceiling refuses further exploration.
func TestMemoryExploreV2_BudgetsAreHardCeilings(t *testing.T) {
	h := newExploreV2Harness(t, true)
	defer h.Close()
	svc := h.svc()
	start, err := svc.Start(h.ctx, h.workspace, "traj-budget", h.planGraphs())
	require.NoError(t, err)

	ceiling := DefaultExploreBudgets().DistinctSegments
	for i := 0; i < ceiling; i++ {
		require.NoError(t, svc.consumeDistinctSegment(h.ctx, h.workspace, "traj-budget", "seg-"+string(rune('a'+i%26))+string(rune('a'+i/26))))
	}
	ref := memorygraph.MemoryRef{Kind: memorygraph.MemoryRefStagingAtom, AtomID: h.atomID, SegmentID: h.segment}
	_, err = svc.Explore(h.ctx, h.workspace, "traj-budget", ref)
	require.ErrorIs(t, err, ErrMemoryExploreBudgetExhausted, "the distinct-segment ceiling must refuse")
	_ = start
}

// Checkpoint rollover: after the watermark advances, a new start re-resolves
// the bounded prior against the new plan and keeps the prior bounded.
func TestMemoryExploreV2_CheckpointRolloverKeepsBoundedPrior(t *testing.T) {
	h := newExploreV2Harness(t, true)
	defer h.Close()
	svc := h.svc()
	_, err := svc.Start(h.ctx, h.workspace, "traj-rollover", h.planGraphs())
	require.NoError(t, err)
	ref := memorygraph.MemoryRef{Kind: memorygraph.MemoryRefStagingAtom, AtomID: h.atomID, SegmentID: h.segment}
	_, err = svc.Explore(h.ctx, h.workspace, "traj-rollover", ref)
	require.NoError(t, err)

	// Advance the watermark and start a rollover generation.
	h.publishForFence(t, "rollover-bump")
	second, err := svc.Start(h.ctx, h.workspace, "traj-rollover", h.planGraphs())
	require.NoError(t, err)
	assert.Greater(t, second.Plan.SegmentPublishSeqMax, int64(0))

	cp, err := svc.Checkpoint(h.ctx, h.workspace, "traj-rollover")
	require.NoError(t, err)
	assert.NotEmpty(t, cp.Prior, "the rollover carries a bounded prior")
	assert.LessOrEqual(t, len(cp.Prior), exploreCheckpointPriorLimit)

	// Stale refs re-resolve against the latest authorized plan.
	resolved, err := svc.Explore(h.ctx, h.workspace, "traj-rollover", ref)
	require.NoError(t, err)
	assert.True(t, len(resolved.Refs) > 0)
}

// History is bounded and reflects the authorized walk only.
func TestMemoryExploreV2_HistoryIsBounded(t *testing.T) {
	h := newExploreV2Harness(t, true)
	defer h.Close()
	svc := h.svc()
	_, err := svc.Start(h.ctx, h.workspace, "traj-history", h.planGraphs())
	require.NoError(t, err)
	ref := memorygraph.MemoryRef{Kind: memorygraph.MemoryRefStagingAtom, AtomID: h.atomID, SegmentID: h.segment}
	_, err = svc.Explore(h.ctx, h.workspace, "traj-history", ref)
	require.NoError(t, err)

	hist, err := svc.History(h.ctx, h.workspace, "traj-history")
	require.NoError(t, err)
	assert.NotEmpty(t, hist.Refs)
	assert.LessOrEqual(t, len(hist.Refs), exploreHistoryLimit)
}

// The gateway protocol fence: generation 2 requires BOTH the daemon-side
// capability and a green server gate — capability alone never authorizes a
// disabled server path, and a red gate never falls back to exposing v2
// payloads through v1.
func TestGraphMemoryAgentProtocolGeneration_Fence(t *testing.T) {
	h := newExploreV2Harness(t, false)
	defer h.Close()

	// No capability → generation 1 even with the gate green.
	h.enableExploreRoute(t)
	gen := ResolveGraphMemoryAgentProtocol(h.ctx, nil, h.pubPool, h.workspace)
	if gen != 1 {
		t.Fatalf("protocol generation without capability = %d, want 1", gen)
	}

	// Capability + green gate → generation 2.
	gen = ResolveGraphMemoryAgentProtocol(h.ctx, []string{"memory_explore_v2"}, h.pubPool, h.workspace)
	if gen != 2 {
		t.Fatalf("protocol generation with capability+gate = %d, want 2", gen)
	}

	// Capability + red gate → generation 1: v2 is unavailable, not exposed.
	_, err := db.New(h.pubPool).SetMemoryReadPhaseGate(h.ctx, db.SetMemoryReadPhaseGateParams{
		WorkspaceID: h.workspace, RetractionCanaryOk: false,
	})
	require.NoError(t, err)
	gen = ResolveGraphMemoryAgentProtocol(h.ctx, []string{"memory_explore_v2"}, h.pubPool, h.workspace)
	if gen != 1 {
		t.Fatalf("protocol generation with capability but red gate = %d, want 1", gen)
	}
}
