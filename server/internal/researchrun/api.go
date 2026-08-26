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

// ResearchRunSubmission is the narrow Agent-facing V6 boundary. It exposes
// frozen inputs and typed submissions without granting access to canonical
// child-table stores.
type ResearchRunSubmission interface {
	WorkManifest(context.Context, V6AttemptAccess) (V6WorkManifest, error)
	WorkArtifact(context.Context, V6AttemptAccess, string) (V6WorkArtifact, error)
	WorkCatalog(context.Context, V6CatalogRequest) (V6CatalogPage, error)
	AcknowledgeWorkCatalog(context.Context, AcknowledgeV6CatalogInput) error
	ReportWorkProgress(context.Context, ReportV6WorkProgressInput) error
	SubmitV6Work(context.Context, V6SubmissionInput) (V6SubmissionOutcome, error)
	DirectorBriefPage(context.Context, V6AttemptAccess, string) (V6DirectorBriefPage, error)
	AcknowledgeDirectorBrief(context.Context, AcknowledgeV6DirectorBriefInput) error
}

// ResearchRunV6Bootstrap is deliberately separate from ResearchRun.Create.
// Operators may enable this unreleased path for fixture and acceptance
// environments without adding V6 to the supported/default production policy.
type ResearchRunV6Bootstrap interface {
	BootstrapV6(context.Context, V6BootstrapInput) (Run, error)
}

type ResearchSourceIngestion interface {
	PersistSourceIngestion(context.Context, PersistSourceIngestionInput) (PersistSourceIngestionResult, error)
}

type ResearchCanonicalRebuild interface {
	RebuildCanonicalRun(context.Context, string, string) (RebuiltCanonicalRun, error)
}

type ResearchRunDirectorControl interface {
	AssignV6Director(context.Context, AssignV6DirectorInput) (V6DirectorAssignment, error)
	MarkV6DirectorUnavailable(context.Context, MarkV6DirectorUnavailableInput) (V6DirectorAssignment, error)
	StartV6DirectorCycle(context.Context, StartV6DirectorCycleInput) (V6DirectorCycle, error)
	AddV6TeamMember(context.Context, AddV6TeamMemberInput) (V6TeamMember, error)
	ArchiveV6TeamMember(context.Context, ArchiveV6TeamMemberInput) (V6TeamMember, error)
	RecordV6MatchDecision(context.Context, RecordV6MatchDecisionInput) (V6MatchDecision, error)
	OpenV6Discussion(context.Context, OpenV6DiscussionInput) (V6Discussion, error)
	ApplyV6SteeringAssessment(context.Context, ApplyV6SteeringAssessmentInput) (V6SteeringAssessment, error)
}

// ResearchRunReconciler is the scheduler-only V6 recovery boundary.
type ResearchRunReconciler interface {
	ReconcileV6Work(context.Context, int) (int, error)
}

var _ ResearchRun = (*Engine)(nil)
var _ ResearchRunSubmission = (*Engine)(nil)
var _ ResearchRunV6Bootstrap = (*Engine)(nil)
var _ ResearchSourceIngestion = (*Engine)(nil)
var _ ResearchCanonicalRebuild = (*Engine)(nil)
var _ ResearchRunReconciler = (*Engine)(nil)
var _ ResearchRunDirectorControl = (*Engine)(nil)
var _ V6ProjectionReader = (*Engine)(nil)
var _ V6WorkActivityWriter = (*Engine)(nil)
