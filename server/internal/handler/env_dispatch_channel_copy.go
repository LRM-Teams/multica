package handler

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// channelCopyMap records all IDs that downstream branch-resume work must
// remap. The copy is deliberately DB-only: historical messages must not wake
// agents or publish channel events.
type channelCopyMap struct {
	ChannelID  string
	MessageIDs map[string]string
}

func (h *Handler) copyEnvDispatchChannel(ctx context.Context, workspaceID, sourceChannelID, destinationProjectID, destinationEnvID string) (channelCopyMap, error) {
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return channelCopyMap{}, err
	}
	defer tx.Rollback(ctx)

	var loadedSourceChannelID string
	if err := tx.QueryRow(ctx, `SELECT id::text FROM channel WHERE id = $1 AND workspace_id = $2`, sourceChannelID, workspaceID).Scan(&loadedSourceChannelID); err != nil {
		return channelCopyMap{}, fmt.Errorf("load source env-dispatch channel: %w", err)
	}
	var destinationChannelID string
	if err := tx.QueryRow(ctx, `
		INSERT INTO channel (workspace_id, name, kind, project_id, created_by)
		SELECT workspace_id, 'env-dispatch-' || $3, 'group', $2, created_by
		FROM channel WHERE id = $1
		RETURNING id::text`, sourceChannelID, destinationProjectID, uuid.NewString()).Scan(&destinationChannelID); err != nil {
		return channelCopyMap{}, fmt.Errorf("create copied env-dispatch channel: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO channel_member (
		  channel_id, workspace_id, member_type, member_id, role,
		  added_by_type, added_by_id, join_source, created_at
		)
		SELECT $2::uuid, workspace_id, member_type, member_id, role,
		       added_by_type, added_by_id, join_source, created_at
		FROM channel_member WHERE channel_id = $1::uuid
		ON CONFLICT (channel_id, member_type, member_id) DO UPDATE
		SET role = EXCLUDED.role,
		    added_by_type = EXCLUDED.added_by_type,
		    added_by_id = EXCLUDED.added_by_id,
		    join_source = EXCLUDED.join_source,
		    created_at = EXCLUDED.created_at`,
		sourceChannelID, destinationChannelID); err != nil {
		return channelCopyMap{}, fmt.Errorf("copy channel members: %w", err)
	}
	// Fail-closed: ordinary group must have ≥1 human owner after copy (Ronan B1/B2).
	var createdBy, workspaceUUID pgtype.UUID
	if err := tx.QueryRow(ctx, `
		SELECT workspace_id, created_by FROM channel WHERE id = $1::uuid`,
		destinationChannelID).Scan(&workspaceUUID, &createdBy); err != nil {
		return channelCopyMap{}, fmt.Errorf("load copied channel identity: %w", err)
	}
	if err := ensureOrdinaryGroupHumanOwnerTx(ctx, tx, destinationChannelID, workspaceUUID, createdBy); err != nil {
		return channelCopyMap{}, fmt.Errorf("ensure copied channel human owner: %w", err)
	}

	if _, err := tx.Exec(ctx, `CREATE TEMP TABLE env_dispatch_channel_message_map (source_id uuid PRIMARY KEY, destination_id uuid NOT NULL) ON COMMIT DROP`); err != nil {
		return channelCopyMap{}, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO env_dispatch_channel_message_map (source_id, destination_id)
		SELECT id, gen_random_uuid() FROM channel_message WHERE channel_id = $1`, sourceChannelID); err != nil {
		return channelCopyMap{}, fmt.Errorf("map channel messages: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO channel_message (
			id, channel_id, workspace_id, author_type, author_id, author_name,
			content, parts, source, external_message_id, client_message_id,
			reply_to_message_id, quote_message_id, quote_snapshot,
			thread_root_message_id, thread_id, trigger_depth,
			created_at, edited_at, deleted_at
		)
		SELECT map.destination_id, $2, m.workspace_id, m.author_type, m.author_id,
			m.author_name, m.content, m.parts, m.source, m.external_message_id,
			m.client_message_id, NULL, NULL, m.quote_snapshot, NULL, m.thread_id,
			m.trigger_depth, m.created_at, m.edited_at, m.deleted_at
		FROM channel_message m
		JOIN env_dispatch_channel_message_map map ON map.source_id = m.id
		WHERE m.channel_id = $1
		ORDER BY m.created_at, m.id`, sourceChannelID, destinationChannelID); err != nil {
		return channelCopyMap{}, fmt.Errorf("copy channel messages: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE channel_message dst
		SET reply_to_message_id = reply_map.destination_id,
			quote_message_id = quote_map.destination_id,
			thread_root_message_id = root_map.destination_id
		FROM channel_message src
		JOIN env_dispatch_channel_message_map self_map ON self_map.source_id = src.id
		LEFT JOIN env_dispatch_channel_message_map reply_map ON reply_map.source_id = src.reply_to_message_id
		LEFT JOIN env_dispatch_channel_message_map quote_map ON quote_map.source_id = src.quote_message_id
		LEFT JOIN env_dispatch_channel_message_map root_map ON root_map.source_id = src.thread_root_message_id
		WHERE dst.id = self_map.destination_id`); err != nil {
		return channelCopyMap{}, fmt.Errorf("remap copied channel message links: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO channel_read (channel_id, user_id, last_read_at, last_read_seq)
		SELECT $2, user_id, last_read_at, last_read_seq
		FROM channel_read WHERE channel_id = $1`, sourceChannelID, destinationChannelID); err != nil {
		return channelCopyMap{}, fmt.Errorf("copy channel read state: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO channel_thread_state (channel_id, root_message_id, user_id, last_read_at, followed_at, last_read_seq, created_at, updated_at)
		SELECT $2, map.destination_id, state.user_id, state.last_read_at, state.followed_at,
			state.last_read_seq, state.created_at, state.updated_at
		FROM channel_thread_state state
		JOIN env_dispatch_channel_message_map map ON map.source_id = state.root_message_id
		WHERE state.channel_id = $1`, sourceChannelID, destinationChannelID); err != nil {
		return channelCopyMap{}, fmt.Errorf("copy channel thread state: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO thread_participant (conversation_id, root_message_id, member_type, member_id, role, wake_state, last_read_seq, followed_at, created_at, updated_at)
		SELECT $2, map.destination_id, participant.member_type, participant.member_id,
			participant.role, participant.wake_state, participant.last_read_seq,
			participant.followed_at, participant.created_at, participant.updated_at
		FROM thread_participant participant
		JOIN env_dispatch_channel_message_map map ON map.source_id = participant.root_message_id
		WHERE participant.conversation_id = $1`, sourceChannelID, destinationChannelID); err != nil {
		return channelCopyMap{}, fmt.Errorf("copy thread participants: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO environment_agent_sandbox (
			env_id, channel_id, agent_id, source_agent_id, model_config_owner_agent_id, status, source_sandbox_instance_id, sandbox_config
		)
		SELECT $2, $3, agent_id, agent_id, agent_id, 'pending',
			CASE WHEN status = 'ready' THEN sandbox_instance_id ELSE source_sandbox_instance_id END,
			sandbox_config
		FROM environment_agent_sandbox WHERE channel_id = $1`, sourceChannelID, destinationEnvID, destinationChannelID); err != nil {
		return channelCopyMap{}, fmt.Errorf("copy env-dispatch bindings: %w", err)
	}

	rows, err := tx.Query(ctx, `SELECT source_id::text, destination_id::text FROM env_dispatch_channel_message_map`)
	if err != nil {
		return channelCopyMap{}, err
	}
	defer rows.Close()
	result := channelCopyMap{ChannelID: destinationChannelID, MessageIDs: map[string]string{}}
	for rows.Next() {
		var sourceID, destinationID string
		if err := rows.Scan(&sourceID, &destinationID); err != nil {
			return channelCopyMap{}, err
		}
		result.MessageIDs[sourceID] = destinationID
	}
	if err := rows.Err(); err != nil {
		return channelCopyMap{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return channelCopyMap{}, err
	}
	return result, nil
}
