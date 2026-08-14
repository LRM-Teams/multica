package handler

import (
	"context"
	"encoding/json"

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

func (h *Handler) setResearchMessageMatchDecisionWithPassport(
	ctx context.Context,
	workspaceID, sessionID, messageID pgtype.UUID,
	payload json.RawMessage,
) (db.ResearchMessage, error) {
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return db.ResearchMessage{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = researchrun.AdvanceProductionResearchMessageMatchDecisionTx(
		ctx, tx, uuidToString(workspaceID), uuidToString(sessionID), uuidToString(messageID), payload,
	); err != nil {
		return db.ResearchMessage{}, err
	}
	msg, err := h.Queries.WithTx(tx).GetResearchMessage(ctx, db.GetResearchMessageParams{
		ID: messageID, SessionID: sessionID, WorkspaceID: workspaceID,
	})
	if err != nil {
		return db.ResearchMessage{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return db.ResearchMessage{}, err
	}
	return msg, nil
}

func ensureGraphNodePassportTx(ctx context.Context, tx pgx.Tx, workspaceID, sessionID, nodeID pgtype.UUID) error {
	return researchrun.RegisterProductionGraphNodeTx(
		ctx,
		tx,
		uuidToString(workspaceID),
		uuidToString(sessionID),
		uuidToString(nodeID),
	)
}

func ensureGraphEdgePassportTx(ctx context.Context, tx pgx.Tx, workspaceID, sessionID, edgeID pgtype.UUID) error {
	return researchrun.RegisterProductionGraphEdgeTx(
		ctx,
		tx,
		uuidToString(workspaceID),
		uuidToString(sessionID),
		uuidToString(edgeID),
	)
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
