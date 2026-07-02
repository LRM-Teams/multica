package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/multica-ai/multica/server/internal/middleware"
)

func seedEvolutionReviewSubmission(t *testing.T, status string) string {
	t.Helper()
	return seedEvolutionReviewSubmissionWithMetadata(t, status, map[string]any{})
}

func seedEvolutionReviewSubmissionWithMetadata(t *testing.T, status string, metadata map[string]any) string {
	t.Helper()
	agentID := createHandlerTestAgent(t, "Evolution Review Bot "+randomID(), []byte("[]"))
	localID := "review-" + randomID()
	content := "Run targeted Go tests before broader checks."
	reviewMetadata, err := json.Marshal(metadata)
	if err != nil {
		t.Fatalf("marshal review metadata: %v", err)
	}
	var submissionID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO evolution_unit_submission (
			workspace_id, source_agent_id, unit_type, local_unit_id, title, summary,
			content, content_hash, sensitivity, confidence, status, review_decision,
			review_risk_level, review_reason, review_metadata, reviewed_at
		) VALUES (
			$1, $2, 'memory', $3, 'Targeted Go tests', 'Run narrow tests before broader checks.',
			$4, $5, 'none', 'high', $6, 'needs_review', 'medium', 'seeded for review', $7::jsonb, now()
		)
		RETURNING id`, testWorkspaceID, agentID, localID, content, hashEvolutionContent(content), status, string(reviewMetadata)).Scan(&submissionID); err != nil {
		t.Fatalf("seed evolution submission: %v", err)
	}
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO evolution_unit_submission_file (workspace_id, submission_id, path, content, content_hash, mime_type, size_bytes)
		VALUES ($1, $2, 'README.md', 'safe details', $3, 'text/markdown', 12)
	`, testWorkspaceID, submissionID, hashEvolutionContent("safe details")); err != nil {
		t.Fatalf("seed evolution submission file: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM agent_skill_suggestion WHERE workspace_id=$1`, testWorkspaceID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM shared_evolution_unit WHERE workspace_id=$1 AND metadata->>'content_hash'=$2`, testWorkspaceID, hashEvolutionContent(content))
		_, _ = testPool.Exec(context.Background(), `DELETE FROM evolution_unit_submission WHERE id=$1`, submissionID)
	})
	return submissionID
}

func TestEvolutionReviewAPIListAndGet(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	submissionID := seedEvolutionReviewSubmission(t, "needs_review")

	listReq := newRequest(http.MethodGet, "/api/evolution/submissions?workspace_id="+testWorkspaceID+"&status=needs_review", nil)
	listRec := httptest.NewRecorder()
	testHandler.ListEvolutionReviewSubmissions(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", listRec.Code, listRec.Body.String())
	}
	var list []EvolutionReviewSubmissionResponse
	if err := json.Unmarshal(listRec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	found := false
	for _, item := range list {
		if item.ID == submissionID && item.Status == "needs_review" && item.ReviewMetadata != nil {
			found = true
		}
	}
	if !found {
		t.Fatalf("seeded submission %s not found in list: %#v", submissionID, list)
	}

	getReq := withURLParam(newRequest(http.MethodGet, "/api/evolution/submissions/"+submissionID+"?workspace_id="+testWorkspaceID, nil), "submissionId", submissionID)
	getRec := httptest.NewRecorder()
	testHandler.GetEvolutionReviewSubmission(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("get status=%d body=%s", getRec.Code, getRec.Body.String())
	}
	var detail EvolutionReviewSubmissionResponse
	if err := json.Unmarshal(getRec.Body.Bytes(), &detail); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	if detail.ID != submissionID || len(detail.Files) != 1 || detail.Files[0].Path != "README.md" {
		t.Fatalf("detail = %#v", detail)
	}
}

func TestEvolutionReviewAPIReject(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	submissionID := seedEvolutionReviewSubmission(t, "needs_review")

	req := withURLParam(newRequest(http.MethodPost, "/api/evolution/submissions/"+submissionID+"/reject?workspace_id="+testWorkspaceID, map[string]string{"reason": "not reusable"}), "submissionId", submissionID)
	rec := httptest.NewRecorder()
	testHandler.RejectEvolutionReviewSubmission(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("reject status=%d body=%s", rec.Code, rec.Body.String())
	}
	var status, decision, reason string
	var metadata map[string]any
	if err := testPool.QueryRow(context.Background(), `SELECT status, review_decision, review_reason, review_metadata FROM evolution_unit_submission WHERE id=$1`, submissionID).Scan(&status, &decision, &reason, &metadata); err != nil {
		t.Fatalf("load rejected submission: %v", err)
	}
	if status != "rejected" || decision != "reject" || reason != "not reusable" || metadata["human_decision"] != "reject" {
		t.Fatalf("status=%q decision=%q reason=%q metadata=%#v", status, decision, reason, metadata)
	}
}

func TestEvolutionReviewAPIPromote(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	submissionID := seedEvolutionReviewSubmission(t, "needs_review")

	req := withURLParam(newRequest(http.MethodPost, "/api/evolution/submissions/"+submissionID+"/promote?workspace_id="+testWorkspaceID, map[string]string{"reason": "approved by admin"}), "submissionId", submissionID)
	rec := httptest.NewRecorder()
	testHandler.PromoteEvolutionReviewSubmission(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("promote status=%d body=%s", rec.Code, rec.Body.String())
	}
	var status, decision string
	var promotedUnitID string
	var metadata map[string]any
	if err := testPool.QueryRow(context.Background(), `SELECT status, review_decision, promoted_unit_id::text, review_metadata FROM evolution_unit_submission WHERE id=$1`, submissionID).Scan(&status, &decision, &promotedUnitID, &metadata); err != nil {
		t.Fatalf("load promoted submission: %v", err)
	}
	if status != "promoted" || decision != "promote" || promotedUnitID == "" || metadata["human_decision"] != "promote" {
		t.Fatalf("status=%q decision=%q promoted=%q metadata=%#v", status, decision, promotedUnitID, metadata)
	}
}

func TestEvolutionReviewAPIPromoteCanApplyReviewSuggestions(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	submissionID := seedEvolutionReviewSubmissionWithMetadata(t, "needs_review", map[string]any{
		"title":                "Reviewer title",
		"summary":              "Reviewer summary",
		"suggested_tags":       []string{"go", "testing"},
		"suggested_task_types": []string{"verification"},
		"suggested_scope":      "workspace",
	})

	req := withURLParam(newRequest(http.MethodPost, "/api/evolution/submissions/"+submissionID+"/promote?workspace_id="+testWorkspaceID, map[string]any{"reason": "approved with suggestions", "apply_review_suggestions": true}), "submissionId", submissionID)
	rec := httptest.NewRecorder()
	testHandler.PromoteEvolutionReviewSubmission(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("promote status=%d body=%s", rec.Code, rec.Body.String())
	}
	var title, summary string
	var tags, taskTypes []string
	var metadata map[string]any
	if err := testPool.QueryRow(context.Background(), `
		SELECT u.title, u.canonical_summary, u.tags, u.task_types, s.review_metadata
		FROM evolution_unit_submission s
		JOIN shared_evolution_unit u ON u.id = s.promoted_unit_id
		WHERE s.id=$1`, submissionID).Scan(&title, &summary, &tags, &taskTypes, &metadata); err != nil {
		t.Fatalf("load promoted unit: %v", err)
	}
	if title != "Reviewer title" || summary != "Reviewer summary" || strings.Join(tags, ",") != "go,testing" || strings.Join(taskTypes, ",") != "verification" {
		t.Fatalf("unit title=%q summary=%q tags=%v taskTypes=%v", title, summary, tags, taskTypes)
	}
	nested, ok := metadata["metadata"].(map[string]any)
	if !ok || nested["applied_review_suggestions"] != true {
		t.Fatalf("metadata missing applied_review_suggestions: %#v", metadata)
	}
}

func TestEvolutionReviewRouteRequiresAdminRole(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	var memberUserID string
	memberSuffix := randomID()
	if err := testPool.QueryRow(ctx, `INSERT INTO "user" (name, email) VALUES ($1, $2) RETURNING id`, "Evolution Member "+memberSuffix, "evolution-member-"+memberSuffix+"@multica.test").Scan(&memberUserID); err != nil {
		t.Fatalf("create member user: %v", err)
	}
	if _, err := testPool.Exec(ctx, `INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, 'member')`, testWorkspaceID, memberUserID); err != nil {
		t.Fatalf("add member: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM member WHERE workspace_id=$1 AND user_id=$2`, testWorkspaceID, memberUserID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM "user" WHERE id=$1`, memberUserID)
	})

	r := chi.NewRouter()
	r.Route("/api/evolution/submissions", func(r chi.Router) {
		r.Use(middleware.RequireWorkspaceRole(testHandler.Queries, "owner", "admin"))
		r.Get("/", testHandler.ListEvolutionReviewSubmissions)
	})
	req := httptest.NewRequest(http.MethodGet, "/api/evolution/submissions?workspace_id="+testWorkspaceID, nil)
	req.Header.Set("X-User-ID", memberUserID)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "insufficient permissions") {
		t.Fatalf("member route status=%d body=%s", rec.Code, rec.Body.String())
	}
}
