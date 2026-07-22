package daemonws

import (
	"encoding/json"
	"log/slog"
	"strconv"

	"github.com/oklog/ulid/v2"

	"github.com/multica-ai/multica/server/internal/realtime"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type ReminderNotifier interface {
	NotifyReminderProjection(runtimeID string, payload protocol.ReminderProjectionEvent)
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

func (n *RelayNotifier) NotifyReminderProjection(runtimeID string, payload protocol.ReminderProjectionEvent) {
	n.notifyReminderWithID(runtimeID, protocol.EventReminderProjection, payload, "reminder-projection:"+runtimeID+":"+strconv.FormatInt(payload.Seq, 10))
}

func (n *RelayNotifier) NotifyReminderOwnerRemoved(runtimeID string, payload protocol.DaemonAgentStopPayload) {
	n.notifyReminder(runtimeID, protocol.EventDaemonAgentStop, payload)
}

func (n *RelayNotifier) NotifyReminderOwnerAdded(runtimeID string, payload protocol.DaemonAgentStartPayload) {
	n.notifyReminder(runtimeID, protocol.EventDaemonAgentStart, payload)
}

func (n *RelayNotifier) notifyReminder(runtimeID, eventType string, payload any) {
	n.notifyReminderWithID(runtimeID, eventType, payload, ulid.Make().String())
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
