package service

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Task #50: chat/@mention task dispatch (createChatTaskRow, called by
// enqueueChatTask) does not call AgentReadiness and never checks runtime or
// daemon connectivity before creating a task row — unlike the autopilot
// admission path (shouldSkipDispatch/dispatchRunOnly).
//
// This is deliberate, not a gap — do not "fix" it by adding a connectivity
// gate here. Delivery is pull-only (the daemon claims 'pending' rows via its
// own heartbeat/poll cycle; nothing here pushes to a possibly-unreachable
// daemon), so there is no "wrongly reject/fail because the daemon looked
// offline at creation time" failure mode to guard against. A daemon that
// never comes back simply never claims the row (matching #1673's
// ExpireStaleQueuedTasks, which explicitly protects offline-runtime rows
// from TTL expiry instead of failing them). More importantly: when the
// daemon IS alive but this specific agent's resident process died, task
// arrival is the trigger that rebuilds it (#42②'s crash-recovery). A
// connectivity gate added here would block that arrival — turning a
// recoverable crashed process into one that can never come back, the exact
// deadlock Parker flagged when reviewing this fix ("兜底的触发器长在了它要兜的
// 那个东西身上").
//
// This test pins the current (correct) behavior so a future well-intentioned
// change doesn't add that gate back in.
func TestCreateChatTaskRow_SucceedsRegardlessOfRuntimeConnectivity(t *testing.T) {
	pool := interactionDAGTestPool(t)
	defer pool.Close()

	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer tx.Rollback(ctx)
	q := db.New(tx)

	ws, err := q.CreateWorkspace(ctx, db.CreateWorkspaceParams{
		Name: "chat-dispatch-liveness-test", Slug: "chat-dispatch-liveness-test", IssuePrefix: "CDL",
	})
	require.NoError(t, err)

	user, err := q.CreateUser(ctx, db.CreateUserParams{
		Name: "chat-dispatch-liveness-user", DisplayName: "Chat Dispatch Liveness User", Email: "chat-dispatch-liveness@example.test",
	})
	require.NoError(t, err)

	// Runtime whose heartbeat is long stale — old enough that
	// RuntimeConnectivity would call it dead, not just stale. If
	// createChatTaskRow ever grows a connectivity gate keyed off this
	// column, this fixture is what should make it start failing.
	var deadRuntimeID pgtype.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO agent_runtime (workspace_id, daemon_id, name, runtime_mode, provider, status, device_info, metadata, visibility, last_seen_at, updated_at)
		VALUES ($1, $2, $3, 'local', 'claude', 'online', '', '{}'::jsonb, 'private', now() - interval '20 minutes', now() - interval '19 minutes')
		RETURNING id
	`, ws.ID, "daemon-dead-chat-dispatch", "dead-chat-dispatch-runtime").Scan(&deadRuntimeID)
	require.NoError(t, err)

	agent, err := q.CreateAgent(ctx, db.CreateAgentParams{
		WorkspaceID: ws.ID, Name: "chat-dispatch-liveness-agent", DisplayName: "Chat Dispatch Liveness Agent",
		Description: "test", RuntimeMode: "local", RuntimeConfig: []byte("{}"), RuntimeID: deadRuntimeID,
		MaxConcurrentTasks: 1, Instructions: "", CustomEnv: []byte("{}"), CustomArgs: []byte("[]"),
		Model: pgtype.Text{String: "composer-1.5", Valid: true},
	})
	require.NoError(t, err)

	chatSession, err := q.CreateChatSession(ctx, db.CreateChatSessionParams{
		WorkspaceID: ws.ID, AgentID: agent.ID, CreatorID: user.ID, Title: "chat dispatch liveness",
	})
	require.NoError(t, err)

	s := &TaskService{Queries: q}
	task, err := s.createChatTaskRow(ctx, q, chatSession, user.ID, true, 1)
	require.NoError(t, err, "createChatTaskRow must not reject work because the runtime's heartbeat is stale — delivery is pull-only, a dead daemon simply never claims the row")
	if task.Status != "pending" {
		t.Fatalf("task.Status = %q, want %q", task.Status, "pending")
	}
}
