package memorygraph

import (
	"context"
	"fmt"
	"sort"
	"sync"
)

// stagingDocPrefix prefixes staging segment ids in retrieval results so
// they never collide with graph node ids.
const stagingDocPrefix = "seg:"

// IsStagingID reports whether a retrieval doc id refers to a staging
// segment ("seg:<segment_id>") rather than a graph node.
func IsStagingID(id string) bool {
	return len(id) > len(stagingDocPrefix) && id[:len(stagingDocPrefix)] == stagingDocPrefix
}

// RetrievalConfig configures the hybrid retriever (design §6).
type RetrievalConfig struct {
	TopK       int     // number of docs returned per query
	BM25Weight float64 // lexical channel weight; vector channel gets 1-BM25Weight
	// View is the caller's visibility scope (spec §5), reapplied on every
	// Search. The zero value is inactive (no filtering), preserving legacy
	// behavior for existing callers.
	View GraphView
}

// DefaultRetrievalConfig returns the design §6 defaults.
func DefaultRetrievalConfig() RetrievalConfig {
	return RetrievalConfig{TopK: 10, BM25Weight: 0.5}
}

// HybridRetriever implements the BM25 + vector hybrid recall of design
// §5.2 over the current graph version plus all staging segments. Call
// Rebuild after ingestion or version switches to refresh the indexes.
type HybridRetriever struct {
	store *Store
	emb   *CachedEmbedder // nil → BM25-only mode
	cfg   RetrievalConfig

	mu    sync.RWMutex // guards bm25, vecs and nodes during Rebuild/Search
	bm25  *BM25Index
	vecs  map[string][]float32 // doc id -> embedding (only when emb != nil)
	nodes map[string]*Node     // doc id -> graph node (graph docs only; staging docs absent)
	// version is the graph version the installed indexes were built from
	// (0 = never built). Explore pins its tool server to a version and uses
	// a matching retriever view for the whole call (design R5).
	version int
}

// NewHybridRetriever returns a retriever over store. emb may be nil, in
// which case retrieval is BM25-only.
func NewHybridRetriever(store *Store, emb *CachedEmbedder, cfg RetrievalConfig) *HybridRetriever {
	if cfg.TopK <= 0 {
		cfg.TopK = DefaultRetrievalConfig().TopK
	}
	return &HybridRetriever{
		store: store,
		emb:   emb,
		cfg:   cfg,
		bm25:  NewBM25Index(),
		vecs:  make(map[string][]float32),
		nodes: make(map[string]*Node),
	}
}

// Rebuild reloads the current version graph and all staging segments,
// re-indexing every node body and staging body into BM25 and (when an
// embedder is configured) into the in-memory vector map. The content-hash
// disk cache dedupes embedding work across rebuilds.
func (r *HybridRetriever) Rebuild(ctx context.Context) error {
	current, err := r.store.CurrentVersion()
	if err != nil {
		return fmt.Errorf("retriever rebuild: current version: %w", err)
	}
	return r.RebuildForVersion(ctx, current)
}

// RebuildForVersion is Rebuild against an explicit version instead of the
// current pointer. Backtests use it to evaluate a candidate version with
// the identical retrieval pipeline production uses against current (design
// Q13/A2).
func (r *HybridRetriever) RebuildForVersion(ctx context.Context, version int) error {
	g, err := LoadGraph(r.store, version)
	if err != nil {
		return fmt.Errorf("retriever rebuild: load graph v%d: %w", version, err)
	}

	// Collect docs: node bodies plus staging segment bodies.
	type doc struct {
		id   string
		body string
	}
	var docs []doc
	nodes := make(map[string]*Node)
	for _, n := range g.Nodes() {
		docs = append(docs, doc{id: n.NodeID, body: n.Body})
		nodes[n.NodeID] = n
	}
	segIDs, err := r.store.ListStagingSegments()
	if err != nil {
		return fmt.Errorf("retriever rebuild: list staging: %w", err)
	}
	for _, segID := range segIDs {
		body, err := r.store.ReadStagingSegment(segID)
		if err != nil {
			return fmt.Errorf("retriever rebuild: read staging %s: %w", segID, err)
		}
		docs = append(docs, doc{id: stagingDocPrefix + segID, body: string(body)})
	}

	index := NewBM25Index()
	for _, d := range docs {
		index.Add(d.id, d.body)
	}

	vecs := make(map[string][]float32)
	if r.emb != nil && len(docs) > 0 {
		texts := make([]string, len(docs))
		for i, d := range docs {
			texts[i] = d.body
		}
		embeddings, err := r.emb.Embed(ctx, texts)
		if err != nil {
			return fmt.Errorf("retriever rebuild: embed docs: %w", err)
		}
		for i, d := range docs {
			vecs[d.id] = embeddings[i]
		}
	}

	r.mu.Lock()
	r.bm25 = index
	r.vecs = vecs
	r.nodes = nodes
	r.version = version
	r.mu.Unlock()
	return nil
}

// Version returns the graph version the installed indexes were built from,
// or 0 when the retriever was never built.
func (r *HybridRetriever) Version() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.version
}

// ForkForVersion returns a retriever over the same store, embedder and
// config whose indexes are built for the given version (design R5/R12:
// version pinning). When this retriever is already built for version, the
// fork is a cheap snapshot sharing the (immutably swapped) index structures;
// otherwise the fork rebuilds for the requested version via the shared
// content-hash embedding cache.
func (r *HybridRetriever) ForkForVersion(ctx context.Context, version int) (*HybridRetriever, error) {
	r.mu.RLock()
	if r.version == version && version > 0 {
		fork := &HybridRetriever{
			store:   r.store,
			emb:     r.emb,
			cfg:     r.cfg,
			bm25:    r.bm25,
			vecs:    r.vecs,
			nodes:   r.nodes,
			version: r.version,
		}
		r.mu.RUnlock()
		return fork, nil
	}
	r.mu.RUnlock()
	fork := NewHybridRetriever(r.store, r.emb, r.cfg)
	if err := fork.RebuildForVersion(ctx, version); err != nil {
		return nil, err
	}
	return fork, nil
}

// Search runs the hybrid recall over the indexes installed by the last
// Rebuild/RebuildForVersion. When cfg.View is active, docs whose node is
// not visible under the view are dropped from the results (spec §5: the
// view is reapplied at every retrieval step so ranking can never leak
// cross-scope material). Staging docs carry no graph node and pass
// through — they are only reachable in the graph they were staged into,
// and recall dirs are already scope-resolved upstream.
func (r *HybridRetriever) Search(ctx context.Context, query string) ([]ScoredDoc, error) {
	r.mu.RLock()
	index := r.bm25
	vecs := r.vecs
	r.mu.RUnlock()
	docs, err := hybridSearch(ctx, index, vecs, r.emb, r.cfg, query)
	if err != nil {
		return nil, err
	}
	if r.viewActive() {
		out := docs[:0]
		for _, d := range docs {
			if n := r.nodeForDoc(d.ID); n == nil || r.cfg.View.Allows(n) {
				out = append(out, d)
			}
		}
		docs = out
	}
	return docs, nil
}

// viewActive reports whether cfg.View filters results (spec §5). The zero
// GraphView is inactive so legacy callers keep unfiltered retrieval.
func (r *HybridRetriever) viewActive() bool {
	return r.cfg.View.Active()
}

// nodeForDoc maps a retrieval doc id back to its graph node, or nil for
// staging docs and unknown ids.
func (r *HybridRetriever) nodeForDoc(id string) *Node {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.nodes[id]
}

// AllowsNodeID reports whether id may be surfaced to this retriever's
// caller: staging docs and unknown ids pass (same rule as Search), graph
// nodes must satisfy the active view. The continuation seed merge uses this
// so prior node ids can never bypass channel visibility.
func (r *HybridRetriever) AllowsNodeID(id string) bool {
	n := r.nodeForDoc(id)
	return n == nil || !r.viewActive() || r.cfg.View.Allows(n)
}

// hybridSearch is the shared two-channel scoring used by production Search
// and by per-candidate backtest retrievers (design Q13/A2): BM25 scores
// normalized to [0,1] (divided by the channel maximum) and vector cosine
// similarity mapped to [0,1] via (cos+1)/2, merged as
//
//	final = BM25Weight*bm25norm + (1-BM25Weight)*vecsim
//
// per doc id; docs present in only one channel keep their single-channel
// weighted score. Without an embedder, final = bm25norm. At most cfg.TopK
// results are returned, sorted by descending final score.
func hybridSearch(ctx context.Context, index *BM25Index, vecs map[string][]float32, emb *CachedEmbedder, cfg RetrievalConfig, query string) ([]ScoredDoc, error) {
	if cfg.TopK <= 0 {
		cfg.TopK = DefaultRetrievalConfig().TopK
	}
	// Over-fetch from the lexical channel so vector-only docs can merge in.
	bm25Hits := index.Search(query, max(cfg.TopK*4, cfg.TopK))
	bm25Norm := make(map[string]float64, len(bm25Hits))
	maxScore := 0.0
	for _, h := range bm25Hits {
		if h.Score > maxScore {
			maxScore = h.Score
		}
	}
	if maxScore > 0 {
		for _, h := range bm25Hits {
			bm25Norm[h.ID] = h.Score / maxScore
		}
	}

	vecSim := make(map[string]float64)
	if emb != nil && len(vecs) > 0 {
		qvec, err := emb.EmbedQuery(ctx, query)
		if err != nil {
			return nil, fmt.Errorf("retriever search: embed query: %w", err)
		}
		for id, dvec := range vecs {
			sim := (cosineSimilarity(qvec, dvec) + 1) / 2
			if sim > 0 {
				vecSim[id] = sim
			}
		}
	}

	merged := make(map[string]float64, len(bm25Norm)+len(vecSim))
	for id := range bm25Norm {
		merged[id] = 0
	}
	for id := range vecSim {
		if _, ok := merged[id]; !ok {
			merged[id] = 0
		}
	}
	switch {
	case emb == nil:
		for id := range merged {
			merged[id] = bm25Norm[id]
		}
	default:
		w := cfg.BM25Weight
		for id := range merged {
			merged[id] = w*bm25Norm[id] + (1-w)*vecSim[id]
		}
	}

	hits := make([]ScoredDoc, 0, len(merged))
	for id, score := range merged {
		if score > 0 {
			hits = append(hits, ScoredDoc{ID: id, Score: score})
		}
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		return hits[i].ID < hits[j].ID
	})
	if len(hits) > cfg.TopK {
		hits = hits[:cfg.TopK]
	}
	return hits, nil
}
