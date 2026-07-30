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
		"/api/channels/"+ch.ID+"/members/user/"+testUserID+"?expected_remove_effect=none", nil)
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
	name := "role-list-brief-" + uuid.NewString()[:8]
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
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM channel WHERE id = $1`, ch.ID)
	})

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

// TestTransferAuditFailureRollsBackOwnership injects a real channel_message
// INSERT failure inside the transfer transaction (Barry hard gate).
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

	restore := installRoleMutationInsertFail(testHandler, ch.ID)

	xfer := newRequestAs(testUserID, http.MethodPost,
		"/api/channels/"+ch.ID+"/members/user/"+peer+"/transfer-ownership", nil)
	xfer = withChannelTestWorkspaceCtx(t, xfer, testUserID)
	xfer = withRouteParams(xfer, "channelId", ch.ID, "memberType", "user", "memberId", peer)
	rec := httptest.NewRecorder()
	testHandler.TransferChannelOwnership(rec, xfer)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("forced INSERT fail want 500, got %d %s", rec.Code, rec.Body.String())
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
		t.Fatalf("after failed INSERT owner=%s peerRole=%s audit=%d want actor/member/0",
			ownerID, peerRole, auditN)
	}

	restore() // remove injector
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

// TestConcurrentTransferVsRoleRace forces transfer-first lock hold with
// barrier proof (no sleep): transfer at post-lock → patch at pre-begin
// (entryWasOwner already true) → release patch begin (blocks on channel
// lock) → release transfer → patch 403 owner_changed every time.
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
	var ch ChannelResponse
	_ = json.Unmarshal(created.Body.Bytes(), &ch)
	peer := insertChannelPeerUser(t, ch.ID, "member")
	targetMgr := insertChannelPeerUser(t, ch.ID, "member")

	postLockGate := make(chan struct{})
	// preBegin installed only after transfer holds the channel lock, so transfer
	// is not blocked by the PATCH pre-begin gate.
	testRoleMutationPostLockGate = postLockGate
	testRoleMutationPreBeginGate = nil
	atomic.StoreInt32(&testRoleMutationPostLockEntered, 0)
	atomic.StoreInt32(&testRoleMutationPreBeginEntered, 0)
	t.Cleanup(func() {
		testRoleMutationPostLockGate = nil
		testRoleMutationPreBeginGate = nil
		atomic.StoreInt32(&testRoleMutationPostLockEntered, 0)
		atomic.StoreInt32(&testRoleMutationPreBeginEntered, 0)
		atomic.StoreInt32(&testRoleMutationLockAttemptEntered, 0)
	})

	type res struct {
		name string
		code int
		body string
	}
	out := make(chan res, 2)

	// 1) Start transfer only — will hold channel lock at post-lock barrier.
	go func() {
		r := newRequestAs(testUserID, http.MethodPost,
			"/api/channels/"+ch.ID+"/members/user/"+peer+"/transfer-ownership", nil)
		r = withChannelTestWorkspaceCtx(t, r, testUserID)
		r = withRouteParams(r, "channelId", ch.ID, "memberType", "user", "memberId", peer)
		rec := httptest.NewRecorder()
		testHandler.TransferChannelOwnership(rec, r)
		out <- res{"transfer", rec.Code, rec.Body.String()}
	}()
	deadline := time.After(5 * time.Second)
	for atomic.LoadInt32(&testRoleMutationPostLockEntered) < 1 {
		select {
		case <-deadline:
			t.Fatal("transfer never reached post-lock (channel lock held)")
		default:
			time.Sleep(time.Millisecond)
		}
	}

	// 2) Arm pre-begin, then start PATCH — snapshots entryWasOwner while transfer
	//    holds lock but has not demoted; blocks at pre-begin before Begin.
	preBeginGate := make(chan struct{})
	testRoleMutationPreBeginGate = preBeginGate
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
	deadline = time.After(5 * time.Second)
	for atomic.LoadInt32(&testRoleMutationPreBeginEntered) < 1 {
		select {
		case <-deadline:
			t.Fatal("patch never reached pre-begin (entry snapshot done)")
		default:
			time.Sleep(time.Millisecond)
		}
	}

	// 3) Release PATCH begin — Begin then noteRoleMutationLockAttempt + FOR UPDATE.
	// Transfer already incremented lock-attempt when it took the channel lock.
	// Wait until attempt count >= 2 (PATCH entered lock path) before releasing transfer.
	if atomic.LoadInt32(&testRoleMutationLockAttemptEntered) < 1 {
		t.Fatal("transfer should already have recorded a lock attempt")
	}
	close(preBeginGate)
	deadline = time.After(5 * time.Second)
	for atomic.LoadInt32(&testRoleMutationLockAttemptEntered) < 2 {
		select {
		case <-deadline:
			t.Fatalf("patch never entered FOR UPDATE attempt (count=%d)", atomic.LoadInt32(&testRoleMutationLockAttemptEntered))
		default:
			time.Sleep(time.Millisecond)
		}
	}
	// 4) Release transfer — demote + commit; PATCH wakes as stale owner.
	close(postLockGate)

	a, b := <-out, <-out
	by := map[string]res{a.name: a, b.name: b}
	if by["transfer"].code != http.StatusOK {
		t.Fatalf("transfer want 200 got %d %s", by["transfer"].code, by["transfer"].body)
	}
	if by["patch"].code != http.StatusForbidden || !strings.Contains(by["patch"].body, channelOwnerChangedCode) {
		t.Fatalf("patch want 403 owner_changed got %d %s", by["patch"].code, by["patch"].body)
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

// TestOwnerReadDBErrorReturns500 uses a QueryRow wrapper so Scan returns
// non-NoRows through the production actorIsChannelOwnerRead path.
func TestOwnerReadDBErrorReturns500(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test DB not configured")
	}
	ctx := context.Background()
	req := newRequestAs(testUserID, http.MethodPost, "/api/channels", map[string]any{
		"name": "role-preread-err-" + uuid.NewString()[:8],
	})
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	created := httptest.NewRecorder()
	testHandler.CreateChannel(created, req)
	var ch ChannelResponse
	_ = json.Unmarshal(created.Body.Bytes(), &ch)
	peer := insertChannelPeerUser(t, ch.ID, "member")

	restore := installRoleMutationOwnerReadFail(testHandler, fmt.Errorf("injected owner pre-read failure"))
	t.Cleanup(restore)

	pr := newRequestAs(testUserID, http.MethodPatch,
		"/api/channels/"+ch.ID+"/members/user/"+peer,
		map[string]any{"role": "manager"})
	pr = withChannelTestWorkspaceCtx(t, pr, testUserID)
	pr = withRouteParams(pr, "channelId", ch.ID, "memberType", "user", "memberId", peer)
	prRec := httptest.NewRecorder()
	testHandler.UpdateChannelMemberRole(prRec, pr)
	if prRec.Code != http.StatusInternalServerError {
		t.Fatalf("PATCH want 500 got %d %s", prRec.Code, prRec.Body.String())
	}
	if strings.Contains(prRec.Body.String(), channelOwnerChangedCode) {
		t.Fatalf("PATCH 500 must not carry owner_changed: %s", prRec.Body.String())
	}

	tr := newRequestAs(testUserID, http.MethodPost,
		"/api/channels/"+ch.ID+"/members/user/"+peer+"/transfer-ownership", nil)
	tr = withChannelTestWorkspaceCtx(t, tr, testUserID)
	tr = withRouteParams(tr, "channelId", ch.ID, "memberType", "user", "memberId", peer)
	trRec := httptest.NewRecorder()
	testHandler.TransferChannelOwnership(trRec, tr)
	if trRec.Code != http.StatusInternalServerError {
		t.Fatalf("transfer want 500 got %d %s", trRec.Code, trRec.Body.String())
	}
	if strings.Contains(trRec.Body.String(), channelOwnerChangedCode) {
		t.Fatalf("transfer 500 must not carry owner_changed: %s", trRec.Body.String())
	}

	var peerRole, ownerID string
	var auditN int
	_ = testPool.QueryRow(ctx, `SELECT role FROM channel_member WHERE channel_id=$1 AND member_id=$2`,
		parseUUID(ch.ID), peer).Scan(&peerRole)
	_ = testPool.QueryRow(ctx, `
		SELECT member_id::text FROM channel_member
		WHERE channel_id=$1 AND role='owner' AND member_type='user'`,
		parseUUID(ch.ID)).Scan(&ownerID)
	_ = testPool.QueryRow(ctx, `
		SELECT count(*) FROM channel_message
		WHERE channel_id=$1 AND author_type='system'
		  AND parts->0->>'event'=$2`,
		parseUUID(ch.ID), channelOwnershipTransferredEvent).Scan(&auditN)
	if peerRole != "member" || ownerID != testUserID || auditN != 0 {
		t.Fatalf("mutations leaked peerRole=%s owner=%s audit=%d", peerRole, ownerID, auditN)
	}
}

// TestListAgentChannelMembersIncludesRoleAndOrder locks agent-route roster
// order: role → created_at → member_type → member_id (same as human ListChannels).
// Earlier human manager before later agent manager proves no agent-first manager rank
// on /api/agent/channels/.../members (Barry #1344 successor).
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

	humanMgr := insertChannelPeerUser(t, ch.ID, "manager")
	m1 := insertChannelPeerUser(t, ch.ID, "member")
	m2 := insertChannelPeerUser(t, ch.ID, "member")
	agentMgr := createHandlerTestAgent(t, "OrdAgent"+uuid.NewString()[:6], []byte("[]"))
	if _, err := testPool.Exec(ctx, `
INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id, role)
VALUES ($1,$2,'agent',$3,'manager')
ON CONFLICT (channel_id, member_type, member_id) DO UPDATE SET role='manager'`,
		parseUUID(ch.ID), parseUUID(testWorkspaceID), parseUUID(agentMgr)); err != nil {
		t.Fatalf("agent: %v", err)
	}
	// owner earliest role rank; human manager earlier than agent manager (created_at);
	// two members same ts → member_id ASC.
	_, _ = testPool.Exec(ctx, `
UPDATE channel_member SET created_at = timestamptz '2020-01-01 00:00:00+00'
WHERE channel_id=$1 AND member_type='user' AND member_id=$2`, parseUUID(ch.ID), testUserID)
	_, _ = testPool.Exec(ctx, `
UPDATE channel_member SET created_at = timestamptz '2020-06-01 00:00:00+00'
WHERE channel_id=$1 AND member_type='user' AND member_id=$2`, parseUUID(ch.ID), humanMgr)
	_, _ = testPool.Exec(ctx, `
UPDATE channel_member SET created_at = timestamptz '2020-09-01 00:00:00+00'
WHERE channel_id=$1 AND member_type='agent' AND member_id=$2`, parseUUID(ch.ID), agentMgr)
	_, _ = testPool.Exec(ctx, `
UPDATE channel_member SET created_at = timestamptz '2021-01-01 00:00:00+00'
WHERE channel_id=$1 AND member_type='user' AND member_id = ANY($2::uuid[])`,
		parseUUID(ch.ID), []string{m1, m2})

	first, second := m1, m2
	if second < first {
		first, second = second, first
	}

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
	wantIDs := []string{testUserID, humanMgr, agentMgr, first, second}
	wantRoles := []string{"owner", "manager", "manager", "member", "member"}
	if len(members) != 5 {
		t.Fatalf("len=%d want 5 %+v", len(members), members)
	}
	for i := range wantIDs {
		if members[i].MemberID != wantIDs[i] || members[i].Role != wantRoles[i] || members[i].Role == "" {
			t.Fatalf("pos %d got id=%s role=%q want id=%s role=%q full=%+v",
				i, members[i].MemberID, members[i].Role, wantIDs[i], wantRoles[i], members)
		}
	}
	if members[1].MemberType != "user" || members[2].MemberType != "agent" {
		t.Fatalf("managers want human then agent by created_at got %+v %+v", members[1], members[2])
	}
}

// TestAuditFailIsChannelScoped: INSERT fail injector is per-handler and
// channel-scoped — channel B still transfers while A is poisoned.
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

	restore := installRoleMutationInsertFail(testHandler, chA.ID)
	t.Cleanup(restore)

	xa := newRequestAs(testUserID, http.MethodPost,
		"/api/channels/"+chA.ID+"/members/user/"+peerA+"/transfer-ownership", nil)
	xa = withChannelTestWorkspaceCtx(t, xa, testUserID)
	xa = withRouteParams(xa, "channelId", chA.ID, "memberType", "user", "memberId", peerA)
	ra := httptest.NewRecorder()
	testHandler.TransferChannelOwnership(ra, xa)
	if ra.Code != http.StatusInternalServerError {
		t.Fatalf("A want 500 got %d", ra.Code)
	}

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

// TestTransferAlreadyOwnerIdempotentExact: exact key set + zero new audit.
func TestTransferAlreadyOwnerIdempotentExact(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test DB not configured")
	}
	ctx := context.Background()
	req := newRequestAs(testUserID, http.MethodPost, "/api/channels", map[string]any{
		"name": "role-xfer-idem-" + uuid.NewString()[:8],
	})
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	created := httptest.NewRecorder()
	testHandler.CreateChannel(created, req)
	var ch ChannelResponse
	_ = json.Unmarshal(created.Body.Bytes(), &ch)

	var auditBefore int
	_ = testPool.QueryRow(ctx, `
		SELECT count(*) FROM channel_message
		WHERE channel_id=$1 AND author_type='system'
		  AND parts->0->>'event'=$2`,
		parseUUID(ch.ID), channelOwnershipTransferredEvent).Scan(&auditBefore)

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
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	wantKeys := map[string]bool{"status": true, "member_type": true, "member_id": true, "role": true, "previous_owner_id": true}
	if len(body) != len(wantKeys) {
		t.Fatalf("body keys=%v want exactly %v", body, wantKeys)
	}
	for k := range wantKeys {
		if _, ok := body[k]; !ok {
			t.Fatalf("missing key %s in %v", k, body)
		}
	}
	if body["status"] != "ok" || body["role"] != "owner" || body["member_type"] != "user" ||
		body["member_id"] != testUserID || body["previous_owner_id"] != testUserID {
		t.Fatalf("body=%v", body)
	}
	var auditAfter int
	_ = testPool.QueryRow(ctx, `
		SELECT count(*) FROM channel_message
		WHERE channel_id=$1 AND author_type='system'
		  AND parts->0->>'event'=$2`,
		parseUUID(ch.ID), channelOwnershipTransferredEvent).Scan(&auditAfter)
	if auditAfter != auditBefore {
		t.Fatalf("audit %d→%d want unchanged on idempotent transfer", auditBefore, auditAfter)
	}
}

// TestRoleMutationRejectsAgentPrincipalExact: PATCH+transfer both 403 exact body.
func TestRoleMutationRejectsAgentPrincipalExact(t *testing.T) {
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

	for _, tc := range []struct {
		name string
		run  func() *httptest.ResponseRecorder
	}{
		{"patch", func() *httptest.ResponseRecorder {
			r := newRequest(http.MethodPatch, "/api/channels/"+ch.ID+"/members/user/"+peer, map[string]any{"role": "manager"})
			r = withAgentPrincipal(r, agentID, testWorkspaceID, testUserID)
			r = withChannelTestWorkspaceCtx(t, r, testUserID)
			r = withRouteParams(r, "channelId", ch.ID, "memberType", "user", "memberId", peer)
			rec := httptest.NewRecorder()
			testHandler.UpdateChannelMemberRole(rec, r)
			return rec
		}},
		{"transfer", func() *httptest.ResponseRecorder {
			r := newRequest(http.MethodPost, "/api/channels/"+ch.ID+"/members/user/"+peer+"/transfer-ownership", nil)
			r = withAgentPrincipal(r, agentID, testWorkspaceID, testUserID)
			r = withChannelTestWorkspaceCtx(t, r, testUserID)
			r = withRouteParams(r, "channelId", ch.ID, "memberType", "user", "memberId", peer)
			rec := httptest.NewRecorder()
			testHandler.TransferChannelOwnership(rec, r)
			return rec
		}},
	} {
		rec := tc.run()
		if rec.Code != http.StatusForbidden {
			t.Fatalf("%s want 403 got %d %s", tc.name, rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "agent must use dedicated") {
			t.Fatalf("%s body=%q want dedicated route message", tc.name, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), channelOwnerChangedCode) {
			t.Fatalf("%s must not use owner_changed", tc.name)
		}
	}
}

// TestRoleMutationCrossWorkspace: channel in other workspace → 404, zero side effects.
func TestRoleMutationCrossWorkspace(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test DB not configured")
	}
	ctx := context.Background()
	// Create foreign workspace + channel owned by a throwaway user.
	var foreignWS, foreignUser, foreignCh string
	tag := uuid.NewString()
	if err := testPool.QueryRow(ctx,
		`INSERT INTO "user" (name, email) VALUES ($1,$2) RETURNING id`,
		"fx-"+tag[:8], "fx-"+tag+"@example.com").Scan(&foreignUser); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(ctx, `DELETE FROM channel_member WHERE channel_id=$1`, foreignCh)
		_, _ = testPool.Exec(ctx, `DELETE FROM channel WHERE id=$1`, foreignCh)
		_, _ = testPool.Exec(ctx, `DELETE FROM member WHERE workspace_id=$1`, foreignWS)
		_, _ = testPool.Exec(ctx, `DELETE FROM workspace WHERE id=$1`, foreignWS)
		_, _ = testPool.Exec(ctx, `DELETE FROM "user" WHERE id=$1`, foreignUser)
	})
	if err := testPool.QueryRow(ctx,
		`INSERT INTO workspace (name, slug) VALUES ($1,$2) RETURNING id`,
		"fx-ws-"+tag[:8], "fx-"+tag[:8]).Scan(&foreignWS); err != nil {
		t.Fatalf("workspace: %v", err)
	}
	_, _ = testPool.Exec(ctx, `INSERT INTO member (workspace_id, user_id, role) VALUES ($1,$2,'owner')`, foreignWS, foreignUser)
	if err := testPool.QueryRow(ctx, `
		INSERT INTO channel (workspace_id, name, kind, created_by)
		VALUES ($1,$2,'group',$3) RETURNING id::text`,
		foreignWS, "fx-ch-"+tag[:8], foreignUser).Scan(&foreignCh); err != nil {
		t.Fatalf("channel: %v", err)
	}
	_, _ = testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id, role)
		VALUES ($1,$2,'user',$3,'owner')`, parseUUID(foreignCh), parseUUID(foreignWS), foreignUser)

	// testUserID acts in testWorkspaceID context against foreign channel id.
	r := newRequestAs(testUserID, http.MethodPatch,
		"/api/channels/"+foreignCh+"/members/user/"+foreignUser,
		map[string]any{"role": "manager"})
	r = withChannelTestWorkspaceCtx(t, r, testUserID)
	r = withRouteParams(r, "channelId", foreignCh, "memberType", "user", "memberId", foreignUser)
	rec := httptest.NewRecorder()
	testHandler.UpdateChannelMemberRole(rec, r)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("cross-ws want exact 404 got %d %s", rec.Code, rec.Body.String())
	}
	var role string
	_ = testPool.QueryRow(ctx, `
		SELECT role FROM channel_member WHERE channel_id=$1 AND member_id=$2`,
		parseUUID(foreignCh), foreignUser).Scan(&role)
	if role != "owner" {
		t.Fatalf("foreign owner role mutated to %q", role)
	}
}

// TestRoleMutationSystemGeneralNoSkip creates a system general channel when missing.
func TestRoleMutationSystemGeneralNoSkip(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test DB not configured")
	}
	ctx := context.Background()
	var channelID string
	err := testPool.QueryRow(ctx, `
		SELECT id::text FROM channel
		WHERE workspace_id=$1 AND system_key='general' LIMIT 1`,
		parseUUID(testWorkspaceID)).Scan(&channelID)
	if err != nil {
		// Create disposable system general for this workspace.
		if err := testPool.QueryRow(ctx, `
			INSERT INTO channel (workspace_id, name, kind, system_key, created_by)
			VALUES ($1,'general','group','general',$2)
			RETURNING id::text`, parseUUID(testWorkspaceID), testUserID).Scan(&channelID); err != nil {
			t.Fatalf("create system general: %v", err)
		}
		t.Cleanup(func() {
			_, _ = testPool.Exec(ctx, `DELETE FROM channel_member WHERE channel_id=$1`, channelID)
			_, _ = testPool.Exec(ctx, `DELETE FROM channel WHERE id=$1`, channelID)
		})
	}
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

// TestRoleMutationWorkspaceAdminNotChannelOwner: workspace admin without channel owner → plain 403.
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
	if _, err := testPool.Exec(ctx,
		`INSERT INTO member (workspace_id, user_id, role) VALUES ($1,$2,'admin')`,
		testWorkspaceID, adminID); err != nil {
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

// TestRoleMutationNegativeMatrix: missing target 404, agent transfer 400, DM 404, archived 409.
func TestRoleMutationNegativeMatrix(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test DB not configured")
	}
	ctx := context.Background()
	req := newRequestAs(testUserID, http.MethodPost, "/api/channels", map[string]any{
		"name": "role-neg-" + uuid.NewString()[:8],
	})
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	created := httptest.NewRecorder()
	testHandler.CreateChannel(created, req)
	var ch ChannelResponse
	_ = json.Unmarshal(created.Body.Bytes(), &ch)

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
		UPDATE channel SET archived_at = now(), archived_by = $2 WHERE id = $1`,
		parseUUID(arch.ID), testUserID)
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

// TestListChannelsMemberBriefExactOrder locks ListChannels avatar-stack order
// (production: single row_number OVER role → created_at → member_type → member_id).
//
// Iris/Parker product: managers are same-rank regardless of human/agent — no
// manager-type priority before created_at. member_type is only a last identity
// tie-break with member_id (same UUID agent+user possible).
//
// Honest flip-red:
//   - role: delete role rank → RED (owner latest still first only with role)
//   - created_at: delete created_at → RED (member order opposite member_id;
//     among managers, earlier human before later agent proves no agent-priority)
//   - member_type: ordinary agent+user same UUID/role/created_at → drop type RED
//   - member_id: same type/role/created_at, two fixed UUIDs → ASC→DESC flip RED
//
// Independent subtests/channels so limit=5 does not force one fixture to carry all keys.
func TestListChannelsMemberBriefExactOrder(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test DB not configured")
	}
	ctx := context.Background()

	insertFixedUser := func(t *testing.T, id, label string) {
		t.Helper()
		if _, err := testPool.Exec(ctx, `
INSERT INTO "user" (id, name, email) VALUES ($1, $2, $3)
ON CONFLICT (id) DO NOTHING`, id, label, label+"@example.com"); err != nil {
			t.Fatalf("user %s: %v", id, err)
		}
		t.Cleanup(func() {
			_, _ = testPool.Exec(ctx, `DELETE FROM channel_member WHERE member_id=$1`, id)
			_, _ = testPool.Exec(ctx, `DELETE FROM member WHERE user_id=$1`, id)
			_, _ = testPool.Exec(ctx, `DELETE FROM "user" WHERE id=$1`, id)
		})
		_, _ = testPool.Exec(ctx, `
INSERT INTO member (workspace_id, user_id, role) VALUES ($1,$2,'member')
ON CONFLICT DO NOTHING`, testWorkspaceID, id)
	}
	insertFixedAgent := func(t *testing.T, id, label string) {
		t.Helper()
		handle := "ord" + id[len(id)-8:] + uuid.NewString()[:4]
		if _, err := testPool.Exec(ctx, `
INSERT INTO agent (
			id, workspace_id, name, display_name, description, runtime_mode, runtime_config, runtime_id, max_concurrent_tasks, owner_id, instructions, custom_env, custom_args, mcp_config
		, model) VALUES ($1, $2, $3, $4, '', 'cloud', '{}'::jsonb, $5, 1, $6, '', '{}'::jsonb, '[]'::jsonb, '[]'::jsonb, 'composer-1.5')
ON CONFLICT (id) DO NOTHING`,
			id, testWorkspaceID, handle, label, handlerTestRuntimeID(t), testUserID); err != nil {
			t.Fatalf("agent %s: %v", id, err)
		}
		t.Cleanup(func() {
			_, _ = testPool.Exec(ctx, `DELETE FROM channel_member WHERE member_id=$1`, id)
			_, _ = testPool.Exec(ctx, `DELETE FROM agent WHERE id=$1`, id)
		})
	}
	addMember := func(t *testing.T, channelID, typ, id, role string) {
		t.Helper()
		if _, err := testPool.Exec(ctx, `
INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id, role)
VALUES ($1,$2,$3,$4,$5)
ON CONFLICT (channel_id, member_type, member_id) DO UPDATE SET role=EXCLUDED.role`,
			parseUUID(channelID), parseUUID(testWorkspaceID), typ, parseUUID(id), role); err != nil {
			t.Fatalf("channel_member %s/%s: %v", typ, id, err)
		}
	}
	createGroup := func(t *testing.T) ChannelResponse {
		t.Helper()
		name := "role-list-ord-" + uuid.NewString()[:8]
		req := newRequestAs(testUserID, http.MethodPost, "/api/channels", map[string]any{"name": name})
		req = withChannelTestWorkspaceCtx(t, req, testUserID)
		created := httptest.NewRecorder()
		testHandler.CreateChannel(created, req)
		if created.Code != http.StatusCreated {
			t.Fatalf("create: %d %s", created.Code, created.Body.String())
		}
		var ch ChannelResponse
		_ = json.Unmarshal(created.Body.Bytes(), &ch)
		return ch
	}
	listMembers := func(t *testing.T, channelName string) []ChannelMemberBrief {
		t.Helper()
		listReq := newRequestAs(testUserID, http.MethodGet, "/api/channels", nil)
		listReq = withChannelTestWorkspaceCtx(t, listReq, testUserID)
		listRec := httptest.NewRecorder()
		testHandler.ListChannels(listRec, listReq)
		if listRec.Code != http.StatusOK {
			t.Fatalf("ListChannels: %d %s", listRec.Code, listRec.Body.String())
		}
		var channels []ChannelResponse
		_ = json.Unmarshal(listRec.Body.Bytes(), &channels)
		for i := range channels {
			if channels[i].Name == channelName {
				return channels[i].Members
			}
		}
		t.Fatalf("channel %q not in list", channelName)
		return nil
	}
	setTS := func(channelID, memberType, memberID, ts string) {
		_, _ = testPool.Exec(ctx, `
UPDATE channel_member SET created_at = $4::timestamptz
WHERE channel_id=$1 AND member_type=$2 AND member_id=$3`,
			parseUUID(channelID), memberType, parseUUID(memberID), ts)
	}

	t.Run("role_and_created_at", func(t *testing.T) {
		// role: owner latest still first. created_at: larger-id member earlier than smaller-id.
		ch := createGroup(t)
		const memLarge = "b2100000-0000-4000-8000-000000000002"
		const memSmall = "b2100000-0000-4000-8000-000000000001"
		insertFixedUser(t, memLarge, "ord-mem-large")
		insertFixedUser(t, memSmall, "ord-mem-small")
		addMember(t, ch.ID, "user", memLarge, "member")
		addMember(t, ch.ID, "user", memSmall, "member")
		setTS(ch.ID, "user", testUserID, "2022-01-01+00") // owner latest
		setTS(ch.ID, "user", memLarge, "2021-01-01+00")
		setTS(ch.ID, "user", memSmall, "2021-06-01+00")
		ms := listMembers(t, ch.Name)
		if len(ms) < 3 {
			t.Fatalf("members=%d want >=3 %+v", len(ms), ms)
		}
		if ms[0].MemberID != testUserID || ms[0].Role != "owner" {
			t.Fatalf("pos0=%+v want owner (role over latest created_at)", ms[0])
		}
		// after owner: memLarge then memSmall by created_at (not member_id ASC)
		if ms[1].MemberID != memLarge || ms[1].Role != "member" {
			t.Fatalf("pos1=%+v want %s earlier created_at", ms[1], memLarge)
		}
		if ms[2].MemberID != memSmall || ms[2].Role != "member" {
			t.Fatalf("pos2=%+v want %s later created_at", ms[2], memSmall)
		}
	})

	t.Run("managers_same_role_rank_by_created_at", func(t *testing.T) {
		// Same role=manager: order by created_at, not agent-before-human rank.
		// Human manager earlier + agent manager later → human first (would RED
		// if agent-priority rank still existed before created_at).
		ch := createGroup(t)
		const humanMgr = "a1300000-0000-4000-8000-000000000001"
		const agentMgr = "a1300000-0000-4000-8000-000000000002"
		insertFixedUser(t, humanMgr, "mgr-human-early")
		insertFixedAgent(t, agentMgr, "mgr-agent-late")
		addMember(t, ch.ID, "user", humanMgr, "manager")
		addMember(t, ch.ID, "agent", agentMgr, "manager")
		setTS(ch.ID, "user", testUserID, "2020-01-01+00")
		setTS(ch.ID, "user", humanMgr, "2020-06-01+00")  // earlier manager
		setTS(ch.ID, "agent", agentMgr, "2020-09-01+00") // later manager
		ms := listMembers(t, ch.Name)
		if len(ms) < 3 {
			t.Fatalf("members=%d want >=3 %+v", len(ms), ms)
		}
		if ms[0].Role != "owner" {
			t.Fatalf("pos0=%+v want owner", ms[0])
		}
		if ms[1].MemberID != humanMgr || ms[1].MemberType != "user" || ms[1].Role != "manager" {
			t.Fatalf("pos1=%+v want earlier human manager (created_at, not agent rank)", ms[1])
		}
		if ms[2].MemberID != agentMgr || ms[2].MemberType != "agent" || ms[2].Role != "manager" {
			t.Fatalf("pos2=%+v want later agent manager", ms[2])
		}
	})

	t.Run("member_type_ordinary_same_uuid", func(t *testing.T) {
		// Ordinary agent+user same UUID / role=member / same created_at.
		// manager-type CASE is ELSE for both — only member_type (then id) decides.
		// Counterfactual: drop member_type from OVER → order among equal keys unstable
		// or id-only; assert agent before user by type.
		ch := createGroup(t)
		const shared = "a1200000-0000-4000-8000-000000000002"
		insertFixedUser(t, shared, "ord-mem-human")
		insertFixedAgent(t, shared, "ord-mem-agent")
		addMember(t, ch.ID, "agent", shared, "member")
		addMember(t, ch.ID, "user", shared, "member")
		setTS(ch.ID, "user", testUserID, "2020-01-01+00")
		setTS(ch.ID, "agent", shared, "2021-01-01+00")
		setTS(ch.ID, "user", shared, "2021-01-01+00")
		ms := listMembers(t, ch.Name)
		if len(ms) < 3 {
			t.Fatalf("members=%d want >=3 %+v", len(ms), ms)
		}
		// Find the same-UUID pair after owner
		var agentPos, userPos = -1, -1
		for i, m := range ms {
			if m.MemberID != shared {
				continue
			}
			if m.MemberType == "agent" {
				agentPos = i
			}
			if m.MemberType == "user" {
				userPos = i
			}
		}
		if agentPos < 0 || userPos < 0 {
			t.Fatalf("missing same-UUID ordinary members in %+v", ms)
		}
		if agentPos >= userPos {
			t.Fatalf("member_type order: agent pos=%d user pos=%d want agent before user %+v", agentPos, userPos, ms)
		}
	})

	t.Run("member_id_same_type_role_created_at", func(t *testing.T) {
		// Two human members, same role/created_at, fixed UUIDs — product order
		// member_id ASC (smaller id first). Honest counterfactual: flip ASC→DESC
		// in the OVER clause → RED. Delete alone is NOT guaranteed RED (planner
		// may still emit same order; Barry exact -count=20).
		ch := createGroup(t)
		const idA = "b2200000-0000-4000-8000-000000000001" // smaller
		const idB = "b2200000-0000-4000-8000-000000000002" // larger
		insertFixedUser(t, idA, "ord-id-a")
		insertFixedUser(t, idB, "ord-id-b")
		addMember(t, ch.ID, "user", idA, "member")
		addMember(t, ch.ID, "user", idB, "member")
		setTS(ch.ID, "user", testUserID, "2020-01-01+00")
		setTS(ch.ID, "user", idA, "2021-01-01+00")
		setTS(ch.ID, "user", idB, "2021-01-01+00")
		ms := listMembers(t, ch.Name)
		if len(ms) < 3 {
			t.Fatalf("members=%d want >=3 %+v", len(ms), ms)
		}
		if ms[1].MemberID != idA || ms[1].Role != "member" {
			t.Fatalf("pos1=%+v want smaller member_id %s", ms[1], idA)
		}
		if ms[2].MemberID != idB || ms[2].Role != "member" {
			t.Fatalf("pos2=%+v want larger member_id %s", ms[2], idB)
		}
	})
}

// TestRoleMutationExactMatrixBothEndpoints locks unique status codes on PATCH+transfer.
func TestRoleMutationExactMatrixBothEndpoints(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test DB not configured")
	}
	ctx := context.Background()

	// Ordinary group fixture
	req := newRequestAs(testUserID, http.MethodPost, "/api/channels", map[string]any{
		"name": "role-exact-mx-" + uuid.NewString()[:8],
	})
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	created := httptest.NewRecorder()
	testHandler.CreateChannel(created, req)
	var ch ChannelResponse
	_ = json.Unmarshal(created.Body.Bytes(), &ch)
	peer := insertChannelPeerUser(t, ch.ID, "member")

	// missing target → 404 both
	ghost := uuid.NewString()
	for _, ep := range []struct {
		name string
		run  func() *httptest.ResponseRecorder
	}{
		{"patch-missing", func() *httptest.ResponseRecorder {
			r := newRequestAs(testUserID, http.MethodPatch, "/api/channels/"+ch.ID+"/members/user/"+ghost, map[string]any{"role": "manager"})
			r = withChannelTestWorkspaceCtx(t, r, testUserID)
			r = withRouteParams(r, "channelId", ch.ID, "memberType", "user", "memberId", ghost)
			rec := httptest.NewRecorder()
			testHandler.UpdateChannelMemberRole(rec, r)
			return rec
		}},
		{"xfer-missing", func() *httptest.ResponseRecorder {
			r := newRequestAs(testUserID, http.MethodPost, "/api/channels/"+ch.ID+"/members/user/"+ghost+"/transfer-ownership", nil)
			r = withChannelTestWorkspaceCtx(t, r, testUserID)
			r = withRouteParams(r, "channelId", ch.ID, "memberType", "user", "memberId", ghost)
			rec := httptest.NewRecorder()
			testHandler.TransferChannelOwnership(rec, r)
			return rec
		}},
	} {
		rec := ep.run()
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s want 404 got %d %s", ep.name, rec.Code, rec.Body.String())
		}
	}

	// peer non-owner: both 403 without owner_changed
	for _, ep := range []struct {
		name string
		run  func() *httptest.ResponseRecorder
	}{
		{"patch-peer", func() *httptest.ResponseRecorder {
			r := newRequestAs(peer, http.MethodPatch, "/api/channels/"+ch.ID+"/members/user/"+peer, map[string]any{"role": "manager"})
			r = withChannelTestWorkspaceCtx(t, r, peer)
			r = withRouteParams(r, "channelId", ch.ID, "memberType", "user", "memberId", peer)
			rec := httptest.NewRecorder()
			testHandler.UpdateChannelMemberRole(rec, r)
			return rec
		}},
		{"xfer-peer", func() *httptest.ResponseRecorder {
			r := newRequestAs(peer, http.MethodPost, "/api/channels/"+ch.ID+"/members/user/"+testUserID+"/transfer-ownership", nil)
			r = withChannelTestWorkspaceCtx(t, r, peer)
			r = withRouteParams(r, "channelId", ch.ID, "memberType", "user", "memberId", testUserID)
			rec := httptest.NewRecorder()
			testHandler.TransferChannelOwnership(rec, r)
			return rec
		}},
	} {
		rec := ep.run()
		if rec.Code != http.StatusForbidden || strings.Contains(rec.Body.String(), channelOwnerChangedCode) {
			t.Fatalf("%s want plain 403 got %d %s", ep.name, rec.Code, rec.Body.String())
		}
	}

	// workspace admin non-channel-owner transfer
	tag := uuid.NewString()
	var adminID string
	_ = testPool.QueryRow(ctx, `INSERT INTO "user" (name, email) VALUES ($1,$2) RETURNING id`,
		"mxadm-"+tag[:8], "mxadm-"+tag+"@example.com").Scan(&adminID)
	t.Cleanup(func() {
		_, _ = testPool.Exec(ctx, `DELETE FROM channel_member WHERE member_id=$1`, adminID)
		_, _ = testPool.Exec(ctx, `DELETE FROM member WHERE user_id=$1`, adminID)
		_, _ = testPool.Exec(ctx, `DELETE FROM "user" WHERE id=$1`, adminID)
	})
	if _, err := testPool.Exec(ctx, `INSERT INTO member (workspace_id, user_id, role) VALUES ($1,$2,'admin')`, testWorkspaceID, adminID); err != nil {
		_, _ = testPool.Exec(ctx, `INSERT INTO member (workspace_id, user_id, role) VALUES ($1,$2,'owner')`, testWorkspaceID, adminID)
	}
	_, _ = testPool.Exec(ctx, `INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id, role) VALUES ($1,$2,'user',$3,'member')`,
		parseUUID(ch.ID), parseUUID(testWorkspaceID), adminID)
	ar := newRequestAs(adminID, http.MethodPost, "/api/channels/"+ch.ID+"/members/user/"+peer+"/transfer-ownership", nil)
	ar = withChannelTestWorkspaceCtx(t, ar, adminID)
	ar = withRouteParams(ar, "channelId", ch.ID, "memberType", "user", "memberId", peer)
	arRec := httptest.NewRecorder()
	testHandler.TransferChannelOwnership(arRec, ar)
	if arRec.Code != http.StatusForbidden || strings.Contains(arRec.Body.String(), channelOwnerChangedCode) {
		t.Fatalf("ws-admin transfer want plain 403 got %d %s", arRec.Code, arRec.Body.String())
	}

	// DM: both 404
	var dmID string
	_ = testPool.QueryRow(ctx, `INSERT INTO channel (workspace_id, name, kind, created_by) VALUES ($1,$2,'dm',$3) RETURNING id::text`,
		testWorkspaceID, "dm-mx-"+uuid.NewString()[:8], testUserID).Scan(&dmID)
	_, _ = testPool.Exec(ctx, `INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id, role) VALUES ($1,$2,'user',$3,'owner')`,
		parseUUID(dmID), parseUUID(testWorkspaceID), testUserID)
	for _, ep := range []struct {
		name string
		run  func() *httptest.ResponseRecorder
	}{
		{"dm-patch", func() *httptest.ResponseRecorder {
			r := newRequestAs(testUserID, http.MethodPatch, "/api/channels/"+dmID+"/members/user/"+testUserID, map[string]any{"role": "manager"})
			r = withChannelTestWorkspaceCtx(t, r, testUserID)
			r = withRouteParams(r, "channelId", dmID, "memberType", "user", "memberId", testUserID)
			rec := httptest.NewRecorder()
			testHandler.UpdateChannelMemberRole(rec, r)
			return rec
		}},
		{"dm-xfer", func() *httptest.ResponseRecorder {
			r := newRequestAs(testUserID, http.MethodPost, "/api/channels/"+dmID+"/members/user/"+testUserID+"/transfer-ownership", nil)
			r = withChannelTestWorkspaceCtx(t, r, testUserID)
			r = withRouteParams(r, "channelId", dmID, "memberType", "user", "memberId", testUserID)
			rec := httptest.NewRecorder()
			testHandler.TransferChannelOwnership(rec, r)
			return rec
		}},
	} {
		rec := ep.run()
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s want 404 got %d %s", ep.name, rec.Code, rec.Body.String())
		}
	}

	// archived: both 409
	archName := "role-arch-mx-" + uuid.NewString()[:8]
	archReq := newRequestAs(testUserID, http.MethodPost, "/api/channels", map[string]any{"name": archName})
	archReq = withChannelTestWorkspaceCtx(t, archReq, testUserID)
	archCreated := httptest.NewRecorder()
	testHandler.CreateChannel(archCreated, archReq)
	var arch ChannelResponse
	_ = json.Unmarshal(archCreated.Body.Bytes(), &arch)
	archPeer := insertChannelPeerUser(t, arch.ID, "member")
	_, _ = testPool.Exec(ctx, `UPDATE channel SET archived_at=now(), archived_by=$2 WHERE id=$1`, parseUUID(arch.ID), testUserID)
	for _, ep := range []struct {
		name string
		run  func() *httptest.ResponseRecorder
	}{
		{"arch-patch", func() *httptest.ResponseRecorder {
			r := newRequestAs(testUserID, http.MethodPatch, "/api/channels/"+arch.ID+"/members/user/"+archPeer, map[string]any{"role": "manager"})
			r = withChannelTestWorkspaceCtx(t, r, testUserID)
			r = withRouteParams(r, "channelId", arch.ID, "memberType", "user", "memberId", archPeer)
			rec := httptest.NewRecorder()
			testHandler.UpdateChannelMemberRole(rec, r)
			return rec
		}},
		{"arch-xfer", func() *httptest.ResponseRecorder {
			r := newRequestAs(testUserID, http.MethodPost, "/api/channels/"+arch.ID+"/members/user/"+archPeer+"/transfer-ownership", nil)
			r = withChannelTestWorkspaceCtx(t, r, testUserID)
			r = withRouteParams(r, "channelId", arch.ID, "memberType", "user", "memberId", archPeer)
			rec := httptest.NewRecorder()
			testHandler.TransferChannelOwnership(rec, r)
			return rec
		}},
	} {
		rec := ep.run()
		if rec.Code != http.StatusConflict {
			t.Fatalf("%s want 409 got %d %s", ep.name, rec.Code, rec.Body.String())
		}
	}

	// system general: both 409
	var genID string
	err := testPool.QueryRow(ctx, `SELECT id::text FROM channel WHERE workspace_id=$1 AND system_key='general' LIMIT 1`, parseUUID(testWorkspaceID)).Scan(&genID)
	if err != nil {
		_ = testPool.QueryRow(ctx, `
			INSERT INTO channel (workspace_id, name, kind, system_key, created_by)
			VALUES ($1,'general','group','general',$2) RETURNING id::text`,
			parseUUID(testWorkspaceID), testUserID).Scan(&genID)
		t.Cleanup(func() {
			_, _ = testPool.Exec(ctx, `DELETE FROM channel_member WHERE channel_id=$1`, genID)
			_, _ = testPool.Exec(ctx, `DELETE FROM channel WHERE id=$1`, genID)
		})
	}
	_, _ = testPool.Exec(ctx, `INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id, role) VALUES ($1,$2,'user',$3,'member') ON CONFLICT DO NOTHING`,
		parseUUID(genID), parseUUID(testWorkspaceID), testUserID)
	for _, ep := range []struct {
		name string
		run  func() *httptest.ResponseRecorder
	}{
		{"gen-patch", func() *httptest.ResponseRecorder {
			r := newRequestAs(testUserID, http.MethodPatch, "/api/channels/"+genID+"/members/user/"+testUserID, map[string]any{"role": "manager"})
			r = withChannelTestWorkspaceCtx(t, r, testUserID)
			r = withRouteParams(r, "channelId", genID, "memberType", "user", "memberId", testUserID)
			rec := httptest.NewRecorder()
			testHandler.UpdateChannelMemberRole(rec, r)
			return rec
		}},
		{"gen-xfer", func() *httptest.ResponseRecorder {
			r := newRequestAs(testUserID, http.MethodPost, "/api/channels/"+genID+"/members/user/"+testUserID+"/transfer-ownership", nil)
			r = withChannelTestWorkspaceCtx(t, r, testUserID)
			r = withRouteParams(r, "channelId", genID, "memberType", "user", "memberId", testUserID)
			rec := httptest.NewRecorder()
			testHandler.TransferChannelOwnership(rec, r)
			return rec
		}},
	} {
		rec := ep.run()
		if rec.Code != http.StatusConflict {
			t.Fatalf("%s want 409 got %d %s", ep.name, rec.Code, rec.Body.String())
		}
	}

	// cross-workspace: exact 404 (not 403) both endpoints, zero mutation
	var foreignWS, foreignUser, foreignCh string
	ftag := uuid.NewString()
	_ = testPool.QueryRow(ctx, `INSERT INTO "user" (name, email) VALUES ($1,$2) RETURNING id`, "fx2-"+ftag[:8], "fx2-"+ftag+"@example.com").Scan(&foreignUser)
	t.Cleanup(func() {
		_, _ = testPool.Exec(ctx, `DELETE FROM channel_member WHERE channel_id=$1`, foreignCh)
		_, _ = testPool.Exec(ctx, `DELETE FROM channel WHERE id=$1`, foreignCh)
		_, _ = testPool.Exec(ctx, `DELETE FROM member WHERE workspace_id=$1`, foreignWS)
		_, _ = testPool.Exec(ctx, `DELETE FROM workspace WHERE id=$1`, foreignWS)
		_, _ = testPool.Exec(ctx, `DELETE FROM "user" WHERE id=$1`, foreignUser)
	})
	_ = testPool.QueryRow(ctx, `INSERT INTO workspace (name, slug) VALUES ($1,$2) RETURNING id`, "fx2-"+ftag[:8], "fx2-"+ftag[:8]).Scan(&foreignWS)
	_, _ = testPool.Exec(ctx, `INSERT INTO member (workspace_id, user_id, role) VALUES ($1,$2,'owner')`, foreignWS, foreignUser)
	_ = testPool.QueryRow(ctx, `INSERT INTO channel (workspace_id, name, kind, created_by) VALUES ($1,$2,'group',$3) RETURNING id::text`,
		foreignWS, "fx2-ch-"+ftag[:8], foreignUser).Scan(&foreignCh)
	_, _ = testPool.Exec(ctx, `INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id, role) VALUES ($1,$2,'user',$3,'owner')`,
		parseUUID(foreignCh), parseUUID(foreignWS), foreignUser)
	for _, ep := range []struct {
		name string
		run  func() *httptest.ResponseRecorder
	}{
		{"xws-patch", func() *httptest.ResponseRecorder {
			r := newRequestAs(testUserID, http.MethodPatch, "/api/channels/"+foreignCh+"/members/user/"+foreignUser, map[string]any{"role": "manager"})
			r = withChannelTestWorkspaceCtx(t, r, testUserID)
			r = withRouteParams(r, "channelId", foreignCh, "memberType", "user", "memberId", foreignUser)
			rec := httptest.NewRecorder()
			testHandler.UpdateChannelMemberRole(rec, r)
			return rec
		}},
		{"xws-xfer", func() *httptest.ResponseRecorder {
			r := newRequestAs(testUserID, http.MethodPost, "/api/channels/"+foreignCh+"/members/user/"+foreignUser+"/transfer-ownership", nil)
			r = withChannelTestWorkspaceCtx(t, r, testUserID)
			r = withRouteParams(r, "channelId", foreignCh, "memberType", "user", "memberId", foreignUser)
			rec := httptest.NewRecorder()
			testHandler.TransferChannelOwnership(rec, r)
			return rec
		}},
	} {
		rec := ep.run()
		// Object-level: foreign channel/workspace → exact 404 both ends (Parker product;
		// not 403 membership). Fixed contract — no dynamic probe.
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s want exact 404 got %d %s", ep.name, rec.Code, rec.Body.String())
		}
	}
	var role string
	_ = testPool.QueryRow(ctx, `SELECT role FROM channel_member WHERE channel_id=$1 AND member_id=$2`, parseUUID(foreignCh), foreignUser).Scan(&role)
	if role != "owner" {
		t.Fatalf("cross-ws mutated role=%s", role)
	}
	var auditN int
	_ = testPool.QueryRow(ctx, `
		SELECT count(*) FROM channel_message
		WHERE channel_id=$1 AND author_type='system'
		  AND parts->0->>'event'=$2`,
		parseUUID(foreignCh), channelOwnershipTransferredEvent).Scan(&auditN)
	if auditN != 0 {
		t.Fatalf("cross-ws audit rows=%d want 0", auditN)
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
