// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

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
	trained := db.AgentTaskQueue{ID: testUUID(1), Context: arealProxyContext("sess-1", "key-1")}

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
	nonTrained := db.AgentTaskQueue{ID: testUUID(2), Context: nil}
	npSvc.closeSegmentForDelegation(context.Background(), nonTrained, "proj-1", leanSnap())
	npSvc.closeSegmentForTerminal(context.Background(), nonTrained, "proj-1", leanSnap())
	assert.Empty(t, npStore.segmentSnapshots, "non-trained task records nothing")
	assert.Empty(t, npStore.edges)
}

// TestInteractionDAG_RecordingErrorIsBestEffort verifies a recording failure
// never panics or leaves a partial edge: a bridge close error aborts the segment
// cleanly, and an edge-insert error leaves the segment recorded (edge swallowed).
func TestInteractionDAG_RecordingErrorIsBestEffort(t *testing.T) {
	// (a) Bridge close failure on the delegation seam: no panic, no segment, no edge.
	store := newFakeInteractionDAGStore()
	client := &fakeArealSegmentClient{closeSegmentErr: errors.New("bridge down"), exportPayload: json.RawMessage(shardExport)}
	svc := newSeamTaskService(store, client)
	parent := db.AgentTaskQueue{ID: testUUID(1), ParentTaskID: testUUID(9), Context: arealProxyContext("sess-1", "key-1")}
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
	child := db.AgentTaskQueue{ID: testUUID(2), ParentTaskID: parentID, Context: arealProxyContext("sess-c", "key-c")}
	require.NoError(t, svc2.Training.DAG.RecordSessionAgentRun(context.Background(), "proj-1", "sess-c", util.UUIDToString(child.ID), "issue-1"))
	svc2.closeSegmentForTerminal(context.Background(), child, "proj-1", leanSnap())
	assert.Len(t, store2.segmentSnapshots, 2, "terminal segment recorded even when the edge insert fails")
}
