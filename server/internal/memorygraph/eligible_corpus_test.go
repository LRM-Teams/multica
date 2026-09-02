// SPDX-License-Identifier: Apache-2.0

package memorygraph

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// eligibleCorpusFixture builds a corpus where every invisible node
// out-scores every visible one on the same query, the exact shape that
// under-fills a rank-then-filter pipeline: a TopK cut taken before the
// eligibility filter would return only invisible docs and drop to zero
// hits after filtering.
func eligibleCorpusFixture(t *testing.T) (*Store, []string, []string) {
	t.Helper()
	store := newTestStore(t)
	now := time.Now().UTC()
	channelIDs := make([]string, 0, 12)
	projectIDs := make([]string, 0, 10)
	for i := 0; i < 12; i++ {
		id := fmt.Sprintf("n-channel-%02d", i)
		channelIDs = append(channelIDs, id)
		n := &Node{
			NodeID: id,
			// High term saturation: the query words repeated many times.
			Body:       "quokka telemetry pivot quokka telemetry pivot quokka telemetry pivot quokka telemetry pivot",
			Visibility: "channel",
			ChannelID:  "ch-hidden",
		}
		n.CreatedBy = CreatorIngester
		n.CreatedVersion = 1
		n.UpdatedVersion = 1
		n.ObservedAt = now
		require.NoError(t, store.SaveNode(1, n))
	}
	for i := 0; i < 10; i++ {
		id := fmt.Sprintf("n-project-%02d", i)
		projectIDs = append(projectIDs, id)
		n := &Node{
			NodeID: id,
			// Weak match: one query term, once.
			Body:       fmt.Sprintf("quokka sighting record number %d", i),
			Visibility: "project",
		}
		n.CreatedBy = CreatorIngester
		n.CreatedVersion = 1
		n.UpdatedVersion = 1
		n.ObservedAt = now
		require.NoError(t, store.SaveNode(1, n))
	}
	return store, channelIDs, projectIDs
}

func eligibleProjectView() RetrievalConfig {
	cfg := DefaultRetrievalConfig() // TopK = 10
	cfg.View = GraphView{AllowProject: true}
	return cfg
}

// TestSearchEligibleBeforeRank pins the Slice 1.2 invariant for Search:
// eligibility (scope + role) is applied before the TopK cut, so invisible
// high scorers can never crowd visible docs out of the result set, and the
// rank-output recheck keeps every hit visible.
func TestSearchEligibleBeforeRank(t *testing.T) {
	store, channelIDs, projectIDs := eligibleCorpusFixture(t)

	r := NewHybridRetriever(store, nil, eligibleProjectView())
	require.NoError(t, r.Rebuild(context.Background()))

	hits, err := r.Search(context.Background(), "quokka telemetry pivot")
	require.NoError(t, err)
	require.Len(t, hits, len(projectIDs),
		"the visible corpus has exactly %d matching docs and none may be displaced by invisible ones", len(projectIDs))
	returned := map[string]bool{}
	for _, hit := range hits {
		returned[hit.ID] = true
		// Defense-in-depth recheck: no invisible or channel-scoped doc leaked.
		assert.NotContains(t, channelIDs, hit.ID)
	}
	for _, id := range projectIDs {
		assert.True(t, returned[id], "visible doc %s missing from the full-K result", id)
	}
}

// TestSearchBM25DegradationKeepsEligibility: the deterministic BM25-only
// degradation (query-time embedder outage, or a BM25-only retriever) runs
// the same eligible corpus — degradation never widens permissions.
func TestSearchBM25DegradationKeepsEligibility(t *testing.T) {
	store, _, projectIDs := eligibleCorpusFixture(t)

	// BM25-only retriever built with the same view: this is the exact
	// state a query-time embedding outage degrades to.
	r := NewHybridRetriever(store, nil, eligibleProjectView())
	require.NoError(t, r.Rebuild(context.Background()))

	hits, err := r.Search(context.Background(), "quokka telemetry pivot")
	require.NoError(t, err)
	require.Len(t, hits, len(projectIDs))
	for _, hit := range hits {
		assert.Contains(t, projectIDs, hit.ID)
	}
}

// TestSearchAtEligibleBeforeRankBothChannels pins the same invariant for
// SearchAt's graph channel and for the staging-atom channel: invisible
// channel-scope candidates on either channel cannot displace eligible
// project-scope candidates from the fused TopK.
func TestSearchAtEligibleBeforeRankBothChannels(t *testing.T) {
	store, _, projectIDs := eligibleCorpusFixture(t)

	r := NewHybridRetriever(store, nil, DefaultRetrievalConfig())
	require.NoError(t, r.Rebuild(context.Background()))

	// Atom ledger: 12 hidden channel atoms out-scoring 10 project atoms.
	atoms := make([]AtomDoc, 0, 22)
	for i := 0; i < 12; i++ {
		atoms = append(atoms, AtomDoc{
			AtomID:     fmt.Sprintf("atom-channel-%02d", i),
			SegmentID:  "seg-a",
			Body:       "quokka telemetry pivot quokka telemetry pivot quokka telemetry pivot",
			PublishSeq: int64(i + 1),
			CreatedAt:  time.Now().UTC(),
			ChannelID:  "ch-hidden",
		})
	}
	for i := 0; i < 10; i++ {
		atoms = append(atoms, AtomDoc{
			AtomID:     fmt.Sprintf("atom-project-%02d", i),
			SegmentID:  "seg-a",
			Body:       fmt.Sprintf("quokka sighting record number %d", i),
			PublishSeq: int64(100 + i),
			CreatedAt:  time.Now().UTC(),
			ChannelID:  "",
		})
	}
	r.InstallAtomSnapshot(atoms, 200, nil)

	hits, err := r.SearchAt(context.Background(), "quokka telemetry pivot",
		GraphView{AllowProject: true}, 200)
	require.NoError(t, err)

	graphHits := map[string]bool{}
	atomHits := map[string]bool{}
	for _, hit := range hits {
		switch hit.Class {
		case SearchGraphNode:
			graphHits[hit.Ref.NodeID] = true
			assert.NotEqual(t, "ch-hidden", hit.Ref.ChannelID)
		case SearchAtom:
			atomHits[hit.Ref.AtomID] = true
			assert.Empty(t, hit.Ref.ChannelID, "a hidden-channel atom leaked into the atom channel")
		}
	}
	// The graph channel must return its full visible TopK (10 project
	// nodes), not an under-filled remainder behind invisible high scorers.
	require.Len(t, graphHits, len(projectIDs))
	for _, id := range projectIDs {
		assert.True(t, graphHits[id], "visible graph node %s displaced from the graph channel", id)
	}
	// The atom channel keeps its eligible atoms too: the fused TopK is 10,
	// so with 10 graph hits at the higher class prior the atoms compete on
	// score; the invariant under test is that no invisible candidate took
	// any of those 10 slots, and every returned atom is project-scoped.
	projectAtomIDs := map[string]bool{}
	for i := 0; i < 10; i++ {
		projectAtomIDs[fmt.Sprintf("atom-project-%02d", i)] = true
	}
	for atomID := range atomHits {
		assert.True(t, projectAtomIDs[atomID], "invisible or unexpected atom hit %s", atomID)
	}
}

// TestSearchFilteredPreservesCorpusStatistics: filtering must not change
// scoring statistics — with every document eligible the filtered search is
// byte-identical to the unfiltered one (same ids, same scores, same order).
func TestSearchFilteredPreservesCorpusStatistics(t *testing.T) {
	store, _, _ := eligibleCorpusFixture(t)

	r := NewHybridRetriever(store, nil, DefaultRetrievalConfig())
	require.NoError(t, r.Rebuild(context.Background()))

	unfiltered := r.bm25.Search("quokka telemetry pivot", 10)
	filtered := r.bm25.SearchFiltered("quokka telemetry pivot", 10, nil)
	require.Equal(t, unfiltered, filtered)

	// A predicate that admits everything behaves identically too.
	admitAll := r.bm25.SearchFiltered("quokka telemetry pivot", 10, func(string) bool { return true })
	require.Equal(t, unfiltered, admitAll)

	// An exclusive predicate selects the top scores of the admitted subset
	// only: the top hit overall is a channel doc, so admitting only project
	// ids must change the top hit.
	projectOnly := r.bm25.SearchFiltered("quokka telemetry pivot", 10, func(id string) bool {
		return len(id) > len("n-channel-") && id[:len("n-project")] == "n-project"
	})
	require.NotEmpty(t, projectOnly)
	assert.Equal(t, "n-project-00", projectOnly[0].ID)
	assert.Greater(t, unfiltered[0].Score, projectOnly[0].Score,
		"corpus-wide IDF must keep the saturated channel doc above the weak project docs")
}
