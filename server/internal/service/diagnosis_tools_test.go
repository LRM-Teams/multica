// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/multica-ai/multica/server/pkg/db/generated"
)

// MockDiagnosisStores combines the required store interfaces for testing
type MockDiagnosisStores struct {
	mock.Mock
}

func (m *MockDiagnosisStores) ListInteractionDAGSegmentsForProject(ctx context.Context, projectID string) ([]db.InteractionDAGSegment, error) {
	args := m.Called(ctx, projectID)
	return args.Get(0).([]db.InteractionDAGSegment), args.Error(1)
}

func (m *MockDiagnosisStores) ListInteractionDAGEdgesForProject(ctx context.Context, projectID string) ([]db.InteractionDAGEdge, error) {
	args := m.Called(ctx, projectID)
	return args.Get(0).([]db.InteractionDAGEdge), args.Error(1)
}

func (m *MockDiagnosisStores) GetInteractionDAGSegmentByID(ctx context.Context, segmentID string) (db.InteractionDAGSegment, error) {
	args := m.Called(ctx, segmentID)
	return args.Get(0).(db.InteractionDAGSegment), args.Error(1)
}

func (m *MockDiagnosisStores) GetInteractionDAGSegmentByAgentRun(ctx context.Context, agentRunID string) (db.InteractionDAGSegment, error) {
	args := m.Called(ctx, agentRunID)
	return args.Get(0).(db.InteractionDAGSegment), args.Error(1)
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

func (m *MockDiagnosisStores) GetInteractionDAGSessionRun(ctx context.Context, sessionID string) (db.InteractionDAGSessionRun, error) {
	args := m.Called(ctx, sessionID)
	return args.Get(0).(db.InteractionDAGSessionRun), args.Error(1)
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

func (m *MockDiagnosisStores) ListInteractionDAGStepRewardsForProject(ctx context.Context, projectID string) ([]db.InteractionDAGStepReward, error) {
	args := m.Called(ctx, projectID)
	return args.Get(0).([]db.InteractionDAGStepReward), args.Error(1)
}

func (m *MockDiagnosisStores) ListInteractionDAGSessionRunsForProject(ctx context.Context, projectID string) ([]db.InteractionDAGSessionRun, error) {
	args := m.Called(ctx, projectID)
	return args.Get(0).([]db.InteractionDAGSessionRun), args.Error(1)
}

func (m *MockDiagnosisStores) ListInteractionDAGEnvSnapshotsForProject(ctx context.Context, projectID string) ([]db.InteractionDAGEnvSnapshot, error) {
	args := m.Called(ctx, projectID)
	return args.Get(0).([]db.InteractionDAGEnvSnapshot), args.Error(1)
}

func TestGetSegmentMessages(t *testing.T) {
	ctx := context.Background()

	// Setup test data
	workspaceID := pgtype.UUID{Bytes: [16]byte{0x01}, Valid: true}
	var projectID pgtype.UUID
	_ = projectID.Scan("123e4567-e89b-12d3-a456-426614174000")
	segmentID := "segment-1"
	agentRunID := "agent-run-1"

	segment := db.InteractionDAGSegment{
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

	segments := []db.InteractionDAGSegment{
		{SegmentID: "seg1", ProjectID: projectIDStr},
		{SegmentID: "seg2", ProjectID: projectIDStr},
	}

	edges := []db.InteractionDAGEdge{
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
