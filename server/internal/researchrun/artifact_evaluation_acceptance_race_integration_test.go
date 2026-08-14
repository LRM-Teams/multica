package researchrun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func setupEvaluationAcceptanceRaceFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool) acceptanceRaceFixture {
	t.Helper()
	fixture := seedResearchRunFixture(t, ctx, pool)
	store := NewPostgresStore(pool)
	run, _, err := store.InitializeRun(ctx, StartInput{
		SessionID: fixture.sessionID, WorkspaceID: fixture.workspaceID, FleetID: fixture.fleetID,
		CreatedBy: fixture.userID, LeadAgentID: fixture.agentID,
		Goal: "Evaluation acceptance race", Title: "Evaluation acceptance race",
		DepthTier: "standard", Language: "English",
	}, DefaultRunConfig("standard"))
	if err != nil {
		t.Fatal(err)
	}
	submitStoreTask(t, ctx, pool, store, fixture, "plan:1", e2eDeliveryPlan(), run.Config)
	evidence := upgradeResultToV5(e2eVerifiedEvidenceV4())
	evidence.AnswerClaimKey = "answer-claim"
	for index, key := range []string{"verify-1", "verify-2", "verify-3"} {
		evidence.ClientRequestID = fmt.Sprintf("evaluation-race-evidence-%d", index+1)
		submitStoreTask(t, ctx, pool, store, fixture, key, evidence, run.Config)
	}
	report := e2eStructuredReport(t, ctx, pool, fixture.sessionID)
	submitStoreTask(t, ctx, pool, store, fixture, "synthesize", ResultEnvelope{
		SchemaVersion: 5, ClientRequestID: "evaluation-race-report",
		Summary: "report", Confidence: 0.9, Report: &report,
	}, run.Config)
	if _, err = store.ActivateReadyTasks(ctx, fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	tasks, err := store.ListTasks(ctx, fixture.sessionID)
	if err != nil {
		t.Fatal(err)
	}
	var task Task
	for _, candidate := range tasks {
		if candidate.ClientKey == "quality" {
			task = candidate
			break
		}
	}
	if task.ID == "" || task.Status != TaskStatusReady {
		t.Fatalf("quality task is not ready: %+v", task)
	}
	attempt, _, err := store.CreateDispatchIntent(ctx, testDispatchIntentInput(
		t, ctx, store, fixture.sessionID, fixture.workspaceID, task.ID, fixture.validatorID,
	))
	if err != nil {
		t.Fatal(err)
	}
	inboxID := seedIntegrationInboxEvent(t, ctx, pool, fixture.workspaceID, fixture.validatorID)
	if _, _, err = store.AttachInboxTask(ctx, attempt.ID, inboxID); err != nil {
		t.Fatal(err)
	}
	evaluation := EvaluationProposal{
		Passed: true, FactualGrounding: 0.9, Coverage: 0.9, AnalyticalDepth: 0.9,
		SourceQuality: 0.9, ContradictionHandling: 0.9, InstructionAdherence: 0.9, Readability: 0.9,
		Findings:          []string{"The report passes the complete independent review."},
		DimensionFindings: e2eDimensionFindings(), ReviewedClaimKeys: []string{"answer-claim"},
		ReviewedSectionIDs: []string{"executive-summary", "method", "finding", "limitations", "conclusion"},
	}
	raw, err := json.Marshal(ResultEnvelope{
		SchemaVersion: 5, ClientRequestID: "evaluation-race-result",
		Summary: "quality evaluation", Confidence: 0.9, Evaluation: &evaluation,
	})
	if err != nil {
		t.Fatal(err)
	}
	validated, hash, err := DecodeAndValidateResultForVersion(run.OrchestratorVersion, raw, task, run.Config)
	if err != nil {
		t.Fatal(err)
	}
	return acceptanceRaceFixture{
		pool: pool, store: store, fixture: fixture, run: run, task: task,
		attempt: attempt, inboxID: inboxID,
		input: AcceptResultInput{
			SessionID: fixture.sessionID, AttemptID: attempt.ID, AgentID: fixture.validatorID,
			InboxTaskID: inboxID, Raw: raw, Result: validated, Hash: hash,
		},
	}
}

func seedRacingEvaluationForAttempt(t *testing.T, ctx context.Context, fx acceptanceRaceFixture) {
	t.Helper()
	var reportID string
	if err := fx.pool.QueryRow(ctx, `
		SELECT id::text FROM research_report
		WHERE workspace_id = $1::uuid AND session_id = $2::uuid
		ORDER BY revision DESC LIMIT 1
	`, fx.fixture.workspaceID, fx.run.SessionID).Scan(&reportID); err != nil {
		t.Fatal(err)
	}
	inputs, _ := json.Marshal(map[string]any{
		"task_id": fx.task.ID, "task_kind": fx.task.Kind, "report_id": reportID,
	})
	outcome, _ := json.Marshal(EvaluationProposal{Passed: false, Findings: []string{"competing review"}})
	rationale := "competing review"
	kind := artifactKindForDecision(string(fx.task.Kind))
	contentHash, err := ArtifactContentHash(kind, evaluationDecisionArtifactContent(
		fx.task.Kind, fx.fixture.validatorID, fx.run.GoalVersion,
		fx.run.PlanVersion, inputs, outcome, rationale,
	))
	if err != nil {
		t.Fatal(err)
	}
	tx, err := fx.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	decisionID := uuid.NewString()
	if _, err = tx.Exec(ctx, `
		INSERT INTO research_decision (
		  id, workspace_id, session_id, decision_kind, actor_type, actor_id,
		  goal_version, plan_version, inputs, outcome, rationale
		) VALUES ($1::uuid, $2::uuid, $3::uuid, $4, 'agent', $5::uuid,
		          $6, $7, $8::jsonb, $9::jsonb, $10)
	`, decisionID, fx.fixture.workspaceID, fx.run.SessionID, fx.task.Kind,
		fx.fixture.validatorID, fx.run.GoalVersion, fx.run.PlanVersion, inputs, outcome, rationale); err != nil {
		t.Fatal(err)
	}
	access, err := deriveManifestOutputAccessTx(
		ctx, tx, fx.fixture.workspaceID, fx.run.SessionID, fx.attempt.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err = registerArtifactPassportTx(ctx, tx, registerArtifactPassportInput{
		WorkspaceID: fx.fixture.workspaceID, SessionID: fx.run.SessionID, EntityID: decisionID,
		Kind: kind, SourceCreatedAt: timePtr(time.Now()),
		ProvenanceCompleteness: ArtifactProvenanceComplete,
		GoalVersion:            int32Ptr(int32(fx.run.GoalVersion)),
		PlanVersion:            int32Ptr(int32(fx.run.PlanVersion)),
		AccessLevel:            access, HashOrigin: ArtifactHashOriginProduction,
		ContentHash: contentHash, ProducedByAttemptID: fx.attempt.ID,
	}); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestAcceptResultRaceRejectsEvaluationOwnedByAttempt(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	fx := setupEvaluationAcceptanceRaceFixture(t, ctx, pool)
	defer cleanupResearchRunFixture(pool, fx.fixture)
	invokeAcceptWithBeforeCommitFault(t, ctx, fx)
	assertAcceptanceRolledBack(t, ctx, fx)
	seedRacingEvaluationForAttempt(t, ctx, fx)

	if _, err = fx.store.AcceptResult(ctx, fx.input); !errors.Is(err, ErrResultConflict) {
		t.Fatalf("AcceptResult after Evaluation race err=%v want ErrResultConflict", err)
	}
	assertAcceptanceRolledBack(t, ctx, fx)
	var evaluations int
	if err = pool.QueryRow(ctx, `
		SELECT count(*)::int FROM research_decision
		WHERE workspace_id = $1::uuid AND session_id = $2::uuid
		  AND decision_kind = 'quality_gate'
	`, fx.fixture.workspaceID, fx.run.SessionID).Scan(&evaluations); err != nil {
		t.Fatal(err)
	}
	if evaluations != 1 {
		t.Fatalf("evaluation count=%d want racing singleton", evaluations)
	}
}
