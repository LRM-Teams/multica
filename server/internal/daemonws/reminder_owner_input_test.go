package daemonws

import (
	"testing"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestReminderOwnerInputMissingTransportIsFinalAndUnstaged(t *testing.T) {
	hub := NewHub()
	payload := protocol.ReminderOwnerInputPayload{
		WorkspaceID: "workspace-a", AgentID: "agent-a", RuntimeID: "runtime-a",
		ReminderID: "reminder-a", Version: 1, Title: "title",
	}
	if hub.NotifyReminderOwnerInput("workspace-a", "daemon-a", payload) {
		t.Fatal("missing transport reported Reminder owner input delivered")
	}
	hub.agentDeliveryMu.Lock()
	defer hub.agentDeliveryMu.Unlock()
	if len(hub.pendingAgentDeliveries) != 0 {
		t.Fatalf("transient Reminder input entered durable/retry staging: %d", len(hub.pendingAgentDeliveries))
	}
}
