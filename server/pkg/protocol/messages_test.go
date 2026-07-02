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
	if _, err := NormalizeChatOutputAction("send_channel_message"); err == nil {
		t.Fatal("NormalizeChatOutputAction accepted legacy send_channel_message")
	}
}

func TestParseMessageSendCommandRequiresMessageSubcommand(t *testing.T) {
	message, ok := ParseMessageSendCommand(`multica message send --message "hello"`)
	if !ok {
		t.Fatal("ParseMessageSendCommand did not match message send command")
	}
	if message != "hello" {
		t.Fatalf("message = %q, want hello", message)
	}
	if _, ok := ParseMessageSendCommand(`multica channel send --message "hello"`); ok {
		t.Fatal("ParseMessageSendCommand accepted legacy channel send command")
	}
}

func TestParseMessageReactCommandRequiresExactSubcommand(t *testing.T) {
	if reaction, ok := ParseMessageReactCommand("multica message reactor --message 111 --emoji 👍"); ok {
		t.Fatalf("ParseMessageReactCommand matched reactor command: %+v", reaction)
	}

	reaction, ok := ParseMessageReactCommand("multica message react --message 111 --emoji 👍")
	if !ok {
		t.Fatal("ParseMessageReactCommand did not match message react command")
	}
	if reaction.MessageID != "111" || reaction.Emoji != "👍" {
		t.Fatalf("reaction = %+v, want message 111 emoji 👍", reaction)
	}
}
