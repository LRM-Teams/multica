package handler

import (
	"context"
	"errors"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// syncWendyWorkGraphAfterIssueUpdate updates the graph after an issue mutation
// has committed. Graph failures are intentionally best-effort: the issue
// update is already durable and must not be reported as failed.
func (h *Handler) syncWendyWorkGraphAfterIssueUpdate(ctx context.Context, issue db.Issue) {
	if h.WorkGraph == nil {
		return
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
	for _, connected := range issues {
		node, err := h.Queries.GetWorkNodeByIssue(ctx, db.GetWorkNodeByIssueParams{
			WorkspaceID:   issue.WorkspaceID,
			LinkedIssueID: connected.ID,
		})
		if err != nil {
			slog.Warn("load Wendy work node failed", "issue_id", connected.ID.String(), "error", err)
			continue
		}
		if err := h.WorkGraph.DetectUnlockForNode(ctx, node.ID); err != nil {
			slog.Warn("detect Wendy unlock failed", "issue_id", connected.ID.String(), "error", err)
		}
	}
}

func (h *Handler) syncWendyWorkGraphAfterTaskSuccess(ctx context.Context, task db.AgentTaskQueue) {
	if h.WorkGraph == nil || !task.IssueID.Valid {
		return
	}
	issue, err := h.Queries.GetIssue(ctx, task.IssueID)
	if err != nil {
		slog.Warn("load issue for Wendy task completion hook failed", "task_id", task.ID.String(), "issue_id", task.IssueID.String(), "error", err)
		return
	}
	if err := h.WorkGraph.TouchIssueProgress(ctx, issue.WorkspaceID, issue.ID); err != nil {
		slog.Warn("touch Wendy issue progress failed", "task_id", task.ID.String(), "issue_id", issue.ID.String(), "error", err)
	}
	h.syncWendyWorkGraphAfterIssueUpdate(ctx, issue)
}

func (h *Handler) wendyGraphConnectedIssues(ctx context.Context, issue db.Issue) ([]db.Issue, error) {
	dependencies, err := h.Queries.ListIssueDependenciesByIssue(ctx, issue.ID)
	if err != nil {
		return nil, err
	}
	issues := []db.Issue{issue}
	seen := map[string]struct{}{issue.ID.String(): {}}
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
		}
	}
	return issues, nil
}
