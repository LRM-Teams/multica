// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/multica-ai/multica/server/internal/memorygraph"
	"github.com/multica-ai/multica/server/internal/util"
)

// Dual-shape recall task identity (recovery spec A1): a channel-message-shaped
// TaskID resolves through message → channel → workspace, the ledger records the
// resolved shape, and the mode gates run after shape resolution so an
// agent-mode workspace answers disabled (200) instead of identity (404).

func dualShapeTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	// Recall Begin resolves the live-guard publication index (universal DAG
	// live version), so the fixture needs the fully migrated faithful schema.
	return bootstrapUniversalDAGProjectionSchema(t)
}

type dualShapeFixture struct {
	pool       *pgxpool.Pool
	root       string
	wsID       string
	channelID  string
	runtimeID  string
	daemonID   string
	userMsgID  string
	agentMsgID string
	inboxEvID  string
	inboxAgent string
}

// newDualShapeFixture provisions one workspace with a channel, an
// agent_runtime under a daemon, a user-authored and an agent-authored channel
// message (the resident-path TaskID shapes), and one agent_inbox_event bound
// to the channel (the task shape). Cleanup removes everything.
func newDualShapeFixture(t *testing.T, pool *pgxpool.Pool) *dualShapeFixture {
	t.Helper()
	ctx := context.Background()
	f := &dualShapeFixture{pool: pool, root: t.TempDir()}
	suffix := uuid.NewString()[:8]
	f.daemonID = "dual-shape-daemon-" + suffix

	var userID string
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO "user" (name, email) VALUES ($1, $2) RETURNING id::text`,
		"dual-shape user "+suffix, "dual-shape-"+suffix+"@multica.test").Scan(&userID))
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM "user" WHERE id=$1::uuid`, userID) })

	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO workspace (name, slug, settings) VALUES ($1, $2, $3) RETURNING id::text`,
		"dual-shape ws "+suffix, "dual-shape-"+suffix,
		`{"memory_provider_policy":{"version":"test","purposes":{"embed":{"enabled":false}}}}`).Scan(&f.wsID))
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM workspace WHERE id=$1::uuid`, f.wsID) })

	_, err := pool.Exec(ctx, `INSERT INTO member (workspace_id, user_id, role) VALUES ($1::uuid, $2::uuid, 'owner')`, f.wsID, userID)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM member WHERE workspace_id=$1::uuid`, f.wsID) })

	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO channel (workspace_id, name, created_by, kind) VALUES ($1::uuid, $2, $3::uuid, 'group')
		RETURNING id::text`, f.wsID, "dual-shape-"+suffix, userID).Scan(&f.channelID))
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM channel WHERE id=$1::uuid`, f.channelID) })

	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO agent_runtime (workspace_id, name, runtime_mode, provider, status, device_info, daemon_id, last_seen_at)
		VALUES ($1::uuid, $2, 'local', 'pi', 'online', '{}', $3, now()) RETURNING id::text`,
		f.wsID, "dual-shape-runtime-"+suffix, f.daemonID).Scan(&f.runtimeID))
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM agent_runtime WHERE id=$1::uuid`, f.runtimeID) })

	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO agent (workspace_id, name, avatar_url, runtime_mode, runtime_id)
		VALUES ($1::uuid, $2, '', 'local', $3::uuid) RETURNING id::text`,
		f.wsID, "dual-shape-agent-"+suffix, f.runtimeID).Scan(&f.inboxAgent))
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM agent WHERE id=$1::uuid`, f.inboxAgent) })

	conv := `(SELECT id FROM conversation WHERE channel_id=$1::uuid LIMIT 1)`
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO channel_message (channel_id, workspace_id, author_type, author_id, content, conversation_id, seq, kind)
		VALUES ($1::uuid, $2::uuid, 'user', $3::uuid, $4, `+conv+`, 11, 'content') RETURNING id::text`,
		f.channelID, f.wsID, userID, "what deploy default should I use?").Scan(&f.userMsgID))
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO channel_message (channel_id, workspace_id, author_type, author_id, content, conversation_id, seq, kind)
		VALUES ($1::uuid, $2::uuid, 'agent', $3::uuid, $4, `+conv+`, 12, 'content') RETURNING id::text`,
		f.channelID, f.wsID, f.inboxAgent, "agent-authored ping").Scan(&f.agentMsgID))
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM channel_message WHERE channel_id=$1::uuid`, f.channelID) })

	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO agent_inbox_event (workspace_id, channel_id, agent_id, reason)
		VALUES ($1::uuid, $2::uuid, $3::uuid, 'mention') RETURNING id::text`,
		f.wsID, f.channelID, f.inboxAgent).Scan(&f.inboxEvID))
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM agent_inbox_event WHERE id=$1::uuid`, f.inboxEvID) })

	// Channel graph on disk so scope resolution, identity verification, and
	// the version pin all succeed for both shapes.
	dir, err := memorygraph.EnsureScopedDir(f.root, f.wsID, memorygraph.GraphDirKindChannel, f.channelID)
	require.NoError(t, err)
	require.NoError(t, memorygraph.NewStore(dir).Init())
	return f
}

func (f *dualShapeFixture) service() *GraphMemoryRecallService {
	return NewGraphMemoryRecallService(f.pool, GraphMemoryLimits{}, f.root, "graph", nil)
}

func parseUUID(t *testing.T, id string) pgtype.UUID {
	t.Helper()
	parsed, err := util.ParseUUID(id)
	require.NoError(t, err)
	return parsed
}

func (f *dualShapeFixture) request(taskID string) GraphMemoryRecallRequest {
	return GraphMemoryRecallRequest{
		WorkspaceID: f.wsID, TaskID: taskID, DaemonID: f.daemonID,
		RuntimeID: f.runtimeID, Query: "deploy default", TraceID: uuid.NewString(),
	}
}

// resolveRecallTask attributes the shape and its identity fields for both
// canonical shapes, and only the invoking agent's authorship yields an agent
// id on the channel-message shape.
func TestGraphMemoryRecallResolveTaskDualShape(t *testing.T) {
	pool := dualShapeTestPool(t)
	f := newDualShapeFixture(t, pool)
	svc := f.service()
	ctx := context.Background()
	wsUUID := parseUUID(t, f.wsID)

	inbox, err := svc.resolveRecallTask(ctx, wsUUID, parseUUID(t, f.inboxEvID), f.inboxEvID)
	require.NoError(t, err)
	assert.Equal(t, graphMemoryTaskShapeInboxEvent, inbox.Shape)
	assert.Equal(t, f.channelID, util.UUIDToString(inbox.ChannelID))

	userMsg, err := svc.resolveRecallTask(ctx, wsUUID, parseUUID(t, f.userMsgID), f.userMsgID)
	require.NoError(t, err)
	assert.Equal(t, graphMemoryTaskShapeChannelMessage, userMsg.Shape)
	assert.Equal(t, f.channelID, util.UUIDToString(userMsg.ChannelID))
	assert.False(t, userMsg.AgentID.Valid, "user-authored message must not carry an invoking agent")

	agentMsg, err := svc.resolveRecallTask(ctx, wsUUID, parseUUID(t, f.agentMsgID), f.agentMsgID)
	require.NoError(t, err)
	assert.Equal(t, graphMemoryTaskShapeChannelMessage, agentMsg.Shape)
	assert.Equal(t, f.inboxAgent, util.UUIDToString(agentMsg.AgentID))

	// A miss on both shapes is an identity denial; so is a task owned by
	// another workspace (cross-tenant probe).
	_, err = svc.resolveRecallTask(ctx, wsUUID, parseUUID(t, uuid.NewString()), "unknown")
	assert.ErrorIs(t, err, ErrGraphMemoryRecallIdentity)
	foreignWS := parseUUID(t, uuid.NewString())
	_, err = svc.resolveRecallTask(ctx, foreignWS, parseUUID(t, f.userMsgID), f.userMsgID)
	assert.ErrorIs(t, err, ErrGraphMemoryRecallIdentity)
}

// Begin accepts a channel-message-shaped TaskID end to end and the ledger
// records the resolved shape (A1: graph+inject channel recall actually lands).
func TestGraphMemoryRecallBeginChannelMessageShapePersists(t *testing.T) {
	pool := dualShapeTestPool(t)
	f := newDualShapeFixture(t, pool)
	svc := f.service()

	req := f.request(f.userMsgID)
	plan, err := svc.Begin(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, graphMemoryTaskShapeChannelMessage, plan.TaskShape)
	assert.Equal(t, "channel", plan.GraphKind)
	assert.Equal(t, f.channelID, plan.GraphOwnerID)
	assert.Equal(t, "offline_capture", plan.TrainingMode)

	var shape string
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT task_shape FROM graph_memory_recall WHERE id=$1::uuid`, plan.RecallID).Scan(&shape))
	assert.Equal(t, graphMemoryTaskShapeChannelMessage, shape)

	// Idempotent replay of the same trace returns the persisted shape.
	replayed, err := svc.Begin(context.Background(), req)
	require.NoError(t, err)
	assert.True(t, replayed.Replayed)
	assert.Equal(t, graphMemoryTaskShapeChannelMessage, replayed.TaskShape)
}

// The task-shaped inbox-event path keeps its shape in the ledger.
func TestGraphMemoryRecallBeginInboxEventShapePersists(t *testing.T) {
	pool := dualShapeTestPool(t)
	f := newDualShapeFixture(t, pool)
	svc := f.service()

	plan, err := svc.Begin(context.Background(), f.request(f.inboxEvID))
	require.NoError(t, err)
	assert.Equal(t, graphMemoryTaskShapeInboxEvent, plan.TaskShape)

	var shape string
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT task_shape FROM graph_memory_recall WHERE id=$1::uuid`, plan.RecallID).Scan(&shape))
	assert.Equal(t, graphMemoryTaskShapeInboxEvent, shape)
}

// The mode gates run after shape resolution: a channel-message-shaped request
// on an agent-mode or legacy workspace answers disabled, never identity —
// the resident path stops producing 404 noise (A1-2).
func TestGraphMemoryRecallGateOrderAfterShapeResolution(t *testing.T) {
	pool := dualShapeTestPool(t)
	f := newDualShapeFixture(t, pool)
	ctx := context.Background()

	setProfile := func(memoryType, mode string) {
		_, err := pool.Exec(ctx, `
			INSERT INTO graph_memory_profile (workspace_id, memory_type, graph_memory_mode)
			VALUES ($1::uuid, $2, $3)
			ON CONFLICT (workspace_id) DO UPDATE SET memory_type=$2, graph_memory_mode=$3`,
			f.wsID, memoryType, mode)
		require.NoError(t, err)
	}

	setProfile("graph", "agent")
	_, err := f.service().Begin(ctx, f.request(f.userMsgID))
	assert.ErrorIs(t, err, ErrGraphMemoryRecallDisabled, "agent-mode resident request must be disabled, not 404")

	setProfile("legacy", "inject")
	_, err = f.service().Begin(ctx, f.request(f.userMsgID))
	assert.ErrorIs(t, err, ErrGraphMemoryRecallDisabled, "legacy workspace must stay disabled (strict separation)")

	// A genuinely unknown task id stays an identity denial even on a gated
	// workspace: the gate reorder never masks cross-tenant probing.
	setProfile("graph", "agent")
	_, err = f.service().Begin(ctx, f.request(uuid.NewString()))
	assert.ErrorIs(t, err, ErrGraphMemoryRecallIdentity)
}

// The storage-layer identity trigger accepts both shapes and still rejects a
// task id that resolves in neither table (spec §16 tenant consistency).
func TestGraphMemoryRecallLedgerRejectsForeignShape(t *testing.T) {
	pool := dualShapeTestPool(t)
	f := newDualShapeFixture(t, pool)
	ctx := context.Background()

	_, err := pool.Exec(ctx, `
		INSERT INTO graph_memory_recall
		  (workspace_id, task_id, daemon_id, runtime_id, graph_kind, graph_owner_id, graph_version, query, trace_id, task_shape)
		VALUES ($1::uuid, $2::uuid, $3, $4::uuid, 'channel', $5::uuid, 1, 'q', $6, 'channel_message')`,
		f.wsID, uuid.NewString(), f.daemonID, f.runtimeID, f.channelID, "trace-"+uuid.NewString())
	require.Error(t, err, "trigger must reject a task id that is no channel_message")
	assert.False(t, errors.Is(err, ErrGraphMemoryRecallIdentity)) // storage denial, not the service sentinel
}
