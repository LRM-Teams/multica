package researchrun

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const deliberationLimitsPolicyV1 = "research-deliberation-limits-v1"

func materializeAcceptedV6DeliberationTx(ctx context.Context, tx pgx.Tx, state acceptedResultState, result V6DeliberationResult, agentID string) error {
	if state.stale {
		return fmt.Errorf("%w: stale Deliberation Result cannot mutate canonical state", ErrInvalidTransition)
	}
	if result.Adjudication != nil {
		return materializeAcceptedV6DirectorAdjudicationTx(ctx, tx, state, result, agentID)
	}
	var disputeID, disputeStatus, subjectKind, subjectID string
	if err := tx.QueryRow(ctx, `SELECT dispute.id::text,dispute.status,dispute.subject_kind,dispute.subject_entity_id::text
		FROM research_dispute dispute
		JOIN research_task_inquiry_target target ON (target.workspace_id,target.session_id,target.target_entity_id)=(dispute.workspace_id,dispute.session_id,dispute.id)
		WHERE dispute.workspace_id=$1::uuid AND dispute.session_id=$2::uuid AND dispute.client_key=$3
		AND target.task_id=$4::uuid AND target.target_kind='dispute' FOR UPDATE OF dispute`, state.workspaceID, state.run.SessionID,
		result.Dispute.ClientKey, state.task.ID).Scan(&disputeID, &disputeStatus, &subjectKind, &subjectID); err != nil {
		if err == pgx.ErrNoRows {
			return fmt.Errorf("%w: Deliberation Task is not bound to the submitted Dispute", ErrInvalidContract)
		}
		return err
	}
	resolvedSubject, err := resolveV6EntityKeyTx(ctx, tx, state, result.Dispute.Subject)
	if err != nil {
		return err
	}
	if subjectKind != result.Dispute.Subject.Kind || subjectID != resolvedSubject || disputeStatus != result.Envelope.StatusUpdates[0].Before {
		return fmt.Errorf("%w: Deliberation Dispute subject or status changed", ErrControlTargetChanged)
	}
	participants, err := loadDeliberationParticipantsTx(ctx, tx, state, disputeID)
	if err != nil {
		return err
	}
	var directorID string
	var directorIdentityVersion int
	if err = tx.QueryRow(ctx, `SELECT agent_id::text,identity_version FROM research_director_identity
		WHERE workspace_id=$1::uuid AND session_id=$2::uuid ORDER BY identity_version DESC LIMIT 1 FOR UPDATE`, state.workspaceID, state.run.SessionID).Scan(&directorID, &directorIdentityVersion); err != nil {
		return err
	}
	deliberationID := uuid.NewSHA1(uuid.MustParse(disputeID), []byte(deliberationLimitsPolicyV1)).String()
	stateValue := DeliberationState{DisputeID: disputeID, DirectorAgentID: directorID, ParticipantAgentIDs: participants, Status: DeliberationActive}
	var existing bool
	err = tx.QueryRow(ctx, `SELECT true,round_count,no_progress_rounds,elapsed_seconds,tokens_used,tool_calls_used,status,canonical_watermark
		FROM research_deliberation WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$3::uuid FOR UPDATE`, state.workspaceID, state.run.SessionID, deliberationID).Scan(
		&existing, &stateValue.Round, &stateValue.NoProgressRounds, &stateValue.ElapsedSeconds, &stateValue.TokensUsed, &stateValue.ToolCallsUsed, &stateValue.Status, &stateValue.Watermark)
	if err == pgx.ErrNoRows {
		existing, err = false, nil
	} else if err != nil {
		return err
	}
	if !existing {
		watermark, _ := json.Marshal(stateValue.Watermark)
		if _, err = tx.Exec(ctx, `INSERT INTO research_deliberation
			(id,workspace_id,session_id,dispute_id,status,round_count,no_progress_rounds,director_agent_id,canonical_watermark,policy_version)
			VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,'active',0,0,$5::uuid,$6::jsonb,$7)`, deliberationID, state.workspaceID,
			state.run.SessionID, disputeID, directorID, watermark, deliberationLimitsPolicyV1); err != nil {
			return err
		}
		if err = registerAcceptedV6IntegrationArtifactTx(ctx, tx, state, deliberationID, ArtifactKindDeliberation,
			map[string]any{"dispute_id": disputeID, "director_agent_id": directorID, "participants": participants, "status": "active", "policy_version": deliberationLimitsPolicyV1}); err != nil {
			return err
		}
		if err = persistTypedArtifactInputReferenceTx(ctx, tx, state.workspaceID, state.run.SessionID, deliberationID, ArtifactKindDeliberation,
			disputeID, ArtifactKindDispute, "deliberates", "v6_deliberation", 0); err != nil {
			return err
		}
	}
	limits := DeliberationLimits{MaximumRounds: 8, MaximumNoProgressRounds: 2, MaximumElapsedSeconds: int64(state.run.Config.TaskTimeoutSeconds) * 2,
		MaximumTokens: 200_000, MaximumToolCalls: 64}
	var escalation *LeadAdjudicationTask
	for _, turn := range result.Turns {
		claimIDs, evidenceIDs := make([]string, 0, len(turn.ClaimRefs)), make([]string, 0, len(turn.EvidenceRefs)+len(turn.CanonicalDelta.EvidenceRefs))
		for _, ref := range turn.ClaimRefs {
			id, resolveErr := resolveV6EntityKeyTx(ctx, tx, state, ref)
			if resolveErr != nil {
				return resolveErr
			}
			claimIDs = append(claimIDs, id)
		}
		for _, ref := range append(append([]V6EntityRef{}, turn.EvidenceRefs...), turn.CanonicalDelta.EvidenceRefs...) {
			id, resolveErr := resolveV6EntityKeyTx(ctx, tx, state, ref)
			if resolveErr != nil {
				return resolveErr
			}
			evidenceIDs = append(evidenceIDs, id)
		}
		sort.Strings(evidenceIDs)
		evidenceIDs = uniqueSortedDeliberationValues(evidenceIDs)
		transition, advanceErr := AdvanceDeliberation(stateValue, DeliberationTurnInput{ActorAgentID: turn.ActorAgentID,
			ClaimedProgress: strings.TrimSpace(turn.Statement), NextWatermark: DeliberationWatermark{PositionHashes: turn.CanonicalDelta.PositionHashes,
				EvidenceIDs: evidenceIDs, ScopeHashes: turn.CanonicalDelta.ScopeHashes}, ResolutionProposalByAgent: turn.ResolutionProposalByAgent,
			NeedsExternalEvidence: turn.NeedsExternalEvidence, UnavailableParticipantIDs: turn.UnavailableParticipantIDs,
			ElapsedSeconds: turn.ElapsedSeconds, TokenCost: turn.TokenCost, ToolCallCost: turn.ToolCallCost}, limits)
		if advanceErr != nil {
			return advanceErr
		}
		stateValue = transition.State
		if transition.LeadAdjudicationTask != nil {
			escalation = transition.LeadAdjudicationTask
		}
		turnID := uuid.NewSHA1(uuid.MustParse(deliberationID), []byte(fmt.Sprintf("turn/%d/%s", stateValue.Round, turn.ActorAgentID))).String()
		var positionID *string
		_ = tx.QueryRow(ctx, `SELECT id::text FROM research_dispute_position WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND dispute_id=$3::uuid
			AND author_agent_id=$4::uuid ORDER BY created_at DESC LIMIT 1`, state.workspaceID, state.run.SessionID, disputeID, turn.ActorAgentID).Scan(&positionID)
		encodedEvidence, _ := json.Marshal(evidenceIDs)
		proposedAction, _ := json.Marshal(turn.ProposedAction)
		canonicalDelta, _ := json.Marshal(turn.CanonicalDelta)
		if _, err = tx.Exec(ctx, `INSERT INTO research_deliberation_turn
			(id,workspace_id,session_id,deliberation_id,round_number,actor_agent_id,position_id,evidence_ids,challenge,concession,proposed_action,canonical_delta)
			VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5,$6::uuid,$7::uuid,$8::jsonb,$9,$10,$11::jsonb,$12::jsonb)`, turnID,
			state.workspaceID, state.run.SessionID, deliberationID, stateValue.Round, turn.ActorAgentID, positionID, encodedEvidence,
			turn.Challenge, turn.Concession, proposedAction, canonicalDelta); err != nil {
			return err
		}
		if err = registerAcceptedV6IntegrationArtifactTx(ctx, tx, state, turnID, ArtifactKindDeliberationTurn,
			map[string]any{"deliberation_id": deliberationID, "round_number": stateValue.Round, "actor_agent_id": turn.ActorAgentID,
				"claim_ids": claimIDs, "evidence_ids": evidenceIDs, "challenge": turn.Challenge, "concession": turn.Concession,
				"proposed_action": turn.ProposedAction, "canonical_delta": turn.CanonicalDelta}); err != nil {
			return err
		}
	}
	watermark, _ := json.Marshal(stateValue.Watermark)
	if _, err = tx.Exec(ctx, `UPDATE research_deliberation SET status=$1,round_count=$2,no_progress_rounds=$3,elapsed_seconds=$4,tokens_used=$5,
		tool_calls_used=$6,canonical_watermark=$7::jsonb,updated_at=now() WHERE id=$8::uuid`, stateValue.Status, stateValue.Round,
		stateValue.NoProgressRounds, stateValue.ElapsedSeconds, stateValue.TokensUsed, stateValue.ToolCallsUsed, watermark, deliberationID); err != nil {
		return err
	}
	after := result.Envelope.StatusUpdates[0].After
	if !validDeliberationDisputeOutcome(stateValue.Status, after) {
		return fmt.Errorf("%w: Dispute outcome does not match canonical Deliberation state", ErrInvalidTransition)
	}
	if _, err = tx.Exec(ctx, `UPDATE research_dispute SET status=$1,resolution_explanation=$2,updated_at=now()
		WHERE workspace_id=$3::uuid AND session_id=$4::uuid AND id=$5::uuid AND status=$6`, after,
		strings.TrimSpace(result.Envelope.StatusUpdates[0].Reason), state.workspaceID, state.run.SessionID, disputeID, disputeStatus); err != nil {
		return err
	}
	adjudicationTaskID := ""
	if escalation != nil {
		adjudicationTaskID, err = createDirectorAdjudicationTaskTx(ctx, tx, state, disputeID, deliberationID, directorID, directorIdentityVersion, escalation.Reason)
		if err != nil {
			return err
		}
	}
	_, err = appendEvent(ctx, tx, state.workspaceID, state.run.SessionID, "v6_deliberation_materialized", "v6-deliberation:"+state.attemptID, "agent", agentID,
		map[string]any{"attempt_id": state.attemptID, "deliberation_id": deliberationID, "dispute_id": disputeID, "round_count": stateValue.Round,
			"status": stateValue.Status, "dispute_status": after, "director_agent_id": directorID, "director_identity_version": directorIdentityVersion,
			"escalation": escalation, "adjudication_task_id": adjudicationTaskID})
	return err
}

func createDirectorAdjudicationTaskTx(ctx context.Context, tx pgx.Tx, state acceptedResultState, disputeID, deliberationID, directorID string, identityVersion int, reason string) (string, error) {
	var role string
	if err := tx.QueryRow(ctx, `SELECT role FROM research_fleet_member WHERE workspace_id=$1::uuid AND fleet_id=$2::uuid AND agent_id=$3::uuid
		AND status='active' AND is_lead=true`, state.workspaceID, state.run.FleetID, directorID).Scan(&role); err != nil {
		if err == pgx.ErrNoRows {
			return "", fmt.Errorf("%w: pinned Research Director is not the active Fleet lead", ErrInvalidTransition)
		}
		return "", err
	}
	var directorIdentityID string
	if err := tx.QueryRow(ctx, `SELECT id::text FROM research_director_identity WHERE workspace_id=$1::uuid AND session_id=$2::uuid
		AND identity_version=$3 AND agent_id=$4::uuid`, state.workspaceID, state.run.SessionID, identityVersion, directorID).Scan(&directorIdentityID); err != nil {
		return "", err
	}
	taskID := uuid.NewSHA1(uuid.MustParse(disputeID), []byte(fmt.Sprintf("director-adjudication/%d", identityVersion))).String()
	clientKey := "lead-adjudication:" + disputeID
	criteria, _ := json.Marshal(map[string]any{"mode": "director_adjudication", "dispute_id": disputeID, "deliberation_id": deliberationID,
		"director_agent_id": directorID, "director_identity_version": identityVersion, "deadlock_reason": reason,
		"required_result": "evidence_bound_adjudication"})
	if _, err := tx.Exec(ctx, `INSERT INTO research_task
		(id,workspace_id,session_id,parent_task_id,client_key,kind,objective,required_capability,expected_result,acceptance_criteria,priority,status,
		 assigned_agent_id,goal_version,plan_version,max_attempts,timeout_seconds,ready_at)
		VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5,'deliberate',$6,$7,'research_deliberation_v6',$8::jsonb,1,'ready',$9::uuid,$10,$11,1,$12,now())
		ON CONFLICT (session_id,goal_version,plan_version,client_key) DO NOTHING`, taskID, state.workspaceID, state.run.SessionID, state.task.ID,
		clientKey, "Adjudicate the deadlocked Dispute from canonical positions and evidence without overriding any position by authority alone. Deadlock reason: "+reason,
		role, criteria, directorID, state.run.GoalVersion, state.targetPlan, state.run.Config.TaskTimeoutSeconds); err != nil {
		return "", err
	}
	if err := registerProductionTaskPassportTx(ctx, tx, state.workspaceID, state.run.SessionID, taskID, state.attemptID, state.outputAccess); err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO research_task_inquiry_target
		(workspace_id,session_id,task_id,target_kind,target_entity_id,goal_version,plan_version,bound_by_attempt_id)
		VALUES($1::uuid,$2::uuid,$3::uuid,'dispute',$4::uuid,$5,$6,$7::uuid) ON CONFLICT DO NOTHING`, state.workspaceID,
		state.run.SessionID, taskID, disputeID, state.run.GoalVersion, state.targetPlan, state.attemptID); err != nil {
		return "", err
	}
	if err := persistTypedArtifactInputReferenceTx(ctx, tx, state.workspaceID, state.run.SessionID, taskID, ArtifactKindTask,
		disputeID, ArtifactKindDispute, "adjudicates", "director_adjudication", 0); err != nil {
		return "", err
	}
	if err := persistTypedArtifactInputReferenceTx(ctx, tx, state.workspaceID, state.run.SessionID, taskID, ArtifactKindTask,
		deliberationID, ArtifactKindDeliberation, "reviews_deadlock", "director_adjudication", 1); err != nil {
		return "", err
	}
	if err := persistTypedArtifactInputReferenceTx(ctx, tx, state.workspaceID, state.run.SessionID, taskID, ArtifactKindTask,
		directorIdentityID, ArtifactKindResearchDirectorIdentity, "authorized_director", "director_adjudication", 2); err != nil {
		return "", err
	}
	positionRows, err := tx.Query(ctx, `SELECT id::text FROM research_dispute_position WHERE workspace_id=$1::uuid AND session_id=$2::uuid
		AND dispute_id=$3::uuid ORDER BY id::text`, state.workspaceID, state.run.SessionID, disputeID)
	if err != nil {
		return "", err
	}
	ordinal := 3
	for positionRows.Next() {
		var positionID string
		if err = positionRows.Scan(&positionID); err != nil {
			positionRows.Close()
			return "", err
		}
		if err = persistTypedArtifactInputReferenceTx(ctx, tx, state.workspaceID, state.run.SessionID, taskID, ArtifactKindTask,
			positionID, ArtifactKindDisputePosition, "adjudicates_position", "director_adjudication", ordinal); err != nil {
			positionRows.Close()
			return "", err
		}
		ordinal++
	}
	positionRows.Close()
	if err = positionRows.Err(); err != nil {
		return "", err
	}
	if err := persistMaterializedTaskRelationshipReferencesTx(ctx, tx, state.workspaceID, state.run.SessionID, taskID); err != nil {
		return "", err
	}
	return taskID, nil
}

func loadDeliberationParticipantsTx(ctx context.Context, tx pgx.Tx, state acceptedResultState, disputeID string) ([]string, error) {
	rows, err := tx.Query(ctx, `SELECT DISTINCT author_agent_id::text FROM research_dispute_position
		WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND dispute_id=$3::uuid ORDER BY author_agent_id::text`, state.workspaceID, state.run.SessionID, disputeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var participants []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		participants = append(participants, id)
	}
	if len(participants) < 2 {
		return nil, fmt.Errorf("%w: Deliberation requires positions from at least two Agents", ErrInvalidContract)
	}
	return participants, rows.Err()
}

func validDeliberationDisputeOutcome(status DeliberationStatus, after string) bool {
	switch status {
	case DeliberationActive, DeliberationAwaitingEvidence:
		return after == "investigating"
	case DeliberationConsensus:
		return after == "resolved" || after == "conditionally_resolved"
	case DeliberationDeadlocked:
		return after == "irreducible"
	default:
		return false
	}
}
