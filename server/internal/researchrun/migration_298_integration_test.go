package researchrun

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestMigration298DownUpRestoresAttemptProbeBindingSchema(t *testing.T) {
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
	defer cleanupResearchCircuitFixture(pool, fixture)
	downSQL, err := os.ReadFile(filepath.Join("..", "..", "migrations", "298_research_attempt_circuit_probe.down.sql"))
	if err != nil {
		t.Fatal(err)
	}
	upSQL, err := os.ReadFile(filepath.Join("..", "..", "migrations", "298_research_attempt_circuit_probe.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	var circuitID, transitionID string
	if err = pool.QueryRow(ctx, `
		INSERT INTO research_execution_circuit (
		  workspace_id, scope, target_key, state, generation,
		  consecutive_failures, opened_at, next_probe_at
		) VALUES ($1::uuid, 'agent', 'migration-298-agent', 'open', 1, 1, now(), now())
		RETURNING id::text
	`, fixture.workspaceID).Scan(&circuitID); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `
		INSERT INTO research_execution_circuit_transition (
		  workspace_id, circuit_id, generation, from_state, to_state, cause, diagnostics
		) VALUES ($1::uuid, $2::uuid, 1, 'half_open', 'open', 'probe_abandoned', 'cancelled by user')
		RETURNING id::text
	`, fixture.workspaceID, circuitID).Scan(&transitionID); err != nil {
		t.Fatal(err)
	}
	reapplied := false
	defer func() {
		if !reapplied {
			_, _ = pool.Exec(context.Background(), string(upSQL))
		}
	}()
	if _, err = pool.Exec(ctx, string(downSQL)); err != nil {
		t.Fatalf("apply migration 298 down: %v", err)
	}
	var tables int
	if err = pool.QueryRow(ctx, `
		SELECT count(*)::int FROM information_schema.tables
		WHERE table_schema = current_schema() AND table_name = 'research_attempt_circuit_probe'
	`).Scan(&tables); err != nil || tables != 0 {
		t.Fatalf("probe table after down=%d err=%v", tables, err)
	}
	var cause, diagnostics string
	if err = pool.QueryRow(ctx, `
		SELECT cause, diagnostics FROM research_execution_circuit_transition WHERE id = $1::uuid
	`, transitionID).Scan(&cause, &diagnostics); err != nil || cause != "probe_failed" || !strings.Contains(diagnostics, "rollback 298") {
		t.Fatalf("downgraded transition cause=%q diagnostics=%q err=%v", cause, diagnostics, err)
	}
	if _, err = pool.Exec(ctx, string(upSQL)); err != nil {
		t.Fatalf("reapply migration 298 up: %v", err)
	}
	reapplied = true
	if err = pool.QueryRow(ctx, `
		SELECT count(*)::int FROM information_schema.tables
		WHERE table_schema = current_schema() AND table_name = 'research_attempt_circuit_probe'
	`).Scan(&tables); err != nil || tables != 1 {
		t.Fatalf("probe table after up=%d err=%v", tables, err)
	}
	var indexes int
	if err = pool.QueryRow(ctx, `
		SELECT count(*)::int FROM pg_indexes
		WHERE schemaname = current_schema()
		  AND indexname IN (
		    'research_attempt_circuit_probe_active_circuit_uidx',
		    'research_attempt_circuit_probe_attempt_idx'
		  )
	`).Scan(&indexes); err != nil || indexes != 2 {
		t.Fatalf("probe indexes after up=%d err=%v", indexes, err)
	}
}
