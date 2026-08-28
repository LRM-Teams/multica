package problemevolution

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func validContract() EvaluatorContract {
	return EvaluatorContract{
		SchemaVersion: SchemaVersion,
		Kind:          EvaluatorKindBuiltinDeterministic,
		Dimensions: []EvaluatorDimension{
			{DimensionID: "correctness", Weight: 0.5, Hard: true, Criteria: "matches the expected result"},
			{DimensionID: "coverage", Weight: 0.5, Criteria: "covers every stated requirement"},
		},
		PassThreshold: 0.8,
		Invoke: EvaluatorInvoke{
			Transport:  "cli",
			Command:    []string{"multica", "problem-evolution", "evaluate"},
			InputPath:  DefaultEvaluatorInput,
			OutputPath: DefaultEvaluatorOutput,
		},
	}
}

func TestEvaluatorContractValidateAcceptsBuiltinContract(t *testing.T) {
	if err := validContract().Validate(); err != nil {
		t.Fatalf("expected valid contract, got %v", err)
	}
}

func TestEvaluatorContractValidateRejectsUserSuppliedVerifier(t *testing.T) {
	// Executing user verifier code needs container isolation, which the daemon
	// does not provide; only builtin kinds may be frozen in this phase.
	contract := validContract()
	contract.Kind = "user_python_verifier"
	err := contract.Validate()
	if !errors.Is(err, ErrContractInvalid) {
		t.Fatalf("expected ErrContractInvalid, got %v", err)
	}
}

func TestEvaluatorContractValidateRejectsDuplicateDimensions(t *testing.T) {
	contract := validContract()
	contract.Dimensions[1].DimensionID = "correctness"
	if err := contract.Validate(); err == nil {
		t.Fatal("expected duplicate dimension_id to be rejected")
	}
}

func TestEvaluatorContractValidateRejectsMissingEvaluatorCommand(t *testing.T) {
	contract := validContract()
	contract.Invoke.Command = nil
	if err := contract.Validate(); err == nil {
		t.Fatal("expected a contract without an evaluator command to be rejected")
	}
}

func TestFeedbackPolicyValidateBoundsBandwidth(t *testing.T) {
	policy := DefaultFeedbackPolicy()
	if err := policy.Validate(); err != nil {
		t.Fatalf("expected the default policy to be valid, got %v", err)
	}
	policy.MaxNumericFields = 12
	if err := policy.Validate(); err == nil {
		t.Fatal("expected an oversized numeric-field budget to be rejected")
	}
	policy = DefaultFeedbackPolicy()
	policy.MaxRounds = 5
	if err := policy.Validate(); err == nil {
		t.Fatal("expected more than two feedback rounds to be rejected")
	}
}

func TestContentHashIsStableAcrossFieldOrder(t *testing.T) {
	contract := validContract()
	policy := DefaultFeedbackPolicy()
	first, err := ContentHash(contract, policy)
	if err != nil {
		t.Fatalf("hash failed: %v", err)
	}
	// Round-tripping through JSON reorders map keys in the intermediate
	// representation; the canonical hash must not move because of that.
	encoded, err := json.Marshal(contract)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	var reordered EvaluatorContract
	if err := json.Unmarshal(encoded, &reordered); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	second, err := ContentHash(reordered, policy)
	if err != nil {
		t.Fatalf("hash failed: %v", err)
	}
	if first != second {
		t.Fatalf("hash drifted: %q vs %q", first, second)
	}
	if !strings.HasPrefix(first, "sha256:") {
		t.Fatalf("expected a sha256-prefixed hash, got %q", first)
	}
}

func TestContentHashChangesWhenScoringChanges(t *testing.T) {
	base, err := ContentHash(validContract(), DefaultFeedbackPolicy())
	if err != nil {
		t.Fatalf("hash failed: %v", err)
	}
	edited := validContract()
	edited.PassThreshold = 0.5
	drifted, err := ContentHash(edited, DefaultFeedbackPolicy())
	if err != nil {
		t.Fatalf("hash failed: %v", err)
	}
	if base == drifted {
		t.Fatal("expected an edited pass threshold to change the content hash")
	}
}

func TestContentHashChangesWhenFeedbackWidens(t *testing.T) {
	base, err := ContentHash(validContract(), DefaultFeedbackPolicy())
	if err != nil {
		t.Fatalf("hash failed: %v", err)
	}
	widened := DefaultFeedbackPolicy()
	widened.Bandwidth = FeedbackBandwidthExact
	drifted, err := ContentHash(validContract(), widened)
	if err != nil {
		t.Fatalf("hash failed: %v", err)
	}
	if base == drifted {
		t.Fatal("expected a widened feedback policy to change the content hash")
	}
}
