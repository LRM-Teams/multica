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

func reportRevisionArtifactContent(persisted json.RawMessage) map[string]any {
	return map[string]any{"persisted": persisted}
}
