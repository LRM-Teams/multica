// SPDX-License-Identifier: Apache-2.0

package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/multica-ai/multica/server/internal/memorygraph"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// seedFixtureGraph writes one graph node into a scoped store dir so the
// seeder's graph channel has something to hit.
func seedFixtureGraph(t *testing.T, dir, channelID string, version int) {
	t.Helper()
	store := memorygraph.NewStore(dir)
	require.NoError(t, store.Init())
	require.NoError(t, store.SaveNode(version, &memorygraph.Node{
		NodeID: "n-dispatch", Body: "the dispatch router selects the cheapest model",
		CreatedBy: memorygraph.CreatorIngester, CreatedVersion: version, UpdatedVersion: version,
		Visibility: "channel", ChannelID: channelID,
	}))
}

// The atom_search adoption gate governs the recall seed retriever: with the
// gate red (or no pool wired) the seeder is byte-identical to the legacy
// hybrid seeder — old inject/v1 behavior is preserved — while a green gate
// adopts the class-aware SearchAt channel with retracted atoms excluded.
func TestGraphMemoryRecallSeeder_AtomSearchAdoptionGate(t *testing.T) {
	h := newExploreV2Harness(t, false)
	defer h.Close()
	dir := t.TempDir()
	seedFixtureGraph(t, dir, h.channel.String(), 1)
	view := memorygraph.GraphView{ChannelID: h.channel.String()}

	legacy := GraphMemoryHybridSeeder{}
	legacySeeds, err := legacy.Seeds(h.ctx, h.workspace.String(), dir, 1, "dispatch router cheapest model", view)
	require.NoError(t, err)
	require.NotEmpty(t, legacySeeds, "fixture must produce graph hits")

	// Gate red: the gated seeder equals the legacy seeder exactly.
	// (The harness starts with every route disabled.)
	gated := NewGraphMemoryAtomSearchSeeder(h.pubPool)
	redSeeds, err := gated.Seeds(h.ctx, h.workspace.String(), dir, 1, "dispatch router cheapest model", view)
	require.NoError(t, err)
	assert.Equal(t, legacySeeds, redSeeds, "red atom_search gate must preserve legacy seed behavior")

	// Gate green: the atom channel joins, and retracted atoms never surface.
	h.enableAtomsRoute(t)
	greenSeeds, err := gated.Seeds(h.ctx, h.workspace.String(), dir, 1, "dispatch router cheapest model", view)
	require.NoError(t, err)
	assert.NotEmpty(t, greenSeeds, "graph-class hits still seed the walk under a green gate")
	for _, seed := range greenSeeds {
		assert.False(t, memorygraph.IsStagingID(seed), "staging ids must never seed the graph walk: %q", seed)
	}

	// The published atoms are visible to the class-aware channel while
	// unfenced; fencing their source removes them from the snapshot and
	// records them in the re-assertion set.
	docs, _, retracted, err := LoadActiveAtomSnapshot(h.ctx, h.pubPool, h.workspace, h.channel.String(), 64)
	require.NoError(t, err)
	require.NotEmpty(t, docs, "fixture must have published atoms")
	assert.Empty(t, retracted, "nothing is retracted yet")

	tx, err := h.pubPool.Begin(h.ctx)
	require.NoError(t, err)
	defer tx.Rollback(h.ctx)
	require.NoError(t, NewMemoryRetractionService().RetractSourcesTx(h.ctx, tx,
		[]MemorySourceRef{h.taskRef}, "user:1", "comment deleted"))
	require.NoError(t, tx.Commit(h.ctx))

	docs, _, retracted, err = LoadActiveAtomSnapshot(h.ctx, h.pubPool, h.workspace, h.channel.String(), 64)
	require.NoError(t, err)
	assert.Empty(t, docs, "a fenced source's atoms must leave the snapshot")
	assert.NotEmpty(t, retracted, "the loader hands the retraction set to the retriever's local re-assertion")
}

// Shadow-disabled mode records only an aggregate comparison — never content.
func TestGraphMemoryRecallSeeder_ShadowComparisonIsAggregateOnly(t *testing.T) {
	h := newExploreV2Harness(t, false)
	defer h.Close()
	dir := t.TempDir()
	seedFixtureGraph(t, dir, h.channel.String(), 1)
	view := memorygraph.GraphView{ChannelID: h.channel.String()}

	gated := NewGraphMemoryAtomSearchSeeder(h.pubPool)
	_, err := gated.Seeds(h.ctx, h.workspace.String(), dir, 1, "dispatch router cheapest model", view)
	require.NoError(t, err)

	comparison := gated.LastShadowComparison()
	// Red gate: the comparison exists, is aggregate-only, and matches the
	// legacy seed count without carrying any query or id content.
	require.NotNil(t, comparison)
	assert.Equal(t, 0, comparison.AtomHits, "no atom ledger snapshot is adopted while the gate is red")
	assert.False(t, comparison.Adopted)
	for _, field := range []string{h.channel.String(), "dispatch router"} {
		assert.NotContains(t, comparison.Describe(), field, "aggregate comparison must not carry content")
	}
}

func (h *exploreV2Harness) enableAtomsRoute(t *testing.T) {
	t.Helper()
	q := db.New(h.pubPool)
	_, err := q.InsertMemoryReadPhaseGate(h.ctx, h.workspace)
	require.NoError(t, err)
	_, err = q.SetMemoryReadPhaseGate(h.ctx, db.SetMemoryReadPhaseGateParams{
		WorkspaceID: h.workspace, AtomsEnabled: true, RetractionCanaryOk: true,
	})
	require.NoError(t, err)
}
