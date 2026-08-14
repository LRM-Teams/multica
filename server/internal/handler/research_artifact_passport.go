package handler

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/researchrun"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
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

func ensureResearchMessagePassportTx(ctx context.Context, tx pgx.Tx, workspaceID, sessionID, messageID pgtype.UUID) error {
	return researchrun.RegisterProductionResearchMessageTx(
		ctx, tx, util.UUIDToString(workspaceID), util.UUIDToString(sessionID), util.UUIDToString(messageID),
	)
}

func (h *Handler) createResearchMessageWithPassport(
	ctx context.Context,
	params db.CreateResearchMessageParams,
) (db.ResearchMessage, error) {
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return db.ResearchMessage{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	msg, err := h.Queries.WithTx(tx).CreateResearchMessage(ctx, params)
	if err != nil {
		return db.ResearchMessage{}, err
	}
	if err = ensureResearchMessagePassportTx(ctx, tx, params.WorkspaceID, params.SessionID, msg.ID); err != nil {
		return db.ResearchMessage{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return db.ResearchMessage{}, err
	}
	return msg, nil
}
