package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestEvolutionMetricsReturnsTaskEfficiency(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	req := newRequest(http.MethodGet, "/api/evolution/metrics?workspace_id="+testWorkspaceID, nil)
	rec := httptest.NewRecorder()
	testHandler.GetEvolutionMetrics(rec, req)

	if rec.Code != http.StatusOK {
		var response EvolutionMetricsResponse
		err := testHandler.loadEvolutionTaskEfficiency(req, testWorkspaceID, 30, &response)
		t.Fatalf("status=%d body=%s task_efficiency_error=%v", rec.Code, rec.Body.String(), err)
	}

	var response EvolutionMetricsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.TaskEfficiency.IssueCount < 0 {
		t.Fatalf("issue_count=%d, want non-negative", response.TaskEfficiency.IssueCount)
	}
	if response.CollaborationEvolution.AttentionRounds < 0 || response.CollaborationEvolution.ImmutableDecisionAuditEvents < 0 {
		t.Fatalf("collaboration metrics must be non-negative: %+v", response.CollaborationEvolution)
	}
	if response.ModelEvolution.AttentionStudentMode == "" {
		t.Fatalf("attention_student_mode must expose the PR6 runtime stub state")
	}
}
