package workgraph

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func sameJSON(a, b []byte) bool {
	var left, right any
	return json.Unmarshal(a, &left) == nil && json.Unmarshal(b, &right) == nil && reflect.DeepEqual(left, right)
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	a = append([]string(nil), a...)
	b = append([]string(nil), b...)
	sort.Strings(a)
	sort.Strings(b)
	return reflect.DeepEqual(a, b)
}

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
	probe := CreateInput{AnchorKind: AnchorChannelGoal, Admission: AdmissionGraph, Reason: in.Reason, ActorType: in.ActorType, BudgetPolicy: json.RawMessage(`{}`), Nodes: in.Nodes}
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
	issueByTemp := map[string]uuid.UUID{}
	canonicalAgentByTemp := map[string]uuid.UUID{}
	semanticChanged := map[string]bool{}
	keep := []uuid.UUID{}
	changedNodes := []uuid.UUID{}
	changedIssues := []uuid.UUID{}
	ordered := append([]NodeSpec(nil), normalized.Nodes...)
	sort.SliceStable(ordered, func(i, j int) bool {
		return dependencyDepth(normalized.Nodes, ordered[i].TempID) < dependencyDepth(normalized.Nodes, ordered[j].TempID)
	})
	for _, spec := range ordered {
		issueID, parseErr := uuid.Parse(spec.IssueID)
		if parseErr != nil {
			return Graph{}, ErrInvalidGraph
		}
		issueByTemp[spec.TempID] = issueID
	}
	// Compare in dependency order so a changed producer always propagates to
	// every descendant, regardless of the order nodes appeared in the request.
	for _, spec := range ordered {
		issueID := issueByTemp[spec.TempID]
		var nodeID uuid.UUID
		var oldRole, oldContext, oldObjective string
		var oldCompletion, oldBudget []byte
		var oldDependencies []string
		var canonicalAgent uuid.UUID
		completion, _ := json.Marshal(spec.CompletionContract)
		err = tx.QueryRow(ctx, `
			SELECT node.id,node.role,node.context_policy,node.objective,node.completion_contract,node.budget,
			       COALESCE(agent.source_agent_id,agent.id),
			       ARRAY(
			         SELECT dependency.issue_id::text
			         FROM work_graph_edge edge
			         JOIN work_graph_node dependency ON dependency.id=edge.from_node_id
			         WHERE edge.graph_id=node.graph_id AND edge.to_node_id=node.id
			           AND edge.edge_type='depends_on' AND edge.required AND edge.retired_version IS NULL
			         ORDER BY dependency.issue_id::text
			       )
			FROM work_graph_node node
			JOIN issue item ON item.id=node.issue_id AND item.workspace_id=node.workspace_id
			JOIN agent ON agent.id=item.assignee_id AND item.assignee_type='agent'
			WHERE node.workspace_id=$1 AND node.graph_id=$2 AND node.issue_id=$3
		`, w, g, issueID).Scan(&nodeID, &oldRole, &oldContext, &oldObjective, &oldCompletion, &oldBudget, &canonicalAgent, &oldDependencies)
		if err == pgx.ErrNoRows {
			semanticChanged[spec.TempID] = true
			status := "ready"
			if len(spec.DependsOn) > 0 {
				status = "queued"
			}
			if err = tx.QueryRow(ctx, `SELECT COALESCE(agent.source_agent_id,agent.id)
				FROM issue item
				JOIN agent ON agent.id=item.assignee_id AND agent.workspace_id=item.workspace_id
				WHERE item.workspace_id=$1 AND item.id=$2 AND item.status='backlog'
				  AND item.assignee_type='agent' AND agent.archived_at IS NULL AND agent.runtime_id IS NOT NULL`, w, issueID).Scan(&canonicalAgent); err != nil {
				return Graph{}, ErrInvalidGraph
			}
			err = tx.QueryRow(ctx, `INSERT INTO work_graph_node(workspace_id,graph_id,issue_id,role,context_policy,execution_status,objective,completion_contract,depth,budget,based_on_graph_version)
				VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
				RETURNING id`, w, g, issueID, spec.Role, spec.ContextPolicy, status, spec.Objective, completion, dependencyDepth(normalized.Nodes, spec.TempID), spec.Budget, next).Scan(&nodeID)
			if err != nil {
				return Graph{}, ErrInvalidGraph
			}
			changedNodes = append(changedNodes, nodeID)
			changedIssues = append(changedIssues, issueID)
		} else if err != nil {
			return Graph{}, err
		} else {
			desiredDependencies := make([]string, 0, len(spec.DependsOn))
			for _, dep := range spec.DependsOn {
				desiredDependencies = append(desiredDependencies, issueByTemp[dep].String())
			}
			upstreamChanged := false
			for _, dep := range spec.DependsOn {
				upstreamChanged = upstreamChanged || semanticChanged[dep]
			}
			unchanged := !upstreamChanged && oldRole == spec.Role && oldContext == spec.ContextPolicy && oldObjective == spec.Objective &&
				sameJSON(oldCompletion, completion) && sameJSON(oldBudget, spec.Budget) && sameStrings(oldDependencies, desiredDependencies)
			if unchanged {
				_, err = tx.Exec(ctx, `UPDATE work_graph_node SET based_on_graph_version=$4,depth=$5,updated_at=now() WHERE workspace_id=$1 AND graph_id=$2 AND id=$3`, w, g, nodeID, next, dependencyDepth(normalized.Nodes, spec.TempID))
			} else {
				semanticChanged[spec.TempID] = true
				// Only a semantic change revokes prior satisfaction. Adding unrelated
				// work to the Graph does not erase already accepted evidence.
				_, err = tx.Exec(ctx, `UPDATE work_graph_node SET role=$4,context_policy=$5,objective=$6,completion_contract=$7,depth=$8,budget=$9,based_on_graph_version=$10,execution_status='queued',validity_status='valid',review_status='unreviewed',effective_completion='pending',updated_at=now() WHERE workspace_id=$1 AND graph_id=$2 AND id=$3`, w, g, nodeID, spec.Role, spec.ContextPolicy, spec.Objective, completion, dependencyDepth(normalized.Nodes, spec.TempID), spec.Budget, next)
				changedNodes = append(changedNodes, nodeID)
				changedIssues = append(changedIssues, issueID)
			}
			if err != nil {
				return Graph{}, err
			}
		}
		nodeByTemp[spec.TempID] = nodeID
		canonicalAgentByTemp[spec.TempID] = canonicalAgent
		keep = append(keep, nodeID)
	}
	for _, spec := range normalized.Nodes {
		if spec.Role == "verifier" && canonicalAgentByTemp[spec.TempID] == canonicalAgentByTemp[spec.DependsOn[0]] {
			return Graph{}, ErrGraphForbidden
		}
	}
	if len(changedIssues) > 0 {
		if _, err = tx.Exec(ctx, `UPDATE issue SET status='backlog',updated_at=now() WHERE workspace_id=$1 AND id=ANY($2::uuid[]) AND status<>'cancelled'`, w, changedIssues); err != nil {
			return Graph{}, err
		}
		// Evidence belongs to a contract revision. Changed producer contracts
		// cannot reuse an artifact that was accepted under the previous plan.
		if _, err = tx.Exec(ctx, `UPDATE work_artifact_revision artifact SET validity_status='stale'
			FROM work_graph_node node
			WHERE artifact.workspace_id=$1 AND artifact.graph_id=$2
			  AND artifact.producer_node_id=node.id AND node.id=ANY($3::uuid[])
			  AND node.role<>'verifier' AND artifact.validity_status='valid'`, w, g, changedNodes); err != nil {
			return Graph{}, err
		}
		if _, err = tx.Exec(ctx, `UPDATE work_verification_attempt SET stale_at=now()
			WHERE workspace_id=$1 AND graph_id=$2 AND stale_at IS NULL
			  AND (producer_node_id=ANY($3::uuid[]) OR verifier_node_id=ANY($3::uuid[]))`, w, g, changedNodes); err != nil {
			return Graph{}, err
		}
	}
	// A semantic replan always rotates ephemeral reviewer identity, even if a
	// prior clone was still active or blocked. The replacement receives neither
	// that clone's context nor its memory.
	for _, spec := range ordered {
		if spec.Role != "verifier" || !semanticChanged[spec.TempID] {
			continue
		}
		var sourceAgent, oldDerived uuid.UUID
		assignmentErr := tx.QueryRow(ctx, `SELECT source_agent_id,derived_agent_id FROM work_review_agent_assignment WHERE workspace_id=$1 AND verifier_node_id=$2 FOR UPDATE`, w, nodeByTemp[spec.TempID]).Scan(&sourceAgent, &oldDerived)
		if assignmentErr == pgx.ErrNoRows {
			continue
		}
		if assignmentErr != nil {
			return Graph{}, assignmentErr
		}
		if _, err = tx.Exec(ctx, `UPDATE agent SET archived_at=now(),status='offline',updated_at=now() WHERE workspace_id=$1 AND id=$2`, w, oldDerived); err != nil {
			return Graph{}, err
		}
		derived, cloneErr := cloneReviewAgent(ctx, tx, w, sourceAgent, fmt.Sprintf("%s-%d", g, next), spec.TempID)
		if cloneErr != nil {
			return Graph{}, cloneErr
		}
		if _, err = tx.Exec(ctx, `UPDATE work_review_agent_assignment SET derived_agent_id=$3,status='active',archived_at=NULL WHERE workspace_id=$1 AND verifier_node_id=$2`, w, nodeByTemp[spec.TempID], derived); err != nil {
			return Graph{}, err
		}
		if _, err = tx.Exec(ctx, `UPDATE issue SET assignee_id=$3,updated_at=now() WHERE workspace_id=$1 AND id=$2`, w, issueByTemp[spec.TempID], derived); err != nil {
			return Graph{}, err
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE work_graph_node SET execution_status='cancelled',validity_status='superseded',effective_completion='stale',updated_at=now() WHERE workspace_id=$1 AND graph_id=$2 AND NOT(id=ANY($3::uuid[]))`, w, g, keep); err != nil {
		return Graph{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE agent SET archived_at=now(),status='offline',updated_at=now()
		WHERE workspace_id=$1 AND id IN (
		  SELECT assignment.derived_agent_id FROM work_review_agent_assignment assignment
		  WHERE assignment.graph_id=$2 AND assignment.status='active'
		    AND NOT(assignment.verifier_node_id=ANY($3::uuid[]))
		)`, w, g, keep); err != nil {
		return Graph{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE work_review_agent_assignment SET status='archived',archived_at=now()
		WHERE workspace_id=$1 AND graph_id=$2 AND status='active'
		  AND NOT(verifier_node_id=ANY($3::uuid[]))`, w, g, keep); err != nil {
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
