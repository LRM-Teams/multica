package handler

import (
	"context"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/multica-ai/multica/server/internal/service"
)

func setupEnvDispatchChannelSessionFixture(t *testing.T) (context.Context, envDispatchChannelSessionInput) {
	t.Helper()
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "Env Dispatch Session Agent "+uuid.NewString(), nil)
	var projectID, channelID string
	require.NoError(t, testPool.QueryRow(ctx, `
		INSERT INTO project (workspace_id, title)
		VALUES ($1, $2) RETURNING id::text`,
		testWorkspaceID, "Env Dispatch Session Project "+uuid.NewString()).Scan(&projectID))
	require.NoError(t, testPool.QueryRow(ctx, `
		INSERT INTO channel (workspace_id, name, kind, project_id, created_by)
		VALUES ($1, $2, 'group', $3, $4) RETURNING id::text`,
		testWorkspaceID, "env-dispatch-session-"+uuid.NewString(), projectID, testUserID).Scan(&channelID))
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM channel WHERE id = $1`, channelID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM chat_session WHERE project_id = $1 AND agent_id = $2`, projectID, agentID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM project WHERE id = $1`, projectID)
	})
	return ctx, envDispatchChannelSessionInput{
		WorkspaceID: testWorkspaceID,
		ProjectID:   projectID,
		ChannelID:   channelID,
		AgentID:     agentID,
		CreatorID:   testUserID,
		RuntimeID:   testRuntimeID,
	}
}

func TestEnsureEnvDispatchChannelSessionCreatesCanonicalSession(t *testing.T) {
	ctx, in := setupEnvDispatchChannelSessionFixture(t)

	sessionID, created, err := testHandler.ensureEnvDispatchChannelSession(ctx, in)
	require.NoError(t, err)
	require.True(t, created)
	require.NotEmpty(t, sessionID)

	var workspaceID, projectID, agentID, runtimeID string
	require.NoError(t, testPool.QueryRow(ctx, `
		SELECT session.workspace_id::text, session.project_id::text,
		       session.agent_id::text, session.runtime_id::text
		FROM channel_agent_session binding
		JOIN chat_session session ON session.id = binding.chat_session_id
		WHERE binding.channel_id = $1 AND binding.agent_id = $2`,
		in.ChannelID, in.AgentID).Scan(&workspaceID, &projectID, &agentID, &runtimeID))
	require.Equal(t, in.WorkspaceID, workspaceID)
	require.Equal(t, in.ProjectID, projectID)
	require.Equal(t, in.AgentID, agentID)
	require.Equal(t, in.RuntimeID, runtimeID)
}

func TestEnsureEnvDispatchChannelSessionReusesMatchingSession(t *testing.T) {
	ctx, in := setupEnvDispatchChannelSessionFixture(t)
	firstID, created, err := testHandler.ensureEnvDispatchChannelSession(ctx, in)
	require.NoError(t, err)
	require.True(t, created)

	secondID, created, err := testHandler.ensureEnvDispatchChannelSession(ctx, in)
	require.NoError(t, err)
	require.False(t, created)
	require.Equal(t, firstID, secondID)
}

func TestEnsureEnvDispatchChannelSessionRejectsMismatchedRuntime(t *testing.T) {
	ctx, in := setupEnvDispatchChannelSessionFixture(t)
	_, _, err := testHandler.ensureEnvDispatchChannelSession(ctx, in)
	require.NoError(t, err)

	in.RuntimeID = uuid.NewString()
	_, _, err = testHandler.ensureEnvDispatchChannelSession(ctx, in)
	require.ErrorContains(t, err, "env-dispatch channel session identity mismatch")
}

func TestEnsureEnvDispatchChannelSessionConcurrentWinnerLeavesNoOrphan(t *testing.T) {
	ctx, in := setupEnvDispatchChannelSessionFixture(t)
	const callers = 2
	ids := make([]string, callers)
	errs := make([]error, callers)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			<-start
			ids[index], _, errs[index] = testHandler.ensureEnvDispatchChannelSession(ctx, in)
		}(i)
	}
	close(start)
	wg.Wait()
	for _, err := range errs {
		require.NoError(t, err)
	}
	require.Equal(t, ids[0], ids[1])

	var mappings, sessions int
	require.NoError(t, testPool.QueryRow(ctx, `
		SELECT count(*) FROM channel_agent_session
		WHERE channel_id = $1 AND agent_id = $2`, in.ChannelID, in.AgentID).Scan(&mappings))
	require.NoError(t, testPool.QueryRow(ctx, `
		SELECT count(*) FROM chat_session
		WHERE project_id = $1 AND agent_id = $2 AND title = 'env-dispatch'`,
		in.ProjectID, in.AgentID).Scan(&sessions))
	require.Equal(t, 1, mappings)
	require.Equal(t, 1, sessions)
}

func TestEnvDispatchAdapterEnqueueChannelRunPersistsPromptWithTask(t *testing.T) {
	ctx, sessionIn := setupEnvDispatchChannelSessionFixture(t)
	sessionID, _, err := testHandler.ensureEnvDispatchChannelSession(ctx, sessionIn)
	require.NoError(t, err)

	adapter := &envDispatchDepsAdapter{h: testHandler}
	const prompt = "complete this task inside the sandbox"
	sandboxInstanceID := "sandbox-" + uuid.NewString()
	messageID, err := adapter.CreateChannelMessage(
		ctx, sessionIn.ChannelID, sessionIn.WorkspaceID, sessionIn.CreatorID, prompt,
	)
	require.NoError(t, err)

	taskID, err := adapter.EnqueueEnvDispatchChannelRun(ctx, sessionIn.WorkspaceID, sessionIn.CreatorID, service.ChannelRunInput{
		AgentID:           sessionIn.AgentID,
		ChannelID:         sessionIn.ChannelID,
		ProjectID:         sessionIn.ProjectID,
		EnvID:             uuid.NewString(),
		ChatSessionID:     sessionID,
		SandboxInstanceID: sandboxInstanceID,
		RuntimeID:         sessionIn.RuntimeID,
		SourceMessageID:   messageID,
	}, 0)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM agent_inbox_event WHERE id = $1`, taskID)
	})

	var gotPrompt, gotAgentID, gotRuntimeID, gotSessionID string
	var gotContext []byte
	require.NoError(t, testPool.QueryRow(ctx, `
		SELECT message.content, task.agent_id::text, task.runtime_id::text,
		       task.chat_session_id::text, task.context
		FROM chat_message message
		JOIN agent_inbox_event task ON task.id = message.task_id
		WHERE task.id = $1 AND message.role = 'user'`, taskID).Scan(
		&gotPrompt, &gotAgentID, &gotRuntimeID, &gotSessionID, &gotContext,
	))
	require.Contains(t, gotPrompt, prompt, "framed prompt must carry the raw channel message")
	require.Contains(t, gotPrompt, "Multica group chat", "prompt must frame the run as a channel message")
	require.Contains(t, gotPrompt, "final answer is delivered to this channel automatically", "prompt must steer the agent to the channel reply path")
	require.NotContains(t, gotPrompt, "multica message send --target dm:@", "prompt must not provide a proactive DM send command")
	require.Equal(t, sessionIn.AgentID, gotAgentID)
	require.Equal(t, sessionIn.RuntimeID, gotRuntimeID)
	require.Equal(t, sessionID, gotSessionID)
	marker, ok := service.ExtractEphemeralSandbox(gotContext)
	require.True(t, ok)
	require.Equal(t, sandboxInstanceID, marker.SandboxInstanceID)
	require.NotNil(t, marker.CleanupOnTerminal)
	require.False(t, *marker.CleanupOnTerminal, "env-dispatch channel owns sandbox cleanup")
}
