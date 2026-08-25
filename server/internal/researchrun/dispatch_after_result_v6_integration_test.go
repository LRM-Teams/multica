package researchrun

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestPrepareV6DispatchAfterAcceptedAtomicResult(t *testing.T) {
	seeded := seedReceivedV6AtomicSubmission(t, "Dispatch after accepted V6 atomic result")
	run := seeded.run
	t.Cleanup(func() { cleanupAcceptedV6ResultProducerBinding(t, seeded) })

	applied, err := run.store.ApplyReceivedV6Submissions(run.ctx, 4)
	if err != nil || applied != 1 {
		t.Fatalf("apply submission: applied=%d err=%v", applied, err)
	}

	workItemID := uuid.NewString()
	payload := `{"mission_prompt":"继续核验独立来源。","task_kind":"research","task_specific_schema":{"type":"object","required":["findings"],"properties":{"findings":{"type":"array","items":{"type":"object"}}},"additionalProperties":false}}`
	if _, err = run.pool.Exec(run.ctx, `INSERT INTO research_work_item(
		id,workspace_id,session_id,kind,status,assigned_agent_id,goal_version,idempotency_key,
		input_state_version,input_event_sequence,priority,max_attempts,payload_schema_id,
		expected_result_schema_id,payload,state_version,ready_at,reason
	) SELECT $1::uuid,workspace_id,id,'research','ready',$2::uuid,goal_version,$3,
		state_version,COALESCE((SELECT max(sequence) FROM research_run_event WHERE session_id=id),0),
		0.9,2,'research.findings.v1','atomic_result_submission',$4::jsonb,1,now(),
		'继续核验独立来源。' FROM research_session WHERE id=$5::uuid`,
		workItemID, run.fixture.agentID, "dispatch-after-result:"+workItemID, payload, run.fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	if _, err = run.pool.Exec(run.ctx, `INSERT INTO research_v6_work_item_branch(
		workspace_id,session_id,work_item_id,branch_id
	) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid)`, run.fixture.workspaceID, run.fixture.sessionID, workItemID, seeded.branchID); err != nil {
		t.Fatal(err)
	}

	prepared, err := run.store.PrepareV6Dispatches(run.ctx, 1)
	if err != nil || prepared != 1 {
		t.Fatalf("prepare next Work after accepted result: prepared=%d err=%v", prepared, err)
	}
	var status string
	var attempts int
	if err = run.pool.QueryRow(run.ctx, `SELECT w.status,
		(SELECT count(*)::int FROM research_work_item_attempt a WHERE a.work_item_id=w.id)
		FROM research_work_item w WHERE w.id=$1::uuid`, workItemID).Scan(&status, &attempts); err != nil {
		t.Fatal(err)
	}
	if status != "dispatching" || attempts != 1 {
		t.Fatalf("Work status=%q attempts=%d want dispatching/1", status, attempts)
	}
}

func TestV6DirectorRejectsSecondActiveAtomicWorkForSameAgent(t *testing.T) {
	run := newTransactionRecoveryRun(t, "Reject multiple active Work Items per Agent")
	if _, err := run.pool.Exec(run.ctx, `UPDATE research_session SET orchestrator_version='research-run-v6' WHERE id=$1::uuid`, run.fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := run.store.AssignV6Director(run.ctx, AssignV6DirectorInput{
		WorkspaceID: run.fixture.workspaceID, RunID: run.fixture.sessionID,
		AgentID: run.fixture.agentID, UserID: run.fixture.userID,
		Reason: "Coordinate parallel research", ClientRequestID: uuid.NewString(), ExpectedStateVersion: 0,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := run.store.AddV6TeamMember(run.ctx, AddV6TeamMemberInput{
		WorkspaceID: run.fixture.workspaceID, RunID: run.fixture.sessionID,
		AgentID: run.fixture.reporterID, MissionPrompt: "核验一个独立方向。",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := run.pool.Exec(run.ctx, `INSERT INTO research_work_item(
		id,workspace_id,session_id,kind,status,assigned_agent_id,goal_version,idempotency_key,
		payload_schema_id,expected_result_schema_id,payload,state_version,ready_at
	) VALUES($1::uuid,$2::uuid,$3::uuid,'research','ready',$4::uuid,1,$5,
		'research.first.v1','atomic_result_submission','{}'::jsonb,1,now())`,
		uuid.NewString(), run.fixture.workspaceID, run.fixture.sessionID, run.fixture.reporterID, "first-active:"+uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	var stateVersion int64
	if err := run.pool.QueryRow(run.ctx, `SELECT state_version FROM research_session WHERE id=$1::uuid`, run.fixture.sessionID).Scan(&stateVersion); err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(map[string]any{
		"kind": "research", "assignee_agent_id": run.fixture.reporterID,
		"mission": "核验第二个独立方向。", "expected_result_schema_id": "atomic_result_submission",
		"payload_schema_id": "research.second.v1",
		"payload":           map[string]any{"task_specific_schema": map[string]any{"type": "object"}},
		"priority":          0.9, "max_attempts": 2, "branch_ids": []string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	err = run.store.executeV6CreateWorkAction(run.ctx, v6DirectorProposal{
		WorkspaceID: run.fixture.workspaceID, RunID: run.fixture.sessionID,
	}, uuid.NewString(), v6DirectorAction{
		ActionID: uuid.NewString(), Kind: "create_work_item", IdempotencyKey: "second-active:" + uuid.NewString(),
		PayloadSchema: "work.create.v1", Payload: payload,
	}, stateVersion)
	if !errors.Is(err, ErrInvalidContract) || !strings.Contains(err.Error(), "独立调研方向必须分配给不同") {
		t.Fatalf("second active Work error=%v", err)
	}
}

func TestPrepareV6DispatchQuarantinesInvalidHeadWithoutStarvingHealthyWork(t *testing.T) {
	run := newTransactionRecoveryRun(t, "Quarantine invalid V6 dispatch head")
	if _, err := run.pool.Exec(run.ctx, `UPDATE research_session SET orchestrator_version='research-run-v6' WHERE id=$1::uuid`, run.fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	membershipID, invalidWorkID := seedV6RecoveryWorkItem(t, run, "ready", time.Now().Add(time.Hour))
	invalidBranchID := seedV6WorkBranchScope(t, run, invalidWorkID, "invalid-dispatch:", "Invalid frozen task", 1)
	if _, err := run.pool.Exec(run.ctx, `UPDATE research_team_membership SET state='idle' WHERE id=$1::uuid`, membershipID); err != nil {
		t.Fatal(err)
	}
	if _, err := run.pool.Exec(run.ctx, `UPDATE research_work_item SET
		priority=1,ready_at=now()-interval '2 minutes',expected_result_schema_id='atomic_result_submission',
		payload_schema_id='research.invalid.v1',payload='{"artifact_version_ids":["not-a-uuid"]}'::jsonb,reason='invalid frozen task'
		WHERE id=$1::uuid`, invalidWorkID); err != nil {
		t.Fatal(err)
	}

	healthyWorkID := uuid.NewString()
	healthyPayload := `{"mission_prompt":"核验健康任务。","task_kind":"research","task_specific_schema":{"type":"object","required":["findings"],"properties":{"findings":{"type":"array","items":{"type":"object"}}},"additionalProperties":false}}`
	if _, err := run.pool.Exec(run.ctx, `INSERT INTO research_work_item(
		id,workspace_id,session_id,kind,status,assigned_agent_id,goal_version,idempotency_key,
		input_state_version,input_event_sequence,priority,max_attempts,payload_schema_id,
		expected_result_schema_id,payload,state_version,ready_at,reason
	) SELECT $1::uuid,workspace_id,id,'research','ready',$2::uuid,goal_version,$3,
		state_version,COALESCE((SELECT max(sequence) FROM research_run_event WHERE session_id=id),0),
		0.9,2,'research.findings.v1','atomic_result_submission',$4::jsonb,1,now()-interval '1 minute',
		'核验健康任务。' FROM research_session WHERE id=$5::uuid`,
		healthyWorkID, run.fixture.agentID, "healthy-dispatch:"+healthyWorkID, healthyPayload, run.fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := run.pool.Exec(run.ctx, `INSERT INTO research_v6_work_item_branch(
		workspace_id,session_id,work_item_id,branch_id
	) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid)`, run.fixture.workspaceID, run.fixture.sessionID, healthyWorkID, invalidBranchID); err != nil {
		t.Fatal(err)
	}

	prepared, err := run.store.PrepareV6Dispatches(run.ctx, 2)
	if err != nil {
		t.Fatalf("prepare dispatches: %v", err)
	}
	if prepared != 1 {
		t.Fatalf("prepared=%d want healthy Work only", prepared)
	}
	var invalidStatus, invalidReason, healthyStatus string
	if err = run.pool.QueryRow(run.ctx, `SELECT invalid.status,invalid.terminal_reason_code,healthy.status
		FROM research_work_item invalid CROSS JOIN research_work_item healthy
		WHERE invalid.id=$1::uuid AND healthy.id=$2::uuid`, invalidWorkID, healthyWorkID).Scan(&invalidStatus, &invalidReason, &healthyStatus); err != nil {
		t.Fatal(err)
	}
	if invalidStatus != "failed" || invalidReason != "contract_rejected" || healthyStatus != "dispatching" {
		t.Fatalf("invalid=%s/%s healthy=%s", invalidStatus, invalidReason, healthyStatus)
	}
}

func TestRecoverV6WorkRejectsSurplusReadyQueueForOneAgent(t *testing.T) {
	run := newTransactionRecoveryRun(t, "Recover oversubscribed V6 Agent queue")
	if _, err := run.pool.Exec(run.ctx, `UPDATE research_session SET orchestrator_version='research-run-v6' WHERE id=$1::uuid`, run.fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	_, firstWorkID := seedV6RecoveryWorkItem(t, run, "ready", time.Now().Add(time.Hour))
	secondWorkID := uuid.NewString()
	if _, err := run.pool.Exec(run.ctx, `UPDATE research_work_item SET ready_at=now()-interval '2 minutes',priority=0.9 WHERE id=$1::uuid`, firstWorkID); err != nil {
		t.Fatal(err)
	}
	if _, err := run.pool.Exec(run.ctx, `INSERT INTO research_work_item(
		id,workspace_id,session_id,kind,status,assigned_agent_id,goal_version,idempotency_key,
		priority,max_attempts,payload_schema_id,expected_result_schema_id,payload,state_version,ready_at
	) VALUES($1::uuid,$2::uuid,$3::uuid,'research','ready',$4::uuid,1,$5,
		0.8,2,'research.second.v1','atomic_result_submission','{}'::jsonb,1,now()-interval '1 minute')`,
		secondWorkID, run.fixture.workspaceID, run.fixture.sessionID, run.fixture.agentID, "oversubscribed:"+secondWorkID); err != nil {
		t.Fatal(err)
	}

	recovered, err := run.store.RecoverExpiredV6WorkItems(run.ctx, 10)
	if err != nil {
		t.Fatalf("recover oversubscribed queue: %v", err)
	}
	if recovered != 1 {
		t.Fatalf("recovered=%d want 1 surplus Work", recovered)
	}
	var firstStatus, secondStatus, secondReason string
	if err = run.pool.QueryRow(run.ctx, `SELECT first.status,second.status,second.terminal_reason_code
		FROM research_work_item first CROSS JOIN research_work_item second
		WHERE first.id=$1::uuid AND second.id=$2::uuid`, firstWorkID, secondWorkID).Scan(&firstStatus, &secondStatus, &secondReason); err != nil {
		t.Fatal(err)
	}
	if firstStatus != "ready" || secondStatus != "failed" || secondReason != "contract_rejected" {
		t.Fatalf("first=%s second=%s/%s", firstStatus, secondStatus, secondReason)
	}
	var eventCount int
	if err = run.pool.QueryRow(run.ctx, `SELECT count(*)::int FROM research_run_event
		WHERE session_id=$1::uuid AND event_type='v6_work_item_recovered'
		AND payload->>'work_item_id'=$2 AND payload->>'recovery_kind'='agent_active_work_conflict'`,
		run.fixture.sessionID, secondWorkID).Scan(&eventCount); err != nil {
		t.Fatal(err)
	}
	if eventCount != 1 {
		t.Fatalf("recovery events=%d want 1", eventCount)
	}
}
