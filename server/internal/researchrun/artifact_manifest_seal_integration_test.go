package researchrun

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestManifestSelectionRowsSealAfterDispatchTransaction(t *testing.T) {
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

	var manifestID string
	if err = pool.QueryRow(ctx, `
		SELECT id::text
		FROM research_artifact_context_manifest
		WHERE workspace_id = $1::uuid AND session_id = $2::uuid AND attempt_id = $3::uuid
	`, fixture.workspaceID, run.SessionID, attempt.ID).Scan(&manifestID); err != nil {
		t.Fatalf("load sealed Manifest: %v", err)
	}

	mutations := []struct {
		name string
		sql  string
	}{
		{
			name: "append entry",
			sql: `INSERT INTO research_artifact_context_entry (
				workspace_id, session_id, manifest_id, ordinal, artifact_version_id,
				eligibility_revision, representation, representation_bytes,
				representation_hash, use_kind
			)
			SELECT workspace_id, session_id, manifest_id, ordinal + 1000,
			       artifact_version_id, eligibility_revision, 'metadata',
			       representation_bytes, representation_hash, use_kind
			FROM research_artifact_context_entry
			WHERE workspace_id = $1::uuid AND session_id = $2::uuid AND manifest_id = $3::uuid
			LIMIT 1`,
		},
		{
			name: "update entry",
			sql: `UPDATE research_artifact_context_entry
			SET reason = 'rewritten after dispatch'
			WHERE workspace_id = $1::uuid AND session_id = $2::uuid AND manifest_id = $3::uuid`,
		},
		{
			name: "append omission",
			sql: `INSERT INTO research_artifact_context_omission (
				workspace_id, session_id, manifest_id, candidate_version_id, ordinal, reason
			)
			SELECT workspace_id, session_id, manifest_id, artifact_version_id, 1000, 'policy_denied'
			FROM research_artifact_context_entry
			WHERE workspace_id = $1::uuid AND session_id = $2::uuid AND manifest_id = $3::uuid
			LIMIT 1`,
		},
		{
			name: "update input reference",
			sql: `UPDATE research_artifact_input_reference
			SET purpose = 'rewritten after dispatch'
			WHERE workspace_id = $1::uuid AND session_id = $2::uuid AND manifest_id = $3::uuid`,
		},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			if _, mutationErr := pool.Exec(ctx, mutation.sql, fixture.workspaceID, run.SessionID, manifestID); mutationErr == nil {
				t.Fatalf("sealed Manifest mutation %q unexpectedly succeeded", mutation.name)
			}
		})
	}

	result, hash, err := DecodeAndValidateResultForVersion(run.OrchestratorVersion, raw, task, run.Config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.AcceptResult(ctx, AcceptResultInput{
		SessionID: run.SessionID, AttemptID: attempt.ID, AgentID: fixture.agentID,
		InboxTaskID: inboxID, Raw: raw, Result: result, Hash: hash,
	}); err != nil {
		t.Fatalf("sealed Manifest blocked legitimate Result lineage append: %v", err)
	}
	var resultReferences int
	if err = pool.QueryRow(ctx, `
		SELECT count(*)::int
		FROM research_artifact_input_reference r
		JOIN research_artifact_version consumer ON consumer.id = r.consumer_version_id
		WHERE r.workspace_id = $1::uuid AND r.session_id = $2::uuid
		  AND r.manifest_id = $3::uuid AND consumer.artifact_id <> $3::uuid
	`, fixture.workspaceID, run.SessionID, manifestID).Scan(&resultReferences); err != nil {
		t.Fatalf("count Result lineage references: %v", err)
	}
	if resultReferences == 0 {
		t.Fatal("expected Result acceptance to append immutable input references")
	}
}
