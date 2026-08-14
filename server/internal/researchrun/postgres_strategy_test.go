package researchrun

import (
	"encoding/json"
	"math"
	"os"
	"testing"
	"time"
)

func TestCanonicalStrategyJSONIgnoresObjectKeyOrder(t *testing.T) {
	left, leftHash, err := canonicalStrategyJSON(json.RawMessage(`{"model":"x","limits":{"cost":4,"time":2}}`))
	if err != nil {
		t.Fatal(err)
	}
	right, rightHash, err := canonicalStrategyJSON(json.RawMessage(`{"limits":{"time":2,"cost":4},"model":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	if string(left) != string(right) || leftHash != rightHash {
		t.Fatalf("canonical config mismatch: %s/%s %s/%s", left, leftHash, right, rightHash)
	}
}

func TestStrategyPromotionRequestHashBindsEvidenceAndDecision(t *testing.T) {
	input := PersistStrategyPromotionInput{
		WorkspaceID: "workspace", RequestKey: "request-1", Promotion: promotionFixture(),
		EvaluationCompletedAt: time.Unix(100, 0),
	}
	decision, err := EvaluateStrategyPromotion(input.Promotion)
	if err != nil {
		t.Fatal(err)
	}
	base, err := strategyPromotionRequestHash(input, "sha256:config", "sha256:evaluation", decision)
	if err != nil {
		t.Fatal(err)
	}
	input.Promotion.ApproverUserID = "other-user"
	changed, err := strategyPromotionRequestHash(input, "sha256:config", "sha256:evaluation", decision)
	if err != nil {
		t.Fatal(err)
	}
	if base == changed {
		t.Fatal("request hash did not bind approver")
	}
}

func TestEvaluateStrategyPromotionRejectsNonFiniteOrSameVersion(t *testing.T) {
	input := promotionFixture()
	input.Candidate.Cost = math.NaN()
	if _, err := EvaluateStrategyPromotion(input); err == nil {
		t.Fatal("expected non-finite candidate cost rejection")
	}
	input = promotionFixture()
	input.Candidate.StrategyVersion = input.Current.StrategyVersion
	if _, err := EvaluateStrategyPromotion(input); err == nil {
		t.Fatal("expected same-version promotion rejection")
	}
	input = promotionFixture()
	input.Candidate.ModeScores["market"] = 1.1
	if _, err := EvaluateStrategyPromotion(input); err == nil {
		t.Fatal("expected invalid mode score rejection")
	}
}

// Promotion is workspace-scoped rather than Run-scoped, so it cannot use the
// Run fixture recovery matrix. This source-level guard keeps it on the common
// begin/commit fault-injection seam; commit outcome behavior is tested once by
// the shared transaction runner tests.
func TestPersistStrategyPromotionTransactionRecovery(t *testing.T) {
	source := readResearchSource(t, "postgres_strategy.go")
	calls := inspectTransactionBoundaryCalls(t, source, "PersistStrategyPromotion")
	if len(calls.direct) != 0 || calls.runner["beginResearchTx"] != 1 || calls.runner["commitResearchTx"] != 2 {
		t.Fatalf("transaction calls=%+v", calls)
	}
}

func readResearchSource(t *testing.T, path string) []byte {
	t.Helper()
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}
