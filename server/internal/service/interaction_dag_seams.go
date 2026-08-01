// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"log/slog"

	"github.com/jackc/pgx/v5/pgtype"

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
// delegation edge. One-segment-per-task (interaction_dag.sql.go
// GetInteractionDAGSegmentByAgentRun doc): if the parent already has a segment
// (it delegated earlier), this is a no-op. Best-effort: errors are logged and
// the run continues. envSnapshot is the lean {sandbox_ids, env_state} shape
// resolved by the caller (SandboxRefs are not in hand at the task.go seams).
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
	// One-segment-per-task: skip if the parent already recorded a segment (a
	// prior delegation closed it). SegmentIDForAgentRun returns ("", ErrNoRows)
	// when none exists -> proceed; any non-empty result -> skip.
	if existing, err := s.Training.DAG.SegmentIDForAgentRun(ctx, parentRunID); err == nil && existing != "" {
		return
	}
	segID, err := s.Training.DAG.CloseSegmentForEvent(ctx, projectID, cfg.SessionID, cfg.APIKey, closingEvent, envSnapshot)
	if err != nil {
		slog.Warn("interaction_dag: delegation segment close failed", "task_id", parentRunID, "err", err)
		return
	}
	// If the parent was itself a delegated-to child, link its segment to the
	// grandparent's (the parent's parent) segment.
	if parent.ParentTaskID.Valid {
		if gpSeg, err := s.Training.DAG.SegmentIDForAgentRun(ctx, util.UUIDToString(parent.ParentTaskID)); err == nil && gpSeg != "" {
			if err := s.Training.DAG.AddEdge(ctx, projectID, gpSeg, segID, closingEventDelegation); err != nil {
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
// One-segment-per-task: skipped if the task already closed its segment (it
// delegated earlier in its run). Best-effort. MUST run before the RL session is
// ended (CloseSegmentForEvent exports the just-closed trajectory over the live
// session) - the caller invokes this before RouteTerminalTrainingTask.
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
	if existing, err := s.Training.DAG.SegmentIDForAgentRun(ctx, runID); err == nil && existing != "" {
		return // already closed via an earlier delegation
	}

	// One segment per task: the trajectory is complete once the task is
	// terminal, whether or not it delegated. A task that @-mentioned someone
	// was already closed by the delegation seam at child creation, which the
	// SegmentIDForAgentRun guard above catches — so skipping non-delegating
	// tasks here left the all-agents-idle sweep as the only producer of
	// segments for them, and every rollout that never delegated produced none.
	closingEvent := "" // leaf (root completion) by default
	if task.ParentTaskID.Valid {
		closingEvent = closingEventCompletion
	}
	segID, err := s.Training.DAG.CloseSegmentForEvent(ctx, projectID, cfg.SessionID, cfg.APIKey, closingEvent, envSnapshot)
	if err != nil {
		slog.Warn("interaction_dag: terminal segment close failed", "task_id", runID, "err", err)
		return
	}
	// Delegation edge parent->child, recorded at the child's close.
	if task.ParentTaskID.Valid {
		if parentSeg, err := s.Training.DAG.SegmentIDForAgentRun(ctx, util.UUIDToString(task.ParentTaskID)); err == nil && parentSeg != "" {
			if err := s.Training.DAG.AddEdge(ctx, projectID, parentSeg, segID, closingEventDelegation); err != nil {
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
func (s *TaskService) maybeRecordLocalSegmentForEvent(ctx context.Context, task db.AgentInboxEvent, projectID, closingEvent string, envSnapshot map[string]any) {
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
		return
	}
	runID := util.UUIDToString(task.ID)
	// One-segment-per-task: skip if this task already recorded a segment.
	if existing, err := s.Training.DAG.SegmentIDForAgentRun(ctx, runID); err == nil && existing != "" {
		return
	}
	// Determine closingEvent for the terminal seam.
	if closingEvent == "" {
		if task.ParentTaskID.Valid {
			closingEvent = closingEventCompletion
		}
	}
	issueID := ""
	if task.IssueID.Valid {
		issueID = util.UUIDToString(task.IssueID)
	}
	segID, err := s.Training.DAG.RecordLocalSegmentForEvent(ctx, projectID, runID, issueID, closingEvent, envSnapshot)
	if err != nil {
		slog.Warn("interaction_dag: local segment record failed", "task_id", runID, "err", err)
		return
	}
	// Delegation edge parent->child, recorded at the child's close.
	if task.ParentTaskID.Valid {
		if parentSeg, err := s.Training.DAG.SegmentIDForAgentRun(ctx, util.UUIDToString(task.ParentTaskID)); err == nil && parentSeg != "" {
			if err := s.Training.DAG.AddEdge(ctx, projectID, parentSeg, segID, closingEventDelegation); err != nil {
				slog.Warn("interaction_dag: local delegation edge failed", "err", err)
			}
		}
	}
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
