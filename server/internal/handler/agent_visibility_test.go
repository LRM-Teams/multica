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

func TestCreateAgent_ChannelRequiresHomeChannelID(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	runtimeID := createHandlerTestRuntime(t)

	w := httptest.NewRecorder()
	req := newRequest(http.MethodPost, "/api/agents", map[string]any{
		"display_name": "channel-no-home-" + uuid.NewString()[:8],
		"runtime_id":   runtimeID,
		"visibility":   "channel",
	})
	testHandler.CreateAgent(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 without home_channel_id, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCreateAgent_ChannelWithHomeChannelID(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	runtimeID := createHandlerTestRuntime(t)
	channelID := seedChannelForTest(t, "home-"+uuid.NewString()[:8], testUserID)
	name := "channel-home-" + uuid.NewString()[:8]

	w := httptest.NewRecorder()
	req := newRequest(http.MethodPost, "/api/agents", map[string]any{
		"display_name":     name,
		"runtime_id":       runtimeID,
		"visibility":       "channel",
		"home_channel_id":  channelID,
	})
	testHandler.CreateAgent(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var resp AgentResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM agent WHERE id = $1`, resp.ID) })
	if resp.Visibility != "channel" {
		t.Fatalf("visibility = %q, want channel", resp.Visibility)
	}
	if resp.HomeChannelID == nil || *resp.HomeChannelID != channelID {
		t.Fatalf("home_channel_id = %v, want %s", resp.HomeChannelID, channelID)
	}
}

func TestCreateAgent_RejectsHomeChannelWithoutChannelVisibility(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	runtimeID := createHandlerTestRuntime(t)
	channelID := seedChannelForTest(t, "orphan-home-"+uuid.NewString()[:8], testUserID)

	w := httptest.NewRecorder()
	req := newRequest(http.MethodPost, "/api/agents", map[string]any{
		"display_name":    "workspace-with-home-" + uuid.NewString()[:8],
		"runtime_id":      runtimeID,
		"visibility":      "workspace",
		"home_channel_id": channelID,
	})
	testHandler.CreateAgent(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for home on workspace visibility, got %d: %s", w.Code, w.Body.String())
	}
}

func TestListAgents_ChannelVisibilityScopedToHome(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	runtimeID := createHandlerTestRuntime(t)
	homeID := seedChannelForTest(t, "list-home-"+uuid.NewString()[:8], testUserID)
	otherID := seedChannelForTest(t, "list-other-"+uuid.NewString()[:8], testUserID)

	createW := httptest.NewRecorder()
	createReq := newRequest(http.MethodPost, "/api/agents", map[string]any{
		"display_name":    "list-channel-" + uuid.NewString()[:8],
		"runtime_id":      runtimeID,
		"visibility":      "channel",
		"home_channel_id": homeID,
	})
	testHandler.CreateAgent(createW, createReq)
	if createW.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", createW.Code, createW.Body.String())
	}
	var created AgentResponse
	if err := json.Unmarshal(createW.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM agent WHERE id = $1`, created.ID) })

	assertListed := func(path string, wantPresent bool) {
		t.Helper()
		w := httptest.NewRecorder()
		req := newRequest(http.MethodGet, path, nil)
		testHandler.ListAgents(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("ListAgents %s: %d %s", path, w.Code, w.Body.String())
		}
		var listed []AgentResponse
		if err := json.Unmarshal(w.Body.Bytes(), &listed); err != nil {
			t.Fatalf("decode list: %v", err)
		}
		found := false
		for _, a := range listed {
			if a.ID == created.ID {
				found = true
				break
			}
		}
		if found != wantPresent {
			t.Fatalf("ListAgents %s: present=%v, want %v", path, found, wantPresent)
		}
	}

	assertListed("/api/agents", false)
	assertListed("/api/agents?channel_id="+otherID, false)
	assertListed("/api/agents?channel_id="+homeID, true)
}

func TestAddChannelMember_RejectsChannelAgentOutsideHome(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	runtimeID := createHandlerTestRuntime(t)
	homeID := seedChannelForTest(t, "invite-home-"+uuid.NewString()[:8], testUserID)
	otherID := seedChannelForTest(t, "invite-other-"+uuid.NewString()[:8], testUserID)

	createW := httptest.NewRecorder()
	testHandler.CreateAgent(createW, newRequest(http.MethodPost, "/api/agents", map[string]any{
		"display_name":    "invite-channel-" + uuid.NewString()[:8],
		"runtime_id":      runtimeID,
		"visibility":      "channel",
		"home_channel_id": homeID,
	}))
	if createW.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", createW.Code, createW.Body.String())
	}
	var created AgentResponse
	if err := json.Unmarshal(createW.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM agent WHERE id = $1`, created.ID) })

	// Outside home → reject.
	w := httptest.NewRecorder()
	req := newRequest(http.MethodPost, "/api/channels/"+otherID+"/members", AddChannelMemberRequest{
		MemberType: "agent",
		MemberID:   created.ID,
	})
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	req = withURLParam(req, "channelId", otherID)
	testHandler.AddChannelMember(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invite outside home: expected 400, got %d: %s", w.Code, w.Body.String())
	}

	// Home → allow.
	w = httptest.NewRecorder()
	req = newRequest(http.MethodPost, "/api/channels/"+homeID+"/members", AddChannelMemberRequest{
		MemberType: "agent",
		MemberID:   created.ID,
	})
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	req = withURLParam(req, "channelId", homeID)
	testHandler.AddChannelMember(w, req)
	if w.Code != http.StatusCreated && w.Code != http.StatusOK {
		t.Fatalf("invite to home: expected 200/201, got %d: %s", w.Code, w.Body.String())
	}
}

func TestChannelMentionCandidates_HidesChannelAgentOutsideHome(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	runtimeID := createHandlerTestRuntime(t)
	homeID := seedChannelForTest(t, "mention-home-"+uuid.NewString()[:8], testUserID)
	otherID := seedChannelForTest(t, "mention-other-"+uuid.NewString()[:8], testUserID)

	createW := httptest.NewRecorder()
	testHandler.CreateAgent(createW, newRequest(http.MethodPost, "/api/agents", map[string]any{
		"display_name":    "mention-channel-" + uuid.NewString()[:8],
		"runtime_id":      runtimeID,
		"visibility":      "channel",
		"home_channel_id": homeID,
	}))
	var created AgentResponse
	if err := json.Unmarshal(createW.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM agent WHERE id = $1`, created.ID) })

	// Seat exists in both channels (存量他群成员保留).
	for _, ch := range []string{homeID, otherID} {
		if _, err := testPool.Exec(context.Background(), `
			INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
			VALUES ($1, $2, 'agent', $3)
			ON CONFLICT DO NOTHING
		`, ch, testWorkspaceID, created.ID); err != nil {
			t.Fatalf("seed membership %s: %v", ch, err)
		}
	}

	homeCandidates := testHandler.channelMentionCandidates(context.Background(), testWorkspaceID, homeID)
	otherCandidates := testHandler.channelMentionCandidates(context.Background(), testWorkspaceID, otherID)

	foundInHome := false
	for _, c := range homeCandidates {
		if c.ID == created.ID {
			foundInHome = true
			break
		}
	}
	if !foundInHome {
		t.Fatal("expected channel agent in home @mention candidates")
	}
	for _, c := range otherCandidates {
		if c.ID == created.ID {
			t.Fatal("channel agent must not appear in @mention candidates outside home")
		}
	}
}

func TestUpdateAgent_ChannelVisibilityMigration(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	agentID := createHandlerTestAgent(t, "migrate-vis-"+uuid.NewString()[:8], nil)
	channelID := seedChannelForTest(t, "migrate-home-"+uuid.NewString()[:8], testUserID)

	w := httptest.NewRecorder()
	req := newRequest(http.MethodPut, "/api/agents/"+agentID, map[string]any{
		"visibility":      "channel",
		"home_channel_id": channelID,
	})
	req = withURLParam(req, "id", agentID)
	testHandler.UpdateAgent(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("migrate to channel: %d %s", w.Code, w.Body.String())
	}
	var resp AgentResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Visibility != "channel" || resp.HomeChannelID == nil || *resp.HomeChannelID != channelID {
		t.Fatalf("resp = %+v, want channel + home", resp)
	}

	var visibility string
	var home pgtype.UUID
	if err := testPool.QueryRow(context.Background(),
		`SELECT visibility, home_channel_id FROM agent WHERE id = $1`, agentID,
	).Scan(&visibility, &home); err != nil {
		t.Fatalf("db: %v", err)
	}
	if visibility != "channel" || uuidToString(home) != channelID {
		t.Fatalf("db visibility/home = %s/%s, want channel/%s", visibility, uuidToString(home), channelID)
	}
}

// createHandlerTestRuntime seeds an online runtime owned by the test user.
func createHandlerTestRuntime(t *testing.T) string {
	t.Helper()
	var rtID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO agent_runtime (workspace_id, owner_id, name, runtime_mode, provider, status, device_info, metadata, last_seen_at)
		VALUES ($1, $2, $3, 'cloud', 'lrm370', 'online', '', '{}'::jsonb, now())
		RETURNING id
	`, testWorkspaceID, testUserID, "lrm370-rt-"+uuid.NewString()).Scan(&rtID); err != nil {
		t.Fatalf("seed runtime: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE id = $1`, rtID) })
	return rtID
}
