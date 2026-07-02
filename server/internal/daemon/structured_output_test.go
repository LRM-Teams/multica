package daemon

import (
	"strings"
	"testing"
)

func TestParseStructuredMessageOutputStickerParts(t *testing.T) {
	content, parts, structured, err := parseStructuredMessageOutput(`{"parts":[{"type":"sticker","sticker_id":"hi"}]}`)
	if err != nil {
		t.Fatalf("parseStructuredMessageOutput returned error: %v", err)
	}
	if !structured {
		t.Fatal("expected structured output")
	}
	if len(parts) != 1 || parts[0].Type != "sticker" || parts[0].PackID != "builtin" || parts[0].StickerID != "hi" || parts[0].Alt == "" {
		t.Fatalf("parts = %+v, want normalized builtin hi sticker", parts)
	}
	if content != parts[0].Alt {
		t.Fatalf("content = %q, want sticker alt fallback %q", content, parts[0].Alt)
	}
}

func TestParseStructuredMessageOutputTextPlusSticker(t *testing.T) {
	content, parts, structured, err := parseStructuredMessageOutput(`{"output":"收到","parts":[{"type":"text","text":"收到"},{"type":"sticker","sticker_id":"got-it"}]}`)
	if err != nil {
		t.Fatalf("parseStructuredMessageOutput returned error: %v", err)
	}
	if !structured {
		t.Fatal("expected structured output")
	}
	if content != "收到" {
		t.Fatalf("content = %q, want explicit output", content)
	}
	if len(parts) != 2 || parts[0].Type != "text" || parts[1].Type != "sticker" {
		t.Fatalf("parts = %+v, want text plus sticker", parts)
	}
}

func TestParseStructuredMessageOutputPlainTextIsUnchanged(t *testing.T) {
	content, parts, structured, err := parseStructuredMessageOutput("hello")
	if err != nil {
		t.Fatalf("parseStructuredMessageOutput returned error: %v", err)
	}
	if structured {
		t.Fatal("plain text must not be treated as structured output")
	}
	if content != "hello" || len(parts) != 0 {
		t.Fatalf("content=%q parts=%+v, want unchanged text and no parts", content, parts)
	}
}

func TestParseStructuredMessageOutputJSONWithoutPartsIsUnchanged(t *testing.T) {
	raw := `{"output":"hello"}`
	content, parts, structured, err := parseStructuredMessageOutput(raw)
	if err != nil {
		t.Fatalf("parseStructuredMessageOutput returned error: %v", err)
	}
	if structured {
		t.Fatal("JSON without parts must not be treated as structured output")
	}
	if strings.TrimSpace(content) != raw || len(parts) != 0 {
		t.Fatalf("content=%q parts=%+v, want unchanged JSON and no parts", content, parts)
	}
}
