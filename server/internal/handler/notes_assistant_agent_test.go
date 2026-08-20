package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEnsureNotesAssistantAgent_SoftProbeNeedsSetupThenClone(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	_ = ensureSystemGeneralForTest(t)
	resetTestWorkspaceOnboardingAgent(t, ctx)

	_, _ = testPool.Exec(ctx, `DELETE FROM agent WHERE workspace_id = $1 AND name = $2`, testWorkspaceID, notesAssistantAgentName)

	onboardingID := createHandlerTestAgent(t, "notes-onboarding-src", nil)
	if _, err := testPool.Exec(ctx, `UPDATE agent SET model = $2 WHERE id = $1`, onboardingID, "notes-assistant-model"); err != nil {
		t.Fatalf("set onboarding model: %v", err)
	}
	if _, err := testPool.Exec(ctx, `UPDATE workspace SET onboarding_agent_id = $2 WHERE id = $1`, testWorkspaceID, onboardingID); err != nil {
		t.Fatalf("bind onboarding: %v", err)
	}

	call := func(body any) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := newRequestAs(testUserID, http.MethodPost, "/api/agents/notes-assistant", body)
		req.Header.Set("X-Workspace-ID", testWorkspaceID)
		testHandler.EnsureNotesAssistantAgent(rec, req)
		return rec
	}

	soft := call(map[string]any{})
	if soft.Code != http.StatusOK {
		t.Fatalf("soft probe=%d body=%s", soft.Code, soft.Body.String())
	}
	var probe EnsureNotesAssistantAgentResponse
	if err := json.Unmarshal(soft.Body.Bytes(), &probe); err != nil {
		t.Fatal(err)
	}
	if !probe.NeedsSetup || probe.Agent != nil || !probe.OnboardingAvailable {
		t.Fatalf("expected needs_setup with onboarding, got %+v", probe)
	}

	rec := call(map[string]any{"clone_onboarding": true})
	if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
		t.Fatalf("clone create=%d body=%s", rec.Code, rec.Body.String())
	}
	var created EnsureNotesAssistantAgentResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if !created.Created || created.Agent == nil {
		t.Fatalf("expected created agent, got %+v", created)
	}
	if created.Agent.Name != notesAssistantAgentName {
		t.Fatalf("name=%q", created.Agent.Name)
	}
	if created.Agent.Model != "notes-assistant-model" {
		t.Fatalf("model=%q", created.Agent.Model)
	}
	if !strings.Contains(created.Agent.Instructions, notesAssistantInstructionsCapabilityMarker) {
		t.Fatal("expected selective-read instructions")
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM agent WHERE id = $1`, created.Agent.ID)
	})

	again := call(map[string]any{})
	if again.Code != http.StatusOK {
		t.Fatalf("idempotent ensure=%d body=%s", again.Code, again.Body.String())
	}
	var existing EnsureNotesAssistantAgentResponse
	if err := json.Unmarshal(again.Body.Bytes(), &existing); err != nil {
		t.Fatal(err)
	}
	if existing.Created || existing.NeedsSetup || existing.Agent == nil || existing.Agent.ID != created.Agent.ID {
		t.Fatalf("idempotent mismatch: %+v", existing)
	}
}

func TestEnsureNotesAssistantAgent_RestoresArchivedOnClone(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	_ = ensureSystemGeneralForTest(t)
	resetTestWorkspaceOnboardingAgent(t, ctx)
	_, _ = testPool.Exec(ctx, `DELETE FROM agent WHERE workspace_id = $1 AND name = $2`, testWorkspaceID, notesAssistantAgentName)

	onboardingID := createHandlerTestAgent(t, "notes-onboarding-restore", nil)
	if _, err := testPool.Exec(ctx, `UPDATE agent SET model = $2 WHERE id = $1`, onboardingID, "restore-model"); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(ctx, `UPDATE workspace SET onboarding_agent_id = $2 WHERE id = $1`, testWorkspaceID, onboardingID); err != nil {
		t.Fatal(err)
	}

	createRec := httptest.NewRecorder()
	createReq := newRequestAs(testUserID, http.MethodPost, "/api/agents/notes-assistant", map[string]any{
		"clone_onboarding": true,
	})
	createReq.Header.Set("X-Workspace-ID", testWorkspaceID)
	testHandler.EnsureNotesAssistantAgent(createRec, createReq)
	if createRec.Code != http.StatusCreated && createRec.Code != http.StatusOK {
		t.Fatalf("create=%d body=%s", createRec.Code, createRec.Body.String())
	}
	var created EnsureNotesAssistantAgentResponse
	if err := json.Unmarshal(createRec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Agent == nil {
		t.Fatal("expected agent")
	}
	agentID := created.Agent.ID
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM agent WHERE id = $1`, agentID)
	})

	if _, err := testPool.Exec(ctx, `UPDATE agent SET archived_at = now(), archived_by = $2 WHERE id = $1`, agentID, testUserID); err != nil {
		t.Fatalf("archive: %v", err)
	}

	soft := httptest.NewRecorder()
	softReq := newRequestAs(testUserID, http.MethodPost, "/api/agents/notes-assistant", map[string]any{})
	softReq.Header.Set("X-Workspace-ID", testWorkspaceID)
	testHandler.EnsureNotesAssistantAgent(soft, softReq)
	var probe EnsureNotesAssistantAgentResponse
	_ = json.Unmarshal(soft.Body.Bytes(), &probe)
	if soft.Code != http.StatusOK || !probe.NeedsSetup || probe.Agent != nil {
		t.Fatalf("archived soft probe should needs_setup, got %d %+v", soft.Code, probe)
	}

	restoreRec := httptest.NewRecorder()
	restoreReq := newRequestAs(testUserID, http.MethodPost, "/api/agents/notes-assistant", map[string]any{
		"clone_onboarding": true,
	})
	restoreReq.Header.Set("X-Workspace-ID", testWorkspaceID)
	testHandler.EnsureNotesAssistantAgent(restoreRec, restoreReq)
	if restoreRec.Code != http.StatusOK {
		t.Fatalf("restore=%d body=%s", restoreRec.Code, restoreRec.Body.String())
	}
	var restored EnsureNotesAssistantAgentResponse
	if err := json.Unmarshal(restoreRec.Body.Bytes(), &restored); err != nil {
		t.Fatal(err)
	}
	if restored.Agent == nil || restored.Agent.ID != agentID || !restored.Created {
		t.Fatalf("expected restored same id, got %+v", restored)
	}
	var archivedAt any
	if err := testPool.QueryRow(ctx, `SELECT archived_at FROM agent WHERE id = $1`, agentID).Scan(&archivedAt); err != nil {
		t.Fatal(err)
	}
	if archivedAt != nil {
		t.Fatalf("expected archived_at NULL, got %v", archivedAt)
	}
}

func TestEnsureNotesAssistantAgent_NeedsSetupWithoutOnboarding(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	resetTestWorkspaceOnboardingAgent(t, ctx)
	_, _ = testPool.Exec(ctx, `DELETE FROM agent WHERE workspace_id = $1 AND name = $2`, testWorkspaceID, notesAssistantAgentName)

	rec := httptest.NewRecorder()
	req := newRequestAs(testUserID, http.MethodPost, "/api/agents/notes-assistant", map[string]any{})
	req.Header.Set("X-Workspace-ID", testWorkspaceID)
	testHandler.EnsureNotesAssistantAgent(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp EnsureNotesAssistantAgentResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.NeedsSetup || resp.Agent != nil {
		t.Fatalf("expected needs_setup without agent, got %+v", resp)
	}
}

func TestEnsureNotesAssistantAgent_ExplicitRuntimeCreate(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	_ = ensureSystemGeneralForTest(t)
	resetTestWorkspaceOnboardingAgent(t, ctx)
	_, _ = testPool.Exec(ctx, `DELETE FROM agent WHERE workspace_id = $1 AND name = $2`, testWorkspaceID, notesAssistantAgentName)

	rec := httptest.NewRecorder()
	req := newRequestAs(testUserID, http.MethodPost, "/api/agents/notes-assistant", map[string]string{
		"runtime_id": testRuntimeID,
		"model":      "explicit-notes-model",
	})
	req.Header.Set("X-Workspace-ID", testWorkspaceID)
	testHandler.EnsureNotesAssistantAgent(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create=%d body=%s", rec.Code, rec.Body.String())
	}
	var created EnsureNotesAssistantAgentResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if !created.Created || created.Agent == nil || created.Agent.Model != "explicit-notes-model" {
		t.Fatalf("unexpected create response: %+v", created)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM agent WHERE id = $1`, created.Agent.ID)
	})
}

func TestAgentTemplatesIncludeNotesAssistant(t *testing.T) {
	tmpl, ok := agentTemplates.Get(notesAssistantAgentTemplate)
	if !ok {
		t.Fatal("notes-assistant template missing from agenttmpl registry")
	}
	if tmpl.Name == "" || tmpl.Instructions == "" {
		t.Fatalf("notes-assistant template incomplete: %+v", tmpl)
	}
	for _, want := range []string{
		"multica-notes-assistant",
		"multica-period-work-brief",
		notesAssistantInstructionsCapabilityMarker,
		"Standalone Agent Chat",
		"notes tree",
		"notes get",
		"final assistant output",
		"Never run `multica message send`",
	} {
		if !strings.Contains(tmpl.Instructions, want) {
			t.Fatalf("notes-assistant instructions missing %q", want)
		}
	}
	for _, banned := range []string{
		"message send --target chat:",
	} {
		if strings.Contains(tmpl.Instructions, banned) {
			t.Fatalf("notes-assistant instructions must not teach %q", banned)
		}
	}
}
