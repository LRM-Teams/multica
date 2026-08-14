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

func TestWithdrawArtifactPersistsReciprocalFactsAndRetainsAuditHistory(t *testing.T) {
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
		LeadAgentID: fixture.agentID, Goal: "Lifecycle withdrawal", Title: "Lifecycle withdrawal",
		DepthTier: "standard", Language: "English",
	}, DefaultRunConfig("standard"))
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	claimID := uuid.NewString()
	seedIntegrationClaimArtifact(t, ctx, pool, fixture.workspaceID, run.SessionID, claimID, "withdrawal-claim", "durable audit history")

	var beforeRevision, beforeWatermark int64
	if err = pool.QueryRow(ctx, `
		SELECT p.eligibility_revision, s.watermark
		FROM research_artifact_passport p
		JOIN research_artifact_policy_state s
		  ON s.workspace_id = p.workspace_id AND s.session_id = p.session_id
		WHERE p.workspace_id = $1::uuid AND p.session_id = $2::uuid AND p.id = $3::uuid
	`, fixture.workspaceID, run.SessionID, claimID).Scan(&beforeRevision, &beforeWatermark); err != nil {
		t.Fatal(err)
	}

	injected := errors.New("injected withdrawal before commit")
	store.txFaultHook = func(_ context.Context, operation researchTxOperation, point researchTxFaultPoint) error {
		if operation == txOpArtifactWithdraw && point == txBeforeCommit {
			return injected
		}
		return nil
	}
	_, err = store.WithdrawArtifact(ctx, WithdrawArtifactInput{
		WorkspaceID: fixture.workspaceID, SessionID: run.SessionID, ArtifactID: claimID,
		ActorType: "user", ActorID: fixture.userID, Reason: "retracted by author",
	})
	if !errors.Is(err, injected) {
		t.Fatalf("faulted WithdrawArtifact err=%v want injected fault", err)
	}
	store.txFaultHook = nil
	assertWithdrawalState(t, ctx, pool, fixture.workspaceID, run.SessionID, claimID,
		ArtifactLifecycleRegistered, beforeRevision, beforeWatermark, 0, 0)

	receipt, err := store.WithdrawArtifact(ctx, WithdrawArtifactInput{
		WorkspaceID: fixture.workspaceID, SessionID: run.SessionID, ArtifactID: claimID,
		ActorType: "user", ActorID: fixture.userID, Reason: "retracted by author",
	})
	if err != nil {
		t.Fatalf("WithdrawArtifact: %v", err)
	}
	if receipt.EntityKind != ArtifactKindClaim || receipt.OldLifecycle != ArtifactLifecycleRegistered ||
		receipt.NewLifecycle != ArtifactLifecycleWithdrawn || receipt.OldEligibilityRevision != beforeRevision ||
		receipt.NewEligibilityRevision != beforeRevision+1 || receipt.PolicyWatermark != beforeWatermark+1 ||
		receipt.LifecycleEventID == "" {
		t.Fatalf("unexpected withdrawal receipt: %+v", receipt)
	}
	assertWithdrawalState(t, ctx, pool, fixture.workspaceID, run.SessionID, claimID,
		ArtifactLifecycleWithdrawn, beforeRevision+1, beforeWatermark+1, 1, 1)

	var claimCount, versionCount int
	if err = pool.QueryRow(ctx, `SELECT count(*)::int FROM research_claim WHERE id = $1::uuid`, claimID).Scan(&claimCount); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `
		SELECT count(*)::int FROM research_artifact_version
		WHERE workspace_id = $1::uuid AND session_id = $2::uuid AND artifact_id = $3::uuid
	`, fixture.workspaceID, run.SessionID, claimID).Scan(&versionCount); err != nil {
		t.Fatal(err)
	}
	if claimCount != 1 || versionCount != 1 {
		t.Fatalf("withdrawal deleted audit history claim=%d versions=%d", claimCount, versionCount)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	plan, err := NewArtifactContextModule().PlanDispatchManifest(ctx, tx, fixture.workspaceID, run.SessionID, run.StateVersion)
	if err != nil {
		t.Fatalf("PlanDispatchManifest: %v", err)
	}
	for _, entry := range plan.Entries {
		if entry.ArtifactID == claimID {
			t.Fatal("withdrawn artifact appeared in new ordinary context")
		}
	}
	foundOmission := false
	for _, omission := range plan.Omissions {
		if omission.ArtifactID == claimID && omission.OmissionReason == "lifecycle" {
			foundOmission = true
		}
	}
	if !foundOmission {
		t.Fatal("withdrawn artifact missing lifecycle omission audit fact")
	}

	replayed, err := store.WithdrawArtifact(ctx, WithdrawArtifactInput{
		WorkspaceID: fixture.workspaceID, SessionID: run.SessionID, ArtifactID: claimID,
		ActorType: "user", ActorID: fixture.userID, Reason: "duplicate withdrawal",
	})
	if err != nil {
		t.Fatalf("replay WithdrawArtifact: %v", err)
	}
	if replayed.LifecycleEventID != receipt.LifecycleEventID || replayed.PolicyWatermark != receipt.PolicyWatermark ||
		replayed.NewEligibilityRevision != receipt.NewEligibilityRevision {
		t.Fatalf("replayed receipt=%+v want original=%+v", replayed, receipt)
	}
	assertWithdrawalState(t, ctx, pool, fixture.workspaceID, run.SessionID, claimID,
		ArtifactLifecycleWithdrawn, beforeRevision+1, beforeWatermark+1, 1, 1)
}

func TestWithdrawArtifactTransactionRecovery(t *testing.T) {
	runTransactionRecoveryMatrix(t, txOpArtifactWithdraw, func(t *testing.T, run *transactionRecoveryRun) transactionRecoveryOperation {
		claimID := uuid.NewString()
		seedIntegrationClaimArtifact(t, run.ctx, run.pool, run.fixture.workspaceID, run.fixture.sessionID,
			claimID, "withdrawal-recovery", "withdrawal recovery")
		var beforeRevision, beforeWatermark int64
		if err := run.pool.QueryRow(run.ctx, `
			SELECT p.eligibility_revision, state.watermark
			FROM research_artifact_passport p
			JOIN research_artifact_policy_state state
			  ON state.workspace_id = p.workspace_id AND state.session_id = p.session_id
			WHERE p.workspace_id = $1::uuid AND p.session_id = $2::uuid AND p.id = $3::uuid
		`, run.fixture.workspaceID, run.fixture.sessionID, claimID).Scan(&beforeRevision, &beforeWatermark); err != nil {
			t.Fatal(err)
		}
		input := WithdrawArtifactInput{
			WorkspaceID: run.fixture.workspaceID, SessionID: run.fixture.sessionID, ArtifactID: claimID,
			ActorType: "user", ActorID: run.fixture.userID, Reason: "transaction recovery",
		}
		invoke := func() error {
			_, err := run.store.WithdrawArtifact(run.ctx, input)
			return err
		}
		return transactionRecoveryOperation{
			invoke: invoke,
			assertRolledBack: func() {
				assertWithdrawalState(t, run.ctx, run.pool, run.fixture.workspaceID, run.fixture.sessionID, claimID,
					ArtifactLifecycleRegistered, beforeRevision, beforeWatermark, 0, 0)
			},
			assertCommitted: func() {
				assertWithdrawalState(t, run.ctx, run.pool, run.fixture.workspaceID, run.fixture.sessionID, claimID,
					ArtifactLifecycleWithdrawn, beforeRevision+1, beforeWatermark+1, 1, 1)
			},
			recover: invoke,
		}
	})
}

func assertWithdrawalState(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	workspaceID, sessionID, artifactID string,
	wantLifecycle ArtifactLifecycleStatus,
	wantRevision, wantWatermark int64,
	wantMutations, wantEvents int,
) {
	t.Helper()
	var lifecycle ArtifactLifecycleStatus
	var revision, watermark int64
	var mutations, events int
	err := pool.QueryRow(ctx, `
		SELECT p.lifecycle_status, p.eligibility_revision, state.watermark,
		       (SELECT count(*)::int FROM research_artifact_policy_mutation mutation
		        WHERE mutation.workspace_id = p.workspace_id AND mutation.session_id = p.session_id
		          AND mutation.artifact_id = p.id AND mutation.mutation_kind = 'lifecycle'),
		       (SELECT count(*)::int FROM research_artifact_lifecycle_event event
		        WHERE event.workspace_id = p.workspace_id AND event.session_id = p.session_id
		          AND event.artifact_id = p.id)
		FROM research_artifact_passport p
		JOIN research_artifact_policy_state state
		  ON state.workspace_id = p.workspace_id AND state.session_id = p.session_id
		WHERE p.workspace_id = $1::uuid AND p.session_id = $2::uuid AND p.id = $3::uuid
	`, workspaceID, sessionID, artifactID).Scan(&lifecycle, &revision, &watermark, &mutations, &events)
	if err != nil {
		t.Fatal(err)
	}
	if lifecycle != wantLifecycle || revision != wantRevision || watermark != wantWatermark ||
		mutations != wantMutations || events != wantEvents {
		t.Fatalf("withdrawal state lifecycle=%q revision=%d watermark=%d mutations=%d events=%d; want %q/%d/%d/%d/%d",
			lifecycle, revision, watermark, mutations, events,
			wantLifecycle, wantRevision, wantWatermark, wantMutations, wantEvents)
	}
}
