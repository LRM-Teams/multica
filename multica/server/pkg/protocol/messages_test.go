package protocol

import "testing"

func TestNormalizeChatOutputActionRequiresMessageSend(t *testing.T) {
	action, err := NormalizeChatOutputAction("message_send")
	if err != nil {
		t.Fatalf("NormalizeChatOutputAction(message_send): %v", err)
	}
	if action != ChatOutputActionMessageSend {
		t.Fatalf("action = %q, want %q", action, ChatOutputActionMessageSend)
	}
	action, err = NormalizeChatOutputAction("send")
	if err != nil {
		t.Fatalf("NormalizeChatOutputAction(send): %v", err)
	}
	if action != ChatOutputActionMessageSend {
		t.Fatalf("action = %q, want %q", action, ChatOutputActionMessageSend)
	}
	action, err = NormalizeChatOutputAction("react")
	if err != nil {
		t.Fatalf("NormalizeChatOutputAction(react): %v", err)
	}
	if action != ChatOutputActionMessageReact {
		t.Fatalf("action = %q, want %q", action, ChatOutputActionMessageReact)
	}
	action, err = NormalizeChatOutputAction("message_react")
	if err != nil {
		t.Fatalf("NormalizeChatOutputAction(message_react): %v", err)
	}
	if action != ChatOutputActionMessageReact {
		t.Fatalf("action = %q, want %q", action, ChatOutputActionMessageReact)
	}
	if _, err := NormalizeChatOutputAction("send_channel_message"); err == nil {
		t.Fatal("NormalizeChatOutputAction accepted legacy send_channel_message")
	}
}
