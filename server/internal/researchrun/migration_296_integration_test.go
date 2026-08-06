package researchrun

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestMigration296DownUpRestoresFrozenExecutionTarget(t *testing.T) {
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
	downSQL, err := os.ReadFile(filepath.Join("..", "..", "migrations", "296_research_attempt_execution_target.down.sql"))
	if err != nil {
		t.Fatal(err)
	}
	upSQL, err := os.ReadFile(filepath.Join("..", "..", "migrations", "296_research_attempt_execution_target.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	down297SQL, err := os.ReadFile(filepath.Join("..", "..", "migrations", "297_research_execution_circuit.down.sql"))
	if err != nil {
		t.Fatal(err)
	}
	up297SQL, err := os.ReadFile(filepath.Join("..", "..", "migrations", "297_research_execution_circuit.up.sql"))
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
	reapplied296 := false
	reapplied297 := false
	reapplied298 := false
	defer func() {
		if !reapplied296 {
			_, _ = pool.Exec(context.Background(), string(upSQL))
		}
		if !reapplied297 {
			_, _ = pool.Exec(context.Background(), string(up297SQL))
		}
		if !reapplied298 {
			_, _ = pool.Exec(context.Background(), string(up298SQL))
		}
	}()
	if _, err = pool.Exec(ctx, string(down298SQL)); err != nil {
		t.Fatalf("apply migration 298 down before 296 rollback: %v", err)
	}
	if _, err = pool.Exec(ctx, string(down297SQL)); err != nil {
		t.Fatalf("apply migration 297 down before 296 rollback: %v", err)
	}
	if _, err = pool.Exec(ctx, string(downSQL)); err != nil {
		t.Fatalf("apply migration 296 down: %v", err)
	}
	var columns int
	if err = pool.QueryRow(ctx, `
		SELECT count(*)::int FROM information_schema.columns
		WHERE table_schema = current_schema() AND table_name = 'research_task_attempt'
		  AND column_name IN ('execution_adapter', 'runtime_id', 'provider', 'model', 'target_config_fingerprint', 'source_failure_reason')
	`).Scan(&columns); err != nil || columns != 0 {
		t.Fatalf("target columns after down=%d err=%v", columns, err)
	}
	if _, err = pool.Exec(ctx, string(upSQL)); err != nil {
		t.Fatalf("reapply migration 296 up: %v", err)
	}
	reapplied296 = true
	if err = pool.QueryRow(ctx, `
		SELECT count(*)::int FROM information_schema.columns
		WHERE table_schema = current_schema() AND table_name = 'research_task_attempt'
		  AND column_name IN ('execution_adapter', 'runtime_id', 'provider', 'model', 'target_config_fingerprint', 'source_failure_reason')
	`).Scan(&columns); err != nil || columns != 6 {
		t.Fatalf("target columns after up=%d err=%v", columns, err)
	}
	if _, err = pool.Exec(ctx, string(up297SQL)); err != nil {
		t.Fatalf("restore migration 297 after 296 rollback test: %v", err)
	}
	reapplied297 = true
	if _, err = pool.Exec(ctx, string(up298SQL)); err != nil {
		t.Fatalf("restore migration 298 after 296 rollback test: %v", err)
	}
	reapplied298 = true
}
