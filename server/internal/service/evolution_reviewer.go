package service

import "context"

type EvolutionReviewer interface {
	Review(ctx context.Context, input EvolutionReviewInput) (EvolutionReviewResult, error)
}

type EvolutionReviewInput struct {
	WorkspaceID    string
	SubmissionID   string
	UnitType       string
	Title          string
	Summary        string
	Content        string
	Sensitivity    string
	Confidence     string
	SuggestedScope string
	Tags           []string
	Tools          []string
	TaskTypes      []string
	ProjectTypes   []string
	Languages      []string
	Frameworks     []string
	Files          []EvolutionReviewFile
}

type EvolutionReviewFile struct {
	Path      string
	Content   string
	MimeType  string
	SizeBytes int64
}

type EvolutionReviewDecision string

const (
	EvolutionReviewPromote     EvolutionReviewDecision = "promote"
	EvolutionReviewNeedsReview EvolutionReviewDecision = "needs_review"
	EvolutionReviewReject      EvolutionReviewDecision = "reject"
)

type EvolutionReviewRiskLevel string

const (
	EvolutionReviewRiskLow    EvolutionReviewRiskLevel = "low"
	EvolutionReviewRiskMedium EvolutionReviewRiskLevel = "medium"
	EvolutionReviewRiskHigh   EvolutionReviewRiskLevel = "high"
)

type EvolutionReviewResult struct {
	Decision           EvolutionReviewDecision
	Confidence         float64
	RiskLevel          EvolutionReviewRiskLevel
	Title              string
	Summary            string
	SuggestedTags      []string
	SuggestedTaskTypes []string
	SuggestedScope     string
	Risks              []string
	Rationale          string
	Metadata           map[string]any
}

type NoopEvolutionReviewer struct{}

func (NoopEvolutionReviewer) Review(context.Context, EvolutionReviewInput) (EvolutionReviewResult, error) {
	return EvolutionReviewResult{
		Decision:   EvolutionReviewNeedsReview,
		Confidence: 0,
		RiskLevel:  EvolutionReviewRiskMedium,
		Rationale:  "LLM review disabled",
	}, nil
}
