package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

// TestCreateIssueAutoBindsSourceMessageAttachments covers LRM-731: chat→issue
// create with source must put reference images on the MAIN issue even when
// attachment_ids is omitted from the request body.
func TestCreateIssueAutoBindsSourceMessageAttachments(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	channelID := seedChannelForTest(t, "lrm-731-source-"+uuid.NewString(), testUserID)
	var messageID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO channel_message (channel_id, workspace_id, author_type, author_id, author_name, content, source, trigger_depth)
		VALUES ($1, $2, 'user', $3, 'Reporter', 'bug with screenshot', 'multica', 0)
		RETURNING id
	`, channelID, testWorkspaceID, testUserID).Scan(&messageID); err != nil {
		t.Fatalf("seed source message: %v", err)
	}
	var attachmentID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO attachment (workspace_id, channel_id, uploader_type, uploader_id, filename, url, content_type, size_bytes)
		VALUES ($1, $2, 'member', $3, 'bug.png', 'https://example.invalid/bug.png', 'image/png', 99)
		RETURNING id
	`, testWorkspaceID, channelID, testUserID).Scan(&attachmentID); err != nil {
		t.Fatalf("seed channel attachment: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM attachment WHERE id = $1`, attachmentID)
	})
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_message_attachment (workspace_id, channel_message_id, attachment_id)
		VALUES ($1, $2, $3)
	`, testWorkspaceID, messageID, attachmentID); err != nil {
		t.Fatalf("seed message attachment ref: %v", err)
	}

	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title": "LRM-731 source auto-bind",
		"source": map[string]any{
			"channel_id": channelID,
			"message_id": messageID,
		},
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

	if len(issue.Attachments) != 1 || issue.Attachments[0].ID != attachmentID {
		t.Fatalf("issue attachments = %#v, want [%s] auto-bound from source", issue.Attachments, attachmentID)
	}
}

// TestCreateIssueClonesAlreadyBoundAttachment covers LRM-731 recovery: when a
// carrier sub-issue already owns the channel screenshot, creating/updating the
// main issue with that attachment id must still surface the image on the main
// issue (clone; original stays on the carrier).
func TestCreateIssueClonesAlreadyBoundAttachment(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	carrierID := createIssueForCommentAttachmentTest(t, "LRM-731 carrier child")
	attID := insertIssueScopedAttachment(t, carrierID, "carrier-shot.png")

	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":          "LRM-731 main with clone",
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

	if len(issue.Attachments) != 1 {
		t.Fatalf("issue attachments = %#v, want 1 cloned attachment", issue.Attachments)
	}
	if issue.Attachments[0].ID == attID {
		t.Fatalf("expected a cloned attachment id, got the original carrier id %q", attID)
	}
	if issue.Attachments[0].URL == "" {
		t.Fatal("cloned attachment missing url")
	}

	var carrierStillOwns string
	if err := testPool.QueryRow(context.Background(),
		`SELECT issue_id::text FROM attachment WHERE id = $1::uuid`, attID,
	).Scan(&carrierStillOwns); err != nil {
		t.Fatalf("reload carrier attachment: %v", err)
	}
	if carrierStillOwns != carrierID {
		t.Fatalf("carrier attachment.issue_id = %q, want %s (clone must not steal)", carrierStillOwns, carrierID)
	}
}
