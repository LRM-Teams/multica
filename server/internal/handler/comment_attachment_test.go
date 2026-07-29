package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

// insertUnboundAttachment mimics `multica attachment upload` without issue_id —
// the path that LRM-733 found silently dropped on `issue comment add --attachment-id`.
func insertUnboundAttachment(t *testing.T, filename string) string {
	t.Helper()
	var id string
	err := testPool.QueryRow(context.Background(), `
		INSERT INTO attachment (workspace_id, uploader_type, uploader_id, filename, url, content_type, size_bytes)
		VALUES ($1::uuid, 'member', $2::uuid, $3, 'https://example.com/' || $3, 'text/plain', 12)
		RETURNING id::text
	`, testWorkspaceID, testUserID, filename).Scan(&id)
	if err != nil {
		t.Fatalf("insert unbound attachment: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM attachment WHERE id = $1::uuid`, id)
	})
	return id
}

func insertIssueScopedAttachment(t *testing.T, issueID, filename string) string {
	t.Helper()
	var id string
	err := testPool.QueryRow(context.Background(), `
		INSERT INTO attachment (workspace_id, issue_id, uploader_type, uploader_id, filename, url, content_type, size_bytes)
		VALUES ($1::uuid, $2::uuid, 'member', $3::uuid, $4, 'https://example.com/' || $4, 'text/plain', 12)
		RETURNING id::text
	`, testWorkspaceID, issueID, testUserID, filename).Scan(&id)
	if err != nil {
		t.Fatalf("insert issue-scoped attachment: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM attachment WHERE id = $1::uuid`, id)
	})
	return id
}

func createIssueForCommentAttachmentTest(t *testing.T, title string) string {
	t.Helper()
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title": title,
	})
	testHandler.CreateIssue(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateIssue: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var issue IssueResponse
	if err := json.NewDecoder(w.Body).Decode(&issue); err != nil {
		t.Fatalf("decode issue: %v", err)
	}
	t.Cleanup(func() {
		cleanup := withURLParam(newRequest("DELETE", "/api/issues/"+issue.ID, nil), "id", issue.ID)
		testHandler.DeleteIssue(httptest.NewRecorder(), cleanup)
	})
	return issue.ID
}

func TestCreateCommentBindsUnboundAttachmentIDs(t *testing.T) {
	issueID := createIssueForCommentAttachmentTest(t, "LRM-733 unbound comment attachment")
	attID := insertUnboundAttachment(t, "unbound-lrm733.txt")

	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues/"+issueID+"/comments", map[string]any{
		"content":        "comment with unbound upload",
		"attachment_ids": []string{attID},
	})
	req = withURLParam(req, "id", issueID)
	testHandler.CreateComment(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateComment: expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var comment CommentResponse
	if err := json.NewDecoder(w.Body).Decode(&comment); err != nil {
		t.Fatalf("decode comment: %v", err)
	}
	if len(comment.Attachments) != 1 {
		t.Fatalf("attachments = %#v, want 1 unbound id bound to comment", comment.Attachments)
	}
	if comment.Attachments[0].ID != attID {
		t.Fatalf("attachment id = %q, want %q", comment.Attachments[0].ID, attID)
	}

	var issueIDOnRow, commentIDOnRow string
	err := testPool.QueryRow(context.Background(), `
		SELECT issue_id::text, comment_id::text FROM attachment WHERE id = $1::uuid
	`, attID).Scan(&issueIDOnRow, &commentIDOnRow)
	if err != nil {
		t.Fatalf("reload attachment: %v", err)
	}
	if issueIDOnRow != issueID {
		t.Fatalf("attachment.issue_id = %q, want %s", issueIDOnRow, issueID)
	}
	if commentIDOnRow != comment.ID {
		t.Fatalf("attachment.comment_id = %q, want %s", commentIDOnRow, comment.ID)
	}
}

func TestCreateCommentStillBindsIssueScopedAttachment(t *testing.T) {
	issueID := createIssueForCommentAttachmentTest(t, "LRM-733 issue-scoped comment attachment")
	attID := insertIssueScopedAttachment(t, issueID, "scoped-lrm733.txt")

	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues/"+issueID+"/comments", map[string]any{
		"content":        "comment with issue-scoped upload",
		"attachment_ids": []string{attID},
	})
	req = withURLParam(req, "id", issueID)
	testHandler.CreateComment(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateComment: expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var comment CommentResponse
	if err := json.NewDecoder(w.Body).Decode(&comment); err != nil {
		t.Fatalf("decode comment: %v", err)
	}
	if len(comment.Attachments) != 1 || comment.Attachments[0].ID != attID {
		t.Fatalf("attachments = %#v, want [%s]", comment.Attachments, attID)
	}
}

func TestCreateCommentRejectsUnknownAttachmentWithoutLeavingComment(t *testing.T) {
	issueID := createIssueForCommentAttachmentTest(t, "LRM-733 unknown attachment")

	var before int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM comment WHERE issue_id = $1::uuid`, issueID).Scan(&before); err != nil {
		t.Fatalf("count comments before: %v", err)
	}

	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues/"+issueID+"/comments", map[string]any{
		"content":        "should not persist",
		"attachment_ids": []string{uuid.NewString()},
	})
	req = withURLParam(req, "id", issueID)
	testHandler.CreateComment(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("CreateComment: expected 400, got %d: %s", w.Code, w.Body.String())
	}

	var after int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM comment WHERE issue_id = $1::uuid`, issueID).Scan(&after); err != nil {
		t.Fatalf("count comments after: %v", err)
	}
	if after != before {
		t.Fatalf("unknown attachment left a comment behind: before=%d after=%d", before, after)
	}
}

func TestCreateIssueStillBindsUnboundAttachmentIDs(t *testing.T) {
	attID := insertUnboundAttachment(t, "issue-create-lrm733.txt")

	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":          "LRM-733 issue create attachment regression",
		"attachment_ids": []string{attID},
	})
	testHandler.CreateIssue(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateIssue: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var issue IssueResponse
	if err := json.NewDecoder(w.Body).Decode(&issue); err != nil {
		t.Fatalf("decode issue: %v", err)
	}
	t.Cleanup(func() {
		cleanup := withURLParam(newRequest("DELETE", "/api/issues/"+issue.ID, nil), "id", issue.ID)
		testHandler.DeleteIssue(httptest.NewRecorder(), cleanup)
	})

	if len(issue.Attachments) != 1 || issue.Attachments[0].ID != attID {
		t.Fatalf("issue attachments = %#v, want [%s]", issue.Attachments, attID)
	}
}
