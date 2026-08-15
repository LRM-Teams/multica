package researchrun

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBuildArtifactProjectionStableAndFailSafe(t *testing.T) {
	t.Parallel()
	rows := []artifactProjectionRow{
		{PassportID: "passport-b", EntityKind: "future-secret", CurrentVersion: intPtr(2), EligibilityRevision: 3, LifecycleStatus: "future-status", Provenance: "future-provenance", AccessLevel: "classified", ContentHash: "sha256:private-b", VersionCount: 2},
		{PassportID: "passport-a", EntityKind: string(ArtifactKindClaim), CurrentVersion: intPtr(1), EligibilityRevision: 1, LifecycleStatus: string(ArtifactLifecycleAccepted), Provenance: string(ArtifactProvenanceComplete), SchemaName: "claim", SchemaVersion: "v1", AccessLevel: string(ArtifactAccessVerifiedOnly), ContentHash: "sha256:private-a", VersionCount: 1, InputCount: 2, OutputCount: 1},
	}

	first, err := buildArtifactProjection("run-1", rows)
	if err != nil {
		t.Fatal(err)
	}
	second, err := buildArtifactProjection("run-1", []artifactProjectionRow{rows[1], rows[0]})
	if err != nil {
		t.Fatal(err)
	}
	if first.ProjectionHash != second.ProjectionHash || len(first.Items) != 2 {
		t.Fatalf("projection must be order-stable: first=%+v second=%+v", first, second)
	}
	unknown := first.Items[1]
	if unknown.EntityKind != "generic" || unknown.AccessLevel != "unknown" || unknown.LifecycleStatus != "unknown" || unknown.ProvenanceCompleteness != "unknown" {
		t.Fatalf("unknown metadata must degrade safely: %+v", unknown)
	}
	if unknown.ID != "run-1:generic:passport-b" || unknown.EntityID != "passport-b" {
		t.Fatalf("projection identity must use passport id: %+v", unknown)
	}
	encoded, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "private-a") || strings.Contains(string(encoded), "private-b") {
		t.Fatalf("bounded projection leaked a content hash: %s", encoded)
	}
}

func TestArtifactProjectionHumanPrivateKindFilterTracksPolicy(t *testing.T) {
	t.Parallel()
	got := evaluationPrivateArtifactKindStrings()
	if len(got) != 2 || got[0] != string(ArtifactKindEvaluationDecision) || got[1] != string(ArtifactKindStageEvaluation) {
		t.Fatalf("evaluation-private projection filter drifted from policy: %v", got)
	}
}
