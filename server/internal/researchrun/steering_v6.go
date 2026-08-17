package researchrun

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

type V6SteeringImpact struct {
	Kind                 string `json:"kind"`
	ID                   string `json:"id"`
	ExpectedStateVersion int64  `json:"expected_state_version,omitempty"`
	Disposition          string `json:"disposition"`
	Reason               string `json:"reason"`
}

type ApplyV6SteeringAssessmentInput struct {
	WorkspaceID, RunID, MessageID, DirectorCycleID     string
	AssessmentKind, Interpretation, Reason             string
	ExpectedGoalVersion                                int
	ExpectedStateVersion                               int64
	SelectedRefs                                       json.RawMessage
	Impacts                                            []V6SteeringImpact
	AcceptedActionIDs                                  []string
	RevisedGoal                                        string
	RevisedScope, RevisedSourcePolicy, RevisedLimits   json.RawMessage
	RevisedAudience, RevisedFreshness, RevisedLanguage string
}

type V6SteeringAssessment struct {
	ID, Kind, MessageID, DirectorCycleID string
	GoalVersionBefore, GoalVersionAfter  int
	AffectedRefs                         json.RawMessage
}

type steeringV6Store interface {
	ApplyV6SteeringAssessment(context.Context, ApplyV6SteeringAssessmentInput) (V6SteeringAssessment, error)
}

type steeringV6Module struct{ store steeringV6Store }

func (m steeringV6Module) Apply(ctx context.Context, in ApplyV6SteeringAssessmentInput) (V6SteeringAssessment, error) {
	if m.store == nil || strings.TrimSpace(in.MessageID) == "" || strings.TrimSpace(in.DirectorCycleID) == "" ||
		strings.TrimSpace(in.Interpretation) == "" || strings.TrimSpace(in.Reason) == "" || in.ExpectedGoalVersion < 1 || in.ExpectedStateVersion < 1 {
		return V6SteeringAssessment{}, fmt.Errorf("%w: incomplete steering assessment", ErrInvalidContract)
	}
	switch in.AssessmentKind {
	case "no_op":
		if len(in.Impacts) != 0 || strings.TrimSpace(in.RevisedGoal) != "" {
			return V6SteeringAssessment{}, fmt.Errorf("%w: no-op assessment cannot mutate state", ErrInvalidContract)
		}
	case "local_change", "full_reassessment":
		if len(in.Impacts) == 0 {
			return V6SteeringAssessment{}, fmt.Errorf("%w: steering impact set is empty", ErrInvalidContract)
		}
	case "goal_revision":
		if strings.TrimSpace(in.RevisedGoal) == "" || len(in.Impacts) == 0 {
			return V6SteeringAssessment{}, fmt.Errorf("%w: goal revision requires goal and explicit impacts", ErrInvalidContract)
		}
	default:
		return V6SteeringAssessment{}, fmt.Errorf("%w: unknown steering assessment kind", ErrInvalidContract)
	}
	seen := map[string]struct{}{}
	for _, impact := range in.Impacts {
		key := impact.Kind + ":" + impact.ID
		if strings.TrimSpace(impact.ID) == "" || strings.TrimSpace(impact.Reason) == "" {
			return V6SteeringAssessment{}, fmt.Errorf("%w: incomplete steering impact", ErrInvalidContract)
		}
		if _, exists := seen[key]; exists {
			return V6SteeringAssessment{}, fmt.Errorf("%w: duplicate steering impact", ErrInvalidContract)
		}
		seen[key] = struct{}{}
	}
	return m.store.ApplyV6SteeringAssessment(ctx, in)
}
