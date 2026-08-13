package protocol

import "testing"

func TestIsResidualChannelChatInboxReason(t *testing.T) {
	cases := []struct {
		reason string
		want   bool
	}{
		{AgentInboxReasonChannelMention, true},
		{AgentInboxReasonChannelMessage, true},
		{AgentInboxReasonChannelThread, true},
		{AgentInboxReasonChannelAmbient, true},
		{AgentInboxReasonChannelDMLegacy, true},
		{AgentInboxReasonChatSession, false},
		{AgentInboxReasonVoiceCall, false},
		{AgentInboxReasonIssueThreadBackflow, false},
		{AgentInboxReasonCollaborationTurn, false},
		{AgentInboxReasonChannelOnboarding, false},
		{AgentInboxReasonGoalGraphDelta, false},
		{AgentInboxReasonNoteWorker, false},
		{"issue", false},
		{"quick_create", false},
	}
	for _, tc := range cases {
		if got := IsResidualChannelChatInboxReason(tc.reason); got != tc.want {
			t.Fatalf("IsResidualChannelChatInboxReason(%q) = %v, want %v", tc.reason, got, tc.want)
		}
	}
}
