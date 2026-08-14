package researchrun

import (
	"errors"
	"os"
	"reflect"
	"testing"
)

func validUpdateInquiryStatusInput() UpdateInquiryStatusInput {
	return UpdateInquiryStatusInput{
		WorkspaceID: "10000000-0000-4000-8000-000000000001", SessionID: "10000000-0000-4000-8000-000000000002",
		TransitionID: "10000000-0000-4000-8000-000000000003", AttemptID: "10000000-0000-4000-8000-000000000004",
		AgentID: "10000000-0000-4000-8000-000000000005", IdempotencyKey: "inquiry-status:1", ExpectedStateVersion: 9,
		Target: InquiryEndpoint{Kind: InquiryKindHypothesis, ID: "10000000-0000-4000-8000-000000000006"},
		Before: "investigating", After: "supported", Reason: "  Two independent Claims support this Hypothesis.  ",
		EvidenceRefs: []InquiryStatusEvidenceRef{
			{Kind: "source", ID: "10000000-0000-4000-8000-000000000008"},
			{Kind: "claim", ID: "10000000-0000-4000-8000-000000000007"},
		},
	}
}

func TestInquiryStatusUpdateValidate(t *testing.T) {
	if err := (inquiryStatusUpdateModule{}).Validate(validUpdateInquiryStatusInput()); err != nil {
		t.Fatalf("valid update rejected: %v", err)
	}
	tests := []struct {
		name   string
		mutate func(*UpdateInquiryStatusInput)
	}{
		{name: "transition", mutate: func(in *UpdateInquiryStatusInput) { in.TransitionID = "transition.one" }},
		{name: "state version", mutate: func(in *UpdateInquiryStatusInput) { in.ExpectedStateVersion = 0 }},
		{name: "invalid transition", mutate: func(in *UpdateInquiryStatusInput) { in.Before = "proposed" }},
		{name: "missing evidence", mutate: func(in *UpdateInquiryStatusInput) { in.EvidenceRefs = nil }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			in := validUpdateInquiryStatusInput()
			test.mutate(&in)
			if err := (inquiryStatusUpdateModule{}).Validate(in); err == nil ||
				(!errors.Is(err, ErrInvalidContract) && !errors.Is(err, ErrInvalidTransition)) {
				t.Fatalf("error=%v, want contract or transition error", err)
			}
		})
	}
}

func TestInquiryStatusEventPayloadIsCanonical(t *testing.T) {
	in := validUpdateInquiryStatusInput()
	payload := inquiryStatusEventPayload(in)
	if payload.Reason != "Two independent Claims support this Hypothesis." || payload.EvidenceRefs[0].Kind != "claim" {
		t.Fatalf("payload is not canonical: %+v", payload)
	}
	reversed := validUpdateInquiryStatusInput()
	reversed.EvidenceRefs[0], reversed.EvidenceRefs[1] = reversed.EvidenceRefs[1], reversed.EvidenceRefs[0]
	if !reflect.DeepEqual(payload, inquiryStatusEventPayload(reversed)) {
		t.Fatal("evidence order changed semantic payload")
	}
}

func TestUpdateInquiryStatusTransactionRecovery(t *testing.T) {
	source, err := os.ReadFile("postgres_inquiry_status.go")
	if err != nil {
		t.Fatal(err)
	}
	calls := inspectTransactionBoundaryCalls(t, source, "UpdateInquiryStatus")
	if len(calls.direct) != 0 || calls.runner["beginResearchTx"] != 1 || calls.runner["commitResearchTx"] != 2 {
		t.Fatalf("transaction boundaries=%+v", calls)
	}
}
