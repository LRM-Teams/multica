package researchrun

import (
	"testing"
	"time"
)

func monitorFixture(now time.Time) ResearchMonitor {
	return ResearchMonitor{MonitorID: "m1", Status: MonitorActive, QuestionID: "q1", SearchPlanID: "sp1", SearchPlanVersion: 2, BaselineReportID: "r1", Interval: 24 * time.Hour, NextRunAt: now, MaterialityThreshold: .2, CredentialsValid: true, SourceReachable: true, RemainingBudget: 10}
}

func TestEvaluateMonitoringCycleRecordsNoChangeWithoutRevision(t *testing.T) {
	now := time.Now().UTC()
	monitor := monitorFixture(now)
	decision, err := EvaluateMonitoringCycle(monitor, MonitoringCycleInput{CycleID: "c1", Now: now, SearchPlanID: "sp1", SearchPlanVersion: 2, QueryExecutionIDs: []string{"query-1"}, ContentDifference: .1})
	if err != nil || !decision.Eligible || !decision.WriteNoChangeDecision || decision.CreateReportRevision || decision.NextRunAt != now.Add(24*time.Hour) {
		t.Fatalf("decision=%+v err=%v", decision, err)
	}
}

func TestEvaluateMonitoringCycleMaterialChangeCreatesOnlyIncrementalWork(t *testing.T) {
	now := time.Now().UTC()
	monitor := monitorFixture(now)
	decision, err := EvaluateMonitoringCycle(monitor, MonitoringCycleInput{CycleID: "c1", Now: now, SearchPlanID: "sp1", SearchPlanVersion: 2, QueryExecutionIDs: []string{"query-1"}, ContentDifference: .4, ChangedArtifactIDs: []string{"snapshot-new"}})
	if err != nil || !decision.CreateIncrementalQuestion || !decision.CreateIntegrationRound || !decision.CreateReportRevision || decision.Reason != "materiality_threshold_met" {
		t.Fatalf("decision=%+v err=%v", decision, err)
	}
}

func TestEvaluateMonitoringCycleRejectsUnpinnedSearchPlanAndStopsAfterCancel(t *testing.T) {
	now := time.Now().UTC()
	monitor := monitorFixture(now)
	cycle := MonitoringCycleInput{CycleID: "c1", Now: now, SearchPlanID: "other", SearchPlanVersion: 3, QueryExecutionIDs: []string{"q"}}
	if _, err := EvaluateMonitoringCycle(monitor, cycle); err == nil {
		t.Fatal("expected Search Plan drift rejection")
	}
	monitor.Status = MonitorCancelled
	decision, err := EvaluateMonitoringCycle(monitor, cycle)
	if err != nil || decision.Eligible || decision.Reason != "user_cancelled" {
		t.Fatalf("decision=%+v err=%v", decision, err)
	}
}

func TestEvaluateMonitoringCycleBlocksCredentialsSourceAndBudget(t *testing.T) {
	now := time.Now().UTC()
	cycle := MonitoringCycleInput{CycleID: "c1", Now: now, SearchPlanID: "sp1", SearchPlanVersion: 2, QueryExecutionIDs: []string{"q"}}
	monitor := monitorFixture(now)
	monitor.CredentialsValid = false
	decision, _ := EvaluateMonitoringCycle(monitor, cycle)
	if decision.Reason != "credentials_invalid" {
		t.Fatalf("decision=%+v", decision)
	}
	monitor = monitorFixture(now)
	monitor.SourceReachable = false
	decision, _ = EvaluateMonitoringCycle(monitor, cycle)
	if decision.Reason != "source_permanently_unreachable" {
		t.Fatalf("decision=%+v", decision)
	}
	monitor = monitorFixture(now)
	monitor.RemainingBudget = 0
	decision, _ = EvaluateMonitoringCycle(monitor, cycle)
	if decision.Reason != "budget_exhausted" {
		t.Fatalf("decision=%+v", decision)
	}
}
