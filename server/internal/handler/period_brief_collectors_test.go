package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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

	cloudDaemon := "cloud-box-" + uuid.NewString()[:8]
	var cloudRuntimeID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_runtime (workspace_id, daemon_id, name, display_name, runtime_mode, provider, status, device_info, metadata, visibility, last_seen_at) VALUES ($1,  $2,  'cloud-box',  'Cloud Box',  'cloud',  $3,  'online',  '',  '{}'::jsonb,  'public',  now())
		RETURNING id
	`, testWorkspaceID, cloudDaemon, "cloud_collect_"+uuid.NewString()).Scan(&cloudRuntimeID); err != nil {
		t.Fatalf("seed cloud runtime: %v", err)
	}
	// LRM-1570: the cloud runtime is its own Computer, owned via an active
	// binding for its daemon (the caller is testUserID).
	if _, err := testPool.Exec(ctx, `
		INSERT INTO computer_workspace_bindings (
			daemon_id, workspace_id, user_id, execution_token_hash, active
		) VALUES ($1, $2, $3, 'cloud-collect-test', TRUE)
	`, cloudDaemon, testWorkspaceID, testUserID); err != nil {
		t.Fatalf("seed cloud owner binding: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE id = $1`, cloudRuntimeID)
	})

	probe := func() *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := newRequestAs(testUserID, http.MethodPost, "/api/agents/period-brief-collectors", map[string]string{
			"model": "collector-model",
		})
		req.Header.Set("X-Workspace-ID", testWorkspaceID)
		testHandler.EnsurePeriodBriefCollectors(rec, req)
		return rec
	}
	create := func(runtimeID string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := newRequestAs(testUserID, http.MethodPost, "/api/agents/period-brief-collectors", map[string]string{
			"model":      "collector-model",
			"runtime_id": runtimeID,
		})
		req.Header.Set("X-Workspace-ID", testWorkspaceID)
		testHandler.EnsurePeriodBriefCollectors(rec, req)
		return rec
	}

	probed := probe()
	if probed.Code != http.StatusOK {
		t.Fatalf("probe=%d body=%s", probed.Code, probed.Body.String())
	}
	var preview EnsurePeriodBriefCollectorsResponse
	if err := json.Unmarshal(probed.Body.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	if len(preview.Created) != 0 {
		t.Fatalf("probe must not create: %#v", preview.Created)
	}
	if len(preview.Missing) < 3 {
		t.Fatalf("missing=%#v", preview.Missing)
	}

	for _, runtimeID := range []string{runtimeA, runtimeB, cloudRuntimeID} {
		rec := create(runtimeID)
		if rec.Code != http.StatusCreated {
			t.Fatalf("create %s=%d body=%s", runtimeID, rec.Code, rec.Body.String())
		}
		var created EnsurePeriodBriefCollectorsResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
			t.Fatal(err)
		}
		if len(created.Created) != 1 {
			t.Fatalf("created=%v agents=%d", created.Created, len(created.Agents))
		}
		for _, agent := range created.Agents {
			t.Cleanup(func() { _, _ = testPool.Exec(context.Background(), `DELETE FROM agent WHERE id = $1`, agent.ID) })
		}
	}

	rec := probe()
	if rec.Code != http.StatusOK {
		t.Fatalf("probe after create=%d body=%s", rec.Code, rec.Body.String())
	}
	var created EnsurePeriodBriefCollectorsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	missingByKey := map[string]PeriodBriefCollectorMissingSlot{}
	for _, slot := range created.Missing {
		missingByKey[slot.Key] = slot
	}
	for _, key := range []string{
		"local:" + strings.ToLower(daemonA),
		"local:" + strings.ToLower(daemonB),
		"cloud:" + cloudRuntimeID,
	} {
		if _, ok := missingByKey[key]; ok {
			t.Fatalf("slot %s still missing after create: %#v", key, created.Missing)
		}
	}
	wantA := periodBriefCollectorNameForDaemon(daemonA)
	wantB := periodBriefCollectorNameForDaemon(daemonB)
	wantCloud := periodBriefCollectorNameForDaemon(cloudRuntimeID)
	names := map[string]string{}
	var sawCloud bool
	for _, agent := range created.Agents {
		names[agent.Name] = agent.ID
		if agent.Name != wantA && agent.Name != wantB && agent.Name != wantCloud {
			continue
		}
		if !strings.HasPrefix(agent.Name, periodBriefCollectorNamePrefix) {
			t.Fatalf("collector name %q", agent.Name)
		}
		if strings.HasPrefix(agent.DisplayName, periodBriefCollectorCloudDisplayLead) {
			sawCloud = true
		} else if !strings.HasPrefix(agent.DisplayName, periodBriefCollectorDisplayLead) {
			t.Fatalf("display_name %q", agent.DisplayName)
		}
		t.Cleanup(func() { _, _ = testPool.Exec(context.Background(), `DELETE FROM agent WHERE id = $1`, agent.ID) })
	}
	if !sawCloud {
		t.Fatal("expected a cloud-labeled collector display name")
	}
	if names[wantA] == "" || names[wantB] == "" || names[wantCloud] == "" {
		t.Fatalf("missing collectors want %q/%q/%q got %#v", wantA, wantB, wantCloud, names)
	}

	again := create(runtimeA)
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

func TestEnsurePeriodBriefCollectors_SkipsOthersPublicComputers(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	_ = ensureSystemGeneralForTest(t)

	otherOwner := createRuntimeLocalSkillTestMember(t, "member")
	otherDaemon := "other-pc-" + uuid.NewString()[:8]
	var otherRuntimeID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_runtime (workspace_id, daemon_id, name, runtime_mode, provider, status, device_info, metadata, visibility, last_seen_at) VALUES ($1,  $2,  $3,  'local',  $4,  'online',  '',  '{}'::jsonb,  'public',  now())
		RETURNING id
	`, testWorkspaceID, otherDaemon, "Other Public Laptop", "other_pub_"+uuid.NewString()).Scan(&otherRuntimeID); err != nil {
		t.Fatalf("seed other public runtime: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE id = $1`, otherRuntimeID)
	})
	// LRM-1570: ownership is machine-level; the other computer is owned by
	// otherOwner, so collectors must not be provisioned for it by testUserID.
	if _, err := testPool.Exec(ctx, `
		INSERT INTO computer_workspace_bindings (
			daemon_id, workspace_id, user_id, execution_token_hash, active
		) VALUES ($1, $2, $3, 'other-pc-test', TRUE)
	`, otherDaemon, testWorkspaceID, otherOwner); err != nil {
		t.Fatalf("seed other owner binding: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM computer_workspace_bindings WHERE daemon_id = $1 AND workspace_id = $2`, otherDaemon, testWorkspaceID)
	})

	ownDaemon := "own-pc-" + uuid.NewString()[:8]
	ownRuntimeID := seedMachineLockedRuntime(t, ownDaemon, "My Laptop")

	probe := httptest.NewRecorder()
	probeReq := newRequestAs(testUserID, http.MethodPost, "/api/agents/period-brief-collectors", map[string]string{
		"model": "collector-model",
	})
	probeReq.Header.Set("X-Workspace-ID", testWorkspaceID)
	testHandler.EnsurePeriodBriefCollectors(probe, probeReq)
	if probe.Code != http.StatusOK {
		t.Fatalf("probe=%d body=%s", probe.Code, probe.Body.String())
	}
	var preview EnsurePeriodBriefCollectorsResponse
	if err := json.Unmarshal(probe.Body.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	forbidden := periodBriefCollectorNameForDaemon(otherDaemon)
	wantOwn := periodBriefCollectorNameForDaemon(ownDaemon)
	sawOwnMissing := false
	for _, slot := range preview.Missing {
		if strings.Contains(strings.ToLower(slot.Key), strings.ToLower(otherDaemon)) {
			t.Fatalf("must not list another member's computer: %#v", slot)
		}
		if slot.Key == "local:"+strings.ToLower(ownDaemon) {
			sawOwnMissing = true
		}
	}
	if !sawOwnMissing {
		t.Fatalf("expected own missing slot %q among %#v", wantOwn, preview.Missing)
	}

	rec := httptest.NewRecorder()
	req := newRequestAs(testUserID, http.MethodPost, "/api/agents/period-brief-collectors", map[string]string{
		"model":      "collector-model",
		"runtime_id": ownRuntimeID,
	})
	req.Header.Set("X-Workspace-ID", testWorkspaceID)
	testHandler.EnsurePeriodBriefCollectors(rec, req)
	if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
		t.Fatalf("ensure=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp EnsurePeriodBriefCollectorsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	sawOwn := false
	for _, agent := range resp.Agents {
		t.Cleanup(func() { _, _ = testPool.Exec(context.Background(), `DELETE FROM agent WHERE id = $1`, agent.ID) })
		if agent.Name == forbidden {
			t.Fatalf("must not provision collector for another member's computer: %#v", agent)
		}
		if agent.Name == wantOwn {
			sawOwn = true
		}
	}
	if !sawOwn {
		t.Fatalf("expected own collector %q among %#v", wantOwn, resp.Agents)
	}
}

func TestEnsurePeriodBriefCollectors_RebindsWrongComputer(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	_ = ensureSystemGeneralForTest(t)

	daemonA := "pc-daemon-" + uuid.NewString()[:8]
	daemonB := "pc-daemon-" + uuid.NewString()[:8]
	runtimeA := seedMachineLockedRuntime(t, daemonA, "Laptop A")
	runtimeB := seedMachineLockedRuntime(t, daemonB, "Laptop B")

	create := func(runtimeID string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := newRequestAs(testUserID, http.MethodPost, "/api/agents/period-brief-collectors", map[string]string{
			"model":      "collector-model",
			"runtime_id": runtimeID,
		})
		req.Header.Set("X-Workspace-ID", testWorkspaceID)
		testHandler.EnsurePeriodBriefCollectors(rec, req)
		return rec
	}

	createdRec := create(runtimeA)
	if createdRec.Code != http.StatusCreated {
		t.Fatalf("create=%d body=%s", createdRec.Code, createdRec.Body.String())
	}
	var created EnsurePeriodBriefCollectorsResponse
	if err := json.Unmarshal(createdRec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if len(created.Agents) != 1 {
		t.Fatalf("agents=%#v", created.Agents)
	}
	agentID := created.Agents[0].ID
	t.Cleanup(func() { _, _ = testPool.Exec(context.Background(), `DELETE FROM agent WHERE id = $1`, agentID) })

	if _, err := testPool.Exec(ctx, `UPDATE agent SET runtime_id = $2 WHERE id = $1`, agentID, runtimeB); err != nil {
		t.Fatalf("rebind fixture: %v", err)
	}

	probe := httptest.NewRecorder()
	probeReq := newRequestAs(testUserID, http.MethodPost, "/api/agents/period-brief-collectors", map[string]string{
		"model": "collector-model",
	})
	probeReq.Header.Set("X-Workspace-ID", testWorkspaceID)
	testHandler.EnsurePeriodBriefCollectors(probe, probeReq)
	if probe.Code != http.StatusOK {
		t.Fatalf("probe=%d body=%s", probe.Code, probe.Body.String())
	}
	var preview EnsurePeriodBriefCollectorsResponse
	if err := json.Unmarshal(probe.Body.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	sawA := false
	for _, slot := range preview.Missing {
		if slot.Key == "local:"+strings.ToLower(daemonA) {
			sawA = true
		}
	}
	if !sawA {
		t.Fatalf("expected Laptop A missing after wrong-computer bind: %#v", preview.Missing)
	}

	repaired := create(runtimeA)
	if repaired.Code != http.StatusOK {
		t.Fatalf("repair=%d body=%s", repaired.Code, repaired.Body.String())
	}
	var after EnsurePeriodBriefCollectorsResponse
	if err := json.Unmarshal(repaired.Body.Bytes(), &after); err != nil {
		t.Fatal(err)
	}
	if len(after.Created) != 0 {
		t.Fatalf("repair should rebind, not create: %#v", after.Created)
	}
	if len(after.Agents) != 1 || after.Agents[0].ID != agentID || after.Agents[0].RuntimeID != runtimeA {
		t.Fatalf("repaired agent=%#v want runtime %s", after.Agents, runtimeA)
	}
}

func TestCreateNotePeriodBriefRejectsCollectorOnOthersComputer(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	prevWait := notePeriodBriefCollectorMaxWait
	notePeriodBriefCollectorMaxWait = 0
	prevBG := notePeriodBriefFinishInBackground
	notePeriodBriefFinishInBackground = false
	t.Cleanup(func() {
		notePeriodBriefCollectorMaxWait = prevWait
		notePeriodBriefFinishInBackground = prevBG
	})

	otherOwner := createRuntimeLocalSkillTestMember(t, "member")
	foreignCollector := createPeriodBriefCollectorTestAgentForOwner(t, "Foreign Laptop", otherOwner)
	synthID := createHandlerTestAgent(t, "Period Brief Own Synth "+uuid.NewString()[:8], nil)

	rec := httptest.NewRecorder()
	testHandler.CreateNotePeriodBrief(rec, newRequest(http.MethodPost, "/api/notes/period-briefs", map[string]any{
		"window":              "day",
		"date":                time.Now().UTC().Format("2006-01-02"),
		"timezone":            "UTC",
		"agent_id":            synthID,
		"collector_agent_ids": []string{foreignCollector},
	}))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for foreign computer collector, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "computer you own") {
		t.Fatalf("error should mention ownership: %s", rec.Body.String())
	}
}
