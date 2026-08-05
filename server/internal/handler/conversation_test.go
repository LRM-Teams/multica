package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"
)

func listConversationsForUser(t *testing.T, userID, path string) (*httptest.ResponseRecorder, ConversationListResponse) {
	t.Helper()
	req := withChannelTestWorkspaceCtx(t, newRequestAs(userID, http.MethodGet, path, nil), userID)
	rec := httptest.NewRecorder()
	testHandler.ListConversations(rec, req)
	var response ConversationListResponse
	if rec.Body.Len() > 0 {
		_ = json.Unmarshal(rec.Body.Bytes(), &response)
	}
	return rec, response
}

func seedHumanDMChannelForConversationTest(t *testing.T, firstUserID, secondUserID string) string {
	t.Helper()
	ctx := context.Background()
	var channelID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO channel (workspace_id, name, created_by, kind)
		VALUES ($1, $2, $3, 'dm')
		RETURNING id`, testWorkspaceID, dmCanonicalName("user", firstUserID, "user", secondUserID), firstUserID).Scan(&channelID); err != nil {
		t.Fatalf("seed human DM channel: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'user', $3), ($1, $2, 'user', $4)
		ON CONFLICT DO NOTHING`, channelID, testWorkspaceID, firstUserID, secondUserID); err != nil {
		t.Fatalf("seed human DM members: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM channel WHERE id = $1`, channelID) })
	return channelID
}

func TestListConversationsAggregatesChannelsAndDMsWithGlobalCursor(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	viewerID := seedWorkspaceUserForTransportTargetTest(t, "conversation-viewer-"+uuid.NewString()[:8])
	peerID := seedWorkspaceUserForTransportTargetTest(t, "conversation-peer-"+uuid.NewString()[:8])
	groupID := seedChannelForTest(t, "conversation-group-"+uuid.NewString(), viewerID)
	dmID := seedHumanDMChannelForConversationTest(t, viewerID, peerID)

	groupUpdatedAt := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Microsecond)
	// Keep the target DM newer than any auto-rostered system channel. The group
	// still wins globally because it is pinned.
	dmUpdatedAt := time.Now().UTC().Add(24 * time.Hour).Truncate(time.Microsecond)
	pinnedAt := dmUpdatedAt.Add(time.Hour)
	if _, err := testPool.Exec(ctx, `
		UPDATE channel SET updated_at = CASE id WHEN $1 THEN $3::timestamptz ELSE $4::timestamptz END WHERE id IN ($1, $2)`,
		groupID, dmID, groupUpdatedAt, dmUpdatedAt); err != nil {
		t.Fatalf("set deterministic activity timestamps: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		UPDATE channel_member
		SET pinned_at = $3, manual_unread_at = $3, muted_at = $3, notify_level = 'mentions'
		WHERE channel_id = $1 AND member_type = 'user' AND member_id = $2`, groupID, viewerID, pinnedAt); err != nil {
		t.Fatalf("set group viewer state: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO dm_peer_state (workspace_id, user_id, peer_type, peer_id, manual_unread_at)
		VALUES ($1, $2, 'user', $3, $4)
		ON CONFLICT (workspace_id, user_id, peer_type, peer_id)
		DO UPDATE SET manual_unread_at = EXCLUDED.manual_unread_at`, testWorkspaceID, viewerID, peerID, pinnedAt); err != nil {
		t.Fatalf("set DM viewer state: %v", err)
	}

	firstRec, firstPage := listConversationsForUser(t, viewerID, "/api/conversations?limit=1")
	if firstRec.Code != http.StatusOK {
		t.Fatalf("first page status=%d body=%s", firstRec.Code, firstRec.Body.String())
	}
	if len(firstPage.Items) != 1 || firstPage.NextCursor == nil {
		t.Fatalf("first page=%+v, want one item and next cursor", firstPage)
	}
	first := firstPage.Items[0]
	if first.Kind != "channel" || first.Channel == nil || first.Channel.ID != groupID || first.DM != nil {
		t.Fatalf("globally pinned group did not sort first: %+v", first)
	}
	if first.Channel.NotifyLevel != channelNotifyLevelMentions || !first.Channel.ManuallyUnread || !first.Channel.Muted || first.Channel.UnreadCount != 1 {
		t.Fatalf("group read projection lost state: %+v", first.Channel)
	}
	if len(first.Channel.Members) != 1 || first.Channel.Members[0].MemberID != viewerID {
		t.Fatalf("group member brief missing: %+v", first.Channel.Members)
	}

	secondPath := "/api/conversations?limit=1&cursor=" + url.QueryEscape(*firstPage.NextCursor)
	secondRec, secondPage := listConversationsForUser(t, viewerID, secondPath)
	if secondRec.Code != http.StatusOK {
		t.Fatalf("second page status=%d body=%s", secondRec.Code, secondRec.Body.String())
	}
	if len(secondPage.Items) != 1 {
		t.Fatalf("second page=%+v, want one item", secondPage)
	}
	second := secondPage.Items[0]
	if second.Kind != "dm" || second.DM == nil || second.DM.ID != dmID || second.Channel != nil {
		t.Fatalf("DM did not follow cursor: %+v", second)
	}
	if second.DM.Peer.Type != "user" || second.DM.Peer.ID != peerID || !second.DM.ManuallyUnread || second.DM.Unread != 1 || second.DM.RealUnread != 0 {
		t.Fatalf("DM read projection lost peer/unread state: %+v", second.DM)
	}
}

func TestListConversationsRejectsInvalidPagination(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	for _, path := range []string{
		"/api/conversations?limit=0",
		"/api/conversations?limit=101",
		"/api/conversations?limit=not-a-number",
		"/api/conversations?cursor=not-a-cursor",
	} {
		rec, _ := listConversationsForUser(t, testUserID, path)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s status=%d body=%s, want 400", path, rec.Code, rec.Body.String())
		}
	}
}

func TestConversationListComparatorUsesStableIDTieBreak(t *testing.T) {
	at := time.Date(2026, 8, 4, 8, 0, 0, 0, time.UTC)
	newerPin := at.Add(time.Minute)
	if got := compareConversationListKeys(
		conversationListSortKey{pinnedAt: &newerPin, updatedAt: at, id: "a"},
		conversationListSortKey{pinnedAt: &at, updatedAt: at.Add(time.Hour), id: "z"},
	); got >= 0 {
		t.Fatalf("newer pin must win regardless of activity, compare=%d", got)
	}
	if got := compareConversationListKeys(
		conversationListSortKey{updatedAt: at, id: "b"},
		conversationListSortKey{updatedAt: at, id: "a"},
	); got >= 0 {
		t.Fatalf("descending id tie-break must be stable, compare=%d", got)
	}
}
