package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestEnsureWindy_RequiresOwnerExplicitRuntimeAndModelThenSeedsGeneral(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	resetTestWorkspaceOnboardingAgent(t, ctx)
	_ = ensureSystemGeneralForTest(t)
	adminSuffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	adminID := createWorkspaceMemberUser(t, "Setup Admin "+adminSuffix, "setup-admin-"+adminSuffix+"@multica.test")
	if _, err := testPool.Exec(ctx, `UPDATE member SET role = 'admin' WHERE workspace_id = $1 AND user_id = $2`, testWorkspaceID, adminID); err != nil {
		t.Fatal(err)
	}

	call := func(userID string, body map[string]string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := newRequestAs(userID, http.MethodPost, "/api/agents/windy", body)
		req.Header.Set("X-Workspace-ID", testWorkspaceID)
		testHandler.EnsureWindy(rec, req)
		return rec
	}
	if rec := call(adminID, map[string]string{"runtime_id": testRuntimeID, "model": "composer-1.5"}); rec.Code != http.StatusForbidden {
		t.Fatalf("admin setup=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec := call(testUserID, map[string]string{"runtime_id": testRuntimeID}); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing model=%d body=%s", rec.Code, rec.Body.String())
	}
	rec := call(testUserID, map[string]string{"runtime_id": testRuntimeID, "model": "explicit-setup-model"})
	if rec.Code != http.StatusOK {
		t.Fatalf("owner setup=%d body=%s", rec.Code, rec.Body.String())
	}
	var response WindyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	agentID := response.Agent.ID
	if response.Agent.Model != "explicit-setup-model" {
		t.Fatalf("setup model=%v", response.Agent.Model)
	}
	var createdOwnerID string
	if err := testPool.QueryRow(ctx, `SELECT owner_id::text FROM agent WHERE id = $1`, agentID).Scan(&createdOwnerID); err != nil {
		t.Fatal(err)
	}
	if createdOwnerID != testUserID {
		t.Fatalf("onboarding Agent owner_id = %q, want creator %q", createdOwnerID, testUserID)
	}
	t.Cleanup(func() { _, _ = testPool.Exec(context.Background(), `DELETE FROM agent WHERE id = $1`, agentID) })

	var launchRuntimeID string
	if err := testPool.QueryRow(ctx, `SELECT runtime_id::text FROM agent WHERE id = $1`, agentID).Scan(&launchRuntimeID); err != nil {
		t.Fatalf("load Wendy desired Runtime: %v", err)
	}
	if launchRuntimeID != testRuntimeID {
		t.Fatalf("Wendy desired Runtime = %q", launchRuntimeID)
	}

	var membershipCount, welcomeCount int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM channel_member cm
		JOIN channel c ON c.id = cm.channel_id
		WHERE c.workspace_id = $1 AND c.system_key = 'general'
		  AND cm.member_type = 'agent' AND cm.member_id = $2`, testWorkspaceID, agentID).Scan(&membershipCount); err != nil {
		t.Fatal(err)
	}
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM channel_message m
		JOIN channel c ON c.id = m.channel_id
		WHERE c.workspace_id = $1 AND c.system_key = 'general'
		  AND m.author_type = 'agent' AND m.author_id = $2
		  AND m.content = ANY($3::text[])`, testWorkspaceID, agentID, onboardingWelcomeV1).Scan(&welcomeCount); err != nil {
		t.Fatal(err)
	}
	if membershipCount != 1 || welcomeCount != len(onboardingWelcomeV1) {
		t.Fatalf("general setup membership=%d welcome=%d", membershipCount, welcomeCount)
	}

	second := call(testUserID, map[string]string{"runtime_id": "not-a-runtime", "model": "ignored-on-retry"})
	if second.Code != http.StatusOK {
		t.Fatalf("idempotent retry=%d body=%s", second.Code, second.Body.String())
	}
	var agentCount int
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM agent WHERE workspace_id = $1 AND id = $2`, testWorkspaceID, agentID).Scan(&agentCount); err != nil {
		t.Fatal(err)
	}
	if agentCount != 1 {
		t.Fatalf("idempotent setup agent count=%d", agentCount)
	}
}

func TestEnsureWindy_IdempotentRetryPreservesDesiredLaunch(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	resetTestWorkspaceOnboardingAgent(t, ctx)

	runtimeID := handlerTestRuntimeID(t)
	var agentID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent (
			workspace_id, name, display_name, runtime_mode, runtime_config,
			runtime_id, max_concurrent_tasks, owner_id, model
		) VALUES ($1, $2, 'Alice', 'local', '{}'::jsonb, $3, 6, $4, 'repair-model')
		RETURNING id`, testWorkspaceID, "wendy-repair-"+uuid.NewString(), runtimeID, testUserID).Scan(&agentID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = testPool.Exec(context.Background(), `DELETE FROM agent WHERE id = $1`, agentID) })
	if _, err := testPool.Exec(ctx, `UPDATE workspace SET onboarding_agent_id = $2 WHERE id = $1`, testWorkspaceID, agentID); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	req := newRequestAs(testUserID, http.MethodPost, "/api/agents/windy", map[string]string{})
	req.Header.Set("X-Workspace-ID", testWorkspaceID)
	testHandler.EnsureWindy(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("idempotent repair=%d body=%s", rec.Code, rec.Body.String())
	}
	var response WindyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Agent.DisplayName != "Alice" {
		t.Fatalf("bound onboarding Agent was renamed from Alice to %q", response.Agent.DisplayName)
	}

	var gotRuntimeID string
	if err := testPool.QueryRow(ctx, `SELECT runtime_id::text FROM agent WHERE id = $1`, agentID).Scan(&gotRuntimeID); err != nil {
		t.Fatalf("load Wendy desired Runtime: %v", err)
	}
	if gotRuntimeID != runtimeID {
		t.Fatalf("Wendy desired Runtime = %q", gotRuntimeID)
	}
}

func TestEnsureWindy_ValidatesAndPersistsThinkingLevel(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	resetTestWorkspaceOnboardingAgent(t, ctx)
	_ = ensureSystemGeneralForTest(t)
	runtimeID := createCodexProviderRuntime(t)

	call := func(thinkingLevel string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := newRequestAs(testUserID, http.MethodPost, "/api/agents/windy", map[string]string{
			"runtime_id": runtimeID, "model": "gpt-5.6-sol", "thinking_level": thinkingLevel,
		})
		req.Header.Set("X-Workspace-ID", testWorkspaceID)
		testHandler.EnsureWindy(rec, req)
		return rec
	}

	if rec := call("supersonic"); rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid thinking_level=%d body=%s", rec.Code, rec.Body.String())
	}
	rec := call("high")
	if rec.Code != http.StatusOK {
		t.Fatalf("valid thinking_level=%d body=%s", rec.Code, rec.Body.String())
	}
	var response WindyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM agent WHERE id = $1`, response.Agent.ID)
	})
	if response.Agent.ThinkingLevel != "high" {
		t.Fatalf("response thinking_level=%q", response.Agent.ThinkingLevel)
	}
	var stored string
	if err := testPool.QueryRow(ctx, `SELECT COALESCE(thinking_level, '') FROM agent WHERE id = $1`, response.Agent.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != "high" {
		t.Fatalf("stored thinking_level=%q", stored)
	}
}

func TestEnsureWindy_DoesNotInferOnboardingIdentityFromAgentName(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	resetTestWorkspaceOnboardingAgent(t, ctx)
	_ = ensureSystemGeneralForTest(t)
	var ordinaryAgentID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent (workspace_id, name, display_name, runtime_mode, runtime_config,
			runtime_id, max_concurrent_tasks, owner_id, model)
		VALUES ($1, $2, 'Wendy', 'local', '{}'::jsonb, $3, 1, $4, 'ordinary-agent-model')
		RETURNING id`, testWorkspaceID, "ordinary-wendy-"+uuid.NewString(), handlerTestRuntimeID(t), testUserID).Scan(&ordinaryAgentID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = testPool.Exec(context.Background(), `DELETE FROM agent WHERE id = $1`, ordinaryAgentID) })

	rec := httptest.NewRecorder()
	req := newRequestAs(testUserID, http.MethodPost, "/api/agents/windy", map[string]string{
		"runtime_id": testRuntimeID, "model": "onboarding-model",
	})
	req.Header.Set("X-Workspace-ID", testWorkspaceID)
	testHandler.EnsureWindy(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("setup=%d body=%s", rec.Code, rec.Body.String())
	}
	var response WindyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Agent.ID == ordinaryAgentID {
		t.Fatalf("ordinary Agent named Wendy was adopted as onboarding Agent: %s", ordinaryAgentID)
	}
	var boundID string
	if err := testPool.QueryRow(ctx, `SELECT onboarding_agent_id::text FROM workspace WHERE id = $1`, testWorkspaceID).Scan(&boundID); err != nil {
		t.Fatal(err)
	}
	if boundID != response.Agent.ID {
		t.Fatalf("onboarding binding = %q, want newly created Agent %q", boundID, response.Agent.ID)
	}
	var ordinaryModel string
	if err := testPool.QueryRow(ctx, `SELECT model FROM agent WHERE id = $1`, ordinaryAgentID).Scan(&ordinaryModel); err != nil {
		t.Fatal(err)
	}
	if ordinaryModel != "ordinary-agent-model" {
		t.Fatalf("ordinary Wendy-named Agent was mutated: model=%q", ordinaryModel)
	}
}

func TestProvisionOnboardingAgent_RollsBackWholeSetupOnWelcomeFailure(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	resetTestWorkspaceOnboardingAgent(t, ctx)
	_ = ensureSystemGeneralForTest(t)
	triggerName := "reject_wendy_welcome_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := testPool.Exec(ctx, `CREATE FUNCTION `+triggerName+`() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'welcome rejected'; END $$`); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(ctx, `CREATE TRIGGER `+triggerName+` BEFORE INSERT ON channel_message FOR EACH ROW WHEN (NEW.author_type = 'agent' AND NEW.content LIKE 'Hi — I’m your Workspace Onboarding Agent.%') EXECUTE FUNCTION `+triggerName+`()`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DROP TRIGGER IF EXISTS `+triggerName+` ON channel_message`)
		_, _ = testPool.Exec(context.Background(), `DROP FUNCTION IF EXISTS `+triggerName+`()`)
	})

	runtime, err := testHandler.Queries.GetAgentRuntime(ctx, parseUUID(testRuntimeID))
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = testHandler.provisionOnboardingAgent(ctx, parseUUID(testWorkspaceID), parseUUID(testUserID), runtime, "rollback-model", "")
	if err == nil {
		t.Fatal("setup unexpectedly succeeded")
	}
	var binding *string
	if err := testPool.QueryRow(ctx, `SELECT onboarding_agent_id::text FROM workspace WHERE id = $1`, testWorkspaceID).Scan(&binding); err != nil {
		t.Fatal(err)
	}
	if binding != nil {
		t.Fatalf("binding survived failed transaction: %v", binding)
	}
	var count int
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM agent WHERE workspace_id = $1 AND model = 'rollback-model'`, testWorkspaceID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("created agent survived failed transaction: %d", count)
	}
}
