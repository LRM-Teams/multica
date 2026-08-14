package researchrun

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// RegisterProductionGraphNodeTx registers a newly persisted graph node from
// the complete database row. The caller must invoke it in the transaction that
// inserted the node so the reciprocal passport guard cannot observe a partial
// write.
func RegisterProductionGraphNodeTx(
	ctx context.Context,
	tx pgx.Tx,
	workspaceID, sessionID, nodeID string,
) error {
	return registerProductionGraphEntityTx(
		ctx,
		tx,
		workspaceID,
		sessionID,
		nodeID,
		ArtifactKindGraphNode,
		`SELECT (to_jsonb(n) - ARRAY['id', 'workspace_id', 'session_id'])::text, n.created_at
		 FROM research_graph_node n
		 WHERE n.workspace_id = $1::uuid AND n.session_id = $2::uuid AND n.id = $3::uuid`,
	)
}

// RegisterProductionGraphEdgeTx registers a newly persisted graph edge from
// its immutable database row in the creator transaction.
func RegisterProductionGraphEdgeTx(
	ctx context.Context,
	tx pgx.Tx,
	workspaceID, sessionID, edgeID string,
) error {
	return registerProductionGraphEntityTx(
		ctx,
		tx,
		workspaceID,
		sessionID,
		edgeID,
		ArtifactKindGraphEdge,
		`SELECT (to_jsonb(e) - ARRAY['id', 'workspace_id', 'session_id'])::text, e.created_at
		 FROM research_graph_edge e
		 WHERE e.workspace_id = $1::uuid AND e.session_id = $2::uuid AND e.id = $3::uuid`,
	)
}

func registerProductionGraphEntityTx(
	ctx context.Context,
	tx pgx.Tx,
	workspaceID, sessionID, entityID string,
	kind ArtifactEntityKind,
	query string,
) error {
	var persistedJSON string
	var createdAt time.Time
	if err := tx.QueryRow(ctx, query, workspaceID, sessionID, entityID).Scan(&persistedJSON, &createdAt); err != nil {
		return fmt.Errorf("load persisted %s %s: %w", kind, entityID, err)
	}

	contentHash, err := ArtifactContentHash(kind, graphArtifactContent(json.RawMessage(persistedJSON)))
	if err != nil {
		return err
	}
	return registerArtifactPassportTx(ctx, tx, registerArtifactPassportInput{
		WorkspaceID:            workspaceID,
		SessionID:              sessionID,
		EntityID:               entityID,
		Kind:                   kind,
		SourceCreatedAt:        &createdAt,
		ProvenanceCompleteness: ArtifactProvenanceComplete,
		AccessLevel:            ArtifactAccessRaw,
		HashOrigin:             ArtifactHashOriginProduction,
		ContentHash:            contentHash,
	})
}

func graphArtifactContent(persisted json.RawMessage) map[string]any {
	return map[string]any{"persisted": persisted}
}
