package handler

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/multica-ai/multica/server/internal/researchrun"
)

func TestResearchV6DeltaCarriesThroughSequenceSnapshotHash(t *testing.T) {
	snapshot := researchV6Snapshot{
		RunID:                "run-1",
		ThroughEventSequence: 7,
		GraphContentHash: map[string]string{
			"nodes": "sha256:nodes", "edges": "sha256:edges", "clusters": "sha256:clusters",
		},
	}
	delta := researchV6DeltaForSnapshot(snapshot, 4)
	if delta.FromSequenceExclusive != 4 || delta.ThroughSequence != 7 {
		t.Fatalf("delta sequence = %d..%d", delta.FromSequenceExclusive, delta.ThroughSequence)
	}
	if !reflect.DeepEqual(delta.GraphContentHash, snapshot.GraphContentHash) {
		t.Fatalf("delta hash = %v, snapshot hash = %v", delta.GraphContentHash, snapshot.GraphContentHash)
	}
	if delta.GraphContentHash["nodes"] == "" || delta.GraphContentHash["edges"] == "" || delta.GraphContentHash["clusters"] == "" {
		t.Fatalf("delta hash is incomplete: %v", delta.GraphContentHash)
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
	got, err := mapResearchV6Node("run-1", node)
	if err != nil {
		t.Fatal(err)
	}
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
	got, err := mapResearchV6Node("run-1", node)
	if err != nil {
		t.Fatal(err)
	}
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

func TestMapResearchV6NodeProjectsGoalAndTypedD5Semantics(t *testing.T) {
	confidence := .84
	clusterID, parentID := "cluster-1", "parent-source"
	derivedFrom, supersededBy := "derived-source", "replacement-source"
	typed := ResearchGraphTypedNodeResp{
		ID: "display-goal", Level: "XXL", Round: 2, ClusterID: &clusterID, ParentID: &parentID,
		Confidence: &confidence, DocumentCount: 46, ConclusionCount: 4,
		DerivedFrom: &derivedFrom, MergedFrom: []string{"merge-source"}, SupersededBy: &supersededBy,
	}
	node := ResearchGraphNodeResp{
		ID: "display-goal", NodeType: "goal", Title: "Research origin", Status: "failed",
		Payload: json.RawMessage(`{"kind":"root"}`),
	}
	got, err := mapResearchV6NodeWithSemantics("run-1", node, &typed)
	if err != nil {
		t.Fatal(err)
	}
	if got.NodeKind != "goal" || got.NodeSubtype != "goal" || got.Level != "m" || got.Status != "active" || got.Round != 2 {
		t.Fatalf("goal semantics=%+v", got)
	}
	if got.ClusterID == nil || *got.ClusterID != clusterID || got.Confidence == nil || *got.Confidence != confidence ||
		got.DocumentCount == nil || *got.DocumentCount != 46 || got.ConclusionCount == nil || *got.ConclusionCount != 4 {
		t.Fatalf("typed metrics=%+v", got)
	}

	remapResearchV6NodeReferences(&got, map[string]string{
		"parent-source": "run-1:insight:parent", "derived-source": "run-1:claim:derived",
		"merge-source": "run-1:insight:merge", "replacement-source": "run-1:insight:replacement",
	})
	if got.ParentID == nil || *got.ParentID != "run-1:insight:parent" || got.DerivedFrom == nil ||
		*got.DerivedFrom != "run-1:claim:derived" || len(got.MergedFrom) != 1 || got.SupersededBy == nil {
		t.Fatalf("remapped lineage=%+v", got)
	}
}

func TestProjectResearchV6ClustersUsesRealMembershipAndNullableMetrics(t *testing.T) {
	clusterID := "cluster-1"
	confidence := .82
	restart := "old-node"
	clusters := projectResearchV6Clusters(
		[]ResearchGraphClusterResp{{ID: clusterID, Label: "New direction"}},
		[]ResearchGraphTypedNodeResp{{ID: "source-node", ClusterID: &clusterID, Confidence: &confidence, RestartOf: &restart}},
		map[string]string{"source-node": "run-1:branch:new"},
	)
	if len(clusters) != 1 || clusters[0].ClusterType != "new_frontier" || len(clusters[0].MemberNodeIDs) != 1 ||
		clusters[0].Confidence == nil || clusters[0].DocumentCount != nil || clusters[0].ConclusionCount != nil {
		t.Fatalf("clusters=%+v", clusters)
	}
}

func TestRealtimeSequenceAdvanceEnvelopeShape(t *testing.T) {
	encoded, err := json.Marshal(researchV6SequenceAdvanceEnvelope{RunID: "run-1", ThroughSequence: 7})
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != `{"run_id":"run-1","through_sequence":7}` {
		t.Fatalf("sequence advance envelope=%s", encoded)
	}
}

func TestProjectGoalStatusDoesNotInheritRuntimeFailure(t *testing.T) {
	if got := projectGoalStatus(researchrun.RunStatusFailed); got != "active" {
		t.Fatalf("failed Run changed goal status to %q", got)
	}
	if got := projectGoalStatus(researchrun.RunStatusCancelled); got != "abandoned" {
		t.Fatalf("cancelled Run goal status=%q", got)
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
