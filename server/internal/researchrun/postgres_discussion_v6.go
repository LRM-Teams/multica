package researchrun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (s *PostgresStore) OpenV6Discussion(ctx context.Context, in OpenV6DiscussionInput) (V6Discussion, error) {
	tx, err := s.beginResearchTx(ctx, txOpV6DiscussionOpen, pgx.TxOptions{})
	if err != nil {
		return V6Discussion{}, err
	}
	defer tx.Rollback(ctx)
	if err = lockRunForMutation(ctx, tx, in.RunID, in.WorkspaceID); err != nil {
		return V6Discussion{}, err
	}
	var existing V6Discussion
	err = tx.QueryRow(ctx, `SELECT id::text,kind,scope_hash,input_set_hash,branch_scope_hash,status,goal_version,revision,through_event_sequence
		FROM research_discussion WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND scope_hash=$3 AND input_set_hash=$4
		AND goal_version=$5 AND branch_scope_hash=$6 AND status='active'`, in.WorkspaceID, in.RunID, in.ScopeHash, in.InputSetHash,
		in.GoalVersion, in.BranchScopeHash).Scan(&existing.ID, &existing.Kind, &existing.ScopeHash, &existing.InputSetHash,
		&existing.BranchScopeHash, &existing.Status, &existing.GoalVersion, &existing.Revision, &existing.ThroughEventSequence)
	if err == nil {
		if existing.Kind != in.Kind {
			return V6Discussion{}, ErrV6IdempotencyConflict
		}
		return existing, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return V6Discussion{}, err
	}
	discussion := V6Discussion{ID: uuid.NewString(), Kind: in.Kind, ScopeHash: in.ScopeHash, InputSetHash: in.InputSetHash,
		BranchScopeHash: in.BranchScopeHash, Status: "active", GoalVersion: in.GoalVersion, Revision: 1, ThroughEventSequence: in.ThroughEventSequence}
	if _, err = tx.Exec(ctx, `INSERT INTO research_discussion(id,workspace_id,session_id,kind,scope_hash,input_set_hash,goal_version,
		branch_scope_hash,through_event_sequence,revision,status,director_assignment_id)
		VALUES($1::uuid,$2::uuid,$3::uuid,$4,$5,$6,$7,$8,$9,1,'active',NULLIF($10,'')::uuid)`, discussion.ID, in.WorkspaceID,
		in.RunID, in.Kind, in.ScopeHash, in.InputSetHash, in.GoalVersion, in.BranchScopeHash, in.ThroughEventSequence, in.DirectorAssignmentID); err != nil {
		return V6Discussion{}, err
	}
	inputs := append([]V6NodeRef(nil), in.Inputs...)
	sort.Slice(inputs, func(i, j int) bool { return inputs[i].VersionID < inputs[j].VersionID })
	branches := append([]V6BranchRef(nil), in.BranchRefs...)
	sort.Slice(branches, func(i, j int) bool { return branches[i].ID < branches[j].ID })
	branchIDs := make([]string, len(branches))
	for i, branch := range branches {
		branchIDs[i] = branch.ID
		var version int64
		var goalVersion int
		var status string
		if err = tx.QueryRow(ctx, `SELECT state_version,goal_version,status FROM research_branch
			WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$3::uuid FOR UPDATE`, in.WorkspaceID, in.RunID, branch.ID).Scan(&version, &goalVersion, &status); err != nil ||
			version != branch.StateVersion || goalVersion != in.GoalVersion || status != "active" {
			return V6Discussion{}, ErrWorkItemChanged
		}
	}
	participants := map[string]struct{}{}
	for ordinal, input := range inputs {
		var tier, hash, stewardID, agentID, membershipID string
		err = tx.QueryRow(ctx, `SELECT f.tier,v.content_hash,st.id::text,st.agent_id::text,st.membership_id::text
			FROM research_branch_frontier f JOIN research_artifact_version v ON v.id=f.node_artifact_version_id
			JOIN research_node_steward_assignment st ON st.node_artifact_version_id=f.node_artifact_version_id AND st.status='active'
			WHERE f.workspace_id=$1::uuid AND f.session_id=$2::uuid AND f.node_artifact_version_id=$3::uuid
			AND f.removed_by_event_sequence IS NULL LIMIT 1 FOR UPDATE OF f,st`, in.WorkspaceID, in.RunID, input.VersionID).Scan(&tier, &hash, &stewardID, &agentID, &membershipID)
		if err != nil || tier != string(input.Tier) || hash != input.ContentHash {
			return V6Discussion{}, ErrWorkItemChanged
		}
		var inScope bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM research_node_branch WHERE session_id=$1::uuid
			AND node_artifact_version_id=$2::uuid AND branch_id=ANY($3::uuid[]))`, in.RunID, input.VersionID, branchIDs).Scan(&inScope); err != nil || !inScope {
			return V6Discussion{}, ErrWorkItemChanged
		}
		if _, err = tx.Exec(ctx, `INSERT INTO research_discussion_input(workspace_id,session_id,discussion_id,node_artifact_version_id,ordinal,tier,content_hash)
			VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5,$6,$7)`, in.WorkspaceID, in.RunID, discussion.ID, input.VersionID, ordinal, tier, hash); err != nil {
			return V6Discussion{}, err
		}
		if _, seen := participants[agentID]; !seen {
			if _, err = tx.Exec(ctx, `INSERT INTO research_discussion_participant(workspace_id,session_id,discussion_id,agent_id,membership_id,steward_assignment_id,joined_ordinal,state)
				VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,$6::uuid,$7,'active')`, in.WorkspaceID, in.RunID, discussion.ID, agentID, membershipID, stewardID, len(participants)); err != nil {
				return V6Discussion{}, err
			}
			participants[agentID] = struct{}{}
		}
	}
	if _, err = appendEvent(ctx, tx, in.WorkspaceID, in.RunID, "v6_discussion_open", "v6-discussion:"+in.InputSetHash, "director", "",
		map[string]any{"discussion_id": discussion.ID, "kind": in.Kind, "input_set_hash": in.InputSetHash, "participant_count": len(participants)}); err != nil {
		return V6Discussion{}, err
	}
	if err = s.commitResearchTx(ctx, txOpV6DiscussionOpen, tx); err != nil {
		return V6Discussion{}, err
	}
	return discussion, nil
}

func (s *PostgresStore) applyDiscussionTurnV6Tx(ctx context.Context, tx pgx.Tx, submissionID string, decoded DecodedV6Contract) error {
	var in V6DiscussionTurnSubmission
	if json.Unmarshal(decoded.Envelope, &in) != nil {
		return ErrInvalidContract
	}
	var contribution struct {
		Vote   string `json:"vote"`
		Reason string `json:"reason"`
	}
	if json.Unmarshal(in.Contribution, &contribution) != nil {
		return ErrInvalidContract
	}
	var revision int
	var status, inputHash string
	err := tx.QueryRow(ctx, `SELECT revision,status,input_set_hash FROM research_discussion
		WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$3::uuid FOR UPDATE`, in.WorkspaceID, in.RunID, in.DiscussionID).Scan(&revision, &status, &inputHash)
	if err != nil || revision != in.DiscussionRevision || status != "active" || inputHash != in.InputSetHash {
		return ErrWorkItemChanged
	}
	var participant bool
	var manifestID, manifestHash string
	var manifest json.RawMessage
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM research_discussion_participant WHERE discussion_id=$1::uuid AND agent_id=$2::uuid AND state='active'),a.manifest_id::text,a.manifest_hash,a.manifest
		FROM research_work_item_attempt a WHERE a.id=$3::uuid AND a.work_item_id=$4::uuid AND a.assigned_agent_id=$2::uuid AND a.status='running' FOR UPDATE`,
		in.DiscussionID, in.AgentID, in.AttemptID, in.WorkItemID).Scan(&participant, &manifestID, &manifestHash, &manifest); err != nil {
		return err
	}
	if !participant || manifestID != in.ManifestID || manifestHash != in.ManifestHash {
		return ErrAttemptNotAssigned
	}
	var evidenceRefs []V6EvidenceRef
	if json.Unmarshal(in.EvidenceRefs, &evidenceRefs) != nil {
		return ErrInvalidContract
	}
	if err = validateV6EvidenceRefsTx(ctx, tx, in.WorkspaceID, in.RunID, manifest, evidenceRefs); err != nil {
		return err
	}
	var ordinal, round int
	if err = tx.QueryRow(ctx, `SELECT COALESCE(max(ordinal),-1)+1,COALESCE(max(round),0)+1 FROM research_discussion_turn
		WHERE discussion_id=$1::uuid AND discussion_revision=$2`, in.DiscussionID, revision).Scan(&ordinal, &round); err != nil {
		return err
	}
	turnID := uuid.NewString()
	if _, err = tx.Exec(ctx, `INSERT INTO research_discussion_turn(id,workspace_id,session_id,discussion_id,discussion_revision,round,ordinal,agent_id,work_item_attempt_id,manifest_id,manifest_hash,visible_message,contribution,evidence_refs,payload_hash,client_request_id)
		VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5,$6,$7,$8::uuid,$9::uuid,$10::uuid,$11,$12,$13::jsonb,$14::jsonb,$15,$16::uuid)`,
		turnID, in.WorkspaceID, in.RunID, in.DiscussionID, revision, round, ordinal, in.AgentID, in.AttemptID, in.ManifestID, in.ManifestHash,
		in.VisibleMessage, in.Contribution, in.EvidenceRefs, decoded.ContentHash, in.ClientRequestID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO research_discussion_vote(workspace_id,session_id,discussion_id,discussion_revision,agent_id,vote,reason,turn_id)
		VALUES($1::uuid,$2::uuid,$3::uuid,$4,$5::uuid,$6,$7,$8::uuid)`, in.WorkspaceID, in.RunID, in.DiscussionID, revision, in.AgentID,
		contribution.Vote, contribution.Reason, turnID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE research_work_item_attempt SET status='succeeded',result_kind='discussion_turn',result_entity_id=$2::uuid,result_hash=$3,client_request_id=$4::uuid,result_submitted_at=now(),completed_at=now(),updated_at=now() WHERE id=$1::uuid`,
		in.AttemptID, in.DiscussionID, decoded.ContentHash, in.ClientRequestID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE research_work_item SET status='succeeded',state_version=state_version+1,completed_at=now(),updated_at=now() WHERE id=$1::uuid`, in.WorkItemID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE research_v6_work_submission SET status='accepted',outcome=jsonb_build_object('turn_id',$2::text),updated_at=now() WHERE id=$1::uuid`, submissionID, turnID); err != nil {
		return err
	}
	if _, err = appendEvent(ctx, tx, in.WorkspaceID, in.RunID, "v6_discussion_append_turn", "v6-discussion-turn:"+in.ClientRequestID, "agent", in.AgentID,
		map[string]any{"discussion_id": in.DiscussionID, "turn_id": turnID, "vote": contribution.Vote}); err != nil {
		return err
	}
	return s.closeV6DiscussionIfReadyTx(ctx, tx, in.WorkspaceID, in.RunID, in.DiscussionID, revision)
}

func (s *PostgresStore) closeV6DiscussionIfReadyTx(ctx context.Context, tx pgx.Tx, workspaceID, runID, discussionID string, revision int) error {
	var participants, votes, accepts, rejects, uncertain, conflictTurns int
	err := tx.QueryRow(ctx, `WITH latest AS(SELECT DISTINCT ON(agent_id) agent_id,vote FROM research_discussion_vote WHERE discussion_id=$1::uuid AND discussion_revision=$2 ORDER BY agent_id,created_at DESC)
		SELECT (SELECT count(*) FROM research_discussion_participant WHERE discussion_id=$1::uuid AND state='active'),count(*),count(*)FILTER(WHERE vote='accept'),count(*)FILTER(WHERE vote='reject'),count(*)FILTER(WHERE vote='uncertain') FROM latest`,
		discussionID, revision).Scan(&participants, &votes, &accepts, &rejects, &uncertain)
	if err != nil || votes < participants {
		return err
	}
	if err = tx.QueryRow(ctx, `SELECT count(*)::int FROM research_discussion_turn WHERE discussion_id=$1::uuid
		AND discussion_revision=$2 AND jsonb_typeof(contribution->'conflicts')='array'
		AND jsonb_array_length(contribution->'conflicts')>0`, discussionID, revision).Scan(&conflictTurns); err != nil {
		return err
	}
	status := "escalated"
	if accepts == participants && conflictTurns == 0 {
		status = "consensus_accept"
	} else if rejects == participants && conflictTurns == 0 {
		status = "consensus_reject"
	}
	if _, err = tx.Exec(ctx, `UPDATE research_discussion SET status=$2,updated_at=now() WHERE id=$1::uuid AND status='active'`, discussionID, status); err != nil {
		return err
	}
	if status == "consensus_reject" {
		if _, err = tx.Exec(ctx, `INSERT INTO research_match_decision(workspace_id,session_id,candidate_set_hash,input_artifact_version_ids,
			goal_version,branch_scope_hash,decision,reason_code,reason_detail,decided_by)
			SELECT d.workspace_id,d.session_id,d.input_set_hash,array_agg(i.node_artifact_version_id ORDER BY i.ordinal),
			d.goal_version,d.branch_scope_hash,'rejected','no_semantic_gain','All current Stewards rejected integration.','discussion_consensus'
			FROM research_discussion d JOIN research_discussion_input i ON i.discussion_id=d.id WHERE d.id=$1::uuid
			GROUP BY d.workspace_id,d.session_id,d.input_set_hash,d.goal_version,d.branch_scope_hash ON CONFLICT DO NOTHING`, discussionID); err != nil {
			return err
		}
	}
	if status == "consensus_accept" {
		var integratorAgentID string
		if err = tx.QueryRow(ctx, `SELECT agent_id::text FROM research_discussion_participant
			WHERE discussion_id=$1::uuid AND state='active' ORDER BY joined_ordinal DESC LIMIT 1`, discussionID).Scan(&integratorAgentID); err != nil {
			return err
		}
		key := "discussion-integration:" + discussionID + ":" + fmt.Sprint(revision)
		if _, err = tx.Exec(ctx, `INSERT INTO research_work_item(workspace_id,session_id,kind,status,target_kind,target_id,
			client_key,idempotency_key,goal_version,input_state_version,input_event_sequence,assigned_agent_id,payload_schema_id,expected_result_schema_id,payload,ready_at)
			SELECT d.workspace_id,d.session_id,'integration','ready','discussion',d.id,$2,$2,d.goal_version,s.state_version,
			d.through_event_sequence,$3::uuid,'integration.task.v1','integration_submission',jsonb_build_object('discussion_id',d.id,
			'discussion_revision',d.revision,'input_set_hash',d.input_set_hash,'branch_scope_hash',d.branch_scope_hash),now()
			FROM research_discussion d JOIN research_session s ON s.id=d.session_id WHERE d.id=$1::uuid ON CONFLICT DO NOTHING`,
			discussionID, key, integratorAgentID); err != nil {
			return err
		}
	}
	if status == "escalated" {
		var directorAgentID string
		if err = tx.QueryRow(ctx, `SELECT a.director_agent_id::text FROM research_session s
			JOIN research_director_assignment a ON a.id=s.current_director_assignment_id
			WHERE s.id=$1::uuid AND a.status='active'`, runID).Scan(&directorAgentID); err != nil {
			return err
		}
		key := "discussion-escalation:" + discussionID + ":" + fmt.Sprint(revision)
		if _, err = tx.Exec(ctx, `INSERT INTO research_work_item(workspace_id,session_id,kind,status,target_kind,target_id,
			client_key,idempotency_key,goal_version,input_state_version,input_event_sequence,assigned_agent_id,payload_schema_id,expected_result_schema_id,payload,ready_at)
			SELECT $1::uuid,$2::uuid,'director','ready','discussion',$3::uuid,$4,$4,s.goal_version,s.state_version,
			COALESCE((SELECT max(sequence) FROM research_run_event WHERE session_id=s.id),0),$5::uuid,'director.action.registry.v1','director_action_proposal',
			jsonb_build_object('discussion_id',$3,'reason','mixed_or_uncertain_votes'),now()
			FROM research_session s WHERE s.id=$2::uuid ON CONFLICT DO NOTHING`, workspaceID, runID, discussionID, key, directorAgentID); err != nil {
			return err
		}
	}
	if _, err = appendEvent(ctx, tx, workspaceID, runID, "v6_discussion_close", "v6-discussion-close:"+discussionID+":"+status, "system", "",
		map[string]any{"discussion_id": discussionID, "status": status, "accepts": accepts, "rejects": rejects, "uncertain": uncertain, "conflict_turns": conflictTurns}); err != nil {
		return err
	}
	return nil
}
