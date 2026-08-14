package researchrun

import (
	"fmt"
	"sort"
)

type DisputeReviewPosition struct {
	PositionID    string
	AuthorAgentID string
	ClaimIDs      []string
	ScopeHash     string
}

type DisputeReviewInput struct {
	DisputeID         string
	SubjectArtifactID string
	Kind              string
	Positions         []DisputeReviewPosition
}

type DisputeReviewTask struct {
	TaskKey            string
	Purpose            string
	TargetPositionID   string
	RequiredCapability string
	VisibleArtifactIDs []string
	ExcludedAgentIDs   []string
}

// PlanDisputeReview creates assignment-neutral review requirements. Execution
// routing must satisfy ExcludedAgentIDs; it may not silently assign a position
// author when no independent Agent is available.
func PlanDisputeReview(input DisputeReviewInput) ([]DisputeReviewTask, error) {
	if input.DisputeID == "" || input.SubjectArtifactID == "" || len(input.Positions) < 2 {
		return nil, fmt.Errorf("%w: dispute review requires a dispute, subject, and at least two positions", ErrInvalidContract)
	}
	authors := make(map[string]struct{}, len(input.Positions))
	positionIDs := make(map[string]struct{}, len(input.Positions))
	for _, position := range input.Positions {
		if position.PositionID == "" || position.AuthorAgentID == "" || position.ScopeHash == "" || len(position.ClaimIDs) == 0 {
			return nil, fmt.Errorf("%w: review position is incomplete", ErrInvalidContract)
		}
		if _, exists := positionIDs[position.PositionID]; exists {
			return nil, fmt.Errorf("%w: duplicate review position %q", ErrInvalidContract, position.PositionID)
		}
		positionIDs[position.PositionID] = struct{}{}
		authors[position.AuthorAgentID] = struct{}{}
	}
	excludedAuthors := sortedStringSet(authors)
	tasks := make([]DisputeReviewTask, 0, len(input.Positions)+2)
	for _, position := range input.Positions {
		visible := append([]string{input.SubjectArtifactID}, position.ClaimIDs...)
		visible = uniqueSortedStrings(visible)
		tasks = append(tasks, DisputeReviewTask{
			TaskKey:            "dispute-review:" + input.DisputeID + ":" + position.PositionID,
			Purpose:            "independently_verify_position",
			TargetPositionID:   position.PositionID,
			RequiredCapability: "validator",
			VisibleArtifactIDs: visible,
			ExcludedAgentIDs:   append([]string(nil), excludedAuthors...),
		})
	}
	if input.Kind == "method" {
		tasks = append(tasks, DisputeReviewTask{
			TaskKey:            "dispute-method-review:" + input.DisputeID,
			Purpose:            "review_measurement_sample_bias_and_comparability",
			RequiredCapability: "methodologist",
			VisibleArtifactIDs: []string{input.SubjectArtifactID},
			ExcludedAgentIDs:   append([]string(nil), excludedAuthors...),
		})
	}
	allClaims := []string{input.SubjectArtifactID}
	for _, position := range input.Positions {
		allClaims = append(allClaims, position.ClaimIDs...)
	}
	tasks = append(tasks, DisputeReviewTask{
		TaskKey:            "dispute-distinguish:" + input.DisputeID,
		Purpose:            "collect_evidence_that_distinguishes_positions",
		RequiredCapability: "researcher",
		VisibleArtifactIDs: uniqueSortedStrings(allClaims),
		ExcludedAgentIDs:   append([]string(nil), excludedAuthors...),
	})
	return tasks, nil
}

type AcceptedDisputeEvidence struct {
	EvidenceID string
	Accepted   bool
}

type DisputeMethodReview struct {
	ArtifactID string
	Accepted   bool
}

type AdjudicatorContext struct {
	DisputeID               string
	SubjectArtifactID       string
	PositionIDs             []string
	AcceptedEvidenceIDs     []string
	AcceptedMethodReviewIDs []string
}

// BuildAdjudicatorContext is an allowlist projection. Hidden Agent scores,
// private reviewer rubrics, and rejected artifacts have no representable field.
func BuildAdjudicatorContext(input DisputeReviewInput, evidence []AcceptedDisputeEvidence, methodReviews []DisputeMethodReview) (AdjudicatorContext, error) {
	if _, err := PlanDisputeReview(input); err != nil {
		return AdjudicatorContext{}, err
	}
	context := AdjudicatorContext{DisputeID: input.DisputeID, SubjectArtifactID: input.SubjectArtifactID}
	for _, position := range input.Positions {
		context.PositionIDs = append(context.PositionIDs, position.PositionID)
	}
	seenEvidence := make(map[string]struct{})
	for _, item := range evidence {
		if item.EvidenceID == "" {
			return AdjudicatorContext{}, fmt.Errorf("%w: evidence id is required", ErrInvalidContract)
		}
		if item.Accepted {
			seenEvidence[item.EvidenceID] = struct{}{}
		}
	}
	seenReviews := make(map[string]struct{})
	for _, item := range methodReviews {
		if item.ArtifactID == "" {
			return AdjudicatorContext{}, fmt.Errorf("%w: method review artifact id is required", ErrInvalidContract)
		}
		if item.Accepted {
			seenReviews[item.ArtifactID] = struct{}{}
		}
	}
	context.PositionIDs = uniqueSortedStrings(context.PositionIDs)
	context.AcceptedEvidenceIDs = sortedStringSet(seenEvidence)
	context.AcceptedMethodReviewIDs = sortedStringSet(seenReviews)
	return context, nil
}

func sortedStringSet(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func uniqueSortedStrings(values []string) []string {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value != "" {
			set[value] = struct{}{}
		}
	}
	return sortedStringSet(set)
}
