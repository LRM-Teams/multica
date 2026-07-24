package handler

import (
	"strings"
	"unicode"
)

// isPureStandaloneChatGreeting reports whether content is a short social
// greeting that can be answered with a platform sticker without enqueueing an
// agent task (L4 greeting fast path). Keep this intentionally narrow: any
// punctuation-heavy, multi-sentence, or attachment-bearing turn stays on the
// normal agent path.
func isPureStandaloneChatGreeting(content string) bool {
	s := strings.TrimSpace(content)
	if s == "" {
		return false
	}
	// Reject anything with newlines or markdown-ish structure.
	if strings.ContainsAny(s, "\n\r`") {
		return false
	}
	// Strip common greeting punctuation / fillers, then compare.
	s = strings.Map(func(r rune) rune {
		switch r {
		case '!', '！', '?', '？', '.', '。', '~', '～', '…', ',', '，', ' ', '\t':
			return -1
		}
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, s)
	if s == "" {
		return false
	}
	switch strings.ToLower(s) {
	case "hi", "hello", "hey", "yo",
		"你好", "您好", "在吗", "在么", "在麼", "嗨", "哈喽", "哈囉":
		return true
	default:
		return false
	}
}
