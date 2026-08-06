package handler

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/multica-ai/multica/server/internal/service"
)

// sharedChannelFixture models a shared_sandbox env-dispatch channel rollout
// right after dispatch: the leader binding is ready on the sample's single
// shared sandbox + daemon runtime, and the squad members retain pending
// bindings stamped with the shared marker. Returns the rollout IDs plus the
// shared execution identity (sandbox S, runtime R, daemon D).
type sharedChannelFixture struct {
	envID, projectID, channelID            string
	leaderAgentID, memberAID, memberBID    string
	sandboxInstanceID, runtimeID, daemonID string
	sharedConfig                           json.RawMessage
}

func setupSharedChannelFixture(t *testing.T) sharedChannelFixture {
	t.Helper()
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	fx := sharedChannelFixture{sharedConfig: json.RawMessage(`{"template":"default","shared":true}`)}
	fx.leaderAgentID = createHandlerTestAgent(t, "Shared Channel Leader "+uuid.NewString(), nil)
	fx.memberAID = createHandlerTestAgent(t, "Shared Channel Member A "+uuid.NewString(), nil)
	fx.memberBID = createHandlerTestAgent(t, "Shared Channel Member B "+uuid.NewString(), nil)

	require.NoError(t, testPool.QueryRow(ctx, `
		INSERT INTO environment (workspace_id, sandbox_ids, mode)
		VALUES ($1, '{}', 'scratch') RETURNING id::text`, testWorkspaceID).Scan(&fx.envID))
	require.NoError(t, testPool.QueryRow(ctx, `
		INSERT INTO project (workspace_id, title, env_id)
		VALUES ($1, $2, $3) RETURNING id::text`, testWorkspaceID, "Shared Channel Project "+uuid.NewString(), fx.envID).Scan(&fx.projectID))
	require.NoError(t, testPool.QueryRow(ctx, `
		INSERT INTO channel (workspace_id, name, kind, project_id, created_by)
		VALUES ($1, $2, 'group', $3, $4) RETURNING id::text`,
		testWorkspaceID, "env-dispatch-shared-"+uuid.NewString(), fx.projectID, testUserID).Scan(&fx.channelID))
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM agent WHERE source_agent_id = ANY($1)`, []string{fx.leaderAgentID, fx.memberAID, fx.memberBID})
		_, _ = testPool.Exec(context.Background(), `DELETE FROM chat_session WHERE project_id = $1`, fx.projectID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM channel WHERE id = $1`, fx.channelID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM project WHERE id = $1`, fx.projectID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM environment WHERE id = $1`, fx.envID)
	})
	for _, agentID := range []string{fx.leaderAgentID, fx.memberAID, fx.memberBID} {
		_, err := testPool.Exec(ctx, `
			INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
			VALUES ($1, $2, 'agent', $3) ON CONFLICT DO NOTHING`, fx.channelID, testWorkspaceID, agentID)
		require.NoError(t, err)
	}

	// The sample's single shared sandbox_instance.
	var nodeID string
	require.NoError(t, testPool.QueryRow(ctx, `
		INSERT INTO sandbox_node (node_key, name, owner_user_id, capabilities, max_concurrency, metadata)
		VALUES ($1, 'shared channel node', $2, '{}'::jsonb, 1, '{}'::jsonb)
		RETURNING id::text`, "shared-channel-"+uuid.NewString(), testUserID).Scan(&nodeID))
	require.NoError(t, testPool.QueryRow(ctx, `
		INSERT INTO sandbox_instance (workspace_id, creator_user_id, node_id, status, template, limits, metadata)
		VALUES ($1, $2, $3, 'running', 'default', '{}'::jsonb, '{}'::jsonb)
		RETURNING id::text`, testWorkspaceID, testUserID, nodeID).Scan(&fx.sandboxInstanceID))
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM sandbox_instance WHERE id = $1`, fx.sandboxInstanceID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM sandbox_node WHERE id = $1`, nodeID)
	})

	// The sample's single shared daemon runtime (the leader's derived agent is
	// bound to it; its cleanup is owned by setupBoundRuntimeAgent).
	_, fx.runtimeID = setupBoundRuntimeAgent(t, "pi")
	fx.daemonID = uuid.NewString()

	store := envDispatchChannelStore{}
	for _, agentID := range []string{fx.leaderAgentID, fx.memberAID, fx.memberBID} {
		require.NoError(t, store.insertBinding(ctx, testPool, envAgentSandboxBinding{
			EnvID: fx.envID, ChannelID: fx.channelID, SourceAgentID: agentID,
			ModelConfigOwnerAgentID: agentID,
			Status:                  "pending", SandboxConfig: fx.sharedConfig,
		}))
	}
	// The leader was provisioned eagerly at dispatch time onto the shared
	// sandbox/runtime (the CHECK constraint requires all three handles once
	// status is 'ready').
	_, err := testPool.Exec(ctx, `
		UPDATE environment_agent_sandbox
		   SET status = 'ready', sandbox_instance_id = $2, runtime_id = $3, daemon_id = $4
		 WHERE env_id = $1 AND agent_id = $5`,
		fx.envID, fx.sandboxInstanceID, fx.runtimeID, fx.daemonID, fx.leaderAgentID)
	require.NoError(t, err)
	return fx
}

func (fx sharedChannelFixture) provisionInput(agentID string) ProvisionEnvDispatchAgentInput {
	return ProvisionEnvDispatchAgentInput{
		WorkspaceID:   testWorkspaceID,
		UserID:        testUserID,
		EnvID:         fx.envID,
		ProjectID:     fx.projectID,
		ChannelID:     fx.channelID,
		AgentID:       agentID,
		SandboxConfig: fx.sharedConfig,
	}
}

// countSandboxInstances reports how many sandbox_instance rows the fixture's
// workspace holds; the shared attach path must never add one.
func countSandboxInstances(t *testing.T) int {
	t.Helper()
	var n int
	require.NoError(t, testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM sandbox_instance WHERE workspace_id = $1`, testWorkspaceID).Scan(&n))
	return n
}

// TestProvisionEnvDispatchAgentSharedAttachesToSharedRuntime is the T008 core
// assertion (research D3): a non-leader squad member's first mention attaches
// to the sample's existing shared runtime — no new sandbox is created, the
// derived agent is cloned onto the shared runtime, and the binding is marked
// ready with the shared identifiers.
func TestProvisionEnvDispatchAgentSharedAttachesToSharedRuntime(t *testing.T) {
	fx := setupSharedChannelFixture(t)
	ctx := context.Background()

	before := countSandboxInstances(t)
	res, err := testHandler.provisionEnvDispatchAgent(ctx, fx.provisionInput(fx.memberAID))
	require.NoError(t, err)
	require.Equal(t, fx.sandboxInstanceID, res.SandboxInstanceID, "member must attach to the sample's shared sandbox")
	require.Equal(t, fx.runtimeID, res.RuntimeID, "member must attach to the sample's shared runtime")
	require.Equal(t, fx.daemonID, res.DaemonID)
	require.NotEmpty(t, res.ChatSessionID)
	require.NotEqual(t, fx.memberAID, res.AgentID, "the execution identity is a derived agent, not the source")
	require.Equal(t, before, countSandboxInstances(t), "shared attach must not create a new sandbox_instance")

	// The binding is ready with the shared identifiers and the derived agent.
	var status, sandboxID, runtimeID, daemonID, derivedID string
	require.NoError(t, testPool.QueryRow(ctx, `
		SELECT status, sandbox_instance_id::text, runtime_id::text, daemon_id::text, derived_agent_id::text
		FROM environment_agent_sandbox WHERE env_id = $1 AND agent_id = $2`,
		fx.envID, fx.memberAID).Scan(&status, &sandboxID, &runtimeID, &daemonID, &derivedID))
	require.Equal(t, "ready", status)
	require.Equal(t, fx.sandboxInstanceID, sandboxID)
	require.Equal(t, fx.runtimeID, runtimeID)
	require.Equal(t, fx.daemonID, daemonID)
	require.Equal(t, res.AgentID, derivedID)

	// The derived agent and its channel session are bound to the shared
	// runtime, so session.RuntimeID == binding.RuntimeID (T013 agreement).
	var agentRuntime, sessionRuntime string
	require.NoError(t, testPool.QueryRow(ctx,
		`SELECT runtime_id::text FROM agent WHERE id = $1`, res.AgentID).Scan(&agentRuntime))
	require.Equal(t, fx.runtimeID, agentRuntime)
	require.NoError(t, testPool.QueryRow(ctx, `
		SELECT session.runtime_id::text
		FROM channel_agent_session binding
		JOIN chat_session session ON session.id = binding.chat_session_id
		WHERE binding.channel_id = $1 AND binding.agent_id = $2`,
		fx.channelID, res.AgentID).Scan(&sessionRuntime))
	require.Equal(t, fx.runtimeID, sessionRuntime)
}

func TestProvisionEnvDispatchAgentSharedPersistsExecutionModel(t *testing.T) {
	fx := setupSharedChannelFixture(t)
	fx.sharedConfig = json.RawMessage(`{"template":"default","shared":true,"execution_model":"env-peer-2/model-b"}`)

	res, err := testHandler.provisionEnvDispatchAgent(context.Background(), fx.provisionInput(fx.memberAID))
	require.NoError(t, err)
	var model string
	require.NoError(t, testPool.QueryRow(context.Background(),
		`SELECT model FROM agent WHERE id = $1`, res.AgentID).Scan(&model))
	require.Equal(t, "env-peer-2/model-b", model)
}

// TestProvisionEnvDispatchAgentSharedConcurrentFirstMentions covers the T008
// concurrency case: two squad members first-mentioned at the same time both
// resolve to the same shared runtime, idempotently — one sandbox, one runtime,
// one derived agent per member.
func TestProvisionEnvDispatchAgentSharedConcurrentFirstMentions(t *testing.T) {
	fx := setupSharedChannelFixture(t)
	ctx := context.Background()

	before := countSandboxInstances(t)
	results := make([]ProvisionEnvDispatchAgentResult, 2)
	errs := make([]error, 2)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i, agentID := range []string{fx.memberAID, fx.memberBID} {
		wg.Add(1)
		go func(index int, id string) {
			defer wg.Done()
			<-start
			results[index], errs[index] = testHandler.provisionEnvDispatchAgent(ctx, fx.provisionInput(id))
		}(i, agentID)
	}
	close(start)
	wg.Wait()
	for _, err := range errs {
		require.NoError(t, err)
	}
	for _, res := range results {
		require.Equal(t, fx.sandboxInstanceID, res.SandboxInstanceID)
		require.Equal(t, fx.runtimeID, res.RuntimeID)
	}
	require.NotEqual(t, results[0].AgentID, results[1].AgentID, "each member keeps its own derived execution agent")
	require.Equal(t, before, countSandboxInstances(t), "concurrent shared attaches must not create sandboxes")

	// A repeated mention of an already-attached member is idempotent: same
	// derived identity, same shared runtime, still no new sandbox.
	again, err := testHandler.provisionEnvDispatchAgent(ctx, fx.provisionInput(fx.memberAID))
	require.NoError(t, err)
	require.Equal(t, results[0].AgentID, again.AgentID)
	require.Equal(t, fx.runtimeID, again.RuntimeID)
	require.Equal(t, before, countSandboxInstances(t))
}

// TestEnqueueEnvDispatchChannelRunSharedRuntimeAgreement pins the T013
// invariant for a shared-mode member run: session.RuntimeID ==
// binding.RuntimeID == task runtime, all equal to the sample's shared runtime,
// and a mismatched runtime is rejected.
func TestEnqueueEnvDispatchChannelRunSharedRuntimeAgreement(t *testing.T) {
	fx := setupSharedChannelFixture(t)
	ctx := context.Background()
	adapter := &envDispatchDepsAdapter{h: testHandler}

	res, err := testHandler.provisionEnvDispatchAgent(ctx, fx.provisionInput(fx.memberAID))
	require.NoError(t, err)
	messageID, err := adapter.CreateChannelMessage(ctx, fx.channelID, testWorkspaceID, testUserID, "work on the shared codebase")
	require.NoError(t, err)

	taskID, err := adapter.EnqueueEnvDispatchChannelRun(ctx, testWorkspaceID, testUserID, service.ChannelRunInput{
		AgentID:           res.AgentID,
		ChannelID:         fx.channelID,
		ProjectID:         fx.projectID,
		EnvID:             fx.envID,
		ChatSessionID:     res.ChatSessionID,
		SandboxInstanceID: res.SandboxInstanceID,
		RuntimeID:         res.RuntimeID,
		SourceMessageID:   messageID,
	}, 0)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM agent_inbox_event WHERE id = $1`, taskID)
	})

	var taskRuntime, taskSession string
	var taskContext []byte
	require.NoError(t, testPool.QueryRow(ctx,
		`SELECT runtime_id::text, chat_session_id::text, context FROM agent_inbox_event WHERE id = $1`,
		taskID).Scan(&taskRuntime, &taskSession, &taskContext))
	require.Equal(t, fx.runtimeID, taskRuntime, "task runtime must be the sample's shared runtime")
	require.Equal(t, res.ChatSessionID, taskSession)
	marker, ok := service.ExtractEphemeralSandbox(taskContext)
	require.True(t, ok)
	require.Equal(t, fx.sandboxInstanceID, marker.SandboxInstanceID, "ephemeral marker must stamp the shared sandbox_instance_id")

	// A run enqueued against a different runtime violates the agreement and
	// is rejected before any task is created.
	_, err = adapter.EnqueueEnvDispatchChannelRun(ctx, testWorkspaceID, testUserID, service.ChannelRunInput{
		AgentID:           res.AgentID,
		ChannelID:         fx.channelID,
		ProjectID:         fx.projectID,
		EnvID:             fx.envID,
		ChatSessionID:     res.ChatSessionID,
		SandboxInstanceID: res.SandboxInstanceID,
		RuntimeID:         uuid.NewString(),
		SourceMessageID:   messageID,
	}, 0)
	require.ErrorContains(t, err, "session identity mismatch")
}
