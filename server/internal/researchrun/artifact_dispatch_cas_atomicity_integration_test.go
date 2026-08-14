package researchrun

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestDispatchManifestCASMismatchRollsBackCompleteIntent(t *testing.T) {
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

	for _, tc := range []struct {
		name   string
		mutate func(context.Context, *testing.T, *pgxpool.Pool, dispatchRaceFixture, artifactVersionCandidate)
	}{
		{
			name: "eligibility revision",
			mutate: func(ctx context.Context, t *testing.T, pool *pgxpool.Pool, fx dispatchRaceFixture, entry artifactVersionCandidate) {
				mutateIntegrationArtifactForCASTest(t, ctx, pool, `
					UPDATE research_artifact_passport
					SET eligibility_revision=eligibility_revision+1
					WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$3::uuid
				`, fx.fixture.workspaceID, fx.run.SessionID, entry.ArtifactID)
			},
		},
		{
			name: "representation content hash",
			mutate: func(ctx context.Context, t *testing.T, pool *pgxpool.Pool, fx dispatchRaceFixture, entry artifactVersionCandidate) {
				mutateIntegrationArtifactForCASTest(t, ctx, pool, `
					UPDATE research_artifact_version
					SET content_hash=$4
					WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$3::uuid
				`, fx.fixture.workspaceID, fx.run.SessionID, entry.VersionRowID,
					contentHashFromPayload([]byte("representation changed after authoritative plan")))
			},
		},
		{
			name: "current version",
			mutate: func(ctx context.Context, t *testing.T, pool *pgxpool.Pool, fx dispatchRaceFixture, entry artifactVersionCandidate) {
				mutateIntegrationArtifactForCASTest(t, ctx, pool, `
					UPDATE research_artifact_passport
					SET current_version=current_version+1
					WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$3::uuid
				`, fx.fixture.workspaceID, fx.run.SessionID, entry.ArtifactID)
			},
		},
		{
			name: "access level",
			mutate: func(ctx context.Context, t *testing.T, pool *pgxpool.Pool, fx dispatchRaceFixture, entry artifactVersionCandidate) {
				mutateIntegrationArtifactForCASTest(t, ctx, pool, `
					UPDATE research_artifact_version
					SET access_level='verified_only'
					WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$3::uuid
				`, fx.fixture.workspaceID, fx.run.SessionID, entry.VersionRowID)
			},
		},
		{
			name: "lifecycle",
			mutate: func(ctx context.Context, t *testing.T, pool *pgxpool.Pool, fx dispatchRaceFixture, entry artifactVersionCandidate) {
				mutateIntegrationArtifactForCASTest(t, ctx, pool, `
					UPDATE research_artifact_passport
					SET lifecycle_status='withdrawn'
					WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$3::uuid
				`, fx.fixture.workspaceID, fx.run.SessionID, entry.ArtifactID)
			},
		},
		{
			name: "provenance",
			mutate: func(ctx context.Context, t *testing.T, pool *pgxpool.Pool, fx dispatchRaceFixture, entry artifactVersionCandidate) {
				mutateIntegrationArtifactForCASTest(t, ctx, pool, `
					UPDATE research_artifact_passport
					SET provenance_completeness='unknown'
					WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$3::uuid
				`, fx.fixture.workspaceID, fx.run.SessionID, entry.ArtifactID)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fx := setupDispatchRaceFixture(t, ctx, pool)
			defer cleanupResearchRunFixture(pool, fx.fixture)
			planned := false
			fx.store.dispatchManifestPlannedHook = func(hookCtx context.Context, plan dispatchManifestPlan) error {
				if planned {
					t.Fatal("dispatch manifest planned hook called more than once")
				}
				planned = true
				entry, ok := manifestEntryForArtifact(plan, fx.claimID)
				if !ok {
					t.Fatalf("selected claim %s missing from authoritative plan", fx.claimID)
				}
				tc.mutate(hookCtx, t, pool, fx, entry)
				return nil
			}
			_, _, err = fx.store.CreateDispatchIntent(ctx, fx.input)
			fx.store.dispatchManifestPlannedHook = nil
			if !planned {
				t.Fatal("dispatch did not reach authoritative manifest plan seam")
			}
			if !errors.Is(err, ErrInvalidTransition) {
				t.Fatalf("CreateDispatchIntent err=%v want ErrInvalidTransition", err)
			}
			assertCompleteDispatchIntentAbsent(t, ctx, fx)
		})
	}
}

func assertCompleteDispatchIntentAbsent(t *testing.T, ctx context.Context, fx dispatchRaceFixture) {
	t.Helper()
	var attempts, passports, versions, manifests, entries, omissions, grants, grantMutations, outboxes, events int
	if err := fx.pool.QueryRow(ctx, `
		SELECT
		  (SELECT count(*)::int FROM research_task_attempt WHERE id=$1::uuid),
		  (SELECT count(*)::int FROM research_artifact_passport
		   WHERE workspace_id=$2::uuid AND session_id=$3::uuid
		     AND (id=$1::uuid OR entity_kind='context_manifest')),
		  (SELECT count(*)::int FROM research_artifact_version v
		   JOIN research_artifact_passport p
		     ON (p.workspace_id,p.session_id,p.id)=(v.workspace_id,v.session_id,v.artifact_id)
		   WHERE p.workspace_id=$2::uuid AND p.session_id=$3::uuid
		     AND (p.id=$1::uuid OR p.entity_kind='context_manifest')),
		  (SELECT count(*)::int FROM research_artifact_context_manifest
		   WHERE workspace_id=$2::uuid AND session_id=$3::uuid),
		  (SELECT count(*)::int FROM research_artifact_context_entry
		   WHERE workspace_id=$2::uuid AND session_id=$3::uuid),
		  (SELECT count(*)::int FROM research_artifact_context_omission
		   WHERE workspace_id=$2::uuid AND session_id=$3::uuid),
		  (SELECT count(*)::int FROM research_artifact_policy_grant
		   WHERE workspace_id=$2::uuid AND session_id=$3::uuid),
		  (SELECT count(*)::int FROM research_artifact_policy_mutation
		   WHERE workspace_id=$2::uuid AND session_id=$3::uuid
		     AND policy_grant_id IS NOT NULL),
		  (SELECT count(*)::int FROM research_dispatch_outbox WHERE attempt_id=$1::uuid),
		  (SELECT count(*)::int FROM research_run_event
		   WHERE workspace_id=$2::uuid AND session_id=$3::uuid
		     AND event_type='task_dispatching' AND payload->>'attempt_id'=$1::text)
	`, fx.input.AttemptID, fx.fixture.workspaceID, fx.run.SessionID).Scan(
		&attempts, &passports, &versions, &manifests, &entries, &omissions,
		&grants, &grantMutations, &outboxes, &events,
	); err != nil {
		t.Fatal(err)
	}
	if attempts != 0 || passports != 0 || versions != 0 || manifests != 0 || entries != 0 || omissions != 0 || grants != 0 || grantMutations != 0 || outboxes != 0 || events != 0 {
		t.Fatalf("CAS mismatch leaked attempt=%d passports=%d versions=%d manifest=%d entries=%d omissions=%d grants=%d grant_mutations=%d outbox=%d events=%d",
			attempts, passports, versions, manifests, entries, omissions, grants, grantMutations, outboxes, events)
	}
	var taskStatus string
	if err := fx.pool.QueryRow(ctx, `SELECT status FROM research_task WHERE id=$1::uuid`, fx.task.ID).Scan(&taskStatus); err != nil {
		t.Fatal(err)
	}
	if taskStatus != string(TaskStatusReady) && taskStatus != string(TaskStatusPending) {
		t.Fatalf("CAS mismatch task status=%q want ready or pending", taskStatus)
	}
}
