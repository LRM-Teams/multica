package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestEnvDispatchHTTPProductionAdapterResolvesPerAgentRoster(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	onlineAgentID, _ := setupRosterAdapterPiAgent(t, "online-source")
	offlineAgentID, _ := setupRosterAdapterPiAgent(t, "offline-source")
	noneAgentID, _ := setupRosterAdapterPiAgent(t, "none-source")
	onlineExecutionID, onlineRuntimeID := setupRosterAdapterPiAgent(t, "online-execution")
	offlineExecutionID, offlineRuntimeID := setupRosterAdapterPiAgent(t, "offline-execution")
	noneExecutionID, noneRuntimeID := setupRosterAdapterPiAgent(t, "none-execution")

	t.Setenv("AREAL_BRIDGE_STUB_URL", "https://areal-bridge.invalid")
	t.Setenv("AREAL_ADMIN_API_KEY", "roster-adapter-test-admin-key")

	type executionBinding struct {
		agentID   string
		runtimeID string
	}
	executions := map[string]executionBinding{
		onlineAgentID:  {agentID: onlineExecutionID, runtimeID: onlineRuntimeID},
		offlineAgentID: {agentID: offlineExecutionID, runtimeID: offlineRuntimeID},
		noneAgentID:    {agentID: noneExecutionID, runtimeID: noneRuntimeID},
	}
	var eventsMu sync.Mutex
	var events []string
	isolated := *testHandler
	isolated.envDispatchProvisionAgentTestHook = func(_ context.Context, in service.EnvDispatchAgentProvisionInput) (service.EnvDispatchAgentProvisionResult, error) {
		binding, ok := executions[in.AgentID]
		if !ok {
			return service.EnvDispatchAgentProvisionResult{}, fmt.Errorf("unexpected roster agent %s", in.AgentID)
		}
		eventsMu.Lock()
		events = append(events, "provision:"+in.AgentID+":"+in.TrainingMode)
		eventsMu.Unlock()
		arealSessionID := ""
		if in.TrainingMode == "online_rl" {
			arealSessionID = "areal-" + in.AgentID
		}
		return service.EnvDispatchAgentProvisionResult{
			AgentID: binding.agentID, RuntimeID: binding.runtimeID,
			ChatSessionID: "pi-" + in.AgentID, AReALSessionID: arealSessionID,
		}, nil
	}
	isolated.envDispatchCreateMessageTestHook = func(_ context.Context, _, _, _, content string) (string, error) {
		eventsMu.Lock()
		defer eventsMu.Unlock()
		events = append(events, "send:"+content)
		return uuid.NewString(), nil
	}
	isolated.envDispatchPreparePiRunTestHook = func(_ context.Context, runID string, runAgent service.MixedDispatchRunAgent) (service.MixedDispatchRunAgent, error) {
		eventsMu.Lock()
		events = append(events, "prepare:"+runAgent.SourceAgentID)
		eventsMu.Unlock()
		runAgent.RunAgentID = uuid.NewString()
		runAgent.PiSessionID = "native-pi-" + runID + "-" + runAgent.SourceAgentID
		runAgent.CaptureBoundary = "boundary-" + runAgent.RunAgentID
		return runAgent, nil
	}

	ctx := context.Background()
	var sourceEnvID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO environment (workspace_id, sandbox_ids, mode)
		VALUES ($1, '{}', 'base')
		RETURNING id::text`, testWorkspaceID).Scan(&sourceEnvID); err != nil {
		t.Fatalf("create source env: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM environment WHERE id = $1`, sourceEnvID)
	})

	request := EnvDispatchRequest{
		Mode:                   "scratch",
		EnvID:                  sourceEnvID,
		Domain:                 "self_play",
		DispatchType:           "message",
		GroupSize:              1,
		AgentID:                onlineAgentID,
		OnlineTrainableAgents:  []string{onlineAgentID},
		OfflineTrainableAgents: []string{offlineAgentID},
		QuietWindowMS:          100,
		TotalTimeoutSeconds:    30,
		Message:                &MessageDispatchInput{Content: "start mixed roster"},
		PerAgentEnv: map[string]PerAgentEnvRequest{
			onlineAgentID:  {Template: "default"},
			offlineAgentID: {Template: "default"},
			noneAgentID:    {Template: "default"},
		},
	}
	req := newRequest(http.MethodPost, "/api/v1/env-dispatch", request)
	req = req.WithContext(middleware.SetMemberContext(req.Context(), testWorkspaceID, db.Member{}))
	recorder := httptest.NewRecorder()

	isolated.EnvDispatch(recorder, req)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", recorder.Code, recorder.Body.String())
	}
	var response EnvDispatchResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	wantModes := map[string]string{
		onlineAgentID:  "online_rl",
		offlineAgentID: "offline_rl",
		noneAgentID:    "none",
	}
	if len(response.RunAgents) != len(wantModes) {
		t.Fatalf("run_agents = %+v, want three roster agents", response.RunAgents)
	}
	for _, agent := range response.RunAgents {
		if wantModes[agent.SourceAgentID] != agent.TrainingMode {
			t.Fatalf("run agent = %+v, want mode %q", agent, wantModes[agent.SourceAgentID])
		}
	}
	if len(response.Rollouts) != 1 || response.RunID == "" {
		t.Fatalf("response rollout identity = %+v, want one persisted mixed run", response)
	}
	createdEnvID := response.Rollouts[0].EnvID
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM env_dispatch_run WHERE run_id = $1`, response.RunID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM project WHERE id = $1`, response.ProjectID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM environment WHERE id = $1`, createdEnvID)
	})

	rows, err := testPool.Query(ctx, `
		SELECT source_agent_id::text, training_mode
		FROM env_dispatch_run_agent
		WHERE run_id = $1`, response.RunID)
	if err != nil {
		t.Fatalf("load bound run agents: %v", err)
	}
	defer rows.Close()
	boundModes := make(map[string]string)
	for rows.Next() {
		var sourceAgentID, mode string
		if err := rows.Scan(&sourceAgentID, &mode); err != nil {
			t.Fatalf("scan bound run agent: %v", err)
		}
		boundModes[sourceAgentID] = mode
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate bound run agents: %v", err)
	}
	if len(boundModes) != len(wantModes) {
		t.Fatalf("persisted run agents = %v, want all three roster agents", boundModes)
	}
	for sourceAgentID, wantMode := range wantModes {
		if boundModes[sourceAgentID] != wantMode {
			t.Fatalf("persisted mode for %s = %q, want %q", sourceAgentID, boundModes[sourceAgentID], wantMode)
		}
	}

	remaining := []string{offlineAgentID, noneAgentID}
	sort.Strings(remaining)
	wantEvents := []string{"provision:" + onlineAgentID + ":online_rl"}
	for _, sourceAgentID := range remaining {
		wantEvents = append(wantEvents, "provision:"+sourceAgentID+":"+wantModes[sourceAgentID])
	}
	wantEvents = append(wantEvents, "prepare:"+onlineAgentID)
	for _, sourceAgentID := range remaining {
		wantEvents = append(wantEvents, "prepare:"+sourceAgentID)
	}
	wantEvents = append(wantEvents, "send:start mixed roster")
	eventsMu.Lock()
	gotEvents := append([]string(nil), events...)
	eventsMu.Unlock()
	if len(gotEvents) != len(wantEvents) {
		t.Fatalf("events = %v, want %v", gotEvents, wantEvents)
	}
	for i := range wantEvents {
		if gotEvents[i] != wantEvents[i] {
			t.Fatalf("events = %v, want %v (all provisions must precede initial send)", gotEvents, wantEvents)
		}
	}
}

func setupRosterAdapterPiAgent(t *testing.T, label string) (agentID, runtimeID string) {
	t.Helper()
	ctx := context.Background()
	suffix := uuid.NewString()
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_runtime (workspace_id, name, runtime_mode, provider, status, metadata, last_seen_at)
		VALUES ($1, $2, 'cloud', 'pi', 'online', '{}'::jsonb, now())
		RETURNING id::text`, testWorkspaceID, "roster-adapter-"+label+"-"+suffix).Scan(&runtimeID); err != nil {
		t.Fatalf("create Pi runtime: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent (
			workspace_id, name, display_name, description, runtime_mode, runtime_config,
			runtime_id, max_concurrent_tasks, owner_id, model
		) VALUES ($1, $2, $3, '', 'cloud', '{}'::jsonb, $4, 1, $5, 'composer-1.5')
		RETURNING id::text`, testWorkspaceID, "roster-adapter-"+label+"-"+suffix,
		"Roster Adapter "+label, runtimeID, testUserID).Scan(&agentID); err != nil {
		_, _ = testPool.Exec(ctx, `DELETE FROM agent_runtime WHERE id = $1`, runtimeID)
		t.Fatalf("create Pi agent: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM agent WHERE id = $1`, agentID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE id = $1`, runtimeID)
	})
	return agentID, runtimeID
}

func TestEnvDispatchProductionAdapterRosterAccessAndLegacyFallback(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	leaderID, _ := setupRosterAdapterPiAgent(t, "legacy-leader")
	adapter := newEnvDispatchDepsAdapter(testHandler)

	roster, err := adapter.ResolveMessageRoster(ctx, testWorkspaceID, leaderID, nil)
	if err != nil {
		t.Fatalf("resolve legacy roster: %v", err)
	}
	if roster.LeaderID != leaderID || len(roster.AgentIDs) != 1 || roster.AgentIDs[0] != leaderID {
		t.Fatalf("legacy roster = %+v, want leader-only fallback", roster)
	}

	if _, err := adapter.ResolveMessageRoster(ctx, testWorkspaceID, leaderID, []service.PerAgentEnvSpec{{
		AgentID: "not-a-uuid", Template: "default",
	}}); err == nil {
		t.Fatal("malformed per_agent_env roster ID unexpectedly resolved")
	}

	var foreignWorkspaceID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO workspace (name, slug)
		VALUES ($1, $2) RETURNING id::text`,
		"Roster Adapter Foreign "+uuid.NewString(), "roster-adapter-foreign-"+uuid.NewString()).Scan(&foreignWorkspaceID); err != nil {
		t.Fatalf("create foreign workspace: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1`, foreignWorkspaceID)
	})
	var foreignRuntimeID, foreignAgentID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_runtime (workspace_id, name, runtime_mode, provider, status, metadata, last_seen_at)
		VALUES ($1, $2, 'cloud', 'pi', 'online', '{}'::jsonb, now())
		RETURNING id::text`, foreignWorkspaceID, "foreign-runtime-"+uuid.NewString()).Scan(&foreignRuntimeID); err != nil {
		t.Fatalf("create foreign runtime: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent (
			workspace_id, name, description, runtime_mode, runtime_config,
			runtime_id, max_concurrent_tasks, owner_id, model
		) VALUES ($1, $2, '', 'cloud', '{}'::jsonb, $3, 1, $4, 'composer-1.5')
		RETURNING id::text`, foreignWorkspaceID, "foreign-agent-"+uuid.NewString(),
		foreignRuntimeID, testUserID).Scan(&foreignAgentID); err != nil {
		t.Fatalf("create foreign agent: %v", err)
	}

	if _, err := adapter.ResolveMessageRoster(ctx, testWorkspaceID, leaderID, []service.PerAgentEnvSpec{{
		AgentID: foreignAgentID, Template: "default",
	}}); err == nil {
		t.Fatal("cross-workspace per_agent_env roster member unexpectedly resolved")
	}
}
