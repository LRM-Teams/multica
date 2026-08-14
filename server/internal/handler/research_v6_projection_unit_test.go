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

func TestResearchV6ResumeRequiresResyncFencesSnapshotAndRetention(t *testing.T) {
	current := researchV6Snapshot{SnapshotID: "sha256:current", ThroughEventSequence: 20}
	tests := []struct {
		name          string
		cursor        researchV6ResumeCursor
		firstRetained int64
		want          bool
	}{
		{name: "contiguous", cursor: researchV6ResumeCursor{SnapshotID: "sha256:current", LastConfirmedSequence: 10}, firstRetained: 11},
		{name: "legacy client remains compatible", cursor: researchV6ResumeCursor{LastConfirmedSequence: 10}, firstRetained: 11},
		{name: "snapshot changed", cursor: researchV6ResumeCursor{SnapshotID: "sha256:old", LastConfirmedSequence: 10}, firstRetained: 11, want: true},
		{name: "cursor ahead", cursor: researchV6ResumeCursor{SnapshotID: "sha256:current", LastConfirmedSequence: 21}, firstRetained: 1, want: true},
		{name: "history expired", cursor: researchV6ResumeCursor{SnapshotID: "sha256:current", LastConfirmedSequence: 10}, firstRetained: 13, want: true},
		{name: "initial baseline history expired", cursor: researchV6ResumeCursor{SnapshotID: "sha256:current", LastConfirmedSequence: 0}, firstRetained: 13, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := researchV6ResumeRequiresResync(tt.cursor, current, tt.firstRetained); got != tt.want {
				t.Fatalf("got=%v want=%v", got, tt.want)
			}
		})
	}
}
