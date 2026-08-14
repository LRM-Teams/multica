package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// channelAgentRuntimeSpec describes one agent+runtime combination created by
// newChannelAgentRuntimeFixture. Shared by collaboration tests that need a
// group channel with several channel-member agents online on distinct
// runtimes.
type channelAgentRuntimeSpec struct {
	status             string
	provider           string
	omitCapability     bool
	localReminderInbox bool
}

type channelAgentRuntimeFixture struct {
	handler    *Handler
	channel    ChannelResponse
	agentIDs   []string
	agentNames []string
	runtimeIDs []string
}

func newChannelAgentRuntimeFixture(t *testing.T, specs []channelAgentRuntimeSpec) channelAgentRuntimeFixture {
	t.Helper()
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	local := *testHandler

	suffix := uuid.NewString()
	var channelID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO channel (workspace_id, name, created_by, kind)
		VALUES ($1, $2, $3, 'group')
		RETURNING id`, testWorkspaceID, "collab-"+suffix, testUserID).Scan(&channelID); err != nil {
		t.Fatalf("create collaboration channel: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'user', $3)
ON CONFLICT DO NOTHING`, channelID, testWorkspaceID, testUserID); err != nil {
		t.Fatalf("add collaboration channel user: %v", err)
	}

	fixture := channelAgentRuntimeFixture{handler: &local}
	for i, spec := range specs {
		status := spec.status
		if status == "" {
			status = "online"
		}
		provider := spec.provider
		if provider == "" {
			provider = "pi"
		}
		capabilities := []string{
			protocol.DaemonCapabilityRestrictedExecution,
			protocol.DaemonCapabilityReminderVersionedCache,
			protocol.DaemonCapabilityReminderTransientInput,
		}
		if spec.localReminderInbox {
			capabilities = append(capabilities, protocol.DaemonCapabilityReminderLocalInbox)
		}
		if spec.omitCapability {
			capabilities = []string{}
		}
		metadata, err := json.Marshal(map[string]any{"capabilities": capabilities})
		if err != nil {
			t.Fatalf("marshal collaboration runtime metadata: %v", err)
		}
		var runtimeID string
		if err := testPool.QueryRow(ctx, `
			INSERT INTO agent_runtime (
			  workspace_id, name, runtime_mode, provider, status,
			  device_info, metadata, last_seen_at
			)
			VALUES ($1, $2, 'cloud', $3, $4, 'collaboration test runtime', $5, now())
			RETURNING id`, testWorkspaceID, fmt.Sprintf("collab-runtime-%s-%d", suffix, i), provider, status, metadata).Scan(&runtimeID); err != nil {
			t.Fatalf("create collaboration runtime: %v", err)
		}
		name := fmt.Sprintf("collab_agent_%s_%d", suffix[:8], i)
		var agentID string
		if err := testPool.QueryRow(ctx, `
			INSERT INTO agent (
			workspace_id, name, display_name, description, runtime_mode, runtime_config, runtime_id, max_concurrent_tasks, owner_id, instructions, custom_env, custom_args, mcp_config
		, model) VALUES ($1, $2, $2, '', 'cloud', '{}'::jsonb, $3, 1, $4, '', '{}'::jsonb, '[]'::jsonb, '[]'::jsonb, 'composer-1.5')
			RETURNING id`, testWorkspaceID, name, runtimeID, testUserID).Scan(&agentID); err != nil {
			t.Fatalf("create collaboration agent: %v", err)
		}
		if _, err := testPool.Exec(ctx, `
			INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
			VALUES ($1, $2, 'agent', $3)
ON CONFLICT DO NOTHING`, channelID, testWorkspaceID, agentID); err != nil {
			t.Fatalf("add collaboration agent to channel: %v", err)
		}
		fixture.runtimeIDs = append(fixture.runtimeIDs, runtimeID)
		fixture.agentIDs = append(fixture.agentIDs, agentID)
		fixture.agentNames = append(fixture.agentNames, name)
	}
	channel, ok := local.getChannel(ctx, testWorkspaceID, parseUUID(channelID))
	if !ok {
		t.Fatal("load collaboration channel")
	}
	fixture.channel = channel
	t.Cleanup(func() {
		cleanupCtx := context.Background()
		_, _ = testPool.Exec(cleanupCtx, `DELETE FROM channel WHERE id = $1`, channelID)
		for _, agentID := range fixture.agentIDs {
			_, _ = testPool.Exec(cleanupCtx, `DELETE FROM agent WHERE id = $1`, agentID)
		}
		for _, runtimeID := range fixture.runtimeIDs {
			_, _ = testPool.Exec(cleanupCtx, `DELETE FROM agent_runtime WHERE id = $1`, runtimeID)
		}
	})
	return fixture
}

func (f channelAgentRuntimeFixture) insertMessage(t *testing.T, authorType, authorID, content string, parts []protocol.MessagePart) ChannelMessageResponse {
	t.Helper()
	id := pgtype.UUID{}
	if authorID != "" {
		id = parseUUID(authorID)
	}
	message, err := f.handler.insertChannelMessageWithParts(
		context.Background(), parseUUID(f.channel.ID), parseUUID(testWorkspaceID),
		authorType, id, authorType+" test", content, parts, "multica", nil,
		pgtype.UUID{}, pgtype.UUID{}, nil, 0,
	)
	if err != nil {
		t.Fatalf("insert collaboration trigger: %v", err)
	}
	return message
}
