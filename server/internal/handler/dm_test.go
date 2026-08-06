package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// TestDMCanonicalName locks in the deterministic, order-independent, lowercased
// canonical name that makes DM create-or-find idempotent via UNIQUE(workspace_id,name).
func TestDMCanonicalName(t *testing.T) {
	a := dmCanonicalName("user", "AAA", "agent", "BBB")
	b := dmCanonicalName("agent", "BBB", "user", "AAA")
	if a != b {
		t.Fatalf("canonical name not order-independent: %q vs %q", a, b)
	}
	if got := dmCanonicalName("user", "ABC", "agent", "abc"); got != "dm:agent:abc|user:abc" {
		t.Fatalf("expected lowercased sorted name, got %q", got)
	}
	// "agent:" sorts before "user:", so a human↔agent DM always leads with the agent token.
	if got := dmCanonicalName("user", "u1", "agent", "a1"); got != "dm:agent:a1|user:u1" {
		t.Fatalf("unexpected human↔agent canonical: %q", got)
	}
}

func postCreateOrFindDM(t *testing.T, peerType, peerID string) (*httptest.ResponseRecorder, DMItem) {
	t.Helper()
	req := newRequest("POST", "/api/dm", map[string]string{"peer_type": peerType, "peer_id": peerID})
	req = withChatTestWorkspaceCtx(t, req)
	rec := httptest.NewRecorder()
	testHandler.CreateOrFindDirectMessage(rec, req)
	var item DMItem
	if rec.Body.Len() > 0 {
		_ = json.Unmarshal(rec.Body.Bytes(), &item)
	}
	return rec, item
}

func seedLegacySession(t *testing.T, agentID string) string {
	t.Helper()
	var id string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO chat_session (workspace_id, agent_id, creator_id, title, status)
		VALUES ($1, $2, $3, 'Legacy Chat', 'active')
		RETURNING id`, testWorkspaceID, agentID, testUserID).Scan(&id); err != nil {
		t.Fatalf("seed legacy session for %s: %v", agentID, err)
	}
	return id
}

func cleanupDMArtifacts(t *testing.T) {
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM dm_peer_state WHERE workspace_id=$1 AND user_id=$2`, testWorkspaceID, testUserID)
		testPool.Exec(context.Background(), `DELETE FROM channel WHERE workspace_id=$1 AND kind='dm'`, testWorkspaceID)
		testPool.Exec(context.Background(), `DELETE FROM chat_session WHERE workspace_id=$1 AND creator_id=$2`, testWorkspaceID, testUserID)
	})
}

func seedAgentDMChannel(t *testing.T, agentID string) string {
	t.Helper()
	return seedAgentDMChannelForUser(t, testUserID, agentID)
}

func seedAgentDMChannelForUser(t *testing.T, userID, agentID string) string {
	t.Helper()
	ctx := context.Background()
	var channelID string
	canonical := dmCanonicalName("user", userID, "agent", agentID)
	if err := testPool.QueryRow(ctx, `
		INSERT INTO channel (workspace_id, name, created_by, kind)
		VALUES ($1,$2,$3,'dm') RETURNING id`, testWorkspaceID, canonical, userID).Scan(&channelID); err != nil {
		t.Fatalf("seed dm channel: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1,$2,'user',$3),($1,$2,'agent',$4)
ON CONFLICT DO NOTHING`, channelID, testWorkspaceID, userID, agentID); err != nil {
		t.Fatalf("seed members: %v", err)
	}
	return channelID
}

// TestCreateOrFindAgentDM_Idempotent: the first call creates the visible
// dm_channel (201), and a repeat call returns the same channel (200).
func TestCreateOrFindAgentDM_Idempotent(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	cleanupDMArtifacts(t)
	agentID := createHandlerTestAgent(t, "DM Idem Bot", []byte("[]"))

	rec1, item1 := postCreateOrFindDM(t, "agent", agentID)
	if rec1.Code != http.StatusCreated {
		t.Fatalf("first create: status=%d body=%s", rec1.Code, rec1.Body.String())
	}
	if item1.Source != dmSourceChannel || item1.Peer.Type != "agent" || item1.Peer.ID != agentID {
		t.Fatalf("unexpected item1: %+v", item1)
	}
	rec2, item2 := postCreateOrFindDM(t, "agent", agentID)
	if rec2.Code != http.StatusOK {
		t.Fatalf("second find: status=%d body=%s", rec2.Code, rec2.Body.String())
	}
	if item2.ID != item1.ID || item2.Source != dmSourceChannel {
		t.Fatalf("not idempotent: first=%+v second=%+v", item1, item2)
	}
	// task #76: count only this test's own DM channel, not every kind='dm'
	// row in the shared testWorkspaceID. Other tests in this package (and
	// production paths like agentDMChannel/ensureAgentHumanDMChannel) create
	// real user<->agent DM channels as a legitimate side effect, so counting
	// the whole workspace fails whenever one of those runs earlier in the
	// same `go test` invocation — this assertion should only care whether
	// *this* create-or-find call produced a duplicate.
	var n int
	canonicalName := dmCanonicalName("user", testUserID, "agent", agentID)
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM channel WHERE workspace_id=$1 AND kind='dm' AND name=$2`, testWorkspaceID, canonicalName).Scan(&n); err != nil || n != 1 {
		t.Fatalf("agent DM channel count=%d err=%v, want 1", n, err)
	}
}

// TestCreateOrFindAgentDM_IgnoresLegacySession proves legacy sessions are kept
// only for migration/history: they must not block a new visible dm_channel.
func TestCreateOrFindAgentDM_IgnoresLegacySession(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	cleanupDMArtifacts(t)
	agentID := createHandlerTestAgent(t, "DM Legacy Bot", []byte("[]"))
	sessionID := seedLegacySession(t, agentID)

	rec, item := postCreateOrFindDM(t, "agent", agentID)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if item.Source != dmSourceChannel || item.ID == sessionID {
		t.Fatalf("expected dm_channel separate from legacy session %s, got %+v", sessionID, item)
	}
	// task #76 sibling: same fix as TestCreateOrFindAgentDM_Idempotent — count
	// only this test's own DM channel, not every kind='dm' row in the shared
	// testWorkspaceID.
	var channelCount, sessionCount int
	canonicalName := dmCanonicalName("user", testUserID, "agent", agentID)
	testPool.QueryRow(ctx, `SELECT count(*) FROM channel WHERE workspace_id=$1 AND kind='dm' AND name=$2`, testWorkspaceID, canonicalName).Scan(&channelCount)
	testPool.QueryRow(ctx, `SELECT count(*) FROM chat_session WHERE id=$1`, sessionID).Scan(&sessionCount)
	if channelCount != 1 || sessionCount != 1 {
		t.Fatalf("channelCount=%d sessionCount=%d, want both preserved", channelCount, sessionCount)
	}
}

// TestCreateOrFindUserDM covers the human↔human path: a dm channel with both
// users as members, idempotent, and self-DM rejected.
func TestCreateOrFindUserDM(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	cleanupDMArtifacts(t)

	var peerUserID string
	peerName := "DM Peer " + uuid.NewString()
	peerEmail := "dm-peer-test-" + uuid.NewString() + "@multica.ai"
	if err := testPool.QueryRow(ctx, `INSERT INTO "user" (name, email) VALUES ($1,$2) RETURNING id`, peerName, peerEmail).Scan(&peerUserID); err != nil {
		t.Fatalf("seed peer user: %v", err)
	}
	if _, err := testPool.Exec(ctx, `INSERT INTO member (workspace_id, user_id, role) VALUES ($1,$2,'member')`, testWorkspaceID, peerUserID); err != nil {
		t.Fatalf("seed peer member: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM member WHERE user_id=$1`, peerUserID)
		testPool.Exec(context.Background(), `DELETE FROM "user" WHERE id=$1`, peerUserID)
	})

	rec, item := postCreateOrFindDM(t, "user", peerUserID)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if item.Source != dmSourceChannel || item.Peer.Type != "user" || item.Peer.ID != peerUserID {
		t.Fatalf("unexpected item: %+v", item)
	}
	var n int
	testPool.QueryRow(ctx, `SELECT count(*) FROM channel_member WHERE channel_id=$1 AND member_type='user'`, item.ID).Scan(&n)
	if n != 2 {
		t.Fatalf("user member count=%d, want 2", n)
	}
	rec2, item2 := postCreateOrFindDM(t, "user", peerUserID)
	if rec2.Code != http.StatusOK || item2.ID != item.ID {
		t.Fatalf("user DM not idempotent: code=%d id=%s vs %s", rec2.Code, item2.ID, item.ID)
	}
	recSelf, _ := postCreateOrFindDM(t, "user", testUserID)
	if recSelf.Code != http.StatusBadRequest {
		t.Fatalf("self-DM should be 400, got %d", recSelf.Code)
	}
}

// TestListDirectMessages_ChannelsOnly: /api/dm only returns dm_channel rows.
// Legacy chat_sessions are preserved for migration/history but are not visible
// DM list sources and cannot block new agent DMs.
func TestListDirectMessages_ChannelsOnly(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	cleanupDMArtifacts(t)
	agentA := createHandlerTestAgent(t, "DM List A", []byte("[]")) // old dm channel only
	agentB := createHandlerTestAgent(t, "DM List B", []byte("[]")) // legacy session only
	agentC := createHandlerTestAgent(t, "DM List C", []byte("[]")) // both → channel only

	seedAgentDMChannel(t, agentA)
	seedLegacySession(t, agentB)
	seedAgentDMChannel(t, agentC)
	seedLegacySession(t, agentC)

	req := newRequest("GET", "/api/dm", nil)
	req = withChatTestWorkspaceCtx(t, req)
	rec := httptest.NewRecorder()
	testHandler.ListDirectMessages(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var items []DMItem
	if err := json.Unmarshal(rec.Body.Bytes(), &items); err != nil {
		t.Fatalf("decode: %v body=%s", err, rec.Body.String())
	}
	source := map[string]string{}
	count := map[string]int{}
	for _, it := range items {
		if it.Peer.Type != "agent" {
			continue
		}
		source[it.Peer.ID] = it.Source
		count[it.Peer.ID]++
	}
	if source[agentA] != dmSourceChannel {
		t.Fatalf("agentA source=%q, want dm_channel", source[agentA])
	}
	if source[agentB] != "" || count[agentB] != 0 {
		t.Fatalf("agentB legacy-only session should not be visible, source=%q count=%d", source[agentB], count[agentB])
	}
	if count[agentC] != 1 || source[agentC] != dmSourceChannel {
		t.Fatalf("agentC should keep visible dm_channel only, source=%q count=%d", source[agentC], count[agentC])
	}
}

// TestAgentDMSupervisionListReadOnly replaces
// "...ListReadOnlyAndOwnerControls" now that the owner pause/grant control
// panel is gone (#813/#830 follow-up, 2026-07-31: Frank asked for all four
// pause gates removed, not just the automatic two). What's still real and
// worth locking down: an agent owner can see and read a DM between their
// agent and another agent even though they aren't a channel member, but
// still cannot post into it; a non-owner can't see or read it at all.
func TestAgentDMSupervisionListReadOnly(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	firstAgentID := createHandlerTestAgent(t, "Supervised A "+uuid.NewString(), []byte("[]"))
	secondAgentID := createHandlerTestAgent(t, "Supervised B "+uuid.NewString(), []byte("[]"))
	canonical := dmCanonicalName("agent", firstAgentID, "agent", secondAgentID)
	channel, created := testHandler.createDMChannel(
		ctx,
		nil,
		testWorkspaceID,
		testUserID,
		canonical,
		[]dmMember{
			{memberType: "agent", memberID: parseUUID(firstAgentID)},
			{memberType: "agent", memberID: parseUUID(secondAgentID)},
		},
	)
	if !created {
		t.Fatal("create supervised A2A DM channel failed")
	}
	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM channel WHERE id = $1`, channel.ID)
	})

	if _, err := testHandler.insertChannelMessage(
		ctx,
		parseUUID(channel.ID),
		parseUUID(testWorkspaceID),
		"agent",
		parseUUID(firstAgentID),
		"Supervised A",
		"private A2A message",
		"multica",
		nil,
		pgtype.UUID{},
		pgtype.UUID{},
		nil,
		0,
	); err != nil {
		t.Fatalf("seed supervised A2A message: %v", err)
	}

	listReq := withChatTestWorkspaceCtx(
		t,
		newRequestAs(testUserID, http.MethodGet, "/api/dm", nil),
	)
	listRec := httptest.NewRecorder()
	testHandler.ListDirectMessages(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("owner list DMs: status=%d body=%s", listRec.Code, listRec.Body.String())
	}
	var items []DMItem
	if err := json.Unmarshal(listRec.Body.Bytes(), &items); err != nil {
		t.Fatalf("decode owner DM list: %v", err)
	}
	var supervised *DMItem
	for i := range items {
		if items[i].ID == channel.ID {
			supervised = &items[i]
			break
		}
	}
	if supervised == nil {
		t.Fatalf("owner DM list omitted supervised channel %s: %+v", channel.ID, items)
	}
	if supervised.Mode != "agent_pair" || !supervised.Supervised || len(supervised.Participants) != 2 {
		t.Fatalf("unexpected supervised DM item: %+v", supervised)
	}

	messageReq := withURLParam(
		withChatTestWorkspaceCtx(
			t,
			newRequestAs(testUserID, http.MethodGet, "/api/channels/"+channel.ID+"/messages", nil),
		),
		"channelId",
		channel.ID,
	)
	messageRec := httptest.NewRecorder()
	testHandler.ListChannelMessages(messageRec, messageReq)
	if messageRec.Code != http.StatusOK {
		t.Fatalf("owner read supervised DM: status=%d body=%s", messageRec.Code, messageRec.Body.String())
	}
	var page ChannelMessagesPageResponse
	if err := json.Unmarshal(messageRec.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode supervised messages: %v", err)
	}
	if len(page.Messages) != 1 {
		t.Fatalf("unexpected supervised messages page: %+v", page)
	}

	sendReq := withURLParam(
		withChatTestWorkspaceCtx(
			t,
			newRequestAs(testUserID, http.MethodPost, "/api/channels/"+channel.ID+"/messages", map[string]any{
				"content":           "owner must not speak",
				"client_message_id": uuid.NewString(),
			}),
		),
		"channelId",
		channel.ID,
	)
	sendRec := httptest.NewRecorder()
	testHandler.SendChannelMessage(sendRec, sendReq)
	if sendRec.Code != http.StatusForbidden {
		t.Fatalf("owner send to supervised DM: status=%d body=%s", sendRec.Code, sendRec.Body.String())
	}

	otherUserID := seedWorkspaceUserForTransportTargetTest(t, "a2a-non-owner-"+uuid.NewString()[:8])
	otherListReq := withChatTestWorkspaceCtx(
		t,
		newRequestAs(otherUserID, http.MethodGet, "/api/dm", nil),
	)
	otherListRec := httptest.NewRecorder()
	testHandler.ListDirectMessages(otherListRec, otherListReq)
	if otherListRec.Code != http.StatusOK {
		t.Fatalf("non-owner list DMs: status=%d body=%s", otherListRec.Code, otherListRec.Body.String())
	}
	var otherItems []DMItem
	if err := json.Unmarshal(otherListRec.Body.Bytes(), &otherItems); err != nil {
		t.Fatalf("decode non-owner DM list: %v", err)
	}
	for _, item := range otherItems {
		if item.ID == channel.ID {
			t.Fatalf("non-owner saw supervised A2A DM: %+v", item)
		}
	}
	otherReadReq := withURLParam(
		withChatTestWorkspaceCtx(
			t,
			newRequestAs(otherUserID, http.MethodGet, "/api/channels/"+channel.ID+"/messages", nil),
		),
		"channelId",
		channel.ID,
	)
	otherReadRec := httptest.NewRecorder()
	testHandler.ListChannelMessages(otherReadRec, otherReadReq)
	if otherReadRec.Code != http.StatusForbidden {
		t.Fatalf("non-owner read supervised DM: status=%d body=%s", otherReadRec.Code, otherReadRec.Body.String())
	}
}

// TestListSupervisedAgentDMChannelsIncludesAllParticipants verifies the list
// keeps every supervised agent-pair's participant set when multiple rows are
// returned. This is the regression coverage for batching the participant lookup
// rather than issuing one query per visible pair.
func TestListSupervisedAgentDMChannelsIncludesAllParticipants(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	channelIDs := make([]string, 0, 3)
	for i := 0; i < 3; i++ {
		firstAgentID := createHandlerTestAgent(t, "Batch pair A "+uuid.NewString(), []byte("[]"))
		secondAgentID := createHandlerTestAgent(t, "Batch pair B "+uuid.NewString(), []byte("[]"))
		channel, created := testHandler.createDMChannel(
			ctx,
			nil,
			testWorkspaceID,
			testUserID,
			dmCanonicalName("agent", firstAgentID, "agent", secondAgentID),
			[]dmMember{
				{memberType: "agent", memberID: parseUUID(firstAgentID)},
				{memberType: "agent", memberID: parseUUID(secondAgentID)},
			},
		)
		if !created {
			t.Fatalf("create supervised pair %d", i)
		}
		channelIDs = append(channelIDs, channel.ID)
		t.Cleanup(func() {
			testPool.Exec(ctx, `DELETE FROM channel WHERE id = $1`, channel.ID)
		})
	}

	items := listDMItemsForTest(t)
	for _, channelID := range channelIDs {
		var found *DMItem
		for i := range items {
			if items[i].ID == channelID {
				found = &items[i]
				break
			}
		}
		if found == nil {
			t.Fatalf("supervised channel %s missing from DM list", channelID)
		}
		if !found.Supervised || found.Mode != "agent_pair" || len(found.Participants) != 2 {
			t.Fatalf("supervised channel %s has unexpected participants: %+v", channelID, found)
		}
	}
}

// TestSupervisedAgentPairUnreadAndMarkRead (LRM-762): supervised agent_pair list
// projects real unread from channel_read; owner mark-read clears it without
// becoming a channel_member (write stays forbidden).
func TestSupervisedAgentPairUnreadAndMarkRead(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	firstAgentID := createHandlerTestAgent(t, "Unread A "+uuid.NewString(), []byte("[]"))
	secondAgentID := createHandlerTestAgent(t, "Unread B "+uuid.NewString(), []byte("[]"))
	canonical := dmCanonicalName("agent", firstAgentID, "agent", secondAgentID)
	channel, created := testHandler.createDMChannel(
		ctx,
		nil,
		testWorkspaceID,
		testUserID,
		canonical,
		[]dmMember{
			{memberType: "agent", memberID: parseUUID(firstAgentID)},
			{memberType: "agent", memberID: parseUUID(secondAgentID)},
		},
	)
	if !created {
		t.Fatal("create supervised A2A DM channel failed")
	}
	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM channel WHERE id = $1`, channel.ID)
	})

	msg, err := testHandler.insertChannelMessage(
		ctx,
		parseUUID(channel.ID),
		parseUUID(testWorkspaceID),
		"agent",
		parseUUID(firstAgentID),
		"Unread A",
		"supervised unread seed",
		"multica",
		nil,
		pgtype.UUID{},
		pgtype.UUID{},
		nil,
		0,
	)
	if err != nil {
		t.Fatalf("seed supervised message: %v", err)
	}

	before := listedDMItemForTest(t, channel.ID)
	if before == nil {
		t.Fatal("supervised channel missing from DM list")
	}
	if before.RealUnread != 1 || before.Unread != 1 {
		t.Fatalf("supervised unread before read = real:%d total:%d, want 1/1", before.RealUnread, before.Unread)
	}

	markReq := withURLParam(
		withChatTestWorkspaceCtx(
			t,
			newRequestAs(testUserID, http.MethodPost, "/api/channels/"+channel.ID+"/read", nil),
		),
		"channelId",
		channel.ID,
	)
	markRec := httptest.NewRecorder()
	testHandler.MarkChannelRead(markRec, markReq)
	if markRec.Code != http.StatusOK {
		t.Fatalf("supervisor mark-read: status=%d body=%s", markRec.Code, markRec.Body.String())
	}

	after := listedDMItemForTest(t, channel.ID)
	if after == nil {
		t.Fatal("supervised channel missing after mark-read")
	}
	if after.RealUnread != 0 || after.Unread != 0 {
		t.Fatalf("supervised unread after read = real:%d total:%d, want 0/0", after.RealUnread, after.Unread)
	}
	if after.LastReadSeq == nil || *after.LastReadSeq != msg.Seq {
		t.Fatalf("supervised last_read_seq = %v, want %d", after.LastReadSeq, msg.Seq)
	}

	var memberCount int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM channel_member
		WHERE channel_id = $1 AND member_type = 'user' AND member_id = $2`,
		channel.ID, testUserID).Scan(&memberCount); err != nil {
		t.Fatalf("count channel_member: %v", err)
	}
	if memberCount != 0 {
		t.Fatalf("mark-read must not add supervisor as channel_member; count=%d", memberCount)
	}

	sendReq := withURLParam(
		withChatTestWorkspaceCtx(
			t,
			newRequestAs(testUserID, http.MethodPost, "/api/channels/"+channel.ID+"/messages", map[string]any{
				"content":           "still read-only",
				"client_message_id": uuid.NewString(),
			}),
		),
		"channelId",
		channel.ID,
	)
	sendRec := httptest.NewRecorder()
	testHandler.SendChannelMessage(sendRec, sendReq)
	if sendRec.Code != http.StatusForbidden {
		t.Fatalf("supervisor send after mark-read: status=%d body=%s", sendRec.Code, sendRec.Body.String())
	}
}

// TestSupervisedAgentPairListPreferences (LRM-845): owner may pin/mute/mark_unread/close
// a supervised agent_pair DM; prefs key by channel in dm_peer_state; list projects them;
// non-owners stay 403; message send stays read-only.
func TestSupervisedAgentPairListPreferences(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	firstAgentID := createHandlerTestAgent(t, "Pref A "+uuid.NewString(), []byte("[]"))
	secondAgentID := createHandlerTestAgent(t, "Pref B "+uuid.NewString(), []byte("[]"))
	// Also seed a personal 1:1 with the lexicographically-first agent so channel-keyed
	// prefs cannot collide with peer-keyed 1:1 state.
	personalChannelID := seedAgentDMChannel(t, firstAgentID)

	canonical := dmCanonicalName("agent", firstAgentID, "agent", secondAgentID)
	channel, created := testHandler.createDMChannel(
		ctx,
		nil,
		testWorkspaceID,
		testUserID,
		canonical,
		[]dmMember{
			{memberType: "agent", memberID: parseUUID(firstAgentID)},
			{memberType: "agent", memberID: parseUUID(secondAgentID)},
		},
	)
	if !created {
		t.Fatal("create supervised A2A DM channel failed")
	}
	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM channel WHERE id = $1`, channel.ID)
		testPool.Exec(ctx, `
			DELETE FROM dm_peer_state
			WHERE workspace_id = $1 AND user_id = $2 AND peer_type = 'channel' AND peer_id = $3`,
			testWorkspaceID, testUserID, channel.ID)
	})

	callPref := func(method, path string, handler http.HandlerFunc) int {
		t.Helper()
		req := withURLParam(
			withChatTestWorkspaceCtx(t, newRequestAs(testUserID, method, path, nil)),
			"channelId",
			channel.ID,
		)
		rec := httptest.NewRecorder()
		handler(rec, req)
		return rec.Code
	}

	if code := callPref(http.MethodPut, "/api/dm/channels/"+channel.ID+"/pin", testHandler.PinDMChannel); code != http.StatusOK {
		t.Fatalf("owner pin supervised: status=%d", code)
	}
	if code := callPref(http.MethodPut, "/api/dm/channels/"+channel.ID+"/mute", testHandler.MuteDMChannel); code != http.StatusOK {
		t.Fatalf("owner mute supervised: status=%d", code)
	}
	if code := callPref(http.MethodPost, "/api/dm/channels/"+channel.ID+"/unread", testHandler.MarkDMChannelUnread); code != http.StatusOK {
		t.Fatalf("owner mark unread supervised: status=%d", code)
	}

	var peerType string
	var peerID string
	var pinned, muted, manualUnread bool
	if err := testPool.QueryRow(ctx, `
		SELECT peer_type, peer_id::text,
		       pinned_at IS NOT NULL, muted_at IS NOT NULL, manual_unread_at IS NOT NULL
		FROM dm_peer_state
		WHERE workspace_id = $1 AND user_id = $2 AND peer_type = 'channel' AND peer_id = $3`,
		testWorkspaceID, testUserID, channel.ID,
	).Scan(&peerType, &peerID, &pinned, &muted, &manualUnread); err != nil {
		t.Fatalf("load channel-keyed dm_peer_state: %v", err)
	}
	if peerType != "channel" || peerID != channel.ID || !pinned || !muted || !manualUnread {
		t.Fatalf("unexpected supervised pref row: type=%s id=%s pinned=%v muted=%v unread=%v",
			peerType, peerID, pinned, muted, manualUnread)
	}

	// Personal 1:1 with firstAgent must remain unpinned/unmuted (no peer-key collision).
	var personalPinned int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM dm_peer_state
		WHERE workspace_id = $1 AND user_id = $2
		  AND peer_type = 'agent' AND peer_id = $3
		  AND (pinned_at IS NOT NULL OR muted_at IS NOT NULL OR manual_unread_at IS NOT NULL)`,
		testWorkspaceID, testUserID, firstAgentID,
	).Scan(&personalPinned); err != nil {
		t.Fatalf("count personal peer state: %v", err)
	}
	if personalPinned != 0 {
		t.Fatalf("supervised prefs leaked onto personal agent peer state; channel=%s", personalChannelID)
	}

	findSupervised := func() *DMItem {
		t.Helper()
		items := listDMItemsForTest(t)
		for i := range items {
			if items[i].ID == channel.ID {
				return &items[i]
			}
		}
		return nil
	}
	listed := findSupervised()
	if listed == nil {
		t.Fatal("supervised channel missing from DM list after prefs")
	}
	if listed.PinnedAt == nil || !listed.Muted || listed.MutedAt == nil || !listed.ManuallyUnread || listed.Unread == 0 {
		t.Fatalf("list did not project supervised prefs: %+v", listed)
	}

	otherUserID := seedWorkspaceUserForTransportTargetTest(t, "a2a-pref-non-owner-"+uuid.NewString()[:8])
	otherReq := withURLParam(
		withChatTestWorkspaceCtx(t, newRequestAs(otherUserID, http.MethodPut, "/api/dm/channels/"+channel.ID+"/pin", nil)),
		"channelId",
		channel.ID,
	)
	otherRec := httptest.NewRecorder()
	testHandler.PinDMChannel(otherRec, otherReq)
	if otherRec.Code != http.StatusForbidden {
		t.Fatalf("non-owner pin supervised: status=%d body=%s", otherRec.Code, otherRec.Body.String())
	}

	sendReq := withURLParam(
		withChatTestWorkspaceCtx(
			t,
			newRequestAs(testUserID, http.MethodPost, "/api/channels/"+channel.ID+"/messages", map[string]any{
				"content":           "owner must stay read-only",
				"client_message_id": uuid.NewString(),
			}),
		),
		"channelId",
		channel.ID,
	)
	sendRec := httptest.NewRecorder()
	testHandler.SendChannelMessage(sendRec, sendReq)
	if sendRec.Code != http.StatusForbidden {
		t.Fatalf("owner send after pref writes: status=%d body=%s", sendRec.Code, sendRec.Body.String())
	}

	if code := callPref(http.MethodDelete, "/api/dm/channels/"+channel.ID, testHandler.CloseDMChannel); code != http.StatusOK {
		t.Fatalf("owner close supervised: status=%d", code)
	}
	if findSupervised() != nil {
		t.Fatal("closed supervised channel still visible in DM list")
	}

	// New A2A message should unhide channel-keyed closed state for the supervisor.
	if _, err := testHandler.insertChannelMessage(
		ctx,
		parseUUID(channel.ID),
		parseUUID(testWorkspaceID),
		"agent",
		parseUUID(firstAgentID),
		"Pref A",
		"reopen after close",
		"multica",
		nil,
		pgtype.UUID{},
		pgtype.UUID{},
		nil,
		0,
	); err != nil {
		t.Fatalf("seed reopen message: %v", err)
	}
	testHandler.clearDMHiddenForChannelMembers(ctx, testWorkspaceID, parseUUID(channel.ID))
	if findSupervised() == nil {
		t.Fatal("supervised channel did not reappear after clearDMHiddenForChannelMembers")
	}
}

func TestListDirectMessages_UnwrapsStructuredAgentLastMessagePreview(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	cleanupDMArtifacts(t)
	agentID := createHandlerTestAgent(t, "DM Preview Bot "+uuid.NewString()[:8], []byte("[]"))
	channelID := seedAgentDMChannel(t, agentID)
	raw := `Assistant reply: {"action":"message_send","output":"Clean DM preview","parts":[{"type":"text","text":"Clean DM preview"}]}`
	if _, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "agent", parseUUID(agentID), "DM Preview Bot", raw, "multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 0); err != nil {
		t.Fatalf("seed structured agent dm message: %v", err)
	}

	var item *DMItem
	items := listDMItemsForTest(t)
	for i := range items {
		if items[i].Peer.Type == "agent" && items[i].Peer.ID == agentID {
			item = &items[i]
			break
		}
	}
	if item == nil || item.LastMessage == nil {
		t.Fatalf("listed dm preview missing for agent %s: %+v", agentID, items)
	}
	if item.LastMessage.Content != "Clean DM preview" {
		t.Fatalf("last message content = %q, want clean preview", item.LastMessage.Content)
	}
	if len(item.LastMessage.Parts) != 1 || item.LastMessage.Parts[0].Type != protocol.MessagePartTypeText || item.LastMessage.Parts[0].Text != "Clean DM preview" {
		t.Fatalf("last message parts = %+v, want one clean text part", item.LastMessage.Parts)
	}
}

func TestDMActionsApplyAtPeerLevelAcrossSources(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	cleanupDMArtifacts(t)
	agentID := createHandlerTestAgent(t, "DM Peer State Bot", []byte("[]"))

	channelID := seedAgentDMChannel(t, agentID)
	sessionID := seedLegacySession(t, agentID)

	req := newRequest(http.MethodPut, "/api/dm/channels/"+channelID+"/pin", nil)
	req = withChatTestWorkspaceCtx(t, req)
	req = withURLParam(req, "channelId", channelID)
	rec := httptest.NewRecorder()
	testHandler.PinDMChannel(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("pin channel-backed DM: status=%d body=%s", rec.Code, rec.Body.String())
	}

	req = newRequest(http.MethodPost, "/api/dm/sessions/"+sessionID+"/unread", nil)
	req = withChatTestWorkspaceCtx(t, req)
	req = withURLParam(req, "sessionId", sessionID)
	rec = httptest.NewRecorder()
	testHandler.MarkDMSessionUnread(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("mark legacy-backed DM unread: status=%d body=%s", rec.Code, rec.Body.String())
	}

	req = newRequest(http.MethodPut, "/api/dm/channels/"+channelID+"/mute", nil)
	req = withChatTestWorkspaceCtx(t, req)
	req = withURLParam(req, "channelId", channelID)
	rec = httptest.NewRecorder()
	testHandler.MuteDMChannel(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("mute channel-backed DM: status=%d body=%s", rec.Code, rec.Body.String())
	}

	got := listDMItemsForTest(t)
	var peer DMItem
	var found bool
	for _, it := range got {
		if it.Peer.Type == "agent" && it.Peer.ID == agentID {
			peer = it
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("peer missing after pin/unread; items=%+v", got)
	}
	if peer.PinnedAt == nil {
		t.Fatalf("peer-level pin missing on listed source %+v", peer)
	}
	if !peer.ManuallyUnread || peer.Unread == 0 {
		t.Fatalf("peer-level manual unread missing on listed source %+v", peer)
	}
	if !peer.Muted || peer.MutedAt == nil {
		t.Fatalf("peer-level mute missing on listed source %+v", peer)
	}
	var mutedAt *string
	if err := testPool.QueryRow(context.Background(), `
		SELECT cm.muted_at::text
		FROM conversation_member cm
		JOIN conversation c ON c.id = cm.conversation_id
		WHERE c.channel_id = $1
		  AND cm.member_type = 'user'
		  AND cm.member_id = $2`, channelID, testUserID).Scan(&mutedAt); err != nil {
		t.Fatalf("load conversation_member muted_at: %v", err)
	}
	if mutedAt == nil {
		t.Fatal("conversation_member muted_at not set for muted DM peer")
	}

	req = newRequest(http.MethodPost, "/api/channels/"+channelID+"/read", nil)
	req = withChatTestWorkspaceCtx(t, req)
	req = withURLParam(req, "channelId", channelID)
	rec = httptest.NewRecorder()
	testHandler.MarkChannelRead(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("mark channel read: status=%d body=%s", rec.Code, rec.Body.String())
	}

	got = listDMItemsForTest(t)
	for _, it := range got {
		if it.Peer.Type == "agent" && it.Peer.ID == agentID {
			if it.ManuallyUnread {
				t.Fatalf("channel read did not clear peer-level manual unread: %+v", it)
			}
			break
		}
	}

	req = newRequest(http.MethodDelete, "/api/dm/channels/"+channelID, nil)
	req = withChatTestWorkspaceCtx(t, req)
	req = withURLParam(req, "channelId", channelID)
	rec = httptest.NewRecorder()
	testHandler.CloseDMChannel(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("close channel-backed DM: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var closedAt *string
	if err := testPool.QueryRow(context.Background(), `
		SELECT cm.closed_at::text
		FROM conversation_member cm
		JOIN conversation c ON c.id = cm.conversation_id
		WHERE c.channel_id = $1
		  AND cm.member_type = 'user'
		  AND cm.member_id = $2`, channelID, testUserID).Scan(&closedAt); err != nil {
		t.Fatalf("load conversation_member closed_at: %v", err)
	}
	if closedAt == nil {
		t.Fatal("conversation_member closed_at not set for closed DM peer")
	}

	got = listDMItemsForTest(t)
	for _, it := range got {
		if it.Peer.Type == "agent" && it.Peer.ID == agentID {
			t.Fatalf("closed peer still visible through source %s: %+v", it.Source, it)
		}
	}

	rec, item := postCreateOrFindDM(t, "agent", agentID)
	if rec.Code != http.StatusOK {
		t.Fatalf("reopen hidden DM: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if item.ID != channelID || item.Source != dmSourceChannel {
		t.Fatalf("reopen should return dm channel %s, got %+v", channelID, item)
	}
	if err := testPool.QueryRow(context.Background(), `
		SELECT cm.closed_at::text
		FROM conversation_member cm
		JOIN conversation c ON c.id = cm.conversation_id
		WHERE c.channel_id = $1
		  AND cm.member_type = 'user'
		  AND cm.member_id = $2`, channelID, testUserID).Scan(&closedAt); err != nil {
		t.Fatalf("reload conversation_member closed_at: %v", err)
	}
	if closedAt != nil {
		t.Fatalf("conversation_member closed_at still set after reopen: %v", *closedAt)
	}
	got = listDMItemsForTest(t)
	found = false
	for _, it := range got {
		if it.Peer.Type == "agent" && it.Peer.ID == agentID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("POST /api/dm did not unhide peer")
	}
}

func TestLegacyDMSessionIsNotVisibleInDMList(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	cleanupDMArtifacts(t)
	agentID := createHandlerTestAgent(t, "DM Legacy Real Unread Bot", []byte("[]"))
	sessionID := seedLegacySession(t, agentID)
	if _, err := testPool.Exec(ctx, `UPDATE chat_session SET unread_since = now() WHERE id = $1`, sessionID); err != nil {
		t.Fatalf("mark legacy session unread: %v", err)
	}

	got := listDMItemsForTest(t)
	for _, it := range got {
		if it.ID == sessionID || it.Source != dmSourceChannel {
			t.Fatalf("legacy session should not be visible in DM list: %+v", it)
		}
	}
}

func TestListDMChannelsTreatsZeroReadSeqAsNoCursor(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	cleanupDMArtifacts(t)
	agentID := createHandlerTestAgent(t, "DM Zero Read Cursor Bot", []byte("[]"))
	channelID := seedAgentDMChannel(t, agentID)
	message, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "agent", parseUUID(agentID), "DM Zero Read Cursor Bot", "unread", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr("dm-zero-read-cursor"), 0)
	if err != nil {
		t.Fatalf("insert unread DM message: %v", err)
	}

	beforeRead := listedDMItemForTest(t, channelID)
	if beforeRead == nil {
		t.Fatal("DM missing from list before mark read")
	}
	if beforeRead.LastReadSeq != nil {
		t.Fatalf("last_read_seq before first read = %d, want nil", *beforeRead.LastReadSeq)
	}
	if beforeRead.RealUnread != 1 || beforeRead.Unread != 1 {
		t.Fatalf("unread before first read = real:%d total:%d, want 1/1", beforeRead.RealUnread, beforeRead.Unread)
	}

	markChannelReadForTest(t, channelID, testUserID)

	afterRead := listedDMItemForTest(t, channelID)
	if afterRead == nil {
		t.Fatal("DM missing from list after mark read")
	}
	if afterRead.LastReadSeq == nil || *afterRead.LastReadSeq != message.Seq {
		t.Fatalf("last_read_seq after mark read = %v, want %d", afterRead.LastReadSeq, message.Seq)
	}
	if afterRead.RealUnread != 0 || afterRead.Unread != 0 {
		t.Fatalf("unread after mark read = real:%d total:%d, want 0/0", afterRead.RealUnread, afterRead.Unread)
	}
}

// TestListDirectMessages_IncludesNonOwnerWendyDM is the end-to-end
// regression test for the 2026-07-31 Wendy DM incident: a member who is not
// the Wendy agent's owner must still see their own DM with it in
// GET /api/dm. Before the fix, accessibleAgentIDs' owner-only gate for
// Windy/Wendy-named agents silently dropped this DM from the list for
// anyone but the agent's owner, even though the channel existed and its
// messages were readable — reproducing exactly what Wren found live
// (POST /api/dm 201, messages 200, GET /api/dm []).
func TestListDirectMessages_IncludesNonOwnerWendyDM(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	cleanupDMArtifacts(t)
	agentID, _, memberID := privateAgentTestFixture(t)
	if _, err := testPool.Exec(context.Background(), `UPDATE agent SET display_name = 'Wendy' WHERE id = $1`, agentID); err != nil {
		t.Fatalf("rename agent to Wendy: %v", err)
	}
	channelID := seedAgentDMChannelForUser(t, memberID, agentID)

	req := newRequestAs(memberID, http.MethodGet, "/api/dm", nil)
	req = withChannelTestWorkspaceCtx(t, req, memberID)
	rec := httptest.NewRecorder()
	testHandler.ListDirectMessages(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list DMs as non-owner member: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var items []DMItem
	if err := json.Unmarshal(rec.Body.Bytes(), &items); err != nil {
		t.Fatalf("decode DMs: %v body=%s", err, rec.Body.String())
	}
	for _, item := range items {
		if item.ID == channelID {
			return
		}
	}
	t.Fatalf("non-owner member's Wendy DM (channel %s) missing from GET /api/dm: %+v", channelID, items)
}

func listDMItemsForTest(t *testing.T) []DMItem {
	t.Helper()
	req := newRequest(http.MethodGet, "/api/dm", nil)
	req = withChatTestWorkspaceCtx(t, req)
	rec := httptest.NewRecorder()
	testHandler.ListDirectMessages(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list DMs: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var items []DMItem
	if err := json.Unmarshal(rec.Body.Bytes(), &items); err != nil {
		t.Fatalf("decode DMs: %v body=%s", err, rec.Body.String())
	}
	return items
}

func listedDMItemForTest(t *testing.T, channelID string) *DMItem {
	t.Helper()
	items := listDMItemsForTest(t)
	for i := range items {
		if items[i].ID == channelID {
			return &items[i]
		}
	}
	return nil
}

// TestSendChannelMessageDM_DispatchesAgent: a user message in an agent DM
// auto-dispatches to the agent without any @-mention (LRM-1079: channel-only
// inbox wake, no forced chat_session).
func TestSendChannelMessageDM_DispatchesAgent(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	cleanupDMArtifacts(t)
	agentID := createHandlerTestAgent(t, "DM Dispatch Bot", nil)

	channelID := seedAgentDMChannel(t, agentID)

	req := newRequest("POST", "/api/channels/"+channelID+"/messages", map[string]string{"content": "hey, no mention needed"})
	req = withChatTestWorkspaceCtx(t, req)
	req = withURLParam(req, "channelId", channelID)
	rec := httptest.NewRecorder()
	testHandler.SendChannelMessage(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("send: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var eventID string
	var chatSessionID pgtype.UUID
	if err := testPool.QueryRow(ctx, `
		SELECT id::text, chat_session_id
		FROM agent_inbox_event
		WHERE channel_id = $1 AND agent_id = $2 AND requires_wake = true
		ORDER BY created_at DESC
		LIMIT 1`, channelID, agentID).Scan(&eventID, &chatSessionID); err != nil {
		t.Fatalf("DM did not auto-dispatch to the agent: %v", err)
	}
	if chatSessionID.Valid {
		t.Fatalf("DM wake must be channel-only; got chat_session_id=%s event=%s", uuidToString(chatSessionID), eventID)
	}
}

// TestListChannelMessages_ArchivedPeerStaysReadable locks the fix for the
// 2026-07-31 Wendy DM incident: a DM whose peer agent has since been
// archived must stay readable (200, real messages), never 404 the whole
// conversation out from under the user's message history.
func TestListChannelMessages_ArchivedPeerStaysReadable(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	cleanupDMArtifacts(t)
	agentID := createHandlerTestAgent(t, "Archived Peer Bot", nil)
	channelID := seedAgentDMChannel(t, agentID)
	if _, err := testPool.Exec(context.Background(), `UPDATE agent SET archived_at = now() WHERE id = $1`, agentID); err != nil {
		t.Fatalf("archive agent: %v", err)
	}

	req := newRequest("GET", "/api/channels/"+channelID+"/messages", nil)
	req = withChatTestWorkspaceCtx(t, req)
	req = withURLParam(req, "channelId", channelID)
	rec := httptest.NewRecorder()
	testHandler.ListChannelMessages(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("archived-peer DM should stay readable, got status=%d body=%s", rec.Code, rec.Body.String())
	}
}

// TestSendChannelMessage_RejectsArchivedPeer locks the other half: reads
// stay open, but new sends to an archived peer must still be blocked (not
// silently succeed into a dead agent).
func TestSendChannelMessage_RejectsArchivedPeer(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	cleanupDMArtifacts(t)
	agentID := createHandlerTestAgent(t, "Archived Peer Bot Write", nil)
	channelID := seedAgentDMChannel(t, agentID)
	if _, err := testPool.Exec(context.Background(), `UPDATE agent SET archived_at = now() WHERE id = $1`, agentID); err != nil {
		t.Fatalf("archive agent: %v", err)
	}

	req := newRequest("POST", "/api/channels/"+channelID+"/messages", map[string]string{"content": "hi"})
	req = withChatTestWorkspaceCtx(t, req)
	req = withURLParam(req, "channelId", channelID)
	rec := httptest.NewRecorder()
	testHandler.SendChannelMessage(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("sending to an archived peer should be rejected, got status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestSendChannelMessageDM_BypassesAmbientGateWithActiveAmbient(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	cleanupDMArtifacts(t)
	withChannelAmbientGateTestConfig(t)
	agentID := createHandlerTestAgent(t, "DM Ambient Gate Bypass "+uuid.NewString()[:8], nil)

	ambientChannelID := seedChannelForTest(t, "dm-ambient-gate-bypass-"+uuid.NewString(), testUserID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3)
ON CONFLICT DO NOTHING`, ambientChannelID, testWorkspaceID, agentID); err != nil {
		t.Fatalf("seed ambient agent member: %v", err)
	}
	ch, found := testHandler.getChannel(ctx, testWorkspaceID, parseUUID(ambientChannelID))
	if !found {
		t.Fatal("ambient channel not found after seed")
	}
	ambient, err := testHandler.insertChannelMessage(ctx, parseUUID(ambientChannelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "ordinary ambient work", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 0)
	if err != nil {
		t.Fatalf("insert ambient trigger: %v", err)
	}
	testHandler.dispatchChannelMessageToAgents(ctx, ch, ambient, parseUUID(testUserID))
	assertChannelAgentInboxEventCounts(t, ambientChannelID, agentID, 0, 1)
	assertChannelAgentWakeReasonPriority(t, ambientChannelID, agentID, ambient.ID, channelMessageWakeReason, channelMessageWakePriority)

	dmChannelID := seedAgentDMChannel(t, agentID)
	req := newRequest("POST", "/api/channels/"+dmChannelID+"/messages", map[string]string{"content": "hey, still direct"})
	req = withChatTestWorkspaceCtx(t, req)
	req = withURLParam(req, "channelId", dmChannelID)
	rec := httptest.NewRecorder()
	testHandler.SendChannelMessage(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("send dm: status=%d body=%s", rec.Code, rec.Body.String())
	}
	assertChannelAgentInboxEventCounts(t, dmChannelID, agentID, 0, 1)
}

// TestPrivateAgentDMChannelAllowsAnyChannelMemberPostBatch908 supersedes the
// old "private-agent DM channel rejects unauthorized member" regression: the
// member here is a genuine participant of this specific DM channel (seeded
// as a channel_member), so the only thing that used to deny them was the
// agent's own private-visibility gate — which task #908 retires. DM
// read/send is now unconditional for any member of the DM channel.
func TestPrivateAgentDMChannelAllowsAnyChannelMemberPostBatch908(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	agentID, _, memberID := privateAgentTestFixture(t)
	cleanupDMArtifacts(t)
	channelID := seedAgentDMChannelForUser(t, memberID, agentID)

	listReq := newRequestAs(memberID, http.MethodGet, "/api/channels/"+channelID+"/messages", nil)
	listReq = withChannelTestWorkspaceCtx(t, listReq, memberID)
	listReq = withURLParam(listReq, "channelId", channelID)
	listRec := httptest.NewRecorder()
	testHandler.ListChannelMessages(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list private-agent dm as channel member: expected 200 (unconditional post-#908), status=%d body=%s", listRec.Code, listRec.Body.String())
	}

	sendReq := newRequestAs(memberID, http.MethodPost, "/api/channels/"+channelID+"/messages", map[string]string{"content": "hello agent"})
	sendReq = withChannelTestWorkspaceCtx(t, sendReq, memberID)
	sendReq = withURLParam(sendReq, "channelId", channelID)
	sendRec := httptest.NewRecorder()
	testHandler.SendChannelMessage(sendRec, sendReq)
	if sendRec.Code != http.StatusCreated {
		t.Fatalf("send private-agent dm as channel member: expected 201 (unconditional post-#908), status=%d body=%s", sendRec.Code, sendRec.Body.String())
	}
}

// TestCanonicalMessageDeliverySkipsSelfAuthoredDM ensures an agent-authored DM
// cannot re-deliver to that agent (otherwise its replies would loop).
func TestCanonicalMessageDeliverySkipsSelfAuthoredDM(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	cleanupDMArtifacts(t)
	agentID := createHandlerTestAgent(t, "DM Guard Bot", nil)

	channelID := seedAgentDMChannel(t, agentID)

	ch, found := testHandler.getChannel(ctx, testWorkspaceID, parseUUID(channelID))
	if !found {
		t.Fatal("dm channel not found after seed")
	}
	trigger, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "agent", parseUUID(agentID), "DM Guard Bot", "my own reply", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr("g1"), 0)
	if err != nil {
		t.Fatalf("insert agent trigger: %v", err)
	}
	if recipients := testHandler.canonicalMessageDeliveryRecipients(ctx, ch, trigger); len(recipients) != 0 {
		t.Fatalf("self-authored DM recipients = %d, want 0", len(recipients))
	}
}

// TestCreateDMChannel_RollsBackOnMemberFailure locks the all-or-nothing
// transaction: if a channel_member insert fails (here, an invalid member_type
// trips the CHECK), the whole createDMChannel must roll back so no member-less
// dm channel survives. Guards against the helper silently regressing to a
// non-transactional write that would leave an invisible, unrecoverable dead DM.
func TestCreateDMChannel_RollsBackOnMemberFailure(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	cleanupDMArtifacts(t)
	canonical := "dm:test-rollback:" + testUserID

	rec := httptest.NewRecorder()
	_, ok := testHandler.createDMChannel(ctx, rec, testWorkspaceID, testUserID, canonical, []dmMember{
		{memberType: "user", memberID: parseUUID(testUserID)},
		{memberType: "bogus", memberID: parseUUID(testUserID)}, // violates channel_member CHECK → insert fails
	})
	if ok {
		t.Fatal("createDMChannel should return false when a member insert violates the CHECK constraint")
	}
	var channels int
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM channel WHERE workspace_id=$1 AND name=$2`, testWorkspaceID, canonical).Scan(&channels); err != nil {
		t.Fatalf("count channels: %v", err)
	}
	if channels != 0 {
		t.Fatalf("transaction not rolled back: %d channel row(s) for %q survived (member-less dead DM)", channels, canonical)
	}
	var members int
	testPool.QueryRow(ctx, `SELECT count(*) FROM channel_member cm JOIN channel ch ON ch.id=cm.channel_id WHERE ch.workspace_id=$1 AND ch.name=$2`, testWorkspaceID, canonical).Scan(&members)
	if members != 0 {
		t.Fatalf("transaction not rolled back: %d orphan member row(s) for %q survived", members, canonical)
	}
}

// TestListDMChannelsProjectsMentionReadModel locks the DM list to the same
// maintained mention counter used by the channel list. It guards the refresh
// hot-path optimization against losing mention badges.
func TestListDMChannelsProjectsMentionReadModel(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	cleanupDMArtifacts(t)
	agentID := createHandlerTestAgent(t, "DM Mention Counter Bot "+uuid.NewString()[:8], []byte("[]"))
	channelID := seedAgentDMChannel(t, agentID)
	parts := []protocol.MessagePart{{
		Type:       protocol.MessagePartTypeReference,
		RefType:    "mention",
		RefSubType: "member",
		RefID:      testUserID,
		Label:      "@Handler Test User",
	}}
	if _, err := testHandler.insertChannelMessageWithParts(
		ctx,
		parseUUID(channelID),
		parseUUID(testWorkspaceID),
		"agent",
		parseUUID(agentID),
		"DM Mention Counter Bot",
		"please review this",
		parts,
		"multica",
		nil,
		pgtype.UUID{},
		pgtype.UUID{},
		nil,
		0,
	); err != nil {
		t.Fatalf("insert mentioned DM message: %v", err)
	}

	beforeRead := listedDMItemForTest(t, channelID)
	if beforeRead == nil || !beforeRead.HasMention {
		t.Fatalf("DM mention badge missing before mark-read: %+v", beforeRead)
	}

	markChannelReadForTest(t, channelID, testUserID)
	afterRead := listedDMItemForTest(t, channelID)
	if afterRead == nil || afterRead.HasMention {
		t.Fatalf("DM mention badge not cleared after mark-read: %+v", afterRead)
	}
}
