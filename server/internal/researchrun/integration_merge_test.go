package researchrun

import (
	"errors"
	"reflect"
	"testing"
)

const (
	integrationMergeLeftID  = "10000000-0000-4000-8000-000000000001"
	integrationMergeRightID = "10000000-0000-4000-8000-000000000002"
	integrationMergeHashA   = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	integrationMergeHashB   = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func integrationMergeFixture() IntegrationMergeIntent {
	return IntegrationMergeIntent{
		PolicyVersion: IntegrationMergePolicyVersionV1,
		Left:          IntegrationMergeEntity{ID: integrationMergeLeftID, Kind: IntegrationMergeClaim, Status: "supported", SemanticFingerprint: integrationMergeHashA, ScopeKey: "global", MethodKey: "cost-model-v1", TimeKey: "2026-q3", Accessible: true},
		Right:         IntegrationMergeEntity{ID: integrationMergeRightID, Kind: IntegrationMergeClaim, Status: "supported", SemanticFingerprint: integrationMergeHashB, ScopeKey: "global", MethodKey: "cost-model-v1", TimeKey: "2026-q3", Accessible: true},
		Signals:       IntegrationMergeSignals{SemanticSimilarity: 0.95, LexicalSimilarity: 0.72, EntityOverlap: 0.8},
		Rationale:     "Both Claims describe the same scoped cost conclusion.",
	}
}

func TestEvaluateIntegrationMergeCandidateProposesNearDuplicate(t *testing.T) {
	decision, err := EvaluateIntegrationMergeCandidate(integrationMergeFixture())
	if err != nil {
		t.Fatal(err)
	}
	if decision.Disposition != "propose_merge" || len(decision.Reasons) != 0 || len(decision.Fingerprint) != 71 {
		t.Fatalf("decision=%+v", decision)
	}
}

func TestEvaluateIntegrationMergeCandidateAcceptsExactFingerprint(t *testing.T) {
	intent := integrationMergeFixture()
	intent.Right.SemanticFingerprint = intent.Left.SemanticFingerprint
	intent.Signals = IntegrationMergeSignals{}
	decision, err := EvaluateIntegrationMergeCandidate(intent)
	if err != nil || decision.Disposition != "propose_merge" {
		t.Fatalf("decision=%+v err=%v", decision, err)
	}
}

func TestEvaluateIntegrationMergeCandidateIsSymmetric(t *testing.T) {
	intent := integrationMergeFixture()
	first, err := EvaluateIntegrationMergeCandidate(intent)
	if err != nil {
		t.Fatal(err)
	}
	intent.Left, intent.Right = intent.Right, intent.Left
	second, err := EvaluateIntegrationMergeCandidate(intent)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("asymmetric decisions: first=%+v second=%+v", first, second)
	}
}

func TestEvaluateIntegrationMergeCandidateReturnsOrderedRejectionReasons(t *testing.T) {
	intent := integrationMergeFixture()
	intent.Right.Kind = IntegrationMergeQuestion
	intent.Right.Accessible = false
	intent.Right.Status = "obsolete"
	intent.Right.ScopeKey = "regional"
	intent.Right.MethodKey = "survey-v1"
	intent.Right.TimeKey = "2025"
	intent.Signals = IntegrationMergeSignals{SemanticSimilarity: 0.4, LexicalSimilarity: 0.3, EntityOverlap: 0.2}
	decision, err := EvaluateIntegrationMergeCandidate(intent)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"below_similarity_threshold", "different_kind", "inaccessible_input", "method_mismatch", "scope_mismatch", "terminal_input", "time_mismatch"}
	if decision.Disposition != "reject" || len(decision.Reasons) != len(want) {
		t.Fatalf("decision=%+v", decision)
	}
	for index := range want {
		if decision.Reasons[index] != want[index] {
			t.Fatalf("reasons=%v want=%v", decision.Reasons, want)
		}
	}
}

func TestEvaluateIntegrationMergeCandidateRejectsInvalidContract(t *testing.T) {
	for name, mutate := range map[string]func(*IntegrationMergeIntent){
		"same entity":     func(intent *IntegrationMergeIntent) { intent.Right.ID = intent.Left.ID },
		"unknown kind":    func(intent *IntegrationMergeIntent) { intent.Left.Kind = "dispute" },
		"unknown status":  func(intent *IntegrationMergeIntent) { intent.Left.Status = "merged" },
		"invalid hash":    func(intent *IntegrationMergeIntent) { intent.Left.SemanticFingerprint = "sha256:ABC" },
		"unbounded score": func(intent *IntegrationMergeIntent) { intent.Signals.SemanticSimilarity = 1.1 },
		"weak rationale":  func(intent *IntegrationMergeIntent) { intent.Rationale = "similar" },
	} {
		t.Run(name, func(t *testing.T) {
			intent := integrationMergeFixture()
			mutate(&intent)
			if _, err := EvaluateIntegrationMergeCandidate(intent); !errors.Is(err, ErrInvalidContract) {
				t.Fatalf("err=%v want ErrInvalidContract", err)
			}
		})
	}
}
