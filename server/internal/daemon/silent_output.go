package daemon

import "strings"

// isSilentNoReplyOutput recognizes short status lines that mean the agent chose
// to stay silent. The daemon converts these to structured no_reply before the
// server can bridge them as visible channel/thread messages.
func isSilentNoReplyOutput(output string) bool {
	normalized := normalizeSilentNoReplyOutput(output)
	if normalized == "" {
		return false
	}
	for _, phrase := range []string{
		"no visible reply needed",
		"not posting",
		"silence - no action",
		"not directed at me - no visible reply needed",
		"not directed at me - this is a general greeting, not a task or request. no visible reply needed",
		"已进入静默状态，不再执行任何操作",
		"已保持静默，无需额外操作",
		"静默 - 无操作",
	} {
		if normalized == phrase {
			return true
		}
	}
	return isSilentNoReplyRationalePrefix(normalized)
}

func isSilentNoReplyRationalePrefix(normalized string) bool {
	if strings.HasPrefix(normalized, "not posting -") {
		return true
	}
	if strings.HasPrefix(normalized, "不发布-") || strings.HasPrefix(normalized, "不发布，") || strings.HasPrefix(normalized, "不发布,") {
		return true
	}
	return false
}

func normalizeSilentNoReplyOutput(output string) string {
	normalized := strings.ToLower(strings.TrimSpace(output))
	normalized = strings.ReplaceAll(normalized, "—", "-")
	normalized = strings.ReplaceAll(normalized, "–", "-")
	for strings.Contains(normalized, "--") {
		normalized = strings.ReplaceAll(normalized, "--", "-")
	}
	normalized = strings.Join(strings.Fields(normalized), " ")
	normalized = strings.TrimSpace(normalized)
	for {
		trimmed := strings.TrimSpace(strings.TrimRight(normalized, ".。！!"))
		if trimmed == normalized {
			break
		}
		normalized = trimmed
	}
	return normalized
}
