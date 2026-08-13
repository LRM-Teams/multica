package protocol

// Agent inbox reasons are the product identity of a drainable work item.
// Each retained product surface has its own reason string so daemon/server
// routing never has to infer origin from secondary columns (channel_id,
// chat_session_id, etc.).
//
// Residual channel dual-write reasons (mention / channel_message / …) are
// listed separately and must not execute after the #2295 hard-cut.

const (
	// AgentInboxReasonChatSession is standalone FAB/bubble chat_session work
	// created by CreateChatTask / EnqueueChatTask. It is not channel chat.
	AgentInboxReasonChatSession = "chat_session"

	// AgentInboxReasonVoiceCall is a live voice-call directed turn on a channel.
	AgentInboxReasonVoiceCall = "voice_call"

	// AgentInboxReasonIssueThreadBackflow is issue→channel thread projection work.
	AgentInboxReasonIssueThreadBackflow = "issue_thread_backflow"

	// AgentInboxReasonCollaborationTurn is env/collab peer wake work.
	AgentInboxReasonCollaborationTurn = "collaboration_turn"

	// AgentInboxReasonChannelOnboarding is membership onboarding protocol work.
	AgentInboxReasonChannelOnboarding = "channel_onboarding"

	// AgentInboxReasonGoalGraphDelta wakes the Goal coordinator after a kernel
	// review verdict changes the executable frontier.
	AgentInboxReasonGoalGraphDelta = "goal_graph_delta"

	// AgentInboxReasonNoteWorker is notes 「按这篇做」/ Worker dispatch into a
	// Messages channel or agent DM. Product reason (not residual channel chat).
	AgentInboxReasonNoteWorker = "note_worker"

	// Residual channel dual-write reasons (no longer written for ordinary
	// channel traffic; still recognized so residual rows are suppressed).
	AgentInboxReasonChannelMention  = "mention"
	AgentInboxReasonChannelMessage  = "channel_message"
	AgentInboxReasonChannelThread   = "thread_reply"
	AgentInboxReasonChannelAmbient  = "ambient"
	AgentInboxReasonChannelDMLegacy = "dm"
)

// IsResidualChannelChatInboxReason reports whether reason is a leftover
// task-shaped channel chat wake. These must not execute: MessageCoordinator
// owns channel Message delivery.
//
// This is a pure reason-set check. Standalone bubble uses chat_session;
// voice uses voice_call; they never appear here.
func IsResidualChannelChatInboxReason(reason string) bool {
	switch reason {
	case AgentInboxReasonChannelMention,
		AgentInboxReasonChannelMessage,
		AgentInboxReasonChannelThread,
		AgentInboxReasonChannelAmbient,
		AgentInboxReasonChannelDMLegacy:
		return true
	default:
		return false
	}
}
