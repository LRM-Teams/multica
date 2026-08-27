package researchrun

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

const v6DirectionSaturationThreshold = 5

// A saturated direction needs an explicit Goal-value check before another
// atomic Work can be created or dispatched. The Director owns this judgment;
// the server only verifies that the judgment refers to the current node count.
type v6DirectionGate struct {
	NodeCount int    `json:"direction_gate_node_count"`
	Decision  string `json:"direction_gate_decision"`
	Rationale string `json:"direction_gate_rationale"`
}

func validateV6DirectionGateTx(ctx context.Context, tx pgx.Tx, workspaceID, runID string, branchIDs []string, gate v6DirectionGate) error {
	if len(branchIDs) == 0 {
		return nil
	}
	var nodeCount int
	if err := tx.QueryRow(ctx, `WITH RECURSIVE branch_tree AS (
		SELECT id,id AS top_id
		FROM research_branch
		WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND parent_branch_id IS NULL
		UNION ALL
		SELECT child.id,branch_tree.top_id
		FROM research_branch child
		JOIN branch_tree ON branch_tree.id=child.parent_branch_id
		WHERE child.workspace_id=$1::uuid AND child.session_id=$2::uuid
	), selected_direction AS (
		SELECT DISTINCT top_id FROM branch_tree WHERE id=ANY($3::uuid[])
	)
	SELECT count(DISTINCT binding.node_artifact_version_id)::int
	FROM branch_tree
	JOIN selected_direction ON selected_direction.top_id=branch_tree.top_id
	JOIN research_node_branch binding ON binding.workspace_id=$1::uuid
		AND binding.session_id=$2::uuid AND binding.branch_id=branch_tree.id`, workspaceID, runID, branchIDs).Scan(&nodeCount); err != nil {
		return err
	}
	if nodeCount <= v6DirectionSaturationThreshold {
		return nil
	}
	if gate.NodeCount != nodeCount || gate.Decision != "continue_direction" || strings.TrimSpace(gate.Rationale) == "" {
		return fmt.Errorf("%w: 该研究方向已有 %d 个节点，继续调研必须先提交当前 Goal 价值判断（direction_gate_node_count、direction_gate_decision=continue_direction、direction_gate_rationale）", ErrInvalidContract, nodeCount)
	}
	return nil
}
