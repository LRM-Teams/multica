package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/spf13/cobra"
)

func newIssueCompleteTestCmd(t *testing.T) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{Use: "complete"}
	cmd.Flags().String("server-url", "", "")
	cmd.Flags().String("workspace-id", "", "")
	cmd.Flags().String("profile", "", "")
	cmd.Flags().String("summary", "", "")
	cmd.Flags().StringArray("evidence", nil, "")
	cmd.Flags().StringArray("artifact", nil, "")
	cmd.Flags().StringArray("risk", nil, "")
	cmd.Flags().String("output", "json", "")
	return cmd
}

func TestRunIssueCompleteBuildsTypedContractFromCanonicalIssue(t *testing.T) {
	const issueID = "11111111-1111-4111-8111-111111111111"
	var submitted struct {
		ExpectedExecutionRevision int64                             `json:"expected_execution_revision"`
		Summary                   string                            `json:"summary"`
		AcceptanceResults         []issueCompletionAcceptanceResult `json:"acceptance_results"`
	}
	getCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/agent/issues/"+issueID {
			getCount++
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": issueID, "identifier": "LRM-1624", "execution_revision": 7,
				"acceptance_criteria": []string{"man A-F screenshots pass", "canonical PR link exists"},
			})
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/api/agent/issues/"+issueID+"/completion" {
			if err := json.NewDecoder(r.Body).Decode(&submitted); err != nil {
				t.Errorf("decode completion: %v", err)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"report": map[string]any{"review_status": "pending"}})
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(server.Close)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("MULTICA_SERVER_URL", server.URL)
	t.Setenv("MULTICA_WORKSPACE_ID", "workspace-test")
	t.Setenv("MULTICA_TOKEN", "mat_completion_test")

	cmd := newIssueCompleteTestCmd(t)
	_ = cmd.Flags().Set("summary", "Restored old/new world continuity")
	_ = cmd.Flags().Set("evidence", "0=screenshot:artifact://man-a-f")
	_ = cmd.Flags().Set("evidence", "1=pull_request:https://github.com/LRM-Teams/game/pull/65")
	if err := runIssueComplete(cmd, []string{issueID}); err != nil {
		t.Fatalf("runIssueComplete: %v", err)
	}
	if getCount != 2 {
		t.Fatalf("canonical Issue GET count = %d, want 2 (resolve + frozen contract)", getCount)
	}
	if submitted.ExpectedExecutionRevision != 7 || submitted.Summary == "" {
		t.Fatalf("submitted header = %#v", submitted)
	}
	if len(submitted.AcceptanceResults) != 2 ||
		submitted.AcceptanceResults[0].Criterion != "man A-F screenshots pass" ||
		submitted.AcceptanceResults[1].EvidenceRefs[0].Kind != "pull_request" {
		t.Fatalf("typed acceptance results = %#v", submitted.AcceptanceResults)
	}
}

func TestRunIssueCompleteRejectsMissingCriterionEvidenceBeforeSubmit(t *testing.T) {
	const issueID = "22222222-2222-4222-8222-222222222222"
	postCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": issueID, "execution_revision": 2,
				"acceptance_criteria": []string{"gameplay passes", "visual review passes"},
			})
			return
		}
		postCount++
		http.Error(w, "unexpected submit", http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("MULTICA_SERVER_URL", server.URL)
	t.Setenv("MULTICA_WORKSPACE_ID", "workspace-test")
	t.Setenv("MULTICA_TOKEN", "mat_completion_test")

	cmd := newIssueCompleteTestCmd(t)
	_ = cmd.Flags().Set("summary", "Gameplay implemented")
	_ = cmd.Flags().Set("evidence", "0=test:artifact://gameplay")
	if err := runIssueComplete(cmd, []string{issueID}); err == nil {
		t.Fatal("expected missing visual-review evidence to fail")
	}
	if postCount != 0 {
		t.Fatalf("completion POST count = %d, want 0", postCount)
	}
}
