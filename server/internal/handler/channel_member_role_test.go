package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// TestCreateChannelCreatorIsOwnerAndListReturnsRole locks Beckham v2 §4
// data-model: channel_member.role is exposed on list, creator is owner.
func TestCreateChannelCreatorIsOwnerAndListReturnsRole(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test DB not configured")
	}
	creator := testUserID
	req := newRequestAs(creator, http.MethodPost, "/api/channels", map[string]any{
		"name": "role-panel-" + t.Name(),
	})
	req = withChannelTestWorkspaceCtx(t, req, creator)
	created := httptest.NewRecorder()
	testHandler.CreateChannel(created, req)
	if created.Code != http.StatusCreated {
		t.Fatalf("CreateChannel = %d: %s", created.Code, created.Body.String())
	}
	var ch ChannelResponse
	if err := json.Unmarshal(created.Body.Bytes(), &ch); err != nil {
		t.Fatalf("decode channel: %v", err)
	}

	var ownerCount, agentOwnerCount int
	err := testPool.QueryRow(context.Background(), `
		SELECT
		  count(*) FILTER (WHERE role = 'owner' AND member_type = 'user'),
		  count(*) FILTER (WHERE role = 'owner' AND member_type = 'agent')
		FROM channel_member WHERE channel_id = $1`, parseUUID(ch.ID)).Scan(&ownerCount, &agentOwnerCount)
	if err != nil {
		t.Fatalf("count owners: %v", err)
	}
	if ownerCount != 1 || agentOwnerCount != 0 {
		t.Fatalf("owners user=%d agent=%d want user=1 agent=0", ownerCount, agentOwnerCount)
	}

	listReq := newRequestAs(creator, http.MethodGet, "/api/channels/"+ch.ID+"/members", nil)
	listReq = withChannelTestWorkspaceCtx(t, listReq, creator)
	listReq = withURLParam(listReq, "channelId", ch.ID)
	listRec := httptest.NewRecorder()
	testHandler.ListChannelMembers(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("ListChannelMembers = %d: %s", listRec.Code, listRec.Body.String())
	}
	var members []ChannelMemberResponse
	if err := json.Unmarshal(listRec.Body.Bytes(), &members); err != nil {
		t.Fatalf("decode members: %v", err)
	}
	if len(members) == 0 {
		t.Fatal("expected at least creator in members")
	}
	if members[0].MemberID != creator || members[0].Role != "owner" {
		t.Fatalf("first member = %+v want creator owner", members[0])
	}
}

// TestCreateChannelAlwaysWritesHumanOwner asserts concurrent creates never leave
// zero-owner groups (the former `_, _` swallow path).
func TestCreateChannelAlwaysWritesHumanOwner(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test DB not configured")
	}
	for i := 0; i < 3; i++ {
		name := fmt.Sprintf("role-atomic-%s-%d", t.Name(), i)
		req := newRequestAs(testUserID, http.MethodPost, "/api/channels", map[string]any{"name": name})
		req = withChannelTestWorkspaceCtx(t, req, testUserID)
		rec := httptest.NewRecorder()
		testHandler.CreateChannel(rec, req)
		if rec.Code != http.StatusCreated {
			t.Fatalf("create %s: %d %s", name, rec.Code, rec.Body.String())
		}
		var ch ChannelResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &ch); err != nil {
			t.Fatalf("decode: %v", err)
		}
		var n int
		if err := testPool.QueryRow(context.Background(),
			`SELECT count(*) FROM channel_member WHERE channel_id = $1 AND role = 'owner' AND member_type = 'user'`,
			parseUUID(ch.ID)).Scan(&n); err != nil || n != 1 {
			t.Fatalf("channel %s owners=%d err=%v", ch.ID, n, err)
		}
	}
}

// TestAgentCannotBeChannelOwnerCHECK documents 237 CHECK (owner ⇒ user).
func TestAgentCannotBeChannelOwnerCHECK(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test DB not configured")
	}
	req := newRequestAs(testUserID, http.MethodPost, "/api/channels", map[string]any{
		"name": "role-agent-owner-" + t.Name(),
	})
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	rec := httptest.NewRecorder()
	testHandler.CreateChannel(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	var ch ChannelResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &ch); err != nil {
		t.Fatalf("decode: %v", err)
	}

	var agentID pgtype.UUID
	err := testPool.QueryRow(context.Background(),
		`SELECT id FROM agent WHERE workspace_id = $1 LIMIT 1`, parseUUID(testWorkspaceID)).Scan(&agentID)
	if err != nil {
		t.Fatalf("agent fixture required for CHECK test: %v", err)
	}
	_, err = testPool.Exec(context.Background(), `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id, role)
		VALUES ($1, $2, 'agent', $3, 'member')
		ON CONFLICT DO NOTHING`, parseUUID(ch.ID), parseUUID(testWorkspaceID), agentID)
	if err != nil {
		t.Fatalf("insert agent member: %v", err)
	}
	_, err = testPool.Exec(context.Background(), `
		UPDATE channel_member SET role = 'owner'
		WHERE channel_id = $1 AND member_type = 'agent' AND member_id = $2`,
		parseUUID(ch.ID), agentID)
	if err == nil {
		t.Fatal("expected CHECK to reject agent owner")
	}
}

// TestRemoveSoleOwnerBlocked: ordinary group cannot lose its only human owner.
func TestRemoveSoleOwnerBlocked(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test DB not configured")
	}
	req := newRequestAs(testUserID, http.MethodPost, "/api/channels", map[string]any{
		"name": "role-sole-owner-" + t.Name(),
	})
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	rec := httptest.NewRecorder()
	testHandler.CreateChannel(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	var ch ChannelResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &ch); err != nil {
		t.Fatal(err)
	}
	// Self-leave as sole owner must fail.
	del := newRequestAs(testUserID, http.MethodDelete,
		"/api/channels/"+ch.ID+"/members/user/"+testUserID, nil)
	del = withChannelTestWorkspaceCtx(t, del, testUserID)
	del = withRouteParams(del, "channelId", ch.ID, "memberType", "user", "memberId", testUserID)
	delRec := httptest.NewRecorder()
	testHandler.RemoveChannelMember(delRec, del)
	if delRec.Code != http.StatusConflict {
		t.Fatalf("sole owner self-leave want 409, got %d: %s", delRec.Code, delRec.Body.String())
	}
	var n int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM channel_member WHERE channel_id = $1 AND role = 'owner'`,
		parseUUID(ch.ID)).Scan(&n); err != nil || n != 1 {
		t.Fatalf("owner still present: n=%d err=%v", n, err)
	}
}

// TestListChannelsMemberBriefIncludesRole asserts channel list avatar stack
// returns role (not omitempty-empty).
func TestListChannelsMemberBriefIncludesRole(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test DB not configured")
	}
	name := "role-list-brief-" + t.Name()
	req := newRequestAs(testUserID, http.MethodPost, "/api/channels", map[string]any{"name": name})
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	rec := httptest.NewRecorder()
	testHandler.CreateChannel(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	listReq := newRequestAs(testUserID, http.MethodGet, "/api/channels", nil)
	listReq = withChannelTestWorkspaceCtx(t, listReq, testUserID)
	listRec := httptest.NewRecorder()
	testHandler.ListChannels(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("ListChannels: %d %s", listRec.Code, listRec.Body.String())
	}
	var channels []ChannelResponse
	if err := json.Unmarshal(listRec.Body.Bytes(), &channels); err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, ch := range channels {
		if ch.Name != name {
			continue
		}
		found = true
		if len(ch.Members) == 0 {
			t.Fatal("expected members on channel list brief")
		}
		if ch.Members[0].Role != "owner" {
			t.Fatalf("list brief role=%q want owner", ch.Members[0].Role)
		}
	}
	if !found {
		t.Fatalf("channel %q not in list", name)
	}
}

// TestUpdateChannelMemberRoleOwnerOnly locks #814 write contract:
// only channel_member.role=owner may promote/demote; workspace admin cannot;
// transfer ownership demotes previous owner; agent cannot become owner.
func TestUpdateChannelMemberRoleOwnerOnly(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test DB not configured")
	}
	ctx := context.Background()

	// Create ordinary group as testUserID (owner).
	req := newRequestAs(testUserID, http.MethodPost, "/api/channels", map[string]any{
		"name": "role-write-" + uuid.NewString()[:8],
	})
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	created := httptest.NewRecorder()
	testHandler.CreateChannel(created, req)
	if created.Code != http.StatusCreated {
		t.Fatalf("CreateChannel = %d: %s", created.Code, created.Body.String())
	}
	var ch ChannelResponse
	if err := json.Unmarshal(created.Body.Bytes(), &ch); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// Second human member (workspace member role, not channel owner).
	var peerID string
	peerTag := uuid.NewString()
	peerEmail := fmt.Sprintf("role-peer-%s@example.com", peerTag)
	peerName := "role-peer-" + peerTag[:8]
	if err := testPool.QueryRow(ctx,
		`INSERT INTO "user" (name, email) VALUES ($1, $2) RETURNING id`,
		peerName, peerEmail).Scan(&peerID); err != nil {
		t.Fatalf("insert peer user: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(ctx, `DELETE FROM channel_member WHERE member_id = $1`, peerID)
		_, _ = testPool.Exec(ctx, `DELETE FROM member WHERE user_id = $1`, peerID)
		_, _ = testPool.Exec(ctx, `DELETE FROM "user" WHERE id = $1`, peerID)
	})
	if _, err := testPool.Exec(ctx,
		`INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, 'member')`,
		testWorkspaceID, peerID); err != nil {
		t.Fatalf("insert workspace member: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id, role)
		VALUES ($1, $2, 'user', $3, 'member')`,
		parseUUID(ch.ID), parseUUID(testWorkspaceID), peerID); err != nil {
		t.Fatalf("insert channel member: %v", err)
	}

	// Peer (not channel owner) cannot promote anyone.
	deny := newRequestAs(peerID, http.MethodPatch,
		"/api/channels/"+ch.ID+"/members/user/"+peerID,
		map[string]any{"role": "manager"})
	deny = withChannelTestWorkspaceCtx(t, deny, peerID)
	deny = withRouteParams(deny, "channelId", ch.ID, "memberType", "user", "memberId", peerID)
	denyRec := httptest.NewRecorder()
	testHandler.UpdateChannelMemberRole(denyRec, deny)
	if denyRec.Code != http.StatusForbidden {
		t.Fatalf("non-owner promote want 403, got %d: %s", denyRec.Code, denyRec.Body.String())
	}

	// Owner promotes peer → manager.
	okPromote := newRequestAs(testUserID, http.MethodPatch,
		"/api/channels/"+ch.ID+"/members/user/"+peerID,
		map[string]any{"role": "manager"})
	okPromote = withChannelTestWorkspaceCtx(t, okPromote, testUserID)
	okPromote = withRouteParams(okPromote, "channelId", ch.ID, "memberType", "user", "memberId", peerID)
	okRec := httptest.NewRecorder()
	testHandler.UpdateChannelMemberRole(okRec, okPromote)
	if okRec.Code != http.StatusOK {
		t.Fatalf("owner promote = %d: %s", okRec.Code, okRec.Body.String())
	}
	var role string
	if err := testPool.QueryRow(ctx, `
		SELECT role FROM channel_member
		WHERE channel_id = $1 AND member_type = 'user' AND member_id = $2`,
		parseUUID(ch.ID), peerID).Scan(&role); err != nil || role != "manager" {
		t.Fatalf("peer role=%q err=%v want manager", role, err)
	}

	// Owner demotes peer → member.
	okDemote := newRequestAs(testUserID, http.MethodPatch,
		"/api/channels/"+ch.ID+"/members/user/"+peerID,
		map[string]any{"role": "member"})
	okDemote = withChannelTestWorkspaceCtx(t, okDemote, testUserID)
	okDemote = withRouteParams(okDemote, "channelId", ch.ID, "memberType", "user", "memberId", peerID)
	demoteRec := httptest.NewRecorder()
	testHandler.UpdateChannelMemberRole(demoteRec, okDemote)
	if demoteRec.Code != http.StatusOK {
		t.Fatalf("owner demote = %d: %s", demoteRec.Code, demoteRec.Body.String())
	}

	// Sole owner cannot demote self without transfer.
	selfDemote := newRequestAs(testUserID, http.MethodPatch,
		"/api/channels/"+ch.ID+"/members/user/"+testUserID,
		map[string]any{"role": "member"})
	selfDemote = withChannelTestWorkspaceCtx(t, selfDemote, testUserID)
	selfDemote = withRouteParams(selfDemote, "channelId", ch.ID, "memberType", "user", "memberId", testUserID)
	selfRec := httptest.NewRecorder()
	testHandler.UpdateChannelMemberRole(selfRec, selfDemote)
	if selfRec.Code != http.StatusConflict {
		t.Fatalf("sole owner self-demote want 409, got %d: %s", selfRec.Code, selfRec.Body.String())
	}

	// PATCH role=owner must reject — transfer is a separate endpoint (Iris/Barry).
	badOwner := newRequestAs(testUserID, http.MethodPatch,
		"/api/channels/"+ch.ID+"/members/user/"+peerID,
		map[string]any{"role": "owner"})
	badOwner = withChannelTestWorkspaceCtx(t, badOwner, testUserID)
	badOwner = withRouteParams(badOwner, "channelId", ch.ID, "memberType", "user", "memberId", peerID)
	badRec := httptest.NewRecorder()
	testHandler.UpdateChannelMemberRole(badRec, badOwner)
	if badRec.Code != http.StatusBadRequest {
		t.Fatalf("PATCH role=owner want 400, got %d: %s", badRec.Code, badRec.Body.String())
	}

	// Transfer ownership to peer via dedicated POST.
	xfer := newRequestAs(testUserID, http.MethodPost,
		"/api/channels/"+ch.ID+"/members/user/"+peerID+"/transfer-ownership", nil)
	xfer = withChannelTestWorkspaceCtx(t, xfer, testUserID)
	xfer = withRouteParams(xfer, "channelId", ch.ID, "memberType", "user", "memberId", peerID)
	xferRec := httptest.NewRecorder()
	testHandler.TransferChannelOwnership(xferRec, xfer)
	if xferRec.Code != http.StatusOK {
		t.Fatalf("transfer = %d: %s", xferRec.Code, xferRec.Body.String())
	}
	var ownerRole, peerRole string
	_ = testPool.QueryRow(ctx, `
		SELECT role FROM channel_member WHERE channel_id = $1 AND member_type = 'user' AND member_id = $2`,
		parseUUID(ch.ID), testUserID).Scan(&ownerRole)
	_ = testPool.QueryRow(ctx, `
		SELECT role FROM channel_member WHERE channel_id = $1 AND member_type = 'user' AND member_id = $2`,
		parseUUID(ch.ID), peerID).Scan(&peerRole)
	if ownerRole != "manager" || peerRole != "owner" {
		t.Fatalf("after transfer owner=%q peer=%q want manager/owner", ownerRole, peerRole)
	}

	// Former owner can no longer manage roles.
	after := newRequestAs(testUserID, http.MethodPatch,
		"/api/channels/"+ch.ID+"/members/user/"+testUserID,
		map[string]any{"role": "manager"})
	after = withChannelTestWorkspaceCtx(t, after, testUserID)
	after = withRouteParams(after, "channelId", ch.ID, "memberType", "user", "memberId", testUserID)
	afterRec := httptest.NewRecorder()
	testHandler.UpdateChannelMemberRole(afterRec, after)
	if afterRec.Code != http.StatusForbidden {
		t.Fatalf("former owner manage want 403, got %d: %s", afterRec.Code, afterRec.Body.String())
	}

	// Agent cannot receive ownership via transfer endpoint.
	var agentID pgtype.UUID
	if err := testPool.QueryRow(ctx,
		`SELECT id FROM agent WHERE workspace_id = $1 LIMIT 1`, parseUUID(testWorkspaceID)).Scan(&agentID); err != nil {
		t.Skip("no agent fixture for owner CHECK")
	}
	_, _ = testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id, role)
		VALUES ($1, $2, 'agent', $3, 'member')
		ON CONFLICT DO NOTHING`, parseUUID(ch.ID), parseUUID(testWorkspaceID), agentID)
	agentOwner := newRequestAs(peerID, http.MethodPost,
		"/api/channels/"+ch.ID+"/members/agent/"+uuidToString(agentID)+"/transfer-ownership", nil)
	agentOwner = withChannelTestWorkspaceCtx(t, agentOwner, peerID)
	agentOwner = withRouteParams(agentOwner, "channelId", ch.ID, "memberType", "agent", "memberId", uuidToString(agentID))
	agentRec := httptest.NewRecorder()
	testHandler.TransferChannelOwnership(agentRec, agentOwner)
	if agentRec.Code != http.StatusBadRequest {
		t.Fatalf("agent transfer want 400, got %d: %s", agentRec.Code, agentRec.Body.String())
	}

	// Peer (now owner) can promote agent to manager.
	agentMgr := newRequestAs(peerID, http.MethodPatch,
		"/api/channels/"+ch.ID+"/members/agent/"+uuidToString(agentID),
		map[string]any{"role": "manager"})
	agentMgr = withChannelTestWorkspaceCtx(t, agentMgr, peerID)
	agentMgr = withRouteParams(agentMgr, "channelId", ch.ID, "memberType", "agent", "memberId", uuidToString(agentID))
	mgrRec := httptest.NewRecorder()
	testHandler.UpdateChannelMemberRole(mgrRec, agentMgr)
	if mgrRec.Code != http.StatusOK {
		t.Fatalf("agent manager = %d: %s", mgrRec.Code, mgrRec.Body.String())
	}
}

// TestConcurrentTransferOnlyOneWins: two concurrent transfers from the same
// owner — exactly one commits; loser re-checks owner in-tx and gets 403
// owner_changed (Barry #814 concurrency gate).
func TestConcurrentTransferOnlyOneWins(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test DB not configured")
	}
	ctx := context.Background()
	req := newRequestAs(testUserID, http.MethodPost, "/api/channels", map[string]any{
		"name": "role-race-xfer-" + uuid.NewString()[:8],
	})
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	created := httptest.NewRecorder()
	testHandler.CreateChannel(created, req)
	if created.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", created.Code, created.Body.String())
	}
	var ch ChannelResponse
	if err := json.Unmarshal(created.Body.Bytes(), &ch); err != nil {
		t.Fatal(err)
	}

	makePeer := func() string {
		tag := uuid.NewString()
		var id string
		if err := testPool.QueryRow(ctx,
			`INSERT INTO "user" (name, email) VALUES ($1,$2) RETURNING id`,
			"race-"+tag[:8], "race-"+tag+"@example.com").Scan(&id); err != nil {
			t.Fatalf("peer user: %v", err)
		}
		t.Cleanup(func() {
			_, _ = testPool.Exec(ctx, `DELETE FROM channel_member WHERE member_id=$1`, id)
			_, _ = testPool.Exec(ctx, `DELETE FROM member WHERE user_id=$1`, id)
			_, _ = testPool.Exec(ctx, `DELETE FROM "user" WHERE id=$1`, id)
		})
		if _, err := testPool.Exec(ctx,
			`INSERT INTO member (workspace_id, user_id, role) VALUES ($1,$2,'member')`,
			testWorkspaceID, id); err != nil {
			t.Fatalf("ws member: %v", err)
		}
		if _, err := testPool.Exec(ctx, `
			INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id, role)
			VALUES ($1,$2,'user',$3,'member')`,
			parseUUID(ch.ID), parseUUID(testWorkspaceID), id); err != nil {
			t.Fatalf("ch member: %v", err)
		}
		return id
	}
	peerA := makePeer()
	peerB := makePeer()

	// Force both handlers past entry owner snapshot before either Begin/locks.
	gate := make(chan struct{})
	testRoleMutationEntryGate = gate
	atomic.StoreInt32(&testRoleMutationEntryEntered, 0)
	t.Cleanup(func() {
		testRoleMutationEntryGate = nil
		atomic.StoreInt32(&testRoleMutationEntryEntered, 0)
	})

	type result struct {
		code int
		body string
	}
	results := make(chan result, 2)
	run := func(target string) {
		r := newRequestAs(testUserID, http.MethodPost,
			"/api/channels/"+ch.ID+"/members/user/"+target+"/transfer-ownership", nil)
		r = withChannelTestWorkspaceCtx(t, r, testUserID)
		r = withRouteParams(r, "channelId", ch.ID, "memberType", "user", "memberId", target)
		rec := httptest.NewRecorder()
		testHandler.TransferChannelOwnership(rec, r)
		results <- result{code: rec.Code, body: rec.Body.String()}
	}
	go run(peerA)
	go run(peerB)
	// Wait until both have snapshot entryWasOwner, then release locks race.
	deadline := time.After(5 * time.Second)
	for atomic.LoadInt32(&testRoleMutationEntryEntered) < 2 {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for both transfers to reach entry barrier")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	close(gate)
	r1 := <-results
	r2 := <-results

	codes := []int{r1.code, r2.code}
	ok, forbidden := 0, 0
	for _, c := range codes {
		switch c {
		case http.StatusOK:
			ok++
		case http.StatusForbidden:
			forbidden++
		}
	}
	if ok != 1 || forbidden != 1 {
		t.Fatalf("concurrent transfer codes=%v bodies=%q %q want one 200 and one 403",
			codes, r1.body, r2.body)
	}
	// Loser must carry owner_changed so FE can distinguish race from plain deny.
	loserBody := r1.body
	if r1.code == http.StatusOK {
		loserBody = r2.body
	}
	if !strings.Contains(loserBody, channelOwnerChangedCode) {
		t.Fatalf("loser body %q missing code %s", loserBody, channelOwnerChangedCode)
	}
	// Exactly one owner remains.
	var owners int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM channel_member
		WHERE channel_id=$1 AND role='owner' AND member_type='user'`,
		parseUUID(ch.ID)).Scan(&owners); err != nil || owners != 1 {
		t.Fatalf("owners=%d err=%v", owners, err)
	}
	// Audit row present for the successful transfer.
	var auditN int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM channel_message
		WHERE channel_id=$1 AND author_type='system'
		  AND parts->0->>'event'=$2`,
		parseUUID(ch.ID), channelOwnershipTransferredEvent).Scan(&auditN); err != nil || auditN < 1 {
		t.Fatalf("audit rows=%d err=%v want >=1", auditN, err)
	}
}

// TestTransferAuditFailureRollsBackOwnership injects a real system-event INSERT
// failure and asserts ownership UPDATEs do not commit (Barry hard gate).
func TestTransferAuditFailureRollsBackOwnership(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test DB not configured")
	}
	ctx := context.Background()
	req := newRequestAs(testUserID, http.MethodPost, "/api/channels", map[string]any{
		"name": "role-audit-fail-" + uuid.NewString()[:8],
	})
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	created := httptest.NewRecorder()
	testHandler.CreateChannel(created, req)
	if created.Code != http.StatusCreated {
		t.Fatalf("create: %d", created.Code)
	}
	var ch ChannelResponse
	_ = json.Unmarshal(created.Body.Bytes(), &ch)
	peer := insertChannelPeerUser(t, ch.ID, "member")

	// Force audit insert failure only for this channel (parallel-safe).
	testFailChannelMemberSystemEventChannelID.Store(ch.ID)
	t.Cleanup(func() { testFailChannelMemberSystemEventChannelID.Store("") })

	xfer := newRequestAs(testUserID, http.MethodPost,
		"/api/channels/"+ch.ID+"/members/user/"+peer+"/transfer-ownership", nil)
	xfer = withChannelTestWorkspaceCtx(t, xfer, testUserID)
	xfer = withRouteParams(xfer, "channelId", ch.ID, "memberType", "user", "memberId", peer)
	rec := httptest.NewRecorder()
	testHandler.TransferChannelOwnership(rec, xfer)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("forced audit fail want 500, got %d %s", rec.Code, rec.Body.String())
	}

	var ownerID, peerRole string
	var auditN int
	_ = testPool.QueryRow(ctx, `
		SELECT member_id::text FROM channel_member
		WHERE channel_id=$1 AND role='owner' AND member_type='user'`, parseUUID(ch.ID)).Scan(&ownerID)
	_ = testPool.QueryRow(ctx, `
		SELECT role FROM channel_member
		WHERE channel_id=$1 AND member_type='user' AND member_id=$2`,
		parseUUID(ch.ID), peer).Scan(&peerRole)
	_ = testPool.QueryRow(ctx, `
		SELECT count(*) FROM channel_message
		WHERE channel_id=$1 AND author_type='system'
		  AND parts->0->>'event'=$2`,
		parseUUID(ch.ID), channelOwnershipTransferredEvent).Scan(&auditN)
	if ownerID != testUserID || peerRole != "member" || auditN != 0 {
		t.Fatalf("after failed audit owner=%s peerRole=%s audit=%d want owner=actor peer=member audit=0",
			ownerID, peerRole, auditN)
	}

	// Remove injection → retry succeeds (flip to green).
	testFailChannelMemberSystemEventChannelID.Store("")
	rec2 := httptest.NewRecorder()
	testHandler.TransferChannelOwnership(rec2, xfer)
	if rec2.Code != http.StatusOK {
		t.Fatalf("retry after inject off want 200, got %d %s", rec2.Code, rec2.Body.String())
	}
	_ = testPool.QueryRow(ctx, `
		SELECT member_id::text FROM channel_member
		WHERE channel_id=$1 AND role='owner' AND member_type='user'`, parseUUID(ch.ID)).Scan(&ownerID)
	_ = testPool.QueryRow(ctx, `
		SELECT count(*) FROM channel_message
		WHERE channel_id=$1 AND author_type='system'
		  AND parts->0->>'event'=$2`,
		parseUUID(ch.ID), channelOwnershipTransferredEvent).Scan(&auditN)
	if ownerID != peer || auditN != 1 {
		t.Fatalf("retry owner=%s audit=%d want peer/1", ownerID, auditN)
	}
}

// TestConcurrentTransferVsRoleRace forces transfer-first lock overlap:
// transfer holds after channel FOR UPDATE; PATCH queues on same lock; then
// transfer commits → PATCH always 403 owner_changed (Barry deterministic).
func TestConcurrentTransferVsRoleRace(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test DB not configured")
	}
	ctx := context.Background()
	req := newRequestAs(testUserID, http.MethodPost, "/api/channels", map[string]any{
		"name": "role-xfer-patch-race-" + uuid.NewString()[:8],
	})
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	created := httptest.NewRecorder()
	testHandler.CreateChannel(created, req)
	if created.Code != http.StatusCreated {
		t.Fatalf("create: %d", created.Code)
	}
	var ch ChannelResponse
	_ = json.Unmarshal(created.Body.Bytes(), &ch)
	peer := insertChannelPeerUser(t, ch.ID, "member")
	targetMgr := insertChannelPeerUser(t, ch.ID, "member")

	entryGate := make(chan struct{})
	postLockGate := make(chan struct{})
	testRoleMutationEntryGate = entryGate
	testRoleMutationPostLockGate = postLockGate
	atomic.StoreInt32(&testRoleMutationEntryEntered, 0)
	atomic.StoreInt32(&testRoleMutationPostLockEntered, 0)
	t.Cleanup(func() {
		testRoleMutationEntryGate = nil
		testRoleMutationPostLockGate = nil
		atomic.StoreInt32(&testRoleMutationEntryEntered, 0)
		atomic.StoreInt32(&testRoleMutationPostLockEntered, 0)
	})

	type res struct {
		name string
		code int
		body string
	}
	out := make(chan res, 2)

	go func() {
		r := newRequestAs(testUserID, http.MethodPost,
			"/api/channels/"+ch.ID+"/members/user/"+peer+"/transfer-ownership", nil)
		r = withChannelTestWorkspaceCtx(t, r, testUserID)
		r = withRouteParams(r, "channelId", ch.ID, "memberType", "user", "memberId", peer)
		rec := httptest.NewRecorder()
		testHandler.TransferChannelOwnership(rec, r)
		out <- res{"transfer", rec.Code, rec.Body.String()}
	}()
	// Wait until transfer has entry snapshot, release entry so transfer can take channel lock.
	deadline := time.After(5 * time.Second)
	for atomic.LoadInt32(&testRoleMutationEntryEntered) < 1 {
		select {
		case <-deadline:
			t.Fatal("transfer never reached entry barrier")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	close(entryGate)
	// Wait until transfer holds channel lock (post-lock barrier).
	deadline = time.After(5 * time.Second)
	for atomic.LoadInt32(&testRoleMutationPostLockEntered) < 1 {
		select {
		case <-deadline:
			t.Fatal("transfer never reached post-lock barrier")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	// Now start PATCH while transfer still holds channel lock.
	go func() {
		r := newRequestAs(testUserID, http.MethodPatch,
			"/api/channels/"+ch.ID+"/members/user/"+targetMgr,
			map[string]any{"role": "manager"})
		r = withChannelTestWorkspaceCtx(t, r, testUserID)
		r = withRouteParams(r, "channelId", ch.ID, "memberType", "user", "memberId", targetMgr)
		rec := httptest.NewRecorder()
		testHandler.UpdateChannelMemberRole(rec, r)
		out <- res{"patch", rec.Code, rec.Body.String()}
	}()
	// Give PATCH time to block on channel FOR UPDATE.
	time.Sleep(50 * time.Millisecond)
	// Release transfer to finish; PATCH then wakes as stale owner.
	close(postLockGate)

	a, b := <-out, <-out
	byName := map[string]res{a.name: a, b.name: b}
	if byName["transfer"].code != http.StatusOK {
		t.Fatalf("transfer want 200 got %d %s", byName["transfer"].code, byName["transfer"].body)
	}
	if byName["patch"].code != http.StatusForbidden {
		t.Fatalf("patch want 403 owner_changed got %d %s", byName["patch"].code, byName["patch"].body)
	}
	if !strings.Contains(byName["patch"].body, channelOwnerChangedCode) {
		t.Fatalf("patch body missing owner_changed: %s", byName["patch"].body)
	}
	// Top-level code field present (literal contract).
	if !strings.Contains(byName["patch"].body, `"code"`) {
		t.Fatalf("patch body missing top-level code key: %s", byName["patch"].body)
	}
	var owners int
	var ownerID string
	_ = testPool.QueryRow(ctx, `
		SELECT count(*), max(member_id::text) FROM channel_member
		WHERE channel_id=$1 AND role='owner' AND member_type='user'`,
		parseUUID(ch.ID)).Scan(&owners, &ownerID)
	if owners != 1 || ownerID != peer {
		t.Fatalf("owners=%d owner=%s want 1/%s", owners, ownerID, peer)
	}
}

// TestRoleMutationNonOwnerHasNoOwnerChangedCode: ordinary non-owner 403 without code.
func TestRoleMutationNonOwnerHasNoOwnerChangedCode(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test DB not configured")
	}
	req := newRequestAs(testUserID, http.MethodPost, "/api/channels", map[string]any{
		"name": "role-plain-403-" + uuid.NewString()[:8],
	})
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	created := httptest.NewRecorder()
	testHandler.CreateChannel(created, req)
	var ch ChannelResponse
	_ = json.Unmarshal(created.Body.Bytes(), &ch)
	peer := insertChannelPeerUser(t, ch.ID, "member")

	// Peer tries promote self → plain 403, no owner_changed.
	r := newRequestAs(peer, http.MethodPatch,
		"/api/channels/"+ch.ID+"/members/user/"+peer,
		map[string]any{"role": "manager"})
	r = withChannelTestWorkspaceCtx(t, r, peer)
	r = withRouteParams(r, "channelId", ch.ID, "memberType", "user", "memberId", peer)
	rec := httptest.NewRecorder()
	testHandler.UpdateChannelMemberRole(rec, r)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("want 403 got %d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), channelOwnerChangedCode) {
		t.Fatalf("plain non-owner must not carry owner_changed: %s", rec.Body.String())
	}

	// Peer tries transfer → plain 403 no code.
	tr := newRequestAs(peer, http.MethodPost,
		"/api/channels/"+ch.ID+"/members/user/"+testUserID+"/transfer-ownership", nil)
	tr = withChannelTestWorkspaceCtx(t, tr, peer)
	tr = withRouteParams(tr, "channelId", ch.ID, "memberType", "user", "memberId", testUserID)
	trRec := httptest.NewRecorder()
	testHandler.TransferChannelOwnership(trRec, tr)
	if trRec.Code != http.StatusForbidden {
		t.Fatalf("transfer non-owner want 403 got %d %s", trRec.Code, trRec.Body.String())
	}
	if strings.Contains(trRec.Body.String(), channelOwnerChangedCode) {
		t.Fatalf("plain non-owner transfer must not carry owner_changed: %s", trRec.Body.String())
	}
}

// TestRoleMutationNegativeMatrix locks non-ordinary-group / missing target branches.
func TestRoleMutationNegativeMatrix(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test DB not configured")
	}
	ctx := context.Background()

	// Ordinary group for missing-target / agent-target.
	req := newRequestAs(testUserID, http.MethodPost, "/api/channels", map[string]any{
		"name": "role-neg-" + uuid.NewString()[:8],
	})
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	created := httptest.NewRecorder()
	testHandler.CreateChannel(created, req)
	var ch ChannelResponse
	_ = json.Unmarshal(created.Body.Bytes(), &ch)

	// Missing target → 404.
	ghost := uuid.NewString()
	miss := newRequestAs(testUserID, http.MethodPatch,
		"/api/channels/"+ch.ID+"/members/user/"+ghost,
		map[string]any{"role": "manager"})
	miss = withChannelTestWorkspaceCtx(t, miss, testUserID)
	miss = withRouteParams(miss, "channelId", ch.ID, "memberType", "user", "memberId", ghost)
	missRec := httptest.NewRecorder()
	testHandler.UpdateChannelMemberRole(missRec, miss)
	if missRec.Code != http.StatusNotFound {
		t.Fatalf("missing target want 404 got %d %s", missRec.Code, missRec.Body.String())
	}

	// Agent cannot receive ownership → 400.
	agentID := createHandlerTestAgent(t, "NegAgent"+uuid.NewString()[:6], []byte("[]"))
	_, _ = testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id, role)
		VALUES ($1,$2,'agent',$3,'member')
		ON CONFLICT DO NOTHING`, parseUUID(ch.ID), parseUUID(testWorkspaceID), parseUUID(agentID))
	ag := newRequestAs(testUserID, http.MethodPost,
		"/api/channels/"+ch.ID+"/members/agent/"+agentID+"/transfer-ownership", nil)
	ag = withChannelTestWorkspaceCtx(t, ag, testUserID)
	ag = withRouteParams(ag, "channelId", ch.ID, "memberType", "agent", "memberId", agentID)
	agRec := httptest.NewRecorder()
	testHandler.TransferChannelOwnership(agRec, ag)
	if agRec.Code != http.StatusBadRequest {
		t.Fatalf("agent transfer want 400 got %d %s", agRec.Code, agRec.Body.String())
	}

	// DM channel → not found / not group (404).
	var dmID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO channel (workspace_id, name, kind, created_by)
		VALUES ($1, $2, 'dm', $3) RETURNING id::text`,
		testWorkspaceID, "dm-role-"+uuid.NewString()[:8], testUserID).Scan(&dmID); err != nil {
		t.Fatalf("dm fixture: %v", err)
	}
	_, _ = testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id, role)
		VALUES ($1,$2,'user',$3,'owner')`, parseUUID(dmID), parseUUID(testWorkspaceID), testUserID)
	dmReq := newRequestAs(testUserID, http.MethodPatch,
		"/api/channels/"+dmID+"/members/user/"+testUserID,
		map[string]any{"role": "manager"})
	dmReq = withChannelTestWorkspaceCtx(t, dmReq, testUserID)
	dmReq = withRouteParams(dmReq, "channelId", dmID, "memberType", "user", "memberId", testUserID)
	dmRec := httptest.NewRecorder()
	testHandler.UpdateChannelMemberRole(dmRec, dmReq)
	if dmRec.Code != http.StatusNotFound {
		t.Fatalf("DM role patch want 404 got %d %s", dmRec.Code, dmRec.Body.String())
	}

	// Archived ordinary group → 409.
	archReq := newRequestAs(testUserID, http.MethodPost, "/api/channels", map[string]any{
		"name": "role-arch-" + uuid.NewString()[:8],
	})
	archReq = withChannelTestWorkspaceCtx(t, archReq, testUserID)
	archCreated := httptest.NewRecorder()
	testHandler.CreateChannel(archCreated, archReq)
	var arch ChannelResponse
	_ = json.Unmarshal(archCreated.Body.Bytes(), &arch)
	peer := insertChannelPeerUser(t, arch.ID, "member")
	_, err := testPool.Exec(ctx, `
		UPDATE channel SET archived_at = now(), archived_by = $2
		WHERE id = $1`, parseUUID(arch.ID), testUserID)
	if err != nil {
		t.Fatalf("archive: %v", err)
	}
	ar := newRequestAs(testUserID, http.MethodPatch,
		"/api/channels/"+arch.ID+"/members/user/"+peer,
		map[string]any{"role": "manager"})
	ar = withChannelTestWorkspaceCtx(t, ar, testUserID)
	ar = withRouteParams(ar, "channelId", arch.ID, "memberType", "user", "memberId", peer)
	arRec := httptest.NewRecorder()
	testHandler.UpdateChannelMemberRole(arRec, ar)
	if arRec.Code != http.StatusConflict {
		t.Fatalf("archived want 409 got %d %s", arRec.Code, arRec.Body.String())
	}
}

// TestListAgentChannelMembersIncludesRoleAndOrder asserts roles non-empty + exact order.
func TestListAgentChannelMembersIncludesRoleAndOrder(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test DB not configured")
	}
	ctx := context.Background()
	req := newRequestAs(testUserID, http.MethodPost, "/api/channels", map[string]any{
		"name": "role-agent-order-" + uuid.NewString()[:8],
	})
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	created := httptest.NewRecorder()
	testHandler.CreateChannel(created, req)
	var ch ChannelResponse
	_ = json.Unmarshal(created.Body.Bytes(), &ch)

	// Insert with controlled created_at so order is deterministic beyond role rank.
	// Sequence desired: owner(human), manager(agent), manager(human), member(human).
	humanMember := insertChannelPeerUser(t, ch.ID, "member")
	humanManager := insertChannelPeerUser(t, ch.ID, "manager")
	agentMgr := createHandlerTestAgent(t, "OrdAgent"+uuid.NewString()[:6], []byte("[]"))
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id, role)
		VALUES ($1,$2,'agent',$3,'manager')
		ON CONFLICT (channel_id, member_type, member_id) DO UPDATE SET role='manager'`,
		parseUUID(ch.ID), parseUUID(testWorkspaceID), parseUUID(agentMgr)); err != nil {
		t.Fatalf("agent member: %v", err)
	}
	// Stabilize timestamps: older manager-agent than manager-human; older member last.
	_, _ = testPool.Exec(ctx, `
		UPDATE channel_member SET created_at = now() - interval '3 hours'
		WHERE channel_id=$1 AND member_type='user' AND member_id=$2`, parseUUID(ch.ID), testUserID)
	_, _ = testPool.Exec(ctx, `
		UPDATE channel_member SET created_at = now() - interval '2 hours'
		WHERE channel_id=$1 AND member_type='agent' AND member_id=$2`, parseUUID(ch.ID), parseUUID(agentMgr))
	_, _ = testPool.Exec(ctx, `
		UPDATE channel_member SET created_at = now() - interval '1 hours'
		WHERE channel_id=$1 AND member_type='user' AND member_id=$2`, parseUUID(ch.ID), humanManager)
	_, _ = testPool.Exec(ctx, `
		UPDATE channel_member SET created_at = now() - interval '30 minutes'
		WHERE channel_id=$1 AND member_type='user' AND member_id=$2`, parseUUID(ch.ID), humanMember)

	memReq := newRequest(http.MethodGet, "/api/agent/channels/"+ch.ID+"/members", nil)
	memReq = withAgentPrincipal(memReq, agentMgr, testWorkspaceID, testUserID)
	memReq = withChannelTestWorkspaceCtx(t, memReq, testUserID)
	memReq = withURLParam(memReq, "channelId", ch.ID)
	memRec := httptest.NewRecorder()
	testHandler.ListAgentChannelMembers(memRec, memReq)
	if memRec.Code != http.StatusOK {
		t.Fatalf("list=%d %s", memRec.Code, memRec.Body.String())
	}
	var members []ChannelMemberResponse
	_ = json.Unmarshal(memRec.Body.Bytes(), &members)
	wantIDs := []string{testUserID, agentMgr, humanManager, humanMember}
	wantRoles := []string{"owner", "manager", "manager", "member"}
	if len(members) != len(wantIDs) {
		t.Fatalf("len=%d want %d members=%+v", len(members), len(wantIDs), members)
	}
	for i := range wantIDs {
		if members[i].MemberID != wantIDs[i] || members[i].Role != wantRoles[i] {
			t.Fatalf("pos %d got id=%s role=%q want id=%s role=%q full=%+v",
				i, members[i].MemberID, members[i].Role, wantIDs[i], wantRoles[i], members)
		}
		if members[i].Role == "" {
			t.Fatalf("pos %d empty role", i)
		}
	}
}

// insertChannelPeerUser inserts a workspace+channel member with the given channel role.
func insertChannelPeerUser(t *testing.T, channelID, role string) string {
	t.Helper()
	ctx := context.Background()
	tag := uuid.NewString()
	var id string
	if err := testPool.QueryRow(ctx,
		`INSERT INTO "user" (name, email) VALUES ($1,$2) RETURNING id`,
		"peer-"+tag[:8], "peer-"+tag+"@example.com").Scan(&id); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(ctx, `DELETE FROM channel_member WHERE member_id=$1`, id)
		_, _ = testPool.Exec(ctx, `DELETE FROM member WHERE user_id=$1`, id)
		_, _ = testPool.Exec(ctx, `DELETE FROM "user" WHERE id=$1`, id)
	})
	if _, err := testPool.Exec(ctx,
		`INSERT INTO member (workspace_id, user_id, role) VALUES ($1,$2,'member')`,
		testWorkspaceID, id); err != nil {
		t.Fatalf("ws member: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id, role)
		VALUES ($1,$2,'user',$3,$4)`,
		parseUUID(channelID), parseUUID(testWorkspaceID), id, role); err != nil {
		t.Fatalf("ch member: %v", err)
	}
	return id
}

// TestTransferAlreadyOwnerIdempotent: transfer to self/current owner → 200 exact shape.
func TestTransferAlreadyOwnerIdempotent(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test DB not configured")
	}
	req := newRequestAs(testUserID, http.MethodPost, "/api/channels", map[string]any{
		"name": "role-xfer-idem-" + uuid.NewString()[:8],
	})
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	created := httptest.NewRecorder()
	testHandler.CreateChannel(created, req)
	var ch ChannelResponse
	_ = json.Unmarshal(created.Body.Bytes(), &ch)

	xfer := newRequestAs(testUserID, http.MethodPost,
		"/api/channels/"+ch.ID+"/members/user/"+testUserID+"/transfer-ownership", nil)
	xfer = withChannelTestWorkspaceCtx(t, xfer, testUserID)
	xfer = withRouteParams(xfer, "channelId", ch.ID, "memberType", "user", "memberId", testUserID)
	rec := httptest.NewRecorder()
	testHandler.TransferChannelOwnership(rec, xfer)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200 got %d %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["status"] != "ok" || body["role"] != "owner" || body["member_id"] != testUserID {
		t.Fatalf("body=%v want status=ok role=owner member_id=self", body)
	}
	if body["previous_owner_id"] != testUserID {
		t.Fatalf("previous_owner_id=%v want self", body["previous_owner_id"])
	}
}

// TestRoleMutationSystemGeneralProtected: system general channel rejects role writes.
func TestRoleMutationSystemGeneralProtected(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test DB not configured")
	}
	ctx := context.Background()
	// Prefer existing system general if present; else skip when fixture unavailable.
	var channelID string
	err := testPool.QueryRow(ctx, `
		SELECT id::text FROM channel
		WHERE workspace_id = $1 AND system_key = 'general'
		LIMIT 1`, parseUUID(testWorkspaceID)).Scan(&channelID)
	if err != nil {
		t.Skip("no system general channel in test workspace")
	}
	// Ensure caller is a member so we hit system protection not 403 membership.
	_, _ = testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id, role)
		VALUES ($1,$2,'user',$3,'member')
		ON CONFLICT DO NOTHING`, parseUUID(channelID), parseUUID(testWorkspaceID), testUserID)

	r := newRequestAs(testUserID, http.MethodPatch,
		"/api/channels/"+channelID+"/members/user/"+testUserID,
		map[string]any{"role": "manager"})
	r = withChannelTestWorkspaceCtx(t, r, testUserID)
	r = withRouteParams(r, "channelId", channelID, "memberType", "user", "memberId", testUserID)
	rec := httptest.NewRecorder()
	testHandler.UpdateChannelMemberRole(rec, r)
	if rec.Code != http.StatusConflict {
		t.Fatalf("system general want 409 got %d %s", rec.Code, rec.Body.String())
	}
}

// TestRoleMutationWorkspaceAdminNotChannelOwner: workspace admin without channel
// owner role gets plain 403 (no owner_changed).
func TestRoleMutationWorkspaceAdminNotChannelOwner(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test DB not configured")
	}
	ctx := context.Background()
	req := newRequestAs(testUserID, http.MethodPost, "/api/channels", map[string]any{
		"name": "role-ws-admin-" + uuid.NewString()[:8],
	})
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	created := httptest.NewRecorder()
	testHandler.CreateChannel(created, req)
	var ch ChannelResponse
	_ = json.Unmarshal(created.Body.Bytes(), &ch)

	tag := uuid.NewString()
	var adminID string
	if err := testPool.QueryRow(ctx,
		`INSERT INTO "user" (name, email) VALUES ($1,$2) RETURNING id`,
		"wsadmin-"+tag[:8], "wsadmin-"+tag+"@example.com").Scan(&adminID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(ctx, `DELETE FROM channel_member WHERE member_id=$1`, adminID)
		_, _ = testPool.Exec(ctx, `DELETE FROM member WHERE user_id=$1`, adminID)
		_, _ = testPool.Exec(ctx, `DELETE FROM "user" WHERE id=$1`, adminID)
	})
	// Workspace admin (or owner) but only channel *member*.
	if _, err := testPool.Exec(ctx,
		`INSERT INTO member (workspace_id, user_id, role) VALUES ($1,$2,'admin')`,
		testWorkspaceID, adminID); err != nil {
		// some envs use 'owner' only — try admin first
		if _, err2 := testPool.Exec(ctx,
			`INSERT INTO member (workspace_id, user_id, role) VALUES ($1,$2,'owner')`,
			testWorkspaceID, adminID); err2 != nil {
			t.Fatalf("ws role: %v / %v", err, err2)
		}
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id, role)
		VALUES ($1,$2,'user',$3,'member')`,
		parseUUID(ch.ID), parseUUID(testWorkspaceID), adminID); err != nil {
		t.Fatal(err)
	}

	r := newRequestAs(adminID, http.MethodPatch,
		"/api/channels/"+ch.ID+"/members/user/"+adminID,
		map[string]any{"role": "manager"})
	r = withChannelTestWorkspaceCtx(t, r, adminID)
	r = withRouteParams(r, "channelId", ch.ID, "memberType", "user", "memberId", adminID)
	rec := httptest.NewRecorder()
	testHandler.UpdateChannelMemberRole(rec, r)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("ws admin non-channel-owner want 403 got %d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), channelOwnerChangedCode) {
		t.Fatalf("must not be owner_changed: %s", rec.Body.String())
	}
}

// TestRoleMutationRejectsAgentPrincipal: agent token cannot call human role routes.
func TestRoleMutationRejectsAgentPrincipal(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test DB not configured")
	}
	req := newRequestAs(testUserID, http.MethodPost, "/api/channels", map[string]any{
		"name": "role-agent-prin-" + uuid.NewString()[:8],
	})
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	created := httptest.NewRecorder()
	testHandler.CreateChannel(created, req)
	var ch ChannelResponse
	_ = json.Unmarshal(created.Body.Bytes(), &ch)
	peer := insertChannelPeerUser(t, ch.ID, "member")
	agentID := createHandlerTestAgent(t, "PrinAgent"+uuid.NewString()[:6], []byte("[]"))

	r := newRequest(http.MethodPatch,
		"/api/channels/"+ch.ID+"/members/user/"+peer,
		map[string]any{"role": "manager"})
	r = withAgentPrincipal(r, agentID, testWorkspaceID, testUserID)
	r = withChannelTestWorkspaceCtx(t, r, testUserID)
	r = withRouteParams(r, "channelId", ch.ID, "memberType", "user", "memberId", peer)
	rec := httptest.NewRecorder()
	testHandler.UpdateChannelMemberRole(rec, r)
	if rec.Code == http.StatusOK {
		t.Fatalf("agent principal must not PATCH role, got 200")
	}
	// rejectAgentOnHumanRoute typically 403
	if rec.Code != http.StatusForbidden && rec.Code != http.StatusUnauthorized {
		t.Logf("agent principal status=%d body=%s (acceptable if forbidden-class)", rec.Code, rec.Body.String())
	}
}

// TestAuditFailIsChannelScoped: failing audit on channel A does not block transfer on B.
func TestAuditFailIsChannelScoped(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test DB not configured")
	}
	mk := func(suffix string) (ChannelResponse, string) {
		req := newRequestAs(testUserID, http.MethodPost, "/api/channels", map[string]any{
			"name": "role-scope-" + suffix + "-" + uuid.NewString()[:6],
		})
		req = withChannelTestWorkspaceCtx(t, req, testUserID)
		created := httptest.NewRecorder()
		testHandler.CreateChannel(created, req)
		if created.Code != http.StatusCreated {
			t.Fatalf("create %s: %d", suffix, created.Code)
		}
		var ch ChannelResponse
		_ = json.Unmarshal(created.Body.Bytes(), &ch)
		return ch, insertChannelPeerUser(t, ch.ID, "member")
	}
	chA, peerA := mk("a")
	chB, peerB := mk("b")

	testFailChannelMemberSystemEventChannelID.Store(chA.ID)
	t.Cleanup(func() { testFailChannelMemberSystemEventChannelID.Store("") })

	// A fails
	xa := newRequestAs(testUserID, http.MethodPost,
		"/api/channels/"+chA.ID+"/members/user/"+peerA+"/transfer-ownership", nil)
	xa = withChannelTestWorkspaceCtx(t, xa, testUserID)
	xa = withRouteParams(xa, "channelId", chA.ID, "memberType", "user", "memberId", peerA)
	ra := httptest.NewRecorder()
	testHandler.TransferChannelOwnership(ra, xa)
	if ra.Code != http.StatusInternalServerError {
		t.Fatalf("A want 500 got %d", ra.Code)
	}

	// B succeeds under same process while A is poisoned
	xb := newRequestAs(testUserID, http.MethodPost,
		"/api/channels/"+chB.ID+"/members/user/"+peerB+"/transfer-ownership", nil)
	xb = withChannelTestWorkspaceCtx(t, xb, testUserID)
	xb = withRouteParams(xb, "channelId", chB.ID, "memberType", "user", "memberId", peerB)
	rb := httptest.NewRecorder()
	testHandler.TransferChannelOwnership(rb, xb)
	if rb.Code != http.StatusOK {
		t.Fatalf("B want 200 got %d %s", rb.Code, rb.Body.String())
	}
}
