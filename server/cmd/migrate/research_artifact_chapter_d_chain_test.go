package main

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

var chapterDMigrationVersions = []string{
	"318_research_artifact_passport",
	"319_research_artifact_passport_backfill",
	"320_research_artifact_reciprocal_guards",
	"321_research_artifact_policy_coupling_guards",
	"322_research_artifact_policy_ledger_guards",
	"323_research_artifact_integrity_guards",
	"324_research_artifact_link_policy_guards",
	"325_research_artifact_migration_diagnostics",
	"326_research_artifact_scoped_relationship_fks",
	"327_research_artifact_canonicalization_registry",
	"328_research_artifact_passport_d_completion",
	"329_research_result_artifact_backfill",
	"330_research_dispatch_manifest_binding",
}

type chapterDMigrationState struct {
	Passports   int
	Versions    int
	Diagnostics int
	ByKind      map[string]int
}

func TestResearchArtifactChapterDMigrationPairLint(t *testing.T) {
	up, down := readAndLintChapterDMigrations(t)
	if len(up) != len(chapterDMigrationVersions) || len(down) != len(chapterDMigrationVersions) {
		t.Fatalf("migration pair inventory up=%d down=%d want=%d", len(up), len(down), len(chapterDMigrationVersions))
	}
}

// TestResearchArtifactChapterDMigrationChain is the aggregate Chapter D §15.1
// proof. Individual migration tests exercise their local guards; this test
// proves that the complete 318-330 chain composes on a fresh legacy schema,
// survives a full reverse down/up cycle, and deterministically reconciles the
// same legacy facts without inventing post-D domain artifacts.
func TestResearchArtifactChapterDMigrationChain(t *testing.T) {
	pool := openTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire connection: %v", err)
	}
	defer conn.Release()

	schema := fmt.Sprintf("research_artifact_d_chain_test_%d", time.Now().UnixNano())
	quotedSchema := pgx.Identifier{schema}.Sanitize()
	if _, err = conn.Exec(ctx, "CREATE SCHEMA "+quotedSchema); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() {
		if _, cleanupErr := pool.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+quotedSchema+" CASCADE"); cleanupErr != nil {
			t.Logf("drop schema %s: %v", schema, cleanupErr)
		}
	})
	if _, err = conn.Exec(ctx, "SET search_path TO "+quotedSchema+", public"); err != nil {
		t.Fatalf("set search path: %v", err)
	}
	fixtureDDL := []struct {
		name string
		sql  string
	}{
		{"legacy", researchArtifactPassportLegacySchema},
		{"scoped-fk", researchArtifactScopedFKTestDDL},
		{"d328", researchArtifact328TestDDL},
		{"d329", researchArtifact329TestDDL},
		{"d330", researchArtifact330TestDDL},
	}
	for _, fixture := range fixtureDDL {
		if _, err = conn.Exec(ctx, fixture.sql); err != nil {
			t.Fatalf("apply %s fixture DDL: %v", fixture.name, err)
		}
	}

	const (
		workspaceID = "10000000-0000-4000-8000-000000000001"
		sessionID   = "20000000-0000-4000-8000-000000000001"
		contractID  = "30000000-0000-4000-8000-000000000001"
		questionID  = "30000000-0000-4000-8000-000000000002"
		taskID      = "30000000-0000-4000-8000-000000000003"
		attemptID   = "30000000-0000-4000-8000-000000000004"
	)
	if _, err = conn.Exec(ctx, `
		INSERT INTO workspace (id) VALUES ($1::uuid);
		INSERT INTO research_session (id, workspace_id, created_at, orchestrator_version)
		VALUES ($2::uuid, $1::uuid, '2026-01-01T00:00:00Z', 'research-run-v5');
		INSERT INTO research_contract_revision (id, workspace_id, session_id, created_at, goal_version)
		VALUES ($3::uuid, $1::uuid, $2::uuid, '2026-01-01T00:00:01Z', 1);
		INSERT INTO research_question (id, workspace_id, session_id, created_at, goal_version, plan_version, client_key)
		VALUES ($4::uuid, $1::uuid, $2::uuid, '2026-01-01T00:00:02Z', 1, 1, 'root');
		INSERT INTO research_task (id, workspace_id, session_id, created_at, goal_version, plan_version, client_key, question_id)
		VALUES ($5::uuid, $1::uuid, $2::uuid, '2026-01-01T00:00:03Z', 1, 1, 'plan:1', $4::uuid);
		INSERT INTO research_task_attempt (
		  id, workspace_id, session_id, task_id, created_at, status,
		  client_request_id, result_hash, result, result_submitted_at
		) VALUES (
		  $6::uuid, $1::uuid, $2::uuid, $5::uuid, '2026-01-01T00:00:04Z', 'succeeded',
		  'request-1', 'sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',
		  '{"schema_version":1,"client_request_id":"request-1","summary":"ok"}'::jsonb,
		  '2026-01-01T00:00:05Z'
		);
	`, workspaceID, sessionID, contractID, questionID, taskID, attemptID); err != nil {
		t.Fatalf("seed legacy fixture: %v", err)
	}

	up, down := readAndLintChapterDMigrations(t)
	applyChapterDMigrations(t, ctx, conn.Conn(), chapterDMigrationVersions, up)
	first := readChapterDMigrationState(t, ctx, conn.Conn())
	assertNoFabricatedPostDArtifacts(t, ctx, conn.Conn())

	for i := len(chapterDMigrationVersions) - 1; i >= 0; i-- {
		version := chapterDMigrationVersions[i]
		if _, err = conn.Exec(ctx, down[version]); err != nil {
			t.Fatalf("apply %s down: %v", version, err)
		}
	}
	applyChapterDMigrations(t, ctx, conn.Conn(), chapterDMigrationVersions, up)
	second := readChapterDMigrationState(t, ctx, conn.Conn())
	assertNoFabricatedPostDArtifacts(t, ctx, conn.Conn())

	if !reflect.DeepEqual(second, first) {
		t.Fatalf("backfill reconciliation changed after down/up: first=%+v second=%+v", first, second)
	}
	if first.Passports == 0 || first.Versions == 0 || first.ByKind["result_artifact"] != 1 {
		t.Fatalf("aggregate chain did not materialize the seeded D facts: %+v", first)
	}
}

func readAndLintChapterDMigrations(t *testing.T) (map[string]string, map[string]string) {
	t.Helper()
	up := make(map[string]string, len(chapterDMigrationVersions))
	down := make(map[string]string, len(chapterDMigrationVersions))
	seen := make(map[string]struct{}, len(chapterDMigrationVersions))
	for _, version := range chapterDMigrationVersions {
		if _, exists := seen[version]; exists {
			t.Fatalf("duplicate Chapter D migration version %q", version)
		}
		seen[version] = struct{}{}
		upSQL, downSQL := readMigrationPair(t, version)
		for direction, sql := range map[string]string{"up": upSQL, "down": downSQL} {
			trimmed := strings.TrimSpace(sql)
			if trimmed == "" || !strings.HasSuffix(trimmed, ";") {
				t.Fatalf("%s %s migration is empty or lacks a terminating semicolon", version, direction)
			}
			upper := strings.ToUpper(trimmed)
			if strings.Contains(upper, "BEGIN;") || strings.Contains(upper, "COMMIT;") {
				t.Fatalf("%s %s migration owns a transaction boundary", version, direction)
			}
		}
		up[version], down[version] = upSQL, downSQL
	}
	return up, down
}

func applyChapterDMigrations(t *testing.T, ctx context.Context, conn *pgx.Conn, versions []string, sqlByVersion map[string]string) {
	t.Helper()
	for _, version := range versions {
		if _, err := conn.Exec(ctx, sqlByVersion[version]); err != nil {
			t.Fatalf("apply %s up: %v", version, err)
		}
	}
}

func readChapterDMigrationState(t *testing.T, ctx context.Context, conn *pgx.Conn) chapterDMigrationState {
	t.Helper()
	state := chapterDMigrationState{ByKind: map[string]int{}}
	if err := conn.QueryRow(ctx, `
		SELECT
		  (SELECT count(*)::int FROM research_artifact_passport),
		  (SELECT count(*)::int FROM research_artifact_version),
		  (SELECT count(*)::int FROM research_artifact_migration_diagnostic)
	`).Scan(&state.Passports, &state.Versions, &state.Diagnostics); err != nil {
		t.Fatalf("read aggregate migration counts: %v", err)
	}
	rows, err := conn.Query(ctx, `
		SELECT entity_kind, count(*)::int
		FROM research_artifact_passport
		GROUP BY entity_kind
		ORDER BY entity_kind
	`)
	if err != nil {
		t.Fatalf("read aggregate kind counts: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var kind string
		var count int
		if err = rows.Scan(&kind, &count); err != nil {
			t.Fatalf("scan aggregate kind count: %v", err)
		}
		state.ByKind[kind] = count
	}
	if err = rows.Err(); err != nil {
		t.Fatalf("iterate aggregate kind counts: %v", err)
	}
	return state
}

func assertNoFabricatedPostDArtifacts(t *testing.T, ctx context.Context, conn *pgx.Conn) {
	t.Helper()
	var count int
	if err := conn.QueryRow(ctx, `
		SELECT count(*)::int
		FROM research_artifact_passport
		WHERE entity_kind IN ('hypothesis', 'branch', 'insight', 'inquiry_edge')
	`).Scan(&count); err != nil {
		t.Fatalf("count fabricated post-D artifacts: %v", err)
	}
	if count != 0 {
		t.Fatalf("fabricated E-N passport rows=%d", count)
	}
}
