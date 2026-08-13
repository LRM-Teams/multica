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

func TestResearchV6RootIDsUsesGoalSubtypeForCompatibilityRoot(t *testing.T) {
	roots := researchV6RootIDs([]researchV6ProjectionNode{{ID: "root", EntityKind: "generic", NodeSubtype: "goal"}, {ID: "task", EntityKind: "task"}})
	if len(roots) != 1 || roots[0] != "root" {
		t.Fatalf("roots=%v", roots)
	}
}
