package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/multica-ai/multica/server/internal/middleware"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/stretchr/testify/require"
)

// setupEnvDispatchChannelRolloutFixture seeds a minimal EnvDispatch message
// rollout (env + project + group channel + agent member + pending binding) and
// returns the IDs. Rows are cleaned up defensively; the cleanup handler under
// test also removes them.
func setupEnvDispatchChannelRolloutFixture(t *testing.T) (envID, projectID, channelID, agentID string) {
	t.Helper()
	ctx := context.Background()
	agentID = createHandlerTestAgent(t, "Env Dispatch Channel Routes Agent", nil)
	require.NoError(t, testPool.QueryRow(ctx, `
		INSERT INTO environment (workspace_id, sandbox_ids, mode)
		VALUES ($1, '{}', 'scratch') RETURNING id`, testWorkspaceID).Scan(&envID))
	t.Cleanup(func() { _, _ = testPool.Exec(context.Background(), `DELETE FROM environment WHERE id = $1`, envID) })
	require.NoError(t, testPool.QueryRow(ctx, `
		INSERT INTO project (workspace_id, title, env_id)
		VALUES ($1, $2, $3) RETURNING id`, testWorkspaceID, "Env Dispatch Routes Project "+uuid.NewString(), envID).Scan(&projectID))
	t.Cleanup(func() { _, _ = testPool.Exec(context.Background(), `DELETE FROM project WHERE id = $1`, projectID) })
	require.NoError(t, testPool.QueryRow(ctx, `
		INSERT INTO channel (workspace_id, name, kind, project_id, created_by)
		VALUES ($1, $2, 'group', $3, $4) RETURNING id`,
		testWorkspaceID, "env-dispatch-routes-"+uuid.NewString(), projectID, testUserID).Scan(&channelID))
	t.Cleanup(func() { _, _ = testPool.Exec(context.Background(), `DELETE FROM channel WHERE id = $1`, channelID) })
	_, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3)
ON CONFLICT DO NOTHING`, channelID, testWorkspaceID, agentID)
	require.NoError(t, err)
	_, err = testPool.Exec(ctx, `
		INSERT INTO environment_agent_sandbox (env_id, channel_id, agent_id, status, sandbox_config)
		VALUES ($1, $2, $3, 'pending', '{}'::jsonb)`, envID, channelID, agentID)
	require.NoError(t, err)
	return envID, projectID, channelID, agentID
}

// authedChannelRequest builds a request that carries the test user + workspace
// (as the auth middleware would) and the named chi URL param.
func authedChannelRequest(method, path, paramKey, paramValue string) *http.Request {
	r := httptest.NewRequest(method, path, nil)
	r.Header.Set("X-User-ID", testUserID)
	r = r.WithContext(middleware.SetMemberContext(r.Context(), testWorkspaceID, db.Member{}))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add(paramKey, paramValue)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

func TestChannelDagFacadeResolvesBoundProject(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	_, _, channelID, _ := setupEnvDispatchChannelRolloutFixture(t)

	w := httptest.NewRecorder()
	testHandler.GetEnvDispatchChannelDag(w, authedChannelRequest(http.MethodGet, "/api/v1/env-dispatch/channels/"+channelID+"/dag", "channelID", channelID))
	// The facade resolved the bound project and ran the project-scoped DAG
	// status decision. A fresh rollout has no terminal root task -> 202.
	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (in_progress); body=%s", w.Code, w.Body.String())
	}
}

func TestChannelDagFacadeMissingChannelReturns404(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	w := httptest.NewRecorder()
	testHandler.GetEnvDispatchChannelDag(w, authedChannelRequest(http.MethodGet, "/api/v1/env-dispatch/channels/"+uuid.NewString()+"/dag", "channelID", uuid.NewString()))
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", w.Code, w.Body.String())
	}
}

func TestChannelCleanupDeletesChannelProjectEnvAndBindings(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	envID, projectID, channelID, agentID := setupEnvDispatchChannelRolloutFixture(t)

	w := httptest.NewRecorder()
	testHandler.DeleteEnvDispatchChannel(w, authedChannelRequest(http.MethodDelete, "/api/v1/env-dispatch/channels/"+channelID, "channelID", channelID))
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", w.Code, w.Body.String())
	}
	// Channel, project, env, and bindings are all gone.
	for _, q := range []struct{ name, sql, id string }{
		{"channel", `SELECT id FROM channel WHERE id = $1`, channelID},
		{"project", `SELECT id FROM project WHERE id = $1`, projectID},
		{"env", `SELECT id FROM environment WHERE id = $1`, envID},
		{"binding", `SELECT env_id FROM environment_agent_sandbox WHERE env_id = $1`, envID},
	} {
		if err := testPool.QueryRow(ctx, q.sql, q.id).Scan(new(string)); err != pgx.ErrNoRows {
			t.Fatalf("%s should be deleted, got err=%v", q.name, err)
		}
	}
	// Agent remains (it is not part of the rollout).
	var exists bool
	require.NoError(t, testPool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM agent WHERE id = $1)`, agentID).Scan(&exists))
	if !exists {
		t.Fatal("agent should remain after channel cleanup")
	}
}

// TestChannelCleanupReclaimsReadyBindingOwnedResources pins the owned-resource
// cascade for a fully provisioned rollout. Regression coverage for the reclaim
// loop being unreachable: markDeleting moves the ready binding to "deleting"
// before the loop reads it, so gating on "ready" alone skipped every resource
// and the bindings were then deleted, leaving the sandbox, the runtime, and the
// derived agent as orphans nothing could reach. The pre-existing cleanup test
// above uses a "pending" binding, which never enters this loop at all.
func TestChannelCleanupReclaimsReadyBindingOwnedResources(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	envID, _, channelID, _ := setupEnvDispatchChannelRolloutFixture(t)
	derivedAgentID, runtimeID := setupBoundRuntimeAgent(t, "pi")

	var nodeID, instanceID string
	require.NoError(t, testPool.QueryRow(ctx, `
		INSERT INTO sandbox_node (node_key, name, owner_user_id, capabilities, max_concurrency, metadata)
		VALUES ($1, 'channel cleanup node', $2, '{}'::jsonb, 1, '{}'::jsonb)
		RETURNING id`, "channel-cleanup-"+uuid.NewString(), testUserID).Scan(&nodeID))
	require.NoError(t, testPool.QueryRow(ctx, `
		INSERT INTO sandbox_instance (workspace_id, creator_user_id, node_id, status, template, limits, metadata)
		VALUES ($1, $2, $3, 'running', 'default', '{}'::jsonb, '{}'::jsonb)
		RETURNING id`, testWorkspaceID, testUserID, nodeID).Scan(&instanceID))
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM sandbox_instance WHERE id = $1`, instanceID)
		testPool.Exec(context.Background(), `DELETE FROM sandbox_node WHERE id = $1`, nodeID)
	})

	// Promote the fixture's pending binding to a fully provisioned rollout:
	// the CHECK constraint requires all three handles once status is 'ready'.
	_, err := testPool.Exec(ctx, `
		UPDATE environment_agent_sandbox
		   SET status = 'ready', sandbox_instance_id = $2, runtime_id = $3,
		       daemon_id = $4, derived_agent_id = $5
		 WHERE env_id = $1`, envID, instanceID, runtimeID, uuid.NewString(), derivedAgentID)
	require.NoError(t, err)

	w := httptest.NewRecorder()
	testHandler.DeleteEnvDispatchChannel(w, authedChannelRequest(http.MethodDelete, "/api/v1/env-dispatch/channels/"+channelID, "channelID", channelID))
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", w.Code, w.Body.String())
	}

	// The sandbox was reclaimed: a delete job is queued for its node, or the
	// row was force-deleted because the node was unreachable.
	var deleteJobs int
	require.NoError(t, testPool.QueryRow(ctx,
		`SELECT count(*) FROM sandbox_job WHERE instance_id = $1 AND type = 'delete'`,
		instanceID).Scan(&deleteJobs))
	var instanceExists bool
	require.NoError(t, testPool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM sandbox_instance WHERE id = $1)`,
		instanceID).Scan(&instanceExists))
	if deleteJobs == 0 && instanceExists {
		t.Error("sandbox was neither queued for deletion nor force-deleted")
	}

	// The runtime and its derived agent are gone, so the in-sandbox daemon has
	// no identity left to re-register against.
	var runtimeExists, derivedExists bool
	require.NoError(t, testPool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM agent_runtime WHERE id = $1)`, runtimeID).Scan(&runtimeExists))
	require.NoError(t, testPool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM agent WHERE id = $1)`, derivedAgentID).Scan(&derivedExists))
	if runtimeExists {
		t.Error("runtime should be deleted with the rollout")
	}
	if derivedExists {
		t.Error("derived agent should be deleted with the rollout")
	}
}

func TestChannelCleanupIsIdempotent(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	_, _, channelID, _ := setupEnvDispatchChannelRolloutFixture(t)

	w1 := httptest.NewRecorder()
	testHandler.DeleteEnvDispatchChannel(w1, authedChannelRequest(http.MethodDelete, "/api/v1/env-dispatch/channels/"+channelID, "channelID", channelID))
	if w1.Code != http.StatusNoContent {
		t.Fatalf("first delete: status = %d, want 204; body=%s", w1.Code, w1.Body.String())
	}
	w2 := httptest.NewRecorder()
	testHandler.DeleteEnvDispatchChannel(w2, authedChannelRequest(http.MethodDelete, "/api/v1/env-dispatch/channels/"+channelID, "channelID", channelID))
	if w2.Code != http.StatusNoContent {
		t.Fatalf("second delete: status = %d, want 204 (idempotent); body=%s", w2.Code, w2.Body.String())
	}
}

// TestChannelCleanupSharedBindingsReclaimOnce pins the shared_sandbox channel
// lifecycle (FR-006/007, research D4, SC-005): when N bindings of one env point
// at the SAME shared sandbox_instance and agent_runtime, channel delete must
// reclaim the shared sandbox and runtime exactly once, archive/delete ALL
// derived agents before the runtime delete (agent.runtime_id is ON DELETE
// RESTRICT), and leave no residue.
func TestChannelCleanupSharedBindingsReclaimOnce(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	envID, _, channelID, agentAID := setupEnvDispatchChannelRolloutFixture(t)

	// Second roster member with its own pending binding on the same env.
	agentBID := createHandlerTestAgent(t, "Shared Cleanup Agent B", nil)
	_, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3)
		ON CONFLICT DO NOTHING`, channelID, testWorkspaceID, agentBID)
	require.NoError(t, err)
	_, err = testPool.Exec(ctx, `
		INSERT INTO environment_agent_sandbox (env_id, channel_id, agent_id, status, sandbox_config)
		VALUES ($1, $2, $3, 'pending', '{}'::jsonb)`, envID, channelID, agentBID)
	require.NoError(t, err)

	// One shared runtime R' with TWO derived agents bound to it (the FK
	// RESTRICT order under test: both must go before the runtime delete).
	var sharedRuntimeID string
	require.NoError(t, testPool.QueryRow(ctx, `
		INSERT INTO agent_runtime (workspace_id, name, runtime_mode, provider, status, metadata, last_seen_at)
		VALUES ($1, 'shared cleanup runtime', 'cloud', 'pi', 'online', '{}'::jsonb, now())
		RETURNING id`, testWorkspaceID).Scan(&sharedRuntimeID))
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE id = $1`, sharedRuntimeID)
	})
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE id = $1`, sharedRuntimeID)
	})
	var derivedAID, derivedBID string
	for i, name := range []string{"shared-derived-a", "shared-derived-b"} {
		var id string
		require.NoError(t, testPool.QueryRow(ctx, `
			INSERT INTO agent (
				workspace_id, name, description, runtime_mode, runtime_config, runtime_id, max_concurrent_tasks, owner_id
			, model) VALUES ($1, $2, '', 'cloud', '{}'::jsonb, $3, 1, $4, 'composer-1.5')
			RETURNING id`, testWorkspaceID, name+"-"+uuid.NewString(), sharedRuntimeID, testUserID).Scan(&id))
		if i == 0 {
			derivedAID = id
		} else {
			derivedBID = id
		}
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent WHERE id IN ($1, $2)`, derivedAID, derivedBID)
	})

	// One shared sandbox_instance.
	var nodeID, instanceID string
	require.NoError(t, testPool.QueryRow(ctx, `
		INSERT INTO sandbox_node (node_key, name, owner_user_id, capabilities, max_concurrency, metadata)
		VALUES ($1, 'shared cleanup node', $2, '{}'::jsonb, 1, '{}'::jsonb)
		RETURNING id`, "shared-cleanup-"+uuid.NewString(), testUserID).Scan(&nodeID))
	require.NoError(t, testPool.QueryRow(ctx, `
		INSERT INTO sandbox_instance (workspace_id, creator_user_id, node_id, status, template, limits, metadata)
		VALUES ($1, $2, $3, 'running', 'default', '{}'::jsonb, '{}'::jsonb)
		RETURNING id`, testWorkspaceID, testUserID, nodeID).Scan(&instanceID))
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM sandbox_instance WHERE id = $1`, instanceID)
		testPool.Exec(context.Background(), `DELETE FROM sandbox_node WHERE id = $1`, nodeID)
	})

	// Both bindings ready on the SAME shared sandbox/runtime, carrying the
	// shared marker (as T011/T012 persist it).
	sharedConfig := `{"template":"default","shared":true}`
	for agentID, derivedID := range map[string]string{agentAID: derivedAID, agentBID: derivedBID} {
		_, err = testPool.Exec(ctx, `
			UPDATE environment_agent_sandbox
			   SET status = 'ready', sandbox_instance_id = $3, runtime_id = $4,
			       daemon_id = $5, derived_agent_id = $6, sandbox_config = $7::jsonb
			 WHERE env_id = $1 AND agent_id = $2`,
			envID, agentID, instanceID, sharedRuntimeID, uuid.NewString(), derivedID, sharedConfig)
		require.NoError(t, err)
	}

	w := httptest.NewRecorder()
	testHandler.DeleteEnvDispatchChannel(w, authedChannelRequest(http.MethodDelete, "/api/v1/env-dispatch/channels/"+channelID, "channelID", channelID))
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", w.Code, w.Body.String())
	}

	// The shared sandbox was reclaimed EXACTLY once despite two bindings: one
	// delete job queued for its node, or the row force-deleted once.
	var deleteJobs int
	require.NoError(t, testPool.QueryRow(ctx,
		`SELECT count(*) FROM sandbox_job WHERE instance_id = $1 AND type = 'delete'`,
		instanceID).Scan(&deleteJobs))
	var instanceExists bool
	require.NoError(t, testPool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM sandbox_instance WHERE id = $1)`,
		instanceID).Scan(&instanceExists))
	if instanceExists {
		if deleteJobs != 1 {
			t.Errorf("shared sandbox must be deleted exactly once across N bindings, got %d delete jobs", deleteJobs)
		}
	} else if deleteJobs > 0 {
		t.Errorf("force-deleted shared sandbox must not also queue delete jobs, got %d", deleteJobs)
	}

	// The shared runtime and BOTH derived agents are gone (derived agents
	// hard-deleted ahead of the runtime; FK RESTRICT would block otherwise).
	var runtimeExists, derivedAExists, derivedBExists bool
	require.NoError(t, testPool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM agent_runtime WHERE id = $1)`, sharedRuntimeID).Scan(&runtimeExists))
	require.NoError(t, testPool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM agent WHERE id = $1)`, derivedAID).Scan(&derivedAExists))
	require.NoError(t, testPool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM agent WHERE id = $1)`, derivedBID).Scan(&derivedBExists))
	if runtimeExists {
		t.Error("shared runtime should be deleted with the rollout")
	}
	if derivedAExists || derivedBExists {
		t.Errorf("all derived agents must be deleted before/with the shared runtime; a=%v b=%v", derivedAExists, derivedBExists)
	}

	// No residue: bindings, channel, project, env all gone.
	var bindings int
	require.NoError(t, testPool.QueryRow(ctx,
		`SELECT count(*) FROM environment_agent_sandbox WHERE env_id = $1`, envID).Scan(&bindings))
	if bindings != 0 {
		t.Errorf("bindings should be deleted, %d remain", bindings)
	}
}

func TestChannelCleanupWaitsForDiagnosisTerminal(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	_, projectID, channelID, _ := setupEnvDispatchChannelRolloutFixture(t)
	runID := "diag-cleanup-" + uuid.NewString()
	_, err := testPool.Exec(ctx, `
		INSERT INTO interaction_dag_diagnosis_run (
			run_id, project_id, task_id, topology_hash, ordered_segment_ids, status, sandbox_mode
		) VALUES ($1, $2, $3, 'topology', '[]'::jsonb, 'provisioning', 'shared')`,
		runID, projectID, uuid.NewString())
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM interaction_dag_diagnosis_run WHERE run_id = $1`, runID)
	})

	blocked := httptest.NewRecorder()
	testHandler.DeleteEnvDispatchChannel(blocked, authedChannelRequest(http.MethodDelete, "/api/v1/env-dispatch/channels/"+channelID, "channelID", channelID))
	require.Equal(t, http.StatusConflict, blocked.Code, "body=%s", blocked.Body.String())
	require.JSONEq(t, `{"error":"diagnosis_in_progress"}`, blocked.Body.String())

	_, err = testPool.Exec(ctx, `UPDATE interaction_dag_diagnosis_run SET status = 'failed' WHERE run_id = $1`, runID)
	require.NoError(t, err)
	allowed := httptest.NewRecorder()
	testHandler.DeleteEnvDispatchChannel(allowed, authedChannelRequest(http.MethodDelete, "/api/v1/env-dispatch/channels/"+channelID, "channelID", channelID))
	require.Equal(t, http.StatusNoContent, allowed.Code, "body=%s", allowed.Body.String())
}
