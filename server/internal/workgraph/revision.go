package workgraph

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/google/uuid"
)

func (s *Store) Revise(ctx context.Context, in ReviseInput) (Graph, error) {
	w, err := uuid.Parse(in.WorkspaceID)
	if err != nil {
		return Graph{}, ErrInvalidGraph
	}
	g, err := uuid.Parse(in.GraphID)
	if err != nil {
		return Graph{}, ErrInvalidGraph
	}
	actor, err := uuid.Parse(in.ActorID)
	if err != nil {
		return Graph{}, ErrInvalidGraph
	}
	if in.ExpectedGraphVersion <= 0 || strings.TrimSpace(in.Reason) == "" || len(in.Nodes) < 2 || len(in.Nodes) > defaultMaxNodes {
		return Graph{}, ErrInvalidGraph
	}
	if in.ExpectedCostDelta == nil {
		in.ExpectedCostDelta = json.RawMessage(`{}`)
	}
	if !json.Valid(in.ExpectedCostDelta) {
		return Graph{}, ErrInvalidGraph
	}
	probe := CreateInput{AnchorKind: AnchorIssue, Admission: AdmissionGraph, Reason: in.Reason, ActorType: in.ActorType, BudgetPolicy: json.RawMessage(`{}`), Nodes: in.Nodes}
	normalized, err := normalizeCreate(probe)
	if err != nil {
		return Graph{}, err
	}
	for _, n := range normalized.Nodes {
		if n.IssueID == "" {
			return Graph{}, ErrInvalidGraph
		}
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Graph{}, err
	}
	defer tx.Rollback(ctx)
	var current int64
	if err = tx.QueryRow(ctx, `SELECT current_version FROM work_graph WHERE workspace_id=$1 AND id=$2 FOR UPDATE`, w, g).Scan(&current); err != nil {
		return Graph{}, err
	}
	if current != in.ExpectedGraphVersion {
		return Graph{}, ErrGraphConflict
	}
	next := current + 1
	nodeByTemp := map[string]uuid.UUID{}
	keep := []uuid.UUID{}
	for _, spec := range normalized.Nodes {
		issueID, parseErr := uuid.Parse(spec.IssueID)
		if parseErr != nil {
			return Graph{}, ErrInvalidGraph
		}
		var nodeID uuid.UUID
		completion, _ := json.Marshal(spec.CompletionContract)
		err = tx.QueryRow(ctx, `SELECT id FROM work_graph_node WHERE workspace_id=$1 AND graph_id=$2 AND issue_id=$3`, w, g, issueID).Scan(&nodeID)
		if err != nil {
			status := "ready"
			if len(spec.DependsOn) > 0 {
				status = "queued"
			}
			err = tx.QueryRow(ctx, `INSERT INTO work_graph_node(workspace_id,graph_id,issue_id,role,context_policy,execution_status,objective,completion_contract,depth,budget,based_on_graph_version) SELECT $1,$2,i.id,$4,$5,$6,$7,$8,$9,$10,$11 FROM issue i WHERE i.workspace_id=$1 AND i.id=$3 AND i.status='backlog' RETURNING id`, w, g, issueID, spec.Role, spec.ContextPolicy, status, spec.Objective, completion, dependencyDepth(normalized.Nodes, spec.TempID), spec.Budget, next).Scan(&nodeID)
			if err != nil {
				return Graph{}, ErrInvalidGraph
			}
		} else {
			// A revision is a new executable plan. Re-admit retained nodes instead of
			// carrying terminal state across a changed contract or dependency set.
			_, err = tx.Exec(ctx, `UPDATE work_graph_node SET role=$4,context_policy=$5,objective=$6,completion_contract=$7,depth=$8,budget=$9,based_on_graph_version=$10,execution_status='queued',validity_status='valid',review_status='unreviewed',updated_at=now() WHERE workspace_id=$1 AND graph_id=$2 AND id=$3`, w, g, nodeID, spec.Role, spec.ContextPolicy, spec.Objective, completion, dependencyDepth(normalized.Nodes, spec.TempID), spec.Budget, next)
			if err != nil {
				return Graph{}, err
			}
		}
		nodeByTemp[spec.TempID] = nodeID
		keep = append(keep, nodeID)
	}
	if _, err = tx.Exec(ctx, `UPDATE work_graph_node SET execution_status='cancelled',validity_status='superseded',updated_at=now() WHERE workspace_id=$1 AND graph_id=$2 AND NOT(id=ANY($3::uuid[]))`, w, g, keep); err != nil {
		return Graph{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE work_graph_edge SET retired_version=$2 WHERE graph_id=$1 AND retired_version IS NULL`, g, next); err != nil {
		return Graph{}, err
	}
	for _, spec := range normalized.Nodes {
		for _, dep := range spec.DependsOn {
			if _, err = tx.Exec(ctx, `INSERT INTO work_graph_edge(workspace_id,graph_id,from_node_id,to_node_id,edge_type,required,created_version) VALUES($1,$2,$3,$4,'depends_on',true,$5)`, w, g, nodeByTemp[dep], nodeByTemp[spec.TempID], next); err != nil {
				return Graph{}, err
			}
		}
	}
	digest := topologyDigest(normalized.Nodes)
	if _, err = tx.Exec(ctx, `INSERT INTO work_graph_revision(graph_id,version,previous_version,reason,author_type,author_id,topology_digest,expected_cost_delta) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, g, next, current, in.Reason, in.ActorType, actor, digest, in.ExpectedCostDelta); err != nil {
		return Graph{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE work_graph SET current_version=$2,updated_at=now() WHERE id=$1`, g, next); err != nil {
		return Graph{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO work_graph_change_event(workspace_id,graph_id,version,event_type,affected_nodes,reason,payload) VALUES($1,$2,$3,'graph_replanned',$4,$5,jsonb_build_object('previous_version',$6::bigint))`, w, g, next, keep, in.Reason, current); err != nil {
		return Graph{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Graph{}, err
	}
	if _, err = s.ReconcileReady(ctx, in.WorkspaceID, in.GraphID); err != nil {
		return Graph{}, err
	}
	return s.Get(ctx, in.WorkspaceID, in.GraphID)
}
