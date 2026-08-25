package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/google/uuid"
)

type collectingAgentLifecycleNotifier struct {
	mu       sync.Mutex
	commands []string
}

func (n *collectingAgentLifecycleNotifier) NotifyAgentRestartCommand(_, _, eventType, _ string, _ any) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.commands = append(n.commands, eventType)
	return true
}

func (n *collectingAgentLifecycleNotifier) count() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return len(n.commands)
}

func TestBulkAgentLifecycleStartsMultipleAgentsInOneRequest(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	firstAgentID, _ := createAgentRestartFixture(t, true)
	secondAgentID, _ := createAgentRestartFixtureWithProvider(t, true, "restart-test-bulk-second")
	notifier := &collectingAgentLifecycleNotifier{}
	previousNotifier := testHandler.AgentRestartNotifier
	testHandler.AgentRestartNotifier = notifier
	t.Cleanup(func() { testHandler.AgentRestartNotifier = previousNotifier })

	rec := httptest.NewRecorder()
	req := newRequestAs(testUserID, http.MethodPost, "/api/agents/lifecycle", map[string]any{
		"agent_ids": []string{firstAgentID, secondAgentID},
		"action":    "start",
	})
	testHandler.BulkAgentLifecycle(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("bulk lifecycle status = %d, want 202: %s", rec.Code, rec.Body.String())
	}
	var response bulkAgentLifecycleResponse
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(response.Results) != 2 || !response.Results[0].Accepted || !response.Results[1].Accepted {
		t.Fatalf("bulk lifecycle results = %+v, want both accepted", response.Results)
	}
	if notifier.count() != 2 {
		t.Fatalf("daemon command count = %d, want 2", notifier.count())
	}
}

func TestBulkAgentLifecycleRejectsUnknownAgentBeforeDispatch(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	agentID, _ := createAgentRestartFixture(t, true)
	notifier := &collectingAgentLifecycleNotifier{}
	previousNotifier := testHandler.AgentRestartNotifier
	testHandler.AgentRestartNotifier = notifier
	t.Cleanup(func() { testHandler.AgentRestartNotifier = previousNotifier })

	rec := httptest.NewRecorder()
	req := newRequestAs(testUserID, http.MethodPost, "/api/agents/lifecycle", map[string]any{
		"agent_ids": []string{agentID, uuid.NewString()},
		"action":    "start",
	})
	testHandler.BulkAgentLifecycle(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("bulk lifecycle status = %d, want 404: %s", rec.Code, rec.Body.String())
	}
	if notifier.count() != 0 {
		t.Fatalf("daemon command count = %d, want no partial dispatch", notifier.count())
	}
}
