package handler

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// createOrdinaryGroupWithOwnerTx inserts a kind=group channel (system_key NULL)
// and exactly one human owner membership in the same transaction.
//
// All ordinary-group creation entry points MUST use this (or call
// insertChannelHumanOwnerTx after their own channel INSERT). Do not open-code
// channel_member inserts with default role=member for group creators.
func createOrdinaryGroupWithOwnerTx(
	ctx context.Context,
	tx pgx.Tx,
	workspaceID, createdBy pgtype.UUID,
	name string,
	description, larkChatID *string,
	projectID pgtype.UUID,
) (channelID string, err error) {
	if err := tx.QueryRow(ctx, `
		INSERT INTO channel (workspace_id, name, description, lark_chat_id, project_id, created_by, kind)
		VALUES ($1, $2, $3, $4, $5, $6, 'group')
		RETURNING id::text`,
		workspaceID, name, description, larkChatID, projectID, createdBy,
	).Scan(&channelID); err != nil {
		return "", err
	}
	if err := insertChannelHumanOwnerTx(ctx, tx, channelID, workspaceID, createdBy); err != nil {
		return "", err
	}
	return channelID, nil
}

// insertChannelHumanOwnerTx inserts the sole human owner membership for a channel.
// Idempotent: migration 237/239 may already auto-seed created_by as owner on
// ordinary group INSERT; ON CONFLICT re-asserts role=owner.
func insertChannelHumanOwnerTx(ctx context.Context, tx pgx.Tx, channelID string, workspaceID, userID pgtype.UUID) error {
	actor := channelMemberUserActor(userID)
	if err := validateChannelMemberActorWithExec(ctx, tx, uuidToString(workspaceID), actor); err != nil {
		return fmt.Errorf("validate channel owner actor: %w", err)
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO channel_member (
		  channel_id, workspace_id, member_type, member_id, role,
		  added_by_type, added_by_id, join_source
		)
		VALUES ($1::uuid, $2, 'user', $3, 'owner', $4, $5, 'manual')
		ON CONFLICT (channel_id, member_type, member_id) DO UPDATE
		SET role = 'owner',
		    added_by_type = EXCLUDED.added_by_type,
		    added_by_id = EXCLUDED.added_by_id`,
		channelID, workspaceID, userID, actor.Type, actor.ID)
	if err != nil {
		return fmt.Errorf("insert channel owner: %w", err)
	}
	return nil
}

// ensureOrdinaryGroupHumanOwnerTx fails closed unless ≥1 human owner exists.
// Prefer promoting created_by; insert if that user is not yet a member.
func ensureOrdinaryGroupHumanOwnerTx(ctx context.Context, tx pgx.Tx, channelID string, workspaceID, createdBy pgtype.UUID) error {
	var ownerCount int
	if err := tx.QueryRow(ctx, `
		SELECT count(*) FROM channel_member
		WHERE channel_id = $1::uuid AND role = 'owner' AND member_type = 'user'`,
		channelID).Scan(&ownerCount); err != nil {
		return err
	}
	if ownerCount > 0 {
		return nil
	}
	tag, err := tx.Exec(ctx, `
		UPDATE channel_member
		SET role = 'owner'
		WHERE channel_id = $1::uuid AND member_type = 'user' AND member_id = $2`,
		channelID, createdBy)
	if err != nil {
		return fmt.Errorf("promote created_by to owner: %w", err)
	}
	if tag.RowsAffected() == 0 {
		if err := insertChannelHumanOwnerTx(ctx, tx, channelID, workspaceID, createdBy); err != nil {
			return fmt.Errorf("insert created_by as owner: %w", err)
		}
	}
	if err := tx.QueryRow(ctx, `
		SELECT count(*) FROM channel_member
		WHERE channel_id = $1::uuid AND role = 'owner' AND member_type = 'user'`,
		channelID).Scan(&ownerCount); err != nil {
		return err
	}
	if ownerCount == 0 {
		return fmt.Errorf("ordinary group %s still has zero human owners after repair", channelID)
	}
	return nil
}
