package daemonws

import (
	"encoding/json"
	"log/slog"

	"github.com/oklog/ulid/v2"

	"github.com/multica-ai/multica/server/internal/realtime"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type ReminderNotifier interface {
	NotifyReminderUpsert(runtimeID string, payload protocol.ReminderUpsertPayload)
	NotifyReminderCancel(runtimeID string, payload protocol.ReminderCancelPayload)
	NotifyReminderOwnerAdded(runtimeID string, payload protocol.DaemonAgentStartPayload)
	NotifyReminderOwnerRemoved(runtimeID string, payload protocol.DaemonAgentStopPayload)
}

// RelayNotifier sends task wakeups to the local daemon hub and, when Redis is
// configured, publishes the same wakeup through the shared realtime relay so
// every API node can attempt local delivery.
type RelayNotifier struct {
	local *Hub
	relay realtime.RelayPublisher
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
	n.notifyReminder(runtimeID, protocol.EventReminderUpsert, payload)
}

func (n *RelayNotifier) NotifyReminderCancel(runtimeID string, payload protocol.ReminderCancelPayload) {
	n.notifyReminder(runtimeID, protocol.EventReminderCancel, payload)
}

func (n *RelayNotifier) NotifyReminderOwnerRemoved(runtimeID string, payload protocol.DaemonAgentStopPayload) {
	n.notifyReminder(runtimeID, protocol.EventDaemonAgentStop, payload)
}

func (n *RelayNotifier) NotifyReminderOwnerAdded(runtimeID string, payload protocol.DaemonAgentStartPayload) {
	n.notifyReminder(runtimeID, protocol.EventDaemonAgentStart, payload)
}

func (n *RelayNotifier) notifyReminder(runtimeID, eventType string, payload any) {
	if runtimeID == "" {
		return
	}
	eventID := ulid.Make().String()
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
