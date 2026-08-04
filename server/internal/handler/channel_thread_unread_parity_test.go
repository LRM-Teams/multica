package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// LRM-1145 — the mainline thread preview「N 条回复 · M 条新」reads
// ChannelMessageResponse.thread_unread_count. Activity already treats a
// personal mention anywhere in the thread (root included) and own
// participation as reasons to count unread replies, so a member who is
// @-mentioned in a root message sees the thread as unread in Activity while
// the channel preview showed no「条新」at all. Both surfaces must answer the
// same question with the same rule, and an explicit unfollow must silence both.
func TestChannelThreadUnreadCountMatchesActivityUnreadRule(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	targetID := createChannelPlainMember(t)
	channelID := seedChannelForTest(t, "thread-unread-parity-"+uuid.NewString(), testUserID, targetID)

	mention := protocol.MessagePart{
		Type:       protocol.MessagePartTypeReference,
		RefType:    "mention",
		RefSubType: "member",
		RefID:      targetID,
		Label:      "@target",
	}

	unreadFor := func(rootID string) int {
		t.Helper()
		for _, msg := range listedMessagesForUser(t, channelID, targetID) {
			if msg.ID == rootID {
				return msg.ThreadUnreadCount
			}
		}
		t.Fatalf("thread root %s missing from target timeline", rootID)
		return -1
	}
	activityUnreadFor := func(rootID string) int {
		t.Helper()
		rows, err := testHandler.loadActivityThreads(ctx, parseUUID(testWorkspaceID), parseUUID(targetID))
		if err != nil {
			t.Fatalf("load activity threads: %v", err)
		}
		for _, row := range rows {
			if row.rootMessageID == rootID {
				return row.unreadCount
			}
		}
		t.Fatalf("thread root %s missing from activity feed", rootID)
		return -1
	}
	seedReply := func(rootID, authorID, authorName, content string) {
		t.Helper()
		if _, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(authorID), authorName, content, "multica", nil, pgtype.UUID{}, parseUUID(rootID), nil, 0); err != nil {
			t.Fatalf("insert thread reply: %v", err)
		}
	}

	// 1. Personal mention lives in the ROOT message. followChannelThreadMentionedUsers
	// only fires for mentions inside replies, so thread_participant stays empty
	// and the old rule hard-zeroed the preview badge.
	mentionRoot, err := testHandler.insertChannelMessageWithParts(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "@target 帮忙看下这个", []protocol.MessagePart{mention}, "multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 0)
	if err != nil {
		t.Fatalf("insert mention root: %v", err)
	}
	seedReply(mentionRoot.ID, testUserID, "Tester", "first follow-up")
	seedReply(mentionRoot.ID, testUserID, "Tester", "second follow-up")
	if got, want := unreadFor(mentionRoot.ID), 2; got != want {
		t.Fatalf("root-mention thread unread_count=%d, want %d", got, want)
	}
	if got, want := activityUnreadFor(mentionRoot.ID), unreadFor(mentionRoot.ID); got != want {
		t.Fatalf("activity unread=%d, channel preview unread=%d — surfaces disagree", got, want)
	}

	// Opening the thread clears it on both surfaces.
	req := newRequestAs(targetID, http.MethodPost, "/api/channels/"+channelID+"/messages/"+mentionRoot.ID+"/thread/read", nil)
	req = withChannelTestWorkspaceCtx(t, req, targetID)
	req = withURLParams(req, "channelId", channelID, "messageId", mentionRoot.ID)
	rec := httptest.NewRecorder()
	testHandler.MarkChannelThreadRead(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("mark thread read: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if got := unreadFor(mentionRoot.ID); got != 0 {
		t.Fatalf("after thread read unread_count=%d, want 0", got)
	}
	// A later reply becomes unread again.
	seedReply(mentionRoot.ID, testUserID, "Tester", "third follow-up")
	if got, want := unreadFor(mentionRoot.ID), 1; got != want {
		t.Fatalf("post-read reply unread_count=%d, want %d", got, want)
	}

	// An explicit unfollow must silence the badge even though the mention stands.
	req = newRequestAs(targetID, http.MethodDelete, "/api/channels/"+channelID+"/messages/"+mentionRoot.ID+"/thread/follow", nil)
	req = withChannelTestWorkspaceCtx(t, req, targetID)
	req = withURLParams(req, "channelId", channelID, "messageId", mentionRoot.ID)
	rec = httptest.NewRecorder()
	testHandler.UnfollowChannelThread(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unfollow thread: status=%d body=%s", rec.Code, rec.Body.String())
	}
	seedReply(mentionRoot.ID, testUserID, "Tester", "reply after unfollow")
	if got := unreadFor(mentionRoot.ID); got != 0 {
		t.Fatalf("explicitly unfollowed thread unread_count=%d, want 0", got)
	}
	if got := activityUnreadFor(mentionRoot.ID); got != 0 {
		t.Fatalf("explicitly unfollowed thread activity unread=%d, want 0", got)
	}

	// 2. The member already spoke in the thread through a path that never wrote
	// thread_participant (bridge/legacy imports). Own replies never count as
	// unread, later replies from others do.
	participatedRoot, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "bridge thread "+uuid.NewString(), "multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 0)
	if err != nil {
		t.Fatalf("insert participated root: %v", err)
	}
	seedReply(participatedRoot.ID, targetID, "Target", "我先回一条")
	if got := unreadFor(participatedRoot.ID); got != 0 {
		t.Fatalf("own-reply-only thread unread_count=%d, want 0", got)
	}
	seedReply(participatedRoot.ID, testUserID, "Tester", "回复你")
	if got, want := unreadFor(participatedRoot.ID), 1; got != want {
		t.Fatalf("participated thread unread_count=%d, want %d", got, want)
	}
	if got, want := activityUnreadFor(participatedRoot.ID), 1; got != want {
		t.Fatalf("participated thread activity unread=%d, want %d", got, want)
	}

	// 3. A pure bystander (no mention, no participation, no follow) still sees
	// only「N 条回复」— broadening the rule must not badge every thread.
	bystanderRoot, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "bystander thread "+uuid.NewString(), "multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 0)
	if err != nil {
		t.Fatalf("insert bystander root: %v", err)
	}
	seedReply(bystanderRoot.ID, testUserID, "Tester", "talking to myself")
	if got := unreadFor(bystanderRoot.ID); got != 0 {
		t.Fatalf("bystander thread unread_count=%d, want 0", got)
	}
}
