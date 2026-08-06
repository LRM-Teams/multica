package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func fetchUserActivity(t *testing.T, userID, tab string) (activityListResponse, int) {
	t.Helper()
	path := "/api/activity"
	if tab != "" {
		path += "?tab=" + tab
	}
	req := newRequestAs(userID, http.MethodGet, path, nil)
	req = withChannelTestWorkspaceCtx(t, req, userID)
	rec := httptest.NewRecorder()
	testHandler.ListUserActivity(rec, req)
	var resp activityListResponse
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode activity response: %v", err)
		}
	}
	return resp, rec.Code
}

func activityThreadIDs(resp activityListResponse) map[string]bool {
	out := map[string]bool{}
	for _, item := range resp.Items {
		if item.Kind == "thread" {
			out[item.ID] = true
		}
	}
	return out
}

func TestUserActivity_FilterMatrix(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	targetHandle := "activity-" + uuid.NewString()[:8]
	targetID := createWorkspaceMemberUser(t, targetHandle, targetHandle+"@multica.test")
	channelID := seedChannelForTest(t, "activity-filter-"+uuid.NewString(), testUserID, targetID)
	mention := protocol.MessagePart{
		Type:       protocol.MessagePartTypeReference,
		RefType:    "mention",
		RefSubType: "member",
		RefID:      targetID,
		Label:      "@" + targetHandle,
	}

	postRoot := func(content string) ChannelMessageResponse {
		t.Helper()
		rec := sendChannelMessageForTest(t, channelID, testUserID, map[string]any{"content": content})
		if rec.Code != http.StatusCreated {
			t.Fatalf("post root: status=%d body=%s", rec.Code, rec.Body.String())
		}
		var root ChannelMessageResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &root); err != nil {
			t.Fatalf("decode root: %v", err)
		}
		return root
	}
	postReplyIn := func(chanID string, root ChannelMessageResponse, content string, parts ...protocol.MessagePart) ChannelMessageResponse {
		t.Helper()
		body := map[string]any{"content": content}
		if len(parts) > 0 {
			body["parts"] = parts
		}
		rec := sendChannelThreadReplyForTest(t, chanID, root.ID, testUserID, body)
		if rec.Code != http.StatusCreated {
			t.Fatalf("post reply: status=%d body=%s", rec.Code, rec.Body.String())
		}
		var reply ChannelMessageResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &reply); err != nil {
			t.Fatalf("decode reply: %v", err)
		}
		return reply
	}
	postReply := func(root ChannelMessageResponse, content string, parts ...protocol.MessagePart) ChannelMessageResponse {
		return postReplyIn(channelID, root, content, parts...)
	}
	unfollow := func(root ChannelMessageResponse) {
		t.Helper()
		req := newRequestAs(targetID, http.MethodDelete, "/api/channels/"+channelID+"/messages/"+root.ID+"/thread/follow", nil)
		req = withChannelTestWorkspaceCtx(t, req, targetID)
		req = withURLParams(req, "channelId", channelID, "messageId", root.ID)
		rec := httptest.NewRecorder()
		testHandler.UnfollowChannelThread(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("unfollow: status=%d body=%s", rec.Code, rec.Body.String())
		}
	}

	followedRoot := postRoot("followed thread " + uuid.NewString())
	postReply(followedRoot, "wake followed thread")
	reqFollow := newRequestAs(targetID, http.MethodPut, "/api/channels/"+channelID+"/messages/"+followedRoot.ID+"/thread/follow", nil)
	reqFollow = withChannelTestWorkspaceCtx(t, reqFollow, targetID)
	reqFollow = withURLParams(reqFollow, "channelId", channelID, "messageId", followedRoot.ID)
	recFollow := httptest.NewRecorder()
	testHandler.FollowChannelThread(recFollow, reqFollow)
	if recFollow.Code != http.StatusOK {
		t.Fatalf("follow thread: status=%d body=%s", recFollow.Code, recFollow.Body.String())
	}
	postReply(followedRoot, "second reply after follow")

	mentionRoot := postRoot("mention thread " + uuid.NewString())
	postReply(mentionRoot, "@"+targetHandle+" please look", mention)

	participatedRoot := postRoot("participated thread " + uuid.NewString())
	req := newRequestAs(targetID, http.MethodPost, "/api/channels/"+channelID+"/messages/"+participatedRoot.ID+"/thread", map[string]any{
		"content": "I participated",
	})
	req = withChannelTestWorkspaceCtx(t, req, targetID)
	req = withURLParams(req, "channelId", channelID, "messageId", participatedRoot.ID)
	rec := httptest.NewRecorder()
	testHandler.SendChannelMessageThreadReply(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("target participated reply: status=%d body=%s", rec.Code, rec.Body.String())
	}

	unfollowedRoot := postRoot("unfollowed thread " + uuid.NewString())
	postReply(unfollowedRoot, "@"+targetHandle+" initial mention", mention)
	unfollow(unfollowedRoot)
	postReply(unfollowedRoot, "@"+targetHandle+" mention after unfollow", mention)

	all, status := fetchUserActivity(t, targetID, "all")
	if status != http.StatusOK {
		t.Fatalf("all tab status=%d", status)
	}
	allThreads := activityThreadIDs(all)
	for _, id := range []string{followedRoot.ID, mentionRoot.ID, participatedRoot.ID} {
		if !allThreads[id] {
			t.Fatalf("all tab missing related thread %s", id)
		}
	}
	if allThreads[unfollowedRoot.ID] {
		t.Fatalf("all tab should not include explicitly unfollowed thread %s", unfollowedRoot.ID)
	}

	mentions, status := fetchUserActivity(t, targetID, "mentions")
	if status != http.StatusOK {
		t.Fatalf("mentions tab status=%d", status)
	}
	mentionThreads := activityThreadIDs(mentions)
	for _, id := range []string{mentionRoot.ID, unfollowedRoot.ID} {
		if !mentionThreads[id] {
			t.Fatalf("mentions tab missing @ thread %s", id)
		}
	}

	postReply(followedRoot, "another unread reply for filter test")
	unread, status := fetchUserActivity(t, targetID, "unread")
	if status != http.StatusOK {
		t.Fatalf("unread tab status=%d", status)
	}
	unreadThreads := activityThreadIDs(unread)
	if !unreadThreads[followedRoot.ID] {
		t.Fatalf("unread tab missing followed thread with new reply")
	}
	if unreadThreads[unfollowedRoot.ID] {
		t.Fatalf("unread tab should not include unfollowed thread even after @")
	}

	leftChannelID := seedChannelForTest(t, "activity-left-"+uuid.NewString(), testUserID, targetID)
	leftRoot, err := testHandler.insertChannelMessage(ctx, parseUUID(leftChannelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "left channel root", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 0)
	if err != nil {
		t.Fatalf("insert left-channel root: %v", err)
	}
	postReplyIn(leftChannelID, leftRoot, "@"+targetHandle+" in channel I will leave", mention)
	if _, err := testPool.Exec(ctx, `DELETE FROM channel_member WHERE channel_id = $1 AND member_id = $2`, leftChannelID, targetID); err != nil {
		t.Fatalf("remove target from channel: %v", err)
	}

	allAfterLeave, status := fetchUserActivity(t, targetID, "all")
	if status != http.StatusOK {
		t.Fatalf("all after leave status=%d", status)
	}
	var leftItem *ActivityItemResponse
	for i := range allAfterLeave.Items {
		if allAfterLeave.Items[i].Kind == "thread" && allAfterLeave.Items[i].ID == leftRoot.ID {
			leftItem = &allAfterLeave.Items[i]
			break
		}
	}
	if leftItem == nil {
		t.Fatalf("expected left-channel thread in activity feed")
	}
	if !leftItem.AccessDenied {
		t.Fatalf("left channel thread should set access_denied=true")
	}
}

func TestUserActivity_MergesIssueInboxItem(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	targetID := createWorkspaceMemberUser(t, "activity-inbox-"+uuid.NewString()[:8], uuid.NewString()+"@multica.test")
	issueID := createIssueForTimeline(t, "activity inbox merge")
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO inbox_item (
			workspace_id, recipient_type, recipient_id, type, severity, issue_id, title, body, read
		) VALUES ($1, 'member', $2, 'issue_assigned', 'action_required', $3, 'Assigned to you', 'please review', false)`,
		testWorkspaceID, targetID, issueID); err != nil {
		t.Fatalf("seed inbox item: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM inbox_item WHERE recipient_id = $1 AND issue_id = $2`, targetID, issueID)
	})

	resp, status := fetchUserActivity(t, targetID, "all")
	if status != http.StatusOK {
		t.Fatalf("status=%d", status)
	}
	found := false
	for _, item := range resp.Items {
		if item.Kind == "inbox" && item.Inbox != nil && item.Inbox.IssueID != nil && *item.Inbox.IssueID == issueID {
			found = true
			if item.Inbox.Type != "issue_assigned" {
				t.Fatalf("inbox type=%q", item.Inbox.Type)
			}
			break
		}
	}
	if !found {
		t.Fatalf("expected issue inbox item in activity feed")
	}

	unread, status := fetchUserActivity(t, targetID, "unread")
	if status != http.StatusOK {
		t.Fatalf("unread status=%d", status)
	}
	foundUnread := false
	for _, item := range unread.Items {
		if item.Kind == "inbox" && item.Inbox != nil && item.Inbox.IssueID != nil && *item.Inbox.IssueID == issueID {
			foundUnread = true
		}
	}
	if !foundUnread {
		t.Fatalf("expected unread issue inbox item")
	}
}

func TestUserActivity_DMThreadIncluded(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	targetID := createWorkspaceMemberUser(t, "activity-dm-"+uuid.NewString()[:8], uuid.NewString()+"@multica.test")
	channelID := seedChannelForTest(t, "activity-dm-"+uuid.NewString(), testUserID, targetID)
	if _, err := testPool.Exec(ctx, `UPDATE channel SET kind = 'dm' WHERE id = $1`, channelID); err != nil {
		t.Fatalf("mark dm: %v", err)
	}
	rec := sendChannelMessageForTest(t, channelID, targetID, map[string]any{"content": "dm root " + uuid.NewString()})
	if rec.Code != http.StatusCreated {
		t.Fatalf("dm root status=%d", rec.Code)
	}
	var root ChannelMessageResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &root); err != nil {
		t.Fatalf("decode dm root: %v", err)
	}
	rec = sendChannelThreadReplyForTest(t, channelID, root.ID, testUserID, map[string]any{"content": "dm reply"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("dm reply status=%d", rec.Code)
	}

	resp, status := fetchUserActivity(t, targetID, "all")
	if status != http.StatusOK {
		t.Fatalf("status=%d", status)
	}
	for _, item := range resp.Items {
		if item.Kind == "thread" && item.ID == root.ID {
			if item.ChannelKind == nil || *item.ChannelKind != "dm" {
				t.Fatalf("dm thread channel_kind=%v", item.ChannelKind)
			}
			// LRM-809: human dm → actor is the peer user (the other member).
			if item.ActorType == nil || *item.ActorType != "user" {
				t.Fatalf("dm thread actor_type=%v, want user", item.ActorType)
			}
			if item.ActorID == nil || *item.ActorID != testUserID {
				t.Fatalf("dm thread actor_id=%v, want %s", item.ActorID, testUserID)
			}
			return
		}
	}
	t.Fatalf("dm thread %s missing from activity feed", root.ID)
}

// LRM-809: the activity row avatar actor — group threads show the root
// author; user↔agent dm threads show the agent peer.
func TestUserActivity_ThreadActor(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()

	t.Run("group thread actor is the root author", func(t *testing.T) {
		targetHandle := "activity-actor-g-" + uuid.NewString()[:8]
		targetID := createWorkspaceMemberUser(t, targetHandle, targetHandle+"@multica.test")
		channelID := seedChannelForTest(t, "activity-actor-g-"+uuid.NewString(), testUserID, targetID)
		mention := protocol.MessagePart{
			Type:       protocol.MessagePartTypeReference,
			RefType:    "mention",
			RefSubType: "member",
			RefID:      targetID,
			Label:      "@" + targetHandle,
		}
		rec := sendChannelMessageForTest(t, channelID, testUserID, map[string]any{
			"content": "group root " + uuid.NewString(),
		})
		if rec.Code != http.StatusCreated {
			t.Fatalf("root status=%d", rec.Code)
		}
		var root ChannelMessageResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &root); err != nil {
			t.Fatalf("decode root: %v", err)
		}
		// Mentions surface the thread for the target user (FilterMatrix pattern:
		// mention parts travel on replies).
		rec = sendChannelThreadReplyForTest(t, channelID, root.ID, testUserID, map[string]any{
			"content": "@" + targetHandle + " please look",
			"parts":   []protocol.MessagePart{mention},
		})
		if rec.Code != http.StatusCreated {
			t.Fatalf("mention reply status=%d body=%s", rec.Code, rec.Body.String())
		}

		resp, status := fetchUserActivity(t, targetID, "all")
		if status != http.StatusOK {
			t.Fatalf("status=%d", status)
		}
		for _, item := range resp.Items {
			if item.Kind == "thread" && item.ID == root.ID {
				if item.ActorType == nil || *item.ActorType != "user" {
					t.Fatalf("group actor_type=%v, want user", item.ActorType)
				}
				if item.ActorID == nil || *item.ActorID != testUserID {
					t.Fatalf("group actor_id=%v, want %s", item.ActorID, testUserID)
				}
				return
			}
		}
		t.Fatalf("group thread %s missing from activity feed", root.ID)
	})

	t.Run("agent dm thread actor is the agent peer", func(t *testing.T) {
		targetID := createWorkspaceMemberUser(t, "activity-actor-a-"+uuid.NewString()[:8], uuid.NewString()+"@multica.test")
		channelID := seedChannelForTest(t, "activity-actor-a-"+uuid.NewString(), testUserID, targetID)
		if _, err := testPool.Exec(ctx, `UPDATE channel SET kind = 'dm' WHERE id = $1`, channelID); err != nil {
			t.Fatalf("mark dm: %v", err)
		}
		var agentID string
		if err := testPool.QueryRow(ctx, `
			INSERT INTO agent (
				workspace_id, name, description, runtime_mode, runtime_config, runtime_id, max_concurrent_tasks, owner_id, instructions, custom_env, custom_args, model
			) VALUES ($1, $2, '', 'cloud', '{}'::jsonb, $3, 1, $4, '', '{}'::jsonb, '[]'::jsonb, 'composer-1.5')
			RETURNING id
		`, testWorkspaceID, "activity-actor-agent-"+uuid.NewString()[:8], handlerTestRuntimeID(t), testUserID).Scan(&agentID); err != nil {
			t.Fatalf("create agent: %v", err)
		}
		t.Cleanup(func() {
			testPool.Exec(context.Background(), `DELETE FROM agent WHERE id = $1`, agentID)
		})
		if _, err := testPool.Exec(ctx, `
			INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
			VALUES ($1, $2, 'agent', $3)`, channelID, testWorkspaceID, agentID); err != nil {
			t.Fatalf("add agent member: %v", err)
		}

		rec := sendChannelMessageForTest(t, channelID, targetID, map[string]any{"content": "agent dm root " + uuid.NewString()})
		if rec.Code != http.StatusCreated {
			t.Fatalf("dm root status=%d", rec.Code)
		}
		var root ChannelMessageResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &root); err != nil {
			t.Fatalf("decode dm root: %v", err)
		}
		rec = sendChannelThreadReplyForTest(t, channelID, root.ID, testUserID, map[string]any{"content": "agent dm reply"})
		if rec.Code != http.StatusCreated {
			t.Fatalf("dm reply status=%d", rec.Code)
		}

		resp, status := fetchUserActivity(t, targetID, "all")
		if status != http.StatusOK {
			t.Fatalf("status=%d", status)
		}
		for _, item := range resp.Items {
			if item.Kind == "thread" && item.ID == root.ID {
				if item.ActorType == nil || *item.ActorType != "agent" {
					t.Fatalf("agent dm actor_type=%v, want agent", item.ActorType)
				}
				if item.ActorID == nil || *item.ActorID != agentID {
					t.Fatalf("agent dm actor_id=%v, want %s", item.ActorID, agentID)
				}
				return
			}
		}
		t.Fatalf("agent dm thread %s missing from activity feed", root.ID)
	})
}

func TestUserActivity_InvalidTab(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	_, status := fetchUserActivity(t, testUserID, "bogus")
	if status != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400", status)
	}
}

func TestUserActivity_MarkAllRead(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	targetID := createWorkspaceMemberUser(t, "activity-read-"+uuid.NewString()[:8], uuid.NewString()+"@multica.test")
	channelID := seedChannelForTest(t, "activity-read-"+uuid.NewString(), testUserID, targetID)
	rec := sendChannelMessageForTest(t, channelID, testUserID, map[string]any{"content": "mark all read " + uuid.NewString()})
	if rec.Code != http.StatusCreated {
		t.Fatalf("root status=%d", rec.Code)
	}
	var root ChannelMessageResponse
	json.Unmarshal(rec.Body.Bytes(), &root)
	rec = sendChannelThreadReplyForTest(t, channelID, root.ID, testUserID, map[string]any{"content": "unread bump"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("reply status=%d", rec.Code)
	}

	issueID := createIssueForTimeline(t, fmt.Sprintf("activity mark read %s", uuid.NewString()[:8]))
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO inbox_item (
			workspace_id, recipient_type, recipient_id, type, severity, issue_id, title, read
		) VALUES ($1, 'member', $2, 'mentioned', 'info', $3, 'issue mention', false)`,
		testWorkspaceID, targetID, issueID); err != nil {
		t.Fatalf("seed issue mention inbox: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM inbox_item WHERE recipient_id = $1 AND issue_id = $2`, targetID, issueID)
	})

	req := newRequestAs(targetID, http.MethodPost, "/api/activity/mark-all-read", nil)
	req = withChannelTestWorkspaceCtx(t, req, targetID)
	markRec := httptest.NewRecorder()
	testHandler.MarkAllUserActivityRead(markRec, req)
	if markRec.Code != http.StatusOK {
		t.Fatalf("mark all read status=%d body=%s", markRec.Code, markRec.Body.String())
	}

	unread, status := fetchUserActivity(t, targetID, "unread")
	if status != http.StatusOK {
		t.Fatalf("unread status=%d", status)
	}
	if len(unread.Items) != 0 {
		t.Fatalf("expected empty unread feed after mark-all-read, got %d items", len(unread.Items))
	}
}
