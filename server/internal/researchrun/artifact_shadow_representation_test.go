package researchrun

import (
	"encoding/json"
	"errors"
	"testing"
)

func shadowRepresentationFixture() RunSnapshot {
	return RunSnapshot{
		Sources:      []SourceSnapshotView{{ID: "source-1", CanonicalURL: "https://example.com/source", Metadata: json.RawMessage(`{"nested":{"rank":1}}`)}},
		Observations: []Observation{{ID: "observation-1", SourceSnapshotID: "source-1", Quote: "fact"}},
		Claims: []Claim{
			{ID: "claim-1", Text: "claim one", Evidence: []ClaimEvidence{{ArtifactID: "evidence-1", ObservationID: "observation-1", Relation: "supports", Strength: 0.9, Rationale: "direct"}}},
			{ID: "claim-2", Text: "claim two", Evidence: []ClaimEvidence{}},
		},
	}
}

func TestFilterClaimsByManifestFiltersNestedEvidenceLinks(t *testing.T) {
	live := shadowRepresentationFixture()
	live.Claims[0].Evidence = append(live.Claims[0].Evidence, ClaimEvidence{ArtifactID: "evidence-denied", ObservationID: "observation-1", Relation: "supports"})
	filtered := filterClaimsByManifest(live.Claims, map[string]struct{}{"claim-1": {}, "evidence-1": {}})
	if len(filtered) != 1 || len(filtered[0].Evidence) != 1 || filtered[0].Evidence[0].ArtifactID != "evidence-1" {
		t.Fatalf("filtered=%+v", filtered)
	}
	if len(live.Claims[0].Evidence) != 2 {
		t.Fatal("filter mutated the independently loaded live Claim")
	}
}

func TestCompareShadowSnapshotRepresentationsProvesBytesHashAndNesting(t *testing.T) {
	live := shadowRepresentationFixture()
	if err := compareShadowSnapshotRepresentations(live, live); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*RunSnapshot){
		"nested source bytes": func(snapshot *RunSnapshot) { snapshot.Sources[0].Metadata = json.RawMessage(`{"nested":{"rank":2}}`) },
		"evidence bytes":      func(snapshot *RunSnapshot) { snapshot.Claims[0].Evidence[0].Rationale = "changed" },
		"evidence parent": func(snapshot *RunSnapshot) {
			evidence := snapshot.Claims[0].Evidence[0]
			snapshot.Claims[0].Evidence = nil
			snapshot.Claims[1].Evidence = []ClaimEvidence{evidence}
		},
	} {
		t.Run(name, func(t *testing.T) {
			filtered := shadowRepresentationFixture()
			mutate(&filtered)
			if err := compareShadowSnapshotRepresentations(live, filtered); !errors.Is(err, ErrInvalidTransition) {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestManifestPromptShadowAllowsAuthorizedNestedEvidenceOmission(t *testing.T) {
	live := shadowRepresentationFixture()
	allowed := map[string]struct{}{"source-1": {}, "observation-1": {}, "claim-1": {}, "claim-2": {}}
	filtered := filterRunSnapshotByManifest(live, allowed)
	if len(filtered.Claims[0].Evidence) != 0 {
		t.Fatal("omitted Evidence Link remained nested in filtered Claim")
	}
	if err := verifyManifestPromptShadow("live prompt", "manifest prompt", live, filtered); err != nil {
		t.Fatalf("authorized Evidence Link omission rejected: %v", err)
	}
}
