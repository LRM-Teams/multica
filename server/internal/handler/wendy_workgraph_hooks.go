package handler

import (
	"context"
	"errors"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// syncWendyWorkGraphAfterIssueCreate records the issue in the dependency graph.
// Issue assignment and source-thread system events own delivery to assignees;
// the workgraph must not create a second coordination wake.
func (h *Handler) syncWendyWorkGraphAfterIssueCreate(ctx context.Context, issue db.Issue) {
	h.syncWendyWorkGraphAfterIssueUpdate(ctx, issue)
}

// syncWendyWorkGraphAfterIssueUpdate updates the graph after an issue mutation
// has committed. Graph failures are intentionally best-effort: the issue
// update is already durable and must not be reported as failed.
func (h *Handler) syncWendyWorkGraphAfterIssueUpdate(ctx context.Context, issue db.Issue) {
	if h.WorkGraph == nil {
		return
	}
	_ = h.WorkGraph.SyncRuntimeIssue(ctx, issue)
	if issue.Status == "done" || issue.Status == "cancelled" {
		if archiveErr := h.WorkGraph.ArchiveDerivedAgentForIssue(ctx, uuidToString(issue.WorkspaceID), uuidToString(issue.ID)); archiveErr != nil {
			slog.Warn("archive terminal derived Issue worker failed", "issue_id", issue.ID.String(), "error", archiveErr)
		}
	}

	issues, err := h.wendyGraphConnectedIssues(ctx, issue)
	if err != nil {
		slog.Warn("list connected issues for Wendy work graph failed", "issue_id", issue.ID.String(), "error", err)
		return
	}
	for _, connected := range issues {
		if _, err := h.WorkGraph.SyncIssueNode(ctx, connected); err != nil {
			slog.Warn("sync Wendy issue node failed", "issue_id", connected.ID.String(), "error", err)
		}
	}
	for _, connected := range issues {
		if err := h.WorkGraph.SyncDependenciesForIssue(ctx, issue.WorkspaceID, connected.ID); err != nil {
			slog.Warn("sync Wendy issue dependencies failed", "issue_id", connected.ID.String(), "error", err)
		}
	}
}

func (h *Handler) syncWendyWorkGraphAfterTaskSuccess(ctx context.Context, task db.AgentInboxEvent) {
	if h.WorkGraph == nil || !task.IssueID.Valid {
		return
	}
	_ = h.WorkGraph.CompleteIssueNode(ctx, uuidToString(task.WorkspaceID), uuidToString(task.IssueID))
	issue, err := h.Queries.GetIssue(ctx, task.IssueID)
	if err != nil {
		slog.Warn("load issue for Wendy task completion hook failed", "task_id", task.ID.String(), "issue_id", task.IssueID.String(), "error", err)
		return
	}
	// Only explicitly decomposed children use the automatic review boundary.
	// Unrelated user-created Issues retain their existing lifecycle semantics.
	managed, managedErr := h.WorkGraph.IsDecomposedIssue(ctx, uuidToString(issue.WorkspaceID), uuidToString(issue.ID))
	if managedErr != nil {
		slog.Warn("inspect Issue-DAG ownership failed", "issue_id", issue.ID.String(), "error", managedErr)
	}
	if managed && issue.Status != "done" && issue.Status != "cancelled" {
		if updated, updateErr := h.IssueExecution.UpdateStatus(ctx, issue, "in_review", service.IssueExecutionReconcileOptions{
			TriggerKind: "decomposed_issue_completed",
		}); updateErr == nil {
			issue = updated
		}
	}
	if ready, unlockErr := h.WorkGraph.UnlockIssueDependents(ctx, uuidToString(issue.WorkspaceID), uuidToString(issue.ID)); unlockErr != nil {
		slog.Warn("unlock ordinary issue dependents failed", "issue_id", issue.ID.String(), "error", unlockErr)
	} else {
		h.WorkGraph.DispatchReadyIssues(ctx, uuidToString(issue.WorkspaceID), ready)
	}
	if err := h.WorkGraph.TouchIssueProgress(ctx, issue.WorkspaceID, issue.ID); err != nil {
		slog.Warn("touch Wendy issue progress failed", "task_id", task.ID.String(), "issue_id", issue.ID.String(), "error", err)
	}
	// Real progress clears this agent's escalation level (it did the work).
	if task.AgentID.Valid {
		h.resetNudgeLadderForAgent(ctx, issue.WorkspaceID, task.AgentID)
	}
	h.syncWendyWorkGraphAfterIssueUpdate(ctx, issue)
}

func (h *Handler) wendyGraphConnectedIssues(ctx context.Context, issue db.Issue) ([]db.Issue, error) {
	issues := []db.Issue{issue}
	seen := map[string]struct{}{issue.ID.String(): {}}
	queue := []pgtype.UUID{issue.ID}
	for len(queue) > 0 {
		currentID := queue[0]
		queue = queue[1:]
		dependencies, err := h.Queries.ListIssueDependenciesByIssue(ctx, currentID)
		if err != nil {
			return nil, err
		}
		for _, dependency := range dependencies {
			for _, issueID := range []pgtype.UUID{dependency.IssueID, dependency.DependsOnIssueID} {
				if _, ok := seen[issueID.String()]; ok {
					continue
				}
				connected, err := h.Queries.GetIssue(ctx, issueID)
				if errors.Is(err, pgx.ErrNoRows) {
					continue
				}
				if err != nil {
					return nil, err
				}
				seen[issueID.String()] = struct{}{}
				issues = append(issues, connected)
				queue = append(queue, issueID)
			}
		}
	}
	return issues, nil
}
