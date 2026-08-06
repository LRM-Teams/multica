package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestBatchUpdateNoMutationReturnsZero — regression for #1660.
//
// When the request payload has valid issue_ids but the "updates" field
// is empty, missing, or doesn't decode any known mutation field, the
// handler used to walk every issue, run a no-op UPDATE, and increment
// `updated` for each one — returning {"updated": N} despite changing
// nothing. Reporters saw 200 + a positive count and assumed the call
// worked, then chased a phantom persistence bug.
//
// The fix is "tell the truth": when no mutation field is present, return
// {"updated": 0} immediately so the count matches reality.
func TestBatchUpdateNoMutationReturnsZero(t *testing.T) {
	// Two fresh issues so we can also assert no fields actually changed.
	a := createTestIssue(t, "BU-no-mut A", "todo", "low")
	b := createTestIssue(t, "BU-no-mut B", "todo", "low")
	t.Cleanup(func() { deleteTestIssue(t, a) })
	t.Cleanup(func() { deleteTestIssue(t, b) })

	cases := []struct {
		desc string
		body map[string]any
	}{
		{
			desc: "updates_missing",
			// Most common reporter pattern: status at top level.
			body: map[string]any{"issue_ids": []string{a, b}, "status": "in_progress"},
		},
		{
			desc: "updates_empty_object",
			body: map[string]any{"issue_ids": []string{a, b}, "updates": map[string]any{}},
		},
		{
			desc: "updates_misnamed",
			// Singular "update" instead of plural "updates".
			body: map[string]any{"issue_ids": []string{a, b}, "update": map[string]any{"status": "done"}},
		},
		{
			desc: "updates_unknown_field_only",
			// Payload IS nested correctly, but every key inside `updates` is
			// unknown to the handler — same class of caller mistake as the
			// shapes above. hasMutation must stay false; behavior is already
			// correct, this case locks it in against future regressions.
			body: map[string]any{"issue_ids": []string{a, b}, "updates": map[string]any{"foo": "bar"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.desc, func(t *testing.T) {
			w := httptest.NewRecorder()
			req := newRequest("POST", "/api/issues/batch-update", tc.body)
			testHandler.BatchUpdateIssues(w, req)
			if w.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
			}
			var resp struct {
				Updated int `json:"updated"`
			}
			json.NewDecoder(w.Body).Decode(&resp)
			if resp.Updated != 0 {
				t.Errorf("expected updated=0 when no mutation field present, got %d", resp.Updated)
			}

			// Belt and braces: confirm the issues weren't touched.
			for _, id := range []string{a, b} {
				gw := httptest.NewRecorder()
				gr := newRequest("GET", "/api/issues/"+id, nil)
				gr = withURLParam(gr, "id", id)
				testHandler.GetIssue(gw, gr)
				var got IssueResponse
				json.NewDecoder(gw.Body).Decode(&got)
				if got.Status != "todo" {
					t.Errorf("issue %s: status changed to %q despite no-mutation request", id, got.Status)
				}
			}
		})
	}
}

// TestBatchUpdateValidUpdatesPersistAndCount — positive case to lock in
// happy-path behavior alongside the regression test above.
func TestBatchUpdateValidUpdatesPersistAndCount(t *testing.T) {
	a := createTestIssue(t, "BU-ok A", "todo", "low")
	b := createTestIssue(t, "BU-ok B", "todo", "low")
	t.Cleanup(func() { deleteTestIssue(t, a) })
	t.Cleanup(func() { deleteTestIssue(t, b) })

	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues/batch-update", map[string]any{
		"issue_ids": []string{a, b},
		"updates":   map[string]any{"status": "in_progress"},
	})
	testHandler.BatchUpdateIssues(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Updated int `json:"updated"`
	}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Updated != 2 {
		t.Errorf("expected updated=2, got %d", resp.Updated)
	}
	for _, id := range []string{a, b} {
		gw := httptest.NewRecorder()
		gr := newRequest("GET", "/api/issues/"+id, nil)
		gr = withURLParam(gr, "id", id)
		testHandler.GetIssue(gw, gr)
		var got IssueResponse
		json.NewDecoder(gw.Body).Decode(&got)
		if got.Status != "in_progress" {
			t.Errorf("issue %s: expected status=in_progress, got %q", id, got.Status)
		}
	}
}

func TestBatchUpdateProjectWorkspaceBoundary(t *testing.T) {
	ctx := context.Background()
	issueID := createTestIssue(t, "BU-project-boundary", "todo", "low")
	t.Cleanup(func() { deleteTestIssue(t, issueID) })

	var localProjectID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO project (workspace_id, title, status, priority)
		VALUES ($1, $2, 'planned', 'none')
		RETURNING id
	`, testWorkspaceID, "Batch local project").Scan(&localProjectID); err != nil {
		t.Fatalf("insert local project: %v", err)
	}
	t.Cleanup(func() { _, _ = testPool.Exec(ctx, `DELETE FROM project WHERE id = $1`, localProjectID) })

	var foreignWorkspaceID string
	suffix := time.Now().UnixNano()
	if err := testPool.QueryRow(ctx, `
		INSERT INTO workspace (name, slug, description, issue_prefix)
		VALUES ($1, $2, $3, $4)
		RETURNING id
	`, "Batch project boundary", fmt.Sprintf("batch-project-boundary-%d", suffix), "Foreign workspace", "BPB").Scan(&foreignWorkspaceID); err != nil {
		t.Fatalf("insert foreign workspace: %v", err)
	}
	t.Cleanup(func() { _, _ = testPool.Exec(ctx, `DELETE FROM workspace WHERE id = $1`, foreignWorkspaceID) })

	var foreignProjectID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO project (workspace_id, title, status, priority)
		VALUES ($1, $2, 'planned', 'none')
		RETURNING id
	`, foreignWorkspaceID, "Batch foreign project").Scan(&foreignProjectID); err != nil {
		t.Fatalf("insert foreign project: %v", err)
	}

	batchUpdate := func(projectID any) int {
		t.Helper()
		w := httptest.NewRecorder()
		req := newRequest("POST", "/api/issues/batch-update", map[string]any{
			"issue_ids": []string{issueID},
			"updates":   map[string]any{"project_id": projectID},
		})
		testHandler.BatchUpdateIssues(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("BatchUpdateIssues: expected 200, got %d: %s", w.Code, w.Body.String())
		}
		var resp struct {
			Updated int `json:"updated"`
		}
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatalf("decode BatchUpdateIssues response: %v", err)
		}
		return resp.Updated
	}
	readProjectID := func() *string {
		t.Helper()
		var projectID *string
		if err := testPool.QueryRow(ctx, `SELECT project_id FROM issue WHERE id = $1`, issueID).Scan(&projectID); err != nil {
			t.Fatalf("read issue project_id: %v", err)
		}
		return projectID
	}

	if updated := batchUpdate(localProjectID); updated != 1 {
		t.Fatalf("local project update count = %d, want 1", updated)
	}
	if got := readProjectID(); got == nil || *got != localProjectID {
		t.Fatalf("local project update persisted %v, want %q", got, localProjectID)
	}
	if updated := batchUpdate(nil); updated != 1 {
		t.Fatalf("clear project update count = %d, want 1", updated)
	}
	if got := readProjectID(); got != nil {
		t.Fatalf("clear project persisted %q, want NULL", *got)
	}
	if updated := batchUpdate(localProjectID); updated != 1 {
		t.Fatalf("restore local project update count = %d, want 1", updated)
	}
	if updated := batchUpdate(foreignProjectID); updated != 0 {
		t.Fatalf("foreign project update count = %d, want 0", updated)
	}
	if got := readProjectID(); got == nil || *got != localProjectID {
		t.Fatalf("foreign project update persisted %v, want unchanged %q", got, localProjectID)
	}
}

// createTestIssue is a small helper to keep the table-driven cases clean.
// Returns the new issue's id; caller is responsible for cleanup.
func createTestIssue(t *testing.T, title, status, priority string) string {
	t.Helper()
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":    title,
		"status":   status,
		"priority": priority,
	})
	testHandler.CreateIssue(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateIssue %q: expected 201, got %d: %s", title, w.Code, w.Body.String())
	}
	var issue IssueResponse
	json.NewDecoder(w.Body).Decode(&issue)
	return issue.ID
}

func deleteTestIssue(t *testing.T, id string) {
	t.Helper()
	w := httptest.NewRecorder()
	req := newRequest("DELETE", "/api/issues/"+id, nil)
	req = withURLParam(req, "id", id)
	testHandler.DeleteIssue(w, req)
}
