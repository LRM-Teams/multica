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

func TestChannelVoiceTranscriptionMIMERepairMigration216(t *testing.T) {
	pool := openTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire connection: %v", err)
	}
	defer conn.Release()

	schema := fmt.Sprintf("channel_voice_mime_repair_test_%d", time.Now().UnixNano())
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
		CREATE TABLE attachment (
		  id UUID PRIMARY KEY,
		  filename TEXT NOT NULL,
		  content_type TEXT NOT NULL
		);
		CREATE TABLE channel_message (
		  id UUID PRIMARY KEY,
		  parts JSONB NOT NULL
		);
		CREATE TABLE channel_voice_transcription (
		  message_id UUID PRIMARY KEY REFERENCES channel_message(id) ON DELETE CASCADE,
		  attachment_id UUID NOT NULL REFERENCES attachment(id) ON DELETE CASCADE,
		  status TEXT NOT NULL,
		  attempts INTEGER NOT NULL,
		  next_attempt_at TIMESTAMPTZ NOT NULL,
		  claimed_at TIMESTAMPTZ,
		  last_error_code TEXT NOT NULL,
		  updated_at TIMESTAMPTZ NOT NULL
		);

		INSERT INTO attachment (id, filename, content_type) VALUES
		  ('10000000-0000-0000-0000-000000000001', 'voice.wav', 'audio/wave'),
		  ('10000000-0000-0000-0000-000000000002', 'provider.wav', 'audio/wave'),
		  ('10000000-0000-0000-0000-000000000003', 'fake.mp3', 'audio/mpeg');
		INSERT INTO channel_message (id, parts) VALUES
		  (
		    '20000000-0000-0000-0000-000000000001',
		    '[{"type":"voice","attachment_id":"10000000-0000-0000-0000-000000000001","transcription_status":"failed"}]'
		  ),
		  (
		    '20000000-0000-0000-0000-000000000002',
		    '[{"type":"voice","attachment_id":"10000000-0000-0000-0000-000000000002","transcription_status":"failed"}]'
		  ),
		  (
		    '20000000-0000-0000-0000-000000000003',
		    '[{"type":"voice","attachment_id":"10000000-0000-0000-0000-000000000003","transcription_status":"failed"}]'
		  );
		INSERT INTO channel_voice_transcription (
		  message_id, attachment_id, status, attempts, next_attempt_at,
		  claimed_at, last_error_code, updated_at
		) VALUES
		  (
		    '20000000-0000-0000-0000-000000000001',
		    '10000000-0000-0000-0000-000000000001',
		    'failed', 1, now() + interval '1 day', now(), 'invalid_recording', now()
		  ),
		  (
		    '20000000-0000-0000-0000-000000000002',
		    '10000000-0000-0000-0000-000000000002',
		    'failed', 3, now() + interval '1 day', now(), 'provider_failed', now()
		  ),
		  (
		    '20000000-0000-0000-0000-000000000003',
		    '10000000-0000-0000-0000-000000000003',
		    'failed', 1, now() + interval '1 day', now(), 'invalid_recording', now()
		  );
	`); err != nil {
		t.Fatalf("create pre-216 fixture: %v", err)
	}

	upSQL, downSQL := readChannelVoiceTranscriptionMIMERepairMigrationSQL(t)
	if _, err := conn.Exec(ctx, upSQL); err != nil {
		t.Fatalf("apply migration 216 up: %v", err)
	}

	var contentType, status, partStatus, lastError string
	var attempts int
	var claimed bool
	if err := conn.QueryRow(ctx, `
		SELECT attachment.content_type, transcription.status, transcription.attempts,
		       transcription.claimed_at IS NOT NULL, transcription.last_error_code,
		       message.parts->0->>'transcription_status'
		FROM channel_voice_transcription transcription
		JOIN attachment ON attachment.id = transcription.attachment_id
		JOIN channel_message message ON message.id = transcription.message_id
		WHERE transcription.message_id = '20000000-0000-0000-0000-000000000001'
	`).Scan(&contentType, &status, &attempts, &claimed, &lastError, &partStatus); err != nil {
		t.Fatalf("load repaired transcription: %v", err)
	}
	if contentType != "audio/wav" || status != "pending" || attempts != 0 || claimed ||
		lastError != "" || partStatus != "pending" {
		t.Fatalf(
			"repaired state = content_type:%q status:%q attempts:%d claimed:%v error:%q part:%q",
			contentType, status, attempts, claimed, lastError, partStatus,
		)
	}

	for _, messageID := range []string{
		"20000000-0000-0000-0000-000000000002",
		"20000000-0000-0000-0000-000000000003",
	} {
		var untouchedStatus string
		if err := conn.QueryRow(ctx, `
			SELECT status FROM channel_voice_transcription WHERE message_id = $1
		`, messageID).Scan(&untouchedStatus); err != nil {
			t.Fatalf("load untouched transcription %s: %v", messageID, err)
		}
		if untouchedStatus != "failed" {
			t.Fatalf("unrelated transcription %s status = %q, want failed", messageID, untouchedStatus)
		}
	}

	if _, err := conn.Exec(ctx, downSQL); err != nil {
		t.Fatalf("apply migration 216 down: %v", err)
	}
}

func readChannelVoiceTranscriptionMIMERepairMigrationSQL(t *testing.T) (string, string) {
	t.Helper()
	_, testFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate migration test file")
	}
	migrationsDir := filepath.Join(filepath.Dir(testFile), "..", "..", "migrations")
	read := func(name string) string {
		body, err := os.ReadFile(filepath.Join(migrationsDir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		return string(body)
	}
	return read("216_channel_voice_transcription_mime_repair.up.sql"),
		read("216_channel_voice_transcription_mime_repair.down.sql")
}
