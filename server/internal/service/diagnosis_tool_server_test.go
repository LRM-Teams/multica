// SPDX-License-Identifier: Apache-2.0

package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// fakeDiagnosisDAGWriter is an in-memory DiagnosisDAGWriter for tool-server tests.
type fakeDiagnosisDAGWriter struct {
	mu      sync.Mutex
	rewards map[string]db.InsertInteractionDAGStepRewardParams // key: segID:seq
}

func newFakeDiagnosisDAGWriter() *fakeDiagnosisDAGWriter {
	return &fakeDiagnosisDAGWriter{rewards: make(map[string]db.InsertInteractionDAGStepRewardParams)}
}

func (f *fakeDiagnosisDAGWriter) key(segmentID string, seq int32) string {
	return fmt.Sprintf("%s:%d", segmentID, seq)
}

func (f *fakeDiagnosisDAGWriter) UpsertDiagnosisStepReward(_ context.Context, projectID, segmentID string, seq int32, score int, rationale string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rewards[f.key(segmentID, seq)] = db.InsertInteractionDAGStepRewardParams{
		SegmentID: segmentID,
		Seq:       seq,
		Score:     int32(score),
		Rationale: rationale,
	}
	return nil
}

func (f *fakeDiagnosisDAGWriter) GetDiagnosisStepReward(_ context.Context, projectID, segmentID string, seq int32) (int, string, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.rewards[f.key(segmentID, seq)]
	if !ok {
		return 0, "", false, nil
	}
	return int(r.Score), r.Rationale, true, nil
}

func (f *fakeDiagnosisDAGWriter) CountDiagnosisStepRewards(_ context.Context, projectID, segmentID string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	count := 0
	for k := range f.rewards {
		// key format: "segmentID:seq"
		if len(k) > len(segmentID) && k[:len(segmentID)] == segmentID && k[len(segmentID)] == ':' {
			count++
		}
	}
	return count, nil
}

func newTestDiagnosisToolServer(t *testing.T) (*DiagnosisToolServer, *fakeDiagnosisStateQueries, *fakeDiagnosisDAGWriter) {
	t.Helper()
	fakeQ := newFakeDiagnosisStateQueries()
	store := NewDiagnosisStateStore(fakeQ)
	dagWriter := newFakeDiagnosisDAGWriter()
	pager := newFakeDiagnosisMessagePager(t)

	ckpt, err := store.CreateRun(context.Background(), DiagnosisRunCheckpoint{
		RunID:             "run-test",
		ProjectID:         "project-test",
		TaskID:            "task-test",
		TopologyHash:      "topo-test-hash",
		OrderedSegmentIDs: []string{"seg-1", "seg-2"},
	})
	require.NoError(t, err)

	server, err := NewDiagnosisToolServer(ckpt, store, pager, dagWriter)
	require.NoError(t, err)

	// Start the server on ephemeral port. Save and restore the global pager key
	// to prevent cross-test pollution (the cursor tests use their own key).
	prevKey := diagnosisPagerKey
	baseURL, err := server.ListenAndServe()
	require.NoError(t, err)
	server.baseURL = baseURL
	t.Cleanup(func() {
		server.Shutdown(context.Background())
		diagnosisPagerKey = prevKey
	})

	return server, fakeQ, dagWriter
}

func authHeader(server *DiagnosisToolServer) string {
	return "Bearer " + server.BearerToken()
}

func doRequest(t *testing.T, server *DiagnosisToolServer, method, path string, body any, expectedStatus int) *http.Response {
	t.Helper()
	var bodyBytes []byte
	if body != nil {
		var err error
		bodyBytes, err = json.Marshal(body)
		require.NoError(t, err)
	}
	url := server.baseURL + path
	req, err := http.NewRequest(method, url, bytes.NewReader(bodyBytes))
	require.NoError(t, err)
	req.Header.Set("Authorization", authHeader(server))
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { resp.Body.Close() })

	if expectedStatus != 0 {
		assert.Equal(t, expectedStatus, resp.StatusCode, "unexpected status for %s %s", method, path)
	}
	return resp
}

func TestDiagnosisToolServer_AuthRejectsMissingToken(t *testing.T) {
	server, _, _ := newTestDiagnosisToolServer(t)

	req, _ := http.NewRequest("GET", server.baseURL+"/v1/diagnosis-progress", nil)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestDiagnosisToolServer_AuthRejectsWrongToken(t *testing.T) {
	server, _, _ := newTestDiagnosisToolServer(t)

	req, _ := http.NewRequest("GET", server.baseURL+"/v1/diagnosis-progress", nil)
	req.Header.Set("Authorization", "Bearer wrong-token")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestDiagnosisToolServer_GetSegmentMessages_ValidPage(t *testing.T) {
	server, _, _ := newTestDiagnosisToolServer(t)

	// Add messages to the pager.
	pager := server.pager.(*fakeDiagnosisMessagePager)
	for i := 1; i <= 5; i++ {
		pager.addMessage(t, int32(i), "assistant", fmt.Sprintf("msg-%d", i))
	}
	require.NoError(t, server.SetSegmentTargets([]DiagnosisSegmentTarget{{
		SegmentID: "seg-1", AgentRunID: "task-test", StartSeq: 1, EndSeq: 5,
		AssistantSeqs: []int32{1, 2, 3, 4, 5},
	}, {
		SegmentID: "seg-2", AgentRunID: "task-test", StartSeq: 1, EndSeq: 1,
		AssistantSeqs: []int32{1},
	}}))

	// Start the segment before fetching.
	_, err := server.stateStore.StartSegment(context.Background(), "run-test", "seg-1", 5, 5)
	require.NoError(t, err)

	reqBody := getSegmentMessagesRequest{SegmentID: "seg-1"}
	resp := doRequest(t, server, "POST", "/v1/get-segment-messages", reqBody, http.StatusOK)

	var page SegmentMessagePage
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&page))
	assert.Equal(t, 5, page.ExpectedCount)
	assert.True(t, page.Complete)
	assert.Len(t, page.Messages, 5)
}

func TestDiagnosisToolServer_GetSegmentMessages_UsesFrozenSegmentRange(t *testing.T) {
	server, _, _ := newTestDiagnosisToolServer(t)
	pager := server.pager.(*fakeDiagnosisMessagePager)
	for i := 1; i <= 7; i++ {
		pager.addMessage(t, int32(i), "assistant", fmt.Sprintf("msg-%d", i))
	}
	require.NoError(t, server.SetSegmentTargets([]DiagnosisSegmentTarget{{
		SegmentID:     "seg-1",
		AgentRunID:    "agent-run-1",
		StartSeq:      2,
		EndSeq:        7,
		AssistantSeqs: []int32{2, 7},
	}, {
		SegmentID: "seg-2", AgentRunID: "agent-run-2", StartSeq: 1, EndSeq: 1,
		AssistantSeqs: []int32{1},
	}}))
	_, err := server.stateStore.StartSegmentWithTargets(
		context.Background(), "run-test", "seg-1", 6, []int32{2, 7},
	)
	require.NoError(t, err)

	resp := doRequest(t, server, "POST", "/v1/get-segment-messages", getSegmentMessagesRequest{SegmentID: "seg-1"}, http.StatusOK)
	var page SegmentMessagePage
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&page))
	require.Len(t, page.Messages, 6)
	assert.Equal(t, int32(2), page.Messages[0].Seq)
	assert.Equal(t, int32(7), page.Messages[len(page.Messages)-1].Seq)

	checkpoint, err := server.stateStore.GetSegment(context.Background(), "run-test", "seg-1")
	require.NoError(t, err)
	assert.Equal(t, 6, checkpoint.FetchedMessageCount)
}

func TestDiagnosisToolServer_GetSegmentMessages_RejectsUnknownSegment(t *testing.T) {
	server, _, _ := newTestDiagnosisToolServer(t)

	reqBody := getSegmentMessagesRequest{SegmentID: "seg-unknown"}
	doRequest(t, server, "POST", "/v1/get-segment-messages", reqBody, http.StatusForbidden)
}

func TestDiagnosisToolServer_RecordStepRewards_Idempotent(t *testing.T) {
	server, _, dagWriter := newTestDiagnosisToolServer(t)

	_, err := server.stateStore.StartSegment(context.Background(), "run-test", "seg-1", 5, 2)
	require.NoError(t, err)

	reqBody := recordStepRewardsRequest{
		SegmentID: "seg-1",
		Rewards: []stepRewardEntry{
			{Seq: 1, Score: 8, Rationale: "good"},
			{Seq: 2, Score: 5, Rationale: "ok"},
		},
	}
	resp := doRequest(t, server, "POST", "/v1/record-step-rewards", reqBody, http.StatusOK)
	var result recordStepRewardsResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
	assert.Equal(t, []int{1, 2}, result.PersistedSeqs)

	// Replay identical rewards — idempotent, no conflict.
	resp2 := doRequest(t, server, "POST", "/v1/record-step-rewards", reqBody, http.StatusOK)
	var result2 recordStepRewardsResponse
	require.NoError(t, json.NewDecoder(resp2.Body).Decode(&result2))
	assert.Equal(t, []int{1, 2}, result2.PersistedSeqs)

	// Verify rewards are persisted in DAG writer.
	score, _, exists, err := dagWriter.GetDiagnosisStepReward(context.Background(), "project-test", "seg-1", 1)
	require.NoError(t, err)
	assert.True(t, exists)
	assert.Equal(t, 8, score)
}

func TestDiagnosisToolServer_RecordStepRewards_RejectsNonTargetSequence(t *testing.T) {
	server, _, dagWriter := newTestDiagnosisToolServer(t)

	_, err := server.stateStore.StartSegmentWithTargets(
		context.Background(), "run-test", "seg-1", 3, []int32{2, 7},
	)
	require.NoError(t, err)

	resp := doRequest(t, server, "POST", "/v1/record-step-rewards", recordStepRewardsRequest{
		SegmentID: "seg-1",
		Rewards: []stepRewardEntry{
			{Seq: 1, Score: 4, Rationale: "not an assistant output"},
			{Seq: 2, Score: 5, Rationale: "first assistant output"},
			{Seq: 7, Score: 6, Rationale: "second assistant output"},
		},
	}, http.StatusOK)
	var result recordStepRewardsResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
	assert.Equal(t, []int{2, 7}, result.PersistedSeqs)
	assert.Equal(t, []rejectedReward{{Seq: 1, Reason: "seq is not an assistant target"}}, result.Rejected)

	_, _, exists, err := dagWriter.GetDiagnosisStepReward(context.Background(), "project-test", "seg-1", 1)
	require.NoError(t, err)
	assert.False(t, exists)
}

func TestDiagnosisToolServer_RecordStepRewards_ConflictingRewrite(t *testing.T) {
	server, _, _ := newTestDiagnosisToolServer(t)

	_, err := server.stateStore.StartSegment(context.Background(), "run-test", "seg-1", 5, 1)
	require.NoError(t, err)

	// First write succeeds.
	reqBody := recordStepRewardsRequest{
		SegmentID: "seg-1",
		Rewards:   []stepRewardEntry{{Seq: 1, Score: 8, Rationale: "first"}},
	}
	doRequest(t, server, "POST", "/v1/record-step-rewards", reqBody, http.StatusOK)

	// Conflicting rewrite is rejected.
	reqBody2 := recordStepRewardsRequest{
		SegmentID: "seg-1",
		Rewards:   []stepRewardEntry{{Seq: 1, Score: 5, Rationale: "changed"}},
	}
	resp := doRequest(t, server, "POST", "/v1/record-step-rewards", reqBody2, http.StatusOK)
	var result recordStepRewardsResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
	assert.Len(t, result.Rejected, 1)
}

func TestDiagnosisToolServer_FinishSegment_RejectsIncomplete(t *testing.T) {
	server, _, _ := newTestDiagnosisToolServer(t)

	_, err := server.stateStore.StartSegment(context.Background(), "run-test", "seg-1", 5, 2)
	require.NoError(t, err)

	// Not all messages fetched, no rewards recorded.
	resp := doRequest(t, server, "POST", "/v1/finish-segment", finishSegmentRequest{SegmentID: "seg-1"}, http.StatusOK)
	var result finishSegmentResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
	assert.Equal(t, "incomplete", result.Status)
}

func TestDiagnosisToolServer_FinishSegment_CompletesWhenCovered(t *testing.T) {
	server, _, dagWriter := newTestDiagnosisToolServer(t)

	_, err := server.stateStore.StartSegment(context.Background(), "run-test", "seg-1", 0, 0)
	require.NoError(t, err)

	// Zero messages and zero rewards expected — completes immediately.
	resp := doRequest(t, server, "POST", "/v1/finish-segment", finishSegmentRequest{SegmentID: "seg-1"}, http.StatusOK)
	var result finishSegmentResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
	assert.Equal(t, "completed", result.Status)
	_ = dagWriter
}

func TestDiagnosisToolServer_DiagnosisProgress_ShowsState(t *testing.T) {
	server, _, _ := newTestDiagnosisToolServer(t)

	resp := doRequest(t, server, "GET", "/v1/diagnosis-progress", nil, http.StatusOK)
	var result diagnosisProgressResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
	assert.Equal(t, "run-test", result.RunID)
	assert.Equal(t, []string{"seg-1", "seg-2"}, result.RemainingSegmentIDs)
	assert.Empty(t, result.CompletedSegmentIDs)
}

func TestDiagnosisToolServer_CompleteDiagnosis_RejectsIncomplete(t *testing.T) {
	server, _, _ := newTestDiagnosisToolServer(t)

	resp := doRequest(t, server, "POST", "/v1/complete-diagnosis", nil, http.StatusOK)
	var result completeDiagnosisResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
	assert.Equal(t, "incomplete", result.Status)
	assert.NotEmpty(t, result.MissingSegs)
}

func TestDiagnosisToolServer_CompleteDiagnosis_FullRun(t *testing.T) {
	server, _, _ := newTestDiagnosisToolServer(t)

	// Complete both segments.
	for _, segID := range []string{"seg-1", "seg-2"} {
		_, err := server.stateStore.StartSegment(context.Background(), "run-test", segID, 0, 0)
		require.NoError(t, err)
		require.NoError(t, server.stateStore.CompleteSegment(context.Background(), "run-test", segID))
	}

	resp := doRequest(t, server, "POST", "/v1/complete-diagnosis", nil, http.StatusOK)
	var result completeDiagnosisResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
	assert.Equal(t, "completed", result.Status)
}

func TestDiagnosisToolServer_BodySizeCap(t *testing.T) {
	server, _, _ := newTestDiagnosisToolServer(t)

	// Large body beyond the 256 KiB cap should be rejected.
	largePayload := make([]byte, diagnosisToolServerMaxBody+1024)
	for i := range largePayload {
		largePayload[i] = 'x'
	}
	req, _ := http.NewRequest("POST", server.baseURL+"/v1/record-step-rewards", bytes.NewReader(largePayload))
	req.Header.Set("Authorization", authHeader(server))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	// Either 400 (bad body) or 413 (too large) depending on where it's caught.
	assert.True(t, resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusRequestEntityTooLarge)
}
