package daemon

import (
	"encoding/json"
	"errors"
	"strings"
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

func TestStandaloneAssistantReplyTextFallsBackToStreamedDeltas(t *testing.T) {
	if got := standaloneAssistantReplyText(nil, "  hello from deltas  "); got != "hello from deltas" {
		t.Fatalf("streamed fallback got %q", got)
	}
	raw, err := json.Marshal(map[string]any{
		"role": "assistant",
		"blocks": []map[string]string{
			{"type": "text", "text": "from capture"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	capture := &agent.ResidentTurnCapture{
		ProviderCalls: []agent.ResidentProviderCallCapture{{FinalAssistantMessage: raw}},
	}
	if got := standaloneAssistantReplyText(capture, "from deltas"); got != "from capture" {
		t.Fatalf("capture must win, got %q", got)
	}
}

func TestStandaloneAssistantFailureReplySurfacesProviderError(t *testing.T) {
	got := standaloneAssistantFailureReply(errors.New("Request timed out."))
	for _, want := range []string{
		"could not complete that reply",
		"Request timed out.",
		"try again",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("failure reply missing %q\n---\n%s", want, got)
		}
	}
	if got := standaloneAssistantFailureReply(nil); got == "" {
		t.Fatal("nil error still needs a non-empty writeback so the bubble leaves 排队中")
	}
}
