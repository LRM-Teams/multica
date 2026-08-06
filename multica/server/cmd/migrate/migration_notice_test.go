package main

import (
	"bytes"
	"context"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestMigrationPoolForwardsPostgresNotice(t *testing.T) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://multica:multica@localhost:5432/multica?sslmode=disable"
	}

	var output bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool, err := newMigrationPool(ctx, dbURL, &output)
	if err != nil {
		t.Skipf("could not connect to %s: %v", dbURL, err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Skipf("database not reachable at %s: %v", dbURL, err)
	}
	t.Cleanup(pool.Close)

	suffix := fmt.Sprintf("%d_%d", time.Now().UnixNano(), rand.Uint32())
	schema := "migrate_notice_test_" + suffix
	quotedSchema := pgx.Identifier{schema}.Sanitize()
	if _, err := pool.Exec(ctx, "CREATE SCHEMA "+quotedSchema); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		if _, err := pool.Exec(cleanupCtx, "DROP SCHEMA IF EXISTS "+quotedSchema+" CASCADE"); err != nil {
			t.Logf("drop schema %s: %v", schema, err)
		}
	})

	const marker = "migration notice forwarding marker"
	migrationPath := filepath.Join(t.TempDir(), "001_notice.up.sql")
	migrationSQL := "DO $$ BEGIN RAISE NOTICE '" + marker + "'; END $$;\n"
	if err := os.WriteFile(migrationPath, []byte(migrationSQL), 0o600); err != nil {
		t.Fatalf("write migration: %v", err)
	}

	if err := runMigrations(ctx, pool, runOptions{
		Direction:             "up",
		Files:                 []string{migrationPath},
		SchemaMigrationsTable: schema + ".schema_migrations",
		AdvisoryLockKey:       int64(rand.Uint64()&0x7fffffffffffffff) | 1,
	}); err != nil {
		t.Fatalf("run migration: %v", err)
	}

	wantLine := "  notice  " + marker + "\n"
	if got := output.String(); !strings.Contains(got, wantLine) {
		t.Fatalf("migration output %q does not contain PostgreSQL notice line %q", got, wantLine)
	}
}
