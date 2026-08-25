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
	queries := &fakeVoiceCallQueries{
		session: testDBVoiceCallSession(),
		turn:    testDBVoiceCallTurn(),
	}
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

	turn, err := store.UpsertProviderTurn(context.Background(), ProviderTurnInput{
		Provider:       "volcengine",
		ProviderTaskID: "voice-task-1",
		Sequence:       1,
		Speaker:        SpeakerMember,
		Transcript:     " 你好。 ",
		IsInterrupted:  false,
		ProviderTurnID: "voice-member-1:3",
	})
	if err != nil {
		t.Fatalf("upsert provider turn: %v", err)
	}
	if turn.ID != testVoiceTurnID ||
		turn.CallSessionID != testVoiceCallID ||
		turn.Transcript != " 你好。 " {
		t.Fatalf("turn = %+v", turn)
	}
	if queries.providerTurn.Provider != "volcengine" ||
		queries.providerTurn.ProviderTaskID.String != "voice-task-1" ||
		queries.providerTurn.Sequence != 1 ||
		queries.providerTurn.Speaker != "member" ||
		queries.providerTurn.Transcript != " 你好。 " ||
		queries.providerTurn.ProviderTurnID.String != "voice-member-1:3" {
		t.Fatalf("provider turn params = %+v", queries.providerTurn)
	}

	providerStart, err := store.BeginProviderStart(
		context.Background(), testVoiceWorkspaceID, testVoiceCallID,
	)
	if err != nil || !providerStart.ProviderStartRequired {
		t.Fatalf("begin provider start: %+v error=%v", providerStart, err)
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

	if _, err := store.ApplyProviderActive(
		context.Background(), "volcengine", "voice-task-1",
	); err != nil {
		t.Fatalf("apply provider active: %v", err)
	}
	if queries.providerActive.Provider != "volcengine" ||
		queries.providerActive.ProviderTaskID.String != "voice-task-1" {
		t.Fatalf("provider active params = %+v", queries.providerActive)
	}

	if _, err := store.ApplyClientAnswered(
		context.Background(),
		testVoiceWorkspaceID,
		testVoiceUserID,
		testVoiceCallID,
	); err != nil {
		t.Fatalf("apply client answered: %v", err)
	}
	if uuidStringForTest(queries.clientAnswered.WorkspaceID) != testVoiceWorkspaceID ||
		uuidStringForTest(queries.clientAnswered.UserID) != testVoiceUserID ||
		uuidStringForTest(queries.clientAnswered.ID) != testVoiceCallID {
		t.Fatalf("client answered params = %+v", queries.clientAnswered)
	}

	if _, err := store.ApplyProviderFailure(
		context.Background(), "volcengine", "voice-task-1", "volcengine_1005002",
	); err != nil {
		t.Fatalf("apply provider failure: %v", err)
	}
	if queries.providerFailure.Provider != "volcengine" ||
		queries.providerFailure.ProviderTaskID.String != "voice-task-1" ||
		queries.providerFailure.ErrorCode != "volcengine_1005002" {
		t.Fatalf("provider failure params = %+v", queries.providerFailure)
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

func TestPostgresStoreRecoversInterruptedSessionsAtStartup(t *testing.T) {
	endedAt := time.Date(2026, time.August, 3, 13, 41, 0, 0, time.UTC)
	row := testDBVoiceCallSession()
	row.Status = string(StatusFailed)
	row.EndedAt = pgtype.Timestamptz{Time: endedAt, Valid: true}
	row.EndReason = "backend_restart_recovery"
	row.ErrorCode = "backend_restart_recovery"
	queries := &restartRecoveryVoiceCallQueries{
		fakeVoiceCallQueries: &fakeVoiceCallQueries{},
		recovered:            []db.VoiceCallSession{row},
	}
	store := &PostgresStore{queries: queries}
	cutoff := time.Date(2026, time.August, 3, 13, 35, 0, 0, time.FixedZone("CST", 8*60*60))

	recovered, err := store.RecoverInterruptedSessionsAtStartup(time.Second, cutoff)
	if err != nil || len(recovered) != 1 ||
		recovered[0].Status != StatusFailed ||
		recovered[0].EndReason != "backend_restart_recovery" ||
		recovered[0].ErrorCode != "backend_restart_recovery" ||
		recovered[0].EndedAt == nil || !recovered[0].EndedAt.Equal(endedAt) {
		t.Fatalf("recovered = %+v error=%v", recovered, err)
	}
	if !queries.cutoff.Valid || !queries.cutoff.Time.Equal(cutoff.UTC()) {
		t.Fatalf("recovery cutoff = %+v, want %s", queries.cutoff, cutoff.UTC())
	}
}

type restartRecoveryVoiceCallQueries struct {
	*fakeVoiceCallQueries
	recovered []db.VoiceCallSession
	cutoff    pgtype.Timestamptz
	err       error
}

func (queries *restartRecoveryVoiceCallQueries) FailVoiceCallSessionsStartedBefore(
	_ context.Context,
	cutoff pgtype.Timestamptz,
) ([]db.VoiceCallSession, error) {
	queries.cutoff = cutoff
	return queries.recovered, queries.err
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

func TestPostgresStoreRejectsInvalidProviderTurnBeforeQuery(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ProviderTurnInput)
	}{
		{
			name: "sequence",
			mutate: func(input *ProviderTurnInput) {
				input.Sequence = 0
			},
		},
		{
			name: "speaker",
			mutate: func(input *ProviderTurnInput) {
				input.Speaker = "system"
			},
		},
		{
			name: "transcript",
			mutate: func(input *ProviderTurnInput) {
				input.Transcript = " "
			},
		},
		{
			name: "provider turn identity",
			mutate: func(input *ProviderTurnInput) {
				input.ProviderTurnID = ""
			},
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			queries := &fakeVoiceCallQueries{
				session: testDBVoiceCallSession(),
				turn:    testDBVoiceCallTurn(),
			}
			store, err := NewPostgresStore(queries)
			if err != nil {
				t.Fatalf("new store: %v", err)
			}
			input := ProviderTurnInput{
				Provider:       "volcengine",
				ProviderTaskID: "voice-task-1",
				Sequence:       1,
				Speaker:        SpeakerMember,
				Transcript:     "你好。",
				ProviderTurnID: "voice-member-1:3",
			}
			testCase.mutate(&input)

			if _, err := store.UpsertProviderTurn(
				context.Background(),
				input,
			); err == nil {
				t.Fatal("invalid provider turn accepted")
			}
			if queries.providerTurnCalls != 0 {
				t.Fatalf("invalid turn reached query %d times", queries.providerTurnCalls)
			}
		})
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
			providerErr:    pgx.ErrNoRows,
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
		if _, err := store.ApplyProviderActive(
			context.Background(), "volcengine", "missing-task",
		); !errors.Is(err, ErrCallNotFound) {
			t.Fatalf("provider callback error = %v, want ErrCallNotFound", err)
		}
		if _, err := store.UpsertProviderTurn(
			context.Background(),
			ProviderTurnInput{
				Provider:       "volcengine",
				ProviderTaskID: "missing-task",
				Sequence:       1,
				Speaker:        SpeakerMember,
				Transcript:     "你好。",
				ProviderTurnID: "voice-member-1:3",
			},
		); !errors.Is(err, ErrCallNotFound) {
			t.Fatalf("provider turn error = %v, want ErrCallNotFound", err)
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
	memberTurn, err := store.UpsertProviderTurn(ctx, ProviderTurnInput{
		Provider:       "volcengine",
		ProviderTaskID: session.ProviderTaskID,
		Sequence:       1,
		Speaker:        SpeakerMember,
		Transcript:     "你好。",
		ProviderTurnID: "voice-member-integration:1",
	})
	if err != nil || memberTurn.Sequence != 1 {
		t.Fatalf("member turn = %+v error=%v", memberTurn, err)
	}
	correctedTurn, err := store.UpsertProviderTurn(ctx, ProviderTurnInput{
		Provider:       "volcengine",
		ProviderTaskID: session.ProviderTaskID,
		Sequence:       1,
		Speaker:        SpeakerMember,
		Transcript:     "你好，贝克汉姆。",
		ProviderTurnID: "voice-member-integration:1",
	})
	if err != nil ||
		correctedTurn.ID != memberTurn.ID ||
		correctedTurn.Sequence != memberTurn.Sequence ||
		correctedTurn.Transcript != "你好，贝克汉姆。" {
		t.Fatalf("corrected turn = %+v error=%v", correctedTurn, err)
	}
	agentTurn, err := store.UpsertProviderTurn(ctx, ProviderTurnInput{
		Provider:       "volcengine",
		ProviderTaskID: session.ProviderTaskID,
		Sequence:       2,
		Speaker:        SpeakerAgent,
		Transcript:     "你好，有什么需要我处理？",
		ProviderTurnID: "voice-agent-integration:1",
	})
	if err != nil || agentTurn.Sequence != 2 {
		t.Fatalf("agent turn = %+v error=%v", agentTurn, err)
	}
	var turnCount int
	if err := connection.QueryRow(
		ctx,
		"SELECT count(*) FROM voice_call_turn WHERE call_session_id = $1",
		session.ID,
	).Scan(&turnCount); err != nil || turnCount != 2 {
		t.Fatalf("turn count = %d error=%v", turnCount, err)
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
	callbackConnecting, err := store.ApplyProviderActive(
		ctx, "volcengine", session.ProviderTaskID,
	)
	if err != nil ||
		callbackConnecting.Status != StatusConnecting ||
		callbackConnecting.ConnectedAt == nil {
		t.Fatalf("early provider callback = %+v error=%v", callbackConnecting, err)
	}
	activeStart, err := store.BeginProviderStart(ctx, testVoiceWorkspaceID, session.ID)
	active := activeStart.Session
	if err != nil || active.Status != StatusActive {
		t.Fatalf("begin provider start: %v", err)
	}
	active, err = store.ApplyProviderActive(ctx, "volcengine", session.ProviderTaskID)
	if err != nil || active.Status != StatusActive {
		t.Fatalf("duplicate provider callback = %+v error=%v", active, err)
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
	lateFailure, err := store.ApplyProviderFailure(
		ctx, "volcengine", session.ProviderTaskID, "volcengine_1005002",
	)
	if err != nil ||
		lateFailure.Status != StatusEnded ||
		lateFailure.ErrorCode != "" {
		t.Fatalf("late provider failure = %+v error=%v", lateFailure, err)
	}

	prepared, err := store.CreateStarting(ctx, NewSession{
		WorkspaceID:    testVoiceWorkspaceID,
		ChannelID:      testVoiceChannelID,
		AgentID:        testVoiceAgentID,
		UserID:         testVoiceUserID,
		Provider:       "volcengine",
		ProviderTaskID: "voice-task-integration-prepared",
		RoomID:         "voice-call-integration-prepared",
	})
	if err != nil {
		t.Fatalf("create prepared session: %v", err)
	}
	preparedEnding, err := store.BeginEnding(
		ctx, testVoiceWorkspaceID, testVoiceUserID, prepared.ID, "browser_closed",
	)
	if err != nil ||
		preparedEnding.ProviderStopRequired ||
		preparedEnding.Session.Status != StatusEnding {
		t.Fatalf("prepared ending = %+v error=%v", preparedEnding, err)
	}
	if _, err := store.MarkEnded(
		ctx, testVoiceWorkspaceID, prepared.ID, "browser_closed",
	); err != nil {
		t.Fatalf("mark prepared session ended: %v", err)
	}

	failing, err := store.CreateStarting(ctx, NewSession{
		WorkspaceID:    testVoiceWorkspaceID,
		ChannelID:      testVoiceChannelID,
		AgentID:        testVoiceAgentID,
		UserID:         testVoiceUserID,
		Provider:       "volcengine",
		ProviderTaskID: "voice-task-integration-failure",
		RoomID:         "voice-call-integration-failure",
	})
	if err != nil {
		t.Fatalf("create failing session: %v", err)
	}
	failed, err := store.ApplyProviderFailure(
		ctx, "volcengine", failing.ProviderTaskID, "volcengine_1005002",
	)
	if err != nil ||
		failed.Status != StatusFailed ||
		failed.ErrorCode != "volcengine_1005002" ||
		failed.EndedAt == nil {
		t.Fatalf("provider failure = %+v error=%v", failed, err)
	}
	failed, err = store.ApplyProviderFailure(
		ctx, "volcengine", failing.ProviderTaskID, "volcengine_1005003",
	)
	if err != nil ||
		failed.Status != StatusFailed ||
		failed.ErrorCode != "volcengine_1005002" {
		t.Fatalf("duplicate provider failure = %+v error=%v", failed, err)
	}
	failed, err = store.ApplyProviderActive(ctx, "volcengine", failing.ProviderTaskID)
	if err != nil || failed.Status != StatusFailed {
		t.Fatalf("late provider activity = %+v error=%v", failed, err)
	}
	failed, err = store.MarkFailed(
		ctx, testVoiceWorkspaceID, failing.ID, "state_transition_failed",
	)
	if err != nil || failed.ErrorCode != "volcengine_1005002" {
		t.Fatalf("duplicate local failure = %+v error=%v", failed, err)
	}

	stopRace, err := store.CreateStarting(ctx, NewSession{
		WorkspaceID:    testVoiceWorkspaceID,
		ChannelID:      testVoiceChannelID,
		AgentID:        testVoiceAgentID,
		UserID:         testVoiceUserID,
		Provider:       "volcengine",
		ProviderTaskID: "voice-task-integration-stop-race",
		RoomID:         "voice-call-integration-stop-race",
	})
	if err != nil {
		t.Fatalf("create stop-race session: %v", err)
	}
	if _, err := store.BeginProviderStart(
		ctx, testVoiceWorkspaceID, stopRace.ID,
	); err != nil {
		t.Fatalf("connect stop-race session: %v", err)
	}
	if _, err := store.BeginEnding(
		ctx,
		testVoiceWorkspaceID,
		testVoiceUserID,
		stopRace.ID,
		"user_hangup",
	); err != nil {
		t.Fatalf("begin stop-race ending: %v", err)
	}
	if _, err := store.ApplyProviderFailure(
		ctx, "volcengine", stopRace.ProviderTaskID, "volcengine_1005004",
	); err != nil {
		t.Fatalf("provider failure during stop: %v", err)
	}
	stopResult, err := store.MarkEnded(
		ctx, testVoiceWorkspaceID, stopRace.ID, "user_hangup",
	)
	if err != nil ||
		stopResult.Status != StatusFailed ||
		stopResult.ErrorCode != "volcengine_1005004" {
		t.Fatalf("provider failure during stop = %+v error=%v", stopResult, err)
	}

	interrupted, err := store.CreateStarting(ctx, NewSession{
		WorkspaceID:    testVoiceWorkspaceID,
		ChannelID:      testVoiceChannelID,
		AgentID:        testVoiceAgentID,
		UserID:         testVoiceUserID,
		Provider:       "volcengine",
		ProviderTaskID: "voice-task-integration-restart",
		RoomID:         "voice-call-integration-restart",
	})
	if err != nil {
		t.Fatalf("create interrupted session: %v", err)
	}
	recovered, err := store.RecoverInterruptedSessionsAtStartup(
		time.Second,
		time.Now().Add(time.Second),
	)
	if err != nil || len(recovered) != 1 || recovered[0].ID != interrupted.ID ||
		recovered[0].Status != StatusFailed ||
		recovered[0].EndReason != "backend_restart_recovery" ||
		recovered[0].ErrorCode != "backend_restart_recovery" ||
		recovered[0].EndedAt == nil {
		t.Fatalf("restart recovery = %+v error=%v", recovered, err)
	}
	if _, err := store.CreateStarting(ctx, NewSession{
		WorkspaceID:    testVoiceWorkspaceID,
		ChannelID:      testVoiceChannelID,
		AgentID:        testVoiceAgentID,
		UserID:         testVoiceUserID,
		Provider:       "volcengine",
		ProviderTaskID: "voice-task-integration-retry",
		RoomID:         "voice-call-integration-retry",
	}); err != nil {
		t.Fatalf("retry after restart recovery: %v", err)
	}
}

const (
	testVoiceCallID      = "10000000-0000-4000-8000-000000000001"
	testVoiceWorkspaceID = "10000000-0000-4000-8000-000000000002"
	testVoiceChannelID   = "10000000-0000-4000-8000-000000000003"
	testVoiceAgentID     = "10000000-0000-4000-8000-000000000004"
	testVoiceUserID      = "10000000-0000-4000-8000-000000000005"
	testVoiceTurnID      = "10000000-0000-4000-8000-000000000006"
)

type fakeVoiceCallQueries struct {
	session           db.VoiceCallSession
	turn              db.UpsertVoiceCallProviderTurnRow
	ending            db.BeginVoiceCallEndingRow
	create            db.CreateVoiceCallSessionParams
	get               db.GetVoiceCallSessionForMemberParams
	connecting        db.BeginVoiceCallProviderStartParams
	failed            db.MarkVoiceCallFailedParams
	beginEnding       db.BeginVoiceCallEndingParams
	ended             db.MarkVoiceCallEndedParams
	providerActive    db.ApplyVoiceCallProviderActiveParams
	clientAnswered    db.ApplyVoiceCallClientAnsweredParams
	providerFailure   db.ApplyVoiceCallProviderFailureParams
	providerTurn      db.UpsertVoiceCallProviderTurnParams
	getCalls          int
	createErr         error
	getErr            error
	beginEndingErr    error
	providerErr       error
	providerTurnCalls int
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

func (queries *fakeVoiceCallQueries) UpsertVoiceCallProviderTurn(
	_ context.Context,
	params db.UpsertVoiceCallProviderTurnParams,
) (db.UpsertVoiceCallProviderTurnRow, error) {
	queries.providerTurn = params
	queries.providerTurnCalls++
	if queries.providerErr != nil {
		return db.UpsertVoiceCallProviderTurnRow{}, queries.providerErr
	}
	return queries.turn, nil
}

func (queries *fakeVoiceCallQueries) BeginVoiceCallProviderStart(
	_ context.Context,
	params db.BeginVoiceCallProviderStartParams,
) (db.BeginVoiceCallProviderStartRow, error) {
	queries.connecting = params
	return testDBBeginProviderStartRow(queries.session, true), nil
}

func (queries *fakeVoiceCallQueries) MarkVoiceCallFailed(
	_ context.Context,
	params db.MarkVoiceCallFailedParams,
) (db.VoiceCallSession, error) {
	queries.failed = params
	return queries.session, nil
}

func (queries *fakeVoiceCallQueries) ApplyVoiceCallProviderActive(
	_ context.Context,
	params db.ApplyVoiceCallProviderActiveParams,
) (db.VoiceCallSession, error) {
	queries.providerActive = params
	if queries.providerErr != nil {
		return db.VoiceCallSession{}, queries.providerErr
	}
	return queries.session, nil
}

func (queries *fakeVoiceCallQueries) ApplyVoiceCallClientAnswered(
	_ context.Context,
	params db.ApplyVoiceCallClientAnsweredParams,
) (db.VoiceCallSession, error) {
	queries.clientAnswered = params
	if queries.providerErr != nil {
		return db.VoiceCallSession{}, queries.providerErr
	}
	return queries.session, nil
}

func (queries *fakeVoiceCallQueries) ApplyVoiceCallProviderFailure(
	_ context.Context,
	params db.ApplyVoiceCallProviderFailureParams,
) (db.VoiceCallSession, error) {
	queries.providerFailure = params
	if queries.providerErr != nil {
		return db.VoiceCallSession{}, queries.providerErr
	}
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

func (queries *fakeVoiceCallQueries) FailVoiceCallSessionsForActivePair(
	_ context.Context,
	params db.FailVoiceCallSessionsForActivePairParams,
) ([]db.VoiceCallSession, error) {
	_ = params
	if queries.session.Status == "ended" || queries.session.Status == "failed" {
		return nil, nil
	}
	queries.session.Status = "failed"
	return []db.VoiceCallSession{queries.session}, nil
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

func testDBVoiceCallTurn() db.UpsertVoiceCallProviderTurnRow {
	return db.UpsertVoiceCallProviderTurnRow{
		ID:             testPGUUID(testVoiceTurnID),
		CallSessionID:  testPGUUID(testVoiceCallID),
		Sequence:       1,
		Speaker:        string(SpeakerMember),
		Transcript:     " 你好。 ",
		IsInterrupted:  false,
		ProviderTurnID: pgtype.Text{String: "voice-member-1:3", Valid: true},
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

func testDBBeginProviderStartRow(
	session db.VoiceCallSession,
	providerStartRequired bool,
) db.BeginVoiceCallProviderStartRow {
	return db.BeginVoiceCallProviderStartRow{
		ID:                    session.ID,
		WorkspaceID:           session.WorkspaceID,
		ChannelID:             session.ChannelID,
		AgentID:               session.AgentID,
		UserID:                session.UserID,
		Provider:              session.Provider,
		ProviderTaskID:        session.ProviderTaskID,
		RoomID:                session.RoomID,
		Status:                string(StatusConnecting),
		StartedAt:             session.StartedAt,
		ConnectedAt:           session.ConnectedAt,
		EndedAt:               session.EndedAt,
		EndReason:             session.EndReason,
		ErrorCode:             session.ErrorCode,
		InputAudioMs:          session.InputAudioMs,
		OutputAudioMs:         session.OutputAudioMs,
		CreatedAt:             session.CreatedAt,
		UpdatedAt:             session.UpdatedAt,
		ProviderStartRequired: providerStartRequired,
	}
}

func testPGUUID(value string) pgtype.UUID {
	return pgtype.UUID{Bytes: uuid.MustParse(value), Valid: true}
}

func uuidStringForTest(value pgtype.UUID) string {
	return uuid.UUID(value.Bytes).String()
}
