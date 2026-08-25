package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAgentCannotBypassCompletionReviewWithDirectStatus(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	runtimeID := createClaimReclaimRuntime(t, ctx, "completion direct done runtime")
	agentID, issueID := createClaimReclaimAgentAndIssue(t, ctx, runtimeID, "completion direct done")
	if _, err := testPool.Exec(ctx, `
		UPDATE issue SET status='in_progress', assignee_type='agent', assignee_id=$2 WHERE id=$1`,
		issueID, agentID); err != nil {
		t.Fatal(err)
	}

	for _, forbiddenStatus := range []string{"in_review", "done"} {
		recorder := httptest.NewRecorder()
		request := withURLParam(newRequest(http.MethodPut, "/api/agent/issues/"+issueID, map[string]any{
			"status": forbiddenStatus,
		}), "id", issueID)
		request = withAgentCredentialPrincipal(request, agentID, testWorkspaceID, testUserID)
		testHandler.UpdateAgentIssue(recorder, request)
		if recorder.Code != http.StatusConflict {
			t.Fatalf("direct %s status = %d body=%s, want 409", forbiddenStatus, recorder.Code, recorder.Body.String())
		}
	}
	var status string
	if err := testPool.QueryRow(ctx, `SELECT status FROM issue WHERE id=$1`, issueID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "in_progress" {
		t.Fatalf("Issue status = %q after rejected direct completion, want in_progress", status)
	}
}
