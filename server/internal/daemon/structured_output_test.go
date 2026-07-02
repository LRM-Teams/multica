package daemon

import (
	"strings"
	"testing"
)

func TestParseStructuredMessageOutputStickerParts(t *testing.T) {
	content, parts, outputType, reaction, structured, err := parseStructuredMessageOutput(`{"parts":[{"type":"sticker","sticker_id":"hi"}]}`)
	if err != nil {
		t.Fatalf("parseStructuredMessageOutput returned error: %v", err)
	}
	if !structured {
		t.Fatal("expected structured output")
	}
	if outputType != "message" {
		t.Fatalf("outputType = %q, want message", outputType)
	}
	if reaction != nil {
		t.Fatalf("reaction = %+v, want nil", reaction)
	}
	if len(parts) != 1 || parts[0].Type != "sticker" || parts[0].PackID != "builtin" || parts[0].StickerID != "hi" || parts[0].Alt == "" {
		t.Fatalf("parts = %+v, want normalized builtin hi sticker", parts)
	}
	if content != parts[0].Alt {
		t.Fatalf("content = %q, want sticker alt fallback %q", content, parts[0].Alt)
	}
}

func TestParseStructuredMessageOutputTextPlusSticker(t *testing.T) {
	content, parts, outputType, reaction, structured, err := parseStructuredMessageOutput(`{"output":"收到","parts":[{"type":"text","text":"收到"},{"type":"sticker","sticker_id":"got-it"}]}`)
	if err != nil {
		t.Fatalf("parseStructuredMessageOutput returned error: %v", err)
	}
	if !structured {
		t.Fatal("expected structured output")
	}
	if outputType != "message" {
		t.Fatalf("outputType = %q, want message", outputType)
	}
	if reaction != nil {
		t.Fatalf("reaction = %+v, want nil", reaction)
	}
	if content != "收到" {
		t.Fatalf("content = %q, want explicit output", content)
	}
	if len(parts) != 2 || parts[0].Type != "text" || parts[1].Type != "sticker" {
		t.Fatalf("parts = %+v, want text plus sticker", parts)
	}
}

func TestParseStructuredMessageOutputPlainTextIsUnchanged(t *testing.T) {
	content, parts, outputType, reaction, structured, err := parseStructuredMessageOutput("hello")
	if err != nil {
		t.Fatalf("parseStructuredMessageOutput returned error: %v", err)
	}
	if structured {
		t.Fatal("plain text must not be treated as structured output")
	}
	if outputType != "" {
		t.Fatalf("outputType = %q, want empty", outputType)
	}
	if reaction != nil {
		t.Fatalf("reaction = %+v, want nil", reaction)
	}
	if content != "hello" || len(parts) != 0 {
		t.Fatalf("content=%q parts=%+v, want unchanged text and no parts", content, parts)
	}
}

func TestParseStructuredMessageOutputJSONWithoutPartsIsUnchanged(t *testing.T) {
	raw := `{"output":"hello"}`
	content, parts, outputType, reaction, structured, err := parseStructuredMessageOutput(raw)
	if err != nil {
		t.Fatalf("parseStructuredMessageOutput returned error: %v", err)
	}
	if structured {
		t.Fatal("JSON without parts must not be treated as structured output")
	}
	if outputType != "" {
		t.Fatalf("outputType = %q, want empty", outputType)
	}
	if reaction != nil {
		t.Fatalf("reaction = %+v, want nil", reaction)
	}
	if strings.TrimSpace(content) != raw || len(parts) != 0 {
		t.Fatalf("content=%q parts=%+v, want unchanged JSON and no parts", content, parts)
	}
}

func TestParseStructuredMessageOutputNoReply(t *testing.T) {
	content, parts, outputType, reaction, structured, err := parseStructuredMessageOutput(`{"type":"no_reply","output":"internal reason"}`)
	if err != nil {
		t.Fatalf("parseStructuredMessageOutput returned error: %v", err)
	}
	if !structured {
		t.Fatal("expected structured no_reply output")
	}
	if outputType != "no_reply" {
		t.Fatalf("outputType = %q, want no_reply", outputType)
	}
	if reaction != nil || content != "" || len(parts) != 0 {
		t.Fatalf("content=%q parts=%+v reaction=%+v, want no visible output", content, parts, reaction)
	}
}

func TestParseStructuredMessageOutputReaction(t *testing.T) {
	content, parts, outputType, reaction, structured, err := parseStructuredMessageOutput(`{"type":"reaction","reaction":{"message_id":"CURRENT_MESSAGE","emoji":"👍"}}`)
	if err != nil {
		t.Fatalf("parseStructuredMessageOutput returned error: %v", err)
	}
	if !structured {
		t.Fatal("expected structured reaction output")
	}
	if outputType != "reaction" {
		t.Fatalf("outputType = %q, want reaction", outputType)
	}
	if content != "" || len(parts) != 0 {
		t.Fatalf("content=%q parts=%+v, want no visible message", content, parts)
	}
	if reaction == nil || reaction.MessageID != "CURRENT_MESSAGE" || reaction.Emoji != "👍" {
		t.Fatalf("reaction = %+v, want CURRENT_MESSAGE thumbs up", reaction)
	}
}

func TestParseStructuredMessageOutputLegacyReactionCommand(t *testing.T) {
	content, parts, outputType, reaction, structured, err := parseStructuredMessageOutput(`multica channel react 11111111-1111-1111-1111-111111111111 👍`)
	if err != nil {
		t.Fatalf("parseStructuredMessageOutput returned error: %v", err)
	}
	if !structured {
		t.Fatal("expected legacy reaction command to be typed")
	}
	if outputType != "reaction" || content != "" || len(parts) != 0 {
		t.Fatalf("content=%q parts=%+v outputType=%q, want reaction with no visible message", content, parts, outputType)
	}
	if reaction == nil || reaction.MessageID != "11111111-1111-1111-1111-111111111111" || reaction.Emoji != "👍" {
		t.Fatalf("reaction = %+v, want typed payload", reaction)
	}
}

func TestParseStructuredMessageOutputRejectsUnknownType(t *testing.T) {
	_, _, _, _, structured, err := parseStructuredMessageOutput(`{"type":"thinking","output":"internal"}`)
	if !structured {
		t.Fatal("expected structured output")
	}
	if err == nil {
		t.Fatal("expected invalid type error")
	}
}
