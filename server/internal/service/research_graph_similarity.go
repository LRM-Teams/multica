// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"sync"

	"github.com/multica-ai/multica/server/internal/memorygraph"
)

// researchSimilarityCache memoizes the hash-embedding vector of every node
// body in one research graph, keyed by (dir, version): a poll scores up to
// one batch of candidates against the same unchanged graph snapshot, so the
// per-node hashing is paid once per version instead of once per candidate.
type researchSimilarityCache struct {
	mu   sync.Mutex
	dirs map[string]researchSimilarityDir
}

type researchSimilarityDir struct {
	version int
	vectors map[string][]float32
}

// NewResearchHashSimilarity returns the production import-time dedup scorer
// for the research graph export (spec §4.2): deterministic, network-free
// cosine similarity between hash embeddings of the candidate body and every
// current node body. The score is a raw cosine in [0,1] — near-duplicate
// bodies score ≈1.0, paraphrases land well below the 0.95 dedup threshold —
// and the exporter applies the threshold. A graph that cannot be read yields
// no match instead of an error, so a transient store failure never blocks an
// export (dedup is an optimization, not a gate).
func NewResearchHashSimilarity() ResearchSimilarityFunc {
	cache := &researchSimilarityCache{dirs: map[string]researchSimilarityDir{}}
	return func(ctx context.Context, dir, body, excludeNodeID string) (string, float64, error) {
		vectors, err := cache.vectorsFor(ctx, dir)
		if err != nil || len(vectors) == 0 {
			return "", 0, nil
		}
		q := memorygraph.HashEmbedText(body)
		best, bestScore := "", 0.0
		for id, vec := range vectors {
			if id == excludeNodeID {
				continue
			}
			if s := dotUnit(q, vec); s > bestScore {
				best, bestScore = id, s
			}
		}
		return best, bestScore, nil
	}
}

// vectorsFor loads (and caches) the node-body vectors of the dir's current
// graph version. HashEmbedText returns unit vectors, so the cosine is the
// plain dot product.
func (c *researchSimilarityCache) vectorsFor(ctx context.Context, dir string) (map[string][]float32, error) {
	store := memorygraph.NewStore(dir)
	version, err := store.CurrentVersion()
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	cached, ok := c.dirs[dir]
	c.mu.Unlock()
	if ok && cached.version == version {
		return cached.vectors, nil
	}
	g, err := memorygraph.LoadGraph(store, version)
	if err != nil {
		return nil, err
	}
	vectors := make(map[string][]float32, len(g.Nodes()))
	for _, n := range g.Nodes() {
		vectors[n.NodeID] = memorygraph.HashEmbedText(n.Body)
	}
	c.mu.Lock()
	c.dirs[dir] = researchSimilarityDir{version: version, vectors: vectors}
	c.mu.Unlock()
	return vectors, nil
}

// dotUnit is the dot product of two unit-length vectors (their cosine).
func dotUnit(a, b []float32) float64 {
	if len(a) != len(b) {
		return 0
	}
	var sum float64
	for i := range a {
		sum += float64(a[i]) * float64(b[i])
	}
	if sum < 0 {
		return 0 // cosine below zero is never a match
	}
	return sum
}
