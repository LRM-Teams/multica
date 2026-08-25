package workgraph

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	// Terminal review state is kernel-owned and may only be produced by an
	// immutable verification attempt. Generic node updates cannot self-approve
	// work or manufacture a rejection without findings/evidence.
	validReview := map[string]bool{"": true, "unreviewed": true, "reviewing": true}
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
	err = tx.QueryRow(ctx, `UPDATE work_graph_node SET execution_status=COALESCE(NULLIF($4,''),execution_status),validity_status=COALESCE(NULLIF($5,''),validity_status),review_status=COALESCE(NULLIF($6,''),review_status),updated_at=now() WHERE workspace_id=$1 AND graph_id=$2 AND id=$3 RETURNING id::text,issue_id::text,role,context_policy,execution_status,validity_status,review_status,completion_authority,effective_completion,objective,completion_contract,budget,based_on_graph_version,created_at`, w, g, n, in.ExecutionStatus, in.ValidityStatus, in.ReviewStatus).Scan(&out.ID, &out.IssueID, &out.Role, &out.ContextPolicy, &out.ExecutionStatus, &out.ValidityStatus, &out.ReviewStatus, &out.CompletionAuthority, &out.EffectiveCompletion, &out.Objective, &completion, &budget, &out.BasedOnVersion, &out.CreatedAt)
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
	if err = s.RefreshNodeCompletion(ctx, in.WorkspaceID, in.GraphID, in.NodeID); err != nil {
		return Node{}, err
	}
	if err = s.pool.QueryRow(ctx, `SELECT effective_completion FROM work_graph_node WHERE workspace_id=$1 AND graph_id=$2 AND id=$3`, w, g, n).Scan(&out.EffectiveCompletion); err != nil {
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
		if err = s.RefreshNodeCompletionByIssue(ctx, workspaceID, graphID, issueID); err != nil {
			return err
		}
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
	var hadPriorOutput bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM work_artifact_revision
		WHERE workspace_id=$1 AND graph_id=$2 AND producer_node_id=$3
	)`, w, g, producer).Scan(&hadPriorOutput); err != nil {
		return ArtifactRevision{}, err
	}
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
	// Any new producer output invalidates prior review attempts, even when the
	// producer used a new artifact_id. Review approval is bound to the latest
	// immutable output, never to a stale successful attempt.
	if _, err = tx.Exec(ctx, `UPDATE work_verification_attempt SET stale_at=now() WHERE stale_at IS NULL AND producer_node_id=$1`, producer); err != nil {
		return ArtifactRevision{}, err
	}
	type reviewTarget struct {
		nodeID        uuid.UUID
		issueID       uuid.UUID
		contextPolicy string
	}
	reviewRows, err := tx.Query(ctx, `
		SELECT verifier.id,verifier.issue_id,verifier.context_policy
		FROM work_graph_edge edge
		JOIN work_graph_node verifier ON verifier.id=edge.to_node_id
		WHERE edge.workspace_id=$1 AND edge.graph_id=$2 AND edge.from_node_id=$3
		  AND edge.edge_type='depends_on' AND edge.required AND edge.retired_version IS NULL
		  AND verifier.role='verifier'
	`, w, g, producer)
	if err != nil {
		return ArtifactRevision{}, err
	}
	reviewTargets := []reviewTarget{}
	for reviewRows.Next() {
		var target reviewTarget
		if err = reviewRows.Scan(&target.nodeID, &target.issueID, &target.contextPolicy); err != nil {
			reviewRows.Close()
			return ArtifactRevision{}, err
		}
		reviewTargets = append(reviewTargets, target)
	}
	reviewRows.Close()
	if err = reviewRows.Err(); err != nil {
		return ArtifactRevision{}, err
	}
	if len(reviewTargets) > 0 {
		if _, err = tx.Exec(ctx, `UPDATE work_graph_node SET review_status='unreviewed',effective_completion='pending',updated_at=now() WHERE workspace_id=$1 AND graph_id=$2 AND id=$3`, w, g, producer); err != nil {
			return ArtifactRevision{}, err
		}
	}
	for _, target := range reviewTargets {
		// A new output creates a new review attempt. Re-open the verifier node and
		// its Issue; the frontier will dispatch it only after producer execution
		// reaches succeeded.
		if _, err = tx.Exec(ctx, `UPDATE work_graph_node SET execution_status='queued',validity_status='valid',review_status='unreviewed',effective_completion='pending',updated_at=now() WHERE workspace_id=$1 AND graph_id=$2 AND id=$3`, w, g, target.nodeID); err != nil {
			return ArtifactRevision{}, err
		}
		if _, err = tx.Exec(ctx, `UPDATE issue SET status='backlog',updated_at=now() WHERE workspace_id=$1 AND id=$2 AND status NOT IN('cancelled')`, w, target.issueID); err != nil {
			return ArtifactRevision{}, err
		}

		// A previous ephemeral reviewer was archived with its verdict. Materialize
		// a new clean identity for this artifact revision before the node becomes
		// ready.
		var sourceAgent uuid.UUID
		var assignmentStatus string
		assignErr := tx.QueryRow(ctx, `SELECT source_agent_id,status FROM work_review_agent_assignment WHERE workspace_id=$1 AND verifier_node_id=$2 FOR UPDATE`, w, target.nodeID).Scan(&sourceAgent, &assignmentStatus)
		if assignErr == nil && assignmentStatus == "archived" {
			derived, cloneErr := cloneReviewAgent(ctx, tx, w, sourceAgent, out.ID, target.nodeID.String())
			if cloneErr != nil {
				return ArtifactRevision{}, cloneErr
			}
			if _, err = tx.Exec(ctx, `UPDATE work_review_agent_assignment SET derived_agent_id=$3,status='active',archived_at=NULL WHERE workspace_id=$1 AND verifier_node_id=$2`, w, target.nodeID, derived); err != nil {
				return ArtifactRevision{}, err
			}
			if _, err = tx.Exec(ctx, `UPDATE issue SET assignee_id=$3,updated_at=now() WHERE workspace_id=$1 AND id=$2`, w, target.issueID, derived); err != nil {
				return ArtifactRevision{}, err
			}
		} else if assignErr != nil && !errors.Is(assignErr, pgx.ErrNoRows) {
			return ArtifactRevision{}, assignErr
		}

		envelope := fmt.Sprintf("Review envelope (server-authored, immutable subject)\n\n- producer_node_id: `%s`\n- artifact_revision_id: `%s`\n- artifact_id: `%s`\n- revision: `%d`\n- digest: `%s`\n- kind: `%s`\n- locator: `%s`\n- context_policy: `%s`\n\nReview only this artifact revision against the Goal and completion contract. Do not use the producer's private session, private memory, or unsupported claims. Submit APPROVED evidence with verdict `PASS`, request rework with `FAIL`, or report an external blocker with `BLOCKED` using `multica issue graph verification %s --input-file <json>`.\n", producer, out.ID, out.ArtifactID, out.Revision, out.Digest, out.Kind, out.Locator, target.contextPolicy, in.GraphID)
		if _, err = tx.Exec(ctx, `INSERT INTO comment(issue_id,workspace_id,author_type,author_id,content,type) VALUES($1,$2,'system',$3,$4,'system')`, target.issueID, w, uuid.Nil, envelope); err != nil {
			return ArtifactRevision{}, err
		}
	}
	if hadPriorOutput {
		// A replacement output invalidates and re-opens the downstream subgraph in
		// the same transaction as the artifact. The producer itself remains valid
		// but pending its new review gate.
		if _, err = tx.Exec(ctx, `UPDATE work_graph_node SET validity_status='valid',review_status='unreviewed',effective_completion='pending',updated_at=now() WHERE workspace_id=$1 AND graph_id=$2 AND id=$3`, w, g, producer); err != nil {
			return ArtifactRevision{}, err
		}
		if err = reopenConsumersAfterArtifactChange(ctx, tx, w, g, producer); err != nil {
			return ArtifactRevision{}, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return ArtifactRevision{}, err
	}
	if err = s.RefreshNodeCompletion(ctx, in.WorkspaceID, in.GraphID, in.ProducerNodeID); err != nil {
		return ArtifactRevision{}, err
	}
	_, _ = s.ReconcileReady(ctx, in.WorkspaceID, in.GraphID)
	_ = s.RefreshDelivery(ctx, in.WorkspaceID, in.GraphID)
	return out, nil
}

func reopenConsumersAfterArtifactChange(ctx context.Context, tx pgx.Tx, workspaceID, graphID, producerNodeID uuid.UUID) error {
	_, err := tx.Exec(ctx, `WITH RECURSIVE affected(id) AS (
		SELECT edge.to_node_id
		FROM work_graph_edge edge
		WHERE edge.workspace_id=$1 AND edge.graph_id=$2 AND edge.from_node_id=$3
		  AND edge.retired_version IS NULL
		UNION
		SELECT edge.to_node_id
		FROM work_graph_edge edge
		JOIN affected parent ON edge.from_node_id=parent.id
		WHERE edge.workspace_id=$1 AND edge.graph_id=$2 AND edge.retired_version IS NULL
	), stale_artifacts AS (
		UPDATE work_artifact_revision artifact SET validity_status='stale'
		WHERE artifact.workspace_id=$1 AND artifact.graph_id=$2
		  AND artifact.producer_node_id IN(SELECT id FROM affected)
		  AND artifact.validity_status='valid'
		RETURNING artifact.id
	), stale_attempts AS (
		UPDATE work_verification_attempt attempt SET stale_at=now()
		WHERE attempt.workspace_id=$1 AND attempt.graph_id=$2 AND attempt.stale_at IS NULL
		  AND (attempt.producer_node_id IN(SELECT id FROM affected)
		       OR attempt.verifier_node_id IN(SELECT id FROM affected)
		       OR attempt.subject_artifact_revision_id IN(SELECT id FROM stale_artifacts))
		RETURNING attempt.id
	), reopened AS (
		UPDATE work_graph_node node
		SET execution_status='queued',validity_status='valid',review_status='unreviewed',effective_completion='pending',updated_at=now()
		WHERE node.workspace_id=$1 AND node.graph_id=$2 AND node.id IN(SELECT id FROM affected)
		  AND node.execution_status<>'cancelled'
		RETURNING node.issue_id
	)
	UPDATE issue item SET status='backlog',updated_at=now()
	WHERE item.workspace_id=$1 AND item.id IN(SELECT issue_id FROM reopened)
	  AND item.status<>'cancelled'`, workspaceID, graphID, producerNodeID)
	return err
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
	reviewer, err := uuid.Parse(in.ReviewerAgentID)
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
	var producer uuid.UUID
	var producerIssue, verifierIssue uuid.UUID
	var logicalArtifactID uuid.UUID
	var contextPolicy string
	var independent bool
	err = tx.QueryRow(ctx, `
		SELECT artifact.producer_node_id,producer.issue_id,verifier.issue_id,artifact.artifact_id,verifier.context_policy,
		       COALESCE(reviewer_agent.source_agent_id,reviewer_agent.id)
		         <> COALESCE(producer_agent.source_agent_id,producer_agent.id)
		FROM work_artifact_revision artifact
		JOIN work_graph_node producer ON producer.id=artifact.producer_node_id
		JOIN work_graph_node verifier ON verifier.workspace_id=producer.workspace_id
		  AND verifier.graph_id=producer.graph_id AND verifier.id=$3 AND verifier.role='verifier'
		JOIN issue verifier_item ON verifier_item.id=verifier.issue_id
		  AND verifier_item.workspace_id=verifier.workspace_id
		  AND verifier_item.assignee_type='agent' AND verifier_item.assignee_id=$5
		JOIN work_graph_edge edge ON edge.graph_id=producer.graph_id
		  AND edge.from_node_id=producer.id AND edge.to_node_id=verifier.id
		  AND edge.edge_type='depends_on' AND edge.required AND edge.retired_version IS NULL
		JOIN issue producer_item ON producer_item.id=producer.issue_id
		JOIN agent producer_agent ON producer_agent.id=producer_item.assignee_id
		JOIN agent reviewer_agent ON reviewer_agent.id=$5 AND reviewer_agent.workspace_id=producer.workspace_id
		WHERE artifact.workspace_id=$1 AND artifact.graph_id=$2 AND artifact.id=$4
		  AND artifact.validity_status='valid'
		  AND producer.execution_status='succeeded' AND producer.validity_status='valid'
		  AND NOT (verifier.execution_status='succeeded' AND verifier.review_status IN('accepted','rejected'))
		  AND NOT EXISTS (
		    SELECT 1 FROM work_artifact_revision newer
		    WHERE newer.workspace_id=artifact.workspace_id AND newer.graph_id=artifact.graph_id
		      AND newer.producer_node_id=artifact.producer_node_id
		      AND newer.validity_status='valid'
		      AND (newer.created_at,newer.id)>(artifact.created_at,artifact.id)
		  )
		FOR UPDATE OF artifact,producer,verifier
	`, w, g, verifier, artifact, reviewer).Scan(&producer, &producerIssue, &verifierIssue, &logicalArtifactID, &contextPolicy, &independent)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrInvalidGraph
	}
	if err != nil {
		return "", err
	}
	if !independent {
		return "", ErrGraphForbidden
	}
	if _, err = tx.Exec(ctx, `UPDATE work_verification_attempt SET stale_at=now()
		WHERE workspace_id=$1 AND graph_id=$2 AND producer_node_id=$3
		  AND verifier_node_id=$4 AND stale_at IS NULL`, w, g, producer, verifier); err != nil {
		return "", err
	}
	var id string
	err = tx.QueryRow(ctx, `INSERT INTO work_verification_attempt(workspace_id,graph_id,verifier_node_id,subject_artifact_revision_id,producer_node_id,reviewer_agent_id,context_policy,scope_digest,verdict,findings,evidence_refs) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) RETURNING id::text`, w, g, verifier, artifact, producer, reviewer, contextPolicy, in.ScopeDigest, in.Verdict, in.Findings, in.EvidenceRefs).Scan(&id)
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
	producerExecution := "succeeded"
	producerIssueStatus := "done"
	if in.Verdict == "FAIL" {
		producerExecution = "ready"
		producerIssueStatus = "todo"
	}
	if in.Verdict == "BLOCKED" {
		producerIssueStatus = "blocked"
	}
	if _, err = tx.Exec(ctx, `UPDATE work_graph_node SET review_status=$4,execution_status=$5,effective_completion='pending',updated_at=now() WHERE workspace_id=$1 AND graph_id=$2 AND id=$3`, w, g, producer, review, producerExecution); err != nil {
		return "", err
	}
	verifierIssueStatus := "done"
	if in.Verdict == "BLOCKED" {
		verifierIssueStatus = "blocked"
	}
	if _, err = tx.Exec(ctx, `UPDATE issue SET status=$3,updated_at=now() WHERE workspace_id=$1 AND id=$2`, w, verifierIssue, verifierIssueStatus); err != nil {
		return "", err
	}
	if _, err = tx.Exec(ctx, `UPDATE issue SET status=$3,updated_at=now() WHERE workspace_id=$1 AND id=$2 AND status<>'cancelled'`, w, producerIssue, producerIssueStatus); err != nil {
		return "", err
	}
	if in.Verdict == "FAIL" {
		feedback := fmt.Sprintf("Independent review requested changes for artifact revision `%s`.\n\nFindings: `%s`\n\nEvidence: `%s`\n\nRevise the work, then register a new revision using artifact_id `%s`.", in.ArtifactRevisionID, string(in.Findings), string(in.EvidenceRefs), logicalArtifactID)
		if _, err = tx.Exec(ctx, `INSERT INTO comment(issue_id,workspace_id,author_type,author_id,content,type) VALUES($1,$2,'system',$3,$4,'system')`, producerIssue, w, uuid.Nil, feedback); err != nil {
			return "", err
		}
		// Rejected evidence cannot be approved later without rework. The worker
		// must register a new immutable artifact revision before another review.
		if _, err = tx.Exec(ctx, `UPDATE work_artifact_revision SET validity_status='stale'
			WHERE workspace_id=$1 AND graph_id=$2 AND id=$3`, w, g, artifact); err != nil {
			return "", err
		}
	}
	var graphVersion int64
	if err = tx.QueryRow(ctx, `SELECT current_version FROM work_graph WHERE workspace_id=$1 AND id=$2`, w, g).Scan(&graphVersion); err != nil {
		return "", err
	}
	eventType := map[string]string{"PASS": "review_passed", "FAIL": "rework_requested", "BLOCKED": "review_blocked"}[in.Verdict]
	if _, err = tx.Exec(ctx, `INSERT INTO work_graph_change_event(workspace_id,graph_id,version,event_type,affected_nodes,reason,payload)
		VALUES($1,$2,$3,$4,ARRAY[$5::uuid,$6::uuid],$7,
		jsonb_build_object('verification_attempt_id',$8::text,'artifact_revision_id',$9::text,'reviewer_agent_id',$10::text,'context_policy',$11::text))`,
		w, g, graphVersion, eventType, producer, verifier, "independent review verdict: "+in.Verdict, id, artifact, reviewer, contextPolicy); err != nil {
		return "", err
	}
	if in.Verdict == "FAIL" && s.OnNodesReadyTx != nil {
		if err = s.OnNodesReadyTx(ctx, tx, in.WorkspaceID, []string{producerIssue.String()}); err != nil {
			return "", err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return "", err
	}
	if err = s.RefreshNodeCompletion(ctx, in.WorkspaceID, in.GraphID, producer.String()); err != nil {
		return "", err
	}
	if err = s.RefreshNodeCompletion(ctx, in.WorkspaceID, in.GraphID, in.VerifierNodeID); err != nil {
		return "", err
	}
	if in.Verdict == "FAIL" && s.OnNodesReady != nil {
		s.OnNodesReady(ctx, in.WorkspaceID, []string{producerIssue.String()})
	}
	_, _ = s.ReconcileReady(ctx, in.WorkspaceID, in.GraphID)
	_ = s.RefreshDelivery(ctx, in.WorkspaceID, in.GraphID)
	if in.Verdict != "BLOCKED" {
		_ = s.ArchiveReviewAgentForNode(ctx, in.WorkspaceID, in.VerifierNodeID)
	}
	if s.OnGraphDelta != nil {
		s.OnGraphDelta(ctx, in.WorkspaceID, in.GraphID, eventType)
	}
	return id, nil
}

// RefreshNodeCompletion derives the only completion signal that may unlock
// downstream nodes. Kernel-managed nodes require durable evidence; legacy
// migrated graphs may retain issue_status authority until explicitly revised.
func (s *Store) RefreshNodeCompletion(ctx context.Context, workspaceID, graphID, nodeID string) error {
	w, err := uuid.Parse(workspaceID)
	if err != nil {
		return ErrInvalidGraph
	}
	g, err := uuid.Parse(graphID)
	if err != nil {
		return ErrInvalidGraph
	}
	n, err := uuid.Parse(nodeID)
	if err != nil {
		return ErrInvalidGraph
	}
	_, err = s.pool.Exec(ctx, `UPDATE work_graph_node n SET effective_completion=CASE
		WHEN n.validity_status='invalidated' THEN 'revoked'
		WHEN n.validity_status IN('stale','superseded') THEN 'stale'
		WHEN n.completion_authority='issue_status' AND n.execution_status='succeeded' AND n.validity_status='valid' THEN 'satisfied'
		WHEN n.completion_authority='kernel_evidence' AND n.role='verifier' AND n.execution_status='succeeded' AND n.validity_status='valid' AND n.review_status='accepted' THEN 'satisfied'
		WHEN n.completion_authority='kernel_evidence' AND n.role<>'verifier'
		 AND n.execution_status='succeeded' AND n.validity_status='valid'
		 AND EXISTS(SELECT 1 FROM work_artifact_revision a WHERE a.workspace_id=n.workspace_id AND a.graph_id=n.graph_id AND a.producer_node_id=n.id AND a.validity_status='valid')
		 AND NOT EXISTS (
		   SELECT 1 FROM work_graph_edge edge
		   JOIN work_graph_node verifier ON verifier.id=edge.to_node_id AND verifier.role='verifier'
		   WHERE edge.graph_id=n.graph_id AND edge.from_node_id=n.id
		     AND edge.edge_type='depends_on' AND edge.required AND edge.retired_version IS NULL
		     AND NOT EXISTS (
		       SELECT 1 FROM work_verification_attempt attempt
		       JOIN work_artifact_revision artifact ON artifact.id=attempt.subject_artifact_revision_id
		       WHERE attempt.workspace_id=n.workspace_id AND attempt.graph_id=n.graph_id
		         AND attempt.producer_node_id=n.id AND attempt.verifier_node_id=verifier.id
		         AND attempt.verdict='PASS' AND attempt.stale_at IS NULL
		         AND artifact.validity_status='valid'
		     )
		 ) THEN 'satisfied'
		ELSE 'pending' END,updated_at=now()
		WHERE n.workspace_id=$1 AND n.graph_id=$2 AND n.id=$3`, w, g, n)
	return err
}

func (s *Store) RefreshNodeCompletionByIssue(ctx context.Context, workspaceID, graphID, issueID string) error {
	w, err := uuid.Parse(workspaceID)
	if err != nil {
		return ErrInvalidGraph
	}
	g, err := uuid.Parse(graphID)
	if err != nil {
		return ErrInvalidGraph
	}
	i, err := uuid.Parse(issueID)
	if err != nil {
		return ErrInvalidGraph
	}
	var nodeID string
	if err = s.pool.QueryRow(ctx, `SELECT id::text FROM work_graph_node WHERE workspace_id=$1 AND graph_id=$2 AND issue_id=$3`, w, g, i).Scan(&nodeID); err != nil {
		return err
	}
	return s.RefreshNodeCompletion(ctx, workspaceID, graphID, nodeID)
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
	err = s.pool.QueryRow(ctx, `SELECT NOT EXISTS(
		SELECT 1 FROM work_graph_node node
		JOIN work_graph graph ON graph.id=node.graph_id AND graph.workspace_id=node.workspace_id
		WHERE node.workspace_id=$1 AND node.graph_id=$2
		  AND node.based_on_graph_version=graph.current_version
		  AND node.effective_completion<>'satisfied'
	)`, w, g).Scan(&deliverable)
	if err != nil {
		return err
	}
	if deliverable {
		_, err = s.pool.Exec(ctx, `UPDATE work_graph SET status='deliverable',updated_at=now() WHERE workspace_id=$1 AND id=$2 AND status='active'`, w, g)
	} else {
		// A new artifact revision or explicit invalidation revokes delivery until
		// the current graph version satisfies its completion gates again.
		_, err = s.pool.Exec(ctx, `UPDATE work_graph SET status='active',updated_at=now() WHERE workspace_id=$1 AND id=$2 AND status='deliverable'`, w, g)
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
	if err == nil && (status == "cancelled" || status == "completed") {
		err = s.archiveReviewAgentsForGoal(ctx, w, goal)
	}
	return err
}
