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
	attempt, inboxID, raw, run, task := setupRunningPlanAttempt(t, ctx, store, fixture)
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
	result, resultHash, err := DecodeAndValidateResultForVersion(run.OrchestratorVersion, raw, task, run.Config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.AcceptResult(ctx, AcceptResultInput{
		SessionID: run.SessionID, AttemptID: attempt.ID, AgentID: fixture.agentID,
		InboxTaskID: inboxID, Raw: raw, Result: result, Hash: resultHash,
	}); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("revoked result acceptance err=%v want ErrInvalidTransition", err)
	}

	var manifestID, grantID string
	if err = pool.QueryRow(ctx, `
		SELECT id::text, normal_grant_id::text
		FROM research_artifact_context_manifest
		WHERE workspace_id = $1::uuid AND session_id = $2::uuid AND attempt_id = $3::uuid
	`, fixture.workspaceID, run.SessionID, attempt.ID).Scan(&manifestID, &grantID); err != nil {
		t.Fatalf("load revoked frozen identities: %v", err)
	}
	deletions := []struct {
		name string
		sql  string
		args []any
	}{
		{
			name: "manifest",
			sql: `DELETE FROM research_artifact_context_manifest
				WHERE workspace_id = $1::uuid AND session_id = $2::uuid AND id = $3::uuid`,
			args: []any{fixture.workspaceID, run.SessionID, manifestID},
		},
		{
			name: "entry",
			sql: `DELETE FROM research_artifact_context_entry
				WHERE id = (SELECT id FROM research_artifact_context_entry
				WHERE workspace_id = $1::uuid AND session_id = $2::uuid AND manifest_id = $3::uuid LIMIT 1)`,
			args: []any{fixture.workspaceID, run.SessionID, manifestID},
		},
		{
			name: "input reference",
			sql: `DELETE FROM research_artifact_input_reference
				WHERE id = (SELECT id FROM research_artifact_input_reference
				WHERE workspace_id = $1::uuid AND session_id = $2::uuid AND manifest_id = $3::uuid LIMIT 1)`,
			args: []any{fixture.workspaceID, run.SessionID, manifestID},
		},
		{
			name: "grant",
			sql: `DELETE FROM research_artifact_policy_grant
				WHERE workspace_id = $1::uuid AND session_id = $2::uuid AND id = $3::uuid`,
			args: []any{fixture.workspaceID, run.SessionID, grantID},
		},
		{
			name: "revocation ledger",
			sql: `DELETE FROM research_artifact_policy_mutation
				WHERE workspace_id = $1::uuid AND session_id = $2::uuid
				  AND policy_grant_id = $3::uuid AND mutation_kind = 'grant_revoke'`,
			args: []any{fixture.workspaceID, run.SessionID, grantID},
		},
	}
	for _, deletion := range deletions {
		t.Run("cannot delete "+deletion.name, func(t *testing.T) {
			if _, deleteErr := pool.Exec(ctx, deletion.sql, deletion.args...); deleteErr == nil {
				t.Fatalf("direct %s deletion unexpectedly succeeded", deletion.name)
			}
		})
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
	var manifestCount, grantCount, revocationCount int
	if err = pool.QueryRow(ctx, `
		SELECT
		  (SELECT count(*)::int FROM research_artifact_context_manifest WHERE id = $1::uuid),
		  (SELECT count(*)::int FROM research_artifact_policy_grant WHERE id = $2::uuid),
		  (SELECT count(*)::int FROM research_artifact_policy_mutation
		   WHERE policy_grant_id = $2::uuid AND mutation_kind = 'grant_revoke')
	`, manifestID, grantID).Scan(&manifestCount, &grantCount, &revocationCount); err != nil {
		t.Fatalf("count preserved revocation history: %v", err)
	}
	if manifestCount != 1 || grantCount != 1 || revocationCount != 1 {
		t.Fatalf("preserved history manifest=%d grant=%d revocation=%d want 1/1/1",
			manifestCount, grantCount, revocationCount)
	}
	if _, err = pool.Exec(ctx, `DELETE FROM workspace WHERE id = $1::uuid`, fixture.workspaceID); err != nil {
		t.Fatalf("whole-workspace audit cascade must remain allowed: %v", err)
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
