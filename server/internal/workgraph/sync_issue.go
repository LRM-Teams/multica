package workgraph

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// SyncIssueNode upserts an issue-backed node and resolves its incoming waits
// when the issue is terminal.
func (s *Store) SyncIssueNode(ctx context.Context, issue db.Issue) (db.WorkNode, error) {
	ownerType, ownerID := issueOwner(issue)
	status := issueNodeStatus(issue.Status)

	node, err := s.queries.UpsertIssueWorkNode(ctx, db.UpsertIssueWorkNodeParams{
		WorkspaceID:      issue.WorkspaceID,
		Title:            issue.Title,
		Description:      issue.Description.String,
		OwnerType:        ownerType,
		OwnerID:          ownerID,
		Status:           status,
		PrimaryChannelID: pgtype.UUID{},
		IssueID:          issue.ID,
	})
	if err != nil {
		return db.WorkNode{}, fmt.Errorf("upsert issue work node: %w", err)
	}

	if isTerminalNodeStatus(status) {
		if err := s.resolveIncomingWaits(ctx, issue.WorkspaceID, issue.ID, node.ID); err != nil {
			return db.WorkNode{}, err
		}
		return node, nil
	}

	if err := s.RecomputeIssueNodeStatus(ctx, node.ID); err != nil {
		return db.WorkNode{}, err
	}
	return s.queries.GetWorkNodeByIssue(ctx, db.GetWorkNodeByIssueParams{
		WorkspaceID:   issue.WorkspaceID,
		LinkedIssueID: issue.ID,
	})
}

// SyncDependenciesForIssue maps issue dependencies involving issueID into
// waits_on edges. Related dependencies are intentionally excluded.
func (s *Store) SyncDependenciesForIssue(ctx context.Context, workspaceID, issueID pgtype.UUID) error {
	dependencies, err := s.queries.ListIssueDependenciesByIssue(ctx, issueID)
	if err != nil {
		return fmt.Errorf("list issue dependencies: %w", err)
	}

	type waiterDependencies struct {
		node            db.WorkNode
		prerequisiteIDs map[string]struct{}
	}
	waiters := make(map[string]*waiterDependencies)
	addWaiter := func(node db.WorkNode) *waiterDependencies {
		key := node.ID.String()
		if waiter, ok := waiters[key]; ok {
			return waiter
		}
		waiter := &waiterDependencies{
			node:            node,
			prerequisiteIDs: make(map[string]struct{}),
		}
		waiters[key] = waiter
		return waiter
	}

	issueNode, err := s.queries.GetWorkNodeByIssue(ctx, db.GetWorkNodeByIssueParams{
		WorkspaceID:   workspaceID,
		LinkedIssueID: issueID,
	})
	if err == nil {
		addWaiter(issueNode)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("load issue work node: %w", err)
	}

	for _, dependency := range dependencies {
		waiterIssueID, prerequisiteIssueID, ok := waitsOnIssues(dependency)
		if !ok {
			continue
		}

		waiter, err := s.queries.GetWorkNodeByIssue(ctx, db.GetWorkNodeByIssueParams{
			WorkspaceID:   workspaceID,
			LinkedIssueID: waiterIssueID,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return fmt.Errorf("load waiter work node: %w", err)
		}
		prerequisite, err := s.queries.GetWorkNodeByIssue(ctx, db.GetWorkNodeByIssueParams{
			WorkspaceID:   workspaceID,
			LinkedIssueID: prerequisiteIssueID,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return fmt.Errorf("load prerequisite work node: %w", err)
		}
		waiterDependencies := addWaiter(waiter)
		waiterDependencies.prerequisiteIDs[prerequisite.ID.String()] = struct{}{}

		evidence, err := json.Marshal(map[string]string{
			"issue_dependency_id": dependency.ID.String(),
			"type":                dependency.Type,
		})
		if err != nil {
			return fmt.Errorf("encode dependency evidence: %w", err)
		}
		if _, err := s.queries.UpsertOpenWaitsOnEdge(ctx, db.UpsertOpenWaitsOnEdgeParams{
			WorkspaceID: workspaceID,
			FromNodeID:  waiter.ID,
			ToNodeID:    prerequisite.ID,
			Evidence:    evidence,
		}); err != nil {
			return fmt.Errorf("upsert waits_on edge: %w", err)
		}

		if isTerminalNodeStatus(prerequisite.Status) {
			if _, err := s.queries.ResolveWaitsOnEdge(ctx, db.ResolveWaitsOnEdgeParams{
				WorkspaceID: workspaceID,
				FromNodeID:  waiter.ID,
				ToNodeID:    prerequisite.ID,
			}); err != nil {
				return fmt.Errorf("resolve terminal prerequisite edge: %w", err)
			}
		}
	}

	for _, waiter := range waiters {
		openEdges, err := s.queries.ListOpenWaitsOnFromNode(ctx, db.ListOpenWaitsOnFromNodeParams{
			WorkspaceID: workspaceID,
			FromNodeID:  waiter.node.ID,
		})
		if err != nil {
			return fmt.Errorf("list open waits_on edges: %w", err)
		}
		for _, edge := range openEdges {
			if _, ok := waiter.prerequisiteIDs[edge.ToNodeID.String()]; ok {
				continue
			}
			if _, err := s.queries.ResolveWaitsOnEdge(ctx, db.ResolveWaitsOnEdgeParams{
				WorkspaceID: workspaceID,
				FromNodeID:  waiter.node.ID,
				ToNodeID:    edge.ToNodeID,
			}); err != nil {
				return fmt.Errorf("resolve stale waits_on edge: %w", err)
			}
		}
		if err := s.RecomputeIssueNodeStatus(ctx, waiter.node.ID); err != nil {
			return err
		}
	}
	return nil
}

// RecomputeIssueNodeStatus applies the dependency-derived status for a
// non-terminal issue node.
func (s *Store) RecomputeIssueNodeStatus(ctx context.Context, nodeID pgtype.UUID) error {
	var workspaceID pgtype.UUID
	var status string
	err := s.pool.QueryRow(ctx, `
		SELECT workspace_id, status
		FROM work_node
		WHERE id = $1 AND kind = 'issue'
	`, nodeID).Scan(&workspaceID, &status)
	if err != nil {
		return fmt.Errorf("load issue work node: %w", err)
	}
	if isTerminalNodeStatus(status) {
		return nil
	}

	unresolved, err := s.queries.CountOpenUnresolvedWaitsOn(ctx, db.CountOpenUnresolvedWaitsOnParams{
		WorkspaceID: workspaceID,
		FromNodeID:  nodeID,
	})
	if err != nil {
		return fmt.Errorf("count unresolved waits: %w", err)
	}

	nextStatus := workNodeStatusActive
	if unresolved > 0 {
		nextStatus = workNodeStatusWaiting
	}
	if _, err := s.pool.Exec(ctx, `
		UPDATE work_node
		SET status = $1, updated_at = now()
		WHERE id = $2 AND workspace_id = $3
	`, nextStatus, nodeID, workspaceID); err != nil {
		return fmt.Errorf("update issue work node status: %w", err)
	}
	return nil
}

func (s *Store) resolveIncomingWaits(ctx context.Context, workspaceID, prerequisiteIssueID, prerequisiteNodeID pgtype.UUID) error {
	dependencies, err := s.queries.ListIssueDependenciesByIssue(ctx, prerequisiteIssueID)
	if err != nil {
		return fmt.Errorf("list incoming dependencies: %w", err)
	}
	for _, dependency := range dependencies {
		waiterIssueID, derivedPrerequisiteIssueID, ok := waitsOnIssues(dependency)
		if !ok || derivedPrerequisiteIssueID != prerequisiteIssueID {
			continue
		}
		waiter, err := s.queries.GetWorkNodeByIssue(ctx, db.GetWorkNodeByIssueParams{
			WorkspaceID:   workspaceID,
			LinkedIssueID: waiterIssueID,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return fmt.Errorf("load waiting work node: %w", err)
		}
		if _, err := s.queries.ResolveWaitsOnEdge(ctx, db.ResolveWaitsOnEdgeParams{
			WorkspaceID: workspaceID,
			FromNodeID:  waiter.ID,
			ToNodeID:    prerequisiteNodeID,
		}); err != nil {
			return fmt.Errorf("resolve waits_on edge: %w", err)
		}
		if err := s.RecomputeIssueNodeStatus(ctx, waiter.ID); err != nil {
			return err
		}
	}
	return nil
}

func issueOwner(issue db.Issue) (string, pgtype.UUID) {
	if !issue.AssigneeID.Valid || !issue.AssigneeType.Valid {
		return ownerTypeUnassigned, pgtype.UUID{}
	}
	switch issue.AssigneeType.String {
	case ownerTypeAgent, ownerTypeMember:
		return issue.AssigneeType.String, issue.AssigneeID
	default:
		return ownerTypeUnassigned, pgtype.UUID{}
	}
}

func issueNodeStatus(issueStatus string) string {
	switch issueStatus {
	case workNodeStatusDone:
		return workNodeStatusDone
	case workNodeStatusCancelled:
		return workNodeStatusCancelled
	default:
		return workNodeStatusActive
	}
}

func waitsOnIssues(dependency db.IssueDependency) (waiterIssueID, prerequisiteIssueID pgtype.UUID, ok bool) {
	switch dependency.Type {
	case issueDependencyBlockedBy:
		return dependency.IssueID, dependency.DependsOnIssueID, true
	case issueDependencyBlocks:
		return dependency.DependsOnIssueID, dependency.IssueID, true
	default:
		return pgtype.UUID{}, pgtype.UUID{}, false
	}
}

func isTerminalNodeStatus(status string) bool {
	return status == workNodeStatusDone || status == workNodeStatusCancelled
}
