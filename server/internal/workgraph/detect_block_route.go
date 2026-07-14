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

// DetectBlockRouteForNode asks the owners of a blocked node's root causes to
// unblock it. It deliberately never sends a "start work" directive to the
// blocked node owner or to downstream waiters.
func (s *Store) DetectBlockRouteForNode(ctx context.Context, nodeID pgtype.UUID) error {
	node, err := s.queries.GetWorkNodeByID(ctx, nodeID)
	if err != nil {
		return fmt.Errorf("load blocked work node: %w", err)
	}
	if node.Status != "blocked" {
		return nil
	}

	rootCauseIDs, err := s.blockRouteRootCauseNodeIDs(ctx, node)
	if err != nil {
		return err
	}
	for _, rootCauseID := range rootCauseIDs {
		rootCause, err := s.queries.GetWorkNodeByID(ctx, rootCauseID)
		if err != nil {
			return fmt.Errorf("load block route root cause: %w", err)
		}
		if !rootCause.OwnerID.Valid || (rootCause.OwnerType != ownerTypeAgent && rootCause.OwnerType != ownerTypeMember) {
			continue
		}
		if node.OwnerID.Valid && node.OwnerID == rootCause.OwnerID && node.OwnerType == rootCause.OwnerType {
			continue
		}
		if rootCause.OwnerType == ownerTypeAgent {
			supervisor, err := isWorkspaceSupervisor(ctx, s.queries, node.WorkspaceID, rootCause.OwnerID)
			if err != nil {
				return err
			}
			if supervisor {
				continue
			}
		}
		_, err = s.queries.InsertPendingHandoff(ctx, db.InsertPendingHandoffParams{
			WorkspaceID:     node.WorkspaceID,
			Urgency:         "fast",
			ReasonCode:      "block_route",
			TargetActorType: rootCause.OwnerType,
			TargetActorID:   rootCause.OwnerID,
			RelatedNodeIds:  []pgtype.UUID{node.ID, rootCause.ID},
			ChannelID:       node.PrimaryChannelID,
			IssueID:         node.LinkedIssueID,
			DedupeKey:       "block_route:" + node.ID.String() + ":" + rootCause.OwnerType + ":" + rootCause.OwnerID.String(),
			NotBefore:       pgtype.Timestamptz{Time: time.Now(), Valid: true},
		})
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return fmt.Errorf("insert block route handoff: %w", err)
		}
	}
	return nil
}

func (s *Store) blockRouteRootCauseNodeIDs(ctx context.Context, node db.WorkNode) ([]pgtype.UUID, error) {
	var ids []pgtype.UUID
	for _, query := range []string{
		`SELECT to_node_id FROM work_edge WHERE workspace_id = $1 AND from_node_id = $2 AND kind = 'blocked_by' AND status = 'open'`,
		`SELECT to_node_id FROM work_edge WHERE workspace_id = $1 AND from_node_id = $2 AND kind = 'waits_on' AND status = 'resolved'`,
		// Issue dependencies are represented as waits_on by Phase A. An open
		// prerequisite is also a concrete root cause when no explicit edge is available.
		`SELECT to_node_id FROM work_edge WHERE workspace_id = $1 AND from_node_id = $2 AND kind = 'waits_on' AND status = 'open'`,
	} {
		rows, err := s.pool.Query(ctx, query, node.WorkspaceID, node.ID)
		if err != nil {
			return nil, fmt.Errorf("list block route causes: %w", err)
		}
		ids = ids[:0]
		for rows.Next() {
			var id pgtype.UUID
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return nil, err
			}
			ids = append(ids, id)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
		if len(ids) > 0 {
			return ids, nil
		}
	}
	slog.DebugContext(ctx, "blocked node has no routable root cause", "work_node_id", node.ID.String())
	return nil, nil
}
