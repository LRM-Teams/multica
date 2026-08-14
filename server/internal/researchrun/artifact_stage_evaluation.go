package researchrun

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// RegisterProductionStageEvaluationTx registers the persisted evaluation row.
// ArtifactKindStageEvaluation places it in the evaluation-private compartment;
// its normal access field remains raw and does not authorize ordinary readers.
func RegisterProductionStageEvaluationTx(
	ctx context.Context,
	tx pgx.Tx,
	workspaceID, sessionID, evaluationID string,
) error {
	var persistedJSON string
	var createdAt time.Time
	if err := tx.QueryRow(ctx, `
		SELECT (to_jsonb(evaluation) - ARRAY['id', 'workspace_id', 'session_id'])::text,
		       evaluation.created_at
		FROM research_stage_eval evaluation
		WHERE evaluation.workspace_id = $1::uuid
		  AND evaluation.session_id = $2::uuid
		  AND evaluation.id = $3::uuid
	`, workspaceID, sessionID, evaluationID).Scan(&persistedJSON, &createdAt); err != nil {
		return fmt.Errorf("load persisted stage evaluation %s: %w", evaluationID, err)
	}

	contentHash, err := ArtifactContentHash(
		ArtifactKindStageEvaluation,
		stageEvaluationArtifactContent(json.RawMessage(persistedJSON)),
	)
	if err != nil {
		return err
	}
	return registerArtifactPassportTx(ctx, tx, registerArtifactPassportInput{
		WorkspaceID:            workspaceID,
		SessionID:              sessionID,
		EntityID:               evaluationID,
		Kind:                   ArtifactKindStageEvaluation,
		SourceCreatedAt:        &createdAt,
		ProvenanceCompleteness: ArtifactProvenanceComplete,
		AccessLevel:            ArtifactAccessRaw,
		HashOrigin:             ArtifactHashOriginProduction,
		ContentHash:            contentHash,
	})
}

func stageEvaluationArtifactContent(persisted json.RawMessage) map[string]any {
	return map[string]any{"persisted": persisted}
}
