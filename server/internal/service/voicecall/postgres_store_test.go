package voicecall

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestPostgresStoreMapsSessionAndScopesEveryMutation(t *testing.T) {
	queries := &fakeVoiceCallQueries{session: testDBVoiceCallSession()}
	store, err := NewPostgresStore(queries)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	created, err := store.CreateStarting(context.Background(), NewSession{
		WorkspaceID:    testVoiceWorkspaceID,
		ChannelID:      testVoiceChannelID,
		AgentID:        testVoiceAgentID,
		UserID:         testVoiceUserID,
		Provider:       "volcengine",
		ProviderTaskID: "voice-task-1",
		RoomID:         "voice-call-1",
	})
	if err != nil {
		t.Fatalf("create starting: %v", err)
	}
	if created.ID != testVoiceCallID ||
		created.ProviderTaskID != "voice-task-1" ||
		created.RoomID != "voice-call-1" ||
		created.Status != StatusStarting ||
		created.ConnectedAt != nil ||
		created.EndedAt != nil {
		t.Fatalf("created session = %+v", created)
	}
	if got := uuidStringForTest(queries.create.WorkspaceID); got != testVoiceWorkspaceID {
		t.Fatalf("create workspace = %q", got)
	}

	if _, err := store.Get(
		context.Background(), testVoiceWorkspaceID, testVoiceUserID, testVoiceCallID,
	); err != nil {
		t.Fatalf("get: %v", err)
	}
	if uuidStringForTest(queries.get.WorkspaceID) != testVoiceWorkspaceID ||
		uuidStringForTest(queries.get.UserID) != testVoiceUserID ||
		uuidStringForTest(queries.get.ID) != testVoiceCallID {
		t.Fatalf("get params = %+v", queries.get)
	}

	if _, err := store.MarkConnecting(
		context.Background(), testVoiceWorkspaceID, testVoiceCallID,
	); err != nil {
		t.Fatalf("mark connecting: %v", err)
	}
	if uuidStringForTest(queries.connecting.WorkspaceID) != testVoiceWorkspaceID ||
		uuidStringForTest(queries.connecting.ID) != testVoiceCallID {
		t.Fatalf("connecting params = %+v", queries.connecting)
	}

	if _, err := store.MarkFailed(
		context.Background(), testVoiceWorkspaceID, testVoiceCallID, "provider_start_failed",
	); err != nil {
		t.Fatalf("mark failed: %v", err)
	}
	if queries.failed.ErrorCode != "provider_start_failed" ||
		uuidStringForTest(queries.failed.WorkspaceID) != testVoiceWorkspaceID ||
		uuidStringForTest(queries.failed.ID) != testVoiceCallID {
		t.Fatalf("failed params = %+v", queries.failed)
	}

	queries.ending = testDBBeginEndingRow(true)
	ending, err := store.BeginEnding(
		context.Background(),
		testVoiceWorkspaceID,
		testVoiceUserID,
		testVoiceCallID,
		"user_hangup",
	)
	if err != nil {
		t.Fatalf("begin ending: %v", err)
	}
	if !ending.ProviderStopRequired || ending.Session.ID != testVoiceCallID {
		t.Fatalf("ending = %+v", ending)
	}
	if queries.beginEnding.EndReason != "user_hangup" ||
		uuidStringForTest(queries.beginEnding.WorkspaceID) != testVoiceWorkspaceID ||
		uuidStringForTest(queries.beginEnding.UserID) != testVoiceUserID ||
		uuidStringForTest(queries.beginEnding.ID) != testVoiceCallID {
		t.Fatalf("begin ending params = %+v", queries.beginEnding)
	}

	if _, err := store.MarkEnded(
		context.Background(), testVoiceWorkspaceID, testVoiceCallID, "user_hangup",
	); err != nil {
		t.Fatalf("mark ended: %v", err)
	}
	if queries.ended.EndReason != "user_hangup" ||
		uuidStringForTest(queries.ended.WorkspaceID) != testVoiceWorkspaceID ||
		uuidStringForTest(queries.ended.ID) != testVoiceCallID {
		t.Fatalf("ended params = %+v", queries.ended)
	}
}

func TestPostgresStoreRejectsInvalidUUIDBeforeQuery(t *testing.T) {
	queries := &fakeVoiceCallQueries{session: testDBVoiceCallSession()}
	store, err := NewPostgresStore(queries)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	_, err = store.Get(context.Background(), "not-a-uuid", testVoiceUserID, testVoiceCallID)
	if err == nil || !strings.Contains(err.Error(), "workspace_id") {
		t.Fatalf("error = %v, want workspace UUID error", err)
	}
	if queries.getCalls != 0 {
		t.Fatalf("invalid UUID reached query %d times", queries.getCalls)
	}
}

func TestPostgresStoreClassifiesMemberVisibleErrors(t *testing.T) {
	t.Run("active pair conflict", func(t *testing.T) {
		queries := &fakeVoiceCallQueries{
			session: testDBVoiceCallSession(),
			createErr: &pgconn.PgError{
				Code:           "23505",
				ConstraintName: "voice_call_session_active_pair_idx",
			},
		}
		store, err := NewPostgresStore(queries)
		if err != nil {
			t.Fatalf("new store: %v", err)
		}
		_, err = store.CreateStarting(context.Background(), NewSession{
			WorkspaceID:    testVoiceWorkspaceID,
			ChannelID:      testVoiceChannelID,
			AgentID:        testVoiceAgentID,
			UserID:         testVoiceUserID,
			Provider:       "volcengine",
			ProviderTaskID: "voice-task-conflict",
			RoomID:         "voice-room-conflict",
		})
		if !errors.Is(err, ErrCallAlreadyActive) {
			t.Fatalf("error = %v, want ErrCallAlreadyActive", err)
		}
	})

	t.Run("scoped session not found", func(t *testing.T) {
		queries := &fakeVoiceCallQueries{
			session:        testDBVoiceCallSession(),
			getErr:         pgx.ErrNoRows,
			beginEndingErr: pgx.ErrNoRows,
		}
		store, err := NewPostgresStore(queries)
		if err != nil {
			t.Fatalf("new store: %v", err)
		}
		if _, err := store.Get(
			context.Background(), testVoiceWorkspaceID, testVoiceUserID, testVoiceCallID,
		); !errors.Is(err, ErrCallNotFound) {
			t.Fatalf("get error = %v, want ErrCallNotFound", err)
		}
		if _, err := store.BeginEnding(
			context.Background(),
			testVoiceWorkspaceID,
			testVoiceUserID,
			testVoiceCallID,
			"user_hangup",
		); !errors.Is(err, ErrCallNotFound) {
			t.Fatalf("ending error = %v, want ErrCallNotFound", err)
		}
	})
}

func TestPostgresStoreStateQueriesAgainstMigration(t *testing.T) {
	databaseURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if databaseURL == "" {
		t.Skip("integration test requires DATABASE_URL")
	}
	ctx := context.Background()
	connection, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect database: %v", err)
	}
	defer connection.Close(ctx)

	schema := "voice_call_store_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	identifier := pgx.Identifier{schema}.Sanitize()
	if _, err := connection.Exec(ctx, "CREATE SCHEMA "+identifier); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = connection.Exec(context.Background(), "SET search_path TO public")
		_, _ = connection.Exec(context.Background(), "DROP SCHEMA "+identifier+" CASCADE")
	})
	if _, err := connection.Exec(ctx, "SET search_path TO "+identifier+", public"); err != nil {
		t.Fatalf("set search path: %v", err)
	}
	if _, err := connection.Exec(ctx, `
		CREATE TABLE workspace (id UUID PRIMARY KEY);
		CREATE TABLE channel (id UUID PRIMARY KEY);
		CREATE TABLE agent (id UUID PRIMARY KEY);
		CREATE TABLE "user" (id UUID PRIMARY KEY);
	`); err != nil {
		t.Fatalf("create prerequisite tables: %v", err)
	}
	migration, err := os.ReadFile(filepath.Join("..", "..", "..", "migrations", "215_voice_call_session.up.sql"))
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	if _, err := connection.Exec(ctx, string(migration)); err != nil {
		t.Fatalf("apply voice call migration: %v", err)
	}
	for table, id := range map[string]string{
		"workspace": testVoiceWorkspaceID,
		"channel":   testVoiceChannelID,
		"agent":     testVoiceAgentID,
		`"user"`:    testVoiceUserID,
	} {
		if _, err := connection.Exec(ctx, "INSERT INTO "+table+" (id) VALUES ($1)", id); err != nil {
			t.Fatalf("insert %s: %v", table, err)
		}
	}

	store, err := NewPostgresStore(db.New(connection))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	session, err := store.CreateStarting(ctx, NewSession{
		WorkspaceID:    testVoiceWorkspaceID,
		ChannelID:      testVoiceChannelID,
		AgentID:        testVoiceAgentID,
		UserID:         testVoiceUserID,
		Provider:       "volcengine",
		ProviderTaskID: "voice-task-integration-1",
		RoomID:         "voice-call-integration-1",
	})
	if err != nil {
		t.Fatalf("create starting: %v", err)
	}
	if _, err := store.CreateStarting(ctx, NewSession{
		WorkspaceID:    testVoiceWorkspaceID,
		ChannelID:      testVoiceChannelID,
		AgentID:        testVoiceAgentID,
		UserID:         testVoiceUserID,
		Provider:       "volcengine",
		ProviderTaskID: "voice-task-integration-duplicate",
		RoomID:         "voice-call-integration-duplicate",
	}); !errors.Is(err, ErrCallAlreadyActive) {
		t.Fatalf("duplicate active member/agent pair error = %v, want ErrCallAlreadyActive", err)
	}
	if _, err := store.MarkConnecting(ctx, testVoiceWorkspaceID, session.ID); err != nil {
		t.Fatalf("mark connecting: %v", err)
	}
	firstEnding, err := store.BeginEnding(
		ctx, testVoiceWorkspaceID, testVoiceUserID, session.ID, "user_hangup",
	)
	if err != nil || !firstEnding.ProviderStopRequired {
		t.Fatalf("first ending = %+v error=%v", firstEnding, err)
	}
	secondEnding, err := store.BeginEnding(
		ctx, testVoiceWorkspaceID, testVoiceUserID, session.ID, "duplicate_hangup",
	)
	if err != nil || !secondEnding.ProviderStopRequired ||
		secondEnding.Session.EndReason != "user_hangup" {
		t.Fatalf("second ending = %+v error=%v", secondEnding, err)
	}
	ended, err := store.MarkEnded(
		ctx, testVoiceWorkspaceID, session.ID, firstEnding.Session.EndReason,
	)
	if err != nil || ended.Status != StatusEnded {
		t.Fatalf("ended = %+v error=%v", ended, err)
	}
	terminal, err := store.BeginEnding(
		ctx, testVoiceWorkspaceID, testVoiceUserID, session.ID, "duplicate_hangup",
	)
	if err != nil || terminal.ProviderStopRequired || terminal.Session.Status != StatusEnded {
		t.Fatalf("terminal ending = %+v error=%v", terminal, err)
	}
}

const (
	testVoiceCallID      = "10000000-0000-4000-8000-000000000001"
	testVoiceWorkspaceID = "10000000-0000-4000-8000-000000000002"
	testVoiceChannelID   = "10000000-0000-4000-8000-000000000003"
	testVoiceAgentID     = "10000000-0000-4000-8000-000000000004"
	testVoiceUserID      = "10000000-0000-4000-8000-000000000005"
)

type fakeVoiceCallQueries struct {
	session        db.VoiceCallSession
	ending         db.BeginVoiceCallEndingRow
	create         db.CreateVoiceCallSessionParams
	get            db.GetVoiceCallSessionForMemberParams
	connecting     db.MarkVoiceCallConnectingParams
	failed         db.MarkVoiceCallFailedParams
	beginEnding    db.BeginVoiceCallEndingParams
	ended          db.MarkVoiceCallEndedParams
	getCalls       int
	createErr      error
	getErr         error
	beginEndingErr error
}

func (queries *fakeVoiceCallQueries) CreateVoiceCallSession(
	_ context.Context,
	params db.CreateVoiceCallSessionParams,
) (db.VoiceCallSession, error) {
	queries.create = params
	if queries.createErr != nil {
		return db.VoiceCallSession{}, queries.createErr
	}
	return queries.session, nil
}

func (queries *fakeVoiceCallQueries) GetVoiceCallSessionForMember(
	_ context.Context,
	params db.GetVoiceCallSessionForMemberParams,
) (db.VoiceCallSession, error) {
	queries.get = params
	queries.getCalls++
	if queries.getErr != nil {
		return db.VoiceCallSession{}, queries.getErr
	}
	return queries.session, nil
}

func (queries *fakeVoiceCallQueries) MarkVoiceCallConnecting(
	_ context.Context,
	params db.MarkVoiceCallConnectingParams,
) (db.VoiceCallSession, error) {
	queries.connecting = params
	return queries.session, nil
}

func (queries *fakeVoiceCallQueries) MarkVoiceCallFailed(
	_ context.Context,
	params db.MarkVoiceCallFailedParams,
) (db.VoiceCallSession, error) {
	queries.failed = params
	return queries.session, nil
}

func (queries *fakeVoiceCallQueries) BeginVoiceCallEnding(
	_ context.Context,
	params db.BeginVoiceCallEndingParams,
) (db.BeginVoiceCallEndingRow, error) {
	queries.beginEnding = params
	if queries.beginEndingErr != nil {
		return db.BeginVoiceCallEndingRow{}, queries.beginEndingErr
	}
	return queries.ending, nil
}

func (queries *fakeVoiceCallQueries) MarkVoiceCallEnded(
	_ context.Context,
	params db.MarkVoiceCallEndedParams,
) (db.VoiceCallSession, error) {
	queries.ended = params
	return queries.session, nil
}

func testDBVoiceCallSession() db.VoiceCallSession {
	startedAt := time.Date(2026, time.July, 23, 10, 0, 0, 0, time.UTC)
	return db.VoiceCallSession{
		ID:             testPGUUID(testVoiceCallID),
		WorkspaceID:    testPGUUID(testVoiceWorkspaceID),
		ChannelID:      testPGUUID(testVoiceChannelID),
		AgentID:        testPGUUID(testVoiceAgentID),
		UserID:         testPGUUID(testVoiceUserID),
		Provider:       "volcengine",
		ProviderTaskID: pgtype.Text{String: "voice-task-1", Valid: true},
		RoomID:         pgtype.Text{String: "voice-call-1", Valid: true},
		Status:         string(StatusStarting),
		StartedAt:      pgtype.Timestamptz{Time: startedAt, Valid: true},
		InputAudioMs:   12,
		OutputAudioMs:  34,
		CreatedAt:      pgtype.Timestamptz{Time: startedAt, Valid: true},
		UpdatedAt:      pgtype.Timestamptz{Time: startedAt, Valid: true},
	}
}

func testDBBeginEndingRow(providerStopRequired bool) db.BeginVoiceCallEndingRow {
	session := testDBVoiceCallSession()
	return db.BeginVoiceCallEndingRow{
		ID:                   session.ID,
		WorkspaceID:          session.WorkspaceID,
		ChannelID:            session.ChannelID,
		AgentID:              session.AgentID,
		UserID:               session.UserID,
		Provider:             session.Provider,
		ProviderTaskID:       session.ProviderTaskID,
		RoomID:               session.RoomID,
		Status:               string(StatusEnding),
		StartedAt:            session.StartedAt,
		EndReason:            "user_hangup",
		InputAudioMs:         session.InputAudioMs,
		OutputAudioMs:        session.OutputAudioMs,
		CreatedAt:            session.CreatedAt,
		UpdatedAt:            session.UpdatedAt,
		ProviderStopRequired: providerStopRequired,
	}
}

func testPGUUID(value string) pgtype.UUID {
	return pgtype.UUID{Bytes: uuid.MustParse(value), Valid: true}
}

func uuidStringForTest(value pgtype.UUID) string {
	return uuid.UUID(value.Bytes).String()
}
