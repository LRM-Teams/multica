package memorygraph

import (
	"context"
	"testing"
	"time"
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

	// Staging docs are indexed and carry the seg: prefix.
	hits, err = r.Search(context.Background(), "sandbox browser automation")
	if err != nil {
		t.Fatalf("Search staging: %v", err)
	}
	if len(hits) == 0 || hits[0].ID != "seg:seg-staging1" {
		t.Fatalf("staging hit = %v, want seg:seg-staging1 first", hits)
	}
	if !IsStagingID(hits[0].ID) {
		t.Fatalf("IsStagingID(%q) = false", hits[0].ID)
	}
	if IsStagingID("n-judge") {
		t.Fatalf("IsStagingID(n-judge) = true")
	}
}

func TestHybridRetrieverMergesBothChannels(t *testing.T) {
	store := newRetrieverFixture(t)
	emb := NewCachedEmbedder(NewHashEmbedder(), store)
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

	// A query matching only the staging doc must surface it through the
	// merged channels as well.
	hits, err = r.Search(context.Background(), "sandbox browser automation tooling")
	if err != nil {
		t.Fatalf("Search staging: %v", err)
	}
	sawStaging := false
	for _, h := range hits {
		if h.ID == "seg:seg-staging1" {
			sawStaging = true
		}
	}
	if !sawStaging {
		t.Fatalf("staging doc not retrievable in hybrid mode: %v", hits)
	}
}

func TestHybridRetrieverTopKLimit(t *testing.T) {
	store := newRetrieverFixture(t)
	cfg := DefaultRetrievalConfig()
	cfg.TopK = 2
	emb := NewCachedEmbedder(NewHashEmbedder(), store)
	r := NewHybridRetriever(store, emb, cfg)
	if err := r.Rebuild(context.Background()); err != nil {
		t.Fatalf("Rebuild: %v", err)
	}
	// The vector channel covers all four docs, so TopK must truncate.
	hits, err := r.Search(context.Background(), "memory graph versions")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != cfg.TopK {
		t.Fatalf("Search returned %d hits, want exactly %d: %v", len(hits), cfg.TopK, hits)
	}
}
