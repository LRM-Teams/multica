// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/pkg/db/generated"
)

// Budget constants mirroring evolution_review_provider.go:
// - maxDiagnosisMessageBytes mirrors maxEvolutionReviewFileBytes (8KB)
// - maxDiagnosisSegmentBudgetBytes mirrors maxEvolutionReviewContentBudgetBytes (24KB)
// - maxDiagnosisSegmentTurns mirrors maxEvolutionReviewListItems (20)
const (
	maxDiagnosisMessageBytes       = 8 * 1024
	maxDiagnosisSegmentBudgetBytes = 24 * 1024
	maxDiagnosisSegmentTurns       = 20
)

// SegmentRow represents a segment in the DAG
type SegmentRow struct {
	SegmentID  string
	AgentRunID string
	StartSeq   int32
	EndSeq     int32
}

// EdgeRow represents an edge between two segments
type EdgeRow struct {
	SrcSegmentID string
	DstSegmentID string
	Type         string
}

// MessageRow represents a truncated task message with truncation indicator
type MessageRow struct {
	Seq       int32
	Type      string
	Content   string
	Truncated bool
}

// TaskContext contains minimal task context (goal/gold when available)
type TaskContext struct {
	Goal        string
	GoldContext string
}

// GetInteractionDAG returns the interaction DAG segments and edges for a project,
// enforcing workspace scoping.
func GetInteractionDAG(
	ctx context.Context,
	store InteractionDAGStore,
	projectStore MessageStore,
	workspaceID pgtype.UUID,
	projectID pgtype.UUID,
) ([]SegmentRow, []EdgeRow, error) {
	// Verify the project belongs to the workspace
	_, err := projectStore.GetProjectInWorkspace(ctx, db.GetProjectInWorkspaceParams{
		ID:          projectID,
		WorkspaceID: workspaceID,
	})
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil, pgx.ErrNoRows
		}
		return nil, nil, err
	}

	projectIDStr := projectID.String()

	segments, err := store.ListInteractionDAGSegmentsForProject(ctx, projectIDStr)
	if err != nil {
		return nil, nil, err
	}

	edges, err := store.ListInteractionDAGEdgesForProject(ctx, projectIDStr)
	if err != nil {
		return nil, nil, err
	}

	// Convert to our row types
	segmentRows := make([]SegmentRow, 0, len(segments))
	for _, seg := range segments {
		segmentRows = append(segmentRows, SegmentRow{
			SegmentID:  seg.SegmentID,
			AgentRunID: seg.AgentRunID,
			StartSeq:   seg.StartSeq,
			EndSeq:     seg.EndSeq,
		})
	}

	edgeRows := make([]EdgeRow, 0, len(edges))
	for _, edge := range edges {
		edgeRows = append(edgeRows, EdgeRow{
			SrcSegmentID: edge.SrcSegmentID,
			DstSegmentID: edge.DstSegmentID,
			Type:         edge.Type,
		})
	}

	return segmentRows, edgeRows, nil
}

// GetSegmentMessages returns task messages for a segment, truncating to stay within budget,
// and enforcing workspace scoping.
func GetSegmentMessages(
	ctx context.Context,
	store InteractionDAGStore,
	messageStore MessageStore,
	workspaceID pgtype.UUID,
	segmentID string,
) ([]MessageRow, error) {
	// Get the segment
	segment, err := store.GetInteractionDAGSegmentByID(ctx, segmentID)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, pgx.ErrNoRows
		}
		return nil, err
	}

	// Parse project ID from segment and verify it belongs to the workspace
	var projectID pgtype.UUID
	err = projectID.Scan(segment.ProjectID)
	if err != nil {
		return nil, err
	}

	_, err = messageStore.GetProjectInWorkspace(ctx, db.GetProjectInWorkspaceParams{
		ID:          projectID,
		WorkspaceID: workspaceID,
	})
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, pgx.ErrNoRows
		}
		return nil, err
	}

	// Get messages for the task in the segment's seq range
	messages, err := messageStore.MessagesForTaskInRange(ctx, segment.AgentRunID, segment.StartSeq, segment.EndSeq)
	if err != nil {
		return nil, err
	}

	// Convert and truncate messages
	result := make([]MessageRow, 0, len(messages))
	totalBytes := 0
	turnCount := 0

	for _, msg := range messages {
		// Check if adding this message would exceed the turn budget
		if turnCount >= maxDiagnosisSegmentTurns {
			break
		}

		content := ""
		if msg.Content.Valid {
			content = msg.Content.String
		}

		truncated := false
		if len(content) > maxDiagnosisMessageBytes {
			content = truncateUTF8Bytes(content, maxDiagnosisMessageBytes)
			truncated = true
		}

		// Check if adding this message would exceed the total budget
		if totalBytes+len(content) > maxDiagnosisSegmentBudgetBytes {
			// Can't add this message without exceeding budget
			break
		}

		result = append(result, MessageRow{
			Seq:       msg.Seq,
			Type:      msg.Type,
			Content:   content,
			Truncated: truncated,
		})
		totalBytes += len(content)
		turnCount++
	}

	return result, nil
}

// GetTaskContext returns task context (goal/gold) for a task, enforcing workspace scoping.
// Goal is mapped from issue.description if present, otherwise issue.title.
// GoldContext is mapped from issue.acceptance_criteria if present, otherwise empty string.
func GetTaskContext(
	ctx context.Context,
	messageStore MessageStore,
	workspaceID pgtype.UUID,
	taskID string,
) (TaskContext, error) {
	issue, err := messageStore.GetIssueForTask(ctx, taskID)
	if err != nil {
		return TaskContext{}, err
	}

	// Enforce workspace scoping
	if issue.WorkspaceID != workspaceID {
		return TaskContext{}, pgx.ErrNoRows
	}

	// Map fields to TaskContext
	goal := ""
	if issue.Description.Valid && issue.Description.String != "" {
		goal = issue.Description.String
	} else {
		goal = issue.Title
	}

	goldContext := ""
	if len(issue.AcceptanceCriteria) > 0 {
		// acceptance_criteria is JSONB, convert to string
		goldContext = string(issue.AcceptanceCriteria)
	}

	return TaskContext{
		Goal:        goal,
		GoldContext: goldContext,
	}, nil
}
