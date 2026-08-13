package researchrun

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestTaskContextForAttemptRejectsRevokedGrantWithoutDeletingFrozenHistory(t *testing.T) {
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
	attempt, _, _, run, _ := setupRunningPlanAttempt(t, ctx, store, fixture)
	if _, err = store.TaskContextForAttempt(ctx, attempt.ID, fixture.workspaceID); err != nil {
		t.Fatalf("authorized frozen read before revocation: %v", err)
	}

	var entryCountBefore int
	var representationBytesBefore int64
	if err = pool.QueryRow(ctx, `
		SELECT count(*), COALESCE(sum(octet_length(e.representation_bytes)), 0)
		FROM research_artifact_context_entry e
		JOIN research_artifact_context_manifest m
		  ON (m.workspace_id, m.session_id, m.id) = (e.workspace_id, e.session_id, e.manifest_id)
		WHERE m.workspace_id = $1::uuid AND m.session_id = $2::uuid AND m.attempt_id = $3::uuid
	`, fixture.workspaceID, run.SessionID, attempt.ID).Scan(&entryCountBefore, &representationBytesBefore); err != nil {
		t.Fatalf("read frozen history before revocation: %v", err)
	}
	if entryCountBefore == 0 || representationBytesBefore == 0 {
		t.Fatalf("missing positive frozen history entries=%d bytes=%d", entryCountBefore, representationBytesBefore)
	}

	revokeIntegrationManifestNormalGrant(t, ctx, pool, fixture.workspaceID, run.SessionID, attempt.ID)
	if _, err = store.TaskContextForAttempt(ctx, attempt.ID, fixture.workspaceID); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("revoked frozen read err=%v want ErrInvalidTransition", err)
	}

	var entryCountAfter int
	var representationBytesAfter int64
	if err = pool.QueryRow(ctx, `
		SELECT count(*), COALESCE(sum(octet_length(e.representation_bytes)), 0)
		FROM research_artifact_context_entry e
		JOIN research_artifact_context_manifest m
		  ON (m.workspace_id, m.session_id, m.id) = (e.workspace_id, e.session_id, e.manifest_id)
		WHERE m.workspace_id = $1::uuid AND m.session_id = $2::uuid AND m.attempt_id = $3::uuid
	`, fixture.workspaceID, run.SessionID, attempt.ID).Scan(&entryCountAfter, &representationBytesAfter); err != nil {
		t.Fatalf("read frozen history after revocation: %v", err)
	}
	if entryCountAfter != entryCountBefore || representationBytesAfter != representationBytesBefore {
		t.Fatalf("revocation changed frozen audit history entries %d→%d bytes %d→%d", entryCountBefore, entryCountAfter, representationBytesBefore, representationBytesAfter)
	}
}

func revokeIntegrationManifestNormalGrant(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	workspaceID, sessionID, attemptID string,
) {
	t.Helper()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	var grantID string
	var oldRevision, watermark int64
	if err = tx.QueryRow(ctx, `
		SELECT normal_grant_id::text, normal_grant_revision
		FROM research_artifact_context_manifest
		WHERE workspace_id = $1::uuid AND session_id = $2::uuid AND attempt_id = $3::uuid
	`, workspaceID, sessionID, attemptID).Scan(&grantID, &oldRevision); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `
		UPDATE research_artifact_policy_state
		SET watermark = watermark + 1, updated_at = now()
		WHERE workspace_id = $1::uuid AND session_id = $2::uuid
		RETURNING watermark
	`, workspaceID, sessionID).Scan(&watermark); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `
		UPDATE research_artifact_policy_grant
		SET status = 'revoked', revision = revision + 1, revoked_at = now()
		WHERE workspace_id = $1::uuid AND session_id = $2::uuid AND id = $3::uuid
	`, workspaceID, sessionID, grantID); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `
		INSERT INTO research_artifact_policy_mutation (
		  workspace_id, session_id, watermark, mutation_kind, policy_grant_id,
		  old_grant_revision, new_grant_revision, old_grant_status, new_grant_status
		) VALUES ($1::uuid, $2::uuid, $3, 'grant_revoke', $4::uuid, $5, $6, 'active', 'revoked')
	`, workspaceID, sessionID, watermark, grantID, oldRevision, oldRevision+1); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}
