// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// MockDiagnosisStores combines the required store interfaces for testing
type MockDiagnosisStores struct {
	mock.Mock
}

func (m *MockDiagnosisStores) ListInteractionDAGSegmentsForProject(ctx context.Context, projectID string) ([]db.ListInteractionDAGSegmentsForProjectRow, error) {
	args := m.Called(ctx, projectID)
	return args.Get(0).([]db.ListInteractionDAGSegmentsForProjectRow), args.Error(1)
}

func (m *MockDiagnosisStores) ListInteractionDAGEdgesForProject(ctx context.Context, projectID string) ([]db.InteractionDagEdge, error) {
	args := m.Called(ctx, projectID)
	return args.Get(0).([]db.InteractionDagEdge), args.Error(1)
}

func (m *MockDiagnosisStores) GetInteractionDAGSegmentByID(ctx context.Context, segmentID string) (db.GetInteractionDAGSegmentByIDRow, error) {
	args := m.Called(ctx, segmentID)
	return args.Get(0).(db.GetInteractionDAGSegmentByIDRow), args.Error(1)
}

func (m *MockDiagnosisStores) GetInteractionDAGSegmentByAgentRun(ctx context.Context, agentRunID string) (db.GetInteractionDAGSegmentByAgentRunRow, error) {
	args := m.Called(ctx, agentRunID)
	return args.Get(0).(db.GetInteractionDAGSegmentByAgentRunRow), args.Error(1)
}

func (m *MockDiagnosisStores) GetLastEndSeqForAgentRun(ctx context.Context, agentRunID string) (int32, error) {
	args := m.Called(ctx, agentRunID)
	return args.Get(0).(int32), args.Error(1)
}

func (m *MockDiagnosisStores) GetMaxTaskMessageSeq(ctx context.Context, taskIDText string) (int32, error) {
	args := m.Called(ctx, taskIDText)
	return args.Get(0).(int32), args.Error(1)
}

func (m *MockDiagnosisStores) GetProjectInWorkspace(ctx context.Context, arg db.GetProjectInWorkspaceParams) (db.Project, error) {
	args := m.Called(ctx, arg)
	return args.Get(0).(db.Project), args.Error(1)
}

func (m *MockDiagnosisStores) MessagesForTaskInRange(ctx context.Context, taskID string, startSeq, endSeq int32) ([]db.TaskMessage, error) {
	args := m.Called(ctx, taskID, startSeq, endSeq)
	return args.Get(0).([]db.TaskMessage), args.Error(1)
}

func (m *MockDiagnosisStores) GetIssueForTask(ctx context.Context, taskID string) (db.Issue, error) {
	args := m.Called(ctx, taskID)
	return args.Get(0).(db.Issue), args.Error(1)
}

func (m *MockDiagnosisStores) UpsertInteractionDAGSessionRun(ctx context.Context, arg db.UpsertInteractionDAGSessionRunParams) error {
	args := m.Called(ctx, arg)
	return args.Error(0)
}

func (m *MockDiagnosisStores) GetInteractionDAGSessionRun(ctx context.Context, sessionID string) (db.InteractionDagSessionRun, error) {
	args := m.Called(ctx, sessionID)
	return args.Get(0).(db.InteractionDagSessionRun), args.Error(1)
}

func (m *MockDiagnosisStores) InsertInteractionDAGSegmentWithSnapshot(ctx context.Context, arg db.InsertInteractionDAGSegmentWithSnapshotParams) error {
	args := m.Called(ctx, arg)
	return args.Error(0)
}

func (m *MockDiagnosisStores) InsertInteractionDAGEdge(ctx context.Context, arg db.InsertInteractionDAGEdgeParams) error {
	args := m.Called(ctx, arg)
	return args.Error(0)
}

func (m *MockDiagnosisStores) InsertInteractionDAGStepReward(ctx context.Context, arg db.InsertInteractionDAGStepRewardParams) error {
	args := m.Called(ctx, arg)
	return args.Error(0)
}

func (m *MockDiagnosisStores) ListInteractionDAGStepRewardsForProject(ctx context.Context, projectID string) ([]db.InteractionDagStepReward, error) {
	args := m.Called(ctx, projectID)
	return args.Get(0).([]db.InteractionDagStepReward), args.Error(1)
}

func (m *MockDiagnosisStores) ListLatestCompletedInteractionDAGDiagnosisTargetsForProject(ctx context.Context, projectID string) ([]db.ListLatestCompletedInteractionDAGDiagnosisTargetsForProjectRow, error) {
	args := m.Called(ctx, projectID)
	return args.Get(0).([]db.ListLatestCompletedInteractionDAGDiagnosisTargetsForProjectRow), args.Error(1)
}

func (m *MockDiagnosisStores) ListInteractionDAGSessionRunsForProject(ctx context.Context, projectID string) ([]db.InteractionDagSessionRun, error) {
	args := m.Called(ctx, projectID)
	return args.Get(0).([]db.InteractionDagSessionRun), args.Error(1)
}

func (m *MockDiagnosisStores) ListInteractionDAGEnvSnapshotsForProject(ctx context.Context, projectID string) ([]db.InteractionDagEnvSnapshot, error) {
	args := m.Called(ctx, projectID)
	return args.Get(0).([]db.InteractionDagEnvSnapshot), args.Error(1)
}

func TestGetSegmentMessages(t *testing.T) {
	ctx := context.Background()

	// Setup test data
	workspaceID := pgtype.UUID{Bytes: [16]byte{0x01}, Valid: true}
	var projectID pgtype.UUID
	_ = projectID.Scan("123e4567-e89b-12d3-a456-426614174000")
	segmentID := "segment-1"
	agentRunID := "agent-run-1"

	segment := db.GetInteractionDAGSegmentByIDRow{
		SegmentID:  segmentID,
		ProjectID:  projectID.String(),
		AgentRunID: agentRunID,
		StartSeq:   1,
		EndSeq:     5,
	}

	messages := []db.TaskMessage{
		{Seq: 1, Content: pgtype.Text{String: "Message 1", Valid: true}},
		{Seq: 2, Content: pgtype.Text{String: "Message 2", Valid: true}},
		{Seq: 3, Content: pgtype.Text{String: "Message 3", Valid: true}},
		{Seq: 4, Content: pgtype.Text{String: "Message 4", Valid: true}},
		{Seq: 5, Content: pgtype.Text{String: "Message 5", Valid: true}},
	}

	t.Run("returns task messages for the segment's seq range", func(t *testing.T) {
		mockStore := new(MockDiagnosisStores)

		// Setup expectations
		mockStore.On("GetInteractionDAGSegmentByID", ctx, segmentID).Return(segment, nil)
		mockStore.On("GetProjectInWorkspace", ctx, mock.MatchedBy(func(arg db.GetProjectInWorkspaceParams) bool {
			return arg.ID.String() == segment.ProjectID && arg.WorkspaceID == workspaceID
		})).Return(db.Project{ID: projectID, WorkspaceID: workspaceID}, nil)
		mockStore.On("MessagesForTaskInRange", ctx, agentRunID, int32(1), int32(5)).Return(messages, nil)

		result, err := GetSegmentMessages(ctx, mockStore, mockStore, workspaceID, segmentID)
		require.NoError(t, err)
		assert.Len(t, result, 5)
		for i, msg := range result {
			assert.Equal(t, int32(i+1), msg.Seq)
		}
		mockStore.AssertExpectations(t)
	})

	t.Run("respects max-bytes budget and truncates", func(t *testing.T) {
		mockStore := new(MockDiagnosisStores)

		// Create long messages
		longContent := "This is a very long message that should be truncated because it exceeds the max message bytes limit. "
		for len(longContent) < maxDiagnosisMessageBytes*2 {
			longContent += longContent
		}

		longMessages := []db.TaskMessage{}
		for i := 1; i <= 5; i++ {
			longMessages = append(longMessages, db.TaskMessage{
				Seq:     int32(i),
				Content: pgtype.Text{String: longContent, Valid: true},
			})
		}

		mockStore.On("GetInteractionDAGSegmentByID", ctx, segmentID).Return(segment, nil)
		mockStore.On("GetProjectInWorkspace", ctx, mock.Anything).Return(db.Project{ID: projectID, WorkspaceID: workspaceID}, nil)
		mockStore.On("MessagesForTaskInRange", ctx, agentRunID, int32(1), int32(5)).Return(longMessages, nil)

		result, err := GetSegmentMessages(ctx, mockStore, mockStore, workspaceID, segmentID)
		require.NoError(t, err)
		assert.NotEmpty(t, result)
		// Each message should be truncated or fit
		for _, msg := range result {
			assert.True(t, msg.Truncated || len(msg.Content) <= maxDiagnosisMessageBytes)
		}
		// Total should be under budget
		total := 0
		for _, msg := range result {
			total += len(msg.Content)
		}
		assert.True(t, total <= maxDiagnosisSegmentBudgetBytes)
		mockStore.AssertExpectations(t)
	})

	t.Run("refuses cross-workspace access", func(t *testing.T) {
		mockStore := new(MockDiagnosisStores)
		otherWorkspaceID := pgtype.UUID{Bytes: [16]byte{0xff}, Valid: true}

		mockStore.On("GetInteractionDAGSegmentByID", ctx, segmentID).Return(segment, nil)
		mockStore.On("GetProjectInWorkspace", ctx, mock.Anything).Return(db.Project{}, pgx.ErrNoRows)

		result, err := GetSegmentMessages(ctx, mockStore, mockStore, otherWorkspaceID, segmentID)
		assert.Error(t, err)
		assert.Nil(t, result)
		mockStore.AssertExpectations(t)
	})

	t.Run("caps at maxDiagnosisSegmentTurns", func(t *testing.T) {
		mockStore := new(MockDiagnosisStores)

		// Segment whose turn range exceeds the turn cap; each message is tiny
		// so the byte budget never binds - the turn cap is the sole constraint.
		cappedSegment := segment
		cappedSegment.EndSeq = 30

		manyMessages := make([]db.TaskMessage, 0, 30)
		for i := 1; i <= 30; i++ {
			manyMessages = append(manyMessages, db.TaskMessage{
				Seq:     int32(i),
				Content: pgtype.Text{String: "m", Valid: true},
			})
		}

		mockStore.On("GetInteractionDAGSegmentByID", ctx, segmentID).Return(cappedSegment, nil)
		mockStore.On("GetProjectInWorkspace", ctx, mock.Anything).Return(db.Project{ID: projectID, WorkspaceID: workspaceID}, nil)
		mockStore.On("MessagesForTaskInRange", ctx, agentRunID, int32(1), int32(30)).Return(manyMessages, nil)

		result, err := GetSegmentMessages(ctx, mockStore, mockStore, workspaceID, segmentID)
		require.NoError(t, err)
		assert.Len(t, result, maxDiagnosisSegmentTurns, "turn budget should cap returned messages at maxDiagnosisSegmentTurns")
		for i, msg := range result {
			assert.Equal(t, int32(i+1), msg.Seq, "first maxDiagnosisSegmentTurns turns returned in seq order")
		}
		mockStore.AssertExpectations(t)
	})
}

func TestGetInteractionDAG(t *testing.T) {
	ctx := context.Background()

	workspaceID := pgtype.UUID{Bytes: [16]byte{0x01}, Valid: true}
	var projectID pgtype.UUID
	_ = projectID.Scan("123e4567-e89b-12d3-a456-426614174000")
	projectIDStr := projectID.String()

	segments := []db.ListInteractionDAGSegmentsForProjectRow{
		{SegmentID: "seg1", ProjectID: projectIDStr},
		{SegmentID: "seg2", ProjectID: projectIDStr},
	}

	edges := []db.InteractionDagEdge{
		{SrcSegmentID: "seg1", DstSegmentID: "seg2", Type: EdgeTypeDelegation},
	}

	t.Run("returns segments and edges for the project", func(t *testing.T) {
		mockStore := new(MockDiagnosisStores)

		// Verify project belongs to workspace
		mockStore.On("GetProjectInWorkspace", ctx, mock.MatchedBy(func(arg db.GetProjectInWorkspaceParams) bool {
			return arg.ID.String() == projectIDStr && arg.WorkspaceID == workspaceID
		})).Return(db.Project{ID: projectID, WorkspaceID: workspaceID}, nil)

		mockStore.On("ListInteractionDAGSegmentsForProject", ctx, projectIDStr).Return(segments, nil)
		mockStore.On("ListInteractionDAGEdgesForProject", ctx, projectIDStr).Return(edges, nil)

		resultSegments, resultEdges, err := GetInteractionDAG(ctx, mockStore, mockStore, workspaceID, projectID)
		require.NoError(t, err)
		assert.Len(t, resultSegments, 2)
		assert.Len(t, resultEdges, 1)
		assert.Equal(t, "seg1", resultSegments[0].SegmentID)
		assert.Equal(t, "seg2", resultSegments[1].SegmentID)
		assert.Equal(t, EdgeTypeDelegation, resultEdges[0].Type)
		mockStore.AssertExpectations(t)
	})
}

// fakeDiagnosisMessagePager is an in-memory DiagnosisMessagePager for Task 2
// paging tests. It holds messages keyed by task_id and supports keyset pagination
// over (seq, id).
type fakeDiagnosisMessagePager struct {
	mu       sync.Mutex
	messages []db.TaskMessage
}

func newFakeDiagnosisMessagePager(t *testing.T) *fakeDiagnosisMessagePager {
	t.Helper()
	return &fakeDiagnosisMessagePager{}
}

func (f *fakeDiagnosisMessagePager) addMessage(t *testing.T, seq int32, typ, content string) {
	t.Helper()
	var id pgtype.UUID
	// Use a deterministic UUID from seq for stable tie-breaking.
	_ = id.Scan(fmt.Sprintf("00000000-0000-0000-0000-%012d", seq))
	f.messages = append(f.messages, db.TaskMessage{
		ID:      id,
		Seq:     seq,
		Type:    typ,
		Content: pgtype.Text{String: content, Valid: true},
	})
}

func (f *fakeDiagnosisMessagePager) PageTaskMessagesInRange(_ context.Context, arg db.PageTaskMessagesInRangeParams) ([]db.TaskMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var result []db.TaskMessage
	for _, m := range f.messages {
		if m.Seq < arg.StartSeq || m.Seq > arg.EndSeq {
			continue
		}
		if m.Seq > arg.LastSeq || (m.Seq == arg.LastSeq && comparePgUUID(m.ID, arg.LastID) > 0) {
			result = append(result, m)
		}
	}
	// Sort by (seq, id) to match the SQL ORDER BY.
	sortTaskMessages(result)
	if len(result) > int(arg.Limit) {
		result = result[:arg.Limit]
	}
	return result, nil
}

func (f *fakeDiagnosisMessagePager) CountTaskMessagesInRange(_ context.Context, arg db.CountTaskMessagesInRangeParams) (int32, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var count int32
	for _, m := range f.messages {
		if m.Seq >= arg.StartSeq && m.Seq <= arg.EndSeq {
			count++
		}
	}
	return count, nil
}

func sortTaskMessages(msgs []db.TaskMessage) {
	for i := 0; i < len(msgs); i++ {
		for j := i + 1; j < len(msgs); j++ {
			if msgs[i].Seq > msgs[j].Seq || (msgs[i].Seq == msgs[j].Seq && comparePgUUID(msgs[i].ID, msgs[j].ID) > 0) {
				msgs[i], msgs[j] = msgs[j], msgs[i]
			}
		}
	}
}

func comparePgUUID(a, b pgtype.UUID) int {
	if !a.Valid && !b.Valid {
		return 0
	}
	if !a.Valid {
		return -1
	}
	if !b.Valid {
		return 1
	}
	for i := 0; i < 16; i++ {
		if a.Bytes[i] < b.Bytes[i] {
			return -1
		}
		if a.Bytes[i] > b.Bytes[i] {
			return 1
		}
	}
	return 0
}

// tamperDiagnosisCursorSegment decodes a cursor, changes its segment_id, and
// re-encodes it with the test key. Used to verify cross-segment cursor rejection.
func tamperDiagnosisCursorSegment(t *testing.T, encoded, newSegmentID string) string {
	t.Helper()
	payload, err := decodeDiagnosisCursor(encoded, testDiagnosisPagerKey)
	require.NoError(t, err)
	payload.SegmentID = newSegmentID
	newEncoded, err := encodeDiagnosisCursor(payload, testDiagnosisPagerKey)
	require.NoError(t, err)
	return newEncoded
}

var testDiagnosisPagerKey = []byte("test-diagnosis-pager-key-32bytes!")

func init() {
	SetDiagnosisPagerKey(func() []byte { return testDiagnosisPagerKey })
}

func TestGetSegmentMessagePage_EmptyCursorFirstPage(t *testing.T) {
	ctx := context.Background()
	// Use the in-memory fake message pager (added in Task 2).
	store := newFakeDiagnosisMessagePager(t)
	// Write enough messages across two pages (default 20/page) to exercise
	// paging with 25 messages.
	for i := 1; i <= 25; i++ {
		store.addMessage(t, int32(i), "assistant", fmt.Sprintf("message-%02d", i))
	}

	page, err := GetSegmentMessagePage(ctx, store, "task-1", "seg-1", 1, 25, "")
	require.NoError(t, err)
	assert.Equal(t, 20, page.FetchedCount, "first page should return up to 20 turns")
	assert.Equal(t, 25, page.ExpectedCount, "expected count is the full segment range")
	assert.False(t, page.Complete, "more pages remain")
	assert.NotEmpty(t, page.NextCursor, "next cursor must be set for the next page")
	assert.Len(t, page.Messages, 20, "first page messages count matches fetches")

	// Fetch second page with the returned cursor.
	page2, err := GetSegmentMessagePage(ctx, store, "task-1", "seg-1", 1, 25, page.NextCursor)
	require.NoError(t, err)
	assert.Equal(t, 25, page2.FetchedCount, "second page: cumulative fetched is 25")
	assert.True(t, page2.Complete, "all messages fetched")
	assert.Empty(t, page2.NextCursor, "terminal cursor is empty")
	assert.Len(t, page2.Messages, 5)
}

func TestGetSegmentMessagePage_ByteLimitNeverSplitsMessage(t *testing.T) {
	ctx := context.Background()
	store := newFakeDiagnosisMessagePager(t)
	// One oversized message (> 24 KiB page budget) must still be returned as a
	// single page rather than splitting mid-message. The message-level cap
	// (maxDiagnosisMessageBytes) still applies.
	huge := strings.Repeat("x", maxDiagnosisSegmentBudgetBytes+1024)
	store.addMessage(t, 1, "assistant", huge)

	page, err := GetSegmentMessagePage(ctx, store, "task-1", "seg-1", 1, 1, "")
	require.NoError(t, err)
	assert.Equal(t, 1, page.FetchedCount, "an oversized message is a single page of 1")
	assert.True(t, page.Complete)
	assert.Len(t, page.Messages, 1)
	// Message-level cap truncates, but the page-level budget does not split.
	assert.True(t, page.Messages[0].Truncated, "oversized message must be truncated")
	assert.LessOrEqual(t, len(page.Messages[0].Content), maxDiagnosisMessageBytes)
}

func TestGetSegmentMessagePage_ReplaySameCursor(t *testing.T) {
	ctx := context.Background()
	store := newFakeDiagnosisMessagePager(t)
	for i := 1; i <= 30; i++ {
		store.addMessage(t, int32(i), "assistant", fmt.Sprintf("msg-%02d", i))
	}

	page1, err := GetSegmentMessagePage(ctx, store, "task-1", "seg-1", 1, 30, "")
	require.NoError(t, err)
	require.False(t, page1.Complete)

	// Replay the empty cursor — must return the same first page.
	replay, err := GetSegmentMessagePage(ctx, store, "task-1", "seg-1", 1, 30, "")
	require.NoError(t, err)
	assert.Equal(t, page1.FetchedCount, replay.FetchedCount)
	assert.Equal(t, page1.NextCursor, replay.NextCursor)
	assert.Len(t, replay.Messages, len(page1.Messages))
}

func TestGetSegmentMessagePage_MalformedCursor(t *testing.T) {
	ctx := context.Background()
	store := newFakeDiagnosisMessagePager(t)
	store.addMessage(t, 1, "assistant", "hello")

	_, err := GetSegmentMessagePage(ctx, store, "task-1", "seg-1", 1, 1, "not-a-valid-cursor")
	assert.Error(t, err)
}

func TestGetSegmentMessagePage_CursorRunMismatch(t *testing.T) {
	ctx := context.Background()
	store := newFakeDiagnosisMessagePager(t)
	for i := 1; i <= 25; i++ {
		store.addMessage(t, int32(i), "assistant", fmt.Sprintf("msg-%02d", i))
	}
	page1, err := GetSegmentMessagePage(ctx, store, "task-1", "seg-1", 1, 25, "")
	require.NoError(t, err)
	require.False(t, page1.Complete)
	require.NotEmpty(t, page1.NextCursor)

	// Tamper with the cursor to point to a different segment — must be rejected.
	tampered := tamperDiagnosisCursorSegment(t, page1.NextCursor, "seg-other")
	_, err = GetSegmentMessagePage(ctx, store, "task-1", "seg-1", 1, 25, tampered)
	assert.Error(t, err)
}

func TestGetSegmentMessagePage_TurnLimit(t *testing.T) {
	ctx := context.Background()
	store := newFakeDiagnosisMessagePager(t)
	for i := 1; i <= 30; i++ {
		store.addMessage(t, int32(i), "assistant", fmt.Sprintf("msg-%02d", i))
	}
	page, err := GetSegmentMessagePage(ctx, store, "task-1", "seg-1", 1, 30, "")
	require.NoError(t, err)
	assert.LessOrEqual(t, page.FetchedCount, maxDiagnosisSegmentTurns)
	assert.False(t, page.Complete, "more pages remain after turn-limited page")
}

func TestGetSegmentMessagePage_SystemMessagesOutsideRange(t *testing.T) {
	ctx := context.Background()
	store := newFakeDiagnosisMessagePager(t)
	// Messages outside [startSeq, endSeq] must not leak into the page.
	store.addMessage(t, 1, "system", "outside-start") // seq < 2
	store.addMessage(t, 2, "assistant", "inside")
	store.addMessage(t, 3, "system", "outside-end") // seq > 2

	page, err := GetSegmentMessagePage(ctx, store, "task-1", "seg-1", 2, 2, "")
	require.NoError(t, err)
	require.Len(t, page.Messages, 1)
	assert.Equal(t, "inside", page.Messages[0].Content)
	assert.True(t, page.Complete)
}

func TestGetTaskContext(t *testing.T) {
	ctx := context.Background()
	workspaceID := pgtype.UUID{Bytes: [16]byte{0x01}, Valid: true}
	otherWorkspaceID := pgtype.UUID{Bytes: [16]byte{0xff}, Valid: true}
	taskID := "task-1"

	t.Run("returns task context with description as goal and acceptance_criteria as gold", func(t *testing.T) {
		mockStore := new(MockDiagnosisStores)

		issue := db.Issue{
			WorkspaceID:        workspaceID,
			Title:              "Test Issue Title",
			Description:        pgtype.Text{String: "Test Issue Description", Valid: true},
			AcceptanceCriteria: []byte(`["criterion 1", "criterion 2"]`),
		}

		mockStore.On("GetIssueForTask", ctx, taskID).Return(issue, nil)

		result, err := GetTaskContext(ctx, mockStore, workspaceID, taskID)
		require.NoError(t, err)
		assert.Equal(t, "Test Issue Description", result.Goal)
		assert.Equal(t, `["criterion 1", "criterion 2"]`, result.GoldContext)
		mockStore.AssertExpectations(t)
	})

	t.Run("returns task context with title as goal when description is empty", func(t *testing.T) {
		mockStore := new(MockDiagnosisStores)

		issue := db.Issue{
			WorkspaceID:        workspaceID,
			Title:              "Test Issue Title",
			Description:        pgtype.Text{Valid: false},
			AcceptanceCriteria: []byte{},
		}

		mockStore.On("GetIssueForTask", ctx, taskID).Return(issue, nil)

		result, err := GetTaskContext(ctx, mockStore, workspaceID, taskID)
		require.NoError(t, err)
		assert.Equal(t, "Test Issue Title", result.Goal)
		assert.Equal(t, "", result.GoldContext)
		mockStore.AssertExpectations(t)
	})

	t.Run("returns pgx.ErrNoRows for cross-workspace access", func(t *testing.T) {
		mockStore := new(MockDiagnosisStores)

		issue := db.Issue{
			WorkspaceID: workspaceID, // wrong workspace
			Title:       "Test Issue",
		}

		mockStore.On("GetIssueForTask", ctx, taskID).Return(issue, nil)

		result, err := GetTaskContext(ctx, mockStore, otherWorkspaceID, taskID)
		assert.Error(t, err)
		assert.Equal(t, pgx.ErrNoRows, err)
		assert.Equal(t, TaskContext{}, result)
		mockStore.AssertExpectations(t)
	})

	t.Run("returns error when task not found", func(t *testing.T) {
		mockStore := new(MockDiagnosisStores)

		mockStore.On("GetIssueForTask", ctx, taskID).Return(db.Issue{}, pgx.ErrNoRows)

		result, err := GetTaskContext(ctx, mockStore, workspaceID, taskID)
		assert.Error(t, err)
		assert.Equal(t, pgx.ErrNoRows, err)
		assert.Equal(t, TaskContext{}, result)
		mockStore.AssertExpectations(t)
	})
}
