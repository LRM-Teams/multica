package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/middleware"
)

// Parker/Barry #801 counterfactuals (authorization body):
//  ① owner ∈ channel, agent ∉ → 403 (no owner-borrow as viewer)
//  ② agent with own membership does not depend on owner membership
//
// Barry independent-verify gate (2026-07-28): tests must not soft-pass when
// Storage is nil, and ② must not depend on "can delete sole owner" (fails once
// #1286 ≥1-human-owner lands). Construct anchor human + hard-assert owner absent.

func withAgentPrincipal(r *http.Request, agentID, workspaceID, ownerUserID string) *http.Request {
	p := middleware.AgentPrincipal{
		AgentID:     agentID,
		WorkspaceID: workspaceID,
		OwnerUserID: ownerUserID,
		ActorSource: "agent_credential",
	}
	ctx := middleware.WithAgentPrincipal(r.Context(), p)
	// Still stamp owner header as production auth does (residual stamp);
	// ACL must not use it as viewer.
	r = r.WithContext(ctx)
	r.Header.Set("X-User-ID", ownerUserID)
	r.Header.Set("X-Agent-ID", agentID)
	r.Header.Set("X-Actor-Source", "agent_credential")
	return r
}

// TestBoundary_Counterfactual1_OwnerInChannelAgentOut_ListHidesChannel:
// owner is member of private channel; agent is not → ListAgentChannels must
// not return that channel (even with X-User-ID=owner stamped); upload must
// hit HTTP 403 via principal-native membership gate (fake storage installed).
func TestBoundary_Counterfactual1_OwnerInChannelAgentOut_ListHidesChannel(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	// Hard path through UploadAgentAttachment: never skip on Storage nil.
	prevStorage := testHandler.Storage
	testHandler.Storage = &mockStorage{}
	t.Cleanup(func() { testHandler.Storage = prevStorage })

	// Owner-only channel (testUserID is workspace owner / channel member).
	channelID := seedChannelForTest(t, "cf1-owner-only-"+uuid.NewString(), testUserID)
	// Agent with same owner, NOT in channel.
	agentID := createHandlerTestAgent(t, "CF1AgentOut", []byte("[]"))

	req := newRequest(http.MethodGet, "/api/agent/channels", nil)
	req = withAgentPrincipal(req, agentID, testWorkspaceID, testUserID)
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	rec := httptest.NewRecorder()
	testHandler.ListAgentChannels(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("ListAgentChannels status=%d body=%s", rec.Code, rec.Body.String())
	}
	var items []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &items); err != nil {
		t.Fatalf("decode: %v body=%s", err, rec.Body.String())
	}
	for _, it := range items {
		if id, _ := it["id"].(string); id == channelID {
			t.Fatalf("agent listed owner-only channel %s — owner-borrow as viewer (counterfactual ①)", channelID)
		}
	}

	// Upload to owner-only channel must be HTTP 403 (membership before storage).
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	fw, err := w.CreateFormFile("file", "x.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	if err := w.WriteField("channel_id", channelID); err != nil {
		t.Fatal(err)
	}
	_ = w.Close()
	upReq := httptest.NewRequest(http.MethodPost, "/api/agent/attachments", &body)
	upReq.Header.Set("Content-Type", w.FormDataContentType())
	upReq = withAgentPrincipal(upReq, agentID, testWorkspaceID, testUserID)
	upReq = withChannelTestWorkspaceCtx(t, upReq, testUserID)
	upRec := httptest.NewRecorder()
	testHandler.UploadAgentAttachment(upRec, upReq)
	if upRec.Code != http.StatusForbidden {
		t.Fatalf("upload to owner-only channel status=%d body=%s; want 403 (counterfactual ① endpoint gate)", upRec.Code, upRec.Body.String())
	}
}

// TestBoundary_Counterfactual2_AgentMember_IndependentOfOwnerMembership:
// agent is direct channel member; credential owner is NOT a member; another
// human anchors the channel (so #1286 ≥1-human-owner never blocks setup).
// Agent surface access must not depend on owner membership.
func TestBoundary_Counterfactual2_AgentMember_IndependentOfOwnerMembership(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	// Anchor human (channel owner) — distinct from agent credential owner.
	var anchorUserID string
	suffix := uuid.NewString()[:8]
	if err := testPool.QueryRow(ctx, `
		INSERT INTO "user" (name, email)
		VALUES ($1, $2) RETURNING id`,
		"CF2 Anchor "+suffix, "cf2-anchor-"+suffix+"@multica.test",
	).Scan(&anchorUserID); err != nil {
		t.Fatalf("create anchor human: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO member (workspace_id, user_id, role)
		VALUES ($1, $2, 'member')`, testWorkspaceID, anchorUserID); err != nil {
		t.Fatalf("add anchor workspace member: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM member WHERE workspace_id=$1 AND user_id=$2`, testWorkspaceID, anchorUserID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM "user" WHERE id=$1`, anchorUserID)
	})

	// Agent owned by testUserID (credential owner stamp) — distinct from channel owner.
	agentID := createHandlerTestAgent(t, "CF2AgentIn", []byte("[]"))

	// Channel created_by = anchor human so #1286 auto-seed owner trigger
	// (trg_channel_seed_human_owner_on_insert) plants anchor as sole human owner.
	// Credential owner (testUserID) must never appear on the roster — that is ②.
	var channelID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO channel (workspace_id, name, created_by)
		VALUES ($1, $2, $3)
		RETURNING id`,
		testWorkspaceID, "cf2-agent-in-"+uuid.NewString(), anchorUserID,
	).Scan(&channelID); err != nil {
		t.Fatalf("create channel: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM channel WHERE id = $1`, channelID)
	})

	// Confirm auto-seed planted anchor as owner (fail hard if trigger missing — setup invalid).
	var anchorOwnerRows int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM channel_member
		WHERE channel_id = $1 AND member_type = 'user' AND member_id = $2 AND role = 'owner'`,
		channelID, anchorUserID,
	).Scan(&anchorOwnerRows); err != nil {
		t.Fatalf("count anchor owner: %v", err)
	}
	if anchorOwnerRows != 1 {
		// Fallback for stacks without auto-seed trigger (pre-#1286): insert explicitly.
		if _, err := testPool.Exec(ctx, `
			INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id, role)
			VALUES ($1, $2, 'user', $3, 'owner')
			ON CONFLICT DO NOTHING`,
			channelID, testWorkspaceID, anchorUserID); err != nil {
			t.Fatalf("add anchor owner fallback: %v", err)
		}
		if err := testPool.QueryRow(ctx, `
			SELECT count(*) FROM channel_member
			WHERE channel_id = $1 AND member_type = 'user' AND member_id = $2 AND role = 'owner'`,
			channelID, anchorUserID,
		).Scan(&anchorOwnerRows); err != nil || anchorOwnerRows != 1 {
			t.Fatalf("anchor owner not present after fallback (rows=%d err=%v)", anchorOwnerRows, err)
		}
	}

	// Agent direct membership.
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id, role)
		VALUES ($1, $2, 'agent', $3, 'member')
		ON CONFLICT DO NOTHING`, channelID, testWorkspaceID, agentID); err != nil {
		t.Fatalf("add agent member: %v", err)
	}

	// Hard assert: credential owner is absent from the channel roster.
	var ownerRows int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM channel_member
		WHERE channel_id = $1 AND workspace_id = $2
		  AND member_type = 'user' AND member_id = $3`,
		channelID, testWorkspaceID, testUserID,
	).Scan(&ownerRows); err != nil {
		t.Fatalf("count owner membership: %v", err)
	}
	if ownerRows != 0 {
		t.Fatalf("credential owner still channel member (rows=%d) — ② setup invalid; do not soft-continue", ownerRows)
	}

	if !testHandler.agentHasSurfaceAccess(ctx, parseUUID(testWorkspaceID), parseUUID(agentID), parseUUID(channelID)) {
		t.Fatal("agent direct member lacks surface access — counterfactual ② fails")
	}

	// ListAgentChannels must include the channel even though owner is absent.
	req := newRequest(http.MethodGet, "/api/agent/channels", nil)
	req = withAgentPrincipal(req, agentID, testWorkspaceID, testUserID)
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	rec := httptest.NewRecorder()
	testHandler.ListAgentChannels(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("ListAgentChannels status=%d body=%s", rec.Code, rec.Body.String())
	}
	var items []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &items); err != nil {
		t.Fatalf("decode: %v", err)
	}
	found := false
	for _, it := range items {
		if id, _ := it["id"].(string); id == channelID {
			found = true
		}
	}
	if !found {
		t.Fatalf("agent member did not see own channel %s while owner absent (②)", channelID)
	}

	// Members list for agent-member channel must not 403 solely due to owner stamp.
	memReq := newRequest(http.MethodGet, "/api/agent/channels/"+channelID+"/members", nil)
	memReq = withAgentPrincipal(memReq, agentID, testWorkspaceID, testUserID)
	memReq = withChannelTestWorkspaceCtx(t, memReq, testUserID)
	memReq = withURLParam(memReq, "channelId", channelID)
	memRec := httptest.NewRecorder()
	testHandler.ListAgentChannelMembers(memRec, memReq)
	if memRec.Code != http.StatusOK {
		t.Fatalf("ListAgentChannelMembers status=%d body=%s — want 200 with owner absent (②)", memRec.Code, memRec.Body.String())
	}

	// Re-assert owner still absent after list (no accidental materialize).
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM channel_member
		WHERE channel_id = $1 AND member_type = 'user' AND member_id = $2`,
		channelID, testUserID,
	).Scan(&ownerRows); err != nil {
		t.Fatalf("re-count owner membership: %v", err)
	}
	if ownerRows != 0 {
		t.Fatalf("credential owner appeared in channel after agent list (rows=%d)", ownerRows)
	}
}
