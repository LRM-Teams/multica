package daemonws

import (
	"encoding/json"
	"log/slog"
	"strconv"
	"strings"

	"github.com/oklog/ulid/v2"

	"github.com/multica-ai/multica/server/internal/realtime"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type ReminderNotifier interface {
	NotifyReminderUpsert(runtimeID string, payload protocol.ReminderUpsertPayload)
	NotifyReminderCancel(runtimeID string, payload protocol.ReminderCancelPayload)
	NotifyReminderFireReceiptAck(runtimeID string, payload protocol.ReminderFireReceiptAckPayload)
}

// AgentDeliveryNotifier is the server-side transport boundary for canonical
// Agent Message deliveries. It intentionally exposes no task/lease concepts.
type AgentDeliveryNotifier interface {
	NotifyWorkspaceAgentDelivery(workspaceID, daemonID string, payload protocol.AgentDeliverPayload) bool
}

// AgentRestartNotifier transports only Raft's discrete WorkspaceDaemon
// commands. Product-level restart/reset orchestration stays on the
// server and advances from Runner status/reset facts.
type AgentRestartNotifier interface {
	NotifyAgentRestartCommand(workspaceID, computerID, eventType, commandID string, payload any) bool
}

// RelayNotifier sends task wakeups to the local daemon hub and, when Redis is
// configured, publishes the same wakeup through the shared realtime relay so
// every API node can attempt local delivery.
type RelayNotifier struct {
	local *Hub
	relay realtime.RelayPublisher
}

func workspaceDaemonRelayScopeID(daemonID, workspaceID string) string {
	return daemonID + "\x00" + workspaceID
}

func parseWorkspaceDaemonRelayScopeID(scopeID string) (daemonID, workspaceID string, ok bool) {
	daemonID, workspaceID, ok = strings.Cut(scopeID, "\x00")
	return daemonID, workspaceID, ok && daemonID != "" && workspaceID != ""
}

func (n *RelayNotifier) NotifyAgentDelivery(runtimeID string, payload protocol.AgentDeliverPayload) bool {
	if runtimeID == "" {
		return false
	}
	frame, err := json.Marshal(protocol.Message{Type: protocol.EventAgentDeliver, Payload: mustMarshalRaw(payload)})
	if err != nil {
		return false
	}
	delivered := false
	eventID := ulid.Make().String()
	if n.local != nil {
		delivered = n.local.notifyAgentDelivery(runtimeID, payload, eventID)
	}
	if n.relay != nil {
		if err := n.relay.PublishWithID(realtime.ScopeDaemonRuntime, runtimeID, "", frame, eventID); err != nil {
			slog.Warn("agent delivery relay publish failed", "error", err, "runtime_id", runtimeID, "delivery_id", payload.DeliveryID)
		} else {
			delivered = true
		}
	}
	return delivered
}

// NotifyWorkspaceAgentDelivery places canonical Messages at the Workspace
// Runner boundary. Runtime IDs are provider placement, not Message transport
// addresses; the Runner's one Manager owns local receipt and handoff.
func (n *RelayNotifier) NotifyWorkspaceAgentDelivery(workspaceID, daemonID string, payload protocol.AgentDeliverPayload) bool {
	if workspaceID == "" || daemonID == "" {
		return false
	}
	frame, err := json.Marshal(protocol.Message{Type: protocol.EventAgentDeliver, Payload: mustMarshalRaw(payload)})
	if err != nil {
		return false
	}
	delivered := false
	eventID := ulid.Make().String()
	if n.local != nil {
		delivered = n.local.notifyWorkspaceAgentDelivery(workspaceID, daemonID, payload, frame, eventID)
	}
	if n.relay != nil {
		scopeID := workspaceDaemonRelayScopeID(daemonID, workspaceID)
		if err := n.relay.PublishWithID(realtime.ScopeDaemonWorkspaceDaemon, scopeID, "", frame, eventID); err != nil {
			slog.Warn("workspace Runner agent delivery relay publish failed", "error", err, "workspace_id", workspaceID, "daemon_id", daemonID, "delivery_id", payload.DeliveryID)
		} else {
			delivered = true
		}
	}
	return delivered
}

func (n *RelayNotifier) NotifyAgentRestartCommand(workspaceID, computerID, eventType, commandID string, payload any) bool {
	if workspaceID == "" || computerID == "" || commandID == "" || !validAgentRestartCommand(eventType, payload) {
		return false
	}
	frame, err := json.Marshal(protocol.Message{Type: eventType, Payload: mustMarshalRaw(payload)})
	if err != nil {
		return false
	}
	delivered := false
	if n.local != nil {
		delivered = n.local.NotifyAgentRestartCommand(workspaceID, computerID, eventType, commandID, payload)
	}
	if n.relay != nil {
		scopeID := workspaceDaemonRelayScopeID(computerID, workspaceID)
		if err := n.relay.PublishWithID(realtime.ScopeDaemonWorkspaceDaemon, scopeID, "", frame, "agent-restart:"+commandID+":"+eventType); err != nil {
			slog.Warn("WorkspaceDaemon Agent Restart command publish failed", "workspace_id", workspaceID, "computer_id", computerID, "operation_id", commandID, "event_type", eventType, "error", err)
		} else {
			delivered = true
		}
	}
	return delivered
}

func validAgentRestartCommand(eventType string, payload any) bool {
	switch command := payload.(type) {
	case protocol.AgentStopPayload:
		return eventType == protocol.EventDaemonAgentStop && command.Validate() == nil
	case protocol.AgentWorkspaceResetPayload:
		return eventType == protocol.EventDaemonAgentResetWorkspace && command.Validate() == nil
	case protocol.AgentStartPayload:
		return eventType == protocol.EventDaemonAgentStart && command.Validate() == nil
	default:
		return false
	}
}

func NewRelayNotifier(local *Hub, relay realtime.RelayPublisher) *RelayNotifier {
	return &RelayNotifier{local: local, relay: relay}
}

func (n *RelayNotifier) NotifyTaskAvailable(runtimeID, taskID string) {
	if runtimeID == "" {
		return
	}
	eventID := ulid.Make().String()
	if n.local != nil {
		n.local.notifyTaskAvailable(runtimeID, taskID, eventID)
	}
	if n.relay == nil {
		return
	}
	frame, err := taskAvailableFrame(runtimeID, taskID)
	if err != nil {
		M.WakeupPublishErrors.Add(1)
		return
	}
	shardKey := taskID
	if shardKey == "" {
		shardKey = eventID
	}
	if err := n.relay.PublishWithID(realtime.ScopeDaemonRuntime, shardKey, "", frame, eventID); err != nil {
		M.WakeupPublishErrors.Add(1)
		slog.Warn("daemon websocket wakeup publish failed", "error", err, "runtime_id", runtimeID, "task_id", taskID)
		return
	}
	M.WakeupPublishedTotal.Add(1)
}

func (n *RelayNotifier) NotifyReminderUpsert(runtimeID string, payload protocol.ReminderUpsertPayload) {
	n.notifyReminderWithID(runtimeID, protocol.EventReminderUpsert, payload, "reminder-upsert:"+payload.Reminder.ReminderID+":"+strconv.FormatInt(payload.Reminder.Version, 10))
}

func (n *RelayNotifier) NotifyReminderCancel(runtimeID string, payload protocol.ReminderCancelPayload) {
	n.notifyReminderWithID(runtimeID, protocol.EventReminderCancel, payload, "reminder-cancel:"+payload.ReminderID+":"+strconv.FormatInt(payload.Version, 10))
}

func (n *RelayNotifier) NotifyReminderFireReceiptAck(runtimeID string, payload protocol.ReminderFireReceiptAckPayload) {
	n.notifyReminderWithID(runtimeID, protocol.EventReminderFireReceiptAck, payload, "reminder-fire-receipt-ack:"+payload.ReminderID+":"+strconv.FormatInt(payload.Version, 10))
}

func (n *RelayNotifier) notifyReminderWithID(runtimeID, eventType string, payload any, eventID string) {
	if runtimeID == "" {
		return
	}
	frame, err := json.Marshal(protocol.Message{Type: eventType, Payload: mustMarshalRaw(payload)})
	if err != nil {
		return
	}
	if n.local != nil {
		n.local.notifyReminderFrame(runtimeID, frame, eventID)
	}
	if n.relay == nil {
		return
	}
	if err := n.relay.PublishWithID(realtime.ScopeDaemonRuntime, runtimeID, "", frame, eventID); err != nil {
		slog.Warn("daemon websocket reminder projection publish failed", "error", err, "runtime_id", runtimeID, "type", eventType)
	}
}
