// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Task 5: gating + best-effort recording.
//
// Gating is enforced inside closeSegmentForDelegation / closeSegmentForTerminal
// (Task 4): each returns early unless s.Training != nil && s.Training.DAG.Enabled()
// AND the task carries an areal_proxy context (a trained rollout). Recording is
// best-effort: close/edge errors are slog.Warn'd and the run continues.
//
// Squad-context handoff is intentionally handled at the producer/parent
// delegation seam, not at daemon-claim time. The child/receiver session is not
// closed until it has emitted its own model output.

// TestInteractionDAG_NonTrainedRolloutRecordsNothing verifies every gating
// condition records no segment/edge across both seams: Training nil, DAG
// disabled, and a task without areal_proxy (not a trained run).
func TestInteractionDAG_NonTrainedRolloutRecordsNothing(t *testing.T) {
	trained := db.AgentInboxEvent{ID: testUUID(1), Context: arealProxyContext("sess-1", "key-1")}

	// (a) Training nil -> both seams no-op (no panic, nothing to record against).
	nilSvc := &TaskService{}
	nilSvc.closeSegmentForDelegation(context.Background(), trained, "proj-1", leanSnap())
	nilSvc.closeSegmentForTerminal(context.Background(), trained, "proj-1", leanSnap())

	// (b) DAG disabled -> both seams no-op.
	disStore := newFakeInteractionDAGStore()
	disSvc := &TaskService{Training: &TrainingSessionDeps{DAG: NewInteractionDAGService(disStore, &fakeArealSegmentClient{}, false)}}
	disSvc.closeSegmentForDelegation(context.Background(), trained, "proj-1", leanSnap())
	disSvc.closeSegmentForTerminal(context.Background(), trained, "proj-1", leanSnap())
	assert.Empty(t, disStore.segmentSnapshots, "disabled DAG records nothing")
	assert.Empty(t, disStore.edges)

	// (c) task without areal_proxy (not a trained run) -> both seams no-op.
	npStore := newFakeInteractionDAGStore()
	npSvc := newSeamTaskService(npStore, &fakeArealSegmentClient{closeSegmentID: 1, exportPayload: json.RawMessage(shardExport)})
	nonTrained := db.AgentInboxEvent{ID: testUUID(2), Context: nil}
	npSvc.closeSegmentForDelegation(context.Background(), nonTrained, "proj-1", leanSnap())
	npSvc.closeSegmentForTerminal(context.Background(), nonTrained, "proj-1", leanSnap())
	assert.Empty(t, npStore.segmentSnapshots, "non-trained task records nothing")
	assert.Empty(t, npStore.edges)
}

// fakeEnvDispatchChecker is a test double for EnvDispatchRunChecker.
type fakeEnvDispatchChecker struct {
	hasRun bool
	err    error
}

func (f *fakeEnvDispatchChecker) HasEnvDispatchRun(_ context.Context, _ string) (bool, error) {
	return f.hasRun, f.err
}

// taskMsg is a helper to build a db.TaskMessage for test fixtures.
func taskMsg(taskID pgtype.UUID, seq int32, typ, content string) db.TaskMessage {
	return db.TaskMessage{
		TaskID:  taskID,
		Seq:     seq,
		Type:    typ,
		Content: pgtype.Text{String: content, Valid: true},
	}
}

// newSeamTaskServiceWithChecker builds a TaskService with both Training.DAG
// (wired with MessageStore for local recording) and an EnvDispatchCheck,
// for testing non-training env-dispatch seam behavior.
func newSeamTaskServiceWithChecker(store *fakeInteractionDAGStore, client *fakeArealSegmentClient, msgs MessageStore, checker EnvDispatchRunChecker) *TaskService {
	dag := NewInteractionDAGServiceWithMessages(store, msgs, client, true)
	return &TaskService{
		Training:         &TrainingSessionDeps{DAG: dag},
		EnvDispatchCheck: checker,
	}
}

// TestInteractionDAG_NonTrainEnvDispatchRecordsLocal verifies that a
// non-training env-dispatch task records a local segment via
// RecordLocalSegmentForEvent instead of making AReaL bridge calls. The task has
// no areal_proxy context, but the project has an env_dispatch_run row.
func TestInteractionDAG_NonTrainEnvDispatchRecordsLocal(t *testing.T) {
	store := newFakeInteractionDAGStore()
	client := &fakeArealSegmentClient{closeSegmentID: 1, exportPayload: json.RawMessage(shardExport)}
	msgs := newFakeMessageStore()
	checker := &fakeEnvDispatchChecker{hasRun: true}
	svc := newSeamTaskServiceWithChecker(store, client, msgs, checker)

	taskID := testUUID(1)
	task := db.AgentInboxEvent{ID: taskID, Context: nil} // no areal_proxy
	msgs.addTaskMessage(util.UUIDToString(taskID), taskMsg(taskID, 1, "assistant", "hello"))

	svc.closeSegmentForTerminal(context.Background(), task, "proj-1", leanSnap())

	require.Len(t, store.segmentSnapshots, 1, "non-training env-dispatch task records a local segment")
	seg := store.segmentSnapshots[0]
	assert.Equal(t, "multica:"+util.UUIDToString(taskID), seg.SegmentID)
	assert.Equal(t, util.UUIDToString(taskID), seg.AgentRunID)
	assert.Equal(t, "task_messages", seg.TrajectorySource)
	assert.False(t, seg.Trainable)
	assert.False(t, seg.TrajectoryID.Valid, "no AReaL trajectory_id for local segment")
	assert.Nil(t, seg.TensorRef, "no tensor_ref for local segment")

	// Zero AReaL client calls in non-training path.
	assert.Empty(t, client.closeCalls, "zero AReaL close calls")
	assert.Empty(t, client.exportCalls, "zero AReaL export calls")
}

// TestInteractionDAG_NonTrainEnvDispatchDelegationRecordsLocal verifies that
// a non-training env-dispatch parent delegation records a local segment.
func TestInteractionDAG_NonTrainEnvDispatchDelegationRecordsLocal(t *testing.T) {
	store := newFakeInteractionDAGStore()
	client := &fakeArealSegmentClient{closeSegmentID: 1, exportPayload: json.RawMessage(shardExport)}
	msgs := newFakeMessageStore()
	checker := &fakeEnvDispatchChecker{hasRun: true}
	svc := newSeamTaskServiceWithChecker(store, client, msgs, checker)

	parentID := testUUID(1)
	parent := db.AgentInboxEvent{ID: parentID, Context: nil}
	msgs.addTaskMessage(util.UUIDToString(parentID), taskMsg(parentID, 1, "assistant", "delegating"))

	svc.closeSegmentForDelegation(context.Background(), parent, "proj-1", leanSnap())

	require.Len(t, store.segmentSnapshots, 1)
	assert.Equal(t, "delegation", store.segmentSnapshots[0].ClosingEvent.String)
	assert.Empty(t, client.closeCalls, "zero AReaL calls")
}

// TestInteractionDAG_MixedTrainNonTrainRecordsBothSources verifies a mixed
// trained/non-trained pair: a trained parent that delegates to a non-trained
// child. Both segments are recorded — the parent via AReaL bridge, the child
// via local task_messages — with a delegation edge between them.
func TestInteractionDAG_MixedTrainNonTrainRecordsBothSources(t *testing.T) {
	store := newFakeInteractionDAGStore()
	client := &fakeArealSegmentClient{exportPayload: json.RawMessage(shardExport)}
	msgs := newFakeMessageStore()
	checker := &fakeEnvDispatchChecker{hasRun: true}
	dag := NewInteractionDAGServiceWithMessages(store, msgs, client, true)
	svc := &TaskService{
		Training:         &TrainingSessionDeps{DAG: dag},
		EnvDispatchCheck: checker,
	}

	parentID, childID := testUUID(1), testUUID(2)
	require.NoError(t, dag.RecordSessionAgentRun(context.Background(), "proj-1", "sess-p", util.UUIDToString(parentID), "issue-1"))

	// Parent closes via delegation with areal_proxy (training path).
	client.closeSegmentID = 3
	parent := db.AgentInboxEvent{ID: parentID, Context: arealProxyContext("sess-p", "key-p")}
	svc.closeSegmentForDelegation(context.Background(), parent, "proj-1", leanSnap())

	require.Len(t, store.segmentSnapshots, 1, "parent segment recorded via AReaL")
	assert.Equal(t, "areal_tensor", store.segmentSnapshots[0].TrajectorySource)
	assert.True(t, store.segmentSnapshots[0].Trainable)
	require.Len(t, client.closeCalls, 1, "one AReaL close call for trained parent")

	// Child completes without areal_proxy (non-training path).
	child := db.AgentInboxEvent{ID: childID, ParentTaskID: parentID, Context: nil}
	msgs.addTaskMessage(util.UUIDToString(childID), taskMsg(childID, 1, "assistant", "done"))

	svc.closeSegmentForTerminal(context.Background(), child, "proj-1", leanSnap())

	require.Len(t, store.segmentSnapshots, 2, "child segment recorded via local task_messages")
	childSeg := store.segmentSnapshots[1]
	assert.Equal(t, "task_messages", childSeg.TrajectorySource)
	assert.False(t, childSeg.Trainable)
	assert.Equal(t, "completion", childSeg.ClosingEvent.String)

	// Edge between parent and child.
	require.Len(t, store.edges, 1)
	assert.Equal(t, "sess-p-3", store.edges[0].SrcSegmentID)
	assert.Equal(t, "multica:"+util.UUIDToString(childID), store.edges[0].DstSegmentID)
	assert.Equal(t, "delegation", store.edges[0].Type)
}

// TestInteractionDAG_NonEnvDispatchTaskNoop verifies that ordinary
// non-env-dispatch tasks remain no-ops: no areal_proxy AND no env_dispatch_run
// means the seam returns early without recording anything.
func TestInteractionDAG_NonEnvDispatchTaskNoop(t *testing.T) {
	store := newFakeInteractionDAGStore()
	client := &fakeArealSegmentClient{closeSegmentID: 1, exportPayload: json.RawMessage(shardExport)}
	msgs := newFakeMessageStore()
	checker := &fakeEnvDispatchChecker{hasRun: false} // not an env-dispatch project
	svc := newSeamTaskServiceWithChecker(store, client, msgs, checker)

	task := db.AgentInboxEvent{ID: testUUID(1), Context: nil}
	svc.closeSegmentForTerminal(context.Background(), task, "proj-1", leanSnap())

	assert.Empty(t, store.segmentSnapshots, "non-env-dispatch task records nothing")
	assert.Empty(t, store.edges)
	assert.Empty(t, client.closeCalls)
}

// TestInteractionDAG_NonTrainNoFakeArealCalls verifies the non-training path
// makes ZERO AReaL client calls — no fake session creation, no trajectory
// lifecycle calls, no CloseSegment, no ExportTrajectory.
func TestInteractionDAG_NonTrainNoFakeArealCalls(t *testing.T) {
	store := newFakeInteractionDAGStore()
	client := &fakeArealSegmentClient{closeSegmentID: 1, exportPayload: json.RawMessage(shardExport)}
	msgs := newFakeMessageStore()
	checker := &fakeEnvDispatchChecker{hasRun: true}
	svc := newSeamTaskServiceWithChecker(store, client, msgs, checker)

	for i := 1; i <= 3; i++ {
		taskID := testUUID(byte(i))
		task := db.AgentInboxEvent{ID: taskID, Context: nil}
		msgs.addTaskMessage(util.UUIDToString(taskID), taskMsg(taskID, 1, "assistant", "msg"))
		svc.closeSegmentForTerminal(context.Background(), task, "proj-1", leanSnap())
	}

	assert.Empty(t, client.closeCalls, "zero AReaL close calls across multiple non-training tasks")
	assert.Empty(t, client.exportCalls, "zero AReaL export calls across multiple non-training tasks")
	assert.Len(t, store.segmentSnapshots, 3, "all 3 non-training tasks recorded locally")
	for _, seg := range store.segmentSnapshots {
		assert.Equal(t, "task_messages", seg.TrajectorySource)
		assert.False(t, seg.Trainable)
	}
}

// TestInteractionDAG_RecordingErrorIsBestEffort verifies a recording failure
// never panics or leaves a partial edge: a bridge close error aborts the segment
// cleanly, and an edge-insert error leaves the segment recorded (edge swallowed).
func TestInteractionDAG_RecordingErrorIsBestEffort(t *testing.T) {
	// (a) Bridge close failure on the delegation seam: no panic, no segment, no edge.
	store := newFakeInteractionDAGStore()
	client := &fakeArealSegmentClient{closeSegmentErr: errors.New("bridge down"), exportPayload: json.RawMessage(shardExport)}
	svc := newSeamTaskService(store, client)
	parent := db.AgentInboxEvent{ID: testUUID(1), ParentTaskID: testUUID(9), Context: arealProxyContext("sess-1", "key-1")}
	require.NoError(t, svc.Training.DAG.RecordSessionAgentRun(context.Background(), "proj-1", "sess-1", util.UUIDToString(parent.ID), "issue-1"))
	svc.closeSegmentForDelegation(context.Background(), parent, "proj-1", leanSnap())
	assert.Empty(t, store.segmentSnapshots, "close failure must not record a segment")
	assert.Empty(t, store.edges, "close failure must not record an edge")

	// (b) Edge-insert failure on the terminal seam: the segment is still recorded;
	// the edge failure is swallowed (best-effort), no panic.
	store2 := newFakeInteractionDAGStore()
	store2.insertEdgeErr = errors.New("edge insert failed")
	client2 := &fakeArealSegmentClient{closeSegmentID: 2, exportPayload: json.RawMessage(shardExport)}
	svc2 := newSeamTaskService(store2, client2)
	parentID := testUUID(5)
	require.NoError(t, svc2.Training.DAG.RecordSessionAgentRun(context.Background(), "proj-1", "sess-p", util.UUIDToString(parentID), "issue-1"))
	_, err := svc2.Training.DAG.CloseSegmentForEvent(context.Background(), "proj-1", "sess-p", "key-p", "delegation", leanSnap())
	require.NoError(t, err)
	child := db.AgentInboxEvent{ID: testUUID(2), ParentTaskID: parentID, Context: arealProxyContext("sess-c", "key-c")}
	require.NoError(t, svc2.Training.DAG.RecordSessionAgentRun(context.Background(), "proj-1", "sess-c", util.UUIDToString(child.ID), "issue-1"))
	svc2.closeSegmentForTerminal(context.Background(), child, "proj-1", leanSnap())
	assert.Len(t, store2.segmentSnapshots, 2, "terminal segment recorded even when the edge insert fails")
}
