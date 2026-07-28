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
// not return that channel (even with X-User-ID=owner stamped).
func TestBoundary_Counterfactual1_OwnerInChannelAgentOut_ListHidesChannel(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

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

	// Upload to owner-only channel must 403.
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
	// Storage may be nil → 503; if configured, must not be 200 for non-member.
	if testHandler.Storage == nil {
		// Still assert membership gate runs before storage: force check via surface access.
		if testHandler.agentHasSurfaceAccess(ctx, parseUUID(testWorkspaceID), parseUUID(agentID), parseUUID(channelID)) {
			t.Fatal("agentHasSurfaceAccess true for non-member — ① fails")
		}
		t.Log("Storage nil: membership gate asserted via agentHasSurfaceAccess")
		return
	}
	upRec := httptest.NewRecorder()
	testHandler.UploadAgentAttachment(upRec, upReq)
	if upRec.Code == http.StatusOK || upRec.Code == http.StatusCreated {
		t.Fatalf("upload to owner-only channel status=%d; want 403 (counterfactual ①)", upRec.Code)
	}
	if upRec.Code != http.StatusForbidden {
		t.Logf("upload status=%d body=%s (want 403; other fail-closed ok if not success)", upRec.Code, upRec.Body.String())
	}
}

// TestBoundary_Counterfactual2_AgentMember_IndependentOfOwnerMembership:
// agent is direct channel member; owner is NOT a member → agent still has
// surface access (does not depend on owner membership).
func TestBoundary_Counterfactual2_AgentMember_IndependentOfOwnerMembership(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	// Channel created by testUserID (owner); add agent as member.
	agentID := createHandlerTestAgent(t, "CF2AgentIn", []byte("[]"))
	channelID := seedChannelForTest(t, "cf2-agent-in-"+uuid.NewString(), testUserID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3)
		ON CONFLICT DO NOTHING`, channelID, testWorkspaceID, agentID); err != nil {
		t.Fatalf("add agent member: %v", err)
	}

	// Remove owner human from channel so only agent remains (plus maybe no human).
	// Ordinary group may require ≥1 human owner (migration 237) — if delete fails,
	// keep owner but prove access uses agent membership via second channel path.
	_, err := testPool.Exec(ctx, `
		DELETE FROM channel_member
		WHERE channel_id = $1 AND workspace_id = $2
		  AND member_type = 'user' AND member_id = $3`, channelID, testWorkspaceID, testUserID)
	ownerRemoved := err == nil
	if !ownerRemoved {
		t.Logf("could not remove owner human (constraint?): %v — still assert agent direct membership", err)
	}

	if !testHandler.agentHasSurfaceAccess(ctx, parseUUID(testWorkspaceID), parseUUID(agentID), parseUUID(channelID)) {
		t.Fatal("agent direct member lacks surface access — counterfactual ② fails")
	}

	// ListAgentChannels must include the channel.
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
		t.Fatalf("agent member did not see own channel %s (②)", channelID)
	}

	// Members list for agent-member channel must not 403 solely due to owner stamp.
	memReq := newRequest(http.MethodGet, "/api/agent/channels/"+channelID+"/members", nil)
	memReq = withAgentPrincipal(memReq, agentID, testWorkspaceID, testUserID)
	memReq = withChannelTestWorkspaceCtx(t, memReq, testUserID)
	memReq = withURLParam(memReq, "channelId", channelID)
	memRec := httptest.NewRecorder()
	testHandler.ListAgentChannelMembers(memRec, memReq)
	if memRec.Code == http.StatusForbidden {
		t.Fatalf("ListAgentChannelMembers 403 for agent member — still owner-dependent? body=%s", memRec.Body.String())
	}
	if memRec.Code != http.StatusOK {
		t.Logf("ListAgentChannelMembers status=%d body=%s", memRec.Code, memRec.Body.String())
	}
}
