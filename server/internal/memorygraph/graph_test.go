package memorygraph

import (
	"sort"
	"strings"
	"testing"
)

func newGraphWithNodes(t *testing.T, ids ...string) *Graph {
	t.Helper()
	g := newGraph()
	for _, id := range ids {
		if err := g.AddNode(&Node{NodeID: id}); err != nil {
			t.Fatalf("AddNode %s: %v", id, err)
		}
	}
	return g
}

func mustAddHier(t *testing.T, g *Graph, id, from, to string, maxFanout int) {
	t.Helper()
	if err := g.AddHierarchyEdge(&Edge{EdgeID: id, From: from, To: to}, maxFanout); err != nil {
		t.Fatalf("AddHierarchyEdge %s: %v", id, err)
	}
}

func TestAddNodeDuplicate(t *testing.T) {
	g := newGraphWithNodes(t, "a")
	if err := g.AddNode(&Node{NodeID: "a"}); err == nil {
		t.Fatal("duplicate AddNode: want error")
	}
}

func TestValidateHierarchyCycle(t *testing.T) {
	g := newGraphWithNodes(t, "a", "b")
	g.hier = []*Edge{
		{EdgeID: "h1", Type: EdgeTypeSummarizes, From: "a", To: "b"},
		{EdgeID: "h2", Type: EdgeTypeSummarizes, From: "b", To: "a"},
	}
	g.rebuild()
	err := g.Validate()
	if err == nil || !strings.Contains(err.Error(), `"a"`) && !strings.Contains(err.Error(), `"b"`) {
		t.Fatalf("Validate = %v; want cycle error naming a node", err)
	}
	if err := g.RecomputeLevels(); err == nil {
		t.Fatal("RecomputeLevels on cycle: want error")
	}
}

func TestValidateDanglingEndpoints(t *testing.T) {
	g := newGraphWithNodes(t, "a")
	g.hier = []*Edge{{EdgeID: "h1", Type: EdgeTypeSummarizes, From: "a", To: "ghost"}}
	g.rebuild()
	if err := g.Validate(); err == nil {
		t.Fatal("dangling hierarchy endpoint: want error")
	}

	g = newGraphWithNodes(t, "a")
	g.rel = []*Edge{{EdgeID: "r1", Type: EdgeTypeCauses, From: "ghost", To: "a"}}
	g.rebuild()
	if err := g.Validate(); err == nil {
		t.Fatal("dangling relation from: want error")
	}
}

func TestValidateRelationTypeAndEpistemic(t *testing.T) {
	tests := []struct {
		name    string
		edge    *Edge
		wantErr bool
	}{
		{"valid", &Edge{EdgeID: "r1", Type: EdgeTypeSupports, From: "a", To: "b", Epistemic: EpistemicAsserted}, false},
		{"unknown type", &Edge{EdgeID: "r1", Type: "likes", From: "a", To: "b"}, true},
		{"bad epistemic", &Edge{EdgeID: "r1", Type: EdgeTypeCauses, From: "a", To: "b", Epistemic: "guessed"}, true},
		{"empty epistemic ok", &Edge{EdgeID: "r1", Type: EdgeTypeCauses, From: "a", To: "b"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := newGraphWithNodes(t, "a", "b")
			g.rel = []*Edge{tt.edge}
			g.rebuild()
			err := g.Validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate = %v; wantErr=%v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateEdgeRefTarget(t *testing.T) {
	g := newGraphWithNodes(t, "a", "b", "c")
	g.rel = []*Edge{
		{EdgeID: "r1", Type: EdgeTypeCauses, From: "a", To: "b"},
		{EdgeID: "r2", Type: EdgeTypeSupports, From: "c", To: "edge:r1"},
	}
	g.rebuild()
	if err := g.Validate(); err != nil {
		t.Fatalf("Validate with edge-ref: %v", err)
	}

	g.rel = append(g.rel, &Edge{EdgeID: "r3", Type: EdgeTypeSupports, From: "c", To: "edge:missing"})
	g.rebuild()
	if err := g.Validate(); err == nil {
		t.Fatal("edge-ref to missing edge: want error")
	}
}

func TestAddHierarchyEdgeCycleRejected(t *testing.T) {
	g := newGraphWithNodes(t, "a", "b", "c")
	mustAddHier(t, g, "h1", "a", "b", 0)
	if err := g.AddHierarchyEdge(&Edge{EdgeID: "h2", From: "b", To: "a"}, 0); err == nil {
		t.Fatal("b->a after a->b: want cycle error")
	}
	if err := g.AddHierarchyEdge(&Edge{EdgeID: "h3", From: "a", To: "a"}, 0); err == nil {
		t.Fatal("self loop: want cycle error")
	}
	// A non-cyclic edge still works.
	mustAddHier(t, g, "h4", "b", "c", 0)
}

func TestAddHierarchyEdgeFanout(t *testing.T) {
	g := newGraphWithNodes(t, "root", "c1", "c2")
	mustAddHier(t, g, "h1", "root", "c1", 1)
	if err := g.AddHierarchyEdge(&Edge{EdgeID: "h2", From: "root", To: "c2"}, 1); err == nil {
		t.Fatal("fanout limit: want error")
	}
	// maxFanout 0 means unlimited.
	mustAddHier(t, g, "h3", "root", "c2", 0)
}

func TestRecomputeLevelsChain(t *testing.T) {
	g := newGraphWithNodes(t, "root", "mid", "leaf")
	mustAddHier(t, g, "h1", "root", "mid", 0)
	mustAddHier(t, g, "h2", "mid", "leaf", 0)
	if err := g.RecomputeLevels(); err != nil {
		t.Fatalf("RecomputeLevels: %v", err)
	}
	levels := map[string]int{}
	for _, n := range g.Nodes() {
		levels[n.NodeID] = n.Level
	}
	if levels["root"] != 2 || levels["mid"] != 1 || levels["leaf"] != 0 {
		t.Fatalf("levels = %v; want root=2 mid=1 leaf=0", levels)
	}
}

func TestAddRelationEdgeLevelDelta(t *testing.T) {
	g := newGraphWithNodes(t, "root", "mid", "leaf")
	mustAddHier(t, g, "h1", "root", "mid", 0)
	mustAddHier(t, g, "h2", "mid", "leaf", 0)

	e := &Edge{EdgeID: "r1", Type: EdgeTypeEvidenceFor, From: "leaf", To: "root"}
	if err := g.AddRelationEdge(e); err != nil {
		t.Fatalf("AddRelationEdge: %v", err)
	}
	if e.SourceLevel != 0 || e.TargetLevel != 2 || e.LevelDelta != 2 {
		t.Fatalf("levels = (%d, %d, %d); want (0, 2, 2)", e.SourceLevel, e.TargetLevel, e.LevelDelta)
	}
	if err := g.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}

	// Edge-ref target resolves levels through the referenced edge's From node.
	e2 := &Edge{EdgeID: "r2", Type: EdgeTypeSupports, From: "mid", To: "edge:r1"}
	if err := g.AddRelationEdge(e2); err != nil {
		t.Fatalf("AddRelationEdge edge-ref: %v", err)
	}
	if e2.SourceLevel != 1 || e2.TargetLevel != 0 || e2.LevelDelta != -1 {
		t.Fatalf("edge-ref levels = (%d, %d, %d); want (1, 0, -1)", e2.SourceLevel, e2.TargetLevel, e2.LevelDelta)
	}
}

func TestNeighbors(t *testing.T) {
	g := newGraphWithNodes(t, "p", "n", "c", "r1node", "r2node")
	mustAddHier(t, g, "h1", "p", "n", 0)
	mustAddHier(t, g, "h2", "n", "c", 0)
	if err := g.AddRelationEdge(&Edge{EdgeID: "e1", Type: EdgeTypeCauses, From: "n", To: "r1node"}); err != nil {
		t.Fatal(err)
	}
	if err := g.AddRelationEdge(&Edge{EdgeID: "e2", Type: EdgeTypeSupports, From: "r2node", To: "n"}); err != nil {
		t.Fatal(err)
	}
	parents, children, related, relEdges := g.Neighbors("n")
	if len(parents) != 1 || parents[0].NodeID != "p" {
		t.Fatalf("parents = %v", parents)
	}
	if len(children) != 1 || children[0].NodeID != "c" {
		t.Fatalf("children = %v", children)
	}
	if len(related) != 2 || len(relEdges) != 2 {
		t.Fatalf("related = %v, relEdges = %v", related, relEdges)
	}
}

func TestKHopNeighborhood(t *testing.T) {
	g := newGraphWithNodes(t, "a", "b", "c", "island")
	mustAddHier(t, g, "h1", "a", "b", 0)
	if err := g.AddRelationEdge(&Edge{EdgeID: "r1", Type: EdgeTypeCauses, From: "b", To: "c"}); err != nil {
		t.Fatal(err)
	}

	keys := func(m map[string]bool) []string {
		out := make([]string, 0, len(m))
		for k := range m {
			out = append(out, k)
		}
		sort.Strings(out)
		return out
	}

	// n=0: only the known seeds.
	if got := keys(g.KHopNeighborhood([]string{"a"}, 0)); strings.Join(got, ",") != "a" {
		t.Fatalf("0-hop neighborhood = %v, want [a]", got)
	}
	// Hierarchy and relation edges are both traversable, undirected.
	if got := keys(g.KHopNeighborhood([]string{"a"}, 1)); strings.Join(got, ",") != "a,b" {
		t.Fatalf("1-hop neighborhood = %v, want [a b]", got)
	}
	if got := keys(g.KHopNeighborhood([]string{"a"}, 2)); strings.Join(got, ",") != "a,b,c" {
		t.Fatalf("2-hop neighborhood = %v, want [a b c]", got)
	}
	// Unconnected nodes never enter the neighborhood, however large n is.
	if got := keys(g.KHopNeighborhood([]string{"a"}, 10)); strings.Join(got, ",") != "a,b,c" {
		t.Fatalf("10-hop neighborhood = %v, want [a b c]", got)
	}
	// Multiple seeds union; unknown seeds are ignored.
	if got := keys(g.KHopNeighborhood([]string{"a", "island", "ghost"}, 0)); strings.Join(got, ",") != "a,island" {
		t.Fatalf("multi-seed 0-hop neighborhood = %v, want [a island]", got)
	}
}

func TestShortestDistance(t *testing.T) {
	g := newGraphWithNodes(t, "a", "b", "c", "island")
	mustAddHier(t, g, "h1", "a", "b", 0)
	if err := g.AddRelationEdge(&Edge{EdgeID: "r1", Type: EdgeTypeCauses, From: "b", To: "c"}); err != nil {
		t.Fatal(err)
	}
	// Hierarchy + relation are both traversable in either direction.
	if d := g.ShortestDistance("a", map[string]bool{"c": true}); d != 2 {
		t.Fatalf("ShortestDistance(a, c) = %d; want 2", d)
	}
	if d := g.ShortestDistance("c", map[string]bool{"a": true}); d != 2 {
		t.Fatalf("ShortestDistance(c, a) = %d; want 2 (undirected)", d)
	}
	if d := g.ShortestDistance("a", map[string]bool{"a": true}); d != 0 {
		t.Fatalf("ShortestDistance(a, a) = %d; want 0", d)
	}
	if d := g.ShortestDistance("a", map[string]bool{"island": true}); d != -1 {
		t.Fatalf("ShortestDistance(a, island) = %d; want -1", d)
	}
	if d := g.ShortestDistance("ghost", map[string]bool{"a": true}); d != -1 {
		t.Fatalf("ShortestDistance(ghost, a) = %d; want -1", d)
	}
}

func TestDeleteNodeRemovesIncidentEdges(t *testing.T) {
	g := newGraphWithNodes(t, "a", "b", "c")
	mustAddHier(t, g, "h1", "a", "b", 0)
	if err := g.AddRelationEdge(&Edge{EdgeID: "r1", Type: EdgeTypeCauses, From: "b", To: "c"}); err != nil {
		t.Fatal(err)
	}
	g.DeleteNode("b")
	if g.Node("b") != nil {
		t.Fatal("node b not deleted")
	}
	if len(g.HierarchyEdges()) != 0 || len(g.RelationEdges()) != 0 {
		t.Fatalf("incident edges remain: hier=%v rel=%v", g.HierarchyEdges(), g.RelationEdges())
	}
	if d := g.ShortestDistance("a", map[string]bool{"c": true}); d != -1 {
		t.Fatalf("ShortestDistance after delete = %d; want -1", d)
	}
}

func TestDeleteEdge(t *testing.T) {
	g := newGraphWithNodes(t, "a", "b")
	mustAddHier(t, g, "h1", "a", "b", 0)
	g.DeleteEdge("h1")
	if len(g.HierarchyEdges()) != 0 {
		t.Fatalf("hier = %v; want empty", g.HierarchyEdges())
	}
	if d := g.ShortestDistance("a", map[string]bool{"b": true}); d != -1 {
		t.Fatalf("ShortestDistance after DeleteEdge = %d; want -1", d)
	}
}

func TestLoadGraphFromStore(t *testing.T) {
	s := newTestStore(t)
	for _, n := range []*Node{
		{NodeID: "p", EntityRefs: []string{"ent-1"}},
		{NodeID: "c", Body: "child"},
	} {
		if err := s.SaveNode(1, n); err != nil {
			t.Fatal(err)
		}
	}
	hier := []*Edge{{EdgeID: "h1", Type: EdgeTypeSummarizes, From: "p", To: "c"}}
	rel := []*Edge{{EdgeID: "r1", Type: EdgeTypeSupports, From: "c", To: "p", Epistemic: EpistemicInferred}}
	if err := s.SaveEdges(1, hier, rel); err != nil {
		t.Fatal(err)
	}
	g, err := LoadGraph(s, 1)
	if err != nil {
		t.Fatalf("LoadGraph: %v", err)
	}
	if err := g.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(g.Nodes()) != 2 || len(g.HierarchyEdges()) != 1 || len(g.RelationEdges()) != 1 {
		t.Fatalf("graph = %d nodes, %d hier, %d rel", len(g.Nodes()), len(g.HierarchyEdges()), len(g.RelationEdges()))
	}
	if ids := g.EntityNodes("ent-1"); len(ids) != 1 || ids[0] != "p" {
		t.Fatalf("EntityNodes = %v", ids)
	}
}
