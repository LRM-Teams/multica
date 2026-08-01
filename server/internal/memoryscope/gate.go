// Package memoryscope implements execution-time gates for scoped Multica memory.
// Spec: Scope / Memory entry rules (LRM-952) — personal memory stays out of
// group-chat wakes unless explicitly brought in; project memory requires a
// bound project.
package memoryscope

import (
	"strings"
	"unicode"
)

// IncludeUserMemory reports whether user/member-scoped memory may enter the
// current execution pack.
//
// Rules (jianghp3 / LRM-952):
//   - Explicit bring-in phrases always allow (DM or group).
//   - Group chat wakes (non-empty chatSessionID + channelKind == "group")
//     exclude personal memory by default.
//   - DM, issue, voice, and other surfaces include by default.
func IncludeUserMemory(channelKind, chatSessionID string, messageTexts ...string) bool {
	if ExplicitUserMemoryBringIn(messageTexts...) {
		return true
	}
	if strings.TrimSpace(chatSessionID) != "" && strings.EqualFold(strings.TrimSpace(channelKind), "group") {
		return false
	}
	return true
}

// ExplicitUserMemoryBringIn detects a clear request to temporarily bring
// personal preferences/memory into the current (often group) turn.
func ExplicitUserMemoryBringIn(messageTexts ...string) bool {
	for _, raw := range messageTexts {
		normalized := normalizeBringInText(raw)
		if normalized == "" {
			continue
		}
		for _, phrase := range explicitBringInPhrases {
			if strings.Contains(normalized, phrase) {
				return true
			}
		}
	}
	return false
}

var explicitBringInPhrases = func() []string {
	raw := []string{
		"带上我的个人记忆",
		"带上我的个人偏好",
		"用我的个人记忆",
		"用我的个人偏好",
		"参考我的个人偏好",
		"带上个人记忆",
		"bring my personal memory",
		"bring my personal preferences",
		"use my personal memory",
		"use my personal preferences",
		"include my personal memory",
		"include my user preferences",
	}
	out := make([]string, 0, len(raw))
	for _, phrase := range raw {
		if normalized := normalizeBringInText(phrase); normalized != "" {
			out = append(out, normalized)
		}
	}
	return out
}()

func normalizeBringInText(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(value))
	for _, r := range value {
		if unicode.IsSpace(r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
