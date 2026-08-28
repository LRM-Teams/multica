package problemevolution

import "time"

// Stop-path bounds (spec §13.1). They are fixed, measurable numbers rather
// than "best effort" so a stuck run has a deadline the platform can enforce
// without waiting for the daemon to cooperate.
const (
	// DaemonStopAcknowledgeWindow is how long the daemon has to stop producing
	// new candidates after a stop is requested.
	DaemonStopAcknowledgeWindow = 5 * time.Second
	// RunCancellationDeadline is how long a run may stay in `stopping` before
	// the server cancels it regardless of what the daemon reports.
	RunCancellationDeadline = 90 * time.Second
	// HeartbeatStaleAfter is the claim liveness window; a claimed run whose
	// heartbeat is older than this is treated as abandoned.
	HeartbeatStaleAfter = 2 * time.Minute
)

// StopReason values the platform sets. They are distinct from evolver failure
// classes because "we stopped it" and "it broke" need different reporting.
const (
	StopReasonUser              = "user_stopped"
	StopReasonBudgetExhausted   = "budget_exhausted"
	StopReasonNoImprovement     = "no_improvement"
	StopReasonTargetReached     = "target_reached"
	StopReasonCostCeiling       = "cost_ceiling"
	StopReasonHeartbeatLost     = "heartbeat_lost"
	StopReasonCancellationForce = "cancellation_deadline"
)

// StopConfig is the run-scoped stop policy.
type StopConfig struct {
	MaxGenerations      int     `json:"max_generations"`
	MaxCandidates       int     `json:"max_candidates"`
	MaxModelCalls       int     `json:"max_model_calls"`
	MaxCostUSD          float64 `json:"max_cost_usd"`
	TargetScore         float64 `json:"target_score"`
	NoImprovementRounds int     `json:"no_improvement_rounds"`
	EliteCount          int     `json:"elite_count"`
}

// DefaultStopConfig is the `standard` budget tier.
func DefaultStopConfig() StopConfig {
	return StopConfig{
		MaxGenerations: 4,
		MaxCandidates:  16,
		MaxModelCalls:  100,
		// Zero means unlimited. A user may still set a positive ceiling.
		MaxCostUSD:          0,
		TargetScore:         0.95,
		NoImprovementRounds: 2,
		EliteCount:          2,
	}
}

// WithDefaults fills unset fields, so a partially specified stop config from
// the API still has every bound the run is enforced against.
func (c StopConfig) WithDefaults() StopConfig {
	defaults := DefaultStopConfig()
	if c.MaxGenerations <= 0 {
		c.MaxGenerations = defaults.MaxGenerations
	}
	if c.MaxCandidates <= 0 {
		c.MaxCandidates = defaults.MaxCandidates
	}
	if c.MaxModelCalls <= 0 {
		c.MaxModelCalls = defaults.MaxModelCalls
	}
	// MaxCostUSD deliberately keeps zero as "unlimited"; it is not defaulted.
	if c.TargetScore <= 0 || c.TargetScore > 1 {
		c.TargetScore = defaults.TargetScore
	}
	if c.NoImprovementRounds <= 0 {
		c.NoImprovementRounds = defaults.NoImprovementRounds
	}
	if c.EliteCount <= 0 {
		c.EliteCount = defaults.EliteCount
	}
	return c
}

// RunProgress is what the stop decision reads.
type RunProgress struct {
	Generation        int
	CandidateCount    int
	ModelCalls        int
	CostUSD           float64
	BestScore         float64
	RoundsWithoutGain int
}

// ShouldStop reports whether the run has hit a stop condition, and why.
//
// Budget exhaustion and a user stop take the same `stopping` path on purpose:
// one code path means one set of guarantees about what happens to in-flight
// candidates.
func ShouldStop(config StopConfig, progress RunProgress) (bool, string) {
	if config.MaxCostUSD > 0 && progress.CostUSD >= config.MaxCostUSD {
		return true, StopReasonCostCeiling
	}
	if config.TargetScore > 0 && progress.BestScore >= config.TargetScore {
		return true, StopReasonTargetReached
	}
	if config.MaxCandidates > 0 && progress.CandidateCount >= config.MaxCandidates {
		return true, StopReasonBudgetExhausted
	}
	if config.MaxModelCalls > 0 && progress.ModelCalls >= config.MaxModelCalls {
		return true, StopReasonBudgetExhausted
	}
	if config.MaxGenerations > 0 && progress.Generation >= config.MaxGenerations {
		return true, StopReasonBudgetExhausted
	}
	if config.NoImprovementRounds > 0 && progress.RoundsWithoutGain >= config.NoImprovementRounds {
		return true, StopReasonNoImprovement
	}
	return false, ""
}

// CancellationOverdue reports whether a stopping run has outlived the
// cancellation deadline and must be cancelled by the server.
func CancellationOverdue(stopRequestedAt, now time.Time) bool {
	if stopRequestedAt.IsZero() {
		return false
	}
	return now.Sub(stopRequestedAt) >= RunCancellationDeadline
}

// ClaimAbandoned reports whether a claimed run's heartbeat has gone stale.
func ClaimAbandoned(lastHeartbeatAt, now time.Time) bool {
	if lastHeartbeatAt.IsZero() {
		return false
	}
	return now.Sub(lastHeartbeatAt) >= HeartbeatStaleAfter
}
