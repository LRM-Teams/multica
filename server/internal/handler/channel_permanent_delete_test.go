package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

// LRM-236: DELETE /api/channels/{id} permanently removes the group (not archive).
func TestDeleteChannelPermanentlyRemovesGroup(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	var rtID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_runtime (workspace_id, owner_id, name, runtime_mode, provider, status, device_info, metadata, last_seen_at)
		VALUES ($1, $2, $3, 'cloud', 'perm-del', 'online', '', '{}'::jsonb, now())
		RETURNING id
	`, testWorkspaceID, testUserID, "perm-del-rt-"+uuid.NewString()).Scan(&rtID); err != nil {
		t.Fatalf("seed runtime: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE id = $1`, rtID) })

	channelID := seedChannelForTest(t, "perm-del-"+uuid.NewString(), testUserID)
	beckham, _, err := testHandler.EnsureGroupManagerForChannel(ctx, parseUUID(testWorkspaceID), parseUUID(channelID), parseUUID(testUserID))
	if err != nil {
		t.Fatalf("ensure beckham: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM agent WHERE id = $1`, beckham.ID) })

	req := withURLParam(withChannelTestWorkspaceCtx(t, newRequestAs(testUserID, http.MethodDelete, "/api/channels/"+channelID, nil), testUserID), "channelId", channelID)
	rec := httptest.NewRecorder()
	testHandler.DeleteChannel(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("DeleteChannel: status=%d body=%s, want 204", rec.Code, rec.Body.String())
	}

	var n int
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM channel WHERE id = $1`, channelID).Scan(&n); err != nil {
		t.Fatalf("count channel: %v", err)
	}
	if n != 0 {
		t.Fatalf("channel still present after permanent delete (%d rows)", n)
	}

	var archivedAt *string
	if err := testPool.QueryRow(ctx, `SELECT archived_at::text FROM agent WHERE id = $1`, beckham.ID).Scan(&archivedAt); err != nil {
		t.Fatalf("load beckham: %v", err)
	}
	if archivedAt == nil {
		t.Fatal("expected group manager to be archived after channel hard-delete")
	}
}

func TestDeleteChannelRejectsPlainMember(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	channelID := seedChannelForTest(t, "perm-del-forbid-"+uuid.NewString(), testUserID)
	memberID := createWorkspaceMemberUser(t, "perm-del-member", "perm-del-"+uuid.NewString()+"@example.com")
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO channel_member (workspace_id, channel_id, member_type, member_id)
		VALUES ($1, $2, 'user', $3)
		ON CONFLICT DO NOTHING`, testWorkspaceID, channelID, memberID); err != nil {
		t.Fatalf("add member: %v", err)
	}

	req := withURLParam(withChannelTestWorkspaceCtx(t, newRequestAs(memberID, http.MethodDelete, "/api/channels/"+channelID, nil), memberID), "channelId", channelID)
	rec := httptest.NewRecorder()
	testHandler.DeleteChannel(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("plain member DeleteChannel: status=%d body=%s, want 403", rec.Code, rec.Body.String())
	}
	var n int
	if err := testPool.QueryRow(context.Background(), `SELECT count(*) FROM channel WHERE id = $1`, channelID).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("channel deleted by unauthorized member")
	}
}

func TestDeleteChannelRejectsDM(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	// requireChannelManager only matches kind=group → DM returns 404.
	var channelID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO channel (workspace_id, name, kind, created_by)
		VALUES ($1, $2, 'dm', $3)
		RETURNING id`, testWorkspaceID, "dm-perm-"+uuid.NewString(), testUserID).Scan(&channelID); err != nil {
		t.Fatalf("seed dm: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM channel WHERE id = $1`, channelID) })

	req := withURLParam(withChannelTestWorkspaceCtx(t, newRequestAs(testUserID, http.MethodDelete, "/api/channels/"+channelID, nil), testUserID), "channelId", channelID)
	rec := httptest.NewRecorder()
	testHandler.DeleteChannel(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("DeleteChannel DM: status=%d body=%s, want 404", rec.Code, rec.Body.String())
	}
}
