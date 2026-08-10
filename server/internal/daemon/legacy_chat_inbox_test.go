package daemon

import "testing"

func TestIsLegacyChatInboxReason(t *testing.T) {
	cases := []struct {
		name   string
		reason string
		task   *Task
		want   bool
	}{
		{name: "channel_message", reason: "channel_message", want: true},
		{name: "thread_reply", reason: "thread_reply", want: true},
		{name: "ambient", reason: "ambient", want: true},
		{name: "mention", reason: "mention", want: true},
		{name: "dm channel", reason: "dm", task: &Task{ChannelID: "ch-1"}, want: true},
		// Retained product surface: FAB/bubble chat keeps inbox execution.
		{name: "dm standalone bubble chat_session", reason: "dm", task: &Task{ChatSessionID: "cs-1"}, want: false},
		{name: "issue product task", reason: "issue", task: &Task{IssueID: "iss-1"}, want: false},
		{name: "onboarding", reason: "channel_onboarding", want: false},
		{name: "collaboration", reason: "collaboration_turn", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isLegacyChatInboxReason(tc.reason, tc.task); got != tc.want {
				t.Fatalf("isLegacyChatInboxReason(%q) = %v, want %v", tc.reason, got, tc.want)
			}
		})
	}
}
