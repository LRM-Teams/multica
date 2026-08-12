package handler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"github.com/multica-ai/multica/server/internal/daemonws"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// replayAgentAttachmentCommands publishes the durable tail to the current
// Workspace Runner. The caller's cursor is only a lower bound: a command stays
// replayable until a newer command for that Agent/Runtime makes it obsolete.
func (h *Handler) replayAgentAttachmentCommands(ctx context.Context, identity daemonws.ClientIdentity, payload protocol.WorkspaceRunnerAttachmentReplayRequest) error {
	if h == nil || h.DB == nil || h.DaemonHub == nil {
		return errors.New("Attachment replay dependencies are unavailable")
	}
	if err := payload.Validate(); err != nil {
		return fmt.Errorf("validate Attachment replay request: %w", err)
	}
	if !h.DaemonHub.WorkspaceRunnerSupportsCapability(identity.DaemonID, identity.WorkspaceID, protocol.DaemonCapabilityWorkspaceRunnerAttachment) {
		return errors.New("Workspace Runner does not support Attachment replay")
	}
	allowed := runnerAttachmentRuntimeScope(identity)
	if len(payload.RuntimeCursors) != len(allowed) {
		return errors.New("Attachment replay request omitted a Runner Runtime cursor")
	}
	events := make([]agentAttachmentReplayEvent, 0)
	end := make(map[string]int64, len(payload.RuntimeCursors))
	for runtimeID, cursor := range payload.RuntimeCursors {
		if _, ok := allowed[runtimeID]; !ok {
			return fmt.Errorf("Attachment replay Runtime outside Runner scope")
		}
		rows, err := h.DB.Query(ctx, `
			SELECT lifecycle_seq, agent_id::text, runtime_id::text, attachment_generation, event_type
			FROM agent_attachment_projection_event
			WHERE workspace_id::text = $1 AND runtime_id::text = $2 AND lifecycle_seq > $3
			ORDER BY lifecycle_seq ASC`, identity.WorkspaceID, runtimeID, cursor)
		if err != nil {
			return fmt.Errorf("query Attachment replay: %w", err)
		}
		end[runtimeID] = cursor
		for rows.Next() {
			var event agentAttachmentReplayEvent
			if err := rows.Scan(&event.lifecycleSeq, &event.agentID, &event.runtimeID, &event.generation, &event.eventType); err != nil {
				rows.Close()
				return fmt.Errorf("scan Attachment replay: %w", err)
			}
			if event.lifecycleSeq > end[runtimeID] {
				end[runtimeID] = event.lifecycleSeq
			}
			events = append(events, event)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return fmt.Errorf("iterate Attachment replay: %w", err)
		}
		rows.Close()
	}
	sort.Slice(events, func(i, j int) bool { return events[i].lifecycleSeq < events[j].lifecycleSeq })
	for _, event := range events {
		base := protocol.WorkspaceRunnerAgentAttachPayload{
			AgentID: event.agentID, RuntimeID: event.runtimeID, AttachmentGeneration: event.generation,
			LifecycleSeq: event.lifecycleSeq,
		}
		var sent bool
		switch event.eventType {
		case "attach":
			sent = h.DaemonHub.NotifyWorkspaceRunner(identity.DaemonID, identity.WorkspaceID, protocol.EventAgentAttach, base)
		case "detach":
			sent = h.DaemonHub.NotifyWorkspaceRunner(identity.DaemonID, identity.WorkspaceID, protocol.EventAgentDetach, protocol.WorkspaceRunnerAgentDetachPayload(base))
		default:
			return fmt.Errorf("unknown Attachment event type %q", event.eventType)
		}
		if !sent {
			return errors.New("Workspace Runner disconnected during Attachment replay")
		}
	}
	if !h.DaemonHub.NotifyWorkspaceRunner(identity.DaemonID, identity.WorkspaceID, protocol.EventAgentAttachmentReplayEnd, protocol.WorkspaceRunnerAttachmentReplayEnd{RuntimeCursors: end}) {
		return errors.New("Workspace Runner disconnected before Attachment replay end")
	}
	slog.Info("Workspace Runner Attachment replay sent", "workspace_id", identity.WorkspaceID, "daemon_id", identity.DaemonID, "event_count", len(events), "outcome", "sent", "reason", "replay_requested")
	return nil
}

type agentAttachmentReplayEvent struct {
	lifecycleSeq int64
	agentID      string
	runtimeID    string
	generation   int64
	eventType    string
}

func (h *Handler) acknowledgeAgentAttachmentCommand(ctx context.Context, identity daemonws.ClientIdentity, eventType string, payload protocol.WorkspaceRunnerAgentAttachedPayload) error {
	if h == nil || h.DB == nil {
		return errors.New("Attachment receipt database is unavailable")
	}
	if err := payload.Validate(); err != nil {
		return fmt.Errorf("validate Attachment receipt: %w", err)
	}
	if _, ok := runnerAttachmentRuntimeScope(identity)[payload.RuntimeID]; !ok {
		return errors.New("Attachment receipt Runtime outside Runner scope")
	}
	wantEvent, wantReceipt := "attach", "attached"
	if eventType == protocol.EventAgentDetached {
		wantEvent, wantReceipt = "detach", "detached"
	}
	var matched bool
	err := h.DB.QueryRow(ctx, `
		SELECT true
		FROM agent_attachment_projection_event
		WHERE lifecycle_seq = $1
		  AND agent_id::text = $2 AND runtime_id::text = $3 AND workspace_id::text = $4
		  AND attachment_generation = $5 AND event_type = $6`,
		payload.LifecycleSeq, payload.AgentID, payload.RuntimeID,
		identity.WorkspaceID, payload.AttachmentGeneration, wantEvent).Scan(&matched)
	if err != nil {
		return errors.New("Attachment receipt does not match a pending command")
	}
	_, err = h.DB.Exec(ctx, `
		INSERT INTO agent_attachment_projection_receipt (lifecycle_seq, receipt_type)
		VALUES ($1, $2)
		ON CONFLICT (lifecycle_seq) DO NOTHING`, payload.LifecycleSeq, wantReceipt)
	if err != nil {
		return fmt.Errorf("persist Attachment receipt: %w", err)
	}
	if eventType == protocol.EventAgentAttached {
		if err := h.dispatchPendingRunnerLaunches(ctx, identity); err != nil {
			return err
		}
	}
	slog.Debug("Workspace Runner Attachment command acknowledged", "workspace_id", identity.WorkspaceID, "runtime_id", payload.RuntimeID, "agent_id", payload.AgentID, "lifecycle_seq", payload.LifecycleSeq, "outcome", "accepted", "reason", "command_receipt")
	return nil
}

func (h *Handler) acknowledgeAgentAttachmentReplay(ctx context.Context, identity daemonws.ClientIdentity, payload protocol.WorkspaceRunnerAttachmentReplayAck) error {
	if h == nil || h.DB == nil {
		return errors.New("Attachment cursor database is unavailable")
	}
	if err := payload.Validate(); err != nil {
		return fmt.Errorf("validate Attachment replay acknowledgement: %w", err)
	}
	allowed := runnerAttachmentRuntimeScope(identity)
	for runtimeID, seq := range payload.RuntimeCursors {
		if _, ok := allowed[runtimeID]; !ok {
			return errors.New("Attachment replay acknowledgement Runtime outside Runner scope")
		}
		var unacknowledged bool
		if err := h.DB.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM agent_attachment_projection_event e
				LEFT JOIN agent_attachment_projection_receipt r ON r.lifecycle_seq = e.lifecycle_seq
				WHERE e.workspace_id::text = $1 AND e.runtime_id::text = $2
				  AND e.lifecycle_seq <= $3 AND r.lifecycle_seq IS NULL
			)`, identity.WorkspaceID, runtimeID, seq).Scan(&unacknowledged); err != nil {
			return fmt.Errorf("check Attachment replay receipts: %w", err)
		}
		if unacknowledged {
			return errors.New("Attachment replay acknowledgement precedes command receipt")
		}
		result, err := h.DB.Exec(ctx, `
			INSERT INTO agent_attachment_projection_cursor (runtime_id, ack_seq)
			SELECT id, $2 FROM agent_runtime WHERE id::text = $1 AND workspace_id::text = $3
			ON CONFLICT (runtime_id) DO UPDATE
			SET ack_seq = GREATEST(agent_attachment_projection_cursor.ack_seq, EXCLUDED.ack_seq), updated_at = now()`, runtimeID, seq, identity.WorkspaceID)
		if err != nil {
			return fmt.Errorf("persist Attachment replay cursor: %w", err)
		}
		if result.RowsAffected() != 1 {
			return errors.New("Attachment replay acknowledgement Runtime outside Workspace")
		}
		if _, err := h.DB.Exec(ctx, `
			DELETE FROM agent_attachment_projection_event e
			WHERE e.runtime_id::text = $1 AND e.lifecycle_seq <= $2
			  AND EXISTS (
				SELECT 1 FROM agent_attachment_projection_event newer
				WHERE newer.runtime_id = e.runtime_id AND newer.agent_id = e.agent_id
				  AND newer.lifecycle_seq > e.lifecycle_seq
			)`, runtimeID, seq); err != nil {
			return fmt.Errorf("compact acknowledged Attachment replay: %w", err)
		}
		slog.Debug("Workspace Runner Attachment replay cursor acknowledged", "workspace_id", identity.WorkspaceID, "runtime_id", runtimeID, "lifecycle_seq", seq, "outcome", "advanced", "reason", "replay_acknowledged")
	}
	return nil
}

func runnerAttachmentRuntimeScope(identity daemonws.ClientIdentity) map[string]struct{} {
	allowed := make(map[string]struct{}, len(identity.RuntimeIDs))
	for _, runtimeID := range identity.RuntimeIDs {
		if runtimeID = strings.TrimSpace(runtimeID); runtimeID != "" {
			allowed[runtimeID] = struct{}{}
		}
	}
	return allowed
}
