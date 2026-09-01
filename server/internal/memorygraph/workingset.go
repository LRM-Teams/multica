package memorygraph

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Working set for consolidation prompts (unification spec §4.3): the bounded
// hot-region node pool that makes update_node, delete_node, merge_node and
// cross-version edges reachable. Without it the prompt carries no existing
// node id, so the agent can only ever fold staging segments into new nodes.
//
// The pool draws from three signal sources — query-log usage (citations and
// judge ground truth), explore trajectory views, dive views/submissions —
// plus each staging segment's top-K hybrid-retrieval matches, plus the 1-hop
// neighborhood of the surviving signal nodes. Total size is O(retrieval
// activity) + O(staging·K), never O(graph size).

// Signal labels attached to working-set nodes, ordered by truncation weight:
// a node holding a lower-weight signal survives pool truncation first
// (spec §4.3: query_log citations win).
const (
	SignalCited           = "cited"          // query_log NodeIDs: adopted-path citation
	SignalJudgeRelevant   = "judge-relevant" // judge RelevantNodes ground truth
	SignalDiveSubmitted   = "dive-submitted"
	SignalDiveViewed      = "dive-viewed"
	SignalExploreViewed   = "explore-viewed"
	SignalResearchImport  = "research-import"  // recently exported research node (§4.5)
	SignalStagingSimilar  = "staging-similar"  // top-K match of a pending staging segment
	SignalResearchSimilar = "research-similar" // top-K old-node match of a recent research import
	SignalNeighbor        = "neighbor"         // 1-hop expansion of a signal node
)

var signalWeights = map[string]int{
	SignalCited:           0,
	SignalJudgeRelevant:   1,
	SignalDiveSubmitted:   2,
	SignalDiveViewed:      3,
	SignalExploreViewed:   4,
	SignalResearchImport:  1,
	SignalStagingSimilar:  5,
	SignalResearchSimilar: 5,
	SignalNeighbor:        6,
}

// OpWorkingSetCursor is the op-log op recording the query-log watermark one
// consolidation build consumed; the next build reads only newer entries.
// The same entry carries the research-import watermark (spec §4.5) when the
// build consumed research exports.
const OpWorkingSetCursor = "working_set"

// IsResearchSourceKind reports whether a node's source kind marks it as
// written by the research exporter (research ledgers, unification spec
// §4.2). Only those nodes feed the research-import signal.
func IsResearchSourceKind(kind string) bool {
	return kind == "research_node" || kind == "research_insight" || kind == "research_result"
}

// queryLogWindowDaemon matches the window the recall-time recorder writes
// (graph_memory_recall_execute.go). The working set reads the same ledger.
const queryLogWindowDaemon = "daemon"

// cursorScanVersions bounds the downward op-log scan that looks for the
// watermark. Version GC keeps a handful of versions; 64 is a safety net
// against pathological trees, not a functional limit.
const cursorScanVersions = 64

// RetrievalSignals supplies recent retrieval-activity node ids from the PG
// ledgers (explore trajectories, dive jobs). The service layer injects the
// readers so this package stays file-store only; nil functions contribute no
// signal.
type RetrievalSignals struct {
	// ExploreViewed returns viewed-node id lists per recent explore
	// trajectory, newest first, at most limit runs.
	ExploreViewed func(ctx context.Context, limit int) ([][]string, error)
	// DiveRuns returns recent dive jobs, newest first, at most limit runs.
	DiveRuns func(ctx context.Context, limit int) ([]DiveSignal, error)
}

// DiveSignal is one recent dive job's node evidence.
type DiveSignal struct {
	ViewedNodeIDs    []string
	SubmittedNodeIDs []string
}

// WorkingSetConfig holds the working-set budgets (spec §4.3 parameter table,
// decision 14). Every value is GraphMemoryLimits-style overridable.
type WorkingSetConfig struct {
	MaxNodes         int // deduped pool cap, truncated by signal weight (default 64)
	NodeBodyRunes    int // per-node summary cap, aligned with dive (default 400)
	StagingTopK      int // similar nodes injected per staging segment (default 3)
	MaxWindowEntries int // signal-window fallback cap, newest first (default 256)
}

// DefaultWorkingSetConfig returns the spec §4.3 defaults.
func DefaultWorkingSetConfig() WorkingSetConfig {
	return WorkingSetConfig{
		MaxNodes:         64,
		NodeBodyRunes:    400,
		StagingTopK:      3,
		MaxWindowEntries: 256,
	}
}

func (c WorkingSetConfig) normalized() WorkingSetConfig {
	d := DefaultWorkingSetConfig()
	if c.MaxNodes <= 0 {
		c.MaxNodes = d.MaxNodes
	}
	if c.NodeBodyRunes <= 0 {
		c.NodeBodyRunes = d.NodeBodyRunes
	}
	if c.StagingTopK <= 0 {
		c.StagingTopK = d.StagingTopK
	}
	if c.MaxWindowEntries <= 0 {
		c.MaxWindowEntries = d.MaxWindowEntries
	}
	return c
}

// WorkingSetNode is one hot-region node as presented to the consolidation
// agent: id, truncated body summary, epistemic state, level, and every
// signal that put it in the pool.
type WorkingSetNode struct {
	NodeID    string
	Summary   string
	Epistemic string
	Level     int
	Signals   []string
}

// HasSignal reports whether the node carries one signal label.
func (n *WorkingSetNode) HasSignal(s string) bool {
	for _, got := range n.Signals {
		if got == s {
			return true
		}
	}
	return false
}

// WorkingSet is one build's bounded pool plus the consumed query-log
// watermark (recorded via RecordCursor so the next build is incremental).
type WorkingSet struct {
	Nodes   []WorkingSetNode
	Cursor  time.Time // watermark consumed by this build (zero when none)
	Entries int       // query-log entries consumed by this build
	// ResearchImports is the number of research-export nodes newer than the
	// import watermark that entered the pool; ResearchCursor is that
	// watermark (max observed_at consumed, zero when none).
	ResearchImports int
	ResearchCursor  time.Time
}

// WorkingSetBuilder assembles working sets over one graph store.
type WorkingSetBuilder struct {
	store   *Store
	signals RetrievalSignals
	emb     *CachedEmbedder // optional vector channel of staging similarity
	cfg     WorkingSetConfig
	fanout  int // neighbor budget per signal node (consolidation MaxFanout)
}

// NewWorkingSetBuilder returns a builder. emb may be nil (staging similarity
// runs BM25-only through the hybrid retriever). fanout <= 0 falls back to 8.
func NewWorkingSetBuilder(store *Store, signals RetrievalSignals, emb *CachedEmbedder, cfg WorkingSetConfig, fanout int) *WorkingSetBuilder {
	return &WorkingSetBuilder{
		store:   store,
		signals: signals,
		emb:     emb,
		cfg:     cfg.normalized(),
		fanout:  fanout,
	}
}

// Build collects the three signal sources, truncates the pool by signal
// weight, expands the survivors' 1-hop neighborhood, and returns the final
// bounded node list with summaries. Ids that no longer resolve to graph
// nodes (stale citations, staging ids) are dropped.
func (b *WorkingSetBuilder) Build(ctx context.Context, g *Graph, version int, staging []stagingSummary) (*WorkingSet, error) {
	cfg := b.cfg.normalized()
	pool := newWSPool()

	cursors, err := readWorkingSetCursors(b.store, version)
	if err != nil {
		return nil, fmt.Errorf("working set: cursor: %w", err)
	}
	ws := &WorkingSet{Cursor: cursors.queryLog, ResearchCursor: cursors.research}
	consumed, err := b.consumeQueryLog(pool, cursors.queryLog, cfg)
	if err != nil {
		return nil, err
	}
	ws.Cursor, ws.Entries = consumed.cursor, consumed.entries

	if fn := b.signals.ExploreViewed; fn != nil {
		runs, err := fn(ctx, cfg.MaxWindowEntries)
		if err != nil {
			return nil, fmt.Errorf("working set: explore signals: %w", err)
		}
		for _, ids := range runs {
			pool.add(ids, SignalExploreViewed)
		}
	}
	if fn := b.signals.DiveRuns; fn != nil {
		runs, err := fn(ctx, cfg.MaxWindowEntries)
		if err != nil {
			return nil, fmt.Errorf("working set: dive signals: %w", err)
		}
		for _, d := range runs {
			pool.add(d.ViewedNodeIDs, SignalDiveViewed)
			pool.add(d.SubmittedNodeIDs, SignalDiveSubmitted)
		}
	}
	imports, importCursor := b.researchImports(g, cursors.research, cfg)
	pool.add(imports, SignalResearchImport)
	ws.ResearchImports, ws.ResearchCursor = len(imports), importCursor

	if err := b.injectStagingSimilar(ctx, pool, version, staging, cfg); err != nil {
		return nil, err
	}
	if err := b.injectResearchSimilar(ctx, pool, version, imports, cfg); err != nil {
		return nil, err
	}

	// Weight truncation happens twice: once on the signal pool (deciding
	// whose neighborhood is worth expanding), once after neighbors join.
	kept := pool.truncate(cfg.MaxNodes, g)
	b.expandNeighbors(g, pool, kept)
	final := pool.truncate(cfg.MaxNodes, g)
	for _, n := range final {
		gn := g.Node(n.NodeID)
		n.Epistemic = gn.Epistemic
		n.Level = gn.Level
		n.Summary = truncateRunes(gn.Body, cfg.NodeBodyRunes)
		ws.Nodes = append(ws.Nodes, *n)
	}
	return ws, nil
}

// RecordCursor persists the consumed watermarks into the version's op-log so
// the next build reads only newer query-log entries and research imports
// (spec §4.3 incremental cursor, §4.5 import watermark). Builds that
// consumed nothing on either channel are not recorded.
func (b *WorkingSetBuilder) RecordCursor(version int, ws *WorkingSet) error {
	if ws == nil || (ws.Entries == 0 && ws.ResearchImports == 0) {
		return nil
	}
	detail := map[string]any{
		"consumed_through": ws.Cursor.UTC().Format(time.RFC3339Nano),
		"entries":          ws.Entries,
		"nodes":            len(ws.Nodes),
	}
	if ws.ResearchImports > 0 {
		detail["research_imports"] = ws.ResearchImports
		detail["research_observed_through"] = ws.ResearchCursor.UTC().Format(time.RFC3339Nano)
	}
	return NewOpLogger(b.store).Append(version, CreatorConsolidator, OpWorkingSetCursor, "cursor", detail)
}

// queryLogWindow is the consumed slice of the query-log window.
type queryLogWindow struct {
	cursor  time.Time
	entries int
}

// consumeQueryLog reads the recall window, keeps only entries newer than the
// cursor, sorts newest first, truncates to the fallback cap, and folds the
// usage signal into the pool.
func (b *WorkingSetBuilder) consumeQueryLog(pool *wsPool, cursor time.Time, cfg WorkingSetConfig) (queryLogWindow, error) {
	entries, err := b.store.ReadQueryLog(queryLogWindowDaemon)
	if err != nil {
		return queryLogWindow{cursor: cursor}, fmt.Errorf("working set: read query log: %w", err)
	}
	fresh := make([]*QueryLogEntry, 0, len(entries))
	for _, e := range entries {
		if e.Timestamp.After(cursor) {
			fresh = append(fresh, e)
		}
	}
	sort.SliceStable(fresh, func(i, j int) bool { return fresh[i].Timestamp.After(fresh[j].Timestamp) })
	if len(fresh) > cfg.MaxWindowEntries {
		fresh = fresh[:cfg.MaxWindowEntries]
	}
	out := queryLogWindow{cursor: cursor, entries: len(fresh)}
	for _, e := range fresh {
		pool.add(e.NodeIDs, SignalCited)
		pool.add(e.RelevantNodes, SignalJudgeRelevant)
		if e.Timestamp.After(out.cursor) {
			out.cursor = e.Timestamp
		}
	}
	return out, nil
}

// injectStagingSimilar adds each pending staging segment's top-K hybrid
// retrieval matches, catching cold nodes and duplicates of new evidence the
// usage signal never sees.
func (b *WorkingSetBuilder) injectStagingSimilar(ctx context.Context, pool *wsPool, version int, staging []stagingSummary, cfg WorkingSetConfig) error {
	if len(staging) == 0 {
		return nil
	}
	r := NewHybridRetriever(b.store, b.emb, DefaultRetrievalConfig())
	if err := r.RebuildForVersion(ctx, version); err != nil {
		return fmt.Errorf("working set: staging retriever: %w", err)
	}
	for _, s := range staging {
		docs, err := r.Search(ctx, s.body)
		if err != nil {
			return fmt.Errorf("working set: staging search %s: %w", s.id, err)
		}
		count := 0
		for _, d := range docs {
			if IsStagingID(d.ID) {
				continue
			}
			pool.add([]string{d.ID}, SignalStagingSimilar)
			count++
			if count >= cfg.StagingTopK {
				break
			}
		}
	}
	return nil
}

// expandNeighbors adds the 1-hop parents, children, and relation neighbors
// of the kept signal nodes, capped by the fanout budget per node.
func (b *WorkingSetBuilder) expandNeighbors(g *Graph, pool *wsPool, kept []*WorkingSetNode) {
	fanout := b.fanout
	if fanout <= 0 {
		fanout = 8
	}
	for _, k := range kept {
		collected := 0
		for _, e := range g.HierarchyEdges() {
			if collected >= fanout {
				break
			}
			switch {
			case e.To == k.NodeID:
				pool.add([]string{e.From}, SignalNeighbor)
				collected++
			case e.From == k.NodeID:
				pool.add([]string{e.To}, SignalNeighbor)
				collected++
			}
		}
		for _, e := range g.RelationEdges() {
			if collected >= fanout {
				break
			}
			// Edge-ref targets ("edge:<id>") point at edges, not nodes.
			if strings.HasPrefix(e.To, "edge:") {
				continue
			}
			switch {
			case e.From == k.NodeID:
				pool.add([]string{e.To}, SignalNeighbor)
				collected++
			case e.To == k.NodeID:
				pool.add([]string{e.From}, SignalNeighbor)
				collected++
			}
		}
	}
}

// wsCursors are the incremental watermarks one build consumes: the query-log
// timestamp cursor and the research-import observed_at cursor.
type wsCursors struct {
	queryLog time.Time
	research time.Time
}

// readWorkingSetCursors scans op logs from version downward for the most
// recent working_set watermark entry, parsing both cursors it carries. A
// missing or malformed field means no cursor on that channel.
func readWorkingSetCursors(store *Store, version int) (wsCursors, error) {
	logger := NewOpLogger(store)
	lo := version - cursorScanVersions
	if lo < 1 {
		lo = 1
	}
	for v := version; v >= lo; v-- {
		entries, err := logger.Read(v)
		if err != nil {
			return wsCursors{}, fmt.Errorf("working set: read op log v%d: %w", v, err)
		}
		for i := len(entries) - 1; i >= 0; i-- {
			if entries[i].Op != OpWorkingSetCursor {
				continue
			}
			out := wsCursors{}
			if s, _ := entries[i].Detail["consumed_through"].(string); s != "" {
				if ts, err := time.Parse(time.RFC3339Nano, s); err == nil {
					out.queryLog = ts
				}
			}
			if s, _ := entries[i].Detail["research_observed_through"].(string); s != "" {
				if ts, err := time.Parse(time.RFC3339Nano, s); err == nil {
					out.research = ts
				}
			}
			return out, nil
		}
	}
	return wsCursors{}, nil
}

// researchImports returns the ids of research-export nodes whose observed_at
// is newer than the import watermark, newest first, capped at the window
// fallback budget, plus the watermark this build consumes (the max
// observed_at among them; zero when there are none).
func (b *WorkingSetBuilder) researchImports(g *Graph, cursor time.Time, cfg WorkingSetConfig) ([]string, time.Time) {
	type importNode struct {
		id string
		at time.Time
	}
	var fresh []importNode
	for _, n := range g.Nodes() {
		if !IsResearchSourceKind(n.SourceKind) || !n.ObservedAt.After(cursor) {
			continue
		}
		fresh = append(fresh, importNode{id: n.NodeID, at: n.ObservedAt})
	}
	sort.SliceStable(fresh, func(i, j int) bool { return fresh[i].at.After(fresh[j].at) })
	if len(fresh) > cfg.MaxWindowEntries {
		fresh = fresh[:cfg.MaxWindowEntries]
	}
	ids := make([]string, len(fresh))
	newest := time.Time{}
	for i, f := range fresh {
		ids[i] = f.id
		if f.at.After(newest) {
			newest = f.at
		}
	}
	return ids, newest
}

// injectResearchSimilar adds each recent research import's top-K retrieval
// matches among the older graph nodes (spec §4.5 merge-candidate pairs),
// skipping the import itself.
func (b *WorkingSetBuilder) injectResearchSimilar(ctx context.Context, pool *wsPool, version int, imports []string, cfg WorkingSetConfig) error {
	if len(imports) == 0 {
		return nil
	}
	r := NewHybridRetriever(b.store, b.emb, DefaultRetrievalConfig())
	if err := r.RebuildForVersion(ctx, version); err != nil {
		return fmt.Errorf("working set: research retriever: %w", err)
	}
	for _, id := range imports {
		n := r.nodeForDoc(id)
		if n == nil {
			continue
		}
		docs, err := r.Search(ctx, n.Body)
		if err != nil {
			return fmt.Errorf("working set: research search %s: %w", id, err)
		}
		count := 0
		for _, d := range docs {
			if IsStagingID(d.ID) || d.ID == id {
				continue
			}
			pool.add([]string{d.ID}, SignalResearchSimilar)
			count++
			if count >= cfg.StagingTopK {
				break
			}
		}
	}
	return nil
}

// wsPool accumulates signal nodes in first-seen order, deduplicating ids and
// merging signal labels.
type wsPool struct {
	order []string
	nodes map[string]*WorkingSetNode
}

func newWSPool() *wsPool {
	return &wsPool{nodes: make(map[string]*WorkingSetNode)}
}

// add folds ids into the pool under one signal label.
func (p *wsPool) add(ids []string, signal string) {
	for _, id := range ids {
		if id == "" {
			continue
		}
		n, ok := p.nodes[id]
		if !ok {
			n = &WorkingSetNode{NodeID: id}
			p.nodes[id] = n
			p.order = append(p.order, id)
		}
		if !n.HasSignal(signal) {
			n.Signals = append(n.Signals, signal)
		}
	}
}

// truncate returns at most max pool nodes ordered by best signal weight,
// stable within equal weights by first-seen order, dropping ids that do not
// resolve to graph nodes.
func (p *wsPool) truncate(max int, g *Graph) []*WorkingSetNode {
	out := make([]*WorkingSetNode, 0, len(p.order))
	for _, id := range p.order {
		if g.Node(id) == nil {
			continue
		}
		out = append(out, p.nodes[id])
	}
	sort.SliceStable(out, func(i, j int) bool { return wsBestWeight(out[i]) < wsBestWeight(out[j]) })
	if len(out) > max {
		out = out[:max]
	}
	return out
}

// wsBestWeight is the node's strongest signal weight; unknown labels rank last.
func wsBestWeight(n *WorkingSetNode) int {
	best := 1<<31 - 1
	for _, s := range n.Signals {
		if w, ok := signalWeights[s]; ok && w < best {
			best = w
		}
	}
	return best
}
