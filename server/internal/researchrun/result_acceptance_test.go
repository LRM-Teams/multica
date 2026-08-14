package researchrun

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestResultAcceptanceModuleRoutesV6PlanToAtomicAdapter(t *testing.T) {
	store, submission := validResultAcceptanceFixture(t)
	store.run.OrchestratorVersion = OrchestratorVersionV6
	store.run.SessionID, store.run.WorkspaceID = uuid.NewString(), uuid.NewString()
	store.task.SessionID, store.task.WorkspaceID = store.run.SessionID, store.run.WorkspaceID
	store.task.ExpectedResult = "research_plan_v6"
	store.members = []FleetMember{{AgentID: uuid.NewString(), Role: "researcher", Status: "active"}}
	submission.SessionID, submission.WorkspaceID = store.run.SessionID, store.run.WorkspaceID
	submission.Raw = encodeResearchV6PlanFixture(t, validResearchV6PlanFixture())

	if _, err := (resultAcceptanceModule{store: store}).Accept(context.Background(), submission); err != nil {
		t.Fatal(err)
	}
	if store.accepted == nil || store.accepted.V6Plan == nil || store.accepted.Result.SchemaVersion != 6 || len(store.accepted.Result.Plan.Tasks) != 1 {
		t.Fatalf("V6 plan did not reach atomic adapter: %+v", store.accepted)
	}
}

func TestResultAcceptanceModuleRoutesV6EvidenceToAtomicAdapter(t *testing.T) {
	store, submission := validResultAcceptanceFixture(t)
	store.run.OrchestratorVersion = OrchestratorVersionV6
	store.task.Kind, store.task.ExpectedResult = TaskKindDiscover, "research_evidence_v6"
	submission.Raw = encodeV6EvidenceFixture(t, validV6EvidenceResultFixture())

	if _, err := (resultAcceptanceModule{store: store}).Accept(context.Background(), submission); err != nil {
		t.Fatal(err)
	}
	if store.accepted == nil || store.accepted.V6Evidence == nil || store.accepted.Result.SchemaVersion != 6 || len(store.accepted.V6Evidence.QueryExecutions) != 1 {
		t.Fatalf("V6 evidence did not reach atomic adapter: %+v", store.accepted)
	}
}

func TestResultAcceptanceModuleValidatesAndPassesCanonicalInput(t *testing.T) {
	store, submission := validResultAcceptanceFixture(t)
	module := resultAcceptanceModule{store: store}

	outcome, err := module.Accept(context.Background(), submission)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.TaskID != store.task.ID || store.accepted == nil {
		t.Fatalf("outcome=%+v accepted=%+v", outcome, store.accepted)
	}
	accepted := store.accepted
	if accepted.SessionID != submission.SessionID || accepted.AttemptID != submission.AttemptID ||
		accepted.AgentID != submission.AgentID || accepted.InboxTaskID != submission.InboxTaskID {
		t.Fatalf("accepted identity=%+v", accepted)
	}
	if accepted.Hash == "" || accepted.Result.SchemaVersion != 1 || accepted.Result.ClientRequestID != "plan-request-1" {
		t.Fatalf("accepted decoded result=%+v hash=%q", accepted.Result, accepted.Hash)
	}
}

func TestResultAcceptanceModuleRejectsAttemptAndInboxMismatchBeforeMaterialization(t *testing.T) {
	t.Run("attempt does not belong to task", func(t *testing.T) {
		store, submission := validResultAcceptanceFixture(t)
		store.attempts[0].TaskID = "another-task"
		_, err := (resultAcceptanceModule{store: store}).Accept(context.Background(), submission)
		if !errors.Is(err, ErrRunNotFound) || store.accepted != nil {
			t.Fatalf("error=%v accepted=%+v", err, store.accepted)
		}
	})
	t.Run("inbox task is not assigned", func(t *testing.T) {
		store, submission := validResultAcceptanceFixture(t)
		submission.InboxTaskID = "another-inbox-task"
		_, err := (resultAcceptanceModule{store: store}).Accept(context.Background(), submission)
		if !errors.Is(err, ErrAttemptNotAssigned) || store.accepted != nil {
			t.Fatalf("error=%v accepted=%+v", err, store.accepted)
		}
	})
}

func TestResultAcceptanceModuleRejectsMissingPlanCapability(t *testing.T) {
	store, submission := validResultAcceptanceFixture(t)
	store.members[0].Status = "inactive"

	_, err := (resultAcceptanceModule{store: store}).Accept(context.Background(), submission)
	if !errors.Is(err, ErrCapabilityUnavailable) || !strings.Contains(err.Error(), "scout") {
		t.Fatalf("error=%v", err)
	}
	if store.accepted != nil {
		t.Fatalf("result reached atomic acceptance: %+v", store.accepted)
	}
}

func TestResultAcceptanceModuleRejectsUnsupportedRunVersion(t *testing.T) {
	store, submission := validResultAcceptanceFixture(t)
	store.run.OrchestratorVersion = "research-run-v999"

	_, err := (resultAcceptanceModule{store: store}).Accept(context.Background(), submission)
	if !errors.Is(err, ErrUnsupportedVersion) || store.accepted != nil {
		t.Fatalf("error=%v accepted=%+v", err, store.accepted)
	}
}

func TestResultAcceptanceModuleRejectsMissingDispatchManifestWhenPassportEnabled(t *testing.T) {
	store, submission := validResultAcceptanceFixture(t)
	store.passportEnabled = true
	store.hasDispatchManifest = false

	_, err := (resultAcceptanceModule{store: store}).Accept(context.Background(), submission)
	if !errors.Is(err, ErrInvalidTransition) || store.accepted != nil {
		t.Fatalf("error=%v accepted=%+v", err, store.accepted)
	}
}

func validResultAcceptanceFixture(t *testing.T) (*resultAcceptanceTestStore, resultSubmission) {
	t.Helper()
	raw, err := json.Marshal(validPlanResult(t))
	if err != nil {
		t.Fatal(err)
	}
	store := &resultAcceptanceTestStore{
		run: Run{
			SessionID: "session-1", WorkspaceID: "workspace-1", DepthTier: "standard",
			OrchestratorVersion: OrchestratorVersionV1, Config: DefaultRunConfig("standard"),
		},
		task: Task{
			ID: "task-1", SessionID: "session-1", WorkspaceID: "workspace-1",
			Kind: TaskKindPlan, ExpectedResult: "research_plan_v1",
		},
		attempts: []Attempt{{ID: "attempt-1", TaskID: "task-1", InboxTaskID: "inbox-1"}},
		members:  []FleetMember{{AgentID: "scout-agent", Role: "scout", Status: "active"}},
		outcome:  AcceptResultOutcome{TaskID: "task-1", TaskKind: TaskKindPlan},
		contract: ResearchContract{SourcePolicy: json.RawMessage(`{}`)},
	}
	return store, resultSubmission{
		SessionID: "session-1", WorkspaceID: "workspace-1", TaskID: "task-1",
		AttemptID: "attempt-1", AgentID: "lead-agent", InboxTaskID: "inbox-1", Raw: raw,
	}
}

type resultAcceptanceTestStore struct {
	run                 Run
	task                Task
	attempts            []Attempt
	members             []FleetMember
	outcome             AcceptResultOutcome
	contract            ResearchContract
	accepted            *AcceptResultInput
	passportEnabled     bool
	hasDispatchManifest bool
}

func (store *resultAcceptanceTestStore) GetRun(context.Context, string, string) (Run, error) {
	return store.run, nil
}

func (store *resultAcceptanceTestStore) GetTask(context.Context, string, string) (Task, error) {
	return store.task, nil
}

func (store *resultAcceptanceTestStore) ListAttempts(context.Context, string) ([]Attempt, error) {
	return append([]Attempt(nil), store.attempts...), nil
}

func (store *resultAcceptanceTestStore) ListFleetMembers(context.Context, string, string) ([]FleetMember, error) {
	return append([]FleetMember(nil), store.members...), nil
}

func (store *resultAcceptanceTestStore) GetCurrentContract(context.Context, string, string) (ResearchContract, error) {
	return store.contract, nil
}

func (store *resultAcceptanceTestStore) AcceptResult(_ context.Context, input AcceptResultInput) (AcceptResultOutcome, error) {
	copy := input
	store.accepted = &copy
	return store.outcome, nil
}

func (store *resultAcceptanceTestStore) SessionArtifactPassportEnabled(context.Context, string, string) (bool, error) {
	return store.passportEnabled, nil
}

func (store *resultAcceptanceTestStore) AttemptHasDispatchManifest(context.Context, string, string, string) (bool, error) {
	return store.hasDispatchManifest, nil
}
