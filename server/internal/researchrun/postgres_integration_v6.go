package researchrun

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type lockedV6IntegrationInput struct {
	V6NodeRef
	InsightID string
}

func (s *PostgresStore) applyIntegrationV6Tx(ctx context.Context, tx pgx.Tx, submissionID string, decoded DecodedV6Contract) (V6IntegrationOutcome, error) {
	var in V6IntegrationSubmission
	if json.Unmarshal(decoded.Envelope, &in) != nil || in.ContentHash != decoded.ContentHash {
		return V6IntegrationOutcome{}, ErrInvalidContract
	}
	if in.InputSetHash != v6InputSetHash(in.InputNodes) || v6BranchScopeHash(in.BranchRefs) == "" {
		return V6IntegrationOutcome{}, ErrInvalidContract
	}
	if err := validateV6IntegrationTiers(in.Mode, in.InputNodes, in.OutputTier); err != nil {
		return V6IntegrationOutcome{}, err
	}
	var attemptStatus, manifestID, manifestHash, assignedAgentID, membershipID, workStatus string
	var manifest json.RawMessage
	var goalVersion, planVersion int
	err := tx.QueryRow(ctx, `SELECT a.status,a.manifest_id::text,a.manifest_hash,a.manifest,a.assigned_agent_id::text,a.membership_id::text,
		w.status,w.goal_version,s.plan_version FROM research_work_item_attempt a JOIN research_work_item w ON w.id=a.work_item_id
		JOIN research_session s ON s.id=a.session_id WHERE a.workspace_id=$1::uuid AND a.session_id=$2::uuid
		AND a.work_item_id=$3::uuid AND a.id=$4::uuid FOR UPDATE OF a,w,s`, in.WorkspaceID, in.RunID, in.WorkItemID, in.AttemptID).Scan(
		&attemptStatus, &manifestID, &manifestHash, &manifest, &assignedAgentID, &membershipID, &workStatus, &goalVersion, &planVersion)
	if err != nil || attemptStatus != "running" || (workStatus != "running" && workStatus != "awaiting_input") || assignedAgentID != in.AgentID || in.StewardAgentID != in.AgentID ||
		manifestID != in.ManifestID || manifestHash != in.ManifestHash {
		return V6IntegrationOutcome{}, ErrAttemptNotAssigned
	}
	var discussionStatus, discussionInputHash, discussionBranchScopeHash string
	var discussionRevision int
	err = tx.QueryRow(ctx, `SELECT status,input_set_hash,branch_scope_hash,revision FROM research_discussion WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$3::uuid FOR UPDATE`,
		in.WorkspaceID, in.RunID, in.DiscussionID).Scan(&discussionStatus, &discussionInputHash, &discussionBranchScopeHash, &discussionRevision)
	if err != nil || discussionStatus != "consensus_accept" || discussionInputHash != in.InputSetHash || discussionRevision != in.DiscussionRevision {
		return V6IntegrationOutcome{}, ErrWorkItemChanged
	}
	if discussionBranchScopeHash != v6BranchScopeHash(in.BranchRefs) {
		return V6IntegrationOutcome{}, ErrWorkItemChanged
	}
	inputs := append([]V6NodeRef(nil), in.InputNodes...)
	sort.Slice(inputs, func(i, j int) bool { return inputs[i].VersionID < inputs[j].VersionID })
	var frozen struct {
		Artifacts []struct {
			ArtifactVersionID string `json:"artifact_version_id"`
		} `json:"artifacts"`
	}
	if json.Unmarshal(manifest, &frozen) != nil {
		return V6IntegrationOutcome{}, ErrInvalidContract
	}
	authorized := map[string]struct{}{}
	for _, artifact := range frozen.Artifacts {
		authorized[artifact.ArtifactVersionID] = struct{}{}
	}
	for _, input := range inputs {
		if _, ok := authorized[input.VersionID]; !ok {
			return V6IntegrationOutcome{}, ErrAttemptNotAssigned
		}
	}
	locked := make([]lockedV6IntegrationInput, 0, len(inputs))
	for _, input := range inputs {
		var tier, hash, nodeKind, artifactID, insightID string
		var alreadyAbsorbed bool
		err = tx.QueryRow(ctx, `SELECT COALESCE(iv.tier,'S'),v.content_hash,
			CASE WHEN rn.id IS NOT NULL THEN 'result_s' ELSE 'insight' END,v.artifact_id::text,COALESCE(iv.insight_id::text,''),
			EXISTS(SELECT 1 FROM research_node_absorption a WHERE a.session_id=v.session_id AND a.input_artifact_version_id=v.id)
			FROM research_artifact_version v LEFT JOIN research_result_node rn ON rn.artifact_version_id=v.id
			LEFT JOIN research_insight_version iv ON iv.artifact_version_id=v.id
			WHERE v.workspace_id=$1::uuid AND v.session_id=$2::uuid AND v.id=$3::uuid FOR UPDATE OF v`,
			in.WorkspaceID, in.RunID, input.VersionID).Scan(&tier, &hash, &nodeKind, &artifactID, &insightID, &alreadyAbsorbed)
		if err != nil || alreadyAbsorbed || tier != string(input.Tier) || hash != input.ContentHash ||
			nodeKind != input.Kind || artifactID != input.ID {
			if alreadyAbsorbed {
				return V6IntegrationOutcome{}, ErrV6NodeAlreadyAbsorbed
			}
			return V6IntegrationOutcome{}, ErrWorkItemChanged
		}
		var fresh bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM research_branch_frontier WHERE session_id=$1::uuid
			AND node_artifact_version_id=$2::uuid AND removed_by_event_sequence IS NULL)`, in.RunID, input.VersionID).Scan(&fresh); err != nil || !fresh {
			return V6IntegrationOutcome{}, ErrWorkItemChanged
		}
		locked = append(locked, lockedV6IntegrationInput{V6NodeRef: input, InsightID: insightID})
	}
	branches := append([]V6BranchRef(nil), in.BranchRefs...)
	sort.Slice(branches, func(i, j int) bool { return branches[i].ID < branches[j].ID })
	for _, branch := range branches {
		var version int64
		var status, currentXXLArtifactVersionID string
		var branchGoal int
		err = tx.QueryRow(ctx, `SELECT b.state_version,b.status,b.goal_version,COALESCE(iv.artifact_version_id::text,'')
			FROM research_branch b LEFT JOIN research_insight_version iv ON iv.id=b.current_xxl_version_id
			WHERE b.workspace_id=$1::uuid AND b.session_id=$2::uuid AND b.id=$3::uuid FOR UPDATE OF b`, in.WorkspaceID, in.RunID, branch.ID).Scan(&version, &status, &branchGoal, &currentXXLArtifactVersionID)
		if err != nil || version != branch.StateVersion || status != "active" || branchGoal != goalVersion {
			return V6IntegrationOutcome{}, ErrWorkItemChanged
		}
		if in.OutputTier == V6TierXXL && currentXXLArtifactVersionID != "" && !containsV6String(inputIDsFromRefs(inputs), currentXXLArtifactVersionID) {
			return V6IntegrationOutcome{}, fmt.Errorf("%w: Branch XXL replacement must absorb the current XXL", ErrV6InvalidTierTransition)
		}
	}
	branchIDs := make([]string, len(branches))
	for i := range branches {
		branchIDs[i] = branches[i].ID
	}
	for _, input := range locked {
		var bound bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM research_node_branch WHERE session_id=$1::uuid
			AND node_artifact_version_id=$2::uuid AND branch_id=ANY($3::uuid[]))`, in.RunID, input.VersionID, branchIDs).Scan(&bound); err != nil || !bound {
			return V6IntegrationOutcome{}, ErrWorkItemChanged
		}
	}

	roundID := uuid.NewString()
	inputIDs := make([]string, len(locked))
	for i := range locked {
		inputIDs[i] = locked[i].VersionID
	}
	inputJSON, _ := json.Marshal(inputIDs)
	if _, err = tx.Exec(ctx, `INSERT INTO research_integration_round(id,workspace_id,session_id,trigger_kind,input_event_sequence,input_state_hash,input_artifact_ids,goal_version,plan_version,status,
		work_item_attempt_id,goal_version_v6,branch_scope_hash,input_set_hash,mode,status_v6,discussion_id_v6)
		VALUES($1::uuid,$2::uuid,$3::uuid,'result_batch',COALESCE((SELECT max(sequence) FROM research_run_event WHERE session_id=$3::uuid),0),$4,$5::jsonb,$6,$7,'accepted',$8::uuid,$6,$9,$4,$10,'accepted',$11::uuid)`,
		roundID, in.WorkspaceID, in.RunID, in.InputSetHash, inputJSON, goalVersion, planVersion, in.AttemptID, v6BranchScopeHash(branches), in.Mode, in.DiscussionID); err != nil {
		return V6IntegrationOutcome{}, err
	}

	outputCanonical, err := marshalV6CanonicalJSON(in.OutputContent)
	if err != nil {
		return V6IntegrationOutcome{}, err
	}
	outputHash := ArtifactContentHashFromCanonicalJSON(outputCanonical)
	insightID := uuid.NewString()
	revision := 1
	if in.Mode == "assimilation" {
		for _, input := range locked {
			if input.Tier == in.OutputTier {
				insightID = input.InsightID
				break
			}
		}
		if insightID == "" {
			return V6IntegrationOutcome{}, ErrV6InvalidTierTransition
		}
		if err = tx.QueryRow(ctx, `SELECT COALESCE(max(revision),0)+1 FROM research_insight_version WHERE insight_id=$1::uuid`, insightID).Scan(&revision); err != nil {
			return V6IntegrationOutcome{}, err
		}
	} else {
		if _, err = tx.Exec(ctx, `INSERT INTO research_insight(id,workspace_id,session_id,client_key,title,summary,status,importance,level)
		VALUES($1::uuid,$2::uuid,$3::uuid,$4,$5,$6,'accepted',0.5,$7)`, insightID, in.WorkspaceID, in.RunID, "v6:"+insightID, in.OutputContent.Objective, in.OutputContent.BriefSummary, v6TierLevel(in.OutputTier)); err != nil {
			return V6IntegrationOutcome{}, err
		}
	}
	now := time.Now().UTC()
	goal32, plan32 := int32(goalVersion), int32(planVersion)
	artifactVersionID := ""
	if revision == 1 {
		if err = registerArtifactPassportTx(ctx, tx, registerArtifactPassportInput{WorkspaceID: in.WorkspaceID, SessionID: in.RunID, EntityID: insightID, Kind: ArtifactKindInsight, SourceCreatedAt: &now,
			ProvenanceCompleteness: ArtifactProvenanceComplete, GoalVersion: &goal32, PlanVersion: &plan32, SchemaVersion: "research-run-v6", AccessLevel: ArtifactAccessRaw, HashOrigin: ArtifactHashOriginProduction,
			ContentHash: outputHash, ProducedByWorkItemID: in.WorkItemID, ProducedByWorkAttemptID: in.AttemptID, ProducedByAgentID: in.AgentID}); err != nil {
			return V6IntegrationOutcome{}, err
		}
		if err = tx.QueryRow(ctx, `SELECT id::text FROM research_artifact_version WHERE artifact_id=$1::uuid AND version=1`, insightID).Scan(&artifactVersionID); err != nil {
			return V6IntegrationOutcome{}, err
		}
	} else {
		artifactVersionID = uuid.NewString()
		if _, err = tx.Exec(ctx, `INSERT INTO research_artifact_version(id,workspace_id,session_id,artifact_id,version,schema_name,schema_version,canonicalization_version,content_hash,access_level,goal_version,plan_version,hash_origin,produced_by_work_item_id,produced_by_work_item_attempt_id,produced_by_agent_id)
		VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5,'insight','research-run-v6',$6,$7,'raw',$8,$9,'production',$10::uuid,$11::uuid,$12::uuid)`, artifactVersionID, in.WorkspaceID, in.RunID, insightID, revision, ArtifactCanonicalizationVersion, outputHash, goalVersion, planVersion, in.WorkItemID, in.AttemptID, in.AgentID); err != nil {
			return V6IntegrationOutcome{}, err
		}
	}
	insightVersionID := uuid.NewString()
	if _, err = tx.Exec(ctx, `INSERT INTO research_insight_version(id,workspace_id,session_id,insight_id,revision,artifact_version_id,tier,catalog_summary,brief_summary,objective,conclusion,content,scope,uncertainties,conflicts,open_questions,status,integration_round_id,discussion_id,content_hash)
		VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5,$6::uuid,$7,$8,$9,$10,$11,$12,$13::jsonb,$14::jsonb,$15::jsonb,$16::jsonb,'accepted',$17::uuid,$18::uuid,$19)`, insightVersionID, in.WorkspaceID, in.RunID, insightID, revision, artifactVersionID, in.OutputTier, in.OutputContent.CatalogSummary, in.OutputContent.BriefSummary, in.OutputContent.Objective, in.OutputContent.Conclusion, in.OutputContent.Content, in.OutputContent.Scope, in.OutputContent.Uncertainties, in.OutputContent.Conflicts, in.OutputContent.OpenQuestions, roundID, in.DiscussionID, outputHash); err != nil {
		return V6IntegrationOutcome{}, err
	}
	if revision > 1 {
		if _, err = tx.Exec(ctx, `UPDATE research_insight_version SET status='superseded',superseded_by_version_id=$2::uuid WHERE insight_id=$1::uuid AND revision=$3-1`, insightID, insightVersionID, revision); err != nil {
			return V6IntegrationOutcome{}, err
		}
		if _, err = tx.Exec(ctx, `UPDATE research_artifact_passport SET current_version=$2,lifecycle_status='accepted' WHERE id=$1::uuid`, insightID, revision); err != nil {
			return V6IntegrationOutcome{}, err
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE research_insight SET current_version_id=$2::uuid,title=$3,summary=$4,status='accepted',level=$5,updated_at=now() WHERE id=$1::uuid`, insightID, insightVersionID, in.OutputContent.Objective, in.OutputContent.BriefSummary, v6TierLevel(in.OutputTier)); err != nil {
		return V6IntegrationOutcome{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE research_integration_round SET output_insight_version_id=$2::uuid,updated_at=now() WHERE id=$1::uuid`, roundID, insightVersionID); err != nil {
		return V6IntegrationOutcome{}, err
	}
	for _, input := range locked {
		if _, err = tx.Exec(ctx, `INSERT INTO research_insight_derivation(workspace_id,session_id,insight_id,input_kind,input_entity_id,input_content_hash,scope_hash,relation,insight_version_id,input_artifact_version_id,integration_round_id,input_tier)
		VALUES($1::uuid,$2::uuid,$3::uuid,NULL,NULL,NULL,$4,'integrates',$5::uuid,$6::uuid,$7::uuid,$8)`, in.WorkspaceID, in.RunID, insightID, v6BranchScopeHash(branches), insightVersionID, input.VersionID, roundID, input.Tier); err != nil {
			return V6IntegrationOutcome{}, err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO research_node_absorption(workspace_id,session_id,input_artifact_version_id,successor_insight_version_id,integration_round_id,discussion_id,relation)
			VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,$6::uuid,$7)`, in.WorkspaceID, in.RunID, input.VersionID, insightVersionID, roundID, in.DiscussionID, in.Mode); err != nil {
			return V6IntegrationOutcome{}, err
		}
	}
	event, err := appendEvent(ctx, tx, in.WorkspaceID, in.RunID, "v6_integration_commit", "v6-integration:"+in.ClientRequestID, "agent", in.AgentID, map[string]any{"integration_round_id": roundID, "insight_version_id": insightVersionID, "artifact_version_id": artifactVersionID, "mode": in.Mode, "tier": in.OutputTier, "inputs": inputIDs})
	if err != nil {
		return V6IntegrationOutcome{}, err
	}
	for _, input := range locked {
		if _, err = tx.Exec(ctx, `UPDATE research_branch_frontier SET removed_by_event_sequence=$3,removal_reason='absorbed' WHERE session_id=$1::uuid AND node_artifact_version_id=$2::uuid AND removed_by_event_sequence IS NULL`, in.RunID, input.VersionID, event.Sequence); err != nil {
			return V6IntegrationOutcome{}, err
		}
		if _, err = tx.Exec(ctx, `UPDATE research_node_steward_assignment SET status='released',released_at=now(),reason='absorbed' WHERE session_id=$1::uuid AND node_artifact_version_id=$2::uuid AND status='active'`, in.RunID, input.VersionID); err != nil {
			return V6IntegrationOutcome{}, err
		}
		if _, err = tx.Exec(ctx, `UPDATE research_result_node SET integration_state='absorbed' WHERE session_id=$1::uuid AND artifact_version_id=$2::uuid`, in.RunID, input.VersionID); err != nil {
			return V6IntegrationOutcome{}, err
		}
	}
	for _, branch := range branches {
		if _, err = tx.Exec(ctx, `INSERT INTO research_node_branch(workspace_id,session_id,node_artifact_version_id,branch_id,bound_by_decision_id)VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid)`, in.WorkspaceID, in.RunID, artifactVersionID, branch.ID, roundID); err != nil {
			return V6IntegrationOutcome{}, err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO research_branch_frontier(workspace_id,session_id,branch_id,node_artifact_version_id,tier,added_by_event_sequence)VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5,$6)`, in.WorkspaceID, in.RunID, branch.ID, artifactVersionID, in.OutputTier, event.Sequence); err != nil {
			return V6IntegrationOutcome{}, err
		}
		if in.OutputTier == V6TierXXL {
			if _, err = tx.Exec(ctx, `UPDATE research_branch SET current_xxl_version_id=$3::uuid,state_version=state_version+1,updated_at=now() WHERE workspace_id=$1::uuid AND id=$2::uuid`, in.WorkspaceID, branch.ID, insightVersionID); err != nil {
				return V6IntegrationOutcome{}, err
			}
		}
	}
	var stewardMembership string
	if err = tx.QueryRow(ctx, `SELECT id::text FROM research_team_membership WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND agent_id=$3::uuid AND state IN('idle','working','offline','retiring') ORDER BY membership_generation DESC LIMIT 1`, in.WorkspaceID, in.RunID, in.StewardAgentID).Scan(&stewardMembership); err != nil {
		return V6IntegrationOutcome{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO research_node_steward_assignment(workspace_id,session_id,node_artifact_version_id,agent_id,membership_id,generation,status,assigned_by_decision_id,reason)
		VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,1,'active',$6::uuid,'integration_successor')`, in.WorkspaceID, in.RunID, artifactVersionID, in.StewardAgentID, stewardMembership, roundID); err != nil {
		return V6IntegrationOutcome{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE research_work_item_attempt SET status='succeeded',result_kind='insight',result_entity_id=$2::uuid,result_artifact_id=NULL,result_hash=$3,client_request_id=$4::uuid,result_submitted_at=now(),completed_at=now(),updated_at=now() WHERE id=$1::uuid`, in.AttemptID, insightVersionID, decoded.ContentHash, in.ClientRequestID); err != nil {
		return V6IntegrationOutcome{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE research_work_item SET status='succeeded',state_version=state_version+1,completed_at=now(),updated_at=now() WHERE id=$1::uuid`, in.WorkItemID); err != nil {
		return V6IntegrationOutcome{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE research_team_membership SET state='idle' WHERE id=$1::uuid AND state='working'`, membershipID); err != nil {
		return V6IntegrationOutcome{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE research_v6_work_submission SET status='accepted',outcome=jsonb_build_object('integration_round_id',$2::text,'insight_version_id',$3::text,'artifact_version_id',$4::text),updated_at=now() WHERE id=$1::uuid`, submissionID, roundID, insightVersionID, artifactVersionID); err != nil {
		return V6IntegrationOutcome{}, err
	}
	return V6IntegrationOutcome{IntegrationRoundID: roundID, InsightID: insightID, InsightVersionID: insightVersionID, ArtifactVersionID: artifactVersionID, Tier: in.OutputTier}, nil
}

func v6TierLevel(tier V6Tier) int {
	switch tier {
	case V6TierM:
		return 2
	case V6TierL:
		return 3
	case V6TierXL:
		return 4
	case V6TierXXL:
		return 5
	}
	return 1
}
func v6BranchScopeHash(refs []V6BranchRef) string {
	raw, _ := marshalV6CanonicalJSON(refs)
	return ArtifactContentHashFromCanonicalJSON(raw)
}

func inputIDsFromRefs(refs []V6NodeRef) []string {
	ids := make([]string, len(refs))
	for i := range refs {
		ids[i] = refs[i].VersionID
	}
	return ids
}

func containsV6String(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
