package memorygraph

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s := NewStore(filepath.Join(t.TempDir(), "memory_graph"))
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	return s
}

func TestStoreInitIdempotent(t *testing.T) {
	s := newTestStore(t)

	for _, dir := range []string{"versions", "shared/embeddings", "staging/segments", "query_log", "op_log"} {
		if info, err := os.Stat(filepath.Join(s.Root, dir)); err != nil || !info.IsDir() {
			t.Fatalf("expected dir %s: %v", dir, err)
		}
	}
	if v, err := s.CurrentVersion(); err != nil || v != 1 {
		t.Fatalf("CurrentVersion = %d, %v; want 1", v, err)
	}
	m, err := s.LoadManifest(1)
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if m.Version != 1 || m.CreatedBy != "init" {
		t.Fatalf("manifest = %+v; want version 1 created by init", m)
	}

	// Second Init must not clobber the existing state.
	if err := s.SwitchCurrent(1); err != nil {
		t.Fatalf("SwitchCurrent: %v", err)
	}
	if err := s.Init(); err != nil {
		t.Fatalf("second Init: %v", err)
	}
	versions, err := s.ListVersions()
	if err != nil || !slices.Equal(versions, []int{1}) {
		t.Fatalf("ListVersions = %v, %v; want [1]", versions, err)
	}
}

// Cold-start wipe (spec §7, no backward compatibility): a store whose data
// predates the protocol marker is generation-1 state; Init wipes it and
// rebuilds a fresh empty store.
func TestStoreInitWipesUnmarkedGeneration1Data(t *testing.T) {
	s := newTestStore(t)
	seedGraphNode(t, s, 1, "legacy", "legacy body")
	if err := s.AppendQueryLog("w1", &QueryLogEntry{TraceID: "legacy", Query: "legacy query", Version: 1}); err != nil {
		t.Fatalf("AppendQueryLog: %v", err)
	}
	// Age the store into generation 1: the marker never existed.
	if err := os.Remove(s.protocolFile()); err != nil {
		t.Fatalf("remove protocol marker: %v", err)
	}
	regressionFile := filepath.Join(s.Root, "regression_set.jsonl")
	if err := os.WriteFile(regressionFile, []byte("{\"query\":\"old\"}\n"), 0o644); err != nil {
		t.Fatalf("seed regression_set.jsonl: %v", err)
	}

	if err := s.Init(); err != nil {
		t.Fatalf("Init over generation-1 store: %v", err)
	}

	versions, err := s.ListVersions()
	if err != nil || !slices.Equal(versions, []int{1}) {
		t.Fatalf("ListVersions after wipe = %v, %v; want fresh [1]", versions, err)
	}
	if v, err := s.CurrentVersion(); err != nil || v != 1 {
		t.Fatalf("CurrentVersion after wipe = %d, %v; want 1", v, err)
	}
	g, err := LoadGraph(s, 1)
	if err != nil || len(g.Nodes()) != 0 {
		t.Fatalf("graph after wipe = %d nodes, %v; want empty", len(g.Nodes()), err)
	}
	if _, err := os.Stat(regressionFile); !os.IsNotExist(err) {
		t.Fatalf("regression_set.jsonl survived the wipe (stat err = %v)", err)
	}
	windows, err := s.ListQueryLogWindows()
	if err != nil || len(windows) != 0 {
		t.Fatalf("query log windows after wipe = %v, %v; want none", windows, err)
	}
	marker, err := os.ReadFile(s.protocolFile())
	if err != nil || string(marker) != strconv.Itoa(GraphProtocolGeneration) {
		t.Fatalf("protocol marker = %q, %v; want %d", string(marker), err, GraphProtocolGeneration)
	}
}

// The scoped identity marker is immutable (spec §3) and must survive the
// protocol wipe: the directory keeps its identity while all data goes.
func TestStoreInitWipePreservesScopedIdentity(t *testing.T) {
	ws, pid := "11111111-1111-1111-1111-111111111111", "22222222-2222-2222-2222-222222222222"
	dir, err := EnsureScopedDir(t.TempDir(), ws, GraphDirKindProject, pid)
	if err != nil {
		t.Fatalf("EnsureScopedDir: %v", err)
	}
	s := NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	seedGraphNode(t, s, 1, "legacy", "legacy body")
	// Age the store into generation 1: the marker never existed.
	if err := os.Remove(s.protocolFile()); err != nil {
		t.Fatalf("remove protocol marker: %v", err)
	}

	if err := s.Init(); err != nil {
		t.Fatalf("Init over generation-1 store: %v", err)
	}
	if err := VerifyGraphIdentity(dir, GraphIdentity{
		WorkspaceID: ws, Kind: string(GraphDirKindProject), OwnerID: pid,
	}); err != nil {
		t.Fatalf("identity did not survive the wipe: %v", err)
	}
	g, err := LoadGraph(s, 1)
	if err != nil || len(g.Nodes()) != 0 {
		t.Fatalf("graph after wipe = %d nodes, %v; want empty", len(g.Nodes()), err)
	}
}

// A store already at the current protocol generation keeps all its data
// across re-Init; only stale generations are wiped.
func TestStoreInitKeepsCurrentGenerationData(t *testing.T) {
	s := newTestStore(t)
	seedGraphNode(t, s, 1, "n1", "alpha beta")
	if err := s.AppendQueryLog("w1", &QueryLogEntry{TraceID: "t1", Query: "alpha", Version: 1}); err != nil {
		t.Fatalf("AppendQueryLog: %v", err)
	}

	if err := s.Init(); err != nil {
		t.Fatalf("re-Init: %v", err)
	}
	g, err := LoadGraph(s, 1)
	if err != nil || g.Node("n1") == nil {
		t.Fatalf("re-Init lost current-generation data: %v", err)
	}
	windows, err := s.ListQueryLogWindows()
	if err != nil || len(windows) != 1 {
		t.Fatalf("query log windows after re-Init = %v, %v; want 1", windows, err)
	}
}

// An explicit older-generation marker triggers the same wipe.
func TestStoreInitWipesOlderMarkerGeneration(t *testing.T) {
	s := newTestStore(t)
	seedGraphNode(t, s, 1, "n1", "alpha beta")
	if err := os.WriteFile(s.protocolFile(), []byte("1"), 0o644); err != nil {
		t.Fatalf("downgrade marker: %v", err)
	}

	if err := s.Init(); err != nil {
		t.Fatalf("Init over older marker: %v", err)
	}
	g, err := LoadGraph(s, 1)
	if err != nil || len(g.Nodes()) != 0 {
		t.Fatalf("graph after wipe = %d nodes, %v; want empty", len(g.Nodes()), err)
	}
	marker, err := os.ReadFile(s.protocolFile())
	if err != nil || string(marker) != strconv.Itoa(GraphProtocolGeneration) {
		t.Fatalf("protocol marker = %q, %v; want %d", string(marker), err, GraphProtocolGeneration)
	}
}

func TestStoreInitRejectsNewerMarkerGeneration(t *testing.T) {
	s := newTestStore(t)
	seedGraphNode(t, s, 1, "n1", "alpha beta")
	newer := GraphProtocolGeneration + 1
	if err := os.WriteFile(s.protocolFile(), []byte(strconv.Itoa(newer)), 0o644); err != nil {
		t.Fatalf("upgrade marker: %v", err)
	}

	err := s.Init()
	if err == nil || !strings.Contains(err.Error(), "newer than supported") {
		t.Fatalf("Init over newer marker = %v, want newer-generation error", err)
	}
	g, err := LoadGraph(s, 1)
	if err != nil || g.Node("n1") == nil {
		t.Fatalf("newer-generation rejection changed graph data: %v", err)
	}
	marker, err := os.ReadFile(s.protocolFile())
	if err != nil || string(marker) != strconv.Itoa(newer) {
		t.Fatalf("protocol marker = %q, %v; want %d", string(marker), err, newer)
	}
}

func TestVersionCreateCopySwitchPersist(t *testing.T) {
	s := newTestStore(t)
	n := &Node{NodeID: "n1", Body: "hello", CreatedBy: CreatorIngester, CreatedVersion: 1}
	if err := s.SaveNode(1, n); err != nil {
		t.Fatalf("SaveNode: %v", err)
	}
	hier := []*Edge{{EdgeID: "h1", Type: EdgeTypeSummarizes, From: "n1", To: "n1"}}
	if err := s.SaveEdges(1, hier, nil); err != nil {
		t.Fatalf("SaveEdges: %v", err)
	}

	newV, err := s.CreateVersionFrom(1, CreatorConsolidator)
	if err != nil {
		t.Fatalf("CreateVersionFrom: %v", err)
	}
	if newV != 2 {
		t.Fatalf("CreateVersionFrom = %d; want 2", newV)
	}
	m, err := s.LoadManifest(2)
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if m.ParentVersion != 1 || m.CreatedBy != CreatorConsolidator || m.NodeCount != 1 || m.HierEdgeCount != 1 {
		t.Fatalf("manifest = %+v", m)
	}
	nodes, err := s.LoadNodes(2)
	if err != nil || len(nodes) != 1 || nodes[0].Body != "hello" {
		t.Fatalf("copied nodes = %v, %v", nodes, err)
	}

	if err := s.SwitchCurrent(2); err != nil {
		t.Fatalf("SwitchCurrent: %v", err)
	}
	// Re-open the store: the current pointer must persist.
	reopened := NewStore(s.Root)
	if v, err := reopened.CurrentVersion(); err != nil || v != 2 {
		t.Fatalf("CurrentVersion after reopen = %d, %v; want 2", v, err)
	}
	if err := reopened.SwitchCurrent(99); err == nil {
		t.Fatal("SwitchCurrent to missing version: want error")
	}
	if _, err := reopened.CreateVersionFrom(99, "x"); err == nil {
		t.Fatal("CreateVersionFrom from missing parent: want error")
	}
}

func TestGCKeepsCurrent(t *testing.T) {
	s := newTestStore(t)
	parent := 1
	for i := 0; i < 4; i++ {
		v, err := s.CreateVersionFrom(parent, CreatorConsolidator)
		if err != nil {
			t.Fatalf("CreateVersionFrom: %v", err)
		}
		parent = v
	}
	// Versions 1..5 exist; make v2 current (outside the keep window).
	if err := s.SwitchCurrent(2); err != nil {
		t.Fatalf("SwitchCurrent: %v", err)
	}
	if err := s.GC(2); err != nil {
		t.Fatalf("GC: %v", err)
	}
	versions, err := s.ListVersions()
	if err != nil {
		t.Fatalf("ListVersions: %v", err)
	}
	want := []int{2, 4, 5}
	if !slices.Equal(versions, want) {
		t.Fatalf("ListVersions = %v; want %v", versions, want)
	}
	if v, _ := s.CurrentVersion(); v != 2 {
		t.Fatalf("CurrentVersion = %d; want 2", v)
	}
}

func TestStagingLifecycle(t *testing.T) {
	s := newTestStore(t)
	if err := s.WriteStagingSegment("seg-1", []byte("summary a")); err != nil {
		t.Fatalf("WriteStagingSegment: %v", err)
	}
	if err := s.WriteStagingSegment("seg-1", []byte("other")); err == nil {
		t.Fatal("rewrite staging segment: want immutability error")
	}
	b, err := s.ReadStagingSegment("seg-1")
	if err != nil || string(b) != "summary a" {
		t.Fatalf("ReadStagingSegment = %q, %v", b, err)
	}
	ids, err := s.ListStagingSegments()
	if err != nil || !slices.Equal(ids, []string{"seg-1"}) {
		t.Fatalf("ListStagingSegments = %v, %v", ids, err)
	}
	if err := s.WriteStagingSegment("../escape", nil); err == nil {
		t.Fatal("path-escaping segment id: want error")
	}
	if err := s.DeleteStagingSegment("seg-1"); err != nil {
		t.Fatalf("DeleteStagingSegment: %v", err)
	}
	if _, err := s.ReadStagingSegment("seg-1"); err == nil {
		t.Fatal("read deleted segment: want error")
	}
}

func TestQueryLogAppendAndUpdate(t *testing.T) {
	s := newTestStore(t)
	entries := []*QueryLogEntry{
		{TraceID: "t1", Query: "q1", Version: 1, Found: true},
		{TraceID: "t2", Query: "q2", Version: 1},
	}
	for _, e := range entries {
		if err := s.AppendQueryLog("w1", e); err != nil {
			t.Fatalf("AppendQueryLog: %v", err)
		}
	}
	found, err := s.UpdateQueryLogEntry("w1", "t2", func(e *QueryLogEntry) {
		e.JudgeDone = true
		e.JudgeScore = 0.9
		e.RelevantNodes = []string{"n1"}
	})
	if err != nil || !found {
		t.Fatalf("UpdateQueryLogEntry = %v, %v", found, err)
	}
	got, err := s.ReadQueryLog("w1")
	if err != nil {
		t.Fatalf("ReadQueryLog: %v", err)
	}
	if len(got) != 2 || got[0].TraceID != "t1" || got[1].TraceID != "t2" {
		t.Fatalf("ReadQueryLog = %+v", got)
	}
	if !got[1].JudgeDone || got[1].JudgeScore != 0.9 || !slices.Equal(got[1].RelevantNodes, []string{"n1"}) {
		t.Fatalf("mutated entry = %+v", got[1])
	}
	if got[0].JudgeDone {
		t.Fatalf("unrelated entry mutated: %+v", got[0])
	}
	found, err = s.UpdateQueryLogEntry("w1", "missing", func(e *QueryLogEntry) {})
	if err != nil || found {
		t.Fatalf("UpdateQueryLogEntry missing = %v, %v; want false, nil", found, err)
	}
	windows, err := s.ListQueryLogWindows()
	if err != nil || !slices.Equal(windows, []string{"w1"}) {
		t.Fatalf("ListQueryLogWindows = %v, %v", windows, err)
	}
}

func TestNodeSaveLoadRoundTrip(t *testing.T) {
	s := newTestStore(t)
	validFrom := time.Date(2026, 8, 14, 10, 0, 0, 0, time.UTC)
	n := &Node{
		NodeID:         "n1",
		SegmentRefs:    []string{"seg-a", "seg-b"},
		Level:          2,
		Epistemic:      StatusAccepted,
		EntityRefs:     []string{"ent-1"},
		ObservedAt:     validFrom,
		ValidFrom:      &validFrom,
		TemporalStatus: TemporalCurrent,
		Tags:           []string{"dispatch"},
		CreatedBy:      CreatorConsolidator,
		CreatedVersion: 1,
		UpdatedVersion: 3,
		Body:           "the payload is routed by priority\nsecond line",
	}
	if err := s.SaveNode(1, n); err != nil {
		t.Fatalf("SaveNode: %v", err)
	}
	if n.ContentHash != ComputeContentHash(n.Body) {
		t.Fatalf("ContentHash = %q; want %q", n.ContentHash, ComputeContentHash(n.Body))
	}
	// Stale hash must be recomputed on save.
	n.ContentHash = "stale"
	if err := s.SaveNode(1, n); err != nil {
		t.Fatalf("SaveNode: %v", err)
	}
	nodes, err := s.LoadNodes(1)
	if err != nil || len(nodes) != 1 {
		t.Fatalf("LoadNodes = %v, %v", nodes, err)
	}
	got := nodes[0]
	if got.Body != n.Body {
		t.Fatalf("Body = %q; want %q", got.Body, n.Body)
	}
	if got.ContentHash != ComputeContentHash(n.Body) {
		t.Fatalf("loaded ContentHash = %q", got.ContentHash)
	}
	if got.NodeID != n.NodeID || got.Level != 2 || got.Epistemic != StatusAccepted ||
		got.TemporalStatus != TemporalCurrent || got.CreatedBy != CreatorConsolidator ||
		got.CreatedVersion != 1 || got.UpdatedVersion != 3 ||
		!slices.Equal(got.SegmentRefs, n.SegmentRefs) || !slices.Equal(got.EntityRefs, n.EntityRefs) ||
		!slices.Equal(got.Tags, n.Tags) || !got.ObservedAt.Equal(validFrom) ||
		got.ValidFrom == nil || !got.ValidFrom.Equal(validFrom) {
		t.Fatalf("frontmatter mismatch: %+v", got)
	}
}

func TestEdgesSaveLoad(t *testing.T) {
	s := newTestStore(t)
	hier := []*Edge{{EdgeID: "h1", Type: EdgeTypeSummarizes, From: "p", To: "c", CreatedBy: CreatorConsolidator, CreatedVersion: 1}}
	rel := []*Edge{{EdgeID: "r1", Type: EdgeTypeCauses, From: "a", To: "edge:h9", Epistemic: EpistemicInferred, Confidence: 0.8, CreatedBy: CreatorConsolidator, CreatedVersion: 1}}
	if err := s.SaveEdges(1, hier, rel); err != nil {
		t.Fatalf("SaveEdges: %v", err)
	}
	gotHier, gotRel, err := s.LoadEdges(1)
	if err != nil {
		t.Fatalf("LoadEdges: %v", err)
	}
	if len(gotHier) != 1 || gotHier[0].EdgeID != "h1" || gotHier[0].To != "c" {
		t.Fatalf("hier = %+v", gotHier)
	}
	if len(gotRel) != 1 || gotRel[0].Type != EdgeTypeCauses || gotRel[0].To != "edge:h9" || gotRel[0].Confidence != 0.8 {
		t.Fatalf("rel = %+v", gotRel)
	}
	// Version without edge files yields empty lists.
	emptyHier, emptyRel, err := s.LoadEdges(42)
	if err != nil || len(emptyHier) != 0 || len(emptyRel) != 0 {
		t.Fatalf("LoadEdges(missing) = %v, %v, %v", emptyHier, emptyRel, err)
	}
}

func TestEmbeddingPath(t *testing.T) {
	s := newTestStore(t)
	p := s.EmbeddingPath("sha256:abc")
	want := filepath.Join(s.Root, "shared", "embeddings", "sha256:abc.vec")
	if p != want {
		t.Fatalf("EmbeddingPath = %q; want %q", p, want)
	}
	if info, err := os.Stat(filepath.Dir(p)); err != nil || !info.IsDir() {
		t.Fatalf("embedding dir missing: %v", err)
	}
}

func TestConcurrentAccess(t *testing.T) {
	s := newTestStore(t)
	done := make(chan error, 20)
	for i := 0; i < 10; i++ {
		go func(i int) {
			done <- s.AppendQueryLog("w", &QueryLogEntry{TraceID: fmt.Sprintf("t%d", i)})
		}(i)
		go func(i int) {
			_, err := s.CurrentVersion()
			done <- err
		}(i)
	}
	for i := 0; i < 20; i++ {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	entries, err := s.ReadQueryLog("w")
	if err != nil || len(entries) != 10 {
		t.Fatalf("ReadQueryLog = %d entries, %v; want 10", len(entries), err)
	}
}
