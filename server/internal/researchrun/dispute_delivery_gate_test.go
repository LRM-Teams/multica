package researchrun

import (
	"errors"
	"testing"
)

const (
	disputeDeliveryReportID = "10000000-0000-4000-8000-000000000001"
	disputeDeliveryID       = "20000000-0000-4000-8000-000000000001"
	disputeDeliveryHash     = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
)

func disputeDeliveryFixture() DisputeDeliveryGateInput {
	return DisputeDeliveryGateInput{
		PolicyVersion:    DisputeDeliveryGatePolicyV1,
		ReportRevisionID: disputeDeliveryReportID,
		ReportHash:       disputeDeliveryHash,
		Disputes: []DisputeDeliveryGateItem{{
			DisputeID: disputeDeliveryID, Severity: "blocking", Status: "conditionally_resolved",
			Condition:           "The conclusion applies only to regulated teams.",
			ResidualUncertainty: "Small teams have insufficient longitudinal evidence.",
			Impact:              "The recommendation is conditional outside regulated teams.",
			Disclosure: &DisputeReportDisclosure{
				ReportRevisionID: disputeDeliveryReportID, Anchor: "section:limitations:paragraph:2",
				Condition:           "The conclusion applies only to regulated teams.",
				ResidualUncertainty: "Small teams have insufficient longitudinal evidence.",
				Impact:              "The recommendation is conditional outside regulated teams.",
			},
		}},
	}
}

func TestEvaluateDisputeDeliveryGatePassesExactDisclosure(t *testing.T) {
	result, err := EvaluateDisputeDeliveryGate(disputeDeliveryFixture())
	if err != nil {
		t.Fatal(err)
	}
	if !result.Passed || len(result.Findings) != 0 || len(result.Fingerprint) != 71 {
		t.Fatalf("result=%+v", result)
	}
}

func TestEvaluateDisputeDeliveryGateBlocksOpenBlockingDispute(t *testing.T) {
	input := disputeDeliveryFixture()
	input.Disputes[0] = DisputeDeliveryGateItem{DisputeID: disputeDeliveryID, Severity: "blocking", Status: "open"}
	result, err := EvaluateDisputeDeliveryGate(input)
	if err != nil {
		t.Fatal(err)
	}
	if result.Passed || len(result.Findings) != 1 || result.Findings[0].Code != "blocking_dispute" {
		t.Fatalf("result=%+v", result)
	}
}

func TestEvaluateDisputeDeliveryGateRequiresCurrentExactDisclosure(t *testing.T) {
	input := disputeDeliveryFixture()
	input.Disputes[0].Disclosure.ReportRevisionID = "10000000-0000-4000-8000-000000000002"
	input.Disputes[0].Disclosure.Impact = "The recommendation has no residual impact."
	result, err := EvaluateDisputeDeliveryGate(input)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"disclosure_mismatch", "disclosure_stale"}
	if result.Passed || len(result.Findings) != len(want) {
		t.Fatalf("result=%+v", result)
	}
	for index := range want {
		if result.Findings[index].Code != want[index] {
			t.Fatalf("findings=%v want=%v", result.Findings, want)
		}
	}
}

func TestEvaluateDisputeDeliveryGateRequiresHumanDecision(t *testing.T) {
	input := disputeDeliveryFixture()
	input.Disputes[0].HumanDecisionRequired = true
	result, err := EvaluateDisputeDeliveryGate(input)
	if err != nil {
		t.Fatal(err)
	}
	if result.Passed || len(result.Findings) != 1 || result.Findings[0].Code != "human_gate_pending" {
		t.Fatalf("result=%+v", result)
	}
	input.Disputes[0].HumanDecisionRecorded = true
	if result, err = EvaluateDisputeDeliveryGate(input); err != nil || !result.Passed {
		t.Fatalf("recorded result=%+v err=%v", result, err)
	}
}

func TestEvaluateDisputeDeliveryGateIsOrderStable(t *testing.T) {
	input := disputeDeliveryFixture()
	first := DisputeDeliveryGateItem{DisputeID: "20000000-0000-4000-8000-000000000002", Severity: "blocking", Status: "investigating"}
	input.Disputes = append(input.Disputes, first)
	firstResult, err := EvaluateDisputeDeliveryGate(input)
	if err != nil {
		t.Fatal(err)
	}
	input.Disputes[0], input.Disputes[1] = input.Disputes[1], input.Disputes[0]
	secondResult, err := EvaluateDisputeDeliveryGate(input)
	if err != nil {
		t.Fatal(err)
	}
	if firstResult.Fingerprint != secondResult.Fingerprint {
		t.Fatalf("unstable fingerprints: %s != %s", firstResult.Fingerprint, secondResult.Fingerprint)
	}
}

func TestEvaluateDisputeDeliveryGateRejectsInvalidInput(t *testing.T) {
	for name, mutate := range map[string]func(*DisputeDeliveryGateInput){
		"invalid report hash": func(input *DisputeDeliveryGateInput) { input.ReportHash = "sha256:ABC" },
		"duplicate dispute":   func(input *DisputeDeliveryGateInput) { input.Disputes = append(input.Disputes, input.Disputes[0]) },
		"missing residual":    func(input *DisputeDeliveryGateInput) { input.Disputes[0].ResidualUncertainty = "" },
		"disclosure on open":  func(input *DisputeDeliveryGateInput) { input.Disputes[0].Status = "open" },
		"unknown status":      func(input *DisputeDeliveryGateInput) { input.Disputes[0].Status = "deadlocked" },
	} {
		t.Run(name, func(t *testing.T) {
			input := disputeDeliveryFixture()
			mutate(&input)
			if _, err := EvaluateDisputeDeliveryGate(input); !errors.Is(err, ErrInvalidContract) {
				t.Fatalf("err=%v want ErrInvalidContract", err)
			}
		})
	}
}
