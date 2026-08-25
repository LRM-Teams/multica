package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/multica-ai/multica/server/internal/service"
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

func TestSubmitAgentIssueCompletionResolvesRunFromActiveClaimForDurableCredential(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	issue, agentID, run := prepareCompletionRun(t, "durable credential completion")
	input := webGameCompletionInput(issue, agentID, uuidToString(run.RunID))
	input.AcceptanceResults[1].EvidenceRefs = []service.CompletionEvidenceRef{
		{Kind: "screenshot", Ref: "artifact://visual-gate"},
	}
	input.ArtifactRefs = nil

	body, err := json.Marshal(map[string]any{
		"expected_execution_revision": input.ExpectedExecutionRevision,
		"summary":                     input.Summary,
		"acceptance_results":          input.AcceptanceResults,
		"artifact_refs":               input.ArtifactRefs,
		"risks":                       input.Risks,
	})
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	request := withURLParam(newRequest(http.MethodPost, "/api/agent/issues/"+uuidToString(issue.ID)+"/completion", json.RawMessage(body)), "id", uuidToString(issue.ID))
	request = withAgentCredentialPrincipal(request, agentID, testWorkspaceID, testUserID)
	testHandler.SubmitAgentIssueCompletion(recorder, request)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("durable credential completion = %d body=%s, want 201", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Issue struct {
			Status string `json:"status"`
		} `json:"issue"`
		Report struct {
			RunID string `json:"run_id"`
		} `json:"report"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Issue.Status != "in_review" {
		t.Fatalf("Issue status = %q, want in_review", response.Issue.Status)
	}
	if response.Report.RunID != uuidToString(run.RunID) {
		t.Fatalf("report run_id = %q, want active claim %s", response.Report.RunID, uuidToString(run.RunID))
	}
}
