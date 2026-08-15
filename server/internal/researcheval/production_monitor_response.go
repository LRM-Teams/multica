package researcheval

import (
	"fmt"
	"strings"
)

const ProductionMonitorDecisionSchemaVersion = "research-production-monitor-decision-v1"

type ProductionMonitorAction string

const (
	ProductionMonitorCollectMoreSamples ProductionMonitorAction = "collect_more_samples"
	ProductionMonitorRetainStrategy     ProductionMonitorAction = "retain_strategy"
	ProductionMonitorRollbackRequired   ProductionMonitorAction = "rollback_required"
	ProductionMonitorFreezeAndEscalate  ProductionMonitorAction = "freeze_and_escalate"
)

type ProductionMonitorDecisionInput struct {
	Report                  ProductionMonitorReport `json:"report"`
	CurrentStrategyVersion  string                  `json:"current_strategy_version"`
	PreviousStrategyVersion string                  `json:"previous_strategy_version,omitempty"`
}

type ProductionMonitorDecision struct {
	SchemaVersion           string                       `json:"schema_version"`
	StrategyVersion         string                       `json:"strategy_version"`
	PreviousStrategyVersion string                       `json:"previous_strategy_version,omitempty"`
	Action                  ProductionMonitorAction      `json:"action"`
	Reason                  string                       `json:"reason"`
	Violations              []ProductionMonitorViolation `json:"violations,omitempty"`
}

// DecideProductionMonitorResponse converts one validated observation window
// into an explicit control-plane obligation. It deliberately does not perform
// rollback or alert delivery; those effects require a durable decision store.
func DecideProductionMonitorResponse(input ProductionMonitorDecisionInput) (ProductionMonitorDecision, error) {
	input.CurrentStrategyVersion = strings.TrimSpace(input.CurrentStrategyVersion)
	input.PreviousStrategyVersion = strings.TrimSpace(input.PreviousStrategyVersion)
	if input.CurrentStrategyVersion == "" || input.Report.StrategyVersion != input.CurrentStrategyVersion {
		return ProductionMonitorDecision{}, fmt.Errorf("%w: monitor report is not bound to the current strategy", ErrInvalidEvaluation)
	}
	if err := validateProductionMonitorReport(input.Report); err != nil {
		return ProductionMonitorDecision{}, err
	}

	decision := ProductionMonitorDecision{
		SchemaVersion:           ProductionMonitorDecisionSchemaVersion,
		StrategyVersion:         input.CurrentStrategyVersion,
		PreviousStrategyVersion: input.PreviousStrategyVersion,
		Violations:              append([]ProductionMonitorViolation(nil), input.Report.Violations...),
	}
	if !input.Report.SufficientData {
		decision.Action = ProductionMonitorCollectMoreSamples
		decision.Reason = "minimum_sample_window_not_reached"
		return decision, nil
	}
	if input.Report.WithinBounds {
		decision.Action = ProductionMonitorRetainStrategy
		decision.Reason = "production_window_within_bounds"
		return decision, nil
	}
	if input.PreviousStrategyVersion == "" || input.PreviousStrategyVersion == input.CurrentStrategyVersion {
		decision.PreviousStrategyVersion = ""
		decision.Action = ProductionMonitorFreezeAndEscalate
		decision.Reason = "production_boundary_breached_without_rollback_version"
		return decision, nil
	}
	decision.Action = ProductionMonitorRollbackRequired
	decision.Reason = "production_boundary_breached"
	return decision, nil
}

func validateProductionMonitorReport(report ProductionMonitorReport) error {
	if report.SchemaVersion != ProductionMonitorSchemaVersion || strings.TrimSpace(report.StrategyVersion) == "" ||
		report.WindowStartedAt.IsZero() || report.WindowEndedAt.Before(report.WindowStartedAt) ||
		report.Metrics.Samples <= 0 || !finiteBetween(report.Metrics.MeanQualityScore, 0, 1) ||
		!finiteBetween(report.Metrics.QualityPassRate, 0, 1) || !finiteAtLeast(report.Metrics.P95CostUnits, 0) ||
		!finiteBetween(report.Metrics.BudgetOverrunRate, 0, 1) {
		return fmt.Errorf("%w: malformed production monitor report", ErrInvalidEvaluation)
	}
	if !report.SufficientData {
		if report.WithinBounds || len(report.Violations) != 0 {
			return fmt.Errorf("%w: insufficient monitor report contains a verdict", ErrInvalidEvaluation)
		}
		return nil
	}
	if report.WithinBounds != (len(report.Violations) == 0) {
		return fmt.Errorf("%w: monitor verdict contradicts violations", ErrInvalidEvaluation)
	}
	seen := make(map[string]struct{}, len(report.Violations))
	for _, violation := range report.Violations {
		if _, duplicate := seen[violation.Code]; duplicate || !validProductionMonitorViolation(violation) {
			return fmt.Errorf("%w: invalid or duplicate monitor violation %q", ErrInvalidEvaluation, violation.Code)
		}
		seen[violation.Code] = struct{}{}
	}
	return nil
}

func validProductionMonitorViolation(violation ProductionMonitorViolation) bool {
	if !finiteAtLeast(violation.Observed, 0) || !finiteAtLeast(violation.Threshold, 0) {
		return false
	}
	switch violation.Code {
	case "mean_quality_below_floor", "quality_pass_rate_below_floor":
		return violation.Observed < violation.Threshold && violation.Threshold <= 1
	case "p95_cost_above_ceiling":
		return violation.Observed > violation.Threshold && violation.Threshold > 0
	case "budget_overrun_rate_above_ceiling":
		return violation.Observed > violation.Threshold && violation.Observed <= 1 && violation.Threshold <= 1
	default:
		return false
	}
}
