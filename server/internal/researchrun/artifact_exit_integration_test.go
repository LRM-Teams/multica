package researchrun

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPolicyWatermarkCASRejectsStaleExpected(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	fixture := seedResearchRunFixture(t, ctx, pool)
	defer cleanupResearchRunFixture(pool, fixture)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if err = ensureSessionPolicyStateTx(ctx, tx, fixture.workspaceID, fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	if _, err = reservePolicyWatermarkCASTx(ctx, tx, fixture.workspaceID, fixture.sessionID, 0); err != nil {
		t.Fatalf("first reserve: %v", err)
	}
	if _, err = reservePolicyWatermarkCASTx(ctx, tx, fixture.workspaceID, fixture.sessionID, 0); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("second reserve err=%v want ErrInvalidTransition", err)
	}
}

func TestPassportEligibilityCASRejectsStaleRevision(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	fixture := seedResearchRunFixture(t, ctx, pool)
	defer cleanupResearchRunFixture(pool, fixture)

	claimID := uuid.NewString()
	backfillIntegrationArtifactPassport(t, ctx, pool, fixture.workspaceID, fixture.sessionID, claimID, string(ArtifactKindClaim), intPtr(1), intPtr(1))

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if err = casPassportEligibilityRevisionTx(
		ctx, tx, fixture.workspaceID, fixture.sessionID, claimID, 1, 1, ArtifactLifecycleRegistered,
	); err != nil {
		t.Fatalf("valid CAS: %v", err)
	}
	if err = casPassportEligibilityRevisionTx(
		ctx, tx, fixture.workspaceID, fixture.sessionID, claimID, 1, 99, ArtifactLifecycleRegistered,
	); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("stale CAS err=%v want ErrInvalidTransition", err)
	}
}

func TestReplayDispatchPromptMatchesOutboxAfterDispatch(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	fixture := seedResearchRunFixture(t, ctx, pool)
	defer cleanupResearchRunFixture(pool, fixture)
	store := NewPostgresStore(pool)
	run, _, err := store.CreateRun(ctx, StartInput{
		WorkspaceID: fixture.workspaceID, FleetID: fixture.fleetID, CreatedBy: fixture.userID,
		LeadAgentID: fixture.agentID, Goal: "Prompt replay", Title: "Prompt replay",
		DepthTier: "standard", Language: "English",
	}, DefaultRunConfig("standard"))
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	tasks, err := store.ListTasks(ctx, run.SessionID)
	if err != nil || len(tasks) == 0 {
		t.Fatalf("ListTasks: %v len=%d", err, len(tasks))
	}
	input := testDispatchIntentInput(t, ctx, store, run.SessionID, fixture.workspaceID, tasks[0].ID, fixture.agentID)
	attempt, _, err := store.CreateDispatchIntent(ctx, input)
	if err != nil {
		t.Fatalf("CreateDispatchIntent: %v", err)
	}
	var outboxPrompt string
	if err = pool.QueryRow(ctx, `
		SELECT request_payload->>'prompt'
		FROM research_dispatch_outbox
		WHERE attempt_id = $1::uuid
	`, attempt.ID).Scan(&outboxPrompt); err != nil {
		t.Fatalf("load outbox prompt: %v", err)
	}
	replayed, err := replayDispatchPromptFromManifest(ctx, store, fixture.workspaceID, attempt.ID)
	if err != nil {
		t.Fatalf("replayDispatchPromptFromManifest: %v", err)
	}
	if replayed != outboxPrompt {
		t.Fatalf("replayed prompt differs from outbox")
	}
}

func TestHistoricalTaskContextAdvancesWhileFrozenManifestStable(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	fixture := seedResearchRunFixture(t, ctx, pool)
	defer cleanupResearchRunFixture(pool, fixture)
	store := NewPostgresStore(pool)
	run, _, err := store.CreateRun(ctx, StartInput{
		WorkspaceID: fixture.workspaceID, FleetID: fixture.fleetID, CreatedBy: fixture.userID,
		LeadAgentID: fixture.agentID, Goal: "Historical live reads", Title: "Historical live",
		DepthTier: "standard", Language: "English",
	}, DefaultRunConfig("standard"))
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	tasks, err := store.ListTasks(ctx, run.SessionID)
	if err != nil || len(tasks) == 0 {
		t.Fatalf("ListTasks: %v len=%d", err, len(tasks))
	}
	input := testDispatchIntentInput(t, ctx, store, run.SessionID, fixture.workspaceID, tasks[0].ID, fixture.agentID)
	attempt, _, err := store.CreateDispatchIntent(ctx, input)
	if err != nil {
		t.Fatalf("CreateDispatchIntent: %v", err)
	}
	frozenBefore, err := store.TaskContextForAttempt(ctx, attempt.ID, fixture.workspaceID)
	if err != nil {
		t.Fatalf("TaskContextForAttempt before: %v", err)
	}
	sourceID := uuid.NewString()
	if _, err = pool.Exec(ctx, `
		INSERT INTO research_source_snapshot (
		  id, workspace_id, session_id, canonical_url, title, publisher, source_class,
		  evidence_traits, independence_key, retrieved_at, content_hash, snapshot_text, metadata,
		  verification_status
		) VALUES (
		  $1::uuid, $2::uuid, $3::uuid, 'https://example.test/post-dispatch', 'Post dispatch', 'example.test',
		  'primary', '{}'::text[], 'example.test', now(), 'sha256:post', 'post snapshot', '{}'::jsonb,
		  'verified'
		)
	`, sourceID, fixture.workspaceID, run.SessionID); err != nil {
		t.Fatalf("insert source: %v", err)
	}
	backfillIntegrationArtifactPassport(t, ctx, pool, fixture.workspaceID, run.SessionID, sourceID, string(ArtifactKindSourceSnapshot), nil, nil)

	live, err := store.TaskContext(ctx, tasks[0].ID, fixture.workspaceID)
	if err != nil {
		t.Fatalf("TaskContext live: %v", err)
	}
	frozenAfter, err := store.TaskContextForAttempt(ctx, attempt.ID, fixture.workspaceID)
	if err != nil {
		t.Fatalf("TaskContextForAttempt after: %v", err)
	}
	if len(live.Sources) != len(frozenBefore.Sources)+1 {
		t.Fatalf("live sources before=%d after=%d", len(frozenBefore.Sources), len(live.Sources))
	}
	if len(frozenAfter.Sources) != len(frozenBefore.Sources) {
		t.Fatalf("frozen sources changed before=%d after=%d", len(frozenBefore.Sources), len(frozenAfter.Sources))
	}
}

func TestDispatchFailsWhenPassportEligibilityAdvances(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	fixture := seedResearchRunFixture(t, ctx, pool)
	defer cleanupResearchRunFixture(pool, fixture)
	store := NewPostgresStore(pool)
	run, _, err := store.CreateRun(ctx, StartInput{
		WorkspaceID: fixture.workspaceID, FleetID: fixture.fleetID, CreatedBy: fixture.userID,
		LeadAgentID: fixture.agentID, Goal: "Eligibility CAS gate", Title: "Eligibility CAS",
		DepthTier: "standard", Language: "English",
	}, DefaultRunConfig("standard"))
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	tasks, err := store.ListTasks(ctx, run.SessionID)
	if err != nil || len(tasks) == 0 {
		t.Fatalf("ListTasks: %v len=%d", err, len(tasks))
	}
	claimID := uuid.NewString()
	if _, err = pool.Exec(ctx, `
		INSERT INTO research_claim (
		  id, workspace_id, session_id, client_key, evidence_standard_key, claim_text,
		  significance, confidence, status, goal_version, plan_version, resolution
		) VALUES (
		  $1::uuid, $2::uuid, $3::uuid, 'cas-claim', '', 'claim for CAS',
		  0.5, 0.5, 'proposed', 1, 1, ''
		)
	`, claimID, fixture.workspaceID, run.SessionID); err != nil {
		t.Fatalf("insert claim: %v", err)
	}
	backfillIntegrationArtifactPassport(t, ctx, pool, fixture.workspaceID, run.SessionID, claimID, string(ArtifactKindClaim), intPtr(1), intPtr(1))

	// Advance eligibility revision after plan would have captured revision 1.
	if _, err = pool.Exec(ctx, `
		UPDATE research_artifact_passport
		SET eligibility_revision = eligibility_revision + 1, updated_at = now()
		WHERE workspace_id = $1::uuid AND session_id = $2::uuid AND id = $3::uuid
	`, fixture.workspaceID, run.SessionID, claimID); err != nil {
		t.Fatalf("bump eligibility: %v", err)
	}

	input := testDispatchIntentInput(t, ctx, store, run.SessionID, fixture.workspaceID, tasks[0].ID, fixture.agentID)
	_, _, err = store.CreateDispatchIntent(ctx, input)
	if !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("CreateDispatchIntent err=%v want ErrInvalidTransition", err)
	}
	var outboxCount int
	if err = pool.QueryRow(ctx, `
		SELECT count(*) FROM research_dispatch_outbox WHERE attempt_id = $1::uuid
	`, input.AttemptID).Scan(&outboxCount); err != nil {
		t.Fatal(err)
	}
	if outboxCount != 0 {
		t.Fatalf("outbox count=%d want 0 after failed dispatch", outboxCount)
	}
}

func TestDispatchFailsWhenArtifactVersionContentHashChanges(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	fixture := seedResearchRunFixture(t, ctx, pool)
	defer cleanupResearchRunFixture(pool, fixture)
	store := NewPostgresStore(pool)
	run, _, err := store.CreateRun(ctx, StartInput{
		WorkspaceID: fixture.workspaceID, FleetID: fixture.fleetID, CreatedBy: fixture.userID,
		LeadAgentID: fixture.agentID, Goal: "Representation CAS gate", Title: "Representation CAS",
		DepthTier: "standard", Language: "English",
	}, DefaultRunConfig("standard"))
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	tasks, err := store.ListTasks(ctx, run.SessionID)
	if err != nil || len(tasks) == 0 {
		t.Fatalf("ListTasks: %v len=%d", err, len(tasks))
	}
	claimID := uuid.NewString()
	if _, err = pool.Exec(ctx, `
		INSERT INTO research_claim (
		  id, workspace_id, session_id, client_key, evidence_standard_key, claim_text,
		  significance, confidence, status, goal_version, plan_version, resolution
		) VALUES (
		  $1::uuid, $2::uuid, $3::uuid, 'repr-cas-claim', '', 'claim for representation CAS',
		  0.5, 0.5, 'proposed', 1, 1, ''
		)
	`, claimID, fixture.workspaceID, run.SessionID); err != nil {
		t.Fatalf("insert claim: %v", err)
	}
	backfillIntegrationArtifactPassport(t, ctx, pool, fixture.workspaceID, run.SessionID, claimID, string(ArtifactKindClaim), intPtr(1), intPtr(1))

	if _, err = pool.Exec(ctx, `
		UPDATE research_artifact_version
		SET content_hash = 'sha256:mutated-after-plan'
		WHERE workspace_id = $1::uuid AND session_id = $2::uuid AND artifact_id = $3::uuid
	`, fixture.workspaceID, run.SessionID, claimID); err != nil {
		t.Fatalf("mutate version content hash: %v", err)
	}

	input := testDispatchIntentInput(t, ctx, store, run.SessionID, fixture.workspaceID, tasks[0].ID, fixture.agentID)
	_, _, err = store.CreateDispatchIntent(ctx, input)
	if !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("CreateDispatchIntent err=%v want ErrInvalidTransition", err)
	}
	var manifestCount int
	if err = pool.QueryRow(ctx, `
		SELECT count(*)::int FROM research_artifact_context_manifest
		WHERE workspace_id = $1::uuid AND session_id = $2::uuid
	`, fixture.workspaceID, run.SessionID).Scan(&manifestCount); err != nil {
		t.Fatal(err)
	}
	if manifestCount != 0 {
		t.Fatalf("manifest count=%d want 0 after representation CAS failure", manifestCount)
	}
}

func cleanupResearchRunFixture(pool *pgxpool.Pool, fixture researchRunFixture) {
	_, _ = pool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1::uuid`, fixture.workspaceID)
	_, _ = pool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1::uuid`, fixture.userID)
}

func intPtr(v int) *int { return &v }
