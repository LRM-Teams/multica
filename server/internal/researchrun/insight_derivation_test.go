package researchrun

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"reflect"
	"testing"
)

func TestInsightDerivationAdmissionComputesServerLevelAndFingerprint(t *testing.T) {
	module := insightDerivationModule{}
	claim := derivationInput(InsightInputClaim, "claim-1", "version-1", "task-1", "", 0)
	insight := derivationInput(InsightInputInsight, "insight-1", "version-2", "task-2", "", 2)
	candidate := InsightDerivationCandidate{
		Relation:       InsightRelationConditions,
		ScopeHash:      testDerivationHash("scope"),
		Inputs:         []InsightDerivationInput{claim, insight},
		ObservedValues: []InsightSemanticValue{InsightValueFrontierChange, InsightValueNewExplanation},
	}
	admission, err := module.Admit(candidate)
	if err != nil {
		t.Fatal(err)
	}
	if !admission.Accepted || admission.Level != 3 || !validSHA256(admission.Fingerprint) {
		t.Fatalf("admission=%+v", admission)
	}
	if !reflect.DeepEqual(admission.InputVersionIDs, []string{"version-1", "version-2"}) {
		t.Fatalf("input versions=%v", admission.InputVersionIDs)
	}

	reversed := candidate
	reversed.Inputs = []InsightDerivationInput{insight, claim}
	reversed.ObservedValues = []InsightSemanticValue{InsightValueNewExplanation, InsightValueFrontierChange}
	second, err := module.Admit(reversed)
	if err != nil {
		t.Fatal(err)
	}
	if second.Fingerprint != admission.Fingerprint || second.Level != admission.Level {
		t.Fatalf("canonical admission drift first=%+v second=%+v", admission, second)
	}
	changedValue := candidate
	changedValue.ObservedValues = []InsightSemanticValue{InsightValueReportChange}
	third, err := module.Admit(changedValue)
	if err != nil {
		t.Fatal(err)
	}
	if third.Fingerprint != admission.Fingerprint {
		t.Fatal("idempotency identity must be input/scope/relation, not value-label order")
	}
}

func TestInsightDerivationAdmissionRejectsDecorativeOrInvalidCandidates(t *testing.T) {
	module := insightDerivationModule{}
	first := derivationInput(InsightInputClaim, "claim-1", "version-1", "task-1", "", 0)
	second := derivationInput(InsightInputClaim, "claim-2", "version-2", "task-2", "", 0)
	base := InsightDerivationCandidate{
		Relation: InsightRelationIntegrates, ScopeHash: testDerivationHash("scope"),
		Inputs: []InsightDerivationInput{first, second}, ObservedValues: []InsightSemanticValue{InsightValueNewExplanation},
	}

	noValue := base
	noValue.ObservedValues = nil
	outcome, err := module.Admit(noValue)
	if err != nil || outcome.Accepted || outcome.RejectionReason != InsightRejectionNoSemanticGain {
		t.Fatalf("no-value outcome=%+v err=%v", outcome, err)
	}

	sameOrigin := base
	sameOrigin.Inputs = append([]InsightDerivationInput(nil), base.Inputs...)
	sameOrigin.Inputs[1].ProducerTaskID = "task-1"
	outcome, err = module.Admit(sameOrigin)
	if err != nil || outcome.Accepted || outcome.RejectionReason != InsightRejectionNoSemanticGain {
		t.Fatalf("same-origin outcome=%+v err=%v", outcome, err)
	}
	differentBranches := sameOrigin
	differentBranches.Inputs = append([]InsightDerivationInput(nil), sameOrigin.Inputs...)
	differentBranches.Inputs[0].BranchID = "branch-1"
	differentBranches.Inputs[1].BranchID = "branch-2"
	outcome, err = module.Admit(differentBranches)
	if err != nil || !outcome.Accepted {
		t.Fatalf("different-branch outcome=%+v err=%v", outcome, err)
	}

	tests := []struct {
		name   string
		mutate func(*InsightDerivationCandidate)
		want   error
	}{
		{name: "one input", mutate: func(c *InsightDerivationCandidate) { c.Inputs = c.Inputs[:1] }, want: ErrInvalidContract},
		{name: "duplicate input", mutate: func(c *InsightDerivationCandidate) { c.Inputs[1] = c.Inputs[0] }, want: ErrInvalidContract},
		{name: "stale input", mutate: func(c *InsightDerivationCandidate) { c.Inputs[0].Fresh = false }, want: ErrInvalidTransition},
		{name: "unaccepted input", mutate: func(c *InsightDerivationCandidate) { c.Inputs[0].Accepted = false }, want: ErrInvalidTransition},
		{name: "claim level", mutate: func(c *InsightDerivationCandidate) { c.Inputs[0].InsightLevel = 1 }, want: ErrInvalidContract},
		{name: "missing origin", mutate: func(c *InsightDerivationCandidate) { c.Inputs[0].ProducerTaskID = "" }, want: ErrInvalidContract},
		{name: "unknown value", mutate: func(c *InsightDerivationCandidate) { c.ObservedValues = []InsightSemanticValue{"future"} }, want: ErrInvalidContract},
		{name: "unknown relation", mutate: func(c *InsightDerivationCandidate) { c.Relation = "future" }, want: ErrInvalidContract},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := base
			candidate.Inputs = append([]InsightDerivationInput(nil), base.Inputs...)
			test.mutate(&candidate)
			_, err := module.Admit(candidate)
			if !errors.Is(err, test.want) {
				t.Fatalf("err=%v want=%v", err, test.want)
			}
		})
	}
}

func TestInsightDerivationDAGAndStalePropagation(t *testing.T) {
	module := insightDerivationModule{}
	edges := []InsightDerivationEdge{
		{InputArtifactID: "claim-a", InsightID: "insight-1"},
		{InputArtifactID: "claim-b", InsightID: "insight-1"},
		{InputArtifactID: "claim-a", InsightID: "insight-2"},
		{InputArtifactID: "insight-1", InsightID: "insight-3"},
		{InputArtifactID: "insight-2", InsightID: "insight-3"},
		{InputArtifactID: "insight-3", InsightID: "insight-4"},
	}
	if err := module.ValidateDAG(edges); err != nil {
		t.Fatalf("valid DAG rejected: %v", err)
	}
	stale, err := module.PropagateStale(edges, []string{"claim-a"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"insight-1", "insight-2", "insight-3", "insight-4"}
	if !reflect.DeepEqual(stale, want) {
		t.Fatalf("stale=%v want=%v", stale, want)
	}
	stale, err = module.PropagateStale(edges, []string{"insight-3"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(stale, []string{"insight-3", "insight-4"}) {
		t.Fatalf("stale insight chain=%v", stale)
	}

	cycle := append(append([]InsightDerivationEdge(nil), edges...), InsightDerivationEdge{InputArtifactID: "insight-4", InsightID: "insight-1"})
	if err = module.ValidateDAG(cycle); !errors.Is(err, ErrInvalidContract) {
		t.Fatalf("cycle err=%v want ErrInvalidContract", err)
	}
	duplicate := append(append([]InsightDerivationEdge(nil), edges...), edges[0])
	if err = module.ValidateDAG(duplicate); !errors.Is(err, ErrInvalidContract) {
		t.Fatalf("duplicate edge err=%v want ErrInvalidContract", err)
	}
	if _, err = module.PropagateStale(cycle, []string{"claim-a"}); !errors.Is(err, ErrInvalidContract) {
		t.Fatalf("cyclic propagation err=%v want ErrInvalidContract", err)
	}
}

func derivationInput(kind InsightInputKind, artifactID, versionID, taskID, branchID string, level int) InsightDerivationInput {
	return InsightDerivationInput{
		Kind: kind, ArtifactID: artifactID, VersionID: versionID,
		ContentHash: testDerivationHash(artifactID), ProducerTaskID: taskID, BranchID: branchID,
		InsightLevel: level, Accepted: true, Fresh: true,
	}
}

func testDerivationHash(seed string) string {
	sum := sha256.Sum256([]byte(seed))
	return "sha256:" + hex.EncodeToString(sum[:])
}
