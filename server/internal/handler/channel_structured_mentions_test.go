package handler

import "testing"

func TestContentUTF16SpanUsesJavaScriptOffsets(t *testing.T) {
	content := "😀 @小明"
	start := len("😀 ")
	end := len(content)

	gotStart, gotEnd := contentUTF16Span(content, start, end)
	if gotStart != 3 || gotEnd != 6 {
		t.Fatalf("contentUTF16Span(%q, %d, %d) = (%d, %d), want (3, 6)", content, start, end, gotStart, gotEnd)
	}
}
