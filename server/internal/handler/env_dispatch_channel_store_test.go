package handler

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/multica-ai/multica/server/internal/service"
)

func TestEnvDispatchChannelStoreClaimProvisioningIsSingleWinner(t *testing.T) {
	if testPool == nil {
		t.Skip("database not available")
	}
	ctx, store, envID, channelID, agentID := setupEnvDispatchChannelStoreFixture(t)
	tx, err := testPool.Begin(ctx)
	require.NoError(t, err)
	defer tx.Rollback(ctx)

	binding := envAgentSandboxBinding{
		EnvID: envID, ChannelID: channelID, SourceAgentID: agentID,
		Status: "pending", SandboxConfig: json.RawMessage(`{"template":"default"}`),
	}
	require.NoError(t, store.insertBinding(ctx, tx, binding))
	won, got, err := store.claimProvisioning(ctx, tx, envID, agentID)
	require.NoError(t, err)
	require.True(t, won)
	require.Equal(t, "credential_ready", got.Status)
	require.Empty(t, got.ModelConfigOwnerAgentID, "nullable legacy owner must normalize to the empty domain value")

	won, got, err = store.claimProvisioning(ctx, tx, envID, agentID)
	require.NoError(t, err)
	require.False(t, won)
	require.Equal(t, "credential_ready", got.Status)
}

func TestEnvDispatchChannelStoreRejectsTriggerAgentOutsideChannel(t *testing.T) {
	if testPool == nil {
		t.Skip("database not available")
	}
	ctx, store, envID, channelID, _ := setupEnvDispatchChannelStoreFixture(t)
	outsideAgentID := createHandlerTestAgent(t, "Env Dispatch Trigger Outsider", nil)
	projectID := projectIDForEnvDispatchStoreFixture(t, ctx, envID)
	trigger := validEnvCollaborationTrigger(outsideAgentID, channelID, projectID)
	require.NoError(t, store.saveTrigger(ctx, testPool, envID, trigger))

	_, err := store.loadTrigger(ctx, testPool, envID, testWorkspaceID)
	require.Error(t, err)
}

func TestEnvDispatchChannelStoreMarkDeletingBlocksProvisioning(t *testing.T) {
	if testPool == nil {
		t.Skip("database not available")
	}
	ctx, store, envID, channelID, agentID := setupEnvDispatchChannelStoreFixture(t)
	binding := envAgentSandboxBinding{
		EnvID: envID, ChannelID: channelID, SourceAgentID: agentID,
		Status: "pending", SandboxConfig: json.RawMessage(`{"template":"default"}`),
	}
	require.NoError(t, store.insertBinding(ctx, testPool, binding))
	require.NoError(t, store.markDeleting(ctx, testPool, envID))

	won, got, err := store.claimProvisioning(ctx, testPool, envID, agentID)
	require.NoError(t, err)
	require.False(t, won)
	require.Equal(t, "deleting", got.Status)
}

func TestDeleteAgentRuntimeReclaimsReadyEnvDispatchBinding(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx, store, envID, channelID, agentID := setupEnvDispatchChannelStoreFixture(t)

	var runtimeID, nodeID, sandboxID string
	require.NoError(t, testPool.QueryRow(ctx, `
		INSERT INTO agent_runtime (workspace_id, name, runtime_mode, provider, status, metadata, last_seen_at)
		VALUES ($1, $2, 'cloud', 'pi', 'online', '{}'::jsonb, now())
		RETURNING id::text`, testWorkspaceID, "env-dispatch-reclaim-"+uuid.NewString()).Scan(&runtimeID))
	require.NoError(t, testPool.QueryRow(ctx, `
		INSERT INTO sandbox_node (node_key, name, owner_user_id, capabilities, max_concurrency, metadata)
		VALUES ($1, 'env dispatch reclaim node', $2, '{}'::jsonb, 1, '{}'::jsonb)
		RETURNING id::text`, "env-dispatch-reclaim-"+uuid.NewString(), testUserID).Scan(&nodeID))
	require.NoError(t, testPool.QueryRow(ctx, `
		INSERT INTO sandbox_instance (workspace_id, creator_user_id, node_id, status, template, limits, metadata)
		VALUES ($1, $2, $3, 'running', 'default', '{}'::jsonb, '{}'::jsonb)
		RETURNING id::text`, testWorkspaceID, testUserID, nodeID).Scan(&sandboxID))
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM sandbox_instance WHERE id = $1`, sandboxID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM sandbox_node WHERE id = $1`, nodeID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE id = $1`, runtimeID)
	})

	require.NoError(t, store.insertBinding(ctx, testPool, envAgentSandboxBinding{
		EnvID: envID, ChannelID: channelID, SourceAgentID: agentID,
		Status: "pending", SandboxConfig: json.RawMessage(`{"template":"default"}`),
	}))
	require.NoError(t, testPool.QueryRow(ctx, `
		UPDATE environment_agent_sandbox
		SET status = 'ready', sandbox_instance_id = $3, runtime_id = $4, daemon_id = $5
		WHERE env_id = $1 AND agent_id = $2
		RETURNING status`, envID, agentID, sandboxID, runtimeID, uuid.NewString()).Scan(new(string)))

	adapter := &envDispatchDepsAdapter{h: testHandler}
	require.NoError(t, adapter.DeleteAgentRuntime(ctx, testWorkspaceID, runtimeID))
	require.NoError(t, adapter.DeleteAgentRuntime(ctx, testWorkspaceID, runtimeID), "repeat reclaim must be a no-op")

	var runtimeCount int
	require.NoError(t, testPool.QueryRow(ctx, `SELECT count(*) FROM agent_runtime WHERE id = $1`, runtimeID).Scan(&runtimeCount))
	require.Zero(t, runtimeCount)

	binding, err := store.binding(ctx, testPool, envID, agentID)
	require.NoError(t, err)
	require.Equal(t, "failed_retryable", binding.Status)
	require.Nil(t, binding.SandboxInstanceID)
	require.Nil(t, binding.RuntimeID)
	require.Nil(t, binding.DaemonID)

	won, claimed, err := store.claimProvisioning(ctx, testPool, envID, agentID)
	require.NoError(t, err)
	require.True(t, won)
	require.Equal(t, "credential_ready", claimed.Status)
}

func setupEnvDispatchChannelStoreFixture(t *testing.T) (context.Context, envDispatchChannelStore, string, string, string) {
	t.Helper()
	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "Env Dispatch Channel Store Agent", nil)
	var envID, projectID, channelID string
	require.NoError(t, testPool.QueryRow(ctx, `
		INSERT INTO environment (workspace_id, sandbox_ids, mode)
		VALUES ($1, '{}', 'scratch') RETURNING id`, testWorkspaceID).Scan(&envID))
	t.Cleanup(func() { _, _ = testPool.Exec(context.Background(), `DELETE FROM environment WHERE id = $1`, envID) })
	require.NoError(t, testPool.QueryRow(ctx, `
		INSERT INTO project (workspace_id, title, env_id)
		VALUES ($1, $2, $3) RETURNING id`, testWorkspaceID, "Env Dispatch Channel Store Project "+uuid.NewString(), envID).Scan(&projectID))
	t.Cleanup(func() { _, _ = testPool.Exec(context.Background(), `DELETE FROM project WHERE id = $1`, projectID) })
	require.NoError(t, testPool.QueryRow(ctx, `
		INSERT INTO channel (workspace_id, name, kind, project_id, created_by)
		VALUES ($1, $2, 'group', $3, $4) RETURNING id`, testWorkspaceID, "env-dispatch-store-"+uuid.NewString(), projectID, testUserID).Scan(&channelID))
	t.Cleanup(func() { _, _ = testPool.Exec(context.Background(), `DELETE FROM channel WHERE id = $1`, channelID) })
	_, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3)
ON CONFLICT DO NOTHING`, channelID, testWorkspaceID, agentID)
	require.NoError(t, err)

	return ctx, envDispatchChannelStore{db: testPool}, envID, channelID, agentID
}

func projectIDForEnvDispatchStoreFixture(t *testing.T, ctx context.Context, envID string) string {
	t.Helper()
	var projectID string
	require.NoError(t, testPool.QueryRow(ctx, `SELECT id FROM project WHERE env_id = $1`, envID).Scan(&projectID))
	return projectID
}

func validEnvCollaborationTrigger(agentID, channelID, projectID string) envCollaborationTrigger {
	return envCollaborationTrigger{
		AgentID: agentID, Kind: "mention", ChannelID: channelID, ProjectID: projectID,
		ChatSessionID: uuid.NewString(), SourceMessageID: uuid.NewString(),
		TaskID: uuid.NewString(), RuntimeID: uuid.NewString(),
	}
}

// TestEnvDispatchChannelStoreClaimProvisioningRetainsRuntimePolicy verifies the
// provisioning claim path returns the stored runtime policy intact, so
// provisionEnvDispatchAgent can decode it and pass the runtime to the sandbox
// lifecycle. This is the exact read path provisioning uses (claim -> decode).
func TestEnvDispatchChannelStoreClaimProvisioningRetainsRuntimePolicy(t *testing.T) {
	if testPool == nil {
		t.Skip("database not available")
	}
	ctx, store, envID, channelID, agentID := setupEnvDispatchChannelStoreFixture(t)
	tx, err := testPool.Begin(ctx)
	require.NoError(t, err)
	defer tx.Rollback(ctx)

	policy := service.ResolvedPerAgentSandboxPolicy{
		Template: "default",
		Runtime: &service.ExternalModelRuntime{
			BaseURL: "https://provider.invalid/v1",
			APIKey:  "synthetic-secret-for-tests",
			Model:   "model-a",
		},
	}
	config, err := marshalEnvDispatchSandboxConfig(policy)
	require.NoError(t, err)
	require.NoError(t, store.insertBinding(ctx, tx, envAgentSandboxBinding{
		EnvID: envID, ChannelID: channelID, SourceAgentID: agentID,
		Status: "pending", SandboxConfig: config,
	}))

	won, got, err := store.claimProvisioning(ctx, tx, envID, agentID)
	require.NoError(t, err)
	require.True(t, won)
	require.Equal(t, "credential_ready", got.Status)

	decoded, err := decodeEnvDispatchSandboxConfig(got.SandboxConfig)
	require.NoError(t, err)
	require.Equal(t, "default", decoded.Template)
	require.NotNil(t, decoded.Runtime)
	require.Equal(t, "https://provider.invalid/v1", decoded.Runtime.BaseURL)
	require.Equal(t, "synthetic-secret-for-tests", decoded.Runtime.APIKey)
	require.Equal(t, "model-a", decoded.Runtime.Model)

	// createInput carries the runtime into the sandbox creation payload.
	in, err := decoded.createInput(testWorkspaceID, "daemon-1")
	require.NoError(t, err)
	require.Equal(t, "default", in.Template)
	require.True(t, in.DaemonEnabled)
	require.Equal(t, "daemon-1", in.RuntimeEnv["MULTICA_DAEMON_ID"])
	require.JSONEq(t, `{"provider":"openai","base_url":"https://provider.invalid/v1","api_key":"synthetic-secret-for-tests","model":"model-a"}`, string(in.Runtime))
}

// TestEnvDispatchAdapter_CreateChannelPersistsCanonicalPolicy verifies the
// adapter's CreateEnvDispatchChannel persists the resolved per-agent runtime
// policy as canonical JSON (trimmed values, template "default") in the
// environment_agent_sandbox.sandbox_config column, while a member without an
// override persists "{}".
func TestEnvDispatchAdapter_CreateChannelPersistsCanonicalPolicy(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx, store, envID, _, agentID := setupEnvDispatchChannelStoreFixture(t)
	projectID := projectIDForEnvDispatchStoreFixture(t, ctx, envID)
	otherAgent := createHandlerTestAgent(t, "Env Dispatch Other Agent", nil)

	adapter := &envDispatchDepsAdapter{h: testHandler}
	policy := service.ResolvedPerAgentSandboxPolicy{
		Template: "  default  ",
		Runtime: &service.ExternalModelRuntime{
			BaseURL: " https://provider.invalid/v1 ",
			APIKey:  " synthetic-secret-for-tests ",
			Model:   " model-a ",
		},
	}
	roster := service.MessageRoster{LeaderID: agentID, AgentIDs: []string{agentID, otherAgent}}
	specs := map[string]service.ResolvedPerAgentSandboxPolicy{agentID: policy}

	channelID, err := adapter.CreateEnvDispatchChannel(ctx, testWorkspaceID, testUserID, projectID, envID, roster, specs)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = testPool.Exec(context.Background(), `DELETE FROM channel WHERE id = $1`, channelID) })

	// Configured member: canonical trimmed runtime + template default.
	binding, err := store.binding(ctx, testPool, envID, agentID)
	require.NoError(t, err)
	decoded, err := decodeEnvDispatchSandboxConfig(binding.SandboxConfig)
	require.NoError(t, err)
	require.Equal(t, "default", decoded.Template)
	require.NotNil(t, decoded.Runtime)
	require.Equal(t, "https://provider.invalid/v1", decoded.Runtime.BaseURL)
	require.Equal(t, "synthetic-secret-for-tests", decoded.Runtime.APIKey)
	require.Equal(t, "model-a", decoded.Runtime.Model)
	require.Equal(t, agentID, binding.SourceAgentID)
	require.Equal(t, agentID, binding.ModelConfigOwnerAgentID)

	// Unconfigured member: "{}" persists, decoding to default template.
	other, err := store.binding(ctx, testPool, envID, otherAgent)
	require.NoError(t, err)
	require.JSONEq(t, `{}`, string(other.SandboxConfig))
	otherDecoded, err := decodeEnvDispatchSandboxConfig(other.SandboxConfig)
	require.NoError(t, err)
	require.Equal(t, "default", otherDecoded.Template)
	require.Nil(t, otherDecoded.Runtime)
}

func TestEnvDispatchChannelJoinSourceIsSynthetic(t *testing.T) {
	require.Equal(t, "env_dispatch", envDispatchChannelJoinSource)
}

func TestEnvDispatchAdapter_CreateChannelSuppressesSourceAgentOnboarding(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx, _, envID, _, agentID := setupEnvDispatchChannelStoreFixture(t)
	projectID := projectIDForEnvDispatchStoreFixture(t, ctx, envID)
	adapter := &envDispatchDepsAdapter{h: testHandler}

	channelID, err := adapter.CreateEnvDispatchChannel(
		ctx, testWorkspaceID, testUserID, projectID, envID,
		service.MessageRoster{LeaderID: agentID, AgentIDs: []string{agentID}}, nil,
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM channel WHERE id = $1`, channelID)
	})

	var joinSource string
	require.NoError(t, testPool.QueryRow(ctx, `
		SELECT join_source FROM channel_member
		WHERE channel_id = $1 AND member_type = 'agent' AND member_id = $2`,
		channelID, agentID).Scan(&joinSource))
	require.Equal(t, envDispatchChannelJoinSource, joinSource)

	for name, query := range map[string]string{
		"onboarding": `SELECT count(*) FROM channel_agent_onboarding WHERE channel_id = $1 AND agent_id = $2`,
		"join message": `SELECT count(*) FROM channel_message message
			JOIN channel_member member
			  ON member.generation_id = message.membership_generation_id
			WHERE message.channel_id = $1 AND member.member_id = $2`,
		"channel session": `SELECT count(*) FROM channel_agent_session WHERE channel_id = $1 AND agent_id = $2`,
	} {
		var count int
		require.NoError(t, testPool.QueryRow(ctx, query, channelID, agentID).Scan(&count), name)
		require.Zero(t, count, name)
	}
}

// TestEnvDispatchBindingIdentityAndRetryState verifies the expanded binding
// identity (id, source_agent_id, model_config_owner_agent_id) is persisted and
// that the single-flight claim transitions a pending binding to
// credential_ready, the first-address entry state of the derived provisioning
// state machine.
func TestEnvDispatchBindingIdentityAndRetryState(t *testing.T) {
	if testPool == nil {
		t.Skip("database not available")
	}
	ctx, store, envID, channelID, sourceID := setupEnvDispatchChannelStoreFixture(t)
	b := envAgentSandboxBinding{
		ID: uuid.NewString(), EnvID: envID, ChannelID: channelID,
		SourceAgentID: sourceID, Status: "pending",
		ModelConfigOwnerAgentID: sourceID,
		SandboxConfig:           json.RawMessage(`{"template":"default"}`),
	}
	require.NoError(t, store.insertBinding(ctx, testPool, b))
	won, claimed, err := store.claimProvisioning(ctx, testPool, envID, sourceID)
	require.NoError(t, err)
	require.True(t, won)
	require.Equal(t, b.ID, claimed.ID)
	require.Equal(t, sourceID, claimed.SourceAgentID)
	require.Equal(t, sourceID, claimed.ModelConfigOwnerAgentID)
	require.Equal(t, "credential_ready", claimed.Status)
}

// TestAgentLineageRejectsCrossWorkspaceSource verifies the agent.source_agent_id
// foreign key is workspace-scoped: a derived agent cannot record a source agent
// from another workspace. The insert supplies a valid foreign-workspace runtime
// so it clears agent.runtime_id NOT NULL and the only failing constraint is the
// (workspace_id, source_agent_id) -> agent(workspace_id, id) FK.
func TestAgentLineageRejectsCrossWorkspaceSource(t *testing.T) {
	if testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	sourceAgentID := createHandlerTestAgent(t, "Lineage Source Agent", nil)
	foreignWS := createOtherTestWorkspace(t)

	var foreignRuntimeID string
	require.NoError(t, testPool.QueryRow(ctx, `
		INSERT INTO agent_runtime (workspace_id, name, runtime_mode, provider, status)
		VALUES ($1, $2, 'cloud', 'multica_agent', 'offline') RETURNING id`,
		foreignWS, "lineage-foreign-runtime-"+uuid.NewString()).Scan(&foreignRuntimeID))
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE id = $1`, foreignRuntimeID)
	})

	_, err := testPool.Exec(ctx, `
		INSERT INTO agent (
			workspace_id, name, display_name, description, runtime_mode, runtime_config,
			runtime_id, max_concurrent_tasks, owner_id,
			instructions, custom_env, custom_args, mcp_config, source_agent_id
		, model)
		VALUES ($1, $2, $2, '', 'cloud', '{}'::jsonb, $3, 1, $4, '', '{}'::jsonb, '[]'::jsonb, '{}'::jsonb, $5, 'composer-1.5')`,
		foreignWS, "derived-cross-ws-"+uuid.NewString(), foreignRuntimeID, testUserID, sourceAgentID)
	require.Error(t, err, "cross-workspace source_agent_id must be rejected by the workspace-scoped FK")
}
