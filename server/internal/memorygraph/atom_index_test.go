// SPDX-License-Identifier: Apache-2.0

package memorygraph

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newAtomSearchFixture builds a retriever over an empty in-memory-scoped
// store with BM25 only (no embedder), plus a fresh atom index.
func newAtomSearchFixture(t *testing.T) *HybridRetriever {
	t.Helper()
	store := NewStore(t.TempDir())
	if err := store.Init(); err != nil {
		t.Fatalf("init store: %v", err)
	}
	r := NewHybridRetriever(store, nil, DefaultRetrievalConfig())
	if err := r.Rebuild(context.Background()); err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	return r
}

func atomFixture(id, segment, body string, seq int64, age time.Duration, channel string) AtomDoc {
	return AtomDoc{
		AtomID: id, SegmentID: segment, Body: body,
		PublishSeq: seq, CreatedAt: time.Now().Add(-age), ChannelID: channel,
	}
}

// SearchAt must never surface retracted or consumed atoms, even when the
// snapshot loader passed them in (Task 9 Step 1: exclusion is proven at the
// retrieval layer, not trusted from the loader).
func TestAtomIndex_SearchAt_ExcludesRetractedAndConsumedAtoms(t *testing.T) {
	r := newAtomSearchFixture(t)
	r.InstallAtomSnapshot([]AtomDoc{
		atomFixture("atom-live", "seg-1", "NIMBUS rotor speed fact", 5, time.Hour, ""),
		atomFixture("atom-retracted", "seg-1", "NIMBUS rotor speed fact retracted copy", 5, time.Hour, ""),
		{AtomID: "atom-consumed", SegmentID: "seg-1", Body: "NIMBUS rotor speed fact consumed copy", PublishSeq: 5, Consumed: true},
	}, 10, map[string]bool{"atom-retracted": true})

	hits, err := r.SearchAt(context.Background(), "NIMBUS rotor speed",
		GraphView{AllowProject: true}, 10)
	require.NoError(t, err)
	ids := make([]string, 0, len(hits))
	for _, h := range hits {
		ids = append(ids, h.Ref.AtomID)
	}
	assert.Contains(t, ids, "atom-live")
	assert.NotContains(t, ids, "atom-retracted")
	assert.NotContains(t, ids, "atom-consumed")
}

// Atoms published after the caller's publish_seq ceiling stay invisible
// (the snapshot is only readable up to the frozen watermark).
func TestAtomIndex_SearchAt_PublishSeqCeiling(t *testing.T) {
	r := newAtomSearchFixture(t)
	r.InstallAtomSnapshot([]AtomDoc{
		atomFixture("atom-old", "seg-1", "kayak stroke rate fact", 4, time.Hour, ""),
		atomFixture("atom-new", "seg-2", "kayak stroke rate fact newer", 9, time.Hour, ""),
	}, 10, nil)

	hits, err := r.SearchAt(context.Background(), "kayak stroke",
		GraphView{AllowProject: true}, 5)
	require.NoError(t, err)
	ids := make([]string, 0, len(hits))
	for _, h := range hits {
		ids = append(ids, h.Ref.AtomID)
	}
	assert.Contains(t, ids, "atom-old")
	assert.NotContains(t, ids, "atom-new")
}

// Channel atoms are partitioned exactly: a channel view sees only its own
// channel's atoms; a project view sees only project-scoped atoms.
func TestAtomIndex_SearchAt_ExactChannelFilter(t *testing.T) {
	r := newAtomSearchFixture(t)
	r.InstallAtomSnapshot([]AtomDoc{
		atomFixture("atom-a", "seg-1", "zephyr sail trim fact", 3, time.Hour, "chan-a"),
		atomFixture("atom-b", "seg-2", "zephyr sail trim fact other channel", 3, time.Hour, "chan-b"),
		atomFixture("atom-p", "seg-3", "zephyr sail trim fact project", 3, time.Hour, ""),
	}, 10, nil)

	chanHits, err := r.SearchAt(context.Background(), "zephyr sail",
		GraphView{ChannelID: "chan-a"}, 10)
	require.NoError(t, err)
	for _, h := range chanHits {
		if h.Class != SearchAtom {
			continue
		}
		assert.Equal(t, "atom-a", h.Ref.AtomID)
		assert.Equal(t, "chan-a", h.Ref.ChannelID)
	}

	projHits, err := r.SearchAt(context.Background(), "zephyr sail",
		GraphView{AllowProject: true}, 10)
	require.NoError(t, err)
	found := map[string]bool{}
	for _, h := range projHits {
		if h.Class == SearchAtom {
			found[h.Ref.AtomID] = true
		}
	}
	assert.True(t, found["atom-p"], "project view must see project-scoped atoms")
	assert.False(t, found["atom-a"], "project view must not see channel atoms")
	assert.False(t, found["atom-b"], "project view must not see other channels' atoms")
}

// The shadow channel applies a 14-day half-life: a 28-day-old atom with an
// identical body scores a quarter of the freshness of a same-age atom, and
// strictly lower overall.
func TestAtomIndex_SearchAt_ShadowHalfLifeComponent(t *testing.T) {
	r := newAtomSearchFixture(t)
	r.InstallAtomSnapshot([]AtomDoc{
		atomFixture("atom-fresh", "seg-1", "orinoco raft drift fact", 3, 0, ""),
		atomFixture("atom-old", "seg-2", "orinoco raft drift fact", 3, 28*24*time.Hour, ""),
	}, 10, nil)

	hits, err := r.SearchAt(context.Background(), "orinoco raft",
		GraphView{AllowProject: true}, 10)
	require.NoError(t, err)
	scores := map[string]SearchHit{}
	for _, h := range hits {
		if h.Class == SearchAtom {
			scores[h.Ref.AtomID] = h
		}
	}
	require.Len(t, scores, 2)
	fresh, old := scores["atom-fresh"], scores["atom-old"]
	assert.InDelta(t, 1.0, fresh.Components.ShadowFreshness, 1e-9)
	assert.InDelta(t, 0.25, old.Components.ShadowFreshness, 1e-6)
	assert.Greater(t, fresh.Score, old.Score)
	assert.InDelta(t, fresh.Score-old.Score,
		(fresh.Components.ShadowFreshness-old.Components.ShadowFreshness)*atomFreshnessWeight, 1e-9)
}

// Without an embedder the search degrades to BM25 deterministically, and
// equal-score hits tie-break by ref key so the fused ranking is stable
// across processes.
func TestAtomIndex_SearchAt_BM25FallbackAndDeterministicFusion(t *testing.T) {
	r := newAtomSearchFixture(t)
	assert.Nil(t, r.emb, "fixture must be BM25-only")
	// Identical bodies AND identical timestamps: only then are the fused
	// scores exactly equal and the ref-key tie-break is what orders them.
	sameAge := time.Now().Add(-time.Hour).Truncate(time.Second)
	r.InstallAtomSnapshot([]AtomDoc{
		{AtomID: "atom-x", SegmentID: "seg-1", Body: "identical deterministic body", PublishSeq: 3, CreatedAt: sameAge},
		{AtomID: "atom-y", SegmentID: "seg-2", Body: "identical deterministic body", PublishSeq: 3, CreatedAt: sameAge},
	}, 10, nil)

	first, err := r.SearchAt(context.Background(), "deterministic body",
		GraphView{AllowProject: true}, 10)
	require.NoError(t, err)
	second, err := r.SearchAt(context.Background(), "deterministic body",
		GraphView{AllowProject: true}, 10)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(first), 2)

	var idsFirst, idsSecond []string
	var prev float64 = 2
	for i, run := range [][]SearchHit{first, second} {
		ids := make([]string, 0, len(run))
		for _, h := range run {
			ids = append(ids, h.Ref.Key())
			assert.LessOrEqual(t, h.Score, prev, "hits must be sorted by descending score")
			prev = h.Score
		}
		if i == 0 {
			idsFirst = ids
		} else {
			idsSecond = ids
		}
		prev = 2
	}
	assert.Equal(t, idsFirst, idsSecond, "identical queries must fuse identically")
	assert.Equal(t, "staging_atom:atom-x", idsFirst[0], "equal scores tie-break by ref key")
}

// A re-installed snapshot fully replaces the previous ledger: stale atoms
// never linger in the index.
func TestAtomIndex_InstallReplacesSnapshot(t *testing.T) {
	r := newAtomSearchFixture(t)
	r.InstallAtomSnapshot([]AtomDoc{
		atomFixture("atom-gone", "seg-1", "ephemeral kite loft fact", 3, time.Hour, ""),
	}, 10, nil)
	r.InstallAtomSnapshot(nil, 10, nil)

	hits, err := r.SearchAt(context.Background(), "kite loft",
		GraphView{AllowProject: true}, 10)
	require.NoError(t, err)
	for _, h := range hits {
		assert.NotEqual(t, "atom-gone", h.Ref.AtomID)
	}
}
