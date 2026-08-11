package protocol

import (
	"encoding/json"
	"testing"
)

func TestReminderOwnerInputPayloadJSONGolden(t *testing.T) {
	payload := ReminderOwnerInputPayload{
		WorkspaceID:         "workspace-a",
		AgentID:             "agent-a",
		RuntimeID:           "runtime-a",
		PlacementGeneration: 7,
		ReminderID:          "reminder-a",
		Version:             11,
		Title:               "Review the deployment",
		Anchor: ReminderOwnerInputAnchor{
			Available:           true,
			ChannelID:           "channel-a",
			MessageID:           "message-a",
			ThreadRootMessageID: "root-a",
			Target:              "thread:root-a",
			ReplyTarget:         "#general:root-a",
			Excerpt:             "Please review after deploy.",
		},
		Occurrence: ReminderOwnerInputOccurrence{
			OccurrenceID: "occurrence-a",
			ScheduledFor: "2026-08-11T07:00:00Z",
			DueAt:        "2026-08-11T07:00:00Z",
			Cadence:      "every:1h",
			Timezone:     "Asia/Shanghai",
		},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal Reminder owner input: %v", err)
	}
	const want = `{"workspace_id":"workspace-a","agent_id":"agent-a","runtime_id":"runtime-a","placement_generation":7,"reminder_id":"reminder-a","version":11,"title":"Review the deployment","anchor":{"available":true,"channel_id":"channel-a","message_id":"message-a","thread_root_message_id":"root-a","target":"thread:root-a","reply_target":"#general:root-a","excerpt":"Please review after deploy."},"occurrence":{"occurrence_id":"occurrence-a","scheduled_for":"2026-08-11T07:00:00Z","due_at":"2026-08-11T07:00:00Z","cadence":"every:1h","timezone":"Asia/Shanghai"}}`
	if string(raw) != want {
		t.Fatalf("Reminder owner input JSON\n got: %s\nwant: %s", raw, want)
	}
	if EventReminderOwnerInput != "reminder.owner_input" {
		t.Fatalf("event name = %q", EventReminderOwnerInput)
	}
	if DaemonCapabilityReminderTransientInput != "reminder_transient_owner_input_v1" {
		t.Fatalf("capability = %q", DaemonCapabilityReminderTransientInput)
	}
}
