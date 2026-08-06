package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

func TestGetRunnerActivityEnforcesObjectWorkspaceAndPrincipalBoundaries(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	agentID := createHandlerTestAgent(t, "runner-activity-read-"+uuid.NewString()[:8], nil)

	request := func(userID, workspaceID, id string) *http.Request {
		req := newRequestAs(userID, http.MethodGet, "/api/agents/"+id+"/runner-activity", nil)
		req.Header.Set("X-Workspace-ID", workspaceID)
		return withURLParam(req, "id", id)
	}

	t.Run("existing workspace member reads an empty projection", func(t *testing.T) {
		rec := httptest.NewRecorder()
		testHandler.GetRunnerActivity(rec, request(testUserID, testWorkspaceID, agentID))
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s want 200", rec.Code, rec.Body.String())
		}
	})

	t.Run("missing object is not found", func(t *testing.T) {
		rec := httptest.NewRecorder()
		testHandler.GetRunnerActivity(rec, request(testUserID, testWorkspaceID, uuid.NewString()))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status=%d body=%s want 404", rec.Code, rec.Body.String())
		}
	})

	t.Run("agent principal is forbidden", func(t *testing.T) {
		req := request(testUserID, testWorkspaceID, agentID)
		req = withAgentPrincipal(req, agentID, testWorkspaceID, testUserID)
		rec := httptest.NewRecorder()
		testHandler.GetRunnerActivity(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status=%d body=%s want 403", rec.Code, rec.Body.String())
		}
	})

	t.Run("other workspace cannot resolve this agent", func(t *testing.T) {
		var otherWorkspaceID string
		if err := testPool.QueryRow(context.Background(), `
			INSERT INTO workspace (name, slug, description, issue_prefix)
			VALUES ($1, $2, '', 'RUN')
			RETURNING id`, "Runner Activity Other "+uuid.NewString()[:8], "runner-activity-other-"+uuid.NewString()[:8]).Scan(&otherWorkspaceID); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_, _ = testPool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1`, otherWorkspaceID)
		})
		rec := httptest.NewRecorder()
		testHandler.GetRunnerActivity(rec, request(testUserID, otherWorkspaceID, agentID))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status=%d body=%s want 404", rec.Code, rec.Body.String())
		}
	})
}
