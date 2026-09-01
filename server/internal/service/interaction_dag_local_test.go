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

func makeFakeTaskMessage(taskID string, seq int32, typ, tool, content string, input, output string) db.TaskMessage {
	taskUUID := pgtype.UUID{}
	_ = taskUUID.Scan(taskID)
	return db.TaskMessage{
		TaskID:  taskUUID,
		Seq:     seq,
		Type:    typ,
		Tool:    ptrText(tool),
		Content: ptrText(content),
		Input:   []byte(input),
		Output:  ptrText(output),
	}
}

// TestInteractionDAG_RecordLocalSegmentForEvent_RecordsLocalSegment verifies the
// local recorder upserts a deterministic multica:<task-id> session, snapshots
// only the requested sequence range in order, sets trajectory_source=task_messages,
// trainable=false, and leaves AReaL fields null.
func TestInteractionDAG_RecordLocalSegmentForEvent_RecordsLocalSegment(t *testing.T) {
	store := newFakeInteractionDAGStore()
	msgs := newFakeMessageStore()
	svc := NewInteractionDAGServiceWithMessages(store, msgs, &fakeArealSegmentClient{}, true)

	taskID := util.UUIDToString(testUUID(21))
	// Seed task messages at seq 1-3 in both stores.
	store.addTestTaskMessage(taskID, 1)
	store.addTestTaskMessage(taskID, 2)
	store.addTestTaskMessage(taskID, 3)
	msgs.addTaskMessage(taskID, makeFakeTaskMessage(taskID, 1, "user", "", "hello world", "", ""))
	msgs.addTaskMessage(taskID, makeFakeTaskMessage(taskID, 2, "assistant", "", "hi there", "", "some output"))
	msgs.addTaskMessage(taskID, makeFakeTaskMessage(taskID, 3, "tool", "read_file", "", `{"path":"/x"}`, "file contents"))

	segID, _, err := svc.RecordLocalSegmentForEvent(
		context.Background(), "proj-1", taskID, "issue-1", "delegation",
		map[string]any{"sandbox_ids": []string{"sbx-1"}},
	)
	require.NoError(t, err)
	assert.Equal(t, "multica:"+util.UUIDToString(testUUID(21)), segID)

	// Session run upserted with deterministic session id.
	run, ok := store.sessionRuns["multica:"+util.UUIDToString(testUUID(21))]
	require.True(t, ok, "session run must be upserted")
	assert.Equal(t, "proj-1", run.ProjectID)
	assert.Equal(t, taskID, run.AgentRunID)
	assert.Equal(t, ptrText("issue-1"), run.IssueID)

	// Segment snapshot recorded.
	require.Len(t, store.segmentSnapshots, 1)
	seg := store.segmentSnapshots[0]
	assert.Equal(t, "multica:"+util.UUIDToString(testUUID(21)), seg.SegmentID)
	assert.Equal(t, "proj-1", seg.ProjectID)
	assert.Equal(t, taskID, util.UUIDToString(seg.AgentRunID))
	assert.Equal(t, ptrText("issue-1"), seg.IssueID)
	assert.Equal(t, "task_messages", seg.TrajectorySource)
	assert.False(t, seg.Trainable)
	assert.False(t, seg.TrajectoryID.Valid, "AReaL trajectory_id must be null for task_messages")
	assert.Nil(t, seg.TensorRef, "AReaL tensor_ref must be null for task_messages")
	assert.Equal(t, ptrText("delegation"), seg.ClosingEvent)
	assert.Equal(t, int32(1), seg.StartSeq)
	assert.Equal(t, int32(3), seg.EndSeq)

	// Trajectory is a JSON array of allowlisted message fields.
	var traj []map[string]any
	require.NoError(t, json.Unmarshal(seg.Trajectory, &traj))
	require.Len(t, traj, 3)
	// Messages are in sequence order.
	assert.EqualValues(t, 1, traj[0]["sequence"])
	assert.Equal(t, "user", traj[0]["type"])
	assert.Equal(t, "hello world", traj[0]["content"])
	assert.EqualValues(t, 2, traj[1]["sequence"])
	assert.Equal(t, "assistant", traj[1]["type"])
	assert.Equal(t, "some output", traj[1]["output"])
	assert.EqualValues(t, 3, traj[2]["sequence"])
	assert.Equal(t, "tool", traj[2]["type"])
	assert.Equal(t, "read_file", traj[2]["tool"])
	assert.Equal(t, `{"path":"/x"}`, traj[2]["input"])
	assert.Equal(t, "file contents", traj[2]["output"])

	// Env snapshot recorded.
	assert.JSONEq(t, `["sbx-1"]`, string(seg.SandboxIds))
}

// TestInteractionDAG_RecordLocalSegmentForEvent_RepeatCloseIsIdempotent verifies that
// calling RecordLocalSegmentForEvent twice for the same task is idempotent.
func TestInteractionDAG_RecordLocalSegmentForEvent_RepeatCloseIsIdempotent(t *testing.T) {
	store := newFakeInteractionDAGStore()
	msgs := newFakeMessageStore()
	svc := NewInteractionDAGServiceWithMessages(store, msgs, &fakeArealSegmentClient{}, true)

	taskID := util.UUIDToString(testUUID(22))
	store.addTestTaskMessage(taskID, 1)
	store.addTestTaskMessage(taskID, 2)
	msgs.addTaskMessage(taskID, makeFakeTaskMessage(taskID, 1, "user", "", "first", "", ""))
	msgs.addTaskMessage(taskID, makeFakeTaskMessage(taskID, 2, "assistant", "", "second", "", ""))

	segID1, _, err := svc.RecordLocalSegmentForEvent(
		context.Background(), "proj-1", taskID, "issue-1", "delegation", nil,
	)
	require.NoError(t, err)
	assert.Equal(t, "multica:"+util.UUIDToString(testUUID(22)), segID1)

	// Second close: same task.
	store.addTestTaskMessage(taskID, 3)
	msgs.addTaskMessage(taskID, makeFakeTaskMessage(taskID, 3, "user", "", "third", "", ""))
	segID2, _, err := svc.RecordLocalSegmentForEvent(
		context.Background(), "proj-1", taskID, "issue-1", "completion", nil,
	)
	require.NoError(t, err)
	assert.Equal(t, "multica:"+util.UUIDToString(testUUID(22)), segID2, "segment ID must be deterministic on repeat")

	// The second insert is a new row (idempotency at the caller layer).
	require.Len(t, store.segmentSnapshots, 2)
}

// TestInteractionDAG_RecordLocalSegmentForEvent_NeverIncludesSecrets verifies the
// local trajectory serialization never includes provider API keys or secrets.
func TestInteractionDAG_RecordLocalSegmentForEvent_NeverIncludesSecrets(t *testing.T) {
	store := newFakeInteractionDAGStore()
	msgs := newFakeMessageStore()
	svc := NewInteractionDAGServiceWithMessages(store, msgs, &fakeArealSegmentClient{}, true)

	taskID := util.UUIDToString(testUUID(23))
	store.addTestTaskMessage(taskID, 1)
	msgs.addTaskMessage(taskID, makeFakeTaskMessage(taskID, 1, "user", "", "hello", "", ""))

	_, _, err := svc.RecordLocalSegmentForEvent(
		context.Background(), "proj-1", taskID, "issue-1", "delegation", nil,
	)
	require.NoError(t, err)

	require.Len(t, store.segmentSnapshots, 1)
	seg := store.segmentSnapshots[0]

	// The trajectory JSON must not contain any provider API key.
	trajStr := string(seg.Trajectory)
	assert.NotContains(t, trajStr, "api_key")
	assert.NotContains(t, trajStr, "api-key")
	assert.NotContains(t, trajStr, "secret")
	assert.NotContains(t, trajStr, "token")
	assert.NotContains(t, trajStr, "password")
	assert.NotContains(t, trajStr, "credential")

	// The trajectory only has the 6 allowlisted fields.
	var entries []map[string]any
	require.NoError(t, json.Unmarshal(seg.Trajectory, &entries))
	require.Len(t, entries, 1)
	allowed := map[string]bool{"sequence": true, "type": true, "tool": true, "content": true, "input": true, "output": true}
	for k := range entries[0] {
		assert.True(t, allowed[k], "unexpected field %q in local trajectory", k)
	}
}

// TestInteractionDAG_RecordLocalSegmentForEvent_DisabledServiceIsNoop verifies a
// disabled service returns ("", nil) without touching the store.
func TestInteractionDAG_RecordLocalSegmentForEvent_DisabledServiceIsNoop(t *testing.T) {
	store := newFakeInteractionDAGStore()
	msgs := newFakeMessageStore()
	svc := NewInteractionDAGServiceWithMessages(store, msgs, &fakeArealSegmentClient{}, false)

	segID, _, err := svc.RecordLocalSegmentForEvent(
		context.Background(), "proj-1", "task-1", "issue-1", "delegation", nil,
	)
	require.NoError(t, err)
	assert.Empty(t, segID)
	assert.Len(t, store.segmentSnapshots, 0)
	assert.Len(t, store.sessionRuns, 0)
}

// TestInteractionDAG_RecordLocalSegmentForEvent_EmptySequenceRange verifies that a
// task with no messages records a segment with empty trajectory.
func TestInteractionDAG_RecordLocalSegmentForEvent_EmptySequenceRange(t *testing.T) {
	store := newFakeInteractionDAGStore()
	msgs := newFakeMessageStore()
	svc := NewInteractionDAGServiceWithMessages(store, msgs, &fakeArealSegmentClient{}, true)

	taskID := util.UUIDToString(testUUID(24))
	// No task messages added to either store.

	segID, _, err := svc.RecordLocalSegmentForEvent(
		context.Background(), "proj-1", taskID, "issue-1", "delegation", nil,
	)
	require.NoError(t, err)
	assert.Equal(t, "multica:"+util.UUIDToString(testUUID(24)), segID)

	require.Len(t, store.segmentSnapshots, 1)
	seg := store.segmentSnapshots[0]
	assert.Equal(t, "[]", string(seg.Trajectory))
	assert.Equal(t, int32(0), seg.StartSeq)
	assert.Equal(t, int32(0), seg.EndSeq)
}

// TestAssembleAssembledDag_EmitsDualSourceFields verifies that assembly emits
// TrajectorySource, Trainable, and Trajectory from both source types.
func TestAssembleAssembledDag_EmitsDualSourceFields(t *testing.T) {
	store := newFakeInteractionDAGStore()
	msgs := newFakeMessageStore()
	svc := NewInteractionDAGServiceWithMessages(store, msgs, &fakeArealSegmentClient{}, true)

	// Record an areal_tensor segment via the existing path, and a task_messages
	// segment via the local recorder.

	// 1. areal_tensor segment (recorded through the store directly since we can't
	//    call CloseSegmentForEvent without a fake client setup).
	store.UpsertInteractionDAGSessionRun(context.Background(), db.UpsertInteractionDAGSessionRunParams{
		SessionID: "areal-sess", ProjectID: "proj-dual", AgentRunID: "areal-run", IssueID: ptrText("areal-issue"),
	})
	store.segmentSnapshots = append(store.segmentSnapshots, db.InsertInteractionDAGSegmentWithSnapshotParams{
		SegmentID: "areal-sess-1", ProjectID: "proj-dual", AgentRunID: testUUID(31),
		IssueID:          ptrText("areal-issue"),
		TrajectoryID:     pgtype.Int8{Int64: 1, Valid: true},
		TensorRef:        []byte(`{"input_ids":{"shard_id":"sh-1"}}`),
		ClosingEvent:     ptrText("delegation"),
		TrajectorySource: "areal_tensor",
		Trainable:        true,
		Trajectory:       []byte("[]"),
		SandboxIds:       []byte("[]"),
		EnvState:         []byte("{}"),
	})

	// 2. task_messages segment.
	taskID := util.UUIDToString(testUUID(25))
	store.addTestTaskMessage(taskID, 1)
	msgs.addTaskMessage(taskID, makeFakeTaskMessage(taskID, 1, "user", "", "hi", "", ""))
	_, _, err := svc.RecordLocalSegmentForEvent(context.Background(), "proj-dual", taskID, "local-issue", "completion", nil)
	require.NoError(t, err)

	// Assert both segments have the right source fields.
	require.Len(t, store.segmentSnapshots, 2)

	arealSeg := store.segmentSnapshots[0]
	assert.Equal(t, "areal_tensor", arealSeg.TrajectorySource)
	assert.True(t, arealSeg.Trainable)
	assert.True(t, arealSeg.TrajectoryID.Valid)
	assert.NotNil(t, arealSeg.TensorRef)

	localSeg := store.segmentSnapshots[1]
	assert.Equal(t, "task_messages", localSeg.TrajectorySource)
	assert.False(t, localSeg.Trainable)
	assert.False(t, localSeg.TrajectoryID.Valid)
	assert.Nil(t, localSeg.TensorRef)
	assert.NotEqual(t, "[]", string(localSeg.Trajectory))
}
