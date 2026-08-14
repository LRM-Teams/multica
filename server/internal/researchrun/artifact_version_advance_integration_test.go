package researchrun

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestAdvanceArtifactVersionPersistsExactCurrentVersionLedger(t *testing.T) {
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
	store := NewPostgresStore(pool)
	run, _, err := store.CreateRun(ctx, StartInput{
		WorkspaceID: fixture.workspaceID, FleetID: fixture.fleetID, CreatedBy: fixture.userID,
		LeadAgentID: fixture.agentID, Goal: "Version root question", Title: "Version advance",
		DepthTier: "standard", Language: "English",
	}, DefaultRunConfig("standard"))
	if err != nil {
		t.Fatal(err)
	}
	var questionID string
	if err = pool.QueryRow(ctx, `
		SELECT id::text FROM research_question
		WHERE session_id = $1::uuid AND client_key = 'root'
	`, run.SessionID).Scan(&questionID); err != nil {
		t.Fatal(err)
	}
	contentHash, err := ArtifactContentHash(ArtifactKindQuestion, map[string]any{"question": "changed"})
	if err != nil {
		t.Fatal(err)
	}
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `UPDATE research_question SET question = 'changed' WHERE id = $1::uuid`, questionID); err != nil {
		t.Fatal(err)
	}
	goalVersion, planVersion := int32(1), int32(1)
	advanced, err := advanceArtifactVersionTx(ctx, tx, advanceArtifactVersionInput{
		WorkspaceID: fixture.workspaceID, SessionID: run.SessionID, ArtifactID: questionID,
		Kind: ArtifactKindQuestion, ContentHash: contentHash, AccessLevel: ArtifactAccessRaw,
		GoalVersion: &goalVersion, PlanVersion: &planVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !advanced {
		t.Fatal("semantic change did not append a version")
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	var currentVersion int
	var eligibility int64
	var storedHash, hashOrigin, mutationKind string
	var oldVersion, newVersion int
	if err = pool.QueryRow(ctx, `
		SELECT p.current_version, p.eligibility_revision, v.content_hash, v.hash_origin,
		       m.mutation_kind, m.old_current_version, m.new_current_version
		FROM research_artifact_passport p
		JOIN research_artifact_version v
		  ON (v.workspace_id, v.session_id, v.artifact_id, v.version) =
		     (p.workspace_id, p.session_id, p.id, p.current_version)
		JOIN research_artifact_policy_mutation m
		  ON (m.workspace_id, m.session_id, m.artifact_id, m.new_eligibility_revision) =
		     (p.workspace_id, p.session_id, p.id, p.eligibility_revision)
		WHERE p.id = $1::uuid
	`, questionID).Scan(&currentVersion, &eligibility, &storedHash, &hashOrigin,
		&mutationKind, &oldVersion, &newVersion); err != nil {
		t.Fatal(err)
	}
	if currentVersion != 2 || eligibility != 2 || oldVersion != 1 || newVersion != 2 {
		t.Fatalf("version/revision=%d/%d ledger=%d->%d", currentVersion, eligibility, oldVersion, newVersion)
	}
	if storedHash != contentHash || hashOrigin != string(ArtifactHashOriginProduction) || mutationKind != "current_version" {
		t.Fatalf("hash=%q origin=%q mutation=%q", storedHash, hashOrigin, mutationKind)
	}
}
