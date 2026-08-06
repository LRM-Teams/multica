package handler

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5/pgtype"
)

type channelDecisionAuditEvent struct {
	WorkspaceID  pgtype.UUID
	ChannelID    pgtype.UUID
	SourceKind   string
	SourceID     pgtype.UUID
	EventType    string
	AgentID      pgtype.UUID
	MessageID    pgtype.UUID
	InboxEventID pgtype.UUID
	Payload      map[string]any
}

func recordChannelDecisionAuditExec(ctx context.Context, exec dbExecutor, event channelDecisionAuditEvent) error {
	payload := event.Payload
	if payload == nil {
		payload = map[string]any{}
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = exec.Exec(ctx, `
		INSERT INTO channel_decision_audit (
		  workspace_id, channel_id, source_kind, source_id, event_type,
		  agent_id, message_id, inbox_event_id, payload
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9::jsonb)`, event.WorkspaceID,
		nullableUUID(event.ChannelID), event.SourceKind, nullableUUID(event.SourceID), event.EventType,
		nullableUUID(event.AgentID), nullableUUID(event.MessageID), nullableUUID(event.InboxEventID), raw)
	return err
}
