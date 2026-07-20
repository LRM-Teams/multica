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

func TestClearLegacyDraftRandomAgentAvatarsMigration(t *testing.T) {
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
		CREATE TABLE agent (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			avatar_url TEXT,
			updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
		);
		CREATE TABLE agent_creation_draft (
			used_agent_id UUID,
			avatar_url TEXT,
			status TEXT NOT NULL
		);
	`); err != nil {
		t.Fatalf("create migration prerequisites: %v", err)
	}

	ids := map[string]string{}
	for name, avatarURL := range map[string]string{
		"automatic":        "/agent-avatars/human-01.jpg",
		"unlinked":         "/agent-avatars/human-02.jpg",
		"explicit-draft":   "/agent-avatars/human-03.jpg",
		"windy":            "/agent-avatars/human-11.jpg",
		"custom":           "https://cdn.example.com/avatar.png",
		"unused-draft":     "/agent-avatars/human-04.jpg",
		"conflicting-used": "/agent-avatars/human-05.jpg",
	} {
		var id string
		if err := conn.QueryRow(ctx, `INSERT INTO agent (avatar_url) VALUES ($1) RETURNING id::text`, avatarURL).Scan(&id); err != nil {
			t.Fatalf("insert %s agent: %v", name, err)
		}
		ids[name] = id
	}
	for _, draft := range []struct {
		agentID   string
		avatarURL *string
		status    string
	}{
		{agentID: ids["automatic"], avatarURL: nil, status: "used"},
		{agentID: ids["explicit-draft"], avatarURL: stringPtr("/agent-avatars/human-03.jpg"), status: "used"},
		{agentID: ids["windy"], avatarURL: nil, status: "used"},
		{agentID: ids["custom"], avatarURL: nil, status: "used"},
		{agentID: ids["unused-draft"], avatarURL: nil, status: "draft"},
		// Although one used draft is NULL, a second used draft has an explicit
		// value. Historic provenance is ambiguous, so the explicit choice wins.
		{agentID: ids["conflicting-used"], avatarURL: nil, status: "used"},
		{agentID: ids["conflicting-used"], avatarURL: stringPtr("/agent-avatars/human-05.jpg"), status: "used"},
	} {
		if _, err := conn.Exec(ctx, `
			INSERT INTO agent_creation_draft (used_agent_id, avatar_url, status)
			VALUES ($1::uuid, $2, $3)
		`, draft.agentID, draft.avatarURL, draft.status); err != nil {
			t.Fatalf("insert draft for %s: %v", draft.agentID, err)
		}
	}

	_, testFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate test file")
	}
	migrationsDir := filepath.Join(filepath.Dir(testFile), "..", "..", "migrations")
	upSQL, err := os.ReadFile(filepath.Join(migrationsDir, "198_clear_legacy_draft_random_agent_avatars.up.sql"))
	if err != nil {
		t.Fatalf("read up migration: %v", err)
	}
	if _, err := conn.Exec(ctx, string(upSQL)); err != nil {
		t.Fatalf("apply up migration: %v", err)
	}

	for name, want := range map[string]*string{
		"automatic":        nil,
		"unlinked":         stringPtr("/agent-avatars/human-02.jpg"),
		"explicit-draft":   stringPtr("/agent-avatars/human-03.jpg"),
		"windy":            stringPtr("/agent-avatars/human-11.jpg"),
		"custom":           stringPtr("https://cdn.example.com/avatar.png"),
		"unused-draft":     stringPtr("/agent-avatars/human-04.jpg"),
		"conflicting-used": stringPtr("/agent-avatars/human-05.jpg"),
	} {
		var got *string
		if err := conn.QueryRow(ctx, `SELECT avatar_url FROM agent WHERE id = $1::uuid`, ids[name]).Scan(&got); err != nil {
			t.Fatalf("read %s agent: %v", name, err)
		}
		if !equalStringPtr(got, want) {
			t.Errorf("%s avatar_url = %v, want %v", name, got, want)
		}
	}
}

func stringPtr(value string) *string {
	return &value
}

func equalStringPtr(left, right *string) bool {
	if left == nil || right == nil {
		return left == right
	}
	return *left == *right
}
