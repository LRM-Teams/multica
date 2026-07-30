package daemon

import "testing"

func TestUsesCanonicalResidentChatRuntimeIncludesCursorFullChat(t *testing.T) {
	chatTask := Task{ChatSessionID: "chat-1"}
	issueTask := Task{ChatSessionID: ""}
	if !usesCanonicalResidentChatRuntime("cursor", chatTask) {
		t.Fatal("cursor full chat should enter canonical resident path")
	}
	if usesCanonicalResidentChatRuntime("cursor", issueTask) {
		t.Fatal("cursor without ChatSessionID must stay one-shot")
	}
	if !usesCanonicalResidentChatRuntime("grok", chatTask) {
		t.Fatal("grok full chat should enter canonical path")
	}
	if !usesCanonicalResidentChatRuntime("pi", chatTask) {
		t.Fatal("pi full chat should enter canonical path")
	}
	if usesCanonicalResidentChatRuntime("claude", chatTask) {
		t.Fatal("claude has no canonical resident adapter")
	}
}
