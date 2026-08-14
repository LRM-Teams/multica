package researchrun

import (
	"errors"
	"testing"
)

func TestCompareShadowDomainProjectionRequiresExactKindIDAndDisposition(t *testing.T) {
	domain := []shadowDomainProjectionRecord{
		{Kind: ArtifactKindClaim, ArtifactID: "claim-a", Disposition: shadowDispositionEntry},
		{Kind: ArtifactKindStageEvaluation, ArtifactID: "eval-a", Disposition: "evaluation_compartment"},
	}
	if err := compareShadowDomainProjection(domain, append([]shadowDomainProjectionRecord(nil), domain...), "sha256:exact"); err != nil {
		t.Fatalf("exact projection mismatch: %v", err)
	}

	mutations := []struct {
		name   string
		mutate func([]shadowDomainProjectionRecord) []shadowDomainProjectionRecord
	}{
		{name: "kind", mutate: func(in []shadowDomainProjectionRecord) []shadowDomainProjectionRecord {
			in[0].Kind = ArtifactKindObservation
			return in
		}},
		{name: "artifact id", mutate: func(in []shadowDomainProjectionRecord) []shadowDomainProjectionRecord {
			in[0].ArtifactID = "claim-b"
			return in
		}},
		{name: "omission classification", mutate: func(in []shadowDomainProjectionRecord) []shadowDomainProjectionRecord {
			in[1].Disposition = shadowDispositionEntry
			return in
		}},
		{name: "missing row", mutate: func(in []shadowDomainProjectionRecord) []shadowDomainProjectionRecord {
			return in[:1]
		}},
	}
	for _, tc := range mutations {
		t.Run(tc.name, func(t *testing.T) {
			manifest := tc.mutate(append([]shadowDomainProjectionRecord(nil), domain...))
			err := compareShadowDomainProjection(domain, manifest, "sha256:mutated")
			if !errors.Is(err, ErrInvalidTransition) {
				t.Fatalf("err=%v want ErrInvalidTransition", err)
			}
		})
	}
}

func TestProjectManifestForShadowIncludesEntriesAndNamedOmissions(t *testing.T) {
	plan := dispatchManifestPlan{
		Entries: []artifactVersionCandidate{{Kind: ArtifactKindTask, ArtifactID: "task-a"}},
		Omissions: []artifactVersionCandidate{{
			Kind: ArtifactKindStageEvaluation, ArtifactID: "eval-a", OmissionReason: "evaluation_compartment",
		}},
	}
	got := projectManifestForShadow(plan)
	want := []shadowDomainProjectionRecord{
		{Kind: ArtifactKindStageEvaluation, ArtifactID: "eval-a", Disposition: "evaluation_compartment"},
		{Kind: ArtifactKindTask, ArtifactID: "task-a", Disposition: shadowDispositionEntry},
	}
	if len(got) != len(want) {
		t.Fatalf("projection=%+v want=%+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("projection[%d]=%+v want=%+v", i, got[i], want[i])
		}
	}
}
