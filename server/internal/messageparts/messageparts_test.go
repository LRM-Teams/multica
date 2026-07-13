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

func TestNormalizeAttachmentPart(t *testing.T) {
	id := "11111111-1111-1111-1111-111111111111"
	content, parts, err := Normalize("", []protocol.MessagePart{{
		Type:         protocol.MessagePartTypeAttachment,
		AttachmentID: id,
		Filename:     "shot.png",
		Text:         "should-clear",
		StickerID:    "should-clear",
	}})
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if content != "" {
		t.Fatalf("content = %q, want empty for attachment-only (no markdown URL)", content)
	}
	if len(parts) != 1 {
		t.Fatalf("parts len = %d, want 1", len(parts))
	}
	p := parts[0]
	if p.Type != protocol.MessagePartTypeAttachment || p.AttachmentID != id {
		t.Fatalf("parts[0] = %+v, want attachment %s", p, id)
	}
	if p.Filename != "shot.png" {
		t.Fatalf("filename = %q, want shot.png", p.Filename)
	}
	if p.Text != "" || p.StickerID != "" || p.PackID != "" || p.Alt != "" {
		t.Fatalf("attachment part retained text/sticker fields: %+v", p)
	}
}

func TestNormalizeAttachmentRequiresID(t *testing.T) {
	_, _, err := Normalize("", []protocol.MessagePart{{Type: protocol.MessagePartTypeAttachment}})
	if err == nil {
		t.Fatal("expected error for missing attachment_id")
	}
}

func TestNormalizeTextPlusAttachments(t *testing.T) {
	a := "11111111-1111-1111-1111-111111111111"
	b := "22222222-2222-2222-2222-222222222222"
	content, parts, err := Normalize("", []protocol.MessagePart{
		{Type: protocol.MessagePartTypeText, Text: "  check s146  "},
		{Type: protocol.MessagePartTypeAttachment, AttachmentID: a},
		{Type: protocol.MessagePartTypeAttachment, AttachmentID: b},
	})
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if content != "check s146" {
		t.Fatalf("content = %q, want text only", content)
	}
	if len(parts) != 3 {
		t.Fatalf("parts len = %d, want 3", len(parts))
	}
	if parts[1].AttachmentID != a || parts[2].AttachmentID != b {
		t.Fatalf("attachment order = %+v", parts)
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

func TestUnwrapStructuredMessageSendEmbeddedTextPrefixedEnvelope(t *testing.T) {
	raw := `Repo is not checked out this turn either - consistent with prior attempts. {"action":"message_send","output":"Visible reply","parts":[{"type":"text","text":"Visible reply"}]}`
	content, parts, unwrapped, err := UnwrapStructuredMessageSend(raw, nil)
	if err != nil {
		t.Fatalf("UnwrapStructuredMessageSend returned error: %v", err)
	}
	if !unwrapped {
		t.Fatal("expected embedded structured message_send payload to unwrap")
	}
	if content != "Visible reply" {
		t.Fatalf("content = %q, want Visible reply", content)
	}
	if len(parts) != 1 || parts[0].Type != protocol.MessagePartTypeText || parts[0].Text != "Visible reply" {
		t.Fatalf("parts = %+v, want one normalized text part", parts)
	}
}

func TestUnwrapStructuredMessageSendEmbeddedEnvelopeHandlesBracesInStrings(t *testing.T) {
	raw := `prefix {"action":"message_send","output":"Visible {brace} reply","parts":[{"type":"text","text":"Visible {brace} reply"}]} suffix {"parts":["left alone"]}`
	content, parts, unwrapped, err := UnwrapStructuredMessageSend(raw, nil)
	if err != nil {
		t.Fatalf("UnwrapStructuredMessageSend returned error: %v", err)
	}
	if !unwrapped {
		t.Fatal("expected embedded structured message_send payload to unwrap")
	}
	if content != "Visible {brace} reply" {
		t.Fatalf("content = %q, want visible reply with braces", content)
	}
	if len(parts) != 1 || parts[0].Text != "Visible {brace} reply" {
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

func TestUnwrapStructuredMessageSendLeavesEmbeddedJSONWithoutActionAlone(t *testing.T) {
	raw := `Here is sample JSON: {"parts":["a","b"]}`
	content, parts, unwrapped, err := UnwrapStructuredMessageSend(raw, nil)
	if err != nil {
		t.Fatalf("UnwrapStructuredMessageSend returned error: %v", err)
	}
	if unwrapped {
		t.Fatal("embedded JSON without action must not unwrap")
	}
	if content != raw || len(parts) != 0 {
		t.Fatalf("content=%q parts=%+v, want unchanged input", content, parts)
	}
}

func TestUnwrapStructuredMessageSendLeavesEmbeddedMessageSendWithoutPartsAlone(t *testing.T) {
	raw := `Here is a JSON snippet: {"action":"message_send","output":"plain text"}`
	content, parts, unwrapped, err := UnwrapStructuredMessageSend(raw, nil)
	if err != nil {
		t.Fatalf("UnwrapStructuredMessageSend returned error: %v", err)
	}
	if unwrapped {
		t.Fatal("embedded message_send without parts must not unwrap")
	}
	if content != raw || len(parts) != 0 {
		t.Fatalf("content=%q parts=%+v, want unchanged input", content, parts)
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
