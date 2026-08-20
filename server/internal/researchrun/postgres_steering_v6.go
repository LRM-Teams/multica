package researchrun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func v6DirectRefExistsTx(ctx context.Context, tx pgx.Tx, kind, workspaceID, runID, entityID string) (bool, error) {
	var query string
	switch kind {
	case "goal":
		query = `SELECT EXISTS(SELECT 1 FROM research_session WHERE workspace_id=$1::uuid AND id=$2::uuid)`
	case "branch":
		query = `SELECT EXISTS(SELECT 1 FROM research_branch WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$3::uuid)`
	case "task":
		query = `SELECT EXISTS(SELECT 1 FROM research_task WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$3::uuid)`
	case "attempt":
		query = `SELECT EXISTS(SELECT 1 FROM research_task_attempt WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$3::uuid)`
	case "work_item":
		query = `SELECT EXISTS(SELECT 1 FROM research_work_item WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$3::uuid)`
	case "agent":
		query = `SELECT EXISTS(SELECT 1 FROM agent a JOIN research_team_membership m ON m.agent_id=a.id AND m.workspace_id=$1::uuid AND m.session_id=$2::uuid WHERE a.workspace_id=$1::uuid AND a.id=$3::uuid)`
	case "report":
		query = `SELECT EXISTS(SELECT 1 FROM research_report WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$3::uuid)`
	case "source_snapshot":
		query = `SELECT EXISTS(SELECT 1 FROM research_source_snapshot WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$3::uuid)`
	case "observation":
		query = `SELECT EXISTS(SELECT 1 FROM research_observation WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$3::uuid)`
	case "claim":
		query = `SELECT EXISTS(SELECT 1 FROM research_claim WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$3::uuid)`
	default:
		return false, nil
	}
	var exists bool
	var err error
	if kind == "goal" {
		err = tx.QueryRow(ctx, query, workspaceID, entityID).Scan(&exists)
	} else {
		err = tx.QueryRow(ctx, query, workspaceID, runID, entityID).Scan(&exists)
	}
	return exists, err
}

func refString(value any) string {
	valueString, _ := value.(string)
	return valueString
}

func resolveV6SelectedRefsTx(ctx context.Context, tx pgx.Tx, workspaceID, runID string, raw json.RawMessage) (json.RawMessage, error) {
	var refs []map[string]any
	if err := json.Unmarshal(raw, &refs); err != nil {
		return nil, fmt.Errorf("%w: selected refs are not an array", ErrInvalidContract)
	}
	for _, ref := range refs {
		status := "unavailable"
		entityID, idOK := ref["entity_id"].(string)
		revisionValue, revisionOK := ref["revision"].(float64)
		hash, hashOK := ref["content_hash"].(string)
		if idOK && revisionOK && hashOK {
			if _, err := uuid.Parse(entityID); err == nil && revisionValue >= 1 && revisionValue == float64(int64(revisionValue)) {
				var lifecycle string
				var currentVersion int
				var versionHash *string
				err = tx.QueryRow(ctx, `SELECT p.lifecycle_status,COALESCE(p.current_version,0),v.content_hash
					FROM research_artifact_passport p
					LEFT JOIN research_artifact_version v ON v.workspace_id=p.workspace_id AND v.session_id=p.session_id AND v.artifact_id=p.id AND v.version=$4
					WHERE p.workspace_id=$1::uuid AND p.session_id=$2::uuid AND p.id=$3::uuid`, workspaceID, runID, entityID, int(revisionValue)).Scan(&lifecycle, &currentVersion, &versionHash)
				if errors.Is(err, pgx.ErrNoRows) {
					exists, existsErr := v6DirectRefExistsTx(ctx, tx, refString(ref["kind"]), workspaceID, runID, entityID)
					if existsErr != nil {
						return nil, existsErr
					}
					if exists {
						status = "current"
					}
				} else if err != nil {
					return nil, err
				} else if versionHash != nil && (lifecycle == "registered" || lifecycle == "accepted") {
					if currentVersion == int(revisionValue) && *versionHash == hash {
						status = "current"
					} else {
						status = "stale"
					}
				} else {
					status = "stale"
				}
			}
		}
		ref["resolution_status"] = status
	}
	resolved, err := marshalV6CanonicalJSON(refs)
	if err != nil {
		return nil, err
	}
	return resolved, nil
}

func (s *PostgresStore) ApplyV6SteeringAssessment(ctx context.Context, in ApplyV6SteeringAssessmentInput) (V6SteeringAssessment, error) {
	tx, err := s.beginResearchTx(ctx, txOpV6SteeringApply, pgx.TxOptions{})
	if err != nil {
		return V6SteeringAssessment{}, err
	}
	defer tx.Rollback(ctx)
	if err = lockRunForMutation(ctx, tx, in.RunID, in.WorkspaceID); err != nil {
		return V6SteeringAssessment{}, err
	}
	if replay, found, replayErr := loadV6SteeringAssessmentTx(ctx, tx, in); replayErr != nil || found {
		return replay, replayErr
	}
	var goalVersion int
	var stateVersion int64
	var orchestrator, messageAuthor, cycleStatus string
	err = tx.QueryRow(ctx, `SELECT s.orchestrator_version,s.goal_version,s.state_version,m.sender_id::text,c.status
		FROM research_session s JOIN research_message m ON m.workspace_id=s.workspace_id AND m.session_id=s.id
		JOIN research_director_cycle c ON c.workspace_id=s.workspace_id AND c.session_id=s.id
		WHERE s.workspace_id=$1::uuid AND s.id=$2::uuid AND m.id=$3::uuid AND m.sender_type='user' AND c.id=$4::uuid
		AND c.director_assignment_id=s.current_director_assignment_id FOR UPDATE OF c`, in.WorkspaceID, in.RunID, in.MessageID, in.DirectorCycleID).
		Scan(&orchestrator, &goalVersion, &stateVersion, &messageAuthor, &cycleStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return V6SteeringAssessment{}, ErrAttemptNotAssigned
	}
	if err != nil {
		return V6SteeringAssessment{}, err
	}
	if orchestrator != OrchestratorVersionV6 || goalVersion != in.ExpectedGoalVersion || stateVersion != in.ExpectedStateVersion ||
		(cycleStatus != "running" && cycleStatus != "pending") {
		return V6SteeringAssessment{}, ErrWorkItemChanged
	}
	before := goalVersion
	if in.AssessmentKind == "goal_revision" {
		goalVersion++
		if err = reviseV6GoalTx(ctx, tx, in, messageAuthor, goalVersion); err != nil {
			return V6SteeringAssessment{}, err
		}
	}
	affected := make([]map[string]any, 0, len(in.Impacts))
	for _, impact := range in.Impacts {
		if err = applyV6SteeringImpactTx(ctx, tx, in, impact, goalVersion); err != nil {
			return V6SteeringAssessment{}, err
		}
		affected = append(affected, map[string]any{"kind": impact.Kind, "id": impact.ID, "expected_state_version": impact.ExpectedStateVersion, "disposition": impact.Disposition, "reason": impact.Reason})
	}
	affectedRaw, err := marshalV6CanonicalJSON(affected)
	if err != nil {
		return V6SteeringAssessment{}, err
	}
	selected := in.SelectedRefs
	if len(selected) == 0 {
		if err = tx.QueryRow(ctx, `SELECT selected_refs FROM research_v6_steering_trigger WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND research_message_id=$3::uuid`,
			in.WorkspaceID, in.RunID, in.MessageID).Scan(&selected); err != nil {
			return V6SteeringAssessment{}, err
		}
	}
	selected = normalizedV6JSON(selected, `[]`)
	selected, err = resolveV6SelectedRefsTx(ctx, tx, in.WorkspaceID, in.RunID, selected)
	if err != nil {
		return V6SteeringAssessment{}, err
	}
	actions, err := json.Marshal(in.AcceptedActionIDs)
	if err != nil {
		return V6SteeringAssessment{}, err
	}
	assessment := V6SteeringAssessment{ID: uuid.NewString(), Kind: in.AssessmentKind, MessageID: in.MessageID,
		DirectorCycleID: in.DirectorCycleID, GoalVersionBefore: before, GoalVersionAfter: goalVersion, AffectedRefs: affectedRaw}
	_, err = tx.Exec(ctx, `INSERT INTO research_steering_assessment(id,workspace_id,session_id,research_message_id,director_cycle_id,
		goal_version_before,goal_version_after,selected_refs,affected_refs,assessment_kind,interpretation,reason,actions)
		VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,$6,$7,$8::jsonb,$9::jsonb,$10,$11,$12,$13::jsonb)`,
		assessment.ID, in.WorkspaceID, in.RunID, in.MessageID, in.DirectorCycleID, before, goalVersion, selected, affectedRaw,
		in.AssessmentKind, strings.TrimSpace(in.Interpretation), strings.TrimSpace(in.Reason), actions)
	if err != nil {
		return V6SteeringAssessment{}, err
	}
	command, err := tx.Exec(ctx, `UPDATE research_session SET goal_version=$3,state_version=state_version+1,updated_at=now()
		WHERE workspace_id=$1::uuid AND id=$2::uuid AND state_version=$4`, in.WorkspaceID, in.RunID, goalVersion, in.ExpectedStateVersion)
	if err != nil {
		return V6SteeringAssessment{}, err
	}
	if command.RowsAffected() != 1 {
		return V6SteeringAssessment{}, ErrWorkItemChanged
	}
	if _, err = appendEvent(ctx, tx, in.WorkspaceID, in.RunID, "v6_steering_assessment_applied", "v6-steering:"+in.MessageID,
		"director", "", map[string]any{"assessment_id": assessment.ID, "message_id": in.MessageID, "assessment_kind": in.AssessmentKind,
			"goal_version_before": before, "goal_version_after": goalVersion, "affected_refs": affected}); err != nil {
		return V6SteeringAssessment{}, err
	}
	if err = s.commitResearchTx(ctx, txOpV6SteeringApply, tx); err != nil {
		return V6SteeringAssessment{}, err
	}
	return assessment, nil
}

func loadV6SteeringAssessmentTx(ctx context.Context, tx pgx.Tx, in ApplyV6SteeringAssessmentInput) (V6SteeringAssessment, bool, error) {
	var out V6SteeringAssessment
	err := tx.QueryRow(ctx, `SELECT id::text,assessment_kind,research_message_id::text,director_cycle_id::text,
		goal_version_before,goal_version_after,affected_refs FROM research_steering_assessment
		WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND research_message_id=$3::uuid`, in.WorkspaceID, in.RunID, in.MessageID).
		Scan(&out.ID, &out.Kind, &out.MessageID, &out.DirectorCycleID, &out.GoalVersionBefore, &out.GoalVersionAfter, &out.AffectedRefs)
	if errors.Is(err, pgx.ErrNoRows) {
		return V6SteeringAssessment{}, false, nil
	}
	if err != nil {
		return V6SteeringAssessment{}, false, err
	}
	if out.DirectorCycleID != in.DirectorCycleID || out.Kind != in.AssessmentKind {
		return V6SteeringAssessment{}, false, ErrResultConflict
	}
	return out, true, nil
}

func reviseV6GoalTx(ctx context.Context, tx pgx.Tx, in ApplyV6SteeringAssessmentInput, author string, nextVersion int) error {
	var scope, sourcePolicy, limits json.RawMessage
	var audience, freshness, language string
	if err := tx.QueryRow(ctx, `SELECT scope,audience,freshness,language,source_policy,run_limits FROM research_contract_revision
		WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND goal_version=$3`, in.WorkspaceID, in.RunID, nextVersion-1).
		Scan(&scope, &audience, &freshness, &language, &sourcePolicy, &limits); err != nil {
		return err
	}
	if len(in.RevisedScope) > 0 {
		scope = normalizedV6JSON(in.RevisedScope, `{}`)
	}
	if len(in.RevisedSourcePolicy) > 0 {
		sourcePolicy = normalizedV6JSON(in.RevisedSourcePolicy, `{}`)
	}
	if len(in.RevisedLimits) > 0 {
		limits = normalizedV6JSON(in.RevisedLimits, `{}`)
	}
	if in.RevisedAudience != "" {
		audience = in.RevisedAudience
	}
	if in.RevisedFreshness != "" {
		freshness = in.RevisedFreshness
	}
	if in.RevisedLanguage != "" {
		language = in.RevisedLanguage
	}
	_, err := tx.Exec(ctx, `INSERT INTO research_contract_revision(workspace_id,session_id,goal_version,goal,scope,audience,freshness,language,source_policy,run_limits,authored_by,reason)
		VALUES($1::uuid,$2::uuid,$3,$4,$5::jsonb,$6,$7,$8,$9::jsonb,$10::jsonb,$11::uuid,$12)`, in.WorkspaceID, in.RunID, nextVersion,
		strings.TrimSpace(in.RevisedGoal), scope, audience, freshness, language, sourcePolicy, limits, author, in.Reason)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE research_session SET goal=$3 WHERE workspace_id=$1::uuid AND id=$2::uuid`, in.WorkspaceID, in.RunID, strings.TrimSpace(in.RevisedGoal))
	return err
}

func applyV6SteeringImpactTx(ctx context.Context, tx pgx.Tx, in ApplyV6SteeringAssessmentInput, impact V6SteeringImpact, goalVersion int) error {
	switch impact.Kind {
	case "branch":
		var version int64
		var status string
		if err := tx.QueryRow(ctx, `SELECT state_version,status FROM research_branch WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$3::uuid FOR UPDATE`,
			in.WorkspaceID, in.RunID, impact.ID).Scan(&version, &status); err != nil {
			return err
		}
		if version != impact.ExpectedStateVersion || status == "terminated" || status == "obsolete" {
			return ErrWorkItemChanged
		}
		switch impact.Disposition {
		case "continue":
			return nil
		case "change_direction", "revalidate":
			_, err := tx.Exec(ctx, `UPDATE research_branch SET goal_version=$4,state_version=state_version+1,reason_code=$5,reason_detail=$6,updated_at=now()
				WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$3::uuid`, in.WorkspaceID, in.RunID, impact.ID, goalVersion, impact.Disposition, impact.Reason)
			return err
		case "terminate":
			return applyV6TerminalPropagationTx(ctx, tx, in.WorkspaceID, in.RunID, impact.ID, impact.Reason)
		default:
			return fmt.Errorf("%w: unsupported branch disposition %q", ErrInvalidContract, impact.Disposition)
		}
	case "result_node":
		if impact.Disposition != "challenge" && impact.Disposition != "refute" && impact.Disposition != "terminate" {
			return fmt.Errorf("%w: unsupported result disposition", ErrInvalidContract)
		}
		state := map[string]string{"challenge": "challenged", "refute": "refuted", "terminate": "invalid"}[impact.Disposition]
		command, err := tx.Exec(ctx, `UPDATE research_result_node SET conclusion_state=$4,reason_code=$5,reason_detail=$6
			WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND artifact_version_id=$3::uuid AND conclusion_state IN ('proposed','accepted')`,
			in.WorkspaceID, in.RunID, impact.ID, state, "steering_"+impact.Disposition, impact.Reason)
		if err != nil || command.RowsAffected() != 1 {
			if err == nil {
				return ErrWorkItemChanged
			}
			return err
		}
		return challengeV6SuccessorsTx(ctx, tx, in, impact)
	case "insight":
		if impact.Disposition != "challenge" && impact.Disposition != "refute" && impact.Disposition != "terminate" {
			return fmt.Errorf("%w: unsupported insight disposition", ErrInvalidContract)
		}
		state := map[string]string{"challenge": "challenged", "refute": "refuted", "terminate": "terminal"}[impact.Disposition]
		command, err := tx.Exec(ctx, `UPDATE research_insight_version SET status=$4 WHERE workspace_id=$1::uuid AND session_id=$2::uuid
			AND artifact_version_id=$3::uuid AND status='accepted'`, in.WorkspaceID, in.RunID, impact.ID, state)
		if err != nil || command.RowsAffected() != 1 {
			if err == nil {
				return ErrWorkItemChanged
			}
			return err
		}
		return challengeV6SuccessorsTx(ctx, tx, in, impact)
	case "work_item":
		if impact.Disposition != "cancel" {
			return fmt.Errorf("%w: unsupported work disposition", ErrInvalidContract)
		}
		command, err := tx.Exec(ctx, `UPDATE research_work_item SET status='cancelled',terminal_reason_code='steering_cancelled',terminal_reason_detail=$4,
			cancelled_at=now(),lease_token=NULL,lease_expires_at=NULL,updated_at=now() WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$3::uuid
			AND status IN ('pending','ready','dispatching','running','awaiting_input')`, in.WorkspaceID, in.RunID, impact.ID, impact.Reason)
		if err != nil || command.RowsAffected() != 1 {
			if err == nil {
				return ErrWorkItemChanged
			}
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE research_work_item_attempt SET status='cancelled',failure_class='steering_cancelled',diagnostics=$2,
			cancellation_completed_at=now(),completed_at=now(),updated_at=now() WHERE work_item_id=$1::uuid AND status IN ('dispatching','running')`, impact.ID, impact.Reason)
		return err
	default:
		return fmt.Errorf("%w: unsupported steering impact kind %q", ErrInvalidContract, impact.Kind)
	}
}

func challengeV6SuccessorsTx(ctx context.Context, tx pgx.Tx, in ApplyV6SteeringAssessmentInput, impact V6SteeringImpact) error {
	_, err := tx.Exec(ctx, `WITH RECURSIVE successors AS (
		SELECT iv.id,iv.artifact_version_id FROM research_node_absorption a JOIN research_insight_version iv ON iv.id=a.successor_insight_version_id
		WHERE a.workspace_id=$1::uuid AND a.session_id=$2::uuid AND a.input_artifact_version_id=$3::uuid
		UNION SELECT next_iv.id,next_iv.artifact_version_id FROM successors s JOIN research_node_absorption a ON a.input_artifact_version_id=s.artifact_version_id
		JOIN research_insight_version next_iv ON next_iv.id=a.successor_insight_version_id
	) UPDATE research_insight_version iv SET status='challenged' FROM successors s WHERE iv.id=s.id AND iv.status='accepted'`, in.WorkspaceID, in.RunID, impact.ID)
	if err != nil {
		return err
	}
	// Repair is explicit work; absorbed children are never restored to Frontier.
	_, err = tx.Exec(ctx, `INSERT INTO research_work_item(id,workspace_id,session_id,kind,status,target_kind,target_id,client_key,idempotency_key,
		goal_version,input_state_version,input_event_sequence,assigned_agent_id,priority,max_attempts,payload_schema_id,expected_result_schema_id,payload,state_version,ready_at,created_by_director_cycle_id)
		SELECT gen_random_uuid(),$1::uuid,$2::uuid,'review','ready','artifact_version',$3::uuid,$4,$4,s.goal_version,s.state_version,
			COALESCE((SELECT max(sequence) FROM research_run_event WHERE session_id=s.id),0),st.agent_id,0.9,3,'research.challenge_repair.v1','atomic_result_submission',
			jsonb_build_object('artifact_version_ids',jsonb_build_array($3::text),'challenge_reason',$5::text,'task_specific_schema',jsonb_build_object(
				'type','object','additionalProperties',false,'required',jsonb_build_array('repair_summary'),
				'properties',jsonb_build_object('repair_summary',jsonb_build_object('type','string','minLength',1),'recommended_action',jsonb_build_object('type','string')))),1,now(),$6::uuid
		FROM research_session s JOIN research_node_steward_assignment st ON st.session_id=s.id AND st.node_artifact_version_id=$3::uuid AND st.status='active'
		WHERE s.workspace_id=$1::uuid AND s.id=$2::uuid ON CONFLICT (session_id,goal_version,idempotency_key) DO NOTHING`,
		in.WorkspaceID, in.RunID, impact.ID, "steering-repair:"+in.MessageID+":"+impact.ID, impact.Reason, in.DirectorCycleID)
	return err
}
