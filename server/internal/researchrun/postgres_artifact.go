package researchrun

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type registerArtifactPassportInput struct {
	WorkspaceID             string
	SessionID               string
	EntityID                string
	Kind                    ArtifactEntityKind
	SourceCreatedAt         *time.Time
	ProvenanceCompleteness  ArtifactProvenanceCompleteness
	GoalVersion             *int32
	PlanVersion             *int32
	SchemaName              string
	SchemaVersion           string
	AccessLevel             ArtifactAccessLevel
	HashOrigin              ArtifactHashOrigin
	ContentHash             string
	ProducedByTaskID        string
	ProducedByAttemptID     string
	ProducedByWorkItemID    string
	ProducedByWorkAttemptID string
	ProducedByAgentID       string
}

type mutableArtifactVersion struct {
	Version             int32
	EligibilityRevision int64
	ContentHash         string
	AccessLevel         ArtifactAccessLevel
	HashOrigin          ArtifactHashOrigin
	SchemaName          string
	SchemaVersion       string
}

func lockMutableArtifactVersionForAttemptTx(
	ctx context.Context,
	tx pgx.Tx,
	workspaceID, sessionID, artifactID, attemptID string,
	kind ArtifactEntityKind,
	requiredAccess ArtifactAccessLevel,
) (mutableArtifactVersion, error) {
	var state mutableArtifactVersion
	var accessLevel, hashOrigin string
	var authorized bool
	err := tx.QueryRow(ctx, `
		SELECT p.current_version, p.eligibility_revision, v.content_hash,
		       v.access_level, v.hash_origin, v.schema_name, v.schema_version,
		       EXISTS (
		         SELECT 1
		         FROM research_artifact_context_manifest manifest
		         JOIN research_artifact_context_entry entry
		           ON entry.workspace_id=manifest.workspace_id
		          AND entry.session_id=manifest.session_id
		          AND entry.manifest_id=manifest.id
		         WHERE manifest.workspace_id=p.workspace_id
		           AND manifest.session_id=p.session_id
		           AND manifest.attempt_id=$4::uuid
		           AND entry.artifact_version_id=v.id
		       )
		FROM research_artifact_passport p
		JOIN research_artifact_version v
		  ON (v.workspace_id,v.session_id,v.artifact_id,v.version)=
		     (p.workspace_id,p.session_id,p.id,p.current_version)
		WHERE p.workspace_id=$1::uuid AND p.session_id=$2::uuid
		  AND p.id=$3::uuid AND p.entity_kind=$5
		  AND p.lifecycle_status IN ('registered','accepted')
		FOR UPDATE OF p
	`, workspaceID, sessionID, artifactID, attemptID, string(kind)).Scan(
		&state.Version, &state.EligibilityRevision, &state.ContentHash,
		&accessLevel, &hashOrigin, &state.SchemaName, &state.SchemaVersion, &authorized,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return mutableArtifactVersion{}, fmt.Errorf("%w: mutable artifact %s has no admissible current version", ErrResultConflict, artifactID)
	}
	if err != nil {
		return mutableArtifactVersion{}, err
	}
	if !authorized {
		return mutableArtifactVersion{}, fmt.Errorf("%w: mutable artifact %s was not frozen into attempt context", ErrResultConflict, artifactID)
	}
	state.AccessLevel = ArtifactAccessLevel(accessLevel)
	state.HashOrigin = ArtifactHashOrigin(hashOrigin)
	if !(ArtifactPolicy{}).NormalAccessDominates(state.AccessLevel, requiredAccess) {
		return mutableArtifactVersion{}, fmt.Errorf("%w: existing artifact access %q cannot accept %q-tainted revision", ErrInvalidTransition, state.AccessLevel, requiredAccess)
	}
	return state, nil
}

func appendProducedArtifactVersionTx(
	ctx context.Context,
	tx pgx.Tx,
	workspaceID, sessionID, artifactID, taskID, attemptID string,
	goalVersion, planVersion int,
	state mutableArtifactVersion,
	contentHash string,
) error {
	nextVersion := state.Version + 1
	if _, err := tx.Exec(ctx, `
		INSERT INTO research_artifact_version (
		  workspace_id, session_id, artifact_id, version, schema_name, schema_version,
		  canonicalization_version, content_hash, access_level, goal_version, plan_version,
		  produced_by_task_id, produced_by_attempt_id, hash_origin
		) VALUES (
		  $1::uuid,$2::uuid,$3::uuid,$4,$5,$6,
		  $7,$8,$9,$10,$11,$12::uuid,$13::uuid,'production'
		)
	`, workspaceID, sessionID, artifactID, nextVersion, state.SchemaName, state.SchemaVersion,
		ArtifactCanonicalizationVersion, contentHash, string(state.AccessLevel), goalVersion, planVersion,
		taskID, attemptID); err != nil {
		return err
	}
	var watermark int64
	if err := tx.QueryRow(ctx, `
		SELECT research_artifact_policy_watermark_for_tx($1::uuid,$2::uuid)
	`, workspaceID, sessionID).Scan(&watermark); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `
		UPDATE research_artifact_passport
		SET current_version=$6, eligibility_revision=eligibility_revision+1,
		    provenance_completeness='complete'
		WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$3::uuid
		  AND current_version=$4 AND eligibility_revision=$5
	`, workspaceID, sessionID, artifactID, state.Version, state.EligibilityRevision, nextVersion)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("%w: artifact version CAS failed", ErrResultConflict)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO research_artifact_policy_mutation (
		  workspace_id, session_id, watermark, mutation_kind, artifact_id,
		  old_eligibility_revision, new_eligibility_revision,
		  old_current_version, new_current_version,
		  old_access_level, new_access_level
		) VALUES (
		  $1::uuid,$2::uuid,$3,'current_version',$4::uuid,
		  $5,$6,$7,$8,NULL,NULL
		)
	`, workspaceID, sessionID, watermark, artifactID,
		state.EligibilityRevision, state.EligibilityRevision+1,
		state.Version, nextVersion)
	return err
}

func migrationArtifactContentHash(kind ArtifactEntityKind, workspaceID, sessionID, entityID string) string {
	payload := fmt.Sprintf(
		"research-artifact-migration:%s:%s:%s:%s",
		kind, workspaceID, sessionID, entityID,
	)
	sum := sha256.Sum256([]byte(payload))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func ensureSessionPolicyStateTx(ctx context.Context, tx pgx.Tx, workspaceID, sessionID string) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO research_artifact_policy_state (workspace_id, session_id, policy_version, watermark)
		SELECT $1::uuid, $2::uuid,
		  CASE WHEN orchestrator_version=$3 THEN $4 ELSE $5 END, 0
		FROM research_session WHERE workspace_id=$1::uuid AND id=$2::uuid
		ON CONFLICT (workspace_id, session_id) DO NOTHING
	`, workspaceID, sessionID, OrchestratorVersionV6, ResearchV6ContextPolicy, LegacyV1V5CompatPolicy)
	return err
}

func recordVerificationPolicyMutationTx(ctx context.Context, tx pgx.Tx, workspaceID, sessionID, artifactID string) error {
	_, err := tx.Exec(ctx, `
		WITH marked AS (
			SELECT 1
			FROM research_artifact_verification_tx_marker marker
			WHERE marker.workspace_id = $1::uuid
			  AND marker.session_id = $2::uuid
			  AND marker.entity_id = $3::uuid
		), watermark AS (
			SELECT research_artifact_policy_watermark_for_tx($1::uuid, $2::uuid) AS value
			FROM marked
		), revised AS (
			UPDATE research_artifact_passport p
			SET eligibility_revision = p.eligibility_revision + 1
			FROM watermark
			WHERE p.workspace_id = $1::uuid
			  AND p.session_id = $2::uuid
			  AND p.id = $3::uuid
			  AND NOT EXISTS (
				SELECT 1
				FROM research_artifact_policy_mutation mutation
				WHERE mutation.workspace_id = p.workspace_id
				  AND mutation.session_id = p.session_id
				  AND mutation.artifact_id = p.id
				  AND mutation.mutation_kind IN ('verification', 'current_version')
				  AND mutation.watermark = watermark.value
			  )
			RETURNING p.eligibility_revision, watermark.value
		)
		INSERT INTO research_artifact_policy_mutation (
			workspace_id, session_id, watermark, mutation_kind, artifact_id,
			old_eligibility_revision, new_eligibility_revision
		)
		SELECT $1::uuid, $2::uuid, revised.value, 'verification', $3::uuid,
		       revised.eligibility_revision - 1, revised.eligibility_revision
		FROM revised
	`, workspaceID, sessionID, artifactID)
	return err
}

func registerArtifactPassportTx(ctx context.Context, tx pgx.Tx, in registerArtifactPassportInput) error {
	if in.HashOrigin == ArtifactHashOriginProduction && in.ContentHash == "" {
		return fmt.Errorf("%w: production artifact %s requires an explicit content hash", ErrInvalidContract, in.Kind)
	}
	if in.AccessLevel == "" {
		in.AccessLevel = ArtifactAccessRaw
	}
	if in.HashOrigin == "" {
		in.HashOrigin = ArtifactHashOriginMigrationRecomputed
	}
	if in.ProvenanceCompleteness == "" {
		in.ProvenanceCompleteness = ArtifactProvenancePartial
	}
	if in.SchemaName == "" {
		in.SchemaName = string(in.Kind)
	}
	if in.SchemaVersion == "" {
		in.SchemaVersion = "legacy-v1"
	}
	contentHash := in.ContentHash
	if contentHash == "" {
		contentHash = migrationArtifactContentHash(in.Kind, in.WorkspaceID, in.SessionID, in.EntityID)
	}

	tag, err := tx.Exec(ctx, `
		INSERT INTO research_artifact_passport (
			id, workspace_id, session_id, entity_kind, current_version, eligibility_revision,
			lifecycle_status, provenance_completeness, source_created_at, registered_at
		) VALUES (
			$1::uuid, $2::uuid, $3::uuid, $4, NULL, 1,
			'registered', $5, $6, now()
		)
		ON CONFLICT (workspace_id, session_id, id) DO NOTHING
	`, in.EntityID, in.WorkspaceID, in.SessionID, string(in.Kind), string(in.ProvenanceCompleteness), in.SourceCreatedAt)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		var currentVersion *int32
		var currentAccess *string
		err = tx.QueryRow(ctx, `
			SELECT p.current_version, v.access_level
			FROM research_artifact_passport p
			LEFT JOIN research_artifact_version v
			  ON (v.workspace_id, v.session_id, v.artifact_id, v.version) =
			     (p.workspace_id, p.session_id, p.id, p.current_version)
			WHERE p.workspace_id = $1::uuid AND p.session_id = $2::uuid AND p.id = $3::uuid
		`, in.WorkspaceID, in.SessionID, in.EntityID).Scan(&currentVersion, &currentAccess)
		if err != nil {
			return err
		}
		if currentVersion != nil {
			if currentAccess == nil || !(ArtifactPolicy{}).NormalAccessDominates(ArtifactAccessLevel(*currentAccess), in.AccessLevel) {
				return fmt.Errorf("%w: existing artifact access %q cannot accept %q-tainted output", ErrInvalidTransition, stringPtrValue(currentAccess), in.AccessLevel)
			}
			return nil
		}
	}

	var versionCount int
	if err = tx.QueryRow(ctx, `
		SELECT count(*)::int FROM research_artifact_version
		WHERE workspace_id = $1::uuid AND session_id = $2::uuid
		  AND artifact_id = $3::uuid AND version = 1
	`, in.WorkspaceID, in.SessionID, in.EntityID).Scan(&versionCount); err != nil {
		return err
	}
	if versionCount == 0 {
		if _, err = tx.Exec(ctx, `
			INSERT INTO research_artifact_version (
				workspace_id, session_id, artifact_id, version, schema_name, schema_version,
				canonicalization_version, content_hash, access_level, goal_version, plan_version,
				hash_origin, produced_by_task_id, produced_by_attempt_id,
				produced_by_work_item_id, produced_by_work_item_attempt_id, produced_by_agent_id
			) VALUES (
				$1::uuid, $2::uuid, $3::uuid, 1, $4, $5,
				$6, $7, $8, $9, $10, $11, NULLIF($12, '')::uuid, NULLIF($13, '')::uuid,
				NULLIF($14, '')::uuid, NULLIF($15, '')::uuid, NULLIF($16, '')::uuid
			)
		`, in.WorkspaceID, in.SessionID, in.EntityID, in.SchemaName, in.SchemaVersion,
			ArtifactCanonicalizationVersion, contentHash, string(in.AccessLevel),
			in.GoalVersion, in.PlanVersion, string(in.HashOrigin), in.ProducedByTaskID, in.ProducedByAttemptID,
			in.ProducedByWorkItemID, in.ProducedByWorkAttemptID, in.ProducedByAgentID); err != nil {
			return err
		}
	}

	_, err = tx.Exec(ctx, `
		UPDATE research_artifact_passport
		SET current_version = 1
		WHERE workspace_id = $1::uuid AND session_id = $2::uuid AND id = $3::uuid
		  AND current_version IS NULL
	`, in.WorkspaceID, in.SessionID, in.EntityID)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		SELECT research_artifact_record_artifact_create_mutation($1::uuid, $2::uuid, $3::uuid)
	`, in.WorkspaceID, in.SessionID, in.EntityID)
	return err
}

func stringPtrValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func registerProductionDecisionPassportTx(
	ctx context.Context,
	tx pgx.Tx,
	workspaceID, sessionID, decisionID, producedByAttemptID string,
	accessLevel ArtifactAccessLevel,
) error {
	var decisionKind, actorType, actorID, rationale string
	var goalVersion, planVersion int32
	var inputs, outcome []byte
	var createdAt time.Time
	err := tx.QueryRow(ctx, `
		SELECT decision_kind, actor_type, COALESCE(actor_id::text, ''),
		       goal_version, plan_version, inputs, outcome, rationale, created_at
		FROM research_decision
		WHERE workspace_id = $1::uuid AND session_id = $2::uuid AND id = $3::uuid
	`, workspaceID, sessionID, decisionID).Scan(
		&decisionKind, &actorType, &actorID, &goalVersion, &planVersion,
		&inputs, &outcome, &rationale, &createdAt,
	)
	if err != nil {
		return err
	}
	kind := artifactKindForDecision(decisionKind)
	contentHash, err := ArtifactContentHash(kind, decisionArtifactContent(
		decisionKind, actorType, actorID, int(goalVersion), int(planVersion), inputs, outcome, rationale,
	))
	if err != nil {
		return err
	}
	if err = registerArtifactPassportTx(ctx, tx, registerArtifactPassportInput{
		WorkspaceID: workspaceID, SessionID: sessionID, EntityID: decisionID,
		Kind: kind, SourceCreatedAt: &createdAt,
		ProvenanceCompleteness: ArtifactProvenanceComplete,
		GoalVersion:            &goalVersion, PlanVersion: &planVersion,
		AccessLevel: accessLevel, HashOrigin: ArtifactHashOriginProduction,
		ContentHash: contentHash, ProducedByAttemptID: producedByAttemptID,
	}); err != nil {
		return err
	}
	return persistDecisionInputReferencesTx(
		ctx, tx, workspaceID, sessionID, decisionID, kind, inputs, outcome,
	)
}

func persistDecisionInputReferencesTx(
	ctx context.Context,
	tx pgx.Tx,
	workspaceID, sessionID, decisionID string,
	decisionKind ArtifactEntityKind,
	inputsJSON, outcomeJSON []byte,
) error {
	var inputs, outcome map[string]json.RawMessage
	if err := json.Unmarshal(inputsJSON, &inputs); err != nil {
		return fmt.Errorf("%w: decode Decision inputs lineage: %v", ErrInvalidContract, err)
	}
	if err := json.Unmarshal(outcomeJSON, &outcome); err != nil {
		return fmt.Errorf("%w: decode Decision outcome lineage: %v", ErrInvalidContract, err)
	}
	type referenceSpec struct {
		container map[string]json.RawMessage
		field     string
		kind      ArtifactEntityKind
		relation  string
	}
	direct := []referenceSpec{
		{inputs, "task_id", ArtifactKindTask, "decision_input_task"},
		{inputs, "attempt_id", ArtifactKindAttempt, "decision_input_attempt"},
		{inputs, "question_id", ArtifactKindQuestion, "decision_input_question"},
		{inputs, "report_id", ArtifactKindReportRevision, "decision_input_report"},
		{outcome, "created_by_task_id", ArtifactKindTask, "decision_creator_task"},
		{outcome, "task_id", ArtifactKindTask, "decision_outcome_task"},
		{outcome, "attempt_id", ArtifactKindAttempt, "decision_outcome_attempt"},
		{outcome, "question_id", ArtifactKindQuestion, "decision_outcome_question"},
		{outcome, "report_id", ArtifactKindReportRevision, "decision_outcome_report"},
		{outcome, "evaluation_decision_id", ArtifactKindEvaluationDecision, "decision_evaluation"},
	}
	for _, reference := range direct {
		artifactID, present, err := decisionReferenceID(reference.container, reference.field)
		if err != nil {
			return err
		}
		if !present {
			continue
		}
		if err = persistTypedArtifactInputReferenceTx(
			ctx, tx, workspaceID, sessionID,
			decisionID, decisionKind, artifactID, reference.kind,
			reference.relation, "decision_materialization", 0,
		); err != nil {
			return err
		}
	}
	arrays := []referenceSpec{
		{inputs, "affected_branch_ids", ArtifactKindBranch, "decision_affected_branch"},
		{outcome, "impacted_branch_ids", ArtifactKindBranch, "decision_impacted_branch"},
		{outcome, "obsolete_branch_ids", ArtifactKindBranch, "decision_obsolete_branch"},
		{outcome, "obsolete_task_ids", ArtifactKindTask, "decision_obsolete_task"},
		{outcome, "cancel_running_task_ids", ArtifactKindTask, "decision_cancel_task"},
		{outcome, "retained_running_task_ids", ArtifactKindTask, "decision_retained_task"},
	}
	for _, reference := range arrays {
		raw, present := reference.container[reference.field]
		if !present || string(raw) == "null" {
			continue
		}
		var artifactIDs []string
		if err := json.Unmarshal(raw, &artifactIDs); err != nil {
			return fmt.Errorf("%w: Decision has invalid %s reference array", ErrInvalidContract, reference.field)
		}
		for ordinal, artifactID := range artifactIDs {
			if strings.TrimSpace(artifactID) == "" {
				return fmt.Errorf("%w: Decision has empty %s reference", ErrInvalidContract, reference.field)
			}
			if err := persistTypedArtifactInputReferenceTx(
				ctx, tx, workspaceID, sessionID,
				decisionID, decisionKind, artifactID, reference.kind,
				reference.relation, "decision_materialization", ordinal,
			); err != nil {
				return err
			}
		}
	}
	if decisionKind == ArtifactKindEvaluationDecision {
		if err := persistEvaluationDecisionLocalReferencesTx(
			ctx, tx, workspaceID, sessionID, decisionID, inputs, outcome,
		); err != nil {
			return err
		}
	}
	return nil
}

type evaluationDecisionDefectReferences struct {
	ClaimKeys  []string `json:"claim_keys"`
	SectionIDs []string `json:"section_ids"`
}

func persistEvaluationDecisionLocalReferencesTx(
	ctx context.Context,
	tx pgx.Tx,
	workspaceID, sessionID, decisionID string,
	inputs, outcome map[string]json.RawMessage,
) error {
	reportID, present, err := decisionReferenceID(inputs, "report_id")
	if err != nil || !present {
		return err
	}
	var reviewedClaimKeys, reviewedSectionIDs []string
	if raw, ok := outcome["reviewed_claim_keys"]; ok && string(raw) != "null" {
		if err = json.Unmarshal(raw, &reviewedClaimKeys); err != nil {
			return fmt.Errorf("%w: Decision has invalid reviewed_claim_keys", ErrInvalidContract)
		}
	}
	if raw, ok := outcome["reviewed_section_ids"]; ok && string(raw) != "null" {
		if err = json.Unmarshal(raw, &reviewedSectionIDs); err != nil {
			return fmt.Errorf("%w: Decision has invalid reviewed_section_ids", ErrInvalidContract)
		}
	}
	var defects []evaluationDecisionDefectReferences
	if raw, ok := outcome["defects"]; ok && string(raw) != "null" {
		if err = json.Unmarshal(raw, &defects); err != nil {
			return fmt.Errorf("%w: Decision has invalid defects", ErrInvalidContract)
		}
	}
	for _, claimKey := range reviewedClaimKeys {
		if err = persistEvaluationClaimKeyReferenceTx(
			ctx, tx, workspaceID, sessionID, decisionID, reportID,
			claimKey, "decision_reviewed_claim", 0,
		); err != nil {
			return err
		}
	}
	defectSectionIDs := make([]string, 0)
	for _, defect := range defects {
		for _, claimKey := range defect.ClaimKeys {
			if err = persistEvaluationClaimKeyReferenceTx(
				ctx, tx, workspaceID, sessionID, decisionID, reportID,
				claimKey, "decision_defect_claim", 0,
			); err != nil {
				return err
			}
		}
		defectSectionIDs = append(defectSectionIDs, defect.SectionIDs...)
	}
	if err = validateEvaluationReportSectionKeysTx(
		ctx, tx, workspaceID, sessionID, reportID, reviewedSectionIDs,
	); err != nil {
		return err
	}
	if len(reviewedSectionIDs) > 0 {
		if err = persistTypedArtifactInputReferenceTx(
			ctx, tx, workspaceID, sessionID,
			decisionID, ArtifactKindEvaluationDecision,
			reportID, ArtifactKindReportRevision,
			"decision_reviewed_report_section", "decision_materialization", 0,
		); err != nil {
			return err
		}
	}
	if err = validateEvaluationReportSectionKeysTx(
		ctx, tx, workspaceID, sessionID, reportID, defectSectionIDs,
	); err != nil {
		return err
	}
	if len(defectSectionIDs) > 0 {
		return persistTypedArtifactInputReferenceTx(
			ctx, tx, workspaceID, sessionID,
			decisionID, ArtifactKindEvaluationDecision,
			reportID, ArtifactKindReportRevision,
			"decision_defect_report_section", "decision_materialization", 0,
		)
	}
	return nil
}

func persistEvaluationClaimKeyReferenceTx(
	ctx context.Context,
	tx pgx.Tx,
	workspaceID, sessionID, decisionID, reportID, claimKey, relation string,
	ordinal int,
) error {
	var claimIDs []string
	rows, err := tx.Query(ctx, `
		SELECT DISTINCT claim.id::text
		FROM research_report_claim link
		JOIN research_claim claim
		  ON claim.workspace_id=link.workspace_id AND claim.session_id=link.session_id
		 AND claim.id=link.claim_id
		WHERE link.workspace_id=$1::uuid AND link.session_id=$2::uuid
		  AND link.report_id=$3::uuid AND claim.client_key=$4
		ORDER BY claim.id::text
	`, workspaceID, sessionID, reportID, claimKey)
	if err != nil {
		return err
	}
	for rows.Next() {
		var claimID string
		if err = rows.Scan(&claimID); err != nil {
			rows.Close()
			return err
		}
		claimIDs = append(claimIDs, claimID)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	if len(claimIDs) != 1 {
		return fmt.Errorf(
			"%w: evaluation Claim key %q resolved to %d report Claims",
			ErrInvalidResult, claimKey, len(claimIDs),
		)
	}
	return persistTypedArtifactInputReferenceTx(
		ctx, tx, workspaceID, sessionID,
		decisionID, ArtifactKindEvaluationDecision,
		claimIDs[0], ArtifactKindClaim, relation, "decision_materialization", ordinal,
	)
}

func validateEvaluationReportSectionKeysTx(
	ctx context.Context,
	tx pgx.Tx,
	workspaceID, sessionID, reportID string,
	sectionIDs []string,
) error {
	for _, sectionID := range sectionIDs {
		var matches int
		if err := tx.QueryRow(ctx, `
			SELECT count(*)::int
			FROM research_report report
			CROSS JOIN LATERAL jsonb_array_elements(
			  CASE WHEN jsonb_typeof(report.structured->'sections')='array'
			    THEN report.structured->'sections' ELSE '[]'::jsonb END
			) section
			WHERE report.workspace_id=$1::uuid AND report.session_id=$2::uuid
			  AND report.id=$3::uuid AND section->>'id'=$4
		`, workspaceID, sessionID, reportID, sectionID).Scan(&matches); err != nil {
			return err
		}
		if matches != 1 {
			return fmt.Errorf(
				"%w: evaluation section id %q resolved to %d report sections",
				ErrInvalidResult, sectionID, matches,
			)
		}
	}
	return nil
}

func decisionReferenceID(container map[string]json.RawMessage, field string) (string, bool, error) {
	raw, present := container[field]
	if !present || string(raw) == "null" {
		return "", false, nil
	}
	var artifactID string
	if err := json.Unmarshal(raw, &artifactID); err != nil {
		return "", false, fmt.Errorf("%w: Decision has invalid %s reference", ErrInvalidContract, field)
	}
	artifactID = strings.TrimSpace(artifactID)
	if artifactID == "" {
		return "", false, nil
	}
	return artifactID, true, nil
}

func decisionArtifactContent(
	decisionKind, actorType, actorID string,
	goalVersion, planVersion int,
	inputs, outcome []byte,
	rationale string,
) map[string]any {
	return map[string]any{
		"decision_kind": decisionKind,
		"actor_type":    actorType,
		"actor_id":      actorID,
		"goal_version":  goalVersion,
		"plan_version":  planVersion,
		"inputs":        json.RawMessage(inputs),
		"outcome":       json.RawMessage(outcome),
		"rationale":     rationale,
	}
}

func registerProductionTaskPassportTx(
	ctx context.Context,
	tx pgx.Tx,
	workspaceID, sessionID, taskID, producedByAttemptID string,
	accessLevel ArtifactAccessLevel,
) error {
	var questionID, parentTaskID, clientKey, kind, objective, capability, expected string
	var criteria []byte
	var priority float64
	var goalVersion, planVersion, maxAttempts, timeoutSeconds int32
	var createdAt time.Time
	err := tx.QueryRow(ctx, `
		SELECT COALESCE(question_id::text, ''), COALESCE(parent_task_id::text, ''),
		       client_key, kind, objective, required_capability, expected_result,
		       acceptance_criteria, priority, goal_version, plan_version,
		       max_attempts, timeout_seconds, created_at
		FROM research_task
		WHERE workspace_id = $1::uuid AND session_id = $2::uuid AND id = $3::uuid
	`, workspaceID, sessionID, taskID).Scan(
		&questionID, &parentTaskID, &clientKey, &kind, &objective, &capability, &expected,
		&criteria, &priority, &goalVersion, &planVersion, &maxAttempts, &timeoutSeconds, &createdAt,
	)
	if err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `
		SELECT depends_on_task_id::text
		FROM research_task_dependency
		WHERE workspace_id = $1::uuid AND session_id = $2::uuid AND task_id = $3::uuid
		ORDER BY depends_on_task_id
	`, workspaceID, sessionID, taskID)
	if err != nil {
		return err
	}
	dependencies := make([]string, 0)
	for rows.Next() {
		var dependencyID string
		if err = rows.Scan(&dependencyID); err != nil {
			rows.Close()
			return err
		}
		dependencies = append(dependencies, dependencyID)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	contentHash, err := ArtifactContentHash(ArtifactKindTask, taskArtifactContent(
		questionID, parentTaskID, clientKey, kind, objective, capability, expected,
		criteria, priority, int(goalVersion), int(planVersion), int(maxAttempts), int(timeoutSeconds), dependencies,
	))
	if err != nil {
		return err
	}
	return registerArtifactPassportTx(ctx, tx, registerArtifactPassportInput{
		WorkspaceID: workspaceID, SessionID: sessionID, EntityID: taskID,
		Kind: ArtifactKindTask, SourceCreatedAt: &createdAt,
		ProvenanceCompleteness: ArtifactProvenanceComplete,
		GoalVersion:            &goalVersion, PlanVersion: &planVersion,
		AccessLevel: accessLevel, HashOrigin: ArtifactHashOriginProduction,
		ContentHash: contentHash, ProducedByAttemptID: producedByAttemptID,
	})
}

func taskArtifactContent(
	questionID, parentTaskID, clientKey, kind, objective, capability, expected string,
	criteria []byte,
	priority float64,
	goalVersion, planVersion, maxAttempts, timeoutSeconds int,
	dependencies []string,
) map[string]any {
	return map[string]any{
		"question_id":         questionID,
		"parent_task_id":      parentTaskID,
		"client_key":          clientKey,
		"kind":                kind,
		"objective":           objective,
		"required_capability": capability,
		"expected_result":     expected,
		"acceptance_criteria": json.RawMessage(criteria),
		"priority":            priority,
		"goal_version":        goalVersion,
		"plan_version":        planVersion,
		"max_attempts":        maxAttempts,
		"timeout_seconds":     timeoutSeconds,
		"dependencies":        dependencies,
	}
}

func registerProductionQuestionPassportTx(
	ctx context.Context,
	tx pgx.Tx,
	workspaceID, sessionID, questionID, producedByAttemptID string,
	accessLevel ArtifactAccessLevel,
) error {
	var parentID, createdByTaskID, clientKey, kind, question string
	var required bool
	var priority, impact, uncertainty, novelty, coverage float64
	var goalVersion, planVersion int32
	var createdAt time.Time
	err := tx.QueryRow(ctx, `
		SELECT COALESCE(parent_question_id::text, ''), COALESCE(created_by_task_id::text, ''),
		       client_key, kind, question, required, priority, impact, uncertainty, novelty,
		       coverage, goal_version, plan_version, created_at
		FROM research_question
		WHERE workspace_id = $1::uuid AND session_id = $2::uuid AND id = $3::uuid
	`, workspaceID, sessionID, questionID).Scan(
		&parentID, &createdByTaskID, &clientKey, &kind, &question, &required,
		&priority, &impact, &uncertainty, &novelty, &coverage,
		&goalVersion, &planVersion, &createdAt,
	)
	if err != nil {
		return err
	}
	contentHash, err := ArtifactContentHash(ArtifactKindQuestion, questionArtifactContent(
		parentID, createdByTaskID, clientKey, kind, question, required,
		priority, impact, uncertainty, novelty, coverage, int(goalVersion), int(planVersion),
	))
	if err != nil {
		return err
	}
	return registerArtifactPassportTx(ctx, tx, registerArtifactPassportInput{
		WorkspaceID: workspaceID, SessionID: sessionID, EntityID: questionID,
		Kind: ArtifactKindQuestion, SourceCreatedAt: &createdAt,
		ProvenanceCompleteness: ArtifactProvenanceComplete,
		GoalVersion:            &goalVersion, PlanVersion: &planVersion,
		AccessLevel: accessLevel, HashOrigin: ArtifactHashOriginProduction,
		ContentHash: contentHash, ProducedByAttemptID: producedByAttemptID,
	})
}

func questionArtifactContent(
	parentID, createdByTaskID, clientKey, kind, question string,
	required bool,
	priority, impact, uncertainty, novelty, coverage float64,
	goalVersion, planVersion int,
) map[string]any {
	return map[string]any{
		"parent_question_id": parentID,
		"created_by_task_id": createdByTaskID,
		"client_key":         clientKey,
		"kind":               kind,
		"question":           question,
		"required":           required,
		"priority":           priority,
		"impact":             impact,
		"uncertainty":        uncertainty,
		"novelty":            novelty,
		"coverage":           coverage,
		"goal_version":       goalVersion,
		"plan_version":       planVersion,
	}
}

func registerRunSessionArtifactTx(ctx context.Context, tx pgx.Tx, workspaceID, sessionID string, sourceCreatedAt time.Time) error {
	var persistedJSON string
	if err := tx.QueryRow(ctx, `
		SELECT (
			to_jsonb(session_row) - ARRAY[
				'id', 'workspace_id', 'artifact_passport_enabled'
			]
		)::text
		FROM research_session session_row
		WHERE session_row.workspace_id = $1::uuid AND session_row.id = $2::uuid
	`, workspaceID, sessionID).Scan(&persistedJSON); err != nil {
		return fmt.Errorf("load initialized run session %s: %w", sessionID, err)
	}
	contentHash, err := ArtifactContentHash(
		ArtifactKindRunSession,
		runSessionArtifactContent(json.RawMessage(persistedJSON)),
	)
	if err != nil {
		return err
	}
	return registerArtifactPassportTx(ctx, tx, registerArtifactPassportInput{
		WorkspaceID:            workspaceID,
		SessionID:              sessionID,
		EntityID:               sessionID,
		Kind:                   ArtifactKindRunSession,
		SourceCreatedAt:        &sourceCreatedAt,
		ProvenanceCompleteness: ArtifactProvenanceComplete,
		AccessLevel:            ArtifactAccessRaw,
		HashOrigin:             ArtifactHashOriginProduction,
		ContentHash:            contentHash,
	})
}

func runSessionArtifactContent(persisted json.RawMessage) map[string]any {
	return map[string]any{"persisted": persisted}
}

func registerInitializedRunArtifactsTx(ctx context.Context, tx pgx.Tx, workspaceID, sessionID string) error {
	var directorID, directorAgentID, directorAssignedBy, directorReason string
	var directorVersion int32
	var directorCreatedAt time.Time
	if err := tx.QueryRow(ctx, `SELECT id::text,identity_version,agent_id::text,assigned_by_user_id::text,reason,created_at
		FROM research_director_identity WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND identity_version=1`, workspaceID, sessionID).Scan(
		&directorID, &directorVersion, &directorAgentID, &directorAssignedBy, &directorReason, &directorCreatedAt); err != nil {
		return err
	}
	directorHash, err := ArtifactContentHash(ArtifactKindResearchDirectorIdentity, map[string]any{"identity_version": directorVersion,
		"agent_id": directorAgentID, "assigned_by_user_id": directorAssignedBy, "reason": directorReason})
	if err != nil {
		return err
	}
	if err = registerArtifactPassportTx(ctx, tx, registerArtifactPassportInput{WorkspaceID: workspaceID, SessionID: sessionID, EntityID: directorID,
		Kind: ArtifactKindResearchDirectorIdentity, SourceCreatedAt: &directorCreatedAt, GoalVersion: &directorVersion,
		ProvenanceCompleteness: ArtifactProvenanceComplete, AccessLevel: ArtifactAccessRaw, HashOrigin: ArtifactHashOriginProduction,
		ContentHash: directorHash, SchemaName: string(ArtifactKindResearchDirectorIdentity), SchemaVersion: OrchestratorVersionV6}); err != nil {
		return err
	}
	if err = registerInitialContractRevisionArtifactTx(ctx, tx, workspaceID, sessionID); err != nil {
		return err
	}

	var questionID string
	var questionCreatedAt time.Time
	if err = tx.QueryRow(ctx, `
		SELECT id::text, created_at
		FROM research_question
		WHERE workspace_id = $1::uuid AND session_id = $2::uuid
		  AND goal_version = 1 AND plan_version = 1 AND client_key = 'root'
	`, workspaceID, sessionID).Scan(&questionID, &questionCreatedAt); err != nil {
		return err
	}
	if err = registerProductionQuestionPassportTx(ctx, tx, workspaceID, sessionID, questionID, "", ArtifactAccessRaw); err != nil {
		return err
	}

	var taskID string
	var taskCreatedAt time.Time
	if err = tx.QueryRow(ctx, `
		SELECT id::text, created_at
		FROM research_task
		WHERE workspace_id = $1::uuid AND session_id = $2::uuid
		  AND goal_version = 1 AND plan_version = 1 AND client_key = 'plan:1'
	`, workspaceID, sessionID).Scan(&taskID, &taskCreatedAt); err != nil {
		return err
	}
	return registerProductionTaskPassportTx(ctx, tx, workspaceID, sessionID, taskID, "", ArtifactAccessRaw)
}

func registerInitialContractRevisionArtifactTx(ctx context.Context, tx pgx.Tx, workspaceID, sessionID string) error {
	var contractID string
	var contractCreatedAt time.Time
	var contractGoalVersion int
	var goal, audience, freshness, language, authoredBy, reason string
	var scope, sourcePolicy, runLimits []byte
	err := tx.QueryRow(ctx, `
		SELECT id::text, goal_version, goal, scope, audience, freshness, language,
		       source_policy, run_limits, authored_by::text, reason, created_at
		FROM research_contract_revision
		WHERE workspace_id = $1::uuid AND session_id = $2::uuid AND goal_version = 1
	`, workspaceID, sessionID).Scan(
		&contractID, &contractGoalVersion, &goal, &scope, &audience, &freshness, &language,
		&sourcePolicy, &runLimits, &authoredBy, &reason, &contractCreatedAt,
	)
	if err != nil {
		return err
	}
	contractHash, err := ArtifactContentHash(ArtifactKindContractRevision, contractRevisionArtifactContent(
		contractGoalVersion, goal, scope, audience, freshness, language, sourcePolicy, runLimits, authoredBy, reason,
	))
	if err != nil {
		return err
	}
	goalVersion := int32(1)
	if err = registerArtifactPassportTx(ctx, tx, registerArtifactPassportInput{
		WorkspaceID: workspaceID, SessionID: sessionID, EntityID: contractID,
		Kind: ArtifactKindContractRevision, SourceCreatedAt: &contractCreatedAt,
		GoalVersion:            &goalVersion,
		ProvenanceCompleteness: ArtifactProvenanceComplete,
		AccessLevel:            ArtifactAccessRaw, HashOrigin: ArtifactHashOriginProduction,
		ContentHash: contractHash,
	}); err != nil {
		return err
	}
	return nil
}

func registerV6BranchArtifactTx(
	ctx context.Context,
	tx pgx.Tx,
	workspaceID, sessionID, branchID string,
	createdAt time.Time,
	goalVersion int32,
	content map[string]any,
) error {
	contentHash, err := ArtifactContentHash(ArtifactKindBranch, content)
	if err != nil {
		return err
	}
	return registerArtifactPassportTx(ctx, tx, registerArtifactPassportInput{
		WorkspaceID: workspaceID, SessionID: sessionID, EntityID: branchID,
		Kind: ArtifactKindBranch, SourceCreatedAt: &createdAt, GoalVersion: &goalVersion,
		ProvenanceCompleteness: ArtifactProvenanceComplete,
		AccessLevel:            ArtifactAccessRaw,
		HashOrigin:             ArtifactHashOriginProduction,
		ContentHash:            contentHash,
		SchemaName:             string(ArtifactKindBranch),
		SchemaVersion:          OrchestratorVersionV6,
	})
}

func contractRevisionArtifactContent(
	goalVersion int,
	goal string,
	scope []byte,
	audience, freshness, language string,
	sourcePolicy, runLimits []byte,
	authoredBy, reason string,
) map[string]any {
	return map[string]any{
		"goal_version":  goalVersion,
		"goal":          goal,
		"scope":         json.RawMessage(scope),
		"audience":      audience,
		"freshness":     freshness,
		"language":      language,
		"source_policy": json.RawMessage(sourcePolicy),
		"run_limits":    json.RawMessage(runLimits),
		"authored_by":   authoredBy,
		"reason":        reason,
	}
}

func registerRunArtifactsAfterInitializationTx(ctx context.Context, tx pgx.Tx, workspaceID, sessionID string) error {
	if err := ensureSessionPolicyStateTx(ctx, tx, workspaceID, sessionID); err != nil {
		return err
	}
	var sessionCreatedAt time.Time
	if err := tx.QueryRow(ctx, `
		SELECT created_at FROM research_session WHERE id = $1::uuid AND workspace_id = $2::uuid
	`, sessionID, workspaceID).Scan(&sessionCreatedAt); err != nil {
		return err
	}
	if err := registerRunSessionArtifactTx(ctx, tx, workspaceID, sessionID, sessionCreatedAt); err != nil {
		return err
	}
	if err := registerInitializedRunArtifactsTx(ctx, tx, workspaceID, sessionID); err != nil {
		return err
	}
	var stateVersion int64
	if err := tx.QueryRow(ctx, `
		SELECT state_version
		FROM research_session
		WHERE workspace_id = $1::uuid AND id = $2::uuid
	`, workspaceID, sessionID).Scan(&stateVersion); err != nil {
		return err
	}
	if err := verifyShadowEquivalenceTx(ctx, tx, workspaceID, sessionID, stateVersion, ArtifactPurposeTaskExecution); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `
		UPDATE research_session
		SET artifact_passport_enabled = true
		WHERE workspace_id = $1::uuid AND id = $2::uuid
	`, workspaceID, sessionID)
	return err
}

func artifactKindForDecision(decisionKind string) ArtifactEntityKind {
	if decisionKind == "research_method" {
		return ArtifactKindMethodDecision
	}
	return ArtifactKindEvaluationDecision
}

func int32Ptr(v int32) *int32 {
	return &v
}

func ensureDomainArtifactPassportTx(
	ctx context.Context,
	tx pgx.Tx,
	kind ArtifactEntityKind,
	workspaceID, sessionID, entityID string,
	sourceCreatedAt time.Time,
	goalVersion, planVersion *int32,
) error {
	return ensureDomainArtifactPassportWithAccessTx(ctx, tx, kind, workspaceID, sessionID, entityID, sourceCreatedAt, goalVersion, planVersion, ArtifactAccessRaw)
}

func ensureDomainArtifactPassportWithAccessTx(
	ctx context.Context,
	tx pgx.Tx,
	kind ArtifactEntityKind,
	workspaceID, sessionID, entityID string,
	sourceCreatedAt time.Time,
	goalVersion, planVersion *int32,
	accessLevel ArtifactAccessLevel,
) error {
	return registerArtifactPassportTx(ctx, tx, registerArtifactPassportInput{
		WorkspaceID:     workspaceID,
		SessionID:       sessionID,
		EntityID:        entityID,
		Kind:            kind,
		SourceCreatedAt: &sourceCreatedAt,
		GoalVersion:     goalVersion,
		PlanVersion:     planVersion,
		AccessLevel:     accessLevel,
	})
}

// ensureProducedDomainArtifactPassportWithAccessTx registers a domain artifact
// created by result acceptance from its canonical semantic content. Unlike the
// migration fallback, new production writes must never receive an ID-derived
// placeholder hash: the version must prove both the accepted bytes and the
// Attempt that produced them.
func ensureProducedDomainArtifactPassportWithAccessTx(
	ctx context.Context,
	tx pgx.Tx,
	kind ArtifactEntityKind,
	workspaceID, sessionID, entityID, producedByAttemptID string,
	sourceCreatedAt time.Time,
	goalVersion, planVersion *int32,
	accessLevel ArtifactAccessLevel,
	content map[string]any,
) error {
	contentHash, err := ArtifactContentHash(kind, content)
	if err != nil {
		return err
	}
	return registerArtifactPassportTx(ctx, tx, registerArtifactPassportInput{
		WorkspaceID:            workspaceID,
		SessionID:              sessionID,
		EntityID:               entityID,
		Kind:                   kind,
		SourceCreatedAt:        &sourceCreatedAt,
		GoalVersion:            goalVersion,
		PlanVersion:            planVersion,
		AccessLevel:            accessLevel,
		HashOrigin:             ArtifactHashOriginProduction,
		ContentHash:            contentHash,
		ProducedByAttemptID:    producedByAttemptID,
		ProvenanceCompleteness: ArtifactProvenanceComplete,
	})
}
