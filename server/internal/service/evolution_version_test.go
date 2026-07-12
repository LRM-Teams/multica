package service

import (
	"errors"
	"testing"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestBuildEvolutionVersionEvalSummaryUsesAttributedFeedback(t *testing.T) {
	lifetime := EvolutionVersionFeedbackCounts{Total: 20, Injected: 10, Used: 8, Success: 7, Failure: 3}
	attributed := EvolutionVersionFeedbackCounts{Total: 7, Injected: 4, Used: 3, Success: 3, Failure: 1}

	got := BuildEvolutionVersionEvalSummary(lifetime, attributed, 13)

	if got.Basis != "version_attributed" || got.Counts != attributed || got.UnitUnattributedEvents != 13 {
		t.Fatalf("summary = %#v", got)
	}
	if got.Verdict != "mixed" || got.SuccessRate == nil || *got.SuccessRate != 0.75 {
		t.Fatalf("verdict/rate = %q/%v, want mixed/0.75", got.Verdict, got.SuccessRate)
	}
	if got.UsageRate == nil || *got.UsageRate != 0.75 {
		t.Fatalf("usage rate = %v, want 0.75", got.UsageRate)
	}
}

func TestBuildEvolutionVersionEvalSummaryExplainsLifetimeFallback(t *testing.T) {
	lifetime := EvolutionVersionFeedbackCounts{Total: 4, Injected: 2, Used: 1, Success: 1}

	got := BuildEvolutionVersionEvalSummary(lifetime, EvolutionVersionFeedbackCounts{}, 4)

	if got.Basis != "unit_lifetime_fallback" || got.Counts != lifetime || got.Verdict != "positive" {
		t.Fatalf("summary = %#v", got)
	}
	if len(got.Explanations) < 2 || got.Explanations[0] == "" {
		t.Fatalf("expected fallback explanation, got %#v", got.Explanations)
	}
}

func TestBuildEvolutionVersionEvalSummaryHandlesNoOutcomes(t *testing.T) {
	got := BuildEvolutionVersionEvalSummary(EvolutionVersionFeedbackCounts{Total: 2, Injected: 2}, EvolutionVersionFeedbackCounts{}, 2)

	if got.Verdict != "insufficient_data" || got.SuccessRate != nil {
		t.Fatalf("summary = %#v", got)
	}
}

func TestEvolutionMatcherSnapshotRoundTripAndValidation(t *testing.T) {
	want := EvolutionMatcherSnapshot{
		CanonicalSummary: "summary", Tags: []string{"tag"}, Tools: []string{}, TaskTypes: []string{"task"},
		ProjectTypes: []string{}, Languages: []string{"go"}, Frameworks: []string{"chi"},
	}
	metadata := metadataWithEvolutionMatcherSnapshot([]byte(`{"source":"test"}`), want)
	got, err := evolutionMatcherSnapshotFromMetadata(metadata)
	if err != nil || got.CanonicalSummary != want.CanonicalSummary || !stringSlicesEqual(got.Tags, want.Tags) || !stringSlicesEqual(got.Tools, want.Tools) {
		t.Fatalf("snapshot=%#v err=%v", got, err)
	}
	if _, err := evolutionMatcherSnapshotFromMetadata([]byte(`{"source":"legacy"}`)); !errors.Is(err, ErrEvolutionSkillVersionSnapshot) {
		t.Fatalf("legacy error=%v, want ErrEvolutionSkillVersionSnapshot", err)
	}
	if _, err := evolutionMatcherSnapshotFromMetadata([]byte(`{"matcher_snapshot":{"canonical_summary":"partial"}}`)); !errors.Is(err, ErrEvolutionSkillVersionSnapshot) {
		t.Fatalf("partial error=%v, want ErrEvolutionSkillVersionSnapshot", err)
	}
}

func TestMaterializedEvolutionVersionFiles(t *testing.T) {
	files := []db.SharedEvolutionUnitFile{
		{Path: "references/runbook.md", Content: "runbook"},
		{Path: "SKILL.md", Content: "---\nname: versioned\ndescription: old description\n---\nbody"},
	}

	main, supporting, err := materializedEvolutionVersionFiles(files)
	if err != nil {
		t.Fatalf("materializedEvolutionVersionFiles: %v", err)
	}
	if main == "" || len(supporting) != 1 || supporting[0].Path != "references/runbook.md" {
		t.Fatalf("main/supporting = %q/%#v", main, supporting)
	}
}

func TestMaterializedEvolutionVersionFilesRequiresSkillMain(t *testing.T) {
	_, _, err := materializedEvolutionVersionFiles([]db.SharedEvolutionUnitFile{{Path: "README.md", Content: "details"}})
	if !errors.Is(err, ErrEvolutionSkillVersionIncomplete) {
		t.Fatalf("error = %v, want ErrEvolutionSkillVersionIncomplete", err)
	}
}
