package workgraph

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// DetectUnlockForNode enqueues an unlock handoff only after all of a waiter
// node's prerequisites have resolved.
func (s *Store) DetectUnlockForNode(ctx context.Context, waiterNodeID pgtype.UUID) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin unlock detection transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	queries := s.queries.WithTx(tx)
	waiter, err := loadWorkNodeForUpdate(ctx, tx, waiterNodeID)
	if err != nil {
		return fmt.Errorf("lock waiter node: %w", err)
	}
	if isTerminalNodeStatus(waiter.Status) {
		return nil
	}

	unresolved, err := queries.CountOpenUnresolvedWaitsOn(ctx, db.CountOpenUnresolvedWaitsOnParams{
		WorkspaceID: waiter.WorkspaceID,
		FromNodeID:  waiter.ID,
	})
	if err != nil {
		return fmt.Errorf("count unresolved waits: %w", err)
	}
	if unresolved > 0 {
		return nil
	}

	hasWaitsOn, err := queries.HasAnyWaitsOnEdge(ctx, db.HasAnyWaitsOnEdgeParams{
		WorkspaceID: waiter.WorkspaceID,
		FromNodeID:  waiter.ID,
	})
	if err != nil {
		return fmt.Errorf("check waiter dependencies: %w", err)
	}
	if !hasWaitsOn {
		return nil
	}

	if waiter.OwnerType != ownerTypeAgent || !waiter.OwnerID.Valid {
		slog.DebugContext(ctx, "skipping unlock handoff for non-agent owner",
			"work_node_id", waiter.ID.String(),
			"owner_type", waiter.OwnerType,
		)
		return nil
	}
	isSupervisor, err := isWorkspaceSupervisor(ctx, queries, waiter.WorkspaceID, waiter.OwnerID)
	if err != nil {
		return err
	}
	if isSupervisor {
		slog.DebugContext(ctx, "skipping unlock handoff for workspace supervisor",
			"work_node_id", waiter.ID.String(),
			"agent_id", waiter.OwnerID.String(),
		)
		return nil
	}

	prerequisiteIDs, err := queries.ListResolvedWaitsOnPrerequisiteIDs(ctx, db.ListResolvedWaitsOnPrerequisiteIDsParams{
		WorkspaceID: waiter.WorkspaceID,
		FromNodeID:  waiter.ID,
	})
	if err != nil {
		return fmt.Errorf("list resolved prerequisites: %w", err)
	}
	sort.Slice(prerequisiteIDs, func(i, j int) bool {
		return prerequisiteIDs[i].String() < prerequisiteIDs[j].String()
	})
	prerequisiteIDStrings := make([]string, len(prerequisiteIDs))
	for i, prerequisiteID := range prerequisiteIDs {
		prerequisiteIDStrings[i] = prerequisiteID.String()
	}

	relatedNodeIDs := make([]pgtype.UUID, 0, len(prerequisiteIDs)+1)
	relatedNodeIDs = append(relatedNodeIDs, waiter.ID)
	relatedNodeIDs = append(relatedNodeIDs, prerequisiteIDs...)
	_, err = queries.InsertPendingHandoff(ctx, db.InsertPendingHandoffParams{
		WorkspaceID:     waiter.WorkspaceID,
		Urgency:         "fast",
		ReasonCode:      "unlock",
		TargetActorType: ownerTypeAgent,
		TargetActorID:   waiter.OwnerID,
		RelatedNodeIds:  relatedNodeIDs,
		ChannelID:       waiter.PrimaryChannelID,
		IssueID:         waiter.LinkedIssueID,
		DedupeKey:       "unlock:" + waiter.ID.String() + ":" + strings.Join(prerequisiteIDStrings, ","),
		NotBefore:       pgtype.Timestamptz{Time: time.Now(), Valid: true},
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("insert unlock handoff: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit unlock detection transaction: %w", err)
	}
	return nil
}

func loadWorkNodeForUpdate(ctx context.Context, tx pgx.Tx, nodeID pgtype.UUID) (db.WorkNode, error) {
	var node db.WorkNode
	err := tx.QueryRow(ctx, `
		SELECT id, workspace_id, kind, title, description, owner_type, owner_id,
		       status, primary_channel_id, linked_issue_id, linked_task_id,
		       last_progress_at, last_progress_summary, last_wendy_nudge_at,
		       last_wendy_nudge_kind, created_at, updated_at
		FROM work_node
		WHERE id = $1
		FOR UPDATE
	`, nodeID).Scan(
		&node.ID,
		&node.WorkspaceID,
		&node.Kind,
		&node.Title,
		&node.Description,
		&node.OwnerType,
		&node.OwnerID,
		&node.Status,
		&node.PrimaryChannelID,
		&node.LinkedIssueID,
		&node.LinkedTaskID,
		&node.LastProgressAt,
		&node.LastProgressSummary,
		&node.LastWendyNudgeAt,
		&node.LastWendyNudgeKind,
		&node.CreatedAt,
		&node.UpdatedAt,
	)
	return node, err
}

func isWorkspaceSupervisor(ctx context.Context, queries *db.Queries, workspaceID, agentID pgtype.UUID) (bool, error) {
	supervisorID, err := queries.GetWorkspaceSupervisorAgentID(ctx, workspaceID)
	switch {
	case err == nil:
		return supervisorID == agentID, nil
	case !errors.Is(err, pgx.ErrNoRows):
		return false, fmt.Errorf("load workspace supervisor: %w", err)
	}

	isNamedSupervisor, err := queries.IsWorkspaceWendyAgent(ctx, db.IsWorkspaceWendyAgentParams{
		WorkspaceID: workspaceID,
		ID:          agentID,
	})
	if err != nil {
		return false, fmt.Errorf("check named workspace supervisor: %w", err)
	}
	return isNamedSupervisor, nil
}
