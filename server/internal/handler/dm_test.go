package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
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
		testPool.Exec(context.Background(), `DELETE FROM channel WHERE workspace_id=$1 AND kind='dm'`, testWorkspaceID)
		testPool.Exec(context.Background(), `DELETE FROM chat_session WHERE workspace_id=$1 AND creator_id=$2`, testWorkspaceID, testUserID)
	})
}

// TestCreateOrFindAgentDM_Idempotent: the first call creates a dm channel (201),
// a repeat call returns the same channel (200). The channel is kind='dm' with
// exactly the two members.
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
	if item2.ID != item1.ID {
		t.Fatalf("not idempotent: %s vs %s", item1.ID, item2.ID)
	}
	var kind string
	if err := testPool.QueryRow(ctx, `SELECT kind FROM channel WHERE id=$1`, item1.ID).Scan(&kind); err != nil || kind != "dm" {
		t.Fatalf("channel kind=%q err=%v, want dm", kind, err)
	}
	var n int
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM channel_member WHERE channel_id=$1`, item1.ID).Scan(&n); err != nil || n != 2 {
		t.Fatalf("member count=%d err=%v, want 2", n, err)
	}
}

// TestCreateOrFindAgentDM_ReusesLegacySession proves legacy-first: with an
// existing unbound chat_session for the agent, create-or-find returns that
// session (source=legacy_session) instead of spawning a parallel dm channel.
func TestCreateOrFindAgentDM_ReusesLegacySession(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	cleanupDMArtifacts(t)
	agentID := createHandlerTestAgent(t, "DM Legacy Bot", []byte("[]"))
	sessionID := seedLegacySession(t, agentID)

	rec, item := postCreateOrFindDM(t, "agent", agentID)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if item.Source != dmSourceLegacy || item.ID != sessionID {
		t.Fatalf("expected legacy reuse %s, got source=%q id=%s", sessionID, item.Source, item.ID)
	}
	var n int
	testPool.QueryRow(ctx, `SELECT count(*) FROM channel WHERE workspace_id=$1 AND kind='dm'`, testWorkspaceID).Scan(&n)
	if n != 0 {
		t.Fatalf("legacy-first must not create a dm channel, found %d", n)
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
	if err := testPool.QueryRow(ctx, `INSERT INTO "user" (name, email) VALUES ($1,$2) RETURNING id`, "DM Peer", "dm-peer-test@multica.ai").Scan(&peerUserID); err != nil {
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

// TestListDirectMessages_UnionAndDedup: /api/dm merges dm channels and unbound
// legacy sessions, and a peer present in both sources appears once.
func TestListDirectMessages_UnionAndDedup(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	cleanupDMArtifacts(t)
	agentA := createHandlerTestAgent(t, "DM List A", []byte("[]")) // dm channel only
	agentB := createHandlerTestAgent(t, "DM List B", []byte("[]")) // legacy session only
	agentC := createHandlerTestAgent(t, "DM List C", []byte("[]")) // both → dedup

	// agentA: dm channel via the handler.
	if rec, _ := postCreateOrFindDM(t, "agent", agentA); rec.Code != http.StatusCreated {
		t.Fatalf("seed agentA dm channel: status=%d", rec.Code)
	}
	// agentB: legacy session only.
	seedLegacySession(t, agentB)
	// agentC: dm channel FIRST (no legacy yet → handler creates it), THEN legacy → both coexist.
	if rec, _ := postCreateOrFindDM(t, "agent", agentC); rec.Code != http.StatusCreated {
		t.Fatalf("seed agentC dm channel: status=%d", rec.Code)
	}
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
	if source[agentB] != dmSourceLegacy {
		t.Fatalf("agentB source=%q, want legacy_session", source[agentB])
	}
	if count[agentC] != 1 {
		t.Fatalf("agentC should be deduped to 1 entry, got %d", count[agentC])
	}
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

	var channelID string
	canonical := dmCanonicalName("user", testUserID, "agent", agentID)
	if err := testPool.QueryRow(ctx, `
		INSERT INTO channel (workspace_id, name, created_by, kind)
		VALUES ($1,$2,$3,'dm') RETURNING id`, testWorkspaceID, canonical, testUserID).Scan(&channelID); err != nil {
		t.Fatalf("seed dm channel: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1,$2,'user',$3),($1,$2,'agent',$4)`, channelID, testWorkspaceID, testUserID, agentID); err != nil {
		t.Fatalf("seed members: %v", err)
	}

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

	var channelID string
	canonical := dmCanonicalName("user", testUserID, "agent", agentID)
	if err := testPool.QueryRow(ctx, `
		INSERT INTO channel (workspace_id, name, created_by, kind)
		VALUES ($1,$2,$3,'dm') RETURNING id`, testWorkspaceID, canonical, testUserID).Scan(&channelID); err != nil {
		t.Fatalf("seed dm channel: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1,$2,'user',$3),($1,$2,'agent',$4)`, channelID, testWorkspaceID, testUserID, agentID); err != nil {
		t.Fatalf("seed members: %v", err)
	}

	ch, found := testHandler.getChannel(ctx, testWorkspaceID, parseUUID(channelID))
	if !found {
		t.Fatal("dm channel not found after seed")
	}
	trigger, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "agent", parseUUID(agentID), "DM Guard Bot", "my own reply", "multica", nil, strPtr("g1"), 0)
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
