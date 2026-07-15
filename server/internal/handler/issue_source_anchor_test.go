package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

func TestCreateIssueSourceMessageAnchorPersistsRootAndServesDetailRef(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	channelID := seedChannelForTest(t, "issue-source-anchor-"+uuid.NewString(), testUserID)
	var rootID, replyID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO channel_message (channel_id, workspace_id, author_type, author_id, author_name, content, source, thread_id, trigger_depth)
		VALUES ($1, $2, 'user', $3, 'Source User', 'Root discussion that should become the anchor', 'multica', $4, 0)
		RETURNING id
	`, channelID, testWorkspaceID, testUserID, "issue-source-root-"+uuid.NewString()).Scan(&rootID); err != nil {
		t.Fatalf("seed source root: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO channel_message (channel_id, workspace_id, author_type, author_id, author_name, content, source, thread_root_message_id, thread_id, trigger_depth)
		VALUES ($1, $2, 'user', $3, 'Source User', 'Reply used to ask the agent to create an issue', 'multica', $4, $5, 1)
		RETURNING id
	`, channelID, testWorkspaceID, testUserID, rootID, "issue-source-root-"+uuid.NewString()).Scan(&replyID); err != nil {
		t.Fatalf("seed source reply: %v", err)
	}

	create := httptest.NewRecorder()
	testHandler.CreateIssue(create, newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":  "Anchor an issue to the parent discussion",
		"status": "backlog",
		"source": map[string]string{
			"channel_id": channelID,
			"message_id": replyID,
		},
	}))
	if create.Code != http.StatusCreated {
		t.Fatalf("CreateIssue = %d: %s", create.Code, create.Body.String())
	}
	var created IssueResponse
	if err := json.NewDecoder(create.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM issue WHERE id = $1`, created.ID) })

	var storedChannelID, storedMessageID string
	if err := testPool.QueryRow(ctx, `
		SELECT channel_id::text, message_id::text FROM issue_source_message WHERE issue_id = $1`, created.ID,
	).Scan(&storedChannelID, &storedMessageID); err != nil {
		t.Fatalf("load persisted source anchor: %v", err)
	}
	if storedChannelID != channelID || storedMessageID != rootID {
		t.Fatalf("stored anchor = %s/%s, want source channel/root %s/%s", storedChannelID, storedMessageID, channelID, rootID)
	}

	detail := httptest.NewRecorder()
	detailReq := newRequest("GET", "/api/issues/"+created.ID+"?workspace_id="+testWorkspaceID, nil)
	detailReq = withURLParam(detailReq, "id", created.ID)
	testHandler.GetIssue(detail, detailReq)
	if detail.Code != http.StatusOK {
		t.Fatalf("GetIssue = %d: %s", detail.Code, detail.Body.String())
	}
	var got IssueResponse
	if err := json.NewDecoder(detail.Body).Decode(&got); err != nil {
		t.Fatalf("decode detail response: %v", err)
	}
	if got.SourceRefs == nil || got.SourceRefs.Message == nil {
		t.Fatalf("source_refs.message missing from issue detail: %s", detail.Body.String())
	}
	ref := got.SourceRefs.Message
	if ref.ChannelID != channelID || ref.MessageID != rootID || ref.ThreadRootMessageID != rootID {
		t.Fatalf("detail source ref = %#v, want channel/root %s/%s", ref, channelID, rootID)
	}
	if ref.Excerpt != "Root discussion that should become the anchor" {
		t.Fatalf("source excerpt = %q", ref.Excerpt)
	}
}
