package researchrun

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type supersessionFixture struct {
	input           SupersedeArtifactInput
	successorID     string
	supersededID    string
	beforeRevision  int64
	beforeWatermark int64
}

func seedSupersessionFixture(t *testing.T, run *transactionRecoveryRun) supersessionFixture {
	t.Helper()
	successorID := uuid.NewString()
	supersededID := uuid.NewString()
	decisionID := uuid.NewString()
	seedIntegrationClaimArtifact(t, run.ctx, run.pool, run.fixture.workspaceID, run.fixture.sessionID,
		successorID, "successor-"+successorID, "successor claim")
	seedIntegrationClaimArtifact(t, run.ctx, run.pool, run.fixture.workspaceID, run.fixture.sessionID,
		supersededID, "superseded-"+supersededID, "superseded claim")
	seedIntegrationSupersessionDecision(t, run.ctx, run.pool, run.fixture.workspaceID, run.fixture.sessionID,
		run.fixture.userID, decisionID)
	var successorVersionID, supersededVersionID string
	if err := run.pool.QueryRow(run.ctx, `
		SELECT id::text FROM research_artifact_version
		WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND artifact_id=$3::uuid AND version=1
	`, run.fixture.workspaceID, run.fixture.sessionID, successorID).Scan(&successorVersionID); err != nil {
		t.Fatal(err)
	}
	if err := run.pool.QueryRow(run.ctx, `
		SELECT id::text FROM research_artifact_version
		WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND artifact_id=$3::uuid AND version=1
	`, run.fixture.workspaceID, run.fixture.sessionID, supersededID).Scan(&supersededVersionID); err != nil {
		t.Fatal(err)
	}
	fixture := supersessionFixture{
		input: SupersedeArtifactInput{
			WorkspaceID: run.fixture.workspaceID, SessionID: run.fixture.sessionID,
			SuccessorVersionID: successorVersionID, SupersededVersionID: supersededVersionID,
			DecisionID: decisionID, Reason: "new evidence refines the claim",
		},
		successorID: successorID, supersededID: supersededID,
	}
	if err := run.pool.QueryRow(run.ctx, `
		SELECT p.eligibility_revision, state.watermark
		FROM research_artifact_passport p
		JOIN research_artifact_policy_state state
		 ON state.workspace_id=p.workspace_id AND state.session_id=p.session_id
		WHERE p.workspace_id=$1::uuid AND p.session_id=$2::uuid AND p.id=$3::uuid
	`, run.fixture.workspaceID, run.fixture.sessionID, supersededID).Scan(
		&fixture.beforeRevision, &fixture.beforeWatermark,
	); err != nil {
		t.Fatal(err)
	}
	return fixture
}

func seedIntegrationSupersessionDecision(
	t *testing.T, ctx context.Context, pool *pgxpool.Pool,
	workspaceID, sessionID, userID, decisionID string,
) {
	t.Helper()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `
		INSERT INTO research_decision (
		 id,workspace_id,session_id,decision_kind,actor_type,actor_id,
		 goal_version,plan_version,inputs,outcome,rationale
		) VALUES ($1::uuid,$2::uuid,$3::uuid,'artifact_supersession','user',$4::uuid,1,1,'{}','{}','supersession audit')
	`, decisionID, workspaceID, sessionID, userID); err != nil {
		t.Fatal(err)
	}
	goalVersion, planVersion := 1, 1
	backfillIntegrationArtifactPassport(t, ctx, tx, workspaceID, sessionID, decisionID,
		string(ArtifactKindEvaluationDecision), &goalVersion, &planVersion)
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}

func assertSupersessionState(t *testing.T, run *transactionRecoveryRun, fixture supersessionFixture, committed bool) {
	t.Helper()
	wantLifecycle := ArtifactLifecycleRegistered
	wantRevision := fixture.beforeRevision
	wantWatermark := fixture.beforeWatermark
	wantEdges := 0
	if committed {
		wantLifecycle = ArtifactLifecycleSuperseded
		wantRevision++
		wantWatermark++
		wantEdges = 1
	}
	var lifecycle ArtifactLifecycleStatus
	var revision, watermark int64
	var edges, mutations int
	if err := run.pool.QueryRow(run.ctx, `
		SELECT p.lifecycle_status,p.eligibility_revision,state.watermark,
		 (SELECT count(*)::int FROM research_artifact_supersession edge
		  WHERE edge.workspace_id=p.workspace_id AND edge.session_id=p.session_id
		    AND edge.superseded_artifact_id=p.id),
		 (SELECT count(*)::int FROM research_artifact_policy_mutation mutation
		  WHERE mutation.workspace_id=p.workspace_id AND mutation.session_id=p.session_id
		    AND mutation.artifact_id=p.id AND mutation.mutation_kind='supersession')
		FROM research_artifact_passport p
		JOIN research_artifact_policy_state state
		 ON state.workspace_id=p.workspace_id AND state.session_id=p.session_id
		WHERE p.workspace_id=$1::uuid AND p.session_id=$2::uuid AND p.id=$3::uuid
	`, run.fixture.workspaceID, run.fixture.sessionID, fixture.supersededID).Scan(
		&lifecycle, &revision, &watermark, &edges, &mutations,
	); err != nil {
		t.Fatal(err)
	}
	if lifecycle != wantLifecycle || revision != wantRevision || watermark != wantWatermark || edges != wantEdges || mutations != wantEdges {
		t.Fatalf("supersession state=%q/%d/%d/%d/%d want=%q/%d/%d/%d/%d",
			lifecycle, revision, watermark, edges, mutations,
			wantLifecycle, wantRevision, wantWatermark, wantEdges, wantEdges)
	}
}

func TestSupersedeArtifactPersistsLineageExcludesContextAndPreservesHistory(t *testing.T) {
	run := newTransactionRecoveryRun(t, "Supersession production")
	fixture := seedSupersessionFixture(t, run)
	receipt, err := run.store.SupersedeArtifact(run.ctx, fixture.input)
	if err != nil {
		t.Fatalf("SupersedeArtifact: %v", err)
	}
	if receipt.ID == "" || receipt.SupersededArtifactID != fixture.supersededID ||
		receipt.OldEligibilityRevision != fixture.beforeRevision ||
		receipt.NewEligibilityRevision != fixture.beforeRevision+1 ||
		receipt.PolicyWatermark != fixture.beforeWatermark+1 {
		t.Fatalf("unexpected supersession receipt: %+v", receipt)
	}
	assertSupersessionState(t, run, fixture, true)
	replayed, err := run.store.SupersedeArtifact(run.ctx, fixture.input)
	if err != nil || replayed.ID != receipt.ID || replayed.PolicyWatermark != receipt.PolicyWatermark {
		t.Fatalf("supersession replay=%+v err=%v want=%+v", replayed, err, receipt)
	}
	assertSupersessionState(t, run, fixture, true)

	tx, err := run.pool.Begin(run.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(run.ctx)
	plan, err := NewArtifactContextModule().PlanDispatchManifest(
		run.ctx, tx, run.fixture.workspaceID, run.fixture.sessionID, 1,
	)
	if err != nil {
		t.Fatal(err)
	}
	foundSuccessor, foundSupersededOmission := false, false
	for _, entry := range plan.Entries {
		foundSuccessor = foundSuccessor || entry.ArtifactID == fixture.successorID
		if entry.ArtifactID == fixture.supersededID {
			t.Fatal("superseded artifact appeared in ordinary context")
		}
	}
	for _, omission := range plan.Omissions {
		foundSupersededOmission = foundSupersededOmission ||
			(omission.ArtifactID == fixture.supersededID && omission.OmissionReason == "lifecycle")
	}
	if !foundSuccessor || !foundSupersededOmission {
		t.Fatalf("context successor=%v superseded omission=%v", foundSuccessor, foundSupersededOmission)
	}
	var claims, versions int
	if err = run.pool.QueryRow(run.ctx, `SELECT count(*)::int FROM research_claim WHERE id=$1::uuid`, fixture.supersededID).Scan(&claims); err != nil {
		t.Fatal(err)
	}
	if err = run.pool.QueryRow(run.ctx, `SELECT count(*)::int FROM research_artifact_version WHERE artifact_id=$1::uuid`, fixture.supersededID).Scan(&versions); err != nil {
		t.Fatal(err)
	}
	if claims != 1 || versions != 1 {
		t.Fatalf("supersession deleted history claims=%d versions=%d", claims, versions)
	}
	if _, err = run.pool.Exec(run.ctx, `DELETE FROM research_artifact_supersession WHERE id=$1::uuid`, receipt.ID); err == nil {
		t.Fatal("append-only supersession accepted delete")
	}
}

func TestSupersedeArtifactTransactionRecovery(t *testing.T) {
	runTransactionRecoveryMatrix(t, txOpArtifactSupersede, func(t *testing.T, run *transactionRecoveryRun) transactionRecoveryOperation {
		fixture := seedSupersessionFixture(t, run)
		invoke := func() error {
			_, err := run.store.SupersedeArtifact(run.ctx, fixture.input)
			return err
		}
		return transactionRecoveryOperation{
			invoke:           invoke,
			assertRolledBack: func() { assertSupersessionState(t, run, fixture, false) },
			assertCommitted:  func() { assertSupersessionState(t, run, fixture, true) },
			recover:          invoke,
		}
	})
}

func TestSupersedeArtifactBlocksAffectedInFlightAcceptance(t *testing.T) {
	run := newTransactionRecoveryRun(t, "Supersession in-flight acceptance")
	fixture := seedSupersessionFixture(t, run)
	input := testDispatchIntentInput(t, run.ctx, run.store, run.fixture.sessionID,
		run.fixture.workspaceID, run.taskID, run.fixture.agentID)
	attempt, _, err := run.store.CreateDispatchIntent(run.ctx, input)
	if err != nil {
		t.Fatalf("CreateDispatchIntent: %v", err)
	}
	inboxID := seedIntegrationInboxEvent(t, run.ctx, run.pool, run.fixture.workspaceID, run.fixture.agentID)
	if _, _, err = run.store.AttachInboxTask(run.ctx, attempt.ID, inboxID); err != nil {
		t.Fatalf("AttachInboxTask: %v", err)
	}
	if _, err = run.store.SupersedeArtifact(run.ctx, fixture.input); err != nil {
		t.Fatalf("SupersedeArtifact: %v", err)
	}
	tasks, err := run.store.ListTasks(run.ctx, run.fixture.sessionID, run.fixture.workspaceID)
	if err != nil || len(tasks) != 1 {
		t.Fatalf("ListTasks: %v len=%d", err, len(tasks))
	}
	raw, err := json.Marshal(upgradeResultToV5(validV4PlanResult(t)))
	if err != nil {
		t.Fatal(err)
	}
	result, hash, err := DecodeAndValidateResultForVersion(
		OrchestratorVersion, raw, tasks[0], DefaultRunConfig("standard"),
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = run.store.AcceptResult(run.ctx, AcceptResultInput{
		SessionID: run.fixture.sessionID, AttemptID: attempt.ID, AgentID: run.fixture.agentID,
		InboxTaskID: inboxID, Raw: raw, Result: result, Hash: hash,
	})
	if !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("AcceptResult err=%v want ErrInvalidTransition", err)
	}
	var resultCount int
	if err = run.pool.QueryRow(run.ctx, `SELECT count(*)::int FROM research_result_artifact WHERE attempt_id=$1::uuid`, attempt.ID).Scan(&resultCount); err != nil {
		t.Fatal(err)
	}
	if resultCount != 0 {
		t.Fatalf("stale in-flight acceptance persisted %d results", resultCount)
	}
}
