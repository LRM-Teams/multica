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
		VALUES ($1,$2,'user',$3),($1,$2,'agent',$4)`, channelID, testWorkspaceID, userID, agentID); err != nil {
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
	var n int
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM channel WHERE workspace_id=$1 AND kind='dm'`, testWorkspaceID).Scan(&n); err != nil || n != 1 {
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
	var channelCount, sessionCount int
	testPool.QueryRow(ctx, `SELECT count(*) FROM channel WHERE workspace_id=$1 AND kind='dm'`, testWorkspaceID).Scan(&channelCount)
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

// TestSendChannelMessageDM_DispatchesAgent: a user message in an agent DM
// auto-dispatches to the agent without any @-mention (a channel_agent_session
// is ensured as the first step of the dispatch chain).
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
	var sessionID string
	if err := testPool.QueryRow(ctx, `SELECT chat_session_id FROM channel_agent_session WHERE channel_id=$1 AND agent_id=$2`, channelID, agentID).Scan(&sessionID); err != nil {
		t.Fatalf("DM did not auto-dispatch to the agent (no channel_agent_session): %v", err)
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
		VALUES ($1, $2, 'agent', $3)`, ambientChannelID, testWorkspaceID, agentID); err != nil {
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
	assertChannelAgentTaskPriorityCounts(t, ambientChannelID, agentID, 1, 0)

	dmChannelID := seedAgentDMChannel(t, agentID)
	req := newRequest("POST", "/api/channels/"+dmChannelID+"/messages", map[string]string{"content": "hey, still direct"})
	req = withChatTestWorkspaceCtx(t, req)
	req = withURLParam(req, "channelId", dmChannelID)
	rec := httptest.NewRecorder()
	testHandler.SendChannelMessage(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("send dm: status=%d body=%s", rec.Code, rec.Body.String())
	}
	assertChannelAgentTaskPriorityCounts(t, dmChannelID, agentID, 0, 1)
}

func TestPrivateAgentDMChannelRejectsUnauthorizedMember(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	agentID, _, memberID := privateAgentTestFixture(t)
	cleanupDMArtifacts(t)
	channelID := seedAgentDMChannelForUser(t, memberID, agentID)

	listReq := newRequestAs(memberID, http.MethodGet, "/api/channels/"+channelID+"/messages", nil)
	listReq = withChannelTestWorkspaceCtx(t, listReq, memberID)
	listReq = withURLParam(listReq, "channelId", channelID)
	listRec := httptest.NewRecorder()
	testHandler.ListChannelMessages(listRec, listReq)
	if listRec.Code != http.StatusForbidden {
		t.Fatalf("list private-agent dm as unauthorized member: status=%d body=%s", listRec.Code, listRec.Body.String())
	}

	sendReq := newRequestAs(memberID, http.MethodPost, "/api/channels/"+channelID+"/messages", map[string]string{"content": "hello private agent"})
	sendReq = withChannelTestWorkspaceCtx(t, sendReq, memberID)
	sendReq = withURLParam(sendReq, "channelId", channelID)
	sendRec := httptest.NewRecorder()
	testHandler.SendChannelMessage(sendRec, sendReq)
	if sendRec.Code != http.StatusForbidden {
		t.Fatalf("send private-agent dm as unauthorized member: status=%d body=%s", sendRec.Code, sendRec.Body.String())
	}

	var sessions int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM channel_agent_session
		WHERE channel_id = $1 AND agent_id = $2`, channelID, agentID).Scan(&sessions); err != nil {
		t.Fatalf("count channel agent sessions: %v", err)
	}
	if sessions != 0 {
		t.Fatalf("unauthorized private-agent dm dispatch created %d session(s)", sessions)
	}
}

// TestDMDispatch_SelfTriggerGuard: an agent-authored DM message must NOT
// re-dispatch to that same agent (otherwise an agent DM would loop on its own
// replies). The guard short-circuits before any session is ensured.
func TestDMDispatch_SelfTriggerGuard(t *testing.T) {
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
	testHandler.dispatchDMAgentReply(ctx, ch, trigger, parseUUID(testUserID))

	var n int
	testPool.QueryRow(ctx, `SELECT count(*) FROM channel_agent_session WHERE channel_id=$1`, channelID).Scan(&n)
	if n != 0 {
		t.Fatalf("self-trigger guard failed: agent's own message created %d sessions (want 0)", n)
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
