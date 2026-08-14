package researchrun

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

type AcceptResultInput struct {
	SessionID   string
	AttemptID   string
	AgentID     string
	InboxTaskID string
	Raw         json.RawMessage
	Result      ResultEnvelope
	V6Plan      *ResearchV6PlanResult
	Hash        string
}

type AcceptResultOutcome struct {
	Replayed            bool     `json:"replayed"`
	TaskID              string   `json:"task_id"`
	TaskKind            TaskKind `json:"task_kind"`
	GoalVersion         int      `json:"goal_version"`
	PlanVersion         int      `json:"plan_version"`
	ReportID            string   `json:"report_id,omitempty"`
	QuestionsCreated    int      `json:"questions_created"`
	TasksCreated        int      `json:"tasks_created"`
	SourcesCreated      int      `json:"sources_created"`
	ObservationsCreated int      `json:"observations_created"`
	ClaimsCreated       int      `json:"claims_created"`
	Event               RunEvent `json:"event"`
}

type AttemptFailure struct {
	AttemptID    string
	FailureClass string
	SourceReason string
	Diagnostics  string
	Retryable    bool
}

// ControlTaskInput is the durable output of the runtime's delivery-gate
// routing decision. QuestionID is an entity binding, not prompt decoration.
type ControlTaskInput struct {
	SessionID  string
	Kind       TaskKind
	Objective  string
	Capability string
	Priority   float64
	QuestionID string
	// Findings are the defects assigned to this task. ObservedFindings retains
	// the complete Gate observation used to choose them.
	Findings         []GateFinding
	ObservedFindings []GateFinding
	Rationale        string
}

type Dispatcher interface {
	Dispatch(context.Context, DispatchRequest) (DispatchResult, error)
	Inspect(context.Context, []string) (map[string]InboxTaskState, error)
	Cancel(context.Context, []string, string) error
}

type classifiedDispatchError struct {
	err    error
	policy FailureDisposition
}

func (e classifiedDispatchError) Error() string                   { return e.err.Error() }
func (e classifiedDispatchError) Unwrap() error                   { return e.err }
func (e classifiedDispatchError) Retryable() bool                 { return e.policy.Retryable }
func (e classifiedDispatchError) Disposition() FailureDisposition { return e.policy }

// NewDispatchFailure preserves the Adapter's structured cause and the
// Research executor action policy. retryable may only narrow a class policy;
// it cannot make a deterministic class retryable.
func NewDispatchFailure(err error, class FailureClass, retryable bool) error {
	if err == nil {
		return nil
	}
	policy := failureDisposition(class)
	policy.Retryable = policy.Retryable && retryable
	return classifiedDispatchError{err: err, policy: policy}
}

// NonRetryableDispatchError marks an Adapter failure that cannot be repaired by
// dispatching the same Research Task again. The Engine fails the run instead of
// consuming attempt and remediation budgets on a deterministic defect.
func NonRetryableDispatchError(err error) error {
	return NewDispatchFailure(err, FailureInternal, false)
}

func DispatchFailurePolicy(err error) FailureDisposition {
	var classified interface{ Disposition() FailureDisposition }
	if errors.As(err, &classified) {
		return classified.Disposition()
	}
	policy := failureDisposition(FailureUnknown)
	var legacy interface{ Retryable() bool }
	if errors.As(err, &legacy) {
		policy.Retryable = legacy.Retryable()
	}
	return policy
}

func dispatchErrorRetryable(err error) bool {
	return DispatchFailurePolicy(err).Retryable
}

type Projector interface {
	Project(context.Context, RunEvent) error
}

type Clock interface {
	Now() time.Time
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now().UTC() }
