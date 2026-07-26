package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestReplyFeedbackUpsertIsIdempotent(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ensureReplyFeedbackTable(t)

	ctx := context.Background()
	channelID := seedChannelForTest(t, "reply-fb-"+uuid.NewString(), testUserID)

	var agentID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent (
			workspace_id, name, description, runtime_mode, runtime_config,
			runtime_id, visibility, max_concurrent_tasks, owner_id
		)
		VALUES ($1, $2, '', 'cloud', '{}'::jsonb, $3, 'workspace', 1, $4)
		RETURNING id
	`, testWorkspaceID, "reply-fb-"+uuid.NewString(), testRuntimeID, testUserID).Scan(&agentID); err != nil {
		t.Fatalf("seed agent: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM reply_feedback WHERE agent_id = $1`, agentID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM agent WHERE id = $1`, agentID)
	})

	msg, err := testHandler.insertChannelMessage(
		ctx, parseUUID(channelID), parseUUID(testWorkspaceID),
		"agent", parseUUID(agentID), "Reply FB Bot", "final answer", "multica",
		nil, pgtype.UUID{}, pgtype.UUID{}, strPtr("reply-fb"), 0,
	)
	if err != nil {
		t.Fatalf("insert agent message: %v", err)
	}

	put := func(value int) ReplyFeedbackResponse {
		t.Helper()
		req := newRequest(http.MethodPut, "/api/channels/"+channelID+"/messages/"+msg.ID+"/reply-feedback", map[string]any{
			"value": value,
		})
		req = withChannelTestWorkspaceCtx(t, req, testUserID)
		req = withRouteParams(req, "channelId", channelID, "messageId", msg.ID)
		rec := httptest.NewRecorder()
		testHandler.UpsertChannelReplyFeedback(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("upsert status=%d body=%s", rec.Code, rec.Body.String())
		}
		var fb ReplyFeedbackResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &fb); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return fb
	}

	first := put(1)
	if first.Value != 1 {
		t.Fatalf("first value=%d", first.Value)
	}
	second := put(-1)
	if second.Value != -1 {
		t.Fatalf("second value=%d", second.Value)
	}
	if second.MessageID != msg.ID {
		t.Fatalf("message_id=%s", second.MessageID)
	}

	var count int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM reply_feedback
		WHERE message_kind = 'channel' AND message_id = $1 AND actor_user_id = $2`,
		msg.ID, parseUUID(testUserID)).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Fatalf("count=%d want 1", count)
	}

	listReq := newRequest(http.MethodGet, "/api/channels/"+channelID+"/messages/"+msg.ID+"/reply-feedback", nil)
	listReq = withChannelTestWorkspaceCtx(t, listReq, testUserID)
	listReq = withRouteParams(listReq, "channelId", channelID, "messageId", msg.ID)
	listRec := httptest.NewRecorder()
	testHandler.ListChannelReplyFeedback(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", listRec.Code, listRec.Body.String())
	}

	delReq := newRequest(http.MethodDelete, "/api/channels/"+channelID+"/messages/"+msg.ID+"/reply-feedback", nil)
	delReq = withChannelTestWorkspaceCtx(t, delReq, testUserID)
	delReq = withRouteParams(delReq, "channelId", channelID, "messageId", msg.ID)
	delRec := httptest.NewRecorder()
	testHandler.DeleteChannelReplyFeedback(delRec, delReq)
	if delRec.Code != http.StatusNoContent {
		t.Fatalf("delete status=%d body=%s", delRec.Code, delRec.Body.String())
	}
}

func ensureReplyFeedbackTable(t *testing.T) {
	t.Helper()
	_, err := testPool.Exec(context.Background(), `
		CREATE TABLE IF NOT EXISTS reply_feedback (
		  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
		  message_kind TEXT NOT NULL CHECK (message_kind IN ('channel', 'chat')),
		  message_id UUID NOT NULL,
		  task_id UUID,
		  agent_id UUID,
		  actor_user_id UUID NOT NULL REFERENCES "user"(id) ON DELETE CASCADE,
		  value SMALLINT NOT NULL CHECK (value IN (1, -1)),
		  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
		  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
		  UNIQUE (message_kind, message_id, actor_user_id)
		)`)
	if err != nil {
		t.Fatalf("ensure reply_feedback table: %v", err)
	}
}
