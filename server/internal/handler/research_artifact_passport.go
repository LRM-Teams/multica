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

func (h *Handler) createResearchMessageWithPassportAndV6Steering(
	ctx context.Context,
	params db.CreateResearchMessageParams,
	clientRequestID string,
	selectedRefs json.RawMessage,
) (db.ResearchMessage, error) {
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return db.ResearchMessage{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var orchestrator string
	if err = tx.QueryRow(ctx, `SELECT orchestrator_version FROM research_session WHERE workspace_id=$1 AND id=$2`, params.WorkspaceID, params.SessionID).Scan(&orchestrator); err != nil {
		return db.ResearchMessage{}, err
	}
	if orchestrator == researchrun.OrchestratorVersionV6 {
		if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, util.UUIDToString(params.WorkspaceID)+":"+util.UUIDToString(params.SessionID)+":"+clientRequestID); err != nil {
			return db.ResearchMessage{}, err
		}
		var existingID string
		err = tx.QueryRow(ctx, `SELECT t.research_message_id::text
			FROM research_v6_steering_trigger t
			JOIN research_message m ON m.workspace_id=t.workspace_id AND m.session_id=t.session_id AND m.id=t.research_message_id
			WHERE t.workspace_id=$1 AND t.session_id=$2 AND t.client_request_id=$3::uuid
			AND m.sender_type=$4 AND m.sender_id=$5 AND m.target_agent_id IS NOT DISTINCT FROM $6
			AND m.body=$7 AND t.selected_refs=$8::jsonb`, params.WorkspaceID, params.SessionID, clientRequestID,
			params.SenderType, params.SenderID, params.TargetAgentID, params.Body, selectedRefs).Scan(&existingID)
		if err == nil {
			return h.Queries.WithTx(tx).GetResearchMessage(ctx, db.GetResearchMessageParams{
				ID: parseUUID(existingID), SessionID: params.SessionID, WorkspaceID: params.WorkspaceID,
			})
		}
		if err != pgx.ErrNoRows {
			return db.ResearchMessage{}, err
		}
		var requestIDUsed bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM research_v6_steering_trigger WHERE workspace_id=$1 AND session_id=$2 AND client_request_id=$3::uuid)`, params.WorkspaceID, params.SessionID, clientRequestID).Scan(&requestIDUsed); err != nil {
			return db.ResearchMessage{}, err
		}
		if requestIDUsed {
			return db.ResearchMessage{}, researchrun.ErrResultConflict
		}
	}
	msg, err := h.Queries.WithTx(tx).CreateResearchMessage(ctx, params)
	if err != nil {
		return db.ResearchMessage{}, err
	}
	if err = ensureResearchMessagePassportTx(ctx, tx, params.WorkspaceID, params.SessionID, msg.ID); err != nil {
		return db.ResearchMessage{}, err
	}
	if err = researchrun.QueueV6SteeringMessageTx(ctx, tx, util.UUIDToString(params.WorkspaceID), util.UUIDToString(params.SessionID),
		util.UUIDToString(msg.ID), clientRequestID, selectedRefs); err != nil {
		return db.ResearchMessage{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return db.ResearchMessage{}, err
	}
	return msg, nil
}
