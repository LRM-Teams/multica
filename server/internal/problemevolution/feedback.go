package problemevolution

import "math"

// FeedbackBuckets is the coarse ladder used when the policy limits bandwidth.
// Coarse buckets are the point: an exact score fed back every round is a
// gradient the evolver can climb toward the verifier instead of toward a
// correct answer.
var FeedbackBuckets = []struct {
	UpperBound float64
	Label      string
}{
	{0.25, "very_low"},
	{0.5, "low"},
	{0.75, "medium"},
	{0.9, "high"},
	{1.01, "very_high"},
}

// FeedbackProjection is everything the evolver is allowed to learn about an
// evaluation. Hidden answers, verifier internals and per-case detail never
// appear here.
type FeedbackProjection struct {
	SchemaVersion  int               `json:"schema_version"`
	Bandwidth      string            `json:"bandwidth"`
	Verdict        string            `json:"verdict"`
	HardGatePassed bool              `json:"hard_gate_passed"`
	TotalBucket    string            `json:"total_bucket,omitempty"`
	Total          *float64          `json:"total,omitempty"`
	Dimensions     map[string]string `json:"dimensions,omitempty"`
}

// BucketFor maps a unit-interval score onto a bucket label.
func BucketFor(value float64) string {
	clamped := math.Max(0, math.Min(1, value))
	for _, bucket := range FeedbackBuckets {
		if clamped < bucket.UpperBound {
			return bucket.Label
		}
	}
	return FeedbackBuckets[len(FeedbackBuckets)-1].Label
}

// ProjectFeedback reduces a full score to the default bucketed projection.
func ProjectFeedback(score Score) FeedbackProjection {
	return ProjectFeedbackWithPolicy(score, DefaultFeedbackPolicy())
}

// ProjectFeedbackWithPolicy reduces a full score to what the policy permits.
func ProjectFeedbackWithPolicy(score Score, policy FeedbackPolicy) FeedbackProjection {
	verdict := "pass"
	if !score.HardGatePassed {
		verdict = "fail"
	}
	projection := FeedbackProjection{
		SchemaVersion:  SchemaVersion,
		Bandwidth:      policy.Bandwidth,
		Verdict:        verdict,
		HardGatePassed: score.HardGatePassed,
	}
	if policy.Bandwidth == FeedbackBandwidthExact {
		total := math.Max(0, math.Min(1, score.Total))
		projection.Total = &total
	} else {
		projection.TotalBucket = BucketFor(score.Total)
	}
	if !policy.IncludeDimensionBreakdown {
		return projection
	}
	projection.Dimensions = make(map[string]string, len(score.Dimensions))
	for _, dimension := range score.Dimensions {
		projection.Dimensions[dimension.DimensionID] = BucketFor(dimension.Score)
	}
	return projection
}
