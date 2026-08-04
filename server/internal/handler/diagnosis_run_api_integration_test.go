// SPDX-License-Identifier: Apache-2.0

package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// fakeDiagnosisRunMessageStore is an in-memory service.MessageStore used to
// prove the task-context endpoint returns exactly what a direct GetTaskContext
// call returns (spec 005 T020).
type fakeDiagnosisRunMessageStore struct {
	issue db.Issue
}

func (f *fakeDiagnosisRunMessageStore) MessagesForTaskInRange(context.Context, string, int32, int32) ([]db.TaskMessage, error) {
	return nil, nil
}

func (f *fakeDiagnosisRunMessageStore) GetProjectInWorkspace(context.Context, db.GetProjectInWorkspaceParams) (db.Project, error) {
	return db.Project{}, nil
}

func (f *fakeDiagnosisRunMessageStore) GetIssueForTask(context.Context, string) (db.Issue, error) {
	return f.issue, nil
}

// TestDiagnosisRunAPI_InputAssembly_TaskContextMatchesDirectGetTaskContext
// fetches the task context through the real API handler and asserts the
// payload equals the direct service.GetTaskContext output (after the shared
// truncation helper), so the sandboxed agent assembles the same input the
// server-side path would.
func TestDiagnosisRunAPI_InputAssembly_TaskContextMatchesDirectGetTaskContext(t *testing.T) {
	env := newDiagnosisRunAPITestEnv(t)

	workspaceID := pgtype.UUID{Bytes: [16]byte{0x42}, Valid: true}
	msgStore := &fakeDiagnosisRunMessageStore{issue: db.Issue{
		WorkspaceID:        workspaceID,
		Title:              "Fallback title",
		Description:        pgtype.Text{String: "Fix the flaky integration test", Valid: true},
		AcceptanceCriteria: []byte(`{"tests":["go test ./..."]}`),
	}}
	env.deps.taskContextFn = func(ctx context.Context, run service.DiagnosisRunCheckpoint) (service.TaskContext, error) {
		return service.GetTaskContext(ctx, msgStore, workspaceID, run.TaskID)
	}

	// Direct server-side assembly.
	direct, err := service.GetTaskContext(context.Background(), msgStore, workspaceID, "task-1")
	require.NoError(t, err)
	goal, goalTrunc, gold, goldTrunc := service.TruncateDiagnosisTaskContext(direct)

	// Assembly through the network API handler.
	w := env.do(t, env.deps.taskContext, http.MethodGet, "/task-context", nil, http.StatusOK)
	var viaAPI diagnosisRunTaskContextResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&viaAPI))

	assert.Equal(t, goal, viaAPI.Goal)
	assert.Equal(t, goalTrunc, viaAPI.GoalTruncated)
	assert.Equal(t, gold, viaAPI.GoldContext)
	assert.Equal(t, goldTrunc, viaAPI.GoldContextTruncated)
}

// TestDiagnosisRunAPI_InputAssembly_PagesMultiPageSegmentToCompletion pages a
// 45-message segment to completion through the real API handler and asserts
// the assembled input equals direct paging over the same pager with the run's
// cursor key. The handler-issued HMAC cursor must be accepted by the direct
// (shared-key) path, proving both transports read identical content.
func TestDiagnosisRunAPI_InputAssembly_PagesMultiPageSegmentToCompletion(t *testing.T) {
	env := newDiagnosisRunAPITestEnv(t)

	const total = 45 // > 2 pages at the 20-turn budget
	var assistantSeqs []int32
	for i := 1; i <= total; i++ {
		typ := "assistant"
		if i%3 == 0 {
			typ = "user"
		} else {
			assistantSeqs = append(assistantSeqs, int32(i))
		}
		env.pager.addMessage(int32(i), typ, fmt.Sprintf("message-%02d body", i))
	}
	env.deps.segments = &fakeDiagnosisRunSegmentLookup{segments: map[string]db.GetInteractionDAGSegmentByIDRow{
		"seg-1": {SegmentID: "seg-1", ProjectID: "project-1", AgentRunID: "task-1", StartSeq: 1, EndSeq: total},
	}}
	env.store.addSegment(service.SegmentDiagnosisCheckpoint{
		RunID: "run-1", SegmentID: "seg-1", Status: service.SegmentDiagnosisInProgress,
		ExpectedMessageCount: total, ExpectedRewardSeqs: assistantSeqs, ExpectedRewardCount: len(assistantSeqs),
	})

	// Page through the API handler to completion.
	var viaAPI []service.DiagnosisMessage
	var apiCursors []string
	cursor := ""
	for {
		w := env.do(t, env.deps.getSegmentMessages, http.MethodPost, "/get-segment-messages",
			map[string]any{"segment_id": "seg-1", "cursor": cursor}, http.StatusOK)
		var page service.SegmentMessagePage
		require.NoError(t, json.NewDecoder(w.Body).Decode(&page))
		assert.LessOrEqual(t, len(page.Messages), 20, "page must respect the 20-turn budget")
		viaAPI = append(viaAPI, page.Messages...)
		apiCursors = append(apiCursors, page.NextCursor)
		if page.Complete {
			assert.Empty(t, page.NextCursor)
			break
		}
		require.NotEmpty(t, page.NextCursor, "incomplete page must carry a next cursor")
		cursor = page.NextCursor
	}
	require.Len(t, viaAPI, total, "all messages assembled via the API")

	// Direct paging over the same pager with the run's derived cursor key,
	// continuing with the handler-issued cursors: the HMAC cursors minted by
	// the API must be accepted by the shared paging logic with the run key.
	key := service.DiagnosisRunCursorKey(env.run.CapabilityTokenHash)
	var direct []service.DiagnosisMessage
	cursor = ""
	for i, apiCursor := range apiCursors {
		page, err := service.GetSegmentMessagePageWithKey(context.Background(), env.pager, key, "task-1", "seg-1", 1, total, cursor)
		require.NoError(t, err, "direct paging must accept the cursor chain (page %d)", i)
		direct = append(direct, page.Messages...)
		assert.Equal(t, apiCursor, page.NextCursor, "cursor %d must be byte-identical across transports", i)
		cursor = apiCursor
		if page.Complete {
			break
		}
	}

	assert.Equal(t, direct, viaAPI, "API-assembled input must equal direct paging output")

	seg, err := env.store.GetSegment(context.Background(), "run-1", "seg-1")
	require.NoError(t, err)
	assert.Equal(t, total, seg.FetchedMessageCount, "coverage recorded to completion")
}

// TestDiagnosisRunAPI_SandboxWiringNeverReferencesStores is the spec 005 T020
// guard: the sandbox-mode wiring (orchestrator + enqueue path) must never
// reference the DAG/message stores — the sandboxed agent's inputs may arrive
// only through the diagnosis-run API. This is a grep-level guard over the
// wiring source files; the API-level integration tests above prove the API
// alone suffices to assemble the full input.
func TestDiagnosisRunAPI_SandboxWiringNeverReferencesStores(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok)
	handlerDir := filepath.Dir(thisFile)

	wiringFiles := []string{
		filepath.Join(handlerDir, "..", "service", "diagnosis_sandbox.go"),      // orchestrator
		filepath.Join(handlerDir, "diagnosis_sandbox_adapter.go"),               // production enqueue path
		filepath.Join(handlerDir, "..", "service", "diagnosis_pi_extension.go"), // in-sandbox tool surface
	}
	for _, path := range wiringFiles {
		data, err := os.ReadFile(path)
		require.NoError(t, err, "guard requires reading %s", path)
		src := string(data)
		assert.NotContains(t, src, "DAGStore", "%s must not reference DAGStore (sandbox inputs come only via the API)", filepath.Base(path))
		assert.NotContains(t, src, "MessageStore", "%s must not reference MessageStore (sandbox inputs come only via the API)", filepath.Base(path))
	}
}
