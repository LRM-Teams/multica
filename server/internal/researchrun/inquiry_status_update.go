package researchrun

import (
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
)

type InquiryStatusEvidenceRef struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

type UpdateInquiryStatusInput struct {
	WorkspaceID          string                     `json:"-"`
	SessionID            string                     `json:"-"`
	TransitionID         string                     `json:"transition_id"`
	AttemptID            string                     `json:"attempt_id"`
	AgentID              string                     `json:"agent_id"`
	IdempotencyKey       string                     `json:"idempotency_key"`
	ExpectedStateVersion int64                      `json:"expected_state_version"`
	Target               InquiryEndpoint            `json:"target"`
	Before               string                     `json:"before"`
	After                string                     `json:"after"`
	Reason               string                     `json:"reason"`
	EvidenceRefs         []InquiryStatusEvidenceRef `json:"evidence_refs"`
}

type UpdateInquiryStatusResult struct {
	TransitionID string   `json:"transition_id"`
	Event        RunEvent `json:"event"`
	Replayed     bool     `json:"replayed"`
}

type inquiryStatusUpdateModule struct{}

func (inquiryStatusUpdateModule) Validate(in UpdateInquiryStatusInput) error {
	for name, value := range map[string]string{
		"workspace": in.WorkspaceID, "session": in.SessionID, "transition": in.TransitionID,
		"attempt": in.AttemptID, "agent": in.AgentID,
	} {
		if _, err := uuid.Parse(value); err != nil {
			return fmt.Errorf("%w: Inquiry status %s identity is invalid", ErrInvalidContract, name)
		}
	}
	if strings.TrimSpace(in.IdempotencyKey) == "" || len(in.IdempotencyKey) > 512 || in.ExpectedStateVersion < 1 {
		return fmt.Errorf("%w: incomplete Inquiry status command identity", ErrInvalidContract)
	}
	refs := make([]inquiryStatusEvidenceRef, len(in.EvidenceRefs))
	for index, ref := range in.EvidenceRefs {
		refs[index] = inquiryStatusEvidenceRef{Kind: ref.Kind, ID: ref.ID}
	}
	return (inquiryModule{}).ValidateStatusUpdate(inquiryStatusUpdateCommand{
		Target: inquiryEndpoint{Kind: in.Target.Kind, ID: in.Target.ID}, Before: in.Before, After: in.After,
		Reason: in.Reason, EvidenceRefs: refs,
	})
}

func canonicalInquiryStatusEvidence(refs []InquiryStatusEvidenceRef) []InquiryStatusEvidenceRef {
	canonical := append([]InquiryStatusEvidenceRef(nil), refs...)
	sort.Slice(canonical, func(i, j int) bool {
		if canonical[i].Kind != canonical[j].Kind {
			return canonical[i].Kind < canonical[j].Kind
		}
		return canonical[i].ID < canonical[j].ID
	})
	return canonical
}

type inquiryStatusUpdatedPayload struct {
	TransitionID         string                     `json:"transition_id"`
	AttemptID            string                     `json:"attempt_id"`
	ExpectedStateVersion int64                      `json:"expected_state_version"`
	Target               InquiryEndpoint            `json:"target"`
	Before               string                     `json:"before"`
	After                string                     `json:"after"`
	Reason               string                     `json:"reason"`
	EvidenceRefs         []InquiryStatusEvidenceRef `json:"evidence_refs"`
}

func inquiryStatusEventPayload(in UpdateInquiryStatusInput) inquiryStatusUpdatedPayload {
	return inquiryStatusUpdatedPayload{
		TransitionID: in.TransitionID, AttemptID: in.AttemptID, ExpectedStateVersion: in.ExpectedStateVersion,
		Target: in.Target, Before: in.Before, After: in.After, Reason: strings.TrimSpace(in.Reason),
		EvidenceRefs: canonicalInquiryStatusEvidence(in.EvidenceRefs),
	}
}
