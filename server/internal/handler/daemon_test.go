package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/internal/daemonws"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/middleware"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// slowProbeLocalSkillListStore wraps a LocalSkillListStore but blocks inside
// HasPending until the provided context is cancelled. PopPending delegates
// to the underlying store. Used to verify that a stalled probe cannot wedge
// the heartbeat — the bound context must cut it short — while the ack-safe
// PopPending path is never reached because HasPending returns an error, not
// true.
type slowProbeLocalSkillListStore struct{ LocalSkillListStore }

func (s slowProbeLocalSkillListStore) HasPending(ctx context.Context, _ string) (bool, error) {
	<-ctx.Done()
	return false, ctx.Err()
}

type slowProbeLocalSkillImportStore struct{ LocalSkillImportStore }

func (s slowProbeLocalSkillImportStore) HasPending(ctx context.Context, _ string) (bool, error) {
	<-ctx.Done()
	return false, ctx.Err()
}

// popRecordingLocalSkillListStore counts PopPending calls so a test can assert
// that the handler never reaches the ack-unsafe side-effecting claim path
// when HasPending reports an empty queue.
type popRecordingLocalSkillListStore struct {
	LocalSkillListStore
	popCalls int
}

func (s *popRecordingLocalSkillListStore) PopPending(ctx context.Context, runtimeID string) (*RuntimeLocalSkillListRequest, error) {
	s.popCalls++
	return s.LocalSkillListStore.PopPending(ctx, runtimeID)
}

type popRecordingLocalSkillImportStore struct {
	LocalSkillImportStore
	popCalls int
}

func (s *popRecordingLocalSkillImportStore) PopPending(ctx context.Context, runtimeID string) (*RuntimeLocalSkillImportRequest, error) {
	s.popCalls++
	return s.LocalSkillImportStore.PopPending(ctx, runtimeID)
}

func setHandlerTestWorkspaceRepos(t *testing.T, repos []map[string]string) {
	t.Helper()
	data, err := json.Marshal(repos)
	if err != nil {
		t.Fatalf("marshal repos: %v", err)
	}
	if _, err := testPool.Exec(context.Background(), `UPDATE workspace SET repos = $1 WHERE id = $2`, data, testWorkspaceID); err != nil {
		t.Fatalf("update workspace repos: %v", err)
	}
	t.Cleanup(func() {
		if _, err := testPool.Exec(context.Background(), `UPDATE workspace SET repos = $1 WHERE id = $2`, []byte("[]"), testWorkspaceID); err != nil {
			t.Fatalf("reset workspace repos: %v", err)
		}
	})
}

// newDaemonTokenRequest creates an HTTP request with daemon token context set
// (simulating DaemonAuth middleware for mdt_ tokens).
func newDaemonTokenRequest(method, path string, body any, workspaceID, daemonID string) *http.Request {
	var buf bytes.Buffer
	if body != nil {
		json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	// No X-User-ID — daemon tokens don't set it.
	ctx := middleware.WithDaemonContext(req.Context(), workspaceID, daemonID)
	return req.WithContext(ctx)
}

func createChannelCompletionTask(t *testing.T, channelKind string) (taskID, channelID string) {
	t.Helper()
	return createChannelCompletionTaskWithCapabilities(t, channelKind, []string{protocol.DaemonCapabilityChannelOutputActions})
}

func createChannelCompletionTaskWithCapabilities(t *testing.T, channelKind string, capabilities []string) (taskID, channelID string) {
	t.Helper()
	ctx := context.Background()

	metadata := []byte(`{}`)
	if capabilities != nil {
		var err error
		metadata, err = json.Marshal(map[string]any{"capabilities": capabilities})
		if err != nil {
			t.Fatalf("setup: marshal runtime capabilities: %v", err)
		}
	}
	var runtimeID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_runtime (
			workspace_id, daemon_id, name, runtime_mode, provider, status, device_info, metadata, last_seen_at
		)
		VALUES ($1, $2, $3, 'local', 'handler_test_runtime', 'online', 'complete-output runtime', $4, now())
		RETURNING id
	`, testWorkspaceID, "complete-output-"+uuid.NewString(), "Complete Output Runtime "+uuid.NewString(), metadata).Scan(&runtimeID); err != nil {
		t.Fatalf("setup: create completion runtime: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE id = $1`, runtimeID) })

	var agentID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent (
			workspace_id, name, description, runtime_mode, runtime_config,
			runtime_id, visibility, max_concurrent_tasks, owner_id,
			instructions, custom_env, custom_args, mcp_config
		)
		VALUES ($1, $2, '', 'local', '{}'::jsonb, $3, 'private', 1, $4, '', '{}'::jsonb, '[]'::jsonb, NULL)
		RETURNING id
	`, testWorkspaceID, "Complete Output Agent "+uuid.NewString(), runtimeID, testUserID).Scan(&agentID); err != nil {
		t.Fatalf("setup: create completion agent: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM agent WHERE id = $1`, agentID) })

	if err := testPool.QueryRow(ctx, `
		INSERT INTO channel (workspace_id, name, created_by, kind)
		VALUES ($1, $2, $3, $4)
		RETURNING id
	`, testWorkspaceID, "complete-output-"+channelKind+"-"+uuid.NewString(), testUserID, channelKind).Scan(&channelID); err != nil {
		t.Fatalf("setup: create channel: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM channel WHERE id = $1`, channelID) })

	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'user', $3), ($1, $2, 'agent', $4)
	`, channelID, testWorkspaceID, testUserID, agentID); err != nil {
		t.Fatalf("setup: seed channel members: %v", err)
	}

	var chatSessionID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO chat_session (workspace_id, agent_id, creator_id, title)
		VALUES ($1, $2, $3, $4)
		RETURNING id
	`, testWorkspaceID, agentID, testUserID, "complete-output session").Scan(&chatSessionID); err != nil {
		t.Fatalf("setup: create chat session: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM chat_session WHERE id = $1`, chatSessionID) })

	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_agent_session (channel_id, agent_id, chat_session_id)
		VALUES ($1, $2, $3)
	`, channelID, agentID, chatSessionID); err != nil {
		t.Fatalf("setup: create channel agent session: %v", err)
	}

	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_task_queue (agent_id, runtime_id, chat_session_id, status, priority, started_at)
		VALUES ($1, $2, $3, 'running', 2, now())
		RETURNING id
	`, agentID, runtimeID, chatSessionID).Scan(&taskID); err != nil {
		t.Fatalf("setup: create running chat task: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM agent_task_queue WHERE id = $1`, taskID) })

	return taskID, channelID
}

func completeTaskForTest(t *testing.T, taskID string, body any) *httptest.ResponseRecorder {
	t.Helper()
	testHandler.Bus.Subscribe(protocol.EventChatDone, func(e events.Event) {
		payload, ok := e.Payload.(protocol.ChatDonePayload)
		if ok && payload.TaskID == taskID {
			testHandler.handleChannelChatDone(e)
		}
	})
	w := httptest.NewRecorder()
	req := newDaemonTokenRequest(http.MethodPost, "/api/daemon/tasks/"+taskID+"/complete", body, testWorkspaceID, "legit-daemon")
	req = withURLParams(req, "taskId", taskID)
	testHandler.CompleteTask(w, req)
	return w
}

func assertTaskOutputSuppressedReason(t *testing.T, taskID, want string) {
	t.Helper()
	var raw []byte
	if err := testPool.QueryRow(context.Background(), `SELECT result FROM agent_task_queue WHERE id = $1`, taskID).Scan(&raw); err != nil {
		t.Fatalf("load task result: %v", err)
	}
	var payload protocol.TaskCompletedPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("decode task result: %v", err)
	}
	if payload.OutputSuppressedReason != want {
		t.Fatalf("output_suppressed_reason = %q, want %q", payload.OutputSuppressedReason, want)
	}
}

func createClaimReclaimRuntime(t *testing.T, ctx context.Context, name string) string {
	t.Helper()

	var runtimeID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_runtime (
			workspace_id, daemon_id, name, runtime_mode, provider,
			status, device_info, metadata, last_seen_at, visibility
		)
		VALUES ($1, NULL, $2, 'cloud', 'handler_test_runtime', 'online', 'claim reclaim fixture', '{}'::jsonb, now(), 'private')
		RETURNING id
	`, testWorkspaceID, name).Scan(&runtimeID); err != nil {
		t.Fatalf("setup: create runtime: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM agent_runtime WHERE id = $1`, runtimeID) })

	return runtimeID
}

func createClaimReclaimAgentAndIssue(t *testing.T, ctx context.Context, runtimeID, name string) (string, string) {
	t.Helper()

	var agentID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent (
			workspace_id, name, description, runtime_mode, runtime_config,
			runtime_id, visibility, max_concurrent_tasks, owner_id
		)
		VALUES ($1, $2, '', 'cloud', '{}'::jsonb, $3, 'private', 1, $4)
		RETURNING id
	`, testWorkspaceID, name, runtimeID, testUserID).Scan(&agentID); err != nil {
		t.Fatalf("setup: create agent: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM agent WHERE id = $1`, agentID) })

	var issueID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO issue (workspace_id, title, status, priority, creator_id, creator_type, number, position)
		VALUES (
			$1, $2, 'in_progress', 'none', $3, 'member',
			(SELECT COALESCE(MAX(number), 82649) + 1 FROM issue WHERE workspace_id = $1),
			0
		)
		RETURNING id
	`, testWorkspaceID, name+" issue", testUserID).Scan(&issueID); err != nil {
		t.Fatalf("setup: create issue: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM issue WHERE id = $1`, issueID) })

	return agentID, issueID
}

func createDispatchedClaimFixtureTask(t *testing.T, ctx context.Context, agentID, runtimeID, issueID, dispatchedAge string, started bool) string {
	t.Helper()

	var taskID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_task_queue (
			agent_id, runtime_id, issue_id, status, priority, dispatched_at, started_at
		)
		VALUES ($1, $2, $3, 'dispatched', 0, now() - ($4::interval), CASE WHEN $5::boolean THEN now() ELSE NULL END)
		RETURNING id
	`, agentID, runtimeID, issueID, dispatchedAge, started).Scan(&taskID); err != nil {
		t.Fatalf("setup: create dispatched task: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM agent_task_queue WHERE id = $1`, taskID) })

	return taskID
}

func claimTaskByRuntimeForTest(t *testing.T, runtimeID string) (*struct {
	ID string `json:"id"`
}, string) {
	t.Helper()

	w := httptest.NewRecorder()
	req := newDaemonTokenRequest("POST", "/api/daemon/runtimes/"+runtimeID+"/tasks/claim", nil,
		testWorkspaceID, "claim-reclaim-review")
	req = withURLParam(req, "runtimeId", runtimeID)

	testHandler.ClaimTaskByRuntime(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("ClaimTaskByRuntime: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Task *struct {
			ID string `json:"id"`
		} `json:"task"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode claim response: %v", err)
	}
	return resp.Task, w.Body.String()
}

func TestClaimTaskByRuntime_ReclaimsStaleDispatchedTask(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	runtimeID := createClaimReclaimRuntime(t, ctx, "Stale dispatch reclaim runtime")
	agentID, issueID := createClaimReclaimAgentAndIssue(t, ctx, runtimeID, "Stale dispatch reclaim agent")
	taskID := createDispatchedClaimFixtureTask(t, ctx, agentID, runtimeID, issueID, "120 seconds", false)

	task, body := claimTaskByRuntimeForTest(t, runtimeID)
	if task == nil {
		t.Fatalf("expected stale dispatched task %s to be reclaimed, got nil response: %s", taskID, body)
	}
	if task.ID != taskID {
		t.Fatalf("reclaimed task id = %s, want %s", task.ID, taskID)
	}

	var refreshed bool
	if err := testPool.QueryRow(ctx, `
		SELECT dispatched_at > now() - interval '15 seconds'
		FROM agent_task_queue
		WHERE id = $1
	`, taskID).Scan(&refreshed); err != nil {
		t.Fatalf("load refreshed dispatched_at: %v", err)
	}
	if !refreshed {
		t.Fatal("expected reclaimed task to refresh dispatched_at")
	}
}

func TestClaimTaskByRuntime_DoesNotReclaimFreshDispatchedTask(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	runtimeID := createClaimReclaimRuntime(t, ctx, "Fresh dispatch reclaim runtime")
	agentID, issueID := createClaimReclaimAgentAndIssue(t, ctx, runtimeID, "Fresh dispatch reclaim agent")
	taskID := createDispatchedClaimFixtureTask(t, ctx, agentID, runtimeID, issueID, "75 seconds", false)

	task, body := claimTaskByRuntimeForTest(t, runtimeID)
	if task != nil {
		t.Fatalf("expected fresh dispatched task %s not to be reclaimed, got %s in %s", taskID, task.ID, body)
	}

	var stillFresh bool
	if err := testPool.QueryRow(ctx, `
		SELECT dispatched_at < now() - interval '70 seconds'
		FROM agent_task_queue
		WHERE id = $1
	`, taskID).Scan(&stillFresh); err != nil {
		t.Fatalf("load fresh dispatched task: %v", err)
	}
	if !stillFresh {
		t.Fatal("expected fresh dispatched task to keep its original dispatched_at")
	}
}

func TestClaimTaskByRuntime_DoesNotReclaimAlreadyStartedTask(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	runtimeID := createClaimReclaimRuntime(t, ctx, "Started dispatch reclaim runtime")
	agentID, issueID := createClaimReclaimAgentAndIssue(t, ctx, runtimeID, "Started dispatch reclaim agent")
	taskID := createDispatchedClaimFixtureTask(t, ctx, agentID, runtimeID, issueID, "120 seconds", true)

	task, body := claimTaskByRuntimeForTest(t, runtimeID)
	if task != nil {
		t.Fatalf("expected started dispatched task %s not to be reclaimed, got %s in %s", taskID, task.ID, body)
	}

	var startedAtValid bool
	if err := testPool.QueryRow(ctx, `
		SELECT started_at IS NOT NULL
		FROM agent_task_queue
		WHERE id = $1
	`, taskID).Scan(&startedAtValid); err != nil {
		t.Fatalf("load started dispatched task: %v", err)
	}
	if !startedAtValid {
		t.Fatal("expected started dispatched task to keep started_at")
	}
}

func TestClaimTaskByRuntime_DoesNotReclaimDifferentRuntimeTask(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	claimingRuntimeID := createClaimReclaimRuntime(t, ctx, "Claiming dispatch reclaim runtime")
	owningRuntimeID := createClaimReclaimRuntime(t, ctx, "Owning dispatch reclaim runtime")
	agentID, issueID := createClaimReclaimAgentAndIssue(t, ctx, owningRuntimeID, "Different runtime reclaim agent")
	taskID := createDispatchedClaimFixtureTask(t, ctx, agentID, owningRuntimeID, issueID, "120 seconds", false)

	task, body := claimTaskByRuntimeForTest(t, claimingRuntimeID)
	if task != nil {
		t.Fatalf("expected other-runtime task %s not to be reclaimed, got %s in %s", taskID, task.ID, body)
	}

	var runtimeID string
	if err := testPool.QueryRow(ctx, `
		SELECT runtime_id::text
		FROM agent_task_queue
		WHERE id = $1
	`, taskID).Scan(&runtimeID); err != nil {
		t.Fatalf("load other-runtime dispatched task: %v", err)
	}
	if runtimeID != owningRuntimeID {
		t.Fatalf("task runtime_id = %s, want %s", runtimeID, owningRuntimeID)
	}
}

func TestClaimTaskByRuntime_ClaimsChatWhenIssueTaskConsumesCapacity(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	runtimeID := createClaimReclaimRuntime(t, ctx, "Chat fallback runtime")
	agentID, issueID := createClaimReclaimAgentAndIssue(t, ctx, runtimeID, "Chat fallback agent")

	var runningIssueTaskID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_task_queue (agent_id, runtime_id, issue_id, status, priority, started_at)
		VALUES ($1, $2, $3, 'running', 0, now())
		RETURNING id
	`, agentID, runtimeID, issueID).Scan(&runningIssueTaskID); err != nil {
		t.Fatalf("setup: create running issue task: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM agent_task_queue WHERE id = $1`, runningIssueTaskID) })

	var chatSessionID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO chat_session (workspace_id, agent_id, creator_id, title)
		VALUES ($1, $2, $3, 'chat fallback session')
		RETURNING id
	`, testWorkspaceID, agentID, testUserID).Scan(&chatSessionID); err != nil {
		t.Fatalf("setup: create chat session: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM chat_session WHERE id = $1`, chatSessionID) })

	var chatTaskID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_task_queue (agent_id, runtime_id, chat_session_id, status, priority)
		VALUES ($1, $2, $3, 'queued', 2)
		RETURNING id
	`, agentID, runtimeID, chatSessionID).Scan(&chatTaskID); err != nil {
		t.Fatalf("setup: create queued chat task: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM agent_task_queue WHERE id = $1`, chatTaskID) })
	if _, err := testPool.Exec(ctx, `
		INSERT INTO chat_message (chat_session_id, role, content)
		VALUES ($1, 'user', 'please answer from the group channel')
	`, chatSessionID); err != nil {
		t.Fatalf("setup: insert chat prompt: %v", err)
	}

	task, body := claimTaskByRuntimeForTest(t, runtimeID)
	if task == nil {
		t.Fatalf("expected queued chat task to bypass issue capacity, got nil response: %s", body)
	}
	if task.ID != chatTaskID {
		t.Fatalf("claimed task id = %s, want chat task %s", task.ID, chatTaskID)
	}
	if got := taskStatus(t, runningIssueTaskID); got != "running" {
		t.Fatalf("running issue task status = %q, want running", got)
	}
	if got := taskStatus(t, chatTaskID); got != "dispatched" {
		t.Fatalf("chat task status = %q, want dispatched", got)
	}
}

func TestClaimTaskByRuntime_ChatBypassUsesSingleInterruptSlot(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	runtimeID := createClaimReclaimRuntime(t, ctx, "Chat single slot runtime")
	agentID, _ := createClaimReclaimAgentAndIssue(t, ctx, runtimeID, "Chat single slot agent")
	if _, err := testPool.Exec(ctx, `UPDATE agent SET max_concurrent_tasks = 3 WHERE id = $1`, agentID); err != nil {
		t.Fatalf("setup: widen max_concurrent_tasks: %v", err)
	}

	var runningChatSessionID, queuedChatSessionID string
	for i, dest := range []*string{&runningChatSessionID, &queuedChatSessionID} {
		if err := testPool.QueryRow(ctx, `
			INSERT INTO chat_session (workspace_id, agent_id, creator_id, title)
			VALUES ($1, $2, $3, $4)
			RETURNING id
		`, testWorkspaceID, agentID, testUserID, fmt.Sprintf("chat slot session %d", i)).Scan(dest); err != nil {
			t.Fatalf("setup: create chat session %d: %v", i, err)
		}
		sid := *dest
		t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM chat_session WHERE id = $1`, sid) })
		if _, err := testPool.Exec(ctx, `INSERT INTO chat_message (chat_session_id, role, content) VALUES ($1, 'user', 'ping')`, sid); err != nil {
			t.Fatalf("setup: insert chat message %d: %v", i, err)
		}
	}

	var runningChatTaskID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_task_queue (agent_id, runtime_id, chat_session_id, status, priority, started_at)
		VALUES ($1, $2, $3, 'running', 2, now())
		RETURNING id
	`, agentID, runtimeID, runningChatSessionID).Scan(&runningChatTaskID); err != nil {
		t.Fatalf("setup: create running chat task: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM agent_task_queue WHERE id = $1`, runningChatTaskID) })

	var queuedChatTaskID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_task_queue (agent_id, runtime_id, chat_session_id, status, priority)
		VALUES ($1, $2, $3, 'queued', 2)
		RETURNING id
	`, agentID, runtimeID, queuedChatSessionID).Scan(&queuedChatTaskID); err != nil {
		t.Fatalf("setup: create queued chat task: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM agent_task_queue WHERE id = $1`, queuedChatTaskID) })

	task, body := claimTaskByRuntimeForTest(t, runtimeID)
	if task != nil {
		t.Fatalf("expected second chat task %s to wait for the single chat interrupt slot, got %s in %s", queuedChatTaskID, task.ID, body)
	}
}

// TestClaimTaskByRuntime_PopulatesWorkspaceContext verifies the claim
// response carries workspace.context so the daemon can inject the
// workspace-level system prompt into every agent brief. Regression coverage
// for MUL-2542: before this fix the field was never plumbed through, so
// even workspaces that had set a context got an empty brief.
func TestClaimTaskByRuntime_PopulatesWorkspaceContext(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	const wsContext = "All comments must be in English. Prefer concise PR descriptions."
	var prior string
	if err := testPool.QueryRow(ctx, `SELECT COALESCE(context, '') FROM workspace WHERE id = $1`, testWorkspaceID).Scan(&prior); err != nil {
		t.Fatalf("read workspace.context: %v", err)
	}
	if _, err := testPool.Exec(ctx, `UPDATE workspace SET context = $1 WHERE id = $2`, wsContext, testWorkspaceID); err != nil {
		t.Fatalf("set workspace.context: %v", err)
	}
	t.Cleanup(func() {
		if prior == "" {
			testPool.Exec(ctx, `UPDATE workspace SET context = NULL WHERE id = $1`, testWorkspaceID)
		} else {
			testPool.Exec(ctx, `UPDATE workspace SET context = $1 WHERE id = $2`, prior, testWorkspaceID)
		}
	})

	runtimeID := createClaimReclaimRuntime(t, ctx, "Workspace context claim runtime")
	agentID, issueID := createClaimReclaimAgentAndIssue(t, ctx, runtimeID, "Workspace context claim agent")
	taskID := createDispatchedClaimFixtureTask(t, ctx, agentID, runtimeID, issueID, "120 seconds", false)

	w := httptest.NewRecorder()
	req := newDaemonTokenRequest("POST", "/api/daemon/runtimes/"+runtimeID+"/tasks/claim", nil,
		testWorkspaceID, "workspace-context-claim")
	req = withURLParam(req, "runtimeId", runtimeID)
	testHandler.ClaimTaskByRuntime(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("ClaimTaskByRuntime: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Task *struct {
			ID               string `json:"id"`
			WorkspaceContext string `json:"workspace_context"`
		} `json:"task"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode claim response: %v", err)
	}
	if resp.Task == nil {
		t.Fatalf("expected dispatched task %s to be claimed, got nil response: %s", taskID, w.Body.String())
	}
	if resp.Task.ID != taskID {
		t.Fatalf("claimed task id = %s, want %s", resp.Task.ID, taskID)
	}
	if resp.Task.WorkspaceContext != wsContext {
		t.Errorf("workspace_context = %q, want %q", resp.Task.WorkspaceContext, wsContext)
	}
}

// TestClaimTaskByRuntime_WorkspaceContextEmptyWhenUnset verifies the field
// is omitted (empty string after JSON decode) when the workspace owner has
// not set a context. Important because the daemon's brief skips the heading
// only on empty input — a stray "context: null" coming back as the string
// "null" would render as a bogus paragraph.
func TestClaimTaskByRuntime_WorkspaceContextEmptyWhenUnset(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	var prior string
	if err := testPool.QueryRow(ctx, `SELECT COALESCE(context, '') FROM workspace WHERE id = $1`, testWorkspaceID).Scan(&prior); err != nil {
		t.Fatalf("read workspace.context: %v", err)
	}
	if _, err := testPool.Exec(ctx, `UPDATE workspace SET context = NULL WHERE id = $1`, testWorkspaceID); err != nil {
		t.Fatalf("clear workspace.context: %v", err)
	}
	t.Cleanup(func() {
		if prior == "" {
			testPool.Exec(ctx, `UPDATE workspace SET context = NULL WHERE id = $1`, testWorkspaceID)
		} else {
			testPool.Exec(ctx, `UPDATE workspace SET context = $1 WHERE id = $2`, prior, testWorkspaceID)
		}
	})

	runtimeID := createClaimReclaimRuntime(t, ctx, "Workspace context empty claim runtime")
	agentID, issueID := createClaimReclaimAgentAndIssue(t, ctx, runtimeID, "Workspace context empty claim agent")
	taskID := createDispatchedClaimFixtureTask(t, ctx, agentID, runtimeID, issueID, "120 seconds", false)

	w := httptest.NewRecorder()
	req := newDaemonTokenRequest("POST", "/api/daemon/runtimes/"+runtimeID+"/tasks/claim", nil,
		testWorkspaceID, "workspace-context-empty-claim")
	req = withURLParam(req, "runtimeId", runtimeID)
	testHandler.ClaimTaskByRuntime(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("ClaimTaskByRuntime: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Task *struct {
			ID               string `json:"id"`
			WorkspaceContext string `json:"workspace_context"`
		} `json:"task"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode claim response: %v", err)
	}
	if resp.Task == nil {
		t.Fatalf("expected dispatched task %s to be claimed, got nil: %s", taskID, w.Body.String())
	}
	if resp.Task.WorkspaceContext != "" {
		t.Errorf("workspace_context = %q, want empty string when workspace.context is NULL", resp.Task.WorkspaceContext)
	}
}

func TestDaemonRegister_WithDaemonToken(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	w := httptest.NewRecorder()
	req := newDaemonTokenRequest("POST", "/api/daemon/register", map[string]any{
		"workspace_id": testWorkspaceID,
		"daemon_id":    "test-daemon-mdt",
		"device_name":  "test-device",
		"cli_version":  "v0.3.0",
		"capabilities": []string{
			protocol.DaemonCapabilityChannelOutputActions,
			protocol.DaemonCapabilityChannelOutputActions,
			protocol.DaemonCapabilityAgentCLITransport,
			protocol.DaemonCapabilityAgentCLITransport,
			" ",
		},
		"runtimes": []map[string]any{
			{"name": "test-runtime", "type": "claude", "version": "1.0.0", "status": "online"},
		},
	}, testWorkspaceID, "test-daemon-mdt")

	testHandler.DaemonRegister(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("DaemonRegister with daemon token: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	runtimes, ok := resp["runtimes"].([]any)
	if !ok || len(runtimes) == 0 {
		t.Fatalf("DaemonRegister: expected runtimes in response, got %v", resp)
	}
	if _, ok := resp["repos_version"].(string); !ok {
		t.Fatalf("DaemonRegister: expected repos_version in response, got %v", resp)
	}
	metadata := runtimes[0].(map[string]any)["metadata"].(map[string]any)
	if got := metadata["cli_version"]; got != "v0.3.0" {
		t.Fatalf("metadata.cli_version = %v, want v0.3.0", got)
	}
	capabilities, ok := metadata["capabilities"].([]any)
	if !ok || !capabilitiesContainExactly(capabilities, protocol.DaemonCapabilityChannelOutputActions, protocol.DaemonCapabilityAgentCLITransport) {
		t.Fatalf("metadata.capabilities = %#v, want [%q %q]", metadata["capabilities"], protocol.DaemonCapabilityChannelOutputActions, protocol.DaemonCapabilityAgentCLITransport)
	}
	responseCapabilities, ok := runtimes[0].(map[string]any)["capabilities"].([]any)
	if !ok || !capabilitiesContainExactly(responseCapabilities, protocol.DaemonCapabilityChannelOutputActions, protocol.DaemonCapabilityAgentCLITransport) {
		t.Fatalf("runtime.capabilities = %#v, want [%q %q]", runtimes[0].(map[string]any)["capabilities"], protocol.DaemonCapabilityChannelOutputActions, protocol.DaemonCapabilityAgentCLITransport)
	}

	// Clean up: deregister the runtime.
	rt := runtimes[0].(map[string]any)
	runtimeID := rt["id"].(string)
	testPool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE id = $1`, runtimeID)
}

func capabilitiesContainExactly(got []any, want ...string) bool {
	if len(got) != len(want) {
		return false
	}
	seen := make(map[string]struct{}, len(got))
	for _, v := range got {
		s, ok := v.(string)
		if !ok {
			return false
		}
		seen[s] = struct{}{}
	}
	if len(seen) != len(want) {
		return false
	}
	for _, w := range want {
		if _, ok := seen[w]; !ok {
			return false
		}
	}
	return true
}

func TestDaemonRegister_WithDaemonToken_WorkspaceMismatch(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	w := httptest.NewRecorder()
	// Daemon token is for a different workspace than the request body.
	req := newDaemonTokenRequest("POST", "/api/daemon/register", map[string]any{
		"workspace_id": testWorkspaceID,
		"daemon_id":    "test-daemon-mdt",
		"device_name":  "test-device",
		"runtimes": []map[string]any{
			{"name": "test-runtime", "type": "claude", "version": "1.0.0", "status": "online"},
		},
	}, "00000000-0000-0000-0000-000000000000", "test-daemon-mdt")

	testHandler.DaemonRegister(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("DaemonRegister with mismatched workspace: expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestDaemonHeartbeat_WithDaemonToken_CrossWorkspace(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	// First, register a runtime using PAT (existing flow).
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/daemon/register", map[string]any{
		"workspace_id": testWorkspaceID,
		"daemon_id":    "test-daemon-heartbeat",
		"device_name":  "test-device",
		"runtimes": []map[string]any{
			{"name": "test-runtime-hb", "type": "claude", "version": "1.0.0", "status": "online"},
		},
	})
	testHandler.DaemonRegister(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("Setup: DaemonRegister failed: %d: %s", w.Code, w.Body.String())
	}
	var regResp map[string]any
	json.NewDecoder(w.Body).Decode(&regResp)
	runtimes := regResp["runtimes"].([]any)
	runtimeID := runtimes[0].(map[string]any)["id"].(string)
	defer testPool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE id = $1`, runtimeID)

	// Try heartbeat with a daemon token from a DIFFERENT workspace — should fail.
	w = httptest.NewRecorder()
	req = newDaemonTokenRequest("POST", "/api/daemon/heartbeat", map[string]any{
		"runtime_id": runtimeID,
	}, "00000000-0000-0000-0000-000000000000", "attacker-daemon")

	testHandler.DaemonHeartbeat(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("DaemonHeartbeat with cross-workspace token: expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

// TestHandleDaemonWSHeartbeat_RuntimeGoneReturnsAckNotError pins the fix for
// issue #2391: when GetAgentRuntime returns pgx.ErrNoRows (runtime row was
// deleted server-side), the WS handler must return a successful ack with
// RuntimeGone=true rather than an error. Returning an error makes the WS hub
// log every beat at Warn — the flood the issue is about.
func TestHandleDaemonWSHeartbeat_RuntimeGoneReturnsAckNotError(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	// A well-formed UUID that does NOT exist in agent_runtime. The handler
	// must turn the resulting pgx.ErrNoRows into a RuntimeGone ack.
	missingRuntime := uuid.New().String()
	ack, err := testHandler.HandleDaemonWSHeartbeat(context.Background(),
		daemonws.ClientIdentity{WorkspaceID: testWorkspaceID},
		missingRuntime, false)
	if err != nil {
		t.Fatalf("HandleDaemonWSHeartbeat: unexpected error %v", err)
	}
	if ack == nil {
		t.Fatal("HandleDaemonWSHeartbeat: nil ack for missing runtime")
	}
	if !ack.RuntimeGone {
		t.Fatalf("ack.RuntimeGone = false, want true")
	}
	if ack.Status != protocol.HeartbeatStatusRuntimeGone {
		t.Fatalf("ack.Status = %q, want %q", ack.Status, protocol.HeartbeatStatusRuntimeGone)
	}
	if ack.RuntimeID != missingRuntime {
		t.Fatalf("ack.RuntimeID = %q, want %q", ack.RuntimeID, missingRuntime)
	}
}

// TestDaemonHeartbeat_HTTPRuntimeGoneReturns404 pins the HTTP-path mirror:
// pgx.ErrNoRows on the runtime lookup is the only DB error mapped to 404.
// Anything else (transient pool issue, schema mismatch, ...) must surface
// as 500 so the daemon does not mistake a hiccup for a deletion.
func TestDaemonHeartbeat_HTTPRuntimeGoneReturns404(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	missingRuntime := uuid.New().String()
	w := httptest.NewRecorder()
	req := newDaemonTokenRequest(http.MethodPost, "/api/daemon/heartbeat", map[string]any{
		"runtime_id": missingRuntime,
	}, testWorkspaceID, "test-daemon")
	testHandler.DaemonHeartbeat(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for missing runtime, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(strings.ToLower(w.Body.String()), "runtime not found") {
		t.Fatalf("expected 'runtime not found' body, got %s", w.Body.String())
	}
}

// TestDaemonHeartbeat_SlowProbeDoesNotWedge pins the invariant that a stalled
// HasPending probe cannot wedge the heartbeat endpoint past the per-probe
// timeout. The probe is the only bounded call; PopPending is ack-safe-
// critical and is intentionally left unbounded. Without the probe bound the
// heartbeat would hang on a slow shared store.
func TestDaemonHeartbeat_SlowProbeDoesNotWedge(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	runtimeID := createRuntimeLocalSkillTestRuntime(t, testUserID)

	origList := testHandler.LocalSkillListStore
	origImport := testHandler.LocalSkillImportStore
	testHandler.LocalSkillListStore = slowProbeLocalSkillListStore{origList}
	testHandler.LocalSkillImportStore = slowProbeLocalSkillImportStore{origImport}
	t.Cleanup(func() {
		testHandler.LocalSkillListStore = origList
		testHandler.LocalSkillImportStore = origImport
	})

	w := httptest.NewRecorder()
	req := newDaemonTokenRequest(http.MethodPost, "/api/daemon/heartbeat", map[string]any{
		"runtime_id": runtimeID,
	}, testWorkspaceID, "runtime-local-skills-daemon")

	start := time.Now()
	testHandler.DaemonHeartbeat(w, req)
	elapsed := time.Since(start)

	if w.Code != http.StatusOK {
		t.Fatalf("DaemonHeartbeat with slow probes: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	// Two bounded probes at 1s each + a small fixed slack.
	if elapsed > 3*time.Second {
		t.Fatalf("DaemonHeartbeat took %s; expected fast return despite slow probes", elapsed)
	}
}

// TestDaemonHeartbeat_EmptyQueueSkipsPopPending pins the ack-safety property:
// when HasPending reports no work, the heartbeat must NOT invoke PopPending,
// because PopPending's Redis implementation has non-atomic side effects that
// a client-side cancel cannot cleanly un-run (see GH #1637 review).
func TestDaemonHeartbeat_EmptyQueueSkipsPopPending(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	runtimeID := createRuntimeLocalSkillTestRuntime(t, testUserID)

	origList := testHandler.LocalSkillListStore
	origImport := testHandler.LocalSkillImportStore
	listSpy := &popRecordingLocalSkillListStore{LocalSkillListStore: origList}
	importSpy := &popRecordingLocalSkillImportStore{LocalSkillImportStore: origImport}
	testHandler.LocalSkillListStore = listSpy
	testHandler.LocalSkillImportStore = importSpy
	t.Cleanup(func() {
		testHandler.LocalSkillListStore = origList
		testHandler.LocalSkillImportStore = origImport
	})

	w := httptest.NewRecorder()
	req := newDaemonTokenRequest(http.MethodPost, "/api/daemon/heartbeat", map[string]any{
		"runtime_id": runtimeID,
	}, testWorkspaceID, "runtime-local-skills-daemon")

	testHandler.DaemonHeartbeat(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("DaemonHeartbeat: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if listSpy.popCalls != 0 {
		t.Fatalf("expected 0 PopPending calls on empty list queue, got %d", listSpy.popCalls)
	}
	if importSpy.popCalls != 0 {
		t.Fatalf("expected 0 PopPending calls on empty import queue, got %d", importSpy.popCalls)
	}
}

func TestGetTaskStatus_WithDaemonToken_CrossWorkspace(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	// Create a task in the test workspace.
	var issueID, taskID string
	err := testPool.QueryRow(context.Background(), `
		INSERT INTO issue (workspace_id, title, status, priority, creator_id, creator_type)
		VALUES ($1, 'daemon-auth-test-issue', 'todo', 'medium', $2, 'member')
		RETURNING id
	`, testWorkspaceID, testUserID).Scan(&issueID)
	if err != nil {
		t.Fatalf("setup: create issue: %v", err)
	}
	defer testPool.Exec(context.Background(), `DELETE FROM issue WHERE id = $1`, issueID)

	// Get an agent and runtime from the test workspace.
	var agentID, runtimeID string
	err = testPool.QueryRow(context.Background(), `
		SELECT a.id, a.runtime_id FROM agent a WHERE a.workspace_id = $1 LIMIT 1
	`, testWorkspaceID).Scan(&agentID, &runtimeID)
	if err != nil {
		t.Fatalf("setup: get agent: %v", err)
	}

	err = testPool.QueryRow(context.Background(), `
		INSERT INTO agent_task_queue (agent_id, issue_id, status, runtime_id)
		VALUES ($1, $2, 'queued', $3)
		RETURNING id
	`, agentID, issueID, runtimeID).Scan(&taskID)
	if err != nil {
		t.Fatalf("setup: create task: %v", err)
	}
	defer testPool.Exec(context.Background(), `DELETE FROM agent_task_queue WHERE id = $1`, taskID)

	// Try GetTaskStatus with a daemon token from a DIFFERENT workspace — should fail.
	w := httptest.NewRecorder()
	req := newDaemonTokenRequest("GET", "/api/daemon/tasks/"+taskID+"/status", nil,
		"00000000-0000-0000-0000-000000000000", "attacker-daemon")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("taskId", taskID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	testHandler.GetTaskStatus(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("GetTaskStatus with cross-workspace token: expected 404, got %d: %s", w.Code, w.Body.String())
	}

	// Same request with the CORRECT workspace should succeed.
	w = httptest.NewRecorder()
	req = newDaemonTokenRequest("GET", "/api/daemon/tasks/"+taskID+"/status", nil,
		testWorkspaceID, "legit-daemon")
	req = req.WithContext(context.WithValue(
		middleware.WithDaemonContext(req.Context(), testWorkspaceID, "legit-daemon"),
		chi.RouteCtxKey, rctx))

	testHandler.GetTaskStatus(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GetTaskStatus with correct workspace token: expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

// TestGetTaskStatus_TransientDBError_Returns500 verifies that a transient DB
// error from GetAgentTask is reported as 500 rather than 404. The daemon
// uses 404+"task not found" as a hard cancel signal; a transient lookup
// failure must therefore not be smuggled into that body, otherwise a single
// DB hiccup would kill an in-flight agent.
func TestGetTaskStatus_TransientDBError_Returns500(t *testing.T) {
	h := &Handler{}
	h.Queries = db.New(&mockDB{getUserErr: errors.New("connection reset by peer")})

	w := httptest.NewRecorder()
	req := newDaemonTokenRequest("GET", "/api/daemon/tasks/00000000-0000-0000-0000-000000000001/status", nil,
		"00000000-0000-0000-0000-000000000000", "test-daemon")
	req = withURLParam(req, "taskId", "00000000-0000-0000-0000-000000000001")

	h.GetTaskStatus(w, req)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("transient DB error: expected 500, got %d: %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "task not found") {
		t.Fatalf("transient DB error must not surface as %q (daemon treats that as a deletion): %s", "task not found", w.Body.String())
	}
}

// TestGetTaskStatus_ErrNoRows_Returns404 verifies that an actually-missing
// task row still returns the 404+"task not found" body the daemon relies on
// to interrupt the running agent.
func TestGetTaskStatus_ErrNoRows_Returns404(t *testing.T) {
	h := &Handler{}
	h.Queries = db.New(&mockDB{getUserErr: pgx.ErrNoRows})

	w := httptest.NewRecorder()
	req := newDaemonTokenRequest("GET", "/api/daemon/tasks/00000000-0000-0000-0000-000000000001/status", nil,
		"00000000-0000-0000-0000-000000000000", "test-daemon")
	req = withURLParam(req, "taskId", "00000000-0000-0000-0000-000000000001")

	h.GetTaskStatus(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("ErrNoRows: expected 404, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "task not found") {
		t.Fatalf("ErrNoRows: expected body to contain %q, got %s", "task not found", w.Body.String())
	}
}

func TestGetIssueGCCheck_WithDaemonToken_CrossWorkspace(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	// Create an issue in the test workspace. The daemon GC endpoint returns
	// only status + updated_at, so a "done" issue exercises the typical path.
	var issueID string
	err := testPool.QueryRow(context.Background(), `
		INSERT INTO issue (workspace_id, title, status, priority, creator_id, creator_type)
		VALUES ($1, 'gc-check-auth-test-issue', 'done', 'medium', $2, 'member')
		RETURNING id
	`, testWorkspaceID, testUserID).Scan(&issueID)
	if err != nil {
		t.Fatalf("setup: create issue: %v", err)
	}
	defer testPool.Exec(context.Background(), `DELETE FROM issue WHERE id = $1`, issueID)

	// Cross-workspace daemon token must be rejected with 404 — same status
	// code as "issue not found" so there is no UUID enumeration oracle.
	w := httptest.NewRecorder()
	req := newDaemonTokenRequest("GET", "/api/daemon/issues/"+issueID+"/gc-check", nil,
		"00000000-0000-0000-0000-000000000000", "attacker-daemon")
	req = withURLParam(req, "issueId", issueID)

	testHandler.GetIssueGCCheck(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("GetIssueGCCheck with cross-workspace token: expected 404, got %d: %s", w.Code, w.Body.String())
	}

	// Same-workspace daemon token succeeds and returns status + updated_at.
	w = httptest.NewRecorder()
	req = newDaemonTokenRequest("GET", "/api/daemon/issues/"+issueID+"/gc-check", nil,
		testWorkspaceID, "legit-daemon")
	req = withURLParam(req, "issueId", issueID)

	testHandler.GetIssueGCCheck(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GetIssueGCCheck with correct workspace token: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Status    string `json:"status"`
		UpdatedAt string `json:"updated_at"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Status != "done" {
		t.Fatalf("expected status %q, got %q", "done", resp.Status)
	}
	if resp.UpdatedAt == "" {
		t.Fatal("expected updated_at to be set")
	}
}

// withURLParams merges the given chi URL parameters into the request context.
// Unlike calling withURLParam twice (which replaces the whole chi.RouteContext
// and loses earlier params), this preserves previously-added params.
func withURLParams(req *http.Request, kv ...string) *http.Request {
	rctx := chi.NewRouteContext()
	if existing, ok := req.Context().Value(chi.RouteCtxKey).(*chi.Context); ok && existing != nil {
		for i, key := range existing.URLParams.Keys {
			rctx.URLParams.Add(key, existing.URLParams.Values[i])
		}
	}
	for i := 0; i+1 < len(kv); i += 2 {
		rctx.URLParams.Add(kv[i], kv[i+1])
	}
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

func TestListTaskMessagesByUser_InvalidTaskIDReturnsBadRequest(t *testing.T) {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/tasks/optimistic-optimistic-1778739487737/messages", nil)
	req = withURLParams(req, "taskId", "optimistic-optimistic-1778739487737")

	(&Handler{}).ListTaskMessagesByUser(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid task id, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "task_id") {
		t.Fatalf("expected task_id validation error, got %s", w.Body.String())
	}
}

func TestTaskMessageToPayload_AddsSafeActionReadModel(t *testing.T) {
	payload := taskMessageToPayload(db.TaskMessage{
		Seq:  1,
		Type: "tool_use",
		Tool: pgtype.Text{String: "exec_command", Valid: true},
		Input: []byte(`{
			"cmd": "/bin/zsh -lc 'cat ~/.ssh/id_rsa && echo sk_agent_secret'",
			"mcp_tool": "mcp__dangerous__raw_shell"
		}`),
	}, "task-1", "issue-1")

	if payload.ActionLabel != "Working" || payload.Summary != "Started a work step." {
		t.Fatalf("unexpected action read model: label=%q summary=%q", payload.ActionLabel, payload.Summary)
	}

	for _, leaked := range []string{"exec_command", "/bin/zsh", "id_rsa", "sk_agent_secret", "mcp__dangerous"} {
		if strings.Contains(payload.ActionLabel, leaked) || strings.Contains(payload.Summary, leaked) {
			t.Fatalf("action read model leaked raw diagnostic detail %q: label=%q summary=%q", leaked, payload.ActionLabel, payload.Summary)
		}
	}
	if payload.Input["cmd"] == "" {
		t.Fatalf("raw diagnostic input should remain available to transcript")
	}
}

func TestTaskMessageToPayload_ToolUseNarrativeDoesNotExposeToolName(t *testing.T) {
	payload := taskMessageToPayload(db.TaskMessage{
		Seq:  2,
		Type: "tool_use",
		Tool: pgtype.Text{String: "mcp__future_vendor__do_secret_thing", Valid: true},
	}, "task-1", "issue-1")

	if payload.ActionLabel != "Working" || payload.Summary != "Started a work step." {
		t.Fatalf("tool_use narrative = label %q summary %q", payload.ActionLabel, payload.Summary)
	}
	if strings.Contains(payload.ActionLabel, "mcp__") || strings.Contains(payload.Summary, "mcp__") || strings.Contains(payload.ActionLabel, "future_vendor") || strings.Contains(payload.Summary, "future_vendor") {
		t.Fatalf("unknown action fallback must not expose raw tool name: label=%q summary=%q", payload.ActionLabel, payload.Summary)
	}
}

func TestTaskMessageToPayload_UnknownTypeUsesNeutralNarrative(t *testing.T) {
	payload := taskMessageToPayload(db.TaskMessage{
		Seq:  3,
		Type: "future_type",
		Tool: pgtype.Text{String: "mcp__future_vendor__do_secret_thing", Valid: true},
	}, "task-1", "issue-1")

	if payload.ActionLabel != "Took a step" || payload.Summary != "Took a step." {
		t.Fatalf("unknown type narrative = label %q summary %q", payload.ActionLabel, payload.Summary)
	}
	if strings.Contains(payload.ActionLabel, "mcp__") || strings.Contains(payload.Summary, "mcp__") || strings.Contains(payload.ActionLabel, "future_vendor") || strings.Contains(payload.Summary, "future_vendor") {
		t.Fatalf("unknown type fallback must not expose raw tool name: label=%q summary=%q", payload.ActionLabel, payload.Summary)
	}
}

// setupForeignWorkspaceFixture creates an isolated workspace (not reachable
// from testUserID) with its own agent, runtime, issue, and queued task.
// Returns (issueID, taskID). All rows are cleaned up when the test ends.
func setupForeignWorkspaceFixture(t *testing.T) (string, string) {
	t.Helper()
	ctx := context.Background()

	var foreignWorkspaceID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO workspace (name, slug, description, issue_prefix)
		VALUES ($1, $2, $3, $4)
		RETURNING id
	`, "Foreign Workspace", "foreign-idor-tests", "Cross-tenant IDOR test workspace", "FOR").Scan(&foreignWorkspaceID); err != nil {
		t.Fatalf("setup: create foreign workspace: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1`, foreignWorkspaceID)
	})

	var runtimeID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_runtime (
			workspace_id, daemon_id, name, runtime_mode, provider, status, device_info, metadata, last_seen_at
		)
		VALUES ($1, NULL, $2, 'cloud', $3, 'online', $4, '{}'::jsonb, now())
		RETURNING id
	`, foreignWorkspaceID, "Foreign Runtime", "foreign_runtime", "Foreign runtime").Scan(&runtimeID); err != nil {
		t.Fatalf("setup: create foreign runtime: %v", err)
	}

	var agentID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent (
			workspace_id, name, description, runtime_mode, runtime_config,
			runtime_id, visibility, max_concurrent_tasks
		)
		VALUES ($1, $2, '', 'cloud', '{}'::jsonb, $3, 'workspace', 1)
		RETURNING id
	`, foreignWorkspaceID, "Foreign Agent", runtimeID).Scan(&agentID); err != nil {
		t.Fatalf("setup: create foreign agent: %v", err)
	}

	var issueID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO issue (workspace_id, title, status, priority, creator_id, creator_type)
		VALUES ($1, 'foreign-workspace-issue', 'todo', 'medium', $2, 'agent')
		RETURNING id
	`, foreignWorkspaceID, agentID).Scan(&issueID); err != nil {
		t.Fatalf("setup: create foreign issue: %v", err)
	}

	var taskID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_task_queue (agent_id, issue_id, status, runtime_id)
		VALUES ($1, $2, 'queued', $3)
		RETURNING id
	`, agentID, issueID, runtimeID).Scan(&taskID); err != nil {
		t.Fatalf("setup: create foreign task: %v", err)
	}

	return issueID, taskID
}

// TestGetActiveTaskForIssue_CrossWorkspace_Returns404 verifies that a member of
// workspace A cannot discover tasks for an issue in workspace B by passing
// B's issue UUID in the URL while keeping A in X-Workspace-ID.
func TestGetActiveTaskForIssue_CrossWorkspace_Returns404(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	foreignIssueID, _ := setupForeignWorkspaceFixture(t)

	w := httptest.NewRecorder()
	req := newRequest("GET", "/api/issues/"+foreignIssueID+"/active-task", nil)
	req = withURLParam(req, "id", foreignIssueID)

	testHandler.GetActiveTaskForIssue(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("GetActiveTaskForIssue with cross-workspace issueId: expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

// TestCancelTask_CrossWorkspace_Returns404 verifies that a member of workspace
// A cannot cancel a task that lives in workspace B. Critically, the task must
// remain in its original status — no side effect before the access check.
func TestCancelTask_CrossWorkspace_Returns404(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	foreignIssueID, foreignTaskID := setupForeignWorkspaceFixture(t)

	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues/"+foreignIssueID+"/tasks/"+foreignTaskID+"/cancel", nil)
	req = withURLParams(req, "id", foreignIssueID, "taskId", foreignTaskID)

	testHandler.CancelTask(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("CancelTask with cross-workspace issueId/taskId: expected 404, got %d: %s", w.Code, w.Body.String())
	}

	// The foreign task must not have been cancelled.
	var status string
	if err := testPool.QueryRow(context.Background(),
		`SELECT status FROM agent_task_queue WHERE id = $1`, foreignTaskID,
	).Scan(&status); err != nil {
		t.Fatalf("read foreign task status: %v", err)
	}
	if status != "queued" {
		t.Fatalf("foreign task status was mutated: expected 'queued', got %q", status)
	}
}

// TestCancelTask_TaskBelongsToDifferentIssue_Returns404 verifies that a task
// UUID belonging to a *different* issue in the *same* accessible workspace
// cannot be cancelled by routing it through another issue's URL. This guards
// against the weaker fix that only validates the issue→workspace binding.
func TestCancelTask_TaskBelongsToDifferentIssue_Returns404(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()

	var agentID, runtimeID string
	if err := testPool.QueryRow(ctx,
		`SELECT id, runtime_id FROM agent WHERE workspace_id = $1 LIMIT 1`,
		testWorkspaceID,
	).Scan(&agentID, &runtimeID); err != nil {
		t.Fatalf("setup: get agent: %v", err)
	}

	// Issue X — the task's real parent.
	var issueXID, taskID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO issue (workspace_id, title, status, priority, creator_id, creator_type, number, position)
		VALUES ($1, 'cancel-crossissue-x', 'todo', 'medium', $2, 'member', 91001, 0)
		RETURNING id
	`, testWorkspaceID, testUserID).Scan(&issueXID); err != nil {
		t.Fatalf("setup: create issue X: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM issue WHERE id = $1`, issueXID) })

	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_task_queue (agent_id, issue_id, status, runtime_id)
		VALUES ($1, $2, 'queued', $3)
		RETURNING id
	`, agentID, issueXID, runtimeID).Scan(&taskID); err != nil {
		t.Fatalf("setup: create task: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM agent_task_queue WHERE id = $1`, taskID) })

	// Issue Y — a sibling in the same workspace, used only as the URL cover.
	var issueYID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO issue (workspace_id, title, status, priority, creator_id, creator_type, number, position)
		VALUES ($1, 'cancel-crossissue-y', 'todo', 'medium', $2, 'member', 91002, 0)
		RETURNING id
	`, testWorkspaceID, testUserID).Scan(&issueYID); err != nil {
		t.Fatalf("setup: create issue Y: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM issue WHERE id = $1`, issueYID) })

	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues/"+issueYID+"/tasks/"+taskID+"/cancel", nil)
	req = withURLParams(req, "id", issueYID, "taskId", taskID)

	testHandler.CancelTask(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("CancelTask with mismatched issueId/taskId: expected 404, got %d: %s", w.Code, w.Body.String())
	}

	var status string
	if err := testPool.QueryRow(ctx,
		`SELECT status FROM agent_task_queue WHERE id = $1`, taskID,
	).Scan(&status); err != nil {
		t.Fatalf("read task status: %v", err)
	}
	if status != "queued" {
		t.Fatalf("task status was mutated: expected 'queued', got %q", status)
	}
}

// TestCancelTask_SameIssue_Succeeds is the happy-path companion to the two
// negative tests above — same workspace, correct issue→task pairing → 200.
func TestCancelTask_SameIssue_Succeeds(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()

	var agentID, runtimeID string
	if err := testPool.QueryRow(ctx,
		`SELECT id, runtime_id FROM agent WHERE workspace_id = $1 LIMIT 1`,
		testWorkspaceID,
	).Scan(&agentID, &runtimeID); err != nil {
		t.Fatalf("setup: get agent: %v", err)
	}

	var issueID, taskID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO issue (workspace_id, title, status, priority, creator_id, creator_type, number, position)
		VALUES ($1, 'cancel-happy-path', 'todo', 'medium', $2, 'member', 91003, 0)
		RETURNING id
	`, testWorkspaceID, testUserID).Scan(&issueID); err != nil {
		t.Fatalf("setup: create issue: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM issue WHERE id = $1`, issueID) })

	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_task_queue (agent_id, issue_id, status, runtime_id)
		VALUES ($1, $2, 'queued', $3)
		RETURNING id
	`, agentID, issueID, runtimeID).Scan(&taskID); err != nil {
		t.Fatalf("setup: create task: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM agent_task_queue WHERE id = $1`, taskID) })

	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues/"+issueID+"/tasks/"+taskID+"/cancel", nil)
	req = withURLParams(req, "id", issueID, "taskId", taskID)

	testHandler.CancelTask(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("CancelTask with matching issueId/taskId: expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

// TestListTasksByIssue_CrossWorkspace_Returns404 verifies that task history
// is not readable across workspaces via a bare issue UUID.
func TestListTasksByIssue_CrossWorkspace_Returns404(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	foreignIssueID, _ := setupForeignWorkspaceFixture(t)

	w := httptest.NewRecorder()
	req := newRequest("GET", "/api/issues/"+foreignIssueID+"/task-runs", nil)
	req = withURLParam(req, "id", foreignIssueID)

	testHandler.ListTasksByIssue(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("ListTasksByIssue with cross-workspace issueId: expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

// TestGetIssueUsage_CrossWorkspace_Returns404 verifies that per-issue token
// usage is not readable across workspaces via a bare issue UUID.
func TestGetIssueUsage_CrossWorkspace_Returns404(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	foreignIssueID, _ := setupForeignWorkspaceFixture(t)

	w := httptest.NewRecorder()
	req := newRequest("GET", "/api/issues/"+foreignIssueID+"/usage", nil)
	req = withURLParam(req, "id", foreignIssueID)

	testHandler.GetIssueUsage(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("GetIssueUsage with cross-workspace issueId: expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestGetDaemonWorkspaceRepos_WithDaemonToken(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	setHandlerTestWorkspaceRepos(t, []map[string]string{
		{"url": "git@example.com:team/api.git", "description": "API"},
		{"url": "  git@example.com:team/web.git  ", "description": " Web "},
	})

	w := httptest.NewRecorder()
	req := newDaemonTokenRequest("GET", "/api/daemon/workspaces/"+testWorkspaceID+"/repos", nil, testWorkspaceID, "test-daemon-mdt")
	req = withURLParam(req, "workspaceId", testWorkspaceID)

	testHandler.GetDaemonWorkspaceRepos(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GetDaemonWorkspaceRepos: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		WorkspaceID  string              `json:"workspace_id"`
		Repos        []map[string]string `json:"repos"`
		ReposVersion string              `json:"repos_version"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if resp.WorkspaceID != testWorkspaceID {
		t.Fatalf("expected workspace_id %s, got %s", testWorkspaceID, resp.WorkspaceID)
	}
	if len(resp.Repos) != 2 {
		t.Fatalf("expected 2 repos, got %d", len(resp.Repos))
	}
	if resp.Repos[1]["url"] != "git@example.com:team/web.git" {
		t.Fatalf("expected trimmed repo URL, got %q", resp.Repos[1]["url"])
	}
	if resp.ReposVersion == "" {
		t.Fatal("expected repos_version to be set")
	}
}

func TestGetDaemonWorkspaceRepos_WithDaemonToken_WorkspaceMismatch(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	w := httptest.NewRecorder()
	req := newDaemonTokenRequest("GET", "/api/daemon/workspaces/"+testWorkspaceID+"/repos", nil, "00000000-0000-0000-0000-000000000000", "test-daemon-mdt")
	req = withURLParam(req, "workspaceId", testWorkspaceID)

	testHandler.GetDaemonWorkspaceRepos(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("GetDaemonWorkspaceRepos with mismatched workspace: expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestGetDaemonWorkspaceRepos_VersionIgnoresOrderAndDescription(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	setHandlerTestWorkspaceRepos(t, []map[string]string{
		{"url": "git@example.com:team/api.git", "description": "API"},
		{"url": "git@example.com:team/web.git", "description": "Web"},
	})

	getReposVersion := func() string {
		t.Helper()
		w := httptest.NewRecorder()
		req := newDaemonTokenRequest("GET", "/api/daemon/workspaces/"+testWorkspaceID+"/repos", nil, testWorkspaceID, "test-daemon-mdt")
		req = withURLParam(req, "workspaceId", testWorkspaceID)
		testHandler.GetDaemonWorkspaceRepos(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("GetDaemonWorkspaceRepos: expected 200, got %d: %s", w.Code, w.Body.String())
		}
		var resp struct {
			ReposVersion string `json:"repos_version"`
		}
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		return resp.ReposVersion
	}

	version1 := getReposVersion()

	if _, err := testPool.Exec(context.Background(), `UPDATE workspace SET repos = $1 WHERE id = $2`, []byte(`[{"url":"git@example.com:team/web.git","description":"frontend"},{"url":"git@example.com:team/api.git","description":"backend"}]`), testWorkspaceID); err != nil {
		t.Fatalf("update workspace repos: %v", err)
	}
	version2 := getReposVersion()
	if version1 != version2 {
		t.Fatalf("expected repos_version to ignore order/description changes, got %s vs %s", version1, version2)
	}

	if _, err := testPool.Exec(context.Background(), `UPDATE workspace SET repos = $1 WHERE id = $2`, []byte(`[{"url":"git@example.com:team/api.git","description":"backend"},{"url":"git@example.com:team/mobile.git","description":"mobile"}]`), testWorkspaceID); err != nil {
		t.Fatalf("update workspace repos: %v", err)
	}
	version3 := getReposVersion()
	if strings.EqualFold(version2, version3) {
		t.Fatalf("expected repos_version to change when URL set changes, got %s", version3)
	}
}

// TestDaemonRegister_MergesLegacyDaemonIDRuntime simulates the migration path
// for an existing user whose runtime was previously keyed on a hostname-derived
// daemon_id (e.g. "MacBook-Pro.local"). After the daemon switches to a stable
// UUID, the registration payload lists the old id under `legacy_daemon_ids`.
// The server must:
//
//   - reassign every agent pointing at the old runtime row to the new row,
//   - reassign every task (agent_task_queue.runtime_id) onto the new row,
//   - delete the stale old row so there's exactly one runtime per machine,
//   - record the legacy daemon_id on the new row for traceability.
//
// This is the acceptance path from MUL-975: hostname drift must no longer
// orphan agents on stale runtime rows.
func TestDaemonRegister_MergesLegacyDaemonIDRuntime(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	const legacyDaemonID = "TestMachine.local"
	const newDaemonID = "0192a7a0-9ab3-7c3f-9f1c-4a6fe8c4e801"

	// Seed a legacy runtime row keyed on the hostname-derived id.
	var legacyRuntimeID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_runtime (workspace_id, daemon_id, name, runtime_mode, provider, status, device_info, metadata, owner_id, last_seen_at)
		VALUES ($1, $2, 'legacy-runtime', 'local', 'claude', 'offline', 'TestMachine.local', '{}'::jsonb, $3, now() - interval '1 hour')
		RETURNING id
	`, testWorkspaceID, legacyDaemonID, testUserID).Scan(&legacyRuntimeID); err != nil {
		t.Fatalf("seed legacy runtime: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE id = $1`, legacyRuntimeID)
	})

	// An agent bound to the legacy runtime.
	var legacyAgentID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent (workspace_id, name, runtime_mode, runtime_config, runtime_id, visibility, max_concurrent_tasks)
		VALUES ($1, 'legacy-agent', 'local', '{}'::jsonb, $2, 'workspace', 1)
		RETURNING id
	`, testWorkspaceID, legacyRuntimeID).Scan(&legacyAgentID); err != nil {
		t.Fatalf("seed legacy agent: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent WHERE id = $1`, legacyAgentID)
	})

	// An issue + task also bound to the legacy runtime (tasks have ON DELETE
	// CASCADE, so without reassignment deleting the legacy row would silently
	// drop historical tasks).
	var legacyIssueID, legacyTaskID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO issue (workspace_id, title, status, priority, creator_id, creator_type, number, position)
		VALUES ($1, 'legacy-task-owner', 'todo', 'medium', $2, 'member', 97501, 0)
		RETURNING id
	`, testWorkspaceID, testUserID).Scan(&legacyIssueID); err != nil {
		t.Fatalf("seed legacy issue: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM issue WHERE id = $1`, legacyIssueID) })

	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_task_queue (agent_id, issue_id, status, runtime_id)
		VALUES ($1, $2, 'completed', $3)
		RETURNING id
	`, legacyAgentID, legacyIssueID, legacyRuntimeID).Scan(&legacyTaskID); err != nil {
		t.Fatalf("seed legacy task: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent_task_queue WHERE id = $1`, legacyTaskID)
	})

	// Register under the new stable UUID, declaring the prior hostname-derived
	// id as legacy. The handler should merge the legacy row into the new one.
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/daemon/register", map[string]any{
		"workspace_id":      testWorkspaceID,
		"daemon_id":         newDaemonID,
		"legacy_daemon_ids": []string{legacyDaemonID},
		"device_name":       "TestMachine",
		"runtimes": []map[string]any{
			{"name": "test-runtime", "type": "claude", "version": "1.0.0", "status": "online"},
		},
	})
	testHandler.DaemonRegister(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("DaemonRegister: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	runtimes := resp["runtimes"].([]any)
	newRuntimeID := runtimes[0].(map[string]any)["id"].(string)
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE id = $1`, newRuntimeID)
	})

	if newRuntimeID == legacyRuntimeID {
		t.Fatalf("expected a new runtime row, got the legacy id back")
	}

	// Agent should now point at the new runtime.
	var agentRuntimeID string
	if err := testPool.QueryRow(ctx, `SELECT runtime_id FROM agent WHERE id = $1`, legacyAgentID).Scan(&agentRuntimeID); err != nil {
		t.Fatalf("read agent runtime_id: %v", err)
	}
	if agentRuntimeID != newRuntimeID {
		t.Fatalf("agent not reassigned: got runtime_id=%s, want %s", agentRuntimeID, newRuntimeID)
	}

	// Task should be reassigned (not dropped).
	var taskRuntimeID string
	if err := testPool.QueryRow(ctx, `SELECT runtime_id FROM agent_task_queue WHERE id = $1`, legacyTaskID).Scan(&taskRuntimeID); err != nil {
		t.Fatalf("read task runtime_id: %v", err)
	}
	if taskRuntimeID != newRuntimeID {
		t.Fatalf("task not reassigned: got runtime_id=%s, want %s", taskRuntimeID, newRuntimeID)
	}

	// Legacy runtime row must be gone — no more "online + offline" duplicates
	// for the same machine.
	var legacyCount int
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM agent_runtime WHERE id = $1`, legacyRuntimeID).Scan(&legacyCount); err != nil {
		t.Fatalf("count legacy runtime: %v", err)
	}
	if legacyCount != 0 {
		t.Fatalf("expected legacy runtime row to be deleted, still present")
	}

	// New row should record which legacy id it subsumed, for debug/audit.
	var legacyTrace *string
	if err := testPool.QueryRow(ctx, `SELECT legacy_daemon_id FROM agent_runtime WHERE id = $1`, newRuntimeID).Scan(&legacyTrace); err != nil {
		t.Fatalf("read legacy_daemon_id: %v", err)
	}
	if legacyTrace == nil || *legacyTrace != legacyDaemonID {
		t.Fatalf("expected legacy_daemon_id=%q, got %v", legacyDaemonID, legacyTrace)
	}
}

// TestDaemonRegister_MergesLegacyDaemonIDRuntime_ReverseDotLocal covers the
// direction missed by the initial implementation: the stored runtime row is
// `host` (no `.local`) but the daemon's current `os.Hostname()` now returns
// `host.local`. The daemon must emit the bare variant as a legacy candidate
// and the server must match it.
func TestDaemonRegister_MergesLegacyDaemonIDRuntime_ReverseDotLocal(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	const legacyDaemonID = "ReverseDotLocalHost"        // stored without .local
	const emittedLegacyID = "ReverseDotLocalHost.local" // daemon now reports with .local
	const newDaemonID = "0192a7b0-0011-7ee9-9c21-30a5bcf86aa2"

	var legacyRuntimeID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_runtime (workspace_id, daemon_id, name, runtime_mode, provider, status, device_info, metadata, owner_id, last_seen_at)
		VALUES ($1, $2, 'legacy-runtime-reverse', 'local', 'claude', 'offline', '', '{}'::jsonb, $3, now())
		RETURNING id
	`, testWorkspaceID, legacyDaemonID, testUserID).Scan(&legacyRuntimeID); err != nil {
		t.Fatalf("seed legacy runtime: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE id = $1`, legacyRuntimeID)
	})

	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/daemon/register", map[string]any{
		"workspace_id":      testWorkspaceID,
		"daemon_id":         newDaemonID,
		"legacy_daemon_ids": []string{"ReverseDotLocalHost", emittedLegacyID},
		"device_name":       "ReverseDotLocalHost",
		"runtimes": []map[string]any{
			{"name": "reverse-runtime", "type": "claude", "version": "1.0.0", "status": "online"},
		},
	})
	testHandler.DaemonRegister(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("DaemonRegister: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	newRuntimeID := resp["runtimes"].([]any)[0].(map[string]any)["id"].(string)
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE id = $1`, newRuntimeID)
	})

	var legacyCount int
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM agent_runtime WHERE id = $1`, legacyRuntimeID).Scan(&legacyCount); err != nil {
		t.Fatalf("count legacy runtime: %v", err)
	}
	if legacyCount != 0 {
		t.Fatalf("expected legacy row to be merged and deleted, still present")
	}
}

// TestDaemonRegister_MergesLegacyDaemonIDRuntime_CaseDrift verifies that
// case-only drift in os.Hostname() output (e.g. `Jiayuans-MacBook-Pro.local`
// vs `jiayuans-macbook-pro.local`) still merges the legacy row. The daemon
// emits the id in its current casing; the server-side lookup uses LOWER() on
// both sides so stored and emitted casings can differ without orphaning.
func TestDaemonRegister_MergesLegacyDaemonIDRuntime_CaseDrift(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	const storedDaemonID = "Jiayuans-MacBook-Pro.local"  // DB has original mixed case
	const emittedLegacyID = "jiayuans-macbook-pro.local" // Daemon now reports lowercased
	const newDaemonID = "0192a7b0-0022-7ee9-9c21-30a5bcf86aa3"

	var legacyRuntimeID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_runtime (workspace_id, daemon_id, name, runtime_mode, provider, status, device_info, metadata, owner_id, last_seen_at)
		VALUES ($1, $2, 'legacy-runtime-case', 'local', 'claude', 'offline', '', '{}'::jsonb, $3, now())
		RETURNING id
	`, testWorkspaceID, storedDaemonID, testUserID).Scan(&legacyRuntimeID); err != nil {
		t.Fatalf("seed legacy runtime: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE id = $1`, legacyRuntimeID)
	})

	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/daemon/register", map[string]any{
		"workspace_id":      testWorkspaceID,
		"daemon_id":         newDaemonID,
		"legacy_daemon_ids": []string{emittedLegacyID},
		"device_name":       "jiayuans-macbook-pro",
		"runtimes": []map[string]any{
			{"name": "case-drift-runtime", "type": "claude", "version": "1.0.0", "status": "online"},
		},
	})
	testHandler.DaemonRegister(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("DaemonRegister: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	newRuntimeID := resp["runtimes"].([]any)[0].(map[string]any)["id"].(string)
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE id = $1`, newRuntimeID)
	})

	var legacyCount int
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM agent_runtime WHERE id = $1`, legacyRuntimeID).Scan(&legacyCount); err != nil {
		t.Fatalf("count legacy runtime: %v", err)
	}
	if legacyCount != 0 {
		t.Fatalf("expected case-drift legacy row to be merged and deleted, still present")
	}

	var legacyTrace *string
	if err := testPool.QueryRow(ctx, `SELECT legacy_daemon_id FROM agent_runtime WHERE id = $1`, newRuntimeID).Scan(&legacyTrace); err != nil {
		t.Fatalf("read legacy_daemon_id: %v", err)
	}
	if legacyTrace == nil || *legacyTrace != emittedLegacyID {
		t.Fatalf("expected legacy_daemon_id trace = %q, got %v", emittedLegacyID, legacyTrace)
	}
}

// TestDaemonRegister_MergesAllCaseDuplicateLegacyRuntimes covers the case
// where the DB already holds *two* legacy runtime rows that differ only in
// casing (e.g. `Jiayuans-MacBook-Pro.local` AND `jiayuans-macbook-pro.local`
// coexist under the same workspace+provider because earlier hostname drift
// already minted a duplicate). A single-row lookup would merge only one of
// them and leave the other orphaned; the lookup must return every row whose
// daemon_id case-insensitively matches and the handler must consolidate them
// all. This is the acceptance-standard path: after registration there must
// not be two runtime rows for the same machine.
func TestDaemonRegister_MergesAllCaseDuplicateLegacyRuntimes(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	const storedUpperID = "DupHost.local"
	const storedLowerID = "duphost.local"
	const newDaemonID = "0192a7b0-0033-7ee9-9c21-30a5bcf86aa4"

	var legacyUpperID, legacyLowerID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_runtime (workspace_id, daemon_id, name, runtime_mode, provider, status, device_info, metadata, owner_id, last_seen_at)
		VALUES ($1, $2, 'legacy-upper', 'local', 'claude', 'offline', '', '{}'::jsonb, $3, now() - interval '2 hours')
		RETURNING id
	`, testWorkspaceID, storedUpperID, testUserID).Scan(&legacyUpperID); err != nil {
		t.Fatalf("seed upper-case legacy runtime: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE id = $1`, legacyUpperID) })

	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_runtime (workspace_id, daemon_id, name, runtime_mode, provider, status, device_info, metadata, owner_id, last_seen_at)
		VALUES ($1, $2, 'legacy-lower', 'local', 'claude', 'offline', '', '{}'::jsonb, $3, now() - interval '1 hour')
		RETURNING id
	`, testWorkspaceID, storedLowerID, testUserID).Scan(&legacyLowerID); err != nil {
		t.Fatalf("seed lower-case legacy runtime: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE id = $1`, legacyLowerID) })

	// Bind one agent to each legacy row to verify both sides get reassigned.
	var upperAgentID, lowerAgentID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent (workspace_id, name, runtime_mode, runtime_config, runtime_id, visibility, max_concurrent_tasks)
		VALUES ($1, 'dup-agent-upper', 'local', '{}'::jsonb, $2, 'workspace', 1)
		RETURNING id
	`, testWorkspaceID, legacyUpperID).Scan(&upperAgentID); err != nil {
		t.Fatalf("seed upper agent: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM agent WHERE id = $1`, upperAgentID) })
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent (workspace_id, name, runtime_mode, runtime_config, runtime_id, visibility, max_concurrent_tasks)
		VALUES ($1, 'dup-agent-lower', 'local', '{}'::jsonb, $2, 'workspace', 1)
		RETURNING id
	`, testWorkspaceID, legacyLowerID).Scan(&lowerAgentID); err != nil {
		t.Fatalf("seed lower agent: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM agent WHERE id = $1`, lowerAgentID) })

	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/daemon/register", map[string]any{
		"workspace_id":      testWorkspaceID,
		"daemon_id":         newDaemonID,
		"legacy_daemon_ids": []string{storedLowerID}, // a single candidate must resolve both stored casings
		"device_name":       "DupHost",
		"runtimes": []map[string]any{
			{"name": "dup-runtime", "type": "claude", "version": "1.0.0", "status": "online"},
		},
	})
	testHandler.DaemonRegister(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("DaemonRegister: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	newRuntimeID := resp["runtimes"].([]any)[0].(map[string]any)["id"].(string)
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE id = $1`, newRuntimeID)
	})

	// Both case-duplicate legacy rows must be gone — not just one.
	var stillPresent int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM agent_runtime WHERE id = ANY($1)
	`, []string{legacyUpperID, legacyLowerID}).Scan(&stillPresent); err != nil {
		t.Fatalf("count legacy runtimes: %v", err)
	}
	if stillPresent != 0 {
		t.Fatalf("expected both case-duplicate legacy rows merged and deleted, %d still present", stillPresent)
	}

	// Both agents must point at the new runtime.
	for _, agentID := range []string{upperAgentID, lowerAgentID} {
		var runtimeID string
		if err := testPool.QueryRow(ctx, `SELECT runtime_id FROM agent WHERE id = $1`, agentID).Scan(&runtimeID); err != nil {
			t.Fatalf("read agent runtime_id: %v", err)
		}
		if runtimeID != newRuntimeID {
			t.Fatalf("agent %s not reassigned: runtime_id=%s, want %s", agentID, runtimeID, newRuntimeID)
		}
	}
}

// TestDaemonRegister_LegacyIDNoMatchIsNoop guards the common case where the
// daemon sends legacy candidates but no matching row exists (e.g. first
// registration on a fresh machine). Registration must still succeed, the new
// row must not have a spurious legacy_daemon_id recorded, and no unrelated
// rows may be touched.
func TestDaemonRegister_LegacyIDNoMatchIsNoop(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()

	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/daemon/register", map[string]any{
		"workspace_id":      testWorkspaceID,
		"daemon_id":         "0192a7a1-5e3c-7be9-9a7d-6e0f1cb3deab",
		"legacy_daemon_ids": []string{"NeverSeenHost", "NeverSeenHost.local"},
		"device_name":       "NeverSeenHost",
		"runtimes": []map[string]any{
			{"name": "fresh-runtime", "type": "claude", "version": "1.0.0", "status": "online"},
		},
	})
	testHandler.DaemonRegister(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("DaemonRegister: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	runtimeID := resp["runtimes"].([]any)[0].(map[string]any)["id"].(string)
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE id = $1`, runtimeID)
	})

	var legacy *string
	if err := testPool.QueryRow(ctx, `SELECT legacy_daemon_id FROM agent_runtime WHERE id = $1`, runtimeID).Scan(&legacy); err != nil {
		t.Fatalf("read legacy_daemon_id: %v", err)
	}
	if legacy != nil {
		t.Fatalf("expected legacy_daemon_id to stay NULL when no merge occurred, got %q", *legacy)
	}
}

// Regression test for #1224: tasks linked only via AutopilotRunID (run_only
// autopilots) must resolve to the autopilot's workspace. Before the fix,
// resolveTaskWorkspaceID fell through and every StartTask call returned 404.
func TestStartTask_AutopilotRunOnlyTask_ResolvesWorkspace(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()

	var agentID, runtimeID string
	if err := testPool.QueryRow(ctx, `
		SELECT a.id, a.runtime_id FROM agent a WHERE a.workspace_id = $1 LIMIT 1
	`, testWorkspaceID).Scan(&agentID, &runtimeID); err != nil {
		t.Fatalf("setup: get agent: %v", err)
	}

	var autopilotID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO autopilot (
			workspace_id, title, assignee_id, execution_mode,
			created_by_type, created_by_id
		)
		VALUES ($1, 'run_only fixture', $2, 'run_only', 'member', $3)
		RETURNING id
	`, testWorkspaceID, agentID, testUserID).Scan(&autopilotID); err != nil {
		t.Fatalf("setup: create autopilot: %v", err)
	}
	defer testPool.Exec(ctx, `DELETE FROM autopilot WHERE id = $1`, autopilotID)

	var runID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO autopilot_run (autopilot_id, source, status)
		VALUES ($1, 'manual', 'running')
		RETURNING id
	`, autopilotID).Scan(&runID); err != nil {
		t.Fatalf("setup: create autopilot_run: %v", err)
	}

	// issue_id is explicitly NULL — the condition that used to trigger 404.
	var taskID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_task_queue (
			agent_id, runtime_id, issue_id, status, priority, autopilot_run_id
		)
		VALUES ($1, $2, NULL, 'dispatched', 0, $3)
		RETURNING id
	`, agentID, runtimeID, runID).Scan(&taskID); err != nil {
		t.Fatalf("setup: create autopilot task: %v", err)
	}
	defer testPool.Exec(ctx, `DELETE FROM agent_task_queue WHERE id = $1`, taskID)

	// Cross-workspace daemon token must still 404.
	w := httptest.NewRecorder()
	req := newDaemonTokenRequest("POST", "/api/daemon/tasks/"+taskID+"/start", nil,
		"00000000-0000-0000-0000-000000000000", "attacker-daemon")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("taskId", taskID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	testHandler.StartTask(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("StartTask with cross-workspace token: expected 404, got %d: %s", w.Code, w.Body.String())
	}

	// Same-workspace daemon token must succeed — this is the bug in #1224.
	w = httptest.NewRecorder()
	req = newDaemonTokenRequest("POST", "/api/daemon/tasks/"+taskID+"/start", nil,
		testWorkspaceID, "legit-daemon")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	testHandler.StartTask(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("StartTask for run_only autopilot task: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var status string
	if err := testPool.QueryRow(ctx, `SELECT status FROM agent_task_queue WHERE id = $1`, taskID).Scan(&status); err != nil {
		t.Fatalf("post-check: read task status: %v", err)
	}
	if status != "running" {
		t.Fatalf("expected task status 'running' after StartTask, got %q", status)
	}
}

// ClaimTaskByRuntime must surface the issue's project github_repo resources
// as resp.Repos and hide the workspace-bound repos. Without this the agent
// would see two repo lists in the meta-skill and have no signal about which
// belongs to the current issue.
func TestClaimTask_ProjectGithubReposOverrideWorkspaceRepos(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()

	// Workspace repos: two of them, neither matches the project repo URL.
	setHandlerTestWorkspaceRepos(t, []map[string]string{
		{"url": "https://github.com/example/workspace-repo-a", "description": "ws a"},
		{"url": "https://github.com/example/workspace-repo-b", "description": "ws b"},
	})

	// Project + project_resource(github_repo) with a URL that is NOT in the
	// workspace's repos list.
	var projectID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO project (workspace_id, title) VALUES ($1, $2) RETURNING id
	`, testWorkspaceID, "Claim project repo override").Scan(&projectID); err != nil {
		t.Fatalf("create project: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM project WHERE id = $1`, projectID) })

	const projectRepoURL = "https://github.com/example/project-only-repo"
	if _, err := testPool.Exec(ctx, `
		INSERT INTO project_resource (
			project_id, workspace_id, resource_type, resource_ref, position
		) VALUES ($1, $2, 'github_repo', $3::jsonb, 0)
	`, projectID, testWorkspaceID, `{"url":"`+projectRepoURL+`"}`); err != nil {
		t.Fatalf("create project_resource: %v", err)
	}

	// Agent + runtime + queued task in this project.
	var agentID, runtimeID string
	if err := testPool.QueryRow(ctx,
		`SELECT id, runtime_id FROM agent WHERE workspace_id = $1 LIMIT 1`,
		testWorkspaceID,
	).Scan(&agentID, &runtimeID); err != nil {
		t.Fatalf("get agent: %v", err)
	}

	var issueID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO issue (
			workspace_id, project_id, title, status, priority, creator_id, creator_type, number, position
		) VALUES ($1, $2, 'project repo override', 'todo', 'medium', $3, 'member', 88001, 0)
		RETURNING id
	`, testWorkspaceID, projectID, testUserID).Scan(&issueID); err != nil {
		t.Fatalf("create issue: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM issue WHERE id = $1`, issueID) })

	var taskID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_task_queue (
			agent_id, runtime_id, issue_id, status, priority
		) VALUES ($1, $2, $3, 'queued', 0)
		RETURNING id
	`, agentID, runtimeID, issueID).Scan(&taskID); err != nil {
		t.Fatalf("create task: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM agent_task_queue WHERE id = $1`, taskID) })

	w := httptest.NewRecorder()
	req := newDaemonTokenRequest("POST", "/api/daemon/runtimes/"+runtimeID+"/claim", nil, testWorkspaceID, "test-claim-project-repos")
	req = withURLParam(req, "runtimeId", runtimeID)
	testHandler.ClaimTaskByRuntime(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("ClaimTaskByRuntime: %d %s", w.Code, w.Body.String())
	}

	var resp struct {
		Task *struct {
			Repos            []RepoData            `json:"repos"`
			ProjectID        string                `json:"project_id"`
			ProjectResources []ProjectResourceData `json:"project_resources"`
		} `json:"task"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Task == nil {
		t.Fatal("expected task in response")
	}
	if resp.Task.ProjectID != projectID {
		t.Errorf("project_id = %q, want %q", resp.Task.ProjectID, projectID)
	}
	if len(resp.Task.Repos) != 1 || resp.Task.Repos[0].URL != projectRepoURL {
		t.Fatalf("expected resp.Repos to contain only the project repo URL, got %+v", resp.Task.Repos)
	}
	for _, r := range resp.Task.Repos {
		if strings.HasSuffix(r.URL, "workspace-repo-a") || strings.HasSuffix(r.URL, "workspace-repo-b") {
			t.Errorf("workspace repo %q leaked into resp.Repos despite project override", r.URL)
		}
	}
	if len(resp.Task.ProjectResources) != 1 {
		t.Errorf("expected 1 project_resources entry, got %d", len(resp.Task.ProjectResources))
	}
}

// When the issue has no project, the claim handler falls back to workspace
// repos. (Project-bound issues with no github_repo resources instead get a
// managed shared workdir — see TestClaimTask_IssueProjectProvisionsManagedWorkdir.)
func TestClaimTask_ProjectWithoutRepos_FallsBackToWorkspaceRepos(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()

	setHandlerTestWorkspaceRepos(t, []map[string]string{
		{"url": "https://github.com/example/workspace-fallback", "description": "ws"},
	})

	var agentID, runtimeID string
	if err := testPool.QueryRow(ctx,
		`SELECT id, runtime_id FROM agent WHERE workspace_id = $1 LIMIT 1`,
		testWorkspaceID,
	).Scan(&agentID, &runtimeID); err != nil {
		t.Fatalf("get agent: %v", err)
	}

	var issueID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO issue (
			workspace_id, title, status, priority, creator_id, creator_type, number, position
		) VALUES ($1, 'no project repos', 'todo', 'medium', $2, 'member', 88002, 0)
		RETURNING id
	`, testWorkspaceID, testUserID).Scan(&issueID); err != nil {
		t.Fatalf("create issue: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM issue WHERE id = $1`, issueID) })

	var taskID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_task_queue (
			agent_id, runtime_id, issue_id, status, priority
		) VALUES ($1, $2, $3, 'queued', 0)
		RETURNING id
	`, agentID, runtimeID, issueID).Scan(&taskID); err != nil {
		t.Fatalf("create task: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM agent_task_queue WHERE id = $1`, taskID) })

	w := httptest.NewRecorder()
	req := newDaemonTokenRequest("POST", "/api/daemon/runtimes/"+runtimeID+"/claim", nil, testWorkspaceID, "test-claim-fallback")
	req = withURLParam(req, "runtimeId", runtimeID)
	testHandler.ClaimTaskByRuntime(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("ClaimTaskByRuntime: %d %s", w.Code, w.Body.String())
	}

	var resp struct {
		Task *struct {
			Repos []RepoData `json:"repos"`
		} `json:"task"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Task == nil {
		t.Fatal("expected task in response")
	}
	if len(resp.Task.Repos) != 1 || !strings.HasSuffix(resp.Task.Repos[0].URL, "workspace-fallback") {
		t.Fatalf("expected workspace fallback repo, got %+v", resp.Task.Repos)
	}
}

// Regression test for #1276: ClaimTaskByRuntime must populate workspace_id in
// the response for run_only autopilot tasks. Before the fix, resp.WorkspaceID
// stayed empty because ClaimTaskByRuntime only handled IssueID and
// ChatSessionID branches, causing the daemon's execenv to fail with
// "workspace ID is required".
func TestClaimTask_AutopilotRunOnly_PopulatesWorkspaceID(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()

	var agentID, runtimeID string
	if err := testPool.QueryRow(ctx, `
		SELECT a.id, a.runtime_id FROM agent a WHERE a.workspace_id = $1 LIMIT 1
	`, testWorkspaceID).Scan(&agentID, &runtimeID); err != nil {
		t.Fatalf("setup: get agent: %v", err)
	}

	var autopilotID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO autopilot (
			workspace_id, title, assignee_id, execution_mode,
			created_by_type, created_by_id
		)
		VALUES ($1, 'claim workspace fixture', $2, 'run_only', 'member', $3)
		RETURNING id
	`, testWorkspaceID, agentID, testUserID).Scan(&autopilotID); err != nil {
		t.Fatalf("setup: create autopilot: %v", err)
	}
	defer testPool.Exec(ctx, `DELETE FROM autopilot WHERE id = $1`, autopilotID)

	var runID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO autopilot_run (autopilot_id, source, status)
		VALUES ($1, 'manual', 'running')
		RETURNING id
	`, autopilotID).Scan(&runID); err != nil {
		t.Fatalf("setup: create autopilot_run: %v", err)
	}

	// Create a queued task with only AutopilotRunID (no IssueID, no ChatSessionID).
	var taskID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_task_queue (
			agent_id, runtime_id, issue_id, status, priority, autopilot_run_id
		)
		VALUES ($1, $2, NULL, 'queued', 0, $3)
		RETURNING id
	`, agentID, runtimeID, runID).Scan(&taskID); err != nil {
		t.Fatalf("setup: create autopilot task: %v", err)
	}
	defer testPool.Exec(ctx, `DELETE FROM agent_task_queue WHERE id = $1`, taskID)

	w := httptest.NewRecorder()
	req := newDaemonTokenRequest("POST", "/api/daemon/runtimes/"+runtimeID+"/claim", nil,
		testWorkspaceID, "test-daemon-claim")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("runtimeId", runtimeID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	testHandler.ClaimTaskByRuntime(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("ClaimTaskByRuntime: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Task *struct {
			WorkspaceID string `json:"workspace_id"`
			ThreadName  string `json:"thread_name"`
		} `json:"task"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Task == nil {
		t.Fatal("expected a task in response, got nil")
	}
	if resp.Task.WorkspaceID == "" {
		t.Fatal("ClaimTaskByRuntime for run_only autopilot: workspace_id is empty in response")
	}
	if resp.Task.WorkspaceID != testWorkspaceID {
		t.Fatalf("expected workspace_id %q, got %q", testWorkspaceID, resp.Task.WorkspaceID)
	}
	if resp.Task.ThreadName != "claim workspace fixture" {
		t.Fatalf("autopilot task thread_name = %q, want autopilot title", resp.Task.ThreadName)
	}
}

// TestClaimTaskByRuntime_TaskWorkspaceMismatch_CancelsAndRejects verifies
// the defense-in-depth check in ClaimTaskByRuntime: if a task is somehow
// dispatched to a runtime whose workspace doesn't match the task's
// resolved workspace (upstream routing / data-integrity bug), the handler
// must 500 AND cancel the dispatched task so it doesn't sit in
// 'dispatched' until the 5-minute sweeper — which would also leave the
// agent stuck reporting 'working' in the UI.
func TestClaimTaskByRuntime_TaskWorkspaceMismatch_CancelsAndRejects(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()

	// Local agent/runtime (belongs to testWorkspace).
	var localAgentID, localRuntimeID string
	if err := testPool.QueryRow(ctx,
		`SELECT id, runtime_id FROM agent WHERE workspace_id = $1 LIMIT 1`,
		testWorkspaceID,
	).Scan(&localAgentID, &localRuntimeID); err != nil {
		t.Fatalf("setup: get local agent: %v", err)
	}

	// Foreign workspace with its own issue — what the misrouted task will
	// resolve to.
	var foreignWorkspaceID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO workspace (name, slug, description, issue_prefix)
		VALUES ($1, $2, $3, $4)
		RETURNING id
	`, "Mismatch Foreign", "mismatch-foreign-claim", "", "MFC").Scan(&foreignWorkspaceID); err != nil {
		t.Fatalf("setup: create foreign workspace: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1`, foreignWorkspaceID) })

	var foreignIssueID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO issue (workspace_id, title, status, priority, creator_id, creator_type, number, position)
		VALUES ($1, 'mismatch-foreign-issue', 'todo', 'medium', $2, 'member', 77001, 0)
		RETURNING id
	`, foreignWorkspaceID, testUserID).Scan(&foreignIssueID); err != nil {
		t.Fatalf("setup: create foreign issue: %v", err)
	}

	// Construct the inconsistent task: runtime_id belongs to testWorkspace,
	// but issue_id is in foreignWorkspace. This is the data shape a routing
	// bug would produce.
	var taskID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_task_queue (agent_id, runtime_id, issue_id, status, priority)
		VALUES ($1, $2, $3, 'queued', 2)
		RETURNING id
	`, localAgentID, localRuntimeID, foreignIssueID).Scan(&taskID); err != nil {
		t.Fatalf("setup: create mismatched task: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM agent_task_queue WHERE id = $1`, taskID) })

	w := httptest.NewRecorder()
	req := newDaemonTokenRequest("POST", "/api/daemon/runtimes/"+localRuntimeID+"/claim", nil,
		testWorkspaceID, "legit-daemon")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("runtimeId", localRuntimeID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	testHandler.ClaimTaskByRuntime(w, req)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("ClaimTaskByRuntime (mismatch): expected 500, got %d: %s", w.Code, w.Body.String())
	}

	// Task must NOT remain dispatched — it has to be cancelled so the agent
	// is released immediately rather than stuck until the sweeper fires.
	var status string
	if err := testPool.QueryRow(ctx,
		`SELECT status FROM agent_task_queue WHERE id = $1`, taskID,
	).Scan(&status); err != nil {
		t.Fatalf("read task status: %v", err)
	}
	if status != "cancelled" {
		t.Fatalf("ClaimTaskByRuntime (mismatch): expected task status=cancelled, got %q", status)
	}
}

func TestCompleteTask_GroupChannelSendActionWritesMessage(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	taskID, channelID := createChannelCompletionTask(t, "group")
	const visibleReply = "Visible action reply"

	w := completeTaskForTest(t, taskID, map[string]any{
		"action": "message_send",
		"output": visibleReply,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("CompleteTask: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var channelMessageCount int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*) FROM channel_message
		WHERE channel_id = $1 AND author_type = 'agent' AND content = $2
	`, channelID, visibleReply).Scan(&channelMessageCount); err != nil {
		t.Fatalf("count bridged channel messages: %v", err)
	}
	if channelMessageCount != 1 {
		t.Fatalf("channel message count = %d, want 1", channelMessageCount)
	}
}

func TestCompleteTask_GroupChannelSendActionWithoutDaemonCapabilityIsSuppressed(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	taskID, channelID := createChannelCompletionTaskWithCapabilities(t, "group", nil)
	const visibleReply = "Outdated daemon action reply"

	w := completeTaskForTest(t, taskID, map[string]any{
		"action": "message_send",
		"output": visibleReply,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("CompleteTask: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	assertNoChannelMessageContent(t, channelID, visibleReply)
	assertTaskOutputSuppressedReason(t, taskID, protocol.ChannelOutputSuppressedReasonDaemonOutdated)
}

func TestCompleteTask_GroupChannelSendTargetDMWithoutDaemonCapabilityIsSuppressed(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	taskID, sourceChannelID := createChannelCompletionTaskWithCapabilities(t, "group", nil)
	var recipientName, agentID string
	if err := testPool.QueryRow(ctx, `SELECT name FROM "user" WHERE id = $1`, testUserID).Scan(&recipientName); err != nil {
		t.Fatalf("load recipient name: %v", err)
	}
	if err := testPool.QueryRow(ctx, `SELECT agent_id FROM agent_task_queue WHERE id = $1`, taskID).Scan(&agentID); err != nil {
		t.Fatalf("load task agent: %v", err)
	}
	canonical := dmCanonicalName("user", testUserID, "agent", agentID)
	const visibleReply = "Outdated daemon targeted DM reply"

	w := completeTaskForTest(t, taskID, map[string]any{
		"action": "send",
		"target": "dm:@" + recipientName,
		"output": visibleReply,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("CompleteTask: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	assertNoChannelMessageContent(t, sourceChannelID, visibleReply)
	assertTaskOutputSuppressedReason(t, taskID, protocol.ChannelOutputSuppressedReasonDaemonOutdated)

	var dmChannelCount int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM channel
		WHERE workspace_id = $1 AND kind = 'dm' AND name = $2
	`, testWorkspaceID, canonical).Scan(&dmChannelCount); err != nil {
		t.Fatalf("count targeted dm channels: %v", err)
	}
	if dmChannelCount != 0 {
		t.Fatalf("targeted dm channel count = %d, want 0", dmChannelCount)
	}
	var visibleCount int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM channel_message
		WHERE content = $1
	`, visibleReply).Scan(&visibleCount); err != nil {
		t.Fatalf("count visible targeted dm messages: %v", err)
	}
	if visibleCount != 0 {
		t.Fatalf("visible targeted dm message count = %d, want 0", visibleCount)
	}
}

func TestCompleteTask_GroupChannelSendAliasTargetHumanDMCreatesMessage(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	taskID, sourceChannelID := createChannelCompletionTask(t, "group")
	recipientID := testUserID
	var recipientName, agentID string
	if err := testPool.QueryRow(ctx, `SELECT name FROM "user" WHERE id = $1`, recipientID).Scan(&recipientName); err != nil {
		t.Fatalf("load recipient name: %v", err)
	}
	if err := testPool.QueryRow(ctx, `SELECT agent_id FROM agent_task_queue WHERE id = $1`, taskID).Scan(&agentID); err != nil {
		t.Fatalf("load task agent: %v", err)
	}
	canonical := dmCanonicalName("user", recipientID, "agent", agentID)
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `
			DELETE FROM channel
			WHERE workspace_id = $1 AND kind = 'dm' AND name = $2
		`, testWorkspaceID, canonical)
	})
	const visibleReply = "Targeted DM reply"

	w := completeTaskForTest(t, taskID, map[string]any{
		"action": "send",
		"target": "dm:@" + recipientName,
		"output": visibleReply,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("CompleteTask: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	assertNoChannelMessageContent(t, sourceChannelID, visibleReply)

	var dmChannelID string
	if err := testPool.QueryRow(ctx, `
		SELECT id FROM channel
		WHERE workspace_id = $1 AND kind = 'dm' AND name = $2
	`, testWorkspaceID, canonical).Scan(&dmChannelID); err != nil {
		t.Fatalf("load targeted dm channel: %v", err)
	}
	var dmMessageCount int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM channel_message
		WHERE channel_id = $1 AND author_type = 'agent' AND author_id = $2 AND content = $3
	`, dmChannelID, agentID, visibleReply).Scan(&dmMessageCount); err != nil {
		t.Fatalf("count targeted dm message: %v", err)
	}
	if dmMessageCount != 1 {
		t.Fatalf("targeted dm message count = %d, want 1", dmMessageCount)
	}
}

func TestCompleteTask_GroupChannelSendTargetInvalidSuppressesNonLeaky(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	tests := []struct {
		name    string
		target  func(t *testing.T, taskID string) string
		options map[string]any
	}{
		{
			name: "missing target",
			target: func(t *testing.T, taskID string) string {
				return "dm:@missing_" + strings.ReplaceAll(uuid.NewString(), "-", "")
			},
		},
		{
			name: "cross workspace user",
			target: func(t *testing.T, taskID string) string {
				userID := createEphemeralUser(t, "cross-workspace")
				var name string
				if err := testPool.QueryRow(context.Background(), `SELECT name FROM "user" WHERE id = $1`, userID).Scan(&name); err != nil {
					t.Fatalf("load cross workspace user name: %v", err)
				}
				return "dm:@" + name
			},
		},
		{
			name: "same workspace member without private agent access",
			target: func(t *testing.T, taskID string) string {
				recipientID := createChannelPlainMember(t)
				var name string
				if err := testPool.QueryRow(context.Background(), `SELECT name FROM "user" WHERE id = $1`, recipientID).Scan(&name); err != nil {
					t.Fatalf("load recipient name: %v", err)
				}
				return "dm:@" + name
			},
		},
		{
			name: "self dm by agent name",
			target: func(t *testing.T, taskID string) string {
				var agentName string
				if err := testPool.QueryRow(context.Background(), `
					SELECT a.name
					FROM agent_task_queue q
					JOIN agent a ON a.id = q.agent_id
					WHERE q.id = $1`, taskID).Scan(&agentName); err != nil {
					t.Fatalf("load task agent name: %v", err)
				}
				return "dm:@" + agentName
			},
		},
		{
			name: "dm options rejected outside thread branch",
			target: func(t *testing.T, taskID string) string {
				var name string
				if err := testPool.QueryRow(context.Background(), `SELECT name FROM "user" WHERE id = $1`, testUserID).Scan(&name); err != nil {
					t.Fatalf("load recipient name: %v", err)
				}
				return "dm:@" + name
			},
			options: map[string]any{"show_in_channel": false},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			taskID, channelID := createChannelCompletionTask(t, "group")
			const visibleReply = "Invalid targeted reply"
			target := tt.target(t, taskID)
			body := map[string]any{
				"action": "send",
				"target": target,
				"output": visibleReply,
			}
			if tt.options != nil {
				body["options"] = tt.options
			}

			w := completeTaskForTest(t, taskID, body)
			if w.Code != http.StatusOK {
				t.Fatalf("CompleteTask: expected 200, got %d: %s", w.Code, w.Body.String())
			}

			assertNoChannelMessageContent(t, channelID, visibleReply)
			assertTaskOutputSuppressedReason(t, taskID, protocol.ChannelOutputSuppressedReasonInvalidTarget)
			var visibleCount int
			if err := testPool.QueryRow(context.Background(), `
				SELECT count(*)
				FROM channel_message
				WHERE content = $1
			`, visibleReply).Scan(&visibleCount); err != nil {
				t.Fatalf("count invalid target visible messages: %v", err)
			}
			if visibleCount != 0 {
				t.Fatalf("invalid target visible message count = %d, want 0", visibleCount)
			}
		})
	}
}

func TestCompleteTask_GroupChannelThreadTargetAllowsFalseShowInChannelOption(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	taskID, channelID := createChannelCompletionTask(t, "group")
	var rootID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO channel_message (channel_id, workspace_id, author_type, author_id, author_name, content, source, external_message_id, trigger_depth)
		VALUES ($1, $2, 'user', $3, 'Tester', 'thread root', 'multica', $4, 0)
		RETURNING id
	`, channelID, testWorkspaceID, testUserID, "thread-target-"+uuid.NewString()).Scan(&rootID); err != nil {
		t.Fatalf("seed thread root: %v", err)
	}
	var channelName string
	if err := testPool.QueryRow(ctx, `SELECT name FROM channel WHERE id = $1`, channelID).Scan(&channelName); err != nil {
		t.Fatalf("load channel name: %v", err)
	}
	const visibleReply = "Targeted thread reply"

	w := completeTaskForTest(t, taskID, map[string]any{
		"action":  "send",
		"target":  "#" + channelName + ":" + rootID,
		"options": map[string]any{"show_in_channel": false},
		"output":  visibleReply,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("CompleteTask: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var replyCount int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM channel_message
		WHERE channel_id = $1 AND author_type = 'agent' AND content = $2 AND thread_root_message_id = $3
	`, channelID, visibleReply, rootID).Scan(&replyCount); err != nil {
		t.Fatalf("count targeted thread reply: %v", err)
	}
	if replyCount != 1 {
		t.Fatalf("targeted thread reply count = %d, want 1", replyCount)
	}
}

func TestCompleteTask_GroupChannelThreadTargetShowInChannelProjectsSameMessage(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	taskID, channelID := createChannelCompletionTask(t, "group")
	targetAgentID := createHandlerTestAgent(t, "Thread Projection Completion Target", nil)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3)`, channelID, testWorkspaceID, targetAgentID); err != nil {
		t.Fatalf("seed target agent member: %v", err)
	}
	var rootID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO channel_message (channel_id, workspace_id, author_type, author_id, author_name, content, source, external_message_id, trigger_depth)
		VALUES ($1, $2, 'user', $3, 'Tester', 'thread root', 'multica', $4, 0)
		RETURNING id
	`, channelID, testWorkspaceID, testUserID, "thread-target-projection-"+uuid.NewString()).Scan(&rootID); err != nil {
		t.Fatalf("seed thread root: %v", err)
	}
	var channelName string
	if err := testPool.QueryRow(ctx, `SELECT name FROM channel WHERE id = $1`, channelID).Scan(&channelName); err != nil {
		t.Fatalf("load channel name: %v", err)
	}
	visibleReply := "please [@Thread Projection Completion Target](mention://agent/" + targetAgentID + ") review from the thread"

	w := completeTaskForTest(t, taskID, map[string]any{
		"action":  "send",
		"target":  "#" + channelName + ":" + rootID,
		"options": map[string]any{"show_in_channel": true},
		"output":  visibleReply,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("CompleteTask: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var replyID string
	var projected bool
	if err := testPool.QueryRow(ctx, `
		SELECT id, main_timeline_visible
		FROM channel_message
		WHERE channel_id = $1 AND author_type = 'agent' AND content = $2 AND thread_root_message_id = $3
	`, channelID, visibleReply, rootID).Scan(&replyID, &projected); err != nil {
		t.Fatalf("load targeted thread reply: %v", err)
	}
	if !projected {
		t.Fatalf("targeted thread reply main_timeline_visible = false, want true")
	}
	var carrierCount int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM channel_message
		WHERE channel_id = $1
		  AND author_type = 'agent'
		  AND thread_root_message_id IS NULL
		  AND reply_to_message_id = $2`, channelID, replyID).Scan(&carrierCount); err != nil {
		t.Fatalf("count legacy thread mirror carriers: %v", err)
	}
	if carrierCount != 0 {
		t.Fatalf("legacy thread mirror carrier count = %d, want 0", carrierCount)
	}
	mainTimeline := listedMessagesForUser(t, channelID, testUserID)
	if len(mainTimeline) != 2 {
		t.Fatalf("main timeline = %+v, want root + projected reply", mainTimeline)
	}
	if mainTimeline[1].ID != replyID {
		t.Fatalf("projected message id = %s, want thread reply %s", mainTimeline[1].ID, replyID)
	}
	if mainTimeline[1].ThreadRootMessageID == nil || *mainTimeline[1].ThreadRootMessageID != rootID {
		t.Fatalf("projected thread_root_message_id = %v, want %s", mainTimeline[1].ThreadRootMessageID, rootID)
	}
	if mainTimeline[1].ThreadRoot == nil || mainTimeline[1].ThreadRoot.ID != rootID {
		t.Fatalf("projected thread root summary = %+v, want root %s", mainTimeline[1].ThreadRoot, rootID)
	}
	var targetTasks int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM agent_task_queue q
		JOIN channel_agent_session cas ON cas.chat_session_id = q.chat_session_id
		WHERE cas.channel_id = $1 AND cas.agent_id = $2`, channelID, targetAgentID).Scan(&targetTasks); err != nil {
		t.Fatalf("count target agent tasks: %v", err)
	}
	if targetTasks != 1 {
		t.Fatalf("target agent task count = %d, want 1", targetTasks)
	}
}

func TestCompleteTask_GroupChannelSendActionWritesParts(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	taskID, channelID := createChannelCompletionTask(t, "group")

	w := completeTaskForTest(t, taskID, map[string]any{
		"action": "message_send",
		"parts": []map[string]any{
			{"type": "sticker", "sticker_id": "hi"},
		},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("CompleteTask: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var storedParts []byte
	if err := testPool.QueryRow(context.Background(), `
		SELECT parts FROM channel_message
		WHERE channel_id = $1 AND author_type = 'agent'
		ORDER BY created_at DESC
		LIMIT 1
	`, channelID).Scan(&storedParts); err != nil {
		t.Fatalf("load bridged channel message parts: %v", err)
	}
	var decoded []protocol.MessagePart
	if err := json.Unmarshal(storedParts, &decoded); err != nil {
		t.Fatalf("decode stored parts: %v", err)
	}
	if len(decoded) != 1 || decoded[0].Type != protocol.MessagePartTypeSticker || decoded[0].StickerID != "hi" || decoded[0].PackID != "builtin" {
		t.Fatalf("stored parts = %+v, want builtin hi sticker", decoded)
	}
}

func TestCompleteTask_GroupChannelCommandSendOutputIsSuppressed(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	taskID, channelID := createChannelCompletionTask(t, "group")
	const visibleReply = "Visible command reply"
	rawOutput := `multica message send --message "` + visibleReply + `"`

	w := completeTaskForTest(t, taskID, map[string]any{
		"type":   "message",
		"output": rawOutput,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("CompleteTask: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	assertNoChannelMessageContent(t, channelID, visibleReply)
	assertNoChannelMessageContent(t, channelID, rawOutput)
	assertTaskOutputSuppressedReason(t, taskID, protocol.ChannelOutputSuppressedReasonLegacyProtocolOutput)
}

func TestCompleteTask_GroupChannelNoReplyRationaleOutputIsSuppressed(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	taskID, channelID := createChannelCompletionTask(t, "group")
	rawOutput := `Not directed at me — Frank's "hi" is a general greeting following my own earlier message, not a new task or all-hands request. No visible reply needed.`

	w := completeTaskForTest(t, taskID, map[string]any{"output": rawOutput})
	if w.Code != http.StatusOK {
		t.Fatalf("CompleteTask: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	assertNoChannelMessageContent(t, channelID, rawOutput)
	assertTaskOutputSuppressedReason(t, taskID, protocol.ChannelOutputSuppressedReasonNoReplyRationale)
}

func TestCompleteTask_GroupChannelNoReplyRationaleOutputIsSuppressedWithCLITransport(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	taskID, channelID := createChannelCompletionTaskWithCapabilities(t, "group", []string{
		protocol.DaemonCapabilityChannelOutputActions,
		protocol.DaemonCapabilityAgentCLITransport,
	})
	rawOutput := `Not directed at me — Frank's "hi" is a general greeting following my own earlier message, not a new task or all-hands request. No visible reply needed.`

	w := completeTaskForTest(t, taskID, map[string]any{"output": rawOutput})
	if w.Code != http.StatusOK {
		t.Fatalf("CompleteTask: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	assertNoChannelMessageContent(t, channelID, rawOutput)
	assertTaskOutputSuppressedReason(t, taskID, protocol.ChannelOutputSuppressedReasonNoReplyRationale)
}

func TestCompleteTask_GroupChannelNoReplyPhraseInsideNormalTextIsNotSuppressed(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	taskID, channelID := createChannelCompletionTask(t, "group")
	visibleReply := `Please keep the user-authored phrase "no reply needed" visible in this normal answer.`

	w := completeTaskForTest(t, taskID, map[string]any{"output": visibleReply})
	if w.Code != http.StatusOK {
		t.Fatalf("CompleteTask: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	assertChannelMessageContentCount(t, channelID, visibleReply, 1)
	assertTaskOutputSuppressedReason(t, taskID, "")
}

func TestCompleteTask_CLICapableRunSuppressesUnsentFinalText(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	taskID, channelID := createChannelCompletionTaskWithCapabilities(t, "group", []string{
		protocol.DaemonCapabilityChannelOutputActions,
		protocol.DaemonCapabilityAgentCLITransport,
	})
	// Novel rationale phrasing that exact-match does not catch.
	rawOutput := "This request was already fully handled in the unread bundle itself — I already sent all the stickers and followed up with Frank An. No new action needed here."

	w := completeTaskForTest(t, taskID, map[string]any{"output": rawOutput})
	if w.Code != http.StatusOK {
		t.Fatalf("CompleteTask: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	assertNoChannelMessageContent(t, channelID, rawOutput)
	assertTaskOutputSuppressedReason(t, taskID, protocol.ChannelOutputSuppressedReasonUnsentFinalOutput)
}

func TestCompleteTask_CLICapableRunSuppressesNormalFinalText(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	taskID, channelID := createChannelCompletionTaskWithCapabilities(t, "group", []string{
		protocol.DaemonCapabilityChannelOutputActions,
		protocol.DaemonCapabilityAgentCLITransport,
	})
	// Even a legitimate-looking message is suppressed on CLI-capable runs:
	// the agent must use `multica send` to produce visible output.
	const visibleReply = "Here is a perfectly normal reply that should have been sent via multica send."

	w := completeTaskForTest(t, taskID, map[string]any{"output": visibleReply})
	if w.Code != http.StatusOK {
		t.Fatalf("CompleteTask: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	assertNoChannelMessageContent(t, channelID, visibleReply)
	assertTaskOutputSuppressedReason(t, taskID, protocol.ChannelOutputSuppressedReasonUnsentFinalOutput)
}

func TestCompleteTask_CLICapableRunDoesNotSuppressExplicitMessageSendAction(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	taskID, channelID := createChannelCompletionTaskWithCapabilities(t, "group", []string{
		protocol.DaemonCapabilityChannelOutputActions,
		protocol.DaemonCapabilityAgentCLITransport,
	})
	const visibleReply = "Intentional explicit action message on CLI-capable run"

	w := completeTaskForTest(t, taskID, map[string]any{
		"action": protocol.ChatOutputActionMessageSend,
		"output": visibleReply,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("CompleteTask: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Explicit message_send action is intentional, not rationale text.
	assertChannelMessageContentCount(t, channelID, visibleReply, 1)
	assertTaskOutputSuppressedReason(t, taskID, "")
}

func TestCompleteTask_GroupChannelStructuredMessageSendOutputIsSuppressed(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	taskID, channelID := createChannelCompletionTask(t, "group")
	const visibleReply = "Structured final text reply"

	w := completeTaskForTest(t, taskID, map[string]any{
		"output": `{"action":"message_send","output":"` + visibleReply + `"}`,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("CompleteTask: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var channelMessageCount int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*) FROM channel_message
		WHERE channel_id = $1 AND author_type = 'agent' AND content = $2
	`, channelID, visibleReply).Scan(&channelMessageCount); err != nil {
		t.Fatalf("count bridged channel messages: %v", err)
	}
	if channelMessageCount != 0 {
		t.Fatalf("channel message count = %d, want 0", channelMessageCount)
	}
	assertTaskOutputSuppressedReason(t, taskID, protocol.ChannelOutputSuppressedReasonLegacyProtocolOutput)
}

func TestCompleteTask_GroupChannelMessageReactActionWritesReaction(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	taskID, channelID := createChannelCompletionTask(t, "group")
	var agentID string
	if err := testPool.QueryRow(ctx, `SELECT agent_id FROM agent_task_queue WHERE id = $1`, taskID).Scan(&agentID); err != nil {
		t.Fatalf("load task agent: %v", err)
	}
	var triggerID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO channel_message (channel_id, workspace_id, author_type, author_id, author_name, content, source, external_message_id, trigger_depth)
		VALUES ($1, $2, 'user', $3, 'Tester', 'react target', 'multica', $4, 0)
		RETURNING id
	`, channelID, testWorkspaceID, testUserID, "react-target-"+uuid.NewString()).Scan(&triggerID); err != nil {
		t.Fatalf("seed trigger message: %v", err)
	}

	w := completeTaskForTest(t, taskID, map[string]any{
		"action": "message_react",
		"reaction": map[string]any{
			"message_id": triggerID,
			"emoji":      "👍",
		},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("CompleteTask: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var reactionCount int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM channel_message_reaction
		WHERE channel_message_id = $1 AND actor_type = 'agent' AND actor_id = $2 AND emoji = '👍'
	`, triggerID, agentID).Scan(&reactionCount); err != nil {
		t.Fatalf("count bridged reaction: %v", err)
	}
	if reactionCount != 1 {
		t.Fatalf("reaction count = %d, want 1", reactionCount)
	}
}

func TestCompleteTask_GroupChannelMessageReactWithoutDaemonCapabilityIsSuppressed(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	taskID, channelID := createChannelCompletionTaskWithCapabilities(t, "group", nil)
	var agentID string
	if err := testPool.QueryRow(ctx, `SELECT agent_id FROM agent_task_queue WHERE id = $1`, taskID).Scan(&agentID); err != nil {
		t.Fatalf("load task agent: %v", err)
	}
	var triggerID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO channel_message (channel_id, workspace_id, author_type, author_id, author_name, content, source, external_message_id, trigger_depth)
		VALUES ($1, $2, 'user', $3, 'Tester', 'old daemon react target', 'multica', $4, 0)
		RETURNING id
	`, channelID, testWorkspaceID, testUserID, "old-daemon-react-target-"+uuid.NewString()).Scan(&triggerID); err != nil {
		t.Fatalf("seed trigger message: %v", err)
	}

	w := completeTaskForTest(t, taskID, map[string]any{
		"action": "message_react",
		"reaction": map[string]any{
			"message_id": triggerID,
			"emoji":      "👍",
		},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("CompleteTask: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var reactionCount int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM channel_message_reaction
		WHERE channel_message_id = $1 AND actor_type = 'agent' AND actor_id = $2
	`, triggerID, agentID).Scan(&reactionCount); err != nil {
		t.Fatalf("count bridged reaction: %v", err)
	}
	if reactionCount != 0 {
		t.Fatalf("reaction count = %d, want 0", reactionCount)
	}
	assertTaskOutputSuppressedReason(t, taskID, protocol.ChannelOutputSuppressedReasonDaemonOutdated)
}

func TestCompleteTask_GroupChannelLegacyTopLevelTypeMessageWritesMessage(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	taskID, channelID := createChannelCompletionTask(t, "group")
	const visibleReply = "Legacy top-level type-only reply"

	w := completeTaskForTest(t, taskID, map[string]any{
		"type":   "message",
		"output": visibleReply,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("CompleteTask: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	assertChannelMessageContentCount(t, channelID, visibleReply, 1)
	assertTaskOutputSuppressedReason(t, taskID, "")

	var chatMessageCount int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*) FROM chat_message
		WHERE task_id = $1 AND role = 'assistant' AND content = $2
	`, taskID, visibleReply).Scan(&chatMessageCount); err != nil {
		t.Fatalf("count assistant chat messages: %v", err)
	}
	if chatMessageCount != 1 {
		t.Fatalf("assistant chat message count = %d, want 1", chatMessageCount)
	}
}

func TestCompleteTask_GroupChannelPlainFinalTextWritesMessage(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	taskID, channelID := createChannelCompletionTask(t, "group")
	const plainReply = "Plain final reply in channel"

	w := completeTaskForTest(t, taskID, map[string]any{"output": plainReply})
	if w.Code != http.StatusOK {
		t.Fatalf("CompleteTask: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	assertChannelMessageContentCount(t, channelID, plainReply, 1)
	assertTaskOutputSuppressedReason(t, taskID, "")

	var chatMessageCount int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*) FROM chat_message
		WHERE task_id = $1 AND role = 'assistant' AND content = $2
	`, taskID, plainReply).Scan(&chatMessageCount); err != nil {
		t.Fatalf("count assistant chat messages: %v", err)
	}
	if chatMessageCount != 1 {
		t.Fatalf("assistant chat message count = %d, want 1", chatMessageCount)
	}
}

func TestCompleteTask_DMChannelPlainTextReplyWritesMessage(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	taskID, channelID := createChannelCompletionTask(t, "dm")
	const dmReply = "Plain final reply in DM"

	w := completeTaskForTest(t, taskID, map[string]any{"output": dmReply})
	if w.Code != http.StatusOK {
		t.Fatalf("CompleteTask: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	assertChannelMessageContentCount(t, channelID, dmReply, 1)
	assertTaskOutputSuppressedReason(t, taskID, "")
}

func TestCompleteTask_DMChannelStructuredMessageSendOutputIsSuppressed(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	taskID, channelID := createChannelCompletionTask(t, "dm")
	const dmReply = "Structured DM reply"

	rawOutput := `Assistant reply: {"action":"message_send","output":"` + dmReply + `","parts":[{"type":"text","text":"` + dmReply + `"}]}`
	w := completeTaskForTest(t, taskID, map[string]any{"output": rawOutput})
	if w.Code != http.StatusOK {
		t.Fatalf("CompleteTask: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	assertNoChannelMessageContent(t, channelID, dmReply)
	assertNoChannelMessageContent(t, channelID, rawOutput)
	assertTaskOutputSuppressedReason(t, taskID, protocol.ChannelOutputSuppressedReasonLegacyProtocolOutput)
}

func TestTaskServiceCompleteTask_UnwrapsStructuredMessageSendBeforePersist(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	var agentID, runtimeID string
	if err := testPool.QueryRow(ctx, `
		SELECT id, runtime_id FROM agent
		WHERE workspace_id = $1 AND runtime_id IS NOT NULL
		LIMIT 1
	`, testWorkspaceID).Scan(&agentID, &runtimeID); err != nil {
		t.Fatalf("setup: load agent runtime: %v", err)
	}
	var chatSessionID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO chat_session (workspace_id, agent_id, creator_id, title)
		VALUES ($1, $2, $3, 'direct structured service output')
		RETURNING id
	`, testWorkspaceID, agentID, testUserID).Scan(&chatSessionID); err != nil {
		t.Fatalf("setup: create chat session: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM chat_session WHERE id = $1`, chatSessionID) })
	var taskID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_task_queue (agent_id, runtime_id, chat_session_id, status, priority, started_at)
		VALUES ($1, $2, $3, 'running', 2, now())
		RETURNING id
	`, agentID, runtimeID, chatSessionID).Scan(&taskID); err != nil {
		t.Fatalf("setup: create running chat task: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM agent_task_queue WHERE id = $1`, taskID) })

	const dmReply = "Structured service reply"
	rawOutput := `Assistant reply: {"action":"message_send","output":"` + dmReply + `","parts":[{"type":"text","text":"` + dmReply + `"}]}`
	result, err := json.Marshal(protocol.TaskCompletedPayload{Output: rawOutput})
	if err != nil {
		t.Fatalf("marshal task result: %v", err)
	}

	if _, err := testHandler.TaskService.CompleteTask(context.Background(), parseUUID(taskID), result, "", ""); err != nil {
		t.Fatalf("TaskService.CompleteTask: %v", err)
	}

	var content string
	var storedParts []byte
	if err := testPool.QueryRow(context.Background(), `
		SELECT content, parts FROM chat_message
		WHERE task_id = $1 AND role = 'assistant'
		ORDER BY created_at DESC
		LIMIT 1
	`, taskID).Scan(&content, &storedParts); err != nil {
		t.Fatalf("load assistant chat message: %v", err)
	}
	if content != dmReply {
		t.Fatalf("chat content = %q, want %q", content, dmReply)
	}
	var decoded []protocol.MessagePart
	if err := json.Unmarshal(storedParts, &decoded); err != nil {
		t.Fatalf("decode stored parts: %v", err)
	}
	if len(decoded) != 1 || decoded[0].Type != protocol.MessagePartTypeText || decoded[0].Text != dmReply {
		t.Fatalf("stored parts = %+v, want one text part", decoded)
	}
}

// Regression test for MUL-1198: comment-triggered tasks that finish without
// the agent posting any comment must still deliver a synthesized result
// comment, threaded under the trigger. Before the fix, CompleteTask exempted
// comment-triggered tasks from the auto-synthesis path, so a Claude Code /
// Codex / etc. agent that ended its run with only terminal text (no
// `multica issue comment add` call) left the user staring at a "Completed"
// badge with no reply.
func TestCompleteTask_CommentTriggered_SynthesizesCommentWhenAgentSilent(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()

	var agentID, runtimeID string
	if err := testPool.QueryRow(ctx, `
		SELECT a.id, a.runtime_id FROM agent a WHERE a.workspace_id = $1 LIMIT 1
	`, testWorkspaceID).Scan(&agentID, &runtimeID); err != nil {
		t.Fatalf("setup: get agent: %v", err)
	}

	var issueID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO issue (workspace_id, title, status, priority, creator_id, creator_type, number, position)
		VALUES ($1, 'mul-1198 fixture', 'in_progress', 'none', $2, 'member', 81198, 0)
		RETURNING id
	`, testWorkspaceID, testUserID).Scan(&issueID); err != nil {
		t.Fatalf("setup: create issue: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM issue WHERE id = $1`, issueID) })

	var triggerCommentID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO comment (issue_id, workspace_id, author_type, author_id, content, type)
		VALUES ($1, $2, 'member', $3, 'please take a look', 'comment')
		RETURNING id
	`, issueID, testWorkspaceID, testUserID).Scan(&triggerCommentID); err != nil {
		t.Fatalf("setup: create trigger comment: %v", err)
	}

	// Comment-triggered, already running (as CompleteAgentTask requires).
	var taskID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_task_queue (
			agent_id, runtime_id, issue_id, trigger_comment_id,
			status, priority, started_at
		)
		VALUES ($1, $2, $3, $4, 'running', 0, now())
		RETURNING id
	`, agentID, runtimeID, issueID, triggerCommentID).Scan(&taskID); err != nil {
		t.Fatalf("setup: create comment-triggered task: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM agent_task_queue WHERE id = $1`, taskID) })

	const agentFinalOutput = "sure, will look into it shortly"

	w := httptest.NewRecorder()
	req := newDaemonTokenRequest("POST", "/api/daemon/tasks/"+taskID+"/complete",
		map[string]any{"output": agentFinalOutput},
		testWorkspaceID, "legit-daemon")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("taskId", taskID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	testHandler.CompleteTask(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("CompleteTask: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Exactly one agent comment on the issue, threaded under the trigger,
	// carrying the agent's final output.
	rows, err := testPool.Query(ctx, `
		SELECT content, parent_id FROM comment
		WHERE issue_id = $1 AND author_type = 'agent' AND author_id = $2
		ORDER BY created_at ASC
	`, issueID, agentID)
	if err != nil {
		t.Fatalf("query synthesized comments: %v", err)
	}
	defer rows.Close()

	var (
		content  string
		parentID *string
		seen     int
	)
	for rows.Next() {
		if err := rows.Scan(&content, &parentID); err != nil {
			t.Fatalf("scan comment: %v", err)
		}
		seen++
	}
	if seen != 1 {
		t.Fatalf("expected exactly 1 synthesized agent comment, got %d", seen)
	}
	if content != agentFinalOutput {
		t.Fatalf("synthesized comment content = %q, want %q", content, agentFinalOutput)
	}
	if parentID == nil || *parentID != triggerCommentID {
		got := "<nil>"
		if parentID != nil {
			got = *parentID
		}
		t.Fatalf("synthesized comment parent_id = %s, want trigger comment %s", got, triggerCommentID)
	}
}

// Companion to the above: when the agent DID post its own comment during the
// run, CompleteTask must not synthesize a duplicate. Guards against the
// common case where the fix is over-eager and creates two comments per task.
func TestCompleteTask_CommentTriggered_SkipsSynthesisWhenAgentAlreadyCommented(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()

	var agentID, runtimeID string
	if err := testPool.QueryRow(ctx, `
		SELECT a.id, a.runtime_id FROM agent a WHERE a.workspace_id = $1 LIMIT 1
	`, testWorkspaceID).Scan(&agentID, &runtimeID); err != nil {
		t.Fatalf("setup: get agent: %v", err)
	}

	var issueID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO issue (workspace_id, title, status, priority, creator_id, creator_type, number, position)
		VALUES ($1, 'mul-1198 dedup fixture', 'in_progress', 'none', $2, 'member', 81199, 0)
		RETURNING id
	`, testWorkspaceID, testUserID).Scan(&issueID); err != nil {
		t.Fatalf("setup: create issue: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM issue WHERE id = $1`, issueID) })

	var triggerCommentID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO comment (issue_id, workspace_id, author_type, author_id, content, type)
		VALUES ($1, $2, 'member', $3, 'please take a look', 'comment')
		RETURNING id
	`, issueID, testWorkspaceID, testUserID).Scan(&triggerCommentID); err != nil {
		t.Fatalf("setup: create trigger comment: %v", err)
	}

	var taskID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_task_queue (
			agent_id, runtime_id, issue_id, trigger_comment_id,
			status, priority, started_at
		)
		VALUES ($1, $2, $3, $4, 'running', 0, now())
		RETURNING id
	`, agentID, runtimeID, issueID, triggerCommentID).Scan(&taskID); err != nil {
		t.Fatalf("setup: create comment-triggered task: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM agent_task_queue WHERE id = $1`, taskID) })

	// Agent posts its own reply during the run — exactly the compliant path.
	if _, err := testPool.Exec(ctx, `
		INSERT INTO comment (issue_id, workspace_id, author_type, author_id, content, type, parent_id)
		VALUES ($1, $2, 'agent', $3, 'done, see PR', 'comment', $4)
	`, issueID, testWorkspaceID, agentID, triggerCommentID); err != nil {
		t.Fatalf("setup: create agent reply: %v", err)
	}

	w := httptest.NewRecorder()
	req := newDaemonTokenRequest("POST", "/api/daemon/tasks/"+taskID+"/complete",
		map[string]any{"output": "final terminal text that must NOT become a comment"},
		testWorkspaceID, "legit-daemon")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("taskId", taskID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	testHandler.CompleteTask(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("CompleteTask: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var count int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM comment
		WHERE issue_id = $1 AND author_type = 'agent' AND author_id = $2
	`, issueID, agentID).Scan(&count); err != nil {
		t.Fatalf("count agent comments: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 agent comment (the agent's own reply), got %d — synthesis duplicated", count)
	}
}

func TestCompleteTask_CommentTriggered_SuppressesTrivialDoneOutput(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()

	var agentID, runtimeID string
	if err := testPool.QueryRow(ctx, `
		SELECT a.id, a.runtime_id FROM agent a WHERE a.workspace_id = $1 LIMIT 1
	`, testWorkspaceID).Scan(&agentID, &runtimeID); err != nil {
		t.Fatalf("setup: get agent: %v", err)
	}

	var issueID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO issue (workspace_id, title, status, priority, creator_id, creator_type, number, position)
		VALUES ($1, 'trivial-done-suppression fixture', 'in_progress', 'none', $2, 'member', 81200, 0)
		RETURNING id
	`, testWorkspaceID, testUserID).Scan(&issueID); err != nil {
		t.Fatalf("setup: create issue: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM issue WHERE id = $1`, issueID) })

	var triggerCommentID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO comment (issue_id, workspace_id, author_type, author_id, content, type)
		VALUES ($1, $2, 'member', $3, 'please follow up', 'comment')
		RETURNING id
	`, issueID, testWorkspaceID, testUserID).Scan(&triggerCommentID); err != nil {
		t.Fatalf("setup: create trigger comment: %v", err)
	}

	var taskID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_task_queue (
			agent_id, runtime_id, issue_id, trigger_comment_id,
			status, priority, started_at
		)
		VALUES ($1, $2, $3, $4, 'running', 0, now())
		RETURNING id
	`, agentID, runtimeID, issueID, triggerCommentID).Scan(&taskID); err != nil {
		t.Fatalf("setup: create comment-triggered task: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM agent_task_queue WHERE id = $1`, taskID) })

	w := httptest.NewRecorder()
	req := newDaemonTokenRequest("POST", "/api/daemon/tasks/"+taskID+"/complete",
		map[string]any{"output": "Done."},
		testWorkspaceID, "legit-daemon")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("taskId", taskID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	testHandler.CompleteTask(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("CompleteTask: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var count int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM comment
		WHERE issue_id = $1 AND author_type = 'agent' AND author_id = $2
	`, issueID, agentID).Scan(&count); err != nil {
		t.Fatalf("count agent comments: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected no synthesized agent comment for trivial Done output, got %d", count)
	}
}

func TestCompleteTask_AssignmentTriggered_DoesNotSuppressTrivialDoneOutput(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()

	var agentID, runtimeID string
	if err := testPool.QueryRow(ctx, `
		SELECT a.id, a.runtime_id FROM agent a WHERE a.workspace_id = $1 LIMIT 1
	`, testWorkspaceID).Scan(&agentID, &runtimeID); err != nil {
		t.Fatalf("setup: get agent: %v", err)
	}

	var issueID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO issue (workspace_id, title, status, priority, creator_id, creator_type, number, position)
		VALUES ($1, 'assignment-trivial-done fixture', 'in_progress', 'none', $2, 'member', 81201, 0)
		RETURNING id
	`, testWorkspaceID, testUserID).Scan(&issueID); err != nil {
		t.Fatalf("setup: create issue: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM issue WHERE id = $1`, issueID) })

	var taskID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_task_queue (
			agent_id, runtime_id, issue_id,
			status, priority, started_at
		)
		VALUES ($1, $2, $3, 'running', 0, now())
		RETURNING id
	`, agentID, runtimeID, issueID).Scan(&taskID); err != nil {
		t.Fatalf("setup: create assignment-triggered task: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM agent_task_queue WHERE id = $1`, taskID) })

	w := httptest.NewRecorder()
	req := newDaemonTokenRequest("POST", "/api/daemon/tasks/"+taskID+"/complete",
		map[string]any{"output": "Done."},
		testWorkspaceID, "legit-daemon")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("taskId", taskID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	testHandler.CompleteTask(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("CompleteTask: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var content string
	if err := testPool.QueryRow(ctx, `
		SELECT content FROM comment
		WHERE issue_id = $1 AND author_type = 'agent' AND author_id = $2
		ORDER BY created_at DESC LIMIT 1
	`, issueID, agentID).Scan(&content); err != nil {
		t.Fatalf("query synthesized comment: %v", err)
	}
	if content != "Done." {
		t.Fatalf("synthesized comment content = %q, want Done.", content)
	}
}

type claimRuntimeGuardTask struct {
	PriorSessionID           string   `json:"prior_session_id"`
	PriorWorkDir             string   `json:"prior_work_dir"`
	ChatMessage              string   `json:"chat_message"`
	ThreadName               string   `json:"thread_name"`
	QuickCreateAttachmentIDs []string `json:"quick_create_attachment_ids"`
}

func claimTaskForRuntimeGuard(t *testing.T, runtimeID, daemonID string) *claimRuntimeGuardTask {
	t.Helper()

	w := httptest.NewRecorder()
	req := newDaemonTokenRequest("POST", "/api/daemon/runtimes/"+runtimeID+"/claim", nil,
		testWorkspaceID, daemonID)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("runtimeId", runtimeID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	testHandler.ClaimTaskByRuntime(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("ClaimTaskByRuntime: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Task *claimRuntimeGuardTask `json:"task"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Task == nil {
		t.Fatal("expected a task in response, got nil")
	}
	return resp.Task
}

func createRuntimeGuardAgent(t *testing.T, ctx context.Context) (agentID, runtimeID, daemonID string) {
	t.Helper()

	daemonID = "runtime-guard-" + strings.ToLower(strings.ReplaceAll(t.Name(), "/", "-"))
	w := httptest.NewRecorder()
	req := newDaemonTokenRequest("POST", "/api/daemon/register", map[string]any{
		"workspace_id": testWorkspaceID,
		"daemon_id":    daemonID,
		"device_name":  "runtime-guard-test",
		"runtimes": []map[string]any{
			{"name": "runtime-guard-current", "type": "opencode", "version": "test", "status": "online"},
		},
	}, testWorkspaceID, daemonID)

	testHandler.DaemonRegister(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("setup: DaemonRegister: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("setup: decode DaemonRegister response: %v", err)
	}
	runtimes, ok := resp["runtimes"].([]any)
	if !ok || len(runtimes) == 0 {
		t.Fatalf("setup: expected registered runtime, got %v", resp)
	}
	runtimeID = runtimes[0].(map[string]any)["id"].(string)
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM agent_runtime WHERE id = $1`, runtimeID) })

	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent (
			workspace_id, name, runtime_mode, runtime_config,
			runtime_id, visibility, max_concurrent_tasks
		)
		VALUES ($1, $2, 'local', '{}'::jsonb, $3, 'workspace', 3)
		RETURNING id
	`, testWorkspaceID, "Runtime Guard Agent "+t.Name(), runtimeID).Scan(&agentID); err != nil {
		t.Fatalf("setup: create runtime guard agent: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM agent WHERE id = $1`, agentID) })

	return agentID, runtimeID, daemonID
}

func createRuntimeGuardRuntime(t *testing.T, ctx context.Context, provider string) string {
	t.Helper()

	var runtimeID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_runtime (
			workspace_id, daemon_id, name, runtime_mode, provider, status,
			device_info, metadata, owner_id, last_seen_at
		)
		VALUES ($1, 'runtime-guard-' || gen_random_uuid()::text, 'Runtime Guard Fixture',
		        'local', $2, 'offline', '{}'::jsonb, '{}'::jsonb, $3, now())
		RETURNING id
	`, testWorkspaceID, provider, testUserID).Scan(&runtimeID); err != nil {
		t.Fatalf("setup: create runtime: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM agent_runtime WHERE id = $1`, runtimeID) })
	return runtimeID
}

func TestChatSessionRuntimeBackfillRequiresMatchingSessionID(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	tx, err := testPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `
		CREATE TEMP TABLE chat_session (
			id uuid PRIMARY KEY,
			session_id text,
			runtime_id uuid
		) ON COMMIT DROP;
	`); err != nil {
		t.Fatalf("setup temp chat_session table: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		CREATE TEMP TABLE agent_task_queue (
			chat_session_id uuid,
			runtime_id uuid,
			session_id text,
			status text,
			completed_at timestamptz,
			started_at timestamptz,
			dispatched_at timestamptz,
			created_at timestamptz
		) ON COMMIT DROP;
	`); err != nil {
		t.Fatalf("setup temp agent_task_queue table: %v", err)
	}

	const (
		poisonedChatID = "00000000-0000-0000-0000-000000000101"
		matchedChatID  = "00000000-0000-0000-0000-000000000102"
		oldRuntimeID   = "00000000-0000-0000-0000-000000000201"
		newRuntimeID   = "00000000-0000-0000-0000-000000000202"
	)

	if _, err := tx.Exec(ctx, `
		INSERT INTO chat_session (id, session_id, runtime_id)
		VALUES
			($1, 'old-runtime-session', NULL),
			($2, 'matched-runtime-session', NULL);
	`, poisonedChatID, matchedChatID); err != nil {
		t.Fatalf("seed temp chat sessions: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO agent_task_queue (
			chat_session_id, runtime_id, session_id, status,
			completed_at, started_at, dispatched_at, created_at
		)
		VALUES
			($1, $3, 'old-runtime-session', 'completed',
			 now() - interval '2 hours', now() - interval '2 hours', now() - interval '2 hours', now() - interval '2 hours'),
			($1, $4, 'new-runtime-session', 'completed',
			 now() - interval '1 hour', now() - interval '1 hour', now() - interval '1 hour', now() - interval '1 hour'),
			($2, $4, 'matched-runtime-session', 'completed',
			 now() - interval '30 minutes', now() - interval '30 minutes', now() - interval '30 minutes', now() - interval '30 minutes');
	`, poisonedChatID, matchedChatID, oldRuntimeID, newRuntimeID); err != nil {
		t.Fatalf("seed temp task sessions: %v", err)
	}

	if _, err := tx.Exec(ctx, `
		UPDATE chat_session cs
		SET runtime_id = latest.runtime_id
		FROM (
			SELECT DISTINCT ON (chat_session_id)
				chat_session_id,
				runtime_id,
				session_id
			FROM agent_task_queue
			WHERE chat_session_id IS NOT NULL
			  AND session_id IS NOT NULL
			  AND status IN ('completed', 'failed')
			ORDER BY chat_session_id, COALESCE(completed_at, started_at, dispatched_at, created_at) DESC
		) latest
		WHERE latest.chat_session_id = cs.id
		  AND latest.session_id = cs.session_id
	`); err != nil {
		t.Fatalf("run runtime backfill: %v", err)
	}

	var poisonedRuntimeID *string
	if err := tx.QueryRow(ctx, `
		SELECT runtime_id::text FROM chat_session WHERE id = $1
	`, poisonedChatID).Scan(&poisonedRuntimeID); err != nil {
		t.Fatalf("query poisoned chat runtime: %v", err)
	}
	if poisonedRuntimeID != nil {
		t.Fatalf("expected stale session mismatch to remain NULL, got %s", *poisonedRuntimeID)
	}

	var matchedRuntimeID string
	if err := tx.QueryRow(ctx, `
		SELECT runtime_id::text FROM chat_session WHERE id = $1
	`, matchedChatID).Scan(&matchedRuntimeID); err != nil {
		t.Fatalf("query matched chat runtime: %v", err)
	}
	if matchedRuntimeID != newRuntimeID {
		t.Fatalf("expected matched session to backfill runtime %s, got %s", newRuntimeID, matchedRuntimeID)
	}
}

func TestClaimTask_IssuePriorSessionRuntimeGuard(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()

	agentID, runtimeID, daemonID := createRuntimeGuardAgent(t, ctx)
	oldRuntimeID := createRuntimeGuardRuntime(t, ctx, "kimi")

	var skipIssueID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO issue (workspace_id, title, status, priority, creator_id, creator_type, number, position)
		VALUES ($1, 'runtime-session-skip fixture', 'in_progress', 'none', $2, 'member', 81203, 0)
		RETURNING id
	`, testWorkspaceID, testUserID).Scan(&skipIssueID); err != nil {
		t.Fatalf("setup: create skip issue: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM issue WHERE id = $1`, skipIssueID) })

	if _, err := testPool.Exec(ctx, `
		INSERT INTO agent_task_queue (
			agent_id, runtime_id, issue_id,
			status, priority, started_at, completed_at,
			session_id, work_dir
		)
		VALUES ($1, $2, $3, 'completed', 0, now(), now(), 'old-runtime-session', '/tmp/old-runtime-workdir')
	`, agentID, oldRuntimeID, skipIssueID); err != nil {
		t.Fatalf("setup: create old-runtime prior task: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO agent_task_queue (
			agent_id, runtime_id, issue_id,
			status, priority
		)
		VALUES ($1, $2, $3, 'queued', 0)
	`, agentID, runtimeID, skipIssueID); err != nil {
		t.Fatalf("setup: create current-runtime task: %v", err)
	}

	task := claimTaskForRuntimeGuard(t, runtimeID, daemonID)
	if task.PriorSessionID != "" {
		t.Fatalf("runtime mismatch: expected empty PriorSessionID, got %q", task.PriorSessionID)
	}
	if task.PriorWorkDir != "/tmp/old-runtime-workdir" {
		t.Fatalf("runtime mismatch: expected PriorWorkDir='/tmp/old-runtime-workdir', got %q", task.PriorWorkDir)
	}
	if task.ThreadName != "runtime-session-skip fixture" {
		t.Fatalf("issue task thread_name = %q, want issue title", task.ThreadName)
	}
	if _, err := testPool.Exec(ctx, `
		UPDATE agent_task_queue
		SET status = 'completed', completed_at = now()
		WHERE issue_id = $1 AND status IN ('dispatched', 'running')
	`, skipIssueID); err != nil {
		t.Fatalf("setup: complete claimed skip task: %v", err)
	}

	var resumeIssueID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO issue (workspace_id, title, status, priority, creator_id, creator_type, number, position)
		VALUES ($1, 'runtime-session-resume fixture', 'in_progress', 'none', $2, 'member', 81204, 0)
		RETURNING id
	`, testWorkspaceID, testUserID).Scan(&resumeIssueID); err != nil {
		t.Fatalf("setup: create resume issue: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM issue WHERE id = $1`, resumeIssueID) })

	if _, err := testPool.Exec(ctx, `
		INSERT INTO agent_task_queue (
			agent_id, runtime_id, issue_id,
			status, priority, started_at, completed_at,
			session_id, work_dir
		)
		VALUES ($1, $2, $3, 'completed', 0, now(), now(), 'same-runtime-session', '/tmp/same-runtime-workdir')
	`, agentID, runtimeID, resumeIssueID); err != nil {
		t.Fatalf("setup: create same-runtime prior task: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO agent_task_queue (
			agent_id, runtime_id, issue_id,
			status, priority
		)
		VALUES ($1, $2, $3, 'queued', 0)
	`, agentID, runtimeID, resumeIssueID); err != nil {
		t.Fatalf("setup: create same-runtime task: %v", err)
	}

	task = claimTaskForRuntimeGuard(t, runtimeID, daemonID)
	if task.PriorSessionID != "same-runtime-session" {
		t.Fatalf("runtime match: expected PriorSessionID='same-runtime-session', got %q", task.PriorSessionID)
	}
	if task.PriorWorkDir != "/tmp/same-runtime-workdir" {
		t.Fatalf("runtime match: expected PriorWorkDir='/tmp/same-runtime-workdir', got %q", task.PriorWorkDir)
	}

	var commentIssueID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO issue (workspace_id, title, status, priority, creator_id, creator_type, number, position)
		VALUES ($1, 'comment-triggered-session-skip fixture', 'in_progress', 'none', $2, 'member', 81205, 0)
		RETURNING id
	`, testWorkspaceID, testUserID).Scan(&commentIssueID); err != nil {
		t.Fatalf("setup: create comment-triggered issue: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM issue WHERE id = $1`, commentIssueID) })

	var triggerCommentID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO comment (issue_id, workspace_id, author_type, author_id, content, type)
		VALUES ($1, $2, 'member', $3, 'please follow up', 'comment')
		RETURNING id
	`, commentIssueID, testWorkspaceID, testUserID).Scan(&triggerCommentID); err != nil {
		t.Fatalf("setup: create trigger comment: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO agent_task_queue (
			agent_id, runtime_id, issue_id,
			status, priority, started_at, completed_at,
			session_id, work_dir
		)
		VALUES ($1, $2, $3, 'completed', 0, now(), now(), 'comment-prior-session', '/tmp/comment-prior-workdir')
	`, agentID, runtimeID, commentIssueID); err != nil {
		t.Fatalf("setup: create comment-trigger prior task: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO agent_task_queue (
			agent_id, runtime_id, issue_id, trigger_comment_id,
			status, priority
		)
		VALUES ($1, $2, $3, $4, 'queued', 0)
	`, agentID, runtimeID, commentIssueID, triggerCommentID); err != nil {
		t.Fatalf("setup: create comment-triggered task: %v", err)
	}

	task = claimTaskForRuntimeGuard(t, runtimeID, daemonID)
	// Comment-triggered tasks now resume the prior session by default (same
	// runtime), so the agent keeps the issue's conversation context across turns.
	if task.PriorSessionID != "comment-prior-session" {
		t.Fatalf("comment trigger: expected PriorSessionID='comment-prior-session' (resume default-on), got %q", task.PriorSessionID)
	}
	if task.PriorWorkDir != "/tmp/comment-prior-workdir" {
		t.Fatalf("comment trigger: expected PriorWorkDir='/tmp/comment-prior-workdir', got %q", task.PriorWorkDir)
	}
	if _, err := testPool.Exec(ctx, `
		UPDATE agent_task_queue
		SET status = 'completed', completed_at = now()
		WHERE issue_id = $1 AND status IN ('dispatched', 'running')
	`, commentIssueID); err != nil {
		t.Fatalf("setup: complete claimed comment-trigger task: %v", err)
	}

	var freshIssueID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO issue (workspace_id, title, status, priority, creator_id, creator_type, number, position)
		VALUES ($1, 'force-fresh-session fixture', 'in_progress', 'none', $2, 'member', 81206, 0)
		RETURNING id
	`, testWorkspaceID, testUserID).Scan(&freshIssueID); err != nil {
		t.Fatalf("setup: create force-fresh issue: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM issue WHERE id = $1`, freshIssueID) })
	if _, err := testPool.Exec(ctx, `
		INSERT INTO agent_task_queue (
			agent_id, runtime_id, issue_id,
			status, priority, started_at, completed_at,
			session_id, work_dir
		)
		VALUES ($1, $2, $3, 'completed', 0, now(), now(), 'force-fresh-prior-session', '/tmp/force-fresh-prior-workdir')
	`, agentID, runtimeID, freshIssueID); err != nil {
		t.Fatalf("setup: create force-fresh prior task: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO agent_task_queue (
			agent_id, runtime_id, issue_id,
			status, priority, force_fresh_session
		)
		VALUES ($1, $2, $3, 'queued', 0, TRUE)
	`, agentID, runtimeID, freshIssueID); err != nil {
		t.Fatalf("setup: create force-fresh task: %v", err)
	}

	task = claimTaskForRuntimeGuard(t, runtimeID, daemonID)
	if task.PriorSessionID != "" {
		t.Fatalf("force fresh: expected empty PriorSessionID, got %q", task.PriorSessionID)
	}
	if task.PriorWorkDir != "" {
		t.Fatalf("force fresh: expected empty PriorWorkDir, got %q", task.PriorWorkDir)
	}
}

func TestClaimTask_ChatPriorSessionRuntimeGuard(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()

	agentID, runtimeID, daemonID := createRuntimeGuardAgent(t, ctx)
	oldRuntimeID := createRuntimeGuardRuntime(t, ctx, "kimi")

	var skipSessionID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO chat_session (
			workspace_id, agent_id, creator_id, title,
			session_id, work_dir, runtime_id
		)
		VALUES ($1, $2, $3, 'runtime guard skip chat', 'old-chat-session', '/tmp/old-chat-workdir', $4)
		RETURNING id
	`, testWorkspaceID, agentID, testUserID, oldRuntimeID).Scan(&skipSessionID); err != nil {
		t.Fatalf("setup: create skip chat session: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM chat_session WHERE id = $1`, skipSessionID) })

	if _, err := testPool.Exec(ctx, `
		INSERT INTO agent_task_queue (
			agent_id, runtime_id, chat_session_id,
			status, priority, started_at, completed_at,
			session_id, work_dir
		)
		VALUES ($1, $2, $3, 'completed', 0, now(), now(), 'old-chat-session', '/tmp/old-chat-workdir')
	`, agentID, oldRuntimeID, skipSessionID); err != nil {
		t.Fatalf("setup: create old-runtime chat task: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO agent_task_queue (
			agent_id, runtime_id, chat_session_id,
			status, priority
		)
		VALUES ($1, $2, $3, 'queued', 0)
	`, agentID, runtimeID, skipSessionID); err != nil {
		t.Fatalf("setup: create current-runtime chat task: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO chat_message (chat_session_id, role, content)
		VALUES ($1, 'user', 'runtime guard skip chat message')
	`, skipSessionID); err != nil {
		t.Fatalf("setup: insert skip chat user message: %v", err)
	}

	task := claimTaskForRuntimeGuard(t, runtimeID, daemonID)
	if task.PriorSessionID != "" {
		t.Fatalf("chat runtime mismatch: expected empty PriorSessionID, got %q", task.PriorSessionID)
	}
	if task.PriorWorkDir != "/tmp/old-chat-workdir" {
		t.Fatalf("chat runtime mismatch: expected PriorWorkDir='/tmp/old-chat-workdir', got %q", task.PriorWorkDir)
	}
	if _, err := testPool.Exec(ctx, `
		UPDATE agent_task_queue
		SET status = 'completed', completed_at = now()
		WHERE chat_session_id = $1 AND status IN ('dispatched', 'running')
	`, skipSessionID); err != nil {
		t.Fatalf("setup: complete claimed skip chat task: %v", err)
	}

	var resumeSessionID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO chat_session (
			workspace_id, agent_id, creator_id, title,
			session_id, work_dir, runtime_id
		)
		VALUES ($1, $2, $3, 'runtime guard resume chat', 'same-chat-session', '/tmp/same-chat-workdir', $4)
		RETURNING id
	`, testWorkspaceID, agentID, testUserID, runtimeID).Scan(&resumeSessionID); err != nil {
		t.Fatalf("setup: create resume chat session: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM chat_session WHERE id = $1`, resumeSessionID) })

	if _, err := testPool.Exec(ctx, `
		INSERT INTO agent_task_queue (
			agent_id, runtime_id, chat_session_id,
			status, priority
		)
		VALUES ($1, $2, $3, 'queued', 0)
	`, agentID, runtimeID, resumeSessionID); err != nil {
		t.Fatalf("setup: create same-runtime chat task: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO chat_message (chat_session_id, role, content)
		VALUES ($1, 'user', 'runtime guard resume chat message')
	`, resumeSessionID); err != nil {
		t.Fatalf("setup: insert resume chat user message: %v", err)
	}

	task = claimTaskForRuntimeGuard(t, runtimeID, daemonID)
	if task.PriorSessionID != "same-chat-session" {
		t.Fatalf("chat runtime match: expected PriorSessionID='same-chat-session', got %q", task.PriorSessionID)
	}
	if task.PriorWorkDir != "/tmp/same-chat-workdir" {
		t.Fatalf("chat runtime match: expected PriorWorkDir='/tmp/same-chat-workdir', got %q", task.PriorWorkDir)
	}
}

// TestClaimTask_ChatDeliversAllUnansweredUserMessages pins the fix for the
// regression the MUL-2968 debounce exposed: when several user messages are
// debounced into a single run, the agent must receive ALL of them, not just
// the most recent. Before the fix the daemon prompt was the single latest
// user message, so "看上海天气" then "还有青岛" answered only Qingdao.
func TestClaimTask_ChatDeliversAllUnansweredUserMessages(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentID, runtimeID, daemonID := createRuntimeGuardAgent(t, ctx)

	var sessionID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO chat_session (workspace_id, agent_id, creator_id, title)
		VALUES ($1, $2, $3, 'debounce delivery chat')
		RETURNING id
	`, testWorkspaceID, agentID, testUserID).Scan(&sessionID); err != nil {
		t.Fatalf("setup: create chat session: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM chat_session WHERE id = $1`, sessionID) })

	// Two user messages debounced into one run (explicit created_at so the
	// ASC ordering is deterministic).
	if _, err := testPool.Exec(ctx, `
		INSERT INTO chat_message (chat_session_id, role, content, created_at) VALUES
			($1, 'user', '看上海天气', now()),
			($1, 'user', '还有青岛',   now() + interval '1 second')
	`, sessionID); err != nil {
		t.Fatalf("setup: insert user messages: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO agent_task_queue (agent_id, runtime_id, chat_session_id, status, priority)
		VALUES ($1, $2, $3, 'queued', 2)
	`, agentID, runtimeID, sessionID); err != nil {
		t.Fatalf("setup: create chat task: %v", err)
	}

	task := claimTaskForRuntimeGuard(t, runtimeID, daemonID)
	if task.ChatMessage != "看上海天气\n\n还有青岛" {
		t.Fatalf("chat prompt must include every unanswered user message in order; got %q", task.ChatMessage)
	}
	if task.ThreadName != "debounce delivery chat" {
		t.Fatalf("chat task thread_name = %q, want chat session title", task.ThreadName)
	}

	// Complete the run and record the agent's assistant reply, then send a
	// fresh user message — only the new one should be delivered next.
	if _, err := testPool.Exec(ctx, `
		UPDATE agent_task_queue SET status = 'completed', completed_at = now()
		WHERE chat_session_id = $1 AND status IN ('dispatched', 'running')
	`, sessionID); err != nil {
		t.Fatalf("setup: complete first chat task: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO chat_message (chat_session_id, role, content, created_at)
		VALUES ($1, 'assistant', '上海与青岛天气如下…', now() + interval '2 second')
	`, sessionID); err != nil {
		t.Fatalf("setup: insert assistant reply: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO chat_message (chat_session_id, role, content, created_at)
		VALUES ($1, 'user', '深圳呢', now() + interval '3 second')
	`, sessionID); err != nil {
		t.Fatalf("setup: insert follow-up user message: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO agent_task_queue (agent_id, runtime_id, chat_session_id, status, priority)
		VALUES ($1, $2, $3, 'queued', 2)
	`, agentID, runtimeID, sessionID); err != nil {
		t.Fatalf("setup: create follow-up chat task: %v", err)
	}

	task = claimTaskForRuntimeGuard(t, runtimeID, daemonID)
	if task.ChatMessage != "深圳呢" {
		t.Fatalf("after a reply, only the new user message must be delivered; got %q", task.ChatMessage)
	}
}

// TestClaimTask_ChatPopulatesInitiator verifies MUL-2645 for chat tasks: the
// claim response surfaces the STORED task initiator (initiator_user_id captured
// at enqueue), NOT chat_session.creator_id. This is the MUL-2645 review fix: for
// Lark group chats the session creator is the installer, not the sender, so the
// claim must read the stored sender. The test pins this by making the creator a
// DIFFERENT user (the "installer") from the stored initiator (the sender) and
// asserting the claim resolves the sender.
func TestClaimTask_ChatPopulatesInitiator(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	agentID, runtimeID, daemonID := createRuntimeGuardAgent(t, ctx)

	// A separate user stands in for the Lark group session creator (installer).
	var installerID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO "user" (name, email) VALUES ('Installer User', 'installer-test@multica.ai')
		RETURNING id
	`).Scan(&installerID); err != nil {
		t.Fatalf("setup: create installer user: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM "user" WHERE id = $1`, installerID) })

	var sessionID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO chat_session (workspace_id, agent_id, creator_id, title)
		VALUES ($1, $2, $3, 'initiator chat')
		RETURNING id
	`, testWorkspaceID, agentID, installerID).Scan(&sessionID); err != nil {
		t.Fatalf("setup: create chat session: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM chat_session WHERE id = $1`, sessionID) })

	if _, err := testPool.Exec(ctx, `
		INSERT INTO chat_message (chat_session_id, role, content) VALUES ($1, 'user', 'hi there')
	`, sessionID); err != nil {
		t.Fatalf("setup: insert user message: %v", err)
	}
	// initiator_user_id = the real sender (testUserID), distinct from creator.
	if _, err := testPool.Exec(ctx, `
		INSERT INTO agent_task_queue (agent_id, runtime_id, chat_session_id, status, priority, initiator_user_id)
		VALUES ($1, $2, $3, 'queued', 2, $4)
	`, agentID, runtimeID, sessionID, testUserID); err != nil {
		t.Fatalf("setup: create chat task: %v", err)
	}

	w := httptest.NewRecorder()
	req := newDaemonTokenRequest("POST", "/api/daemon/runtimes/"+runtimeID+"/tasks/claim", nil, testWorkspaceID, daemonID)
	req = withURLParam(req, "runtimeId", runtimeID)
	testHandler.ClaimTaskByRuntime(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("ClaimTaskByRuntime: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Task *struct {
			InitiatorType  string `json:"initiator_type"`
			InitiatorID    string `json:"initiator_id"`
			InitiatorName  string `json:"initiator_name"`
			InitiatorEmail string `json:"initiator_email"`
		} `json:"task"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode claim response: %v", err)
	}
	if resp.Task == nil {
		t.Fatalf("expected a claimed task, got %s", w.Body.String())
	}
	if resp.Task.InitiatorType != "member" || resp.Task.InitiatorID != testUserID ||
		resp.Task.InitiatorName != handlerTestName || resp.Task.InitiatorEmail != handlerTestEmail {
		t.Errorf("chat initiator = {type:%q id:%q name:%q email:%q}, want {member %q %q %q}",
			resp.Task.InitiatorType, resp.Task.InitiatorID, resp.Task.InitiatorName, resp.Task.InitiatorEmail,
			testUserID, handlerTestName, handlerTestEmail)
	}
}

func TestClaimTask_ChatBoundProjectSurfacesResources(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	agentID, runtimeID, daemonID := createRuntimeGuardAgent(t, ctx)

	// A project with a github_repo resource, bound to the chat session via
	// chat_session.project_id — the chat agent should work in that project and
	// see its repo, exactly like an issue task does.
	var projectID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO project (workspace_id, title) VALUES ($1, $2) RETURNING id
	`, testWorkspaceID, "Chat bound project").Scan(&projectID); err != nil {
		t.Fatalf("setup: create project: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM project WHERE id = $1`, projectID) })

	const projectRepoURL = "https://github.com/example/chat-bound-repo"
	if _, err := testPool.Exec(ctx, `
		INSERT INTO project_resource (project_id, workspace_id, resource_type, resource_ref, position)
		VALUES ($1, $2, 'github_repo', $3::jsonb, 0)
	`, projectID, testWorkspaceID, `{"url":"`+projectRepoURL+`"}`); err != nil {
		t.Fatalf("setup: create project_resource: %v", err)
	}

	var sessionID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO chat_session (workspace_id, agent_id, creator_id, title, project_id)
		VALUES ($1, $2, $3, 'bound chat', $4) RETURNING id
	`, testWorkspaceID, agentID, testUserID, projectID).Scan(&sessionID); err != nil {
		t.Fatalf("setup: create chat session: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM chat_session WHERE id = $1`, sessionID) })

	if _, err := testPool.Exec(ctx, `
		INSERT INTO chat_message (chat_session_id, role, content) VALUES ($1, 'user', 'work on the project')
	`, sessionID); err != nil {
		t.Fatalf("setup: insert user message: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO agent_task_queue (agent_id, runtime_id, chat_session_id, status, priority, initiator_user_id)
		VALUES ($1, $2, $3, 'queued', 2, $4)
	`, agentID, runtimeID, sessionID, testUserID); err != nil {
		t.Fatalf("setup: create chat task: %v", err)
	}

	w := httptest.NewRecorder()
	req := newDaemonTokenRequest("POST", "/api/daemon/runtimes/"+runtimeID+"/tasks/claim", nil, testWorkspaceID, daemonID)
	req = withURLParam(req, "runtimeId", runtimeID)
	testHandler.ClaimTaskByRuntime(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("ClaimTaskByRuntime: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Task *struct {
			Repos                   []RepoData            `json:"repos"`
			ProjectID               string                `json:"project_id"`
			ProjectResources        []ProjectResourceData `json:"project_resources"`
			ProvisionManagedWorkdir bool                  `json:"provision_managed_workdir"`
		} `json:"task"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode claim response: %v", err)
	}
	if resp.Task == nil {
		t.Fatalf("expected a claimed task, got %s", w.Body.String())
	}
	if resp.Task.ProjectID != projectID {
		t.Errorf("project_id = %q, want %q", resp.Task.ProjectID, projectID)
	}
	if resp.Task.ProvisionManagedWorkdir {
		t.Errorf("did not expect provision flag when the bound project already has a resource")
	}
	foundRepo := false
	for _, repo := range resp.Task.Repos {
		if repo.URL == projectRepoURL {
			foundRepo = true
		}
	}
	if !foundRepo {
		t.Errorf("expected chat repos to include the bound project repo %q, got %+v", projectRepoURL, resp.Task.Repos)
	}
}

func TestClaimTask_IssueProjectProvisionsManagedWorkdir(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	agentID, runtimeID, daemonID := createRuntimeGuardAgent(t, ctx)

	// A project with NO resources → the claim should ask the daemon to
	// provision a managed shared workdir at projects/<id>/workdir.
	var projectID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO project (workspace_id, title) VALUES ($1, $2) RETURNING id
	`, testWorkspaceID, "Managed workdir project").Scan(&projectID); err != nil {
		t.Fatalf("setup: create project: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM project WHERE id = $1`, projectID) })

	var issueID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO issue (workspace_id, project_id, title, status, priority, creator_id, creator_type, number, position)
		VALUES ($1, $2, 'managed workdir issue', 'todo', 'medium', $3, 'member', 88010, 0)
		RETURNING id
	`, testWorkspaceID, projectID, testUserID).Scan(&issueID); err != nil {
		t.Fatalf("setup: create issue: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM issue WHERE id = $1`, issueID) })

	var taskID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_task_queue (agent_id, runtime_id, issue_id, status, priority)
		VALUES ($1, $2, $3, 'queued', 0) RETURNING id
	`, agentID, runtimeID, issueID).Scan(&taskID); err != nil {
		t.Fatalf("setup: create task: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM agent_task_queue WHERE id = $1`, taskID) })

	w := httptest.NewRecorder()
	req := newDaemonTokenRequest("POST", "/api/daemon/runtimes/"+runtimeID+"/tasks/claim", nil, testWorkspaceID, daemonID)
	req = withURLParam(req, "runtimeId", runtimeID)
	testHandler.ClaimTaskByRuntime(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("ClaimTaskByRuntime: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Task *struct {
			ProjectID               string `json:"project_id"`
			ProvisionManagedWorkdir bool   `json:"provision_managed_workdir"`
			ManagedWorkdirRelPath   string `json:"managed_workdir_rel_path"`
		} `json:"task"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode claim response: %v", err)
	}
	if resp.Task == nil {
		t.Fatalf("expected a claimed task, got %s", w.Body.String())
	}
	if !resp.Task.ProvisionManagedWorkdir {
		t.Errorf("expected provision_managed_workdir=true for a project with no resource")
	}
	wantRel := "projects/" + projectID + "/workdir"
	if resp.Task.ManagedWorkdirRelPath != wantRel {
		t.Errorf("managed_workdir_rel_path = %q, want %q", resp.Task.ManagedWorkdirRelPath, wantRel)
	}
}

func TestClaimTask_QuickCreatePopulatesThreadName(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentID, runtimeID, daemonID := createRuntimeGuardAgent(t, ctx)

	quickPrompt := "create a follow-up issue for Codex session titles"
	attachmentID := "019ec09d-6222-722b-bdfa-427b105d80be"
	quickContext, _ := json.Marshal(map[string]any{
		"type":           "quick_create",
		"prompt":         quickPrompt,
		"requester_id":   testUserID,
		"workspace_id":   testWorkspaceID,
		"attachment_ids": []string{attachmentID},
	})

	if _, err := testPool.Exec(ctx, `
		INSERT INTO agent_task_queue (agent_id, runtime_id, status, priority, context)
		VALUES ($1, $2, 'queued', 2, $3)
	`, agentID, runtimeID, quickContext); err != nil {
		t.Fatalf("setup: create quick-create task: %v", err)
	}

	task := claimTaskForRuntimeGuard(t, runtimeID, daemonID)
	if task.ThreadName != quickPrompt {
		t.Fatalf("quick-create task thread_name = %q, want prompt", task.ThreadName)
	}
	if len(task.QuickCreateAttachmentIDs) != 1 || task.QuickCreateAttachmentIDs[0] != attachmentID {
		t.Fatalf("quick-create attachment ids = %#v, want [%q]", task.QuickCreateAttachmentIDs, attachmentID)
	}
}

func TestClaimTask_ChatForceFreshSessionSkipsPriorSession(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()

	agentID, runtimeID, daemonID := createRuntimeGuardAgent(t, ctx)

	var chatSessionID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO chat_session (
			workspace_id, agent_id, creator_id, title,
			session_id, work_dir, runtime_id
		)
		VALUES ($1, $2, $3, 'force fresh chat', 'chat-pointer-session', '/tmp/chat-pointer-workdir', $4)
		RETURNING id
	`, testWorkspaceID, agentID, testUserID, runtimeID).Scan(&chatSessionID); err != nil {
		t.Fatalf("setup: create chat session: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM chat_session WHERE id = $1`, chatSessionID) })

	if _, err := testPool.Exec(ctx, `
		INSERT INTO agent_task_queue (
			agent_id, runtime_id, chat_session_id,
			status, priority, started_at, completed_at,
			session_id, work_dir
		)
		VALUES ($1, $2, $3, 'completed', 0, now(), now(), 'task-row-session', '/tmp/task-row-workdir')
	`, agentID, runtimeID, chatSessionID); err != nil {
		t.Fatalf("setup: create prior chat task: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO agent_task_queue (
			agent_id, runtime_id, chat_session_id,
			status, priority, force_fresh_session
		)
		VALUES ($1, $2, $3, 'queued', 0, TRUE)
	`, agentID, runtimeID, chatSessionID); err != nil {
		t.Fatalf("setup: create force-fresh chat task: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO chat_message (chat_session_id, role, content)
		VALUES ($1, 'user', 'force fresh chat message')
	`, chatSessionID); err != nil {
		t.Fatalf("setup: insert force-fresh chat user message: %v", err)
	}

	task := claimTaskForRuntimeGuard(t, runtimeID, daemonID)
	if task.PriorSessionID != "" {
		t.Fatalf("force fresh chat: expected empty PriorSessionID, got %q", task.PriorSessionID)
	}
	if task.PriorWorkDir != "" {
		t.Fatalf("force fresh chat: expected empty PriorWorkDir, got %q", task.PriorWorkDir)
	}
}

// Locks the legacy-row fallback: chat_session.runtime_id IS NULL (e.g. a row
// the migration left untouched because no prior task matched the cs pointer)
// but a completed task on the claiming runtime exists. ClaimTaskByRuntime
// must recover the session from the task row, not start a fresh conversation.
func TestClaimTask_ChatLegacyNullRuntimeFallsBackToTaskRow(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()

	agentID, runtimeID, daemonID := createRuntimeGuardAgent(t, ctx)

	var legacySessionID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO chat_session (
			workspace_id, agent_id, creator_id, title,
			session_id, work_dir, runtime_id
		)
		VALUES ($1, $2, $3, 'runtime guard legacy chat', NULL, NULL, NULL)
		RETURNING id
	`, testWorkspaceID, agentID, testUserID).Scan(&legacySessionID); err != nil {
		t.Fatalf("setup: create legacy chat session: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM chat_session WHERE id = $1`, legacySessionID) })

	if _, err := testPool.Exec(ctx, `
		INSERT INTO agent_task_queue (
			agent_id, runtime_id, chat_session_id,
			status, priority, started_at, completed_at,
			session_id, work_dir
		)
		VALUES ($1, $2, $3, 'completed', 0, now(), now(), 'legacy-fallback-session', '/tmp/legacy-fallback-workdir')
	`, agentID, runtimeID, legacySessionID); err != nil {
		t.Fatalf("setup: create matching-runtime prior task: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO agent_task_queue (
			agent_id, runtime_id, chat_session_id,
			status, priority
		)
		VALUES ($1, $2, $3, 'queued', 0)
	`, agentID, runtimeID, legacySessionID); err != nil {
		t.Fatalf("setup: create current chat task: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO chat_message (chat_session_id, role, content)
		VALUES ($1, 'user', 'legacy fallback chat message')
	`, legacySessionID); err != nil {
		t.Fatalf("setup: insert legacy chat user message: %v", err)
	}

	task := claimTaskForRuntimeGuard(t, runtimeID, daemonID)
	if task.PriorSessionID != "legacy-fallback-session" {
		t.Fatalf("legacy fallback: expected PriorSessionID='legacy-fallback-session', got %q", task.PriorSessionID)
	}
	if task.PriorWorkDir != "/tmp/legacy-fallback-workdir" {
		t.Fatalf("legacy fallback: expected PriorWorkDir='/tmp/legacy-fallback-workdir', got %q", task.PriorWorkDir)
	}
}

// TestGetChatSessionGCCheck verifies the chat session gc-check endpoint
// matches the same anti-enumeration shape as GetIssueGCCheck: cross-workspace
// daemon tokens get 404, same-workspace tokens get the live status.
func TestGetChatSessionGCCheck(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()

	var agentID string
	if err := testPool.QueryRow(ctx, `SELECT id FROM agent WHERE workspace_id = $1 LIMIT 1`, testWorkspaceID).Scan(&agentID); err != nil {
		t.Fatalf("setup: get agent: %v", err)
	}

	var sessionID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO chat_session (workspace_id, agent_id, creator_id, title, status)
		VALUES ($1, $2, $3, 'gc-check fixture', 'active')
		RETURNING id
	`, testWorkspaceID, agentID, testUserID).Scan(&sessionID); err != nil {
		t.Fatalf("setup: create chat session: %v", err)
	}
	defer testPool.Exec(ctx, `DELETE FROM chat_session WHERE id = $1`, sessionID)

	// Cross-workspace daemon token must 404 with no oracle.
	w := httptest.NewRecorder()
	req := newDaemonTokenRequest("GET", "/api/daemon/chat-sessions/"+sessionID+"/gc-check", nil,
		"00000000-0000-0000-0000-000000000000", "attacker-daemon")
	req = withURLParam(req, "sessionId", sessionID)
	testHandler.GetChatSessionGCCheck(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("cross-workspace token: expected 404, got %d: %s", w.Code, w.Body.String())
	}

	// Same-workspace daemon token sees the live row.
	w = httptest.NewRecorder()
	req = newDaemonTokenRequest("GET", "/api/daemon/chat-sessions/"+sessionID+"/gc-check", nil,
		testWorkspaceID, "legit-daemon")
	req = withURLParam(req, "sessionId", sessionID)
	testHandler.GetChatSessionGCCheck(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("same-workspace token: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Status    string `json:"status"`
		UpdatedAt string `json:"updated_at"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Status != "active" {
		t.Fatalf("expected status %q, got %q", "active", resp.Status)
	}
	if resp.UpdatedAt == "" {
		t.Fatal("expected updated_at to be set")
	}

	// Hard-deleted session: 404 — exactly what the daemon needs to reclaim
	// the workdir on the next GC pass after a user runs DeleteChatSession.
	if _, err := testPool.Exec(ctx, `DELETE FROM chat_session WHERE id = $1`, sessionID); err != nil {
		t.Fatalf("delete chat session: %v", err)
	}
	w = httptest.NewRecorder()
	req = newDaemonTokenRequest("GET", "/api/daemon/chat-sessions/"+sessionID+"/gc-check", nil,
		testWorkspaceID, "legit-daemon")
	req = withURLParam(req, "sessionId", sessionID)
	testHandler.GetChatSessionGCCheck(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("hard-deleted session: expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

// TestGetAutopilotRunGCCheck verifies the autopilot-run gc-check endpoint:
// 200 with status+completed_at on success, 404 on cross-workspace probe.
func TestGetAutopilotRunGCCheck(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()

	var agentID string
	if err := testPool.QueryRow(ctx, `SELECT id FROM agent WHERE workspace_id = $1 LIMIT 1`, testWorkspaceID).Scan(&agentID); err != nil {
		t.Fatalf("setup: get agent: %v", err)
	}

	var autopilotID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO autopilot (
			workspace_id, title, assignee_id, execution_mode,
			created_by_type, created_by_id
		)
		VALUES ($1, 'gc-check autopilot', $2, 'run_only', 'member', $3)
		RETURNING id
	`, testWorkspaceID, agentID, testUserID).Scan(&autopilotID); err != nil {
		t.Fatalf("setup: create autopilot: %v", err)
	}
	defer testPool.Exec(ctx, `DELETE FROM autopilot WHERE id = $1`, autopilotID)

	var runID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO autopilot_run (autopilot_id, source, status, completed_at)
		VALUES ($1, 'manual', 'completed', NOW() - INTERVAL '6 days')
		RETURNING id
	`, autopilotID).Scan(&runID); err != nil {
		t.Fatalf("setup: create autopilot_run: %v", err)
	}

	// Cross-workspace probe.
	w := httptest.NewRecorder()
	req := newDaemonTokenRequest("GET", "/api/daemon/autopilot-runs/"+runID+"/gc-check", nil,
		"00000000-0000-0000-0000-000000000000", "attacker-daemon")
	req = withURLParam(req, "runId", runID)
	testHandler.GetAutopilotRunGCCheck(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("cross-workspace token: expected 404, got %d: %s", w.Code, w.Body.String())
	}

	// Same-workspace probe.
	w = httptest.NewRecorder()
	req = newDaemonTokenRequest("GET", "/api/daemon/autopilot-runs/"+runID+"/gc-check", nil,
		testWorkspaceID, "legit-daemon")
	req = withURLParam(req, "runId", runID)
	testHandler.GetAutopilotRunGCCheck(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("same-workspace token: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Status      string `json:"status"`
		CompletedAt string `json:"completed_at"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Status != "completed" {
		t.Fatalf("expected status %q, got %q", "completed", resp.Status)
	}
	if resp.CompletedAt == "" {
		t.Fatal("expected completed_at to be set for terminal run")
	}
}

// TestGetTaskGCCheck verifies the task gc-check endpoint that quick-create
// workdirs key on. Same anti-enumeration shape via requireDaemonTaskAccess.
func TestGetTaskGCCheck(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()

	var agentID, runtimeID string
	if err := testPool.QueryRow(ctx, `
		SELECT a.id, a.runtime_id FROM agent a WHERE a.workspace_id = $1 LIMIT 1
	`, testWorkspaceID).Scan(&agentID, &runtimeID); err != nil {
		t.Fatalf("setup: get agent: %v", err)
	}

	// Quick-create-shaped task: no issue_id, no chat_session_id, no run id.
	// context.type is set so ResolveTaskWorkspaceID can recover workspace.
	quickContext, _ := json.Marshal(map[string]any{
		"type":         "quick_create",
		"prompt":       "fixture",
		"requester_id": testUserID,
		"workspace_id": testWorkspaceID,
	})

	var taskID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_task_queue (
			agent_id, runtime_id, status, priority, context, completed_at
		)
		VALUES ($1, $2, 'completed', 0, $3, NOW())
		RETURNING id
	`, agentID, runtimeID, quickContext).Scan(&taskID); err != nil {
		t.Fatalf("setup: create quick-create task: %v", err)
	}
	defer testPool.Exec(ctx, `DELETE FROM agent_task_queue WHERE id = $1`, taskID)

	// Cross-workspace probe.
	w := httptest.NewRecorder()
	req := newDaemonTokenRequest("GET", "/api/daemon/tasks/"+taskID+"/gc-check", nil,
		"00000000-0000-0000-0000-000000000000", "attacker-daemon")
	req = withURLParam(req, "taskId", taskID)
	testHandler.GetTaskGCCheck(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("cross-workspace token: expected 404, got %d: %s", w.Code, w.Body.String())
	}

	// Same-workspace probe — terminal task returns its status.
	w = httptest.NewRecorder()
	req = newDaemonTokenRequest("GET", "/api/daemon/tasks/"+taskID+"/gc-check", nil,
		testWorkspaceID, "legit-daemon")
	req = withURLParam(req, "taskId", taskID)
	testHandler.GetTaskGCCheck(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("same-workspace token: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Status      string `json:"status"`
		CompletedAt string `json:"completed_at"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Status != "completed" {
		t.Fatalf("expected status %q, got %q", "completed", resp.Status)
	}
	if resp.CompletedAt == "" {
		t.Fatal("expected completed_at to be set for completed task")
	}
}

// ---------------------------------------------------------------------------
// Membership Cache Integration Tests
//
// These tests don't just exercise the cache primitive (that's covered in
// auth/membership_cache_test.go). They prove two things that the unit tests
// can't:
//
//  1. requireDaemonWorkspaceAccess actually short-circuits the DB on a cache
//     hit (the "ghost user" trick below).
//  2. Each handler that mutates membership actually calls
//     h.MembershipCache.Invalidate(...) — so a future refactor that drops
//     one of those calls will fail CI instead of silently leaking a stale
//     authorization grant for up to MembershipCacheTTL.
// ---------------------------------------------------------------------------

// installFreshMembershipCache swaps in a Redis-backed MembershipCache against
// a freshly-flushed Redis DB for the test, restoring the original on cleanup.
func installFreshMembershipCache(t *testing.T) {
	t.Helper()
	rdb := newRedisTestClient(t)
	origCache := testHandler.MembershipCache
	testHandler.MembershipCache = auth.NewMembershipCache(rdb)
	t.Cleanup(func() { testHandler.MembershipCache = origCache })
}

// createEphemeralUser inserts a throwaway user with a unique email and
// deletes it on test cleanup. Returns the user id as a string.
func createEphemeralUser(t *testing.T, label string) string {
	t.Helper()
	email := fmt.Sprintf("membership-cache-%s-%s@multica.ai", label, uuid.NewString())
	var userID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO "user" (name, email) VALUES ($1, $2) RETURNING id
	`, "Membership Cache Test "+label, email).Scan(&userID); err != nil {
		t.Fatalf("create ephemeral user: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1`, userID)
	})
	return userID
}

// createEphemeralMember creates a throwaway user AND a member row in the
// given workspace. Returns (userID, memberID). Both rows are cleaned up on
// test exit.
func createEphemeralMember(t *testing.T, workspaceID, label, role string) (string, string) {
	t.Helper()
	userID := createEphemeralUser(t, label)
	var memberID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, $3) RETURNING id
	`, workspaceID, userID, role).Scan(&memberID); err != nil {
		t.Fatalf("create ephemeral member: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM member WHERE id = $1`, memberID)
	})
	return userID, memberID
}

// TestRequireDaemonWorkspaceAccess_CacheHit proves the cache lookup actually
// short-circuits the DB query. The trick: the request actor is a "ghost"
// user with NO member row in the workspace. With an empty cache the access
// check must fail; after priming the cache it must succeed. If a future
// change ever bypasses the cache and falls through to the DB, the priming
// step stops mattering and the second assertion catches it.
func TestRequireDaemonWorkspaceAccess_CacheHit(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	installFreshMembershipCache(t)
	ctx := context.Background()

	ghostUserID := createEphemeralUser(t, "ghost")

	// Baseline: with an empty cache the ghost has no path to access.
	req := newRequestAsUser(ghostUserID, "GET", "/api/daemon/workspaces/"+testWorkspaceID+"/repos", nil)
	w := httptest.NewRecorder()
	if testHandler.requireDaemonWorkspaceAccess(w, req, testWorkspaceID) {
		t.Fatal("setup: ghost user must not be allowed without cache priming")
	}

	// Priming the cache is the only thing that changes — the access check
	// must now succeed via the cache short-circuit.
	testHandler.MembershipCache.Set(ctx, ghostUserID, testWorkspaceID)

	req = newRequestAsUser(ghostUserID, "GET", "/api/daemon/workspaces/"+testWorkspaceID+"/repos", nil)
	w = httptest.NewRecorder()
	if !testHandler.requireDaemonWorkspaceAccess(w, req, testWorkspaceID) {
		t.Fatalf("expected access via cache hit, got denied (status %d)", w.Code)
	}
}

func TestRequireDaemonWorkspaceAccess_CacheMissBackfills(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	installFreshMembershipCache(t)

	ctx := context.Background()

	// Cache is empty — verify miss.
	if testHandler.MembershipCache.Get(ctx, testUserID, testWorkspaceID) {
		t.Fatal("expected cache miss before request")
	}

	// Make a request that triggers DB lookup.
	req := newRequest("GET", "/api/daemon/workspaces/"+testWorkspaceID+"/repos", nil)
	w := httptest.NewRecorder()
	if !testHandler.requireDaemonWorkspaceAccess(w, req, testWorkspaceID) {
		t.Fatalf("expected access granted via DB lookup, got denied (status %d)", w.Code)
	}

	// Cache should now be backfilled.
	if !testHandler.MembershipCache.Get(ctx, testUserID, testWorkspaceID) {
		t.Fatal("expected cache to be backfilled after DB hit")
	}
}

// TestMembershipCache_InvalidatedOnDeleteMember drives a real DeleteMember
// HTTP handler call and asserts the cache entry for the removed member is
// gone afterwards. Guards against future refactors that move or drop the
// h.MembershipCache.Invalidate(...) line in workspace.go.
func TestMembershipCache_InvalidatedOnDeleteMember(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	installFreshMembershipCache(t)
	ctx := context.Background()

	targetUserID, targetMemberID := createEphemeralMember(t, testWorkspaceID, "delete", "admin")
	testHandler.MembershipCache.Set(ctx, targetUserID, testWorkspaceID)
	if !testHandler.MembershipCache.Get(ctx, targetUserID, testWorkspaceID) {
		t.Fatal("setup: expected cache hit after Set")
	}

	w := httptest.NewRecorder()
	req := newRequest("DELETE", "/api/workspaces/"+testWorkspaceID+"/members/"+targetMemberID, nil)
	req = withURLParams(req, "id", testWorkspaceID, "memberId", targetMemberID)
	testHandler.DeleteMember(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("DeleteMember: expected 204, got %d: %s", w.Code, w.Body.String())
	}

	if testHandler.MembershipCache.Get(ctx, targetUserID, testWorkspaceID) {
		t.Fatal("DeleteMember handler did not invalidate membership cache for removed user")
	}
}

// TestMembershipCache_InvalidatedOnUpdateMember drives a real UpdateMember
// (role change) call and asserts the cache entry is invalidated. The cache
// stores only the existence of membership, but UpdateMember still flushes
// the entry so any downstream caller that did add role-aware caching later
// would not silently see a stale role.
func TestMembershipCache_InvalidatedOnUpdateMember(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	installFreshMembershipCache(t)
	ctx := context.Background()

	targetUserID, targetMemberID := createEphemeralMember(t, testWorkspaceID, "update", "admin")
	testHandler.MembershipCache.Set(ctx, targetUserID, testWorkspaceID)

	w := httptest.NewRecorder()
	req := newRequest("PUT", "/api/workspaces/"+testWorkspaceID+"/members/"+targetMemberID,
		map[string]any{"role": "member"})
	req = withURLParams(req, "id", testWorkspaceID, "memberId", targetMemberID)
	testHandler.UpdateMember(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("UpdateMember: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	if testHandler.MembershipCache.Get(ctx, targetUserID, testWorkspaceID) {
		t.Fatal("UpdateMember handler did not invalidate membership cache for updated user")
	}
}

// TestMembershipCache_InvalidatedOnLeaveWorkspace exercises the self-removal
// path (LeaveWorkspace, not DeleteMember). Both handlers route through
// revokeAndRemoveMember, but the Invalidate call lives in the handler — a
// refactor that consolidates them could drop one invalidation without the
// other test catching it.
func TestMembershipCache_InvalidatedOnLeaveWorkspace(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	installFreshMembershipCache(t)
	ctx := context.Background()

	targetUserID, _ := createEphemeralMember(t, testWorkspaceID, "leave", "admin")
	testHandler.MembershipCache.Set(ctx, targetUserID, testWorkspaceID)

	w := httptest.NewRecorder()
	req := newRequestAsUser(targetUserID, "DELETE", "/api/workspaces/"+testWorkspaceID+"/leave", nil)
	req = withURLParam(req, "id", testWorkspaceID)
	testHandler.LeaveWorkspace(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("LeaveWorkspace: expected 204, got %d: %s", w.Code, w.Body.String())
	}

	if testHandler.MembershipCache.Get(ctx, targetUserID, testWorkspaceID) {
		t.Fatal("LeaveWorkspace handler did not invalidate membership cache for leaver")
	}
}

// TestMembershipCache_InvalidatedOnDeleteWorkspace exercises the bulk
// invalidation path: when the workspace is deleted, every member's cache
// entry must be flushed. We create an isolated workspace with two members
// (owner + extra) so the shared testWorkspace stays intact.
func TestMembershipCache_InvalidatedOnDeleteWorkspace(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	installFreshMembershipCache(t)
	ctx := context.Background()

	const slug = "membership-cache-delete-ws"
	_, _ = testPool.Exec(ctx, `DELETE FROM workspace WHERE slug = $1`, slug)
	var wsID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO workspace (name, slug, description, issue_prefix)
		VALUES ($1, $2, $3, $4) RETURNING id
	`, "Membership Cache Delete WS", slug, "DeleteWorkspace cache invalidation test", "MCD").Scan(&wsID); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1`, wsID)
	})

	// testUser must be an owner of the isolated workspace to call
	// DeleteWorkspace.
	if _, err := testPool.Exec(ctx, `
		INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, 'owner')
	`, wsID, testUserID); err != nil {
		t.Fatalf("add owner: %v", err)
	}

	extraUserID, _ := createEphemeralMember(t, wsID, "ws-delete-extra", "admin")

	testHandler.MembershipCache.Set(ctx, testUserID, wsID)
	testHandler.MembershipCache.Set(ctx, extraUserID, wsID)

	w := httptest.NewRecorder()
	req := newRequest("DELETE", "/api/workspaces/"+wsID, nil)
	req = withURLParam(req, "id", wsID)
	testHandler.DeleteWorkspace(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("DeleteWorkspace: expected 204, got %d: %s", w.Code, w.Body.String())
	}

	if testHandler.MembershipCache.Get(ctx, testUserID, wsID) {
		t.Fatal("DeleteWorkspace handler did not invalidate owner cache entry")
	}
	if testHandler.MembershipCache.Get(ctx, extraUserID, wsID) {
		t.Fatal("DeleteWorkspace handler did not invalidate extra-member cache entry")
	}
}

// createCommentTriggeredClaimTask seeds a queued comment-triggered task whose
// trigger comment is rooted under parentID (nil → trigger is itself a root).
// Returns the task id and the trigger comment id.
func createCommentTriggeredClaimTask(t *testing.T, ctx context.Context, agentID, runtimeID, issueID string, parentID *string) (string, string) {
	t.Helper()
	var commentID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO comment (issue_id, workspace_id, author_type, author_id, content, type, parent_id)
		VALUES ($1, $2, 'member', $3, 'trigger comment', 'comment', $4)
		RETURNING id
	`, issueID, testWorkspaceID, testUserID, parentID).Scan(&commentID); err != nil {
		t.Fatalf("insert trigger comment: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM comment WHERE id = $1`, commentID) })

	var taskID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_task_queue (agent_id, runtime_id, issue_id, status, priority, trigger_comment_id)
		VALUES ($1, $2, $3, 'queued', 0, $4)
		RETURNING id
	`, agentID, runtimeID, issueID, commentID).Scan(&taskID); err != nil {
		t.Fatalf("insert comment-triggered task: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM agent_task_queue WHERE id = $1`, taskID) })
	return taskID, commentID
}

type claimCommentTaskResp struct {
	Task *struct {
		ID               string `json:"id"`
		PriorSessionID   string `json:"prior_session_id"`
		TriggerCommentID string `json:"trigger_comment_id"`
		NewCommentCount  int    `json:"new_comment_count"`
		NewCommentsSince string `json:"new_comments_since"`
	} `json:"task"`
}

func claimCommentTask(t *testing.T, runtimeID, daemonID string) claimCommentTaskResp {
	t.Helper()
	w := httptest.NewRecorder()
	req := newDaemonTokenRequest("POST", "/api/daemon/runtimes/"+runtimeID+"/tasks/claim", nil, testWorkspaceID, daemonID)
	req = withURLParam(req, "runtimeId", runtimeID)
	testHandler.ClaimTaskByRuntime(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("ClaimTaskByRuntime: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp claimCommentTaskResp
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode claim response: %v", err)
	}
	if resp.Task == nil {
		t.Fatalf("expected a claimed task, got nil: %s", w.Body.String())
	}
	return resp
}

// TestClaimTaskByRuntime_CommentTaskPopulatesNewCommentCount
// verifies the claim response carries new_comment_count + new_comments_since for
// a comment task when the agent ran before: the count is ISSUE-WIDE (covers
// every thread, not just the triggering one), excludes the injected trigger
// comment, excludes the agent's own comments, and the since anchor is the prior
// run's started_at.
func TestClaimTaskByRuntime_CommentTaskPopulatesNewCommentCount(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	runtimeID := createClaimReclaimRuntime(t, ctx, "Comment newcount runtime")
	agentID, issueID := createClaimReclaimAgentAndIssue(t, ctx, runtimeID, "Comment newcount agent")

	// A prior run establishes the "since" anchor (its started_at, in the past).
	var priorTaskID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_task_queue (agent_id, runtime_id, issue_id, status, priority, started_at, completed_at)
		VALUES ($1, $2, $3, 'completed', 0, now() - interval '1 hour', now() - interval '50 minutes')
		RETURNING id
	`, agentID, runtimeID, issueID).Scan(&priorTaskID); err != nil {
		t.Fatalf("insert prior task: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM agent_task_queue WHERE id = $1`, priorTaskID) })

	var threadRootID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO comment (issue_id, workspace_id, author_type, author_id, content, type)
		VALUES ($1, $2, 'member', $3, 'same-thread context', 'comment')
		RETURNING id
	`, issueID, testWorkspaceID, testUserID).Scan(&threadRootID); err != nil {
		t.Fatalf("insert trigger thread root: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM comment WHERE id = $1`, threadRootID) })

	var unrelatedRootID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO comment (issue_id, workspace_id, author_type, author_id, content, type)
		VALUES ($1, $2, 'member', $3, 'unrelated thread context', 'comment')
		RETURNING id
	`, issueID, testWorkspaceID, testUserID).Scan(&unrelatedRootID); err != nil {
		t.Fatalf("insert unrelated thread root: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM comment WHERE id = $1`, unrelatedRootID) })

	var agentOwnID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO comment (issue_id, workspace_id, author_type, author_id, content, type, parent_id)
		VALUES ($1, $2, 'agent', $3, 'agent self reply', 'comment', $4)
		RETURNING id
	`, issueID, testWorkspaceID, agentID, threadRootID).Scan(&agentOwnID); err != nil {
		t.Fatalf("insert agent self reply: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM comment WHERE id = $1`, agentOwnID) })

	// The trigger comment (member-authored, created now) lands after the anchor
	// but is injected into the prompt, so it should not be counted.
	_, triggerID := createCommentTriggeredClaimTask(t, ctx, agentID, runtimeID, issueID, &threadRootID)

	resp := claimCommentTask(t, runtimeID, "comment-newcount-claim")
	if resp.Task.TriggerCommentID != triggerID {
		t.Fatalf("trigger_comment_id = %s, want %s", resp.Task.TriggerCommentID, triggerID)
	}
	if resp.Task.NewCommentsSince == "" {
		t.Errorf("new_comments_since must be set when a prior run exists, got empty")
	}
	// Issue-wide: the same-thread context comment AND the unrelated-thread root
	// both count; only the agent's own reply and the injected trigger are excluded.
	if resp.Task.NewCommentCount != 2 {
		t.Errorf("new_comment_count = %d, want 2 (issue-wide: same-thread + unrelated thread)", resp.Task.NewCommentCount)
	}
}

// TestClaimTaskByRuntime_CommentTaskPopulatesInitiator verifies MUL-2645: the
// claim response surfaces the triggering comment's member author as the task
// initiator (type + id + name + email), so a workspace-visible agent learns who
// actually asked rather than seeing the runtime owner. createCommentTriggeredClaimTask
// authors the trigger comment as the fixture member (testUserID).
func TestClaimTaskByRuntime_CommentTaskPopulatesInitiator(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	runtimeID := createClaimReclaimRuntime(t, ctx, "Comment initiator runtime")
	agentID, issueID := createClaimReclaimAgentAndIssue(t, ctx, runtimeID, "Comment initiator agent")
	taskID, _ := createCommentTriggeredClaimTask(t, ctx, agentID, runtimeID, issueID, nil)

	w := httptest.NewRecorder()
	req := newDaemonTokenRequest("POST", "/api/daemon/runtimes/"+runtimeID+"/tasks/claim", nil, testWorkspaceID, "comment-initiator-claim")
	req = withURLParam(req, "runtimeId", runtimeID)
	testHandler.ClaimTaskByRuntime(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("ClaimTaskByRuntime: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Task *struct {
			ID             string `json:"id"`
			InitiatorType  string `json:"initiator_type"`
			InitiatorID    string `json:"initiator_id"`
			InitiatorName  string `json:"initiator_name"`
			InitiatorEmail string `json:"initiator_email"`
		} `json:"task"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode claim response: %v", err)
	}
	if resp.Task == nil || resp.Task.ID != taskID {
		t.Fatalf("expected claimed task %s, got %s", taskID, w.Body.String())
	}
	if resp.Task.InitiatorType != "member" {
		t.Errorf("initiator_type = %q, want %q", resp.Task.InitiatorType, "member")
	}
	if resp.Task.InitiatorID != testUserID {
		t.Errorf("initiator_id = %q, want %q", resp.Task.InitiatorID, testUserID)
	}
	if resp.Task.InitiatorName != handlerTestName {
		t.Errorf("initiator_name = %q, want %q", resp.Task.InitiatorName, handlerTestName)
	}
	if resp.Task.InitiatorEmail != handlerTestEmail {
		t.Errorf("initiator_email = %q, want %q", resp.Task.InitiatorEmail, handlerTestEmail)
	}
}

func TestClaimTaskByRuntime_CommentTaskOmitsDeltaWhenOnlyTriggerIsNew(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	runtimeID := createClaimReclaimRuntime(t, ctx, "Comment trigger-only runtime")
	agentID, issueID := createClaimReclaimAgentAndIssue(t, ctx, runtimeID, "Comment trigger-only agent")

	var priorTaskID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_task_queue (agent_id, runtime_id, issue_id, status, priority, started_at, completed_at)
		VALUES ($1, $2, $3, 'completed', 0, now() - interval '1 hour', now() - interval '50 minutes')
		RETURNING id
	`, agentID, runtimeID, issueID).Scan(&priorTaskID); err != nil {
		t.Fatalf("insert prior task: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM agent_task_queue WHERE id = $1`, priorTaskID) })

	_, triggerID := createCommentTriggeredClaimTask(t, ctx, agentID, runtimeID, issueID, nil)

	resp := claimCommentTask(t, runtimeID, "comment-trigger-only-claim")
	if resp.Task.TriggerCommentID != triggerID {
		t.Fatalf("trigger_comment_id = %s, want %s", resp.Task.TriggerCommentID, triggerID)
	}
	if resp.Task.NewCommentCount != 0 {
		t.Errorf("new_comment_count = %d, want 0 when only the injected trigger is new", resp.Task.NewCommentCount)
	}
	if resp.Task.NewCommentsSince != "" {
		t.Errorf("new_comments_since = %q, want empty when only the injected trigger is new", resp.Task.NewCommentsSince)
	}
}

// TestClaimTaskByRuntime_CommentResumeDefaultOn verifies comment-triggered tasks
// resume the prior session by default (no env flag), as long as the prior
// session ran on the same runtime.
func TestClaimTaskByRuntime_CommentResumeDefaultOn(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	runtimeID := createClaimReclaimRuntime(t, ctx, "Comment resume runtime")
	agentID, issueID := createClaimReclaimAgentAndIssue(t, ctx, runtimeID, "Comment resume agent")

	// A prior completed task on the same (agent, issue, runtime) with a session.
	const priorSession = "sess-prior-123"
	var priorTaskID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_task_queue (agent_id, runtime_id, issue_id, status, priority, session_id, completed_at)
		VALUES ($1, $2, $3, 'completed', 0, $4, now())
		RETURNING id
	`, agentID, runtimeID, issueID, priorSession).Scan(&priorTaskID); err != nil {
		t.Fatalf("insert prior completed task: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM agent_task_queue WHERE id = $1`, priorTaskID) })

	createCommentTriggeredClaimTask(t, ctx, agentID, runtimeID, issueID, nil)

	resp := claimCommentTask(t, runtimeID, "comment-resume-default")
	if resp.Task.PriorSessionID != priorSession {
		t.Errorf("prior_session_id = %q, want %q (comment resume is default-on)", resp.Task.PriorSessionID, priorSession)
	}
}
