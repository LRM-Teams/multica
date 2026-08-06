package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

func TestMuteAndUnmuteChannelAgentPersistState(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "agent-mute-"+uuid.NewString(), nil)
	channelID := seedChannelForTest(t, "agent-mute-"+uuid.NewString(), testUserID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3)
ON CONFLICT DO NOTHING`, channelID, testWorkspaceID, agentID); err != nil {
		t.Fatalf("add agent channel member: %v", err)
	}
	taskID := createHandlerTestTaskForAgent(t, agentID)

	call := func(muted bool) *httptest.ResponseRecorder {
		method := http.MethodPut
		if !muted {
			method = http.MethodDelete
		}
		req := newRequest(method, "/api/channels/"+channelID+"/agent-mute", nil)
		req.Header.Set("X-Actor-Source", "task_token")
		req.Header.Set("X-Agent-ID", agentID)
		req.Header.Set("X-Task-ID", taskID)
		req = withChannelTestWorkspaceCtx(t, req, testUserID)
		req = withURLParam(req, "channelId", channelID)
		rec := httptest.NewRecorder()
		if muted {
			testHandler.MuteChannelAgent(rec, req)
		} else {
			testHandler.UnmuteChannelAgent(rec, req)
		}
		return rec
	}

	if rec := call(true); rec.Code != http.StatusOK {
		t.Fatalf("mute channel agent: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var isMuted bool
	if err := testPool.QueryRow(ctx, `
		SELECT muted_at IS NOT NULL
		FROM channel_member
		WHERE channel_id = $1 AND workspace_id = $2 AND member_type = 'agent' AND member_id = $3`,
		channelID, testWorkspaceID, agentID,
	).Scan(&isMuted); err != nil {
		t.Fatalf("load agent mute state: %v", err)
	}
	if !isMuted {
		t.Fatal("agent channel_member muted_at is not set")
	}

	if rec := call(false); rec.Code != http.StatusOK {
		t.Fatalf("unmute channel agent: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if err := testPool.QueryRow(ctx, `
		SELECT muted_at IS NOT NULL
		FROM channel_member
		WHERE channel_id = $1 AND workspace_id = $2 AND member_type = 'agent' AND member_id = $3`,
		channelID, testWorkspaceID, agentID,
	).Scan(&isMuted); err != nil {
		t.Fatalf("load agent unmute state: %v", err)
	}
	if isMuted {
		t.Fatal("agent channel_member muted_at remains set after unmute")
	}
}
