package researchrun

import (
	"fmt"
	"regexp"
	"strings"
)

const (
	maxInquiryHypotheses = 256
	maxInquiryBranches   = 128
	maxInquiryInsights   = 64
	maxInquiryEdges      = 1024
)

var inquiryClientKeyPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,159}$`)

type inquiryHypothesisIntent struct {
	ClientKey            string
	QuestionKey          string
	Statement            string
	ExpectedObservations []string
	WeakeningConditions  []string
	ConfidenceLow        *float64
	ConfidenceHigh       *float64
}

type inquiryBranchIntent struct {
	ClientKey       string
	ParentBranchKey string
	Objective       string
	EntryConditions []string
	ExitConditions  []string
	BudgetShare     float64
}

type inquiryInsightIntent struct {
	ClientKey     string
	Title         string
	Summary       string
	Inputs        []inquiryKeyRef
	Relation      string
	SemanticValue string
}

type inquiryEdgeIntent struct {
	ClientKey string
	From      inquiryKeyRef
	To        inquiryKeyRef
	Relation  InquiryRelation
	Rationale string
}

type inquiryKeyRef struct {
	Kind InquiryEntityKind
	Key  string
}

// inquiryBatchIntent is the small interface consumed by the future Postgres
// write adapter. ExistingEndpoints are authoritative references already
// resolved in the same session; all other references must resolve within this
// batch. Validation is deliberately independent of persistence ordering.
type inquiryBatchIntent struct {
	ExistingEndpoints []inquiryKeyRef
	Hypotheses        []inquiryHypothesisIntent
	Branches          []inquiryBranchIntent
	Insights          []inquiryInsightIntent
	Edges             []inquiryEdgeIntent
}

func (inquiryModule) ValidateBatch(batch inquiryBatchIntent) error {
	if len(batch.Hypotheses) > maxInquiryHypotheses || len(batch.Branches) > maxInquiryBranches ||
		len(batch.Insights) > maxInquiryInsights || len(batch.Edges) > maxInquiryEdges {
		return fmt.Errorf("%w: inquiry batch exceeds contract limits", ErrInvalidContract)
	}
	known := make(map[inquiryKeyRef]struct{})
	for _, endpoint := range batch.ExistingEndpoints {
		if err := validateInquiryEndpointKey(endpoint); err != nil || endpoint.Kind == InquiryKindDispute {
			return fmt.Errorf("%w: invalid existing inquiry endpoint", ErrInvalidContract)
		}
		known[endpoint] = struct{}{}
	}
	add := func(kind InquiryEntityKind, key string) error {
		endpoint := inquiryKeyRef{Kind: kind, Key: key}
		if err := validateInquiryEndpointKey(endpoint); err != nil {
			return err
		}
		if _, exists := known[endpoint]; exists {
			return fmt.Errorf("%w: duplicate %s client key %q", ErrInvalidContract, kind, key)
		}
		known[endpoint] = struct{}{}
		return nil
	}
	for _, item := range batch.Hypotheses {
		if err := add(InquiryKindHypothesis, item.ClientKey); err != nil {
			return err
		}
	}
	for _, item := range batch.Branches {
		if err := add(InquiryKindBranch, item.ClientKey); err != nil {
			return err
		}
	}
	for _, item := range batch.Insights {
		if err := add(InquiryKindInsight, item.ClientKey); err != nil {
			return err
		}
	}
	for _, item := range batch.Edges {
		if err := add(InquiryEntityKind("inquiry_edge"), item.ClientKey); err != nil {
			return err
		}
	}

	for _, item := range batch.Hypotheses {
		if !validInquiryText(item.Statement, 32768) || len(item.ExpectedObservations) == 0 || len(item.ExpectedObservations) > 64 ||
			len(item.WeakeningConditions) == 0 || len(item.WeakeningConditions) > 64 ||
			!validInquiryTextList(item.ExpectedObservations) || !validInquiryTextList(item.WeakeningConditions) ||
			!validConfidenceRange(item.ConfidenceLow, item.ConfidenceHigh) {
			return fmt.Errorf("%w: invalid hypothesis %q", ErrInvalidContract, item.ClientKey)
		}
		if _, ok := known[inquiryKeyRef{Kind: InquiryKindQuestion, Key: item.QuestionKey}]; !ok {
			return fmt.Errorf("%w: hypothesis %q references unknown question %q", ErrInvalidContract, item.ClientKey, item.QuestionKey)
		}
	}
	for _, item := range batch.Branches {
		if !validInquiryText(item.Objective, 32768) || len(item.EntryConditions) == 0 || len(item.EntryConditions) > 64 ||
			len(item.ExitConditions) == 0 || len(item.ExitConditions) > 64 || !validInquiryTextList(item.EntryConditions) ||
			!validInquiryTextList(item.ExitConditions) || item.BudgetShare < 0 || item.BudgetShare > 1 {
			return fmt.Errorf("%w: invalid branch %q", ErrInvalidContract, item.ClientKey)
		}
		if item.ParentBranchKey != "" {
			if _, ok := known[inquiryKeyRef{Kind: InquiryKindBranch, Key: item.ParentBranchKey}]; !ok || item.ParentBranchKey == item.ClientKey {
				return fmt.Errorf("%w: branch %q references invalid parent %q", ErrInvalidContract, item.ClientKey, item.ParentBranchKey)
			}
		}
	}
	for _, item := range batch.Insights {
		if !validInquiryText(item.Title, 4096) || !validInquiryText(item.Summary, 32768) || len(item.Inputs) < 2 || len(item.Inputs) > 128 ||
			!validInquiryInsightRelation(item.Relation) || !validInquirySemanticValue(item.SemanticValue) {
			return fmt.Errorf("%w: invalid insight %q", ErrInvalidContract, item.ClientKey)
		}
		seen := map[inquiryKeyRef]struct{}{}
		for _, endpoint := range item.Inputs {
			if _, ok := known[endpoint]; !ok {
				return fmt.Errorf("%w: insight %q references unknown input", ErrInvalidContract, item.ClientKey)
			}
			if _, duplicate := seen[endpoint]; duplicate {
				return fmt.Errorf("%w: insight %q repeats an input", ErrInvalidContract, item.ClientKey)
			}
			seen[endpoint] = struct{}{}
		}
	}
	for _, item := range batch.Edges {
		if err := (inquiryModule{}).ValidateEdge(inquiryEdgeCommand{
			From:     inquiryEndpoint{Kind: item.From.Kind, ID: item.From.Key},
			To:       inquiryEndpoint{Kind: item.To.Kind, ID: item.To.Key},
			Relation: item.Relation, Rationale: item.Rationale,
		}); err != nil {
			return err
		}
		if _, ok := known[item.From]; !ok {
			return fmt.Errorf("%w: edge %q references unknown from endpoint", ErrInvalidContract, item.ClientKey)
		}
		if _, ok := known[item.To]; !ok {
			return fmt.Errorf("%w: edge %q references unknown to endpoint", ErrInvalidContract, item.ClientKey)
		}
	}
	return nil
}

func validateInquiryEndpointKey(endpoint inquiryKeyRef) error {
	if endpoint.Kind != InquiryKindQuestion && endpoint.Kind != InquiryKindHypothesis && endpoint.Kind != InquiryKindBranch &&
		endpoint.Kind != InquiryKindClaim && endpoint.Kind != InquiryKindInsight && endpoint.Kind != InquiryKindDispute &&
		endpoint.Kind != InquiryEntityKind("inquiry_edge") {
		return fmt.Errorf("%w: unknown inquiry endpoint kind %q", ErrInvalidContract, endpoint.Kind)
	}
	if !inquiryClientKeyPattern.MatchString(endpoint.Key) {
		return fmt.Errorf("%w: invalid %s client key", ErrInvalidContract, endpoint.Kind)
	}
	return nil
}

func validInquiryText(value string, limit int) bool {
	return strings.TrimSpace(value) != "" && len(value) <= limit
}

func validInquiryTextList(values []string) bool {
	for _, value := range values {
		if !validInquiryText(value, 32768) {
			return false
		}
	}
	return true
}

func validConfidenceRange(low, high *float64) bool {
	if low != nil && (*low < 0 || *low > 1) || high != nil && (*high < 0 || *high > 1) {
		return false
	}
	return low == nil || high == nil || *low <= *high
}

func validInquiryInsightRelation(value string) bool {
	switch value {
	case "integrates", "explains", "conditions", "resolves", "distinguishes":
		return true
	default:
		return false
	}
}

func validInquirySemanticValue(value string) bool {
	switch value {
	case "new_explanation", "deduplication", "conflict_resolution", "hypothesis_change", "frontier_change", "report_change", "lossless_compression":
		return true
	default:
		return false
	}
}
