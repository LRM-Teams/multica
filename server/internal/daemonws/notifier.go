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
	NotifyReminderProjection(runtimeID string, payload protocol.ReminderProjectionEvent)
	NotifyAgentAttachmentAdded(workspaceID, daemonID string, payload protocol.WorkspaceRunnerAgentAttachPayload)
	NotifyAgentAttachmentRemoved(workspaceID, daemonID string, payload protocol.WorkspaceRunnerAgentDetachPayload)
}

// ReminderOwnerInputNotifier is the non-durable post-commit transport for one
// private Reminder input. A false result is final; implementations must not
// stage retries or reconnect replay.
type ReminderOwnerInputNotifier interface {
	NotifyReminderOwnerInput(workspaceID, daemonID string, payload protocol.ReminderOwnerInputPayload) bool
}

// AgentDeliveryNotifier is the server-side transport boundary for canonical
// Agent Message deliveries. It intentionally exposes no task/lease concepts.
type AgentDeliveryNotifier interface {
	NotifyWorkspaceAgentDelivery(workspaceID, daemonID string, payload protocol.AgentDeliverPayload) bool
}

// AgentLifecycleNotifier transports only Raft's discrete Workspace Runner
// lifecycle commands. Product-level restart/reset orchestration stays on the
// server and advances from Runner status/reset facts.
type AgentLifecycleNotifier interface {
	NotifyAgentLifecycleCommand(workspaceID, daemonID, eventType, commandID string, payload any) bool
}

// RelayNotifier sends task wakeups to the local daemon hub and, when Redis is
// configured, publishes the same wakeup through the shared realtime relay so
// every API node can attempt local delivery.
type RelayNotifier struct {
	local *Hub
	relay realtime.RelayPublisher
}

func workspaceRunnerRelayScopeID(daemonID, workspaceID string) string {
	return daemonID + "\x00" + workspaceID
}

func parseWorkspaceRunnerRelayScopeID(scopeID string) (daemonID, workspaceID string, ok bool) {
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
		scopeID := workspaceRunnerRelayScopeID(daemonID, workspaceID)
		if err := n.relay.PublishWithID(realtime.ScopeDaemonWorkspaceRunner, scopeID, "", frame, eventID); err != nil {
			slog.Warn("workspace Runner agent delivery relay publish failed", "error", err, "workspace_id", workspaceID, "daemon_id", daemonID, "delivery_id", payload.DeliveryID)
		} else {
			delivered = true
		}
	}
	return delivered
}

func (n *RelayNotifier) NotifyAgentLifecycleCommand(workspaceID, daemonID, eventType, commandID string, payload any) bool {
	if workspaceID == "" || daemonID == "" || commandID == "" || !validAgentLifecycleCommand(eventType, payload) {
		return false
	}
	frame, err := json.Marshal(protocol.Message{Type: eventType, Payload: mustMarshalRaw(payload)})
	if err != nil {
		return false
	}
	delivered := false
	if n.local != nil {
		delivered = n.local.NotifyAgentLifecycleCommand(workspaceID, daemonID, eventType, commandID, payload)
	}
	if n.relay != nil {
		scopeID := workspaceRunnerRelayScopeID(daemonID, workspaceID)
		if err := n.relay.PublishWithID(realtime.ScopeDaemonWorkspaceRunner, scopeID, "", frame, "agent-lifecycle:"+commandID+":"+eventType); err != nil {
			slog.Warn("workspace Runner lifecycle command publish failed", "workspace_id", workspaceID, "daemon_id", daemonID, "command_id", commandID, "event_type", eventType, "error", err)
		} else {
			delivered = true
		}
	}
	return delivered
}

func validAgentLifecycleCommand(eventType string, payload any) bool {
	switch command := payload.(type) {
	case protocol.WorkspaceRunnerAgentStopPayload:
		return eventType == protocol.EventDaemonAgentStop && command.Validate() == nil
	case protocol.WorkspaceRunnerAgentResetWorkspacePayload:
		return eventType == protocol.EventDaemonAgentResetWorkspace && command.Validate() == nil
	case protocol.WorkspaceRunnerAgentStartPayload:
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

func (n *RelayNotifier) NotifyReminderProjection(runtimeID string, payload protocol.ReminderProjectionEvent) {
	n.notifyReminderWithID(runtimeID, protocol.EventReminderProjection, payload, "reminder-projection:"+runtimeID+":"+strconv.FormatInt(payload.Seq, 10))
}

func (n *RelayNotifier) NotifyAgentAttachmentRemoved(workspaceID, daemonID string, payload protocol.WorkspaceRunnerAgentDetachPayload) {
	n.notifyWorkspaceRunnerCommand(workspaceID, daemonID, protocol.EventAgentDetach, payload, "attachment:"+strconv.FormatInt(payload.LifecycleSeq, 10))
}

func (n *RelayNotifier) NotifyAgentAttachmentAdded(workspaceID, daemonID string, payload protocol.WorkspaceRunnerAgentAttachPayload) {
	n.notifyWorkspaceRunnerCommand(workspaceID, daemonID, protocol.EventAgentAttach, payload, "attachment:"+strconv.FormatInt(payload.LifecycleSeq, 10))
}

func (n *RelayNotifier) notifyWorkspaceRunnerCommand(workspaceID, daemonID, eventType string, payload any, eventID string) {
	if workspaceID == "" || daemonID == "" {
		return
	}
	frame, err := json.Marshal(protocol.Message{Type: eventType, Payload: mustMarshalRaw(payload)})
	if err != nil {
		return
	}
	if n.local != nil {
		n.local.notifyWorkspaceRunnerFrame(daemonID, workspaceID, frame)
	}
	if n.relay == nil {
		return
	}
	if eventID == "" {
		eventID = ulid.Make().String()
	}
	scopeID := workspaceRunnerRelayScopeID(daemonID, workspaceID)
	if err := n.relay.PublishWithID(realtime.ScopeDaemonWorkspaceRunner, scopeID, "", frame, eventID); err != nil {
		slog.Warn("workspace Runner command relay publish failed", "error", err, "workspace_id", workspaceID, "daemon_id", daemonID, "type", eventType)
	}
}

func (n *RelayNotifier) NotifyReminderOwnerInput(workspaceID, daemonID string, payload protocol.ReminderOwnerInputPayload) bool {
	if workspaceID == "" || daemonID == "" {
		return false
	}
	input := protocol.AgentTransientDeliverPayload{
		Kind: protocol.AgentTransientDeliverKindReminder, Transient: true, Reminder: payload,
	}
	frame, err := json.Marshal(protocol.Message{Type: protocol.EventAgentDeliver, Payload: mustMarshalRaw(input)})
	if err != nil {
		return false
	}
	delivered := false
	eventID := ulid.Make().String()
	if n.local != nil {
		delivered = n.local.notifyWorkspaceRunnerFrame(daemonID, workspaceID, frame)
	}
	if n.relay != nil {
		scopeID := workspaceRunnerRelayScopeID(daemonID, workspaceID)
		if err := n.relay.PublishWithID(realtime.ScopeDaemonWorkspaceRunner, scopeID, "", frame, eventID); err != nil {
			slog.Warn("workspace Runner Reminder owner input publish failed", "workspace_id", workspaceID, "daemon_id", daemonID, "runtime_id", payload.RuntimeID, "error", err)
		} else {
			delivered = true
		}
	}
	if !delivered {
		slog.Info("transient Reminder owner input", "outcome", "transport_lost", "workspace_id", workspaceID, "daemon_id", daemonID, "runtime_id", payload.RuntimeID, "agent_id", payload.AgentID, "reminder_id", payload.ReminderID, "version", payload.Version)
	}
	return delivered
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
