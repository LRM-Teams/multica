package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestClaimTaskUsesEnqueuedExecutionConfig(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	runtimeID := createClaimReclaimRuntime(t, ctx, "execution config claim runtime")
	agentID, issueID := createClaimReclaimAgentAndIssue(t, ctx, runtimeID, "execution config claim agent")
	if _, err := testPool.Exec(ctx, `UPDATE agent SET model = 'queued-model', thinking_level = 'high' WHERE id = $1`, agentID); err != nil {
		t.Fatalf("seed agent execution config: %v", err)
	}
	configContext, err := service.WithTaskExecutionConfig(nil, "queued-model", "high")
	if err != nil {
		t.Fatalf("build execution config: %v", err)
	}
	task, err := testHandler.Queries.CreateAgentTask(ctx, db.CreateAgentTaskParams{
		AgentID:           parseUUID(agentID),
		RuntimeID:         parseUUID(runtimeID),
		IssueID:           parseUUID(issueID),
		Priority:          0,
		Context:           configContext,
		ForceFreshSession: pgtype.Bool{Bool: true, Valid: true},
	})
	if err != nil {
		t.Fatalf("enqueue task: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM agent_inbox_event WHERE id = $1`, task.ID) })

	if _, err := testPool.Exec(ctx, `UPDATE agent SET model = 'edited-model', thinking_level = 'minimal' WHERE id = $1`, agentID); err != nil {
		t.Fatalf("edit agent after enqueue: %v", err)
	}

	w := httptest.NewRecorder()
	req := newDaemonTokenRequest(http.MethodPost, "/api/daemon/runtimes/"+runtimeID+"/claim", nil, testWorkspaceID, "execution-config-claim")
	req = withURLParam(req, "runtimeId", runtimeID)
	claimTaskThroughInboxForTest(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("claim task: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var response struct {
		Task *struct {
			Agent *TaskAgentData `json:"agent"`
		} `json:"task"`
	}
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("decode claim response: %v", err)
	}
	if response.Task == nil || response.Task.Agent == nil {
		t.Fatalf("claim response missing task agent: %s", w.Body.String())
	}
	if response.Task.Agent.Model != "queued-model" || response.Task.Agent.ThinkingLevel != "high" {
		t.Fatalf("claimed config = model:%q thinking:%q, want enqueue snapshot", response.Task.Agent.Model, response.Task.Agent.ThinkingLevel)
	}
}
