package researchrun

import (
	"context"
	"encoding/json"
)

// ResearchRun is the application boundary exposed to HTTP handlers,
// task-scoped result submission, and the scheduler. It contains run-level use
// cases and read models only; canonical child entities can be mutated only by
// the internal Modules and PostgreSQL transactions behind Engine.
type ResearchRun interface {
	Create(context.Context, StartInput) (Run, error)
	Snapshot(context.Context, string, string) (RunSnapshot, error)
	SnapshotForAttempt(context.Context, string, string, string) (RunSnapshot, error)
	ListFleetMembers(context.Context, string, string) ([]FleetMember, error)

	Pause(context.Context, string, string, string) (Run, error)
	Resume(context.Context, string, string, string) (Run, error)
	Cancel(context.Context, string, string, string, string) (Run, error)
	Archive(context.Context, string, string, string, string) (Run, error)
	Confirm(context.Context, string, string, string) (Run, error)
	Steer(context.Context, SteerInput) (Run, error)
	NodeCommand(context.Context, NodeCommandInput) (NodeCommandOutcome, error)

	SubmitResult(context.Context, string, string, string, string, string, string, json.RawMessage) (AcceptResultOutcome, error)
	ReconcileDue(context.Context, int) (int, error)
}

var _ ResearchRun = (*Engine)(nil)
