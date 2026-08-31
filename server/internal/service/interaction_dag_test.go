// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

var (
	interactionDAGRunID1 = util.UUIDToString(testUUID(91))
	interactionDAGRunID2 = util.UUIDToString(testUUID(92))
)

// fakeMessageStore is an in-memory MessageStore for unit tests.
type fakeMessageStore struct {
	mu       sync.Mutex
	messages map[string][]db.TaskMessage // taskID -> messages
}

func newFakeMessageStore() *fakeMessageStore {
	return &fakeMessageStore{messages: map[string][]db.TaskMessage{}}
}

func (f *fakeMessageStore) MessagesForTaskInRange(_ context.Context, arg db.MessagesForTaskInRangeParams) ([]db.TaskMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	msgs := f.messages[arg.TaskID]
	var out []db.TaskMessage
	for _, m := range msgs {
		if m.Seq >= arg.StartSeq && m.Seq <= arg.EndSeq {
			out = append(out, m)
		}
	}
	return out, nil
}

func (f *fakeMessageStore) addTaskMessage(taskID string, msg db.TaskMessage) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.messages[taskID] = append(f.messages[taskID], msg)
}

func (f *fakeMessageStore) GetProjectInWorkspace(_ context.Context, arg db.GetProjectInWorkspaceParams) (db.Project, error) {
	return db.Project{}, errors.New("not implemented in fake")
}

func (f *fakeMessageStore) GetIssueForTask(_ context.Context, taskID string) (db.Issue, error) {
	return db.Issue{}, errors.New("not implemented in fake")
}

// Compile-time interface compliance check.
var _ MessageStore = (*fakeMessageStore)(nil)

// fakeInteractionDAGStore is an in-memory InteractionDAGStore for unit tests.
type fakeInteractionDAGStore struct {
	mu                   sync.Mutex
	sessionRuns          map[string]db.InteractionDagSessionRun
	segmentSnapshots     []db.InsertInteractionDAGSegmentWithSnapshotParams
	edges                []db.InsertUniversalDAGEdgeParams
	edgeTriggerMessageID pgtype.UUID
	nextEdgeSeq          int64
	taskMessages         map[string][]int32 // taskID -> list of seq numbers
	stepRewards          []db.InteractionDagStepReward
	diagnosisTargetSeqs  map[string][]int32
	// order, when non-nil, records cross-helper call ordering by appending
	// "RecordStepRewards" on each InsertInteractionDAGStepReward. nil-safe
	// (the default) so existing tests are unaffected. Used by the Task 4
	// diagnosis-before-close-hook ordering test.
	order *[]string

	upsertErr                error
	getSessionRunErr         error
	insertSegmentSnapshotErr error
	insertEdgeErr            error
}

func newFakeInteractionDAGStore() *fakeInteractionDAGStore {
	return &fakeInteractionDAGStore{
		sessionRuns:          map[string]db.InteractionDagSessionRun{},
		taskMessages:         map[string][]int32{},
		stepRewards:          []db.InteractionDagStepReward{},
		diagnosisTargetSeqs:  map[string][]int32{},
		nextEdgeSeq:          1,
		edgeTriggerMessageID: testUUID(99),
	}
}

func (f *fakeInteractionDAGStore) UpsertInteractionDAGSessionRun(_ context.Context, arg db.UpsertInteractionDAGSessionRunParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.upsertErr != nil {
		return f.upsertErr
	}
	f.sessionRuns[arg.SessionID] = db.InteractionDagSessionRun{
		SessionID:  arg.SessionID,
		ProjectID:  arg.ProjectID,
		AgentRunID: arg.AgentRunID,
		IssueID:    arg.IssueID,
	}
	return nil
}

func (f *fakeInteractionDAGStore) GetInteractionDAGSessionRun(_ context.Context, sessionID string) (db.InteractionDagSessionRun, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.getSessionRunErr != nil {
		return db.InteractionDagSessionRun{}, f.getSessionRunErr
	}
	row, ok := f.sessionRuns[sessionID]
	if !ok {
		return db.InteractionDagSessionRun{}, pgx.ErrNoRows
	}
	return row, nil
}

func (f *fakeInteractionDAGStore) InsertInteractionDAGSegmentWithSnapshot(_ context.Context, arg db.InsertInteractionDAGSegmentWithSnapshotParams) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.insertSegmentSnapshotErr != nil {
		return "", f.insertSegmentSnapshotErr
	}
	f.segmentSnapshots = append(f.segmentSnapshots, arg)
	return arg.SegmentID, nil
}

// GetLastEndSeqForAgentRun implements InteractionDAGStore.
func (f *fakeInteractionDAGStore) GetLastEndSeqForAgentRun(_ context.Context, agentRunID pgtype.UUID) (int32, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var lastEndSeq int32 = 0
	for _, seg := range f.segmentSnapshots {
		if seg.AgentRunID == agentRunID && seg.EndSeq > lastEndSeq {
			lastEndSeq = seg.EndSeq
		}
	}
	return lastEndSeq, nil
}

// GetMaxTaskMessageSeq implements InteractionDAGStore.
func (f *fakeInteractionDAGStore) GetMaxTaskMessageSeq(_ context.Context, taskIDText string) (int32, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var maxSeq int32 = 0
	if seqs, ok := f.taskMessages[taskIDText]; ok {
		for _, seq := range seqs {
			if seq > maxSeq {
				maxSeq = seq
			}
		}
	}
	return maxSeq, nil
}

// addTestTaskMessage adds a task message seq for testing.
func (f *fakeInteractionDAGStore) addTestTaskMessage(taskIDText string, seq int32) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.taskMessages[taskIDText] = append(f.taskMessages[taskIDText], seq)
}

func (f *fakeInteractionDAGStore) AllocateUniversalDAGEdgeSeq(_ context.Context, _ pgtype.UUID) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	seq := f.nextEdgeSeq
	f.nextEdgeSeq++
	return seq, nil
}

func (f *fakeInteractionDAGStore) GetUniversalDAGEdgeTriggerMessageID(_ context.Context, _ db.GetUniversalDAGEdgeTriggerMessageIDParams) (pgtype.UUID, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.edgeTriggerMessageID.Valid {
		return pgtype.UUID{}, pgx.ErrNoRows
	}
	return f.edgeTriggerMessageID, nil
}

func (f *fakeInteractionDAGStore) InsertUniversalDAGEdge(_ context.Context, arg db.InsertUniversalDAGEdgeParams) (db.InteractionDagEdge, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.insertEdgeErr != nil {
		return db.InteractionDagEdge{}, f.insertEdgeErr
	}
	f.edges = append(f.edges, arg)
	return db.InteractionDagEdge{
		WorkspaceID: arg.WorkspaceID, EdgeSeq: arg.EdgeSeq, SrcSegmentID: arg.SrcSegmentID,
		DstSegmentID: arg.DstSegmentID, Type: arg.EdgeType, TriggerMessageID: arg.TriggerMessageID,
	}, nil
}

func (f *fakeInteractionDAGStore) InsertUniversalDAGEdgeAtomic(_ context.Context, arg db.InsertUniversalDAGEdgeAtomicParams) (db.InteractionDagEdge, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.insertEdgeErr != nil {
		return db.InteractionDagEdge{}, f.insertEdgeErr
	}
	seq := f.nextEdgeSeq
	f.nextEdgeSeq++
	trigger := pgtype.UUID{}
	if arg.EdgeType != EdgeTypeContinues {
		if !f.edgeTriggerMessageID.Valid {
			return db.InteractionDagEdge{}, pgx.ErrNoRows
		}
		trigger = f.edgeTriggerMessageID
	}
	stored := db.InsertUniversalDAGEdgeParams{
		WorkspaceID: arg.WorkspaceID, EdgeSeq: seq, SrcSegmentID: arg.SrcSegmentID,
		DstSegmentID: arg.DstSegmentID, EdgeType: arg.EdgeType, TriggerMessageID: trigger,
	}
	f.edges = append(f.edges, stored)
	return db.InteractionDagEdge{
		WorkspaceID: arg.WorkspaceID, EdgeSeq: seq, SrcSegmentID: arg.SrcSegmentID,
		DstSegmentID: arg.DstSegmentID, Type: arg.EdgeType, TriggerMessageID: trigger,
	}, nil
}

// GetInteractionDAGSegmentByAgentRun satisfies InteractionDAGStore. Returns the
// latest segment recorded for the run (one-segment-per-task in change 1; the
// reverse scan keeps this stable under a future multi-segment model, mirroring
// the real query's ORDER BY created_at DESC LIMIT 1).
func (f *fakeInteractionDAGStore) GetInteractionDAGSegmentByAgentRun(_ context.Context, agentRunID pgtype.UUID) (db.GetInteractionDAGSegmentByAgentRunRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.segmentSnapshots) - 1; i >= 0; i-- {
		s := f.segmentSnapshots[i]
		if s.AgentRunID == agentRunID {
			return db.GetInteractionDAGSegmentByAgentRunRow{
				SegmentID:                 s.SegmentID,
				ProjectID:                 pgText(s.ProjectID),
				AgentRunID:                s.AgentRunID,
				IssueID:                   s.IssueID,
				TaskID:                    s.TaskID,
				TrajectoryID:              s.TrajectoryID.Int64,
				TensorRef:                 s.TensorRef,
				ClosingEvent:              s.ClosingEvent,
				ClosingEventTargetSegment: s.ClosingEventTargetSegment,
				StartSeq:                  s.StartSeq,
				EndSeq:                    s.EndSeq,
				WorkspaceID:               testUUID(70),
				ProjectIDAtEvent:          testUUID(80),
				ContentStatus:             "published",
				TrainableEligible:         true,
			}, nil
		}
	}
	return db.GetInteractionDAGSegmentByAgentRunRow{}, pgx.ErrNoRows
}

// GetInteractionDAGSegmentByID satisfies InteractionDAGStore. Returns the segment
// with the given segment_id, or pgx.ErrNoRows if not found.
func (f *fakeInteractionDAGStore) GetInteractionDAGSegmentByID(_ context.Context, segmentID string) (db.GetInteractionDAGSegmentByIDRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, s := range f.segmentSnapshots {
		if s.SegmentID == segmentID {
			return db.GetInteractionDAGSegmentByIDRow{
				SegmentID:                 s.SegmentID,
				ProjectID:                 pgText(s.ProjectID),
				AgentRunID:                s.AgentRunID,
				IssueID:                   s.IssueID,
				TaskID:                    s.TaskID,
				TrajectoryID:              s.TrajectoryID.Int64,
				TensorRef:                 s.TensorRef,
				ClosingEvent:              s.ClosingEvent,
				ClosingEventTargetSegment: s.ClosingEventTargetSegment,
				StartSeq:                  s.StartSeq,
				EndSeq:                    s.EndSeq,
				WorkspaceID:               testUUID(70),
				ProjectIDAtEvent:          testUUID(80),
				ContentStatus:             "published",
				TrainableEligible:         true,
			}, nil
		}
	}
	return db.GetInteractionDAGSegmentByIDRow{}, pgx.ErrNoRows
}

func (f *fakeInteractionDAGStore) GetUniversalDAGProjectWorkspace(_ context.Context, _ string) (pgtype.UUID, error) {
	return testUUID(70), nil
}

// ListInteractionDAGSegmentsForProject satisfies InteractionDAGStore (U8
// assembly). Returns segments recorded for projectID, ordered by insertion
// order (the fake appends; the real query orders by created_at). Converts the
// stored insert params to InteractionDagSegment row structs.
func (f *fakeInteractionDAGStore) ListInteractionDAGSegmentsForProject(_ context.Context, arg db.ListInteractionDAGSegmentsForProjectParams) ([]db.ListInteractionDAGSegmentsForProjectRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []db.ListInteractionDAGSegmentsForProjectRow{}
	for _, s := range f.segmentSnapshots {
		if s.ProjectID != arg.ProjectID {
			continue
		}
		out = append(out, db.ListInteractionDAGSegmentsForProjectRow{
			SegmentID:                 s.SegmentID,
			ProjectID:                 pgText(s.ProjectID),
			AgentRunID:                s.AgentRunID,
			IssueID:                   s.IssueID,
			TaskID:                    s.TaskID,
			TrajectoryID:              s.TrajectoryID.Int64,
			TensorRef:                 s.TensorRef,
			ClosingEvent:              s.ClosingEvent,
			ClosingEventTargetSegment: s.ClosingEventTargetSegment,
			StartSeq:                  s.StartSeq,
			EndSeq:                    s.EndSeq,
			WorkspaceID:               arg.WorkspaceID,
			ProjectIDAtEvent:          testUUID(80),
			ContentStatus:             "published",
			TrainableEligible:         true,
		})
	}
	return out, nil
}

// ListInteractionDAGEdgesForProject satisfies InteractionDAGStore (U8 assembly).
// Returns edges recorded for projectID in insertion order (real query orders by
// id). Converts insert params to InteractionDagEdge row structs.
func (f *fakeInteractionDAGStore) ListInteractionDAGEdgesForProject(_ context.Context, _ db.ListInteractionDAGEdgesForProjectParams) ([]db.ListInteractionDAGEdgesForProjectRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []db.ListInteractionDAGEdgesForProjectRow{}
	for i, e := range f.edges {
		out = append(out, db.ListInteractionDAGEdgesForProjectRow{
			ID: int64(i + 1), SrcSegmentID: e.SrcSegmentID,
			DstSegmentID: e.DstSegmentID, Type: e.EdgeType,
		})
	}
	return out, nil
}

// ListInteractionDAGSessionRunsForProject satisfies InteractionDAGStore (U8
// assembly). Returns session_run rows for projectID.
func (f *fakeInteractionDAGStore) ListInteractionDAGSessionRunsForProject(_ context.Context, projectID string) ([]db.InteractionDagSessionRun, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []db.InteractionDagSessionRun{}
	for _, r := range f.sessionRuns {
		if r.ProjectID != projectID {
			continue
		}
		out = append(out, r)
	}
	return out, nil
}

// ListInteractionDAGEnvSnapshotsForProject satisfies InteractionDAGStore (U8
// assembly). Reconstructs env_snapshot rows from the stored segment+snapshot
// insert params (the fake stores them together via
// InsertInteractionDAGSegmentWithSnapshot), filtered by projectID. Mirrors the
// real query's join through interaction_dag_segment on segment_id.
func (f *fakeInteractionDAGStore) ListInteractionDAGEnvSnapshotsForProject(_ context.Context, arg db.ListInteractionDAGEnvSnapshotsForProjectParams) ([]db.InteractionDagEnvSnapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []db.InteractionDagEnvSnapshot{}
	for _, s := range f.segmentSnapshots {
		if s.ProjectID != arg.ProjectID {
			continue
		}
		out = append(out, db.InteractionDagEnvSnapshot{
			SegmentID:       s.SegmentID,
			SandboxIds:      s.SandboxIds,
			IssueSnapshotID: s.IssueSnapshotID,
			EnvState:        s.EnvState,
		})
	}
	return out, nil
}

func (f *fakeInteractionDAGStore) InsertInteractionDAGStepReward(_ context.Context, arg db.InsertInteractionDAGStepRewardParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.order != nil {
		*f.order = append(*f.order, "RecordStepRewards")
	}
	for i, sr := range f.stepRewards {
		if sr.SegmentID == arg.SegmentID && sr.Seq == arg.Seq {
			f.stepRewards[i] = db.InteractionDagStepReward{SegmentID: arg.SegmentID, Seq: arg.Seq, Score: arg.Score, Rationale: arg.Rationale}
			return nil
		}
	}
	f.stepRewards = append(f.stepRewards, db.InteractionDagStepReward{SegmentID: arg.SegmentID, Seq: arg.Seq, Score: arg.Score, Rationale: arg.Rationale})
	return nil
}

func (f *fakeInteractionDAGStore) ListInteractionDAGStepRewardsForProject(_ context.Context, arg db.ListInteractionDAGStepRewardsForProjectParams) ([]db.InteractionDagStepReward, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	projSegs := map[string]bool{}
	for _, s := range f.segmentSnapshots {
		if s.ProjectID == arg.ProjectID {
			projSegs[s.SegmentID] = true
		}
	}
	out := []db.InteractionDagStepReward{}
	for _, sr := range f.stepRewards {
		if projSegs[sr.SegmentID] {
			out = append(out, sr)
		}
	}
	return out, nil
}

func (f *fakeInteractionDAGStore) ListLatestCompletedInteractionDAGDiagnosisTargetsForProject(_ context.Context, projectID string) ([]db.ListLatestCompletedInteractionDAGDiagnosisTargetsForProjectRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	projectSegments := make(map[string]struct{})
	for _, segment := range f.segmentSnapshots {
		if segment.ProjectID == projectID {
			projectSegments[segment.SegmentID] = struct{}{}
		}
	}
	targets := make([]db.ListLatestCompletedInteractionDAGDiagnosisTargetsForProjectRow, 0, len(projectSegments))
	for _, segment := range f.segmentSnapshots {
		if _, exists := projectSegments[segment.SegmentID]; !exists {
			continue
		}
		seqs, exists := f.diagnosisTargetSeqs[segment.SegmentID]
		if !exists {
			continue
		}
		encoded, err := json.Marshal(seqs)
		if err != nil {
			return nil, err
		}
		targets = append(targets, db.ListLatestCompletedInteractionDAGDiagnosisTargetsForProjectRow{SegmentID: segment.SegmentID, ExpectedRewardSeqs: encoded})
	}
	return targets, nil
}

var _ InteractionDAGStore = (*fakeInteractionDAGStore)(nil)

func TestRecordStepRewards(t *testing.T) {
	store := newFakeInteractionDAGStore()
	svc := NewInteractionDAGService(store, &fakeArealSegmentClient{}, true)

	err := svc.RecordStepRewards(context.Background(), "proj-1", []StepReward{
		{SegmentID: "seg-1", Seq: 1, Score: 8, Rationale: "good"},
		{SegmentID: "seg-1", Seq: 2, Score: 3, Rationale: "weak"},
	})
	require.NoError(t, err)
	require.Len(t, store.stepRewards, 2)

	// Re-recording (segment_id, seq) upserts - updates, not duplicates.
	err = svc.RecordStepRewards(context.Background(), "proj-1", []StepReward{
		{SegmentID: "seg-1", Seq: 1, Score: 10, Rationale: "revised"},
	})
	require.NoError(t, err)
	require.Len(t, store.stepRewards, 2, "upsert must not duplicate")
	assert.Equal(t, int32(10), store.stepRewards[0].Score)
	assert.Equal(t, "revised", store.stepRewards[0].Rationale)

	// Disabled service is a no-op.
	disabled := NewInteractionDAGService(newFakeInteractionDAGStore(), &fakeArealSegmentClient{}, false)
	err = disabled.RecordStepRewards(context.Background(), "proj-1", []StepReward{{SegmentID: "seg-1", Seq: 1, Score: 5}})
	require.NoError(t, err)
}

func TestAssembleAssembledDag_StepRewards(t *testing.T) {
	store := newFakeInteractionDAGStore()
	store.segmentSnapshots = append(store.segmentSnapshots, db.InsertInteractionDAGSegmentWithSnapshotParams{
		SegmentID:  "seg-1",
		ProjectID:  "proj-1",
		AgentRunID: testUUID(41),
		StartSeq:   1,
		EndSeq:     2,
	})
	svc := NewInteractionDAGService(store, &fakeArealSegmentClient{}, true)
	require.NoError(t, svc.RecordStepRewards(context.Background(), "proj-1", []StepReward{
		{SegmentID: "seg-1", Seq: 1, Score: 8, Rationale: "good"},
		{SegmentID: "seg-1", Seq: 2, Score: 3, Rationale: "weak"},
	}))

	dag, err := svc.AssembleAssembledDag(context.Background(), "proj-1")
	require.NoError(t, err)
	require.Len(t, dag.Segments, 1)
	require.Len(t, dag.StepRewards, 2)
	assert.Equal(t, "seg-1", dag.StepRewards[0].SegmentID)
	assert.Equal(t, 1, dag.StepRewards[0].Seq)
	assert.Equal(t, 8, dag.StepRewards[0].Score)

	// Absent rewards -> empty slice (JSON []), not nil and not fabricated zeros.
	store2 := newFakeInteractionDAGStore()
	store2.segmentSnapshots = append(store2.segmentSnapshots, db.InsertInteractionDAGSegmentWithSnapshotParams{
		SegmentID: "seg-2", ProjectID: "proj-2", AgentRunID: testUUID(42), StartSeq: 1, EndSeq: 1,
	})
	svc2 := NewInteractionDAGService(store2, &fakeArealSegmentClient{}, true)
	dag2, err := svc2.AssembleAssembledDag(context.Background(), "proj-2")
	require.NoError(t, err)
	assert.Equal(t, []StepReward{}, dag2.StepRewards, "absent rewards -> empty slice, not nil")
}

func TestAssembleAssembledDag_IncludesFrozenAssistantTurnSequences(t *testing.T) {
	store := newFakeInteractionDAGStore()
	store.segmentSnapshots = append(store.segmentSnapshots, db.InsertInteractionDAGSegmentWithSnapshotParams{
		SegmentID: "seg-1", ProjectID: "proj-1", AgentRunID: testUUID(41), StartSeq: 1, EndSeq: 7,
	})
	store.diagnosisTargetSeqs = map[string][]int32{"seg-1": {2, 7}}

	dag, err := NewInteractionDAGService(store, nil, true).AssembleAssembledDag(context.Background(), "proj-1")
	require.NoError(t, err)
	require.Len(t, dag.Segments, 1)
	assert.Equal(t, []int32{2, 7}, dag.Segments[0].AssistantTurnSeqs)
}

func TestAssembleAssembledDag_UsesEmptyAssistantTurnSequencesWithoutDiagnosis(t *testing.T) {
	store := newFakeInteractionDAGStore()
	store.segmentSnapshots = append(store.segmentSnapshots, db.InsertInteractionDAGSegmentWithSnapshotParams{
		SegmentID: "seg-1", ProjectID: "proj-1", AgentRunID: testUUID(41), StartSeq: 1, EndSeq: 1,
	})

	dag, err := NewInteractionDAGService(store, nil, true).AssembleAssembledDag(context.Background(), "proj-1")
	require.NoError(t, err)
	encoded, err := json.Marshal(dag)
	require.NoError(t, err)
	assert.JSONEq(t, `{"segments":[{"segment_id":"seg-1","agent_run_id":"29000000-0000-0000-0000-000000000000","issue_id":"","trajectory_id":null,"tensor_ref":null,"closing_event":null,"trajectory_source":"","trainable":false,"trajectory":null,"env_snapshot":{"sandbox_ids":null,"issue_snapshot_id":null,"env_state":null},"assistant_turn_seqs":[]}],"edges":[],"session_to_agent_run":{},"step_rewards":[],"score_max":0}`, string(encoded))
}

// fakeArealSegmentClient is an in-memory ArealSegmentClient for unit tests.
type fakeArealSegmentClient struct {
	mu                sync.Mutex
	closeSegmentID    int
	closeSegmentErr   error
	exportPayload     json.RawMessage
	exportErr         error
	closeCalls        []string
	exportCalls       []exportCall
	exportCallForTraj map[int]json.RawMessage // trajectoryID -> payload (per-call override)
}

type exportCall struct {
	sessionID    string
	trajectoryID int
}

func (c *fakeArealSegmentClient) CloseSegment(_ context.Context, proxyKey string) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closeCalls = append(c.closeCalls, proxyKey)
	if c.closeSegmentErr != nil {
		return 0, c.closeSegmentErr
	}
	return c.closeSegmentID, nil
}

func (c *fakeArealSegmentClient) ExportTrajectory(_ context.Context, sessionID string, trajectoryID int) (json.RawMessage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.exportCalls = append(c.exportCalls, exportCall{sessionID: sessionID, trajectoryID: trajectoryID})
	if c.exportErr != nil {
		return nil, c.exportErr
	}
	if c.exportCallForTraj != nil {
		if p, ok := c.exportCallForTraj[trajectoryID]; ok {
			return p, nil
		}
	}
	return c.exportPayload, nil
}

var _ ArealSegmentClient = (*fakeArealSegmentClient)(nil)

func ptrText(s string) pgtype.Text { return pgtype.Text{String: s, Valid: s != ""} }

// TestRecordSessionAgentRun_UpsertsMapping verifies RecordSessionAgentRun
// stores the {session_id -> agent_run_id, issue_id} mapping and that a second
// call upserts (re-binds) rather than erroring on the PK.
func TestInteractionDAG_RecordSessionAgentRun_UpsertsMapping(t *testing.T) {
	store := newFakeInteractionDAGStore()
	svc := NewInteractionDAGService(store, &fakeArealSegmentClient{}, true)

	if err := svc.RecordSessionAgentRun(context.Background(), "proj-1", "sess-1", interactionDAGRunID1, "issue-1"); err != nil {
		t.Fatalf("first record: %v", err)
	}
	row, ok := store.sessionRuns["sess-1"]
	if !ok {
		t.Fatal("session_run row not stored")
	}
	assert.Equal(t, interactionDAGRunID1, row.AgentRunID)
	assert.Equal(t, ptrText("issue-1"), row.IssueID)
	assert.Equal(t, "proj-1", row.ProjectID)

	// Upsert: re-bind the same session to a new run (retry attempt, D8).
	if err := svc.RecordSessionAgentRun(context.Background(), "proj-1", "sess-1", interactionDAGRunID2, "issue-1"); err != nil {
		t.Fatalf("upsert record: %v", err)
	}
	row = store.sessionRuns["sess-1"]
	assert.Equal(t, interactionDAGRunID2, row.AgentRunID, "upsert must re-bind agent_run_id")
}

// TestRecordSessionAgentRun_RejectsMissingIDs verifies required-id validation.
func TestInteractionDAG_RecordSessionAgentRun_RejectsMissingIDs(t *testing.T) {
	svc := NewInteractionDAGService(newFakeInteractionDAGStore(), &fakeArealSegmentClient{}, true)
	for _, tc := range []struct{ project, session, run string }{
		{"", "s", "r"}, {"p", "", "r"}, {"p", "s", ""},
	} {
		if err := svc.RecordSessionAgentRun(context.Background(), tc.project, tc.session, tc.run, "i"); err == nil {
			t.Fatalf("expected error for %+v", tc)
		}
	}
}

// TestInteractionDAG_LinkSessionTask_DelegatesToRecord verifies LinkSessionTask
// links a training session to the real derived-agent task id (env-dispatch
// provisioning call order: sessionID, projectID, realTaskID, issueID) by
// delegating to RecordSessionAgentRun's upsert.
func TestInteractionDAG_LinkSessionTask_DelegatesToRecord(t *testing.T) {
	store := newFakeInteractionDAGStore()
	svc := NewInteractionDAGService(store, &fakeArealSegmentClient{}, true)

	if err := svc.LinkSessionTask(context.Background(), "sess-1", "proj-1", "run-real", "issue-1"); err != nil {
		t.Fatalf("LinkSessionTask: %v", err)
	}
	row, ok := store.sessionRuns["sess-1"]
	if !ok {
		t.Fatal("session_run row not stored")
	}
	assert.Equal(t, "run-real", row.AgentRunID, "LinkSessionTask must bind the real task id")
	assert.Equal(t, "proj-1", row.ProjectID)
	assert.Equal(t, ptrText("issue-1"), row.IssueID)
}

// TestCloseSegmentForEvent_RecordsSegment verifies the full close+export+
// record path: looks up agent_run_id via session, closes the segment, exports
// the trajectory, decodes tensor_ref, and stores a segment + env snapshot.
func TestInteractionDAG_CloseSegmentForEvent_RecordsSegment(t *testing.T) {
	store := newFakeInteractionDAGStore()
	client := &fakeArealSegmentClient{
		closeSegmentID: 7,
		exportPayload:  json.RawMessage(`{"input_ids":{"type":"dataclass","class_path":"areal.infra.rpc.rtensor.RTensor","data":{"shard":{"type":"dataclass","class_path":"areal.infra.rpc.rtensor.TensorShardInfo","data":{"shard_id":"shard-1","node_addr":"10.0.0.1:8000"}},"data":{"shape":[1,4]}}},"attention_mask":{"type":"dataclass","class_path":"areal.infra.rpc.rtensor.RTensor","data":{"shard":{"type":"dataclass","class_path":"areal.infra.rpc.rtensor.TensorShardInfo","data":{"shard_id":"shard-2","node_addr":"10.0.0.1:8000"}},"data":{"shape":[1,4]}}}}`),
	}
	svc := NewInteractionDAGService(store, client, true)

	require.NoError(t, svc.RecordSessionAgentRun(context.Background(), "proj-1", "sess-1", interactionDAGRunID1, "issue-1"))

	segID, _, err := svc.CloseSegmentForEvent(context.Background(), "proj-1", "sess-1", "proxy-key", "delegation",
		map[string]any{"sandbox_ids": []string{"sbx-1"}, "issue_snapshot_id": "snap-1", "env_state": map[string]any{"k": "v"}})
	require.NoError(t, err)

	// segment_id is stable: <sessionID>-<trajectoryID>.
	assert.Equal(t, "sess-1-7", segID)

	// CloseSegment called once with the per-call proxy key.
	assert.Equal(t, []string{"proxy-key"}, client.closeCalls)
	// ExportTrajectory called once with the session + the returned trajectory id.
	require.Len(t, client.exportCalls, 1)
	assert.Equal(t, "sess-1", client.exportCalls[0].sessionID)
	assert.Equal(t, 7, client.exportCalls[0].trajectoryID)

	// The combined insert records both segment fields and env-snapshot fields in
	// a single atomic call (CTE), carrying the looked-up agent_run_id + issue_id.
	require.Len(t, store.segmentSnapshots, 1)
	row := store.segmentSnapshots[0]
	assert.Equal(t, "sess-1-7", row.SegmentID)
	assert.Equal(t, "proj-1", row.ProjectID)
	assert.Equal(t, interactionDAGRunID1, util.UUIDToString(row.AgentRunID), "agent_run_id must come from the session lookup, not the caller")
	assert.Equal(t, ptrText("issue-1"), row.IssueID, "issue_id must come from the session lookup")
	assert.EqualValues(t, int64(7), row.TrajectoryID.Int64)
	assert.True(t, row.TrajectoryID.Valid)
	assert.JSONEq(t, `{"input_ids":{"shard_id":"shard-1","node_addr":"10.0.0.1:8000"},"attention_mask":{"shard_id":"shard-2","node_addr":"10.0.0.1:8000"}}`, string(row.TensorRef))
	assert.Equal(t, ptrText("delegation"), row.ClosingEvent)
	// Env-snapshot fields are 1:1 with the segment (atomic insert).
	assert.JSONEq(t, `["sbx-1"]`, string(row.SandboxIds))
	assert.Equal(t, ptrText("snap-1"), row.IssueSnapshotID)
	assert.JSONEq(t, `{"k":"v"}`, string(row.EnvState))
}

// TestCloseSegmentForEvent_LeafSegmentClosingEventEmpty verifies a leaf
// (root-completion) segment records closing_event as NULL (invalid pgtype.Text).
func TestInteractionDAG_CloseSegmentForEvent_LeafSegmentClosingEventEmpty(t *testing.T) {
	store := newFakeInteractionDAGStore()
	client := &fakeArealSegmentClient{
		closeSegmentID: 3,
		exportPayload:  json.RawMessage(`{"input_ids":{"shard_id":"shard-leaf","node_addr":"10.0.0.2:8000"}}`),
	}
	svc := NewInteractionDAGService(store, client, true)
	require.NoError(t, svc.RecordSessionAgentRun(context.Background(), "proj-1", "sess-2", interactionDAGRunID2, ""))

	// Add test task messages.
	store.addTestTaskMessage(interactionDAGRunID2, 1)
	store.addTestTaskMessage(interactionDAGRunID2, 2)
	store.addTestTaskMessage(interactionDAGRunID2, 3)

	_, _, err := svc.CloseSegmentForEvent(context.Background(), "proj-1", "sess-2", "pk", "",
		map[string]any{"sandbox_ids": []string{"sbx-1"}})
	require.NoError(t, err)

	require.Len(t, store.segmentSnapshots, 1)
	assert.False(t, store.segmentSnapshots[0].ClosingEvent.Valid, "leaf segment closing_event must be NULL")
	// Check that the turn range is correct.
	assert.Equal(t, int32(1), store.segmentSnapshots[0].StartSeq)
	assert.Equal(t, int32(3), store.segmentSnapshots[0].EndSeq)
	// tensor_ref decoded from a payload that has a bare shard ref per field.
	assert.JSONEq(t, `{"input_ids":{"shard_id":"shard-leaf","node_addr":"10.0.0.2:8000"}}`, string(store.segmentSnapshots[0].TensorRef))
}

// TestCloseSegmentForEvent_TurnRanges verifies that multiple segments get correct
// start_seq and end_seq values, with start_seq = previous end_seq + 1.
func TestInteractionDAG_CloseSegmentForEvent_TurnRanges(t *testing.T) {
	store := newFakeInteractionDAGStore()
	client := &fakeArealSegmentClient{
		closeSegmentID: 1,
		exportPayload:  json.RawMessage(`{"input_ids":{"shard_id":"shard-1","node_addr":"10.0.0.1:8000"}}`),
	}
	svc := NewInteractionDAGService(store, client, true)
	require.NoError(t, svc.RecordSessionAgentRun(context.Background(), "proj-1", "sess-1", interactionDAGRunID1, ""))

	// First segment: task messages 1-2.
	store.addTestTaskMessage(interactionDAGRunID1, 1)
	store.addTestTaskMessage(interactionDAGRunID1, 2)
	_, _, err := svc.CloseSegmentForEvent(context.Background(), "proj-1", "sess-1", "pk", "delegation", nil)
	require.NoError(t, err)
	require.Len(t, store.segmentSnapshots, 1)
	assert.Equal(t, int32(1), store.segmentSnapshots[0].StartSeq)
	assert.Equal(t, int32(2), store.segmentSnapshots[0].EndSeq)

	// Second segment: task messages 3-5.
	client.closeSegmentID = 2
	store.addTestTaskMessage(interactionDAGRunID1, 3)
	store.addTestTaskMessage(interactionDAGRunID1, 4)
	store.addTestTaskMessage(interactionDAGRunID1, 5)
	_, _, err = svc.CloseSegmentForEvent(context.Background(), "proj-1", "sess-1", "pk", "completion", nil)
	require.NoError(t, err)
	require.Len(t, store.segmentSnapshots, 2)
	// Check second segment.
	assert.Equal(t, int32(3), store.segmentSnapshots[1].StartSeq) // previous end was 2
	assert.Equal(t, int32(5), store.segmentSnapshots[1].EndSeq)
}

func TestDecodeTensorRef_MultiShardEnvelope(t *testing.T) {
	// Real areal export shape: each field is a serialized RTensor dataclass whose
	// data.shard.data carries {shard_id, node_addr}.
	traj := map[string]any{
		"input_ids": map[string]any{
			"type": "dataclass", "class_path": "areal.infra.rpc.rtensor.RTensor",
			"data": map[string]any{
				"shard": map[string]any{
					"type": "dataclass", "class_path": "areal.infra.rpc.rtensor.TensorShardInfo",
					"data": map[string]any{"shard_id": "shard-input-1", "node_addr": "10.0.0.1:8000"},
				},
				"data": map[string]any{"shape": []int{1, 4}},
			},
		},
		"attention_mask": map[string]any{
			"type": "dataclass", "class_path": "areal.infra.rpc.rtensor.RTensor",
			"data": map[string]any{
				"shard": map[string]any{
					"type": "dataclass", "class_path": "areal.infra.rpc.rtensor.TensorShardInfo",
					"data": map[string]any{"shard_id": "shard-mask-1", "node_addr": "10.0.0.1:8000"},
				},
				"data": map[string]any{"shape": []int{1, 4}},
			},
		},
	}
	raw, _ := json.Marshal(traj)
	out, err := decodeTensorRef(raw)
	if err != nil {
		t.Fatalf("decodeTensorRef: %v", err)
	}
	var ref map[string]map[string]string
	if err := json.Unmarshal(out, &ref); err != nil {
		t.Fatalf("unmarshal decoded: %v", err)
	}
	if ref["input_ids"]["shard_id"] != "shard-input-1" {
		t.Fatalf("input_ids shard_id = %q", ref["input_ids"]["shard_id"])
	}
	if ref["attention_mask"]["node_addr"] != "10.0.0.1:8000" {
		t.Fatalf("attention_mask node_addr = %q", ref["attention_mask"]["node_addr"])
	}
	if _, ok := ref["shard_id"]; ok {
		t.Fatalf("must not be a single shard_id map; got %v", ref)
	}
}

func TestDecodeTensorRef_BareShardRef(t *testing.T) {
	// Tolerant: a bare {"shard_id":...} per field is also accepted.
	traj := map[string]any{
		"input_ids": map[string]any{"shard_id": "s1", "node_addr": "h:1"},
	}
	raw, _ := json.Marshal(traj)
	out, err := decodeTensorRef(raw)
	if err != nil {
		t.Fatalf("decodeTensorRef: %v", err)
	}
	var ref map[string]map[string]string
	_ = json.Unmarshal(out, &ref)
	if ref["input_ids"]["shard_id"] != "s1" {
		t.Fatalf("got %v", ref)
	}
}

func TestDecodeTensorRef_MissingShardIDIsError(t *testing.T) {
	traj := map[string]any{"input_ids": map[string]any{"data": "no shard"}}
	raw, _ := json.Marshal(traj)
	if _, err := decodeTensorRef(raw); err == nil {
		t.Fatal("expected error for field missing shard_id")
	}
}

// TestCloseSegmentForEvent_MissingSessionLookupErrors verifies that when no
// RecordSessionAgentRun was called, CloseSegmentForEvent errors and does NOT
// call CloseSegment (no dangling trajectory close on the bridge).
func TestInteractionDAG_CloseSegmentForEvent_MissingSessionLookupErrors(t *testing.T) {
	store := newFakeInteractionDAGStore()
	client := &fakeArealSegmentClient{closeSegmentID: 1, exportPayload: json.RawMessage(`{"input_ids":{"type":"dataclass","class_path":"areal.infra.rpc.rtensor.RTensor","data":{"shard":{"type":"dataclass","class_path":"areal.infra.rpc.rtensor.TensorShardInfo","data":{"shard_id":"s","node_addr":"10.0.0.1:8000"}},"data":{"shape":[1,4]}}}}`)}
	svc := NewInteractionDAGService(store, client, true)

	_, _, err := svc.CloseSegmentForEvent(context.Background(), "proj-1", "unknown-sess", "pk", "delegation", nil)
	require.Error(t, err)
	assert.Empty(t, client.closeCalls, "must not close a segment when the session lookup fails")
	assert.Empty(t, client.exportCalls, "must not export when the session lookup fails")
}

// TestCloseSegmentForEvent_CloseSegmentErrorPropagates verifies a bridge
// CloseSegment error is returned and ExportTrajectory is not called.
func TestInteractionDAG_CloseSegmentForEvent_CloseSegmentErrorPropagates(t *testing.T) {
	store := newFakeInteractionDAGStore()
	client := &fakeArealSegmentClient{
		closeSegmentErr: errors.New("bridge down"),
	}
	svc := NewInteractionDAGService(store, client, true)
	require.NoError(t, svc.RecordSessionAgentRun(context.Background(), "proj-1", "sess-1", interactionDAGRunID1, "issue-1"))

	_, _, err := svc.CloseSegmentForEvent(context.Background(), "proj-1", "sess-1", "pk", "delegation", nil)
	require.Error(t, err)
	assert.Empty(t, client.exportCalls, "must not export after a close failure")
	assert.Empty(t, store.segmentSnapshots, "must not record a segment after a close failure")
}

// TestCloseSegmentForEvent_ExportErrorPropagates verifies an export error is
// returned and no segment row is recorded.
func TestInteractionDAG_CloseSegmentForEvent_ExportErrorPropagates(t *testing.T) {
	store := newFakeInteractionDAGStore()
	client := &fakeArealSegmentClient{
		closeSegmentID: 9,
		exportErr:      errors.New("export 500"),
	}
	svc := NewInteractionDAGService(store, client, true)
	require.NoError(t, svc.RecordSessionAgentRun(context.Background(), "proj-1", "sess-1", interactionDAGRunID1, "issue-1"))

	_, _, err := svc.CloseSegmentForEvent(context.Background(), "proj-1", "sess-1", "pk", "delegation", nil)
	require.Error(t, err)
	assert.Empty(t, store.segmentSnapshots, "must not record a segment after an export failure")
}

// TestCloseSegmentForEvent_BadTensorRefErrors verifies that an export payload
// with no decodable tensor_ref is reported as an error (absence stays
// distinguishable at the boundary), and no segment is recorded.
func TestInteractionDAG_CloseSegmentForEvent_BadTensorRefErrors(t *testing.T) {
	store := newFakeInteractionDAGStore()
	client := &fakeArealSegmentClient{
		closeSegmentID: 4,
		exportPayload:  json.RawMessage(`"not-an-object"`),
	}
	svc := NewInteractionDAGService(store, client, true)
	require.NoError(t, svc.RecordSessionAgentRun(context.Background(), "proj-1", "sess-1", interactionDAGRunID1, "issue-1"))

	_, _, err := svc.CloseSegmentForEvent(context.Background(), "proj-1", "sess-1", "pk", "delegation", nil)
	require.Error(t, err)
	assert.Empty(t, store.segmentSnapshots, "must not record a segment when tensor_ref is undecodable")
}

// TestCloseSegmentForEvent_StoreInsertErrorPropagates verifies that when the
// atomic segment+snapshot store insert fails, the error is returned and nothing
// is persisted to the fake store. (With the real DB the CTE guarantees neither
// row survives; the unit test asserts the call was attempted but rejected.)
func TestInteractionDAG_CloseSegmentForEvent_StoreInsertErrorPropagates(t *testing.T) {
	store := newFakeInteractionDAGStore()
	store.insertSegmentSnapshotErr = errors.New("db write failed")
	client := &fakeArealSegmentClient{
		closeSegmentID: 5,
		exportPayload:  json.RawMessage(`{"input_ids":{"shard_id":"s","node_addr":"10.0.0.1:8000"}}`),
	}
	svc := NewInteractionDAGService(store, client, true)
	require.NoError(t, svc.RecordSessionAgentRun(context.Background(), "proj-1", "sess-1", interactionDAGRunID1, "issue-1"))

	_, _, err := svc.CloseSegmentForEvent(context.Background(), "proj-1", "sess-1", "pk", "delegation", nil)
	require.Error(t, err)
	assert.Empty(t, store.segmentSnapshots, "must not persist on insert error")
}

// TestAddEdge_StoresTypedEdge verifies each canonical edge type is stored.
func TestInteractionDAG_AddEdge_StoresTypedEdge(t *testing.T) {
	workspaceID := testUUID(80)
	for _, tc := range []struct {
		name         string
		edgeType     string
		needsTrigger bool
	}{
		{"continues", EdgeTypeContinues, false},
		{"responds_to", EdgeTypeRespondsTo, true},
		{"delegates_to", EdgeTypeDelegation, true},
		{"mentions", EdgeTypeMention, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newFakeInteractionDAGStore()
			if tc.needsTrigger {
				store.edgeTriggerMessageID = testUUID(81)
			}
			svc := NewInteractionDAGService(store, &fakeArealSegmentClient{}, true)
			err := svc.AddEdge(context.Background(), workspaceID, "seg-a", "seg-b", tc.edgeType)
			require.NoError(t, err)
			require.Len(t, store.edges, 1)
			e := store.edges[0]
			assert.Equal(t, workspaceID, e.WorkspaceID)
			assert.Equal(t, int64(1), e.EdgeSeq)
			assert.Equal(t, "seg-a", e.SrcSegmentID)
			assert.Equal(t, "seg-b", e.DstSegmentID)
			assert.Equal(t, tc.edgeType, e.EdgeType)
			assert.Equal(t, tc.needsTrigger, e.TriggerMessageID.Valid)
		})
	}
}

// TestAddEdge_RejectsBadType verifies invalid and legacy edge types are rejected.
func TestInteractionDAG_AddEdge_RejectsBadType(t *testing.T) {
	for _, edgeType := range []string{"handoff", "completion", "delegation", "mention"} {
		store := newFakeInteractionDAGStore()
		svc := NewInteractionDAGService(store, &fakeArealSegmentClient{}, true)
		err := svc.AddEdge(context.Background(), testUUID(80), "seg-a", "seg-b", edgeType)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrInvalidEdgeType)
		assert.Empty(t, store.edges)
	}
}

// TestAddEdge_RejectsMissingIDs verifies required-id validation.
func TestInteractionDAG_AddEdge_RejectsMissingIDs(t *testing.T) {
	svc := NewInteractionDAGService(newFakeInteractionDAGStore(), &fakeArealSegmentClient{}, true)
	for _, tc := range []struct {
		workspace pgtype.UUID
		src, dst  string
	}{
		{pgtype.UUID{}, "s", "d"}, {testUUID(80), "", "d"}, {testUUID(80), "s", ""},
	} {
		if err := svc.AddEdge(context.Background(), tc.workspace, tc.src, tc.dst, EdgeTypeDelegation); err == nil {
			t.Fatalf("expected error for %+v", tc)
		}
	}
}

// TestInteractionDAGService_DisabledIsNoOp verifies that a disabled service
// (INTERACTION_DAG_ENABLED=false) touches neither the store nor the bridge.
func TestInteractionDAGService_DisabledIsNoOp(t *testing.T) {
	store := newFakeInteractionDAGStore()
	client := &fakeArealSegmentClient{closeSegmentID: 1, exportPayload: json.RawMessage(`{"input_ids":{"shard_id":"s","node_addr":"10.0.0.1:8000"}}`)}
	svc := NewInteractionDAGService(store, client, false)

	assert.NoError(t, svc.RecordSessionAgentRun(context.Background(), "p", "s", "r", "i"))
	_, _, err := svc.CloseSegmentForEvent(context.Background(), "p", "s", "pk", "delegation", nil)
	assert.NoError(t, err)
	assert.NoError(t, svc.AddEdge(context.Background(), pgtype.UUID{}, "a", "b", EdgeTypeDelegation))

	assert.Empty(t, store.sessionRuns)
	assert.Empty(t, store.segmentSnapshots)
	assert.Empty(t, store.edges)
	assert.Empty(t, client.closeCalls)
}

// TestCloseSegmentForEvent_FanOutDeterministic verifies that closing several
// segments for one session produces distinct, deterministic segment ids and
// acyclic edge ordering (delegation fan-out: src -> many dst).
func TestInteractionDAG_CloseSegmentForEvent_FanOutDeterministic(t *testing.T) {
	store := newFakeInteractionDAGStore()
	client := &fakeArealSegmentClient{
		closeSegmentID: 100,
		exportCallForTraj: map[int]json.RawMessage{
			100: json.RawMessage(`{"input_ids":{"shard_id":"sh-0","node_addr":"10.0.0.1:8000"}}`),
			101: json.RawMessage(`{"input_ids":{"shard_id":"sh-1","node_addr":"10.0.0.1:8000"}}`),
			102: json.RawMessage(`{"input_ids":{"shard_id":"sh-2","node_addr":"10.0.0.1:8000"}}`),
		},
	}
	svc := NewInteractionDAGService(store, client, true)
	require.NoError(t, svc.RecordSessionAgentRun(context.Background(), "proj-1", "sess-1", interactionDAGRunID1, "issue-1"))

	// First close: the source segment.
	rootID, _, err := svc.CloseSegmentForEvent(context.Background(), "proj-1", "sess-1", "pk", "completion", nil)
	require.NoError(t, err)
	assert.Equal(t, "sess-1-100", rootID)

	// Two further closes (children). closeSegmentID is fixed at 100, so bump
	// it per call to get distinct trajectory ids.
	client.closeSegmentID = 101
	child1, _, err := svc.CloseSegmentForEvent(context.Background(), "proj-1", "sess-1", "pk", "delegation", nil)
	require.NoError(t, err)
	client.closeSegmentID = 102
	child2, _, err := svc.CloseSegmentForEvent(context.Background(), "proj-1", "sess-1", "pk", "delegation", nil)
	require.NoError(t, err)

	ids := map[string]bool{rootID: true, child1: true, child2: true}
	assert.Len(t, ids, 3, "segment ids must be distinct")
	assert.Equal(t, "sess-1-101", child1)
	assert.Equal(t, "sess-1-102", child2)
	// Three atomic segment+snapshot inserts recorded.
	require.Len(t, store.segmentSnapshots, 3)

	// Fan-out edges root -> child1, root -> child2 (acyclic, deterministic).
	store.edgeTriggerMessageID = testUUID(81)
	require.NoError(t, svc.AddEdge(context.Background(), testUUID(80), rootID, child1, EdgeTypeDelegation))
	require.NoError(t, svc.AddEdge(context.Background(), testUUID(80), rootID, child2, EdgeTypeDelegation))
	require.Len(t, store.edges, 2)
	assert.Equal(t, rootID, store.edges[0].SrcSegmentID)
	assert.Equal(t, child1, store.edges[0].DstSegmentID)
}

// TestEncodeEnvSnapshot_NilOrEmpty verifies that a nil or empty env-snapshot
// map yields "{}" for env_state (not JSON "null"), matching the column's
// DEFAULT '{}'::jsonb intent. Minor #2: json.Marshal(nil map) returns "null".
func TestInteractionDAG_EncodeEnvSnapshot_NilOrEmpty(t *testing.T) {
	for _, tc := range []struct {
		name string
		snap map[string]any
	}{
		{"nil", nil},
		{"empty", map[string]any{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sandboxIDs, issueSnapshotID, envState := encodeEnvSnapshot(tc.snap)
			assert.JSONEq(t, `[]`, string(sandboxIDs))
			assert.False(t, issueSnapshotID.Valid)
			assert.JSONEq(t, `{}`, string(envState), "nil/empty snapshot must yield {} not null")
		})
	}
}

func interactionDAGTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		t.Skip("integration test requires DATABASE_URL")
	}
	pool, err := pgxpool.New(context.Background(), databaseURL)
	if err != nil {
		t.Fatalf("connect to PostgreSQL: %v", err)
	}
	return pool
}

// --- Canonical private-schema PostgreSQL integration tests ---

func newCanonicalInteractionDAGQueries(t *testing.T) (context.Context, *db.Queries) {
	t.Helper()
	ctx := context.Background()
	_, conn := openUniversalDAGServiceSchema(t, ctx)
	t.Cleanup(conn.Release)
	if _, err := conn.Exec(ctx, universalDAGLegacySchema); err != nil {
		t.Fatalf("create pre-454 schema: %v", err)
	}
	applyUniversalDAGMigrationIfPresent(t, ctx, conn)
	seedUniversalDAGCanonicalOwners(t, ctx, conn)
	return ctx, db.New(conn)
}

// TestInteractionDAGQueries_Integration exercises the retained session and
// segment writers on migration 454. The writer must commit only the approved
// legacy_unverified compatibility shape.
func TestInteractionDAGQueries_Integration(t *testing.T) {
	ctx, q := newCanonicalInteractionDAGQueries(t)
	require.NoError(t, q.UpsertInteractionDAGSessionRun(ctx, db.UpsertInteractionDAGSessionRunParams{
		SessionID: "int-sess-1", ProjectID: universalProjectA, AgentRunID: universalTaskA, IssueID: ptrText("int-issue"),
	}))
	got, err := q.GetInteractionDAGSessionRun(ctx, "int-sess-1")
	require.NoError(t, err)
	assert.Equal(t, universalTaskA, got.AgentRunID)
	assert.Equal(t, "int-issue", got.IssueID.String)

	segID := "int-sess-1-42"
	insertedID, err := q.InsertInteractionDAGSegmentWithSnapshot(ctx, db.InsertInteractionDAGSegmentWithSnapshotParams{
		SegmentID: segID, ProjectID: universalProjectA, AgentRunID: util.MustParseUUID(universalTaskA),
		IssueID: ptrText("int-issue"), TrajectoryID: pgtype.Int8{Int64: 42, Valid: true},
		TensorRef: []byte(`{"opaque":true}`), ClosingEvent: ptrText("delegation"),
		StartSeq: 1, EndSeq: 1, TrajectorySource: "areal_tensor", Trainable: true,
		Trajectory: []byte(`[]`), SandboxIds: []byte(`[]`), EnvState: []byte(`{}`),
	})
	require.NoError(t, err)
	require.Equal(t, segID, insertedID)
	row, err := q.GetInteractionDAGSegmentByID(ctx, segID)
	require.NoError(t, err)
	assert.Equal(t, "legacy_unverified", row.ContentStatus)
	assert.Zero(t, row.TrajectoryID)
	assert.Empty(t, row.TensorRef)
	assert.False(t, row.Trainable)
	assert.JSONEq(t, `[]`, string(row.Trajectory))
}

// TestAssembleAssembledDag_ProjectsRecordedRows verifies that a retained writer
// remains runnable while the assembler preserves metadata and snapshot shape
// but strips all legacy body/tensor/training fields.
func TestAssembleAssembledDag_ProjectsRecordedRows(t *testing.T) {
	ctx, q := newCanonicalInteractionDAGQueries(t)
	svc := NewInteractionDAGService(q, nil, true)
	require.NoError(t, q.UpsertInteractionDAGSessionRun(ctx, db.UpsertInteractionDAGSessionRunParams{
		SessionID: "asm-sess-1", ProjectID: universalProjectA, AgentRunID: universalTaskA, IssueID: ptrText("asm-issue-1"),
	}))
	insertedID, err := q.InsertInteractionDAGSegmentWithSnapshot(ctx, db.InsertInteractionDAGSegmentWithSnapshotParams{
		SegmentID: "asm-sess-1-10", ProjectID: universalProjectA, AgentRunID: util.MustParseUUID(universalTaskA),
		IssueID: ptrText("asm-issue-1"), TrajectoryID: pgtype.Int8{Int64: 10, Valid: true},
		TensorRef: []byte(`{"opaque":true}`), ClosingEvent: ptrText("delegation"),
		StartSeq: 1, EndSeq: 1, TrajectorySource: "areal_tensor", Trainable: true,
		Trajectory: []byte(`[]`), SandboxIds: []byte(`["sbx-1"]`),
		IssueSnapshotID: ptrText("snap-1"), EnvState: []byte(`{"k":"v"}`),
	})
	require.NoError(t, err)
	require.Equal(t, "asm-sess-1-10", insertedID)

	dag, err := svc.AssembleAssembledDag(ctx, universalProjectA)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"asm-sess-1": universalTaskA}, dag.SessionToAgentRun)
	require.Len(t, dag.Segments, 1)
	seg := dag.Segments[0]
	assert.Equal(t, "asm-sess-1-10", seg.SegmentID)
	assert.Equal(t, universalTaskA, seg.AgentRunID)
	assert.Equal(t, "asm-issue-1", seg.IssueID)
	assert.Nil(t, seg.TrajectoryID)
	assert.Empty(t, seg.TensorRef)
	assert.False(t, seg.Trainable)
	assert.JSONEq(t, `[]`, string(seg.Trajectory))
	assert.JSONEq(t, `["sbx-1"]`, string(seg.EnvSnapshot.SandboxIDs))
	require.NotNil(t, seg.EnvSnapshot.IssueSnapshotID)
	assert.Equal(t, "snap-1", *seg.EnvSnapshot.IssueSnapshotID)
	assert.JSONEq(t, `{"k":"v"}`, string(seg.EnvSnapshot.EnvState))

	raw, err := json.Marshal(seg)
	require.NoError(t, err)
	var segKeys map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &segKeys))
	expectedSegKeys := map[string]bool{
		"segment_id": true, "agent_run_id": true, "issue_id": true,
		"trajectory_id": true, "tensor_ref": true, "closing_event": true,
		"trajectory_source": true, "trainable": true, "trajectory": true,
		"env_snapshot": true, "assistant_turn_seqs": true,
	}
	assert.Equal(t, expectedSegKeys, keysOf(segKeys), "segment JSON keys must match SegmentSpec exactly")
	for _, banned := range []string{"judge_scores", "start_turn_idx", "end_turn_idx", "text", "messages"} {
		_, ok := segKeys[banned]
		assert.False(t, ok, "segment must not carry banned key %q", banned)
	}
}

// TestAssembleAssembledDag_EdgesTypedAndAcyclic is a fake-store unit cycle test.
// It verifies continues, responds_to, delegates_to, and mentions remain acyclic.
func TestAssembleAssembledDag_EdgesTypedAndAcyclic(t *testing.T) {
	store := newFakeInteractionDAGStore()
	const proj = "asm-proj-2"
	for i, segmentID := range []string{"asm-seg-a", "asm-seg-b", "asm-seg-c"} {
		store.segmentSnapshots = append(store.segmentSnapshots, db.InsertInteractionDAGSegmentWithSnapshotParams{
			SegmentID: segmentID, ProjectID: proj, AgentRunID: testUUID(byte(60 + i)),
		})
	}
	workspaceID := testUUID(70)
	triggerID := testUUID(71)
	store.edges = []db.InsertUniversalDAGEdgeParams{
		{WorkspaceID: workspaceID, EdgeSeq: 1, SrcSegmentID: "asm-seg-a", DstSegmentID: "asm-seg-b", EdgeType: EdgeTypeDelegation, TriggerMessageID: triggerID},
		{WorkspaceID: workspaceID, EdgeSeq: 2, SrcSegmentID: "asm-seg-b", DstSegmentID: "asm-seg-c", EdgeType: EdgeTypeMention, TriggerMessageID: triggerID},
		{WorkspaceID: workspaceID, EdgeSeq: 3, SrcSegmentID: "asm-seg-a", DstSegmentID: "asm-seg-c", EdgeType: EdgeTypeRespondsTo, TriggerMessageID: triggerID},
	}
	svc := NewInteractionDAGService(store, nil, true)
	dag, err := svc.AssembleAssembledDag(context.Background(), proj)
	require.NoError(t, err)
	require.Len(t, dag.Edges, 3)
	for _, edge := range dag.Edges {
		assert.Contains(t, []string{EdgeTypeDelegation, EdgeTypeMention, EdgeTypeRespondsTo}, edge.Type)
	}
	assert.True(t, dagIsAcyclic(dag), "assembled graph must be acyclic")

	store.edges = append(store.edges, db.InsertUniversalDAGEdgeParams{
		WorkspaceID: workspaceID, EdgeSeq: 4, SrcSegmentID: "asm-seg-c", DstSegmentID: "asm-seg-a",
		EdgeType: EdgeTypeRespondsTo, TriggerMessageID: triggerID,
	})
	dag, err = svc.AssembleAssembledDag(context.Background(), proj)
	require.NoError(t, err)
	assert.False(t, dagIsAcyclic(dag), "graph with a back-edge must be cyclic")
}

// dagIsAcyclic reports whether the assembled DAG's segment graph is acyclic,
// via Kahn's topological sort: count in-degrees, repeatedly remove zero-in-degree
// nodes, and check all nodes are drained. A cycle leaves nodes with in-degree > 0.
func dagIsAcyclic(dag AssembledDag) bool {
	nodes := make(map[string]int, len(dag.Segments))
	for _, s := range dag.Segments {
		nodes[s.SegmentID] = 0
	}
	for _, e := range dag.Edges {
		// Only count edges between known segment nodes; edges to unrecorded
		// endpoints are ignored defensively if an invalid fixture bypasses canonical storage.
		if _, ok := nodes[e.SrcSegmentID]; !ok {
			continue
		}
		if _, ok := nodes[e.DstSegmentID]; !ok {
			continue
		}
		nodes[e.DstSegmentID]++
	}
	// Seed the queue with zero-in-degree nodes.
	queue := make([]string, 0, len(nodes))
	for n, deg := range nodes {
		if deg == 0 {
			queue = append(queue, n)
		}
	}
	drained := 0
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		drained++
		for _, e := range dag.Edges {
			if e.SrcSegmentID != n {
				continue
			}
			if _, ok := nodes[e.DstSegmentID]; !ok {
				continue
			}
			nodes[e.DstSegmentID]--
			if nodes[e.DstSegmentID] == 0 {
				queue = append(queue, e.DstSegmentID)
			}
		}
	}
	return drained == len(nodes)
}

// keysOf returns the set of keys in a JSON object (for exact-key assertions).
func keysOf(m map[string]json.RawMessage) map[string]bool {
	out := make(map[string]bool, len(m))
	for k := range m {
		out[k] = true
	}
	return out
}
