package researchrun

import "testing"

func TestBuildDivergenceContextIsDeterministicAndIsolated(t *testing.T) {
	input := DivergenceContextInput{ContractVersion: 2, PlanVersion: 3, Contract: jsonBytes(`{"goal":"g"}`), Method: jsonBytes(`{"method":"m"}`), KnownFactIDs: []string{"f2", "f1", "f1"}}
	first, err := BuildDivergenceContext(input)
	if err != nil {
		t.Fatal(err)
	}
	input.KnownFactIDs = []string{"f1", "f2"}
	second, err := BuildDivergenceContext(input)
	if err != nil || first.ContextHash != second.ContextHash {
		t.Fatalf("first=%+v second=%+v err=%v", first, second, err)
	}
	input.ConclusionIDs = []string{"conclusion"}
	if _, err := BuildDivergenceContext(input); err == nil {
		t.Fatal("expected forbidden conclusion input rejection")
	}
}

func TestCheckPreDeliveryDivergenceRequiresCurrentVersionAndDisposition(t *testing.T) {
	pass := DivergencePassRecord{
		Trigger: DivergencePreDelivery, ContractVersion: 2, PlanVersion: 3,
		ContextHash:           "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		CandidateDispositions: []DivergenceCandidateDisposition{{CandidateID: "probe", HighImpact: true, RejectionReason: "permission_denied"}},
	}
	if err := CheckPreDeliveryDivergence(2, 3, []DivergencePassRecord{pass}); err != nil {
		t.Fatal(err)
	}
	if err := CheckPreDeliveryDivergence(2, 4, []DivergencePassRecord{pass}); err == nil {
		t.Fatal("expected stale pass rejection")
	}
	pass.CandidateDispositions[0].RejectionReason = ""
	if err := CheckPreDeliveryDivergence(2, 3, []DivergencePassRecord{pass}); err == nil {
		t.Fatal("expected unhandled candidate rejection")
	}
}

func TestValidateDivergencePassUsesFrozenTriggerVocabulary(t *testing.T) {
	pass := DivergencePassRecord{Trigger: "user_requested", ContractVersion: 1, PlanVersion: 1, ContextHash: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	if err := ValidateDivergencePass(pass); err == nil {
		t.Fatal("expected non-V6 trigger rejection")
	}
}

func jsonBytes(value string) []byte { return []byte(value) }
