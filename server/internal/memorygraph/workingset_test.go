package memorygraph

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// wsSignals builds a RetrievalSignals with only the explore/dive readers the
// test needs; nil functions contribute no signal.
func wsSignals(explore [][]string, dives []DiveSignal) RetrievalSignals {
	return RetrievalSignals{
		ExploreViewed: func(context.Context, int) ([][]string, error) { return explore, nil },
		DiveRuns:      func(context.Context, int) ([]DiveSignal, error) { return dives, nil },
	}
}

func recordQueryAt(t *testing.T, store *Store, traceID string, ts time.Time, cited, relevant []string) {
	t.Helper()
	if err := NewQueryRecorder(store, "daemon").RecordRecall(QueryLogEntry{
		TraceID:       traceID,
		Query:         "q-" + traceID,
		Timestamp:     ts,
		Version:       1,
		NodeIDs:       cited,
		Found:         true,
		JudgeDone:     relevant != nil,
		RelevantNodes: relevant,
	}); err != nil {
		t.Fatalf("RecordRecall %s: %v", traceID, err)
	}
}

func wsNodeIDs(ws *WorkingSet) []string {
	ids := make([]string, 0, len(ws.Nodes))
	for _, n := range ws.Nodes {
		ids = append(ids, n.NodeID)
	}
	return ids
}

func wsNode(t *testing.T, ws *WorkingSet, id string) *WorkingSetNode {
	t.Helper()
	for i := range ws.Nodes {
		if ws.Nodes[i].NodeID == id {
			return &ws.Nodes[i]
		}
	}
	t.Fatalf("working set missing node %q; have %v", id, wsNodeIDs(ws))
	return nil
}

// findWSNode is the non-fatal lookup used for negative assertions.
func findWSNode(ws *WorkingSet, id string) *WorkingSetNode {
	for i := range ws.Nodes {
		if ws.Nodes[i].NodeID == id {
			return &ws.Nodes[i]
		}
	}
	return nil
}

// TestWorkingSetAggregatesThreeSignals checks that the three signal sources
// of unification spec §4.3 — query-log citations + judge ground truth,
// explore trajectory views, dive views/submissions — all land in one deduped
// pool, with every signal a node earned preserved on it.
func TestWorkingSetAggregatesThreeSignals(t *testing.T) {
	store := newTestStore(t)
	seedGraphNode(t, store, 1, "n1", "query cited body")
	seedGraphNode(t, store, 1, "n2", "judge relevant body")
	seedGraphNode(t, store, 1, "n3", "explore viewed body")
	seedGraphNode(t, store, 1, "n4", "dive body")
	seedGraphNode(t, store, 1, "n5", "cited and explored body")
	ts := time.Now().UTC().Add(-time.Hour)
	recordQueryAt(t, store, "t1", ts, []string{"n1", "n5"}, []string{"n2"})

	g, err := LoadGraph(store, 1)
	if err != nil {
		t.Fatalf("LoadGraph: %v", err)
	}
	signals := wsSignals([][]string{{"n3", "n5"}}, []DiveSignal{{ViewedNodeIDs: []string{"n4"}, SubmittedNodeIDs: []string{"n4"}}})
	b := NewWorkingSetBuilder(store, signals, nil, DefaultWorkingSetConfig(), 8)

	ws, err := b.Build(context.Background(), g, 1, nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(ws.Nodes) != 5 {
		t.Fatalf("nodes = %v, want all 5 signal nodes deduped", wsNodeIDs(ws))
	}
	for id, want := range map[string][]string{
		"n1": {SignalCited},
		"n2": {SignalJudgeRelevant},
		"n3": {SignalExploreViewed},
		"n4": {SignalDiveViewed, SignalDiveSubmitted},
		"n5": {SignalCited, SignalExploreViewed},
	} {
		n := wsNode(t, ws, id)
		if len(n.Signals) != len(want) {
			t.Fatalf("node %s signals = %v, want %v", id, n.Signals, want)
		}
		for _, w := range want {
			if !n.HasSignal(w) {
				t.Fatalf("node %s signals = %v, want %v", id, n.Signals, want)
			}
		}
		if n.Epistemic != StatusAccepted || n.Level < 0 {
			t.Fatalf("node %s = %+v, want epistemic %s and a computed level", id, n, StatusAccepted)
		}
	}
}

// TestWorkingSetTruncatesBySignalWeight checks the total node cap: when the
// pool exceeds MaxNodes, query-log citations win over weaker signals (spec
// §4.3 "query_log 引用优先").
func TestWorkingSetTruncatesBySignalWeight(t *testing.T) {
	store := newTestStore(t)
	seedGraphNode(t, store, 1, "nc", "cited node")
	seedGraphNode(t, store, 1, "nj", "judge node")
	seedGraphNode(t, store, 1, "ne", "explore node")
	recordQueryAt(t, store, "t1", time.Now().UTC().Add(-time.Hour), []string{"nc"}, []string{"nj"})

	g, err := LoadGraph(store, 1)
	if err != nil {
		t.Fatalf("LoadGraph: %v", err)
	}
	signals := wsSignals([][]string{{"ne"}}, nil)
	cfg := DefaultWorkingSetConfig()
	cfg.MaxNodes = 2
	b := NewWorkingSetBuilder(store, signals, nil, cfg, 8)

	ws, err := b.Build(context.Background(), g, 1, nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	got := strings.Join(wsNodeIDs(ws), ",")
	if got != "nc,nj" && got != "nj,nc" {
		t.Fatalf("nodes = %v, want {nc,nj}; the explore-only node must be truncated first", got)
	}
}

// TestWorkingSetCursorIsIncremental checks the op-log watermark (spec §4.3):
// a recorded cursor keeps repeated builds from re-reading the same window,
// and only newer entries are consumed on the next build.
func TestWorkingSetCursorIsIncremental(t *testing.T) {
	store := newTestStore(t)
	seedGraphNode(t, store, 1, "n1", "first window body")
	seedGraphNode(t, store, 1, "n2", "second window body")
	base := time.Now().UTC().Add(-2 * time.Hour)
	recordQueryAt(t, store, "t1", base, []string{"n1"}, nil)
	recordQueryAt(t, store, "t2", base.Add(time.Minute), []string{"n1"}, nil)

	g, err := LoadGraph(store, 1)
	if err != nil {
		t.Fatalf("LoadGraph: %v", err)
	}
	b := NewWorkingSetBuilder(store, wsSignals(nil, nil), nil, DefaultWorkingSetConfig(), 8)

	ws1, err := b.Build(context.Background(), g, 1, nil)
	if err != nil {
		t.Fatalf("Build 1: %v", err)
	}
	if ws1.Entries != 2 || !ws1.Cursor.Equal(base.Add(time.Minute)) {
		t.Fatalf("Build 1: entries = %d cursor = %v, want 2 / %v", ws1.Entries, ws1.Cursor, base.Add(time.Minute))
	}
	if err := b.RecordCursor(1, ws1); err != nil {
		t.Fatalf("RecordCursor: %v", err)
	}

	// A rebuild without new query-log activity re-consumes nothing.
	ws2, err := b.Build(context.Background(), g, 1, nil)
	if err != nil {
		t.Fatalf("Build 2: %v", err)
	}
	if ws2.Entries != 0 || len(ws2.Nodes) != 0 {
		t.Fatalf("Build 2: entries = %d nodes = %v, want 0 / empty", ws2.Entries, wsNodeIDs(ws2))
	}

	// New entries after the watermark are consumed; old ones stay skipped.
	recordQueryAt(t, store, "t3", base.Add(2*time.Minute), []string{"n2"}, nil)
	ws3, err := b.Build(context.Background(), g, 1, nil)
	if err != nil {
		t.Fatalf("Build 3: %v", err)
	}
	if ws3.Entries != 1 || len(ws3.Nodes) != 1 || ws3.Nodes[0].NodeID != "n2" {
		t.Fatalf("Build 3: entries = %d nodes = %v, want 1 / [n2]", ws3.Entries, wsNodeIDs(ws3))
	}
}

// TestWorkingSetCursorEntryFallbackCap checks the 条数兜底: a window larger
// than MaxWindowEntries is truncated to the NEWEST entries, never the oldest.
func TestWorkingSetCursorEntryFallbackCap(t *testing.T) {
	store := newTestStore(t)
	seedGraphNode(t, store, 1, "n-old", "oldest entry body")
	seedGraphNode(t, store, 1, "n-new", "newest entry body")
	base := time.Now().UTC().Add(-24 * time.Hour)
	for i := 0; i < DefaultWorkingSetConfig().MaxWindowEntries+44; i++ {
		id := "n-old"
		if i >= 44 {
			id = "n-new"
		}
		recordQueryAt(t, store, fmt.Sprintf("t%03d", i), base.Add(time.Duration(i)*time.Second), []string{id}, nil)
	}

	g, err := LoadGraph(store, 1)
	if err != nil {
		t.Fatalf("LoadGraph: %v", err)
	}
	b := NewWorkingSetBuilder(store, wsSignals(nil, nil), nil, DefaultWorkingSetConfig(), 8)
	ws, err := b.Build(context.Background(), g, 1, nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if ws.Entries != DefaultWorkingSetConfig().MaxWindowEntries {
		t.Fatalf("entries = %d, want fallback cap %d", ws.Entries, DefaultWorkingSetConfig().MaxWindowEntries)
	}
	if wsNode(t, ws, "n-new") == nil {
		t.Fatalf("nodes = %v, want the newest entry's node to survive the cap", wsNodeIDs(ws))
	}
}

// TestWorkingSetNeighborhoodExpansion checks the 1-hop expansion: parents,
// children, and relation neighbors of signal nodes join the pool with the
// neighbor signal, bounded by the fanout budget.
func TestWorkingSetNeighborhoodExpansion(t *testing.T) {
	store := newTestStore(t)
	// seedGraphNode saves directly; edges need a persisted graph, so build
	// one through the store's graph file layout.
	seedGraphNode(t, store, 1, "hub", "hub body")
	seedGraphNode(t, store, 1, "parent", "parent body")
	seedGraphNode(t, store, 1, "child", "child body")
	seedGraphNode(t, store, 1, "peer", "peer body")
	seedGraphNode(t, store, 1, "far", "far body")
	g, err := LoadGraph(store, 1)
	if err != nil {
		t.Fatalf("LoadGraph: %v", err)
	}
	if err := g.AddHierarchyEdge(&Edge{EdgeID: "h1", From: "parent", To: "hub"}, 8); err != nil {
		t.Fatalf("hierarchy edge: %v", err)
	}
	if err := g.AddHierarchyEdge(&Edge{EdgeID: "h2", From: "hub", To: "child"}, 8); err != nil {
		t.Fatalf("hierarchy edge: %v", err)
	}
	if err := g.AddRelationEdge(&Edge{EdgeID: "r1", Type: EdgeTypeSupports, From: "hub", To: "peer"}); err != nil {
		t.Fatalf("relation edge: %v", err)
	}
	if err := persistGraph(store, 1, g); err != nil {
		t.Fatalf("persistGraph: %v", err)
	}
	recordQueryAt(t, store, "t1", time.Now().UTC().Add(-time.Hour), []string{"hub"}, nil)

	g, err = LoadGraph(store, 1)
	if err != nil {
		t.Fatalf("LoadGraph: %v", err)
	}
	b := NewWorkingSetBuilder(store, wsSignals(nil, nil), nil, DefaultWorkingSetConfig(), 8)
	ws, err := b.Build(context.Background(), g, 1, nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	for _, id := range []string{"parent", "child", "peer"} {
		n := wsNode(t, ws, id)
		if !n.HasSignal(SignalNeighbor) {
			t.Fatalf("node %s signals = %v, want %s", id, n.Signals, SignalNeighbor)
		}
	}
	for _, id := range []string{"hub"} {
		n := wsNode(t, ws, id)
		if n.HasSignal(SignalNeighbor) {
			t.Fatalf("signal node %s must not carry the neighbor signal: %v", id, n.Signals)
		}
	}
	if findWSNode(ws, "far") != nil {
		t.Fatalf("2-hop node far must stay outside the working set: %v", wsNodeIDs(ws))
	}
}

// TestWorkingSetStagingSimilarity checks the cold-start channel: each
// pending staging segment injects its top-K similar graph nodes (BM25-only
// without an embedder) so duplicates of new evidence enter the pool.
func TestWorkingSetStagingSimilarity(t *testing.T) {
	store := newTestStore(t)
	seedGraphNode(t, store, 1, "n-rel", "postgres streaming replication lag tuning notes")
	seedGraphNode(t, store, 1, "n-unrel", "cooking pasta alfredo recipe")
	if err := store.WriteStagingSegment("seg-1", []byte("postgres replication lag keeps growing during streaming replay")); err != nil {
		t.Fatalf("WriteStagingSegment: %v", err)
	}

	g, err := LoadGraph(store, 1)
	if err != nil {
		t.Fatalf("LoadGraph: %v", err)
	}
	b := NewWorkingSetBuilder(store, wsSignals(nil, nil), nil, DefaultWorkingSetConfig(), 8)
	ws, err := b.Build(context.Background(), g, 1, []stagingSummary{{id: "seg-1", body: "postgres replication lag keeps growing during streaming replay"}})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	n := wsNode(t, ws, "n-rel")
	if !n.HasSignal(SignalStagingSimilar) {
		t.Fatalf("n-rel signals = %v, want %s", n.Signals, SignalStagingSimilar)
	}
	if u := findWSNode(ws, "n-unrel"); u != nil && u.HasSignal(SignalStagingSimilar) {
		t.Fatalf("unrelated node %s matched the staging segment: %v", u.NodeID, u.Signals)
	}
}

// TestWorkingSetSummaryTruncation checks the per-node summary cap of 400
// runes (aligned with dive's diveNodeBodyMaxRunes).
func TestWorkingSetSummaryTruncation(t *testing.T) {
	store := newTestStore(t)
	long := strings.Repeat("长", 600)
	seedGraphNode(t, store, 1, "n1", long)
	recordQueryAt(t, store, "t1", time.Now().UTC().Add(-time.Hour), []string{"n1"}, nil)

	g, err := LoadGraph(store, 1)
	if err != nil {
		t.Fatalf("LoadGraph: %v", err)
	}
	b := NewWorkingSetBuilder(store, wsSignals(nil, nil), nil, DefaultWorkingSetConfig(), 8)
	ws, err := b.Build(context.Background(), g, 1, nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	n := wsNode(t, ws, "n1")
	// dive's truncateRunes keeps 400 content runes and appends an ellipsis
	// marker; the summary is its body prefix plus that marker.
	if got := utf8.RuneCountInString(n.Summary); got != 401 {
		t.Fatalf("summary runes = %d, want 400 content + ellipsis", got)
	}
	if content := strings.TrimSuffix(n.Summary, "…"); !strings.HasPrefix(long, content) || utf8.RuneCountInString(content) != 400 {
		t.Fatalf("summary must be a 400-rune body prefix plus the ellipsis marker")
	}
}

// TestBuildPromptIncludesWorkingSet checks the prompt injection: the
// working-set section carries ids, epistemic state and signal labels, the
// merge_node manifest entry exists, and mutations are restricted to the
// working set. An empty working set must leave the prompt unchanged.
func TestBuildPromptIncludesWorkingSet(t *testing.T) {
	store := newTestStore(t)
	c := NewConsolidator(store, nil, DefaultConsolidateConfig(), "test", nil, nil)

	ws := &WorkingSet{Nodes: []WorkingSetNode{{
		NodeID:    "n1",
		Summary:   "alpha routing notes",
		Epistemic: StatusAccepted,
		Level:     1,
		Signals:   []string{SignalCited, SignalNeighbor},
	}}}
	prompt := c.buildPrompt(nil, graphStats{}, "", ws)
	for _, want := range []string{"n1", "alpha routing notes", StatusAccepted, SignalCited, "merge_node", "input_node_ids", "Working set"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}

	empty := c.buildPrompt(nil, graphStats{}, "", nil)
	if strings.Contains(empty, "merge_node guidance") || strings.Contains(empty, "n1") {
		t.Fatalf("empty working set must not alter the prompt:\n%s", empty)
	}
	if !strings.Contains(empty, "merge_node") {
		t.Fatalf("merge_node must be part of the operations manifest regardless of working set:\n%s", empty)
	}
}

// TestConsolidateUsesWorkingSet wires the full path: the Consolidator builds
// the working set from the query log, injects it into the prompt, and the
// agent's merge_node over two cited nodes lands in the graph with the cursor
// recorded ahead of the applied op (unification spec §4.3).
func TestConsolidateUsesWorkingSet(t *testing.T) {
	store := newTestStore(t)
	seedGraphNode(t, store, 1, "n1", "alpha routing notes")
	seedGraphNode(t, store, 1, "n2", "alpha routing duplicate notes")
	if err := store.WriteStagingSegment("seg-1", []byte("alpha routing consolidated evidence")); err != nil {
		t.Fatalf("WriteStagingSegment: %v", err)
	}
	recordQueryAt(t, store, "t1", time.Now().UTC().Add(-time.Hour), []string{"n1", "n2"}, nil)

	backend := &fakeConsolidateBackend{respond: func(string, int) string {
		return consolidateOpsJSON(ConsolidateOp{
			Op:           OpMergeNode,
			InputNodeIDs: []string{"n1", "n2"},
			Node:         &Node{NodeID: "n3", Body: "alpha routing merged", SegmentRefs: []string{"seg-1"}},
		})
	}}
	cfg := DefaultConsolidateConfig()
	cfg.TTVTrajectories = 1
	c := NewConsolidator(store, backend, cfg, "test", nil, nil)
	c.SetWorkingSetBuilder(NewWorkingSetBuilder(store, wsSignals(nil, nil), nil, DefaultWorkingSetConfig(), 8))

	res, err := c.Consolidate(context.Background())
	if err != nil {
		t.Fatalf("Consolidate: %v", err)
	}
	if res.OpsApplied != 1 || len(res.Rejected) != 0 {
		t.Fatalf("OpsApplied = %d Rejected = %+v, want 1/none", res.OpsApplied, res.Rejected)
	}

	prompt := backend.allPrompts()
	for _, want := range []string{"Working set", "n1", "n2", SignalCited} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}

	g, err := LoadGraph(store, 1)
	if err != nil {
		t.Fatalf("LoadGraph: %v", err)
	}
	if g.Node("n3") == nil || g.Node("n1").Epistemic != StatusSuperseded || g.Node("n2").Epistemic != StatusSuperseded {
		t.Fatalf("merge did not land: n3=%v n1=%v n2=%v", g.Node("n3"), g.Node("n1"), g.Node("n2"))
	}

	entries, err := NewOpLogger(store).Read(1)
	if err != nil {
		t.Fatalf("read op log: %v", err)
	}
	if len(entries) != 2 || entries[0].Op != OpWorkingSetCursor || entries[1].Op != OpMergeNode {
		t.Fatalf("op log = %+v, want working_set cursor then merge_node", entries)
	}
}

// Cost invariant (review checklist item 6, unification spec §4.3): the
// working set — and therefore the consolidation prompt's working-set
// section — is bounded by config, not by graph size. A graph with thousands
// of nodes and a saturated query-log window yields the same capped pool and
// the same absolute summary ceiling as a small graph.
func TestWorkingSetCostInvariantToGraphSize(t *testing.T) {
	cfg := DefaultWorkingSetConfig()
	ceiling := func(ws *WorkingSet) (nodes, runes int) {
		for _, n := range ws.Nodes {
			runes += len([]rune(n.Summary))
		}
		return len(ws.Nodes), runes
	}

	smallStore := newTestStore(t)
	for i := 0; i < 5; i++ {
		seedGraphNode(t, smallStore, 1, fmt.Sprintf("s%d", i), strings.Repeat("small body ", 40))
	}
	base := time.Now().UTC().Add(-24 * time.Hour)
	for i := 0; i < 5; i++ {
		recordQueryAt(t, smallStore, fmt.Sprintf("s%03d", i), base.Add(time.Duration(i)*time.Second), []string{fmt.Sprintf("s%d", i)}, nil)
	}
	smallGraph, err := LoadGraph(smallStore, 1)
	if err != nil {
		t.Fatal(err)
	}
	smallWS, err := NewWorkingSetBuilder(smallStore, wsSignals(nil, nil), nil, cfg, 8).Build(context.Background(), smallGraph, 1, nil)
	if err != nil {
		t.Fatalf("small Build: %v", err)
	}

	const largeNodes = 3000
	const largeQueries = 600
	largeStore := newTestStore(t)
	for i := 0; i < largeNodes; i++ {
		seedGraphNode(t, largeStore, 1, fmt.Sprintf("n%04d", i), strings.Repeat("large body ", 60))
	}
	for i := 0; i < largeQueries; i++ {
		recordQueryAt(t, largeStore, fmt.Sprintf("t%03d", i), base.Add(time.Duration(i)*time.Second),
			[]string{fmt.Sprintf("n%04d", i*5%largeNodes)}, nil)
	}
	largeGraph, err := LoadGraph(largeStore, 1)
	if err != nil {
		t.Fatal(err)
	}
	largeWS, err := NewWorkingSetBuilder(largeStore, wsSignals(nil, nil), nil, cfg, 8).Build(context.Background(), largeGraph, 1, nil)
	if err != nil {
		t.Fatalf("large Build: %v", err)
	}

	smallCount, smallRunes := ceiling(smallWS)
	largeCount, largeRunes := ceiling(largeWS)
	if largeCount > cfg.MaxNodes || smallCount > cfg.MaxNodes {
		t.Fatalf("working set nodes small=%d large=%d, cap=%d", smallCount, largeCount, cfg.MaxNodes)
	}
	absolute := cfg.MaxNodes*cfg.NodeBodyRunes + cfg.MaxNodes // +1 newline per node
	if largeRunes > absolute || smallRunes > absolute {
		t.Fatalf("working set runes small=%d large=%d, ceiling=%d", smallRunes, largeRunes, absolute)
	}
	if largeCount < cfg.MaxNodes {
		t.Fatalf("large graph produced only %d nodes; the pool should saturate the cap and prove truncation", largeCount)
	}
}
