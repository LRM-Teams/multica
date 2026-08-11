package main

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestArchivedAgentGroupExitMigrationPreservesMessagesAndDMIdentity(t *testing.T) {
	pool := openTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()

	schema := fmt.Sprintf("archived_agent_group_exit_migration_test_%d", time.Now().UnixNano())
	quotedSchema := pgx.Identifier{schema}.Sanitize()
	if _, err := conn.Exec(ctx, "CREATE SCHEMA "+quotedSchema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+quotedSchema+" CASCADE") })
	if _, err := conn.Exec(ctx, "SET search_path TO "+quotedSchema); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `
		CREATE TABLE agent (
			id UUID PRIMARY KEY,
			workspace_id UUID NOT NULL,
			archived_at TIMESTAMPTZ
		);
		CREATE TABLE channel (
			id UUID PRIMARY KEY,
			workspace_id UUID NOT NULL,
			kind TEXT NOT NULL
		);
		CREATE TABLE channel_member (
			channel_id UUID NOT NULL,
			workspace_id UUID NOT NULL,
			member_type TEXT NOT NULL,
			member_id UUID NOT NULL,
			PRIMARY KEY (channel_id, member_type, member_id)
		);
		CREATE TABLE channel_message (
			id UUID PRIMARY KEY,
			channel_id UUID NOT NULL,
			workspace_id UUID NOT NULL,
			author_type TEXT NOT NULL,
			author_id UUID,
			content TEXT NOT NULL
		);
		INSERT INTO agent VALUES ('10000000-0000-0000-0000-000000000333', '20000000-0000-0000-0000-000000000333', NULL);
		INSERT INTO channel VALUES
			('30000000-0000-0000-0000-000000000333', '20000000-0000-0000-0000-000000000333', 'group'),
			('40000000-0000-0000-0000-000000000333', '20000000-0000-0000-0000-000000000333', 'dm');
		INSERT INTO channel_member VALUES
			('30000000-0000-0000-0000-000000000333', '20000000-0000-0000-0000-000000000333', 'agent', '10000000-0000-0000-0000-000000000333'),
			('40000000-0000-0000-0000-000000000333', '20000000-0000-0000-0000-000000000333', 'agent', '10000000-0000-0000-0000-000000000333');
		INSERT INTO channel_message VALUES
			('50000000-0000-0000-0000-000000000333', '30000000-0000-0000-0000-000000000333', '20000000-0000-0000-0000-000000000333', 'agent', '10000000-0000-0000-0000-000000000333', 'group history'),
			('60000000-0000-0000-0000-000000000333', '40000000-0000-0000-0000-000000000333', '20000000-0000-0000-0000-000000000333', 'agent', '10000000-0000-0000-0000-000000000333', 'dm history');
	`); err != nil {
		t.Fatal(err)
	}

	_, testFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate test file")
	}
	migrationsDir := filepath.Join(filepath.Dir(testFile), "..", "..", "migrations")
	applyMigrationFile(t, ctx, conn, filepath.Join(migrationsDir, "333_archived_agent_group_exit.up.sql"))
	if _, err := conn.Exec(ctx, `UPDATE agent SET archived_at = now() WHERE id = '10000000-0000-0000-0000-000000000333'`); err != nil {
		t.Fatal(err)
	}
	var groupMembers, dmMembers, messages int
	if err := conn.QueryRow(ctx, `
		SELECT
		  (SELECT count(*) FROM channel_member member JOIN channel ch ON ch.id = member.channel_id WHERE ch.kind = 'group'),
		  (SELECT count(*) FROM channel_member member JOIN channel ch ON ch.id = member.channel_id WHERE ch.kind = 'dm'),
		  (SELECT count(*) FROM channel_message)
	`).Scan(&groupMembers, &dmMembers, &messages); err != nil {
		t.Fatal(err)
	}
	if groupMembers != 0 || dmMembers != 1 || messages != 2 {
		t.Fatalf("after archive group/dm/messages=%d/%d/%d want 0/1/2", groupMembers, dmMembers, messages)
	}

	applyMigrationFile(t, ctx, conn, filepath.Join(migrationsDir, "333_archived_agent_group_exit.down.sql"))
	if _, err := conn.Exec(ctx, `
		UPDATE agent SET archived_at = NULL WHERE id = '10000000-0000-0000-0000-000000000333';
		INSERT INTO channel_member VALUES ('30000000-0000-0000-0000-000000000333', '20000000-0000-0000-0000-000000000333', 'agent', '10000000-0000-0000-0000-000000000333');
		UPDATE agent SET archived_at = now() WHERE id = '10000000-0000-0000-0000-000000000333';
	`); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM channel_member member JOIN channel ch ON ch.id = member.channel_id WHERE ch.kind = 'group'`).Scan(&groupMembers); err != nil {
		t.Fatal(err)
	}
	if groupMembers != 1 {
		t.Fatalf("down migration still removed group membership: %d", groupMembers)
	}
}
