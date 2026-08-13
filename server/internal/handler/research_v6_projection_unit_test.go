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

func TestMapResearchV6GraphUsesSnapshotIdentitiesForRealtimeEdges(t *testing.T) {
	nodes := []ResearchGraphNodeResp{
		{ID: "legacy-task", NodeType: "probe", Payload: json.RawMessage(`{"kind":"task","task_id":"task-1"}`)},
		{ID: "legacy-attempt", NodeType: "agent_activity", Payload: json.RawMessage(`{"kind":"attempt","attempt_id":"attempt-1"}`)},
	}
	edges := []ResearchGraphEdgeResp{{ID: "legacy-edge", FromNodeID: "legacy-task", ToNodeID: "legacy-attempt", EdgeType: "attempted_by"}}

	gotNodes, gotEdges := mapResearchV6Graph("run-1", nodes, edges)
	if len(gotNodes) != 2 || len(gotEdges) != 1 {
		t.Fatalf("nodes=%+v edges=%+v", gotNodes, gotEdges)
	}
	if gotEdges[0].ID != "run-1:edge:legacy-edge" || gotEdges[0].RunID != "run-1" {
		t.Fatalf("unexpected edge identity: %+v", gotEdges[0])
	}
	if gotEdges[0].FromNodeID != "run-1:task:task-1" || gotEdges[0].ToNodeID != "run-1:attempt:attempt-1" {
		t.Fatalf("edge endpoints do not use V6 node identities: %+v", gotEdges[0])
	}
}

func TestResearchV6TransitionKindForCommittedRunEvent(t *testing.T) {
	tests := map[string]string{
		"task_dispatched":      "task_dispatched",
		"task_result_accepted": "result_accepted",
		"dispute_opened":       "dispute_opened",
		"report_revised":       "report_revised",
	}
	for eventType, want := range tests {
		got := researchV6TransitionKindForEvent(eventType)
		if got == nil || *got != want {
			t.Fatalf("event=%q transition=%v want=%q", eventType, got, want)
		}
	}
	if got := researchV6TransitionKindForEvent("run_started"); got != nil {
		t.Fatalf("non-animated event transition=%q, want nil", *got)
	}
}

func TestBuildResearchV6ProjectedGraphEnvelopeFramesCommittedEvent(t *testing.T) {
	nodes := []ResearchGraphNodeResp{{
		ID: "goal", NodeType: "goal", Payload: json.RawMessage(`{"kind":"root"}`),
	}}
	edges := []ResearchGraphEdgeResp{}

	got := buildResearchV6ProjectedGraphEnvelope("run-1", "task_dispatched", 7, nodes, edges)
	if got.RunID != "run-1" || got.Delta.FromSequenceExclusive != 6 || got.Delta.ThroughSequence != 7 {
		t.Fatalf("unexpected envelope framing: %+v", got)
	}
	if len(got.Delta.NodeUpserts) != 1 || got.Delta.NodeUpserts[0].RunID != "run-1" {
		t.Fatalf("unexpected node upserts: %+v", got.Delta.NodeUpserts)
	}
	if len(got.Delta.EdgeUpserts) != 0 || got.Delta.EdgeUpserts == nil || got.Delta.NodeTombstones == nil || got.Delta.EdgeTombstones == nil {
		t.Fatalf("delta collections must be explicit arrays: %+v", got.Delta)
	}
	if len(got.Delta.AffectedRootNodeIDs) != 1 || got.Delta.AffectedRootNodeIDs[0] != got.Delta.NodeUpserts[0].ID {
		t.Fatalf("affected roots=%v nodes=%+v", got.Delta.AffectedRootNodeIDs, got.Delta.NodeUpserts)
	}
	if got.Delta.TransitionKind == nil || *got.Delta.TransitionKind != "task_dispatched" {
		t.Fatalf("transition=%v", got.Delta.TransitionKind)
	}
}
