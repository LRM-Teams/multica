package researchrun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestV6DirectorBriefSurvivesItsOwnOperationalEvents(t *testing.T) {
	cycleID := "00000000-0000-4000-8000-000000000001"
	workID := "00000000-0000-4000-8000-000000000002"
	briefID := "00000000-0000-4000-8000-000000000003"
	for _, test := range []struct {
		name, eventType, payload string
		want                     bool
	}{
		{name: "cycle created", eventType: "v6_director_cycle_created", payload: `{"cycle_id":"` + cycleID + `","work_item_id":"` + workID + `","brief_id":"` + briefID + `"}`, want: true},
		{name: "dispatch prepared", eventType: "v6_work_item_dispatch_prepared", payload: `{"work_item_id":"` + workID + `"}`, want: true},
		{name: "dispatch completed", eventType: "v6_work_item_dispatched", payload: `{"work_item_id":"` + workID + `"}`, want: true},
		{name: "attempt recovered", eventType: "v6_work_item_recovered", payload: `{"work_item_id":"` + workID + `"}`, want: true},
		{name: "brief acknowledged", eventType: "v6_director_brief_page_acknowledged", payload: `{"brief_id":"` + briefID + `"}`, want: true},
		{name: "proposal received", eventType: "v6_work_submission_received", payload: `{"work_item_id":"` + workID + `"}`, want: true},
		{name: "other work", eventType: "v6_work_item_dispatched", payload: `{"work_item_id":"00000000-0000-4000-8000-000000000009"}`},
		{name: "material event", eventType: "v6_branch_created", payload: `{"work_item_id":"` + workID + `"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := isV6DirectorCycleOperationalEvent(test.eventType, json.RawMessage(test.payload), cycleID, workID, briefID); got != test.want {
				t.Fatalf("allowed=%v want=%v", got, test.want)
			}
		})
	}
}

type teamV6StoreStub struct {
	added    AddV6TeamMemberInput
	archived ArchiveV6TeamMemberInput
}

func (s *teamV6StoreStub) AddV6TeamMember(_ context.Context, in AddV6TeamMemberInput) (V6TeamMember, error) {
	s.added = in
	return V6TeamMember{AgentID: in.AgentID}, nil
}
func (s *teamV6StoreStub) ArchiveV6TeamMember(_ context.Context, in ArchiveV6TeamMemberInput) (V6TeamMember, error) {
	s.archived = in
	return V6TeamMember{ID: in.MembershipID, State: V6TeamArchived}, nil
}

func (s *teamV6StoreStub) FindActiveV6TeamMemberByAgent(context.Context, string, string, string) (V6TeamMember, bool, error) {
	return V6TeamMember{}, false, nil
}

func TestV6TeamCapacityRules(t *testing.T) {
	store := &teamV6StoreStub{}
	_, err := (teamV6Module{store: store}).Add(context.Background(), AddV6TeamMemberInput{
		WorkspaceID: "workspace", RunID: "run", AgentID: "agent", MissionPrompt: "Investigate the assigned question.",
	})
	if err != nil || store.added.AgentID != "agent" {
		t.Fatalf("add member: input=%+v err=%v", store.added, err)
	}
}

func TestV6TeamArchiveRequiresReason(t *testing.T) {
	_, err := (teamV6Module{store: &teamV6StoreStub{}}).Archive(context.Background(), ArchiveV6TeamMemberInput{MembershipID: "membership"})
	if !errors.Is(err, ErrInvalidContract) {
		t.Fatalf("error=%v", err)
	}
}

type directorV6StoreStub struct{}

func (directorV6StoreStub) AssignV6Director(_ context.Context, in AssignV6DirectorInput) (V6DirectorAssignment, error) {
	return V6DirectorAssignment{AgentID: in.AgentID}, nil
}
func (directorV6StoreStub) MarkV6DirectorUnavailable(_ context.Context, in MarkV6DirectorUnavailableInput) (V6DirectorAssignment, error) {
	return V6DirectorAssignment{ID: in.AssignmentID, Status: "unavailable"}, nil
}

func TestV6DirectorAssignmentValidation(t *testing.T) {
	_, err := (directorModule{store: directorV6StoreStub{}}).Assign(context.Background(), AssignV6DirectorInput{AgentID: "agent", UserID: "user"})
	if !errors.Is(err, ErrInvalidContract) {
		t.Fatalf("error=%v", err)
	}
}

func TestV6DirectorFailureValidation(t *testing.T) {
	got, err := (directorModule{store: directorV6StoreStub{}}).MarkUnavailable(context.Background(), MarkV6DirectorUnavailableInput{AssignmentID: "assignment", FailureClass: "quota", ClientRequestID: "request"})
	if err != nil || got.Status != "unavailable" {
		t.Fatalf("assignment=%+v err=%v", got, err)
	}
}

func TestV6DirectorBriefIsBoundedAndPaged(t *testing.T) {
	branches := make([]any, 257)
	for i := range branches {
		branches[i] = map[string]any{
			"branch":    map[string]any{"id": fmt.Sprintf("00000000-0000-4000-8000-%012d", i+1), "state_version": 1},
			"objective": "Investigate", "scope": map[string]any{}, "status": "active", "frontier_nodes": []any{}, "has_more": false,
		}
	}
	facts := DirectorBriefFacts{
		WorkspaceID: "00000000-0000-4000-8000-000000000001", RunID: "00000000-0000-4000-8000-000000000002",
		AssignmentID: "00000000-0000-4000-8000-000000000003", DirectorGeneration: 1, StateVersion: 1,
		Goal:          map[string]any{"goal_version": 1, "goal": "Research", "scope": map[string]any{}, "audience": "", "freshness": "", "language": "en", "source_policy": map[string]any{}},
		DirectorState: "available", Team: []any{map[string]any{"agent_id": "00000000-0000-4000-8000-000000000004", "membership_id": "00000000-0000-4000-8000-000000000005", "state": "idle", "mission_summary": "Direct"}}, Branches: branches,
		TerminalSummaries: []any{}, WorkItems: []any{}, Discussions: []any{}, Reports: []any{}, UnresolvedDisputes: []any{}, Steering: []any{},
	}
	brief, err := (contextCompilerModule{}).CompileDirectorBrief(facts, time.Unix(1, 0))
	if err != nil || len(brief.Pages) != 5 {
		t.Fatalf("pages=%d err=%v", len(brief.Pages), err)
	}
}

type directorBriefStoreStub struct {
	acknowledged AcknowledgeV6DirectorBriefInput
}

func (*directorBriefStoreStub) LoadDirectorBriefFacts(context.Context, StartV6DirectorCycleInput) (DirectorBriefFacts, error) {
	return DirectorBriefFacts{}, nil
}
func (*directorBriefStoreStub) PersistDirectorCycle(context.Context, StartV6DirectorCycleInput, CompiledDirectorBrief) (V6DirectorCycle, error) {
	return V6DirectorCycle{}, nil
}
func (*directorBriefStoreStub) LoadDirectorBriefPage(context.Context, V6AttemptAccess, string) (V6DirectorBriefPage, error) {
	return V6DirectorBriefPage{}, nil
}
func (s *directorBriefStoreStub) AcknowledgeDirectorBriefPage(_ context.Context, in AcknowledgeV6DirectorBriefInput) error {
	s.acknowledged = in
	return nil
}

func TestV6DirectorBriefAcknowledgementDelegates(t *testing.T) {
	store := &directorBriefStoreStub{}
	in := AcknowledgeV6DirectorBriefInput{ClientRequestID: "request", PageKey: "page"}
	if err := (directorBriefModule{store: store}).Acknowledge(context.Background(), in); err != nil || store.acknowledged.PageKey != "page" {
		t.Fatalf("ack=%+v err=%v", store.acknowledged, err)
	}
}

func TestReviewedV6DirectorBriefCanBeReadAgainForRetry(t *testing.T) {
	run := newTransactionRecoveryRun(t, "Re-read acknowledged Director Brief")
	if _, err := run.pool.Exec(run.ctx, `UPDATE research_session SET orchestrator_version='research-run-v6' WHERE id=$1::uuid`, run.fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := run.store.AssignV6Director(run.ctx, AssignV6DirectorInput{
		WorkspaceID: run.fixture.workspaceID, RunID: run.fixture.sessionID,
		AgentID: run.fixture.agentID, UserID: run.fixture.userID,
		Reason: "Run the retry readability test", ClientRequestID: uuid.NewString(),
		ExpectedStateVersion: 0,
	}); err != nil {
		t.Fatal(err)
	}

	var stateVersion, throughSequence int64
	if err := run.pool.QueryRow(run.ctx, `
		SELECT state_version,COALESCE((SELECT max(sequence) FROM research_run_event WHERE session_id=$1::uuid),0)
		FROM research_session WHERE id=$1::uuid
	`, run.fixture.sessionID).Scan(&stateVersion, &throughSequence); err != nil {
		t.Fatal(err)
	}
	cycle, err := (directorBriefModule{store: run.store}).Start(run.ctx, StartV6DirectorCycleInput{
		WorkspaceID: run.fixture.workspaceID, RunID: run.fixture.sessionID,
		TriggerKey: uuid.NewString(), FromSequence: throughSequence, ThroughSequence: throughSequence,
		ExpectedStateVersion: stateVersion, Now: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	var membershipID string
	if err = run.pool.QueryRow(run.ctx, `
		SELECT id::text FROM research_team_membership
		WHERE session_id=$1::uuid AND agent_id=$2::uuid AND state='idle'
		ORDER BY membership_generation DESC LIMIT 1
	`, run.fixture.sessionID, run.fixture.agentID).Scan(&membershipID); err != nil {
		t.Fatal(err)
	}
	attemptID := seedV6RecoveryAttempt(t, run, membershipID, cycle.WorkItemID)
	access := V6AttemptAccess{
		WorkspaceID: run.fixture.workspaceID, RunID: run.fixture.sessionID,
		WorkItemID: cycle.WorkItemID, AttemptID: attemptID, AgentID: run.fixture.agentID,
	}
	page, err := run.store.LoadDirectorBriefPage(run.ctx, access, "")
	if err != nil {
		t.Fatal(err)
	}
	if page.Reviewed {
		t.Fatal("new Director Brief page is already reviewed")
	}
	if err = run.store.AcknowledgeDirectorBriefPage(run.ctx, AcknowledgeV6DirectorBriefInput{
		V6AttemptAccess: access, ClientRequestID: uuid.NewString(),
		BriefID: cycle.BriefID, BriefHash: cycle.BriefHash,
		PageKey: page.PageKey, PageHash: page.PageHash,
	}); err != nil {
		t.Fatal(err)
	}

	replayed, err := run.store.LoadDirectorBriefPage(run.ctx, access, "")
	if err != nil {
		t.Fatalf("re-read acknowledged Director Brief: %v", err)
	}
	if !replayed.Reviewed || replayed.PageKey != page.PageKey || string(replayed.Bytes) != string(page.Bytes) {
		t.Fatalf("replayed page = reviewed:%v key:%q, want reviewed page %q", replayed.Reviewed, replayed.PageKey, page.PageKey)
	}
}

func TestV6EventTriggerWaitsForMaterialRuntimeEffects(t *testing.T) {
	run := newTransactionRecoveryRun(t, "Wait for V6 material runtime effects")
	if _, err := run.pool.Exec(run.ctx, `UPDATE research_session SET orchestrator_version='research-run-v6' WHERE id=$1::uuid`, run.fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := run.store.AssignV6Director(run.ctx, AssignV6DirectorInput{
		WorkspaceID: run.fixture.workspaceID, RunID: run.fixture.sessionID,
		AgentID: run.fixture.agentID, UserID: run.fixture.userID,
		Reason: "Run the material effect ordering test", ClientRequestID: uuid.NewString(),
		ExpectedStateVersion: 0,
	}); err != nil {
		t.Fatal(err)
	}

	tx, err := run.pool.Begin(run.ctx)
	if err != nil {
		t.Fatal(err)
	}
	event, err := appendEvent(run.ctx, tx, run.fixture.workspaceID, run.fixture.sessionID,
		"v6_agent_creation_requested", "event-trigger-material-effect:"+uuid.NewString(), "system", "",
		map[string]any{"name": "source scout"})
	if err != nil {
		_ = tx.Rollback(run.ctx)
		t.Fatal(err)
	}
	if err = tx.Commit(run.ctx); err != nil {
		t.Fatal(err)
	}
	outboxID := uuid.NewString()
	if _, err = run.pool.Exec(run.ctx, `
		INSERT INTO research_v6_outbox(id,workspace_id,session_id,kind,idempotency_key,payload)
		VALUES($1::uuid,$2::uuid,$3::uuid,'create_agent',$4,'{}'::jsonb)
	`, outboxID, run.fixture.workspaceID, run.fixture.sessionID, "material-effect:"+uuid.NewString()); err != nil {
		t.Fatal(err)
	}

	processed, err := run.store.ProcessV6EventTriggers(run.ctx, 32)
	if err != nil {
		t.Fatal(err)
	}
	if processed != 0 {
		t.Fatalf("processed %d event triggers while material outbox was pending", processed)
	}
	var cycles int
	if err = run.pool.QueryRow(run.ctx, `SELECT count(*)::int FROM research_director_cycle WHERE session_id=$1::uuid`, run.fixture.sessionID).Scan(&cycles); err != nil {
		t.Fatal(err)
	}
	if cycles != 0 {
		t.Fatalf("created %d Director cycles before material runtime effects settled", cycles)
	}

	if _, err = run.pool.Exec(run.ctx, `UPDATE research_v6_outbox SET status='delivered',updated_at=now() WHERE id=$1::uuid`, outboxID); err != nil {
		t.Fatal(err)
	}
	processed, err = run.store.ProcessV6EventTriggers(run.ctx, 32)
	if err != nil {
		t.Fatal(err)
	}
	if processed != 1 {
		t.Fatalf("processed %d event triggers after material outbox settled, want 1", processed)
	}
	var throughSequence int64
	if err = run.pool.QueryRow(run.ctx, `
		SELECT trigger_through_sequence FROM research_director_cycle
		WHERE session_id=$1::uuid ORDER BY created_at DESC LIMIT 1
	`, run.fixture.sessionID).Scan(&throughSequence); err != nil {
		t.Fatal(err)
	}
	if throughSequence < event.Sequence {
		t.Fatalf("Director Brief watermark %d does not include material request event %d", throughSequence, event.Sequence)
	}
}

func TestRejectedV6DirectorProposalTerminatesAndEmitsTrigger(t *testing.T) {
	run := newTransactionRecoveryRun(t, "Reject stale V6 Director proposal")
	membershipID, workItemID := seedV6RecoveryWorkItem(t, run, "running", time.Now().Add(time.Minute))
	if _, err := run.pool.Exec(run.ctx, `UPDATE research_work_item
		SET kind='director',client_key=$2 WHERE id=$1::uuid`, workItemID, "director-cycle:"+uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	attemptID := seedV6RecoveryAttempt(t, run, membershipID, workItemID)
	submissionID := uuid.NewString()
	if _, err := run.pool.Exec(run.ctx, `INSERT INTO research_v6_work_submission(
		id,workspace_id,session_id,work_item_id,attempt_id,client_request_id,contract_kind,content_hash,envelope,status
	) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,$6::uuid,'director_action_proposal',$7,'{}'::jsonb,'processing')`,
		submissionID, run.fixture.workspaceID, run.fixture.sessionID, workItemID, attemptID, uuid.NewString(),
		"sha256:"+strings.Repeat("7", 64)); err != nil {
		t.Fatal(err)
	}

	reason := ErrWorkItemChanged.Error()
	if err := run.store.rejectV6DirectorProposal(run.ctx, submissionID, reason); err != nil {
		t.Fatal(err)
	}
	var submissionStatus, outcomeError, attemptStatus, workStatus string
	if err := run.pool.QueryRow(run.ctx, `SELECT s.status,s.outcome->>'error',a.status,w.status
		FROM research_v6_work_submission s
		JOIN research_work_item_attempt a ON a.id=s.attempt_id
		JOIN research_work_item w ON w.id=s.work_item_id
		WHERE s.id=$1::uuid`, submissionID).Scan(&submissionStatus, &outcomeError, &attemptStatus, &workStatus); err != nil {
		t.Fatal(err)
	}
	if submissionStatus != "rejected" || outcomeError != reason || attemptStatus != "failed" || workStatus != "failed" {
		t.Fatalf("submission=%s outcome=%q attempt=%s work=%s", submissionStatus, outcomeError, attemptStatus, workStatus)
	}
	var rejectedEvents int
	if err := run.pool.QueryRow(run.ctx, `SELECT count(*)::int FROM research_run_event
		WHERE session_id=$1::uuid AND event_type='v6_work_submission_rejected'
		AND payload->>'submission_id'=$2::text`, run.fixture.sessionID, submissionID).Scan(&rejectedEvents); err != nil {
		t.Fatal(err)
	}
	if rejectedEvents != 1 {
		t.Fatalf("rejection events=%d want 1", rejectedEvents)
	}
}

func TestV6DirectorCreatedWorkStartsAtVersionOne(t *testing.T) {
	run := newTransactionRecoveryRun(t, "Create versioned V6 Work")
	if _, err := run.pool.Exec(run.ctx, `UPDATE research_session SET orchestrator_version='research-run-v6' WHERE id=$1::uuid`, run.fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := run.store.AssignV6Director(run.ctx, AssignV6DirectorInput{
		WorkspaceID: run.fixture.workspaceID, RunID: run.fixture.sessionID,
		AgentID: run.fixture.agentID, UserID: run.fixture.userID,
		Reason: "Create versioned work", ClientRequestID: uuid.NewString(), ExpectedStateVersion: 0,
	}); err != nil {
		t.Fatal(err)
	}
	var stateVersion, throughSequence int64
	if err := run.pool.QueryRow(run.ctx, `SELECT state_version,
		COALESCE((SELECT max(sequence) FROM research_run_event WHERE session_id=$1::uuid),0)
		FROM research_session WHERE id=$1::uuid`, run.fixture.sessionID).Scan(&stateVersion, &throughSequence); err != nil {
		t.Fatal(err)
	}
	cycle, err := (directorBriefModule{store: run.store}).Start(run.ctx, StartV6DirectorCycleInput{
		WorkspaceID: run.fixture.workspaceID, RunID: run.fixture.sessionID,
		TriggerKey: uuid.NewString(), FromSequence: throughSequence, ThroughSequence: throughSequence,
		ExpectedStateVersion: stateVersion, Now: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = run.pool.QueryRow(run.ctx, `SELECT state_version FROM research_session WHERE id=$1::uuid`, run.fixture.sessionID).Scan(&stateVersion); err != nil {
		t.Fatal(err)
	}
	actionPayload, err := json.Marshal(map[string]any{
		"kind": "research", "assignee_agent_id": run.fixture.agentID, "mission": "Investigate the assigned question",
		"expected_result_schema_id": "atomic_result_submission", "payload_schema_id": "research.test.v1",
		"payload":  map[string]any{"task_specific_schema": map[string]any{"type": "object"}},
		"priority": 0.5, "max_attempts": 1, "branch_ids": []string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	idempotencyKey := "create-versioned-work:" + uuid.NewString()
	if err = run.store.executeV6CreateWorkAction(run.ctx, v6DirectorProposal{
		WorkspaceID: run.fixture.workspaceID, RunID: run.fixture.sessionID,
	}, cycle.ID, v6DirectorAction{
		ActionID: uuid.NewString(), Kind: "create_work_item", IdempotencyKey: idempotencyKey,
		PayloadSchema: "work.create.v1", Payload: actionPayload,
	}, stateVersion); err != nil {
		t.Fatal(err)
	}
	var workStateVersion int64
	if err = run.pool.QueryRow(run.ctx, `SELECT state_version FROM research_work_item
		WHERE session_id=$1::uuid AND idempotency_key=$2`, run.fixture.sessionID, idempotencyKey).Scan(&workStateVersion); err != nil {
		t.Fatal(err)
	}
	if workStateVersion != 1 {
		t.Fatalf("created Work state_version=%d want 1", workStateVersion)
	}
}
