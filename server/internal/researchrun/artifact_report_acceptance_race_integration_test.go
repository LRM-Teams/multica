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

func setupReportAcceptanceRaceFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool) acceptanceRaceFixture {
	t.Helper()
	fixture := seedResearchRunFixture(t, ctx, pool)
	store := NewPostgresStore(pool)
	run, _, err := store.InitializeRun(ctx, StartInput{
		SessionID: fixture.sessionID, WorkspaceID: fixture.workspaceID, FleetID: fixture.fleetID,
		CreatedBy: fixture.userID, LeadAgentID: fixture.agentID,
		Goal: "Report acceptance race", Title: "Report acceptance race",
		DepthTier: "standard", Language: "English",
	}, DefaultRunConfig("standard"))
	if err != nil {
		t.Fatal(err)
	}
	submitStoreTask(t, ctx, pool, store, fixture, "plan:1", e2eDeliveryPlan(), run.Config)
	evidence := upgradeResultToV5(e2eVerifiedEvidenceV4())
	evidence.AnswerClaimKey = "answer-claim"
	for index, key := range []string{"verify-1", "verify-2", "verify-3"} {
		evidence.ClientRequestID = fmt.Sprintf("report-race-evidence-%d", index+1)
		submitStoreTask(t, ctx, pool, store, fixture, key, evidence, run.Config)
	}
	if _, err = store.ActivateReadyTasks(ctx, fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	tasks, err := store.ListTasks(ctx, fixture.sessionID)
	if err != nil {
		t.Fatal(err)
	}
	var task Task
	for _, candidate := range tasks {
		if candidate.ClientKey == "synthesize" {
			task = candidate
			break
		}
	}
	if task.ID == "" || task.Status != TaskStatusReady {
		t.Fatalf("synthesize task is not ready: %+v", task)
	}
	attempt, _, err := store.CreateDispatchIntent(ctx, testDispatchIntentInput(
		t, ctx, store, fixture.sessionID, fixture.workspaceID, task.ID, fixture.reporterID,
	))
	if err != nil {
		t.Fatal(err)
	}
	inboxID := seedIntegrationInboxEvent(t, ctx, pool, fixture.workspaceID, fixture.reporterID)
	if _, _, err = store.AttachInboxTask(ctx, attempt.ID, inboxID); err != nil {
		t.Fatal(err)
	}
	report := e2eStructuredReport(t, ctx, pool, fixture.sessionID)
	raw, err := json.Marshal(ResultEnvelope{
		SchemaVersion: 5, ClientRequestID: "report-race-result",
		Summary: "report", Confidence: 0.9, Report: &report,
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
			SessionID: fixture.sessionID, AttemptID: attempt.ID, AgentID: fixture.reporterID,
			InboxTaskID: inboxID, Raw: raw, Result: validated, Hash: hash,
		},
	}
}

func seedRacingReportForAttempt(t *testing.T, ctx context.Context, fx acceptanceRaceFixture) string {
	t.Helper()
	tx, err := fx.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	reportID := uuid.NewString()
	structured := json.RawMessage(`{}`)
	content := "competing report for the same Attempt"
	if _, err = tx.Exec(ctx, `
		INSERT INTO research_report (
		  id, workspace_id, session_id, revision, content_md, structured,
		  goal_version, plan_version, produced_by_task_id, produced_by_attempt_id, author_agent_id
		) VALUES ($1::uuid, $2::uuid, $3::uuid, 1, $4, $5::jsonb,
		          $6, $7, $8::uuid, $9::uuid, $10::uuid)
	`, reportID, fx.fixture.workspaceID, fx.run.SessionID, content, structured,
		fx.run.GoalVersion, fx.run.PlanVersion, fx.task.ID, fx.attempt.ID, fx.fixture.reporterID); err != nil {
		t.Fatal(err)
	}
	reportHash, err := ArtifactContentHash(ArtifactKindReportRevision, map[string]any{
		"revision": 1, "content_md": content, "structured": structured,
		"goal_version": fx.run.GoalVersion, "plan_version": fx.run.PlanVersion,
		"produced_by_task_id": fx.task.ID, "produced_by_attempt_id": fx.attempt.ID,
		"author_agent_id": fx.fixture.reporterID,
	})
	if err != nil {
		t.Fatal(err)
	}
	access, err := deriveManifestOutputAccessTx(
		ctx, tx, fx.fixture.workspaceID, fx.run.SessionID, fx.attempt.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err = registerArtifactPassportTx(ctx, tx, registerArtifactPassportInput{
		WorkspaceID: fx.fixture.workspaceID, SessionID: fx.run.SessionID, EntityID: reportID,
		Kind: ArtifactKindReportRevision, SourceCreatedAt: timePtr(time.Now()),
		ProvenanceCompleteness: ArtifactProvenanceComplete,
		GoalVersion:            int32Ptr(int32(fx.run.GoalVersion)),
		PlanVersion:            int32Ptr(int32(fx.run.PlanVersion)),
		AccessLevel:            access, HashOrigin: ArtifactHashOriginProduction,
		ContentHash: reportHash, ProducedByAttemptID: fx.attempt.ID,
	}); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return reportID
}

func TestAcceptResultRaceRejectsReportOwnedByAttempt(t *testing.T) {
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
	fx := setupReportAcceptanceRaceFixture(t, ctx, pool)
	defer cleanupResearchRunFixture(pool, fx.fixture)
	invokeAcceptWithBeforeCommitFault(t, ctx, fx)
	assertAcceptanceRolledBack(t, ctx, fx)
	seedRacingReportForAttempt(t, ctx, fx)

	if _, err = fx.store.AcceptResult(ctx, fx.input); !errors.Is(err, ErrResultConflict) {
		t.Fatalf("AcceptResult after Report race err=%v want ErrResultConflict", err)
	}
	assertAcceptanceRolledBack(t, ctx, fx)
	var reports int
	if err = pool.QueryRow(ctx, `
		SELECT count(*)::int FROM research_report
		WHERE workspace_id = $1::uuid AND session_id = $2::uuid
	`, fx.fixture.workspaceID, fx.run.SessionID).Scan(&reports); err != nil {
		t.Fatal(err)
	}
	if reports != 1 {
		t.Fatalf("report count=%d want racing singleton", reports)
	}
}
