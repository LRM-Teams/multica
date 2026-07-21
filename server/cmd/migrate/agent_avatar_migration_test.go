package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var migratedAgentAvatarPattern = regexp.MustCompile(`^/agent-avatars/human-(0[1-9]|1[0-9]|2[0-4])\.jpg$`)

func TestAgentAvatarMigration203PreservesAndEnforcesDurableTruth(t *testing.T) {
	pool := openTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire connection: %v", err)
	}
	defer conn.Release()

	schema := fmt.Sprintf("agent_avatar_migration_test_%d", time.Now().UnixNano())
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
		CREATE TABLE attachment (id UUID PRIMARY KEY DEFAULT gen_random_uuid());
		CREATE TABLE agent (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			avatar_url TEXT
		);
	`); err != nil {
		t.Fatalf("create migration prerequisites: %v", err)
	}

	type historicalRow struct {
		id  string
		url *string
	}
	customURL := "  https://cdn.example.test/custom agent.png?x=1  "
	presetURL := "/agent-avatars/human-07.jpg"
	blankURL := "   "
	fixtures := []historicalRow{
		{id: "00000000-0000-0000-0000-000000000201", url: &customURL},
		{id: "00000000-0000-0000-0000-000000000202", url: &presetURL},
		{id: "00000000-0000-0000-0000-000000000203", url: &blankURL},
		{id: "00000000-0000-0000-0000-000000000204", url: nil},
	}
	for _, fixture := range fixtures {
		if _, err := conn.Exec(ctx, `INSERT INTO agent (id, avatar_url) VALUES ($1, $2)`, fixture.id, fixture.url); err != nil {
			t.Fatalf("seed historical agent %s: %v", fixture.id, err)
		}
	}

	_, testFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate test file")
	}
	migrationsDir := filepath.Join(filepath.Dir(testFile), "..", "..", "migrations")
	upSQL, err := os.ReadFile(filepath.Join(migrationsDir, "203_agent_durable_avatar.up.sql"))
	if err != nil {
		t.Fatalf("read migration 203 up: %v", err)
	}
	if _, err := conn.Exec(ctx, string(upSQL)); err != nil {
		t.Fatalf("apply migration 203 up: %v", err)
	}

	var count int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM agent`).Scan(&count); err != nil || count != len(fixtures) {
		t.Fatalf("row count after migration = %d, err=%v, want %d", count, err, len(fixtures))
	}
	for _, fixture := range fixtures {
		var gotURL, source string
		var attachmentID *string
		if err := conn.QueryRow(ctx, `
			SELECT avatar_url, avatar_source, avatar_attachment_id::text
			FROM agent WHERE id = $1
		`, fixture.id).Scan(&gotURL, &source, &attachmentID); err != nil {
			t.Fatalf("read migrated agent %s: %v", fixture.id, err)
		}
		if source != "assigned" || attachmentID != nil {
			t.Fatalf("migrated agent %s source=%q attachment=%v, want assigned/null", fixture.id, source, attachmentID)
		}
		if fixture.url != nil && strings.TrimSpace(*fixture.url) != "" {
			if gotURL != *fixture.url {
				t.Fatalf("historical URL %s changed byte-for-byte: got %q want %q", fixture.id, gotURL, *fixture.url)
			}
		} else if !migratedAgentAvatarPattern.MatchString(gotURL) {
			t.Fatalf("blank historical URL %s became %q, want concrete pool path", fixture.id, gotURL)
		}
	}

	var directURL, directSource string
	var directAttachmentID *string
	if err := conn.QueryRow(ctx, `
		INSERT INTO agent (id, avatar_url)
		VALUES ('00000000-0000-0000-0000-000000000205', NULL)
		RETURNING avatar_url, avatar_source, avatar_attachment_id::text
	`).Scan(&directURL, &directSource, &directAttachmentID); err != nil {
		t.Fatalf("post-203 direct blank insert: %v", err)
	}
	if !migratedAgentAvatarPattern.MatchString(directURL) || directSource != "assigned" || directAttachmentID != nil {
		t.Fatalf("direct blank insert url=%q source=%q attachment=%v", directURL, directSource, directAttachmentID)
	}

	_, err = conn.Exec(ctx, `UPDATE agent SET avatar_source = 'legacy' WHERE id = $1`, fixtures[0].id)
	assertPgCode(t, err, "23514", "invalid avatar source")
	_, err = conn.Exec(ctx, `UPDATE agent SET avatar_source = 'uploaded' WHERE id = $1`, fixtures[0].id)
	assertPgCode(t, err, "23514", "uploaded source without attachment")
	_, err = conn.Exec(ctx, `UPDATE agent SET avatar_url = '   ' WHERE id = $1`, fixtures[0].id)
	assertPgCode(t, err, "23514", "blank durable avatar URL")
	_, err = conn.Exec(ctx, `UPDATE agent SET avatar_url = NULL WHERE id = $1`, fixtures[0].id)
	assertPgCode(t, err, "23502", "null durable avatar URL")

	var uniqueAttachmentID string
	if err := conn.QueryRow(ctx, `INSERT INTO attachment DEFAULT VALUES RETURNING id`).Scan(&uniqueAttachmentID); err != nil {
		t.Fatalf("create unique avatar attachment: %v", err)
	}
	if _, err := conn.Exec(ctx, `
		UPDATE agent SET avatar_source = 'uploaded', avatar_attachment_id = $2 WHERE id = $1
	`, fixtures[0].id, uniqueAttachmentID); err != nil {
		t.Fatalf("bind first agent avatar attachment: %v", err)
	}
	_, err = conn.Exec(ctx, `
		UPDATE agent SET avatar_source = 'uploaded', avatar_attachment_id = $2 WHERE id = $1
	`, fixtures[1].id, uniqueAttachmentID)
	assertPgCode(t, err, "23505", "reuse one avatar attachment across agents")

	downSQL, err := os.ReadFile(filepath.Join(migrationsDir, "203_agent_durable_avatar.down.sql"))
	if err != nil {
		t.Fatalf("read migration 203 down: %v", err)
	}
	if _, err := conn.Exec(ctx, string(downSQL)); err != nil {
		t.Fatalf("apply migration 203 down: %v", err)
	}
	var avatarNullable string
	if err := conn.QueryRow(ctx, `
		SELECT is_nullable
		FROM information_schema.columns
		WHERE table_schema = current_schema() AND table_name = 'agent' AND column_name = 'avatar_url'
	`).Scan(&avatarNullable); err != nil || avatarNullable != "YES" {
		t.Fatalf("avatar_url nullable after down = %q, err=%v", avatarNullable, err)
	}
	for _, column := range []string{"avatar_source", "avatar_attachment_id"} {
		var exists bool
		if err := conn.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM information_schema.columns
				WHERE table_schema = current_schema() AND table_name = 'agent' AND column_name = $1
			)
		`, column).Scan(&exists); err != nil {
			t.Fatalf("check down column %s: %v", column, err)
		}
		if exists {
			t.Errorf("column %s still exists after down", column)
		}
	}
}

func assertPgCode(t *testing.T, err error, code, operation string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s unexpectedly succeeded", operation)
	}
	pgErr, ok := err.(*pgconn.PgError)
	if !ok || pgErr.Code != code {
		t.Fatalf("%s error = %v, want PostgreSQL code %s", operation, err, code)
	}
}
