package researchrun

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"
	"strings"
)

const IntegrationConvergencePolicyVersionV1 = "research-integration-convergence-v1"

type IntegrationConvergencePolicy struct {
	Version                 string
	MaximumRounds           int
	MaximumInsightDepth     int
	MaximumCostUnits        int64
	MinimumMarginalGain     float64
	LowGainRoundsToConverge int
}

type IntegrationConvergenceSnapshot struct {
	WorkspaceID                string
	SessionID                  string
	GoalVersion                int64
	PlanVersion                int64
	ThroughEventSequence       int64
	CanonicalStateHash         string
	CompletedRounds            int
	CurrentMaximumInsightDepth int
	ConsumedCostUnits          int64
	UnassimilatedResultCount   int
	PendingDerivationCount     int
	StaleInsightCount          int
	BlockingDisputeCount       int
	OpenRequiredQuestionCount  int
	FrontierStable             bool
	RecentMarginalGains        []float64
}

type IntegrationConvergenceAction string

const (
	IntegrationConvergenceContinue      IntegrationConvergenceAction = "continue"
	IntegrationConvergenceAwaitUpstream IntegrationConvergenceAction = "await_upstream"
	IntegrationConvergenceConverged     IntegrationConvergenceAction = "converged"
	IntegrationConvergenceEscalate      IntegrationConvergenceAction = "escalate"
)

type IntegrationConvergenceReason string

const (
	IntegrationConvergenceBlockingDispute      IntegrationConvergenceReason = "blocking_dispute"
	IntegrationConvergenceRoundLimit           IntegrationConvergenceReason = "round_limit"
	IntegrationConvergenceDepthLimit           IntegrationConvergenceReason = "insight_depth_limit"
	IntegrationConvergenceCostLimit            IntegrationConvergenceReason = "cost_limit"
	IntegrationConvergenceUnassimilatedResults IntegrationConvergenceReason = "unassimilated_results"
	IntegrationConvergencePendingDerivations   IntegrationConvergenceReason = "pending_derivations"
	IntegrationConvergenceStaleInsights        IntegrationConvergenceReason = "stale_insights"
	IntegrationConvergenceFrontierChanging     IntegrationConvergenceReason = "frontier_changing"
	IntegrationConvergenceRequiredQuestions    IntegrationConvergenceReason = "open_required_questions"
	IntegrationConvergenceObservationWindow    IntegrationConvergenceReason = "observation_window_incomplete"
	IntegrationConvergenceLowMarginalGain      IntegrationConvergenceReason = "low_marginal_gain"
)

type IntegrationConvergenceDecision struct {
	Action      IntegrationConvergenceAction
	Reason      IntegrationConvergenceReason
	Fingerprint string
}

// DecideIntegrationConvergence evaluates one canonical graph watermark. Only a
// stable frontier with a complete low-gain window may converge. Disputes and
// required evidence leave Integration for their owning subsystem, while hard
// limits escalate instead of manufacturing a successful terminal state.
func DecideIntegrationConvergence(policy IntegrationConvergencePolicy, snapshot IntegrationConvergenceSnapshot) (IntegrationConvergenceDecision, error) {
	if err := validateIntegrationConvergence(policy, snapshot); err != nil {
		return IntegrationConvergenceDecision{}, err
	}
	decision := IntegrationConvergenceDecision{}
	integrationWork := snapshot.UnassimilatedResultCount > 0 || snapshot.PendingDerivationCount > 0 || snapshot.StaleInsightCount > 0 || !snapshot.FrontierStable
	depthWork := snapshot.PendingDerivationCount > 0 || snapshot.StaleInsightCount > 0 || !snapshot.FrontierStable
	switch {
	case snapshot.CompletedRounds >= policy.MaximumRounds && integrationWork:
		decision.Action, decision.Reason = IntegrationConvergenceEscalate, IntegrationConvergenceRoundLimit
	case snapshot.CurrentMaximumInsightDepth >= policy.MaximumInsightDepth && depthWork:
		decision.Action, decision.Reason = IntegrationConvergenceEscalate, IntegrationConvergenceDepthLimit
	case snapshot.ConsumedCostUnits >= policy.MaximumCostUnits && integrationWork:
		decision.Action, decision.Reason = IntegrationConvergenceEscalate, IntegrationConvergenceCostLimit
	case snapshot.UnassimilatedResultCount > 0:
		decision.Action, decision.Reason = IntegrationConvergenceContinue, IntegrationConvergenceUnassimilatedResults
	case snapshot.PendingDerivationCount > 0:
		decision.Action, decision.Reason = IntegrationConvergenceContinue, IntegrationConvergencePendingDerivations
	case snapshot.StaleInsightCount > 0:
		decision.Action, decision.Reason = IntegrationConvergenceContinue, IntegrationConvergenceStaleInsights
	case !snapshot.FrontierStable:
		decision.Action, decision.Reason = IntegrationConvergenceContinue, IntegrationConvergenceFrontierChanging
	case snapshot.BlockingDisputeCount > 0:
		decision.Action, decision.Reason = IntegrationConvergenceAwaitUpstream, IntegrationConvergenceBlockingDispute
	case snapshot.OpenRequiredQuestionCount > 0:
		decision.Action, decision.Reason = IntegrationConvergenceAwaitUpstream, IntegrationConvergenceRequiredQuestions
	case !hasLowGainConvergenceWindow(policy, snapshot.RecentMarginalGains):
		decision.Action, decision.Reason = IntegrationConvergenceAwaitUpstream, IntegrationConvergenceObservationWindow
	default:
		decision.Action, decision.Reason = IntegrationConvergenceConverged, IntegrationConvergenceLowMarginalGain
	}
	fingerprintInput := struct {
		Policy   IntegrationConvergencePolicy
		Snapshot IntegrationConvergenceSnapshot
		Action   IntegrationConvergenceAction
		Reason   IntegrationConvergenceReason
	}{Policy: policy, Snapshot: normalizedIntegrationConvergenceSnapshot(snapshot), Action: decision.Action, Reason: decision.Reason}
	encoded, err := json.Marshal(fingerprintInput)
	if err != nil {
		return IntegrationConvergenceDecision{}, err
	}
	digest := sha256.Sum256(encoded)
	decision.Fingerprint = fmt.Sprintf("sha256:%x", digest)
	return decision, nil
}

func validateIntegrationConvergence(policy IntegrationConvergencePolicy, snapshot IntegrationConvergenceSnapshot) error {
	if policy.Version != IntegrationConvergencePolicyVersionV1 ||
		policy.MaximumRounds < 1 || policy.MaximumRounds > 10_000 ||
		policy.MaximumInsightDepth < 1 || policy.MaximumInsightDepth > 1_000 ||
		policy.MaximumCostUnits < 1 ||
		math.IsNaN(policy.MinimumMarginalGain) || math.IsInf(policy.MinimumMarginalGain, 0) ||
		policy.MinimumMarginalGain < 0 || policy.MinimumMarginalGain > 1 ||
		policy.LowGainRoundsToConverge < 1 || policy.LowGainRoundsToConverge > 100 {
		return fmt.Errorf("%w: Integration Convergence Policy is invalid", ErrInvalidContract)
	}
	if !validConvergenceIdentity(snapshot.WorkspaceID) || !validConvergenceIdentity(snapshot.SessionID) ||
		snapshot.GoalVersion < 1 || snapshot.PlanVersion < 1 || snapshot.ThroughEventSequence < 1 ||
		!validConvergenceHash(snapshot.CanonicalStateHash) || snapshot.CompletedRounds < 0 ||
		snapshot.CurrentMaximumInsightDepth < 0 || snapshot.ConsumedCostUnits < 0 ||
		snapshot.UnassimilatedResultCount < 0 || snapshot.PendingDerivationCount < 0 ||
		snapshot.StaleInsightCount < 0 || snapshot.BlockingDisputeCount < 0 ||
		snapshot.OpenRequiredQuestionCount < 0 || len(snapshot.RecentMarginalGains) > 100 {
		return fmt.Errorf("%w: Integration Convergence snapshot is invalid", ErrInvalidContract)
	}
	if len(snapshot.RecentMarginalGains) > snapshot.CompletedRounds {
		return fmt.Errorf("%w: Integration marginal-gain window exceeds completed rounds", ErrInvalidContract)
	}
	for _, gain := range snapshot.RecentMarginalGains {
		if math.IsNaN(gain) || math.IsInf(gain, 0) || gain < 0 || gain > 1 {
			return fmt.Errorf("%w: Integration marginal gain is invalid", ErrInvalidContract)
		}
	}
	return nil
}

func hasLowGainConvergenceWindow(policy IntegrationConvergencePolicy, gains []float64) bool {
	if len(gains) < policy.LowGainRoundsToConverge {
		return false
	}
	for _, gain := range gains[len(gains)-policy.LowGainRoundsToConverge:] {
		if gain >= policy.MinimumMarginalGain {
			return false
		}
	}
	return true
}

func normalizedIntegrationConvergenceSnapshot(snapshot IntegrationConvergenceSnapshot) IntegrationConvergenceSnapshot {
	normalized := snapshot
	normalized.RecentMarginalGains = append([]float64(nil), snapshot.RecentMarginalGains...)
	if normalized.RecentMarginalGains == nil {
		normalized.RecentMarginalGains = []float64{}
	}
	return normalized
}

func validConvergenceIdentity(value string) bool {
	return value != "" && len(value) <= 512 && strings.TrimSpace(value) == value
}

func validConvergenceHash(value string) bool {
	if len(value) != len("sha256:")+sha256.Size*2 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	for _, r := range strings.TrimPrefix(value, "sha256:") {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}
