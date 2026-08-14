package researchrun

import (
	"errors"
	"testing"
)

const (
	explorationSignalID = "10000000-0000-4000-8000-000000000001"
	explorationTargetID = "20000000-0000-4000-8000-000000000001"
)

func explorationNeedFixture() ExplorationNeedSignal {
	return ExplorationNeedSignal{
		SignalID: explorationSignalID, Kind: ExplorationNeedEvidenceGap,
		TargetID: explorationTargetID, TargetKind: "claim",
		SourceFamilyKey: "official-records", MethodKey: "document-analysis", PerspectiveKey: "buyer",
		CounterevidenceRequired: true, ReasonableImpactPath: true,
		Score:     ExplorationCandidateScoreFacts{DecisionImpact: 0.9, UncertaintyReduction: 0.8, ExpectedInformationGain: 0.75, Novelty: 0.5, ExpectedSuccessProbability: 0.85, DuplicateOverlap: 0.1, DependencyProviderRisk: 0.15},
		Cost:      ExplorationCandidateCostFacts{Time: 2, Token: 1, Tool: 0.5},
		Rationale: "The high-impact Claim lacks independent supporting evidence.",
	}
}

func TestGenerateExplorationCandidatesExpandsEvidenceGap(t *testing.T) {
	result, err := GenerateExplorationCandidates(ExplorationCandidateGenerationPolicyV1, []ExplorationNeedSignal{explorationNeedFixture()})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Candidates) != 2 || len(result.Fingerprint) != 71 {
		t.Fatalf("result=%+v", result)
	}
	kinds := map[string]bool{}
	for _, candidate := range result.Candidates {
		kinds[candidate.TaskKind] = true
		if !candidate.ProtectsHighImpactTarget || candidate.CandidateKey == "" || candidate.TargetID != explorationTargetID {
			t.Fatalf("candidate=%+v", candidate)
		}
	}
	if !kinds["verify"] || !kinds["counter_search"] {
		t.Fatalf("task kinds=%v", kinds)
	}
}

func TestGenerateExplorationCandidatesMapsCanonicalNeeds(t *testing.T) {
	tests := []struct {
		need       ExplorationNeedKind
		targetKind string
		taskKind   string
		purpose    string
	}{
		{ExplorationNeedRequiredQuestion, "question", "discover", "answer_required_question"},
		{ExplorationNeedBlockingDispute, "dispute", "verify", "distinguish_dispute_positions"},
		{ExplorationNeedInsightStale, "insight", "synthesize", "reintegrate_stale_insight"},
		{ExplorationNeedMethodGap, "hypothesis", "verify", "validate_method_gap"},
	}
	for _, test := range tests {
		t.Run(string(test.need), func(t *testing.T) {
			signal := explorationNeedFixture()
			signal.Kind, signal.TargetKind, signal.CounterevidenceRequired = test.need, test.targetKind, false
			if test.need == ExplorationNeedMethodGap {
				signal.MethodKey = "causal-inference"
			}
			result, err := GenerateExplorationCandidates(ExplorationCandidateGenerationPolicyV1, []ExplorationNeedSignal{signal})
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Candidates) == 0 || result.Candidates[0].TaskKind != test.taskKind || result.Candidates[0].Purpose != test.purpose {
				t.Fatalf("candidates=%+v", result.Candidates)
			}
		})
	}
}

func TestGenerateExplorationCandidatesCreatesDivergenceProbe(t *testing.T) {
	signal := explorationNeedFixture()
	signal.Kind, signal.TargetKind, signal.CounterevidenceRequired = ExplorationNeedHomogeneous, "branch", false
	signal.SourceFamilyKey, signal.PerspectiveKey = "vendor-ecosystem", "regulator"
	result, err := GenerateExplorationCandidates(ExplorationCandidateGenerationPolicyV1, []ExplorationNeedSignal{signal})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Candidates) != 1 || !result.Candidates[0].DivergenceProbe || result.Candidates[0].SourceIndependenceGain != 1 {
		t.Fatalf("candidates=%+v", result.Candidates)
	}
}

func TestGenerateExplorationCandidatesIsOrderStableAndDeduplicatesIdentity(t *testing.T) {
	first := explorationNeedFixture()
	first.CounterevidenceRequired = false
	first.HardConstraintFailures = []string{"tool_unavailable", "permission_denied"}
	second := first
	second.SignalID = "10000000-0000-4000-8000-000000000002"
	second.HardConstraintFailures = []string{"permission_denied", "tool_unavailable"}
	one, err := GenerateExplorationCandidates(ExplorationCandidateGenerationPolicyV1, []ExplorationNeedSignal{first, second})
	if err != nil {
		t.Fatal(err)
	}
	two, err := GenerateExplorationCandidates(ExplorationCandidateGenerationPolicyV1, []ExplorationNeedSignal{second, first})
	if err != nil {
		t.Fatal(err)
	}
	if len(one.Candidates) != 1 || one.Fingerprint != two.Fingerprint {
		t.Fatalf("one=%+v two=%+v", one, two)
	}
}

func TestGenerateExplorationCandidatesRejectsInvalidSignals(t *testing.T) {
	for name, mutate := range map[string]func(*ExplorationNeedSignal){
		"wrong target kind": func(signal *ExplorationNeedSignal) { signal.TargetKind = "dispute" },
		"unknown need":      func(signal *ExplorationNeedSignal) { signal.Kind = "future" },
		"unbounded score":   func(signal *ExplorationNeedSignal) { signal.Score.Novelty = 1.1 },
		"negative cost":     func(signal *ExplorationNeedSignal) { signal.Cost.Tool = -1 },
		"duplicate failure": func(signal *ExplorationNeedSignal) {
			signal.HardConstraintFailures = []string{"permission_denied", "permission_denied"}
		},
		"weak rationale": func(signal *ExplorationNeedSignal) { signal.Rationale = "needed" },
	} {
		t.Run(name, func(t *testing.T) {
			signal := explorationNeedFixture()
			mutate(&signal)
			if _, err := GenerateExplorationCandidates(ExplorationCandidateGenerationPolicyV1, []ExplorationNeedSignal{signal}); !errors.Is(err, ErrInvalidContract) {
				t.Fatalf("err=%v want ErrInvalidContract", err)
			}
		})
	}
}
