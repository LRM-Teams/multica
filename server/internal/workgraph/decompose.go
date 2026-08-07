package workgraph

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func normalizeDecompose(in DecomposeInput) (DecomposeInput, error) {
	in.Reason = strings.TrimSpace(in.Reason)
	if in.Reason == "" || len(in.Nodes) < 2 || len(in.Nodes) > defaultMaxNodes {
		return in, ErrInvalidGraph
	}
	seen := make(map[string]IssuePlanNode, len(in.Nodes))
	for i := range in.Nodes {
		n := &in.Nodes[i]
		n.TempID = strings.TrimSpace(n.TempID)
		n.Title = strings.TrimSpace(n.Title)
		n.AssigneeID = strings.TrimSpace(n.AssigneeID)
		n.WorkerMode = strings.TrimSpace(n.WorkerMode)
		n.CloneReason = strings.TrimSpace(n.CloneReason)
		if n.WorkerMode == "" {
			n.WorkerMode = WorkerModeReuseAgent
		}
		if n.TempID == "" || n.Title == "" || n.AssigneeID == "" {
			return in, ErrInvalidGraph
		}
		if n.WorkerMode != WorkerModeReuseAgent && n.WorkerMode != WorkerModeDerivedAgent {
			return in, ErrInvalidGraph
		}
		if n.WorkerMode == WorkerModeDerivedAgent && (n.CloneReason == "" || len(n.CloneReason) > 1000) {
			return in, ErrInvalidGraph
		}
		if _, exists := seen[n.TempID]; exists {
			return in, ErrInvalidGraph
		}
		seen[n.TempID] = *n
	}
	visiting := map[string]bool{}
	visited := map[string]bool{}
	var visit func(string) error
	visit = func(id string) error {
		if visiting[id] {
			return ErrInvalidGraph
		}
		if visited[id] {
			return nil
		}
		visiting[id] = true
		for _, dep := range seen[id].DependsOn {
			if dep == id {
				return ErrInvalidGraph
			}
			if _, exists := seen[dep]; !exists {
				return ErrInvalidGraph
			}
			if err := visit(dep); err != nil {
				return err
			}
		}
		delete(visiting, id)
		visited[id] = true
		return nil
	}
	for id := range seen {
		if err := visit(id); err != nil {
			return in, err
		}
	}
	return in, nil
}

func digestDecompose(in DecomposeInput) string {
	copy := in
	copy.WorkspaceID, copy.ActorAgentID, copy.IdempotencyKey = "", "", ""
	copy.Nodes = append([]IssuePlanNode(nil), copy.Nodes...)
	sort.Slice(copy.Nodes, func(i, j int) bool { return copy.Nodes[i].TempID < copy.Nodes[j].TempID })
	b, _ := json.Marshal(copy)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// DecomposeIssue atomically creates a light-weight Issue DAG. It deliberately
// creates no Goal or Work Graph; the Issue dependency table is the only
// scheduler input for this bounded collaboration.
func (s *Store) DecomposeIssue(ctx context.Context, input DecomposeInput) (DecomposeResult, error) {
	in, err := normalizeDecompose(input)
	if err != nil {
		return DecomposeResult{}, err
	}
	w, err := uuid.Parse(in.WorkspaceID)
	if err != nil {
		return DecomposeResult{}, ErrInvalidGraph
	}
	parent, err := uuid.Parse(in.ParentIssueID)
	if err != nil {
		return DecomposeResult{}, ErrInvalidGraph
	}
	actor, err := uuid.Parse(in.ActorAgentID)
	if err != nil {
		return DecomposeResult{}, ErrInvalidGraph
	}
	key, err := uuid.Parse(in.IdempotencyKey)
	if err != nil {
		return DecomposeResult{}, ErrInvalidGraph
	}
	digest := digestDecompose(in)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return DecomposeResult{}, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, in.WorkspaceID+":"+in.ActorAgentID+":"+in.IdempotencyKey); err != nil {
		return DecomposeResult{}, err
	}
	var priorDigest string
	var prior []byte
	err = tx.QueryRow(ctx, `SELECT request_digest,response FROM issue_decompose_request WHERE workspace_id=$1 AND actor_agent_id=$2 AND idempotency_key=$3 FOR UPDATE`, w, actor, key).Scan(&priorDigest, &prior)
	if err == nil {
		if priorDigest != digest {
			return DecomposeResult{}, ErrIdempotencyConflict
		}
		var result DecomposeResult
		if json.Unmarshal(prior, &result) != nil {
			return DecomposeResult{}, ErrInvalidGraph
		}
		result.Replayed = true
		return result, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return DecomposeResult{}, err
	}

	var projectID, sourceChannelID, sourceMessageID pgtype.UUID
	var parentStatus string
	err = tx.QueryRow(ctx, `
		SELECT parent.project_id,source.channel_id,source.message_id,parent.status
		FROM issue parent
		LEFT JOIN issue_source_message source ON source.issue_id=parent.id
		WHERE parent.workspace_id=$1 AND parent.id=$2
		  AND parent.status NOT IN ('done','cancelled')
		  AND ((parent.assignee_type='agent' AND parent.assignee_id=$3)
		       OR (parent.creator_type='agent' AND parent.creator_id=$3))
	`, w, parent, actor).Scan(&projectID, &sourceChannelID, &sourceMessageID, &parentStatus)
	if err != nil {
		return DecomposeResult{}, ErrGraphForbidden
	}

	issueByTemp := make(map[string]uuid.UUID, len(in.Nodes))
	result := DecomposeResult{ParentIssueID: parent.String(), IssueIDs: map[string]string{}, AgentIDs: map[string]string{}, ReadyIssueIDs: []string{}}
	for _, node := range in.Nodes {
		sourceAgentID, parseErr := uuid.Parse(node.AssigneeID)
		if parseErr != nil {
			return DecomposeResult{}, ErrInvalidGraph
		}
		var agentOK bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM agent WHERE workspace_id=$1 AND id=$2 AND archived_at IS NULL AND runtime_id IS NOT NULL)`, w, sourceAgentID).Scan(&agentOK); err != nil || !agentOK {
			return DecomposeResult{}, ErrInvalidGraph
		}
		agentID := sourceAgentID
		if node.WorkerMode == WorkerModeDerivedAgent {
			// A derived worker may only snapshot the acting Agent. Cross-Agent
			// cloning would disclose another identity's private memory and config.
			if sourceAgentID != actor {
				return DecomposeResult{}, ErrGraphForbidden
			}
			agentID, err = cloneIssueWorker(ctx, tx, w, sourceAgentID, in.IdempotencyKey, node.TempID)
			if err != nil {
				return DecomposeResult{}, err
			}
		}
		var number int
		if err = tx.QueryRow(ctx, `UPDATE workspace SET issue_counter=issue_counter+1 WHERE id=$1 RETURNING issue_counter`, w).Scan(&number); err != nil {
			return DecomposeResult{}, err
		}
		status := "todo"
		if len(node.DependsOn) > 0 {
			status = "backlog"
		}
		var issueID uuid.UUID
		err = tx.QueryRow(ctx, `
			INSERT INTO issue(
				workspace_id,title,description,status,priority,assignee_type,assignee_id,
				creator_type,creator_id,parent_issue_id,project_id,
				acceptance_criteria,position,number
			) VALUES($1,$2,$3,$4,'none','agent',$5,'agent',$6,$7,$8,'[]'::jsonb,
				COALESCE((SELECT min(position)-1 FROM issue WHERE workspace_id=$1 AND status=$4),0),$9)
			RETURNING id
		`, w, node.Title, node.Description, status, agentID, actor, parent, projectID, number).Scan(&issueID)
		if err != nil {
			return DecomposeResult{}, err
		}
		if sourceChannelID.Valid {
			if _, err = tx.Exec(ctx, `INSERT INTO issue_source_message(issue_id,workspace_id,channel_id,message_id) VALUES($1,$2,$3,$4)`, issueID, w, sourceChannelID, sourceMessageID); err != nil {
				return DecomposeResult{}, err
			}
		}
		if node.WorkerMode == WorkerModeDerivedAgent {
			if _, err = tx.Exec(ctx, `INSERT INTO issue_derived_agent_assignment(workspace_id,parent_issue_id,issue_id,source_agent_id,derived_agent_id,clone_reason) VALUES($1,$2,$3,$4,$5,$6)`, w, parent, issueID, sourceAgentID, agentID, node.CloneReason); err != nil {
				return DecomposeResult{}, err
			}
		}
		if _, err = tx.Exec(ctx, `INSERT INTO issue_decompose_child(workspace_id,parent_issue_id,issue_id) VALUES($1,$2,$3)`, w, parent, issueID); err != nil {
			return DecomposeResult{}, err
		}
		issueByTemp[node.TempID] = issueID
		result.IssueIDs[node.TempID] = issueID.String()
		result.AgentIDs[node.TempID] = agentID.String()
		if status == "todo" {
			result.ReadyIssueIDs = append(result.ReadyIssueIDs, issueID.String())
		}
	}
	for _, node := range in.Nodes {
		for _, dep := range node.DependsOn {
			if _, err = tx.Exec(ctx, `INSERT INTO issue_dependency(issue_id,depends_on_issue_id,type) VALUES($1,$2,'blocked_by')`, issueByTemp[node.TempID], issueByTemp[dep]); err != nil {
				return DecomposeResult{}, err
			}
		}
	}
	encoded, _ := json.Marshal(result)
	if _, err = tx.Exec(ctx, `INSERT INTO issue_decompose_request(workspace_id,parent_issue_id,actor_agent_id,idempotency_key,request_digest,response) VALUES($1,$2,$3,$4,$5,$6)`, w, parent, actor, key, digest, encoded); err != nil {
		return DecomposeResult{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return DecomposeResult{}, err
	}
	if s.OnNodesReady != nil && len(result.ReadyIssueIDs) > 0 {
		s.OnNodesReady(ctx, in.WorkspaceID, result.ReadyIssueIDs)
	}
	return result, nil
}
