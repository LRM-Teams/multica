package memorygraph

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"
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
// §5.2 over the current graph version. Staging segments are no longer
// indexed as retrievable docs: staging memory is reachable only through
// the class-aware atom channel (SearchAt). Call Rebuild after ingestion
// or version switches to refresh the indexes.
type HybridRetriever struct {
	store *Store
	emb   *CachedEmbedder // nil → BM25-only mode
	cfg   RetrievalConfig

	mu    sync.RWMutex // guards bm25, vecs and nodes during Rebuild/Search
	bm25  *BM25Index
	vecs  map[string][]float32 // doc id -> embedding (only when emb != nil)
	nodes map[string]*Node     // doc id -> graph node (graph docs only; staging docs absent)

	// atoms is the Task 9 active-atom ledger snapshot (staging channel of
	// the class-aware search). Installed by the service loader; nil-safe.
	atoms *atomIndex
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
		atoms: newAtomIndex(),
	}
}

// Rebuild reloads the current version graph, re-indexing every node body
// into BM25 and (when an embedder is configured) into the in-memory vector
// map. The content-hash disk cache dedupes embedding work across rebuilds.
//
// Offline default only: the file current pointer is never reader
// authority (Task 14). Production callers pin the DB-authoritative
// version via RebuildForVersion.
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

	// Collect docs: node bodies only. Staging segments are deliberately
	// absent from the default corpus (Task 22): unconditional staging
	// retrieval predated the class-aware atom channel, and keeping both
	// would surface the same memory twice with different visibility rules.
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
		if err == nil {
			for i, d := range docs {
				vecs[d.id] = embeddings[i]
			}
		}
		// Provider outage degrades this index generation to BM25. The
		// resolved provider is never replaced and later rebuilds may retry it.
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
			atoms:   r.atoms,
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
// cross-scope material). Staging segments are not part of the corpus at
// all; staging memory is reachable only through SearchAt's atom channel.
func (r *HybridRetriever) Search(ctx context.Context, query string) ([]ScoredDoc, error) {
	r.mu.RLock()
	index := r.bm25
	vecs := r.vecs
	r.mu.RUnlock()
	// Eligible corpus before rank (Slice 1.2): the view's role and scope
	// gates run inside both retrieval channels, so invisible high scorers
	// cannot displace visible docs from the TopK. The rank-output recheck
	// below stays as defense in depth.
	eligible := func(id string) bool {
		n := r.nodeForDoc(id)
		return n == nil || r.cfg.View.visibleForRetrieval(n)
	}
	docs, err := hybridSearch(ctx, index, vecs, r.emb, r.cfg, query, eligible)
	if err != nil && r.emb != nil {
		// Query-time embedding outage follows the same deterministic BM25
		// degradation as rebuild; never retry through another provider.
		docs, err = hybridSearch(ctx, index, nil, nil, r.cfg, query, eligible)
	}
	if err != nil {
		return nil, err
	}
	// Defense-in-depth recheck: re-assert the view on the rank output so a
	// predicate/index bug cannot surface an invisible doc.
	out := docs[:0]
	for _, d := range docs {
		if n := r.nodeForDoc(d.ID); n != nil && !r.cfg.View.visibleForRetrieval(n) {
			continue
		}
		out = append(out, d)
	}
	docs = out
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
// nodes must clear the pattern-role gate and, when the view is active, the
// scoped view. The continuation seed merge uses this so prior node ids can
// never bypass channel visibility or the evolution-plane boundary.
func (r *HybridRetriever) AllowsNodeID(id string) bool {
	n := r.nodeForDoc(id)
	return n == nil || r.cfg.View.visibleForRetrieval(n)
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
// results are returned, sorted by descending final score. eligible (nil =
// admit all) is applied inside both channels before the TopK cut, so the
// ranked corpus is the eligible corpus (Slice 1.2).
func hybridSearch(ctx context.Context, index *BM25Index, vecs map[string][]float32, emb *CachedEmbedder, cfg RetrievalConfig, query string, eligible func(id string) bool) ([]ScoredDoc, error) {
	if cfg.TopK <= 0 {
		cfg.TopK = DefaultRetrievalConfig().TopK
	}
	// Over-fetch from the lexical channel so vector-only docs can merge in.
	bm25Hits := index.SearchFiltered(query, max(cfg.TopK*4, cfg.TopK), eligible)
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
			if eligible != nil && !eligible(id) {
				continue
			}
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

// InstallAtomSnapshot atomically replaces the active-atom ledger of the
// staging search channel (Task 9). The DB loader resolves the snapshot at a
// publish_seq watermark and applies the Task 8A retraction fence upstream;
// the retriever independently re-asserts the retraction set, the consumed
// flag, the exact-channel partition and the caller's publish_seq ceiling on
// every SearchAt, so a loader bug cannot leak a forbidden atom. A nil slice
// clears the channel.
func (r *HybridRetriever) InstallAtomSnapshot(atoms []AtomDoc, publishSeqMax int64, retractedAtomIDs map[string]bool) {
	if r.atoms == nil {
		r.atoms = newAtomIndex()
	}
	r.atoms.install(atoms, publishSeqMax, retractedAtomIDs)
}

// SearchAt is the class-aware retrieval of Task 9: the graph channel (nodes
// of the version the retriever was built for — current-node-only by
// construction, staging segments never surface as graph hits) fused with the
// staging-atom channel, filtered by the caller's view and publish_seq
// watermark before scoring. Fusion is deterministic: per-channel scores are
// normalized to [0,1], multiplied by the class prior, the atom channel adds
// its 14-day shadow half-life component, and ties break by ref key. The
// results are shadow-only until the DB atom_search gate turns green.
func (r *HybridRetriever) SearchAt(ctx context.Context, query string, view GraphView, publishSeqMax int64) ([]SearchHit, error) {
	r.mu.RLock()
	index := r.bm25
	vecs := r.vecs
	emb := r.emb
	r.mu.RUnlock()

	// Graph channel eligible corpus before rank (Slice 1.2): the view's
	// pattern-role gate and scoped visibility run inside hybridSearch, so
	// invisible high scorers cannot displace eligible nodes from the TopK.
	// The per-hit recheck below stays as defense in depth.
	viewOn := view.AllowProject || view.ChannelID != ""
	graphEligible := func(id string) bool {
		n := r.nodeForDoc(id)
		if n == nil {
			// Staging segment docs never surface as graph hits.
			return false
		}
		if !view.patternEligible(n) {
			return false
		}
		return !viewOn || view.Allows(n)
	}
	docs, err := hybridSearch(ctx, index, vecs, emb, r.cfg, query, graphEligible)
	if err != nil && emb != nil {
		// Query-time embedding outage follows the same deterministic BM25
		// degradation as Search, on the same eligible corpus.
		docs, err = hybridSearch(ctx, index, nil, nil, r.cfg, query, graphEligible)
	}
	if err != nil {
		return nil, err
	}

	hits := make([]SearchHit, 0, len(docs))
	for _, d := range docs {
		n := r.nodeForDoc(d.ID)
		if n == nil {
			// Staging segment docs never surface as graph hits: staging
			// memory is reachable only through the atom ledger channel,
			// never by treating a missing graph node as visible.
			continue
		}
		if !view.patternEligible(n) {
			continue
		}
		if viewOn && !view.Allows(n) {
			continue
		}
		hits = append(hits, SearchHit{
			Ref:   MemoryRef{Kind: MemoryRefGraphNode, NodeID: n.NodeID, ChannelID: n.ChannelID},
			Class: SearchGraphNode,
			Score: d.Score * searchGraphClassPrior,
			Components: SearchScoreComponents{
				Lexical:    d.Score, // fused channel score (BM25+vector) normalized in [0,1]
				ClassPrior: searchGraphClassPrior,
			},
		})
	}

	// Staging-atom channel: BM25 over the installed snapshot with the
	// visibility partition applied inside the lexical selection, visibility
	// re-asserted per atom, half-limited freshness folded in.
	norm, order := r.atomScores(query, func(a AtomDoc) bool {
		return r.atoms.visible(a, view, publishSeqMax)
	})
	now := time.Now()
	for _, id := range order {
		a := r.atomDoc(id)
		if r.atoms != nil && !r.atoms.visible(a, view, publishSeqMax) {
			continue
		}
		base := norm[id] * searchAtomClassPrior
		fresh := atomShadowFreshness(a.CreatedAt, now)
		hits = append(hits, SearchHit{
			Ref: MemoryRef{
				Kind: MemoryRefStagingAtom, AtomID: a.AtomID,
				SegmentID: a.SegmentID, ChannelID: a.ChannelID,
			},
			Class: SearchAtom,
			Score: base*(1-atomFreshnessWeight) + fresh*atomFreshnessWeight,
			Components: SearchScoreComponents{
				Lexical:         norm[id],
				ShadowFreshness: fresh,
				ClassPrior:      searchAtomClassPrior,
			},
		})
	}

	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		return hits[i].Ref.Key() < hits[j].Ref.Key()
	})
	if len(hits) > r.cfg.TopK {
		hits = hits[:r.cfg.TopK]
	}
	return hits, nil
}

// atomScores runs the lexical channel over the installed atom snapshot,
// gated by visible inside the selection, and returns the normalized scores
// plus the index's deterministic hit order.
func (r *HybridRetriever) atomScores(query string, visible func(AtomDoc) bool) (map[string]float64, []string) {
	if r.atoms == nil {
		return map[string]float64{}, nil
	}
	return r.atoms.search(query, 0, visible)
}

// atomDoc returns the installed atom by id (zero AtomDoc when absent).
func (r *HybridRetriever) atomDoc(id string) AtomDoc {
	if r.atoms == nil {
		return AtomDoc{}
	}
	r.atoms.mu.RLock()
	defer r.atoms.mu.RUnlock()
	return r.atoms.atoms[id]
}
