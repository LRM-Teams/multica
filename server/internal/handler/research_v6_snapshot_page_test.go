package handler

import (
	"fmt"
	"testing"
)

func TestPaginateResearchV6SnapshotTenThousandNodesIsStableAndUnique(t *testing.T) {
	snapshot := researchV6Snapshot{SnapshotID: "snapshot-1", RunID: "run", ThroughEventSequence: 42, GraphContentHash: map[string]string{"nodes": "nh", "edges": "eh"}}
	for index := 0; index < 10000; index++ {
		snapshot.Nodes = append(snapshot.Nodes, researchV6ProjectionNode{ID: fmt.Sprintf("node-%05d", index)})
	}
	for index := 0; index < 100; index++ {
		snapshot.Edges = append(snapshot.Edges, researchV6ProjectionEdge{ID: fmt.Sprintf("edge-%05d", index)})
	}
	cursor := ""
	seenNodes := map[string]struct{}{}
	seenEdges := map[string]struct{}{}
	pages := 0
	for {
		page, err := paginateResearchV6Snapshot(snapshot, 500, cursor)
		if err != nil {
			t.Fatal(err)
		}
		pages++
		if len(page.Nodes)+len(page.Edges) > 500 || page.SnapshotID != snapshot.SnapshotID || page.ThroughEventSequence != 42 || page.GraphContentHash["nodes"] != "nh" {
			t.Fatalf("page=%+v", page)
		}
		for _, node := range page.Nodes {
			if _, exists := seenNodes[node.ID]; exists {
				t.Fatalf("duplicate node %s", node.ID)
			}
			seenNodes[node.ID] = struct{}{}
		}
		for _, edge := range page.Edges {
			if _, exists := seenEdges[edge.ID]; exists {
				t.Fatalf("duplicate edge %s", edge.ID)
			}
			seenEdges[edge.ID] = struct{}{}
		}
		if page.NextCursor == nil {
			break
		}
		cursor = *page.NextCursor
	}
	if len(seenNodes) != 10000 || len(seenEdges) != 100 || pages != 21 {
		t.Fatalf("nodes=%d edges=%d pages=%d", len(seenNodes), len(seenEdges), pages)
	}
}

func TestPaginateResearchV6SnapshotRejectsChangedBaseline(t *testing.T) {
	snapshot := researchV6Snapshot{SnapshotID: "new", Nodes: []researchV6ProjectionNode{{ID: "node"}}}
	cursor := encodeResearchV6SnapshotCursor(researchV6SnapshotCursor{SnapshotID: "old", Offset: 0})
	if _, err := paginateResearchV6Snapshot(snapshot, 1, cursor); err == nil {
		t.Fatal("expected snapshot baseline rejection")
	}
}
