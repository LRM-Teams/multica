package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestAgentDMControlActionsAreTruthfulForEffectiveState(t *testing.T) {
	tests := []struct {
		name           string
		state          string
		exchangeExists bool
		ownerPaused    bool
		want           []string
	}{
		{
			name:           "active",
			state:          "active",
			exchangeExists: true,
			want:           []string{"view_dm", "pause_pair", "pause_global"},
		},
		{
			name:           "budget needs more rounds",
			state:          "paused_budget",
			exchangeExists: true,
			want:           []string{"view_dm", "grant_rounds", "pause_pair", "pause_global"},
		},
		{
			name:           "frequency needs pair resume",
			state:          "paused_frequency",
			exchangeExists: true,
			want:           []string{"view_dm", "resume_pair", "pause_global"},
		},
		{
			name:           "manual pair pause needs pair resume",
			state:          "paused_pair",
			exchangeExists: true,
			want:           []string{"view_dm", "resume_pair", "pause_global"},
		},
		{
			name:           "this owner global pause needs global resume",
			state:          "paused_global",
			exchangeExists: true,
			ownerPaused:    true,
			want:           []string{"view_dm", "resume_global"},
		},
		{
			name:           "other owner global pause is not recoverable by viewer",
			state:          "paused_global",
			exchangeExists: true,
			want:           []string{"view_dm", "pause_global"},
		},
		{
			name: "no exchange cannot grant",
			want: []string{"view_dm", "pause_pair", "pause_global"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := agentDMControlActions(tt.state, tt.exchangeExists, tt.ownerPaused)
			if !slices.Equal(got, tt.want) {
				t.Fatalf("agentDMControlActions(%q, %t, %t) = %v, want %v",
					tt.state, tt.exchangeExists, tt.ownerPaused, got, tt.want)
			}
		})
	}
}

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

func TestAgentDMSupervisionListReadOnlyAndOwnerControls(t *testing.T) {
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
		testPool.Exec(ctx, `
			DELETE FROM agent_dm_owner_control
			WHERE workspace_id = $1 AND owner_id = $2`,
			testWorkspaceID, testUserID)
	})

	lowID, highID, ok := normalizedAgentDMPair(parseUUID(firstAgentID), parseUUID(secondAgentID))
	if !ok {
		t.Fatal("normalize supervised A2A pair failed")
	}
	var exchangeID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_dm_exchange (
		  workspace_id, channel_id, agent_low_id, agent_high_id, matter_id,
		  turn_count, state, pause_reason
		)
		VALUES ($1, $2, $3, $4, gen_random_uuid(), 6, 'paused_budget', 'round limit')
		RETURNING id`,
		testWorkspaceID, channel.ID, lowID, highID).Scan(&exchangeID); err != nil {
		t.Fatalf("seed supervised A2A exchange: %v", err)
	}
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
	if supervised.Mode != "agent_pair" || !supervised.Supervised ||
		len(supervised.Participants) != 2 || supervised.AgentDMControl == nil {
		t.Fatalf("unexpected supervised DM item: %+v", supervised)
	}
	if supervised.AgentDMControl.ExchangeID == nil || *supervised.AgentDMControl.ExchangeID != exchangeID {
		t.Fatalf("supervised control exchange=%v, want %s", supervised.AgentDMControl.ExchangeID, exchangeID)
	}
	if want := []string{"view_dm", "grant_rounds", "pause_pair", "pause_global"}; !slices.Equal(supervised.AgentDMControl.Actions, want) {
		t.Fatalf("paused-budget actions=%v, want %v", supervised.AgentDMControl.Actions, want)
	}
	if !supervised.AgentDMControl.CanGrantRounds || !supervised.AgentDMControl.CanPausePair ||
		!supervised.AgentDMControl.CanPauseGlobal {
		t.Fatalf("paused-budget capabilities=%+v", supervised.AgentDMControl)
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
	if len(page.Messages) != 1 || page.A2AControl == nil || page.A2AControl.State != "paused_budget" {
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

	controlReq := withURLParam(
		withChatTestWorkspaceCtx(
			t,
			newRequestAs(testUserID, http.MethodPost, "/api/dm/channels/"+channel.ID+"/a2a-control", map[string]any{
				"action":      "grant_rounds",
				"exchange_id": exchangeID,
				"rounds":      2,
			}),
		),
		"channelId",
		channel.ID,
	)
	controlRec := httptest.NewRecorder()
	testHandler.UpdateAgentDMControl(controlRec, controlReq)
	if controlRec.Code != http.StatusOK {
		t.Fatalf("owner grant A2A rounds: status=%d body=%s", controlRec.Code, controlRec.Body.String())
	}
	var control AgentDMControlResponse
	if err := json.Unmarshal(controlRec.Body.Bytes(), &control); err != nil {
		t.Fatalf("decode owner A2A control: %v", err)
	}
	if control.State != "active" || control.RoundLimit != agentDMDefaultRoundLimit+2 {
		t.Fatalf("control after grant=%+v", control)
	}
	if want := []string{"view_dm", "pause_pair", "pause_global"}; !slices.Equal(control.Actions, want) {
		t.Fatalf("actions after grant=%v, want %v", control.Actions, want)
	}
	pausePairReq := withURLParam(
		withChatTestWorkspaceCtx(
			t,
			newRequestAs(testUserID, http.MethodPost, "/api/dm/channels/"+channel.ID+"/a2a-control", map[string]any{
				"action": "pause_pair",
			}),
		),
		"channelId",
		channel.ID,
	)
	pausePairRec := httptest.NewRecorder()
	testHandler.UpdateAgentDMControl(pausePairRec, pausePairReq)
	if pausePairRec.Code != http.StatusOK {
		t.Fatalf("owner pause A2A pair: status=%d body=%s", pausePairRec.Code, pausePairRec.Body.String())
	}
	if err := json.Unmarshal(pausePairRec.Body.Bytes(), &control); err != nil {
		t.Fatalf("decode pair-pause control: %v", err)
	}
	if control.State != "paused_pair" {
		t.Fatalf("control after pair pause=%+v", control)
	}
	if want := []string{"view_dm", "resume_pair", "pause_global"}; !slices.Equal(control.Actions, want) {
		t.Fatalf("actions after pair pause=%v, want %v", control.Actions, want)
	}
	newResumePairReq := func() *http.Request {
		return withURLParam(
			withChatTestWorkspaceCtx(
				t,
				newRequestAs(testUserID, http.MethodPost, "/api/dm/channels/"+channel.ID+"/a2a-control", map[string]any{
					"action": "resume_pair",
				}),
			),
			"channelId",
			channel.ID,
		)
	}
	resumePairRec := httptest.NewRecorder()
	testHandler.UpdateAgentDMControl(resumePairRec, newResumePairReq())
	if resumePairRec.Code != http.StatusOK {
		t.Fatalf("owner resume A2A pair: status=%d body=%s", resumePairRec.Code, resumePairRec.Body.String())
	}
	if err := json.Unmarshal(resumePairRec.Body.Bytes(), &control); err != nil {
		t.Fatalf("decode pair-resume control: %v", err)
	}
	if control.State != "active" {
		t.Fatalf("control after pair resume=%+v", control)
	}
	if _, err := testPool.Exec(ctx, `
		UPDATE agent_dm_pair_control
		SET state = 'paused_frequency', pause_reason = 'frequency limit'
		WHERE workspace_id = $1 AND agent_low_id = $2 AND agent_high_id = $3`,
		testWorkspaceID, lowID, highID); err != nil {
		t.Fatalf("seed frequency pair pause: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		UPDATE agent_dm_exchange
		SET state = 'paused_frequency', pause_reason = 'frequency limit'
		WHERE id = $1`,
		exchangeID); err != nil {
		t.Fatalf("seed frequency exchange pause: %v", err)
	}
	frequencyControl, ok := testHandler.agentDMControlForOwner(
		ctx, parseUUID(testWorkspaceID), parseUUID(channel.ID), parseUUID(testUserID),
	)
	if !ok || frequencyControl.State != "paused_frequency" {
		t.Fatalf("frequency control=%+v ok=%t", frequencyControl, ok)
	}
	if want := []string{"view_dm", "resume_pair", "pause_global"}; !slices.Equal(frequencyControl.Actions, want) {
		t.Fatalf("frequency actions=%v, want %v", frequencyControl.Actions, want)
	}
	frequencyResumeRec := httptest.NewRecorder()
	testHandler.UpdateAgentDMControl(frequencyResumeRec, newResumePairReq())
	if frequencyResumeRec.Code != http.StatusOK {
		t.Fatalf("owner resume frequency-paused pair: status=%d body=%s",
			frequencyResumeRec.Code, frequencyResumeRec.Body.String())
	}
	if err := json.Unmarshal(frequencyResumeRec.Body.Bytes(), &control); err != nil {
		t.Fatalf("decode frequency-resume control: %v", err)
	}
	if control.State != "active" {
		t.Fatalf("control after frequency resume=%+v", control)
	}
	globalReq := withChatTestWorkspaceCtx(
		t,
		newRequestAs(testUserID, http.MethodPost, "/api/dm/a2a-control", map[string]any{
			"action": "pause_global",
		}),
	)
	globalRec := httptest.NewRecorder()
	testHandler.UpdateAgentDMGlobalControl(globalRec, globalReq)
	if globalRec.Code != http.StatusOK {
		t.Fatalf("owner pause global A2A: status=%d body=%s", globalRec.Code, globalRec.Body.String())
	}
	var globalControl AgentDMGlobalControlResponse
	if err := json.Unmarshal(globalRec.Body.Bytes(), &globalControl); err != nil {
		t.Fatalf("decode global A2A control: %v", err)
	}
	if !globalControl.Paused || globalControl.State != "paused_global" {
		t.Fatalf("global A2A control=%+v", globalControl)
	}
	globalPairControl, ok := testHandler.agentDMControlForOwner(
		ctx, parseUUID(testWorkspaceID), parseUUID(channel.ID), parseUUID(testUserID),
	)
	if !ok || globalPairControl.State != "paused_global" {
		t.Fatalf("owner-global pair control=%+v ok=%t", globalPairControl, ok)
	}
	if want := []string{"view_dm", "resume_global"}; !slices.Equal(globalPairControl.Actions, want) {
		t.Fatalf("owner-global actions=%v, want %v", globalPairControl.Actions, want)
	}
	resumeGlobalReq := withChatTestWorkspaceCtx(
		t,
		newRequestAs(testUserID, http.MethodPost, "/api/dm/a2a-control", map[string]any{
			"action": "resume_global",
		}),
	)
	resumeGlobalRec := httptest.NewRecorder()
	testHandler.UpdateAgentDMGlobalControl(resumeGlobalRec, resumeGlobalReq)
	if resumeGlobalRec.Code != http.StatusOK {
		t.Fatalf("owner resume global A2A: status=%d body=%s", resumeGlobalRec.Code, resumeGlobalRec.Body.String())
	}
	var controlEvents []string
	rows, err := testPool.Query(ctx, `
		SELECT parts->0->>'event'
		FROM channel_message
		WHERE channel_id = $1
		  AND author_type = 'system'
		  AND parts->0->>'event' IN ($2, $3, $4)
		ORDER BY seq`,
		channel.ID,
		agentDMSystemEventPausedPair,
		agentDMSystemEventPausedGlobal,
		agentDMSystemEventResumed)
	if err != nil {
		t.Fatalf("load global A2A control system events: %v", err)
	}
	for rows.Next() {
		var event string
		if err := rows.Scan(&event); err != nil {
			rows.Close()
			t.Fatalf("scan global A2A control system event: %v", err)
		}
		controlEvents = append(controlEvents, event)
	}
	rows.Close()
	wantControlEvents := []string{
		agentDMSystemEventPausedPair,
		agentDMSystemEventResumed,
		agentDMSystemEventResumed,
		agentDMSystemEventPausedGlobal,
		agentDMSystemEventResumed,
	}
	if !slices.Equal(controlEvents, wantControlEvents) {
		t.Fatalf("A2A control system events=%v, want pair pause/resume, frequency resume, then global pause/resume", controlEvents)
	}
	var pausedGlobalContent, resumedContent string
	if err := testPool.QueryRow(ctx, `
		SELECT content
		FROM channel_message
		WHERE channel_id = $1
		  AND parts->0->>'event' = $2
		ORDER BY seq DESC
		LIMIT 1`,
		channel.ID, agentDMSystemEventPausedGlobal).Scan(&pausedGlobalContent); err != nil {
		t.Fatalf("load owner-scoped global-pause copy: %v", err)
	}
	if pausedGlobalContent != "你暂停了涉及你智能体的所有私聊——它们暂时不再和任何智能体互发，直到你恢复。" {
		t.Fatalf("global-pause copy=%q", pausedGlobalContent)
	}
	if err := testPool.QueryRow(ctx, `
		SELECT content
		FROM channel_message
		WHERE channel_id = $1
		  AND parts->0->>'event' = $2
		ORDER BY seq DESC
		LIMIT 1`,
		channel.ID, agentDMSystemEventResumed).Scan(&resumedContent); err != nil {
		t.Fatalf("load owner-scoped resume copy: %v", err)
	}
	if resumedContent != "已恢复，你的智能体可以继续私聊了。" {
		t.Fatalf("resume copy=%q", resumedContent)
	}
	repeatResumeReq := withChatTestWorkspaceCtx(
		t,
		newRequestAs(testUserID, http.MethodPost, "/api/dm/a2a-control", map[string]any{
			"action": "resume_global",
		}),
	)
	repeatResumeRec := httptest.NewRecorder()
	testHandler.UpdateAgentDMGlobalControl(repeatResumeRec, repeatResumeReq)
	if repeatResumeRec.Code != http.StatusOK {
		t.Fatalf("repeat owner resume global A2A: status=%d body=%s", repeatResumeRec.Code, repeatResumeRec.Body.String())
	}
	var resumedCount int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM channel_message
		WHERE channel_id = $1
		  AND author_type = 'system'
		  AND parts->0->>'event' = $2`,
		channel.ID, agentDMSystemEventResumed).Scan(&resumedCount); err != nil {
		t.Fatalf("count resumed A2A system events: %v", err)
	}
	if resumedCount != 3 {
		t.Fatalf("resumed A2A system events=%d, want 3 after pair/frequency/global resumes and repeated global resume no-op", resumedCount)
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
	otherGlobalReq := withChatTestWorkspaceCtx(
		t,
		newRequestAs(otherUserID, http.MethodGet, "/api/dm/a2a-control", nil),
	)
	otherGlobalRec := httptest.NewRecorder()
	testHandler.GetAgentDMGlobalControl(otherGlobalRec, otherGlobalReq)
	if otherGlobalRec.Code != http.StatusForbidden {
		t.Fatalf("non-owner global A2A control: status=%d body=%s", otherGlobalRec.Code, otherGlobalRec.Body.String())
	}
}

func TestAgentDMGlobalPauseIsScopedToPairsInvolvingOwnedAgents(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	otherOwnerID := seedWorkspaceUserForTransportTargetTest(
		t,
		"a2a-second-owner-"+uuid.NewString()[:8],
	)
	firstOwnerAgentID := createHandlerTestAgent(t, "First Owner Agent "+uuid.NewString(), []byte("[]"))
	otherOwnerAgentAID := createHandlerTestAgent(t, "Other Owner Agent A "+uuid.NewString(), []byte("[]"))
	otherOwnerAgentBID := createHandlerTestAgent(t, "Other Owner Agent B "+uuid.NewString(), []byte("[]"))
	if _, err := testPool.Exec(ctx, `
		UPDATE agent
		SET owner_id = $1
		WHERE id = ANY($2::uuid[])`,
		otherOwnerID,
		[]pgtype.UUID{parseUUID(otherOwnerAgentAID), parseUUID(otherOwnerAgentBID)},
	); err != nil {
		t.Fatalf("assign second owner's agents: %v", err)
	}

	type pairFixture struct {
		channelID  string
		exchangeID string
	}
	createPair := func(agentAID, agentBID string) pairFixture {
		t.Helper()
		channel, created := testHandler.createDMChannel(
			ctx,
			nil,
			testWorkspaceID,
			testUserID,
			dmCanonicalName("agent", agentAID, "agent", agentBID),
			[]dmMember{
				{memberType: "agent", memberID: parseUUID(agentAID)},
				{memberType: "agent", memberID: parseUUID(agentBID)},
			},
		)
		if !created {
			t.Fatalf("create supervised pair %s/%s failed", agentAID, agentBID)
		}
		lowID, highID, ok := normalizedAgentDMPair(parseUUID(agentAID), parseUUID(agentBID))
		if !ok {
			t.Fatalf("normalize supervised pair %s/%s failed", agentAID, agentBID)
		}
		var exchangeID string
		if err := testPool.QueryRow(ctx, `
			INSERT INTO agent_dm_exchange (
			  workspace_id, channel_id, agent_low_id, agent_high_id, matter_id
			)
			VALUES ($1, $2, $3, $4, gen_random_uuid())
			RETURNING id`,
			testWorkspaceID, channel.ID, lowID, highID,
		).Scan(&exchangeID); err != nil {
			t.Fatalf("seed supervised exchange: %v", err)
		}
		return pairFixture{channelID: channel.ID, exchangeID: exchangeID}
	}

	mixedOwners := createPair(firstOwnerAgentID, otherOwnerAgentAID)
	otherOwnerOnly := createPair(otherOwnerAgentAID, otherOwnerAgentBID)
	t.Cleanup(func() {
		testPool.Exec(ctx, `
			DELETE FROM agent_dm_owner_control
			WHERE workspace_id = $1 AND owner_id IN ($2, $3)`,
			testWorkspaceID, testUserID, otherOwnerID)
		testPool.Exec(ctx, `DELETE FROM channel WHERE id IN ($1, $2)`,
			mixedOwners.channelID, otherOwnerOnly.channelID)
	})

	changed, err := testHandler.updateAgentDMOwnerGlobalControl(
		ctx,
		parseUUID(testWorkspaceID),
		parseUUID(testUserID),
		true,
	)
	if err != nil || !changed {
		t.Fatalf("pause first owner's A2A: changed=%t err=%v", changed, err)
	}
	var mixedState, otherOnlyState string
	if err := testPool.QueryRow(ctx, `
		SELECT
		  (SELECT state FROM agent_dm_exchange WHERE id = $1),
		  (SELECT state FROM agent_dm_exchange WHERE id = $2)`,
		mixedOwners.exchangeID, otherOwnerOnly.exchangeID,
	).Scan(&mixedState, &otherOnlyState); err != nil {
		t.Fatalf("load exchange states after owner pause: %v", err)
	}
	if mixedState != "paused_global" {
		t.Fatalf("mixed-owner exchange state=%q, want paused_global", mixedState)
	}
	if otherOnlyState != "active" {
		t.Fatalf("other-owner-only exchange state=%q, want active", otherOnlyState)
	}
	if control := testHandler.agentDMGlobalControl(
		ctx,
		parseUUID(testWorkspaceID),
		parseUUID(otherOwnerID),
	); control.Paused || control.State != "active" {
		t.Fatalf("other owner's independent global control=%+v, want active", control)
	}
	otherView, ok := testHandler.agentDMControlForOwner(
		ctx,
		parseUUID(testWorkspaceID),
		parseUUID(mixedOwners.channelID),
		parseUUID(otherOwnerID),
	)
	if !ok || otherView.State != "paused_global" {
		t.Fatalf("other owner mixed-pair control=%+v ok=%t", otherView, ok)
	}
	if want := []string{"view_dm", "pause_global"}; !slices.Equal(otherView.Actions, want) {
		t.Fatalf("other owner mixed-pair actions=%v, want %v", otherView.Actions, want)
	}

	changed, err = testHandler.updateAgentDMOwnerGlobalControl(
		ctx,
		parseUUID(testWorkspaceID),
		parseUUID(otherOwnerID),
		false,
	)
	if err != nil || changed {
		t.Fatalf("other owner no-op resume: changed=%t err=%v", changed, err)
	}
	if err := testPool.QueryRow(ctx, `
		SELECT state
		FROM agent_dm_exchange
		WHERE id = $1`,
		mixedOwners.exchangeID,
	).Scan(&mixedState); err != nil {
		t.Fatalf("load mixed exchange after other owner resume: %v", err)
	}
	if mixedState != "paused_global" {
		t.Fatalf("other owner resumed first owner's pause: state=%q", mixedState)
	}

	changed, err = testHandler.updateAgentDMOwnerGlobalControl(
		ctx,
		parseUUID(testWorkspaceID),
		parseUUID(testUserID),
		false,
	)
	if err != nil || !changed {
		t.Fatalf("resume first owner's A2A: changed=%t err=%v", changed, err)
	}
	if err := testPool.QueryRow(ctx, `
		SELECT state
		FROM agent_dm_exchange
		WHERE id = $1`,
		mixedOwners.exchangeID,
	).Scan(&mixedState); err != nil {
		t.Fatalf("load mixed exchange after owning resume: %v", err)
	}
	if mixedState != "active" {
		t.Fatalf("mixed-owner exchange state after owning resume=%q, want active", mixedState)
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
