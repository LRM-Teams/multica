package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/google/uuid"
)

func TestListRunnerActivitySummariesReturnsEmptyWorkspaceProjection(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	var workspaceID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO workspace (name, slug, description, issue_prefix)
		VALUES ($1, $2, '', 'RUN')
		RETURNING id`, "Runner Activity Summaries "+uuid.NewString()[:8], "runner-activity-summaries-"+uuid.NewString()[:8]).Scan(&workspaceID); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO member (workspace_id, user_id, role)
		VALUES ($1, $2, 'owner')`, workspaceID, testUserID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM member WHERE workspace_id = $1`, workspaceID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1`, workspaceID)
	})

	req := newRequestAs(testUserID, http.MethodGet, "/api/agents/runner-activity-summaries", nil)
	req.Header.Set("X-Workspace-ID", workspaceID)
	rec := httptest.NewRecorder()
	testHandler.ListRunnerActivitySummaries(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	var response RunnerActivitySummariesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(response.Items) != 0 {
		t.Fatalf("items=%+v want empty", response.Items)
	}
}

func TestListRunnerActivitySummariesMatchesPerAgentProjection(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	workingAgentID := createHandlerTestAgent(t, "runner-summary-working-"+uuid.NewString()[:8], nil)
	errorAgentID := createHandlerTestAgent(t, "runner-summary-error-"+uuid.NewString()[:8], nil)
	h := *testHandler
	h.RunnerPresenceSource = fakeRunnerPresenceSource{current: map[string]bool{
		"daemon-summary/" + testWorkspaceID + "/instance-summary": true,
	}}
	ctx := context.Background()
	for _, fixture := range []struct {
		agentID      string
		activityKind string
		detailKind   string
		factID       string
	}{
		{workingAgentID, "working", "running_command", "summary-working-" + uuid.NewString()},
		{errorAgentID, "error", "runtime_error", "summary-error-" + uuid.NewString()},
	} {
		if _, err := testPool.Exec(ctx, `
			INSERT INTO agent_activity_snapshot (
				workspace_id, agent_id, runtime_id, daemon_id, daemon_instance_id,
				launch_id, client_sequence, producer_fact_id, activity_kind,
				detail_kind, observed_at
			) VALUES ($1, $2, $3, 'daemon-summary', 'instance-summary',
				'launch-summary', 1, $4, $5, $6, now())`,
			testWorkspaceID, fixture.agentID, handlerTestRuntimeID(t), fixture.factID,
			fixture.activityKind, fixture.detailKind); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO agent_activity_entry (
			workspace_id, agent_id, runtime_id, daemon_id, daemon_instance_id,
			launch_id, client_sequence, producer_fact_id, entry_position,
			entry_kind, entry_body, observed_at
		) VALUES ($1, $2, $3, 'daemon-summary', 'instance-summary',
			'launch-summary', 1, $4, 0, 'narrative',
			'{"text":"payment required","activity_kind":"error","detail_kind":"runtime_error"}'::jsonb,
			now())`, testWorkspaceID, errorAgentID, handlerTestRuntimeID(t), "summary-error-entry-"+uuid.NewString()); err != nil {
		t.Fatal(err)
	}

	req := newRequestAs(testUserID, http.MethodGet, "/api/agents/runner-activity-summaries", nil)
	req.Header.Set("X-Workspace-ID", testWorkspaceID)
	rec := httptest.NewRecorder()
	h.ListRunnerActivitySummaries(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	var summaries RunnerActivitySummariesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &summaries); err != nil {
		t.Fatal(err)
	}

	byAgent := make(map[string]RunnerActivitySummaryResponseItem, len(summaries.Items))
	for _, item := range summaries.Items {
		byAgent[item.AgentID] = item
	}
	for _, agentID := range []string{workingAgentID, errorAgentID} {
		detailReq := newRequestAs(testUserID, http.MethodGet, "/api/agents/"+agentID+"/runner-activity", nil)
		detailReq.Header.Set("X-Workspace-ID", testWorkspaceID)
		detailReq = withURLParam(detailReq, "id", agentID)
		detailRec := httptest.NewRecorder()
		h.GetRunnerActivity(detailRec, detailReq)
		if detailRec.Code != http.StatusOK {
			t.Fatalf("detail status=%d body=%s want 200", detailRec.Code, detailRec.Body.String())
		}
		var detail RunnerActivityResponse
		if err := json.Unmarshal(detailRec.Body.Bytes(), &detail); err != nil {
			t.Fatal(err)
		}
		item, ok := byAgent[agentID]
		if !ok {
			t.Fatalf("missing summary for agent %s", agentID)
		}
		if detail.Summary == nil || !reflect.DeepEqual(item.Summary, *detail.Summary) {
			t.Fatalf("summary=%+v detail=%+v want identical projection", item.Summary, detail.Summary)
		}
	}
}

func TestListRunnerActivitySummariesEnforcesWorkspaceAndPrincipalBoundaries(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	var foreignWorkspaceID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO workspace (name, slug, description, issue_prefix)
		VALUES ($1, $2, '', 'RUN')
		RETURNING id`, "Runner Summary Boundary "+uuid.NewString()[:8], "runner-summary-boundary-"+uuid.NewString()[:8]).Scan(&foreignWorkspaceID); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(ctx, `INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, 'owner')`, foreignWorkspaceID, testUserID); err != nil {
		t.Fatal(err)
	}
	var foreignRuntimeID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_runtime (
			workspace_id, name, runtime_mode, provider, status, device_info, metadata, visibility, last_seen_at
		) VALUES ($1, $2, 'cloud', $3, 'online', '', '{}'::jsonb, 'public', now())
		RETURNING id`, foreignWorkspaceID, "Runner Summary Boundary Runtime", "runner_summary_boundary_"+uuid.NewString()).Scan(&foreignRuntimeID); err != nil {
		t.Fatal(err)
	}
	var foreignAgentID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent (
			workspace_id, name, display_name, description, runtime_mode, runtime_config,
			runtime_id, max_concurrent_tasks, owner_id, instructions, custom_env,
			custom_args, model
		) VALUES ($1, $2, 'Foreign Runner', '', 'cloud', '{}'::jsonb,
			$3, 1, $4, '', '{}'::jsonb, '[]'::jsonb, 'composer-1.5')
		RETURNING id`, foreignWorkspaceID, "foreign-runner-"+uuid.NewString()[:8], foreignRuntimeID, testUserID).Scan(&foreignAgentID); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO agent_activity_snapshot (
			workspace_id, agent_id, runtime_id, daemon_id, daemon_instance_id,
			launch_id, client_sequence, producer_fact_id, activity_kind,
			detail_kind, observed_at
		) VALUES ($1, $2, $3, 'daemon-boundary', 'instance-boundary',
			'launch-boundary', 1, $4, 'thinking', '', now())`,
		foreignWorkspaceID, foreignAgentID, foreignRuntimeID, "summary-boundary-"+uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	h := *testHandler
	h.RunnerPresenceSource = fakeRunnerPresenceSource{current: map[string]bool{
		"daemon-boundary/" + foreignWorkspaceID + "/instance-boundary": true,
	}}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1`, foreignWorkspaceID)
	})

	request := func(userID, workspaceID string) *http.Request {
		req := newRequestAs(userID, http.MethodGet, "/api/agents/runner-activity-summaries", nil)
		req.Header.Set("X-Workspace-ID", workspaceID)
		return req
	}
	read := func(t *testing.T, req *http.Request) (*httptest.ResponseRecorder, RunnerActivitySummariesResponse) {
		t.Helper()
		rec := httptest.NewRecorder()
		h.ListRunnerActivitySummaries(rec, req)
		var response RunnerActivitySummariesResponse
		if rec.Code == http.StatusOK {
			if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
		}
		return rec, response
	}

	t.Run("member sees only the selected Workspace", func(t *testing.T) {
		currentRec, current := read(t, request(testUserID, testWorkspaceID))
		if currentRec.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", currentRec.Code, currentRec.Body.String())
		}
		for _, item := range current.Items {
			if item.AgentID == foreignAgentID {
				t.Fatalf("foreign Agent leaked into current Workspace: %+v", item)
			}
		}

		foreignRec, foreign := read(t, request(testUserID, foreignWorkspaceID))
		if foreignRec.Code != http.StatusOK || len(foreign.Items) != 1 || foreign.Items[0].AgentID != foreignAgentID {
			t.Fatalf("status=%d body=%s projection=%+v", foreignRec.Code, foreignRec.Body.String(), foreign)
		}
	})

	t.Run("unauthenticated request is rejected", func(t *testing.T) {
		rec, _ := read(t, request("", testWorkspaceID))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status=%d body=%s want 401", rec.Code, rec.Body.String())
		}
	})

	t.Run("Agent principal is rejected", func(t *testing.T) {
		agentID := createHandlerTestAgent(t, "runner-summary-principal-"+uuid.NewString()[:8], nil)
		req := withAgentPrincipal(request(testUserID, testWorkspaceID), agentID, testWorkspaceID, testUserID)
		rec, _ := read(t, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status=%d body=%s want 403", rec.Code, rec.Body.String())
		}
	})
}
