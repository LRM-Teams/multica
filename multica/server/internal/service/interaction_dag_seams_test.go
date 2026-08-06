// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
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
func TestDelegation_ClosesParentSegment(t *testing.T) {
	store := newFakeInteractionDAGStore()
	client := &fakeArealSegmentClient{closeSegmentID: 7, exportPayload: json.RawMessage(shardExport)}
	svc := newSeamTaskService(store, client)

	parentID := testUUID(1)
	parent := db.AgentInboxEvent{ID: parentID, Context: arealProxyContext("sess-1", "key-1")}
	require.NoError(t, svc.Training.DAG.RecordSessionAgentRun(context.Background(), "proj-1", "sess-1", util.UUIDToString(parentID), "issue-1"))

	svc.closeSegmentForDelegation(context.Background(), parent, "proj-1", leanSnap())

	require.Len(t, store.segmentSnapshots, 1)
	seg := store.segmentSnapshots[0]
	assert.Equal(t, "sess-1-7", seg.SegmentID)
	assert.Equal(t, util.UUIDToString(parentID), seg.AgentRunID)
	assert.True(t, seg.ClosingEvent.Valid)
	assert.Equal(t, "delegation", seg.ClosingEvent.String)
	assert.Empty(t, store.edges, "root parent has no grandparent -> no edge at delegation")
}

// TestDelegation_LinksGrandparentEdge: a parent that was itself delegated to
// (has a parent) delegates -> grandparent->parent delegation edge recorded.
func TestDelegation_LinksGrandparentEdge(t *testing.T) {
	store := newFakeInteractionDAGStore()
	client := &fakeArealSegmentClient{exportPayload: json.RawMessage(shardExport)}
	svc := newSeamTaskService(store, client)

	gpID, parentID := testUUID(1), testUUID(2)
	// Grandparent already closed its segment (sess-gp -> "sess-gp-3").
	require.NoError(t, svc.Training.DAG.RecordSessionAgentRun(context.Background(), "proj-1", "sess-gp", util.UUIDToString(gpID), "issue-1"))
	client.closeSegmentID = 3
	_, err := svc.Training.DAG.CloseSegmentForEvent(context.Background(), "proj-1", "sess-gp", "key-gp", "completion", leanSnap())
	require.NoError(t, err)

	// Parent (with grandparent) now delegates.
	client.closeSegmentID = 5
	parent := db.AgentInboxEvent{ID: parentID, ParentTaskID: gpID, Context: arealProxyContext("sess-p", "key-p")}
	require.NoError(t, svc.Training.DAG.RecordSessionAgentRun(context.Background(), "proj-1", "sess-p", util.UUIDToString(parentID), "issue-1"))
	svc.closeSegmentForDelegation(context.Background(), parent, "proj-1", leanSnap())

	require.Len(t, store.segmentSnapshots, 2)
	assert.Equal(t, "sess-p-5", store.segmentSnapshots[1].SegmentID)
	require.Len(t, store.edges, 1)
	assert.Equal(t, "sess-gp-3", store.edges[0].SrcSegmentID)
	assert.Equal(t, "sess-p-5", store.edges[0].DstSegmentID)
	assert.Equal(t, "delegation", store.edges[0].Type)
}

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
		MaxConcurrentTasks: 1, Instructions: "", CustomEnv: []byte("{}"), CustomArgs: []byte("[]"),
			Model:              pgtype.Text{String: "composer-1.5", Valid: true},
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
func TestCompletion_ClosesChildSegmentAndRecordsDelegationEdge(t *testing.T) {
	store := newFakeInteractionDAGStore()
	client := &fakeArealSegmentClient{exportPayload: json.RawMessage(shardExport)}
	svc := newSeamTaskService(store, client)

	parentID, childID := testUUID(1), testUUID(2)
	// Parent already closed its segment at delegation (sess-p -> "sess-p-1").
	require.NoError(t, svc.Training.DAG.RecordSessionAgentRun(context.Background(), "proj-1", "sess-p", util.UUIDToString(parentID), "issue-1"))
	client.closeSegmentID = 1
	_, err := svc.Training.DAG.CloseSegmentForEvent(context.Background(), "proj-1", "sess-p", "key-p", "delegation", leanSnap())
	require.NoError(t, err)

	// Child completes.
	client.closeSegmentID = 9
	child := db.AgentInboxEvent{ID: childID, ParentTaskID: parentID, Context: arealProxyContext("sess-c", "key-c")}
	require.NoError(t, svc.Training.DAG.RecordSessionAgentRun(context.Background(), "proj-1", "sess-c", util.UUIDToString(childID), "issue-1"))
	svc.closeSegmentForTerminal(context.Background(), child, "proj-1", leanSnap())

	require.Len(t, store.segmentSnapshots, 2)
	childSeg := store.segmentSnapshots[1]
	assert.Equal(t, "sess-c-9", childSeg.SegmentID)
	assert.True(t, childSeg.ClosingEvent.Valid)
	assert.Equal(t, "completion", childSeg.ClosingEvent.String)
	require.Len(t, store.edges, 1)
	assert.Equal(t, "sess-p-1", store.edges[0].SrcSegmentID)
	assert.Equal(t, "sess-c-9", store.edges[0].DstSegmentID)
	assert.Equal(t, "delegation", store.edges[0].Type)
}

// TestLeaf_ClosesSegmentWithEmptyClosingEvent: a root task (no parent) completes
// -> one segment with closing_event=NULL (leaf). No edge (no parent).
func TestLeaf_ClosesSegmentWithEmptyClosingEvent(t *testing.T) {
	store := newFakeInteractionDAGStore()
	client := &fakeArealSegmentClient{closeSegmentID: 4, exportPayload: json.RawMessage(shardExport)}
	svc := newSeamTaskService(store, client)

	taskID := testUUID(1)
	task := db.AgentInboxEvent{ID: taskID, Context: arealProxyContext("sess-1", "key-1")} // no ParentTaskID
	require.NoError(t, svc.Training.DAG.RecordSessionAgentRun(context.Background(), "proj-1", "sess-1", util.UUIDToString(taskID), "issue-1"))

	svc.closeSegmentForTerminal(context.Background(), task, "proj-1", leanSnap())

	require.Len(t, store.segmentSnapshots, 1)
	assert.False(t, store.segmentSnapshots[0].ClosingEvent.Valid, "leaf closing_event must be NULL")
	assert.Empty(t, store.edges, "leaf has no parent -> no edge")
}

// TestOneSegmentPerTask_SkipsSecondClose: a task that already recorded a segment
// (it delegated earlier) does not record a second on completion.
func TestOneSegmentPerTask_SkipsSecondClose(t *testing.T) {
	store := newFakeInteractionDAGStore()
	client := &fakeArealSegmentClient{closeSegmentID: 2, exportPayload: json.RawMessage(shardExport)}
	svc := newSeamTaskService(store, client)

	taskID := testUUID(1)
	task := db.AgentInboxEvent{ID: taskID, Context: arealProxyContext("sess-1", "key-1")}
	require.NoError(t, svc.Training.DAG.RecordSessionAgentRun(context.Background(), "proj-1", "sess-1", util.UUIDToString(taskID), "issue-1"))

	svc.closeSegmentForTerminal(context.Background(), task, "proj-1", leanSnap())
	require.Len(t, store.segmentSnapshots, 1)
	// Second close: already has a segment -> skip (one-segment-per-task).
	svc.closeSegmentForTerminal(context.Background(), task, "proj-1", leanSnap())
	require.Len(t, store.segmentSnapshots, 1, "second close must be skipped (one-segment-per-task)")
}

// TestConcurrentFanOut_RecordsDelegationEdges: a parent delegates to N children
// (parent segment closes once; subsequent delegations skip) and each child
// completes -> N parent->child delegation edges, in deterministic insert order.
func TestConcurrentFanOut_RecordsDelegationEdges(t *testing.T) {
	store := newFakeInteractionDAGStore()
	client := &fakeArealSegmentClient{exportPayload: json.RawMessage(shardExport)}
	svc := newSeamTaskService(store, client)

	parentID := testUUID(1)
	require.NoError(t, svc.Training.DAG.RecordSessionAgentRun(context.Background(), "proj-1", "sess-p", util.UUIDToString(parentID), "issue-1"))
	client.closeSegmentID = 1
	parent := db.AgentInboxEvent{ID: parentID, Context: arealProxyContext("sess-p", "key-p")}
	svc.closeSegmentForDelegation(context.Background(), parent, "proj-1", leanSnap())
	// A second delegation by the same parent is a no-op (one segment per task).
	svc.closeSegmentForDelegation(context.Background(), parent, "proj-1", leanSnap())
	require.Len(t, store.segmentSnapshots, 1, "parent records one segment")

	const n = 3
	for i := 1; i <= n; i++ {
		childID := testUUID(byte(i + 1))
		sess := "sess-c" + strconv.Itoa(i)
		child := db.AgentInboxEvent{ID: childID, ParentTaskID: parentID, Context: arealProxyContext(sess, "key-c")}
		require.NoError(t, svc.Training.DAG.RecordSessionAgentRun(context.Background(), "proj-1", sess, util.UUIDToString(childID), "issue-1"))
		client.closeSegmentID = i
		svc.closeSegmentForTerminal(context.Background(), child, "proj-1", leanSnap())
	}

	require.Len(t, store.segmentSnapshots, 1+n)
	require.Len(t, store.edges, n)
	for i, e := range store.edges {
		assert.Equal(t, "sess-p-1", e.SrcSegmentID, "edge %d src", i)
		assert.Equal(t, "delegation", e.Type, "edge %d type", i)
		assert.Equal(t, fmt.Sprintf("sess-c%d-%d", i+1, i+1), e.DstSegmentID, "edge %d dst (deterministic order)", i)
	}
}

// TestSeams_NoopWhenDisabled: with the DAG disabled, the seams record nothing.
func TestSeams_NoopWhenDisabled(t *testing.T) {
	store := newFakeInteractionDAGStore()
	svc := &TaskService{
		Training: &TrainingSessionDeps{DAG: NewInteractionDAGService(store, &fakeArealSegmentClient{}, false)},
	}
	task := db.AgentInboxEvent{ID: testUUID(1), Context: arealProxyContext("sess-1", "key-1")}
	svc.closeSegmentForDelegation(context.Background(), task, "proj-1", leanSnap())
	svc.closeSegmentForTerminal(context.Background(), task, "proj-1", leanSnap())
	assert.Empty(t, store.segmentSnapshots)
	assert.Empty(t, store.edges)
}

// TestSeams_NoopWithoutArealProxy: a task with no areal_proxy context is not a
// trained run -> the seam skips it (gating to trained rollouts only).
func TestSeams_NoopWithoutArealProxy(t *testing.T) {
	store := newFakeInteractionDAGStore()
	client := &fakeArealSegmentClient{closeSegmentID: 1, exportPayload: json.RawMessage(shardExport)}
	svc := newSeamTaskService(store, client)
	task := db.AgentInboxEvent{ID: testUUID(1), Context: nil}
	svc.closeSegmentForTerminal(context.Background(), task, "proj-1", leanSnap())
	assert.Empty(t, store.segmentSnapshots, "non-trained task records nothing")
}

// TestSeams_BestEffortOnCloseError: a bridge close failure is logged and
// swallowed (no panic, no partial segment/edge) - recording is best-effort.
func TestSeams_BestEffortOnCloseError(t *testing.T) {
	store := newFakeInteractionDAGStore()
	client := &fakeArealSegmentClient{closeSegmentErr: errors.New("bridge down"), exportPayload: json.RawMessage(shardExport)}
	svc := newSeamTaskService(store, client)

	parentID := testUUID(1)
	parent := db.AgentInboxEvent{ID: parentID, ParentTaskID: testUUID(9), Context: arealProxyContext("sess-1", "key-1")}
	require.NoError(t, svc.Training.DAG.RecordSessionAgentRun(context.Background(), "proj-1", "sess-1", util.UUIDToString(parentID), "issue-1"))

	svc.closeSegmentForDelegation(context.Background(), parent, "proj-1", leanSnap())
	assert.Empty(t, store.segmentSnapshots, "close failure must not record a segment")
	assert.Empty(t, store.edges, "close failure must not record an edge")
}
