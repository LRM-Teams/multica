package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type agentTaskCancelHTTPHandler interface {
	CancelAgentTask(http.ResponseWriter, *http.Request)
}

func callAgentTaskCancel(t *testing.T, h *Handler, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	cancel, ok := any(h).(agentTaskCancelHTTPHandler)
	if !ok {
		t.Fatal("Handler is missing the agent-only CancelAgentTask contract")
	}
	cancel.CancelAgentTask(w, r)
}

func agentTaskCancelRequest(taskID string, principal *middleware.AgentPrincipal) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/api/agent/tasks/"+taskID+"/cancel", nil)
	req = withURLParam(req, "taskId", taskID)
	if principal != nil {
		req = req.WithContext(middleware.WithAgentPrincipal(req.Context(), *principal))
	}
	return req
}

func taskScopedCancelPrincipal(agentID, taskID, source string) middleware.AgentPrincipal {
	return middleware.AgentPrincipal{
		AgentID:     agentID,
		WorkspaceID: testWorkspaceID,
		OwnerUserID: testUserID,
		ActorSource: source,
		TaskID:      taskID,
	}
}

func assertExactHTTPError(t *testing.T, rec *httptest.ResponseRecorder, status int, body string) {
	t.Helper()
	if rec.Code != status || rec.Body.String() != body {
		t.Fatalf("response=(%d, %q), want exact (%d, %q)", rec.Code, rec.Body.String(), status, body)
	}
}

func TestCancelAgentTask_RequiresAgentPrincipalExact403(t *testing.T) {
	taskID := uuid.NewString()
	rec := httptest.NewRecorder()

	callAgentTaskCancel(t, &Handler{}, rec, agentTaskCancelRequest(taskID, nil))

	assertExactHTTPError(t, rec, http.StatusForbidden, "{\"error\":\"agent principal required\"}\n")
}

func TestCancelAgentTask_RequiresValidTaskScopedPrincipalExact403(t *testing.T) {
	taskID := uuid.NewString()
	tests := []struct {
		name      string
		principal middleware.AgentPrincipal
	}{
		{
			name: "unscoped agent credential",
			principal: middleware.AgentPrincipal{
				AgentID:     uuid.NewString(),
				WorkspaceID: uuid.NewString(),
				ActorSource: "agent_credential",
			},
		},
		{
			name: "agent credential cannot borrow task scope",
			principal: middleware.AgentPrincipal{
				AgentID:     uuid.NewString(),
				WorkspaceID: uuid.NewString(),
				ActorSource: "agent_credential",
				TaskID:      taskID,
			},
		},
		{
			name: "invalid agent id",
			principal: middleware.AgentPrincipal{
				AgentID:     "not-a-uuid",
				WorkspaceID: uuid.NewString(),
				ActorSource: "task_token",
				TaskID:      taskID,
			},
		},
		{
			name: "invalid task id",
			principal: middleware.AgentPrincipal{
				AgentID:     uuid.NewString(),
				WorkspaceID: uuid.NewString(),
				ActorSource: "task_token",
				TaskID:      "not-a-uuid",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			callAgentTaskCancel(t, &Handler{}, rec, agentTaskCancelRequest(taskID, &tt.principal))
			assertExactHTTPError(t, rec, http.StatusForbidden, "{\"error\":\"task-scoped agent principal required\"}\n")
		})
	}
}

func TestCancelAgentTask_InvalidPathExact400(t *testing.T) {
	principal := taskScopedCancelPrincipal(uuid.NewString(), uuid.NewString(), "task_token")
	rec := httptest.NewRecorder()

	callAgentTaskCancel(t, &Handler{}, rec, agentTaskCancelRequest("not-a-uuid", &principal))

	assertExactHTTPError(t, rec, http.StatusBadRequest, "{\"error\":\"invalid task id\"}\n")
}

func TestCancelAgentTask_TaskIDMismatchAndMissingAreIndistinguishable404(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	agentID := createHandlerTestAgent(t, "Agent Cancel Task ID Boundary", []byte("[]"))
	principalTaskID := createHandlerTestTaskForAgent(t, agentID)
	otherOwnedTaskID := createHandlerTestTaskForAgent(t, agentID)
	principal := taskScopedCancelPrincipal(agentID, principalTaskID, "task_token")

	var bodies []string
	for _, targetTaskID := range []string{otherOwnedTaskID, uuid.NewString()} {
		rec := httptest.NewRecorder()
		callAgentTaskCancel(t, testHandler, rec, agentTaskCancelRequest(targetTaskID, &principal))
		assertExactHTTPError(t, rec, http.StatusNotFound, "{\"error\":\"task not found\"}\n")
		bodies = append(bodies, rec.Body.String())
	}
	if bodies[0] != bodies[1] {
		t.Fatalf("existing-invisible body %q differs from missing body %q", bodies[0], bodies[1])
	}
	if got := taskStatus(t, otherOwnedTaskID); got != "draining" {
		t.Fatalf("TaskID mismatch mutated task: status=%q", got)
	}
}

func TestCancelAgentTask_AgentIDMismatchReturnsSame404WithoutMutation(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ownerAgentID := createHandlerTestAgent(t, "Agent Cancel Owner", []byte("[]"))
	foreignAgentID := createHandlerTestAgent(t, "Agent Cancel Foreign Principal", []byte("[]"))
	taskID := createHandlerTestTaskForAgent(t, ownerAgentID)
	principal := taskScopedCancelPrincipal(foreignAgentID, taskID, "task_token")
	rec := httptest.NewRecorder()

	callAgentTaskCancel(t, testHandler, rec, agentTaskCancelRequest(taskID, &principal))

	assertExactHTTPError(t, rec, http.StatusNotFound, "{\"error\":\"task not found\"}\n")
	if got := taskStatus(t, taskID); got != "draining" {
		t.Fatalf("AgentID mismatch mutated task: status=%q", got)
	}
}

func TestCancelAgentTask_OwnTaskScopedTokenCancelsAndRevokesCredential(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	for _, source := range []string{"task_token", "agent_inbox_token"} {
		t.Run(source, func(t *testing.T) {
			agentID := createHandlerTestAgent(t, "Agent Self Cancel "+source, []byte("[]"))
			taskID := ""
			deliveryID := ""
			if source == "task_token" {
				taskID = createHandlerTestTaskForAgent(t, agentID)
			} else {
				channelID := seedChannelForTest(t, "agent-self-cancel-"+uuid.NewString(), testUserID)
				if _, err := testPool.Exec(context.Background(), `
					INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
					VALUES ($1, $2, 'agent', $3)
					ON CONFLICT DO NOTHING`, channelID, testWorkspaceID, agentID,
				); err != nil {
					t.Fatalf("seed agent channel membership: %v", err)
				}
				taskID, deliveryID = seedChannelInboxWakeWithDelivery(t, channelID, agentID, source)
			}
			tokenHash := "cancel-self-" + source + "-" + uuid.NewString()
			if source == "task_token" {
				if _, err := testPool.Exec(context.Background(), `
					INSERT INTO task_token (token_hash, task_id, agent_id, workspace_id, user_id, expires_at)
					VALUES ($1, $2, $3, $4, $5, $6)`,
					tokenHash, taskID, agentID, testWorkspaceID, testUserID, time.Now().Add(time.Hour),
				); err != nil {
					t.Fatalf("seed task token: %v", err)
				}
			} else {
				if _, err := testPool.Exec(context.Background(), `
					INSERT INTO agent_inbox_token (token_hash, inbox_event_id, delivery_id, agent_id, workspace_id, user_id, expires_at)
					VALUES ($1, $2, $3, $4, $5, $6, $7)`,
					tokenHash, taskID, deliveryID, agentID, testWorkspaceID, testUserID, time.Now().Add(time.Hour),
				); err != nil {
					t.Fatalf("seed inbox token: %v", err)
				}
			}

			principal := taskScopedCancelPrincipal(agentID, taskID, source)
			rec := httptest.NewRecorder()
			callAgentTaskCancel(t, testHandler, rec, agentTaskCancelRequest(taskID, &principal))
			if rec.Code != http.StatusOK {
				t.Fatalf("response=(%d, %q), want exact status 200", rec.Code, rec.Body.String())
			}

			var body map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode response: %v; body=%s", err, rec.Body.String())
			}
			if body["id"] != taskID || body["agent_id"] != agentID || body["workspace_id"] != testWorkspaceID || body["status"] != "cancelled" {
				t.Fatalf("response identity/status=%v", body)
			}

			var status, outcome string
			if err := testPool.QueryRow(context.Background(), `
				SELECT status, COALESCE(terminal_outcome, '')
				FROM agent_inbox_event
				WHERE id = $1`, taskID,
			).Scan(&status, &outcome); err != nil {
				t.Fatalf("read cancelled task: %v", err)
			}
			if status != "suppressed" || outcome != "cancelled" {
				t.Fatalf("task terminal state=(%q, %q), want (suppressed, cancelled)", status, outcome)
			}

			tokenTable := "task_token"
			if source == "agent_inbox_token" {
				tokenTable = "agent_inbox_token"
			}
			var count int
			if err := testPool.QueryRow(context.Background(),
				"SELECT count(*) FROM "+tokenTable+" WHERE token_hash = $1", tokenHash,
			).Scan(&count); err != nil {
				t.Fatalf("count remaining credential: %v", err)
			}
			if count != 0 {
				t.Fatalf("%s credential remains after self-cancel: count=%d", source, count)
			}
		})
	}
}

func TestCancelAgentTask_TerminalOwnTaskIsIdempotent200(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	agentID := createHandlerTestAgent(t, "Agent Cancel Terminal", []byte("[]"))
	taskID := createHandlerTestTaskForAgent(t, agentID)
	cancelledEvents := 0
	testHandler.Bus.Subscribe(protocol.EventTaskCancelled, func(event events.Event) {
		payload, ok := event.Payload.(map[string]any)
		if ok && payload["task_id"] == taskID {
			cancelledEvents++
		}
	})
	principal := taskScopedCancelPrincipal(agentID, taskID, "task_token")
	rec := httptest.NewRecorder()

	callAgentTaskCancel(t, testHandler, rec, agentTaskCancelRequest(taskID, &principal))

	if rec.Code != http.StatusOK {
		t.Fatalf("response=(%d, %q), want idempotent 200", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v; body=%s", err, rec.Body.String())
	}
	if body["id"] != taskID || body["status"] != "cancelled" {
		t.Fatalf("response identity/status=%v", body)
	}
	if cancelledEvents != 1 {
		t.Fatalf("first cancellation published task:cancelled %d times, want 1", cancelledEvents)
	}

	tokenHash := "terminal-reissue-" + uuid.NewString()
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO task_token (token_hash, task_id, agent_id, workspace_id, user_id, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		tokenHash, taskID, agentID, testWorkspaceID, testUserID, time.Now().Add(time.Hour),
	); err != nil {
		t.Fatalf("seed credential before idempotent retry: %v", err)
	}
	second := httptest.NewRecorder()
	callAgentTaskCancel(t, testHandler, second, agentTaskCancelRequest(taskID, &principal))
	if second.Code != http.StatusOK {
		t.Fatalf("second response=(%d, %q), want idempotent 200", second.Code, second.Body.String())
	}
	if cancelledEvents != 1 {
		t.Fatalf("idempotent retry changed task:cancelled count to %d, want 1", cancelledEvents)
	}
	var remaining int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM task_token WHERE token_hash = $1`, tokenHash,
	).Scan(&remaining); err != nil {
		t.Fatalf("count credential after idempotent retry: %v", err)
	}
	if remaining != 1 {
		t.Fatalf("idempotent retry revoked a fresh credential: remaining=%d, want 1", remaining)
	}
}
