// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/multica-ai/multica/server/internal/memorygraph"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// explorePlanHarness: retraction harness (454+464+466+467) plus migration
// 468 (plan ledger).
type explorePlanHarness struct {
	*retractionHarness
}

func newExplorePlanHarness(t *testing.T) *explorePlanHarness {
	t.Helper()
	h := &explorePlanHarness{retractionHarness: newRetractionHarness(t)}
	applyMemoryExploreV2Migration(t, h.ctx, h.conn)
	return h
}

func (h *explorePlanHarness) enableExploreRoute(t *testing.T) {
	t.Helper()
	q := db.New(h.pubPool)
	_, err := q.InsertMemoryReadPhaseGate(h.ctx, h.workspace)
	require.NoError(t, err)
	_, setErr := q.SetMemoryReadPhaseGate(h.ctx, db.SetMemoryReadPhaseGateParams{
		WorkspaceID:    h.workspace,
		ExploreEnabled: true, RetractionCanaryOk: true,
	})
	require.NoError(t, setErr)
}

func (h *explorePlanHarness) planGraphs() []PinnedGraph {
	return []PinnedGraph{{Kind: "channel", OwnerID: h.channel.String(), Generation: 1}}
}

// A disabled memory_explore_v2 gate must refuse to create a plan and leave
// no row behind — disabled mode cannot create a user/Agent trajectory.
func TestMemoryExplorePlan_DisabledGateIsUnavailableAndPersistsNothing(t *testing.T) {
	h := newExplorePlanHarness(t)
	defer h.Close()

	svc := NewMemoryExplorePlanService(h.pubPool)
	_, err := svc.CreatePlan(h.ctx, h.workspace, "traj-disabled", h.planGraphs())
	require.ErrorIs(t, err, ErrMemoryRouteDisabled)
	assert.Equal(t, 0, h.countRows(t,
		`SELECT count(*) FROM memory_explore_plan WHERE trajectory_id=$1`, "traj-disabled"))

	// Reads are gated too.
	_, err = svc.GetPlan(h.ctx, h.workspace, "traj-disabled")
	require.ErrorIs(t, err, ErrMemoryRouteDisabled)
}

// A replayed start for the same trajectory is idempotent: one row, the same
// watermarks, and a rollover counter that proves the replay happened.
func TestMemoryExplorePlan_ReplayedStartIsIdempotent(t *testing.T) {
	h := newExplorePlanHarness(t)
	defer h.Close()
	h.enableExploreRoute(t)

	svc := NewMemoryExplorePlanService(h.pubPool)
	first, err := svc.CreatePlan(h.ctx, h.workspace, "traj-replay", h.planGraphs())
	require.NoError(t, err)
	second, err := svc.CreatePlan(h.ctx, h.workspace, "traj-replay", h.planGraphs())
	require.NoError(t, err)

	assert.Equal(t, first.TrajectoryID, second.TrajectoryID)
	assert.Equal(t, first.SegmentPublishSeqMax, second.SegmentPublishSeqMax)
	assert.Equal(t, first.InteractionEdgeSeqMax, second.InteractionEdgeSeqMax)
	assert.Equal(t, 1, h.countRows(t,
		`SELECT count(*) FROM memory_explore_plan WHERE trajectory_id=$1`, "traj-replay"))
	assert.Equal(t, 1, h.countRows(t,
		`SELECT rollover_count FROM memory_explore_plan WHERE trajectory_id=$1`, "traj-replay"))
}

// Publishing after a plan was created advances the watermark on rollover —
// a new start for a NEW trajectory freezes the higher ceiling.
func TestMemoryExplorePlan_WatermarkAdvancesOnRollover(t *testing.T) {
	h := newExplorePlanHarness(t)
	defer h.Close()
	h.enableExploreRoute(t)

	svc := NewMemoryExplorePlanService(h.pubPool)
	before, err := svc.CreatePlan(h.ctx, h.workspace, "traj-wm-1", h.planGraphs())
	require.NoError(t, err)

	// Publish one more segment: the workspace watermark must move.
	h.publishForFence(t, "watermark-bump")

	after, err := svc.CreatePlan(h.ctx, h.workspace, "traj-wm-2", h.planGraphs())
	require.NoError(t, err)
	assert.Greater(t, after.SegmentPublishSeqMax, before.SegmentPublishSeqMax,
		"the frozen publish watermark must advance after a new publish")

	got, err := svc.GetPlan(h.ctx, h.workspace, "traj-wm-2")
	require.NoError(t, err)
	assert.Equal(t, after.SegmentPublishSeqMax, got.SegmentPublishSeqMax)
	assert.Equal(t, DefaultExploreBudgets(), got.Budgets)
}

// Plan validation happens before persistence: forged graph identities and
// malformed trajectories never reach the ledger.
func TestMemoryExplorePlan_RejectsForgedPinnedGraphs(t *testing.T) {
	h := newExplorePlanHarness(t)
	defer h.Close()
	h.enableExploreRoute(t)

	svc := NewMemoryExplorePlanService(h.pubPool)
	for _, tc := range []struct {
		name       string
		trajectory string
		graphs     []PinnedGraph
	}{
		{"empty trajectory", "   ", h.planGraphs()},
		{"oversize trajectory", padExplore(129), h.planGraphs()},
		{"no graphs", "traj-x", nil},
		{"unknown graph kind", "traj-x", []PinnedGraph{{Kind: "workspace", OwnerID: h.channel.String(), Generation: 1}}},
		{"bad owner uuid", "traj-x", []PinnedGraph{{Kind: "channel", OwnerID: "not-a-uuid", Generation: 1}}},
		{"zero generation", "traj-x", []PinnedGraph{{Kind: "project", OwnerID: h.channel.String(), Generation: 0}}},
	} {
		_, err := svc.CreatePlan(h.ctx, h.workspace, tc.trajectory, tc.graphs)
		require.Error(t, err, tc.name)
	}
	assert.Equal(t, 0, h.countRows(t, `SELECT count(*) FROM memory_explore_plan`))
}

// Authorization comes from the plan, never from the ref: resolving a ref
// whose graph is not pinned fails closed, and a fenced source fails closed
// through the Task 8A registry — even for a correctly-shaped ref.
func TestMemoryExplorePlan_ResolveRefAuthorizesFromPlanAndFence(t *testing.T) {
	h := newExplorePlanHarness(t)
	defer h.Close()
	h.enableExploreRoute(t)

	// Publish atoms on the harness channel graph, then fence one task's
	// source.
	taskRef, _ := h.publishForFence(t, "resolve-fence")
	var atomID, segmentID string
	require.NoError(t, h.conn.QueryRow(h.ctx, `
		SELECT atom_id, segment_id FROM graph_memory_atom
		WHERE workspace_id=$1 ORDER BY atom_id LIMIT 1`, h.workspace).Scan(&atomID, &segmentID))
	require.NotEmpty(t, atomID)

	tx, err := h.pubPool.Begin(h.ctx)
	require.NoError(t, err)
	require.NoError(t, NewMemoryRetractionService().RetractSourcesTx(h.ctx, tx,
		[]MemorySourceRef{taskRef}, "member:u1", "task output deleted"))
	require.NoError(t, tx.Commit(h.ctx))

	svc := NewMemoryExplorePlanService(h.pubPool)
	// The harness publishes under channel scope; pin that channel.
	plan, err := svc.CreatePlan(h.ctx, h.workspace, "traj-resolve", h.planGraphs())
	require.NoError(t, err)

	fencedRef := memorygraph.MemoryRef{Kind: memorygraph.MemoryRefStagingAtom, AtomID: atomID, SegmentID: segmentID}
	require.NoError(t, memorygraph.ValidateMemoryRef(fencedRef))
	_, err = svc.ResolveRef(h.ctx, h.workspace, plan, fencedRef)
	require.ErrorIs(t, err, ErrMemorySourceRetracted)

	// A ref whose graph is not in the plan is unauthorized regardless of
	// its own fields.
	otherPlan := plan
	otherPlan.Graphs = []PinnedGraph{{Kind: "project", OwnerID: uuid.NewString(), Generation: 3}}
	_, err = svc.ResolveRef(h.ctx, h.workspace, otherPlan, fencedRef)
	require.ErrorIs(t, err, ErrMemoryRefUnauthorized)
}

func padExplore(n int) string {
	out := make([]byte, n)
	for i := range out {
		out[i] = 'x'
	}
	return string(out)
}

// applyMemoryExploreV2Migration applies migration 468 into the harness
// schema (same pattern as the 466/467 appliers).
func applyMemoryExploreV2Migration(t *testing.T, ctx context.Context, conn *pgxpool.Conn) {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate explore plan test")
	}
	path := filepath.Join(filepath.Dir(filename), "..", "..", "migrations", "483_memory_explore_v2.up.sql")
	migration, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read migration 468: %v", err)
	}
	if _, err := conn.Exec(ctx, string(migration)); err != nil {
		t.Fatalf("apply migration 468: %v", err)
	}
}
