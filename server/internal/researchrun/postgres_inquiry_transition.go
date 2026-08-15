package researchrun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

type inquiryVersionState struct {
	passportVersion int32
	eligibility     int64
	versionRowID    string
	access          ArtifactAccessLevel
	goalVersion     *int32
	planVersion     *int32
	manifestID      string
}

func (s *PostgresStore) TransitionInquiry(ctx context.Context, in InquiryTransitionInput) (InquiryTransitionResult, error) {
	module := inquiryModule{}
	if err := module.ValidateTransitionInput(in); err != nil {
		return InquiryTransitionResult{}, err
	}
	in.Changes = canonicalInquiryTransitionChanges(in.Changes)
	payload := map[string]any{"attempt_id": in.AttemptID, "changes": in.Changes}
	tx, err := s.beginResearchTx(ctx, txOpInquiryTransition, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return InquiryTransitionResult{}, err
	}
	defer tx.Rollback(ctx)
	if event, found, err := loadMatchingInquiryTransitionEvent(ctx, tx, in, payload); err != nil {
		return InquiryTransitionResult{}, err
	} else if found {
		if err = s.commitResearchTx(ctx, txOpInquiryTransition, tx); err != nil {
			return InquiryTransitionResult{}, err
		}
		return InquiryTransitionResult{Event: event}, nil
	}
	var stateVersion int64
	var attemptAgent, attemptStatus string
	if err = tx.QueryRow(ctx, `SELECT session.state_version,attempt.assigned_agent_id::text,attempt.status
		FROM research_session session JOIN research_task_attempt attempt
		ON attempt.workspace_id=session.workspace_id AND attempt.session_id=session.id
		WHERE session.workspace_id=$1::uuid AND session.id=$2::uuid AND attempt.id=$3::uuid
		FOR UPDATE OF session,attempt`, in.WorkspaceID, in.SessionID, in.AttemptID).Scan(&stateVersion, &attemptAgent, &attemptStatus); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return InquiryTransitionResult{}, ErrRunNotFound
		}
		return InquiryTransitionResult{}, err
	}
	if stateVersion != in.ExpectedStateVersion {
		return InquiryTransitionResult{}, fmt.Errorf("%w: inquiry expected state version %d, got %d", ErrInvalidTransition, in.ExpectedStateVersion, stateVersion)
	}
	if attemptAgent != in.AgentID || (attemptStatus != string(AttemptStatusRunning) && attemptStatus != string(AttemptStatusSucceeded)) {
		return InquiryTransitionResult{}, fmt.Errorf("%w: inquiry producer is not the assigned active attempt", ErrInvalidTransition)
	}
	outputAccess, err := deriveManifestOutputAccessTx(ctx, tx, in.WorkspaceID, in.SessionID, in.AttemptID)
	if err != nil {
		return InquiryTransitionResult{}, err
	}
	for _, change := range in.Changes {
		versionState, err := lockInquiryVersionState(ctx, tx, in, change, outputAccess)
		if err != nil {
			return InquiryTransitionResult{}, err
		}
		content, err := updateInquiryDomainState(ctx, tx, in, change)
		if err != nil {
			return InquiryTransitionResult{}, err
		}
		if err = appendInquiryArtifactVersionTx(ctx, tx, in, change, versionState, content); err != nil {
			return InquiryTransitionResult{}, err
		}
	}
	event, err := appendEvent(ctx, tx, in.WorkspaceID, in.SessionID, "inquiry_state_changed", in.IdempotencyKey, "agent", in.AgentID, payload)
	if err != nil {
		return InquiryTransitionResult{}, err
	}
	if err = s.commitResearchTx(ctx, txOpInquiryTransition, tx); err != nil {
		return InquiryTransitionResult{}, err
	}
	return InquiryTransitionResult{Event: event}, nil
}

func loadMatchingInquiryTransitionEvent(ctx context.Context, tx pgx.Tx, in InquiryTransitionInput, payload any) (RunEvent, bool, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return RunEvent{}, false, err
	}
	var event RunEvent
	err = tx.QueryRow(ctx, `SELECT id::text,workspace_id::text,session_id::text,sequence,event_type,idempotency_key,actor_type,
		COALESCE(actor_id::text,''),payload,projection_attempts,created_at FROM research_run_event
		WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND idempotency_key=$3 FOR UPDATE`, in.WorkspaceID, in.SessionID, in.IdempotencyKey).Scan(
		&event.ID, &event.WorkspaceID, &event.SessionID, &event.Sequence, &event.Type, &event.IdempotencyKey, &event.ActorType, &event.ActorID, &event.Payload, &event.ProjectionAttempts, &event.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return RunEvent{}, false, nil
	}
	if err != nil {
		return RunEvent{}, false, err
	}
	if event.Type != "inquiry_state_changed" || event.ActorType != "agent" || event.ActorID != in.AgentID || !semanticJSONEqual(event.Payload, encoded) {
		return RunEvent{}, false, fmt.Errorf("%w: inquiry transition idempotency key was reused", ErrResultConflict)
	}
	return event, true, nil
}

func lockInquiryVersionState(ctx context.Context, tx pgx.Tx, in InquiryTransitionInput, change InquiryTransitionChange, outputAccess ArtifactAccessLevel) (inquiryVersionState, error) {
	var state inquiryVersionState
	var access string
	err := tx.QueryRow(ctx, `SELECT passport.current_version,passport.eligibility_revision,version.id::text,version.access_level,
		version.goal_version,version.plan_version,manifest.id::text
		FROM research_artifact_passport passport
		JOIN research_artifact_version version ON (version.workspace_id,version.session_id,version.artifact_id,version.version)=
		 (passport.workspace_id,passport.session_id,passport.id,passport.current_version)
		JOIN research_artifact_context_manifest manifest
		  ON manifest.workspace_id=passport.workspace_id AND manifest.session_id=passport.session_id
		 AND manifest.attempt_id=$5::uuid
		WHERE passport.workspace_id=$1::uuid AND passport.session_id=$2::uuid AND passport.id=$3::uuid
		 AND passport.entity_kind=$4 FOR UPDATE OF passport,version`, in.WorkspaceID, in.SessionID, change.EntityID, string(change.Kind), in.AttemptID).Scan(
		&state.passportVersion, &state.eligibility, &state.versionRowID, &access, &state.goalVersion, &state.planVersion, &state.manifestID)
	if errors.Is(err, pgx.ErrNoRows) {
		return state, fmt.Errorf("%w: inquiry artifact %s:%s is absent from the frozen Attempt manifest", ErrInvalidTransition, change.Kind, change.EntityID)
	}
	if err != nil {
		return state, err
	}
	state.access = ArtifactAccessLevel(access)
	if !(ArtifactPolicy{}).NormalAccessDominates(outputAccess, state.access) {
		return state, fmt.Errorf("%w: inquiry output access %q cannot carry prior %q state", ErrInvalidTransition, outputAccess, state.access)
	}
	// The new version inherits the most sensitive access level across the whole
	// frozen manifest, not merely the prior version's level.
	state.access = outputAccess
	return state, nil
}

func updateInquiryDomainState(ctx context.Context, tx pgx.Tx, in InquiryTransitionInput, change InquiryTransitionChange) (map[string]any, error) {
	var raw []byte
	var err error
	switch change.Kind {
	case InquiryKindHypothesis:
		err = tx.QueryRow(ctx, `UPDATE research_hypothesis SET status=$1,last_evaluated_state_version=$2,updated_at=now()
			WHERE workspace_id=$3::uuid AND session_id=$4::uuid AND id=$5::uuid AND status=$6
			RETURNING to_jsonb(research_hypothesis)-ARRAY['id','workspace_id','session_id','created_at','updated_at','created_by_task_id','created_by_attempt_id']`,
			change.AfterStatus, in.ExpectedStateVersion, in.WorkspaceID, in.SessionID, change.EntityID, change.BeforeStatus).Scan(&raw)
	case InquiryKindBranch:
		terminationReason := ""
		if change.AfterStatus == "terminated" {
			terminationReason = strings.TrimSpace(change.Reason)
		}
		err = tx.QueryRow(ctx, `UPDATE research_branch SET status=$1,termination_reason=$2,updated_at=now()
			WHERE workspace_id=$3::uuid AND session_id=$4::uuid AND id=$5::uuid AND status=$6
			RETURNING to_jsonb(research_branch)-ARRAY['id','workspace_id','session_id','created_at','updated_at','created_by_task_id']`,
			change.AfterStatus, terminationReason, in.WorkspaceID, in.SessionID, change.EntityID, change.BeforeStatus).Scan(&raw)
	case InquiryKindInsight:
		err = tx.QueryRow(ctx, `UPDATE research_insight SET status=$1,updated_at=now()
			WHERE workspace_id=$2::uuid AND session_id=$3::uuid AND id=$4::uuid AND status=$5
			RETURNING to_jsonb(research_insight)-ARRAY['id','workspace_id','session_id','created_at','updated_at','created_by_attempt_id']`,
			change.AfterStatus, in.WorkspaceID, in.SessionID, change.EntityID, change.BeforeStatus).Scan(&raw)
	default:
		return nil, fmt.Errorf("%w: unsupported inquiry transition kind %q", ErrInvalidTransition, change.Kind)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("%w: inquiry %s:%s no longer has status %q", ErrInvalidTransition, change.Kind, change.EntityID, change.BeforeStatus)
	}
	if err != nil {
		return nil, err
	}
	var content map[string]any
	if err = json.Unmarshal(raw, &content); err != nil {
		return nil, err
	}
	content["transition_reason"] = strings.TrimSpace(change.Reason)
	return content, nil
}

func appendInquiryArtifactVersionTx(ctx context.Context, tx pgx.Tx, in InquiryTransitionInput, change InquiryTransitionChange, state inquiryVersionState, content map[string]any) error {
	kind := ArtifactEntityKind(change.Kind)
	hash, err := ArtifactContentHash(kind, content)
	if err != nil {
		return err
	}
	next := state.passportVersion + 1
	var newVersionID string
	err = tx.QueryRow(ctx, `INSERT INTO research_artifact_version(workspace_id,session_id,artifact_id,version,schema_name,schema_version,
		canonicalization_version,content_hash,access_level,goal_version,plan_version,produced_by_attempt_id,hash_origin)
		VALUES($1::uuid,$2::uuid,$3::uuid,$4,$5,'research-run-v6',$6,$7,$8,$9,$10,$11::uuid,'production') RETURNING id::text`,
		in.WorkspaceID, in.SessionID, change.EntityID, next, string(kind), ArtifactCanonicalizationVersion, hash, string(state.access), state.goalVersion, state.planVersion, in.AttemptID).Scan(&newVersionID)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO research_artifact_input_reference(workspace_id,session_id,consumer_version_id,input_version_id,relation,manifest_id,explicitly_used,purpose,ordinal)
		VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,'revises',$5::uuid,true,'inquiry_transition',0)`, in.WorkspaceID, in.SessionID, newVersionID, state.versionRowID, state.manifestID); err != nil {
		return err
	}
	var watermark int64
	if err = tx.QueryRow(ctx, `SELECT research_artifact_policy_watermark_for_tx($1::uuid,$2::uuid)`, in.WorkspaceID, in.SessionID).Scan(&watermark); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE research_artifact_passport SET current_version=$1,eligibility_revision=$2 WHERE workspace_id=$3::uuid AND session_id=$4::uuid AND id=$5::uuid AND current_version=$6 AND eligibility_revision=$7`,
		next, state.eligibility+1, in.WorkspaceID, in.SessionID, change.EntityID, state.passportVersion, state.eligibility)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("%w: inquiry artifact version changed concurrently", ErrInvalidTransition)
	}
	_, err = tx.Exec(ctx, `INSERT INTO research_artifact_policy_mutation(workspace_id,session_id,watermark,mutation_kind,artifact_id,
		old_eligibility_revision,new_eligibility_revision,old_current_version,new_current_version,old_access_level,new_access_level,eligibility_reason)
		VALUES($1::uuid,$2::uuid,$3,'current_version',$4::uuid,$5,$6,$7,$8,NULL,NULL,$9)`, in.WorkspaceID, in.SessionID, watermark, change.EntityID, state.eligibility, state.eligibility+1, state.passportVersion, next, strings.TrimSpace(change.Reason))
	return err
}
