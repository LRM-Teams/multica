// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// testUUID is defined in task_complete_race_test.go (same package); reused here.

// arealProxyContext builds a task context JSONB carrying an areal_proxy config
// (the shape extractArealProxyConfig parses) so a task looks like a trained run.
func arealProxyContext(sessionID, apiKey string) []byte {
	b, _ := json.Marshal(map[string]any{
		"areal_proxy": map[string]any{
			"provider":   "areal",
			"model":      "areal-default",
			"api_key":    apiKey,
			"base_url":   "http://proxy",
			"session_id": sessionID,
		},
	})
	return b
}

// newSeamTaskService builds a TaskService wired only with a fake-backed DAG -
// the seam helpers touch s.Training.DAG (and extractArealProxyConfig), nothing
// else, so no Queries/DB is needed for these unit tests.
func newSeamTaskService(store *fakeInteractionDAGStore, client *fakeArealSegmentClient) *TaskService {
	return &TaskService{
		Training: &TrainingSessionDeps{DAG: NewInteractionDAGService(store, client, true)},
	}
}

func leanSnap() map[string]any {
	return map[string]any{"sandbox_ids": []string{"sbx-1"}, "env_state": map[string]any{}}
}

const shardExport = `{"input_ids":{"shard_id":"s","node_addr":"n"}}`

// TestDelegation_ClosesParentSegment: a root parent delegates -> its segment
// closes with closing_event="delegation"; no edge (no grandparent). The
// parent->child edge is recorded later at the child's close (see completion test).
func TestDiscoverDelegationParent_ExcludesNewChildTask(t *testing.T) {
	pool := interactionDAGTestPool(t)
	defer pool.Close()

	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer tx.Rollback(ctx)
	q := db.New(tx)

	ws, err := q.CreateWorkspace(ctx, db.CreateWorkspaceParams{Name: "seam-ws", Slug: "seam-ws", IssuePrefix: "SM"})
	require.NoError(t, err)
	var rtID pgtype.UUID
	err = tx.QueryRow(ctx, `INSERT INTO agent_runtime (workspace_id, daemon_id, name, runtime_mode, provider, status, device_info, metadata, visibility) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING id`,
		ws.ID, "daemon-seam", "seam-runtime", "cloud", "daytona", "online", "", []byte("{}"), "private",
	).Scan(&rtID)
	require.NoError(t, err)
	agent, err := q.CreateAgent(ctx, db.CreateAgentParams{
		WorkspaceID: ws.ID, Name: "seam-agent", DisplayName: "Seam Agent", Description: "test",
		RuntimeMode: "cloud", RuntimeConfig: []byte("{}"), RuntimeID: rtID,
		Instructions: "", CustomEnv: []byte("{}"), CustomArgs: []byte("[]"),
		Model: pgtype.Text{String: "composer-1.5", Valid: true},
	})
	require.NoError(t, err)
	proj, err := q.CreateProject(ctx, db.CreateProjectParams{WorkspaceID: ws.ID, Title: "seam-proj", Status: "in_progress", Priority: "none"})
	require.NoError(t, err)
	issue, err := q.CreateIssue(ctx, db.CreateIssueParams{
		WorkspaceID: ws.ID, Title: "seam-issue", Status: "in_progress", Priority: "medium",
		CreatorType: "member", CreatorID: util.MustParseUUID("cccccccc-0000-0000-0000-000000000002"), Number: 1, ProjectID: proj.ID,
	})
	require.NoError(t, err)
	comment, err := q.CreateComment(ctx, db.CreateCommentParams{
		IssueID: issue.ID, WorkspaceID: ws.ID, AuthorType: "agent", AuthorID: agent.ID, Content: "@squad please handle", Type: "comment",
	})
	require.NoError(t, err)

	parent, err := q.CreateAgentTask(ctx, db.CreateAgentTaskParams{AgentID: agent.ID, RuntimeID: rtID, IssueID: issue.ID, Priority: 0})
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `UPDATE agent_inbox_event SET status='draining', context=$1 WHERE id=$2`, arealProxyContext("sess-parent", "key-parent"), parent.ID)
	require.NoError(t, err)
	parent, err = q.GetAgentTask(ctx, parent.ID)
	require.NoError(t, err)

	child, err := q.CreateAgentTask(ctx, db.CreateAgentTaskParams{AgentID: agent.ID, RuntimeID: rtID, IssueID: issue.ID, Priority: 0})
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `UPDATE agent_inbox_event SET context=$1 WHERE id=$2`, arealProxyContext("sess-child", "key-child"), child.ID)
	require.NoError(t, err)

	svc := &TaskService{Queries: q}
	got, ok := svc.discoverDelegationParent(ctx, issue.ID, comment.ID, child.ID)
	require.True(t, ok)
	assert.Equal(t, parent.ID, got.ID, "must not select the just-created receiver task as its own producer")
}

// TestCompletion_ClosesChildSegmentAndRecordsDelegationEdge: a child (with a
// parent) completes -> child segment closes ("completion") + parent->child
// delegation edge recorded at the child's close (where childSeg is finally known).

func TestTerminalSideEffectsWriteNoLegacySegments(t *testing.T) {
	env := setupRetryTestDB(t, "timeout")

	dagStore := env.dagStore
	// Make the retained bridge fully operable so its absence after the
	// cleanup is the only reason no legacy row appears: seed the session
	// mapping and a valid trajectory export the AReaL path needs.
	dagStore.mu.Lock()
	dagStore.sessionRuns["sess-parent"] = db.InteractionDagSessionRun{
		SessionID: "sess-parent", ProjectID: env.project.ID.String(),
		AgentRunID: env.parent.ID.String(),
	}
	dagStore.mu.Unlock()
	env.svc.Training.DAG.client = &fakeArealSegmentClient{
		closeSegmentID: 7,
		exportPayload:  json.RawMessage(`{"input_ids":{"shard_id":"shard-1","node_addr":"10.0.0.1:8000"}}`),
	}

	dagStore.mu.Lock()
	before := len(dagStore.segmentSnapshots)
	dagStore.mu.Unlock()

	env.svc.FinalizeTerminalTaskPostCommitSideEffects(context.Background(), env.parent)

	dagStore.mu.Lock()
	after := len(dagStore.segmentSnapshots)
	dagStore.mu.Unlock()
	if after != before {
		t.Fatalf("terminal side effects wrote %d legacy segment(s); the canonical writer owns the lifecycle", after-before)
	}
}
