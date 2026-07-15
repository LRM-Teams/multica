package workgraph

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// SetPrimaryChannel records where Wendy should speak for this work node.
func (s *Store) SetPrimaryChannel(ctx context.Context, nodeID, channelID pgtype.UUID) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE work_node
		SET primary_channel_id = $2, updated_at = now()
		WHERE id = $1
	`, nodeID, channelID)
	if err != nil {
		return fmt.Errorf("set work node primary channel: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// DetectStartWorkForNode enqueues a fast start_work handoff when an assigned
// issue is ready to begin (no unresolved waits) and has a speaking channel.
func (s *Store) DetectStartWorkForNode(ctx context.Context, nodeID pgtype.UUID) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin start-work detection transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	queries := s.queries.WithTx(tx)
	node, err := loadWorkNodeForUpdate(ctx, tx, nodeID)
	if err != nil {
		return fmt.Errorf("lock work node: %w", err)
	}
	if isTerminalNodeStatus(node.Status) || node.Status == workNodeStatusWaiting || node.Status == workNodeStatusBlocked {
		return nil
	}
	if node.OwnerType != ownerTypeAgent && node.OwnerType != ownerTypeMember {
		return nil
	}
	if !node.OwnerID.Valid || !node.PrimaryChannelID.Valid {
		return nil
	}

	unresolved, err := queries.CountOpenUnresolvedWaitsOn(ctx, db.CountOpenUnresolvedWaitsOnParams{
		WorkspaceID: node.WorkspaceID,
		FromNodeID:  node.ID,
	})
	if err != nil {
		return fmt.Errorf("count unresolved waits: %w", err)
	}
	if unresolved > 0 {
		return nil
	}

	if node.OwnerType == ownerTypeAgent {
		isSupervisor, err := isWorkspaceSupervisor(ctx, queries, node.WorkspaceID, node.OwnerID)
		if err != nil {
			return err
		}
		if isSupervisor {
			slog.DebugContext(ctx, "skipping start_work handoff for workspace supervisor",
				"work_node_id", node.ID.String(),
				"agent_id", node.OwnerID.String(),
			)
			return nil
		}
	}

	_, err = queries.InsertPendingHandoff(ctx, db.InsertPendingHandoffParams{
		WorkspaceID:     node.WorkspaceID,
		Urgency:         "fast",
		ReasonCode:      "start_work",
		TargetActorType: node.OwnerType,
		TargetActorID:   node.OwnerID,
		RelatedNodeIds:  []pgtype.UUID{node.ID},
		ChannelID:       node.PrimaryChannelID,
		IssueID:         node.LinkedIssueID,
		DedupeKey:       "start_work:" + node.ID.String() + ":" + node.OwnerID.String(),
		NotBefore:       pgtype.Timestamptz{Time: time.Now(), Valid: true},
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("insert start_work handoff: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit start-work detection transaction: %w", err)
	}
	return nil
}

// ResolveSharedGroupChannel finds a group channel that has a bound group manager
// (Beckham) and where the assignee is a member — i.e. a channel where Beckham can
// speak the handoff. Prefer the most recently updated channel.
func (s *Store) ResolveSharedGroupChannel(ctx context.Context, workspaceID pgtype.UUID, ownerType string, ownerID pgtype.UUID) (pgtype.UUID, error) {
	var channelID pgtype.UUID
	var err error
	switch ownerType {
	case ownerTypeAgent:
		err = s.pool.QueryRow(ctx, `
			SELECT ch.id
			FROM channel ch
			JOIN agent mgr
			  ON mgr.id = ch.group_manager_agent_id
			 AND mgr.workspace_id = ch.workspace_id
			 AND mgr.archived_at IS NULL
			JOIN channel_member assignee
			  ON assignee.channel_id = ch.id
			 AND assignee.workspace_id = ch.workspace_id
			 AND assignee.member_type = 'agent'
			 AND assignee.member_id = $2
			WHERE ch.workspace_id = $1
			  AND ch.kind = 'group'
			  AND ch.archived_at IS NULL
			  AND ch.group_manager_agent_id IS NOT NULL
			ORDER BY ch.updated_at DESC
			LIMIT 1
		`, workspaceID, ownerID).Scan(&channelID)
	case ownerTypeMember:
		err = s.pool.QueryRow(ctx, `
			SELECT ch.id
			FROM channel ch
			JOIN agent mgr
			  ON mgr.id = ch.group_manager_agent_id
			 AND mgr.workspace_id = ch.workspace_id
			 AND mgr.archived_at IS NULL
			JOIN channel_member cm
			  ON cm.channel_id = ch.id
			 AND cm.workspace_id = ch.workspace_id
			 AND cm.member_type = 'user'
			JOIN member m
			  ON m.user_id = cm.member_id
			 AND m.workspace_id = ch.workspace_id
			 AND m.id = $2
			WHERE ch.workspace_id = $1
			  AND ch.kind = 'group'
			  AND ch.archived_at IS NULL
			  AND ch.group_manager_agent_id IS NOT NULL
			ORDER BY ch.updated_at DESC
			LIMIT 1
		`, workspaceID, ownerID).Scan(&channelID)
	default:
		return pgtype.UUID{}, nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return pgtype.UUID{}, nil
	}
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("resolve shared group channel: %w", err)
	}
	return channelID, nil
}
