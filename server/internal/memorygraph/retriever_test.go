package memorygraph

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// newRetrieverFixture builds a store with one version containing three
// nodes plus one staging segment, each on a distinct topic.
func newRetrieverFixture(t *testing.T) *Store {
	t.Helper()
	store := newTestStore(t)
	nodes := []*Node{
		{NodeID: "n-dispatch", Body: "the dispatch router selects the cheapest model for batch jobs"},
		{NodeID: "n-embedcache", Body: "the embedding cache is shared across memory graph versions"},
		{NodeID: "n-judge", Body: "the judge agent scores recalled nodes asynchronously"},
	}
	now := time.Now().UTC()
	for _, n := range nodes {
		n.CreatedBy = CreatorIngester
		n.CreatedVersion = 1
		n.UpdatedVersion = 1
		n.ObservedAt = now
		if err := store.SaveNode(1, n); err != nil {
			t.Fatalf("SaveNode %s: %v", n.NodeID, err)
		}
	}
	if err := store.WriteStagingSegment("seg-staging1", []byte("staging summary about sandbox browser automation tooling")); err != nil {
		t.Fatalf("WriteStagingSegment: %v", err)
	}
	return store
}

func TestHybridRetrieverBM25Only(t *testing.T) {
	store := newRetrieverFixture(t)
	r := NewHybridRetriever(store, nil, DefaultRetrievalConfig())
	if err := r.Rebuild(context.Background()); err != nil {
		t.Fatalf("Rebuild: %v", err)
	}

	hits, err := r.Search(context.Background(), "embedding cache versions")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) == 0 {
		t.Fatalf("Search returned no hits")
	}
	if hits[0].ID != "n-embedcache" {
		t.Fatalf("top hit = %q, want n-embedcache (hits: %v)", hits[0].ID, hits)
	}
	for _, h := range hits {
		if h.Score <= 0 || h.Score > 1 {
			t.Fatalf("score %f out of (0,1] for %s", h.Score, h.ID)
		}
	}

	// Staging segments are excluded from the default corpus (Task 22): a
	// staging-only topic yields no hits, and no staging id can appear.
	hits, err = r.Search(context.Background(), "sandbox browser automation")
	if err != nil {
		t.Fatalf("Search staging: %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("staging topic surfaced %v, want no hits outside the atom channel", hits)
	}
	if IsStagingID("n-judge") {
		t.Fatalf("IsStagingID(n-judge) = true")
	}
	if !IsStagingID("seg:seg-staging1") {
		t.Fatalf("IsStagingID(seg:seg-staging1) = false")
	}
}

func TestHybridRetrieverMergesBothChannels(t *testing.T) {
	store := newRetrieverFixture(t)
	emb := mustCachedEmbedder(t, NewHashEmbedder(), store)
	r := NewHybridRetriever(store, emb, DefaultRetrievalConfig())
	if err := r.Rebuild(context.Background()); err != nil {
		t.Fatalf("Rebuild: %v", err)
	}

	hits, err := r.Search(context.Background(), "embedding cache versions")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) == 0 {
		t.Fatalf("Search returned no hits")
	}
	if hits[0].ID != "n-embedcache" {
		t.Fatalf("top hit = %q, want n-embedcache (hits: %v)", hits[0].ID, hits)
	}
	// The vector channel contributes too: every score is a weighted blend
	// of the lexical and cosine channels.
	for _, h := range hits {
		if h.Score <= 0 || h.Score > 1 {
			t.Fatalf("score %f out of (0,1] for %s", h.Score, h.ID)
		}
	}

	// A query matching only the staging doc yields nothing in hybrid mode
	// too: staging segments are outside the default corpus in both channels.
	hits, err = r.Search(context.Background(), "sandbox browser automation tooling")
	if err != nil {
		t.Fatalf("Search staging: %v", err)
	}
	for _, h := range hits {
		if h.ID == "seg:seg-staging1" {
			t.Fatalf("staging doc retrievable in hybrid mode: %v", hits)
		}
	}
}

func TestHybridRetrieverTopKLimit(t *testing.T) {
	store := newRetrieverFixture(t)
	cfg := DefaultRetrievalConfig()
	cfg.TopK = 2
	emb := mustCachedEmbedder(t, NewHashEmbedder(), store)
	r := NewHybridRetriever(store, emb, cfg)
	if err := r.Rebuild(context.Background()); err != nil {
		t.Fatalf("Rebuild: %v", err)
	}
	// The vector channel covers all graph nodes, so TopK must truncate.
	hits, err := r.Search(context.Background(), "memory graph versions")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != cfg.TopK {
		t.Fatalf("Search returned %d hits, want exactly %d: %v", len(hits), cfg.TopK, hits)
	}
}

// SearchAt's graph channel surfaces only current-version graph nodes under
// the caller's view: staging segment docs are never promoted to graph hits
// (they are reachable only through the atom ledger), and view-invisible
// nodes are dropped before scoring.
func TestHybridRetrieverSearchAtGraphChannelIsCurrentNodesOnly(t *testing.T) {
	store := newRetrieverFixture(t)
	chanNode := &Node{
		NodeID: "n-chan", Body: "the channel runner fan-outs jobs to channel workers",
		Visibility: "channel", ChannelID: "chan-a",
		CreatedBy: CreatorIngester, CreatedVersion: 1, UpdatedVersion: 1,
		ObservedAt: time.Now().UTC(),
	}
	if err := store.SaveNode(1, chanNode); err != nil {
		t.Fatalf("SaveNode chan: %v", err)
	}
	r := NewHybridRetriever(store, nil, DefaultRetrievalConfig())
	if err := r.Rebuild(context.Background()); err != nil {
		t.Fatalf("Rebuild: %v", err)
	}

	// Project view: "sandbox browser automation" matches ONLY the staging
	// segment, which must not surface as a graph hit — so a query with no
	// graph match yields no hits at all rather than a promoted staging doc.
	hits, err := r.SearchAt(context.Background(), "sandbox browser automation",
		GraphView{AllowProject: true}, 0)
	if err != nil {
		t.Fatalf("SearchAt: %v", err)
	}
	require.Empty(t, hits, "staging segments must never surface as graph hits")

	// A query that matches a graph node returns it as a graph_node hit; the
	// channel node stays invisible to the project view.
	hits, err = r.SearchAt(context.Background(), "embedding cache versions",
		GraphView{AllowProject: true}, 0)
	if err != nil {
		t.Fatalf("SearchAt graph: %v", err)
	}
	require.NotEmpty(t, hits)
	for _, h := range hits {
		require.Equal(t, SearchGraphNode, h.Class, "no atom snapshot installed: every hit is a graph node")
		require.False(t, IsStagingID(h.Ref.NodeID))
		require.NotEqual(t, "n-chan", h.Ref.NodeID)
		require.Equal(t, MemoryRefGraphNode, h.Ref.Kind)
	}

	// Channel view sees its own node and nothing else.
	chanHits, err := r.SearchAt(context.Background(), "channel runner fan-outs",
		GraphView{ChannelID: "chan-a"}, 0)
	if err != nil {
		t.Fatalf("SearchAt channel: %v", err)
	}
	require.NotEmpty(t, chanHits)
	for _, h := range chanHits {
		if h.Class == SearchGraphNode {
			require.Equal(t, "n-chan", h.Ref.NodeID)
		}
	}

	// A different channel sees nothing at all.
	other, err := r.SearchAt(context.Background(), "channel runner fan-outs",
		GraphView{ChannelID: "chan-b"}, 0)
	if err != nil {
		t.Fatalf("SearchAt other channel: %v", err)
	}
	require.Empty(t, other)
}

// TestHybridRetrieverDefaultCorpusExcludesStagingDocs pins the spec §9.1
// default corpus (Task 22 cleanup): the default Search indexes graph nodes
// plus active Atoms, never every staging file. Staging docs previously
// entered the index unconditionally and carried no scope sidecar, so a
// shared Project Graph could leak exact-channel material through them.
func TestHybridRetrieverDefaultCorpusExcludesStagingDocs(t *testing.T) {
	store := newRetrieverFixture(t)
	r := NewHybridRetriever(store, nil, DefaultRetrievalConfig())
	if err := r.Rebuild(context.Background()); err != nil {
		t.Fatalf("Rebuild: %v", err)
	}

	hits, err := r.Search(context.Background(), "sandbox browser automation")
	if err != nil {
		t.Fatalf("Search staging-only topic: %v", err)
	}
	for _, h := range hits {
		if IsStagingID(h.ID) {
			t.Fatalf("staging doc %q surfaced in the default corpus (hits: %v)", h.ID, hits)
		}
	}

	// Graph nodes remain the default corpus's graph channel.
	hits, err = r.Search(context.Background(), "embedding cache versions")
	if err != nil {
		t.Fatalf("Search graph topic: %v", err)
	}
	if len(hits) == 0 || hits[0].ID != "n-embedcache" {
		t.Fatalf("graph node not retrievable: %v", hits)
	}

	// Atoms remain the staging channel of the default corpus: an installed
	// active atom is searchable through SearchAt even while the raw staging
	// file is excluded from the index.
	r.InstallAtomSnapshot([]AtomDoc{{AtomID: "atom-1", SegmentID: "seg-staging1",
		Body: "atom summary about sandbox browser automation", ChannelID: "ch-1"}}, 0, nil)
	atHits, err := r.SearchAt(context.Background(), "sandbox browser automation",
		GraphView{AllowProject: true, ChannelID: "ch-1"}, 0)
	if err != nil {
		t.Fatalf("SearchAt after atom install: %v", err)
	}
	found := false
	for _, h := range atHits {
		if h.Ref.Kind == MemoryRefStagingAtom && h.Ref.AtomID == "atom-1" {
			found = true
		}
	}
	if !found {
		t.Fatalf("active atom not retrievable after install: %v", atHits)
	}
}
