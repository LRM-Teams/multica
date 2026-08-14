package researchrun

import (
	"context"
	"fmt"
	"os"
	"sync"
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

func TestAdvanceArtifactVersionConcurrentWritersKeepContinuousLedger(t *testing.T) {
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
		LeadAgentID: fixture.agentID, Goal: "Concurrent version writers", Title: "Concurrent version writers",
		DepthTier: "standard", Language: "English",
	}, DefaultRunConfig("standard"))
	if err != nil {
		t.Fatal(err)
	}
	var questionID string
	if err = pool.QueryRow(ctx, `
		SELECT id::text FROM research_question
		WHERE session_id=$1::uuid AND client_key='root'
	`, run.SessionID).Scan(&questionID); err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, question := range []string{"concurrent revision alpha", "concurrent revision beta"} {
		question := question
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			tx, txErr := pool.BeginTx(ctx, pgx.TxOptions{})
			if txErr != nil {
				errs <- txErr
				return
			}
			defer tx.Rollback(ctx)
			if _, txErr = tx.Exec(ctx, `
				UPDATE research_question SET question=$2, updated_at=now() WHERE id=$1::uuid
			`, questionID, question); txErr != nil {
				errs <- txErr
				return
			}
			contentHash, hashErr := ArtifactContentHash(ArtifactKindQuestion, map[string]any{"question": question})
			if hashErr != nil {
				errs <- hashErr
				return
			}
			goalVersion, planVersion := int32(run.GoalVersion), int32(run.PlanVersion)
			advanced, advanceErr := advanceArtifactVersionTx(ctx, tx, advanceArtifactVersionInput{
				WorkspaceID: fixture.workspaceID, SessionID: run.SessionID, ArtifactID: questionID,
				Kind: ArtifactKindQuestion, ContentHash: contentHash, AccessLevel: ArtifactAccessRaw,
				GoalVersion: &goalVersion, PlanVersion: &planVersion,
			})
			if advanceErr != nil {
				errs <- advanceErr
				return
			}
			if !advanced {
				errs <- fmt.Errorf("concurrent semantic change %q did not append a version", question)
				return
			}
			if txErr = tx.Commit(ctx); txErr != nil {
				errs <- txErr
			}
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for writerErr := range errs {
		if writerErr != nil {
			t.Fatalf("concurrent version writer: %v", writerErr)
		}
	}

	var currentVersion int
	var eligibilityRevision int64
	var versionCount, mutationCount, distinctTargets int
	var minimumWatermark, maximumWatermark int64
	var currentHash, currentQuestion string
	if err = pool.QueryRow(ctx, `
		SELECT passport.current_version, passport.eligibility_revision,
		       (SELECT count(*)::int FROM research_artifact_version version
		        WHERE version.workspace_id=passport.workspace_id AND version.session_id=passport.session_id
		          AND version.artifact_id=passport.id),
		       (SELECT count(*)::int FROM research_artifact_policy_mutation mutation
		        WHERE mutation.workspace_id=passport.workspace_id AND mutation.session_id=passport.session_id
		          AND mutation.artifact_id=passport.id AND mutation.mutation_kind='current_version'),
		       (SELECT count(DISTINCT mutation.new_current_version)::int
		        FROM research_artifact_policy_mutation mutation
		        WHERE mutation.workspace_id=passport.workspace_id AND mutation.session_id=passport.session_id
		          AND mutation.artifact_id=passport.id AND mutation.mutation_kind='current_version'),
		       (SELECT min(mutation.watermark)::bigint FROM research_artifact_policy_mutation mutation
		        WHERE mutation.workspace_id=passport.workspace_id AND mutation.session_id=passport.session_id
		          AND mutation.artifact_id=passport.id AND mutation.mutation_kind='current_version'),
		       (SELECT max(mutation.watermark)::bigint FROM research_artifact_policy_mutation mutation
		        WHERE mutation.workspace_id=passport.workspace_id AND mutation.session_id=passport.session_id
		          AND mutation.artifact_id=passport.id AND mutation.mutation_kind='current_version'),
		       version.content_hash, question.question
		FROM research_artifact_passport passport
		JOIN research_artifact_version version
		  ON (version.workspace_id,version.session_id,version.artifact_id,version.version)=
		     (passport.workspace_id,passport.session_id,passport.id,passport.current_version)
		JOIN research_question question ON question.id=passport.id
		WHERE passport.workspace_id=$1::uuid AND passport.session_id=$2::uuid AND passport.id=$3::uuid
	`, fixture.workspaceID, run.SessionID, questionID).Scan(
		&currentVersion, &eligibilityRevision, &versionCount, &mutationCount, &distinctTargets,
		&minimumWatermark, &maximumWatermark, &currentHash, &currentQuestion,
	); err != nil {
		t.Fatal(err)
	}
	if currentVersion != 3 || eligibilityRevision != 3 || versionCount != 3 || mutationCount != 2 || distinctTargets != 2 {
		t.Fatalf("concurrent version state current=%d eligibility=%d versions=%d mutations=%d targets=%d",
			currentVersion, eligibilityRevision, versionCount, mutationCount, distinctTargets)
	}
	if maximumWatermark-minimumWatermark != 1 {
		t.Fatalf("concurrent mutation watermarks=%d..%d want consecutive", minimumWatermark, maximumWatermark)
	}
	wantHash, err := ArtifactContentHash(ArtifactKindQuestion, map[string]any{"question": currentQuestion})
	if err != nil {
		t.Fatal(err)
	}
	if currentHash != wantHash {
		t.Fatalf("current version hash=%q want final domain hash=%q", currentHash, wantHash)
	}
}
