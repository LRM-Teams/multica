package researchrun

import (
	"context"
	"os"
	"sort"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestArtifactDDLInventoryMatchesChapterDContract(t *testing.T) {
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

	scopedTables := []string{
		"research_artifact_passport",
		"research_artifact_version",
		"research_result_artifact",
		"research_artifact_context_manifest",
		"research_artifact_context_entry",
		"research_artifact_context_omission",
		"research_artifact_input_reference",
		"research_artifact_supersession",
		"research_artifact_lifecycle_event",
		"research_artifact_policy_state",
		"research_artifact_policy_grant",
		"research_artifact_policy_mutation",
	}
	for _, table := range scopedTables {
		t.Run("scope/"+table, func(t *testing.T) {
			var workspaceNullable, sessionNullable string
			if err := pool.QueryRow(ctx, `
				SELECT
				  max(is_nullable) FILTER (WHERE column_name = 'workspace_id'),
				  max(is_nullable) FILTER (WHERE column_name = 'session_id')
				FROM information_schema.columns
				WHERE table_schema = current_schema() AND table_name = $1
			`, table).Scan(&workspaceNullable, &sessionNullable); err != nil {
				t.Fatal(err)
			}
			if workspaceNullable != "NO" || sessionNullable != "NO" {
				t.Fatalf("scope columns workspace=%q session=%q", workspaceNullable, sessionNullable)
			}
			var scopedFKs int
			if err := pool.QueryRow(ctx, `
				SELECT count(*)::int
				FROM pg_constraint constraint_row
				JOIN pg_class table_row ON table_row.oid = constraint_row.conrelid
				WHERE table_row.relnamespace = current_schema()::regnamespace
				  AND table_row.relname = $1
				  AND constraint_row.contype = 'f'
				  AND ARRAY(
				    SELECT attribute.attname
				    FROM unnest(constraint_row.conkey) WITH ORDINALITY key(attnum, ordinal)
				    JOIN pg_attribute attribute
				      ON attribute.attrelid = constraint_row.conrelid AND attribute.attnum = key.attnum
				    ORDER BY key.ordinal
				  ) @> ARRAY['workspace_id', 'session_id']::name[]
			`, table).Scan(&scopedFKs); err != nil {
				t.Fatal(err)
			}
			if scopedFKs == 0 {
				t.Fatal("missing composite workspace/session foreign key")
			}
		})
	}

	expectedConstraints := []string{
		"research_artifact_passport_current_version_fkey",
		"research_artifact_version_passport_fkey",
		"research_artifact_version_contract_fkey",
		"research_artifact_version_task_fkey",
		"research_artifact_version_attempt_fkey",
		"research_result_artifact_passport_fkey",
		"research_result_artifact_attempt_fkey",
		"research_artifact_context_manifest_passport_fkey",
		"research_artifact_context_manifest_attempt_fkey",
		"research_artifact_context_manifest_task_fkey",
		"research_artifact_context_entry_manifest_fkey",
		"research_artifact_context_entry_version_fkey",
		"research_artifact_context_omission_manifest_fkey",
		"research_artifact_context_omission_version_fkey",
		"research_artifact_input_reference_consumer_fkey",
		"research_artifact_input_reference_input_fkey",
		"research_artifact_input_reference_manifest_fkey",
		"research_artifact_supersession_successor_fkey",
		"research_artifact_supersession_superseded_fkey",
		"research_artifact_supersession_passport_fkey",
		"research_artifact_supersession_decision_fkey",
		"research_artifact_lifecycle_event_passport_fkey",
		"research_artifact_lifecycle_event_decision_fkey",
	}
	assertCatalogNames(t, ctx, pool, "constraint", expectedConstraints, `
		SELECT constraint_row.conname
		FROM pg_constraint constraint_row
		WHERE constraint_row.connamespace = current_schema()::regnamespace
	`)
	assertCatalogNames(t, ctx, pool, "index", []string{
		"research_artifact_policy_mutation_artifact_revision_uidx",
		"research_artifact_policy_mutation_grant_revision_uidx",
	}, `
		SELECT indexname
		FROM pg_indexes
		WHERE schemaname = current_schema()
	`)

	expectedTriggers := []string{
		"research_artifact_version_immutable_guard",
		"research_artifact_policy_mutation_append_only_guard",
		"research_artifact_lifecycle_event_append_only_guard",
		"research_artifact_passport_class_guard",
		"research_artifact_passport_delete_guard",
		"research_source_snapshot_verification_tx_marker",
		"research_observation_verification_tx_marker",
		"research_claim_evidence_verification_tx_marker",
	}
	expectedTriggers = append(expectedTriggers, ReciprocalArtifactPassportGuardTriggerNames()...)
	expectedTriggers = append(expectedTriggers, PolicyCouplingGuardTriggerNames()...)
	expectedTriggers = append(expectedTriggers, PolicyLedgerGuardTriggerNames()...)
	expectedTriggers = append(expectedTriggers, IntegrityGuardTriggerNames()...)
	expectedTriggers = append(expectedTriggers, LinkPolicyGuardTriggerNames()...)
	for _, name := range ReciprocalArtifactPassportGuardTriggerNames() {
		expectedTriggers = append(expectedTriggers, name[:len(name)-len("_guard")]+"_delete_guard")
	}
	assertCatalogNames(t, ctx, pool, "trigger", expectedTriggers, `
		SELECT trigger_row.tgname
		FROM pg_trigger trigger_row
		JOIN pg_class table_row ON table_row.oid = trigger_row.tgrelid
		WHERE table_row.relnamespace = current_schema()::regnamespace
		  AND NOT trigger_row.tgisinternal
	`)
}

func assertCatalogNames(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	kind string,
	want []string,
	query string,
) {
	t.Helper()
	rows, err := pool.Query(ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	present := make(map[string]struct{})
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		present[name] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	var missing []string
	for _, name := range want {
		if _, ok := present[name]; !ok {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	if len(missing) != 0 {
		t.Fatalf("missing %s names=%v", kind, missing)
	}
}
