package protocol

import "testing"

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
