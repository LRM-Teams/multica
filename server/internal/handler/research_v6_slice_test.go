package handler

import "testing"

func TestBuildResearchV6ProjectionSliceIsBoundedStableAndFiltered(t *testing.T) {
	snapshot := researchV6Snapshot{SnapshotID: "snap-1", Nodes: []researchV6ProjectionNode{
		{ID: "root", Status: "active", Importance: 1}, {ID: "a", Status: "active", Importance: .8}, {ID: "b", Status: "stale", Importance: .9}, {ID: "c", Status: "active", Importance: .7},
	}, Edges: []researchV6ProjectionEdge{{ID: "e1", FromNodeID: "root", ToNodeID: "a", EdgeType: "decomposes"}, {ID: "e2", FromNodeID: "root", ToNodeID: "b", EdgeType: "decomposes"}, {ID: "e3", FromNodeID: "a", ToNodeID: "c", EdgeType: "depends_on"}}, Clusters: []researchV6ProjectionCluster{{ID: "cluster-a", Label: "A", ClusterType: "stable_result", MemberNodeIDs: []string{"a", "c"}}, {ID: "cluster-b", Label: "B", ClusterType: "exploration", MemberNodeIDs: []string{"b"}}}}
	request := researchV6SliceRequest{RootNodeID: "root", Direction: "out", RelationTypes: []string{"decomposes"}, MaxDepth: 2, Statuses: []string{"active"}, ImportanceFloor: .5, Limit: 1}
	first, err := buildResearchV6ProjectionSlice(snapshot, request)
	if err != nil || len(first.Nodes) != 1 || first.Nodes[0].Node.ID != "root" || first.NextCursor == nil {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	request.Cursor = first.NextCursor
	second, err := buildResearchV6ProjectionSlice(snapshot, request)
	if err != nil || len(second.Nodes) != 1 || second.Nodes[0].Node.ID != "a" || second.NextCursor != nil || len(second.Clusters) != 1 || second.Clusters[0].ID != "cluster-a" {
		t.Fatalf("second=%+v err=%v", second, err)
	}
}

func TestBuildResearchV6ProjectionSliceRejectsCursorAcrossSnapshotOrQuery(t *testing.T) {
	snapshot := researchV6Snapshot{SnapshotID: "snap-1", Nodes: []researchV6ProjectionNode{{ID: "root", Importance: 1}}}
	request := researchV6SliceRequest{RootNodeID: "root", Direction: "both", MaxDepth: 0, Limit: 1}
	page, err := buildResearchV6ProjectionSlice(snapshot, request)
	if err != nil {
		t.Fatal(err)
	}
	cursor := encodeResearchV6SliceCursor(researchV6SliceCursor{SnapshotID: "old", RequestHash: hashResearchV6SliceRequest(request), Offset: 0})
	request.Cursor = &cursor
	if _, err := buildResearchV6ProjectionSlice(snapshot, request); err == nil {
		t.Fatal("expected snapshot-fenced cursor rejection")
	}
	_ = page
}
