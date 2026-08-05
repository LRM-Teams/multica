package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/middleware"
)

func TestDecodeResearchJSON(t *testing.T) {
	type request struct {
		Goal string `json:"goal"`
	}
	tests := []struct {
		name       string
		body       string
		wantOK     bool
		wantStatus int
	}{
		{name: "valid", body: `{"goal":"compare"}`, wantOK: true},
		{name: "unknown field", body: `{"goal":"compare","extra":true}`, wantStatus: http.StatusBadRequest},
		{name: "second object", body: `{"goal":"compare"}{"goal":"replace"}`, wantStatus: http.StatusBadRequest},
		{name: "too large", body: `{"goal":"` + strings.Repeat("x", int(maxResearchControlRequestBytes)) + `"}`, wantStatus: http.StatusRequestEntityTooLarge},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			httpRequest := httptest.NewRequest(http.MethodPost, "/research", strings.NewReader(tt.body))
			var got request
			ok := decodeResearchJSON(recorder, httpRequest, &got)
			if ok != tt.wantOK {
				t.Fatalf("decodeResearchJSON() = %v, want %v", ok, tt.wantOK)
			}
			if tt.wantOK {
				if got.Goal != "compare" {
					t.Fatalf("goal = %q, want compare", got.Goal)
				}
				return
			}
			if recorder.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", recorder.Code, tt.wantStatus)
			}
		})
	}
}

// Production regression: a correctly leased research Agent used to receive a
// 403 while submitting its structured task result through the Agent route.
func TestResolveResearchResultInboxTaskIDAllowsActiveAgentCredentialDelivery(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	fixture := seedAgentCredentialTransportFixture(t)
	sessionID := uuid.NewString()
	taskID := uuid.NewString()
	attemptID := uuid.NewString()
	if _, err := testPool.Exec(context.Background(), `
		UPDATE agent_inbox_event
		SET context = jsonb_build_object(
		  'type', 'research_run_task',
		  'research_session_id', $2::text,
		  'research_task_id', $3::text,
		  'research_attempt_id', $4::text
		)
		WHERE id = $1::uuid
	`, fixture.event.ID, sessionID, taskID, attemptID); err != nil {
		t.Fatalf("bind research context: %v", err)
	}

	req := newRequest(http.MethodPost, "/api/agent/research/task-result", nil)
	req = withChatTestWorkspaceCtx(t, req)
	req.Header.Set("X-Agent-ID", fixture.agentID)
	req.Header.Set("X-Agent-Inbox-Event-ID", fixture.event.ID)
	req.Header.Set("X-Agent-Inbox-Delivery-ID", fixture.event.DeliveryID)
	req.Header.Set("X-Agent-Inbox-Lease-Token", fixture.event.LeaseToken)
	principal := middleware.AgentPrincipal{
		AgentID: fixture.agentID, WorkspaceID: testWorkspaceID, ActorSource: "agent_credential",
	}
	recorder := httptest.NewRecorder()
	got, ok := testHandler.resolveResearchResultInboxTaskID(recorder, req, principal, sessionID, taskID, attemptID)
	if !ok {
		t.Fatalf("active research delivery rejected: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if got != fixture.event.ID {
		t.Fatalf("inbox task id=%q, want %q", got, fixture.event.ID)
	}
}

func TestResolveResearchResultInboxTaskIDRejectsMismatchedResearchContext(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	fixture := seedAgentCredentialTransportFixture(t)
	sessionID := uuid.NewString()
	taskID := uuid.NewString()
	attemptID := uuid.NewString()
	if _, err := testPool.Exec(context.Background(), `
		UPDATE agent_inbox_event
		SET context = jsonb_build_object(
		  'type', 'research_run_task',
		  'research_session_id', $2::text,
		  'research_task_id', $3::text,
		  'research_attempt_id', $4::text
		)
		WHERE id = $1::uuid
	`, fixture.event.ID, sessionID, taskID, uuid.NewString()); err != nil {
		t.Fatalf("bind research context: %v", err)
	}

	req := newRequest(http.MethodPost, "/api/agent/research/task-result", nil)
	req = withChatTestWorkspaceCtx(t, req)
	req.Header.Set("X-Agent-ID", fixture.agentID)
	req.Header.Set("X-Agent-Inbox-Event-ID", fixture.event.ID)
	req.Header.Set("X-Agent-Inbox-Delivery-ID", fixture.event.DeliveryID)
	req.Header.Set("X-Agent-Inbox-Lease-Token", fixture.event.LeaseToken)
	principal := middleware.AgentPrincipal{
		AgentID: fixture.agentID, WorkspaceID: testWorkspaceID, ActorSource: "agent_credential",
	}
	recorder := httptest.NewRecorder()
	if _, ok := testHandler.resolveResearchResultInboxTaskID(recorder, req, principal, sessionID, taskID, attemptID); ok {
		t.Fatal("mismatched research attempt context was accepted")
	}
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status=%d, want 403: %s", recorder.Code, recorder.Body.String())
	}
}

func TestResolveResearchResultInboxTaskIDRejectsExpiredAgentCredentialDelivery(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	fixture := seedAgentCredentialTransportFixture(t)
	sessionID := uuid.NewString()
	taskID := uuid.NewString()
	attemptID := uuid.NewString()
	if _, err := testPool.Exec(context.Background(), `
		UPDATE agent_inbox_event
		SET context = jsonb_build_object(
		  'type', 'research_run_task',
		  'research_session_id', $2::text,
		  'research_task_id', $3::text,
		  'research_attempt_id', $4::text
		)
		WHERE id = $1::uuid
	`, fixture.event.ID, sessionID, taskID, attemptID); err != nil {
		t.Fatalf("bind research context: %v", err)
	}
	if _, err := testPool.Exec(context.Background(), `
		UPDATE agent_event_delivery
		SET lease_expires_at = now() - interval '1 second'
		WHERE id = $1::uuid
	`, fixture.event.DeliveryID); err != nil {
		t.Fatalf("expire delivery: %v", err)
	}

	req := newRequest(http.MethodPost, "/api/agent/research/task-result", nil)
	req = withChatTestWorkspaceCtx(t, req)
	req.Header.Set("X-Agent-ID", fixture.agentID)
	req.Header.Set("X-Agent-Inbox-Event-ID", fixture.event.ID)
	req.Header.Set("X-Agent-Inbox-Delivery-ID", fixture.event.DeliveryID)
	req.Header.Set("X-Agent-Inbox-Lease-Token", fixture.event.LeaseToken)
	principal := middleware.AgentPrincipal{
		AgentID: fixture.agentID, WorkspaceID: testWorkspaceID, ActorSource: "agent_credential",
	}
	recorder := httptest.NewRecorder()
	if _, ok := testHandler.resolveResearchResultInboxTaskID(recorder, req, principal, sessionID, taskID, attemptID); ok {
		t.Fatal("expired research delivery was accepted")
	}
	if recorder.Code != http.StatusConflict {
		t.Fatalf("status=%d, want 409: %s", recorder.Code, recorder.Body.String())
	}
}
