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
)

var (
	ErrInvalidGraph        = errors.New("invalid work graph")
	ErrGraphConflict       = errors.New("work graph version conflict")
	ErrIdempotencyConflict = errors.New("idempotency key reused with different request")
)

const (
	defaultMaxNodes = 10
	defaultMaxDepth = 4
)

var validRoles = map[string]bool{
	"planner": true, "worker": true, "explorer": true, "critic": true,
	"verifier": true, "replicator": true, "judge": true,
	"synthesizer": true, "promoter": true, "observer": true,
}

var validContextPolicies = map[string]bool{
	"full": true, "bounded": true, "blind": true, "adversarial": true,
	"replication": true, "sealed": true,
}

func normalizeCreate(in CreateInput) (CreateInput, error) {
	in.Reason = strings.TrimSpace(in.Reason)
	if in.Reason == "" || len(in.Nodes) < 2 || len(in.Nodes) > defaultMaxNodes {
		return in, ErrInvalidGraph
	}
	if in.AnchorKind != AnchorChannelGoal && in.AnchorKind != AnchorIssue && in.AnchorKind != AnchorResearchRun {
		return in, ErrInvalidGraph
	}
	if in.Admission != AdmissionGraph && in.Admission != AdmissionProposeGraph {
		return in, ErrInvalidGraph
	}
	if in.ActorType != "member" && in.ActorType != "agent" {
		return in, ErrInvalidGraph
	}
	if in.BudgetPolicy == nil {
		in.BudgetPolicy = json.RawMessage(`{}`)
	}
	if !isJSONObject(in.BudgetPolicy) {
		return in, ErrInvalidGraph
	}
	seen := make(map[string]NodeSpec, len(in.Nodes))
	for i := range in.Nodes {
		n := &in.Nodes[i]
		n.TempID = strings.TrimSpace(n.TempID)
		n.IssueID = strings.TrimSpace(n.IssueID)
		n.Title = strings.TrimSpace(n.Title)
		n.AssigneeID = strings.TrimSpace(n.AssigneeID)
		n.Role = strings.TrimSpace(n.Role)
		n.Objective = strings.TrimSpace(n.Objective)
		if n.ContextPolicy == "" {
			n.ContextPolicy = "bounded"
		}
		if n.TempID == "" || (n.IssueID == "" && (n.Title == "" || n.AssigneeID == "")) || !validRoles[n.Role] || !validContextPolicies[n.ContextPolicy] {
			return in, ErrInvalidGraph
		}
		if _, ok := seen[n.TempID]; ok {
			return in, ErrInvalidGraph
		}
		if n.Budget == nil {
			n.Budget = json.RawMessage(`{}`)
		}
		if !isJSONObject(n.Budget) {
			return in, ErrInvalidGraph
		}
		if n.CompletionContract == nil {
			n.CompletionContract = []string{}
		}
		seen[n.TempID] = *n
	}
	depthMemo := map[string]int{}
	visiting := map[string]bool{}
	var depth func(string) (int, error)
	depth = func(id string) (int, error) {
		if d, ok := depthMemo[id]; ok {
			return d, nil
		}
		if visiting[id] {
			return 0, ErrInvalidGraph
		}
		visiting[id] = true
		n := seen[id]
		max := 0
		for _, dep := range n.DependsOn {
			if _, ok := seen[dep]; !ok || dep == id {
				return 0, ErrInvalidGraph
			}
			d, err := depth(dep)
			if err != nil {
				return 0, err
			}
			if d+1 > max {
				max = d + 1
			}
		}
		visiting[id] = false
		depthMemo[id] = max
		if max > defaultMaxDepth {
			return 0, ErrInvalidGraph
		}
		return max, nil
	}
	for id := range seen {
		if _, err := depth(id); err != nil {
			return in, err
		}
	}
	return in, nil
}

func isJSONObject(raw json.RawMessage) bool {
	var value map[string]any
	return json.Unmarshal(raw, &value) == nil && value != nil
}

func digestCreate(in CreateInput) string {
	copy := in
	copy.WorkspaceID, copy.ActorType, copy.ActorID, copy.IdempotencyKey = "", "", "", ""
	b, _ := json.Marshal(copy)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func (s *Store) Create(ctx context.Context, input CreateInput) (CreateResult, error) {
	in, err := normalizeCreate(input)
	if err != nil {
		return CreateResult{}, err
	}
	workspaceID, err := uuid.Parse(in.WorkspaceID)
	if err != nil {
		return CreateResult{}, ErrInvalidGraph
	}
	anchorID, err := uuid.Parse(in.AnchorID)
	if err != nil {
		return CreateResult{}, ErrInvalidGraph
	}
	actorID, err := uuid.Parse(in.ActorID)
	if err != nil {
		return CreateResult{}, ErrInvalidGraph
	}
	idempotencyKey, err := uuid.Parse(in.IdempotencyKey)
	if err != nil {
		return CreateResult{}, ErrInvalidGraph
	}
	requestDigest := digestCreate(in)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return CreateResult{}, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, in.WorkspaceID+":"+in.ActorType+":"+in.ActorID+":"+in.IdempotencyKey); err != nil {
		return CreateResult{}, err
	}

	var priorDigest string
	var priorResponse []byte
	err = tx.QueryRow(ctx, `SELECT request_digest, response FROM work_graph_create_request WHERE workspace_id=$1 AND actor_type=$2 AND actor_id=$3 AND idempotency_key=$4 FOR UPDATE`, workspaceID, in.ActorType, actorID, idempotencyKey).Scan(&priorDigest, &priorResponse)
	if err == nil {
		if priorDigest != requestDigest {
			return CreateResult{}, ErrIdempotencyConflict
		}
		var result CreateResult
		if json.Unmarshal(priorResponse, &result) != nil {
			return CreateResult{}, ErrInvalidGraph
		}
		result.Replayed = true
		return result, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return CreateResult{}, err
	}

	if err := validateAnchor(ctx, tx, workspaceID, in.AnchorKind, anchorID); err != nil {
		return CreateResult{}, err
	}
	issueIDs := make([]uuid.UUID, 0, len(in.Nodes))
	issueIDMap := make(map[string]string, len(in.Nodes))
	for _, n := range in.Nodes {
		if n.IssueID != "" {
			id, parseErr := uuid.Parse(n.IssueID)
			if parseErr != nil {
				return CreateResult{}, ErrInvalidGraph
			}
			var ok bool
			if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM issue WHERE workspace_id=$1 AND id=$2 AND status='backlog' AND assignee_type='agent' AND assignee_id IS NOT NULL)`, workspaceID, id).Scan(&ok); err != nil || !ok {
				return CreateResult{}, ErrInvalidGraph
			}
			issueIDs = append(issueIDs, id)
			issueIDMap[n.TempID] = id.String()
			continue
		}
		agentID, parseErr := uuid.Parse(n.AssigneeID)
		if parseErr != nil {
			return CreateResult{}, ErrInvalidGraph
		}
		var agentOK bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM agent WHERE workspace_id=$1 AND id=$2 AND archived_at IS NULL)`, workspaceID, agentID).Scan(&agentOK); err != nil || !agentOK {
			return CreateResult{}, ErrInvalidGraph
		}
		var number int
		if err = tx.QueryRow(ctx, `UPDATE workspace SET issue_counter=issue_counter+1 WHERE id=$1 RETURNING issue_counter`, workspaceID).Scan(&number); err != nil {
			return CreateResult{}, err
		}
		criteria, _ := json.Marshal(n.CompletionContract)
		parent := any(nil)
		if in.AnchorKind == AnchorIssue {
			parent = anchorID
		}
		var id uuid.UUID
		err = tx.QueryRow(ctx, `INSERT INTO issue(workspace_id,title,description,status,priority,assignee_type,assignee_id,creator_type,creator_id,parent_issue_id,acceptance_criteria,position,number) VALUES($1,$2,$3,'backlog','none','agent',$4,$5,$6,$7,$8,COALESCE((SELECT min(position)-1 FROM issue WHERE workspace_id=$1 AND status='backlog'),0),$9) RETURNING id`, workspaceID, n.Title, n.Description, agentID, in.ActorType, actorID, parent, criteria, number).Scan(&id)
		if err != nil {
			return CreateResult{}, err
		}
		issueIDs = append(issueIDs, id)
		issueIDMap[n.TempID] = id.String()
	}

	var graphID uuid.UUID
	err = tx.QueryRow(ctx, `INSERT INTO work_graph (workspace_id,anchor_kind,anchor_id,admission_decision,budget_policy,created_by_type,created_by_id) VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING id`, workspaceID, in.AnchorKind, anchorID, in.Admission, in.BudgetPolicy, in.ActorType, actorID).Scan(&graphID)
	if err != nil {
		return CreateResult{}, err
	}

	nodeIDs := make(map[string]string, len(in.Nodes))
	nodeUUIDs := make(map[string]uuid.UUID, len(in.Nodes))
	for i, n := range in.Nodes {
		status := "ready"
		if len(n.DependsOn) > 0 {
			status = "queued"
		}
		completion, _ := json.Marshal(n.CompletionContract)
		var nodeID uuid.UUID
		err = tx.QueryRow(ctx, `INSERT INTO work_graph_node (workspace_id,graph_id,issue_id,role,context_policy,execution_status,objective,completion_contract,depth,budget,based_on_graph_version) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,1) RETURNING id`, workspaceID, graphID, issueIDs[i], n.Role, n.ContextPolicy, status, n.Objective, completion, dependencyDepth(in.Nodes, n.TempID), n.Budget).Scan(&nodeID)
		if err != nil {
			return CreateResult{}, err
		}
		nodeUUIDs[n.TempID] = nodeID
		nodeIDs[n.TempID] = nodeID.String()
	}
	for _, n := range in.Nodes {
		for _, dep := range n.DependsOn {
			_, err = tx.Exec(ctx, `INSERT INTO work_graph_edge (workspace_id,graph_id,from_node_id,to_node_id,edge_type,required,created_version) VALUES ($1,$2,$3,$4,'depends_on',true,1)`, workspaceID, graphID, nodeUUIDs[dep], nodeUUIDs[n.TempID])
			if err != nil {
				return CreateResult{}, err
			}
		}
	}
	topology := topologyDigest(in.Nodes)
	if _, err = tx.Exec(ctx, `INSERT INTO work_graph_revision (graph_id,version,reason,author_type,author_id,topology_digest) VALUES ($1,1,$2,$3,$4,$5)`, graphID, in.Reason, in.ActorType, actorID, topology); err != nil {
		return CreateResult{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO work_graph_change_event (workspace_id,graph_id,version,event_type,reason,payload) VALUES ($1,$2,1,'graph_created',$3,jsonb_build_object('node_ids',$4::jsonb))`, workspaceID, graphID, in.Reason, mustJSON(nodeIDs)); err != nil {
		return CreateResult{}, err
	}
	result, err := loadGraphTx(ctx, tx, workspaceID, graphID)
	if err != nil {
		return CreateResult{}, err
	}
	created := CreateResult{Graph: result, NodeIDs: nodeIDs, IssueIDs: issueIDMap}
	encoded, _ := json.Marshal(created)
	if _, err = tx.Exec(ctx, `INSERT INTO work_graph_create_request (workspace_id,actor_type,actor_id,idempotency_key,request_digest,graph_id,response) VALUES ($1,$2,$3,$4,$5,$6,$7)`, workspaceID, in.ActorType, actorID, idempotencyKey, requestDigest, graphID, encoded); err != nil {
		return CreateResult{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return CreateResult{}, err
	}
	readyIssues := []string{}
	for _, node := range created.Graph.Nodes {
		if node.ExecutionStatus == "ready" {
			readyIssues = append(readyIssues, node.IssueID)
		}
	}
	if s.OnNodesReady != nil && len(readyIssues) > 0 {
		s.OnNodesReady(ctx, in.WorkspaceID, readyIssues)
	}
	return created, nil
}

func validateAnchor(ctx context.Context, tx pgx.Tx, workspaceID uuid.UUID, kind AnchorKind, anchorID uuid.UUID) error {
	var ok bool
	var query string
	switch kind {
	case AnchorChannelGoal:
		query = `SELECT EXISTS(SELECT 1 FROM channel_goal WHERE workspace_id=$1 AND id=$2 AND status IN ('active','paused'))`
	case AnchorIssue:
		query = `SELECT EXISTS(SELECT 1 FROM issue WHERE workspace_id=$1 AND id=$2)`
	case AnchorResearchRun:
		query = `SELECT EXISTS(SELECT 1 FROM research_session WHERE workspace_id=$1 AND id=$2)`
	}
	if err := tx.QueryRow(ctx, query, workspaceID, anchorID).Scan(&ok); err != nil || !ok {
		return ErrInvalidGraph
	}
	return nil
}

func dependencyDepth(nodes []NodeSpec, target string) int {
	byID := map[string]NodeSpec{}
	for _, n := range nodes {
		byID[n.TempID] = n
	}
	var depth func(string) int
	depth = func(id string) int {
		max := 0
		for _, dep := range byID[id].DependsOn {
			if d := depth(dep) + 1; d > max {
				max = d
			}
		}
		return max
	}
	return depth(target)
}

func topologyDigest(nodes []NodeSpec) string {
	items := append([]NodeSpec(nil), nodes...)
	sort.Slice(items, func(i, j int) bool { return items[i].TempID < items[j].TempID })
	b, _ := json.Marshal(items)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }

func (s *Store) Get(ctx context.Context, workspaceID, graphID string) (Graph, error) {
	w, err := uuid.Parse(workspaceID)
	if err != nil {
		return Graph{}, ErrInvalidGraph
	}
	g, err := uuid.Parse(graphID)
	if err != nil {
		return Graph{}, ErrInvalidGraph
	}
	return loadGraphTx(ctx, s.pool, w, g)
}

type rowQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

func loadGraphTx(ctx context.Context, q rowQuerier, workspaceID, graphID uuid.UUID) (Graph, error) {
	var g Graph
	var budget []byte
	err := q.QueryRow(ctx, `SELECT id::text,workspace_id::text,anchor_kind,anchor_id::text,status,current_version,admission_decision,budget_policy FROM work_graph WHERE workspace_id=$1 AND id=$2`, workspaceID, graphID).Scan(&g.ID, &g.WorkspaceID, &g.AnchorKind, &g.AnchorID, &g.Status, &g.CurrentVersion, &g.AdmissionDecision, &budget)
	if err != nil {
		return g, err
	}
	g.BudgetPolicy = budget
	g.Nodes = []Node{}
	g.Edges = []Edge{}
	rows, err := q.Query(ctx, `SELECT id::text,issue_id::text,role,context_policy,execution_status,validity_status,review_status,objective,completion_contract,budget,based_on_graph_version,created_at FROM work_graph_node WHERE graph_id=$1 ORDER BY created_at,id`, graphID)
	if err != nil {
		return g, err
	}
	for rows.Next() {
		var n Node
		var completion, b []byte
		if err = rows.Scan(&n.ID, &n.IssueID, &n.Role, &n.ContextPolicy, &n.ExecutionStatus, &n.ValidityStatus, &n.ReviewStatus, &n.Objective, &completion, &b, &n.BasedOnVersion, &n.CreatedAt); err != nil {
			rows.Close()
			return g, err
		}
		_ = json.Unmarshal(completion, &n.Completion)
		n.Budget = b
		g.Nodes = append(g.Nodes, n)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return g, err
	}
	rows, err = q.Query(ctx, `SELECT id::text,from_node_id::text,to_node_id::text,edge_type,required FROM work_graph_edge WHERE graph_id=$1 AND retired_version IS NULL ORDER BY created_at,id`, graphID)
	if err != nil {
		return g, err
	}
	for rows.Next() {
		var e Edge
		if err = rows.Scan(&e.ID, &e.FromNodeID, &e.ToNodeID, &e.Type, &e.Required); err != nil {
			rows.Close()
			return g, err
		}
		g.Edges = append(g.Edges, e)
	}
	rows.Close()
	return g, rows.Err()
}

func (s *Store) ReconcileReady(ctx context.Context, workspaceID, graphID string) ([]string, error) {
	w, err := uuid.Parse(workspaceID)
	if err != nil {
		return nil, ErrInvalidGraph
	}
	g, err := uuid.Parse(graphID)
	if err != nil {
		return nil, ErrInvalidGraph
	}
	rows, err := s.pool.Query(ctx, `UPDATE work_graph_node n SET execution_status='ready',updated_at=now() WHERE n.workspace_id=$1 AND n.graph_id=$2 AND n.execution_status IN ('queued','waiting') AND n.validity_status='valid' AND EXISTS (SELECT 1 FROM work_graph graph WHERE graph.id=$2 AND graph.workspace_id=$1 AND graph.status='active' AND n.based_on_graph_version=graph.current_version) AND NOT EXISTS (SELECT 1 FROM work_graph_edge e JOIN work_graph_node upstream ON upstream.id=e.from_node_id WHERE e.graph_id=n.graph_id AND e.to_node_id=n.id AND e.edge_type='depends_on' AND e.required AND e.retired_version IS NULL AND (upstream.execution_status<>'succeeded' OR upstream.validity_status<>'valid')) RETURNING n.id::text`, w, g)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if s.OnNodesReady != nil && len(ids) > 0 {
		issueRows, qerr := s.pool.Query(ctx, `SELECT issue_id::text FROM work_graph_node WHERE id=ANY($1::uuid[])`, ids)
		if qerr == nil {
			issueIDs := []string{}
			for issueRows.Next() {
				var id string
				if issueRows.Scan(&id) == nil {
					issueIDs = append(issueIDs, id)
				}
			}
			issueRows.Close()
			if len(issueIDs) > 0 {
				s.OnNodesReady(ctx, workspaceID, issueIDs)
			}
		}
	}
	return ids, nil
}

func (s *Store) InvalidateFrom(ctx context.Context, workspaceID, nodeID, reason string) ([]string, error) {
	w, err := uuid.Parse(workspaceID)
	if err != nil {
		return nil, ErrInvalidGraph
	}
	n, err := uuid.Parse(nodeID)
	if err != nil {
		return nil, ErrInvalidGraph
	}
	rows, err := s.pool.Query(ctx, `WITH RECURSIVE affected(id) AS (SELECT $2::uuid UNION SELECT e.to_node_id FROM work_graph_edge e JOIN affected a ON e.from_node_id=a.id WHERE e.workspace_id=$1 AND e.retired_version IS NULL AND e.edge_type IN ('depends_on','synthesizes','verifies','replicates')) UPDATE work_graph_node n SET validity_status=CASE WHEN n.id=$2 THEN 'invalidated' ELSE 'stale' END,updated_at=now() FROM affected a WHERE n.id=a.id AND n.workspace_id=$1 RETURNING n.id::text`, w, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if len(ids) > 0 {
		affected := make([]uuid.UUID, 0, len(ids))
		for _, id := range ids {
			parsed, parseErr := uuid.Parse(id)
			if parseErr != nil {
				return nil, parseErr
			}
			affected = append(affected, parsed)
		}
		_, _ = s.pool.Exec(ctx, `UPDATE work_verification_attempt SET stale_at=now() WHERE stale_at IS NULL AND subject_artifact_revision_id IN (SELECT a.id FROM work_artifact_revision a JOIN work_graph_node n ON n.id=a.producer_node_id WHERE n.id=ANY($1::uuid[]))`, affected)
	}
	_ = reason
	return ids, rows.Err()
}
