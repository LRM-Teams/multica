package messageparts

import (
	"testing"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestNormalizeTreatsStickerTokenAsPlainText(t *testing.T) {
	content, parts, err := Normalize(" :sticker:hi: ", nil)
	if err != nil {
		t.Fatalf("Normalize returned error: %v", err)
	}
	if content != ":sticker:hi:" {
		t.Fatalf("content = %q, want trimmed plain text", content)
	}
	if len(parts) != 0 {
		t.Fatalf("parts = %+v, want no structured parts without parts input", parts)
	}
}

func TestNormalizeBuildsFallbackContentFromParts(t *testing.T) {
	content, parts, err := Normalize("", []protocol.MessagePart{
		{Type: protocol.MessagePartTypeText, Text: "hello"},
		{Type: protocol.MessagePartTypeSticker, StickerID: "hi"},
	})
	if err != nil {
		t.Fatalf("Normalize returned error: %v", err)
	}
	if len(parts) != 2 || parts[1].PackID != BuiltinStickerPackID {
		t.Fatalf("parts = %+v, want normalized builtin sticker", parts)
	}
	if content != "hello "+parts[1].Alt {
		t.Fatalf("content = %q, want text plus sticker alt fallback %q", content, "hello "+parts[1].Alt)
	}
}

func TestNormalizeRejectsUnknownSticker(t *testing.T) {
	_, _, err := Normalize("", []protocol.MessagePart{{Type: protocol.MessagePartTypeSticker, StickerID: "does-not-exist"}})
	if err == nil {
		t.Fatal("Normalize accepted an unknown sticker")
	}
}

func TestUnwrapStructuredMessageSendTextParts(t *testing.T) {
	content, parts, unwrapped, err := UnwrapStructuredMessageSend(
		`{"action":"message_send","output":"Hello","parts":[{"type":"text","text":"Hello"}]}`,
		nil,
	)
	if err != nil {
		t.Fatalf("UnwrapStructuredMessageSend returned error: %v", err)
	}
	if !unwrapped {
		t.Fatal("expected structured message_send payload to unwrap")
	}
	if content != "Hello" {
		t.Fatalf("content = %q, want Hello", content)
	}
	if len(parts) != 1 || parts[0].Type != protocol.MessagePartTypeText || parts[0].Text != "Hello" {
		t.Fatalf("parts = %+v, want one normalized text part", parts)
	}
}

func TestUnwrapStructuredMessageSendLeavesPlainJSONAlone(t *testing.T) {
	raw := `{"output":"Hello"}`
	content, parts, unwrapped, err := UnwrapStructuredMessageSend(raw, nil)
	if err != nil {
		t.Fatalf("UnwrapStructuredMessageSend returned error: %v", err)
	}
	if unwrapped {
		t.Fatal("plain JSON without action/type/parts must not unwrap")
	}
	if content != raw || len(parts) != 0 {
		t.Fatalf("content=%q parts=%+v, want unchanged raw JSON", content, parts)
	}
}

func TestUnwrapStructuredMessageSendLeavesExistingPartsAlone(t *testing.T) {
	existing := []protocol.MessagePart{{Type: protocol.MessagePartTypeText, Text: "Already normalized"}}
	raw := `{"action":"message_send","output":"Hidden"}`
	content, parts, unwrapped, err := UnwrapStructuredMessageSend(raw, existing)
	if err != nil {
		t.Fatalf("UnwrapStructuredMessageSend returned error: %v", err)
	}
	if unwrapped {
		t.Fatal("content with existing structured parts must not unwrap again")
	}
	if content != raw || len(parts) != 1 || parts[0].Text != "Already normalized" {
		t.Fatalf("content=%q parts=%+v, want unchanged input", content, parts)
	}
}
