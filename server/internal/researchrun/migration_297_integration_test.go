package researchrun

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestMigration297DownUpRestoresExecutionCircuitSchema(t *testing.T) {
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
	downSQL, err := os.ReadFile(filepath.Join("..", "..", "migrations", "297_research_execution_circuit.down.sql"))
	if err != nil {
		t.Fatal(err)
	}
	upSQL, err := os.ReadFile(filepath.Join("..", "..", "migrations", "297_research_execution_circuit.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	down298SQL, err := os.ReadFile(filepath.Join("..", "..", "migrations", "298_research_attempt_circuit_probe.down.sql"))
	if err != nil {
		t.Fatal(err)
	}
	up298SQL, err := os.ReadFile(filepath.Join("..", "..", "migrations", "298_research_attempt_circuit_probe.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	reapplied := false
	reapplied298 := false
	defer func() {
		if !reapplied {
			_, _ = pool.Exec(context.Background(), string(upSQL))
		}
		if !reapplied298 {
			_, _ = pool.Exec(context.Background(), string(up298SQL))
		}
	}()
	if _, err = pool.Exec(ctx, string(down298SQL)); err != nil {
		t.Fatalf("apply migration 298 down before 297 rollback: %v", err)
	}
	if _, err = pool.Exec(ctx, string(downSQL)); err != nil {
		t.Fatalf("apply migration 297 down: %v", err)
	}
	var columns, tables int
	if err = pool.QueryRow(ctx, `
		SELECT count(*)::int FROM information_schema.columns
		WHERE table_schema = current_schema() AND table_name = 'research_task_attempt'
		  AND column_name IN ('agent_config_fingerprint', 'runtime_config_fingerprint', 'provider_config_fingerprint')
	`).Scan(&columns); err != nil || columns != 0 {
		t.Fatalf("scoped target columns after down=%d err=%v", columns, err)
	}
	if err = pool.QueryRow(ctx, `
		SELECT count(*)::int FROM information_schema.tables
		WHERE table_schema = current_schema()
		  AND table_name IN ('research_execution_circuit', 'research_execution_circuit_transition')
	`).Scan(&tables); err != nil || tables != 0 {
		t.Fatalf("circuit tables after down=%d err=%v", tables, err)
	}
	if _, err = pool.Exec(ctx, string(upSQL)); err != nil {
		t.Fatalf("reapply migration 297 up: %v", err)
	}
	reapplied = true
	if _, err = pool.Exec(ctx, string(up298SQL)); err != nil {
		t.Fatalf("restore migration 298 after 297 rollback test: %v", err)
	}
	reapplied298 = true
	if err = pool.QueryRow(ctx, `
		SELECT count(*)::int FROM information_schema.columns
		WHERE table_schema = current_schema() AND table_name = 'research_task_attempt'
		  AND column_name IN ('agent_config_fingerprint', 'runtime_config_fingerprint', 'provider_config_fingerprint')
	`).Scan(&columns); err != nil || columns != 3 {
		t.Fatalf("scoped target columns after up=%d err=%v", columns, err)
	}
	if err = pool.QueryRow(ctx, `
		SELECT count(*)::int FROM information_schema.tables
		WHERE table_schema = current_schema()
		  AND table_name IN ('research_execution_circuit', 'research_execution_circuit_transition')
	`).Scan(&tables); err != nil || tables != 2 {
		t.Fatalf("circuit tables after up=%d err=%v", tables, err)
	}
	var indexes int
	if err = pool.QueryRow(ctx, `
		SELECT count(*)::int FROM pg_indexes
		WHERE schemaname = current_schema()
		  AND indexname IN (
		    'research_execution_circuit_probe_due_idx',
		    'research_execution_circuit_probe_lease_idx',
		    'research_execution_circuit_transition_attempt_observation_uidx'
		  )
	`).Scan(&indexes); err != nil || indexes != 3 {
		t.Fatalf("circuit indexes after up=%d err=%v", indexes, err)
	}
}
