package execenv

import (
	"strings"
	"testing"
)

func TestRenderIssueContextChannelWakeNotEmptyAssignment(t *testing.T) {
	t.Skip("retired task-shaped chat runtime contract")
	t.Parallel()
	out := renderIssueContext("claude", TaskContextForEnv{
		ChannelID: "ch-1",
		// ChatSessionID intentionally empty (LRM-1081 channel_wake)
	})
	if strings.Contains(out, "New Assignment") {
		t.Fatalf("channel wake must not look like New Assignment:\n%s", out)
	}
	if strings.Contains(out, "**Issue ID:**") {
		t.Fatalf("channel wake must not show blank Issue ID:\n%s", out)
	}
	if !strings.Contains(out, "Chat / Channel Wake") {
		t.Fatalf("expected channel conversation context:\n%s", out)
	}
	if strings.Contains(out, "Chat session ID") {
		t.Fatalf("must not print ChatSessionID:\n%s", out)
	}
}

func TestRenderIssueContextChatSessionIDAloneNotChannelConversation(t *testing.T) {
	t.Parallel()
	out := renderIssueContext("claude", TaskContextForEnv{
		MessageDelivery: true,
	})
	if strings.Contains(out, "Chat / Channel Wake") || strings.Contains(out, "Channel Conversation") {
		t.Fatalf("ChatSessionID alone must not force channel conversation:\n%s", out)
	}
}
