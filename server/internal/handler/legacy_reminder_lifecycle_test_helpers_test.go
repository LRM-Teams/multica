package handler

import (
	"context"
	"fmt"

	"github.com/multica-ai/multica/server/internal/daemonws"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// Legacy Reminder lifecycle query helpers remain test-only so historical
// migration fixtures can prove data compatibility without restoring the old
// wake-socket authority to the server binary.
func (h *Handler) HandleDaemonReminderOwnerLifecycle(ctx context.Context, identity daemonws.ClientIdentity, payload protocol.DaemonAgentLifecycleRequestPayload) ([]protocol.DaemonAgentLifecycleEvent, map[string]int64, error) {
	allowed := make(map[string]bool, len(identity.RuntimeIDs))
	for _, runtimeID := range identity.RuntimeIDs {
		allowed[runtimeID] = true
	}
	events := make([]protocol.DaemonAgentLifecycleEvent, 0)
	endCursors := make(map[string]int64, len(payload.RuntimeCursors))
	for runtimeID, cursor := range payload.RuntimeCursors {
		if !allowed[runtimeID] || cursor < 0 {
			return nil, nil, fmt.Errorf("agent lifecycle cursor outside daemon scope")
		}
		rows, err := h.DB.Query(ctx, `
			WITH latest AS (
				SELECT DISTINCT ON (agent_id)
				       event_type, agent_id, runtime_id, workspace_id, seq, placement_generation
				FROM agent_reminder_daemon_owner_event
				WHERE ($1 = '' OR workspace_id::text = $1) AND runtime_id::text = $2
				ORDER BY agent_id, seq DESC
			)
			SELECT event_type, agent_id::text, runtime_id::text, workspace_id::text, seq, placement_generation
			FROM (
				SELECT event_type, agent_id, runtime_id, workspace_id, seq, placement_generation
				FROM agent_reminder_daemon_owner_event
				WHERE ($1 = '' OR workspace_id::text = $1) AND runtime_id::text = $2 AND seq > $3
				UNION ALL
				SELECT event_type, agent_id, runtime_id, workspace_id, seq, placement_generation
				FROM latest
				WHERE event_type = 'start' AND seq <= $3
				  AND EXISTS (
					SELECT 1 FROM agent_reminder
					WHERE agent_reminder.agent_id = latest.agent_id
					  AND agent_reminder.status = 'scheduled'
				  )
			) replay
			ORDER BY seq ASC`, identity.WorkspaceID, runtimeID, cursor)
		if err != nil {
			return nil, nil, err
		}
		end := cursor
		for rows.Next() {
			var event protocol.DaemonAgentLifecycleEvent
			if err := rows.Scan(&event.EventType, &event.AgentID, &event.RuntimeID, &event.WorkspaceID, &event.LifecycleSeq, &event.PlacementGeneration); err != nil {
				rows.Close()
				return nil, nil, err
			}
			if event.LifecycleSeq > end {
				end = event.LifecycleSeq
			}
			events = append(events, event)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, nil, err
		}
		rows.Close()
		endCursors[runtimeID] = end
	}
	return events, endCursors, nil
}

func (h *Handler) HandleDaemonReminderOwnerLifecycleAck(ctx context.Context, identity daemonws.ClientIdentity, payload protocol.DaemonAgentLifecycleAckPayload) error {
	allowed := make(map[string]bool, len(identity.RuntimeIDs))
	for _, runtimeID := range identity.RuntimeIDs {
		allowed[runtimeID] = true
	}
	for runtimeID, seq := range payload.RuntimeCursors {
		if !allowed[runtimeID] || seq < 0 {
			return fmt.Errorf("agent lifecycle ack outside daemon scope")
		}
		result, err := h.DB.Exec(ctx, `
			INSERT INTO agent_reminder_daemon_owner_cursor (runtime_id, ack_seq)
			SELECT id, $2 FROM agent_runtime WHERE id::text = $1 AND ($3 = '' OR workspace_id::text = $3)
			ON CONFLICT (runtime_id) DO UPDATE
			SET ack_seq = GREATEST(agent_reminder_daemon_owner_cursor.ack_seq, EXCLUDED.ack_seq), updated_at = now()`, runtimeID, seq, identity.WorkspaceID)
		if err != nil {
			return err
		}
		if result.RowsAffected() != 1 {
			return fmt.Errorf("agent lifecycle ack runtime outside workspace")
		}
		if _, err := h.DB.Exec(ctx, `
			DELETE FROM agent_reminder_daemon_owner_event e
			WHERE e.runtime_id::text = $1 AND e.seq <= $2
			  AND EXISTS (
				SELECT 1 FROM agent_reminder_daemon_owner_event newer
				WHERE newer.runtime_id = e.runtime_id AND newer.agent_id = e.agent_id AND newer.seq > e.seq
			  )`, runtimeID, seq); err != nil {
			return err
		}
	}
	return nil
}
