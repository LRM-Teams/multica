package service

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/events"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// mockRow implements pgx.Row, returning either a scanned task or pgx.ErrNoRows.
type mockRow struct {
	task *db.AgentInboxEvent
	err  error
}

func (r *mockRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	t := r.task
	ptrs := []any{
		&t.ID, &t.WorkspaceID, &t.AgentSessionID, &t.ConversationID,
		&t.ChannelID, &t.ChatSessionID, &t.AgentID, &t.SourceMessageID,
		&t.Reason, &t.RequiresWake, &t.Status, &t.Priority,
		&t.SeqFrom, &t.SeqTo, &t.Attempt, &t.LastError,
		&t.ClaimedAt, &t.AckedAt, &t.CreatedAt, &t.UpdatedAt,
		&t.TerminalOutcome, &t.TerminalDeliveryID, &t.Retryable, &t.TerminalAt,
		&t.RuntimeID, &t.ExecutionConfig, &t.DeliveryMode, &t.ResponseMode,
		&t.ChannelOnboardingID, &t.IssueID, &t.SourceChatMessageID, &t.Context,
		&t.DispatchedAt, &t.StartedAt, &t.CompletedAt, &t.Result,
		&t.Error, &t.SessionID, &t.WorkDir, &t.TriggerCommentID,
		&t.AutopilotRunID, &t.MaxAttempts, &t.ParentTaskID, &t.FailureReason,
		&t.TriggerSummary, &t.ForceFreshSession, &t.IsLeaderTask,
		&t.WaitReason, &t.InitiatorUserID,
	}
	for i, p := range ptrs {
		if i >= len(dest) {
			break
		}
		reflect.ValueOf(dest[i]).Elem().Set(reflect.ValueOf(p).Elem())
	}
	return nil
}

// mockDBTX routes QueryRow calls: complete/fail queries return ErrNoRows,
// getAgentTask returns the stored task.
type mockDBTX struct {
	task       db.AgentInboxEvent
	failedTask *db.AgentInboxEvent
	executed   []string
	execArgs   [][]interface{}
	queried    []string
}

func (m *mockDBTX) Exec(_ context.Context, sql string, args ...interface{}) (pgconn.CommandTag, error) {
	m.executed = append(m.executed, sql)
	m.execArgs = append(m.execArgs, args)
	return pgconn.NewCommandTag(""), nil
}

func (m *mockDBTX) Query(_ context.Context, _ string, _ ...interface{}) (pgx.Rows, error) {
	return nil, pgx.ErrNoRows
}

func (m *mockDBTX) QueryRow(_ context.Context, sql string, _ ...interface{}) pgx.Row {
	m.queried = append(m.queried, sql)
	if strings.Contains(sql, "INSERT INTO chat_message") || strings.Contains(sql, "RefreshAgentStatusFromTasks") || strings.Contains(sql, "FROM chat_session") {
		return &mockRow{err: pgx.ErrNoRows}
	}
	// CompleteAgentTask and FailAgentTask SQL contain "SET status ="
	if strings.Contains(sql, "SET status =") {
		if m.failedTask != nil {
			return &mockRow{task: m.failedTask}
		}
		return &mockRow{err: pgx.ErrNoRows}
	}
	// GetAgentTask — return the existing task
	return &mockRow{task: &m.task}
}

func TestFailTaskWithoutPublicOutputSkipsChatFailureMessage(t *testing.T) {
	taskID := testUUID(11)
	failed := db.AgentInboxEvent{
		ID:            taskID,
		AgentID:       testUUID(12),
		ChatSessionID: testUUID(13),
		Status:        "failed",
	}
	mock := &mockDBTX{failedTask: &failed}
	svc := &TaskService{Queries: db.New(mock), Bus: events.New()}

	if _, err := svc.FailTaskWithoutPublicOutput(context.Background(), taskID, "invalid restricted JSON", "", "", "restricted_output_invalid"); err != nil {
		t.Fatalf("FailTaskWithoutPublicOutput() error = %v", err)
	}
	for _, sql := range mock.queried {
		if strings.Contains(sql, "INSERT INTO chat_message") {
			t.Fatal("suppressed restricted failure created a public chat message")
		}
	}
}

func testUUID(b byte) pgtype.UUID {
	var u pgtype.UUID
	u.Valid = true
	u.Bytes[0] = b
	return u
}

func TestCompleteTask_AlreadyFinalized(t *testing.T) {
	taskID := testUUID(1)
	agentID := testUUID(2)

	tests := []struct {
		name   string
		status string
	}{
		{"already acknowledged", "acked"},
		{"already suppressed", "suppressed"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := &mockDBTX{task: db.AgentInboxEvent{
				ID:      taskID,
				AgentID: agentID,
				Status:  tt.status,
			}}
			svc := &TaskService{
				Queries: db.New(mock),
				Bus:     events.New(),
			}

			got, err := svc.CompleteTask(context.Background(), taskID, nil, "", "")
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			if got == nil {
				t.Fatal("expected task, got nil")
			}
			if got.Status != tt.status {
				t.Errorf("expected status %q, got %q", tt.status, got.Status)
			}
			if got.ID != taskID {
				t.Error("returned task ID doesn't match")
			}
		})
	}
}

func TestFailTask_AlreadyFinalized(t *testing.T) {
	taskID := testUUID(1)
	agentID := testUUID(2)

	tests := []struct {
		name   string
		status string
	}{
		{"already acknowledged", "acked"},
		{"already suppressed", "suppressed"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := &mockDBTX{task: db.AgentInboxEvent{
				ID:      taskID,
				AgentID: agentID,
				Status:  tt.status,
			}}
			svc := &TaskService{
				Queries: db.New(mock),
				Bus:     events.New(),
			}

			got, err := svc.FailTask(context.Background(), taskID, "agent crashed", "", "", "")
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			if got == nil {
				t.Fatal("expected task, got nil")
			}
			if got.Status != tt.status {
				t.Errorf("expected status %q, got %q", tt.status, got.Status)
			}
			if got.ID != taskID {
				t.Error("returned task ID doesn't match")
			}
		})
	}
}

func TestTaskFailureClassifiers(t *testing.T) {
	cases := []struct {
		reason       string
		wantType     string
		wantResumeOK bool
		wantRetry    bool
	}{
		{reason: "timeout", wantType: "timeout", wantResumeOK: true, wantRetry: true},
		{reason: "codex_semantic_inactivity", wantType: "timeout", wantResumeOK: false, wantRetry: true},
		{reason: "grok_first_turn_no_progress", wantType: "timeout", wantResumeOK: false, wantRetry: false},
		{reason: "runtime_recovery", wantType: "runtime", wantResumeOK: true, wantRetry: true},
		{reason: "iteration_limit", wantType: "agent_output", wantResumeOK: false, wantRetry: false},
		{reason: "api_invalid_request", wantType: "agent_error", wantResumeOK: false, wantRetry: false},
		{reason: "agent_error.context_overflow", wantType: "agent_error", wantResumeOK: false, wantRetry: false},
		{reason: "agent_error", wantType: "agent_error", wantResumeOK: true, wantRetry: false},
	}

	for _, tc := range cases {
		t.Run(tc.reason, func(t *testing.T) {
			if got := taskErrorType(tc.reason); got != tc.wantType {
				t.Fatalf("taskErrorType(%q) = %q, want %q", tc.reason, got, tc.wantType)
			}
			if got := !resumeUnsafeFailureReason(tc.reason); got != tc.wantResumeOK {
				t.Fatalf("resume-safe(%q) = %v, want %v", tc.reason, got, tc.wantResumeOK)
			}
			if got := retryableReasons[tc.reason]; got != tc.wantRetry {
				t.Fatalf("retryableReasons[%q] = %v, want %v", tc.reason, got, tc.wantRetry)
			}
		})
	}
}

func TestFailTask_ClearsMatchingChatResumeAfterGrokNoProgress(t *testing.T) {
	taskID := testUUID(1)
	chatSessionID := testUUID(2)
	agentID := testUUID(3)
	failed := db.AgentInboxEvent{
		ID:            taskID,
		AgentID:       agentID,
		ChatSessionID: chatSessionID,
		Status:        "failed",
	}
	mock := &mockDBTX{failedTask: &failed}
	svc := &TaskService{Queries: db.New(mock), Bus: events.New()}

	if _, err := svc.FailTask(context.Background(), taskID, "grok first stream event timeout", "stuck-session", "/tmp/work", "grok_first_turn_no_progress"); err != nil {
		t.Fatalf("FailTask() error = %v", err)
	}

	var cleared bool
	for i, sql := range mock.executed {
		if strings.Contains(sql, "SET session_id = NULL") && strings.Contains(sql, "AND session_id = $2") {
			if got, ok := mock.execArgs[i][1].(pgtype.Text); !ok || !got.Valid || got.String != "stuck-session" {
				t.Fatalf("clear pointer session argument = %#v, want stuck-session", mock.execArgs[i][1])
			}
			cleared = true
			break
		}
	}
	if !cleared {
		t.Fatal("expected matching chat resume pointer to be cleared for a Grok no-progress failure")
	}
}
