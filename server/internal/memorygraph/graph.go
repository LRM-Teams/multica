package memorygraph

import (
	"fmt"
	"sort"
	"strings"
)

// Graph is the in-memory index over one stored version. It is built by
// LoadGraph and mutated by the consolidator through Add*/Delete* before the
// result is persisted back to a (candidate) version directory.
type Graph struct {
	nodes map[string]*Node
	hier  []*Edge // summarizes edges (strict DAG)
	rel   []*Edge // typed relation edges (may form cycles)

	childrenOf  map[string][]*Edge // summarizes from -> child edges
	parentsOf   map[string][]*Edge // summarizes to -> parent edges
	relByID     map[string]*Edge   // relation edge_id -> edge
	entityIndex map[string][]string
	adj         map[string]map[string]bool // undirected adjacency over all edges
}

// LoadGraph loads version v from the store and builds the in-memory index.
// Callers should run Validate to enforce the schema hard gate (design §5.4).
func LoadGraph(store *Store, v int) (*Graph, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.loadGraphLocked(v)
}

func newGraph() *Graph {
	return &Graph{
		nodes:       make(map[string]*Node),
		childrenOf:  make(map[string][]*Edge),
		parentsOf:   make(map[string][]*Edge),
		relByID:     make(map[string]*Edge),
		entityIndex: make(map[string][]string),
		adj:         make(map[string]map[string]bool),
	}
}

// Nodes returns all nodes sorted by id.
func (g *Graph) Nodes() []*Node {
	out := make([]*Node, 0, len(g.nodes))
	for _, n := range g.nodes {
		out = append(out, n)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].NodeID < out[j].NodeID })
	return out
}

// Node returns the node with the given id, or nil.
func (g *Graph) Node(id string) *Node { return g.nodes[id] }

// HierarchyEdges returns the summarizes edges.
func (g *Graph) HierarchyEdges() []*Edge { return g.hier }

// RelationEdges returns the typed relation edges.
func (g *Graph) RelationEdges() []*Edge { return g.rel }

// Validate enforces the schema hard gate: the hierarchy is a strict DAG,
// every edge endpoint references an existing node (or an existing relation
// edge via "edge:<id>"), relation types are in RelationEdgeTypes and
// epistemic markers are asserted|inferred when set.
func (g *Graph) Validate() error {
	for _, e := range g.hier {
		if e.Type != EdgeTypeSummarizes {
			return fmt.Errorf("hierarchy edge %s has type %q, want %q", e.EdgeID, e.Type, EdgeTypeSummarizes)
		}
		if _, ok := g.nodes[e.From]; !ok {
			return fmt.Errorf("hierarchy edge %s: from node %q does not exist", e.EdgeID, e.From)
		}
		if _, ok := g.nodes[e.To]; !ok {
			return fmt.Errorf("hierarchy edge %s: to node %q does not exist", e.EdgeID, e.To)
		}
	}
	if node := g.findHierarchyCycle(); node != "" {
		return fmt.Errorf("hierarchy is not a DAG: cycle through node %q", node)
	}
	for _, e := range g.rel {
		if !RelationEdgeTypes[e.Type] {
			return fmt.Errorf("relation edge %s has unknown type %q", e.EdgeID, e.Type)
		}
		if _, ok := g.nodes[e.From]; !ok {
			return fmt.Errorf("relation edge %s: from node %q does not exist", e.EdgeID, e.From)
		}
		if err := g.validateRelTarget(e); err != nil {
			return err
		}
		if e.Epistemic != "" && e.Epistemic != EpistemicAsserted && e.Epistemic != EpistemicInferred {
			return fmt.Errorf("relation edge %s has invalid epistemic %q", e.EdgeID, e.Epistemic)
		}
	}
	return nil
}

func (g *Graph) validateRelTarget(e *Edge) error {
	if e.IsEdgeRef() {
		refID := e.To[len("edge:"):]
		if _, ok := g.relByID[refID]; !ok {
			return fmt.Errorf("relation edge %s: target edge %q does not exist", e.EdgeID, refID)
		}
		return nil
	}
	if _, ok := g.nodes[e.To]; !ok {
		return fmt.Errorf("relation edge %s: to node %q does not exist", e.EdgeID, e.To)
	}
	return nil
}

// AddNode inserts a node. A duplicate id is an error.
func (g *Graph) AddNode(n *Node) error {
	if n.NodeID == "" {
		return fmt.Errorf("node id is required")
	}
	if _, ok := g.nodes[n.NodeID]; ok {
		return fmt.Errorf("node %q already exists", n.NodeID)
	}
	g.nodes[n.NodeID] = n
	g.indexEntity(n)
	return nil
}

// AddHierarchyEdge inserts a summarizes edge after enforcing the DAG and
// fanout invariants: the edge must not create a cycle and the From node's
// children count must stay below maxFanout. Node levels are recomputed on
// success.
func (g *Graph) AddHierarchyEdge(e *Edge, maxFanout int) error {
	if e.Type != "" && e.Type != EdgeTypeSummarizes {
		return fmt.Errorf("hierarchy edge %s has type %q, want %q", e.EdgeID, e.Type, EdgeTypeSummarizes)
	}
	e.Type = EdgeTypeSummarizes
	if _, ok := g.nodes[e.From]; !ok {
		return fmt.Errorf("hierarchy edge %s: from node %q does not exist", e.EdgeID, e.From)
	}
	if _, ok := g.nodes[e.To]; !ok {
		return fmt.Errorf("hierarchy edge %s: to node %q does not exist", e.EdgeID, e.To)
	}
	if e.From == e.To || g.reachable(e.To, e.From) {
		return fmt.Errorf("hierarchy edge %s would create a cycle through node %q", e.EdgeID, e.From)
	}
	if maxFanout > 0 && CountableHierarchyFanout(g, e.From) >= maxFanout {
		return fmt.Errorf("hierarchy edge %s: node %q fanout limit %d reached", e.EdgeID, e.From, maxFanout)
	}
	g.hier = append(g.hier, e)
	g.rebuild()
	return g.RecomputeLevels()
}

// AddRelationEdge inserts a typed relation edge. Cycles are allowed and the
// target may be another relation edge via To="edge:<id>". SourceLevel,
// TargetLevel and LevelDelta are filled from the current node levels.
func (g *Graph) AddRelationEdge(e *Edge) error {
	if !RelationEdgeTypes[e.Type] {
		return fmt.Errorf("relation edge %s has unknown type %q", e.EdgeID, e.Type)
	}
	from, ok := g.nodes[e.From]
	if !ok {
		return fmt.Errorf("relation edge %s: from node %q does not exist", e.EdgeID, e.From)
	}
	if err := g.validateRelTarget(e); err != nil {
		return err
	}
	e.SourceLevel = from.Level
	e.TargetLevel = g.targetNode(e).Level
	e.LevelDelta = e.TargetLevel - e.SourceLevel
	g.rel = append(g.rel, e)
	g.rebuild()
	return nil
}

// targetNode resolves the node whose level a relation edge target carries:
// the To node directly, or the From node of the referenced edge.
func (g *Graph) targetNode(e *Edge) *Node {
	if !e.IsEdgeRef() {
		return g.nodes[e.To]
	}
	ref := g.relByID[e.To[len("edge:"):]]
	if ref == nil {
		return nil
	}
	return g.nodes[ref.From]
}

// RecomputeLevels writes Node.Level for every node: level(n) = 0 when n has
// no outgoing summarizes edges (leaf = most specific layer), otherwise
// 1 + max(level of children). A hierarchy cycle is an error naming the node.
func (g *Graph) RecomputeLevels() error {
	const (
		unvisited = 0
		visiting  = 1
		done      = 2
	)
	state := make(map[string]int, len(g.nodes))
	var levelOf func(id string) (int, error)
	levelOf = func(id string) (int, error) {
		switch state[id] {
		case visiting:
			return 0, fmt.Errorf("hierarchy is not a DAG: cycle through node %q", id)
		case done:
			return g.nodes[id].Level, nil
		}
		state[id] = visiting
		maxChild := -1
		for _, e := range g.childrenOf[id] {
			child, err := levelOf(e.To)
			if err != nil {
				return 0, err
			}
			if child > maxChild {
				maxChild = child
			}
		}
		state[id] = done
		level := 0
		if maxChild >= 0 {
			level = maxChild + 1
		}
		if n := g.nodes[id]; n != nil {
			n.Level = level
		}
		return level, nil
	}
	for id := range g.nodes {
		if _, err := levelOf(id); err != nil {
			return err
		}
	}
	return nil
}

// Neighbors returns the hierarchy parents and children of nodeID plus the
// nodes connected by typed relation edges in either direction (and the
// relation edges themselves).
func (g *Graph) Neighbors(nodeID string) (parents, children, related []*Node, relEdges []*Edge) {
	seenRelated := make(map[string]bool)
	addRelated := func(id string) {
		if id == nodeID || seenRelated[id] {
			return
		}
		if n := g.nodes[id]; n != nil {
			seenRelated[id] = true
			related = append(related, n)
		}
	}
	for _, e := range g.parentsOf[nodeID] {
		if n := g.nodes[e.From]; n != nil {
			parents = append(parents, n)
		}
	}
	for _, e := range g.childrenOf[nodeID] {
		if n := g.nodes[e.To]; n != nil {
			children = append(children, n)
		}
	}
	for _, e := range g.rel {
		switch {
		case e.From == nodeID:
			relEdges = append(relEdges, e)
			addRelated(g.targetNodeID(e))
		case !e.IsEdgeRef() && e.To == nodeID:
			relEdges = append(relEdges, e)
			addRelated(e.From)
		}
	}
	return parents, children, related, relEdges
}

// targetNodeID resolves a relation edge target to a node id: the To node, or
// the From node of the referenced edge.
func (g *Graph) targetNodeID(e *Edge) string {
	if !e.IsEdgeRef() {
		return e.To
	}
	if ref := g.relByID[e.To[len("edge:"):]]; ref != nil {
		return ref.From
	}
	return ""
}

// ShortestDistance returns the BFS hop count from fromID to the nearest node
// in toSet, treating hierarchy and relation edges as undirected. It returns
// -1 when no target is reachable (used as the explore-rounds proxy for
// backtesting, design §5.4).
func (g *Graph) ShortestDistance(fromID string, toSet map[string]bool) int {
	if toSet[fromID] {
		return 0
	}
	if _, ok := g.nodes[fromID]; !ok {
		return -1
	}
	visited := map[string]bool{fromID: true}
	frontier := []string{fromID}
	for dist := 1; len(frontier) > 0; dist++ {
		var next []string
		for _, id := range frontier {
			for nb := range g.adj[id] {
				if visited[nb] {
					continue
				}
				if toSet[nb] {
					return dist
				}
				visited[nb] = true
				next = append(next, nb)
			}
		}
		frontier = next
	}
	return -1
}

// KHopNeighborhood returns the set of node ids reachable from any seed id
// within n hops, traversing hierarchy and relation edges undirected via the
// same adjacency as ShortestDistance. Seed ids are included in the result;
// unknown seeds are ignored. n <= 0 returns the known seeds only. Backtests
// use it to decide whether the ground truth set is inside the graph region
// the original query explored (design Q13/A2).
func (g *Graph) KHopNeighborhood(ids []string, n int) map[string]bool {
	out := make(map[string]bool, len(ids))
	frontier := make([]string, 0, len(ids))
	for _, id := range ids {
		if _, ok := g.nodes[id]; ok && !out[id] {
			out[id] = true
			frontier = append(frontier, id)
		}
	}
	for hop := 0; hop < n && len(frontier) > 0; hop++ {
		var next []string
		for _, id := range frontier {
			for nb := range g.adj[id] {
				if out[nb] {
					continue
				}
				out[nb] = true
				next = append(next, nb)
			}
		}
		frontier = next
	}
	return out
}

// DeleteNode removes a node and every hierarchy/relation edge incident to it.
func (g *Graph) DeleteNode(id string) {
	delete(g.nodes, id)
	g.hier = filterEdges(g.hier, func(e *Edge) bool { return e.From != id && e.To != id })
	g.rel = filterEdges(g.rel, func(e *Edge) bool { return e.From != id && e.To != id })
	g.rebuild()
}

// DeleteEdge removes the edge (hierarchy or relation) with the given id.
func (g *Graph) DeleteEdge(edgeID string) {
	g.hier = filterEdges(g.hier, func(e *Edge) bool { return e.EdgeID != edgeID })
	g.rel = filterEdges(g.rel, func(e *Edge) bool { return e.EdgeID != edgeID })
	g.rebuild()
}

func filterEdges(edges []*Edge, keep func(*Edge) bool) []*Edge {
	out := edges[:0]
	for _, e := range edges {
		if keep(e) {
			out = append(out, e)
		}
	}
	return out
}

// rebuild recomputes all derived indexes from nodes/hier/rel.
func (g *Graph) rebuild() {
	g.childrenOf = make(map[string][]*Edge)
	g.parentsOf = make(map[string][]*Edge)
	g.relByID = make(map[string]*Edge)
	g.entityIndex = make(map[string][]string)
	g.adj = make(map[string]map[string]bool)
	for _, e := range g.hier {
		g.childrenOf[e.From] = append(g.childrenOf[e.From], e)
		g.parentsOf[e.To] = append(g.parentsOf[e.To], e)
		g.link(e.From, e.To)
	}
	for _, e := range g.rel {
		g.relByID[e.EdgeID] = e
	}
	for _, e := range g.rel {
		// For an edge-ref target, connect From to the referenced edge's
		// endpoints so BFS treats the evidence link as a real path.
		if e.IsEdgeRef() {
			if ref := g.relByID[strings.TrimPrefix(e.To, "edge:")]; ref != nil && ref != e {
				g.link(e.From, ref.From)
				if !ref.IsEdgeRef() {
					g.link(e.From, ref.To)
				}
			}
			continue
		}
		g.link(e.From, e.To)
	}
	for _, n := range g.nodes {
		g.indexEntity(n)
	}
}

func (g *Graph) indexEntity(n *Node) {
	for _, ref := range n.EntityRefs {
		g.entityIndex[ref] = append(g.entityIndex[ref], n.NodeID)
	}
}

func (g *Graph) link(a, b string) {
	if a == "" || b == "" {
		return
	}
	if g.adj[a] == nil {
		g.adj[a] = make(map[string]bool)
	}
	if g.adj[b] == nil {
		g.adj[b] = make(map[string]bool)
	}
	g.adj[a][b] = true
	g.adj[b][a] = true
}

// reachable reports whether target is reachable from start following
// summarizes edges in the parent -> child direction.
func (g *Graph) reachable(start, target string) bool {
	visited := map[string]bool{start: true}
	queue := []string{start}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if id == target {
			return true
		}
		for _, e := range g.childrenOf[id] {
			if !visited[e.To] {
				visited[e.To] = true
				queue = append(queue, e.To)
			}
		}
	}
	return false
}

// findHierarchyCycle returns one node participating in a hierarchy cycle,
// or "" when the hierarchy is a DAG.
func (g *Graph) findHierarchyCycle() string {
	const (
		unvisited = 0
		visiting  = 1
		done      = 2
	)
	state := make(map[string]int, len(g.nodes))
	var visit func(id string) string
	visit = func(id string) string {
		state[id] = visiting
		for _, e := range g.childrenOf[id] {
			switch state[e.To] {
			case visiting:
				return e.To
			case unvisited:
				if node := visit(e.To); node != "" {
					return node
				}
			}
		}
		state[id] = done
		return ""
	}
	for id := range g.nodes {
		if state[id] == unvisited {
			if node := visit(id); node != "" {
				return node
			}
		}
	}
	return ""
}

// EntityNodes returns the node ids indexed under an entity_ref key.
func (g *Graph) EntityNodes(entityRef string) []string {
	return g.entityIndex[entityRef]
}
