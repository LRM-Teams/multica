package daemon

import (
	"encoding/json"
	"testing"

	"github.com/multica-ai/multica/server/pkg/agent"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestStandaloneChatSessionIDFromTarget(t *testing.T) {
	sessionID, ok := standaloneChatSessionID("chat:aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")
	if !ok || sessionID != "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee" {
		t.Fatalf("got %q %v", sessionID, ok)
	}
	if _, ok := standaloneChatSessionID("channel:x"); ok {
		t.Fatal("channel target must not parse as standalone chat")
	}
}

func TestStandaloneAssistantTextFromCaptureUsesLastTextBlock(t *testing.T) {
	raw, err := json.Marshal(map[string]any{
		"role": "assistant",
		"blocks": []map[string]string{
			{"type": "thinking", "text": "scratch"},
			{"type": "text", "text": "hello from the bubble"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := standaloneAssistantTextFromCapture(&agent.ResidentTurnCapture{
		ProviderCalls: []agent.ResidentProviderCallCapture{{FinalAssistantMessage: raw}},
	})
	if got != "hello from the bubble" {
		t.Fatalf("got %q", got)
	}
}

func TestStandaloneChatSessionIDFromDeliveredBatch(t *testing.T) {
	sessionID, ok := standaloneChatSessionIDFromMessages([]protocol.AgentMessageProjection{
		{Target: "channel:1"},
		{Target: "chat:session-9"},
	})
	if !ok || sessionID != "session-9" {
		t.Fatalf("got %q %v", sessionID, ok)
	}
}
