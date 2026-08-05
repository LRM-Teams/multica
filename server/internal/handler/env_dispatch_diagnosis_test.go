package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestDiagnoseEnvDispatchProject_WithoutEnablementFlags(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	// Default DIAGNOSIS_EXECUTION_MODE is sandbox (async 200). This test pins
	// the deprecated server path so a missing agent binary fails closed.
	t.Setenv("DIAGNOSIS_EXECUTION_MODE", "server")
	t.Setenv("DIAGNOSIS_AGENT_PATH", "/nonexistent/multica-pi")

	ctx := context.Background()
	projectID, rootTaskID := seedHandlerDagNonTrainingCompletedRoot(t, ctx, testWorkspaceID)
	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM env_dispatch_run WHERE project_id = $1`, projectID)
		testPool.Exec(ctx, `DELETE FROM project WHERE id = $1`, projectID)
	})
	if _, err := testPool.Exec(ctx, `UPDATE agent_inbox_event SET status = 'acked' WHERE id = $1`, rootTaskID); err != nil {
		t.Fatalf("set root task terminal: %v", err)
	}
	seedDAGSegment(t, projectID, projectID+"-diagnosis", rootTaskID, 1)

	w := httptest.NewRecorder()
	r := withURLParam(newRequest(http.MethodPost, "/api/v1/env-dispatch/"+projectID+"/diagnosis", nil), "projectID", projectID)
	// Direct handler call bypasses workspace middleware; mirror other env_dispatch tests.
	r = r.WithContext(middleware.SetMemberContext(r.Context(), testWorkspaceID, db.Member{}))
	testHandler.DiagnoseEnvDispatchProject(w, r)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code, "valid terminal DAG must reach the runner seam: %s", w.Body.String())
	assert.Contains(t, w.Body.String(), `"error":"diagnosis_failed"`)
}

func TestDiagnosisTopologicalSegmentIDs_RespectsEdges(t *testing.T) {
	ordered, err := diagnosisTopologicalSegmentIDs(service.AssembledDag{
		Segments: []service.AssembledSegment{
			{SegmentID: "root"},
			{SegmentID: "child"},
			{SegmentID: "leaf"},
		},
		Edges: []service.AssembledEdge{
			{SrcSegmentID: "root", DstSegmentID: "child"},
			{SrcSegmentID: "child", DstSegmentID: "leaf"},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"root", "child", "leaf"}, ordered)
}

// ── T023: consumer transparency across execution modes ──

// TestDiagnosisReportContractUnchangedAcrossExecutionModes pins the trigger
// response contract: the sandbox branch returns the same DiagnosisReport shape
// (promptly, status running) as the server branch (status completed) — only
// the field values differ, never the keys.
func TestDiagnosisReportContractUnchangedAcrossExecutionModes(t *testing.T) {
	serverModeReport := service.DiagnosisReport{
		RunID: "run-server", CompletedSegments: 3, TotalSegments: 3, Status: service.DiagnosisRunCompleted,
	}
	sandboxModeReport := service.DiagnosisReport{
		RunID: "run-sandbox", CompletedSegments: 0, TotalSegments: 3, Status: service.DiagnosisRunRunning,
	}
	assert.Equal(t, diagnosisReportJSONKeys(t, serverModeReport), diagnosisReportJSONKeys(t, sandboxModeReport))
}

func diagnosisReportJSONKeys(t *testing.T, report service.DiagnosisReport) []string {
	t.Helper()
	encoded, err := json.Marshal(report)
	require.NoError(t, err)
	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(encoded, &fields))
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// TestGetDag_DiagnosisStepRewardsTransparentAcrossExecutionModes proves the
// /dag consumer cannot tell which execution mode produced the diagnosis:
// rewards persisted through the sandbox-mode transport (the network
// diagnosis-run API the sandboxed agent calls) surface with the same
// step_rewards/assistant_turn_seqs/score_max shape as the identical rewards
// persisted through the server-mode transport (the DAG writer behind the
// loopback tool server).
func TestGetDag_DiagnosisStepRewardsTransparentAcrossExecutionModes(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	t.Setenv("DIAGNOSIS_AGENT_SCORE_MAX", "20")
	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "GetDagTransparencyAgent", []byte("[]"))
	projectID, taskID := seedTrainingRollout(t, testWorkspaceID, agentID, "completed")
	seedDAGSegment(t, projectID, "dag-transparency-sess", taskID, 7)
	segID := "dag-transparency-sess-7"
	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM interaction_dag_step_reward WHERE segment_id = $1`, segID)
	})

	rewards := []diagnosisRunStepRewardEntry{
		{Seq: 1, Score: 18, Rationale: "decisive turn"},
		{Seq: 2, Score: 7, Rationale: "drifting turn"},
	}
	getDag := func() service.AssembledDag {
		t.Helper()
		w := httptest.NewRecorder()
		testHandler.GetDag(w, getDagRequest(t, projectID))
		require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
		var dag service.AssembledDag
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &dag))
		return dag
	}

	// Phase 1 — sandbox mode: rewards land through the network diagnosis-run
	// API handler, exactly as the sandboxed agent's extension calls it.
	fakeState := newFakeDiagnosisRunAPIStore()
	fakeState.addSegment(service.SegmentDiagnosisCheckpoint{
		RunID: "run-sandbox", SegmentID: segID,
		ExpectedRewardSeqs: []int32{1, 2}, ExpectedRewardCount: 2,
	})
	deps := diagnosisRunAPIDeps{
		state:     fakeState,
		dagWriter: diagnosisDAGWriterAdapter{store: testHandler.Queries},
	}
	body, err := json.Marshal(diagnosisRunRecordStepRewardsRequest{SegmentID: segID, Rewards: rewards})
	require.NoError(t, err)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/diagnosis-runs/run-sandbox/record-step-rewards", bytes.NewReader(body))
	r = r.WithContext(middleware.WithDiagnosisRun(r.Context(), middleware.DiagnosisRun{
		RunID: "run-sandbox", ProjectID: projectID, OrderedSegmentIDs: []string{segID},
		Status: string(service.DiagnosisRunRunning), ExecutionMode: service.DiagnosisExecutionModeSandbox,
	}))
	deps.recordStepRewards(w, r)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	var sandboxResp diagnosisRunRecordStepRewardsResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &sandboxResp))
	assert.ElementsMatch(t, []int{1, 2}, sandboxResp.PersistedSeqs, "sandbox transport must persist both rewards")
	sandboxModeDag := getDag()

	// Phase 2 — server mode: reset the rewards and persist the identical set
	// through the server-mode writer (diagnosisDAGWriterAdapter, the same
	// adapter the in-process runner uses behind the loopback tool server).
	_, err = testPool.Exec(ctx, `DELETE FROM interaction_dag_step_reward WHERE segment_id = $1`, segID)
	require.NoError(t, err)
	serverWriter := diagnosisDAGWriterAdapter{store: testHandler.Queries}
	for _, reward := range rewards {
		require.NoError(t, serverWriter.UpsertDiagnosisStepReward(ctx, projectID, segID, int32(reward.Seq), reward.Score, reward.Rationale))
	}
	serverModeDag := getDag()

	// Consumer transparency: the consumer-facing shapes are identical.
	marshal := func(v any) string {
		t.Helper()
		encoded, err := json.Marshal(v)
		require.NoError(t, err)
		return string(encoded)
	}
	require.Len(t, sandboxModeDag.Segments, 1)
	require.Len(t, serverModeDag.Segments, 1)
	assert.JSONEq(t, marshal(serverModeDag.StepRewards), marshal(sandboxModeDag.StepRewards), "step_rewards shape/content")
	assert.JSONEq(t, marshal(serverModeDag.Segments[0].AssistantTurnSeqs), marshal(sandboxModeDag.Segments[0].AssistantTurnSeqs), "assistant_turn_seqs shape/content")
	assert.Equal(t, serverModeDag.ScoreMax, sandboxModeDag.ScoreMax, "score_max")
	assert.Equal(t, 20, sandboxModeDag.ScoreMax, "score_max stamped from DIAGNOSIS_AGENT_SCORE_MAX")
	require.Len(t, sandboxModeDag.StepRewards, 2, "rewards persisted via the sandbox transport must be served")
}
