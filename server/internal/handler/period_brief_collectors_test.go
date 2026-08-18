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

func TestEnsurePeriodBriefCollectors_CreatesPerLocalComputer(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	_ = ensureSystemGeneralForTest(t)

	daemonA := "pc-daemon-" + uuid.NewString()[:8]
	daemonB := "pc-daemon-" + uuid.NewString()[:8]
	runtimeA := seedMachineLockedRuntime(t, daemonA, "Laptop A")
	runtimeB := seedMachineLockedRuntime(t, daemonB, "Laptop B")
	// Second runtime on the same Computer must not create a second collector.
	_ = seedMachineLockedRuntime(t, daemonA, "Laptop A Twin")

	var cloudRuntimeID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_runtime (
			workspace_id, name, display_name, runtime_mode, provider, status, device_info, metadata, visibility, owner_id, last_seen_at
		) VALUES ($1, 'cloud-box', 'Cloud Box', 'cloud', $2, 'online', '', '{}'::jsonb, 'public', $3, now())
		RETURNING id
	`, testWorkspaceID, "cloud_collect_"+uuid.NewString(), testUserID).Scan(&cloudRuntimeID); err != nil {
		t.Fatalf("seed cloud runtime: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE id = $1`, cloudRuntimeID)
	})

	call := func() *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := newRequestAs(testUserID, http.MethodPost, "/api/agents/period-brief-collectors", map[string]string{
			"model": "collector-model",
		})
		req.Header.Set("X-Workspace-ID", testWorkspaceID)
		testHandler.EnsurePeriodBriefCollectors(rec, req)
		return rec
	}

	rec := call()
	if rec.Code != http.StatusCreated {
		t.Fatalf("create=%d body=%s", rec.Code, rec.Body.String())
	}
	var created EnsurePeriodBriefCollectorsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if len(created.Created) < 3 {
		t.Fatalf("created=%v agents=%d", created.Created, len(created.Agents))
	}
	names := map[string]string{}
	var sawCloud bool
	for _, agent := range created.Agents {
		if !strings.HasPrefix(agent.Name, periodBriefCollectorNamePrefix) {
			t.Fatalf("collector name %q", agent.Name)
		}
		if strings.HasPrefix(agent.DisplayName, periodBriefCollectorCloudDisplayLead) {
			sawCloud = true
		} else if !strings.HasPrefix(agent.DisplayName, periodBriefCollectorDisplayLead) {
			t.Fatalf("display_name %q", agent.DisplayName)
		}
		names[agent.Name] = agent.ID
		t.Cleanup(func() { _, _ = testPool.Exec(context.Background(), `DELETE FROM agent WHERE id = $1`, agent.ID) })
	}
	if !sawCloud {
		t.Fatal("expected a cloud-labeled collector display name")
	}
	wantA := periodBriefCollectorNameForDaemon(daemonA)
	wantB := periodBriefCollectorNameForDaemon(daemonB)
	wantCloud := periodBriefCollectorNameForDaemon(cloudRuntimeID)
	if names[wantA] == "" || names[wantB] == "" || names[wantCloud] == "" {
		t.Fatalf("missing collectors want %q/%q/%q got %#v", wantA, wantB, wantCloud, names)
	}

	again := call()
	if again.Code != http.StatusOK {
		t.Fatalf("idempotent=%d body=%s", again.Code, again.Body.String())
	}
	var existing EnsurePeriodBriefCollectorsResponse
	if err := json.Unmarshal(again.Body.Bytes(), &existing); err != nil {
		t.Fatal(err)
	}
	if len(existing.Created) != 0 {
		t.Fatalf("created on second ensure = %#v", existing.Created)
	}

	_ = runtimeA
	_ = runtimeB
}

func TestPeriodBriefCollectorNameForDaemonStable(t *testing.T) {
	a := periodBriefCollectorNameForDaemon("ABC-DEF-12345678")
	b := periodBriefCollectorNameForDaemon("abc-def-12345678")
	if a != b {
		t.Fatalf("%q != %q", a, b)
	}
	if !strings.HasPrefix(a, periodBriefCollectorNamePrefix) {
		t.Fatalf("%q", a)
	}
	if len(a) > 32 {
		t.Fatalf("name too long: %q", a)
	}
}

func TestAgentTemplatesIncludePeriodWorkCollector(t *testing.T) {
	tmpl, ok := agentTemplates.Get(periodBriefCollectorTemplate)
	if !ok {
		t.Fatal("period-work-collector template missing")
	}
	if tmpl.Instructions == "" || !strings.Contains(tmpl.Instructions, "multica-period-work-collect") {
		t.Fatalf("template should point at collect skill: %+v", tmpl)
	}
}
