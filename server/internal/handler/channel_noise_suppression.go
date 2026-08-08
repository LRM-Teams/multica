package handler

// channel_noise_suppression.go
//
// System-level no-op / echo suppression (LRM-1523). The goal is "回声归零":
// a pure acknowledgement / greeting / status message (收到 / 明白 / OK /
// 好的 / 已办理 / thanks …) that carries no new information, no @-activated
// actionable directive and no concrete next action must not produce a
// "wake-required" message that re-triggers another agent and feeds the
// confirmation echo chain.
//
// The classifier is deliberately CONSERVATIVE (high precision): it only fires
// when the substantive content, after stripping structured mentions and normal
// conversational glue, matches a narrow confirmation lexicon. Any message that
// contains recognisable new information, a question, an actionable directive,
// or is long enough to plausibly carry content is NOT classified as pure
// confirmation — so real task hand-off, decisions and work updates are never
// swallowed.

import (
	"strings"

	"github.com/multica-ai/multica/server/internal/util"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// channelAmbientSkipReasonNonAction is the observable "silence reason" used
// when a pure confirmation / no-op message is suppressed instead of waking an
// agent. It is surfaced through the ambient-gate decision metric seam so a
// drained flow ("静默原因可查") can see exactly why a message produced no run.
const channelAmbientSkipReasonNonAction = "non_action"

// confirmationLexicon lists confirmation / acknowledgement phrases that, when
// used as the whole substantive content of a message, do not add information.
// Dialects: 简体中文 + English. Keep entries short and unambiguous; a message
// that merely contains one of these as part of a larger reply is not skipped.
var confirmationLexicon = []string{
	// 中文
	"收到", "明白", "确认", "好的", "好", "知道了", "了解", "没问题", "可以",
	"已收到", "已确认", "已了解", "收到明白", "明白了", "收到收到",
	"已办理", "已处理", "已完成确认", "好的收到", "好的明白", "收到确认",
	"了解收到", "明白收到",
	// English
	"ok", "okay", "got it", "gotit", "noted", "roger", "understood", "ack",
	"thanks", "thank you", "thx", "thank", "received", "confirmed", "sure",
	"copy",
	// Shared / casual
	"👍",
}

// channelMessageIsPureConfirmation classifies a channel message as a pure
// acknowledgement with no new actionable content. When true, a directed wake
// must not be produced for its @-targets (concept "confirmation 不唤醒任何
// agent，连 @@ 目标也不唤醒"), because doing so would let an ack re-trigger the
// acknowledged agent and start an echo chain.
//
// It returns (pure, hasAgentMention). hasAgentMention is reported separately so
// callers can distinguish "standalone ack" from "ack directed at another agent"
// without re-parsing mentions.
func channelMessageIsPureConfirmation(trigger ChannelMessageResponse) (pure bool, hasAgentMention bool) {
	mentions := util.ParseMentionsFromContentAndParts(trigger.Content, trigger.Parts)
	for _, m := range mentions {
		if m.Type == "agent" {
			hasAgentMention = true
			break
		}
	}
	return channelContentIsPureConfirmation(trigger.Content, trigger.Parts), hasAgentMention
}

// channelContentIsPureConfirmation decides whether the substantive text of a
// message is nothing more than a confirmation. Structured mentions (the
// [@A](mention://agent/..) markers) and the mention labels inside the visible
// content are stripped before the confirmation check so that "收到 @A" is still
// recognised as a pure ack to A.
func channelContentIsPureConfirmation(content string, parts []protocol.MessagePart) bool {
	body := stripMentionLabels(content, parts)
	body = strings.TrimSpace(body)
	if body == "" {
		return false
	}
	// A question or request marker signals new actionable content and forces
	// the message out of the confirmation class.
	if strings.ContainsAny(strings.ToLower(body), "?？") {
		return false
	}
	return channelConfirmationSupportingContent(body)
}

// channelConfirmationSupportingContent reports whether body still contains
// substantive content beyond a pure confirmation phrase.
func channelConfirmationSupportingContent(body string) bool {
	normalized := normalizeConfirmationBody(body)
	if normalized == "" {
		return false
	}
	// Require the whole (normalised) body to be a confirmation phrase; if any
	// leftover non-glue token remains after the longest matching phrase, the
	// message carries extra content and is not a pure confirmation.
	return confirmationLexiconMatch(normalized)
}

// confirmationLexiconMatch reports whether the normalised body consists only of
// confirmation phrases (and glue) from the lexicon. Matching is greedy and
// picks the longest matching phrase at each position so that overlapping
// lexemes (e.g. "ok" vs "okay", "好" vs "好的") are resolved in favour of the
// longer form instead of leaving dangling leftovers.
func confirmationLexiconMatch(normalized string) bool {
	remainder := normalized
	for remainder != "" {
		stripped := strings.TrimLeft(remainder, " ！!~～啦了哟哦嗯。，,.")
		if stripped != remainder {
			remainder = stripped
			continue
		}
		longest := ""
		for _, phrase := range confirmationLexicon {
			if phrase == "" {
				continue
			}
			if strings.HasPrefix(remainder, phrase) && len([]rune(phrase)) > len([]rune(longest)) {
				longest = phrase
			}
		}
		if longest == "" {
			return false
		}
		remainder = strings.TrimPrefix(remainder, longest)
	}
	return true
}

// normalizeConfirmationBody lowercases and trims the body so lexicon matching
// tolerates capitalisation and surrounding glue while preserving internal
// whitespace (so multi-word English phrases like "got it" still match).
func normalizeConfirmationBody(body string) string {
	return strings.ToLower(strings.TrimSpace(body))
}

// stripMentionLabels removes structured mention markers and their visible
// labels from the content so an ack addressing "@A" still reads as an ack.
// We strip the canonical [@Label](mention://type/id) markdown plus any leading
// @Label so the remaining body is agent-mention-free for the pure-ack check.
var channelMentionMarkerRe = util.MentionRe

func stripMentionLabels(content string, _ []protocol.MessagePart) string {
	out := content
	// remove the markdown [@Label](mention://type/id) / [Label](mention://...) tokens
	out = channelMentionMarkerRe.ReplaceAllString(out, "")
	return strings.TrimSpace(out)
}
