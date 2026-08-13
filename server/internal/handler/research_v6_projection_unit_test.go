package handler

import (
	"encoding/json"
	"testing"
)

func TestMapResearchV6NodeBindsRunAndCanonicalEntity(t *testing.T) {
	actor := "agent-1"
	node := ResearchGraphNodeResp{
		ID: "display-node", NodeType: "probe", Title: "Investigate",
		Status: "running", ActorAgentID: &actor,
		Payload:   json.RawMessage(`{"kind":"task","task_id":"task-1"}`),
		CreatedAt: "2026-08-13T00:00:00Z", UpdatedAt: "2026-08-13T00:00:01Z",
	}
	got := mapResearchV6Node("run-1", node)
	if got.ID != "run-1:task:task-1" || got.RunID != "run-1" || got.EntityKind != "task" || got.EntityID != "task-1" {
		t.Fatalf("unexpected projection identity: %+v", got)
	}
	if got.CreatedSequence != nil || got.UpdatedSequence != nil {
		t.Fatalf("derived V5 projection must not fabricate event sequence: %+v", got)
	}
}

func TestMapResearchV6NodeDegradesUnknownKindToGeneric(t *testing.T) {
	node := ResearchGraphNodeResp{
		ID: "display-node", NodeType: "future-shape", Title: "Future node",
		Payload: json.RawMessage(`{"kind":"future_private_kind","entity_id":"hidden-id"}`),
	}
	got := mapResearchV6Node("run-1", node)
	if got.EntityKind != "generic" || got.NodeKind != "generic" {
		t.Fatalf("unknown kind must degrade to generic: %+v", got)
	}
	if got.EntityID != "display-node" || got.ID != "run-1:generic:display-node" {
		t.Fatalf("generic identity must use the stable source node id: %+v", got)
	}
	if detail, ok := got.Detail.(map[string]any); !ok || detail["kind"] != "future_private_kind" {
		t.Fatalf("bounded opaque detail should preserve the source diagnostic: %#v", got.Detail)
	}
}

func TestResearchV6RootIDsUsesGoalSubtypeForCompatibilityRoot(t *testing.T) {
	roots := researchV6RootIDs([]researchV6ProjectionNode{{ID: "root", EntityKind: "generic", NodeSubtype: "goal"}, {ID: "task", EntityKind: "task"}})
	if len(roots) != 1 || roots[0] != "root" {
		t.Fatalf("roots=%v", roots)
	}
}

func TestMapResearchV6GraphStrictBuildsDeterministicUniqueTopology(t *testing.T) {
	nodes := []ResearchGraphNodeResp{
		{ID: "legacy-task", Payload: json.RawMessage(`{"kind":"task","task_id":"task-1"}`)},
		{ID: "legacy-attempt", Payload: json.RawMessage(`{"kind":"attempt","attempt_id":"attempt-1"}`)},
	}
	edges := []ResearchGraphEdgeResp{{ID: "attempt-edge", FromNodeID: "legacy-task", ToNodeID: "legacy-attempt", EdgeType: "attempted_by"}}
	gotNodes, gotEdges, err := mapResearchV6GraphStrict("run-1", nodes, edges)
	if err != nil || len(gotNodes) != 2 || len(gotEdges) != 1 {
		t.Fatalf("nodes=%+v edges=%+v err=%v", gotNodes, gotEdges, err)
	}
	if gotEdges[0].FromNodeID != "run-1:task:task-1" || gotEdges[0].ToNodeID != "run-1:attempt:attempt-1" {
		t.Fatalf("edge=%+v", gotEdges[0])
	}
}

func TestMapResearchV6GraphStrictRejectsIdentityCollapseAndDanglingEdges(t *testing.T) {
	baseNodes := []ResearchGraphNodeResp{
		{ID: "legacy-a", Payload: json.RawMessage(`{"kind":"task","task_id":"task-1"}`)},
		{ID: "legacy-b", Payload: json.RawMessage(`{"kind":"attempt","attempt_id":"attempt-1"}`)},
	}
	tests := []struct {
		name  string
		nodes []ResearchGraphNodeResp
		edges []ResearchGraphEdgeResp
	}{
		{name: "canonical node collapse", nodes: []ResearchGraphNodeResp{baseNodes[0], {ID: "legacy-other", Payload: json.RawMessage(`{"kind":"task","task_id":"task-1"}`)}}},
		{name: "duplicate source node", nodes: []ResearchGraphNodeResp{baseNodes[0], baseNodes[0]}},
		{name: "dangling edge", nodes: baseNodes, edges: []ResearchGraphEdgeResp{{ID: "edge", FromNodeID: "legacy-a", ToNodeID: "missing", EdgeType: "depends_on"}}},
		{name: "duplicate edge", nodes: baseNodes, edges: []ResearchGraphEdgeResp{{ID: "edge", FromNodeID: "legacy-a", ToNodeID: "legacy-b", EdgeType: "depends_on"}, {ID: "edge", FromNodeID: "legacy-a", ToNodeID: "legacy-b", EdgeType: "depends_on"}}},
		{name: "empty edge type", nodes: baseNodes, edges: []ResearchGraphEdgeResp{{ID: "edge", FromNodeID: "legacy-a", ToNodeID: "legacy-b"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, _, err := mapResearchV6GraphStrict("run-1", tt.nodes, tt.edges); err == nil {
				t.Fatal("expected projection integrity rejection")
			}
		})
	}
}
