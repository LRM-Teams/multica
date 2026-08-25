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

type shadowEvidenceTieSeed struct {
	ClaimID          string
	EvidenceFirstID  string
	EvidenceSecondID string
}

func seedShadowEvidenceTieArtifacts(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	workspaceID, sessionID string,
) shadowEvidenceTieSeed {
	t.Helper()
	sourceID := uuid.NewString()
	observationID := uuid.NewString()
	claimID := uuid.NewString()
	evidenceFirstID := uuid.NewString()
	evidenceSecondID := uuid.NewString()
	tieTime := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)

	if _, err = tx.Exec(ctx, `
		INSERT INTO research_source_snapshot (
		  id, workspace_id, session_id, canonical_url, title, publisher, source_class,
		  evidence_traits, independence_key, retrieved_at, content_hash, snapshot_text, metadata,
		  verification_status, created_at
		) VALUES (
		  $1::uuid, $2::uuid, $3::uuid, 'https://example.test/tie-source', 'Tie source', 'example.test',
		  'primary', '{}'::text[], 'example.test', $4, 'sha256:tie-source', 'tie snapshot', '{}'::jsonb,
		  'verified', $4
		)
	`, sourceID, workspaceID, sessionID, tieTime); err != nil {
		t.Fatalf("insert source snapshot: %v", err)
	}
	backfillIntegrationArtifactPassport(t, ctx, tx, workspaceID, sessionID, sourceID, string(ArtifactKindSourceSnapshot), nil, nil)

	if _, err = tx.Exec(ctx, `
		INSERT INTO research_observation (
		  id, workspace_id, session_id, source_snapshot_id, quote, datum, locator,
		  interpretation, content_hash, verification_status, created_at
		) VALUES (
		  $1::uuid, $2::uuid, $3::uuid, $4::uuid, 'tie quote', '"tie datum"'::jsonb, 'loc',
		  '', 'sha256:tie-observation', 'verified', $5
		)
	`, observationID, workspaceID, sessionID, sourceID, tieTime); err != nil {
		t.Fatalf("insert observation: %v", err)
	}
	backfillIntegrationArtifactPassport(t, ctx, tx, workspaceID, sessionID, observationID, string(ArtifactKindObservation), nil, nil)

	if _, err = tx.Exec(ctx, `
		INSERT INTO research_claim (
		  id, workspace_id, session_id, client_key, evidence_standard_key, claim_text,
		  significance, confidence, status, goal_version, plan_version, resolution,
		  created_at, updated_at
		) VALUES (
		  $1::uuid, $2::uuid, $3::uuid, 'shadow-tie-claim', '', 'claim with tied evidence',
		  'medium', 0.5, 'proposed', 1, 1, '', $4, $4
		)
	`, claimID, workspaceID, sessionID, tieTime); err != nil {
		t.Fatalf("insert claim: %v", err)
	}
	backfillIntegrationArtifactPassport(t, ctx, tx, workspaceID, sessionID, claimID, string(ArtifactKindClaim), intPtr(1), intPtr(1))

	// Same created_at, claim, observation; canonical order breaks on relation then id.
	if _, err = tx.Exec(ctx, `
		INSERT INTO research_claim_evidence (
		  id, workspace_id, session_id, claim_id, observation_id, relation, strength,
		  directness, method_fit, verification_status, rationale, created_at
		) VALUES
		  ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5::uuid, 'supports', 0.8, 0.8, 0.8, 'verified', 'z-last', $6),
		  ($7::uuid, $2::uuid, $3::uuid, $4::uuid, $5::uuid, 'contradicts', 0.2, 0.2, 0.2, 'verified', 'a-first', $6)
	`, evidenceSecondID, workspaceID, sessionID, claimID, observationID, tieTime, evidenceFirstID); err != nil {
		t.Fatalf("insert claim evidence: %v", err)
	}
	backfillIntegrationArtifactPassport(t, ctx, tx, workspaceID, sessionID, evidenceFirstID, string(ArtifactKindEvidenceLink), nil, nil)
	backfillIntegrationArtifactPassport(t, ctx, tx, workspaceID, sessionID, evidenceSecondID, string(ArtifactKindEvidenceLink), nil, nil)
	if err = tx.Commit(ctx); err != nil {
		t.Fatalf("commit shadow evidence tie artifacts: %v", err)
	}

	return shadowEvidenceTieSeed{
		ClaimID:          claimID,
		EvidenceFirstID:  evidenceFirstID,
		EvidenceSecondID: evidenceSecondID,
	}
}

func TestShadowEquivalenceEvidenceTieOrderMatchesListClaims(t *testing.T) {
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
		LeadAgentID: fixture.agentID, Goal: "Shadow evidence tie", Title: "Shadow evidence tie",
		DepthTier: "standard", Language: "English",
	}, DefaultRunConfig("standard"))
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}

	seed := seedShadowEvidenceTieArtifacts(t, ctx, pool, fixture.workspaceID, run.SessionID)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	var stateVersion int64
	if err = tx.QueryRow(ctx, `
		SELECT state_version FROM research_session
		WHERE workspace_id = $1::uuid AND id = $2::uuid
	`, fixture.workspaceID, run.SessionID).Scan(&stateVersion); err != nil {
		t.Fatal(err)
	}
	if err = verifyShadowEquivalenceTx(ctx, tx, fixture.workspaceID, run.SessionID, stateVersion, ArtifactPurposeTaskExecution); err != nil {
		t.Fatalf("verifyShadowEquivalenceTx: %v", err)
	}

	claims, err := store.ListClaims(ctx, run.SessionID, run.WorkspaceID)
	if err != nil {
		t.Fatalf("ListClaims: %v", err)
	}
	var claim *Claim
	for i := range claims {
		if claims[i].ID == seed.ClaimID {
			claim = &claims[i]
			break
		}
	}
	if claim == nil {
		t.Fatal("claim not found")
	}
	if len(claim.Evidence) != 2 {
		t.Fatalf("evidence len=%d want 2", len(claim.Evidence))
	}
	if claim.Evidence[0].Relation != "contradicts" || claim.Evidence[1].Relation != "supports" {
		t.Fatalf("evidence order=%q,%q want contradicts,supports", claim.Evidence[0].Relation, claim.Evidence[1].Relation)
	}

	module := NewArtifactContextModule()
	plan, err := module.PlanDispatchManifest(ctx, tx, fixture.workspaceID, run.SessionID, stateVersion)
	if err != nil {
		t.Fatalf("PlanDispatchManifest: %v", err)
	}
	manifestEvidence := make([]string, 0, 2)
	for _, entry := range plan.Entries {
		if entry.ArtifactID == seed.EvidenceFirstID || entry.ArtifactID == seed.EvidenceSecondID {
			manifestEvidence = append(manifestEvidence, entry.ArtifactID)
		}
	}
	if len(manifestEvidence) != 2 {
		t.Fatalf("manifest evidence entries=%v want both tie links", manifestEvidence)
	}
}

func TestShadowEquivalenceRejectsWhenEvidenceLinkRemovedFromLegacyVisibleSet(t *testing.T) {
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
		LeadAgentID: fixture.agentID, Goal: "Shadow evidence omission", Title: "Shadow evidence omission",
		DepthTier: "standard", Language: "English",
	}, DefaultRunConfig("standard"))
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	seed := seedShadowEvidenceTieArtifacts(t, ctx, pool, fixture.workspaceID, run.SessionID)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	var stateVersion int64
	if err = tx.QueryRow(ctx, `
		SELECT state_version FROM research_session
		WHERE workspace_id = $1::uuid AND id = $2::uuid
	`, fixture.workspaceID, run.SessionID).Scan(&stateVersion); err != nil {
		t.Fatal(err)
	}
	liveIDs, err := loadLegacyManifestVisibleArtifactIDsTx(ctx, tx, fixture.workspaceID, run.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	module := NewArtifactContextModule()
	plan, err := module.PlanDispatchManifest(ctx, tx, fixture.workspaceID, run.SessionID, stateVersion)
	if err != nil {
		t.Fatal(err)
	}
	manifestIDs := make(map[string]struct{}, len(plan.Entries))
	for _, entry := range plan.Entries {
		manifestIDs[entry.ArtifactID] = struct{}{}
	}
	delete(liveIDs, seed.EvidenceFirstID)
	if err = compareShadowManifestError(liveIDs, manifestArtifactSet{
		ArtifactIDs: manifestIDs,
		Hash:        plan.ManifestHash,
	}); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("compareShadowManifestError after evidence removal err=%v want ErrInvalidTransition", err)
	}
}

func TestShadowEvidenceTieOrderPromptHashMatchesAfterDispatch(t *testing.T) {
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
		LeadAgentID: fixture.agentID, Goal: "Shadow tie prompt", Title: "Shadow tie prompt",
		DepthTier: "standard", Language: "English",
	}, DefaultRunConfig("standard"))
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	seedShadowEvidenceTieArtifacts(t, ctx, pool, fixture.workspaceID, run.SessionID)
	tasks, err := store.ListTasks(ctx, run.SessionID, run.WorkspaceID)
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
		t.Fatal("tie-order fixture: replayed prompt differs from outbox")
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	var stateVersion int64
	if err = tx.QueryRow(ctx, `
		SELECT state_version FROM research_session
		WHERE workspace_id = $1::uuid AND id = $2::uuid
	`, fixture.workspaceID, run.SessionID).Scan(&stateVersion); err != nil {
		t.Fatal(err)
	}
	if err = verifyShadowEquivalenceTx(ctx, tx, fixture.workspaceID, run.SessionID, stateVersion, ArtifactPurposeTaskExecution); err != nil {
		t.Fatalf("verifyShadowEquivalenceTx after tie dispatch: %v", err)
	}
}
