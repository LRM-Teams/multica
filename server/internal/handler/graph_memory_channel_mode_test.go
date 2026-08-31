package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/multica-ai/multica/server/internal/service"
)

type graphMemoryModeControlStub struct{}

func (graphMemoryModeControlStub) ReconcileChannel(_ context.Context, _, channelID string) (service.GraphMemoryAgentChannelStatus, error) {
	return service.GraphMemoryAgentChannelStatus{ChannelID: channelID, EffectiveMode: service.GraphMemoryModeAgent, Status: "active"}, nil
}
func (graphMemoryModeControlStub) ObserveActivity(context.Context, string, string, time.Time) error {
	return nil
}
func (graphMemoryModeControlStub) ResetState(context.Context, string, string) error { return nil }

func TestGraphMemoryChannelModeRuntimeOverrideRoundTrip(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test database unavailable")
	}
	ctx := context.Background()
	workspaceID := createGraphMemoryTestWorkspace(t)
	mustGraphMemoryMember(t, workspaceID, "owner")

	insertRuntime := func(provider string) string {
		var id string
		if err := testPool.QueryRow(ctx, `
			INSERT INTO agent_runtime (
			  workspace_id, daemon_id, name, runtime_mode, provider,
			  status, device_info, metadata, last_seen_at
			) VALUES ($1,$2,$3,'local',$4,'online','memory override test','{}'::jsonb,now())
			RETURNING id::text`,
			workspaceID, "memory-override-daemon-"+uuid.NewString(), "memory-override-runtime-"+uuid.NewString(), provider,
		).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	profileRuntimeID := insertRuntime("pi")
	channelRuntimeID := insertRuntime("pi")
	nonPiRuntimeID := insertRuntime("codex")

	if _, err := testPool.Exec(ctx, `
		INSERT INTO graph_memory_profile (
		  workspace_id, memory_type, graph_memory_mode,
		  memory_agent_runtime_id, memory_agent_model, memory_agent_thinking
		) VALUES ($1,'graph','agent',$2,'profile/model','medium')`,
		workspaceID, profileRuntimeID); err != nil {
		t.Fatal(err)
	}
	var channelID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO channel (workspace_id,name,kind,created_by)
		VALUES ($1,$2,'group',$3) RETURNING id::text`,
		workspaceID, "memory-runtime-override-"+uuid.NewString(), testUserID,
	).Scan(&channelID); err != nil {
		t.Fatal(err)
	}

	h := *testHandler
	h.GraphMemoryAgentControl = graphMemoryModeControlStub{}
	put := func(body map[string]any) *httptest.ResponseRecorder {
		req := newRequest(http.MethodPut,
			"/api/workspaces/"+workspaceID.String()+"/graph-memory/channels/"+channelID+"/mode", body)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("id", workspaceID.String())
		rctx.URLParams.Add("channelId", channelID)
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
		req.Header.Set("X-Workspace-ID", workspaceID.String())
		rec := httptest.NewRecorder()
		h.UpdateGraphMemoryChannelMode(rec, req)
		return rec
	}
	decode := func(rec *httptest.ResponseRecorder) graphMemoryChannelModeResponse {
		t.Helper()
		var response graphMemoryChannelModeResponse
		if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
			t.Fatal(err)
		}
		return response
	}

	rec := put(map[string]any{
		"override":                         "agent",
		"memory_agent_runtime_id_override": channelRuntimeID,
		"memory_agent_model_override":      "channel/model",
		"memory_agent_thinking_override":   "high",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("set override: status=%d body=%s", rec.Code, rec.Body.String())
	}
	set := decode(rec)
	if set.MemoryAgentRuntimeIDOverride != channelRuntimeID ||
		set.MemoryAgentModelOverride != "channel/model" ||
		set.MemoryAgentThinkingOverride != "high" ||
		set.EffectiveMemoryAgentRuntimeID != channelRuntimeID ||
		set.EffectiveMemoryAgentModel != "channel/model" ||
		set.EffectiveMemoryAgentThinking != "high" {
		t.Fatalf("set response = %+v", set)
	}

	// Existing clients only send mode; they must preserve the runtime tuple.
	rec = put(map[string]any{"override": "inject"})
	if rec.Code != http.StatusOK {
		t.Fatalf("legacy preserve: status=%d body=%s", rec.Code, rec.Body.String())
	}
	preserved := decode(rec)
	if preserved.MemoryAgentRuntimeIDOverride != channelRuntimeID || preserved.EffectiveMemoryAgentModel != "channel/model" {
		t.Fatalf("legacy update did not preserve override: %+v", preserved)
	}

	// Explicit null clears the channel tuple and returns to workspace config.
	rec = put(map[string]any{
		"override":                         "agent",
		"memory_agent_runtime_id_override": nil,
		"memory_agent_model_override":      nil,
		"memory_agent_thinking_override":   nil,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("clear override: status=%d body=%s", rec.Code, rec.Body.String())
	}
	cleared := decode(rec)
	if cleared.MemoryAgentRuntimeIDOverride != "" ||
		cleared.EffectiveMemoryAgentRuntimeID != profileRuntimeID ||
		cleared.EffectiveMemoryAgentModel != "profile/model" ||
		cleared.EffectiveMemoryAgentThinking != "medium" {
		t.Fatalf("clear response = %+v", cleared)
	}

	rec = put(map[string]any{
		"override":                         "agent",
		"memory_agent_runtime_id_override": nonPiRuntimeID,
		"memory_agent_model_override":      "codex/model",
	})
	if rec.Code != http.StatusConflict {
		t.Fatalf("non-Pi override: status=%d body=%s, want 409", rec.Code, rec.Body.String())
	}
}

func TestGraphMemoryChannelRuntimeOverrideCheckpointsActiveRunBeforeRebind(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test database unavailable")
	}
	ctx := context.Background()
	workspaceID := createGraphMemoryTestWorkspace(t)
	mustGraphMemoryMember(t, workspaceID, "owner")
	channelID := createGraphMemoryTestChannel(t, workspaceID)

	insertRuntime := func(label string) string {
		var id string
		if err := testPool.QueryRow(ctx, `
			INSERT INTO agent_runtime (
			  workspace_id, daemon_id, name, runtime_mode, provider,
			  status, device_info, metadata, last_seen_at
			) VALUES ($1,$2,$3,'local','pi','online','','{}'::jsonb,now())
			RETURNING id::text`,
			workspaceID, "memory-rebind-daemon-"+label+"-"+uuid.NewString(), "memory-rebind-"+label,
		).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	runtimeA := insertRuntime("a")
	runtimeB := insertRuntime("b")
	if _, err := testPool.Exec(ctx, `
		INSERT INTO graph_memory_profile (
		  workspace_id,memory_type,graph_memory_mode,
		  memory_agent_runtime_id,memory_agent_model,memory_agent_thinking
		) VALUES($1,'graph','agent',$2,'profile/model','low')`,
		workspaceID, runtimeA); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(ctx, `
		UPDATE channel SET
		  graph_memory_agent_runtime_id_override=$2,
		  graph_memory_agent_model_override='channel/model-a',
		  graph_memory_agent_thinking_override='medium'
		WHERE id=$1`, channelID, runtimeA); err != nil {
		t.Fatal(err)
	}
	var agentID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent (
		  workspace_id,name,display_name,runtime_mode,runtime_config,runtime_id,
		  owner_id,managed_role,instructions,model,thinking_level
		) VALUES($1,$2,'Memory rebind','local','{}',$3,$4,'graph_memory_channel','managed memory','channel/model-a','medium')
		RETURNING id::text`,
		workspaceID, "memory-rebind-"+channelID.String()[:8], runtimeA, testUserID,
	).Scan(&agentID); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO graph_memory_channel_agent (
		  channel_id,workspace_id,agent_id,runtime_id,sponsor_user_id,
		  handle,display_name,status
		) VALUES($1,$2,$3,$4,$5,$6,'Memory rebind','active')`,
		channelID, workspaceID, agentID, runtimeA, testUserID, "memory-rebind-"+channelID.String()[:8]); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member(channel_id,workspace_id,member_type,member_id,role)
		VALUES($1,$2,'agent',$3,'member')`, channelID, workspaceID, agentID); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(ctx, `INSERT INTO graph_memory_agent_state(channel_id) VALUES($1)`, channelID); err != nil {
		t.Fatal(err)
	}

	createActiveRun := func(fencingToken int64) string {
		var runID string
		if err := testPool.QueryRow(ctx, `
			INSERT INTO graph_memory_agent_run(workspace_id,channel_id,status,fencing_token)
			VALUES($1,$2,'running',$3) RETURNING id::text`,
			workspaceID, channelID, fencingToken).Scan(&runID); err != nil {
			t.Fatal(err)
		}
		if _, err := testPool.Exec(ctx, `
			INSERT INTO graph_memory_agent_trajectory(run_id,status) VALUES($1,'active')`,
			runID); err != nil {
			t.Fatal(err)
		}
		if _, err := testPool.Exec(ctx, `
			UPDATE graph_memory_agent_state SET active_run_id=$1,lease_expires_at=now()+interval '5 minutes' WHERE channel_id=$2`,
			runID, channelID); err != nil {
			t.Fatal(err)
		}
		return runID
	}
	runA := createActiveRun(1)
	var stateVersionBefore int64
	if err := testPool.QueryRow(ctx, `SELECT state_version FROM graph_memory_agent_state WHERE channel_id=$1`, channelID).Scan(&stateVersionBefore); err != nil {
		t.Fatal(err)
	}

	if _, err := testPool.Exec(ctx, `
		UPDATE channel SET
		  graph_memory_agent_runtime_id_override=$2,
		  graph_memory_agent_model_override='channel/model-b',
		  graph_memory_agent_thinking_override='high'
		WHERE id=$1`, channelID, runtimeB); err != nil {
		t.Fatal(err)
	}
	control := service.NewPostgresGraphMemoryAgentControlPlane(testPool)
	if _, err := control.ReconcileChannel(ctx, workspaceID.String(), channelID.String()); err != nil {
		t.Fatalf("ReconcileChannel rebind: %v", err)
	}

	var runStatus, trajectoryStatus, boundRuntime, agentRuntime, agentModel, agentThinking string
	var activeRunID *string
	var leaseExpiresAt *time.Time
	var stateVersionAfter int64
	if err := testPool.QueryRow(ctx, `SELECT status FROM graph_memory_agent_run WHERE id=$1`, runA).Scan(&runStatus); err != nil {
		t.Fatal(err)
	}
	if err := testPool.QueryRow(ctx, `SELECT status FROM graph_memory_agent_trajectory WHERE run_id=$1`, runA).Scan(&trajectoryStatus); err != nil {
		t.Fatal(err)
	}
	if err := testPool.QueryRow(ctx, `
		SELECT active_run_id::text,lease_expires_at,state_version
		FROM graph_memory_agent_state WHERE channel_id=$1`, channelID).Scan(&activeRunID, &leaseExpiresAt, &stateVersionAfter); err != nil {
		t.Fatal(err)
	}
	if err := testPool.QueryRow(ctx, `SELECT runtime_id::text FROM graph_memory_channel_agent WHERE channel_id=$1`, channelID).Scan(&boundRuntime); err != nil {
		t.Fatal(err)
	}
	if err := testPool.QueryRow(ctx, `SELECT runtime_id::text,model,COALESCE(thinking_level,'') FROM agent WHERE id=$1`, agentID).Scan(&agentRuntime, &agentModel, &agentThinking); err != nil {
		t.Fatal(err)
	}
	if runStatus != "checkpointed" || trajectoryStatus != "checkpointed" || activeRunID != nil || leaseExpiresAt != nil || stateVersionAfter <= stateVersionBefore {
		t.Fatalf("checkpoint state run=%s trajectory=%s active=%v lease=%v version=%d->%d", runStatus, trajectoryStatus, activeRunID, leaseExpiresAt, stateVersionBefore, stateVersionAfter)
	}
	if boundRuntime != runtimeB || agentRuntime != runtimeB || agentModel != "channel/model-b" || agentThinking != "high" {
		t.Fatalf("rebound tuple managed=%s agent=%s model=%s thinking=%s", boundRuntime, agentRuntime, agentModel, agentThinking)
	}

	// Reconcile with the same effective tuple must not checkpoint a new run.
	runB := createActiveRun(2)
	if _, err := control.ReconcileChannel(ctx, workspaceID.String(), channelID.String()); err != nil {
		t.Fatalf("ReconcileChannel unchanged: %v", err)
	}
	if err := testPool.QueryRow(ctx, `SELECT status FROM graph_memory_agent_run WHERE id=$1`, runB).Scan(&runStatus); err != nil {
		t.Fatal(err)
	}
	if runStatus != "running" {
		t.Fatalf("unchanged config checkpointed active run: status=%s", runStatus)
	}
}
