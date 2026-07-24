package handler

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

// ensureGroupManagerPatrolIfNeverCreated bootstraps the platform-owned adaptive
// patrol for a newly bound group-manager agent. It deliberately treats any historical
// patrol row, including a cancelled one, as proof that bootstrap already ran:
// cancellation is durable and only an explicit agent-side natural-language
// management action may re-enable the patrol.
func (h *Handler) ensureGroupManagerPatrolIfNeverCreated(ctx context.Context, workspaceID, channelID, managerID, initiatorID pgtype.UUID) error {
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var lockedManagerID pgtype.UUID
	if err := tx.QueryRow(ctx, `
		SELECT ch.group_manager_agent_id
		FROM channel ch
		JOIN agent manager
		  ON manager.id = ch.group_manager_agent_id
		 AND manager.workspace_id = ch.workspace_id
		 AND manager.archived_at IS NULL
		 AND manager.managed_role = 'group_manager'
		WHERE ch.id = $1
		  AND ch.workspace_id = $2
		  AND ch.kind = 'group'
		  AND ch.archived_at IS NULL
		  AND ch.group_manager_agent_id = $3
		FOR UPDATE OF ch, manager`, channelID, workspaceID, managerID).Scan(&lockedManagerID); err != nil {
		return err
	}
	var exists bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
		  SELECT 1
		  FROM agent_reminder
		  WHERE workspace_id = $1
		    AND agent_id = $2
		    AND anchor_channel_id = $3
		    AND origin_kind = 'group_manager_auto'
		    AND managed_kind = 'patrol'
		)`, workspaceID, managerID, channelID).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return tx.Commit(ctx)
	}
	if _, err := lockReminderOwnerCapability(ctx, tx, workspaceID, managerID); err != nil {
		return err
	}
	var anchorMessageID pgtype.UUID
	_ = tx.QueryRow(ctx, `
		SELECT id
		FROM channel_message
		WHERE workspace_id = $1 AND channel_id = $2 AND deleted_at IS NULL
		ORDER BY created_at ASC, id ASC LIMIT 1`, workspaceID, channelID).Scan(&anchorMessageID)
	patrolState, err := loadManagedPatrolIssueState(ctx, tx, workspaceID, channelID)
	if err != nil {
		return err
	}
	next := time.Now().UTC().Add(managedPatrolMinDelay)
	status := "fired"
	reason := "group_manager_patrol_bootstrapped_dormant"
	if patrolState.Active > 0 {
		status = "scheduled"
		reason = "group_manager_patrol_bootstrapped"
	}
	created, err := scanAgentReminder(tx.QueryRow(ctx, `
		INSERT INTO agent_reminder (
		  workspace_id, agent_id, initiator_user_id, title, anchor_channel_id,
		  anchor_message_id, fire_at, status, origin_kind, managed_kind, origin_key
		) VALUES (
		  $1, $2, $3, '群巡检', $4, $5, $6, $7,
		  'group_manager_auto', 'patrol', $8
		)
		RETURNING `+reminderSelectColumns(), workspaceID, managerID, initiatorID,
		channelID, nullableUUID(anchorMessageID), next, status, "patrol:"+uuidToString(channelID)))
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO agent_reminder_lifecycle_event (
		  reminder_id, workspace_id, agent_id, event_type, actor_type, actor_id,
		  next_fire_at, title_snapshot, cadence_snapshot, resulting_state,
		  reason_code
		) VALUES (
		  $1, $2, $3, 'scheduled', 'system', $3, $4, $5, $6, $7,
		  $8
		)`, created.ID, created.WorkspaceID, created.AgentID, created.FireAt,
		created.Title, created.Cadence, created.Status, reason); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	h.publishAgentReminderChanged(ctx, created.WorkspaceID, created.AgentID)
	if created.Status == "scheduled" {
		h.projectReminderUpsert(ctx, created)
	} else {
		h.projectReminderCancel(ctx, created)
	}
	return nil
}
