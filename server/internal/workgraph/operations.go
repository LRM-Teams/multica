package workgraph

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func (s *Store) UpdateNode(ctx context.Context, in NodeUpdateInput) (Node, error) {
	w, err := uuid.Parse(in.WorkspaceID)
	if err != nil {
		return Node{}, ErrInvalidGraph
	}
	g, err := uuid.Parse(in.GraphID)
	if err != nil {
		return Node{}, ErrInvalidGraph
	}
	n, err := uuid.Parse(in.NodeID)
	if err != nil {
		return Node{}, ErrInvalidGraph
	}
	if in.ExpectedGraphVersion <= 0 || strings.TrimSpace(in.Reason) == "" {
		return Node{}, ErrInvalidGraph
	}
	validExec := map[string]bool{"": true, "queued": true, "ready": true, "running": true, "waiting": true, "succeeded": true, "failed": true, "cancelled": true}
	validValidity := map[string]bool{"": true, "valid": true, "stale": true, "invalidated": true, "superseded": true}
	validReview := map[string]bool{"": true, "unreviewed": true, "reviewing": true, "accepted": true, "rejected": true, "blocked": true}
	if !validExec[in.ExecutionStatus] || !validValidity[in.ValidityStatus] || !validReview[in.ReviewStatus] {
		return Node{}, ErrInvalidGraph
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Node{}, err
	}
	defer tx.Rollback(ctx)
	var version int64
	if err = tx.QueryRow(ctx, `SELECT current_version FROM work_graph WHERE workspace_id=$1 AND id=$2 FOR UPDATE`, w, g).Scan(&version); err != nil {
		return Node{}, err
	}
	if version != in.ExpectedGraphVersion {
		return Node{}, ErrGraphConflict
	}
	var out Node
	var completion, budget []byte
	err = tx.QueryRow(ctx, `UPDATE work_graph_node SET execution_status=COALESCE(NULLIF($4,''),execution_status),validity_status=COALESCE(NULLIF($5,''),validity_status),review_status=COALESCE(NULLIF($6,''),review_status),updated_at=now() WHERE workspace_id=$1 AND graph_id=$2 AND id=$3 RETURNING id::text,issue_id::text,role,context_policy,execution_status,validity_status,review_status,objective,completion_contract,budget,based_on_graph_version,created_at`, w, g, n, in.ExecutionStatus, in.ValidityStatus, in.ReviewStatus).Scan(&out.ID, &out.IssueID, &out.Role, &out.ContextPolicy, &out.ExecutionStatus, &out.ValidityStatus, &out.ReviewStatus, &out.Objective, &completion, &budget, &out.BasedOnVersion, &out.CreatedAt)
	if err != nil {
		return Node{}, err
	}
	_ = json.Unmarshal(completion, &out.Completion)
	out.Budget = budget
	if _, err = tx.Exec(ctx, `INSERT INTO work_graph_change_event(workspace_id,graph_id,version,event_type,affected_nodes,reason,payload) VALUES($1,$2,$3,'node_changed',ARRAY[$4::uuid],$5,jsonb_build_object('execution_status',$6::text,'validity_status',$7::text,'review_status',$8::text))`, w, g, version, n, in.Reason, out.ExecutionStatus, out.ValidityStatus, out.ReviewStatus); err != nil {
		return Node{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Node{}, err
	}
	if out.ExecutionStatus == "succeeded" && out.ValidityStatus == "valid" {
		_, _ = s.ReconcileReady(ctx, in.WorkspaceID, in.GraphID)
	}
	if out.ExecutionStatus == "failed" {
		_, _ = s.InvalidateFrom(ctx, in.WorkspaceID, in.GraphID, in.NodeID, in.Reason)
	}
	return out, nil
}

func (s *Store) CompleteIssueNode(ctx context.Context, workspaceID, issueID string) error {
	w, err := uuid.Parse(workspaceID)
	if err != nil {
		return ErrInvalidGraph
	}
	issue, err := uuid.Parse(issueID)
	if err != nil {
		return ErrInvalidGraph
	}
	rows, err := s.pool.Query(ctx, `UPDATE work_graph_node SET execution_status='succeeded',updated_at=now() WHERE workspace_id=$1 AND issue_id=$2 AND execution_status IN('ready','running','waiting') RETURNING graph_id::text`, w, issue)
	if err != nil {
		return err
	}
	defer rows.Close()
	graphs := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return err
		}
		graphs = append(graphs, id)
	}
	if err = rows.Err(); err != nil {
		return err
	}
	for _, graphID := range graphs {
		_, _ = s.ReconcileReady(ctx, workspaceID, graphID)
		_ = s.RefreshDelivery(ctx, workspaceID, graphID)
	}
	return nil
}

func (s *Store) SyncRuntimeIssue(ctx context.Context, issue db.Issue) error {
	status := map[string]string{"backlog": "queued", "todo": "ready", "in_progress": "running", "in_review": "succeeded", "done": "succeeded", "blocked": "waiting", "cancelled": "cancelled"}[issue.Status]
	if status == "" {
		return nil
	}
	_, err := s.pool.Exec(ctx, `UPDATE work_graph_node SET execution_status=$3,updated_at=now() WHERE workspace_id=$1 AND issue_id=$2 AND execution_status<>'cancelled'`, issue.WorkspaceID, issue.ID, status)
	if err == nil && status == "succeeded" {
		return s.CompleteIssueNode(ctx, uuid.UUID(issue.WorkspaceID.Bytes).String(), uuid.UUID(issue.ID.Bytes).String())
	}
	return err
}

func (s *Store) AddArtifact(ctx context.Context, in ArtifactInput) (ArtifactRevision, error) {
	w, err := uuid.Parse(in.WorkspaceID)
	if err != nil {
		return ArtifactRevision{}, ErrInvalidGraph
	}
	g, err := uuid.Parse(in.GraphID)
	if err != nil {
		return ArtifactRevision{}, ErrInvalidGraph
	}
	producer, err := uuid.Parse(in.ProducerNodeID)
	if err != nil {
		return ArtifactRevision{}, ErrInvalidGraph
	}
	in.Digest = strings.TrimSpace(in.Digest)
	in.Kind = strings.TrimSpace(in.Kind)
	in.Locator = strings.TrimSpace(in.Locator)
	if in.Digest == "" || in.Kind == "" || in.Locator == "" {
		return ArtifactRevision{}, ErrInvalidGraph
	}
	if in.Metadata == nil {
		in.Metadata = json.RawMessage(`{}`)
	}
	if !json.Valid(in.Metadata) {
		return ArtifactRevision{}, ErrInvalidGraph
	}
	artifactID := uuid.New()
	if in.ArtifactID != "" {
		artifactID, err = uuid.Parse(in.ArtifactID)
		if err != nil {
			return ArtifactRevision{}, ErrInvalidGraph
		}
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ArtifactRevision{}, err
	}
	defer tx.Rollback(ctx)
	var revision int64
	if err = tx.QueryRow(ctx, `SELECT COALESCE(max(revision),0)+1 FROM work_artifact_revision WHERE workspace_id=$1 AND graph_id=$2 AND artifact_id=$3`, w, g, artifactID).Scan(&revision); err != nil {
		return ArtifactRevision{}, err
	}
	if revision > 1 {
		if _, err = tx.Exec(ctx, `UPDATE work_artifact_revision SET validity_status='superseded' WHERE workspace_id=$1 AND graph_id=$2 AND artifact_id=$3 AND validity_status='valid'`, w, g, artifactID); err != nil {
			return ArtifactRevision{}, err
		}
		if _, err = tx.Exec(ctx, `UPDATE work_verification_attempt SET stale_at=now() WHERE stale_at IS NULL AND subject_artifact_revision_id IN(SELECT id FROM work_artifact_revision WHERE workspace_id=$1 AND graph_id=$2 AND artifact_id=$3 AND revision<$4)`, w, g, artifactID, revision); err != nil {
			return ArtifactRevision{}, err
		}
	}
	var out ArtifactRevision
	var metadata []byte
	err = tx.QueryRow(ctx, `INSERT INTO work_artifact_revision(workspace_id,graph_id,artifact_id,producer_node_id,revision,digest,kind,locator,metadata) SELECT $1,$2,$3,n.id,$5,$6,$7,$8,$9 FROM work_graph_node n WHERE n.workspace_id=$1 AND n.graph_id=$2 AND n.id=$4 RETURNING id::text,artifact_id::text,producer_node_id::text,revision,digest,kind,locator,metadata,validity_status,created_at`, w, g, artifactID, producer, revision, in.Digest, in.Kind, in.Locator, in.Metadata).Scan(&out.ID, &out.ArtifactID, &out.ProducerNodeID, &out.Revision, &out.Digest, &out.Kind, &out.Locator, &metadata, &out.ValidityStatus, &out.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ArtifactRevision{}, ErrInvalidGraph
		}
		return ArtifactRevision{}, err
	}
	out.Metadata = metadata
	if err = tx.Commit(ctx); err != nil {
		return ArtifactRevision{}, err
	}
	if revision > 1 {
		_, _ = s.InvalidateFrom(ctx, in.WorkspaceID, in.GraphID, in.ProducerNodeID, "artifact revision changed")
		// The new revision is valid output from the producer. Only consumers need
		// recomputation; keep the producer itself eligible as their dependency.
		_, _ = s.pool.Exec(ctx, `UPDATE work_graph_node SET validity_status='valid',updated_at=now() WHERE workspace_id=$1 AND graph_id=$2 AND id=$3`, w, g, producer)
	}
	return out, nil
}

func (s *Store) AddVerification(ctx context.Context, in VerificationInput) (string, error) {
	w, err := uuid.Parse(in.WorkspaceID)
	if err != nil {
		return "", ErrInvalidGraph
	}
	g, err := uuid.Parse(in.GraphID)
	if err != nil {
		return "", ErrInvalidGraph
	}
	verifier, err := uuid.Parse(in.VerifierNodeID)
	if err != nil {
		return "", ErrInvalidGraph
	}
	artifact, err := uuid.Parse(in.ArtifactRevisionID)
	if err != nil {
		return "", ErrInvalidGraph
	}
	if in.Verdict != "PASS" && in.Verdict != "FAIL" && in.Verdict != "BLOCKED" {
		return "", ErrInvalidGraph
	}
	if strings.TrimSpace(in.ScopeDigest) == "" {
		return "", ErrInvalidGraph
	}
	if in.Findings == nil {
		in.Findings = json.RawMessage(`[]`)
	}
	if in.EvidenceRefs == nil {
		in.EvidenceRefs = json.RawMessage(`[]`)
	}
	if !json.Valid(in.Findings) || !json.Valid(in.EvidenceRefs) {
		return "", ErrInvalidGraph
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	var id string
	err = tx.QueryRow(ctx, `INSERT INTO work_verification_attempt(workspace_id,graph_id,verifier_node_id,subject_artifact_revision_id,scope_digest,verdict,findings,evidence_refs) SELECT $1,$2,$3,$4,$5,$6,$7,$8 WHERE EXISTS(SELECT 1 FROM work_graph_node WHERE workspace_id=$1 AND id=$3 AND graph_id=$2 AND role='verifier') AND EXISTS(SELECT 1 FROM work_artifact_revision WHERE workspace_id=$1 AND id=$4 AND graph_id=$2 AND validity_status='valid' AND producer_node_id<>$3) RETURNING id::text`, w, g, verifier, artifact, in.ScopeDigest, in.Verdict, in.Findings, in.EvidenceRefs).Scan(&id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrInvalidGraph
		}
		return "", err
	}
	review := map[string]string{"PASS": "accepted", "FAIL": "rejected", "BLOCKED": "blocked"}[in.Verdict]
	execution := "succeeded"
	if in.Verdict == "BLOCKED" {
		execution = "waiting"
	}
	if _, err = tx.Exec(ctx, `UPDATE work_graph_node SET review_status=$4,execution_status=$5,updated_at=now() WHERE workspace_id=$1 AND graph_id=$2 AND id=$3`, w, g, verifier, review, execution); err != nil {
		return "", err
	}
	if _, err = tx.Exec(ctx, `UPDATE work_graph SET status='deliverable',updated_at=now() WHERE workspace_id=$1 AND id=$2 AND status='active' AND NOT EXISTS(SELECT 1 FROM work_graph_node WHERE workspace_id=$1 AND graph_id=$2 AND (execution_status NOT IN('succeeded','cancelled') OR validity_status<>'valid' OR (role='verifier' AND review_status<>'accepted')))`, w, g); err != nil {
		return "", err
	}
	if err = tx.Commit(ctx); err != nil {
		return "", err
	}
	return id, nil
}

func (s *Store) RefreshDelivery(ctx context.Context, workspaceID, graphID string) error {
	w, err := uuid.Parse(workspaceID)
	if err != nil {
		return ErrInvalidGraph
	}
	g, err := uuid.Parse(graphID)
	if err != nil {
		return ErrInvalidGraph
	}
	var deliverable bool
	err = s.pool.QueryRow(ctx, `SELECT NOT EXISTS(SELECT 1 FROM work_graph_node WHERE workspace_id=$1 AND graph_id=$2 AND (execution_status NOT IN('succeeded','cancelled') OR validity_status<>'valid' OR (role='verifier' AND review_status<>'accepted')))`, w, g).Scan(&deliverable)
	if err != nil {
		return err
	}
	if deliverable {
		_, err = s.pool.Exec(ctx, `UPDATE work_graph SET status='deliverable',updated_at=now() WHERE workspace_id=$1 AND id=$2 AND status='active'`, w, g)
	}
	return err
}

func (s *Store) SyncGoalLifecycle(ctx context.Context, workspaceID, goalID, status string) error {
	w, err := uuid.Parse(workspaceID)
	if err != nil {
		return ErrInvalidGraph
	}
	goal, err := uuid.Parse(goalID)
	if err != nil {
		return ErrInvalidGraph
	}
	graphStatus := map[string]string{"active": "active", "paused": "paused", "cancelled": "cancelled", "completed": "completed"}[status]
	if graphStatus == "" {
		return ErrInvalidGraph
	}
	_, err = s.pool.Exec(ctx, `UPDATE work_graph SET status=$3,updated_at=now() WHERE workspace_id=$1 AND anchor_kind='channel_goal' AND anchor_id=$2 AND status NOT IN('completed','cancelled')`, w, goal, graphStatus)
	if err != nil {
		return err
	}
	if status == "cancelled" {
		_, err = s.pool.Exec(ctx, `UPDATE work_graph_node SET execution_status='cancelled',updated_at=now() WHERE graph_id IN(SELECT id FROM work_graph WHERE workspace_id=$1 AND anchor_kind='channel_goal' AND anchor_id=$2) AND execution_status IN('draft','queued','ready','waiting')`, w, goal)
	}
	return err
}
