// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/multica-ai/multica/server/internal/memorygraph"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// --- Unit tests (no DB) ---

func TestChannelRunTrajectoryShape(t *testing.T) {
	traj := channelRunTrajectory("learned: codename is NIMBUS")
	var entries []localTrajectoryEntry
	require.NoError(t, json.Unmarshal(traj, &entries))
	require.Len(t, entries, 1)
	assert.Equal(t, int32(1), entries[0].Seq)
	assert.Equal(t, "user", entries[0].Type)
	assert.Contains(t, entries[0].Content, "NIMBUS")
}

func TestChannelSegmentScopeText(t *testing.T) {
	assert.Equal(t, "channel:abc", channelSegmentScopeText("abc"))
}

func TestClampInt32(t *testing.T) {
	assert.Equal(t, int32(0), clampInt32(-5))
	assert.Equal(t, int32(7), clampInt32(7))
	assert.Equal(t, int32(1<<31-1), clampInt32(1<<62))
}

// recordingFakeSink captures RecordSubmittedRun notifications.
type recordingFakeSink struct {
	mu    sync.Mutex
	calls []graphMemoryRunCall
}

type graphMemoryRunCall struct {
	runID, workspaceID, channelID string
	consumedSeq                   int64
}

func (f *recordingFakeSink) RecordSubmittedRun(_ context.Context, runID, workspaceID, channelID string, consumedSeq int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, graphMemoryRunCall{runID, workspaceID, channelID, consumedSeq})
}

func (f *recordingFakeSink) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *recordingFakeSink) snapshot() graphMemoryRunCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) == 0 {
		return graphMemoryRunCall{}
	}
	return f.calls[0]
}

// --- Integration tests (real Postgres at DATABASE_URL) ---

func graphMemoryRunSegmentTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("integration test requires Postgres at DATABASE_URL")
	}
	pool, err := pgxpool.New(context.Background(), dbURL)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	return pool
}

type graphMemoryRunFixture struct {
	pool      *pgxpool.Pool
	userID    string
	wsID      string
	channelID string
	runID     string
	run2ID    string
}

// newRunningRun inserts one running graph_memory_agent_run (+ trajectory) at
// target_seq 9. Only one running run may exist per channel
// (graph_memory_agent_run_one_active_idx), so call this sequentially.
func (f *graphMemoryRunFixture) newRunningRun(t *testing.T, suffix string) string {
	t.Helper()
	var runID string
	require.NoError(t, f.pool.QueryRow(t.Context(), `
		INSERT INTO graph_memory_agent_run
		  (workspace_id, channel_id, target_kind, target_id, status, initial_query, effective_objective, graph_version, fencing_token, target_seq)
		VALUES ($1::uuid, $2::uuid, 'channel', NULL, 'running', $3, $3, 1, 1, 9)
		RETURNING id::text`, f.wsID, f.channelID,
		"my project codename is NIMBUS-"+suffix).Scan(&runID))
	_, err := f.pool.Exec(t.Context(), `
		INSERT INTO graph_memory_agent_trajectory (run_id) VALUES ($1::uuid)`, runID)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = f.pool.Exec(context.Background(), `DELETE FROM graph_memory_agent_trajectory WHERE run_id=$1::uuid`, runID)
		_, _ = f.pool.Exec(context.Background(), `DELETE FROM interaction_dag_segment WHERE agent_run_id=$1::text`, runID)
		_, _ = f.pool.Exec(context.Background(), `DELETE FROM interaction_dag_session_run WHERE agent_run_id=$1::text`, runID)
		_, _ = f.pool.Exec(context.Background(), `DELETE FROM graph_memory_agent_run WHERE id=$1::uuid`, runID)
	})
	return runID
}

// graphMemoryRunFixtures commits user/workspace/channel/conversation, one
// channel_message at seq 9 (the learn text), and two running
// graph_memory_agent_runs (target_seq=9). Cleanup deletes everything.
func graphMemoryRunFixtures(t *testing.T, pool *pgxpool.Pool) *graphMemoryRunFixture {
	t.Helper()
	ctx := context.Background()
	f := &graphMemoryRunFixture{pool: pool}
	suffix := uuid.NewString()[:8]

	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO "user" (name, email) VALUES ($1, $2) RETURNING id::text`,
		"gm-run-seg test user "+suffix, "gm-run-seg-"+suffix+"@multica.test").Scan(&f.userID))
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM "user" WHERE id=$1::uuid`, f.userID) })

	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO workspace (name, slug) VALUES ($1, $2) RETURNING id::text`,
		"gm-run-seg ws "+suffix, "gm-run-seg-"+suffix).Scan(&f.wsID))
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM workspace WHERE id=$1::uuid`, f.wsID) })

	// The channel owner invariant (migration 237) auto-seeds a channel_member
	// owner from created_by only when the creator is a workspace member.
	_, err := pool.Exec(ctx, `INSERT INTO member (workspace_id, user_id, role) VALUES ($1::uuid, $2::uuid, 'owner')`, f.wsID, f.userID)
	require.NoError(t, err)

	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO channel (workspace_id, name, created_by, kind) VALUES ($1::uuid, $2, $3::uuid, 'group')
		RETURNING id::text`, f.wsID, "gm-run-seg-"+suffix, f.userID).Scan(&f.channelID))
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM channel WHERE id=$1::uuid`, f.channelID) })
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM member WHERE workspace_id=$1::uuid`, f.wsID) })

	// graph_memory_agent_run.channel_id references the channel's managed
	// Memory Agent row (ON DELETE CASCADE), so provision the agent chain.
	var runtimeID, managedAgentID string
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO agent_runtime (workspace_id, name, runtime_mode, provider, status, device_info, daemon_id, last_seen_at)
		VALUES ($1::uuid, $2, 'local', 'pi', 'online', '{}', $3, now()) RETURNING id::text`,
		f.wsID, "gm-run-seg-runtime-"+suffix, "gm-run-seg-daemon-"+suffix).Scan(&runtimeID))
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO agent (workspace_id, name, avatar_url, runtime_mode, runtime_id, managed_role)
		VALUES ($1::uuid, $2, '', 'local', $3::uuid, 'graph_memory_channel') RETURNING id::text`,
		f.wsID, "gm-run-seg-memory-"+suffix, runtimeID).Scan(&managedAgentID))
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM agent WHERE id=$1::uuid`, managedAgentID) })
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM agent_runtime WHERE id=$1::uuid`, runtimeID) })
	_, err = pool.Exec(ctx, `
		INSERT INTO graph_memory_channel_agent (workspace_id, channel_id, agent_id, handle, display_name, status)
		VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $4, 'active')`,
		f.wsID, f.channelID, managedAgentID, "memory-gm-run-seg-"+suffix)
	require.NoError(t, err)

	// conversation is auto-created for the channel by trg_channel_conversation.
	_, err = pool.Exec(ctx, `
		INSERT INTO channel_message (channel_id, workspace_id, author_type, author_id, content, conversation_id, seq, kind)
		VALUES ($1::uuid, $2::uuid, 'user', $3::uuid, $4, (SELECT id FROM conversation WHERE channel_id=$1::uuid LIMIT 1), 9, 'content')`,
		f.channelID, f.wsID, f.userID, "Please persist this synthetic test fact: my project codename is NIMBUS-"+suffix+".")
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM channel_message WHERE channel_id=$1::uuid`, f.channelID) })

	f.runID = f.newRunningRun(t, suffix)
	return f
}

// TestGraphMemoryRunSegment_SubmittedRunFeedsStaging covers the whole seam:
// finish(submitted) notifies the recorder, the recorder persists a
// channel-scoped interaction_dag_segment, and the ingest hook lands a staging
// summary in the channel graph under MULTICA_WORKSPACES_ROOT.
func TestGraphMemoryRunSegment_SubmittedRunFeedsStaging(t *testing.T) {
	t.Setenv("MULTICA_MEMORY_TYPE", "graph")
	root := t.TempDir()
	t.Setenv("MULTICA_WORKSPACES_ROOT", root)
	t.Setenv("MULTICA_PI_PATH", "/nonexistent/pi") // force the extractive summarizer

	pool := graphMemoryRunSegmentTestPool(t)
	f := graphMemoryRunFixtures(t, pool)
	ctx := context.Background()

	store := NewGraphMemoryAgentRunStore(pool)
	sink := &recordingFakeSink{}
	store.SetSubmittedRunSink(sink)
	require.NoError(t, store.Finish(ctx, f.runID, 1, "submitted", 9, []byte(`{"objective":""}`), nil))
	require.Eventually(t, func() bool { return sink.count() == 1 }, 5*time.Second, 20*time.Millisecond,
		"finish(submitted) notifies the sink")
	call := sink.snapshot()
	require.Equal(t, f.runID, call.runID)
	require.Equal(t, f.wsID, call.workspaceID)
	require.Equal(t, f.channelID, call.channelID)

	recorder := NewGraphMemoryRunSegmentRecorder(pool)
	recorder.RecordSubmittedRun(ctx, call.runID, f.wsID, call.channelID, call.consumedSeq)

	queries := db.New(pool)
	seg, err := queries.GetInteractionDAGSegmentByAgentRun(ctx, f.runID)
	require.NoError(t, err, "submitted run records an interaction_dag segment")
	assert.Equal(t, "channel:"+f.channelID, seg.ProjectID)
	assert.Equal(t, "memory_agent_run", seg.TrajectorySource)
	assert.False(t, seg.Trainable)
	assert.Contains(t, string(seg.Trajectory), "NIMBUS", "trajectory carries the user's learn message")

	// The ingest hook routed the segment into the channel graph staging.
	stagingDir := filepath.Join(root, f.wsID, "memory_graph", "channels", f.channelID, "staging", "segments")
	entries, readErr := os.ReadDir(stagingDir)
	require.NoError(t, readErr, "staging segments directory exists for the channel graph")
	var summary string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".md") && strings.Contains(e.Name(), seg.SegmentID) {
			data, _ := os.ReadFile(filepath.Join(stagingDir, e.Name()))
			summary = string(data)
		}
	}
	assert.NotEmpty(t, summary, "staging summary file written for the run segment")

	// Idempotent: a duplicate notification records nothing further.
	recorder.RecordSubmittedRun(ctx, f.runID, f.wsID, f.channelID, 9)
	var count int
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT count(*) FROM interaction_dag_segment WHERE agent_run_id=$1::text`, f.runID).Scan(&count))
	assert.Equal(t, 1, count, "duplicate notification stays one segment")

	// A checkpointed run never notifies and never records. run2 is created
	// after run1 finishes (one active running run per channel).
	f.run2ID = f.newRunningRun(t, "RUN2")
	require.NoError(t, store.Finish(ctx, f.run2ID, 1, "checkpointed", 9, []byte(`{}`), nil))
	time.Sleep(200 * time.Millisecond)
	assert.Equal(t, 1, sink.count(), "checkpointed run does not notify the sink")
	_, err = queries.GetInteractionDAGSegmentByAgentRun(ctx, f.run2ID)
	assert.Error(t, err, "checkpointed run records no segment")
}

// TestGraphMemoryRunSegment_LegacyWorkspaceNoop verifies the memory_type gate:
// a legacy workspace records nothing.
func TestGraphMemoryRunSegment_LegacyWorkspaceNoop(t *testing.T) {
	t.Setenv("MULTICA_MEMORY_TYPE", "legacy")
	pool := graphMemoryRunSegmentTestPool(t)
	f := graphMemoryRunFixtures(t, pool)
	ctx := context.Background()

	recorder := NewGraphMemoryRunSegmentRecorder(pool)
	recorder.RecordSubmittedRun(ctx, f.runID, f.wsID, f.channelID, 9)

	queries := db.New(pool)
	_, err := queries.GetInteractionDAGSegmentByAgentRun(ctx, f.runID)
	assert.Error(t, err, "legacy workspace records no segment")
}

// TestGraphMemoryRunSegment_IngestOverrideCapturesExport verifies the ingest
// contract (segment id, run id, trajectory) without touching the filesystem.
func TestGraphMemoryRunSegment_IngestOverrideCapturesExport(t *testing.T) {
	t.Setenv("MULTICA_MEMORY_TYPE", "graph")
	pool := graphMemoryRunSegmentTestPool(t)
	f := graphMemoryRunFixtures(t, pool)
	ctx := context.Background()

	var mu sync.Mutex
	var exports []memorygraph.SegmentExport
	recorder := NewGraphMemoryRunSegmentRecorder(pool)
	recorder.ingestOverride = func(_ context.Context, workspaceID, channelID, _, runID string, seg memorygraph.SegmentExport) error {
		mu.Lock()
		defer mu.Unlock()
		assert.Equal(t, f.wsID, workspaceID)
		assert.Equal(t, f.channelID, channelID)
		assert.Equal(t, f.runID, runID)
		exports = append(exports, seg)
		return nil
	}

	recorder.RecordSubmittedRun(ctx, f.runID, f.wsID, f.channelID, 9)
	require.Len(t, exports, 1)
	assert.Equal(t, "multica:"+f.runID, exports[0].SegmentID)
	assert.Equal(t, f.runID, exports[0].AgentRunID)
	assert.Contains(t, string(exports[0].Trajectory), "NIMBUS")
}
