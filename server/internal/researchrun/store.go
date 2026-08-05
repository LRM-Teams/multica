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
	Diagnostics  string
	Retryable    bool
}

type Store interface {
	CreateRun(context.Context, StartInput, RunConfig) (Run, RunEvent, error)
	InitializeRun(context.Context, StartInput, RunConfig) (Run, RunEvent, error)
	GetRun(context.Context, string, string) (Run, error)
	GetCurrentContract(context.Context, string, string) (ResearchContract, error)
	GetCurrentMethod(context.Context, string, string) (*ResearchMethod, error)
	ListQuestions(context.Context, string) ([]Question, error)
	ListTasks(context.Context, string) ([]Task, error)
	ListAttempts(context.Context, string) ([]Attempt, error)
	ListPendingCancellations(context.Context, string) ([]PendingCancellation, error)
	MarkCancellationsCompleted(context.Context, string, []string) error
	ListSourceSnapshots(context.Context, string) ([]SourceSnapshotView, error)
	ListObservations(context.Context, string) ([]Observation, error)
	ListClaims(context.Context, string) ([]Claim, error)
	ListFleetMembers(context.Context, string, string) ([]FleetMember, error)
	GetTask(context.Context, string, string) (Task, error)
	TaskContext(context.Context, string, string) (RunSnapshot, error)

	ClaimRun(context.Context, string, string, time.Duration) (Run, bool, error)
	ReleaseRun(context.Context, string, string, time.Time) error
	ListDueRunIDs(context.Context, int) ([]string, error)

	ActivateReadyTasks(context.Context, string) (int, error)
	CreateAttempt(context.Context, string, string, string) (Attempt, RunEvent, error)
	AttachInboxTask(context.Context, string, string) (Attempt, RunEvent, error)
	FailAttempt(context.Context, AttemptFailure) (RunEvent, error)
	AcceptResult(context.Context, AcceptResultInput) (AcceptResultOutcome, error)
	CreateControlTask(context.Context, string, TaskKind, string, string, float64) (Task, RunEvent, error)
	NodeCommand(context.Context, NodeCommandInput) (NodeCommandOutcome, error)
	SetAwaitingConfirmation(context.Context, string, GateResult) (Run, RunEvent, error)
	Complete(context.Context, string, string, string) (Run, RunEvent, error)

	Steer(context.Context, SteerInput) (Run, RunEvent, []string, error)
	Pause(context.Context, string, string, string) (Run, RunEvent, []string, error)
	Resume(context.Context, string, string, string) (Run, RunEvent, error)
	Archive(context.Context, string, string, string, string) (Run, RunEvent, []string, error)
	Cancel(context.Context, string, string, string, string) (Run, RunEvent, []string, error)
	MarkFailed(context.Context, string, string) (Run, RunEvent, []string, error)
	RecordBudgetExhausted(context.Context, string, string, string) (RunEvent, error)

	EvaluateGate(context.Context, string) (GateResult, error)
	ListUnprojectedEvents(context.Context, string, int) ([]RunEvent, error)
	MarkEventProjected(context.Context, string) error
	MarkEventProjectionFailed(context.Context, string, string, time.Time) error
	ReconcileAttempts(context.Context, string, map[string]InboxTaskState) ([]RunEvent, error)
}

type Dispatcher interface {
	Dispatch(context.Context, DispatchRequest) (DispatchResult, error)
	Inspect(context.Context, []string) (map[string]InboxTaskState, error)
	Cancel(context.Context, []string, string) error
}

type classifiedDispatchError struct {
	err       error
	retryable bool
}

func (e classifiedDispatchError) Error() string   { return e.err.Error() }
func (e classifiedDispatchError) Unwrap() error   { return e.err }
func (e classifiedDispatchError) Retryable() bool { return e.retryable }

// NonRetryableDispatchError marks an Adapter failure that cannot be repaired by
// dispatching the same Research Task again. The Engine fails the run instead of
// consuming attempt and remediation budgets on a deterministic defect.
func NonRetryableDispatchError(err error) error {
	if err == nil {
		return nil
	}
	return classifiedDispatchError{err: err, retryable: false}
}

func dispatchErrorRetryable(err error) bool {
	var classified interface{ Retryable() bool }
	if errors.As(err, &classified) {
		return classified.Retryable()
	}
	// Existing Dispatcher implementations predate explicit classification.
	// Preserve bounded retry for unknown/transient errors; production Adapters
	// must classify deterministic contract/configuration failures.
	return true
}

type Projector interface {
	Project(context.Context, RunEvent) error
}

type Clock interface {
	Now() time.Time
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now().UTC() }
