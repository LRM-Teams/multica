package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEnsurePeriodBriefAgent_IdempotentCreateAndResolve(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	_ = ensureSystemGeneralForTest(t)

	// Clean any leftover weekly-report from a prior run in the shared test workspace.
	_, _ = testPool.Exec(ctx, `DELETE FROM agent WHERE workspace_id = $1 AND name = $2`, testWorkspaceID, periodBriefAgentName)

	call := func(body map[string]string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := newRequestAs(testUserID, http.MethodPost, "/api/agents/period-brief", body)
		req.Header.Set("X-Workspace-ID", testWorkspaceID)
		testHandler.EnsurePeriodBriefAgent(rec, req)
		return rec
	}

	if rec := call(map[string]string{"runtime_id": testRuntimeID}); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing model=%d body=%s", rec.Code, rec.Body.String())
	}

	rec := call(map[string]string{"runtime_id": testRuntimeID, "model": "period-brief-model"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create=%d body=%s", rec.Code, rec.Body.String())
	}
	var created EnsurePeriodBriefAgentResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if !created.Created {
		t.Fatal("expected created=true on first ensure")
	}
	if created.Agent.Name != periodBriefAgentName {
		t.Fatalf("name=%q want %q", created.Agent.Name, periodBriefAgentName)
	}
	if created.Agent.DisplayName != periodBriefAgentDisplayName {
		t.Fatalf("display_name=%q want %q", created.Agent.DisplayName, periodBriefAgentDisplayName)
	}
	if created.Agent.Model != "period-brief-model" {
		t.Fatalf("model=%q", created.Agent.Model)
	}
	if created.Agent.Instructions == "" {
		t.Fatal("expected template instructions")
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM agent WHERE id = $1`, created.Agent.ID)
	})

	again := call(map[string]string{"runtime_id": testRuntimeID, "model": "other-model"})
	if again.Code != http.StatusOK {
		t.Fatalf("idempotent ensure=%d body=%s", again.Code, again.Body.String())
	}
	var existing EnsurePeriodBriefAgentResponse
	if err := json.Unmarshal(again.Body.Bytes(), &existing); err != nil {
		t.Fatal(err)
	}
	if existing.Created {
		t.Fatal("expected created=false on second ensure")
	}
	if existing.Agent.ID != created.Agent.ID {
		t.Fatalf("id=%q want %q", existing.Agent.ID, created.Agent.ID)
	}
	if existing.Agent.Model != "period-brief-model" {
		t.Fatalf("idempotent must not rewrite model, got %q", existing.Agent.Model)
	}
}

func TestAgentTemplatesIncludeWeeklyReport(t *testing.T) {
	tmpl, ok := agentTemplates.Get(periodBriefAgentTemplate)
	if !ok {
		t.Fatal("weekly-report template missing from agenttmpl registry")
	}
	if tmpl.Name == "" || tmpl.Instructions == "" {
		t.Fatalf("weekly-report template incomplete: %+v", tmpl)
	}
	for _, want := range []string{
		"filesystem path",
		"Mermaid",
		"nested sub-points",
		"multica-period-work-brief",
		periodBriefInstructionsCapabilityMarker,
	} {
		if !strings.Contains(tmpl.Instructions, want) {
			t.Fatalf("weekly-report instructions missing %q", want)
		}
	}
}

func TestEnsurePeriodBriefAgent_RefreshesStaleInstructions(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	_ = ensureSystemGeneralForTest(t)

	_, _ = testPool.Exec(ctx, `DELETE FROM agent WHERE workspace_id = $1 AND name = $2`, testWorkspaceID, periodBriefAgentName)

	rec := httptest.NewRecorder()
	req := newRequestAs(testUserID, http.MethodPost, "/api/agents/period-brief", map[string]string{
		"runtime_id": testRuntimeID,
		"model":      "period-brief-model",
	})
	req.Header.Set("X-Workspace-ID", testWorkspaceID)
	testHandler.EnsurePeriodBriefAgent(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create=%d body=%s", rec.Code, rec.Body.String())
	}
	var created EnsurePeriodBriefAgentResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM agent WHERE id = $1`, created.Agent.ID)
	})

	stale := "You are the Workspace Period Brief Agent. Give ≤3 evidence bullets per thread. Mermaid is optional."
	if strings.Contains(stale, periodBriefInstructionsCapabilityMarker) {
		t.Fatal("stale fixture unexpectedly contains capability marker")
	}
	if _, err := testPool.Exec(ctx, `UPDATE agent SET instructions = $1 WHERE id = $2`, stale, created.Agent.ID); err != nil {
		t.Fatal(err)
	}

	again := httptest.NewRecorder()
	req2 := newRequestAs(testUserID, http.MethodPost, "/api/agents/period-brief", map[string]string{
		"runtime_id": testRuntimeID,
		"model":      "other-model",
	})
	req2.Header.Set("X-Workspace-ID", testWorkspaceID)
	testHandler.EnsurePeriodBriefAgent(again, req2)
	if again.Code != http.StatusOK {
		t.Fatalf("ensure=%d body=%s", again.Code, again.Body.String())
	}
	var existing EnsurePeriodBriefAgentResponse
	if err := json.Unmarshal(again.Body.Bytes(), &existing); err != nil {
		t.Fatal(err)
	}
	if existing.Created {
		t.Fatal("expected created=false")
	}
	if !strings.Contains(existing.Agent.Instructions, periodBriefInstructionsCapabilityMarker) {
		t.Fatalf("stale instructions were not refreshed:\n%s", existing.Agent.Instructions)
	}
	if strings.Contains(existing.Agent.Instructions, "≤3 evidence bullets") {
		t.Fatal("old 3-bullet flattening contract must not remain after refresh")
	}
}
