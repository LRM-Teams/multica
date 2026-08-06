package researchrun

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestMigration295DownUpRestoresReconcileFence(t *testing.T) {
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
	defer func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1::uuid`, fixture.workspaceID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1::uuid`, fixture.userID)
	}()
	downSQL, err := os.ReadFile(filepath.Join("..", "..", "migrations", "295_research_reconcile_fencing.down.sql"))
	if err != nil {
		t.Fatal(err)
	}
	upSQL, err := os.ReadFile(filepath.Join("..", "..", "migrations", "295_research_reconcile_fencing.up.sql"))
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
		t.Fatalf("apply migration 295 down: %v", err)
	}
	var columnCount int
	if err = pool.QueryRow(ctx, `
		SELECT count(*)::int
		FROM information_schema.columns
		WHERE table_schema = current_schema()
		  AND table_name = 'research_session'
		  AND column_name = 'reconcile_lease_generation'
	`).Scan(&columnCount); err != nil || columnCount != 0 {
		t.Fatalf("generation column after down=%d err=%v", columnCount, err)
	}
	if _, err = pool.Exec(ctx, string(upSQL)); err != nil {
		t.Fatalf("reapply migration 295 up: %v", err)
	}
	reapplied = true
	if err = pool.QueryRow(ctx, `
		SELECT count(*)::int
		FROM information_schema.columns
		WHERE table_schema = current_schema()
		  AND table_name = 'research_session'
		  AND column_name = 'reconcile_lease_generation'
	`).Scan(&columnCount); err != nil || columnCount != 1 {
		t.Fatalf("generation column after up=%d err=%v", columnCount, err)
	}
	var invalidPairAccepted bool
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, pairErr := tx.Exec(ctx, `
		UPDATE research_session
		SET reconcile_lease_token = gen_random_uuid(), reconcile_lease_expires_at = NULL
		WHERE id = $1::uuid
	`, fixture.sessionID)
	invalidPairAccepted = pairErr == nil
	_ = tx.Rollback(ctx)
	if invalidPairAccepted {
		t.Fatal("lease token/expiry pair constraint accepted a partial owner")
	}
}
