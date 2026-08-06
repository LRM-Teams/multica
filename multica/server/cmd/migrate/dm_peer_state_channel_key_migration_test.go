package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// readMigrationPair reads a migration's up.sql and down.sql content by
// version name (e.g. "254_dm_peer_state_channel_key").
func readMigrationPair(t *testing.T, version string) (upSQL, downSQL string) {
	t.Helper()
	_, testFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate test file")
	}
	migrationsDir := filepath.Join(filepath.Dir(testFile), "..", "..", "migrations")
	up, err := os.ReadFile(filepath.Join(migrationsDir, version+".up.sql"))
	if err != nil {
		t.Fatalf("read %s up: %v", version, err)
	}
	down, err := os.ReadFile(filepath.Join(migrationsDir, version+".down.sql"))
	if err != nil {
		t.Fatalf("read %s down: %v", version, err)
	}
	return string(up), string(down)
}

// TestDmPeerState254RefusesToDropChannelPeerRows locks task #101's fix for
// migration 254: peer_type='channel' rows are real, active viewer
// preference data (dm.go) with no safe remap target — 254's own up.sql
// comment explains that remapping to user/agent would recreate the exact
// 1:1-DM collision this migration exists to avoid. The original DELETE
// silently destroyed them on rollback with no warning.
func TestDmPeerState254RefusesToDropChannelPeerRows(t *testing.T) {
	pool := openTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire connection: %v", err)
	}
	defer conn.Release()

	schema := fmt.Sprintf("dm_peer_state_254_migration_test_%d", time.Now().UnixNano())
	quotedSchema := pgx.Identifier{schema}.Sanitize()
	if _, err := conn.Exec(ctx, "CREATE SCHEMA "+quotedSchema); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+quotedSchema+" CASCADE"); err != nil {
			t.Logf("drop schema %s: %v", schema, err)
		}
	})
	if _, err := conn.Exec(ctx, "SET search_path TO "+quotedSchema); err != nil {
		t.Fatalf("set search path: %v", err)
	}

	if _, err := conn.Exec(ctx, `
		CREATE TABLE workspace (id UUID PRIMARY KEY DEFAULT gen_random_uuid());
		CREATE TABLE "user" (id UUID PRIMARY KEY DEFAULT gen_random_uuid());
		CREATE TABLE dm_peer_state (
		  workspace_id UUID NOT NULL REFERENCES workspace(id),
		  user_id UUID NOT NULL REFERENCES "user"(id),
		  peer_type TEXT NOT NULL CHECK (peer_type IN ('user', 'agent')),
		  peer_id UUID NOT NULL,
		  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
		  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
		  PRIMARY KEY (workspace_id, user_id, peer_type, peer_id)
		);
	`); err != nil {
		t.Fatalf("create minimal pre-254 schema: %v", err)
	}

	var wsID, userID string
	if err := conn.QueryRow(ctx, `INSERT INTO workspace DEFAULT VALUES RETURNING id`).Scan(&wsID); err != nil {
		t.Fatalf("seed workspace: %v", err)
	}
	if err := conn.QueryRow(ctx, `INSERT INTO "user" DEFAULT VALUES RETURNING id`).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}

	upSQL, downSQL := readMigrationPair(t, "254_dm_peer_state_channel_key")

	if _, err := conn.Exec(ctx, upSQL); err != nil {
		t.Fatalf("apply 254 up: %v", err)
	}
	if _, err := conn.Exec(ctx, downSQL); err != nil {
		t.Fatalf("down must succeed with no channel-peer rows: %v", err)
	}

	if _, err := conn.Exec(ctx, upSQL); err != nil {
		t.Fatalf("re-apply 254 up: %v", err)
	}
	if _, err := conn.Exec(ctx, `
		INSERT INTO dm_peer_state (workspace_id, user_id, peer_type, peer_id)
		VALUES ($1, $2, 'channel', gen_random_uuid())
	`, wsID, userID); err != nil {
		t.Fatalf("seed channel-peer row: %v", err)
	}

	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	_, downErr := tx.Exec(ctx, downSQL)
	if downErr == nil {
		_ = tx.Rollback(ctx)
		t.Fatal("down must fail while a channel-peer row exists, it succeeded instead")
	}
	if !strings.Contains(downErr.Error(), "peer_type='channel'") {
		_ = tx.Rollback(ctx)
		t.Fatalf("down failed for the wrong reason: %v", downErr)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatalf("rollback: %v", err)
	}

	var count int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM dm_peer_state WHERE peer_type = 'channel'`).Scan(&count); err != nil {
		t.Fatalf("count channel-peer rows after failed down: %v", err)
	}
	if count != 1 {
		t.Fatalf("channel-peer row count after failed down = %d, want 1", count)
	}

	if _, err := conn.Exec(ctx, `DELETE FROM dm_peer_state WHERE peer_type = 'channel'`); err != nil {
		t.Fatalf("manual delete: %v", err)
	}
	if _, err := conn.Exec(ctx, downSQL); err != nil {
		t.Fatalf("down must succeed once no channel-peer rows remain: %v", err)
	}
}
