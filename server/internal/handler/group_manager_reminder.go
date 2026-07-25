package handler

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

func rearmDormantManagedPatrolForChannelMessage(
	ctx context.Context,
	exec dbExecutor,
	workspaceID, channelID pgtype.UUID,
	message ChannelMessageResponse,
) error {
	var updated int
	err := exec.QueryRow(ctx, `
		WITH locked AS (
		  SELECT reminder.id, reminder.workspace_id, reminder.agent_id,
		         reminder.fire_at AS previous_fire_at, reminder.title,
		         reminder.cadence, reminder.schedule_timezone
		  FROM agent_reminder reminder
		  JOIN channel ch
		    ON ch.id = reminder.anchor_channel_id
		   AND ch.workspace_id = reminder.workspace_id
		   AND ch.kind = 'group'
		   AND ch.archived_at IS NULL
		   AND ch.group_manager_agent_id = reminder.agent_id
		  WHERE reminder.workspace_id = $1
		    AND reminder.anchor_channel_id = $2
		    AND reminder.origin_kind = 'group_manager_auto'
		    AND reminder.managed_kind = 'patrol'
		    AND reminder.status = 'fired'
		  FOR UPDATE OF reminder
		),
		rearmed AS (
		  UPDATE agent_reminder reminder
		  SET status = 'scheduled',
		      fire_at = now() + interval '15 minutes',
		      cadence = NULL,
		      schedule_timezone = NULL,
		      cadence_next_at = NULL,
		      current_occurrence_id = NULL,
		      terminal_reason = NULL,
		      fired_task_id = NULL,
		      managed_backoff_step = 0,
		      version = reminder.version + 1,
		      updated_at = now()
		  FROM locked
		  WHERE reminder.id = locked.id
		  RETURNING reminder.id, reminder.workspace_id, reminder.agent_id,
		            reminder.fire_at, reminder.title, reminder.cadence,
		            reminder.schedule_timezone, locked.previous_fire_at
		),
		lifecycle AS (
		  INSERT INTO agent_reminder_lifecycle_event (
		    reminder_id, workspace_id, agent_id, event_type, actor_type, actor_id,
		    previous_fire_at, next_fire_at, title_snapshot, cadence_snapshot,
		    timezone_snapshot, resulting_state, reason_code, details
		  )
		  SELECT
		    rearmed.id, rearmed.workspace_id, rearmed.agent_id,
		    'scheduled', 'system', rearmed.agent_id,
		    rearmed.previous_fire_at, rearmed.fire_at, rearmed.title,
		    rearmed.cadence, rearmed.schedule_timezone, 'scheduled',
		    'patrol_open_loop_message_rearm',
		    jsonb_build_object(
		      'policy', 'group_manager_open_loop_v1',
		      'message_id', $3::text,
		      'message_seq', $4::bigint,
		      'author_type', $5::text,
		      'delay_seconds', 900
		    )
		  FROM rearmed
		  RETURNING 1
		)
		SELECT count(*)::int FROM lifecycle`,
		workspaceID,
		channelID,
		message.ID,
		message.Seq,
		message.Type,
	).Scan(&updated)
	return err
}

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
	patrolContext, err := loadManagedPatrolOpenLoopContext(ctx, tx, workspaceID, channelID, managerID)
	if err != nil {
		return err
	}
	next := time.Now().UTC().Add(managedPatrolMinDelay)
	status := "fired"
	reason := "group_manager_patrol_bootstrapped_dormant"
	if patrolContext.HasCandidates() {
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
