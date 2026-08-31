// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/memorygraph"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// interaction_dag closing_event values recorded at the D11 event seams. An empty
// closing_event is a leaf (root-completion) segment, stored as NULL.
const (
	closingEventDelegation = "delegation"
	closingEventCompletion = "completion"
)

// EnvDispatchRunChecker checks whether a project was created via env-dispatch.
// The interaction-dag seams use this to decide between AReaL bridge recording
// (training mode) and local task_messages recording (non-training mode).
// Nil means no env-dispatch awareness (no-op for non-proxy tasks).
type EnvDispatchRunChecker interface {
	HasEnvDispatchRun(ctx context.Context, projectID string) (bool, error)
}

// closeSegmentForDelegation is the normal delegation event seam (D11): the
// trained parent that posted the trigger comment is delegating to a child task.
func (s *TaskService) closeSegmentForDelegation(ctx context.Context, parent db.AgentInboxEvent, projectID string, envSnapshot map[string]any) {
	s.closeSegmentForDelegationEvent(ctx, parent, projectID, closingEventDelegation, envSnapshot)
}

// closeSegmentForDelegationEvent closes the parent's segment for a handoff and,
// if the parent was itself delegated to, records the grandparent->parent
// delegation edge. This retained seam builds a legacy snapshot projection;
// UniversalInteractionDAG owns the live multi-generation lifecycle. Best-effort:
// errors are logged and the run continues. envSnapshot is the lean
// {sandbox_ids, env_state} shape resolved by the caller (SandboxRefs are not in
// hand at the task.go seams).
//
// Routing:
//   - Training task (has areal_proxy context) → AReaL bridge (CloseSegmentForEvent, export)
//   - Non-training env-dispatch task (no proxy, but env_dispatch_run exists) → local
//     task_messages recording via RecordLocalSegmentForEvent
//   - Neither → no-op (ordinary task, not in an env-dispatch project)
func (s *TaskService) closeSegmentForDelegationEvent(ctx context.Context, parent db.AgentInboxEvent, projectID string, closingEvent string, envSnapshot map[string]any) {
	if s.Training == nil || s.Training.DAG == nil || !s.Training.DAG.Enabled() {
		return
	}
	cfg, ok := extractArealProxyConfig(parent.Context)
	if ok {
		s.closeTrainedSegmentForDelegation(ctx, parent, projectID, closingEvent, envSnapshot, cfg)
		return
	}
	// Non-training env-dispatch: record locally via task_messages.
	s.maybeRecordLocalSegmentForEvent(ctx, parent, projectID, closingEvent, envSnapshot)
}

// closeTrainedSegmentForDelegation handles the AReaL bridge path for a training task.
func (s *TaskService) closeTrainedSegmentForDelegation(ctx context.Context, parent db.AgentInboxEvent, projectID, closingEvent string, envSnapshot map[string]any, cfg *arealProxyConfig) {
	parentRunID := util.UUIDToString(parent.ID)
	// Legacy snapshot projection only: suppress a duplicate retained row. This
	// lookup does not allocate or close a UniversalInteractionDAG generation.
	if existing, err := s.Training.DAG.SegmentIDForAgentRun(ctx, parentRunID); err == nil && existing != "" {
		return
	}
	segID, traj, err := s.Training.DAG.CloseSegmentForEvent(ctx, projectID, cfg.SessionID, cfg.APIKey, closingEvent, envSnapshot)
	if err != nil {
		slog.Warn("interaction_dag: delegation segment close failed", "task_id", parentRunID, "err", err)
		return
	}
	// Graph-memory ingest (async, best-effort): the seam forwards the real
	// AReaL trajectory export so the staging summary is built from actual
	// segment content (review R1).
	s.fireSegmentIngest(ctx, memorygraph.SegmentExport{
		SegmentID:    segID,
		AgentRunID:   parentRunID,
		Trajectory:   traj,
		ClosingEvent: closingEvent,
	})
	// If the parent was itself a delegated-to child, link its segment to the
	// grandparent's (the parent's parent) segment.
	if parent.ParentTaskID.Valid {
		if gpSeg, err := s.Training.DAG.SegmentIDForAgentRun(ctx, util.UUIDToString(parent.ParentTaskID)); err == nil && gpSeg != "" {
			if err := s.Training.DAG.AddEdge(ctx, parent.WorkspaceID, gpSeg, segID, EdgeTypeDelegation); err != nil {
				slog.Warn("interaction_dag: delegation grandparent edge failed", "err", err)
			}
		}
	}
}

// closeSegmentForTerminal is the completion/leaf event seam (D11): a trained
// task is completing. It closes the task's segment - closing_event="completion"
// if the task has a parent (was delegated to), "" for a leaf (root) - and, if
// the task has a parent, records the parent->task delegation edge at the child's
// close (the only point where childSeg is known).
//
// Best-effort. MUST run before the RL session is ended because
// CloseSegmentForEvent exports the just-closed trajectory over the live session;
// the caller invokes this before RouteTerminalTrainingTask.
//
// Routing:
//   - Training task (has areal_proxy context) → AReaL bridge (CloseSegmentForEvent, export)
//   - Non-training env-dispatch task (no proxy, but env_dispatch_run exists) → local
//     task_messages recording via RecordLocalSegmentForEvent
//   - Neither → no-op (ordinary task, not in an env-dispatch project)
func (s *TaskService) closeSegmentForTerminal(ctx context.Context, task db.AgentInboxEvent, projectID string, envSnapshot map[string]any) {
	if s.Training == nil || s.Training.DAG == nil || !s.Training.DAG.Enabled() {
		return
	}
	cfg, ok := extractArealProxyConfig(task.Context)
	if ok {
		s.closeTrainedSegmentForTerminal(ctx, task, projectID, envSnapshot, cfg)
		return
	}
	// Non-training env-dispatch: record locally via task_messages.
	s.maybeRecordLocalSegmentForEvent(ctx, task, projectID, "", envSnapshot)
}

// closeTrainedSegmentForTerminal handles the AReaL bridge path for a training task.
//
// T9: a trained task's segment is closed at the handoff point — when the agent
// finishes and its channel message @-mentions other agents (child tasks exist).
// When the agent finishes without @-mentions (no child tasks), the segment is
// left open and closed later by the all-agents-idle end-session sweep.
func (s *TaskService) closeTrainedSegmentForTerminal(ctx context.Context, task db.AgentInboxEvent, projectID string, envSnapshot map[string]any, cfg *arealProxyConfig) {
	runID := util.UUIDToString(task.ID)
	// Legacy snapshot projection only: canonical terminal generation state is
	// owned by UniversalInteractionDAG before this retained bridge runs.
	if existing, err := s.Training.DAG.SegmentIDForAgentRun(ctx, runID); err == nil && existing != "" {
		return
	}

	closingEvent := "" // leaf (root completion) by default
	if task.ParentTaskID.Valid {
		closingEvent = closingEventCompletion
	}
	segID, traj, err := s.Training.DAG.CloseSegmentForEvent(ctx, projectID, cfg.SessionID, cfg.APIKey, closingEvent, envSnapshot)
	if err != nil {
		slog.Warn("interaction_dag: terminal segment close failed", "task_id", runID, "err", err)
		return
	}
	// Graph-memory ingest (async, best-effort): the seam forwards the real
	// AReaL trajectory export so the staging summary is built from actual
	// segment content (review R1).
	s.fireSegmentIngest(ctx, memorygraph.SegmentExport{
		SegmentID:    segID,
		AgentRunID:   runID,
		Trajectory:   traj,
		ClosingEvent: closingEvent,
	})
	// Delegation edge parent->child, recorded at the child's close.
	if task.ParentTaskID.Valid {
		if parentSeg, err := s.Training.DAG.SegmentIDForAgentRun(ctx, util.UUIDToString(task.ParentTaskID)); err == nil && parentSeg != "" {
			if err := s.Training.DAG.AddEdge(ctx, task.WorkspaceID, parentSeg, segID, EdgeTypeDelegation); err != nil {
				slog.Warn("interaction_dag: delegation edge failed", "err", err)
			}
		}
	}
}

// maybeRecordLocalSegmentForEvent checks whether the task belongs to an
// env-dispatch project and, if so, records a local segment via
// RecordLocalSegmentForEvent. The task is non-training (no areal_proxy context).
// Zero AReaL client calls are made in this path. Recording is best-effort:
// errors are logged and the run continues.
//
// Tasks that are not env-dispatch (including channel conversations without a
// project) fall through to maybeRecordChannelConversationSegment, so ordinary
// channel conversations still produce a segment for graph-memory staging.
func (s *TaskService) maybeRecordLocalSegmentForEvent(ctx context.Context, task db.AgentInboxEvent, projectID, closingEvent string, envSnapshot map[string]any) {
	if projectID == "" {
		// No project to check: an ordinary channel conversation.
		s.maybeRecordChannelConversationSegment(ctx, task, closingEvent)
		return
	}
	if s.EnvDispatchCheck == nil {
		return
	}
	isDispatch, err := s.EnvDispatchCheck.HasEnvDispatchRun(ctx, projectID)
	if err != nil {
		slog.Warn("interaction_dag: env_dispatch check failed",
			"task_id", util.UUIDToString(task.ID),
			"project_id", projectID,
			"error", err,
		)
		return
	}
	if !isDispatch {
		s.maybeRecordChannelConversationSegment(ctx, task, closingEvent)
		return
	}
	runID := util.UUIDToString(task.ID)
	// Legacy snapshot projection only; this lookup is not a canonical lifecycle
	// decision and remains solely until the Task 4 projection replacement.
	if existing, err := s.Training.DAG.SegmentIDForAgentRun(ctx, runID); err == nil && existing != "" {
		return
	}
	// Determine closingEvent for the retained snapshot projection.
	if closingEvent == "" {
		if task.ParentTaskID.Valid {
			closingEvent = closingEventCompletion
		}
	}
	issueID := ""
	if task.IssueID.Valid {
		issueID = util.UUIDToString(task.IssueID)
	}
	segID, traj, err := s.Training.DAG.RecordLocalSegmentForEvent(ctx, projectID, runID, issueID, closingEvent, envSnapshot)
	if err != nil {
		slog.Warn("interaction_dag: local segment record failed", "task_id", runID, "err", err)
		return
	}
	// Graph-memory ingest (async, best-effort): the seam forwards the
	// allowlisted task_message snapshot so the staging summary is built from
	// actual segment content (review R1).
	s.fireSegmentIngest(ctx, memorygraph.SegmentExport{
		SegmentID:    segID,
		AgentRunID:   runID,
		Trajectory:   traj,
		ClosingEvent: closingEvent,
	})
	// Delegation edge parent->child, recorded at the child's close.
	if task.ParentTaskID.Valid {
		if parentSeg, err := s.Training.DAG.SegmentIDForAgentRun(ctx, util.UUIDToString(task.ParentTaskID)); err == nil && parentSeg != "" {
			if err := s.Training.DAG.AddEdge(ctx, task.WorkspaceID, parentSeg, segID, EdgeTypeDelegation); err != nil {
				slog.Warn("interaction_dag: local delegation edge failed", "err", err)
			}
		}
	}
}

// channelStagingFeedOnce dedups the post-commit staging feed per canonical
// segment id within this process. A repeated terminal close of the same task
// must not double-feed the reviewer; across restarts the staging store's
// immutable segment write is the durable backstop.
var channelStagingFeedOnce sync.Map

// maybeRecordChannelConversationSegment feeds an ordinary channel conversation
// (non-training, non-env-dispatch) into graph-memory staging, so learning in a
// channel reaches the reviewer's staging area and consolidation has material
// to merge.
//
// Under the canonical 454 schema this seam is write-free: the owning terminal
// transaction already closed the task's universal DAG segment (Task 3), so
// there is no legacy segment row to record here. The seam resolves the
// canonical segment id and forwards the allowlisted task_message snapshot
// (never the legacy segment body) through the ingest hook, which resolves the
// task's channel and routes the summary into the channel-scoped graph.
//
// Gated on memory_type=graph: legacy workspaces keep the historical no-op.
// Tasks without a channel or without a canonical segment also stay no-op.
func (s *TaskService) maybeRecordChannelConversationSegment(ctx context.Context, task db.AgentInboxEvent, closingEvent string) {
	if !task.ChannelID.Valid {
		return
	}
	if rt := resolveGraphMemoryType(ctx, s.Queries, task.WorkspaceID, graphMemoryEnvMemoryType()); rt != "graph" {
		return
	}
	runID := util.UUIDToString(task.ID)
	segID, err := s.Training.DAG.SegmentIDForAgentRun(ctx, runID)
	if err != nil {
		slog.Warn("interaction_dag: channel conversation segment lookup failed", "task_id", runID, "err", err)
		return
	}
	if segID == "" {
		// No canonical segment: the terminal close never committed for this
		// task (or predates the canonical wiring); nothing to feed.
		return
	}
	if _, loaded := channelStagingFeedOnce.LoadOrStore(segID, struct{}{}); loaded {
		return
	}
	traj, err := s.channelTaskMessagesTrajectory(ctx, runID)
	if err != nil {
		// Release the in-process guard so a later close can retry the feed.
		channelStagingFeedOnce.Delete(segID)
		slog.Warn("interaction_dag: channel conversation trajectory read failed", "task_id", runID, "err", err)
		return
	}
	s.fireSegmentIngest(ctx, memorygraph.SegmentExport{
		SegmentID:    segID,
		AgentRunID:   runID,
		Trajectory:   traj,
		ClosingEvent: closingEvent,
	})
}

// channelTaskMessagesTrajectory reads the task's persisted task_messages
// through the seam message store and serializes the allowlisted fields, the
// same projection RecordLocalSegmentForEvent uses for its legacy snapshot.
// The legacy segment body is never read: legacy rows are unverified and are
// never surfaced downstream.
func (s *TaskService) channelTaskMessagesTrajectory(ctx context.Context, runID string) (json.RawMessage, error) {
	dag := s.Training.DAG
	if dag == nil || dag.store == nil || dag.msgs == nil {
		return nil, errors.New("interaction_dag: message store not configured")
	}
	endSeq, err := dag.store.GetMaxTaskMessageSeq(ctx, runID)
	if err != nil {
		return nil, fmt.Errorf("interaction_dag: read max task_message seq for %s: %w", runID, err)
	}
	if endSeq <= 0 {
		return nil, errors.New("interaction_dag: no task messages to feed")
	}
	msgs, err := dag.msgs.MessagesForTaskInRange(ctx, db.MessagesForTaskInRangeParams{
		TaskID:   runID,
		StartSeq: 1,
		EndSeq:   endSeq,
	})
	if err != nil {
		return nil, fmt.Errorf("interaction_dag: read messages for %s [1,%d]: %w", runID, endSeq, err)
	}
	if len(msgs) == 0 {
		return nil, errors.New("interaction_dag: no task messages in range")
	}
	return json.RawMessage(serializeLocalTrajectory(msgs)), nil
}

// leanEnvSnapshot builds the refs-only env snapshot available at the task.go
// seams: sandbox_ids from project.env_id -> environment.sandbox_ids, else [].
// issue_snapshot_id is omitted (NULL) - no snapshot capture (F-independence);
// env_state is {}. Returns a safe empty snapshot on any lookup miss so a missing
// environment never blocks segment recording (best-effort).
func (s *TaskService) leanEnvSnapshot(ctx context.Context, projectID pgtype.UUID) map[string]any {
	snap := map[string]any{"sandbox_ids": []string{}, "env_state": map[string]any{}}
	if !projectID.Valid {
		return snap
	}
	proj, err := s.Queries.GetProject(ctx, projectID)
	if err != nil {
		return snap
	}
	if !proj.EnvID.Valid {
		return snap
	}
	env, err := s.Queries.GetEnvironment(ctx, db.GetEnvironmentParams{ID: proj.EnvID, WorkspaceID: proj.WorkspaceID})
	if err != nil {
		return snap
	}
	snap["sandbox_ids"] = env.SandboxIds
	return snap
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
// canonical; absent legacy source Segments are left for later reconciliation.
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
