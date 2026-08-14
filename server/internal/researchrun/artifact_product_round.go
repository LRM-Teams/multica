package researchrun

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// RegisterProductionProductRoundDecisionTx registers a Product Round judgment
// from the row that was actually persisted. It must run in the same transaction
// as card creation so the reciprocal artifact guard observes one atomic write.
func RegisterProductionProductRoundDecisionTx(
	ctx context.Context,
	tx pgx.Tx,
	workspaceID, sessionID, cardID string,
) error {
	var persistedJSON string
	var createdAt time.Time
	if err := tx.QueryRow(ctx, `
		SELECT (to_jsonb(card) - ARRAY['id', 'workspace_id', 'session_id'])::text,
		       card.created_at
		FROM research_product_round_card card
		WHERE card.workspace_id = $1::uuid
		  AND card.session_id = $2::uuid
		  AND card.id = $3::uuid
	`, workspaceID, sessionID, cardID).Scan(&persistedJSON, &createdAt); err != nil {
		return fmt.Errorf("load persisted product round decision %s: %w", cardID, err)
	}

	contentHash, err := ArtifactContentHash(
		ArtifactKindProductRoundDecision,
		productRoundDecisionArtifactContent(json.RawMessage(persistedJSON)),
	)
	if err != nil {
		return err
	}
	return registerArtifactPassportTx(ctx, tx, registerArtifactPassportInput{
		WorkspaceID:            workspaceID,
		SessionID:              sessionID,
		EntityID:               cardID,
		Kind:                   ArtifactKindProductRoundDecision,
		SourceCreatedAt:        &createdAt,
		ProvenanceCompleteness: ArtifactProvenanceComplete,
		AccessLevel:            ArtifactAccessRaw,
		HashOrigin:             ArtifactHashOriginProduction,
		ContentHash:            contentHash,
	})
}

func productRoundDecisionArtifactContent(persisted json.RawMessage) map[string]any {
	return map[string]any{"persisted": persisted}
}
