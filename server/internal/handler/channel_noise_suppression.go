package handler

// channel_noise_suppression.go
//
// System-level no-op / echo suppression (LRM-1523 + LRM-1529). The goal is
// "回声归零": a pure acknowledgement / greeting / status message that carries
// no new information must not produce a wake that re-triggers another agent.
//
// LRM-1529 adds structured agent output kinds. Resolution order:
//   1. agent structured kind
//   2. system-internal kind
//   3. legacy lexicon fallback
//   4. default content
// Each resolution is persisted as kind_source for observability.

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

// channelMessageKindHint carries an optional structured / system kind into the
// insert path without forcing every caller to invent its own classifier.
type channelMessageKindHint struct {
	Kind   string
	Source string // structured | system; empty means derive Source with Kind
}

// channelMessageKindResolution is the persisted (kind, kind_source) pair.
type channelMessageKindResolution struct {
	Kind   string
	Source string
}

// channelMessageIsPureConfirmation classifies a channel message as a pure
// acknowledgement with no new actionable content. When true, a directed wake
// must not be produced for its @-targets.
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

// channelMessageKindFor is the legacy helper kept for call sites that only need
// the kind string. Prefer resolveChannelMessageKind when kind_source matters.
func channelMessageKindFor(authorType, content string, parts []protocol.MessagePart) string {
	return resolveChannelMessageKind(authorType, content, parts, channelMessageKindHint{}).Kind
}

// resolveChannelMessageKind derives kind + kind_source (LRM-1529).
func resolveChannelMessageKind(authorType, content string, parts []protocol.MessagePart, hint channelMessageKindHint) channelMessageKindResolution {
	kind := protocol.NormalizeChannelMessageKind(hint.Kind)
	source := strings.TrimSpace(strings.ToLower(hint.Source))

	// 1. Explicit structured / system hint.
	if kind != "" {
		switch source {
		case protocol.ChannelMessageKindSourceStructured:
			if kind != protocol.ChannelMessageKindSystemReminder {
				return channelMessageKindResolution{Kind: kind, Source: source}
			}
		case protocol.ChannelMessageKindSourceSystem:
			return channelMessageKindResolution{Kind: kind, Source: source}
		case "":
			if kind == protocol.ChannelMessageKindSystemReminder {
				return channelMessageKindResolution{
					Kind:   kind,
					Source: protocol.ChannelMessageKindSourceSystem,
				}
			}
			if authorType == "agent" {
				return channelMessageKindResolution{
					Kind:   kind,
					Source: protocol.ChannelMessageKindSourceStructured,
				}
			}
		}
	}

	// 2. System-internal classification.
	if authorType == "system" {
		if channelPartsHaveSystemReminder(parts) {
			return channelMessageKindResolution{
				Kind:   protocol.ChannelMessageKindSystemReminder,
				Source: protocol.ChannelMessageKindSourceSystem,
			}
		}
		return channelMessageKindResolution{
			Kind:   protocol.ChannelMessageKindContent,
			Source: protocol.ChannelMessageKindSourceDefault,
		}
	}

	// 3. Legacy lexicon fallback (agent-authored pure ack only).
	if authorType == "agent" && channelContentIsPureConfirmation(content, parts) {
		return channelMessageKindResolution{
			Kind:   protocol.ChannelMessageKindConfirmation,
			Source: protocol.ChannelMessageKindSourceLexicon,
		}
	}

	// 4. Default content.
	return channelMessageKindResolution{
		Kind:   protocol.ChannelMessageKindContent,
		Source: protocol.ChannelMessageKindSourceDefault,
	}
}

func channelPartsHaveSystemReminder(parts []protocol.MessagePart) bool {
	for _, part := range parts {
		if part.Type != protocol.MessagePartTypeSystemEvent {
			continue
		}
		event := strings.TrimSpace(strings.ToLower(part.Event))
		if strings.Contains(event, "reminder") {
			return true
		}
	}
	return false
}

// channelMessageIsConfirmationNoWake reports whether a channel message must be
// treated as observe-only (no agent wake). Persisted observe-only kinds are
// authoritative; legacy rows fall back to the runtime lexicon classifier.
func channelMessageIsConfirmationNoWake(trigger ChannelMessageResponse) bool {
	if protocol.ChannelMessageKindIsObserveOnly(trigger.Kind) {
		return true
	}
	switch trigger.Kind {
	case protocol.ChannelMessageKindContent,
		protocol.ChannelMessageKindHandoff,
		protocol.ChannelMessageKindDelegation,
		protocol.ChannelMessageKindReview,
		protocol.ChannelMessageKindDeliverable:
		return false
	}
	pure, _ := channelMessageIsPureConfirmation(trigger)
	return pure
}

// channelContentIsPureConfirmation decides whether the substantive text of a
// message is nothing more than a confirmation.
func channelContentIsPureConfirmation(content string, parts []protocol.MessagePart) bool {
	body := stripMentionLabels(content, parts)
	body = strings.TrimSpace(body)
	if body == "" {
		return false
	}
	if strings.ContainsAny(strings.ToLower(body), "?？") {
		return false
	}
	return channelConfirmationSupportingContent(body)
}

func channelConfirmationSupportingContent(body string) bool {
	normalized := normalizeConfirmationBody(body)
	if normalized == "" {
		return false
	}
	return confirmationLexiconMatch(normalized)
}

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

func normalizeConfirmationBody(body string) string {
	return strings.ToLower(strings.TrimSpace(body))
}

var channelMentionMarkerRe = util.MentionRe

func stripMentionLabels(content string, _ []protocol.MessagePart) string {
	out := content
	out = channelMentionMarkerRe.ReplaceAllString(out, "")
	return strings.TrimSpace(out)
}
