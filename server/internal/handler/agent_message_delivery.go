package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/daemonws"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func (h *Handler) HandleAgentDeliveryAck(ctx context.Context, identity daemonws.ClientIdentity, ack protocol.AgentDeliverAckPayload) error {
	if strings.TrimSpace(ack.AgentID) == "" || strings.TrimSpace(ack.DeliveryID) == "" || ack.Seq <= 0 {
		return errors.New("invalid agent delivery acknowledgement")
	}
	if err := h.requireAgentMessageDaemonScope(ctx, identity, ack.AgentID); err != nil {
		return err
	}
	messageID, ok := canonicalDeliveryMessageID(ack.DeliveryID, ack.AgentID)
	if !ok {
		return errors.New("invalid canonical delivery acknowledgement")
	}
	if h.TxStarter == nil {
		return errors.New("agent delivery acknowledgement transaction unavailable")
	}
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin agent delivery acknowledgement: %w", err)
	}
	defer tx.Rollback(ctx)
	command, err := tx.Exec(ctx, `
		UPDATE agent_message_delivery
		SET acked_at = COALESCE(acked_at, now())
		WHERE workspace_id = $1 AND agent_id = $2 AND message_id = $3 AND seq = $4`,
		parseUUID(identity.WorkspaceID), parseUUID(ack.AgentID), parseUUID(messageID), ack.Seq)
	if err != nil {
		return fmt.Errorf("persist agent delivery acknowledgement: %w", err)
	}
	if command.RowsAffected() != 1 {
		return errors.New("unknown agent delivery acknowledgement")
	}
	if _, err := service.SettleDeliveryObligationForExecutionAgent(ctx, tx, parseUUID(messageID), parseUUID(ack.AgentID)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func canonicalDeliveryMessageID(deliveryID, agentID string) (string, bool) {
	const prefix = "message:"
	suffix := ":agent:" + agentID
	if !strings.HasPrefix(deliveryID, prefix) || !strings.HasSuffix(deliveryID, suffix) {
		return "", false
	}
	messageID := strings.TrimSuffix(strings.TrimPrefix(deliveryID, prefix), suffix)
	if _, err := util.ParseUUID(messageID); err != nil {
		return "", false
	}
	return messageID, true
}

func (h *Handler) HandleAgentMessageHandoff(ctx context.Context, identity daemonws.ClientIdentity, payload protocol.AgentMessageHandoffPayload) error {
	if strings.TrimSpace(payload.AgentID) == "" || strings.TrimSpace(payload.RuntimeID) == "" || strings.TrimSpace(payload.HandoffID) == "" || payload.Count <= 0 {
		return errors.New("invalid agent Message handoff")
	}
	if err := h.requireAgentMessageDaemonScope(ctx, identity, payload.AgentID); err != nil {
		return err
	}
	var boundRuntimeID pgtype.UUID
	if err := h.DB.QueryRow(ctx, `SELECT runtime_id FROM agent WHERE id = $1 AND workspace_id = $2`, parseUUID(payload.AgentID), parseUUID(identity.WorkspaceID)).Scan(&boundRuntimeID); err != nil {
		return err
	}
	if uuidToString(boundRuntimeID) != payload.RuntimeID {
		return errors.New("agent Message handoff runtime mismatch")
	}
	if h.TxStarter == nil {
		return errors.New("agent Message handoff transaction unavailable")
	}
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('agent_message_handoff'), hashtext($1))`, payload.HandoffID); err != nil {
		return err
	}
	targets, err := json.Marshal(payload.Targets)
	if err != nil {
		return fmt.Errorf("encode Message handoff targets: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO agent_message_handoff_receipt (workspace_id, agent_id, handoff_id, message_count, targets)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (workspace_id, agent_id, handoff_id) DO NOTHING`,
		parseUUID(identity.WorkspaceID), parseUUID(payload.AgentID), payload.HandoffID, payload.Count, targets); err != nil {
		return fmt.Errorf("persist Message handoff receipt: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	return nil
}

func (h *Handler) requireAgentMessageDaemonScope(ctx context.Context, identity daemonws.ClientIdentity, agentID string) error {
	agentUUID, err := util.ParseUUID(agentID)
	if err != nil {
		return errors.New("invalid agent id")
	}
	var workspaceID, runtimeID pgtype.UUID
	var daemonID *string
	if err := h.DB.QueryRow(ctx, `
		SELECT agent.workspace_id, agent.runtime_id, runtime.daemon_id
		FROM agent
		LEFT JOIN agent_runtime runtime ON runtime.id = agent.runtime_id
		WHERE agent.id = $1 AND agent.archived_at IS NULL`, agentUUID).Scan(&workspaceID, &runtimeID, &daemonID); err != nil {
		return err
	}
	if uuidToString(workspaceID) != identity.WorkspaceID {
		return errors.New("agent delivery workspace mismatch")
	}
	if daemonID == nil || *daemonID == "" || identity.DaemonID == "" || *daemonID != identity.DaemonID {
		return errors.New("agent delivery daemon mismatch")
	}
	return nil
}
