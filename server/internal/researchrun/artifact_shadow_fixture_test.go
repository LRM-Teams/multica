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

func TestShadowEquivalenceFixtureMatchesLegacyVisibleSet(t *testing.T) {
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
		WorkspaceID: fixture.workspaceID,
		FleetID:     fixture.fleetID,
		CreatedBy:   fixture.userID,
		LeadAgentID: fixture.agentID,
		Goal:        "Shadow equivalence fixture",
		Title:       "Shadow equivalence",
		DepthTier:   "standard",
		Language:    "English",
	}, DefaultRunConfig("standard"))
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}

	artifactIDs := seedShadowEquivalenceArtifacts(t, ctx, pool, fixture.workspaceID, run.SessionID)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)

	var stateVersion int64
	if err = tx.QueryRow(ctx, `
		SELECT state_version
		FROM research_session
		WHERE workspace_id = $1::uuid AND id = $2::uuid
	`, fixture.workspaceID, run.SessionID).Scan(&stateVersion); err != nil {
		t.Fatalf("load state_version: %v", err)
	}

	if err = verifyShadowEquivalenceTx(ctx, tx, fixture.workspaceID, run.SessionID, stateVersion); err != nil {
		t.Fatalf("verifyShadowEquivalenceTx: %v", err)
	}

	liveIDs, err := loadLegacyManifestVisibleArtifactIDsTx(ctx, tx, fixture.workspaceID, run.SessionID)
	if err != nil {
		t.Fatalf("loadLegacyManifestVisibleArtifactIDsTx: %v", err)
	}
	module := NewArtifactContextModule()
	plan, err := module.PlanDispatchManifest(ctx, tx, fixture.workspaceID, run.SessionID, stateVersion)
	if err != nil {
		t.Fatalf("PlanDispatchManifest: %v", err)
	}
	manifestIDs := make(map[string]struct{}, len(plan.Entries))
	for _, entry := range plan.Entries {
		manifestIDs[entry.ArtifactID] = struct{}{}
	}
	if err = compareShadowManifestError(liveIDs, manifestArtifactSet{
		ArtifactIDs: manifestIDs,
		Hash:        plan.ManifestHash,
	}); err != nil {
		t.Fatalf("expected legacy and manifest sets to match: %v", err)
	}

	for _, id := range artifactIDs {
		if _, ok := liveIDs[id]; !ok {
			t.Fatalf("seeded artifact %s missing from legacy visible set", id)
		}
		if _, ok := manifestIDs[id]; !ok {
			t.Fatalf("seeded artifact %s missing from manifest plan entries", id)
		}
	}

	// Removing any legacy-visible artifact must fail shadow comparison.
	tampered := copyArtifactIDSet(liveIDs)
	var removedID string
	for id := range tampered {
		removedID = id
		delete(tampered, id)
		break
	}
	if removedID == "" {
		t.Fatal("expected non-empty legacy visible set")
	}
	if err = compareShadowManifestError(tampered, manifestArtifactSet{
		ArtifactIDs: manifestIDs,
		Hash:        plan.ManifestHash,
	}); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("compareShadowManifestError after removal err=%v want ErrInvalidTransition", err)
	}
}

func TestShadowEquivalencePromptHashMatchesAfterDispatch(t *testing.T) {
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
		LeadAgentID: fixture.agentID, Goal: "Shadow prompt hash", Title: "Shadow prompt",
		DepthTier: "standard", Language: "English",
	}, DefaultRunConfig("standard"))
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	seedShadowEquivalenceArtifacts(t, ctx, pool, fixture.workspaceID, run.SessionID)
	tasks, err := store.ListTasks(ctx, run.SessionID)
	if err != nil || len(tasks) == 0 {
		t.Fatalf("ListTasks: %v len=%d", err, len(tasks))
	}
	input := testDispatchIntentInput(t, ctx, store, run.SessionID, fixture.workspaceID, tasks[0].ID, fixture.agentID)
	attempt, _, err := store.CreateDispatchIntent(ctx, input)
	if err != nil {
		t.Fatalf("CreateDispatchIntent: %v", err)
	}
	replayed, err := replayDispatchPromptFromManifest(ctx, store, fixture.workspaceID, attempt.ID)
	if err != nil {
		t.Fatalf("replayDispatchPromptFromManifest: %v", err)
	}
	var outboxPrompt string
	if err = pool.QueryRow(ctx, `
		SELECT request_payload->>'prompt'
		FROM research_dispatch_outbox WHERE attempt_id = $1::uuid
	`, attempt.ID).Scan(&outboxPrompt); err != nil {
		t.Fatalf("load outbox prompt: %v", err)
	}
	if replayed != outboxPrompt {
		t.Fatal("shadow dispatch prompt hash path: replayed prompt differs from outbox")
	}
	var stateVersion int64
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if err = tx.QueryRow(ctx, `
		SELECT state_version FROM research_session
		WHERE workspace_id = $1::uuid AND id = $2::uuid
	`, fixture.workspaceID, run.SessionID).Scan(&stateVersion); err != nil {
		t.Fatal(err)
	}
	if err = verifyShadowEquivalenceTx(ctx, tx, fixture.workspaceID, run.SessionID, stateVersion); err != nil {
		t.Fatalf("verifyShadowEquivalenceTx after dispatch: %v", err)
	}
}

func seedShadowEquivalenceArtifacts(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	workspaceID, sessionID string,
) []string {
	t.Helper()
	sourceID := uuid.NewString()
	observationID := uuid.NewString()
	claimID := uuid.NewString()
	evidenceID := uuid.NewString()
	now := time.Now().UTC()

	if _, err := pool.Exec(ctx, `
		INSERT INTO research_source_snapshot (
		  id, workspace_id, session_id, canonical_url, title, publisher, source_class,
		  evidence_traits, independence_key, retrieved_at, content_hash, snapshot_text, metadata,
		  verification_status, created_at
		) VALUES (
		  $1::uuid, $2::uuid, $3::uuid, 'https://example.test/shadow-source', 'Shadow source', 'example.test',
		  'primary', '{}'::text[], 'example.test', $4, 'sha256:shadow-source', 'shadow snapshot', '{}'::jsonb,
		  'verified', $4
		)
	`, sourceID, workspaceID, sessionID, now); err != nil {
		t.Fatalf("insert source snapshot: %v", err)
	}
	backfillIntegrationArtifactPassport(t, ctx, pool, workspaceID, sessionID, sourceID, string(ArtifactKindSourceSnapshot), nil, nil)

	if _, err := pool.Exec(ctx, `
		INSERT INTO research_observation (
		  id, workspace_id, session_id, source_snapshot_id, quote, datum, locator,
		  interpretation, verification_status, created_at
		) VALUES (
		  $1::uuid, $2::uuid, $3::uuid, $4::uuid, 'shadow quote', 'shadow datum', 'loc',
		  '', 'verified', $5
		)
	`, observationID, workspaceID, sessionID, sourceID, now); err != nil {
		t.Fatalf("insert observation: %v", err)
	}
	backfillIntegrationArtifactPassport(t, ctx, pool, workspaceID, sessionID, observationID, string(ArtifactKindObservation), nil, nil)

	if _, err := pool.Exec(ctx, `
		INSERT INTO research_claim (
		  id, workspace_id, session_id, client_key, evidence_standard_key, claim_text,
		  significance, confidence, status, goal_version, plan_version, resolution,
		  created_at, updated_at
		) VALUES (
		  $1::uuid, $2::uuid, $3::uuid, 'shadow-claim', '', 'shadow claim text',
		  0.5, 0.5, 'proposed', 1, 1, '', $4, $4
		)
	`, claimID, workspaceID, sessionID, now); err != nil {
		t.Fatalf("insert claim: %v", err)
	}
	backfillIntegrationArtifactPassport(t, ctx, pool, workspaceID, sessionID, claimID, string(ArtifactKindClaim), intPtr(1), intPtr(1))

	if _, err := pool.Exec(ctx, `
		INSERT INTO research_claim_evidence (
		  id, workspace_id, session_id, claim_id, observation_id, relation, strength,
		  directness, method_fit, verification_status, rationale, created_at
		) VALUES (
		  $1::uuid, $2::uuid, $3::uuid, $4::uuid, $5::uuid, 'supports', 0.8, 0.8, 0.8,
		  'verified', 'shadow evidence', $6
		)
	`, evidenceID, workspaceID, sessionID, claimID, observationID, now); err != nil {
		t.Fatalf("insert claim evidence: %v", err)
	}
	backfillIntegrationArtifactPassport(t, ctx, pool, workspaceID, sessionID, evidenceID, string(ArtifactKindEvidenceLink), nil, nil)

	return []string{sourceID, observationID, claimID, evidenceID}
}

func copyArtifactIDSet(src map[string]struct{}) map[string]struct{} {
	dst := make(map[string]struct{}, len(src))
	for id := range src {
		dst[id] = struct{}{}
	}
	return dst
}
