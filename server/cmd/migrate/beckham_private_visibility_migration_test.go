package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// LRM-233: flipping existing Beckhams to private must not auto-kick them from
// #general (issue default: no auto-removal of existing memberships).
func TestBeckhamPrivateVisibilityMigration206PreservesGeneralMembership(t *testing.T) {
	pool := openTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire connection: %v", err)
	}
	defer conn.Release()

	schema := fmt.Sprintf("beckham_private_visibility_migration_test_%d", time.Now().UnixNano())
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

	workspaceID := "10000000-0000-0000-0000-000000000101"
	ownerID := "20000000-0000-0000-0000-000000000101"
	beckhamID := "30000000-0000-0000-0000-000000000101"
	generalID := "40000000-0000-0000-0000-000000000101"

	if _, err := conn.Exec(ctx, `
		CREATE TABLE member (
			workspace_id UUID NOT NULL,
			user_id UUID NOT NULL,
			PRIMARY KEY (workspace_id, user_id)
		);
		CREATE TABLE agent (
			id UUID PRIMARY KEY,
			workspace_id UUID NOT NULL,
			visibility TEXT NOT NULL,
			archived_at TIMESTAMPTZ,
			managed_role TEXT,
			updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
		);
		CREATE TABLE channel (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			workspace_id UUID NOT NULL,
			name TEXT NOT NULL,
			description TEXT,
			created_by UUID,
			kind TEXT NOT NULL DEFAULT 'group',
			system_key TEXT,
			archived_at TIMESTAMPTZ,
			project_id UUID,
			lark_chat_id TEXT,
			group_manager_agent_id UUID
		);
		CREATE UNIQUE INDEX channel_workspace_system_key_unique
			ON channel (workspace_id, system_key)
			WHERE system_key IS NOT NULL;
		CREATE TABLE channel_member (
			channel_id UUID NOT NULL,
			workspace_id UUID NOT NULL,
			member_type TEXT NOT NULL,
			member_id UUID NOT NULL,
			PRIMARY KEY (channel_id, member_type, member_id)
		);
	`); err != nil {
		t.Fatalf("seed pre-206 schema: %v", err)
	}
	if _, err := conn.Exec(ctx, `INSERT INTO member (workspace_id, user_id) VALUES ($1, $2)`, workspaceID, ownerID); err != nil {
		t.Fatalf("seed member: %v", err)
	}
	if _, err := conn.Exec(ctx, `
		INSERT INTO channel (id, workspace_id, name, description, created_by, kind, system_key)
		VALUES ($1, $2, 'general', 'Workspace-wide conversation', $3, 'group', 'general')
	`, generalID, workspaceID, ownerID); err != nil {
		t.Fatalf("seed general channel: %v", err)
	}
	if _, err := conn.Exec(ctx, `
		INSERT INTO agent (id, workspace_id, visibility, managed_role)
		VALUES ($1, $2, 'workspace', 'group_manager')
	`, beckhamID, workspaceID); err != nil {
		t.Fatalf("seed beckham agent: %v", err)
	}
	if _, err := conn.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3)
	`, generalID, workspaceID, beckhamID); err != nil {
		t.Fatalf("seed general membership: %v", err)
	}

	_, testFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate test file")
	}
	migrationsDir := filepath.Join(filepath.Dir(testFile), "..", "..", "migrations")
	upSQL, err := os.ReadFile(filepath.Join(migrationsDir, "206_beckham_private_visibility.up.sql"))
	if err != nil {
		t.Fatalf("read migration 206 up: %v", err)
	}
	if _, err := conn.Exec(ctx, string(upSQL)); err != nil {
		t.Fatalf("apply migration 206 up: %v", err)
	}

	var visibility string
	if err := conn.QueryRow(ctx, `SELECT visibility FROM agent WHERE id = $1`, beckhamID).Scan(&visibility); err != nil {
		t.Fatalf("load visibility: %v", err)
	}
	if visibility != "private" {
		t.Fatalf("visibility = %q, want private", visibility)
	}

	var n int
	if err := conn.QueryRow(ctx, `
		SELECT count(*) FROM channel_member
		WHERE channel_id = $1 AND member_type = 'agent' AND member_id = $2
	`, generalID, beckhamID).Scan(&n); err != nil {
		t.Fatalf("count membership: %v", err)
	}
	if n != 1 {
		t.Fatalf("group manager membership rows = %d, want 1 (no auto-kick from UPDATE alone)", n)
	}

	if _, err := conn.Exec(ctx, `SELECT ensure_system_general_channel($1, $2)`, workspaceID, ownerID); err != nil {
		t.Fatalf("ensure_system_general_channel after private flip: %v", err)
	}
	if err := conn.QueryRow(ctx, `
		SELECT count(*) FROM channel_member
		WHERE channel_id = $1 AND member_type = 'agent' AND member_id = $2
	`, generalID, beckhamID).Scan(&n); err != nil {
		t.Fatalf("count membership after ensure: %v", err)
	}
	if n != 1 {
		t.Fatalf("after ensure, membership rows = %d, want 1 (private group manager preserved)", n)
	}
}
