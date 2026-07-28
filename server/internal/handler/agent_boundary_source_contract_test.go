package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// Residual hard controls beyond Ronan agent_surface_source_contract_test.go:
//  - label attach audit actor must be agent (Parker audit product point)
//  - private peer hidden from directory
//  - directory must not leak secret instruction *values* even if keys evolve

// TestBoundary_LabelAttach_AuditActorIsAgent asserts EventIssueLabelsChanged
// publishes actor_type=agent and actor_id=agent (not owner human).
func TestBoundary_LabelAttach_AuditActorIsAgent(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "LabelAuditAgent", []byte("[]"))

	var issueID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO issue (workspace_id, title, status, priority, creator_type, creator_id, number, position)
		VALUES ($1, $2, 'todo', 'none', 'agent', $3, $4, 0)
		RETURNING id`,
		testWorkspaceID, "cf-label-audit-"+uuid.NewString(), agentID, 800000+int(uuid.New().ID()%100000),
	).Scan(&issueID); err != nil {
		t.Fatalf("create issue: %v", err)
	}
	t.Cleanup(func() { _, _ = testPool.Exec(context.Background(), `DELETE FROM issue WHERE id = $1`, issueID) })

	var labelID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO issue_label (workspace_id, name, color)
		VALUES ($1, $2, '#112233')
		RETURNING id`,
		testWorkspaceID, "cf-audit-"+uuid.NewString()[:8],
	).Scan(&labelID); err != nil {
		t.Fatalf("create label: %v", err)
	}
	t.Cleanup(func() { _, _ = testPool.Exec(context.Background(), `DELETE FROM issue_label WHERE id = $1`, labelID) })

	var (
		mu   sync.Mutex
		got  *events.Event
		done = make(chan struct{}, 1)
	)
	testHandler.Bus.Subscribe(protocol.EventIssueLabelsChanged, func(e events.Event) {
		mu.Lock()
		defer mu.Unlock()
		ev := e
		got = &ev
		select {
		case done <- struct{}{}:
		default:
		}
	})

	req := newRequest(http.MethodPost, "/api/agent/issues/"+issueID+"/labels", map[string]any{
		"label_id": labelID,
	})
	req = withAgentPrincipal(req, agentID, testWorkspaceID, testUserID)
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	req = withURLParam(req, "id", issueID)
	rec := httptest.NewRecorder()
	testHandler.AttachAgentIssueLabel(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("AttachAgentIssueLabel status=%d body=%s", rec.Code, rec.Body.String())
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for EventIssueLabelsChanged")
	}
	mu.Lock()
	defer mu.Unlock()
	if got == nil {
		t.Fatal("no event captured")
	}
	if got.ActorType != "agent" {
		t.Fatalf("audit ActorType=%q want agent (must not attribute to owner)", got.ActorType)
	}
	if got.ActorID != agentID {
		t.Fatalf("audit ActorID=%q want agent %s (not owner %s)", got.ActorID, agentID, testUserID)
	}
}

// TestBoundary_Directory_PrivateAgentHiddenFromPeer asserts other private
// agents are not listed (no owner-borrow directory expansion).
func TestBoundary_Directory_PrivateAgentHiddenFromPeer(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	privateID := createHandlerTestAgent(t, "DirPrivatePeer", []byte("[]"))
	if _, err := testPool.Exec(context.Background(), `
		UPDATE agent SET visibility = 'private' WHERE id = $1`, privateID); err != nil {
		t.Fatalf("set private: %v", err)
	}
	callerID := createHandlerTestAgent(t, "DirPrivateCaller", []byte("[]"))

	req := newRequest(http.MethodGet, "/api/agent/agents", nil)
	req = withAgentPrincipal(req, callerID, testWorkspaceID, testUserID)
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	rec := httptest.NewRecorder()
	testHandler.ListAgentDirectoryAgents(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var items []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &items); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, it := range items {
		if id, _ := it["id"].(string); id == privateID {
			t.Fatalf("private peer agent %s visible in directory — owner-borrow / over-share", privateID)
		}
	}
}

// TestBoundary_Directory_NoSecretInstructionValue seeds a public agent with a
// secret instructions payload and asserts the directory JSON body never contains it.
func TestBoundary_Directory_NoSecretInstructionValue(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	const secret = "SECRET_SYSTEM_PROMPT_DO_NOT_LEAK_BOUNDARY"
	victimID := createHandlerTestAgent(t, "DirSecretVictim", []byte("[]"))
	if _, err := testPool.Exec(context.Background(), `
		UPDATE agent SET visibility = 'workspace', instructions = $2
		WHERE id = $1`, victimID, secret); err != nil {
		t.Fatalf("seed secret instructions: %v", err)
	}
	callerID := createHandlerTestAgent(t, "DirSecretCaller", []byte("[]"))

	req := newRequest(http.MethodGet, "/api/agent/agents", nil)
	req = withAgentPrincipal(req, callerID, testWorkspaceID, testUserID)
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	rec := httptest.NewRecorder()
	testHandler.ListAgentDirectoryAgents(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), secret) {
		t.Fatalf("directory response body contains secret instructions for public agent %s", victimID)
	}
}
