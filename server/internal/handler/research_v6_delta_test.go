package handler

import (
	"encoding/json"
	"testing"
)

func TestBuildResearchV6EventDeltaReturnsOnlyAffectedNodes(t *testing.T) {
	snapshot := researchV6Snapshot{ThroughEventSequence: 2, Nodes: []researchV6ProjectionNode{{ID: "task-node", EntityKind: "task", EntityID: "task-1"}, {ID: "attempt-node", EntityKind: "attempt", EntityID: "attempt-1"}, {ID: "other", EntityKind: "task", EntityID: "task-2"}}, Edges: []researchV6ProjectionEdge{{ID: "edge", FromNodeID: "task-node", ToNodeID: "attempt-node"}}}
	events := []researchV6ProjectionEvent{{Sequence: 1, Type: "task_dispatching", Payload: json.RawMessage(`{"task_id":"task-1","attempt_id":"attempt-1"}`)}, {Sequence: 2, Type: "task_dispatched", Payload: json.RawMessage(`{"task_id":"task-1","attempt_id":"attempt-1"}`)}}
	delta, safe := buildResearchV6EventDelta(snapshot, 0, events)
	if !safe || len(delta.NodeUpserts) != 2 || delta.NodeUpserts[0].ID != "attempt-node" || len(delta.EdgeUpserts) != 1 || delta.ThroughSequence != 2 || delta.TransitionKind == nil || *delta.TransitionKind != "task_dispatched" {
		t.Fatalf("delta=%+v safe=%v", delta, safe)
	}
}

func TestBuildResearchV6EventDeltaFailsClosedOnGapUnknownOrStructuralResult(t *testing.T) {
	snapshot := researchV6Snapshot{ThroughEventSequence: 2, Nodes: []researchV6ProjectionNode{{ID: "task", EntityKind: "task", EntityID: "t"}, {ID: "attempt", EntityKind: "attempt", EntityID: "a"}}}
	tests := [][]researchV6ProjectionEvent{
		{{Sequence: 2, Type: "task_dispatched", Payload: json.RawMessage(`{"task_id":"t","attempt_id":"a"}`)}},
		{{Sequence: 1, Type: "future_event", Payload: json.RawMessage(`{}`)}, {Sequence: 2, Type: "run_completed", Payload: json.RawMessage(`{}`)}},
		{{Sequence: 1, Type: "task_result_accepted", Payload: json.RawMessage(`{"task_id":"t","attempt_id":"a","claims_created":1}`)}, {Sequence: 2, Type: "run_completed", Payload: json.RawMessage(`{}`)}},
	}
	for _, events := range tests {
		if _, safe := buildResearchV6EventDelta(snapshot, 0, events); safe {
			t.Fatalf("events=%+v unexpectedly safe", events)
		}
	}
}

func TestBuildResearchV6EventDeltaRequiresResyncWhenClusterTombstoneCannotBeProven(t *testing.T) {
	snapshot := researchV6Snapshot{ThroughEventSequence: 1, Clusters: []researchV6ProjectionCluster{}}
	events := []researchV6ProjectionEvent{{Sequence: 1, Type: "goal_steered", Payload: json.RawMessage(`{"goal_version":2}`)}}
	if _, safe := buildResearchV6EventDelta(snapshot, 0, events); safe {
		t.Fatal("structural event without prior cluster baseline must require Snapshot resync")
	}
}
