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
