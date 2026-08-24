package researchrun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (s *PostgresStore) acceptAtomicResultV6Tx(ctx context.Context, tx pgx.Tx, submissionID string, decoded DecodedV6Contract) (V6AcceptedResultNode, error) {
	var in V6AtomicResultSubmission
	if err := json.Unmarshal(decoded.Envelope, &in); err != nil {
		return V6AcceptedResultNode{}, fmt.Errorf("%w: atomic result envelope", ErrInvalidContract)
	}
	if in.ContentHash != decoded.ContentHash || len(in.BranchRefs) == 0 {
		return V6AcceptedResultNode{}, fmt.Errorf("%w: atomic result binding", ErrInvalidContract)
	}

	var attemptStatus, manifestID, manifestHash, assignedAgentID, membershipID string
	var manifest json.RawMessage
	var workStatus, orchestrator string
	var goalVersion int
	err := tx.QueryRow(ctx, `SELECT a.status,a.manifest_id::text,a.manifest_hash,a.manifest,a.assigned_agent_id::text,
		a.membership_id::text,w.status,w.goal_version,s.orchestrator_version
		FROM research_work_item_attempt a
		JOIN research_work_item w ON w.id=a.work_item_id
		JOIN research_session s ON (s.workspace_id,s.id)=(a.workspace_id,a.session_id)
		WHERE a.workspace_id=$1::uuid AND a.session_id=$2::uuid AND a.work_item_id=$3::uuid AND a.id=$4::uuid
		FOR UPDATE OF a,w,s`, in.WorkspaceID, in.RunID, in.WorkItemID, in.AttemptID).Scan(
		&attemptStatus, &manifestID, &manifestHash, &manifest, &assignedAgentID, &membershipID, &workStatus, &goalVersion, &orchestrator)
	if errors.Is(err, pgx.ErrNoRows) {
		return V6AcceptedResultNode{}, ErrAttemptNotAssigned
	}
	if err != nil {
		return V6AcceptedResultNode{}, err
	}
	if orchestrator != OrchestratorVersionV6 || attemptStatus != "running" || (workStatus != "running" && workStatus != "awaiting_input") ||
		assignedAgentID != in.AgentID || manifestID != in.ManifestID || manifestHash != in.ManifestHash || goalVersion != in.GoalVersion {
		return V6AcceptedResultNode{}, ErrWorkItemChanged
	}
	var taskBound bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM research_task WHERE workspace_id=$1::uuid AND session_id=$2::uuid
		AND id=$3::uuid AND work_item_id=$4::uuid AND goal_version=$5)`, in.WorkspaceID, in.RunID, in.TaskID, in.WorkItemID, in.GoalVersion).Scan(&taskBound); err != nil {
		return V6AcceptedResultNode{}, err
	}
	if !taskBound {
		return V6AcceptedResultNode{}, fmt.Errorf("%w: task is not bound to Work Item", ErrInvalidContract)
	}
	if err = validateV6EvidenceRefsTx(ctx, tx, in.WorkspaceID, in.RunID, manifest, in.EvidenceRefs); err != nil {
		return V6AcceptedResultNode{}, err
	}
	branchIDs := make([]string, len(in.BranchRefs))
	branchVersions := make(map[string]int64, len(in.BranchRefs))
	for index, ref := range in.BranchRefs {
		branchIDs[index] = ref.ID
		branchVersions[ref.ID] = ref.StateVersion
	}
	sort.Strings(branchIDs)
	for _, branchID := range branchIDs {
		var stateVersion int64
		var branchGoalVersion int
		var status string
		err = tx.QueryRow(ctx, `SELECT state_version,goal_version,status FROM research_branch
			WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$3::uuid FOR UPDATE`, in.WorkspaceID, in.RunID, branchID).Scan(&stateVersion, &branchGoalVersion, &status)
		if errors.Is(err, pgx.ErrNoRows) || stateVersion != branchVersions[branchID] || branchGoalVersion != in.GoalVersion || status != "active" {
			return V6AcceptedResultNode{}, ErrWorkItemChanged
		}
		if err != nil {
			return V6AcceptedResultNode{}, err
		}
	}

	resultArtifactID := uuid.NewString()
	resultNodeID := uuid.NewString()
	now := time.Now().UTC()
	goal := int32(in.GoalVersion)
	if err = registerArtifactPassportTx(ctx, tx, registerArtifactPassportInput{
		WorkspaceID: in.WorkspaceID, SessionID: in.RunID, EntityID: resultArtifactID,
		Kind: ArtifactKindResultArtifact, SourceCreatedAt: &now, ProvenanceCompleteness: ArtifactProvenanceComplete,
		GoalVersion: &goal, SchemaVersion: "research-run-v6",
		AccessLevel: ArtifactAccessRaw, HashOrigin: ArtifactHashOriginProduction, ContentHash: decoded.ContentHash,
		ProducedByWorkItemID: in.WorkItemID, ProducedByWorkAttemptID: in.AttemptID, ProducedByAgentID: in.AgentID,
	}); err != nil {
		return V6AcceptedResultNode{}, err
	}
	var artifactVersionID string
	if err = tx.QueryRow(ctx, `SELECT id::text FROM research_artifact_version
		WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND artifact_id=$3::uuid AND version=1`, in.WorkspaceID, in.RunID, resultArtifactID).Scan(&artifactVersionID); err != nil {
		return V6AcceptedResultNode{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE research_work_item_attempt SET result_kind='result_node',result_entity_id=$5::uuid,
		result_artifact_id=$6::uuid,result_hash=$7,client_request_id=$8::uuid,result_submitted_at=now()
		WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND work_item_id=$3::uuid AND id=$4::uuid`,
		in.WorkspaceID, in.RunID, in.WorkItemID, in.AttemptID, resultNodeID, resultArtifactID, decoded.ContentHash, in.ClientRequestID); err != nil {
		return V6AcceptedResultNode{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO research_result_artifact(id,workspace_id,session_id,attempt_id,work_item_attempt_id,
		orchestrator_version,result_schema_version,result,client_request_id,content_hash,accepted_at,
		acceptance_work_manifest_id,acceptance_work_manifest_hash,resolved_input_versions_v6,acceptance_lineage_v6)
		VALUES($1::uuid,$2::uuid,$3::uuid,NULL,$4::uuid,$5,'6',$6::jsonb,$7,$8,now(),$9::uuid,$10,$11::jsonb,$12::jsonb)`,
		resultArtifactID, in.WorkspaceID, in.RunID, in.AttemptID, OrchestratorVersionV6, decoded.Canonical,
		in.ClientRequestID, decoded.ContentHash, in.ManifestID, in.ManifestHash, v6EvidenceVersionIDs(in.EvidenceRefs), v6EvidenceLineage(in.EvidenceRefs)); err != nil {
		return V6AcceptedResultNode{}, err
	}
	terminationCode, terminationDetail := v6Termination(in.StateProposal.Termination)
	if _, err = tx.Exec(ctx, `INSERT INTO research_result_node(id,workspace_id,session_id,result_artifact_id,artifact_version_id,work_item_attempt_id,
		catalog_summary,brief_summary,objective,conclusion,content,scope,uncertainties,conflicts,open_questions,
		conclusion_state,integration_state,reason_code,reason_detail,content_hash)
		VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,$6::uuid,$7,$8,$9,$10,$11,$12::jsonb,$13::jsonb,$14::jsonb,$15::jsonb,$16,$17,$18,$19,$20)`,
		resultNodeID, in.WorkspaceID, in.RunID, resultArtifactID, artifactVersionID, in.AttemptID,
		in.ContentLayers.CatalogSummary, in.ContentLayers.BriefSummary, in.ContentLayers.Objective, in.ContentLayers.Conclusion,
		in.ContentLayers.Content, in.ContentLayers.Scope, in.ContentLayers.Uncertainties, in.ContentLayers.Conflicts,
		in.ContentLayers.OpenQuestions, in.StateProposal.ConclusionState, in.StateProposal.IntegrationState,
		terminationCode, terminationDetail, decoded.ContentHash); err != nil {
		return V6AcceptedResultNode{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE research_artifact_passport SET lifecycle_status='accepted',accepted_at=now()
		WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$3::uuid`, in.WorkspaceID, in.RunID, resultArtifactID); err != nil {
		return V6AcceptedResultNode{}, err
	}
	event, err := appendEvent(ctx, tx, in.WorkspaceID, in.RunID, "v6_result_node_accepted", "v6-result-node:"+in.ClientRequestID,
		"agent", in.AgentID, map[string]any{"result_node_id": resultNodeID, "result_artifact_id": resultArtifactID,
			"artifact_version_id": artifactVersionID, "work_item_id": in.WorkItemID, "work_item_attempt_id": in.AttemptID, "tier": "S", "content_hash": decoded.ContentHash})
	if err != nil {
		return V6AcceptedResultNode{}, err
	}
	for _, branchID := range branchIDs {
		if _, err = tx.Exec(ctx, `INSERT INTO research_node_branch(workspace_id,session_id,node_artifact_version_id,branch_id)
			VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid)`, in.WorkspaceID, in.RunID, artifactVersionID, branchID); err != nil {
			return V6AcceptedResultNode{}, err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO research_branch_frontier(workspace_id,session_id,branch_id,node_artifact_version_id,tier,added_by_event_sequence)
			VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,'S',$5)`, in.WorkspaceID, in.RunID, branchID, artifactVersionID, event.Sequence); err != nil {
			return V6AcceptedResultNode{}, err
		}
	}
	if _, err = tx.Exec(ctx, `INSERT INTO research_node_steward_assignment(workspace_id,session_id,node_artifact_version_id,
		agent_id,membership_id,generation,status,reason)
		SELECT $1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,COALESCE(max(generation),0)+1,'active','accepted_result_owner'
		FROM research_node_steward_assignment WHERE session_id=$2::uuid AND node_artifact_version_id=$3::uuid`,
		in.WorkspaceID, in.RunID, artifactVersionID, in.AgentID, membershipID); err != nil {
		return V6AcceptedResultNode{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE research_work_item_attempt SET status='succeeded',completed_at=now(),updated_at=now() WHERE id=$1::uuid`, in.AttemptID); err != nil {
		return V6AcceptedResultNode{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE research_work_item SET status='succeeded',state_version=state_version+1,completed_at=now(),lease_token=NULL,lease_expires_at=NULL,updated_at=now() WHERE id=$1::uuid`, in.WorkItemID); err != nil {
		return V6AcceptedResultNode{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE research_team_membership SET state='idle' WHERE id=$1::uuid AND state='working'`, membershipID); err != nil {
		return V6AcceptedResultNode{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE research_v6_work_submission SET status='accepted',outcome=jsonb_build_object('result_node_id',$2::text,'result_artifact_id',$3::text,'artifact_version_id',$4::text),updated_at=now() WHERE id=$1::uuid`, submissionID, resultNodeID, resultArtifactID, artifactVersionID); err != nil {
		return V6AcceptedResultNode{}, err
	}
	return V6AcceptedResultNode{ID: resultNodeID, ResultArtifactID: resultArtifactID, ArtifactVersionID: artifactVersionID, WorkItemAttemptID: in.AttemptID, ContentHash: decoded.ContentHash}, nil
}

func validateV6EvidenceRefsTx(ctx context.Context, tx pgx.Tx, workspaceID, runID string, manifest json.RawMessage, refs []V6EvidenceRef) error {
	var frozen struct {
		Artifacts []struct {
			ArtifactVersionID string `json:"artifact_version_id"`
		} `json:"artifacts"`
	}
	if json.Unmarshal(manifest, &frozen) != nil {
		return fmt.Errorf("%w: Work Manifest artifacts", ErrInvalidContract)
	}
	authorized := map[string]struct{}{}
	for _, artifact := range frozen.Artifacts {
		authorized[artifact.ArtifactVersionID] = struct{}{}
	}
	for _, ref := range refs {
		if _, ok := authorized[ref.VersionID]; !ok {
			return fmt.Errorf("%w: evidence version was not frozen", ErrAttemptNotAssigned)
		}
		var kind, artifactID, hash, lifecycle string
		err := tx.QueryRow(ctx, `SELECT p.entity_kind,p.id::text,v.content_hash,p.lifecycle_status
			FROM research_artifact_version v JOIN research_artifact_passport p ON p.id=v.artifact_id
			WHERE v.workspace_id=$1::uuid AND v.session_id=$2::uuid AND v.id=$3::uuid`, workspaceID, runID, ref.VersionID).Scan(&kind, &artifactID, &hash, &lifecycle)
		if err != nil || kind != ref.Kind || artifactID != ref.ID || hash != ref.ContentHash || lifecycle != "accepted" {
			return fmt.Errorf("%w: evidence passport mismatch", ErrResultConflict)
		}
	}
	return nil
}

func v6EvidenceVersionIDs(refs []V6EvidenceRef) json.RawMessage {
	ids := make([]string, len(refs))
	for i := range refs {
		ids[i] = refs[i].VersionID
	}
	raw, _ := json.Marshal(ids)
	return raw
}

func v6EvidenceLineage(refs []V6EvidenceRef) json.RawMessage {
	items := make([]map[string]any, len(refs))
	for i, ref := range refs {
		items[i] = map[string]any{"input_version_id": ref.VersionID, "relation": "evidence", "ordinal": i}
	}
	raw, _ := json.Marshal(items)
	return raw
}

func v6Termination(raw json.RawMessage) (string, string) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", ""
	}
	var value struct {
		ReasonCode   string `json:"reason_code"`
		ReasonDetail string `json:"reason_detail"`
	}
	_ = json.Unmarshal(raw, &value)
	return strings.TrimSpace(value.ReasonCode), strings.TrimSpace(value.ReasonDetail)
}
