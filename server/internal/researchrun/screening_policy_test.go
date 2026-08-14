package researchrun

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

func screeningPolicyFixture() ScreeningPolicy {
	return ScreeningPolicy{Version: ScreeningPolicyVersionV1, Criteria: []ScreeningCriterion{
		{ID: "primary-relevance", Kind: "inclusion", Text: "Directly addresses the research question"},
		{ID: "verifiable", Kind: "inclusion", Text: "Contains facts that can be independently checked"},
		{ID: "out-of-scope", Kind: "exclusion", Text: "Falls outside the frozen research scope"},
		{ID: "unverifiable", Kind: "exclusion", Text: "Provides no inspectable evidence"},
	}}
}

func screeningAssessmentFixture() ScreeningAssessment {
	return ScreeningAssessment{
		CandidateID: "candidate-1", Disposition: "accepted",
		MatchedCriterionIDs: []string{"verifiable", "primary-relevance"},
		Reason:              "The primary record directly answers the scoped question.",
		ReviewerKind:        "agent", ReviewerID: "agent-1",
		ReviewedAt: time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC),
		Facts: []ScreeningFact{
			{Kind: "metadata", Locator: "publisher", Value: "Official registry"},
			{Kind: "snippet", Locator: "paragraph:4", Value: "The inspected finding"},
		},
	}
}

func TestValidateScreeningDecisionNormalizesAuditFingerprint(t *testing.T) {
	policy := screeningPolicyFixture()
	assessment := screeningAssessmentFixture()
	first, err := ValidateScreeningDecision(policy, assessment)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Fingerprint) != 71 || first.Assessment.MatchedCriterionIDs[0] != "primary-relevance" {
		t.Fatalf("decision=%+v", first)
	}
	shuffledPolicy := policy
	shuffledPolicy.Criteria = []ScreeningCriterion{policy.Criteria[3], policy.Criteria[1], policy.Criteria[0], policy.Criteria[2]}
	shuffled := assessment
	shuffled.ReviewedAt = assessment.ReviewedAt.In(time.FixedZone("reviewer-local", 8*60*60))
	shuffled.MatchedCriterionIDs = []string{"primary-relevance", "verifiable"}
	shuffled.Facts = []ScreeningFact{assessment.Facts[1], assessment.Facts[0]}
	second, err := ValidateScreeningDecision(shuffledPolicy, shuffled)
	if err != nil {
		t.Fatal(err)
	}
	if first.Fingerprint != second.Fingerprint || !reflect.DeepEqual(first.Assessment, second.Assessment) {
		t.Fatalf("unstable decisions\nfirst=%+v\nsecond=%+v", first, second)
	}
}

func TestValidateScreeningDecisionAcceptsExcludedAndDuplicate(t *testing.T) {
	policy := screeningPolicyFixture()
	excluded := screeningAssessmentFixture()
	excluded.CandidateID, excluded.Disposition = "candidate-excluded", "excluded"
	excluded.MatchedCriterionIDs = []string{"primary-relevance", "unverifiable"}
	excluded.Reason = "The page is relevant but exposes no inspectable evidence."
	if _, err := ValidateScreeningDecision(policy, excluded); err != nil {
		t.Fatalf("excluded: %v", err)
	}
	duplicate := screeningAssessmentFixture()
	duplicate.CandidateID, duplicate.Disposition = "candidate-mirror", "duplicate"
	duplicate.MatchedCriterionIDs = nil
	duplicate.CanonicalCandidateID = "candidate-primary"
	duplicate.Reason = "The content hash matches the retained canonical candidate."
	duplicate.Facts = []ScreeningFact{{Kind: "content_hash", Value: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}
	if _, err := ValidateScreeningDecision(policy, duplicate); err != nil {
		t.Fatalf("duplicate: %v", err)
	}
}

func TestValidateScreeningDecisionRejectsUnauditableMatrices(t *testing.T) {
	for name, mutate := range map[string]func(*ScreeningPolicy, *ScreeningAssessment){
		"unknown policy":          func(policy *ScreeningPolicy, _ *ScreeningAssessment) { policy.Version = "future" },
		"policy lacks exclusions": func(policy *ScreeningPolicy, _ *ScreeningAssessment) { policy.Criteria = policy.Criteria[:2] },
		"unknown criterion": func(_ *ScreeningPolicy, assessment *ScreeningAssessment) {
			assessment.MatchedCriterionIDs = []string{"missing"}
		},
		"accepted exclusion": func(_ *ScreeningPolicy, assessment *ScreeningAssessment) {
			assessment.MatchedCriterionIDs = []string{"out-of-scope"}
		},
		"excluded without exclusion": func(_ *ScreeningPolicy, assessment *ScreeningAssessment) { assessment.Disposition = "excluded" },
		"duplicate without canonical": func(_ *ScreeningPolicy, assessment *ScreeningAssessment) {
			assessment.Disposition, assessment.MatchedCriterionIDs = "duplicate", nil
		},
		"duplicate without identity fact": func(_ *ScreeningPolicy, assessment *ScreeningAssessment) {
			assessment.Disposition, assessment.MatchedCriterionIDs, assessment.CanonicalCandidateID = "duplicate", nil, "canonical"
		},
		"weak reason": func(_ *ScreeningPolicy, assessment *ScreeningAssessment) { assessment.Reason = "because" },
		"no facts":    func(_ *ScreeningPolicy, assessment *ScreeningAssessment) { assessment.Facts = nil },
		"duplicate fact": func(_ *ScreeningPolicy, assessment *ScreeningAssessment) {
			assessment.Facts = append(assessment.Facts, assessment.Facts[0])
		},
		"invalid content hash": func(_ *ScreeningPolicy, assessment *ScreeningAssessment) {
			assessment.Facts = []ScreeningFact{{Kind: "content_hash", Value: "sha256:not-a-digest"}}
		},
		"noncanonical content hash": func(_ *ScreeningPolicy, assessment *ScreeningAssessment) {
			assessment.Facts = []ScreeningFact{{Kind: "content_hash", Value: "sha256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"}}
		},
		"noncanonical url": func(_ *ScreeningPolicy, assessment *ScreeningAssessment) {
			assessment.Facts = []ScreeningFact{{Kind: "canonical_url", Value: "HTTPS://Example.COM:443/source#fragment"}}
		},
		"credential url": func(_ *ScreeningPolicy, assessment *ScreeningAssessment) {
			assessment.Facts = []ScreeningFact{{Kind: "canonical_url", Value: "https://user:secret@example.com/source"}}
		},
		"snippet without locator": func(_ *ScreeningPolicy, assessment *ScreeningAssessment) {
			assessment.Facts = []ScreeningFact{{Kind: "snippet", Value: "The inspected finding"}}
		},
		"unknown reviewer": func(_ *ScreeningPolicy, assessment *ScreeningAssessment) { assessment.ReviewerKind = "model" },
	} {
		t.Run(name, func(t *testing.T) {
			policy, assessment := screeningPolicyFixture(), screeningAssessmentFixture()
			mutate(&policy, &assessment)
			if _, err := ValidateScreeningDecision(policy, assessment); !errors.Is(err, ErrInvalidContract) {
				t.Fatalf("err=%v want ErrInvalidContract", err)
			}
		})
	}
}
