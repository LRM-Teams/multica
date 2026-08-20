// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"

	"github.com/multica-ai/multica/server/internal/memorygraph"
)

// GraphMemoryHybridSeeder is the production seed retriever (spec §3 step
// 5): hybrid retrieval over the pinned graph version scoped to the resolved
// graph view. It is BM25-only unless an embedding endpoint is configured
// server-side.
type GraphMemoryHybridSeeder struct{}

// Seeds returns the round-0 candidate node ids for one pinned graph version.
func (GraphMemoryHybridSeeder) Seeds(ctx context.Context, dir string, version int, query string, view memorygraph.GraphView) ([]string, error) {
	store := memorygraph.NewStore(dir)
	cfg := memorygraph.DefaultRetrievalConfig()
	cfg.View = view
	retr := memorygraph.NewHybridRetriever(store, nil, cfg)
	if err := retr.RebuildForVersion(ctx, version); err != nil {
		return nil, err
	}
	hits, err := retr.Search(ctx, query)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(hits))
	for _, h := range hits {
		ids = append(ids, h.ID)
	}
	return ids, nil
}
