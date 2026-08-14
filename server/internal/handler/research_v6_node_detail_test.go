package handler

import "testing"

func TestBuildResearchV6NodeDetailReturnsStableNeighborsAndCompleteness(t *testing.T) {
	complete := map[string]any{}
	for _, field := range researchV6RequiredDetailFields {
		complete[field] = "value"
	}
	snapshot := researchV6Snapshot{SnapshotID: "snap", ThroughEventSequence: 7, Nodes: []researchV6ProjectionNode{{ID: "root", NodeKind: "question", Title: "Q", Status: "active", Detail: complete}, {ID: "a", NodeKind: "task", Title: "A"}, {ID: "b", NodeKind: "claim", Title: "B"}}, Edges: []researchV6ProjectionEdge{{ID: "e2", FromNodeID: "root", ToNodeID: "b", EdgeType: "supports"}, {ID: "e1", FromNodeID: "a", ToNodeID: "root", EdgeType: "depends_on"}}}
	detail, found := buildResearchV6NodeDetail(snapshot, "root")
	if !found || !detail.DetailComplete || len(detail.Incoming) != 1 || len(detail.Outgoing) != 1 || detail.Incoming[0].NodeID != "a" || detail.Outgoing[0].NodeID != "b" {
		t.Fatalf("detail=%+v found=%v", detail, found)
	}
}

func TestBuildResearchV6NodeDetailReportsMissingFieldsWithoutFabrication(t *testing.T) {
	snapshot := researchV6Snapshot{Nodes: []researchV6ProjectionNode{{ID: "node", Detail: map[string]any{"purpose": "known"}}}}
	detail, found := buildResearchV6NodeDetail(snapshot, "node")
	if !found || detail.DetailComplete || len(detail.MissingDetailFields) != len(researchV6RequiredDetailFields)-1 {
		t.Fatalf("detail=%+v", detail)
	}
	if _, found := buildResearchV6NodeDetail(snapshot, "missing"); found {
		t.Fatal("expected missing node")
	}
}
