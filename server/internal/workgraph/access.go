package workgraph

import (
	"context"

	"github.com/google/uuid"
)

type AccessMode string

const (
	AccessRead       AccessMode = "read"
	AccessCoordinate AccessMode = "coordinate"
	AccessExecute    AccessMode = "execute"
	AccessVerify     AccessMode = "verify"
)

// AuthorizeAgent centralizes the Graph participant boundary. Workspace
// membership alone never grants access to a Goal's executable plan.
func (s *Store) AuthorizeAgent(ctx context.Context, workspaceID, graphID, agentID, nodeID string, mode AccessMode) error {
	w, err := uuid.Parse(workspaceID)
	if err != nil {
		return ErrInvalidGraph
	}
	g, err := uuid.Parse(graphID)
	if err != nil {
		return ErrInvalidGraph
	}
	a, err := uuid.Parse(agentID)
	if err != nil {
		return ErrInvalidGraph
	}
	var node uuid.UUID
	if nodeID != "" {
		node, err = uuid.Parse(nodeID)
		if err != nil {
			return ErrInvalidGraph
		}
	}

	var allowed bool
	switch mode {
	case AccessCoordinate:
		err = s.pool.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM work_graph graph
				JOIN channel_goal goal ON graph.anchor_kind='channel_goal' AND goal.id=graph.anchor_id
				LEFT JOIN channel_member participant
				  ON participant.channel_id=goal.channel_id
				 AND participant.workspace_id=goal.workspace_id
				 AND participant.member_type='agent'
				 AND participant.member_id=$3
				WHERE graph.workspace_id=$1 AND graph.id=$2
				  AND ((graph.created_by_type='agent' AND graph.created_by_id=$3)
				       OR (goal.created_by_type='agent' AND goal.created_by_id=$3)
				       OR participant.role='manager')
			)
		`, w, g, a).Scan(&allowed)
	case AccessExecute, AccessVerify:
		roleClause := "node.role<>'verifier'"
		if mode == AccessVerify {
			roleClause = "node.role='verifier'"
		}
		err = s.pool.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM work_graph_node node
				JOIN issue item ON item.id=node.issue_id AND item.workspace_id=node.workspace_id
				WHERE node.workspace_id=$1 AND node.graph_id=$2 AND node.id=$3
				  AND item.assignee_type='agent' AND item.assignee_id=$4
				  AND `+roleClause+`
			)
		`, w, g, node, a).Scan(&allowed)
	case AccessRead:
		err = s.pool.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM work_graph graph
				JOIN channel_goal goal ON graph.anchor_kind='channel_goal' AND goal.id=graph.anchor_id
				LEFT JOIN channel_member participant
				  ON participant.channel_id=goal.channel_id
				 AND participant.workspace_id=goal.workspace_id
				 AND participant.member_type='agent'
				 AND participant.member_id=$3
				WHERE graph.workspace_id=$1 AND graph.id=$2
				  AND (participant.member_id IS NOT NULL OR
				       (graph.created_by_type='agent' AND graph.created_by_id=$3) OR
				       EXISTS (SELECT 1 FROM work_graph_node node JOIN issue item ON item.id=node.issue_id
				               WHERE node.graph_id=graph.id AND item.assignee_type='agent' AND item.assignee_id=$3))
			)
		`, w, g, a).Scan(&allowed)
	default:
		return ErrInvalidGraph
	}
	if err != nil {
		return err
	}
	if !allowed {
		return ErrGraphForbidden
	}
	return nil
}
