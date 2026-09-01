// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Task 22 removed the retained legacy snapshot bridge from the live seams:
// the canonical Universal writer is the only live segment writer (AC56), so
// the Task 4/5 routing matrix (trained vs local vs channel-conversation
// recording, best-effort bridge errors, staging feed) no longer exists as
// production behavior and its tests were removed with it. What remains
// pinned here is the cleanup invariant itself: terminal side effects fire
// zero AReaL bridge calls and write zero legacy segments.

// TestInteractionDAG_TerminalSideEffectsMakeNoArealCalls pins the cleanup
// from the caller side: after the terminal transaction recorded the
// canonical boundary, the post-commit side effects never reach the AReaL
// bridge (no close, no export) regardless of the task's training context.
func TestInteractionDAG_TerminalSideEffectsMakeNoArealCalls(t *testing.T) {
	store := newFakeInteractionDAGStore()
	client := &fakeArealSegmentClient{closeSegmentID: 1, exportPayload: json.RawMessage(shardExport)}
	svc := newSeamTaskService(store, client)
	trained := db.AgentInboxEvent{WorkspaceID: testUUID(90), ID: testUUID(1), Context: arealProxyContext("sess-1", "key-1")}

	svc.FinalizeTerminalTaskPostCommitSideEffects(context.Background(), trained)

	assert.Empty(t, client.closeCalls, "terminal side effects fire zero AReaL close calls")
	assert.Empty(t, client.exportCalls, "terminal side effects fire zero AReaL export calls")
	assert.Empty(t, store.segmentSnapshots, "terminal side effects write zero legacy segments")
}
