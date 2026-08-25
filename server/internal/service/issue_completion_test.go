package service

import (
	"errors"
	"testing"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestValidateCompletionPayloadWebGameEvidence(t *testing.T) {
	issue := db.Issue{AcceptanceCriteria: []byte(`[
		"旧世界与新世界地形连续且树木完整",
		"默认画质六面语义和缓存稳定",
		"默认生存 HUD 与原版一致",
		"CI、Pages 和 man A-F 视觉验收全部通过"
	]`)}
	input := SubmitIssueCompletionInput{
		Summary: "恢复旧版视觉基线，并以真实线上截图独立验收。",
		AcceptanceResults: []CompletionAcceptanceResult{
			{CriterionIndex: 0, Criterion: "旧世界与新世界地形连续且树木完整", Satisfied: true, EvidenceRefs: []CompletionEvidenceRef{{Kind: "screenshot", Ref: "artifact://old-new-world-parity"}}},
			{CriterionIndex: 1, Criterion: "默认画质六面语义和缓存稳定", Satisfied: true, EvidenceRefs: []CompletionEvidenceRef{{Kind: "test", Ref: "playwright:visual-baseline"}}},
			{CriterionIndex: 2, Criterion: "默认生存 HUD 与原版一致", Satisfied: true, EvidenceRefs: []CompletionEvidenceRef{{Kind: "commit", Ref: "c47b615"}}},
			{CriterionIndex: 3, Criterion: "CI、Pages 和 man A-F 视觉验收全部通过", Satisfied: true, EvidenceRefs: []CompletionEvidenceRef{{Kind: "pull_request", Ref: "https://github.com/example/game/pull/65"}, {Kind: "deployment", Ref: "https://example.github.io/game"}}},
		},
		ArtifactRefs: []CompletionEvidenceRef{{Kind: "pull_request", Ref: "https://github.com/example/game/pull/65"}},
		Risks:        []string{"canonical PR link must be connected before review acceptance"},
	}
	if err := validateCompletionPayload(issue, &input); err != nil {
		t.Fatalf("real web-game completion rejected: %v", err)
	}
}

func TestValidateCompletionPayloadRejectsMissingAndStaleEvidence(t *testing.T) {
	issue := db.Issue{AcceptanceCriteria: []byte(`["CI green","visual gate passed"]`)}
	tests := []struct {
		name  string
		input SubmitIssueCompletionInput
		want  error
	}{
		{
			name: "missing criterion evidence",
			input: SubmitIssueCompletionInput{Summary: "done", AcceptanceResults: []CompletionAcceptanceResult{
				{CriterionIndex: 0, Criterion: "CI green", Satisfied: true, EvidenceRefs: []CompletionEvidenceRef{{Kind: "test", Ref: "ci:green"}}},
				{CriterionIndex: 1, Criterion: "visual gate passed", Satisfied: true},
			}},
			want: ErrIssueCompletionValidation,
		},
		{
			name: "criterion changed since run",
			input: SubmitIssueCompletionInput{Summary: "done", AcceptanceResults: []CompletionAcceptanceResult{
				{CriterionIndex: 0, Criterion: "old CI wording", Satisfied: true, EvidenceRefs: []CompletionEvidenceRef{{Kind: "test", Ref: "ci:green"}}},
				{CriterionIndex: 1, Criterion: "visual gate passed", Satisfied: true, EvidenceRefs: []CompletionEvidenceRef{{Kind: "screenshot", Ref: "artifact://gate"}}},
			}},
			want: ErrIssueCompletionConflict,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateCompletionPayload(issue, &tc.input)
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestValidateReviewResultsRequiresIndependentCriterionVerdicts(t *testing.T) {
	criteria := []string{"CI green", "visual gate passed"}
	if err := validateReviewResults(criteria, "accepted", []CompletionReviewResult{
		{CriterionIndex: 0, Accepted: true}, {CriterionIndex: 1, Accepted: true},
	}); err != nil {
		t.Fatalf("accepted review rejected: %v", err)
	}
	if err := validateReviewResults(criteria, "rejected", []CompletionReviewResult{
		{CriterionIndex: 0, Accepted: true}, {CriterionIndex: 1, Accepted: false, Reason: "online screenshot is missing"},
	}); err != nil {
		t.Fatalf("rejected review rejected: %v", err)
	}
	if err := validateReviewResults(criteria, "accepted", []CompletionReviewResult{
		{CriterionIndex: 0, Accepted: true}, {CriterionIndex: 1, Accepted: false, Reason: "missing"},
	}); !errors.Is(err, ErrIssueCompletionValidation) {
		t.Fatalf("mixed accepted review error = %v", err)
	}
}
