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

func TestMapResearchV6NodeStrictUsesKindSpecificIdentity(t *testing.T) {
	claim := ResearchGraphNodeResp{ID: "display-claim", Payload: json.RawMessage(`{"kind":"claim","claim_id":"claim-1","task_id":"producer-task"}`)}
	mapped, err := mapResearchV6NodeStrict("run-1", claim)
	if err != nil || mapped.EntityKind != "claim" || mapped.EntityID != "claim-1" || mapped.ID != "run-1:claim:claim-1" {
		t.Fatalf("mapped=%+v err=%v", mapped, err)
	}
	attempt := ResearchGraphNodeResp{ID: "display-attempt", Payload: json.RawMessage(`{"kind":"attempt","attempt_id":"attempt-1","task_id":"task-1"}`)}
	mapped, err = mapResearchV6NodeStrict("run-1", attempt)
	if err != nil || mapped.EntityID != "attempt-1" {
		t.Fatalf("mapped=%+v err=%v", mapped, err)
	}
}

func TestMapResearchV6NodeStrictRejectsUnprovableCanonicalIdentity(t *testing.T) {
	tests := []ResearchGraphNodeResp{
		{ID: "task", Payload: json.RawMessage(`{"kind":"task","claim_id":"wrong-kind"}`)},
		{ID: "claim", Payload: json.RawMessage(`{"kind":"claim","task_id":"producer-only"}`)},
		{ID: "broken", Payload: json.RawMessage(`{"kind":`)},
	}
	for _, node := range tests {
		if _, err := mapResearchV6NodeStrict("run-1", node); err == nil {
			t.Fatalf("node=%+v unexpectedly accepted", node)
		}
	}
}

func TestMapResearchV6NodeStrictUsesExplicitFutureEntityIdentity(t *testing.T) {
	node := ResearchGraphNodeResp{ID: "display-future", Payload: json.RawMessage(`{"kind":"monitoring_cycle","entity_id":"cycle-1","task_id":"related-task"}`)}
	mapped, err := mapResearchV6NodeStrict("run-1", node)
	if err != nil || mapped.EntityID != "cycle-1" || mapped.ID != "run-1:monitoring_cycle:cycle-1" {
		t.Fatalf("mapped=%+v err=%v", mapped, err)
	}
}
