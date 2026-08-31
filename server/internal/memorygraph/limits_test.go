package memorygraph

import (
	"fmt"
	"strings"
	"testing"
)

func newLimitsConsolidator(t *testing.T, maxRel, maxFan int) *Consolidator {
	t.Helper()
	cfg := DefaultConsolidateConfig()
	if maxRel != 0 {
		cfg.MaxRelationEdges = maxRel
	}
	if maxFan != 0 {
		cfg.MaxFanout = maxFan
	}
	return NewConsolidator(newTestStore(t), nil, cfg, testConsolidateScope(), nil, nil)
}

func mustAddNode(t *testing.T, g *Graph, id string, level int) {
	t.Helper()
	n := &Node{NodeID: id, Level: level, CreatedBy: CreatorConsolidator}
	if level == SourceLayerLevel {
		n.CreatedBy = CreatorIngester
		n.SourceKind = SourceKindFile
	}
	if err := g.AddNode(n); err != nil {
		t.Fatalf("AddNode %s: %v", id, err)
	}
}

func mustSeedRelation(t *testing.T, g *Graph, id, from, to string) {
	t.Helper()
	if err := g.AddRelationEdge(&Edge{EdgeID: id, Type: EdgeTypeCauses, From: from, To: to}); err != nil {
		t.Fatalf("AddRelationEdge %s: %v", id, err)
	}
}

func applyAddRelation(c *Consolidator, g *Graph, e *Edge) (int, []RejectReason, error) {
	return c.applyOperations(g, 1, CreatorConsolidator, []ConsolidateOp{
		{Op: OpAddRelationEdge, Edge: e},
	})
}

func TestRelationDegreeNodeToNodeFromFull(t *testing.T) {
	const limit = 3
	c := newLimitsConsolidator(t, limit, 0)
	g := newGraph()
	mustAddNode(t, g, "A", 0)
	for i := 0; i < limit; i++ {
		to := fmt.Sprintf("B%d", i)
		mustAddNode(t, g, to, 0)
		mustSeedRelation(t, g, fmt.Sprintf("r-ab-%d", i), "A", to)
	}
	mustAddNode(t, g, "C", 0)
	before := len(g.RelationEdges())

	applied, rejected, err := applyAddRelation(c, g, &Edge{EdgeID: "r-overflow", Type: EdgeTypeCauses, From: "A", To: "C"})
	if err != nil {
		t.Fatalf("applyOperations: %v", err)
	}
	if applied != 0 {
		t.Fatalf("applied = %d, want 0", applied)
	}
	if len(rejected) != 1 {
		t.Fatalf("rejected = %+v, want 1", rejected)
	}
	if rejected[0].Op != OpAddRelationEdge {
		t.Fatalf("rejected op = %q, want %s", rejected[0].Op, OpAddRelationEdge)
	}
	if !strings.Contains(rejected[0].Reason, "A") {
		t.Fatalf("reject reason = %q, want it to name node A", rejected[0].Reason)
	}
	if len(g.RelationEdges()) != before {
		t.Fatalf("relation edge count = %d, want unchanged %d", len(g.RelationEdges()), before)
	}
	if edgeExists(g, "r-overflow") {
		t.Fatal("overflow edge was inserted")
	}
}

func TestRelationDegreeNodeToNodeToFull(t *testing.T) {
	const limit = 3
	c := newLimitsConsolidator(t, limit, 0)
	g := newGraph()
	mustAddNode(t, g, "S", 0)
	mustAddNode(t, g, "T", 0)
	for i := 0; i < limit; i++ {
		from := fmt.Sprintf("X%d", i)
		mustAddNode(t, g, from, 0)
		mustSeedRelation(t, g, fmt.Sprintf("r-xt-%d", i), from, "T")
	}
	before := len(g.RelationEdges())

	applied, rejected, err := applyAddRelation(c, g, &Edge{EdgeID: "r-to-full", Type: EdgeTypeCauses, From: "S", To: "T"})
	if err != nil {
		t.Fatalf("applyOperations: %v", err)
	}
	if applied != 0 || len(rejected) != 1 {
		t.Fatalf("applied=%d rejected=%+v, want 0/1", applied, rejected)
	}
	if !strings.Contains(rejected[0].Reason, "T") {
		t.Fatalf("reject reason = %q, want it to name target T", rejected[0].Reason)
	}
	if len(g.RelationEdges()) != before || edgeExists(g, "r-to-full") {
		t.Fatalf("graph mutated: edges=%d exists=%v", len(g.RelationEdges()), edgeExists(g, "r-to-full"))
	}
}

func TestRelationDegreeNodeToEdge(t *testing.T) {
	const limit = 3
	c := newLimitsConsolidator(t, limit, 0)
	g := newGraph()
	mustAddNode(t, g, "src", 0)
	mustAddNode(t, g, "a", 0)
	mustAddNode(t, g, "b", 0)
	mustSeedRelation(t, g, "r0", "a", "b")
	for i := 0; i < 20; i++ {
		from := fmt.Sprintf("ref%d", i)
		mustAddNode(t, g, from, 0)
		mustSeedRelation(t, g, fmt.Sprintf("r-ref-%d", i), from, "edge:r0")
	}

	// From at limit: rejected even though the referenced edge already has 20 refs.
	for i := 0; i < limit; i++ {
		to := fmt.Sprintf("full%d", i)
		mustAddNode(t, g, to, 0)
		mustSeedRelation(t, g, fmt.Sprintf("r-src-full-%d", i), "src", to)
	}
	before := len(g.RelationEdges())
	applied, rejected, err := applyAddRelation(c, g, &Edge{EdgeID: "r-edge-full", Type: EdgeTypeSupports, From: "src", To: "edge:r0"})
	if err != nil {
		t.Fatalf("apply at limit: %v", err)
	}
	if applied != 0 || len(rejected) != 1 {
		t.Fatalf("at limit: applied=%d rejected=%+v", applied, rejected)
	}
	if len(g.RelationEdges()) != before {
		t.Fatalf("at limit: graph mutated, edges %d -> %d", before, len(g.RelationEdges()))
	}

	// Free one source slot: From at limit-1, 20 refs on r0, accepted.
	g.DeleteEdge("r-src-full-0")
	before = len(g.RelationEdges())
	applied, rejected, err = applyAddRelation(c, g, &Edge{EdgeID: "r-edge-ok", Type: EdgeTypeSupports, From: "src", To: "edge:r0"})
	if err != nil {
		t.Fatalf("apply at limit-1: %v", err)
	}
	if applied != 1 || len(rejected) != 0 {
		t.Fatalf("at limit-1: applied=%d rejected=%+v, want 1/empty", applied, rejected)
	}
	if len(g.RelationEdges()) != before+1 || !edgeExists(g, "r-edge-ok") {
		t.Fatalf("accepted edge missing: count=%d exists=%v", len(g.RelationEdges()), edgeExists(g, "r-edge-ok"))
	}
}

func TestRelationDegreeExactlyAtLimit(t *testing.T) {
	const limit = 3
	c := newLimitsConsolidator(t, limit, 0)
	g := newGraph()
	mustAddNode(t, g, "A", 0)
	mustAddNode(t, g, "Z", 0)
	for i := 0; i < limit-1; i++ {
		to := fmt.Sprintf("B%d", i)
		mustAddNode(t, g, to, 0)
		mustSeedRelation(t, g, fmt.Sprintf("r-ab-%d", i), "A", to)
	}

	applied, rejected, err := applyAddRelation(c, g, &Edge{EdgeID: "r-boundary", Type: EdgeTypeCauses, From: "A", To: "Z"})
	if err != nil {
		t.Fatalf("applyOperations: %v", err)
	}
	if applied != 1 || len(rejected) != 0 {
		t.Fatalf("applied=%d rejected=%+v, want 1/empty (degree limit-1 is accepted)", applied, rejected)
	}
	if !edgeExists(g, "r-boundary") {
		t.Fatal("boundary edge missing")
	}
}

func TestFanoutExemptSourceProvenance(t *testing.T) {
	const maxFanout = 3
	g := newGraph()
	mustAddNode(t, g, "parent", 1)
	mustAddNode(t, g, "child", 0)
	for i := 0; i < maxFanout; i++ {
		id := fmt.Sprintf("src-file-%d", i)
		mustAddNode(t, g, id, SourceLayerLevel)
		g.hier = append(g.hier, &Edge{
			EdgeID:    fmt.Sprintf("ha-%d", i),
			Type:      EdgeTypeHasAttachment,
			From:      "parent",
			To:        id,
			CreatedBy: CreatorIngester,
		})
	}
	g.rebuild()
	if got := len(g.childrenOf["parent"]); got < maxFanout {
		t.Fatalf("raw childrenOf = %d, want >= %d", got, maxFanout)
	}
	if got := CountableHierarchyFanout(g, "parent"); got != 0 {
		t.Fatalf("CountableHierarchyFanout = %d, want 0 (provenance skipped)", got)
	}

	err := g.AddHierarchyEdge(&Edge{EdgeID: "h-ok", From: "parent", To: "child"}, maxFanout)
	if err != nil {
		t.Fatalf("AddHierarchyEdge: %v (provenance edges must not consume fanout)", err)
	}
	if !edgeExists(g, "h-ok") {
		t.Fatal("summarizes child missing")
	}
}

func TestLimitsRelationExemption(t *testing.T) {
	const limit = 3
	c := newLimitsConsolidator(t, limit, 0)
	g := newGraph()
	mustAddNode(t, g, "stmt", 0)
	for i := 0; i < 4; i++ {
		fid := fmt.Sprintf("src-file-%d", i)
		mustAddNode(t, g, fid, SourceLayerLevel)
		g.rel = append(g.rel, &Edge{
			EdgeID:    fmt.Sprintf("ha-%d", i),
			Type:      EdgeTypeHasAttachment,
			From:      "stmt",
			To:        fid,
			CreatedBy: CreatorIngester,
		})
	}
	g.rebuild()
	if got := CountableRelationDegree(g, "stmt"); got != 0 {
		t.Fatalf("CountableRelationDegree = %d, want 0 (has_attachment skipped)", got)
	}

	for i := 0; i < limit; i++ {
		to := fmt.Sprintf("n%d", i)
		mustAddNode(t, g, to, 0)
		applied, rejected, err := applyAddRelation(c, g, &Edge{
			EdgeID: fmt.Sprintf("r-ok-%d", i),
			Type:   EdgeTypeCauses,
			From:   "stmt",
			To:     to,
		})
		if err != nil {
			t.Fatalf("apply %d: %v", i, err)
		}
		if applied != 1 || len(rejected) != 0 {
			t.Fatalf("relation %d applied=%d rejected=%+v, want 1/empty", i, applied, rejected)
		}
	}
	if got := CountableRelationDegree(g, "stmt"); got != limit {
		t.Fatalf("after %d normal relations, degree = %d", limit, got)
	}
}

func TestLimitsConfigDefault(t *testing.T) {
	cfg := ConsolidateConfig{}.normalized()
	if cfg.MaxRelationEdges != 8 {
		t.Fatalf("zero ConsolidateConfig MaxRelationEdges = %d, want 8", cfg.MaxRelationEdges)
	}
	if DefaultConsolidateConfig().MaxRelationEdges != 8 {
		t.Fatalf("DefaultConsolidateConfig MaxRelationEdges = %d, want 8", DefaultConsolidateConfig().MaxRelationEdges)
	}

	c := NewConsolidator(newTestStore(t), nil, ConsolidateConfig{}, testConsolidateScope(), nil, nil)
	if c.cfg.MaxRelationEdges != 8 {
		t.Fatalf("constructor MaxRelationEdges = %d, want 8", c.cfg.MaxRelationEdges)
	}

	prompt := c.buildPrompt(nil, graphStats{}, "")
	limitsIdx := strings.Index(prompt, "Graph limits:")
	if limitsIdx < 0 {
		t.Fatalf("prompt missing Graph limits line:\n%s", prompt)
	}
	lineEnd := strings.Index(prompt[limitsIdx:], "\n")
	if lineEnd < 0 {
		lineEnd = len(prompt) - limitsIdx
	}
	limitsLine := prompt[limitsIdx : limitsIdx+lineEnd]
	if !strings.Contains(limitsLine, "8") || !strings.Contains(strings.ToLower(limitsLine), "relation") {
		t.Fatalf("Graph limits line = %q, want relation-degree cap of 8", limitsLine)
	}
}
