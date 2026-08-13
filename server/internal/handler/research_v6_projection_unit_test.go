package handler

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/researchrun"
)

func TestResearchV6DeltaCarriesThroughSequenceSnapshotHash(t *testing.T) {
	run := researchrun.RunSnapshot{
		Run:      researchrun.Run{SessionID: "run-1", Goal: "goal", Status: researchrun.RunStatusRunning, StateVersion: 3, LastProgressAt: time.Unix(1_700_000_000, 0).UTC()},
		Contract: researchrun.ResearchContract{Goal: "goal"},
		Tasks:    []researchrun.Task{{ID: "task-1", Objective: "verify", Kind: researchrun.TaskKindVerify, Status: researchrun.TaskStatusReady}},
	}
	snapshot := buildResearchV6Snapshot("run-1", 7, run)
	delta := researchV6DeltaForSnapshot(snapshot, 4)
	if delta.FromSequenceExclusive != 4 || delta.ThroughSequence != 7 {
		t.Fatalf("delta sequence = %d..%d", delta.FromSequenceExclusive, delta.ThroughSequence)
	}
	if !reflect.DeepEqual(delta.GraphContentHash, snapshot.GraphContentHash) {
		t.Fatalf("delta hash = %v, snapshot hash = %v", delta.GraphContentHash, snapshot.GraphContentHash)
	}
	if delta.GraphContentHash["nodes"] == "" || delta.GraphContentHash["edges"] == "" {
		t.Fatalf("delta hash is incomplete: %v", delta.GraphContentHash)
	}
	rebuilt := buildResearchV6Snapshot("run-1", 7, run)
	if !reflect.DeepEqual(rebuilt.GraphContentHash, snapshot.GraphContentHash) {
		t.Fatalf("same state rebuilt hash = %v, want %v", rebuilt.GraphContentHash, snapshot.GraphContentHash)
	}
}

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
