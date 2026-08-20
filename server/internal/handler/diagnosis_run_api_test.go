// SPDX-License-Identifier: Apache-2.0

package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// ── Fakes (no live DB; mirror diagnosis_tool_server_test.go patterns) ──

// fakeDiagnosisRunAPIStore is an in-memory service.DiagnosisRunAPIStore that
// mirrors the SQL compare-and-set predicates of the real store.
type fakeDiagnosisRunAPIStore struct {
	mu       sync.Mutex
	segments map[string]service.SegmentDiagnosisCheckpoint // runID/segmentID
}

func newFakeDiagnosisRunAPIStore() *fakeDiagnosisRunAPIStore {
	return &fakeDiagnosisRunAPIStore{segments: map[string]service.SegmentDiagnosisCheckpoint{}}
}

func (f *fakeDiagnosisRunAPIStore) addSegment(seg service.SegmentDiagnosisCheckpoint) {
	f.segments[seg.RunID+"/"+seg.SegmentID] = seg
}

func (f *fakeDiagnosisRunAPIStore) GetSegment(_ context.Context, runID, segmentID string) (service.SegmentDiagnosisCheckpoint, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	seg, ok := f.segments[runID+"/"+segmentID]
	if !ok {
		return service.SegmentDiagnosisCheckpoint{}, fmt.Errorf("%w: segment %s for run %s", service.ErrDiagnosisRunNotFound, segmentID, runID)
	}
	return seg, nil
}

func (f *fakeDiagnosisRunAPIStore) ListSegments(_ context.Context, runID string) ([]service.SegmentDiagnosisCheckpoint, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []service.SegmentDiagnosisCheckpoint
	for _, seg := range f.segments {
		if seg.RunID == runID {
			out = append(out, seg)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Ordinal < out[j].Ordinal })
	return out, nil
}

func (f *fakeDiagnosisRunAPIStore) RecordSegmentPage(_ context.Context, runID, segmentID, prevCursor, nextCursor string, fetchedTotal int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := runID + "/" + segmentID
	seg, ok := f.segments[key]
	if !ok {
		return fmt.Errorf("%w: segment %s", service.ErrDiagnosisRunNotFound, segmentID)
	}
	if seg.NextCursor != prevCursor {
		return fmt.Errorf("%w: segment %s", service.ErrDiagnosisStaleCursor, segmentID)
	}
	seg.NextCursor = nextCursor
	seg.FetchedMessageCount = fetchedTotal
	f.segments[key] = seg
	return nil
}

func (f *fakeDiagnosisRunAPIStore) RecordSegmentRewards(_ context.Context, runID, segmentID string, rewardCount int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := runID + "/" + segmentID
	seg, ok := f.segments[key]
	if !ok {
		return fmt.Errorf("%w: segment %s", service.ErrDiagnosisRunNotFound, segmentID)
	}
	seg.RewardCount = rewardCount
	f.segments[key] = seg
	return nil
}

func (f *fakeDiagnosisRunAPIStore) CompleteSegment(_ context.Context, runID, segmentID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := runID + "/" + segmentID
	seg, ok := f.segments[key]
	if !ok {
		return fmt.Errorf("%w: segment %s", service.ErrDiagnosisRunNotFound, segmentID)
	}
	if seg.Status == service.SegmentDiagnosisCompleted {
		return nil
	}
	if seg.FetchedMessageCount < seg.ExpectedMessageCount {
		return fmt.Errorf("%w: segment %s", service.ErrDiagnosisIncompleteMessages, segmentID)
	}
	if seg.RewardCount < seg.ExpectedRewardCount {
		return fmt.Errorf("%w: segment %s", service.ErrDiagnosisIncompleteRewards, segmentID)
	}
	seg.Status = service.SegmentDiagnosisCompleted
	f.segments[key] = seg
	return nil
}

func (f *fakeDiagnosisRunAPIStore) CompleteRun(_ context.Context, runID, topologyHash string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if topologyHash != "topo-1" {
		return fmt.Errorf("%w: run %s", service.ErrDiagnosisTopologyMismatch, runID)
	}
	for _, seg := range f.segments {
		if seg.RunID == runID && seg.Status != service.SegmentDiagnosisCompleted {
			return fmt.Errorf("%w: segment %s is %s", service.ErrDiagnosisInvalidTransition, seg.SegmentID, seg.Status)
		}
	}
	return nil
}

// fakeDiagnosisRunPager is an in-memory service.DiagnosisMessagePager with
// keyset pagination over (seq, id), mirroring fakeDiagnosisMessagePager.
type fakeDiagnosisRunPager struct {
	messages []db.TaskMessage
}

func (f *fakeDiagnosisRunPager) addMessage(seq int32, typ, content string) {
	var id pgtype.UUID
	_ = id.Scan(fmt.Sprintf("00000000-0000-0000-0000-%012d", seq))
	f.messages = append(f.messages, db.TaskMessage{
		ID:      id,
		Seq:     seq,
		Type:    typ,
		Content: pgtype.Text{String: content, Valid: true},
	})
}

func (f *fakeDiagnosisRunPager) PageTaskMessagesInRange(_ context.Context, arg db.PageTaskMessagesInRangeParams) ([]db.TaskMessage, error) {
	var result []db.TaskMessage
	for _, m := range f.messages {
		if m.Seq < arg.StartSeq || m.Seq > arg.EndSeq {
			continue
		}
		if m.Seq > arg.LastSeq {
			result = append(result, m)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Seq < result[j].Seq })
	if len(result) > int(arg.PageLimit) {
		result = result[:arg.PageLimit]
	}
	return result, nil
}

func (f *fakeDiagnosisRunPager) CountTaskMessagesInRange(_ context.Context, arg db.CountTaskMessagesInRangeParams) (int32, error) {
	var count int32
	for _, m := range f.messages {
		if m.Seq >= arg.StartSeq && m.Seq <= arg.EndSeq {
			count++
		}
	}
	return count, nil
}

// fakeDiagnosisRunDAGWriter is an in-memory service.DiagnosisDAGWriter.
type fakeDiagnosisRunDAGWriter struct {
	mu      sync.Mutex
	rewards map[string]db.InsertInteractionDAGStepRewardParams // segID:seq
}

func newFakeDiagnosisRunDAGWriter() *fakeDiagnosisRunDAGWriter {
	return &fakeDiagnosisRunDAGWriter{rewards: map[string]db.InsertInteractionDAGStepRewardParams{}}
}

func (f *fakeDiagnosisRunDAGWriter) UpsertDiagnosisStepReward(_ context.Context, _, segmentID string, seq int32, score int, rationale string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rewards[fmt.Sprintf("%s:%d", segmentID, seq)] = db.InsertInteractionDAGStepRewardParams{
		SegmentID: segmentID, Seq: seq, Score: int32(score), Rationale: rationale,
	}
	return nil
}

func (f *fakeDiagnosisRunDAGWriter) GetDiagnosisStepReward(_ context.Context, _, segmentID string, seq int32) (int, string, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.rewards[fmt.Sprintf("%s:%d", segmentID, seq)]
	if !ok {
		return 0, "", false, nil
	}
	return int(r.Score), r.Rationale, true, nil
}

func (f *fakeDiagnosisRunDAGWriter) CountDiagnosisStepRewards(_ context.Context, _, segmentID string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	count := 0
	for k := range f.rewards {
		if strings.HasPrefix(k, segmentID+":") {
			count++
		}
	}
	return count, nil
}

// fakeDiagnosisRunSegmentLookup is an in-memory diagnosisRunSegmentLookup.
type fakeDiagnosisRunSegmentLookup struct {
	segments map[string]db.GetInteractionDAGSegmentByIDRow
}

func (f *fakeDiagnosisRunSegmentLookup) GetInteractionDAGSegmentByID(_ context.Context, segmentID string) (db.GetInteractionDAGSegmentByIDRow, error) {
	seg, ok := f.segments[segmentID]
	if !ok {
		return db.GetInteractionDAGSegmentByIDRow{}, pgx.ErrNoRows
	}
	return seg, nil
}

// ── Harness ──

type diagnosisRunAPITestEnv struct {
	deps      diagnosisRunAPIDeps
	store     *fakeDiagnosisRunAPIStore
	pager     *fakeDiagnosisRunPager
	dagWriter *fakeDiagnosisRunDAGWriter
	run       middleware.DiagnosisRun
}

func newDiagnosisRunAPITestEnv(t *testing.T) *diagnosisRunAPITestEnv {
	t.Helper()
	store := newFakeDiagnosisRunAPIStore()
	pager := &fakeDiagnosisRunPager{}
	dagWriter := newFakeDiagnosisRunDAGWriter()
	segments := &fakeDiagnosisRunSegmentLookup{segments: map[string]db.GetInteractionDAGSegmentByIDRow{
		"seg-1": {SegmentID: "seg-1", ProjectID: "project-1", AgentRunID: "task-1", StartSeq: 1, EndSeq: 5},
		"seg-2": {SegmentID: "seg-2", ProjectID: "project-1", AgentRunID: "task-1", StartSeq: 1, EndSeq: 1},
	}}
	run := middleware.DiagnosisRun{
		RunID:               "run-1",
		ProjectID:           "project-1",
		TaskID:              "task-1",
		TopologyHash:        "topo-1",
		OrderedSegmentIDs:   []string{"seg-1", "seg-2"},
		Status:              "running",
		CapabilityTokenHash: "hash-1",
		ExecutionMode:       "sandbox",
	}
	env := &diagnosisRunAPITestEnv{
		store:     store,
		pager:     pager,
		dagWriter: dagWriter,
		run:       run,
	}
	env.deps = diagnosisRunAPIDeps{
		state:     store,
		pager:     pager,
		dagWriter: dagWriter,
		segments:  segments,
		taskContextFn: func(context.Context, service.DiagnosisRunCheckpoint) (service.TaskContext, error) {
			return service.TaskContext{Goal: "fix the bug", GoldContext: `{"acceptance":"tests pass"}`}, nil
		},
	}
	return env
}

func (env *diagnosisRunAPITestEnv) do(t *testing.T, handler http.HandlerFunc, method, path string, body any, expectedStatus int) *httptest.ResponseRecorder {
	t.Helper()
	var bodyBytes []byte
	if body != nil {
		var err error
		bodyBytes, err = json.Marshal(body)
		require.NoError(t, err)
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(bodyBytes))
	req = req.WithContext(middleware.WithDiagnosisRun(req.Context(), env.run))
	w := httptest.NewRecorder()
	handler(w, req)
	if expectedStatus != 0 {
		assert.Equal(t, expectedStatus, w.Code, "unexpected status for %s %s: %s", method, path, w.Body.String())
	}
	return w
}

// ── get-segment-messages ──

func TestDiagnosisRunAPI_GetSegmentMessages_ValidPage(t *testing.T) {
	env := newDiagnosisRunAPITestEnv(t)
	for i := 1; i <= 5; i++ {
		env.pager.addMessage(int32(i), "assistant", fmt.Sprintf("msg-%d", i))
	}
	env.store.addSegment(service.SegmentDiagnosisCheckpoint{
		RunID: "run-1", SegmentID: "seg-1", Status: service.SegmentDiagnosisInProgress,
		ExpectedMessageCount: 5, ExpectedRewardSeqs: []int32{1, 2, 3, 4, 5}, ExpectedRewardCount: 5,
	})

	w := env.do(t, env.deps.getSegmentMessages, http.MethodPost, "/get-segment-messages",
		map[string]any{"segment_id": "seg-1"}, http.StatusOK)

	var page service.SegmentMessagePage
	require.NoError(t, json.NewDecoder(w.Body).Decode(&page))
	assert.Equal(t, 5, page.ExpectedCount)
	assert.True(t, page.Complete)
	assert.Len(t, page.Messages, 5)

	seg, err := env.store.GetSegment(context.Background(), "run-1", "seg-1")
	require.NoError(t, err)
	assert.Equal(t, 5, seg.FetchedMessageCount)
}

func TestDiagnosisRunAPI_GetSegmentMessages_RejectsUnknownSegment(t *testing.T) {
	env := newDiagnosisRunAPITestEnv(t)

	w := env.do(t, env.deps.getSegmentMessages, http.MethodPost, "/get-segment-messages",
		map[string]any{"segment_id": "seg-unknown"}, http.StatusNotFound)
	assert.Contains(t, w.Body.String(), "unknown_segment")
}

func TestDiagnosisRunAPI_GetSegmentMessages_StaleCursor(t *testing.T) {
	env := newDiagnosisRunAPITestEnv(t)
	env.pager.addMessage(1, "assistant", "msg-1")
	env.store.addSegment(service.SegmentDiagnosisCheckpoint{
		RunID: "run-1", SegmentID: "seg-1", Status: service.SegmentDiagnosisInProgress,
		ExpectedMessageCount: 1, NextCursor: "cursor-held-by-another-caller",
	})

	// The request carries the empty first-page cursor but the store has
	// already advanced: CAS rejects the replay.
	w := env.do(t, env.deps.getSegmentMessages, http.MethodPost, "/get-segment-messages",
		map[string]any{"segment_id": "seg-1"}, http.StatusConflict)
	assert.Contains(t, w.Body.String(), "stale_cursor")
}

// ── record-step-rewards ──

func TestDiagnosisRunAPI_RecordStepRewards_Idempotent(t *testing.T) {
	env := newDiagnosisRunAPITestEnv(t)
	env.store.addSegment(service.SegmentDiagnosisCheckpoint{
		RunID: "run-1", SegmentID: "seg-1", Status: service.SegmentDiagnosisInProgress,
		ExpectedRewardSeqs: []int32{1, 2}, ExpectedRewardCount: 2,
	})

	body := map[string]any{"segment_id": "seg-1", "rewards": []map[string]any{
		{"seq": 1, "score": 8, "rationale": "good"},
		{"seq": 2, "score": 5, "rationale": "ok"},
	}}
	w := env.do(t, env.deps.recordStepRewards, http.MethodPost, "/record-step-rewards", body, http.StatusOK)
	var result diagnosisRunRecordStepRewardsResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&result))
	assert.Equal(t, []int{1, 2}, result.PersistedSeqs)

	// Identical replay is idempotent.
	w = env.do(t, env.deps.recordStepRewards, http.MethodPost, "/record-step-rewards", body, http.StatusOK)
	require.NoError(t, json.NewDecoder(w.Body).Decode(&result))
	assert.Equal(t, []int{1, 2}, result.PersistedSeqs)
	assert.Empty(t, result.Rejected)

	score, _, exists, err := env.dagWriter.GetDiagnosisStepReward(context.Background(), "project-1", "seg-1", 1)
	require.NoError(t, err)
	assert.True(t, exists)
	assert.Equal(t, 8, score)
}

func TestDiagnosisRunAPI_RecordStepRewards_ConflictingRewrite(t *testing.T) {
	env := newDiagnosisRunAPITestEnv(t)
	env.store.addSegment(service.SegmentDiagnosisCheckpoint{
		RunID: "run-1", SegmentID: "seg-1", Status: service.SegmentDiagnosisInProgress,
		ExpectedRewardSeqs: []int32{1}, ExpectedRewardCount: 1,
	})

	env.do(t, env.deps.recordStepRewards, http.MethodPost, "/record-step-rewards",
		map[string]any{"segment_id": "seg-1", "rewards": []map[string]any{{"seq": 1, "score": 8, "rationale": "first"}}}, http.StatusOK)

	w := env.do(t, env.deps.recordStepRewards, http.MethodPost, "/record-step-rewards",
		map[string]any{"segment_id": "seg-1", "rewards": []map[string]any{{"seq": 1, "score": 5, "rationale": "changed"}}}, http.StatusOK)
	var result diagnosisRunRecordStepRewardsResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&result))
	require.Len(t, result.Rejected, 1)
	assert.Contains(t, result.Rejected[0].Reason, "conflicting rewrite")
}

func TestDiagnosisRunAPI_RecordStepRewards_RejectsNonTargetSequence(t *testing.T) {
	env := newDiagnosisRunAPITestEnv(t)
	env.store.addSegment(service.SegmentDiagnosisCheckpoint{
		RunID: "run-1", SegmentID: "seg-1", Status: service.SegmentDiagnosisInProgress,
		ExpectedRewardSeqs: []int32{2, 7}, ExpectedRewardCount: 2,
	})

	w := env.do(t, env.deps.recordStepRewards, http.MethodPost, "/record-step-rewards",
		map[string]any{"segment_id": "seg-1", "rewards": []map[string]any{
			{"seq": 1, "score": 4, "rationale": "not an assistant output"},
			{"seq": 2, "score": 5, "rationale": "first assistant output"},
			{"seq": 7, "score": 6, "rationale": "second assistant output"},
		}}, http.StatusOK)
	var result diagnosisRunRecordStepRewardsResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&result))
	assert.Equal(t, []int{2, 7}, result.PersistedSeqs)
	assert.Equal(t, []diagnosisRunRejectedReward{{Seq: 1, Reason: "seq is not an assistant target"}}, result.Rejected)
}

func TestDiagnosisRunAPI_RecordStepRewards_RejectsUnknownSegment(t *testing.T) {
	env := newDiagnosisRunAPITestEnv(t)

	w := env.do(t, env.deps.recordStepRewards, http.MethodPost, "/record-step-rewards",
		map[string]any{"segment_id": "seg-unknown", "rewards": []map[string]any{{"seq": 1, "score": 1}}}, http.StatusNotFound)
	assert.Contains(t, w.Body.String(), "unknown_segment")
}

func TestDiagnosisRunAPI_RecordStepRewards_BodySizeCap(t *testing.T) {
	env := newDiagnosisRunAPITestEnv(t)
	env.store.addSegment(service.SegmentDiagnosisCheckpoint{
		RunID: "run-1", SegmentID: "seg-1", Status: service.SegmentDiagnosisInProgress,
	})

	largePayload := bytes.Repeat([]byte("x"), diagnosisRunAPIMaxBody+1024)
	req := httptest.NewRequest(http.MethodPost, "/record-step-rewards", bytes.NewReader(largePayload))
	req = req.WithContext(middleware.WithDiagnosisRun(req.Context(), env.run))
	w := httptest.NewRecorder()
	env.deps.recordStepRewards(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// ── diagnosis-progress ──

func TestDiagnosisRunAPI_DiagnosisProgress_ShowsSegments(t *testing.T) {
	env := newDiagnosisRunAPITestEnv(t)
	env.store.addSegment(service.SegmentDiagnosisCheckpoint{
		RunID: "run-1", SegmentID: "seg-1", Ordinal: 0, Status: service.SegmentDiagnosisCompleted,
		ExpectedMessageCount: 5, FetchedMessageCount: 5, ExpectedRewardCount: 2, RewardCount: 2,
	})
	env.store.addSegment(service.SegmentDiagnosisCheckpoint{
		RunID: "run-1", SegmentID: "seg-2", Ordinal: 1, Status: service.SegmentDiagnosisInProgress,
		ExpectedMessageCount: 3, FetchedMessageCount: 1, ExpectedRewardCount: 1,
	})

	w := env.do(t, env.deps.diagnosisProgress, http.MethodGet, "/diagnosis-progress", nil, http.StatusOK)
	var progress service.DiagnosisRunProgress
	require.NoError(t, json.NewDecoder(w.Body).Decode(&progress))
	assert.Equal(t, "run-1", progress.RunID)
	assert.Equal(t, service.DiagnosisRunRunning, progress.Status)
	require.Len(t, progress.Segments, 2)
	assert.Equal(t, "seg-1", progress.Segments[0].SegmentID)
	assert.Equal(t, "completed", progress.Segments[0].Status)
	assert.Equal(t, 5, progress.Segments[0].FetchedMessageCount)
	assert.Equal(t, 6, progress.FetchedMessageCount)
	assert.Equal(t, 8, progress.ExpectedMessageCount)
	assert.Equal(t, 2, progress.RecordedRewardCount)
	assert.Equal(t, 3, progress.ExpectedRewardCount)
}

// ── finish-segment ──

func TestDiagnosisRunAPI_FinishSegment_RejectsIncomplete(t *testing.T) {
	env := newDiagnosisRunAPITestEnv(t)
	env.store.addSegment(service.SegmentDiagnosisCheckpoint{
		RunID: "run-1", SegmentID: "seg-1", Status: service.SegmentDiagnosisInProgress,
		ExpectedMessageCount: 5, FetchedMessageCount: 2, ExpectedRewardCount: 2,
	})

	w := env.do(t, env.deps.finishSegment, http.MethodPost, "/finish-segment",
		map[string]any{"segment_id": "seg-1"}, http.StatusOK)
	var result diagnosisRunFinishSegmentResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&result))
	assert.False(t, result.Completed)
	require.Len(t, result.Incomplete, 2)
	assert.Equal(t, "missing_messages", result.Incomplete[0].Code)
	assert.Equal(t, "missing_rewards", result.Incomplete[1].Code)
}

func TestDiagnosisRunAPI_FinishSegment_CompletesWhenCovered(t *testing.T) {
	env := newDiagnosisRunAPITestEnv(t)
	env.store.addSegment(service.SegmentDiagnosisCheckpoint{
		RunID: "run-1", SegmentID: "seg-1", Status: service.SegmentDiagnosisInProgress,
	})

	w := env.do(t, env.deps.finishSegment, http.MethodPost, "/finish-segment",
		map[string]any{"segment_id": "seg-1"}, http.StatusOK)
	var result diagnosisRunFinishSegmentResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&result))
	assert.True(t, result.Completed)
}

// ── complete-diagnosis ──

func TestDiagnosisRunAPI_CompleteDiagnosis_ConflictWithIncompleteSegments(t *testing.T) {
	env := newDiagnosisRunAPITestEnv(t)
	env.store.addSegment(service.SegmentDiagnosisCheckpoint{
		RunID: "run-1", SegmentID: "seg-1", Ordinal: 0, Status: service.SegmentDiagnosisCompleted,
	})
	env.store.addSegment(service.SegmentDiagnosisCheckpoint{
		RunID: "run-1", SegmentID: "seg-2", Ordinal: 1, Status: service.SegmentDiagnosisInProgress,
		ExpectedMessageCount: 3, FetchedMessageCount: 1,
	})

	w := env.do(t, env.deps.completeDiagnosis, http.MethodPost, "/complete-diagnosis", nil, http.StatusConflict)
	var result diagnosisRunCompleteResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&result))
	assert.Equal(t, "running", result.Status)
	require.Len(t, result.IncompleteSegments, 1)
	assert.Equal(t, "seg-2", result.IncompleteSegments[0].SegmentID)
	assert.Equal(t, []string{"message coverage gap"}, result.IncompleteSegments[0].Reasons)
}

func TestDiagnosisRunAPI_CompleteDiagnosis_FullRun(t *testing.T) {
	env := newDiagnosisRunAPITestEnv(t)
	env.store.addSegment(service.SegmentDiagnosisCheckpoint{
		RunID: "run-1", SegmentID: "seg-1", Ordinal: 0, Status: service.SegmentDiagnosisCompleted,
	})
	env.store.addSegment(service.SegmentDiagnosisCheckpoint{
		RunID: "run-1", SegmentID: "seg-2", Ordinal: 1, Status: service.SegmentDiagnosisCompleted,
	})

	w := env.do(t, env.deps.completeDiagnosis, http.MethodPost, "/complete-diagnosis", nil, http.StatusOK)
	var result diagnosisRunCompleteResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&result))
	assert.Equal(t, "completed", result.Status)
}

// ── task-context ──

func TestDiagnosisRunAPI_TaskContext_ReturnsGoalAndGold(t *testing.T) {
	env := newDiagnosisRunAPITestEnv(t)

	w := env.do(t, env.deps.taskContext, http.MethodGet, "/task-context", nil, http.StatusOK)
	var result diagnosisRunTaskContextResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&result))
	assert.Equal(t, "fix the bug", result.Goal)
	assert.False(t, result.GoalTruncated)
	assert.JSONEq(t, `{"acceptance":"tests pass"}`, result.GoldContext)
	assert.False(t, result.GoldContextTruncated)
}

func TestDiagnosisRunAPI_TaskContext_TruncatesLongFields(t *testing.T) {
	env := newDiagnosisRunAPITestEnv(t)
	env.deps.taskContextFn = func(context.Context, service.DiagnosisRunCheckpoint) (service.TaskContext, error) {
		return service.TaskContext{
			Goal:        strings.Repeat("g", 10*1024),
			GoldContext: strings.Repeat("x", 9*1024),
		}, nil
	}

	w := env.do(t, env.deps.taskContext, http.MethodGet, "/task-context", nil, http.StatusOK)
	var result diagnosisRunTaskContextResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&result))
	assert.Len(t, result.Goal, 8*1024)
	assert.True(t, result.GoalTruncated)
	assert.Len(t, result.GoldContext, 8*1024)
	assert.True(t, result.GoldContextTruncated)
}

func TestDiagnosisRunAPI_TaskContext_NotFound(t *testing.T) {
	env := newDiagnosisRunAPITestEnv(t)
	env.deps.taskContextFn = func(context.Context, service.DiagnosisRunCheckpoint) (service.TaskContext, error) {
		return service.TaskContext{}, pgx.ErrNoRows
	}

	w := env.do(t, env.deps.taskContext, http.MethodGet, "/task-context", nil, http.StatusNotFound)
	assert.Contains(t, w.Body.String(), "task_context_not_found")
}
