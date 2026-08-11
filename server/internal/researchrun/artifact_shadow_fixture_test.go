package researchrun

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type shadowEquivalenceSeed struct {
	EntryArtifactIDs []string
	OmissionChecks   map[string]string
}

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

	seed := seedShadowEquivalenceArtifacts(t, ctx, pool, fixture.workspaceID, run.SessionID)

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

	for _, id := range seed.EntryArtifactIDs {
		if _, ok := liveIDs[id]; !ok {
			t.Fatalf("seeded artifact %s missing from legacy visible set", id)
		}
		if _, ok := manifestIDs[id]; !ok {
			t.Fatalf("seeded artifact %s missing from manifest plan entries", id)
		}
	}
	for artifactID, wantReason := range seed.OmissionChecks {
		found := false
		for _, omission := range plan.Omissions {
			if omission.ArtifactID == artifactID && omission.OmissionReason == wantReason {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected omission artifact=%s reason=%q omissions=%+v", artifactID, wantReason, plan.Omissions)
		}
		if _, ok := manifestIDs[artifactID]; ok {
			t.Fatalf("omitted artifact %s must not appear in manifest entries", artifactID)
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
) shadowEquivalenceSeed {
	t.Helper()
	sourceID := uuid.NewString()
	legacySourceID := uuid.NewString()
	observationID := uuid.NewString()
	claimID := uuid.NewString()
	evidenceID := uuid.NewString()
	nodeFromID := uuid.NewString()
	nodeToID := uuid.NewString()
	edgeID := uuid.NewString()
	messageID := uuid.NewString()
	productRoundID := uuid.NewString()
	reportID := uuid.NewString()
	stageEvalID := uuid.NewString()
	now := time.Now().UTC()
	gv, pv := 1, 1
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)

	if _, err = tx.Exec(ctx, `
		INSERT INTO research_source (
		  id, workspace_id, session_id, url, title, source_class, summary, created_at, updated_at
		) VALUES (
		  $1::uuid, $2::uuid, $3::uuid, 'https://example.test/legacy-source', 'Legacy source',
		  'primary', 'legacy source summary', $4, $4
		)
	`, legacySourceID, workspaceID, sessionID, now); err != nil {
		t.Fatalf("insert legacy source: %v", err)
	}
	backfillIntegrationArtifactPassport(t, ctx, tx, workspaceID, sessionID, legacySourceID, string(ArtifactKindLegacySource), nil, nil)

	if _, err = tx.Exec(ctx, `
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
	backfillIntegrationArtifactPassport(t, ctx, tx, workspaceID, sessionID, sourceID, string(ArtifactKindSourceSnapshot), nil, nil)

	if _, err = tx.Exec(ctx, `
		INSERT INTO research_observation (
		  id, workspace_id, session_id, source_snapshot_id, quote, datum, locator,
		  interpretation, content_hash, verification_status, created_at
		) VALUES (
		  $1::uuid, $2::uuid, $3::uuid, $4::uuid, 'shadow quote', '"shadow datum"'::jsonb, 'loc',
		  '', 'sha256:shadow-observation', 'verified', $5
		)
	`, observationID, workspaceID, sessionID, sourceID, now); err != nil {
		t.Fatalf("insert observation: %v", err)
	}
	backfillIntegrationArtifactPassport(t, ctx, tx, workspaceID, sessionID, observationID, string(ArtifactKindObservation), nil, nil)

	if _, err = tx.Exec(ctx, `
		INSERT INTO research_claim (
		  id, workspace_id, session_id, client_key, evidence_standard_key, claim_text,
		  significance, confidence, status, goal_version, plan_version, resolution,
		  created_at, updated_at
		) VALUES (
		  $1::uuid, $2::uuid, $3::uuid, 'shadow-claim', '', 'shadow claim text',
		  'medium', 0.5, 'proposed', 1, 1, '', $4, $4
		)
	`, claimID, workspaceID, sessionID, now); err != nil {
		t.Fatalf("insert claim: %v", err)
	}
	backfillIntegrationArtifactPassport(t, ctx, tx, workspaceID, sessionID, claimID, string(ArtifactKindClaim), intPtr(1), intPtr(1))

	if _, err = tx.Exec(ctx, `
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
	backfillIntegrationArtifactPassport(t, ctx, tx, workspaceID, sessionID, evidenceID, string(ArtifactKindEvidenceLink), nil, nil)
	if err = tx.Commit(ctx); err != nil {
		t.Fatalf("commit shadow artifacts and passports: %v", err)
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO research_graph_node (
		  id, workspace_id, session_id, node_type, title, summary, status, payload, created_at, updated_at
		) VALUES
		  ($1::uuid, $3::uuid, $4::uuid, 'finding', 'Shadow finding A', 'first finding', 'active', '{}'::jsonb, $5, $5),
		  ($2::uuid, $3::uuid, $4::uuid, 'finding', 'Shadow finding B', 'second finding', 'active', '{}'::jsonb, $5, $5)
	`, nodeFromID, nodeToID, workspaceID, sessionID, now); err != nil {
		t.Fatalf("insert graph nodes: %v", err)
	}
	backfillIntegrationArtifactPassport(t, ctx, pool, workspaceID, sessionID, nodeFromID, string(ArtifactKindGraphNode), nil, nil)
	backfillIntegrationArtifactPassport(t, ctx, pool, workspaceID, sessionID, nodeToID, string(ArtifactKindGraphNode), nil, nil)

	if _, err := pool.Exec(ctx, `
		INSERT INTO research_graph_edge (
		  id, workspace_id, session_id, from_node_id, to_node_id, edge_type, created_at
		) VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5::uuid, 'supports', $6)
	`, edgeID, workspaceID, sessionID, nodeFromID, nodeToID, now); err != nil {
		t.Fatalf("insert graph edge: %v", err)
	}
	backfillIntegrationArtifactPassport(t, ctx, pool, workspaceID, sessionID, edgeID, string(ArtifactKindGraphEdge), nil, nil)

	messageMeta, err := json.Marshal(map[string]any{
		"match_decision": map[string]any{
			"matched_node_ids": []string{nodeFromID},
			"decisions": []map[string]any{
				{"node_id": nodeFromID, "action": "continue"},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `
		INSERT INTO research_message (
		  id, workspace_id, session_id, sender_type, body, card_kind, meta, created_at
		) VALUES ($1::uuid, $2::uuid, $3::uuid, 'system', 'shadow match decision', 'process', $4::jsonb, $5)
	`, messageID, workspaceID, sessionID, messageMeta, now); err != nil {
		t.Fatalf("insert research message: %v", err)
	}
	backfillIntegrationArtifactPassport(t, ctx, pool, workspaceID, sessionID, messageID, string(ArtifactKindResearchMessage), nil, nil)

	if _, err = pool.Exec(ctx, `
		INSERT INTO research_product_round_card (
		  id, workspace_id, session_id, round_number, decision, budget_used, budget_remaining, created_at
		) VALUES ($1::uuid, $2::uuid, $3::uuid, 1, 'continue', 1, 4, $4)
	`, productRoundID, workspaceID, sessionID, now); err != nil {
		t.Fatalf("insert product round card: %v", err)
	}
	backfillIntegrationArtifactPassport(t, ctx, pool, workspaceID, sessionID, productRoundID, string(ArtifactKindProductRoundDecision), nil, nil)

	if _, err = pool.Exec(ctx, `
		INSERT INTO research_report (
		  id, workspace_id, session_id, revision, content_md, structured,
		  goal_version, plan_version, created_at, updated_at
		) VALUES ($1::uuid, $2::uuid, $3::uuid, 1, '# Shadow report', '{}'::jsonb, $4, $5, $6, $6)
	`, reportID, workspaceID, sessionID, gv, pv, now); err != nil {
		t.Fatalf("insert report: %v", err)
	}
	if _, err = pool.Exec(ctx, `
		INSERT INTO research_report_claim (report_id, claim_id, section_id, anchor_quote)
		VALUES ($1::uuid, $2::uuid, 'executive-summary', 'shadow claim appears in report')
	`, reportID, claimID); err != nil {
		t.Fatalf("insert report claim: %v", err)
	}
	backfillIntegrationArtifactPassport(t, ctx, pool, workspaceID, sessionID, reportID, string(ArtifactKindReportRevision), intPtr(gv), intPtr(pv))

	if _, err = pool.Exec(ctx, `
		INSERT INTO research_stage_eval (
		  id, workspace_id, session_id, stage, passed, score, findings, remediation, created_at
		) VALUES ($1::uuid, $2::uuid, $3::uuid, 's1_plan', true, 0.9, '[]'::jsonb, '', $4)
	`, stageEvalID, workspaceID, sessionID, now); err != nil {
		t.Fatalf("insert stage eval: %v", err)
	}
	backfillIntegrationArtifactPassport(t, ctx, pool, workspaceID, sessionID, stageEvalID, string(ArtifactKindStageEvaluation), nil, nil)

	return shadowEquivalenceSeed{
		EntryArtifactIDs: []string{
			legacySourceID, sourceID, observationID, claimID, evidenceID,
			nodeFromID, nodeToID, edgeID, messageID, productRoundID, reportID,
		},
		OmissionChecks: map[string]string{
			stageEvalID: "evaluation_compartment",
		},
	}
}

func copyArtifactIDSet(src map[string]struct{}) map[string]struct{} {
	dst := make(map[string]struct{}, len(src))
	for id := range src {
		dst[id] = struct{}{}
	}
	return dst
}
