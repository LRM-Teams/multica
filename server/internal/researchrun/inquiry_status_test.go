package researchrun

import (
	"errors"
	"testing"
)

func validInquiryStatusUpdate() inquiryStatusUpdateCommand {
	return inquiryStatusUpdateCommand{
		Target: inquiryEndpoint{Kind: InquiryKindHypothesis, ID: "10000000-0000-4000-8000-000000000001"},
		Before: "investigating", After: "supported",
		Reason: "Two independent observations support the expected outcome.",
		EvidenceRefs: []inquiryStatusEvidenceRef{
			{Kind: "claim", ID: "20000000-0000-4000-8000-000000000001"},
			{Kind: "source", ID: "30000000-0000-4000-8000-000000000001"},
		},
	}
}

func TestInquiryModuleValidateStatusUpdate(t *testing.T) {
	module := inquiryModule{}
	if err := module.ValidateStatusUpdate(validInquiryStatusUpdate()); err != nil {
		t.Fatalf("ValidateStatusUpdate: %v", err)
	}
	question := validInquiryStatusUpdate()
	question.Target.Kind = InquiryKindQuestion
	question.Before = "answered"
	question.After = "in_progress"
	question.Reason = "New contradictory evidence reopens the question."
	if err := module.ValidateStatusUpdate(question); err != nil {
		t.Fatalf("reopen answered Question: %v", err)
	}
}

func TestInquiryModuleValidateStatusUpdateFailsClosed(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*inquiryStatusUpdateCommand)
		want   error
	}{
		{name: "unresolved target", mutate: func(c *inquiryStatusUpdateCommand) { c.Target.ID = "hypothesis.one" }, want: ErrInvalidContract},
		{name: "unsupported target kind", mutate: func(c *inquiryStatusUpdateCommand) { c.Target.Kind = InquiryKindClaim }, want: ErrInvalidContract},
		{name: "no-op", mutate: func(c *inquiryStatusUpdateCommand) { c.After = c.Before }, want: ErrInvalidTransition},
		{name: "invalid transition", mutate: func(c *inquiryStatusUpdateCommand) { c.Before, c.After = "proposed", "supported" }, want: ErrInvalidTransition},
		{name: "blank reason", mutate: func(c *inquiryStatusUpdateCommand) { c.Reason = "  " }, want: ErrInvalidContract},
		{name: "missing evidence", mutate: func(c *inquiryStatusUpdateCommand) { c.EvidenceRefs = nil }, want: ErrInvalidContract},
		{name: "unknown evidence kind", mutate: func(c *inquiryStatusUpdateCommand) { c.EvidenceRefs[0].Kind = "dispute" }, want: ErrInvalidContract},
		{name: "unresolved evidence", mutate: func(c *inquiryStatusUpdateCommand) { c.EvidenceRefs[0].ID = "claim.one" }, want: ErrInvalidContract},
		{name: "duplicate evidence", mutate: func(c *inquiryStatusUpdateCommand) { c.EvidenceRefs[1] = c.EvidenceRefs[0] }, want: ErrInvalidContract},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			command := validInquiryStatusUpdate()
			tc.mutate(&command)
			if err := (inquiryModule{}).ValidateStatusUpdate(command); !errors.Is(err, tc.want) {
				t.Fatalf("ValidateStatusUpdate err=%v want %v", err, tc.want)
			}
		})
	}
}
