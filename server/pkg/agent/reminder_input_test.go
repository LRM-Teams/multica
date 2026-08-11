package agent

import (
	"strings"
	"testing"
)

var (
	_ ResidentReminderInputReceiver = (*codexAppServerBackend)(nil)
	_ ResidentReminderInputReceiver = (*claudeStreamJSONBackend)(nil)
	_ ResidentReminderInputReceiver = (*cursorACPBackend)(nil)
	_ ResidentReminderInputReceiver = (*piRPCBackend)(nil)
	_ ResidentReminderInputReceiver = (*opencodeServeBackend)(nil)
)

func TestFormatResidentReminderInputIsPrivateSystemInputWithReturnTarget(t *testing.T) {
	prompt, err := formatResidentReminderInput(ResidentReminderInput{
		ReminderID: "reminder-a", Version: 3, Title: "Review deploy",
		Anchor: ResidentReminderAnchor{
			Available: true, ChannelID: "channel-a", MessageID: "message-a",
			Target: "channel:channel-a", ReplyTarget: "#general", Excerpt: "Review after deploy",
		},
		Occurrence: ResidentReminderOccurrence{
			OccurrenceID: "occurrence-a", ScheduledFor: "2026-08-11T07:00:00Z", DueAt: "2026-08-11T07:00:00Z",
		},
	})
	if err != nil {
		t.Fatalf("format Reminder input: %v", err)
	}
	for _, want := range []string{"Private Reminder system input", `"kind":"reminder"`, `"message_id":"message-a"`, `"reply_target":"#general"`, "not a canonical Message"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
	if strings.Contains(prompt, "Canonical Messages received") {
		t.Fatalf("Reminder reused canonical Message prompt:\n%s", prompt)
	}
}
