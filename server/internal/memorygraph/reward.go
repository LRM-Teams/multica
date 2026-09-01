// SPDX-License-Identifier: Apache-2.0

package memorygraph

// Scoring and reward (spec §7): continuous per-dimension scores in [0,1],
// the min-dimension overall, and the unclamped round-cost reward. The system
// defines no score thresholds, pass/fail labels, or fixed miss penalties.

// Overall returns the minimum of the three grading dimensions: no dimension
// compensates for another.
func (s DiveTrajectoryScore) Overall() float64 {
	m := s.Relevance
	if s.Groundedness < m {
		m = s.Groundedness
	}
	if s.Completeness < m {
		m = s.Completeness
	}
	return m
}

// ExploreReward computes reward = overall − w_round × server-counted explore
// rounds. The reward is never clamped; a low-quality/high-cost normal
// trajectory may receive a negative reward (A7).
func ExploreReward(overall, wRound float64, rounds int) float64 {
	return overall - wRound*float64(rounds)
}

// ExploreRewardPolicyVersion stamps every explore reward record (spec 14.2:
// dive model, prompt, policy, input manifest and raw dimensions are written
// to the reward ledger). The version freezes the formula bootstrap
// (overall − w_round × server_counted_rounds); production weights only move
// under a new policy version after shadow calibration (spec 14.4, A55).
const ExploreRewardPolicyVersion = "explore-reward-v1"

// ConsolidationRewardPolicyVersion stamps the consolidation reward bootstrap
// (spec 14.3: baseline delta + absolute costs, winner-independent).
const ConsolidationRewardPolicyVersion = "consolidation-reward-v1"

// DeterministicViolationReward computes the deterministic negative reward
// for a trajectory that terminated in its own violation state (budget /
// timeout / execution error, spec 14.2). It is zero overall quality minus the
// cost of the rounds it burned (at least one) minus a fixed violation
// penalty — strictly negative, strictly below any graded trajectory of the
// same rounds, and a pure function of its inputs (deterministic replay).
func DeterministicViolationReward(wRound float64, rounds int) float64 {
	if rounds < 1 {
		rounds = 1
	}
	return ExploreReward(0, wRound, rounds) - 1
}

// QueryPartition classifies one backtest/evaluation query into the stable,
// auditable evaluation partition (spec 14.4): evaluation queries feed reward
// computation, holdout queries stay hidden for weight calibration, safety
// queries feed the canary. The class depends only on the query's trace id —
// never on the surrounding batch — so partitions are stable across windows
// and replayable. Bootstrap split: 60/20/20 (fnv-1a hash mod 10).
func QueryPartition(traceID string) string {
	const fnvOffset32 = 2166136261
	const fnvPrime32 = 16777619
	h := uint32(fnvOffset32)
	for i := 0; i < len(traceID); i++ {
		h ^= uint32(traceID[i])
		h *= fnvPrime32
	}
	switch v := h % 10; {
	case v < 6:
		return "evaluation"
	case v < 8:
		return "holdout"
	default:
		return "safety"
	}
}

// RewardComponents are the raw reward inputs persisted with every immutable
// reward record (spec 14.2/14.3: raw dimensions and cost components are part
// of the ledger, never re-derived later). Shared by the explore and
// consolidation kinds; unused fields stay zero.
type RewardComponents struct {
	Source string `json:"source"` // graded|deterministic|unavailable|rejected|hard_gate_failure|consolidation

	// Explore (dive) dimensions.
	Relevance    float64 `json:"relevance,omitempty"`
	Groundedness float64 `json:"groundedness,omitempty"`
	Completeness float64 `json:"completeness,omitempty"`
	Overall      float64 `json:"overall,omitempty"`
	Rounds       float64 `json:"rounds,omitempty"`
	WRound       float64 `json:"w_round,omitempty"`
	Violation    string  `json:"violation,omitempty"` // budget|timeout|error

	// Consolidation deltas (spec 14.3).
	RecallDelta       float64 `json:"recall_delta,omitempty"`
	CoverageDelta     float64 `json:"coverage_delta,omitempty"`
	RegressionPenalty float64 `json:"regression_penalty,omitempty"`
	QualityDelta      float64 `json:"quality_delta,omitempty"`
	// Efficiency: baseline rounds saved minus ABSOLUTE per-candidate costs
	// (embedding cost in KiB, node churn, edge churn) — never batch-relative.
	BaselineRounds  float64 `json:"baseline_rounds,omitempty"`
	CandidateRounds float64 `json:"candidate_rounds,omitempty"`
	EfficiencyDelta float64 `json:"efficiency_delta,omitempty"`
	EmbedBytes      float64 `json:"embed_bytes,omitempty"`
	NodeChurn       float64 `json:"node_churn,omitempty"`
	EdgeChurn       float64 `json:"edge_churn,omitempty"`

	// Partition accounting: rewards are computed over evaluation queries
	// only; holdout/safety stay out (spec 14.4).
	EvaluationQueries int `json:"evaluation_queries,omitempty"`
	HoldoutQueries    int `json:"holdout_queries,omitempty"`
	SafetyQueries     int `json:"safety_queries,omitempty"`
}

// ConsolidationRewardInput carries one candidate's OWN absolute stats (never
// batch-relative values; Task 19 Step 4 forbids reusing SelectWinner's
// min-max-normalized cost as an RL reward).
type ConsolidationRewardInput struct {
	Passed           bool
	GateFailures     []string // recorded for audit; Passed=false is the switch
	Recall           float64  // fraction of evaluation queries with finite distance
	BaselineRecall   float64
	Coverage         float64 // fraction of backtest items satisfied
	BaselineCoverage float64
	Regressions      int // evaluation queries that regressed vs baseline
	Queries          int // evaluation queries contributing
	CandidateRounds  float64
	BaselineRounds   float64
	EmbedBytes       int // absolute embedding cost (body bytes of changed nodes)
	ChangedNodes     int // absolute node churn
	EdgeChurn        int // absolute edge churn

	// Stable partition accounting (spec §14.4): rewards read evaluation
	// queries only; holdout/safety counts ride along for audit.
	EvaluationQueries int
	HoldoutQueries    int
	SafetyQueries     int
}

// ConsolidationRewardWeights scale the two delta groups (spec 14.3). The
// bootstrap values are conservative; production weights move only under a
// new policy version after offline replay + holdout correlation calibration
// (spec 14.4, A55).
type ConsolidationRewardWeights struct {
	Quality    float64
	Efficiency float64
}

// DefaultConsolidationRewardWeights is the conservative bootstrap: quality
// deltas dominate; absolute efficiency costs apply a small brake so raw byte
// magnitudes cannot swamp the quality signal before calibration.
func DefaultConsolidationRewardWeights() ConsolidationRewardWeights {
	return ConsolidationRewardWeights{Quality: 1.0, Efficiency: 0.01}
}

// HardGateFailureReward is the deterministic negative reward for a candidate
// that failed the consolidation hard gates (spec 14.3: hard gate failure is
// deterministic negative — never a formula value computed from rejected
// stats).
const HardGateFailureReward = -1.0

// ConsolidationReward computes one candidate's reward from its own baseline
// deltas and ABSOLUTE costs (spec 14.3):
//
//	quality_delta    = recall_delta + coverage_delta − regression_penalty
//	efficiency_delta = baseline_rounds − candidate_rounds
//	                   − absolute_embedding_cost − node_churn − edge_churn
//	reward           = w_quality × quality_delta + w_efficiency × efficiency_delta
//
// Winner identity contributes nothing (Task 19: loser/winner semantics — the
// winner of the cost comparison can carry the lower reward when its quality
// deltas are worse). Gate failure short-circuits to HardGateFailureReward.
// The embedding cost uses documented KiB units so magnitudes stay comparable
// to rounds; it is still an absolute per-candidate quantity.
func ConsolidationReward(input ConsolidationRewardInput, w ConsolidationRewardWeights) (float64, RewardComponents) {
	if !input.Passed {
		return HardGateFailureReward, RewardComponents{Source: "hard_gate_failure"}
	}
	components := RewardComponents{
		Source:            "consolidation",
		RecallDelta:       input.Recall - input.BaselineRecall,
		CoverageDelta:     input.Coverage - input.BaselineCoverage,
		CandidateRounds:   input.CandidateRounds,
		BaselineRounds:    input.BaselineRounds,
		EmbedBytes:        float64(input.EmbedBytes),
		NodeChurn:         float64(input.ChangedNodes),
		EdgeChurn:         float64(input.EdgeChurn),
		EvaluationQueries: input.EvaluationQueries,
		HoldoutQueries:    input.HoldoutQueries,
		SafetyQueries:     input.SafetyQueries,
	}
	if input.Queries > 0 {
		components.RegressionPenalty = float64(input.Regressions) / float64(input.Queries)
	}
	embeddingKiB := float64(input.EmbedBytes) / 1024
	components.QualityDelta = components.RecallDelta + components.CoverageDelta - components.RegressionPenalty
	components.EfficiencyDelta = input.BaselineRounds - input.CandidateRounds - embeddingKiB -
		float64(input.ChangedNodes) - float64(input.EdgeChurn)
	value := w.Quality*components.QualityDelta + w.Efficiency*components.EfficiencyDelta
	return value, components
}
