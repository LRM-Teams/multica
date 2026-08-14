package researcheval

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

const ProductionMonitorSchemaVersion = "research-production-monitor-v1"

type ProductionRunObservation struct {
	RunID           string    `json:"run_id"`
	StrategyVersion string    `json:"strategy_version"`
	ObservedAt      time.Time `json:"observed_at"`
	QualityScore    float64   `json:"quality_score"`
	QualityPassed   bool      `json:"quality_passed"`
	CostUnits       float64   `json:"cost_units"`
	BudgetUnits     float64   `json:"budget_units"`
}

type ProductionMonitorPolicy struct {
	MinimumSamples           int     `json:"minimum_samples"`
	MinimumMeanQualityScore  float64 `json:"minimum_mean_quality_score"`
	MinimumQualityPassRate   float64 `json:"minimum_quality_pass_rate"`
	MaximumP95CostUnits      float64 `json:"maximum_p95_cost_units"`
	MaximumBudgetOverrunRate float64 `json:"maximum_budget_overrun_rate"`
}

type ProductionMonitorMetrics struct {
	Samples           int     `json:"samples"`
	MeanQualityScore  float64 `json:"mean_quality_score"`
	QualityPassRate   float64 `json:"quality_pass_rate"`
	P95CostUnits      float64 `json:"p95_cost_units"`
	BudgetOverrunRate float64 `json:"budget_overrun_rate"`
}

type ProductionMonitorViolation struct {
	Code      string  `json:"code"`
	Observed  float64 `json:"observed"`
	Threshold float64 `json:"threshold"`
}

type ProductionMonitorReport struct {
	SchemaVersion   string                       `json:"schema_version"`
	StrategyVersion string                       `json:"strategy_version"`
	WindowStartedAt time.Time                    `json:"window_started_at"`
	WindowEndedAt   time.Time                    `json:"window_ended_at"`
	Metrics         ProductionMonitorMetrics     `json:"metrics"`
	Violations      []ProductionMonitorViolation `json:"violations,omitempty"`
	SufficientData  bool                         `json:"sufficient_data"`
	WithinBounds    bool                         `json:"within_bounds"`
}

func EvaluateProductionWindow(observations []ProductionRunObservation, policy ProductionMonitorPolicy) (ProductionMonitorReport, error) {
	if err := validateProductionMonitorPolicy(policy); err != nil {
		return ProductionMonitorReport{}, err
	}
	if len(observations) == 0 {
		return ProductionMonitorReport{}, fmt.Errorf("%w: production monitor window is empty", ErrInvalidEvaluation)
	}

	report := ProductionMonitorReport{SchemaVersion: ProductionMonitorSchemaVersion}
	costs := make([]float64, 0, len(observations))
	seenRuns := make(map[string]struct{}, len(observations))
	passed, overBudget := 0, 0
	for index, observation := range observations {
		observation.RunID = strings.TrimSpace(observation.RunID)
		observation.StrategyVersion = strings.TrimSpace(observation.StrategyVersion)
		if observation.RunID == "" || observation.StrategyVersion == "" || observation.ObservedAt.IsZero() {
			return ProductionMonitorReport{}, fmt.Errorf("%w: observation %d requires run, strategy, and time identity", ErrInvalidEvaluation, index)
		}
		if _, duplicate := seenRuns[observation.RunID]; duplicate {
			return ProductionMonitorReport{}, fmt.Errorf("%w: duplicate production run %q", ErrInvalidEvaluation, observation.RunID)
		}
		seenRuns[observation.RunID] = struct{}{}
		if report.StrategyVersion == "" {
			report.StrategyVersion = observation.StrategyVersion
		} else if report.StrategyVersion != observation.StrategyVersion {
			return ProductionMonitorReport{}, fmt.Errorf("%w: production window mixes strategy versions", ErrInvalidEvaluation)
		}
		if !finiteBetween(observation.QualityScore, 0, 1) || !finiteAtLeast(observation.CostUnits, 0) || !finiteGreaterThan(observation.BudgetUnits, 0) {
			return ProductionMonitorReport{}, fmt.Errorf("%w: observation %q has invalid quality or cost readings", ErrInvalidEvaluation, observation.RunID)
		}
		if index == 0 || observation.ObservedAt.Before(report.WindowStartedAt) {
			report.WindowStartedAt = observation.ObservedAt
		}
		if index == 0 || observation.ObservedAt.After(report.WindowEndedAt) {
			report.WindowEndedAt = observation.ObservedAt
		}
		report.Metrics.MeanQualityScore += observation.QualityScore
		costs = append(costs, observation.CostUnits)
		if observation.QualityPassed {
			passed++
		}
		if observation.CostUnits > observation.BudgetUnits {
			overBudget++
		}
	}

	report.Metrics.Samples = len(observations)
	report.Metrics.MeanQualityScore /= float64(len(observations))
	report.Metrics.QualityPassRate = float64(passed) / float64(len(observations))
	report.Metrics.BudgetOverrunRate = float64(overBudget) / float64(len(observations))
	sort.Float64s(costs)
	report.Metrics.P95CostUnits = costs[int(math.Ceil(0.95*float64(len(costs))))-1]
	report.SufficientData = len(observations) >= policy.MinimumSamples
	if !report.SufficientData {
		return report, nil
	}

	report.Violations = productionMonitorViolations(report.Metrics, policy)
	report.WithinBounds = len(report.Violations) == 0
	return report, nil
}

func validateProductionMonitorPolicy(policy ProductionMonitorPolicy) error {
	if policy.MinimumSamples <= 0 || !finiteGreaterThan(policy.MinimumMeanQualityScore, 0) || policy.MinimumMeanQualityScore > 1 ||
		!finiteGreaterThan(policy.MinimumQualityPassRate, 0) || policy.MinimumQualityPassRate > 1 ||
		!finiteGreaterThan(policy.MaximumP95CostUnits, 0) ||
		!finiteBetween(policy.MaximumBudgetOverrunRate, 0, 1) {
		return fmt.Errorf("%w: invalid production monitor policy", ErrInvalidEvaluation)
	}
	return nil
}

func productionMonitorViolations(metrics ProductionMonitorMetrics, policy ProductionMonitorPolicy) []ProductionMonitorViolation {
	violations := make([]ProductionMonitorViolation, 0, 4)
	if metrics.MeanQualityScore < policy.MinimumMeanQualityScore {
		violations = append(violations, ProductionMonitorViolation{Code: "mean_quality_below_floor", Observed: metrics.MeanQualityScore, Threshold: policy.MinimumMeanQualityScore})
	}
	if metrics.QualityPassRate < policy.MinimumQualityPassRate {
		violations = append(violations, ProductionMonitorViolation{Code: "quality_pass_rate_below_floor", Observed: metrics.QualityPassRate, Threshold: policy.MinimumQualityPassRate})
	}
	if metrics.P95CostUnits > policy.MaximumP95CostUnits {
		violations = append(violations, ProductionMonitorViolation{Code: "p95_cost_above_ceiling", Observed: metrics.P95CostUnits, Threshold: policy.MaximumP95CostUnits})
	}
	if metrics.BudgetOverrunRate > policy.MaximumBudgetOverrunRate {
		violations = append(violations, ProductionMonitorViolation{Code: "budget_overrun_rate_above_ceiling", Observed: metrics.BudgetOverrunRate, Threshold: policy.MaximumBudgetOverrunRate})
	}
	return violations
}

func finiteBetween(value, minimum, maximum float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= minimum && value <= maximum
}

func finiteAtLeast(value, minimum float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= minimum
}

func finiteGreaterThan(value, minimum float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value > minimum
}
