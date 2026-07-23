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
)

func TestVoiceCallSessionMigration215EnforcesOneNonTerminalCallPerPair(t *testing.T) {
	conn, ctx := setupVoiceCallMigrationTest(t)

	const insertCall = `
		INSERT INTO voice_call_session (
		  workspace_id, channel_id, agent_id, user_id, provider, status
		)
		VALUES (
		  '10000000-0000-0000-0000-000000000001',
		  '40000000-0000-0000-0000-000000000001',
		  '30000000-0000-0000-0000-000000000001',
		  '20000000-0000-0000-0000-000000000001',
		  'volcengine',
		  $1
		)
	`
	if _, err := conn.Exec(ctx, insertCall, "starting"); err != nil {
		t.Fatalf("insert first active call: %v", err)
	}
	_, err := conn.Exec(ctx, insertCall, "connecting")
	assertPgCode(t, err, "23505", "insert second non-terminal call for pair")

	if _, err := conn.Exec(ctx, `
		UPDATE voice_call_session
		SET status = 'failed', ended_at = now(), end_reason = 'provider_error'
	`); err != nil {
		t.Fatalf("finish first call: %v", err)
	}
	if _, err := conn.Exec(ctx, insertCall, "starting"); err != nil {
		t.Fatalf("start call after terminal session: %v", err)
	}
}

func TestVoiceCallSessionMigration215EnforcesStatusTransitions(t *testing.T) {
	conn, ctx := setupVoiceCallMigrationTest(t)

	callID := insertVoiceCallSession(t, conn, ctx)

	for _, transition := range []struct {
		status   string
		extraSQL string
	}{
		{status: "connecting"},
		{status: "active", extraSQL: ", connected_at = now()"},
		{status: "reconnecting"},
		{status: "active"},
		{status: "ending"},
		{status: "ended", extraSQL: ", ended_at = now(), end_reason = 'user_hangup'"},
	} {
		query := "UPDATE voice_call_session SET status = $2" + transition.extraSQL + " WHERE id = $1"
		if _, err := conn.Exec(ctx, query, callID, transition.status); err != nil {
			t.Fatalf("transition call to %s: %v", transition.status, err)
		}
	}

	_, err := conn.Exec(ctx, `
		UPDATE voice_call_session
		SET status = 'active', ended_at = NULL
		WHERE id = $1
	`, callID)
	assertPgCode(t, err, "23514", "restart terminal call")
}

func TestVoiceCallSessionMigration215DeduplicatesDurableTurns(t *testing.T) {
	conn, ctx := setupVoiceCallMigrationTest(t)
	callID := insertVoiceCallSession(t, conn, ctx)

	const insertTurn = `
		INSERT INTO voice_call_turn (
		  call_session_id, sequence, speaker, transcript, provider_turn_id,
		  started_at, ended_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`
	started := time.Date(2026, 7, 23, 8, 0, 0, 0, time.UTC)
	ended := started.Add(2 * time.Second)
	if _, err := conn.Exec(ctx, insertTurn, callID, 1, "member", "请汇报当前进度", "member-turn-1", started, ended); err != nil {
		t.Fatalf("insert first final turn: %v", err)
	}

	_, err := conn.Exec(ctx, insertTurn, callID, 2, "member", "duplicate callback", "member-turn-1", started, ended)
	assertPgCode(t, err, "23505", "insert duplicate provider turn")

	_, err = conn.Exec(ctx, insertTurn, callID, 1, "agent", "duplicate sequence", "agent-turn-1", started, ended)
	assertPgCode(t, err, "23505", "insert duplicate turn sequence")

	if _, err := conn.Exec(ctx, insertTurn, callID, 2, "agent", "现在汇报", "agent-turn-1", started, ended); err != nil {
		t.Fatalf("insert next final turn: %v", err)
	}
}

func TestVoiceCallSessionMigration215RejectsInconsistentSessionFacts(t *testing.T) {
	conn, ctx := setupVoiceCallMigrationTest(t)

	const insertCall = `
		INSERT INTO voice_call_session (
		  workspace_id, channel_id, agent_id, user_id, provider, status,
		  started_at, connected_at, ended_at, input_audio_ms, output_audio_ms
		)
		VALUES (
		  '10000000-0000-0000-0000-000000000001',
		  '40000000-0000-0000-0000-000000000001',
		  '30000000-0000-0000-0000-000000000001',
		  '20000000-0000-0000-0000-000000000001',
		  $1, $2, $3, $4, $5, $6, $7
		)
	`
	started := time.Date(2026, 7, 23, 8, 0, 0, 0, time.UTC)

	cases := []struct {
		name          string
		provider      string
		status        string
		startedAt     time.Time
		connectedAt   *time.Time
		endedAt       *time.Time
		inputAudioMS  int64
		outputAudioMS int64
	}{
		{
			name: "blank provider", provider: " ", status: "starting",
			startedAt: started,
		},
		{
			name: "unknown status", provider: "volcengine", status: "ringing",
			startedAt: started,
		},
		{
			name: "terminal status without ended time", provider: "volcengine", status: "failed",
			startedAt: started,
		},
		{
			name: "non-terminal status with ended time", provider: "volcengine", status: "active",
			startedAt: started, connectedAt: voiceCallTimePointer(started.Add(time.Second)),
			endedAt: voiceCallTimePointer(started.Add(2 * time.Second)),
		},
		{
			name: "connection before start", provider: "volcengine", status: "connecting",
			startedAt: started, connectedAt: voiceCallTimePointer(started.Add(-time.Second)),
		},
		{
			name: "negative audio duration", provider: "volcengine", status: "starting",
			startedAt: started, inputAudioMS: -1,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := conn.Exec(
				ctx,
				insertCall,
				testCase.provider,
				testCase.status,
				testCase.startedAt,
				testCase.connectedAt,
				testCase.endedAt,
				testCase.inputAudioMS,
				testCase.outputAudioMS,
			)
			if err == nil {
				_, _ = conn.Exec(ctx, "DELETE FROM voice_call_session")
			}
			assertPgCode(t, err, "23514", testCase.name)
		})
	}
}

func TestVoiceCallSessionMigration215RejectsInconsistentTurnFacts(t *testing.T) {
	conn, ctx := setupVoiceCallMigrationTest(t)
	callID := insertVoiceCallSession(t, conn, ctx)
	started := time.Date(2026, 7, 23, 8, 0, 0, 0, time.UTC)
	ended := started.Add(2 * time.Second)

	const insertTurn = `
		INSERT INTO voice_call_turn (
		  call_session_id, sequence, speaker, transcript, started_at, ended_at,
		  spoken_duration_ms
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`
	cases := []struct {
		name             string
		sequence         int64
		speaker          string
		transcript       string
		startedAt        *time.Time
		endedAt          *time.Time
		spokenDurationMS int64
		wantCode         string
	}{
		{
			name: "zero sequence", sequence: 0, speaker: "member", transcript: "hello",
			startedAt: &started, endedAt: &ended,
		},
		{
			name: "unknown speaker", sequence: 1, speaker: "system", transcript: "hello",
			startedAt: &started, endedAt: &ended,
		},
		{
			name: "blank final transcript", sequence: 2, speaker: "member", transcript: " ",
			startedAt: &started, endedAt: &ended,
		},
		{
			name: "missing start time", sequence: 3, speaker: "agent", transcript: "hello",
			endedAt: &ended, wantCode: "23502",
		},
		{
			name: "turn ends before start", sequence: 4, speaker: "agent", transcript: "hello",
			startedAt: &ended, endedAt: &started,
		},
		{
			name: "negative spoken duration", sequence: 5, speaker: "agent", transcript: "hello",
			startedAt: &started, endedAt: &ended, spokenDurationMS: -1,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := conn.Exec(
				ctx,
				insertTurn,
				callID,
				testCase.sequence,
				testCase.speaker,
				testCase.transcript,
				testCase.startedAt,
				testCase.endedAt,
				testCase.spokenDurationMS,
			)
			if err == nil {
				_, _ = conn.Exec(ctx, "DELETE FROM voice_call_turn")
			}
			wantCode := testCase.wantCode
			if wantCode == "" {
				wantCode = "23514"
			}
			assertPgCode(t, err, wantCode, testCase.name)
		})
	}
}

func TestVoiceCallSessionMigration215DeduplicatesProviderSessionIdentity(t *testing.T) {
	conn, ctx := setupVoiceCallMigrationTest(t)

	const insertTerminalCall = `
		INSERT INTO voice_call_session (
		  workspace_id, channel_id, agent_id, user_id, provider,
		  provider_task_id, room_id, status, ended_at
		)
		VALUES (
		  '10000000-0000-0000-0000-000000000001',
		  '40000000-0000-0000-0000-000000000001',
		  '30000000-0000-0000-0000-000000000001',
		  '20000000-0000-0000-0000-000000000001',
		  'volcengine', $1, $2, 'failed', now()
		)
	`
	if _, err := conn.Exec(ctx, insertTerminalCall, "task-1", "room-1"); err != nil {
		t.Fatalf("insert first provider session: %v", err)
	}

	_, err := conn.Exec(ctx, insertTerminalCall, "task-1", "room-2")
	assertPgCode(t, err, "23505", "reuse provider task")

	_, err = conn.Exec(ctx, insertTerminalCall, "task-2", "room-1")
	assertPgCode(t, err, "23505", "reuse provider room")
}

func TestVoiceCallSessionMigration215DownRemovesCallState(t *testing.T) {
	conn, ctx := setupVoiceCallMigrationTest(t)
	_, downSQL := readVoiceCallSessionMigrationSQL(t)

	if _, err := conn.Exec(ctx, downSQL); err != nil {
		t.Fatalf("apply migration 215 down: %v", err)
	}

	var sessionExists, turnExists, transitionFunctionExists bool
	if err := conn.QueryRow(ctx, `
		SELECT
		  to_regclass(current_schema() || '.voice_call_session') IS NOT NULL,
		  to_regclass(current_schema() || '.voice_call_turn') IS NOT NULL,
		  to_regprocedure(
		    current_schema() || '.enforce_voice_call_session_status_transition()'
		  ) IS NOT NULL
	`).Scan(&sessionExists, &turnExists, &transitionFunctionExists); err != nil {
		t.Fatalf("inspect schema after down: %v", err)
	}
	if sessionExists || turnExists || transitionFunctionExists {
		t.Fatalf(
			"down left session=%t turn=%t transition_function=%t",
			sessionExists,
			turnExists,
			transitionFunctionExists,
		)
	}
}

func voiceCallTimePointer(value time.Time) *time.Time {
	return &value
}

func insertVoiceCallSession(t *testing.T, conn *pgxpool.Conn, ctx context.Context) string {
	t.Helper()
	var callID string
	if err := conn.QueryRow(ctx, `
		INSERT INTO voice_call_session (
		  workspace_id, channel_id, agent_id, user_id, provider
		)
		VALUES (
		  '10000000-0000-0000-0000-000000000001',
		  '40000000-0000-0000-0000-000000000001',
		  '30000000-0000-0000-0000-000000000001',
		  '20000000-0000-0000-0000-000000000001',
		  'volcengine'
		)
		RETURNING id
	`).Scan(&callID); err != nil {
		t.Fatalf("create call: %v", err)
	}
	return callID
}

func setupVoiceCallMigrationTest(t *testing.T) (*pgxpool.Conn, context.Context) {
	t.Helper()
	pool := openTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)

	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire connection: %v", err)
	}
	t.Cleanup(conn.Release)

	schema := fmt.Sprintf("voice_call_session_migration_test_%d", time.Now().UnixNano())
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
		CREATE TABLE workspace (id UUID PRIMARY KEY);
		CREATE TABLE "user" (id UUID PRIMARY KEY);
		CREATE TABLE agent (id UUID PRIMARY KEY);
		CREATE TABLE channel (id UUID PRIMARY KEY);

		INSERT INTO workspace VALUES ('10000000-0000-0000-0000-000000000001');
		INSERT INTO "user" VALUES ('20000000-0000-0000-0000-000000000001');
		INSERT INTO agent VALUES ('30000000-0000-0000-0000-000000000001');
		INSERT INTO channel VALUES ('40000000-0000-0000-0000-000000000001');
	`); err != nil {
		t.Fatalf("create pre-215 schema: %v", err)
	}

	upSQL, _ := readVoiceCallSessionMigrationSQL(t)
	if _, err := conn.Exec(ctx, upSQL); err != nil {
		t.Fatalf("apply migration 215 up: %v", err)
	}
	return conn, ctx
}

func readVoiceCallSessionMigrationSQL(t *testing.T) (string, string) {
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
	return read("215_voice_call_session.up.sql"), read("215_voice_call_session.down.sql")
}
