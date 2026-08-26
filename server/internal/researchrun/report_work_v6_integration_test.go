package researchrun

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestV6CreateReportActionFreezesServerOwnedReporterAndLatestInputs(t *testing.T) {
	seeded := seedReceivedV6AtomicSubmission(t, "Create report from server-owned inputs")
	run := seeded.run
	t.Cleanup(func() { cleanupAcceptedV6ResultProducerBinding(t, seeded) })

	applied, err := run.store.ApplyReceivedV6Submissions(run.ctx, 1)
	if err != nil || applied != 1 {
		t.Fatalf("apply atomic result: applied=%d err=%v", applied, err)
	}
	var directorStateVersion int64
	if err = run.pool.QueryRow(run.ctx, `SELECT director_state_version FROM research_session WHERE id=$1::uuid`, run.fixture.sessionID).Scan(&directorStateVersion); err != nil {
		t.Fatal(err)
	}
	if _, err = run.store.AssignV6Director(run.ctx, AssignV6DirectorInput{
		WorkspaceID: run.fixture.workspaceID, RunID: run.fixture.sessionID,
		AgentID: run.fixture.agentID, UserID: run.fixture.userID,
		Reason: "Coordinate report creation", ClientRequestID: uuid.NewString(), ExpectedStateVersion: directorStateVersion,
	}); err != nil {
		t.Fatal(err)
	}
	reporterMission := "Maintain the report"
	reporterHash := ArtifactContentHashFromCanonicalJSON([]byte(`{"mission":"Maintain the report"}`))
	if _, err = run.pool.Exec(run.ctx, `INSERT INTO research_team_membership(
		id,workspace_id,session_id,agent_id,membership_generation,mission_prompt,mission_hash,mission_revision,state,role
	) VALUES(gen_random_uuid(),$1::uuid,$2::uuid,$3::uuid,1,$4,$5,1,'idle','reporter')`,
		run.fixture.workspaceID, run.fixture.sessionID, run.fixture.reporterID, reporterMission, reporterHash); err != nil {
		t.Fatal(err)
	}

	var stateVersion, briefThroughSequence int64
	if err = run.pool.QueryRow(run.ctx, `SELECT state_version,COALESCE((SELECT max(sequence) FROM research_run_event WHERE session_id=$1::uuid),0)
		FROM research_session WHERE id=$1::uuid`, run.fixture.sessionID).Scan(&stateVersion, &briefThroughSequence); err != nil {
		t.Fatal(err)
	}
	cycle, err := (directorBriefModule{store: run.store, compiler: contextCompilerModule{}}).Start(run.ctx, StartV6DirectorCycleInput{
		WorkspaceID: run.fixture.workspaceID, RunID: run.fixture.sessionID,
		TriggerKey: uuid.NewString(), FromSequence: briefThroughSequence, ThroughSequence: briefThroughSequence,
		ExpectedStateVersion: stateVersion, Now: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}

	tx, err := run.pool.Begin(run.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(run.ctx)
	if err = lockRunForMutation(run.ctx, tx, run.fixture.sessionID, run.fixture.workspaceID); err != nil {
		t.Fatal(err)
	}
	if _, err = appendEvent(run.ctx, tx, run.fixture.workspaceID, run.fixture.sessionID, "v6_test_report_input_advanced", "report-input-advanced:"+uuid.NewString(), "system", "", map[string]any{"reason": "accepted result arrived after brief"}); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(run.ctx); err != nil {
		t.Fatal(err)
	}

	payload, err := json.Marshal(map[string]any{"title": "阶段性调研报告"})
	if err != nil {
		t.Fatal(err)
	}
	idempotencyKey := "server-owned-report:" + uuid.NewString()
	if err = run.store.executeV6CreateReportAction(run.ctx, v6DirectorProposal{
		WorkspaceID: run.fixture.workspaceID, RunID: run.fixture.sessionID,
	}, cycle.ID, v6DirectorAction{
		ActionID: uuid.NewString(), Kind: "create_report", IdempotencyKey: idempotencyKey,
		PayloadSchema: "report.create.v1", Payload: payload, Reason: "Refresh the phase report",
	}, run.goalVersion, stateVersion); err != nil {
		t.Fatal(err)
	}

	var assignedAgentID, reportID string
	var frozenEventSequence int64
	if err = run.pool.QueryRow(run.ctx, `SELECT assigned_agent_id::text,target_id::text,input_event_sequence
		FROM research_work_item WHERE session_id=$1::uuid AND idempotency_key=$2`, run.fixture.sessionID, idempotencyKey).Scan(&assignedAgentID, &reportID, &frozenEventSequence); err != nil {
		t.Fatal(err)
	}
	if assignedAgentID != run.fixture.reporterID {
		t.Fatalf("report assigned Agent=%s want server-owned reporter %s", assignedAgentID, run.fixture.reporterID)
	}
	if frozenEventSequence <= briefThroughSequence {
		t.Fatalf("report event watermark=%d want newer than frozen Brief %d", frozenEventSequence, briefThroughSequence)
	}
	var selectedVersionID string
	if err = run.pool.QueryRow(run.ctx, `SELECT node_artifact_version_id::text FROM research_report_input WHERE report_id=$1::uuid`, reportID).Scan(&selectedVersionID); err != nil {
		t.Fatal(err)
	}
	var acceptedVersionID string
	if err = run.pool.QueryRow(run.ctx, `SELECT artifact_version_id::text FROM research_result_node WHERE work_item_attempt_id=$1::uuid`, seeded.attemptID).Scan(&acceptedVersionID); err != nil {
		t.Fatal(err)
	}
	if selectedVersionID != acceptedVersionID {
		t.Fatalf("report input=%s want latest accepted version %s", selectedVersionID, acceptedVersionID)
	}
}
