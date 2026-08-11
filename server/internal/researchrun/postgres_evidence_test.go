package researchrun

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestListClaimsOrdersEvidenceByRelationAndID(t *testing.T) {
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
	defer func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1::uuid`, fixture.workspaceID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1::uuid`, fixture.userID)
	}()

	claimID := uuid.NewString()
	observationID := uuid.NewString()
	sourceID := uuid.NewString()
	evidenceLowID := uuid.NewString()
	evidenceHighID := uuid.NewString()
	tieTime := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)

	_, err = tx.Exec(ctx, `
		INSERT INTO research_source_snapshot (
		  id, workspace_id, session_id, canonical_url, title, publisher, source_class,
		  evidence_traits, independence_key, retrieved_at, content_hash, snapshot_text, metadata,
		  verification_status, created_at
		) VALUES (
		  $1::uuid, $2::uuid, $3::uuid, 'https://example.test/source', 'Source', 'example.test',
		  'primary', '{}'::text[], 'example.test', $4, 'sha256:abc', 'quote text', '{}'::jsonb,
		  'verified', $4
		)
	`, sourceID, fixture.workspaceID, fixture.sessionID, tieTime)
	if err != nil {
		t.Fatalf("insert source snapshot: %v", err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO research_observation (
		  id, workspace_id, session_id, source_snapshot_id, quote, datum, locator,
		  interpretation, content_hash, verification_status, created_at
		) VALUES (
		  $1::uuid, $2::uuid, $3::uuid, $4::uuid, 'quote text', '{}'::jsonb, 'loc',
		  '', repeat('a', 64), 'verified', $5
		)
	`, observationID, fixture.workspaceID, fixture.sessionID, sourceID, tieTime)
	if err != nil {
		t.Fatalf("insert observation: %v", err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO research_claim (
		  id, workspace_id, session_id, client_key, evidence_standard_key, claim_text,
		  significance, confidence, status, goal_version, plan_version, resolution,
		  created_at, updated_at
		) VALUES (
		  $1::uuid, $2::uuid, $3::uuid, 'claim-tie', '', 'claim text',
		  'medium', 0.5, 'proposed', 1, 1, '', $4, $4
		)
	`, claimID, fixture.workspaceID, fixture.sessionID, tieTime)
	if err != nil {
		t.Fatalf("insert claim: %v", err)
	}
	// Same created_at, claim, observation; order breaks on relation then id.
	_, err = tx.Exec(ctx, `
		INSERT INTO research_claim_evidence (
		  id, workspace_id, session_id, claim_id, observation_id, relation, strength,
		  directness, method_fit, verification_status, rationale, created_at
		) VALUES
		  ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5::uuid, 'supports', 0.8, 0.8, 0.8, 'verified', 'z-last', $6),
		  ($7::uuid, $2::uuid, $3::uuid, $4::uuid, $5::uuid, 'contradicts', 0.2, 0.2, 0.2, 'verified', 'a-first', $6)
	`, evidenceHighID, fixture.workspaceID, fixture.sessionID, claimID, observationID, tieTime,
		evidenceLowID)
	if err != nil {
		t.Fatalf("insert claim evidence: %v", err)
	}
	goalVersion, planVersion := 1, 1
	backfillIntegrationArtifactPassport(t, ctx, tx, fixture.workspaceID, fixture.sessionID, sourceID, string(ArtifactKindSourceSnapshot), nil, nil)
	backfillIntegrationArtifactPassport(t, ctx, tx, fixture.workspaceID, fixture.sessionID, observationID, string(ArtifactKindObservation), nil, nil)
	backfillIntegrationArtifactPassport(t, ctx, tx, fixture.workspaceID, fixture.sessionID, claimID, string(ArtifactKindClaim), &goalVersion, &planVersion)
	backfillIntegrationArtifactPassport(t, ctx, tx, fixture.workspaceID, fixture.sessionID, evidenceLowID, string(ArtifactKindEvidenceLink), &goalVersion, &planVersion)
	backfillIntegrationArtifactPassport(t, ctx, tx, fixture.workspaceID, fixture.sessionID, evidenceHighID, string(ArtifactKindEvidenceLink), &goalVersion, &planVersion)
	if err = tx.Commit(ctx); err != nil {
		t.Fatalf("commit evidence fixture: %v", err)
	}

	store := NewPostgresStore(pool)
	claims, err := store.ListClaims(ctx, fixture.sessionID)
	if err != nil {
		t.Fatalf("ListClaims: %v", err)
	}
	var claim *Claim
	for i := range claims {
		if claims[i].ID == claimID {
			claim = &claims[i]
			break
		}
	}
	if claim == nil {
		t.Fatal("claim not found")
	}
	if len(claim.Evidence) != 2 {
		t.Fatalf("evidence len=%d want=2", len(claim.Evidence))
	}
	if claim.Evidence[0].Relation != "contradicts" || claim.Evidence[1].Relation != "supports" {
		t.Fatalf("evidence order=%q,%q want contradicts,supports", claim.Evidence[0].Relation, claim.Evidence[1].Relation)
	}
}
