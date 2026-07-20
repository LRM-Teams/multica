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
		EnvID: envID, ChannelID: channelID, AgentID: agentID,
		Status: "pending", SandboxConfig: json.RawMessage(`{"template":"default"}`),
	}
	require.NoError(t, store.insertBinding(ctx, tx, binding))
	won, got, err := store.claimProvisioning(ctx, tx, envID, agentID)
	require.NoError(t, err)
	require.True(t, won)
	require.Equal(t, "provisioning", got.Status)

	won, got, err = store.claimProvisioning(ctx, tx, envID, agentID)
	require.NoError(t, err)
	require.False(t, won)
	require.Equal(t, "provisioning", got.Status)
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
		EnvID: envID, ChannelID: channelID, AgentID: agentID,
		Status: "pending", SandboxConfig: json.RawMessage(`{"template":"default"}`),
	}
	require.NoError(t, store.insertBinding(ctx, testPool, binding))
	require.NoError(t, store.markDeleting(ctx, testPool, envID))

	won, got, err := store.claimProvisioning(ctx, testPool, envID, agentID)
	require.NoError(t, err)
	require.False(t, won)
	require.Equal(t, "deleting", got.Status)
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
		VALUES ($1, $2, 'agent', $3)`, channelID, testWorkspaceID, agentID)
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
		EnvID: envID, ChannelID: channelID, AgentID: agentID,
		Status: "pending", SandboxConfig: config,
	}))

	won, got, err := store.claimProvisioning(ctx, tx, envID, agentID)
	require.NoError(t, err)
	require.True(t, won)
	require.Equal(t, "provisioning", got.Status)

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
	require.JSONEq(t, `{"base_url":"https://provider.invalid/v1","api_key":"synthetic-secret-for-tests","model":"model-a"}`, string(in.Runtime))
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

	// Unconfigured member: "{}" persists, decoding to default template.
	other, err := store.binding(ctx, testPool, envID, otherAgent)
	require.NoError(t, err)
	require.JSONEq(t, `{}`, string(other.SandboxConfig))
	otherDecoded, err := decodeEnvDispatchSandboxConfig(other.SandboxConfig)
	require.NoError(t, err)
	require.Equal(t, "default", otherDecoded.Template)
	require.Nil(t, otherDecoded.Runtime)
}
