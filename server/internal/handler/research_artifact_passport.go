package handler

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func backfillArtifactPassportTx(
	ctx context.Context,
	tx pgx.Tx,
	workspaceID, sessionID, entityID pgtype.UUID,
	kind string,
) error {
	_, err := tx.Exec(ctx, `
		SELECT research_artifact_backfill_registered(
			$1::uuid, $2::uuid, $3::uuid, $4, now(), NULL, NULL
		)
	`, workspaceID, sessionID, entityID, kind)
	return err
}

func ensureGraphNodePassportTx(ctx context.Context, tx pgx.Tx, workspaceID, sessionID, nodeID pgtype.UUID) error {
	return backfillArtifactPassportTx(ctx, tx, workspaceID, sessionID, nodeID, "graph_node")
}

func ensureGraphEdgePassportTx(ctx context.Context, tx pgx.Tx, workspaceID, sessionID, edgeID pgtype.UUID) error {
	return backfillArtifactPassportTx(ctx, tx, workspaceID, sessionID, edgeID, "graph_edge")
}
