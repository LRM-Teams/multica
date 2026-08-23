package researchrun

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestAcceptResultRaceRejectsObservationIdentityDrift(t *testing.T) {
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

	fixture := seedResearchRunFixture(t, ctx, pool)
	defer cleanupResearchRunFixture(pool, fixture)
	store := NewPostgresStore(pool)
	run, _, err := store.InitializeRun(ctx, StartInput{
		SessionID: fixture.sessionID, WorkspaceID: fixture.workspaceID,
		FleetID: fixture.fleetID, CreatedBy: fixture.userID, LeadAgentID: fixture.agentID,
		Goal: "Observation acceptance race", Title: "Observation acceptance race",
		DepthTier: "standard", Language: "English",
	}, DefaultRunConfig("standard"))
	if err != nil {
		t.Fatal(err)
	}
	submitStoreTask(t, ctx, pool, store, fixture, "plan:1", e2eDeliveryPlan(), run.Config)
	if _, err = store.ActivateReadyTasks(ctx, fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	tasks, err := store.ListTasks(ctx, fixture.sessionID, fixture.workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	var task Task
	for _, candidate := range tasks {
		if candidate.ClientKey == "verify-1" {
			task = candidate
			break
		}
	}
	if task.ID == "" || task.Status != TaskStatusReady {
		t.Fatalf("verify-1 task is not ready: %+v", task)
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
	evidence := upgradeResultToV5(e2eVerifiedEvidenceV4())
	evidence.ClientRequestID = "observation-acceptance-race"
	evidence.AnswerClaimKey = "answer-claim"
	raw, err := json.Marshal(evidence)
	if err != nil {
		t.Fatal(err)
	}
	validated, resultHash, err := DecodeAndValidateResultForVersion(run.OrchestratorVersion, raw, task, run.Config)
	if err != nil {
		t.Fatal(err)
	}
	fx := acceptanceRaceFixture{
		pool: pool, store: store, fixture: fixture, run: run, task: task,
		attempt: attempt, inboxID: inboxID,
		input: AcceptResultInput{
			SessionID: fixture.sessionID, AttemptID: attempt.ID, AgentID: fixture.validatorID,
			InboxTaskID: inboxID, Raw: raw, Result: validated, Hash: resultHash,
		},
	}

	invokeAcceptWithBeforeCommitFault(t, ctx, fx)
	assertAcceptanceRolledBack(t, ctx, fx)

	source := evidence.Sources[0]
	observation := evidence.Observations[0]
	canonicalURL, err := CanonicalURL(source.URL)
	if err != nil {
		t.Fatal(err)
	}
	sourceHash := sha256.Sum256([]byte(source.SnapshotText))
	observationContent, err := json.Marshal(map[string]any{
		"quote": observation.Quote, "datum": json.RawMessage(observation.Datum),
		"locator": observation.Locator, "interpretation": observation.Interpretation,
	})
	if err != nil {
		t.Fatal(err)
	}
	observationHash := sha256.Sum256(observationContent)
	sourceID, observationID := uuid.NewString(), uuid.NewString()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `
		INSERT INTO research_source_snapshot (
		  id, workspace_id, session_id, produced_by_task_id, canonical_url,
		  title, publisher, source_class, evidence_traits, independence_key,
		  retrieved_at, snapshot_text, content_hash, metadata, verification_status
		) VALUES (
		  $1::uuid, $2::uuid, $3::uuid, $4::uuid, $5,
		  $6, $7, $8, $9::text[], $10,
		  $11, $12, $13, $14::jsonb, 'verified'
		)
	`, sourceID, fixture.workspaceID, fixture.sessionID, task.ID, canonicalURL,
		source.Title, source.Publisher, source.SourceClass, source.EvidenceTraits,
		source.IndependenceKey, source.RetrievedAt, source.SnapshotText,
		hex.EncodeToString(sourceHash[:]), normalizeJSON(source.Metadata, `{}`)); err != nil {
		t.Fatal(err)
	}
	backfillIntegrationArtifactPassport(
		t, ctx, tx, fixture.workspaceID, fixture.sessionID, sourceID,
		string(ArtifactKindSourceSnapshot), nil, nil,
	)
	if _, err = tx.Exec(ctx, `
		INSERT INTO research_observation (
		  id, workspace_id, session_id, source_snapshot_id, produced_by_task_id,
		  quote, datum, locator, interpretation, content_hash, verification_status
		) VALUES (
		  $1::uuid, $2::uuid, $3::uuid, $4::uuid, $5::uuid,
		  $6, $7::jsonb, $8, $9, $10, 'verified'
		)
	`, observationID, fixture.workspaceID, fixture.sessionID, sourceID, task.ID,
		"competing quote", normalizeJSON(observation.Datum, `{}`),
		truncateBytes(observation.Locator, 1024), truncateBytes(observation.Interpretation, 8192),
		hex.EncodeToString(observationHash[:])); err != nil {
		t.Fatal(err)
	}
	backfillIntegrationArtifactPassport(
		t, ctx, tx, fixture.workspaceID, fixture.sessionID, observationID,
		string(ArtifactKindObservation), nil, nil,
	)
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	if _, err = store.AcceptResult(ctx, fx.input); !errors.Is(err, ErrResultConflict) {
		t.Fatalf("AcceptResult after observation identity drift err=%v want ErrResultConflict", err)
	}
	assertAcceptanceRolledBack(t, ctx, fx)

	var observations int
	if err = pool.QueryRow(ctx, `
		SELECT count(*)::int FROM research_observation
		WHERE workspace_id=$1::uuid AND session_id=$2::uuid
	`, fixture.workspaceID, fixture.sessionID).Scan(&observations); err != nil {
		t.Fatal(err)
	}
	if observations != 1 {
		t.Fatalf("observations=%d want exactly the competing observation", observations)
	}
}
