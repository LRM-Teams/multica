package main

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// TestQuickCreateCompletion_SubscribesRequester locks in the fix for the
// quick-create requester not being subscribed to the issue: the agent runs
// the CLI and is recorded as the issue's creator, so the issue:created event
// only auto-subscribes the agent. The completion path must explicitly
// subscribe the human requester so they receive follow-up notifications.
func TestQuickCreateCompletion_SubscribesRequester(t *testing.T) {
	ctx := context.Background()
	queries := db.New(testPool)
	bus := events.New()
	taskSvc := service.NewTaskService(queries, testPool, nil, bus)

	var agentID string
	if err := testPool.QueryRow(ctx,
		`SELECT id::text FROM agent WHERE workspace_id = $1 ORDER BY created_at ASC LIMIT 1`,
		testWorkspaceID,
	).Scan(&agentID); err != nil {
		t.Fatalf("load fixture agent: %v", err)
	}

	task, err := taskSvc.EnqueueQuickCreateTask(ctx,
		parseUUID(testWorkspaceID),
		parseUUID(testUserID),
		parseUUID(agentID),
		pgtype.UUID{},
		"please file a bug",
		pgtype.UUID{},
		pgtype.UUID{},
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("EnqueueQuickCreateTask: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent_task_queue WHERE id = $1`, task.ID)
	})

	if _, err := testPool.Exec(ctx,
		`UPDATE agent_task_queue SET status = 'dispatched', dispatched_at = now() WHERE id = $1`,
		task.ID,
	); err != nil {
		t.Fatalf("dispatch task: %v", err)
	}
	if _, err := queries.StartAgentTask(ctx, task.ID); err != nil {
		t.Fatalf("StartAgentTask: %v", err)
	}

	number, err := queries.IncrementIssueCounter(ctx, parseUUID(testWorkspaceID))
	if err != nil {
		t.Fatalf("IncrementIssueCounter: %v", err)
	}
	issue, err := queries.CreateIssueWithOrigin(ctx, db.CreateIssueWithOriginParams{
		WorkspaceID: parseUUID(testWorkspaceID),
		Title:       "agent-filed bug",
		Status:      "todo",
		Priority:    "none",
		CreatorType: "agent",
		CreatorID:   parseUUID(agentID),
		Number:      number,
		OriginType:  pgtype.Text{String: "quick_create", Valid: true},
		OriginID:    task.ID,
	})
	if err != nil {
		t.Fatalf("CreateIssueWithOrigin: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM issue WHERE id = $1`, issue.ID)
	})

	if _, err := taskSvc.CompleteTask(ctx, task.ID, []byte(`{"output":"done"}`), "", ""); err != nil {
		t.Fatalf("CompleteTask: %v", err)
	}

	if !isSubscribed(t, queries, util.UUIDToString(issue.ID), "member", testUserID) {
		t.Fatal("expected requester to be subscribed after quick-create completion")
	}
}

// TestQuickCreateFailure_DoesNotSubscribeRequester confirms the failure path
// (agent finished without producing an issue) does not invent a subscriber
// row — there is nothing to subscribe to.
func TestQuickCreateFailure_DoesNotSubscribeRequester(t *testing.T) {
	ctx := context.Background()
	queries := db.New(testPool)
	bus := events.New()
	taskSvc := service.NewTaskService(queries, testPool, nil, bus)

	var agentID string
	if err := testPool.QueryRow(ctx,
		`SELECT id::text FROM agent WHERE workspace_id = $1 ORDER BY created_at ASC LIMIT 1`,
		testWorkspaceID,
	).Scan(&agentID); err != nil {
		t.Fatalf("load fixture agent: %v", err)
	}

	task, err := taskSvc.EnqueueQuickCreateTask(ctx,
		parseUUID(testWorkspaceID),
		parseUUID(testUserID),
		parseUUID(agentID),
		pgtype.UUID{},
		"another bug",
		pgtype.UUID{},
		pgtype.UUID{},
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("EnqueueQuickCreateTask: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent_task_queue WHERE id = $1`, task.ID)
	})

	if _, err := testPool.Exec(ctx,
		`UPDATE agent_task_queue SET status = 'dispatched', dispatched_at = now() WHERE id = $1`,
		task.ID,
	); err != nil {
		t.Fatalf("dispatch task: %v", err)
	}
	if _, err := queries.StartAgentTask(ctx, task.ID); err != nil {
		t.Fatalf("StartAgentTask: %v", err)
	}

	// No issue with origin_type=quick_create + this task id exists. Completion
	// hits the failure branch and writes a failure inbox; no subscriber row.
	if _, err := taskSvc.CompleteTask(ctx, task.ID, []byte(`{"output":"done"}`), "", ""); err != nil {
		t.Fatalf("CompleteTask: %v", err)
	}

	var leaked int
	if err := testPool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM issue_subscriber s
		JOIN issue i ON i.id = s.issue_id
		WHERE s.user_type = 'member' AND s.user_id = $1
		  AND i.origin_type = 'quick_create' AND i.origin_id = $2
	`, testUserID, task.ID).Scan(&leaked); err != nil {
		t.Fatalf("count leaked subscribers: %v", err)
	}
	if leaked != 0 {
		t.Fatalf("expected no subscriber rows for failed quick-create, got %d", leaked)
	}
}

func TestQuickCreateCompletion_ReturnsToSourceChannelThreadAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	queries := db.New(testPool)
	taskSvc := service.NewTaskService(queries, testPool, nil, events.New())
	agentID := quickCreateFixtureAgentID(t)
	channelID, rootID, replyID, threadID := seedQuickCreateSourceThread(t, "group", agentID, true)

	source := &protocol.QuickCreateSourceContext{
		ChannelID:           channelID,
		ChannelKind:         "group",
		ChannelName:         "qc-source-group",
		ThreadRootMessageID: rootID,
		SourceMessageID:     replyID,
	}
	task := enqueueStartedQuickCreateTask(t, queries, taskSvc, agentID, source)
	issue := createQuickCreateOriginIssue(t, queries, agentID, task.ID, "source-linked bug")

	if _, err := taskSvc.CompleteTask(ctx, task.ID, []byte(`{"output":"done"}`), "", ""); err != nil {
		t.Fatalf("CompleteTask: %v", err)
	}

	clientMessageID := "quick-create-return:" + util.UUIDToString(task.ID)
	var returnCount int
	var content, gotThreadID string
	var rootText pgtype.Text
	var mainTimelineVisible bool
	if err := testPool.QueryRow(ctx, `
		SELECT COUNT(*), MAX(content), MAX(thread_id), MAX(thread_root_message_id::text), bool_or(main_timeline_visible)
		FROM channel_message
		WHERE channel_id = $1
		  AND author_type = 'agent'
		  AND author_id = $2
		  AND client_message_id = $3
	`, channelID, agentID, clientMessageID).Scan(&returnCount, &content, &gotThreadID, &rootText, &mainTimelineVisible); err != nil {
		t.Fatalf("load return message: %v", err)
	}
	if returnCount != 1 {
		t.Fatalf("return message count = %d, want 1", returnCount)
	}
	if rootText.String != rootID || gotThreadID != threadID {
		t.Fatalf("return thread root/thread = %q/%q, want %q/%q", rootText.String, gotThreadID, rootID, threadID)
	}
	if mainTimelineVisible {
		t.Fatal("return message should stay in source thread, not project into main timeline")
	}
	if !strings.Contains(content, "Created issue [") || !strings.Contains(content, util.UUIDToString(issue.ID)) || !strings.Contains(content, "Status: todo.") {
		t.Fatalf("return content missing human-readable issue summary: %q", content)
	}
	assertQuickCreateReturnMetadata(t, issue.ID, "sent", "")

	// Duplicate terminal callbacks are treated as idempotent success by the
	// service and must not create a second source-thread return.
	if _, err := taskSvc.CompleteTask(ctx, task.ID, []byte(`{"output":"done again"}`), "", ""); err != nil {
		t.Fatalf("duplicate CompleteTask: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM channel_message
		WHERE channel_id = $1
		  AND author_type = 'agent'
		  AND author_id = $2
		  AND client_message_id = $3
	`, channelID, agentID, clientMessageID).Scan(&returnCount); err != nil {
		t.Fatalf("count duplicate return messages: %v", err)
	}
	if returnCount != 1 {
		t.Fatalf("duplicate completion return count = %d, want 1", returnCount)
	}
}

func TestQuickCreateCompletion_ReturnsToSourceDMThread(t *testing.T) {
	ctx := context.Background()
	queries := db.New(testPool)
	taskSvc := service.NewTaskService(queries, testPool, nil, events.New())
	agentID := quickCreateFixtureAgentID(t)
	channelID, rootID, replyID, threadID := seedQuickCreateSourceThread(t, "dm", agentID, true)

	task := enqueueStartedQuickCreateTask(t, queries, taskSvc, agentID, &protocol.QuickCreateSourceContext{
		ChannelID:           channelID,
		ChannelKind:         "dm",
		ThreadRootMessageID: rootID,
		SourceMessageID:     replyID,
	})
	issue := createQuickCreateOriginIssue(t, queries, agentID, task.ID, "dm source bug")

	if _, err := taskSvc.CompleteTask(ctx, task.ID, []byte(`{"output":"done"}`), "", ""); err != nil {
		t.Fatalf("CompleteTask: %v", err)
	}

	clientMessageID := "quick-create-return:" + util.UUIDToString(task.ID)
	var content, gotThreadID, rootText string
	if err := testPool.QueryRow(ctx, `
		SELECT content, thread_id, thread_root_message_id::text
		FROM channel_message
		WHERE channel_id = $1
		  AND author_type = 'agent'
		  AND author_id = $2
		  AND client_message_id = $3
	`, channelID, agentID, clientMessageID).Scan(&content, &gotThreadID, &rootText); err != nil {
		t.Fatalf("load DM return message: %v", err)
	}
	if rootText != rootID || gotThreadID != threadID {
		t.Fatalf("DM return thread root/thread = %q/%q, want %q/%q", rootText, gotThreadID, rootID, threadID)
	}
	if !strings.Contains(content, util.UUIDToString(issue.ID)) {
		t.Fatalf("DM return content missing issue link: %q", content)
	}
	assertQuickCreateReturnMetadata(t, issue.ID, "sent", "")
}

func TestQuickCreateCompletion_ReturnsToSourceRootWithoutExistingThreadID(t *testing.T) {
	ctx := context.Background()
	queries := db.New(testPool)
	taskSvc := service.NewTaskService(queries, testPool, nil, events.New())
	agentID := quickCreateFixtureAgentID(t)
	channelID, rootID, _, _ := seedQuickCreateSourceThread(t, "group", agentID, true)
	if _, err := testPool.Exec(ctx, `DELETE FROM channel_message WHERE thread_root_message_id = $1`, rootID); err != nil {
		t.Fatalf("delete seeded source reply: %v", err)
	}
	if _, err := testPool.Exec(ctx, `UPDATE channel_message SET thread_id = NULL WHERE id = $1`, rootID); err != nil {
		t.Fatalf("make source root threadless: %v", err)
	}

	task := enqueueStartedQuickCreateTask(t, queries, taskSvc, agentID, &protocol.QuickCreateSourceContext{
		ChannelID:           channelID,
		ChannelKind:         "group",
		ThreadRootMessageID: rootID,
		SourceMessageID:     rootID,
	})
	issue := createQuickCreateOriginIssue(t, queries, agentID, task.ID, "root source bug")

	if _, err := taskSvc.CompleteTask(ctx, task.ID, []byte(`{"output":"done"}`), "", ""); err != nil {
		t.Fatalf("CompleteTask: %v", err)
	}

	clientMessageID := "quick-create-return:" + util.UUIDToString(task.ID)
	var rootThreadID, returnThreadID string
	if err := testPool.QueryRow(ctx, `
		SELECT root.thread_id, ret.thread_id
		FROM channel_message root
		JOIN channel_message ret
		  ON ret.thread_root_message_id = root.id
		 AND ret.client_message_id = $3
		WHERE root.channel_id = $1
		  AND root.id = $2
	`, channelID, rootID, clientMessageID).Scan(&rootThreadID, &returnThreadID); err != nil {
		t.Fatalf("load root/return thread ids: %v", err)
	}
	if rootThreadID == "" || returnThreadID != rootThreadID {
		t.Fatalf("root/return thread_id = %q/%q, want same non-empty value", rootThreadID, returnThreadID)
	}
	assertQuickCreateReturnMetadata(t, issue.ID, "sent", "")
}

func TestQuickCreateCompletion_SourceUnavailableRecordsSkippedReturn(t *testing.T) {
	ctx := context.Background()
	queries := db.New(testPool)
	taskSvc := service.NewTaskService(queries, testPool, nil, events.New())
	agentID := quickCreateFixtureAgentID(t)

	task := enqueueStartedQuickCreateTask(t, queries, taskSvc, agentID, &protocol.QuickCreateSourceContext{
		ChannelID:           uuid.NewString(),
		ChannelKind:         "group",
		ThreadRootMessageID: uuid.NewString(),
		SourceMessageID:     uuid.NewString(),
	})
	issue := createQuickCreateOriginIssue(t, queries, agentID, task.ID, "missing source bug")

	if _, err := taskSvc.CompleteTask(ctx, task.ID, []byte(`{"output":"done"}`), "", ""); err != nil {
		t.Fatalf("CompleteTask: %v", err)
	}
	assertQuickCreateReturnMetadata(t, issue.ID, "skipped", "source_channel_unavailable")
	assertQuickCreateTaskActivity(t, task.ID, "source_channel_unavailable")
}

func TestQuickCreateCompletion_SourcePermissionDeniedRecordsSkippedReturn(t *testing.T) {
	ctx := context.Background()
	queries := db.New(testPool)
	taskSvc := service.NewTaskService(queries, testPool, nil, events.New())
	agentID := quickCreateFixtureAgentID(t)
	channelID, rootID, replyID, _ := seedQuickCreateSourceThread(t, "group", agentID, true)

	task := enqueueStartedQuickCreateTask(t, queries, taskSvc, agentID, &protocol.QuickCreateSourceContext{
		ChannelID:           channelID,
		ChannelKind:         "group",
		ThreadRootMessageID: rootID,
		SourceMessageID:     replyID,
	})
	issue := createQuickCreateOriginIssue(t, queries, agentID, task.ID, "permission source bug")

	if _, err := testPool.Exec(ctx, `
		DELETE FROM channel_member
		WHERE channel_id = $1 AND member_type = 'user' AND member_id = $2
	`, channelID, testUserID); err != nil {
		t.Fatalf("remove requester from source channel: %v", err)
	}

	if _, err := taskSvc.CompleteTask(ctx, task.ID, []byte(`{"output":"done"}`), "", ""); err != nil {
		t.Fatalf("CompleteTask: %v", err)
	}
	assertQuickCreateReturnMetadata(t, issue.ID, "skipped", "source_requester_not_member")
	assertQuickCreateTaskActivity(t, task.ID, "source_requester_not_member")
}

func quickCreateFixtureAgentID(t *testing.T) string {
	t.Helper()
	var agentID string
	if err := testPool.QueryRow(context.Background(),
		`SELECT id::text FROM agent WHERE workspace_id = $1 ORDER BY created_at ASC LIMIT 1`,
		testWorkspaceID,
	).Scan(&agentID); err != nil {
		t.Fatalf("load fixture agent: %v", err)
	}
	return agentID
}

func enqueueStartedQuickCreateTask(t *testing.T, queries *db.Queries, taskSvc *service.TaskService, agentID string, source *protocol.QuickCreateSourceContext) db.AgentTaskQueue {
	t.Helper()
	ctx := context.Background()
	task, err := taskSvc.EnqueueQuickCreateTask(ctx,
		parseUUID(testWorkspaceID),
		parseUUID(testUserID),
		parseUUID(agentID),
		pgtype.UUID{},
		"please file a bug",
		pgtype.UUID{},
		pgtype.UUID{},
		nil,
		source,
	)
	if err != nil {
		t.Fatalf("EnqueueQuickCreateTask: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent_task_queue WHERE id = $1`, task.ID)
	})
	if _, err := testPool.Exec(ctx,
		`UPDATE agent_task_queue SET status = 'dispatched', dispatched_at = now() WHERE id = $1`,
		task.ID,
	); err != nil {
		t.Fatalf("dispatch task: %v", err)
	}
	if _, err := queries.StartAgentTask(ctx, task.ID); err != nil {
		t.Fatalf("StartAgentTask: %v", err)
	}
	return task
}

func createQuickCreateOriginIssue(t *testing.T, queries *db.Queries, agentID string, taskID pgtype.UUID, title string) db.Issue {
	t.Helper()
	ctx := context.Background()
	number, err := queries.IncrementIssueCounter(ctx, parseUUID(testWorkspaceID))
	if err != nil {
		t.Fatalf("IncrementIssueCounter: %v", err)
	}
	issue, err := queries.CreateIssueWithOrigin(ctx, db.CreateIssueWithOriginParams{
		WorkspaceID: parseUUID(testWorkspaceID),
		Title:       title,
		Status:      "todo",
		Priority:    "none",
		CreatorType: "agent",
		CreatorID:   parseUUID(agentID),
		Number:      number,
		OriginType:  pgtype.Text{String: "quick_create", Valid: true},
		OriginID:    taskID,
	})
	if err != nil {
		t.Fatalf("CreateIssueWithOrigin: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM issue WHERE id = $1`, issue.ID)
	})
	return issue
}

func seedQuickCreateSourceThread(t *testing.T, kind, agentID string, includeRequester bool) (channelID, rootID, replyID, threadID string) {
	t.Helper()
	ctx := context.Background()
	name := "qc-source-" + kind + "-" + uuid.NewString()
	if err := testPool.QueryRow(ctx, `
		INSERT INTO channel (workspace_id, name, created_by, kind)
		VALUES ($1, $2, $3, $4)
		RETURNING id
	`, testWorkspaceID, name, testUserID, kind).Scan(&channelID); err != nil {
		t.Fatalf("seed source channel: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM channel WHERE id = $1`, channelID)
	})
	if includeRequester {
		if _, err := testPool.Exec(ctx, `
			INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
			VALUES ($1, $2, 'user', $3)
			ON CONFLICT DO NOTHING
		`, channelID, testWorkspaceID, testUserID); err != nil {
			t.Fatalf("seed source requester member: %v", err)
		}
	}
	if agentID != "" {
		if _, err := testPool.Exec(ctx, `
			INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
			VALUES ($1, $2, 'agent', $3)
			ON CONFLICT DO NOTHING
		`, channelID, testWorkspaceID, agentID); err != nil {
			t.Fatalf("seed source agent member: %v", err)
		}
	}
	threadID = "qc-source-thread-" + uuid.NewString()
	if err := testPool.QueryRow(ctx, `
		INSERT INTO channel_message (channel_id, workspace_id, author_type, author_id, author_name, content, source, thread_id, trigger_depth)
		VALUES ($1, $2, 'user', $3, 'Source User', 'source root', 'multica', $4, 0)
		RETURNING id
	`, channelID, testWorkspaceID, testUserID, threadID).Scan(&rootID); err != nil {
		t.Fatalf("seed source root: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO channel_message (channel_id, workspace_id, author_type, author_id, author_name, content, source, thread_root_message_id, thread_id, trigger_depth)
		VALUES ($1, $2, 'user', $3, 'Source User', 'source reply', 'multica', $4, $5, 1)
		RETURNING id
	`, channelID, testWorkspaceID, testUserID, rootID, threadID).Scan(&replyID); err != nil {
		t.Fatalf("seed source reply: %v", err)
	}
	return channelID, rootID, replyID, threadID
}

func assertQuickCreateReturnMetadata(t *testing.T, issueID pgtype.UUID, wantStatus, wantReason string) {
	t.Helper()
	var status, reason string
	if err := testPool.QueryRow(context.Background(), `
		SELECT COALESCE(metadata #>> '{chat_issue_return,status}', ''),
		       COALESCE(metadata #>> '{chat_issue_return,reason}', '')
		FROM issue
		WHERE id = $1
	`, issueID).Scan(&status, &reason); err != nil {
		t.Fatalf("load quick-create return metadata: %v", err)
	}
	if status != wantStatus || reason != wantReason {
		t.Fatalf("return metadata status/reason = %q/%q, want %q/%q", status, reason, wantStatus, wantReason)
	}
}

func assertQuickCreateTaskActivity(t *testing.T, taskID pgtype.UUID, want string) {
	t.Helper()
	var count int
	if err := testPool.QueryRow(context.Background(), `
		SELECT COUNT(*)
		FROM task_message
		WHERE task_id = $1
		  AND content ILIKE '%' || $2 || '%'
	`, taskID, want).Scan(&count); err != nil {
		t.Fatalf("count quick-create task activity: %v", err)
	}
	if count == 0 {
		t.Fatalf("expected task activity containing %q", want)
	}
}
