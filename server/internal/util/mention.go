package util

import (
	"regexp"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

// Mention represents a parsed @mention from markdown content.
type Mention struct {
	Type string // "member", "agent", "issue", or "all"
	ID   string // user_id, agent_id, issue_id, or "all"
}

// MentionRe matches [@Label](mention://type/id) or [Label](mention://issue/id) in markdown.
// The @ prefix is optional to support issue mentions which use [MUL-123](mention://issue/...).
// Uses .+? (non-greedy) instead of [^\]]* so labels containing square brackets
// (e.g. "David[TF]") are matched correctly — the ](mention:// anchor is specific
// enough to prevent over-matching.
var MentionRe = regexp.MustCompile(`\[@?(.+?)\]\(mention://(member|agent|squad|issue|run|all)/([0-9a-fA-F-]+|all)\)`)

// IsMentionAll returns true if the mention is an @all mention.
func (m Mention) IsMentionAll() bool {
	return m.Type == "all"
}

// ParseMentions extracts deduplicated mentions from markdown content.
func ParseMentions(content string) []Mention {
	matches := MentionRe.FindAllStringSubmatch(content, -1)
	collector := newMentionCollector()
	for _, m := range matches {
		collector.add(Mention{Type: m[2], ID: m[3]})
	}
	return collector.result
}

// ParseMentionsFromContentAndParts extracts deduplicated channel mentions
// exclusively from structured reference message parts. Legacy markdown is
// migrated once at persistence time and is never reinterpreted by readers.
func ParseMentionsFromContentAndParts(_ string, parts []protocol.MessagePart) []Mention {
	collector := newMentionCollector()
	for _, part := range parts {
		if part.Type != protocol.MessagePartTypeReference {
			continue
		}
		if part.RefType != "mention" {
			continue
		}
		if part.RefSubType == "" || part.RefID == "" || part.RefSubType == "all" {
			continue
		}
		collector.add(Mention{Type: part.RefSubType, ID: part.RefID})
	}
	return collector.result
}

// mentionCollector centralizes the ordering and deduplication contract shared
// by markdown and structured-reference parsing.
type mentionCollector struct {
	result []Mention
	seen   map[string]struct{}
}

func newMentionCollector() mentionCollector {
	return mentionCollector{seen: make(map[string]struct{})}
}

func (c *mentionCollector) add(mention Mention) {
	key := mention.Type + ":" + mention.ID
	if _, exists := c.seen[key]; exists {
		return
	}
	c.seen[key] = struct{}{}
	c.result = append(c.result, mention)
}

// HasMentionAll returns true if any mention in the slice is an @all mention.
func HasMentionAll(mentions []Mention) bool {
	for _, m := range mentions {
		if m.IsMentionAll() {
			return true
		}
	}
	return false
}
