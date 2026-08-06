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
	reapplied := false
	defer func() {
		if !reapplied {
			_, _ = pool.Exec(context.Background(), string(upSQL))
		}
	}()
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
	reapplied = true
	if err = pool.QueryRow(ctx, `
		SELECT count(*)::int FROM information_schema.columns
		WHERE table_schema = current_schema() AND table_name = 'research_task_attempt'
		  AND column_name IN ('execution_adapter', 'runtime_id', 'provider', 'model', 'target_config_fingerprint', 'source_failure_reason')
	`).Scan(&columns); err != nil || columns != 6 {
		t.Fatalf("target columns after up=%d err=%v", columns, err)
	}
}
