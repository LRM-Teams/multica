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
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/multica-ai/multica/server/internal/agentavatar"
)

func TestAgentAvatarMigration314MovesOnlySystemPresetsToOSS(t *testing.T) {
	pool := openTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire connection: %v", err)
	}
	defer conn.Release()

	schema := fmt.Sprintf("agent_avatar_oss_migration_test_%d", time.Now().UnixNano())
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

	_, testFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate test file")
	}
	migrationsDir := filepath.Join(filepath.Dir(testFile), "..", "..", "migrations")
	applyMigrationFile(t, ctx, conn, filepath.Join(migrationsDir, "203_agent_durable_avatar.up.sql"))

	const (
		assignedID = "00000000-0000-0000-0000-000000000314"
		pickedID   = "00000000-0000-0000-0000-000000000315"
		uploadedID = "00000000-0000-0000-0000-000000000316"
		customID   = "00000000-0000-0000-0000-000000000317"
	)
	var attachmentID string
	if err := conn.QueryRow(ctx, `INSERT INTO attachment DEFAULT VALUES RETURNING id`).Scan(&attachmentID); err != nil {
		t.Fatalf("create avatar attachment: %v", err)
	}
	if _, err := conn.Exec(ctx, `
		INSERT INTO agent (id, avatar_url, avatar_source, avatar_attachment_id) VALUES
			($1, '/agent-avatars/human-01.jpg', 'assigned', NULL),
			($2, '/agent-avatars/human-24.jpg', 'picked', NULL),
			($3, 'https://cdn.example.com/uploaded.png', 'uploaded', $5),
			($4, 'https://cdn.example.com/custom-system.png', 'assigned', NULL)
	`, assignedID, pickedID, uploadedID, customID, attachmentID); err != nil {
		t.Fatalf("seed pre-314 avatars: %v", err)
	}

	applyMigrationFile(t, ctx, conn, filepath.Join(migrationsDir, "314_agent_avatar_oss_presets.up.sql"))

	assertMigrationAvatarURL(t, ctx, conn, assignedID, agentavatar.LegacyURL(1))
	assertMigrationAvatarURL(t, ctx, conn, pickedID, agentavatar.LegacyURL(24))
	assertMigrationAvatarURL(t, ctx, conn, uploadedID, "https://cdn.example.com/uploaded.png")
	assertMigrationAvatarURL(t, ctx, conn, customID, "https://cdn.example.com/custom-system.png")

	applyMigrationFile(t, ctx, conn, filepath.Join(migrationsDir, "421_agent_avatar_v3_presets.up.sql"))

	const directID = "00000000-0000-0000-0000-000000000318"
	var directURL string
	if err := conn.QueryRow(ctx, `
		INSERT INTO agent (id, avatar_url) VALUES ($1, NULL) RETURNING avatar_url
	`, directID).Scan(&directURL); err != nil {
		t.Fatalf("insert post-421 Agent: %v", err)
	}
	if want := agentavatar.DefaultURL(directID); directURL != want {
		t.Fatalf("post-421 default = %q, want %q", directURL, want)
	}

	applyMigrationFile(t, ctx, conn, filepath.Join(migrationsDir, "421_agent_avatar_v3_presets.down.sql"))
	applyMigrationFile(t, ctx, conn, filepath.Join(migrationsDir, "314_agent_avatar_oss_presets.down.sql"))
	assertMigrationAvatarURL(t, ctx, conn, assignedID, "/agent-avatars/human-01.jpg")
	assertMigrationAvatarURL(t, ctx, conn, pickedID, "/agent-avatars/human-24.jpg")
	assertMigrationAvatarURL(t, ctx, conn, uploadedID, "https://cdn.example.com/uploaded.png")
	assertMigrationAvatarURL(t, ctx, conn, customID, "https://cdn.example.com/custom-system.png")
}

func applyMigrationFile(t *testing.T, ctx context.Context, conn *pgxpool.Conn, path string) {
	t.Helper()
	sql, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read migration %s: %v", filepath.Base(path), err)
	}
	if _, err := conn.Exec(ctx, string(sql)); err != nil {
		t.Fatalf("apply migration %s: %v", filepath.Base(path), err)
	}
}

func assertMigrationAvatarURL(t *testing.T, ctx context.Context, conn *pgxpool.Conn, agentID, want string) {
	t.Helper()
	var got string
	if err := conn.QueryRow(ctx, `SELECT avatar_url FROM agent WHERE id = $1`, agentID).Scan(&got); err != nil {
		t.Fatalf("read Agent %s avatar: %v", agentID, err)
	}
	if got != want {
		t.Fatalf("Agent %s avatar = %q, want %q", agentID, got, want)
	}
}
