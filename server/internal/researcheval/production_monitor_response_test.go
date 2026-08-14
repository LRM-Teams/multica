package researcheval

import (
	"errors"
	"testing"
	"time"
)

func monitorReportFixture() ProductionMonitorReport {
	return ProductionMonitorReport{
		SchemaVersion: ProductionMonitorSchemaVersion, StrategyVersion: "strategy-v2",
		WindowStartedAt: time.Unix(100, 0), WindowEndedAt: time.Unix(200, 0),
		Metrics:        ProductionMonitorMetrics{Samples: 10, MeanQualityScore: .7, QualityPassRate: .7, P95CostUnits: 80, BudgetOverrunRate: .1},
		Violations:     []ProductionMonitorViolation{{Code: "mean_quality_below_floor", Observed: .7, Threshold: .8}},
		SufficientData: true,
	}
}

func TestDecideProductionMonitorResponseRequiresRollbackOnBreach(t *testing.T) {
	decision, err := DecideProductionMonitorResponse(ProductionMonitorDecisionInput{
		Report: monitorReportFixture(), CurrentStrategyVersion: "strategy-v2", PreviousStrategyVersion: "strategy-v1",
	})
	if err != nil || decision.Action != ProductionMonitorRollbackRequired || decision.PreviousStrategyVersion != "strategy-v1" || len(decision.Violations) != 1 {
		t.Fatalf("decision=%+v err=%v", decision, err)
	}
}

func TestDecideProductionMonitorResponseFreezesWhenRollbackIsImpossible(t *testing.T) {
	for _, previous := range []string{"", "strategy-v2"} {
		decision, err := DecideProductionMonitorResponse(ProductionMonitorDecisionInput{
			Report: monitorReportFixture(), CurrentStrategyVersion: "strategy-v2", PreviousStrategyVersion: previous,
		})
		if err != nil || decision.Action != ProductionMonitorFreezeAndEscalate || decision.PreviousStrategyVersion != "" {
			t.Fatalf("previous=%q decision=%+v err=%v", previous, decision, err)
		}
	}
}

func TestDecideProductionMonitorResponseDoesNotJudgeInsufficientData(t *testing.T) {
	report := monitorReportFixture()
	report.SufficientData = false
	report.Violations = nil
	decision, err := DecideProductionMonitorResponse(ProductionMonitorDecisionInput{
		Report: report, CurrentStrategyVersion: "strategy-v2", PreviousStrategyVersion: "strategy-v1",
	})
	if err != nil || decision.Action != ProductionMonitorCollectMoreSamples {
		t.Fatalf("decision=%+v err=%v", decision, err)
	}
}

func TestDecideProductionMonitorResponseRetainsOnlyConsistentPassingReport(t *testing.T) {
	report := monitorReportFixture()
	report.WithinBounds = true
	report.Violations = nil
	decision, err := DecideProductionMonitorResponse(ProductionMonitorDecisionInput{Report: report, CurrentStrategyVersion: "strategy-v2"})
	if err != nil || decision.Action != ProductionMonitorRetainStrategy {
		t.Fatalf("decision=%+v err=%v", decision, err)
	}

	report.Violations = []ProductionMonitorViolation{{Code: "mean_quality_below_floor", Observed: .7, Threshold: .8}}
	if _, err = DecideProductionMonitorResponse(ProductionMonitorDecisionInput{Report: report, CurrentStrategyVersion: "strategy-v2"}); !errors.Is(err, ErrInvalidEvaluation) {
		t.Fatalf("contradictory report error=%v", err)
	}
}

func TestDecideProductionMonitorResponseRejectsForgedReport(t *testing.T) {
	tests := map[string]func(*ProductionMonitorDecisionInput){
		"wrong strategy":      func(input *ProductionMonitorDecisionInput) { input.CurrentStrategyVersion = "strategy-v3" },
		"unknown violation":   func(input *ProductionMonitorDecisionInput) { input.Report.Violations[0].Code = "ignore_quality" },
		"reversed inequality": func(input *ProductionMonitorDecisionInput) { input.Report.Violations[0].Observed = .9 },
		"duplicate violation": func(input *ProductionMonitorDecisionInput) {
			input.Report.Violations = append(input.Report.Violations, input.Report.Violations[0])
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			input := ProductionMonitorDecisionInput{Report: monitorReportFixture(), CurrentStrategyVersion: "strategy-v2", PreviousStrategyVersion: "strategy-v1"}
			mutate(&input)
			if _, err := DecideProductionMonitorResponse(input); !errors.Is(err, ErrInvalidEvaluation) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}
