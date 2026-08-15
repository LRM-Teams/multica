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

type directorAdjudicationCriteria struct {
	Mode                    string `json:"mode"`
	DisputeID               string `json:"dispute_id"`
	DeliberationID          string `json:"deliberation_id"`
	DirectorAgentID         string `json:"director_agent_id"`
	DirectorIdentityVersion int    `json:"director_identity_version"`
	RequiredResult          string `json:"required_result"`
}

func materializeAcceptedV6DirectorAdjudicationTx(ctx context.Context, tx pgx.Tx, state acceptedResultState, result V6DeliberationResult, agentID string) error {
	decision := *result.Adjudication
	var criteria directorAdjudicationCriteria
	if err := json.Unmarshal(state.task.AcceptanceCriteria, &criteria); err != nil || criteria.Mode != "director_adjudication" || criteria.RequiredResult != "evidence_bound_adjudication" {
		return fmt.Errorf("%w: Task is not a Director adjudication", ErrInvalidContract)
	}
	if decision.ActorAgentID != agentID || decision.ActorAgentID != criteria.DirectorAgentID || decision.DirectorIdentityVersion != criteria.DirectorIdentityVersion {
		return fmt.Errorf("%w: adjudication principal does not match the pinned Research Director identity", ErrInvalidTransition)
	}
	var disputeID, disputeStatus, subjectKind, subjectID string
	if err := tx.QueryRow(ctx, `SELECT dispute.id::text,dispute.status,dispute.subject_kind,dispute.subject_entity_id::text
		FROM research_dispute dispute JOIN research_task_inquiry_target target
		ON (target.workspace_id,target.session_id,target.target_entity_id)=(dispute.workspace_id,dispute.session_id,dispute.id)
		WHERE dispute.workspace_id=$1::uuid AND dispute.session_id=$2::uuid AND dispute.client_key=$3
		AND target.task_id=$4::uuid AND target.target_kind='dispute' FOR UPDATE OF dispute`, state.workspaceID, state.run.SessionID,
		result.Dispute.ClientKey, state.task.ID).Scan(&disputeID, &disputeStatus, &subjectKind, &subjectID); err != nil {
		if err == pgx.ErrNoRows {
			return fmt.Errorf("%w: adjudication Task is not bound to the submitted Dispute", ErrInvalidContract)
		}
		return err
	}
	if disputeID != criteria.DisputeID || disputeStatus != "irreducible" || result.Envelope.StatusUpdates[0].Before != "irreducible" || result.Envelope.StatusUpdates[0].After != decision.Decision {
		return fmt.Errorf("%w: adjudication must transition its deadlocked Dispute to the declared decision", ErrInvalidTransition)
	}
	resolvedSubject, err := resolveV6EntityKeyTx(ctx, tx, state, result.Dispute.Subject)
	if err != nil {
		return err
	}
	if subjectKind != result.Dispute.Subject.Kind || subjectID != resolvedSubject {
		return fmt.Errorf("%w: adjudication Dispute subject changed", ErrControlTargetChanged)
	}
	var identityID, identityAgentID string
	var identityVersion int
	if err = tx.QueryRow(ctx, `SELECT id::text,agent_id::text,identity_version FROM research_director_identity
		WHERE workspace_id=$1::uuid AND session_id=$2::uuid ORDER BY identity_version DESC LIMIT 1 FOR UPDATE`, state.workspaceID, state.run.SessionID).Scan(&identityID, &identityAgentID, &identityVersion); err != nil {
		return err
	}
	if identityAgentID != decision.ActorAgentID || identityVersion != decision.DirectorIdentityVersion {
		return fmt.Errorf("%w: Research Director identity changed after adjudication dispatch", ErrControlTargetChanged)
	}
	var deliberationStatus string
	if err = tx.QueryRow(ctx, `SELECT status FROM research_deliberation WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$3::uuid AND dispute_id=$4::uuid FOR UPDATE`, state.workspaceID, state.run.SessionID, criteria.DeliberationID, disputeID).Scan(&deliberationStatus); err != nil {
		return err
	}
	if deliberationStatus != string(DeliberationDeadlocked) {
		return fmt.Errorf("%w: only a deadlocked Deliberation may be adjudicated", ErrInvalidTransition)
	}

	rows, err := tx.Query(ctx, `SELECT id::text FROM research_dispute_position WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND dispute_id=$3::uuid ORDER BY id::text`, state.workspaceID, state.run.SessionID, disputeID)
	if err != nil {
		return err
	}
	var canonicalPositions []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		canonicalPositions = append(canonicalPositions, id)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	assessed := make([]string, 0, len(decision.PositionAssessments))
	evidenceSet := map[string]ArtifactEntityKind{}
	for _, assessment := range decision.PositionAssessments {
		assessed = append(assessed, assessment.PositionID)
		if err = requireAdjudicationManifestArtifactTx(ctx, tx, state, assessment.PositionID); err != nil {
			return err
		}
		for _, ref := range assessment.EvidenceRefs {
			var evidenceKind ArtifactEntityKind
			switch ref.Kind {
			case "claim":
				evidenceKind = ArtifactKindClaim
			case "source":
				evidenceKind = ArtifactKindSourceSnapshot
			default:
				return fmt.Errorf("%w: adjudication evidence must reference a Claim or Source Snapshot", ErrInvalidResult)
			}
			evidenceID, resolveErr := resolveV6EntityKeyTx(ctx, tx, state, ref)
			if resolveErr != nil {
				return resolveErr
			}
			if err = requireAdjudicationManifestArtifactTx(ctx, tx, state, evidenceID); err != nil {
				return err
			}
			evidenceSet[evidenceID] = evidenceKind
		}
	}
	sort.Strings(assessed)
	if strings.Join(assessed, "\x00") != strings.Join(canonicalPositions, "\x00") {
		return fmt.Errorf("%w: adjudication must assess every canonical Dispute Position exactly once", ErrInvalidContract)
	}
	evidenceIDs := make([]string, 0, len(evidenceSet))
	for id := range evidenceSet {
		evidenceIDs = append(evidenceIDs, id)
	}
	sort.Strings(evidenceIDs)
	decisionID := uuid.NewSHA1(uuid.MustParse(disputeID), []byte(fmt.Sprintf("director-adjudication-result/%d", identityVersion))).String()
	conditions, _ := json.Marshal(decision.Conditions)
	assessments, _ := json.Marshal(decision.PositionAssessments)
	evidenceJSON, _ := json.Marshal(evidenceIDs)
	if _, err = tx.Exec(ctx, `INSERT INTO research_adjudication_decision
		(id,workspace_id,session_id,dispute_id,deliberation_id,task_id,attempt_id,director_identity_id,director_identity_version,decision,rationale,conditions,residual_uncertainty,position_assessments,evidence_ids)
		VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,$6::uuid,$7::uuid,$8::uuid,$9,$10,$11,$12::jsonb,$13,$14::jsonb,$15::jsonb)`, decisionID,
		state.workspaceID, state.run.SessionID, disputeID, criteria.DeliberationID, state.task.ID, state.attemptID, identityID, identityVersion,
		decision.Decision, strings.TrimSpace(decision.Rationale), conditions, strings.TrimSpace(decision.ResidualUncertainty), assessments, evidenceJSON); err != nil {
		return err
	}
	if err = registerAcceptedV6IntegrationArtifactTx(ctx, tx, state, decisionID, ArtifactKindAdjudicationDecision, map[string]any{
		"dispute_id": disputeID, "deliberation_id": criteria.DeliberationID, "director_identity_id": identityID, "director_identity_version": identityVersion,
		"decision": decision.Decision, "rationale": decision.Rationale, "conditions": decision.Conditions, "residual_uncertainty": decision.ResidualUncertainty,
		"position_assessments": decision.PositionAssessments, "evidence_ids": evidenceIDs}); err != nil {
		return err
	}
	inputs := []struct {
		id       string
		kind     ArtifactEntityKind
		relation string
	}{
		{identityID, ArtifactKindResearchDirectorIdentity, "authorized_by"}, {disputeID, ArtifactKindDispute, "adjudicates"}, {criteria.DeliberationID, ArtifactKindDeliberation, "resolves_deadlock"},
	}
	for _, input := range inputs {
		if err = persistTypedArtifactInputReferenceTx(ctx, tx, state.workspaceID, state.run.SessionID, decisionID, ArtifactKindAdjudicationDecision, input.id, input.kind, input.relation, "director_adjudication", 0); err != nil {
			return err
		}
	}
	for ordinal, id := range canonicalPositions {
		if err = persistTypedArtifactInputReferenceTx(ctx, tx, state.workspaceID, state.run.SessionID, decisionID, ArtifactKindAdjudicationDecision, id, ArtifactKindDisputePosition, "assesses", "director_adjudication", ordinal); err != nil {
			return err
		}
	}
	for ordinal, id := range evidenceIDs {
		if err = persistTypedArtifactInputReferenceTx(ctx, tx, state.workspaceID, state.run.SessionID, decisionID, ArtifactKindAdjudicationDecision, id, evidenceSet[id], "evidence_bound_by", "director_adjudication", ordinal); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE research_dispute SET status=$1,resolution_explanation=$2,updated_at=now() WHERE workspace_id=$3::uuid AND session_id=$4::uuid AND id=$5::uuid AND status='irreducible'`, decision.Decision, decision.Rationale, state.workspaceID, state.run.SessionID, disputeID); err != nil {
		return err
	}
	_, err = appendEvent(ctx, tx, state.workspaceID, state.run.SessionID, "v6_director_adjudication_materialized", "v6-director-adjudication:"+state.attemptID, "agent", agentID,
		map[string]any{"attempt_id": state.attemptID, "decision_id": decisionID, "dispute_id": disputeID, "deliberation_id": criteria.DeliberationID, "decision": decision.Decision, "director_identity_version": identityVersion, "evidence_ids": evidenceIDs})
	return err
}

func requireAdjudicationManifestArtifactTx(ctx context.Context, tx pgx.Tx, state acceptedResultState, artifactID string) error {
	var present bool
	err := tx.QueryRow(ctx, `SELECT true FROM research_artifact_context_manifest manifest
		JOIN research_artifact_context_entry entry ON (entry.workspace_id,entry.session_id,entry.manifest_id)=(manifest.workspace_id,manifest.session_id,manifest.id)
		JOIN research_artifact_version version ON (version.workspace_id,version.session_id,version.id)=(entry.workspace_id,entry.session_id,entry.artifact_version_id)
		JOIN research_artifact_passport passport ON (passport.workspace_id,passport.session_id,passport.id,passport.current_version)=(version.workspace_id,version.session_id,version.artifact_id,version.version)
		WHERE manifest.workspace_id=$1::uuid AND manifest.session_id=$2::uuid AND manifest.attempt_id=$3::uuid AND passport.id=$4::uuid LIMIT 1`,
		state.workspaceID, state.run.SessionID, state.attemptID, artifactID).Scan(&present)
	if err == pgx.ErrNoRows {
		return fmt.Errorf("%w: adjudication evidence %s is absent from the frozen Attempt manifest", ErrInvalidTransition, artifactID)
	}
	return err
}
