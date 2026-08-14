package researchrun

import (
	"fmt"
	"time"
)

type ResearchMonitorStatus string

const (
	MonitorActive    ResearchMonitorStatus = "active"
	MonitorPaused    ResearchMonitorStatus = "paused"
	MonitorCancelled ResearchMonitorStatus = "cancelled"
	MonitorBlocked   ResearchMonitorStatus = "blocked"
	MonitorExhausted ResearchMonitorStatus = "budget_exhausted"
)

type ResearchMonitor struct {
	MonitorID            string
	Status               ResearchMonitorStatus
	QuestionID           string
	SearchPlanID         string
	SearchPlanVersion    int
	BaselineReportID     string
	Interval             time.Duration
	NextRunAt            time.Time
	MaterialityThreshold float64
	CredentialsValid     bool
	SourceReachable      bool
	RemainingBudget      float64
}

type MonitoringCycleInput struct {
	CycleID            string
	Now                time.Time
	SearchPlanID       string
	SearchPlanVersion  int
	QueryExecutionIDs  []string
	ContentDifference  float64
	ChangedArtifactIDs []string
}

type MonitoringCycleDecision struct {
	Eligible                  bool
	Status                    string
	Reason                    string
	NextRunAt                 time.Time
	WriteNoChangeDecision     bool
	CreateIncrementalQuestion bool
	CreateIntegrationRound    bool
	CreateReportRevision      bool
}

func EvaluateMonitoringCycle(monitor ResearchMonitor, cycle MonitoringCycleInput) (MonitoringCycleDecision, error) {
	if monitor.MonitorID == "" || monitor.QuestionID == "" || monitor.SearchPlanID == "" || monitor.SearchPlanVersion <= 0 || monitor.BaselineReportID == "" || monitor.Interval <= 0 || monitor.MaterialityThreshold < 0 || monitor.MaterialityThreshold > 1 || cycle.CycleID == "" || cycle.Now.IsZero() {
		return MonitoringCycleDecision{}, fmt.Errorf("%w: Monitor or Cycle contract is incomplete", ErrInvalidContract)
	}
	decision := MonitoringCycleDecision{Status: "not_eligible", NextRunAt: monitor.NextRunAt}
	switch monitor.Status {
	case MonitorPaused:
		decision.Reason = "user_paused"
		return decision, nil
	case MonitorCancelled:
		decision.Reason = "user_cancelled"
		return decision, nil
	case MonitorBlocked:
		decision.Reason = "monitor_blocked"
		return decision, nil
	case MonitorExhausted:
		decision.Reason = "budget_exhausted"
		return decision, nil
	case MonitorActive:
	default:
		return MonitoringCycleDecision{}, fmt.Errorf("%w: unsupported Monitor status %q", ErrInvalidContract, monitor.Status)
	}
	if cycle.Now.Before(monitor.NextRunAt) {
		decision.Reason = "not_due"
		return decision, nil
	}
	if !monitor.CredentialsValid {
		decision.Status, decision.Reason = "blocked", "credentials_invalid"
		return decision, nil
	}
	if !monitor.SourceReachable {
		decision.Status, decision.Reason = "blocked", "source_permanently_unreachable"
		return decision, nil
	}
	if monitor.RemainingBudget <= 0 {
		decision.Status, decision.Reason = "budget_exhausted", "budget_exhausted"
		return decision, nil
	}
	if cycle.SearchPlanID != monitor.SearchPlanID || cycle.SearchPlanVersion != monitor.SearchPlanVersion {
		return MonitoringCycleDecision{}, fmt.Errorf("%w: Monitoring Cycle must reuse the pinned Search Plan version", ErrInvalidTransition)
	}
	if len(cycle.QueryExecutionIDs) == 0 || cycle.ContentDifference < 0 || cycle.ContentDifference > 1 {
		return MonitoringCycleDecision{}, fmt.Errorf("%w: Monitoring Cycle requires Query Executions and bounded content difference", ErrInvalidContract)
	}
	decision.Eligible = true
	decision.NextRunAt = cycle.Now.Add(monitor.Interval)
	if cycle.ContentDifference < monitor.MaterialityThreshold {
		decision.Status, decision.Reason, decision.WriteNoChangeDecision = "no_material_change", "below_materiality_threshold", true
		return decision, nil
	}
	if len(cycle.ChangedArtifactIDs) == 0 {
		return MonitoringCycleDecision{}, fmt.Errorf("%w: material change requires changed artifact references", ErrInvalidContract)
	}
	decision.Status, decision.Reason = "material_change", "materiality_threshold_met"
	decision.CreateIncrementalQuestion, decision.CreateIntegrationRound, decision.CreateReportRevision = true, true, true
	return decision, nil
}
