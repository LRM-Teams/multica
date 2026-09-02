// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Task 22 removed the expired internal compatibility paths from the live
// seams: the one-task-one-segment legacy snapshot bridge (AReaL close/export
// and local task_messages recording), its SegmentIDForAgentRun duplicate
// suppression, the best-effort file-staging feed fired from task terminal
// seams, and the unconditional all-staging retrieval default. The canonical
// UniversalInteractionDAG writer (RecordBoundaryTx in the business
// transaction) is the only live segment writer (AC56); graph staging is fed
// exclusively by the durable publish pipeline, and the default search
// corpus indexes graph nodes plus active Atoms (spec §9.1/§21).
//
// What remains in this file are the live canonical seams.

// EnvDispatchRunChecker checks whether a project was created via env-dispatch.
// The interaction-dag seams use this to decide between AReaL bridge recording
// (training mode) and local task_messages recording (non-training mode).
// Nil means no env-dispatch awareness (no-op for non-proxy tasks).
type EnvDispatchRunChecker interface {
	HasEnvDispatchRun(ctx context.Context, projectID string) (bool, error)
}

// discoverDelegationParent resolves the trained parent task that posted
// triggerCommentID: the comment's agent author's active task on the issue whose
// is no such parent (e.g. a user-authored mention, or the parent is not a
// trained run) so the caller skips the delegation seam. Best-effort: any lookup
// miss yields ok=false.
func (s *TaskService) discoverDelegationParent(ctx context.Context, issueID, triggerCommentID, excludeTaskID pgtype.UUID) (db.AgentInboxEvent, bool) {
	if !triggerCommentID.Valid {
		return db.AgentInboxEvent{}, false
	}
	comment, err := s.Queries.GetComment(ctx, triggerCommentID)
	if err != nil {
		return db.AgentInboxEvent{}, false
	}
	if comment.AuthorType != "agent" || !comment.AuthorID.Valid {
		return db.AgentInboxEvent{}, false
	}
	tasks, err := s.Queries.ListActiveTasksByIssue(ctx, issueID)
	if err != nil {
		return db.AgentInboxEvent{}, false
	}
	for _, t := range tasks {
		if excludeTaskID.Valid && t.ID == excludeTaskID {
			continue
		}
		if t.AgentID == comment.AuthorID && hasArealProxyContext(t.Context) {
			return t, true
		}
	}
	return db.AgentInboxEvent{}, false
}

// RecordTaskOriginLinkageTx links the durable action that woke or spawned a
// Task to that Task's first closed Segment. The source action must already be
// canonical; absent legacy source Segments are left for the Task 22 legacy
// backfill to project.
func RecordTaskOriginLinkageTx(
	ctx context.Context,
	dag *UniversalInteractionDAG,
	q *db.Queries,
	tx pgx.Tx,
	task db.AgentInboxEvent,
) error {
	if dag == nil || q == nil || tx == nil || !task.WorkspaceID.Valid || !task.ID.Valid {
		return errors.New("task origin linkage requires an active universal DAG transaction")
	}

	type linkageOrigin struct {
		actionID pgtype.UUID
		edgeType string
	}
	origin := linkageOrigin{}
	switch {
	case task.TriggerCommentID.Valid:
		origin.actionID = task.TriggerCommentID
		origin.edgeType = EdgeTypeRespondsTo
		if strings.Contains(strings.ToLower(task.Reason), "mention") {
			origin.edgeType = EdgeTypeMention
		}
	case task.SourceMessageID.Valid:
		origin.actionID = task.SourceMessageID
		origin.edgeType = EdgeTypeRespondsTo
		if strings.Contains(strings.ToLower(task.Reason), "mention") {
			origin.edgeType = EdgeTypeMention
		}
	case task.SourceChatMessageID.Valid:
		origin.actionID = task.SourceChatMessageID
		origin.edgeType = EdgeTypeRespondsTo
	case task.ParentTaskID.Valid:
		segments, err := q.ListUniversalDAGSegmentsByTask(ctx, db.ListUniversalDAGSegmentsByTaskParams{
			WorkspaceID: task.WorkspaceID,
			AgentRunID:  task.ParentTaskID,
		})
		if err != nil {
			return err
		}
		if len(segments) == 0 {
			return nil
		}
		source := segments[len(segments)-1]
		return dag.RecordLinkageTx(ctx, q, tx, DAGLinkageInput{
			WorkspaceID:     task.WorkspaceID,
			SourceSegmentID: source.SegmentID,
			TargetRunID:     task.ID,
			Type:            EdgeTypeRespondsTo,
		})
	default:
		return nil
	}

	source, err := q.GetUniversalDAGSegmentByVisibleAction(ctx, db.GetUniversalDAGSegmentByVisibleActionParams{
		WorkspaceID:      task.WorkspaceID,
		VisibleActionKey: pgtype.Text{String: string(DAGCloseMessage) + ":" + origin.actionID.String(), Valid: true},
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return dag.RecordLinkageTx(ctx, q, tx, DAGLinkageInput{
		WorkspaceID:     task.WorkspaceID,
		SourceSegmentID: source.SegmentID,
		TargetRunID:     task.ID,
		Type:            origin.edgeType,
		DurableEventID:  origin.actionID,
	})
}
