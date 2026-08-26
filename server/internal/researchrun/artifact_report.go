package researchrun

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// RegisterProductionReportRevisionTx registers a persisted Report Revision.
// Legacy non-durable sessions do not have an Attempt, so their authenticated
// Agent author is stored on the report and included in the production hash.
func RegisterProductionReportRevisionTx(
	ctx context.Context,
	tx pgx.Tx,
	workspaceID, sessionID, reportID string,
) error {
	var persistedJSON string
	var createdAt time.Time
	var goalVersion, planVersion int32
	if err := tx.QueryRow(ctx, `
		SELECT (to_jsonb(report) - ARRAY['id', 'workspace_id', 'session_id'])::text,
		       report.created_at, report.goal_version, report.plan_version
		FROM research_report report
		WHERE report.workspace_id = $1::uuid
		  AND report.session_id = $2::uuid
		  AND report.id = $3::uuid
	`, workspaceID, sessionID, reportID).Scan(
		&persistedJSON,
		&createdAt,
		&goalVersion,
		&planVersion,
	); err != nil {
		return fmt.Errorf("load persisted report revision %s: %w", reportID, err)
	}

	contentHash, err := ArtifactContentHash(
		ArtifactKindReportRevision,
		reportRevisionArtifactContent(json.RawMessage(persistedJSON)),
	)
	if err != nil {
		return err
	}
	return registerArtifactPassportTx(ctx, tx, registerArtifactPassportInput{
		WorkspaceID:            workspaceID,
		SessionID:              sessionID,
		EntityID:               reportID,
		Kind:                   ArtifactKindReportRevision,
		SourceCreatedAt:        &createdAt,
		ProvenanceCompleteness: ArtifactProvenanceComplete,
		GoalVersion:            &goalVersion,
		PlanVersion:            &planVersion,
		AccessLevel:            ArtifactAccessRaw,
		HashOrigin:             ArtifactHashOriginProduction,
		ContentHash:            contentHash,
	})
}

// registerDraftReportRevisionPassportTx creates the reciprocal passport shell
// required while a report revision is still waiting for its immutable package.
// Package acceptance fills version 1 and makes it current.
func registerDraftReportRevisionPassportTx(
	ctx context.Context,
	tx pgx.Tx,
	workspaceID, sessionID, reportID string,
) error {
	if err := ensureSessionPolicyStateTx(ctx, tx, workspaceID, sessionID); err != nil {
		return err
	}
	command, err := tx.Exec(ctx, `
		INSERT INTO research_artifact_passport (
			id, workspace_id, session_id, entity_kind, current_version,
			lifecycle_status, provenance_completeness, source_created_at
		)
		SELECT id, workspace_id, session_id, 'report_revision', NULL,
		       'registered', 'complete', created_at
		FROM research_report
		WHERE workspace_id = $1::uuid
		  AND session_id = $2::uuid
		  AND id = $3::uuid
		ON CONFLICT (workspace_id, session_id, id) DO NOTHING
	`, workspaceID, sessionID, reportID)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		var kind string
		var currentVersion *int32
		if err = tx.QueryRow(ctx, `
		SELECT entity_kind, current_version
		FROM research_artifact_passport
		WHERE workspace_id = $1::uuid
		  AND session_id = $2::uuid
		  AND id = $3::uuid
	`, workspaceID, sessionID, reportID).Scan(&kind, &currentVersion); err != nil {
			return err
		}
		if kind != string(ArtifactKindReportRevision) || currentVersion != nil {
			return ErrResultConflict
		}
	}
	_, err = tx.Exec(ctx, `
		SELECT research_artifact_record_artifact_create_mutation(
			$1::uuid, $2::uuid, $3::uuid
		)
	`, workspaceID, sessionID, reportID)
	return err
}

func reportRevisionArtifactContent(persisted json.RawMessage) map[string]any {
	return map[string]any{"persisted": persisted}
}
