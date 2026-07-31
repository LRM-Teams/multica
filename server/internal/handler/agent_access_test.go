package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// privateAgentTestFixture sets up a private agent owned by a freshly created
// user, plus a second non-admin member in the workspace. Returns the agent
// id, the owner's user id, and the unrelated member's user id. The caller's
// own testUserID stays workspace owner so it can act as the privileged
// admin path.
func privateAgentTestFixture(t *testing.T) (agentID, ownerID, memberID string) {
	t.Helper()

	ctx := context.Background()
	if err := testPool.QueryRow(ctx, `
		INSERT INTO "user" (name, email)
		VALUES ('Private Agent Owner', 'private-agent-owner@multica.test')
		RETURNING id
	`).Scan(&ownerID); err != nil {
		t.Fatalf("create owner user: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(),
			`DELETE FROM "user" WHERE email = 'private-agent-owner@multica.test'`)
	})

	if _, err := testPool.Exec(ctx, `
		INSERT INTO member (workspace_id, user_id, role)
		VALUES ($1, $2, 'member')
	`, testWorkspaceID, ownerID); err != nil {
		t.Fatalf("add owner as member: %v", err)
	}

	if err := testPool.QueryRow(ctx, `
		INSERT INTO "user" (name, email)
		VALUES ('Plain Member', 'plain-member@multica.test')
		RETURNING id
	`).Scan(&memberID); err != nil {
		t.Fatalf("create plain member user: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(),
			`DELETE FROM "user" WHERE email = 'plain-member@multica.test'`)
	})

	if _, err := testPool.Exec(ctx, `
		INSERT INTO member (workspace_id, user_id, role)
		VALUES ($1, $2, 'member')
	`, testWorkspaceID, memberID); err != nil {
		t.Fatalf("add plain member: %v", err)
	}

	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent (
			workspace_id, name, description, runtime_mode, runtime_config, runtime_id, max_concurrent_tasks, owner_id, instructions, custom_env, custom_args
		, model) VALUES ($1, 'private-access-test-agent', '', 'cloud', '{}'::jsonb, $2, 1, $3, '', '{}'::jsonb, '[]'::jsonb, 'composer-1.5')
		RETURNING id
	`, testWorkspaceID, handlerTestRuntimeID(t), ownerID).Scan(&agentID); err != nil {
		t.Fatalf("create private agent: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(),
			`DELETE FROM agent WHERE id = $1`, agentID)
	})

	return agentID, ownerID, memberID
}

func newRequestAs(userID, method, path string, body any) *http.Request {
	req := newRequest(method, path, body)
	req.Header.Set("X-User-ID", userID)
	return req
}

// TestGetAgent_PrivateAgentForbidsPlainMember verifies the private-agent
// visibility gate at the read-detail endpoint: a workspace member who is
// neither the agent owner nor a workspace owner/admin gets 403, while the
// agent owner and workspace owner both succeed. Mirrors the four-entry-point
// gate (chat, history, edit, delete) on its read surface.
// TestGetAgent_UnconditionalButInternalsGated is the task #908 successor:
// GetAgent no longer 403s for any workspace member (existence/identity is
// unconditional — Parker, #multica f83df812, 2026-07-30 14:50 "端点保持
// 200，不然成员点详情页直接坏"), but Instructions/RuntimeConfig/CustomArgs
// (the agent's internal construction) stay redacted to non-owner/non-admin
// viewers via canAccessAgentInternals.
func TestGetAgent_UnconditionalButInternalsGated(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	agentID, ownerID, memberID := privateAgentTestFixture(t)
	if _, err := testPool.Exec(context.Background(), `
		UPDATE agent
		   SET instructions = 'secret system prompt',
		       custom_args = '["--secret-flag"]'::jsonb,
		       runtime_config = '{"secret_key":"secret_value"}'::jsonb
		 WHERE id = $1`, agentID); err != nil {
		t.Fatalf("seed internals: %v", err)
	}

	// Workspace owner: 200, sees internals.
	w := httptest.NewRecorder()
	testHandler.GetAgent(w, withURLParam(newRequest("GET", "/api/agents/"+agentID, nil), "id", agentID))
	if w.Code != http.StatusOK {
		t.Fatalf("GetAgent as workspace owner: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var ownerResp AgentResponse
	if err := json.Unmarshal(w.Body.Bytes(), &ownerResp); err != nil {
		t.Fatalf("decode owner response: %v", err)
	}
	if ownerResp.Instructions != "secret system prompt" {
		t.Fatalf("GetAgent as workspace owner: instructions = %q, want populated", ownerResp.Instructions)
	}
	if len(ownerResp.CustomArgs) != 1 || ownerResp.CustomArgs[0] != "--secret-flag" {
		t.Fatalf("GetAgent as workspace owner: custom_args = %#v, want populated", ownerResp.CustomArgs)
	}
	if rc, ok := ownerResp.RuntimeConfig.(map[string]any); !ok || rc["secret_key"] != "secret_value" {
		t.Fatalf("GetAgent as workspace owner: runtime_config = %#v, want populated", ownerResp.RuntimeConfig)
	}

	// Agent owner: 200, sees internals.
	w = httptest.NewRecorder()
	testHandler.GetAgent(w, withURLParam(newRequestAs(ownerID, "GET", "/api/agents/"+agentID, nil), "id", agentID))
	if w.Code != http.StatusOK {
		t.Fatalf("GetAgent as agent owner: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var agentOwnerResp AgentResponse
	if err := json.Unmarshal(w.Body.Bytes(), &agentOwnerResp); err != nil {
		t.Fatalf("decode agent-owner response: %v", err)
	}
	if agentOwnerResp.Instructions != "secret system prompt" {
		t.Fatalf("GetAgent as agent owner: instructions = %q, want populated", agentOwnerResp.Instructions)
	}

	// Plain member: 200 (existence unconditional), but internals redacted.
	w = httptest.NewRecorder()
	testHandler.GetAgent(w, withURLParam(newRequestAs(memberID, "GET", "/api/agents/"+agentID, nil), "id", agentID))
	if w.Code != http.StatusOK {
		t.Fatalf("GetAgent as plain member: expected 200 (existence unconditional post-#908), got %d: %s", w.Code, w.Body.String())
	}
	var memberResp AgentResponse
	if err := json.Unmarshal(w.Body.Bytes(), &memberResp); err != nil {
		t.Fatalf("decode plain-member response: %v", err)
	}
	if memberResp.Instructions != "" {
		t.Fatalf("GetAgent as plain member: instructions = %q, want redacted (empty)", memberResp.Instructions)
	}
	if len(memberResp.CustomArgs) != 0 {
		t.Fatalf("GetAgent as plain member: custom_args = %#v, want redacted (empty)", memberResp.CustomArgs)
	}
	if memberResp.RuntimeConfig != nil {
		t.Fatalf("GetAgent as plain member: runtime_config = %#v, want redacted (nil)", memberResp.RuntimeConfig)
	}
	if memberResp.MemoryGrowth != nil {
		t.Fatalf("GetAgent as plain member: memory_growth = %#v, want redacted (nil)", memberResp.MemoryGrowth)
	}
	if memberResp.ID != agentID || memberResp.DisplayName == "" {
		t.Fatalf("GetAgent as plain member: identity fields must still be populated, got %+v", memberResp)
	}
}

// TestListAgents_IncludesPrivateAgentsForAllMembers verifies that the
// workspace agents listing no longer hides private agents from plain members
// (task #908: every agent in a workspace is listable/mentionable by every
// member — "agent = colleague", Frank 2026-07-30 10:56 "所有的代码全部删掉，
// 默认public的"). Existence/listing is unconditional now; the four sensitive
// tabs (Activity/Files/Reminders/Usage) keep their own admin-or-owner gate
// separately and are not affected by this endpoint.
func TestListAgents_IncludesPrivateAgentsForAllMembers(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	agentID, _, memberID := privateAgentTestFixture(t)

	// Workspace owner sees the agent.
	w := httptest.NewRecorder()
	testHandler.ListAgents(w, newRequest("GET", "/api/agents", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("ListAgents as owner: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if !listContainsAgent(t, w.Body.Bytes(), agentID) {
		t.Fatalf("ListAgents as owner did not include private agent %s", agentID)
	}

	// Plain member also sees the agent now.
	w = httptest.NewRecorder()
	testHandler.ListAgents(w, newRequestAs(memberID, "GET", "/api/agents", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("ListAgents as plain member: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if !listContainsAgent(t, w.Body.Bytes(), agentID) {
		t.Fatalf("ListAgents as plain member did not include private agent %s (existence should be unconditional post-#908)", agentID)
	}
}

func TestPublishAgentVisibilityEventBroadcastsNormalAgent(t *testing.T) {
	workspaceID := "33333333-3333-3333-3333-333333333333"
	bus := events.New()
	h := &Handler{Bus: bus}
	var got []events.Event
	bus.SubscribeAll(func(e events.Event) {
		got = append(got, e)
	})

	h.publishAgentVisibilityEvent("agent:status", workspaceID, "member", "actor", db.Agent{
		OwnerID:     util.MustParseUUID("11111111-1111-1111-1111-111111111111"),
		DisplayName: "Researcher",
	}, map[string]any{"ok": true})
	if len(got) != 1 {
		t.Fatalf("published events = %d, want 1", len(got))
	}
	if got[0].RecipientUserIDs != nil {
		t.Fatalf("recipient user ids = %#v, want nil workspace broadcast", got[0].RecipientUserIDs)
	}
}

// TestPublishAgentVisibilityEventBroadcastsWendy proves the 2026-07-31 Wendy
// DM incident fix: a Wendy-named agent is no longer a special case here
// either — it broadcasts workspace-wide exactly like any other agent.
func TestPublishAgentVisibilityEventBroadcastsWendy(t *testing.T) {
	ownerUserID := "11111111-1111-1111-1111-111111111111"
	workspaceID := "33333333-3333-3333-3333-333333333333"
	bus := events.New()
	h := &Handler{Bus: bus}
	var got []events.Event
	bus.SubscribeAll(func(e events.Event) {
		got = append(got, e)
	})

	h.publishAgentVisibilityEvent("agent:status", workspaceID, "member", "actor", db.Agent{
		OwnerID:     util.MustParseUUID(ownerUserID),
		DisplayName: "Wendy",
	}, map[string]any{"ok": true})
	if len(got) != 1 {
		t.Fatalf("published events = %d, want 1", len(got))
	}
	if got[0].RecipientUserIDs != nil {
		t.Fatalf("recipient user ids = %#v, want nil workspace broadcast (no owner-only scoping for Wendy)", got[0].RecipientUserIDs)
	}
}

// TestWendyListedAndDetailFollowsGenericInternalsRule verifies task #908's
// full retirement: existence/listing is unconditional for every agent
// including Wendy (task #870/#902's personal onboarding agent), and Wendy's
// detail view no longer gets a name-based owner-only carve-out — she now
// follows the same generic canAccessAgentInternals rule as any other agent
// (owner OR workspace owner/admin). The old isWindyAgentName-driven
// "excludes even admin" exception was itself the display-name-inference
// anti-pattern task #902 exists to eliminate; batch 2 removes its last use.
func TestWendyListedAndDetailFollowsGenericInternalsRule(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	agentID, ownerID, memberID := privateAgentTestFixture(t)
	if _, err := testPool.Exec(context.Background(), `UPDATE agent SET display_name = 'Wendy', instructions = 'wendy secret prompt' WHERE id = $1`, agentID); err != nil {
		t.Fatalf("rename private agent to Wendy: %v", err)
	}

	w := httptest.NewRecorder()
	testHandler.ListAgents(w, newRequest("GET", "/api/agents", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("ListAgents as workspace owner: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if !listContainsAgent(t, w.Body.Bytes(), agentID) {
		t.Fatalf("ListAgents as workspace owner did not include another user's Wendy %s (existence should be unconditional post-#908)", agentID)
	}

	w = httptest.NewRecorder()
	testHandler.ListAgents(w, newRequestAs(ownerID, "GET", "/api/agents", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("ListAgents as Wendy owner: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if !listContainsAgent(t, w.Body.Bytes(), agentID) {
		t.Fatalf("ListAgents as Wendy owner did not include Wendy %s", agentID)
	}

	// Detail access is now unconditional (200) for everyone, same as any
	// other agent — including workspace admin, which the old name-based
	// carve-out used to specifically exclude for Wendy.
	w = httptest.NewRecorder()
	testHandler.GetAgent(w, withURLParam(newRequest("GET", "/api/agents/"+agentID, nil), "id", agentID))
	if w.Code != http.StatusOK {
		t.Fatalf("GetAgent as workspace owner for another user's Wendy: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var ownerResp AgentResponse
	if err := json.Unmarshal(w.Body.Bytes(), &ownerResp); err != nil {
		t.Fatalf("decode workspace-owner response: %v", err)
	}
	if ownerResp.Instructions != "wendy secret prompt" {
		t.Fatalf("GetAgent as workspace owner (admin role) for Wendy: instructions = %q, want populated — admin is no longer excluded", ownerResp.Instructions)
	}

	// A plain member (neither Wendy's owner nor workspace owner/admin) still
	// gets internals redacted.
	w = httptest.NewRecorder()
	testHandler.GetAgent(w, withURLParam(newRequestAs(memberID, "GET", "/api/agents/"+agentID, nil), "id", agentID))
	if w.Code != http.StatusOK {
		t.Fatalf("GetAgent as plain member for Wendy: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var memberResp AgentResponse
	if err := json.Unmarshal(w.Body.Bytes(), &memberResp); err != nil {
		t.Fatalf("decode plain-member response: %v", err)
	}
	if memberResp.Instructions != "" {
		t.Fatalf("GetAgent as plain member for Wendy: instructions = %q, want redacted", memberResp.Instructions)
	}
}

// TestPublishAgentReminderChangedAlwaysBroadcastsWorkspaceWide proves the
// 2026-07-31 Wendy DM incident fix: no agent, including the workspace's
// bound onboarding agent (Wendy), gets owner-only recipient scoping.
// Frank, #prj-daemon: "不要有特殊逻辑" — every agent's reminder-changed
// event broadcasts workspace-wide, whether or not it is Wendy-named or
// bound as workspace.onboarding_agent_id.
func TestPublishAgentReminderChangedAlwaysBroadcastsWorkspaceWide(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	agentID, _, _ := privateAgentTestFixture(t)
	if _, err := testPool.Exec(ctx, `UPDATE agent SET display_name = 'Wendy' WHERE id = $1`, agentID); err != nil {
		t.Fatalf("rename agent to Wendy: %v", err)
	}
	wsUUID := util.MustParseUUID(testWorkspaceID)
	agentUUID := util.MustParseUUID(agentID)

	events1 := captureReminderChangedEvents(t, testHandler, agentID)
	testHandler.publishAgentReminderChanged(ctx, wsUUID, agentUUID)
	if len(*events1) != 1 {
		t.Fatalf("unbound Wendy-named agent: got %d events, want 1", len(*events1))
	}
	if len((*events1)[0].RecipientUserIDs) != 0 {
		t.Fatalf("unbound Wendy-named agent: expected a workspace-wide broadcast (no explicit recipients), got %v", (*events1)[0].RecipientUserIDs)
	}

	// Bind it as the workspace's onboarding agent — the binding no longer
	// changes the scoping at all; it must still broadcast workspace-wide.
	if err := testHandler.Queries.SetWorkspaceOnboardingAgentID(ctx, db.SetWorkspaceOnboardingAgentIDParams{
		ID: wsUUID, OnboardingAgentID: agentUUID,
	}); err != nil {
		t.Fatalf("bind onboarding agent: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `UPDATE workspace SET onboarding_agent_id = NULL WHERE id = $1`, testWorkspaceID)
	})

	events2 := captureReminderChangedEvents(t, testHandler, agentID)
	testHandler.publishAgentReminderChanged(ctx, wsUUID, agentUUID)
	if len(*events2) != 1 {
		t.Fatalf("bound onboarding agent: got %d events, want 1", len(*events2))
	}
	if len((*events2)[0].RecipientUserIDs) != 0 {
		t.Fatalf("bound onboarding agent: expected a workspace-wide broadcast (no owner-only scoping), got %v", (*events2)[0].RecipientUserIDs)
	}
}

func listContainsAgent(t *testing.T, body []byte, agentID string) bool {
	t.Helper()
	var resp []AgentResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode ListAgents response: %v", err)
	}
	for _, a := range resp {
		if a.ID == agentID {
			return true
		}
	}
	return false
}

// TestListAgentTasks_PrivateAgentForbidsPlainMember verifies that the agent
// task history endpoint (the "查看历史会话" surface) is also gated.
func TestListAgentTasks_PrivateAgentForbidsPlainMember(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	agentID, ownerID, memberID := privateAgentTestFixture(t)

	w := httptest.NewRecorder()
	testHandler.ListAgentTasks(w, withURLParam(newRequestAs(ownerID, "GET", "/api/agents/"+agentID+"/tasks", nil), "id", agentID))
	if w.Code != http.StatusOK {
		t.Fatalf("ListAgentTasks as owner: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	testHandler.ListAgentTasks(w, withURLParam(newRequestAs(memberID, "GET", "/api/agents/"+agentID+"/tasks", nil), "id", agentID))
	if w.Code != http.StatusForbidden {
		t.Fatalf("ListAgentTasks as plain member: expected 403, got %d: %s", w.Code, w.Body.String())
	}
}

// TestCreateIssue_AssignToAgentUnconditionalPostBatch908 supersedes the old
// private-agent assignment gate: task #908 makes assigning an issue to any
// workspace agent unconditional for every member (Felix's option ① —
// "agent = colleague, seeing it should mean being able to work with it").
func TestCreateIssue_AssignToAgentUnconditionalPostBatch908(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	agentID, ownerID, memberID := privateAgentTestFixture(t)

	body := func(actorID string) map[string]any {
		return map[string]any{
			"title":         "assign-to-agent test " + actorID,
			"status":        "todo",
			"priority":      "medium",
			"assignee_type": "agent",
			"assignee_id":   agentID,
		}
	}

	// Workspace owner (testUserID): allowed.
	w := httptest.NewRecorder()
	testHandler.CreateIssue(w, newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, body(testUserID)))
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateIssue as workspace owner: expected 201, got %d: %s", w.Code, w.Body.String())
	}

	// Agent owner: allowed.
	w = httptest.NewRecorder()
	testHandler.CreateIssue(w, newRequestAs(ownerID, "POST", "/api/issues?workspace_id="+testWorkspaceID, body(ownerID)))
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateIssue as agent owner: expected 201, got %d: %s", w.Code, w.Body.String())
	}

	// Plain member: also allowed now — assignment no longer depends on
	// visibility/ownership, only on the agent existing in this workspace.
	w = httptest.NewRecorder()
	testHandler.CreateIssue(w, newRequestAs(memberID, "POST", "/api/issues?workspace_id="+testWorkspaceID, body(memberID)))
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateIssue as plain member: expected 201 (assignment unconditional post-#908), got %d: %s", w.Code, w.Body.String())
	}
}

// TestCreateChatSession_PrivateAgentForbidsPlainMember verifies that members
// who could not access a private agent are now allowed to start a chat
// session against it (task #908: chat is a "use" surface, unconditional for
// every workspace member). The chat handler reads workspace context from
// middleware, so we set it explicitly via middleware.SetMemberContext before
// invoking the handler (the test harness doesn't run the real middleware
// chain).
func TestCreateChatSession_UnconditionalPostBatch908(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	agentID, _, memberID := privateAgentTestFixture(t)

	// Load the plain member's row so we can build a realistic context.
	memberRow, err := testHandler.Queries.GetMemberByUserAndWorkspace(context.Background(), db.GetMemberByUserAndWorkspaceParams{
		UserID:      util.MustParseUUID(memberID),
		WorkspaceID: util.MustParseUUID(testWorkspaceID),
	})
	if err != nil {
		t.Fatalf("load plain member row: %v", err)
	}

	body := map[string]any{
		"agent_id": agentID,
		"title":    "should be allowed",
	}
	w := httptest.NewRecorder()
	req := newRequestAs(memberID, "POST", "/api/chat/sessions", body)
	req = req.WithContext(middleware.SetMemberContext(req.Context(), testWorkspaceID, memberRow))
	testHandler.CreateChatSession(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateChatSession as plain member: expected 201 (unconditional post-#908), got %d: %s", w.Code, w.Body.String())
	}
}

// TestGetAgent_ForgedAgentIDHeaderStillRedactsInternals is the task #908
// successor to the #2359 "X-Agent-ID can be forged" regression test. The
// original concern (a plain member setting X-Agent-ID to bypass the private
// gate) no longer applies to the endpoint's 200/403 status — GetAgent is
// unconditional now. What must still hold: canAccessAgentInternals grants
// its actorType=="agent" bypass only to a *genuine* agent actor (agent +
// valid X-Task-ID pair), not to a member who merely sets the header. A member
// setting X-Agent-ID without a valid X-Task-ID must fall back to member
// identity and still get Instructions/RuntimeConfig redacted.
func TestGetAgent_ForgedAgentIDHeaderStillRedactsInternals(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	agentID, _, memberID := privateAgentTestFixture(t)
	if _, err := testPool.Exec(context.Background(), `
		UPDATE agent SET instructions = 'secret system prompt' WHERE id = $1`, agentID); err != nil {
		t.Fatalf("seed instructions: %v", err)
	}

	w := httptest.NewRecorder()
	req := newRequestAs(memberID, "GET", "/api/agents/"+agentID, nil)
	// Forge X-Agent-ID without X-Task-ID. Pre-fix this would have made
	// resolveActor return ("agent", agentID) and canAccessAgentInternals
	// would have unconditionally allowed the internal fields through.
	req.Header.Set("X-Agent-ID", agentID)
	req = withURLParam(req, "id", agentID)
	testHandler.GetAgent(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GetAgent with forged X-Agent-ID: expected 200 (existence unconditional post-#908), got %d: %s", w.Code, w.Body.String())
	}
	var resp AgentResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Instructions != "" {
		t.Fatalf("GetAgent with forged X-Agent-ID (no valid X-Task-ID): instructions = %q, want redacted — resolveActor must still fall back to member identity", resp.Instructions)
	}
}

// TestListChatMessages_ReadableRegardlessOfAgentPrivacy supersedes the old
// #2359 "chat history read path doesn't re-gate" regression test — that test
// depended on a private agent being able to fall out of a member's reach,
// which task #908 retires (chat is unconditional now). The creator-only
// check in loadChatSessionForUser (unrelated to agent visibility) is what
// remains as the real boundary here; this asserts a session's own creator
// can still read it even when the target agent was created as "private".
func TestListChatMessages_ReadableRegardlessOfAgentPrivacy(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentID, _, memberID := privateAgentTestFixture(t)

	var sessionID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO chat_session (workspace_id, agent_id, creator_id, title, status)
		VALUES ($1, $2, $3, 'session', 'active')
		RETURNING id
	`, testWorkspaceID, agentID, memberID).Scan(&sessionID); err != nil {
		t.Fatalf("seed chat session: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM chat_session WHERE id = $1`, sessionID)
	})

	memberRow, err := testHandler.Queries.GetMemberByUserAndWorkspace(ctx, db.GetMemberByUserAndWorkspaceParams{
		UserID:      util.MustParseUUID(memberID),
		WorkspaceID: util.MustParseUUID(testWorkspaceID),
	})
	if err != nil {
		t.Fatalf("load plain member row: %v", err)
	}

	w := httptest.NewRecorder()
	req := newRequestAs(memberID, "GET", "/api/chat/sessions/"+sessionID+"/messages", nil)
	req = req.WithContext(middleware.SetMemberContext(req.Context(), testWorkspaceID, memberRow))
	req = withURLParam(req, "sessionId", sessionID)
	testHandler.ListChatMessages(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("ListChatMessages as the session's own creator: expected 200 (unconditional post-#908), got %d: %s", w.Code, w.Body.String())
	}
}

// TestMentionAgent_RejectsCrossWorkspaceAgentUUID is the regression test for
// the #2359 review finding "@mention path doesn't constrain the mentioned
// agent to the current workspace". A plain member in workspace A who happens
// to be owner of workspace B should NOT be able to @mention a private agent
// in workspace B from a comment on a workspace-A issue and have it pass the
// gate (the gate was being applied against the wrong workspace's roles).
func TestMentionAgent_RejectsCrossWorkspaceAgentUUID(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()

	// Create a separate workspace + agent runtime + private agent.
	var foreignWorkspaceID, foreignUserID, foreignRuntimeID, foreignAgentID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO "user" (name, email)
		VALUES ('Foreign Owner', 'cross-ws-foreign@multica.test')
		RETURNING id
	`).Scan(&foreignUserID); err != nil {
		t.Fatalf("create foreign user: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(),
			`DELETE FROM "user" WHERE email = 'cross-ws-foreign@multica.test'`)
	})

	if err := testPool.QueryRow(ctx, `
		INSERT INTO workspace (name, slug, description, issue_prefix)
		VALUES ('Cross-WS Foreign', 'cross-ws-foreign', '', 'XWF')
		RETURNING id
	`).Scan(&foreignWorkspaceID); err != nil {
		t.Fatalf("create foreign workspace: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(),
			`DELETE FROM workspace WHERE slug = 'cross-ws-foreign'`)
	})
	if _, err := testPool.Exec(ctx, `
		INSERT INTO member (workspace_id, user_id, role)
		VALUES ($1, $2, 'owner')
	`, foreignWorkspaceID, foreignUserID); err != nil {
		t.Fatalf("add foreign member: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_runtime (workspace_id, daemon_id, name, runtime_mode, provider, status, device_info, metadata, last_seen_at)
		VALUES ($1, NULL, 'Foreign Runtime', 'cloud', 'foreign_test', 'online', 'Foreign', '{}'::jsonb, now())
		RETURNING id
	`, foreignWorkspaceID).Scan(&foreignRuntimeID); err != nil {
		t.Fatalf("create foreign runtime: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent (
			workspace_id, name, description, runtime_mode, runtime_config, runtime_id, max_concurrent_tasks, owner_id, instructions, custom_env, custom_args
		, model) VALUES ($1, 'foreign-private-agent', '', 'cloud', '{}'::jsonb, $2, 1, $3, '', '{}'::jsonb, '[]'::jsonb, 'composer-1.5')
		RETURNING id
	`, foreignWorkspaceID, foreignRuntimeID, foreignUserID).Scan(&foreignAgentID); err != nil {
		t.Fatalf("create foreign agent: %v", err)
	}

	// Create an issue in OUR workspace and a comment that @mentions the
	// foreign agent's UUID. testUserID is owner of our workspace; pre-fix
	// the gate would have applied our-workspace-owner status to the foreign
	// agent and enqueued a task.
	var issueID, commentID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO issue (workspace_id, title, status, priority, creator_type, creator_id, number)
		VALUES ($1, 'cross-ws mention test', 'todo', 'medium', 'member', $2,
		        COALESCE((SELECT MAX(number) FROM issue WHERE workspace_id = $1), 0) + 1)
		RETURNING id
	`, testWorkspaceID, testUserID).Scan(&issueID); err != nil {
		t.Fatalf("create test issue: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM issue WHERE id = $1`, issueID)
	})

	// Multica's mention format is markdown-linked: [@Name](mention://agent/<uuid>).
	mention := "[@Foreign](mention://agent/" + foreignAgentID + ")"
	if err := testPool.QueryRow(ctx, `
		INSERT INTO comment (workspace_id, issue_id, author_type, author_id, content)
		VALUES ($1, $2, 'member', $3, $4)
		RETURNING id
	`, testWorkspaceID, issueID, testUserID, mention).Scan(&commentID); err != nil {
		t.Fatalf("create test comment: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM comment WHERE id = $1`, commentID)
	})

	issue, err := testHandler.Queries.GetIssue(ctx, util.MustParseUUID(issueID))
	if err != nil {
		t.Fatalf("load test issue: %v", err)
	}
	comment, err := testHandler.Queries.GetComment(ctx, util.MustParseUUID(commentID))
	if err != nil {
		t.Fatalf("load test comment: %v", err)
	}

	// Count tasks for the foreign agent before. Calling the dispatcher
	// directly bypasses HTTP-layer concerns and exercises only the
	// workspace-scoping check.
	var beforeCount int
	if err := testPool.QueryRow(ctx,
		`SELECT COUNT(*) FROM agent_inbox_event WHERE agent_id = $1`,
		foreignAgentID,
	).Scan(&beforeCount); err != nil {
		t.Fatalf("count tasks before: %v", err)
	}

	enqueueMentionedAgentTasksForTest(t, ctx, issue, comment, nil, "member", testUserID)

	var afterCount int
	if err := testPool.QueryRow(ctx,
		`SELECT COUNT(*) FROM agent_inbox_event WHERE agent_id = $1`,
		foreignAgentID,
	).Scan(&afterCount); err != nil {
		t.Fatalf("count tasks after: %v", err)
	}
	if afterCount != beforeCount {
		t.Fatalf("foreign agent task count changed: before=%d after=%d — cross-workspace mention was not rejected",
			beforeCount, afterCount)
	}
}

// TestShouldEnqueueOnComment_UnconditionalPostBatch908 supersedes GH #3300's
// visibility gate: task #908 makes issue-comment dispatch unconditional for
// every workspace member, same as chat/@mention/assignment. Once an agent is
// assigned to an issue, any member commenting on it should be able to
// trigger it — that's the "agent = colleague" principle applied to this
// surface. This test now asserts the gate never blocks based on actor.
func TestShouldEnqueueOnComment_UnconditionalPostBatch908(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentID, _, _ := privateAgentTestFixture(t)

	// Assign the agent to a fresh issue.
	var issueID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO issue (workspace_id, title, status, priority, creator_type, creator_id,
		                   assignee_type, assignee_id, number)
		VALUES ($1, 'on_comment dispatch test', 'todo', 'medium', 'member', $2,
		        'agent', $3,
		        COALESCE((SELECT MAX(number) FROM issue WHERE workspace_id = $1), 0) + 1)
		RETURNING id
	`, testWorkspaceID, testUserID, agentID).Scan(&issueID); err != nil {
		t.Fatalf("create issue assigned to agent: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM issue WHERE id = $1`, issueID)
	})

	issue, err := testHandler.Queries.GetIssue(ctx, util.MustParseUUID(issueID))
	if err != nil {
		t.Fatalf("load issue: %v", err)
	}

	if !testHandler.shouldEnqueueOnComment(ctx, issue) {
		t.Fatal("shouldEnqueueOnComment: want true for a runtime-bound, non-archived assigned agent — visibility no longer gates this surface")
	}
}
