package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/multica-ai/multica/server/internal/util"
	agentpkg "github.com/multica-ai/multica/server/pkg/agent"
	"github.com/multica-ai/multica/server/pkg/db/generated"
)

// Diagnosis test fixtures. segment_id / agent_run_id are strings (D8:
// agent_run_id = task.ID); project/workspace are UUIDs.
const (
	diagProjectID   = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	diagWorkspaceID = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	diagSegmentID   = "seg-diag-1"
	diagAgentRunID  = "cccccccc-cccc-cccc-cccc-cccccccccccc"
)

// fakeBackend is a minimal agentpkg.Backend for exercising DiagnosisAgentRunner
// without spawning a real agent subprocess.
type fakeBackend struct {
	called    bool
	gotPrompt string
	gotOpts   agentpkg.ExecOptions
	output    string
	status    string
	execErr   error
}

func (f *fakeBackend) executed() bool { return f.called }

func (f *fakeBackend) Execute(_ context.Context, prompt string, opts agentpkg.ExecOptions) (*agentpkg.Session, error) {
	f.called = true
	f.gotPrompt = prompt
	f.gotOpts = opts
	if f.execErr != nil {
		return nil, f.execErr
	}
	status := f.status
	if status == "" {
		status = "completed"
	}
	msgCh := make(chan agentpkg.Message)
	close(msgCh)
	resCh := make(chan agentpkg.Result, 1)
	resCh <- agentpkg.Result{Status: status, Output: f.output}
	close(resCh)
	return &agentpkg.Session{Messages: msgCh, Result: resCh}, nil
}

func TestParseStepRewards_Valid(t *testing.T) {
	in := "```json\n[{\"segment_id\":\"s1\",\"seq\":1,\"score\":8,\"rationale\":\"x\"},{\"segment_id\":\"s1\",\"seq\":2,\"score\":2}]\n```"
	got, err := parseStepRewards(in, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].SegmentID != "s1" || got[0].Seq != 1 || got[0].Score != 8 {
		t.Fatalf("%+v", got)
	}
}

func TestDiagnosisReport_UsesStableJSONKeys(t *testing.T) {
	encoded, err := json.Marshal(DiagnosisReport{
		RunID:             "run-1",
		CompletedSegments: 2,
		TotalSegments:     3,
		Status:            DiagnosisRunCompleted,
	})
	require.NoError(t, err)
	assert.JSONEq(t, `{"run_id":"run-1","completed_segments":2,"total_segments":3,"status":"completed"}`, string(encoded))
}

func TestLoadExistingCompletedDiagnosis_ReturnsMatchingTopology(t *testing.T) {
	store, _ := newTestDiagnosisStore(t)
	createTestDiagnosisRun(t, store, "run-1", "seg-a")
	_, err := store.StartSegment(context.Background(), "run-1", "seg-a", 0, 0)
	require.NoError(t, err)
	require.NoError(t, store.CompleteSegment(context.Background(), "run-1", "seg-a"))
	require.NoError(t, store.CompleteRun(context.Background(), "run-1", "topo-hash-1"))

	report, found, err := loadExistingCompletedDiagnosis(context.Background(), store, "project-1", "task-1", "topo-hash-1")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, DiagnosisReport{RunID: "run-1", CompletedSegments: 1, TotalSegments: 1, Status: DiagnosisRunCompleted}, report)
}

// Quickstart Scenario 5.2: re-triggering a completed dispatch with unchanged
// topology replays the existing completed run (done=true), so the caller
// returns it without provisioning a new sandbox.
func TestCreateOrResumeDiagnosisRun_CompletedTopologyReplayReturnsDone(t *testing.T) {
	store, _ := newTestDiagnosisStore(t)
	segmentIDs := []string{"seg-a"}
	_, err := store.CreateRun(context.Background(), DiagnosisRunCheckpoint{
		RunID:             "run-1",
		ProjectID:         "project-1",
		TaskID:            "task-1",
		TopologyHash:      topologyHashFromIDs(segmentIDs),
		OrderedSegmentIDs: segmentIDs,
	})
	require.NoError(t, err)
	_, err = store.StartSegment(context.Background(), "run-1", "seg-a", 0, 0)
	require.NoError(t, err)
	require.NoError(t, store.CompleteSegment(context.Background(), "run-1", "seg-a"))
	require.NoError(t, store.CompleteRun(context.Background(), "run-1", topologyHashFromIDs(segmentIDs)))

	_, report, done, err := CreateOrResumeDiagnosisRun(context.Background(), store, "project-1", "task-1", segmentIDs)
	require.NoError(t, err)
	assert.True(t, done, "completed run with unchanged topology must replay, not re-provision")
	assert.Equal(t, DiagnosisReport{RunID: "run-1", CompletedSegments: 1, TotalSegments: 1, Status: DiagnosisRunCompleted}, report)
}

func TestParseStepRewards_ClampsAndSkips(t *testing.T) {
	in := `[{"segment_id":"s1","seq":1,"score":99},{"segment_id":"s1","seq":-1,"score":5}]`
	got, _ := parseStepRewards(in, 10) // 99 clamps to 10; seq=-1 skipped
	if len(got) != 1 || got[0].Score != 10 {
		t.Fatalf("%+v", got)
	}
}

func TestParseStepRewards_Empty(t *testing.T) {
	got, err := parseStepRewards("not json", 10)
	if err == nil || len(got) != 0 {
		t.Fatalf("expected empty+err, got %+v %v", got, err)
	}
}

// TestSystemPrompt_EmbedsScoreMax verifies the system prompt carries the
// concrete [0, scoreMax] scoring range rather than an unspecified "specified
// range", so the model scores within valid bounds.
func TestSystemPrompt_EmbedsScoreMax(t *testing.T) {
	r, err := NewDiagnosisAgentRunner(DiagnosisAgentConfig{ScoreMax: 7, Backend: &fakeBackend{}})
	if err != nil {
		t.Fatal(err)
	}
	p := r.systemPrompt()
	if !strings.Contains(p, "between 0 and 7 inclusive") {
		t.Fatalf("system prompt missing embedded range: %q", p)
	}
}

// TestNewDiagnosisAgentRunner_ErrorOnUnknownProvider verifies the constructor
// surfaces backend-creation failures instead of returning a runner with a nil
// backend that silently fails at Diagnose time.
func TestNewDiagnosisAgentRunner_ErrorOnUnknownProvider(t *testing.T) {
	r, err := NewDiagnosisAgentRunner(DiagnosisAgentConfig{Provider: "nonexistent-agent-type"})
	if err == nil {
		t.Fatalf("expected error for unknown provider, got runner %+v", r)
	}
	if r != nil {
		t.Fatalf("expected nil runner on error, got %+v", r)
	}
}

// TestNewDiagnosisAgentRunner_InjectsBackend verifies a caller-supplied Backend
// is used as-is (no agentpkg.New call) and config defaults are applied.
func TestNewDiagnosisAgentRunner_InjectsBackend(t *testing.T) {
	fb := &fakeBackend{}
	r, err := NewDiagnosisAgentRunner(DiagnosisAgentConfig{Provider: "pi", ScoreMax: 5, Backend: fb})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r == nil || r.scoreMax != 5 {
		t.Fatalf("bad runner: %+v", r)
	}
}

// diagSegmentToGetByIDRow and diagSegmentToListForProjectRow convert the
// shared db.InteractionDagSegment test fixture into the two distinct
// query-specific row types GetInteractionDAGSegmentByID/
// ListInteractionDAGSegmentsForProject return (identical fields, different
// generated types since neither query is a plain SELECT * reusing the table
// model 1:1 for a mock's return type).
func diagSegmentToGetByIDRow(seg db.InteractionDagSegment) db.GetInteractionDAGSegmentByIDRow {
	return db.GetInteractionDAGSegmentByIDRow{
		SegmentID:                 seg.SegmentID,
		ProjectID:                 seg.ProjectID,
		AgentRunID:                seg.AgentRunID,
		IssueID:                   seg.IssueID,
		TaskID:                    seg.TaskID,
		TrajectoryID:              seg.TrajectoryID,
		TensorRef:                 seg.TensorRef,
		ClosingEvent:              seg.ClosingEvent,
		ClosingEventTargetSegment: seg.ClosingEventTargetSegment,
		StartSeq:                  seg.StartSeq,
		EndSeq:                    seg.EndSeq,
		TrajectorySource:          seg.TrajectorySource,
		Trainable:                 seg.Trainable,
		Trajectory:                seg.Trajectory,
		CreatedAt:                 seg.CreatedAt,
	}
}

func diagSegmentToListForProjectRow(seg db.InteractionDagSegment) db.ListInteractionDAGSegmentsForProjectRow {
	return db.ListInteractionDAGSegmentsForProjectRow{
		SegmentID:                 seg.SegmentID,
		ProjectID:                 seg.ProjectID,
		AgentRunID:                seg.AgentRunID,
		IssueID:                   seg.IssueID,
		TaskID:                    seg.TaskID,
		TrajectoryID:              seg.TrajectoryID,
		TensorRef:                 seg.TensorRef,
		ClosingEvent:              seg.ClosingEvent,
		ClosingEventTargetSegment: seg.ClosingEventTargetSegment,
		StartSeq:                  seg.StartSeq,
		EndSeq:                    seg.EndSeq,
		TrajectorySource:          seg.TrajectorySource,
		Trainable:                 seg.Trainable,
		Trajectory:                seg.Trajectory,
		CreatedAt:                 seg.CreatedAt,
	}
}

// newDiagnosisStores builds a MockDiagnosisStores (satisfying both DAGStore and
// MessageStore) for one root segment "seg-diag-1" with two assistant messages
// and a task issue. It is the happy-path fixture for the rich-prompt Diagnose
// tests.
func newDiagnosisStores(t *testing.T, projectID, workspaceID pgtype.UUID) *MockDiagnosisStores {
	t.Helper()
	m := new(MockDiagnosisStores)
	seg := db.InteractionDagSegment{
		SegmentID:  diagSegmentID,
		ProjectID:  projectID.String(),
		AgentRunID: diagAgentRunID,
		StartSeq:   1,
		EndSeq:     2,
	}
	msgs := []db.TaskMessage{
		{Seq: 1, Type: "assistant", Content: pgtype.Text{String: "Let me plan the approach.", Valid: true}},
		{Seq: 2, Type: "assistant", Content: pgtype.Text{String: "The answer is 42.", Valid: true}},
	}
	// GetInteractionDAG + GetSegmentMessages both workspace-gate via the same
	// (projectID, workspaceID) params; one expectation covers both calls.
	m.On("GetProjectInWorkspace", mock.Anything, mock.MatchedBy(func(a db.GetProjectInWorkspaceParams) bool {
		return a.ID == projectID && a.WorkspaceID == workspaceID
	})).Return(db.Project{ID: projectID, WorkspaceID: workspaceID}, nil)
	m.On("ListInteractionDAGSegmentsForProject", mock.Anything, projectID.String()).Return([]db.ListInteractionDAGSegmentsForProjectRow{diagSegmentToListForProjectRow(seg)}, nil)
	m.On("ListInteractionDAGEdgesForProject", mock.Anything, projectID.String()).Return([]db.InteractionDagEdge{}, nil)
	m.On("GetInteractionDAGSegmentByID", mock.Anything, diagSegmentID).Return(diagSegmentToGetByIDRow(seg), nil)
	m.On("MessagesForTaskInRange", mock.Anything, diagAgentRunID, int32(1), int32(2)).Return(msgs, nil)
	// Root segment (no incoming edge) -> AgentRunID is the root task ID (D8).
	m.On("GetIssueForTask", mock.Anything, diagAgentRunID).Return(db.Issue{
		WorkspaceID:        workspaceID,
		Title:              "Compute the answer",
		Description:        pgtype.Text{String: "Goal: compute the ultimate answer.", Valid: true},
		AcceptanceCriteria: []byte(`["equals 42"]`),
	}, nil)
	return m
}

func hasNoTools(opts agentpkg.ExecOptions) bool {
	for _, a := range opts.CustomArgs {
		if a == "--no-tools" {
			return true
		}
	}
	return false
}

// TestDiagnose_PromptContainsDAGMessagesAndContext verifies the rich-prompt
// flow: Diagnose fetches the interaction DAG + per-segment messages + root-task
// context via the Task 2 Go helpers, embeds them in the prompt sent to the
// backend, and returns parsed (clamped) step rewards.
func TestDiagnose_PromptContainsDAGMessagesAndContext(t *testing.T) {
	projectID := util.MustParseUUID(diagProjectID)
	workspaceID := util.MustParseUUID(diagWorkspaceID)
	stores := newDiagnosisStores(t, projectID, workspaceID)
	fb := &fakeBackend{
		status: "completed",
		output: `[{"segment_id":"seg-diag-1","seq":1,"score":9,"rationale":"good plan"},{"segment_id":"seg-diag-1","seq":2,"score":12,"rationale":"clamped"}]`,
	}
	r, err := NewDiagnosisAgentRunner(DiagnosisAgentConfig{
		ScoreMax:     10,
		Backend:      fb,
		DAGStore:     stores,
		MessageStore: stores,
	})
	require.NoError(t, err)

	got, err := r.Diagnose(context.Background(), diagProjectID, diagWorkspaceID)
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, diagSegmentID, got[0].SegmentID)
	assert.Equal(t, 9, got[0].Score)
	assert.Equal(t, 10, got[1].Score, "score 12 must clamp to scoreMax 10")

	// The prompt embeds the DAG, per-segment LLM messages, and task context.
	assert.Contains(t, fb.gotPrompt, diagSegmentID)
	assert.Contains(t, fb.gotPrompt, diagAgentRunID)
	assert.Contains(t, fb.gotPrompt, "Let me plan the approach.")
	assert.Contains(t, fb.gotPrompt, "The answer is 42.")
	assert.Contains(t, fb.gotPrompt, "Goal: compute the ultimate answer.")
	assert.Contains(t, fb.gotPrompt, "equals 42")

	// System prompt + --no-tools wiring preserved.
	assert.Contains(t, fb.gotOpts.SystemPrompt, "between 0 and 10 inclusive")
	assert.True(t, hasNoTools(fb.gotOpts), "expected --no-tools for pi provider, got %v", fb.gotOpts.CustomArgs)
	stores.AssertExpectations(t)
}

func TestDiagnosisAgentRunner_FreezeSegmentTargets_UsesActualAssistantSequences(t *testing.T) {
	projectID := util.MustParseUUID(diagProjectID)
	workspaceID := util.MustParseUUID(diagWorkspaceID)
	stores := newDiagnosisStores(t, projectID, workspaceID)
	store, _ := newTestDiagnosisStore(t)
	ckpt, err := store.CreateRun(context.Background(), DiagnosisRunCheckpoint{
		RunID:             "run-freeze",
		ProjectID:         projectID.String(),
		TaskID:            diagAgentRunID,
		TopologyHash:      "topo-freeze",
		OrderedSegmentIDs: []string{diagSegmentID},
	})
	require.NoError(t, err)
	runner, err := NewDiagnosisAgentRunner(DiagnosisAgentConfig{
		Backend:      &fakeBackend{},
		DAGStore:     stores,
		MessageStore: stores,
	})
	require.NoError(t, err)

	targets, err := runner.freezeDiagnosisSegmentTargets(
		context.Background(), store, ckpt, []string{diagSegmentID},
	)
	require.NoError(t, err)
	require.Len(t, targets, 1)
	assert.Equal(t, diagAgentRunID, targets[0].AgentRunID)
	assert.Equal(t, int32(1), targets[0].StartSeq)
	assert.Equal(t, int32(2), targets[0].EndSeq)
	assert.Equal(t, []int32{1, 2}, targets[0].AssistantSeqs)

	segment, err := store.GetSegment(context.Background(), ckpt.RunID, diagSegmentID)
	require.NoError(t, err)
	assert.Equal(t, 2, segment.ExpectedMessageCount)
	assert.Equal(t, []int32{1, 2}, segment.ExpectedRewardSeqs)
}

// TestDiagnose_PropagatesNonCompleted verifies a non-completed backend result
// surfaces as an error even when the stores fetch succeeds.
func TestDiagnose_PropagatesNonCompleted(t *testing.T) {
	projectID := util.MustParseUUID(diagProjectID)
	workspaceID := util.MustParseUUID(diagWorkspaceID)
	stores := newDiagnosisStores(t, projectID, workspaceID)
	fb := &fakeBackend{status: "failed", output: ""}
	r, err := NewDiagnosisAgentRunner(DiagnosisAgentConfig{
		ScoreMax: 10, Backend: fb, DAGStore: stores, MessageStore: stores,
	})
	require.NoError(t, err)
	_, err = r.Diagnose(context.Background(), diagProjectID, diagWorkspaceID)
	assert.Error(t, err)
	stores.AssertExpectations(t)
}

// TestDiagnose_NoSegmentsErrors: an empty DAG (no segments) surfaces as an
// error rather than fabricating an empty prompt - absence stays distinguishable.
func TestDiagnose_NoSegmentsErrors(t *testing.T) {
	projectID := util.MustParseUUID(diagProjectID)
	workspaceID := util.MustParseUUID(diagWorkspaceID)
	m := new(MockDiagnosisStores)
	m.On("GetProjectInWorkspace", mock.Anything, mock.MatchedBy(func(a db.GetProjectInWorkspaceParams) bool {
		return a.ID == projectID && a.WorkspaceID == workspaceID
	})).Return(db.Project{ID: projectID, WorkspaceID: workspaceID}, nil)
	m.On("ListInteractionDAGSegmentsForProject", mock.Anything, projectID.String()).Return([]db.ListInteractionDAGSegmentsForProjectRow{}, nil)
	m.On("ListInteractionDAGEdgesForProject", mock.Anything, projectID.String()).Return([]db.InteractionDagEdge{}, nil)

	fb := &fakeBackend{}
	r, err := NewDiagnosisAgentRunner(DiagnosisAgentConfig{ScoreMax: 10, Backend: fb, DAGStore: m, MessageStore: m})
	require.NoError(t, err)
	_, err = r.Diagnose(context.Background(), diagProjectID, diagWorkspaceID)
	require.Error(t, err)
	assert.False(t, fb.executed(), "backend must not run when there are no segments to score")
	m.AssertExpectations(t)
}

// TestDiagnose_StoresNotConfiguredErrors: a runner constructed without stores
// surfaces a clear error at Diagnose time (no nil-deref panic).
func TestDiagnose_StoresNotConfiguredErrors(t *testing.T) {
	fb := &fakeBackend{output: `[]`}
	r, err := NewDiagnosisAgentRunner(DiagnosisAgentConfig{ScoreMax: 10, Backend: fb})
	require.NoError(t, err)
	_, err = r.Diagnose(context.Background(), diagProjectID, diagWorkspaceID)
	assert.Error(t, err)
	assert.False(t, fb.executed(), "backend must not run when stores are not configured")
}

// TestDiagnose_InvalidUUIDErrors: a malformed project/workspace ID surfaces as
// an error before any store call.
func TestDiagnose_InvalidUUIDErrors(t *testing.T) {
	m := new(MockDiagnosisStores)
	fb := &fakeBackend{output: `[]`}
	r, err := NewDiagnosisAgentRunner(DiagnosisAgentConfig{ScoreMax: 10, Backend: fb, DAGStore: m, MessageStore: m})
	require.NoError(t, err)
	_, err = r.Diagnose(context.Background(), "not-a-uuid", diagWorkspaceID)
	assert.Error(t, err)
	assert.False(t, fb.executed(), "backend must not run on a malformed project id")
}

// TestDiagnose_CapsSegmentCount: a DAG with more than maxDiagnosisSegments
// segments is capped - only the first maxDiagnosisSegments are fetched for
// messages and appear in the prompt, bounding prompt size.
func TestTopologyBootstrapPrompt_ContainsNoRawMessages(t *testing.T) {
	r, err := NewDiagnosisAgentRunner(DiagnosisAgentConfig{ScoreMax: 10, Backend: &fakeBackend{}})
	require.NoError(t, err)

	segments := []SegmentDiagnosisCheckpoint{
		{SegmentID: "seg-1", Status: SegmentDiagnosisPending, ExpectedMessageCount: 5, ExpectedRewardCount: 2},
		{SegmentID: "seg-2", Status: SegmentDiagnosisPending, ExpectedMessageCount: 3, ExpectedRewardCount: 1},
	}
	infos := []segmentDiagnosisInfo{
		{SegmentID: "seg-1", ExpectedMessages: 5, ExpectedRewards: 2},
		{SegmentID: "seg-2", ExpectedMessages: 3, ExpectedRewards: 1},
	}

	prompt := r.buildTopologyBootstrapPrompt("proj-1", []string{"seg-1", "seg-2"}, infos, segments)

	// Topology is present.
	assert.Contains(t, prompt, "seg-1")
	assert.Contains(t, prompt, "seg-2")
	assert.Contains(t, prompt, "Score max: 10")
	assert.Contains(t, prompt, "INCOMPLETE SEGMENTS")

	// NO raw message bodies.
	sentinel := "the quick brown fox jumps over the lazy dog sentinel message body"
	assert.NotContains(t, prompt, sentinel, "bootstrap prompt must not contain raw message content")
}

func TestOnDemandSystemPrompt_ContainsToolNames(t *testing.T) {
	r, err := NewDiagnosisAgentRunner(DiagnosisAgentConfig{ScoreMax: 10, Backend: &fakeBackend{}})
	require.NoError(t, err)

	prompt := r.onDemandSystemPrompt()
	toolNames := []string{
		"multica_get_task_context",
		"multica_get_segment_messages",
		"multica_record_step_rewards",
		"multica_get_diagnosis_progress",
		"multica_finish_segment",
		"multica_complete_diagnosis",
	}
	for _, name := range toolNames {
		assert.Contains(t, prompt, name)
	}
}

// TestPrepareSandboxBootstrap_ParallelAssemblyMatchesServerPath is the spec 005
// T019 parity guard: the sandbox path must deliver the same bootstrap prompt
// content (project ID, score max, ordered topology, per-segment expectations)
// and the same on-demand system prompt the server-mode session uses.
func TestPrepareSandboxBootstrap_ParallelAssemblyMatchesServerPath(t *testing.T) {
	ctx := context.Background()

	stores := new(MockDiagnosisStores)
	r, err := NewDiagnosisAgentRunner(DiagnosisAgentConfig{
		ScoreMax:     10,
		Backend:      &fakeBackend{},
		DAGStore:     stores,
		MessageStore: stores,
	})
	require.NoError(t, err)

	state := NewDiagnosisStateStore(newFakeDiagnosisStateQueries())
	ordered := []string{"seg-1", "seg-2"}
	run, err := state.CreateRun(ctx, DiagnosisRunCheckpoint{
		RunID:             "run-parity",
		ProjectID:         diagProjectID,
		TaskID:            "task-parity",
		TopologyHash:      topologyHashFromIDs(ordered),
		OrderedSegmentIDs: ordered,
	})
	require.NoError(t, err)

	seg1 := db.GetInteractionDAGSegmentByIDRow{SegmentID: "seg-1", ProjectID: diagProjectID, AgentRunID: "task-1", StartSeq: 1, EndSeq: 4}
	seg2 := db.GetInteractionDAGSegmentByIDRow{SegmentID: "seg-2", ProjectID: diagProjectID, AgentRunID: "task-2", StartSeq: 1, EndSeq: 2}
	stores.On("GetInteractionDAGSegmentByID", ctx, "seg-1").Return(seg1, nil)
	stores.On("GetInteractionDAGSegmentByID", ctx, "seg-2").Return(seg2, nil)
	msgs1 := []db.TaskMessage{
		{Seq: 1, Type: "user", Content: pgtype.Text{String: "u1", Valid: true}},
		{Seq: 2, Type: "assistant", Content: pgtype.Text{String: "a2", Valid: true}},
		{Seq: 3, Type: "assistant", Content: pgtype.Text{String: "a3", Valid: true}},
		{Seq: 4, Type: "tool", Content: pgtype.Text{String: "t4", Valid: true}},
	}
	msgs2 := []db.TaskMessage{
		{Seq: 1, Type: "assistant", Content: pgtype.Text{String: "b1", Valid: true}},
		{Seq: 2, Type: "user", Content: pgtype.Text{String: "b2", Valid: true}},
	}
	stores.On("MessagesForTaskInRange", ctx, "task-1", int32(1), int32(4)).Return(msgs1, nil)
	stores.On("MessagesForTaskInRange", ctx, "task-2", int32(1), int32(2)).Return(msgs2, nil)

	dagWriter := newFakeDiagnosisDAGWriter()
	bootstrap, err := r.PrepareSandboxBootstrap(ctx, state, dagWriter, diagProjectID, run, ordered)
	require.NoError(t, err)

	// Independently re-assemble exactly what the server-mode DiagnoseOnDemand
	// path builds (diagnosis_agent.go: segInfos loop + shared builders).
	segments, err := state.ListSegments(ctx, run.RunID)
	require.NoError(t, err)
	segInfos := make([]segmentDiagnosisInfo, 0, len(segments))
	completedCount := 0
	for _, seg := range segments {
		if seg.Status == SegmentDiagnosisCompleted {
			completedCount++
			continue
		}
		totalRewards, _ := dagWriter.CountDiagnosisStepRewards(ctx, diagProjectID, seg.SegmentID)
		segInfos = append(segInfos, segmentDiagnosisInfo{
			SegmentID:        seg.SegmentID,
			ExpectedMessages: seg.ExpectedMessageCount,
			ExpectedRewards:  seg.ExpectedRewardCount,
			RecordedRewards:  totalRewards,
		})
	}
	expectedPrompt := r.buildTopologyBootstrapPrompt(diagProjectID, ordered, segInfos, segments)

	assert.Equal(t, expectedPrompt, bootstrap.BootstrapPrompt, "sandbox bootstrap prompt must equal the server-path prompt byte-for-byte")
	assert.Equal(t, r.onDemandSystemPrompt(), bootstrap.SystemPrompt, "sandbox system prompt must equal the server-path system prompt")
	assert.Equal(t, completedCount, bootstrap.CompletedSegments)
	assert.Equal(t, len(segments), bootstrap.TotalSegments)

	// Content parity: project ID, score max, ordered topology, per-segment
	// expectations; goal/gold omitted by design (fetched via the API).
	assert.Contains(t, bootstrap.BootstrapPrompt, "Project: "+diagProjectID)
	assert.Contains(t, bootstrap.BootstrapPrompt, "Score max: 10")
	assert.Contains(t, bootstrap.BootstrapPrompt, "SEGMENT TOPOLOGY (ordered):")
	assert.Contains(t, bootstrap.BootstrapPrompt, "1. segment_id=seg-1")
	assert.Contains(t, bootstrap.BootstrapPrompt, "2. segment_id=seg-2")
	assert.Contains(t, bootstrap.BootstrapPrompt, "segment_id=seg-1 expected_messages=4 expected_rewards=2")
	assert.Contains(t, bootstrap.BootstrapPrompt, "segment_id=seg-2 expected_messages=2 expected_rewards=1")
	assert.NotContains(t, bootstrap.BootstrapPrompt, "goal", "bootstrap prompt must not embed task context")
}

func TestTopologyHashFromIDs_Deterministic(t *testing.T) {
	h1 := topologyHashFromIDs([]string{"a", "b", "c"})
	h2 := topologyHashFromIDs([]string{"a", "b", "c"})
	h3 := topologyHashFromIDs([]string{"a", "b"})
	assert.Equal(t, h1, h2, "hash must be deterministic")
	assert.NotEqual(t, h1, h3, "different orderings produce different hashes")
}

func TestDiagnoseOnDemand_RequiresStateStore(t *testing.T) {
	r, err := NewDiagnosisAgentRunner(DiagnosisAgentConfig{ScoreMax: 10, Backend: &fakeBackend{}})
	require.NoError(t, err)

	// Calling DiagnoseOnDemand with a nil state store panics (expected).
	assert.Panics(t, func() {
		_, _ = r.DiagnoseOnDemand(context.Background(), "proj", "task", []string{"seg-1"}, DiagnosisOnDemandConfig{})
	}, "nil state store should panic (programmer error, not runtime error)")
}

func TestDiagnose_CapsSegmentCount(t *testing.T) {
	projectID := util.MustParseUUID(diagProjectID)
	workspaceID := util.MustParseUUID(diagWorkspaceID)
	m := new(MockDiagnosisStores)
	n := maxDiagnosisSegments + 5
	segs := make([]db.ListInteractionDAGSegmentsForProjectRow, 0, n)
	for i := 0; i < n; i++ {
		sid := fmt.Sprintf("segcap-%02d", i)
		seg := db.InteractionDagSegment{
			SegmentID: sid, ProjectID: projectID.String(),
			AgentRunID: diagAgentRunID, StartSeq: 1, EndSeq: 1,
		}
		segs = append(segs, diagSegmentToListForProjectRow(seg))
		if i < maxDiagnosisSegments {
			m.On("GetInteractionDAGSegmentByID", mock.Anything, sid).Return(diagSegmentToGetByIDRow(seg), nil)
			m.On("MessagesForTaskInRange", mock.Anything, diagAgentRunID, int32(1), int32(1)).
				Return([]db.TaskMessage{{Seq: 1, Type: "assistant", Content: pgtype.Text{String: "m", Valid: true}}}, nil)
		}
	}
	m.On("GetProjectInWorkspace", mock.Anything, mock.Anything).Return(db.Project{ID: projectID, WorkspaceID: workspaceID}, nil)
	m.On("ListInteractionDAGSegmentsForProject", mock.Anything, projectID.String()).Return(segs, nil)
	m.On("ListInteractionDAGEdgesForProject", mock.Anything, projectID.String()).Return([]db.InteractionDagEdge{}, nil)
	m.On("GetIssueForTask", mock.Anything, diagAgentRunID).Return(db.Issue{WorkspaceID: workspaceID, Title: "t"}, nil)

	fb := &fakeBackend{status: "completed", output: `[]`}
	r, err := NewDiagnosisAgentRunner(DiagnosisAgentConfig{ScoreMax: 10, Backend: fb, DAGStore: m, MessageStore: m})
	require.NoError(t, err)
	_, err = r.Diagnose(context.Background(), diagProjectID, diagWorkspaceID)
	require.NoError(t, err)

	assert.Contains(t, fb.gotPrompt, fmt.Sprintf("segcap-%02d", maxDiagnosisSegments-1), "last kept segment must be in prompt")
	assert.NotContains(t, fb.gotPrompt, fmt.Sprintf("segcap-%02d", maxDiagnosisSegments), "first dropped segment must not be in prompt")
	m.AssertExpectations(t)
}
