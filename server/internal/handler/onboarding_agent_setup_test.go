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
	t.Cleanup(func() { _, _ = testPool.Exec(context.Background(), `DELETE FROM agent WHERE id = $1`, agentID) })

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

func TestEnsureWindy_AdoptsOneLegacyCandidateWithoutChangingRuntimeOrModel(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	resetTestWorkspaceOnboardingAgent(t, ctx)
	_ = ensureSystemGeneralForTest(t)
	var legacyID string
	const legacyModel = "legacy-model-must-survive"
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent (workspace_id, name, display_name, runtime_mode, runtime_config,
			runtime_id, max_concurrent_tasks, owner_id, model)
		VALUES ($1, $2, 'Windy', 'local', '{}'::jsonb, $3, 1, $4, $5)
		RETURNING id`, testWorkspaceID, "legacy-setup-"+uuid.NewString(), handlerTestRuntimeID(t), testUserID, legacyModel).Scan(&legacyID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = testPool.Exec(context.Background(), `DELETE FROM agent WHERE id = $1`, legacyID) })

	rec := httptest.NewRecorder()
	req := newRequestAs(testUserID, http.MethodPost, "/api/agents/windy", map[string]string{
		"runtime_id": testRuntimeID, "model": "new-model-must-not-overwrite",
	})
	req.Header.Set("X-Workspace-ID", testWorkspaceID)
	testHandler.EnsureWindy(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("adopt=%d body=%s", rec.Code, rec.Body.String())
	}
	var gotModel, gotRuntime string
	if err := testPool.QueryRow(ctx, `SELECT model, runtime_id FROM agent WHERE id = $1`, legacyID).Scan(&gotModel, &gotRuntime); err != nil {
		t.Fatal(err)
	}
	if gotModel != legacyModel || gotRuntime != handlerTestRuntimeID(t) {
		t.Fatalf("adoption changed model/runtime: %q %q", gotModel, gotRuntime)
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
	_, _, err = testHandler.provisionOnboardingAgent(ctx, parseUUID(testWorkspaceID), runtime, "rollback-model")
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
