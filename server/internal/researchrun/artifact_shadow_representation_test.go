package researchrun

import (
	"encoding/json"
	"errors"
	"testing"
)

func shadowRepresentationFixture() RunSnapshot {
	return RunSnapshot{
		Run:          Run{SessionID: "run-1", Goal: "goal", StateVersion: 1},
		Questions:    []Question{{ID: "question-1", Question: "question one"}},
		Tasks:        []Task{{ID: "task-1", QuestionID: "question-1", Objective: "task one"}},
		Attempts:     []Attempt{{ID: "attempt-1", TaskID: "task-1", AttemptNumber: 1}},
		Sources:      []SourceSnapshotView{{ID: "source-1", CanonicalURL: "https://example.com/source", Metadata: json.RawMessage(`{"nested":{"rank":1}}`)}},
		Observations: []Observation{{ID: "observation-1", SourceSnapshotID: "source-1", Quote: "fact"}},
		Claims: []Claim{
			{ID: "claim-1", Text: "claim one", Evidence: []ClaimEvidence{
				{ArtifactID: "evidence-1", ObservationID: "observation-1", Relation: "supports", Strength: 0.9, Rationale: "direct"},
				{ArtifactID: "evidence-2", ObservationID: "observation-1", Relation: "contradicts", Strength: 0.2, Rationale: "counter"},
			}},
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
	if len(live.Claims[0].Evidence) != 3 {
		t.Fatal("filter mutated the independently loaded live Claim")
	}
}

func TestCompareShadowSnapshotRepresentationsProvesBytesHashAndNesting(t *testing.T) {
	live := shadowRepresentationFixture()
	if err := compareShadowSnapshotRepresentations(live, live); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*RunSnapshot){
		"run bytes":           func(snapshot *RunSnapshot) { snapshot.Run.StateVersion++ },
		"question relation":   func(snapshot *RunSnapshot) { snapshot.Questions[0].ParentQuestionID = "question-parent" },
		"task relation":       func(snapshot *RunSnapshot) { snapshot.Tasks[0].QuestionID = "question-other" },
		"attempt relation":    func(snapshot *RunSnapshot) { snapshot.Attempts[0].TaskID = "task-other" },
		"nested source bytes": func(snapshot *RunSnapshot) { snapshot.Sources[0].Metadata = json.RawMessage(`{"nested":{"rank":2}}`) },
		"evidence bytes":      func(snapshot *RunSnapshot) { snapshot.Claims[0].Evidence[0].Rationale = "changed" },
		"evidence parent": func(snapshot *RunSnapshot) {
			evidence := snapshot.Claims[0].Evidence[0]
			snapshot.Claims[0].Evidence = snapshot.Claims[0].Evidence[1:]
			snapshot.Claims[1].Evidence = []ClaimEvidence{evidence}
		},
		"evidence ordinal": func(snapshot *RunSnapshot) {
			snapshot.Claims[0].Evidence[0], snapshot.Claims[0].Evidence[1] =
				snapshot.Claims[0].Evidence[1], snapshot.Claims[0].Evidence[0]
		},
		"claim ordinal": func(snapshot *RunSnapshot) {
			snapshot.Claims[0], snapshot.Claims[1] = snapshot.Claims[1], snapshot.Claims[0]
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
	allowed := map[string]struct{}{
		"run-1": {}, "question-1": {}, "task-1": {}, "attempt-1": {},
		"source-1": {}, "observation-1": {}, "claim-1": {}, "claim-2": {},
	}
	filtered := filterRunSnapshotByManifest(live, allowed)
	if len(filtered.Claims[0].Evidence) != 0 {
		t.Fatal("omitted Evidence Link remained nested in filtered Claim")
	}
	if err := verifyManifestPromptShadow("live prompt", "manifest prompt", live, filtered); err != nil {
		t.Fatalf("authorized Evidence Link omission rejected: %v", err)
	}
}

func TestFilterRunSnapshotByManifestExcludesUnauthorizedControlArtifacts(t *testing.T) {
	live := shadowRepresentationFixture()
	allowed := map[string]struct{}{"run-1": {}, "question-1": {}, "task-1": {}}
	filtered := filterRunSnapshotByManifest(live, allowed)
	if filtered.Run.SessionID != "run-1" || len(filtered.Questions) != 1 || len(filtered.Tasks) != 1 {
		t.Fatalf("allowed control surface missing: %+v", filtered)
	}
	if len(filtered.Attempts) != 0 || len(filtered.Sources) != 0 || len(filtered.Observations) != 0 || len(filtered.Claims) != 0 {
		t.Fatalf("unauthorized artifact remained in filtered snapshot: %+v", filtered)
	}

	withoutRun := filterRunSnapshotByManifest(live, map[string]struct{}{})
	if withoutRun.Run.SessionID != "" {
		t.Fatalf("unauthorized Run remained in filtered snapshot: %+v", withoutRun.Run)
	}
}
