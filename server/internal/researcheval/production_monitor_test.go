package researcheval

import (
	"errors"
	"math"
	"testing"
	"time"
)

func productionMonitorPolicy() ProductionMonitorPolicy {
	return ProductionMonitorPolicy{
		MinimumSamples: 3, MinimumMeanQualityScore: 0.8, MinimumQualityPassRate: 0.8,
		MaximumP95CostUnits: 100, MaximumBudgetOverrunRate: 0.2,
	}
}

func productionObservation(id string, score, cost, budget float64, passed bool, offset time.Duration) ProductionRunObservation {
	return ProductionRunObservation{
		RunID: id, StrategyVersion: "strategy-v7", ObservedAt: time.Unix(1_700_000_000, 0).Add(offset),
		QualityScore: score, QualityPassed: passed, CostUnits: cost, BudgetUnits: budget,
	}
}

func TestEvaluateProductionWindowReportsEveryQualityAndCostViolation(t *testing.T) {
	report, err := EvaluateProductionWindow([]ProductionRunObservation{
		productionObservation("run-3", 0.6, 130, 100, false, 3*time.Hour),
		productionObservation("run-1", 0.7, 90, 100, true, time.Hour),
		productionObservation("run-2", 0.8, 110, 100, false, 2*time.Hour),
	}, productionMonitorPolicy())
	if err != nil {
		t.Fatal(err)
	}
	wantCodes := []string{"mean_quality_below_floor", "quality_pass_rate_below_floor", "p95_cost_above_ceiling", "budget_overrun_rate_above_ceiling"}
	if report.SchemaVersion != ProductionMonitorSchemaVersion || report.StrategyVersion != "strategy-v7" || report.WithinBounds || !report.SufficientData || len(report.Violations) != len(wantCodes) {
		t.Fatalf("report=%+v", report)
	}
	for index, code := range wantCodes {
		if report.Violations[index].Code != code {
			t.Fatalf("violations=%+v", report.Violations)
		}
	}
	if report.Metrics.Samples != 3 || report.Metrics.P95CostUnits != 130 || report.WindowStartedAt != time.Unix(1_700_000_000, 0).Add(time.Hour) || report.WindowEndedAt != time.Unix(1_700_000_000, 0).Add(3*time.Hour) {
		t.Fatalf("metrics/window=%+v %v..%v", report.Metrics, report.WindowStartedAt, report.WindowEndedAt)
	}
}

func TestEvaluateProductionWindowDoesNotJudgeInsufficientWindow(t *testing.T) {
	report, err := EvaluateProductionWindow([]ProductionRunObservation{
		productionObservation("run-1", 0.1, 500, 100, false, time.Hour),
		productionObservation("run-2", 0.1, 500, 100, false, 2*time.Hour),
	}, productionMonitorPolicy())
	if err != nil || report.SufficientData || report.WithinBounds || len(report.Violations) != 0 {
		t.Fatalf("report=%+v err=%v", report, err)
	}
}

func TestEvaluateProductionWindowAcceptsExactBoundaries(t *testing.T) {
	policy := productionMonitorPolicy()
	observations := []ProductionRunObservation{
		productionObservation("run-1", 0.8, 100, 100, true, time.Hour),
		productionObservation("run-2", 0.8, 100, 100, true, 2*time.Hour),
		productionObservation("run-3", 0.8, 100, 100, true, 3*time.Hour),
		productionObservation("run-4", 0.8, 100, 100, true, 4*time.Hour),
		productionObservation("run-5", 0.8, 101, 100, false, 5*time.Hour),
	}
	policy.MinimumSamples = 5
	policy.MaximumP95CostUnits = 101
	report, err := EvaluateProductionWindow(observations, policy)
	if err != nil || !report.SufficientData || !report.WithinBounds || len(report.Violations) != 0 || report.Metrics.BudgetOverrunRate != 0.2 {
		t.Fatalf("report=%+v err=%v", report, err)
	}
}

func TestEvaluateProductionWindowRejectsAmbiguousOrInvalidEvidence(t *testing.T) {
	valid := productionObservation("run-1", 0.9, 80, 100, true, time.Hour)
	tests := map[string][]ProductionRunObservation{
		"mixed strategy": {valid, func() ProductionRunObservation {
			value := valid
			value.RunID = "run-2"
			value.StrategyVersion = "strategy-v8"
			return value
		}()},
		"duplicate run": {valid, valid},
		"nan quality":   {func() ProductionRunObservation { value := valid; value.QualityScore = math.NaN(); return value }()},
		"infinite cost": {func() ProductionRunObservation { value := valid; value.CostUnits = math.Inf(1); return value }()},
		"zero budget":   {func() ProductionRunObservation { value := valid; value.BudgetUnits = 0; return value }()},
	}
	for name, observations := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := EvaluateProductionWindow(observations, productionMonitorPolicy()); !errors.Is(err, ErrInvalidEvaluation) {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestEvaluateProductionWindowRejectsInvalidPolicy(t *testing.T) {
	policy := productionMonitorPolicy()
	policy.MaximumBudgetOverrunRate = math.NaN()
	if _, err := EvaluateProductionWindow([]ProductionRunObservation{productionObservation("run-1", 0.9, 80, 100, true, time.Hour)}, policy); !errors.Is(err, ErrInvalidEvaluation) {
		t.Fatalf("err=%v", err)
	}
}
