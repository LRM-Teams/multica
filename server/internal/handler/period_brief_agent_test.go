package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestEnsurePeriodBriefAgent_ReturnsNotesAssistantAndArchivesWeeklyReport(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	_ = ensureSystemGeneralForTest(t)

	_, _ = testPool.Exec(ctx, `DELETE FROM agent WHERE workspace_id = $1 AND name = $2`, testWorkspaceID, retiredPeriodBriefAgentName)
	_, _ = testPool.Exec(ctx, `DELETE FROM agent WHERE workspace_id = $1 AND name = $2`, testWorkspaceID, notesAssistantAgentName)

	notesID := insertNamedHandlerTestAgent(t, notesAssistantAgentName, "笔记助手")
	weeklyID := insertNamedHandlerTestAgent(t, retiredPeriodBriefAgentName, "周报")

	rec := httptest.NewRecorder()
	req := newRequestAs(testUserID, http.MethodPost, "/api/agents/period-brief", map[string]string{})
	req.Header.Set("X-Workspace-ID", testWorkspaceID)
	testHandler.EnsurePeriodBriefAgent(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("ensure=%d body=%s", rec.Code, rec.Body.String())
	}
	var existing EnsurePeriodBriefAgentResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &existing); err != nil {
		t.Fatal(err)
	}
	if existing.Created {
		t.Fatal("must not create 周报")
	}
	if existing.Agent.ID != notesID {
		t.Fatalf("synthesizer=%q want notes assistant %q", existing.Agent.ID, notesID)
	}
	if existing.Agent.Name != notesAssistantAgentName {
		t.Fatalf("name=%q want %q", existing.Agent.Name, notesAssistantAgentName)
	}

	var weeklyArchived bool
	if err := testPool.QueryRow(ctx, `SELECT archived_at IS NOT NULL FROM agent WHERE id = $1`, weeklyID).Scan(&weeklyArchived); err != nil {
		t.Fatal(err)
	}
	if !weeklyArchived {
		t.Fatal("leftover weekly-report must be archived")
	}
}

func TestEnsurePeriodBriefAgent_ConflictsWhenNotesAssistantMissing(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	_ = ensureSystemGeneralForTest(t)
	_, _ = testPool.Exec(ctx, `DELETE FROM agent WHERE workspace_id = $1 AND name = $2`, testWorkspaceID, retiredPeriodBriefAgentName)
	_, _ = testPool.Exec(ctx, `DELETE FROM agent WHERE workspace_id = $1 AND name = $2`, testWorkspaceID, notesAssistantAgentName)

	rec := httptest.NewRecorder()
	req := newRequestAs(testUserID, http.MethodPost, "/api/agents/period-brief", map[string]string{})
	req.Header.Set("X-Workspace-ID", testWorkspaceID)
	testHandler.EnsurePeriodBriefAgent(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("missing notes assistant=%d body=%s", rec.Code, rec.Body.String())
	}
}

func insertNamedHandlerTestAgent(t *testing.T, name, displayName string) string {
	t.Helper()
	var agentID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO agent (
			workspace_id, name, display_name, description, runtime_mode, runtime_config, runtime_id,
			max_concurrent_tasks, owner_id, instructions, custom_env, custom_args, mcp_config, model
		) VALUES ($1, $2, $3, '', 'cloud', '{}'::jsonb, $4, 6, $5, 'Standalone Agent Chat', '{}'::jsonb, '[]'::jsonb, '{}'::jsonb, 'composer-1.5')
		RETURNING id
	`, testWorkspaceID, name, displayName, handlerTestRuntimeID(t), testUserID).Scan(&agentID); err != nil {
		t.Fatalf("insert named agent %s: %v", name, err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM agent WHERE id = $1`, agentID)
	})
	return agentID
}
