package daemon

import (
	"testing"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestResidualChannelChatInboxReasonSet(t *testing.T) {
	// Daemon uses the shared reason taxonomy; standalone bubble is not residual.
	residual := []string{
		protocol.AgentInboxReasonChannelMention,
		protocol.AgentInboxReasonChannelMessage,
		protocol.AgentInboxReasonChannelThread,
		protocol.AgentInboxReasonChannelAmbient,
		protocol.AgentInboxReasonChannelDMLegacy,
	}
	for _, reason := range residual {
		if !protocol.IsResidualChannelChatInboxReason(reason) {
			t.Fatalf("%q should be residual channel chat", reason)
		}
	}
	retained := []string{
		protocol.AgentInboxReasonChatSession,
		protocol.AgentInboxReasonVoiceCall,
		protocol.AgentInboxReasonIssueThreadBackflow,
		protocol.AgentInboxReasonCollaborationTurn,
		protocol.AgentInboxReasonChannelOnboarding,
		"issue",
		"quick_create",
	}
	for _, reason := range retained {
		if protocol.IsResidualChannelChatInboxReason(reason) {
			t.Fatalf("%q must not be residual channel chat", reason)
		}
	}
}
