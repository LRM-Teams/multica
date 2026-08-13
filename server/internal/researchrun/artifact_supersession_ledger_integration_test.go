package researchrun

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestSupersessionLedgerExcludesOldArtifactAndPreservesBothVersions(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
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
		LeadAgentID: fixture.agentID, Goal: "Supersession ledger", Title: "Supersession ledger",
		DepthTier: "standard", Language: "English",
	}, DefaultRunConfig("standard"))
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}

	oldClaimID := uuid.NewString()
	successorClaimID := uuid.NewString()
	seedIntegrationClaimArtifact(t, ctx, pool, fixture.workspaceID, run.SessionID, oldClaimID, "old-claim", "old claim")
	seedIntegrationClaimArtifact(t, ctx, pool, fixture.workspaceID, run.SessionID, successorClaimID, "successor-claim", "successor claim")

	decisionID := uuid.NewString()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `
		INSERT INTO research_decision (
		  id, workspace_id, session_id, decision_kind, actor_type, actor_id,
		  goal_version, plan_version, inputs, outcome, rationale
		) VALUES ($1::uuid,$2::uuid,$3::uuid,'artifact_supersession','system',NULL,$4,$5,
		          jsonb_build_object('superseded_artifact_id',$6::text,'successor_artifact_id',$7::text),
		          '{"approved":true}'::jsonb,'successor corrects the old claim')
	`, decisionID, fixture.workspaceID, run.SessionID, run.GoalVersion, run.PlanVersion, oldClaimID, successorClaimID); err != nil {
		t.Fatal(err)
	}
	backfillIntegrationDecisionPassport(
		t, ctx, tx, fixture.workspaceID, run.SessionID, decisionID,
		"artifact_supersession", run.GoalVersion, run.PlanVersion,
	)
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	oldRevision, newRevision, watermark := supersedeIntegrationArtifact(
		t, ctx, pool, fixture.workspaceID, run.SessionID,
		oldClaimID, successorClaimID, decisionID,
	)

	var edgeCount, mutationCount, versionCount int
	if err = pool.QueryRow(ctx, `
		SELECT
		  (SELECT count(*)::int FROM research_artifact_supersession
		   WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND superseded_artifact_id=$3::uuid
		     AND decision_id=$4::uuid AND old_eligibility_revision=$5
		     AND new_eligibility_revision=$6 AND policy_watermark=$7),
		  (SELECT count(*)::int FROM research_artifact_policy_mutation
		   WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND artifact_id=$3::uuid
		     AND mutation_kind='supersession' AND old_eligibility_revision=$5
		     AND new_eligibility_revision=$6 AND watermark=$7),
		  (SELECT count(*)::int FROM research_artifact_version
		   WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND artifact_id IN ($3::uuid,$8::uuid))
	`, fixture.workspaceID, run.SessionID, oldClaimID, decisionID, oldRevision, newRevision, watermark, successorClaimID).Scan(
		&edgeCount, &mutationCount, &versionCount,
	); err != nil {
		t.Fatal(err)
	}
	if edgeCount != 1 || mutationCount != 1 || versionCount != 2 {
		t.Fatalf("supersession edge=%d mutation=%d versions=%d want 1/1/2", edgeCount, mutationCount, versionCount)
	}

	tx, err = pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	plan, err := NewArtifactContextModule().PlanDispatchManifest(ctx, tx, fixture.workspaceID, run.SessionID, run.StateVersion)
	if err != nil {
		t.Fatal(err)
	}
	oldOmitted := false
	successorIncluded := false
	for _, omission := range plan.Omissions {
		if omission.ArtifactID == oldClaimID && omission.OmissionReason == "lifecycle" {
			oldOmitted = true
		}
	}
	for _, entry := range plan.Entries {
		if entry.ArtifactID == oldClaimID {
			t.Fatal("superseded artifact remained ordinary context input")
		}
		if entry.ArtifactID == successorClaimID {
			successorIncluded = true
		}
	}
	if !oldOmitted || !successorIncluded {
		t.Fatalf("context projection oldOmitted=%v successorIncluded=%v", oldOmitted, successorIncluded)
	}
}

func supersedeIntegrationArtifact(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	workspaceID, sessionID, oldArtifactID, successorArtifactID, decisionID string,
) (oldRevision, newRevision, watermark int64) {
	t.Helper()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	var lockedSession string
	if err = tx.QueryRow(ctx, `
		SELECT id::text FROM research_session
		WHERE workspace_id=$1::uuid AND id=$2::uuid FOR UPDATE
	`, workspaceID, sessionID).Scan(&lockedSession); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `
		SELECT research_artifact_policy_watermark_for_tx($1::uuid,$2::uuid)
	`, workspaceID, sessionID).Scan(&watermark); err != nil {
		t.Fatal(err)
	}
	rows, err := tx.Query(ctx, `
		SELECT id::text FROM research_artifact_passport
		WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id IN ($3::uuid,$4::uuid)
		ORDER BY id::text FOR UPDATE
	`, workspaceID, sessionID, oldArtifactID, successorArtifactID)
	if err != nil {
		t.Fatal(err)
	}
	locked := 0
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		locked++
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		t.Fatal(err)
	}
	rows.Close()
	if locked != 2 {
		t.Fatalf("locked passports=%d want 2", locked)
	}
	var oldVersionID, successorVersionID, oldStatus string
	if err = tx.QueryRow(ctx, `
		SELECT v.id::text, p.lifecycle_status, p.eligibility_revision
		FROM research_artifact_passport p
		JOIN research_artifact_version v
		  ON (v.workspace_id,v.session_id,v.artifact_id,v.version)=(p.workspace_id,p.session_id,p.id,p.current_version)
		WHERE p.workspace_id=$1::uuid AND p.session_id=$2::uuid AND p.id=$3::uuid
	`, workspaceID, sessionID, oldArtifactID).Scan(&oldVersionID, &oldStatus, &oldRevision); err != nil {
		t.Fatal(err)
	}
	if oldStatus != string(ArtifactLifecycleRegistered) {
		t.Fatalf("old lifecycle=%q want registered", oldStatus)
	}
	if err = tx.QueryRow(ctx, `
		SELECT v.id::text
		FROM research_artifact_passport p
		JOIN research_artifact_version v
		  ON (v.workspace_id,v.session_id,v.artifact_id,v.version)=(p.workspace_id,p.session_id,p.id,p.current_version)
		WHERE p.workspace_id=$1::uuid AND p.session_id=$2::uuid AND p.id=$3::uuid
	`, workspaceID, sessionID, successorArtifactID).Scan(&successorVersionID); err != nil {
		t.Fatal(err)
	}
	newRevision = oldRevision + 1
	if _, err = tx.Exec(ctx, `
		UPDATE research_artifact_passport
		SET lifecycle_status='superseded', eligibility_revision=$4
		WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$3::uuid
		  AND lifecycle_status='registered' AND eligibility_revision=$5
	`, workspaceID, sessionID, oldArtifactID, newRevision, oldRevision); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `
		INSERT INTO research_artifact_policy_mutation (
		  workspace_id,session_id,watermark,mutation_kind,artifact_id,
		  old_eligibility_revision,new_eligibility_revision
		) VALUES ($1::uuid,$2::uuid,$3,'supersession',$4::uuid,$5,$6)
	`, workspaceID, sessionID, watermark, oldArtifactID, oldRevision, newRevision); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `
		INSERT INTO research_artifact_supersession (
		  workspace_id,session_id,successor_version_id,superseded_version_id,
		  superseded_artifact_id,reason,decision_id,policy_watermark,
		  old_eligibility_revision,new_eligibility_revision
		) VALUES ($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,
		          'corrected by verified successor',$6::uuid,$7,$8,$9)
	`, workspaceID, sessionID, successorVersionID, oldVersionID, oldArtifactID,
		decisionID, watermark, oldRevision, newRevision); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return oldRevision, newRevision, watermark
}
