// SPDX-License-Identifier: Apache-2.0

package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/multica-ai/multica/server/internal/service"
)

// seedReadySharedBinding marks one environment_agent_sandbox row ready with a
// shared sandbox/runtime/daemon triple. Returns the triple IDs.
func seedReadySharedBinding(t *testing.T, envID, agentID, instanceID, runtimeID, daemonID string) {
	t.Helper()
	ctx := context.Background()
	_, err := testPool.Exec(ctx, `
		UPDATE environment_agent_sandbox
		   SET status = 'ready',
		       sandbox_instance_id = $3,
		       runtime_id = $4,
		       daemon_id = $5,
		       sandbox_config = '{"template":"default","shared":true}'::jsonb
		 WHERE env_id = $1 AND agent_id = $2`,
		envID, agentID, instanceID, runtimeID, daemonID)
	require.NoError(t, err)
}

// seedSharedBindingTargets creates the real FK targets required by a ready
// environment_agent_sandbox row and registers dependency-safe cleanup.
func seedSharedBindingTargets(t *testing.T) (instanceID, runtimeID string) {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, testPool.QueryRow(ctx, `
		INSERT INTO agent_runtime (workspace_id, name, runtime_mode, provider, status, metadata, last_seen_at)
		VALUES ($1, $2, 'cloud', 'pi', 'online', '{}'::jsonb, now())
		RETURNING id`, testWorkspaceID, "shared-diag-binding-rt-"+uuid.NewString()).Scan(&runtimeID))
	t.Cleanup(func() {
		_, err := testPool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE id = $1`, runtimeID)
		require.NoError(t, err)
	})

	var nodeID string
	require.NoError(t, testPool.QueryRow(ctx, `
		INSERT INTO sandbox_node (node_key, name, owner_user_id, capabilities, max_concurrency, metadata)
		VALUES ($1, 'shared diagnosis binding node', $2, '{}'::jsonb, 1, '{}'::jsonb)
		RETURNING id`, "shared-diag-binding-"+uuid.NewString(), testUserID).Scan(&nodeID))
	t.Cleanup(func() {
		_, err := testPool.Exec(context.Background(), `DELETE FROM sandbox_node WHERE id = $1`, nodeID)
		require.NoError(t, err)
	})

	require.NoError(t, testPool.QueryRow(ctx, `
		INSERT INTO sandbox_instance (workspace_id, creator_user_id, node_id, status, template, limits, metadata)
		VALUES ($1, $2, $3, 'running', 'default', '{}'::jsonb, '{}'::jsonb)
		RETURNING id`, testWorkspaceID, testUserID, nodeID).Scan(&instanceID))
	t.Cleanup(func() {
		_, err := testPool.Exec(context.Background(), `
			UPDATE environment_agent_sandbox
			   SET status = 'pending', sandbox_instance_id = NULL, runtime_id = NULL, daemon_id = NULL
			 WHERE sandbox_instance_id = $1 OR runtime_id = $2`, instanceID, runtimeID)
		require.NoError(t, err)
		_, err = testPool.Exec(context.Background(), `DELETE FROM sandbox_instance WHERE id = $1`, instanceID)
		require.NoError(t, err)
	})
	return instanceID, runtimeID
}

func TestResolveSharedDiagnosisBindingRequiresOneCanonicalRuntime(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	envID, projectID, channelID, agentAID := setupEnvDispatchChannelRolloutFixture(t)

	agentBID := createHandlerTestAgent(t, "Shared Binding Agent B", nil)
	_, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3)
		ON CONFLICT DO NOTHING`, channelID, testWorkspaceID, agentBID)
	require.NoError(t, err)
	_, err = testPool.Exec(ctx, `
		INSERT INTO environment_agent_sandbox (env_id, channel_id, agent_id, status, sandbox_config)
		VALUES ($1, $2, $3, 'pending', '{}'::jsonb)`, envID, channelID, agentBID)
	require.NoError(t, err)

	sharedSandboxID, sharedRuntimeID := seedSharedBindingTargets(t)
	sharedDaemonID := uuid.NewString()
	seedReadySharedBinding(t, envID, agentAID, sharedSandboxID, sharedRuntimeID, sharedDaemonID)
	seedReadySharedBinding(t, envID, agentBID, sharedSandboxID, sharedRuntimeID, sharedDaemonID)

	ref, err := testHandler.resolveSharedDiagnosisBinding(ctx, testWorkspaceID, projectID)
	require.NoError(t, err)
	require.NotNil(t, ref)
	assert.Equal(t, sharedSandboxID, ref.InstanceID)
	assert.Equal(t, sharedRuntimeID, ref.RuntimeID)
	assert.Equal(t, sharedDaemonID, ref.DaemonID)
}

func TestResolveSharedDiagnosisBindingReturnsNilForNonShared(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	envID, projectID, _, agentID := setupEnvDispatchChannelRolloutFixture(t)
	instanceID, runtimeID := seedSharedBindingTargets(t)
	_, err := testPool.Exec(ctx, `
		UPDATE environment_agent_sandbox
		   SET status = 'ready',
		       sandbox_instance_id = $3,
		       runtime_id = $4,
		       daemon_id = $5,
		       sandbox_config = '{"template":"default","shared":false}'::jsonb
		 WHERE env_id = $1 AND agent_id = $2`,
		envID, agentID, instanceID, runtimeID, uuid.NewString())
	require.NoError(t, err)

	ref, err := testHandler.resolveSharedDiagnosisBinding(ctx, testWorkspaceID, projectID)
	require.NoError(t, err)
	assert.Nil(t, ref)
}

func TestResolveSharedDiagnosisBindingFailsClosedOnDivergentTriples(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	envID, projectID, channelID, agentAID := setupEnvDispatchChannelRolloutFixture(t)

	agentBID := createHandlerTestAgent(t, "Shared Binding Divergent B", nil)
	_, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3)
		ON CONFLICT DO NOTHING`, channelID, testWorkspaceID, agentBID)
	require.NoError(t, err)
	_, err = testPool.Exec(ctx, `
		INSERT INTO environment_agent_sandbox (env_id, channel_id, agent_id, status, sandbox_config)
		VALUES ($1, $2, $3, 'pending', '{}'::jsonb)`, envID, channelID, agentBID)
	require.NoError(t, err)

	instanceAID, runtimeAID := seedSharedBindingTargets(t)
	instanceBID, runtimeBID := seedSharedBindingTargets(t)
	seedReadySharedBinding(t, envID, agentAID, instanceAID, runtimeAID, uuid.NewString())
	seedReadySharedBinding(t, envID, agentBID, instanceBID, runtimeBID, uuid.NewString())

	ref, err := testHandler.resolveSharedDiagnosisBinding(ctx, testWorkspaceID, projectID)
	require.Error(t, err)
	assert.Nil(t, ref)
	assert.True(t, strings.HasPrefix(err.Error(), "provisioning_binding:"))
}

func TestResolveSharedDiagnosisBindingFailsClosedOnIncompleteTriple(t *testing.T) {
	instanceID := uuid.NewString()
	daemonID := uuid.NewString()

	ref, err := mergeSharedDiagnosisBinding(nil, &instanceID, nil, &daemonID)
	require.Error(t, err)
	assert.Nil(t, ref)
	assert.True(t, strings.HasPrefix(err.Error(), "provisioning_binding:"))
}

func TestChannelCleanupWaitsForSharedDiagnosis(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	envID, projectID, channelID, agentID := setupEnvDispatchChannelRolloutFixture(t)

	var runtimeID, nodeID, instanceID string
	require.NoError(t, testPool.QueryRow(ctx, `
		INSERT INTO agent_runtime (workspace_id, name, runtime_mode, provider, status, metadata, last_seen_at)
		VALUES ($1, $2, 'cloud', 'pi', 'online', '{}'::jsonb, now())
		RETURNING id`, testWorkspaceID, "shared-diag-cleanup-rt-"+uuid.NewString()).Scan(&runtimeID))
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE id = $1`, runtimeID)
	})
	require.NoError(t, testPool.QueryRow(ctx, `
		INSERT INTO sandbox_node (node_key, name, owner_user_id, capabilities, max_concurrency, metadata)
		VALUES ($1, 'shared-diag-cleanup-node', $2, '{}'::jsonb, 1, '{}'::jsonb)
		RETURNING id`, "shared-diag-cleanup-"+uuid.NewString(), testUserID).Scan(&nodeID))
	require.NoError(t, testPool.QueryRow(ctx, `
		INSERT INTO sandbox_instance (workspace_id, creator_user_id, node_id, status, template, limits, metadata)
		VALUES ($1, $2, $3, 'running', 'default', '{}'::jsonb, '{}'::jsonb)
		RETURNING id`, testWorkspaceID, testUserID, nodeID).Scan(&instanceID))
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM sandbox_instance WHERE id = $1`, instanceID)
		testPool.Exec(context.Background(), `DELETE FROM sandbox_node WHERE id = $1`, nodeID)
	})
	seedReadySharedBinding(t, envID, agentID, instanceID, runtimeID, uuid.NewString())

	runID := "diag-shared-" + uuid.NewString()
	_, err := testPool.Exec(ctx, `
		INSERT INTO interaction_dag_diagnosis_run (
			run_id, project_id, task_id, topology_hash, ordered_segment_ids, status,
			sandbox_instance_id, capability_token_hash, execution_mode, sandbox_mode
		) VALUES (
			$1, $2, $3, 'topo', '["seg-1"]'::jsonb, 'running',
			$4, 'hash-not-a-token', 'sandbox', 'shared'
		)`, runID, projectID, uuid.NewString(), instanceID)
	require.NoError(t, err)
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM interaction_dag_diagnosis_run WHERE run_id = $1`, runID)
	})

	w := httptest.NewRecorder()
	testHandler.DeleteEnvDispatchChannel(w, authedChannelRequest(
		http.MethodDelete, "/api/v1/env-dispatch/channels/"+channelID, "channelID", channelID))
	require.Equal(t, http.StatusConflict, w.Code, "body=%s", w.Body.String())
	assert.Contains(t, w.Body.String(), `"error":"diagnosis_in_progress"`)

	var bindingAlive bool
	require.NoError(t, testPool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM environment_agent_sandbox WHERE env_id = $1)`, envID).Scan(&bindingAlive))
	assert.True(t, bindingAlive, "active shared diagnosis must keep the team sandbox binding alive")

	_, err = testPool.Exec(ctx, `
		UPDATE interaction_dag_diagnosis_run
		   SET status = 'completed', completed_at = now(), updated_at = now()
		 WHERE run_id = $1`, runID)
	require.NoError(t, err)

	w2 := httptest.NewRecorder()
	testHandler.DeleteEnvDispatchChannel(w2, authedChannelRequest(
		http.MethodDelete, "/api/v1/env-dispatch/channels/"+channelID, "channelID", channelID))
	require.Equal(t, http.StatusNoContent, w2.Code, "body=%s", w2.Body.String())
}

func TestProjectCleanupWaitsForActiveDiagnosis(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	_, projectID, _, _ := setupEnvDispatchChannelRolloutFixture(t)
	runID := "diag-project-cleanup-" + uuid.NewString()
	_, err := testPool.Exec(ctx, `
		INSERT INTO interaction_dag_diagnosis_run (
			run_id, project_id, task_id, topology_hash, ordered_segment_ids, status,
			sandbox_instance_id, capability_token_hash, execution_mode, sandbox_mode
		) VALUES (
			$1, $2, $3, 'topo', '["seg-1"]'::jsonb, 'running',
			$4, 'hash-not-a-token', 'sandbox', 'shared'
		)`, runID, projectID, uuid.NewString(), uuid.NewString())
	require.NoError(t, err)
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM interaction_dag_diagnosis_run WHERE run_id = $1`, runID)
	})

	w := httptest.NewRecorder()
	testHandler.DeleteEnvDispatchProject(w, authedChannelRequest(
		http.MethodDelete, "/api/v1/env-dispatch/"+projectID, "projectID", projectID))
	require.Equal(t, http.StatusConflict, w.Code, "body=%s", w.Body.String())
	assert.Contains(t, w.Body.String(), `"error":"diagnosis_in_progress"`)

	var projectAlive bool
	require.NoError(t, testPool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM project WHERE id = $1)`, projectID).Scan(&projectAlive))
	assert.True(t, projectAlive, "active diagnosis must keep the dispatch project alive")
}

func TestGetLatestEnvDispatchDiagnosisExposesSharedExecutionTriple(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	envID, projectID, channelID, agentID := setupEnvDispatchChannelRolloutFixture(t)
	teamSandboxID, runtimeID := seedSharedBindingTargets(t)
	daemonID := uuid.NewString()
	seedReadySharedBinding(t, envID, agentID, teamSandboxID, runtimeID, daemonID)

	runID := "diag-latest-" + uuid.NewString()
	store := service.NewDiagnosisStateStore(testHandler.Queries)
	_, err := store.CreateRun(ctx, service.DiagnosisRunCheckpoint{
		RunID:             runID,
		ProjectID:         projectID,
		TaskID:            uuid.NewString(),
		TopologyHash:      "topo-latest",
		OrderedSegmentIDs: []string{"seg-latest-1"},
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM interaction_dag_diagnosis_segment WHERE run_id = $1`, runID)
		testPool.Exec(ctx, `DELETE FROM interaction_dag_diagnosis_run WHERE run_id = $1`, runID)
	})
	require.NoError(t, store.SetRunSandbox(ctx, runID, teamSandboxID, "hash-not-a-token",
		service.DiagnosisExecutionModeSandbox, service.DiagnosisSandboxModeShared))

	w := httptest.NewRecorder()
	r := authedChannelRequest(http.MethodGet, "/api/v1/env-dispatch/channels/"+channelID+"/diagnosis/latest", "channelID", channelID)
	testHandler.GetLatestEnvDispatchChannelDiagnosis(w, r)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "sandbox", body["execution_mode"])
	assert.Equal(t, "shared", body["sandbox_mode"])
	assert.Equal(t, teamSandboxID, body["sandbox_instance_id"])
	assert.Equal(t, runtimeID, body["runtime_id"])
	assert.Equal(t, daemonID, body["daemon_id"])
	encoded := w.Body.String()
	assert.NotContains(t, encoded, "hash-not-a-token")
	assert.NotContains(t, strings.ToLower(encoded), "capability_token")
}
