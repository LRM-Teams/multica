package researchrun

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type evidenceLinkRevisionFixture struct {
	acceptanceRaceFixture
	linkID string
}

func setupEvidenceLinkRevisionFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool) evidenceLinkRevisionFixture {
	t.Helper()
	fixture := seedResearchRunFixture(t, ctx, pool)
	store := NewPostgresStore(pool)
	run, _, err := store.InitializeRun(ctx, StartInput{
		SessionID: fixture.sessionID, WorkspaceID: fixture.workspaceID,
		FleetID: fixture.fleetID, CreatedBy: fixture.userID, LeadAgentID: fixture.agentID,
		Goal: "Evidence Link revision", Title: "Evidence Link revision",
		DepthTier: "standard", Language: "English",
	}, DefaultRunConfig("standard"))
	if err != nil {
		t.Fatal(err)
	}
	submitStoreTask(t, ctx, pool, store, fixture, "plan:1", e2eDeliveryPlan(), run.Config)
	first := upgradeResultToV5(e2eVerifiedEvidenceV4())
	first.ClientRequestID = "evidence-link-revision-first"
	first.AnswerClaimKey = "answer-claim"
	submitStoreTask(t, ctx, pool, store, fixture, "verify-1", first, run.Config)

	if _, err = store.ActivateReadyTasks(ctx, fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	tasks, err := store.ListTasks(ctx, fixture.sessionID)
	if err != nil {
		t.Fatal(err)
	}
	var task Task
	for _, candidate := range tasks {
		if candidate.ClientKey == "verify-2" {
			task = candidate
			break
		}
	}
	if task.ID == "" || task.Status != TaskStatusReady {
		t.Fatalf("verify-2 task is not ready: %+v", task)
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
	second := upgradeResultToV5(e2eVerifiedEvidenceV4())
	second.ClientRequestID = "evidence-link-revision-second"
	second.AnswerClaimKey = "answer-claim"
	second.Claims[0].Evidence[0].Strength = 0.85
	second.Claims[0].Evidence[0].Rationale = "independent verifier revised the evidence weight"
	raw, err := json.Marshal(second)
	if err != nil {
		t.Fatal(err)
	}
	validated, resultHash, err := DecodeAndValidateResultForVersion(run.OrchestratorVersion, raw, task, run.Config)
	if err != nil {
		t.Fatal(err)
	}
	var linkID string
	if err = pool.QueryRow(ctx, `
		SELECT link.id::text
		FROM research_claim_evidence link
		JOIN research_claim claim ON claim.id=link.claim_id
		WHERE link.workspace_id=$1::uuid AND link.session_id=$2::uuid
		  AND claim.client_key='answer-claim'
		ORDER BY link.id LIMIT 1
	`, fixture.workspaceID, fixture.sessionID).Scan(&linkID); err != nil {
		t.Fatal(err)
	}
	return evidenceLinkRevisionFixture{
		acceptanceRaceFixture: acceptanceRaceFixture{
			pool: pool, store: store, fixture: fixture, run: run, task: task,
			attempt: attempt, inboxID: inboxID,
			input: AcceptResultInput{
				SessionID: fixture.sessionID, AttemptID: attempt.ID, AgentID: fixture.validatorID,
				InboxTaskID: inboxID, Raw: raw, Result: validated, Hash: resultHash,
			},
		},
		linkID: linkID,
	}
}

func TestAcceptResultCreatesEvidenceLinkRevisionFromFrozenManifest(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	fx := setupEvidenceLinkRevisionFixture(t, ctx, pool)
	defer cleanupResearchRunFixture(pool, fx.fixture)

	if _, err = fx.store.AcceptResult(ctx, fx.input); err != nil {
		t.Fatalf("AcceptResult: %v", err)
	}
	var currentVersion int
	var eligibilityRevision int64
	var versionCount int
	var currentHash, producerAttempt string
	var claimID, observationID, relation, verificationStatus, verifiedBy, rationale string
	var strength, directness, methodFit float64
	if err = pool.QueryRow(ctx, `
		SELECT passport.current_version, passport.eligibility_revision,
		       (SELECT count(*)::int FROM research_artifact_version all_versions
		        WHERE (all_versions.workspace_id,all_versions.session_id,all_versions.artifact_id)=
		              (passport.workspace_id,passport.session_id,passport.id)),
		       version.content_hash, version.produced_by_attempt_id::text,
		       link.claim_id::text, link.observation_id::text, link.relation,
		       link.strength, link.directness, link.method_fit, link.verification_status,
		       COALESCE(link.verified_by_task_id::text,''), link.rationale
		FROM research_claim_evidence link
		JOIN research_artifact_passport passport ON passport.id=link.id
		JOIN research_artifact_version version
		  ON (version.workspace_id,version.session_id,version.artifact_id,version.version)=
		     (passport.workspace_id,passport.session_id,passport.id,passport.current_version)
		WHERE link.workspace_id=$1::uuid AND link.session_id=$2::uuid AND link.id=$3::uuid
	`, fx.fixture.workspaceID, fx.run.SessionID, fx.linkID).Scan(
		&currentVersion, &eligibilityRevision, &versionCount, &currentHash, &producerAttempt,
		&claimID, &observationID, &relation, &strength, &directness, &methodFit,
		&verificationStatus, &verifiedBy, &rationale,
	); err != nil {
		t.Fatal(err)
	}
	canonicalHash, err := ArtifactContentHash(ArtifactKindEvidenceLink, map[string]any{
		"claim_id": claimID, "observation_id": observationID, "relation": relation,
		"strength": strength, "directness": directness, "method_fit": methodFit,
		"verification_status": verificationStatus, "verified_by_task_id": verifiedBy,
		"rationale": rationale,
	})
	if err != nil {
		t.Fatal(err)
	}
	if currentVersion != 2 || eligibilityRevision != 2 || versionCount != 2 ||
		currentHash != canonicalHash || producerAttempt != fx.attempt.ID {
		t.Fatalf("version=%d revision=%d count=%d hash=%q canonical=%q producer=%q want producer=%q",
			currentVersion, eligibilityRevision, versionCount, currentHash, canonicalHash,
			producerAttempt, fx.attempt.ID)
	}
}

func TestAcceptResultRaceRejectsEvidenceLinkCanonicalDrift(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	fx := setupEvidenceLinkRevisionFixture(t, ctx, pool)
	defer cleanupResearchRunFixture(pool, fx.fixture)

	invokeAcceptWithBeforeCommitFault(t, ctx, fx.acceptanceRaceFixture)
	assertAcceptanceRolledBack(t, ctx, fx.acceptanceRaceFixture)
	if _, err = pool.Exec(ctx, `
		UPDATE research_claim_evidence SET strength=0.4, updated_at=now()
		WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$3::uuid
	`, fx.fixture.workspaceID, fx.run.SessionID, fx.linkID); err != nil {
		t.Fatal(err)
	}
	if _, err = fx.store.AcceptResult(ctx, fx.input); !errors.Is(err, ErrResultConflict) {
		t.Fatalf("AcceptResult after Evidence Link drift err=%v want ErrResultConflict", err)
	}
	assertAcceptanceRolledBack(t, ctx, fx.acceptanceRaceFixture)

	var currentVersion, versionCount int
	if err = pool.QueryRow(ctx, `
		SELECT passport.current_version,
		       (SELECT count(*)::int FROM research_artifact_version version
		        WHERE version.workspace_id=passport.workspace_id
		          AND version.session_id=passport.session_id AND version.artifact_id=passport.id)
		FROM research_artifact_passport passport
		WHERE passport.workspace_id=$1::uuid AND passport.session_id=$2::uuid AND passport.id=$3::uuid
	`, fx.fixture.workspaceID, fx.run.SessionID, fx.linkID).Scan(&currentVersion, &versionCount); err != nil {
		t.Fatal(err)
	}
	if currentVersion != 1 || versionCount != 1 {
		t.Fatalf("current version=%d count=%d want rolled-back version 1 only", currentVersion, versionCount)
	}
}
