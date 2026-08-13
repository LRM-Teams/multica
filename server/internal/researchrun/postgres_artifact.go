package researchrun

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

type registerArtifactPassportInput struct {
	WorkspaceID            string
	SessionID              string
	EntityID               string
	Kind                   ArtifactEntityKind
	SourceCreatedAt        *time.Time
	ProvenanceCompleteness ArtifactProvenanceCompleteness
	GoalVersion            *int32
	PlanVersion            *int32
	SchemaName             string
	SchemaVersion          string
	AccessLevel            ArtifactAccessLevel
	HashOrigin             ArtifactHashOrigin
	ContentHash            string
	ProducedByAttemptID    string
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
		VALUES ($1::uuid, $2::uuid, $3, 0)
		ON CONFLICT (workspace_id, session_id) DO NOTHING
	`, workspaceID, sessionID, LegacyV1V5CompatPolicy)
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
				  AND mutation.mutation_kind = 'verification'
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
				hash_origin, produced_by_attempt_id
			) VALUES (
				$1::uuid, $2::uuid, $3::uuid, 1, $4, $5,
				$6, $7, $8, $9, $10, $11, NULLIF($12, '')::uuid
			)
		`, in.WorkspaceID, in.SessionID, in.EntityID, in.SchemaName, in.SchemaVersion,
			ArtifactCanonicalizationVersion, contentHash, string(in.AccessLevel),
			in.GoalVersion, in.PlanVersion, string(in.HashOrigin), in.ProducedByAttemptID); err != nil {
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
	return registerArtifactPassportTx(ctx, tx, registerArtifactPassportInput{
		WorkspaceID:            workspaceID,
		SessionID:              sessionID,
		EntityID:               sessionID,
		Kind:                   ArtifactKindRunSession,
		SourceCreatedAt:        &sourceCreatedAt,
		ProvenanceCompleteness: ArtifactProvenancePartial,
	})
}

func registerInitializedRunArtifactsTx(ctx context.Context, tx pgx.Tx, workspaceID, sessionID string) error {
	var contractID string
	var contractCreatedAt time.Time
	err := tx.QueryRow(ctx, `
		SELECT id::text, created_at
		FROM research_contract_revision
		WHERE workspace_id = $1::uuid AND session_id = $2::uuid AND goal_version = 1
	`, workspaceID, sessionID).Scan(&contractID, &contractCreatedAt)
	if err != nil {
		return err
	}
	goalVersion := int32(1)
	planVersion := int32(1)
	if err = registerArtifactPassportTx(ctx, tx, registerArtifactPassportInput{
		WorkspaceID: workspaceID, SessionID: sessionID, EntityID: contractID,
		Kind: ArtifactKindContractRevision, SourceCreatedAt: &contractCreatedAt,
		GoalVersion: &goalVersion,
	}); err != nil {
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
	return registerArtifactPassportTx(ctx, tx, registerArtifactPassportInput{
		WorkspaceID: workspaceID, SessionID: sessionID, EntityID: taskID,
		Kind: ArtifactKindTask, SourceCreatedAt: &taskCreatedAt,
		GoalVersion: &goalVersion, PlanVersion: &planVersion,
	})
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
	if err := verifyShadowEquivalenceTx(ctx, tx, workspaceID, sessionID, stateVersion); err != nil {
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
