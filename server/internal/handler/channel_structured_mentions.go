package handler

import (
	"context"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type channelMentionCandidate struct {
	Type  string
	ID    string
	Label string
}

func (h *Handler) enrichChannelMessageMentions(ctx context.Context, ch ChannelResponse, content string, parts []protocol.MessagePart) (string, []protocol.MessagePart) {
	if ch.Kind != "group" {
		return content, parts
	}
	parts = appendMissingMentionParts(parts, legacyChannelMentionMarkdownReferenceParts(content, parts))
	candidates := h.channelMentionCandidates(ctx, ch.WorkspaceID, ch.ID)
	if len(candidates) > 0 {
		mentions := h.resolveBareChannelMentions(content, parts, candidates)
		parts = appendMissingMentionParts(parts, mentions)
	}
	return normalizeChannelMentionMarkdownText(content), normalizeChannelMentionMarkdownParts(parts)
}

func (h *Handler) channelMentionCandidates(ctx context.Context, workspaceID, channelID string) map[string]channelMentionCandidate {
	rows, err := h.DB.Query(ctx, `
		SELECT cm.member_type, cm.member_id,
		       COALESCE(u.name, a.name, ''),
		       COALESCE(NULLIF(u.display_name, ''), NULLIF(a.display_name, ''), '')
		FROM channel_member cm
		LEFT JOIN "user" u ON cm.member_type = 'user' AND u.id = cm.member_id
		LEFT JOIN agent a ON cm.member_type = 'agent' AND a.id = cm.member_id
		WHERE cm.channel_id = $1 AND cm.workspace_id = $2
		  AND (cm.member_type = 'user' OR (cm.member_type = 'agent' AND a.archived_at IS NULL))`,
		parseUUID(channelID), parseUUID(workspaceID))
	if err != nil {
		return nil
	}
	defer rows.Close()

	candidates := map[string]channelMentionCandidate{
		"all": {Type: "all", ID: "all", Label: "all"},
	}
	ambiguous := map[string]bool{}
	for rows.Next() {
		var memberType, name, displayName string
		var memberID pgtype.UUID
		if err := rows.Scan(&memberType, &memberID, &name, &displayName); err != nil {
			continue
		}
		mentionType := "member"
		if memberType == "agent" {
			mentionType = "agent"
		}
		candidate := channelMentionCandidate{Type: mentionType, ID: uuidToString(memberID), Label: firstNonEmpty(displayName, name)}
		for _, label := range []string{name, displayName} {
			key := normalizeMentionCandidateLabel(label)
			if key == "" {
				continue
			}
			if key == "all" {
				continue
			}
			if existing, ok := candidates[key]; ok && (existing.Type != candidate.Type || existing.ID != candidate.ID) {
				delete(candidates, key)
				ambiguous[key] = true
				continue
			}
			if ambiguous[key] {
				continue
			}
			candidates[key] = candidate
		}
	}
	return candidates
}

func (h *Handler) resolveBareChannelMentions(content string, parts []protocol.MessagePart, candidates map[string]channelMentionCandidate) []protocol.MessagePart {
	seen := map[string]bool{}
	out := make([]protocol.MessagePart, 0)
	for _, mention := range util.ParseMentionsFromContentAndParts(content, parts) {
		key := mention.Type + ":" + mention.ID
		seen[key] = true
	}
	for _, text := range mentionSourceTexts(content, parts) {
		for _, candidate := range findBareMentionCandidates(text, candidates) {
			key := candidate.Type + ":" + candidate.ID
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, protocol.MessagePart{
				Type:       protocol.MessagePartTypeReference,
				RefType:    "mention",
				RefSubType: candidate.Type,
				RefID:      candidate.ID,
				Label:      candidate.Label,
			})
		}
	}
	return out
}

func mentionSourceTexts(content string, parts []protocol.MessagePart) []string {
	texts := make([]string, 0, len(parts)+1)
	if strings.TrimSpace(content) != "" {
		texts = append(texts, content)
	}
	for _, part := range parts {
		if part.Type == protocol.MessagePartTypeText && strings.TrimSpace(part.Text) != "" {
			texts = append(texts, part.Text)
		}
	}
	return texts
}

func findBareMentionCandidates(content string, candidates map[string]channelMentionCandidate) []channelMentionCandidate {
	if !strings.Contains(content, "@") {
		return nil
	}
	labels := make([]string, 0, len(candidates))
	for label := range candidates {
		labels = append(labels, label)
	}
	sort.Slice(labels, func(i, j int) bool {
		return len([]rune(labels[i])) > len([]rune(labels[j]))
	})

	lowerContent := strings.ToLower(content)
	seen := map[string]bool{}
	out := make([]channelMentionCandidate, 0)
	for start := 0; start < len(lowerContent); {
		at := strings.IndexByte(lowerContent[start:], '@')
		if at < 0 {
			break
		}
		at += start
		if !mentionHandleBoundaryBefore(lowerContent, at) {
			start = at + 1
			continue
		}
		matchEnd := at + 1
		for _, label := range labels {
			begin := at + 1
			end := begin + len(label)
			if end > len(lowerContent) || lowerContent[begin:end] != label {
				continue
			}
			if !mentionHandleBoundaryAfter(lowerContent, end) {
				continue
			}
			candidate := candidates[label]
			key := candidate.Type + ":" + candidate.ID
			if !seen[key] {
				seen[key] = true
				out = append(out, candidate)
			}
			matchEnd = end
			break
		}
		start = matchEnd
	}
	return out
}

func normalizeMentionCandidateLabel(label string) string {
	return strings.ToLower(strings.TrimSpace(label))
}

func appendMissingMentionParts(parts []protocol.MessagePart, mentions []protocol.MessagePart) []protocol.MessagePart {
	seen := map[string]bool{}
	for _, part := range parts {
		if part.Type != protocol.MessagePartTypeReference || part.RefType != "mention" {
			continue
		}
		seen["mention:"+part.RefSubType+":"+part.RefID] = true
	}
	out := append([]protocol.MessagePart{}, parts...)
	for _, mention := range mentions {
		key := mention.RefType + ":" + mention.RefSubType + ":" + mention.RefID
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, mention)
	}
	return out
}

func legacyChannelMentionMarkdownReferenceParts(content string, parts []protocol.MessagePart) []protocol.MessagePart {
	seen := map[string]bool{}
	out := make([]protocol.MessagePart, 0)
	for _, text := range mentionSourceTexts(content, parts) {
		for _, match := range util.MentionRe.FindAllStringSubmatch(text, -1) {
			if len(match) < 4 || match[2] == "issue" {
				continue
			}
			key := match[2] + ":" + match[3]
			if seen[key] {
				continue
			}
			seen[key] = true
			label := strings.ReplaceAll(match[1], `\[`, `[`)
			label = strings.ReplaceAll(label, `\]`, `]`)
			if !strings.HasPrefix(label, "@") {
				label = "@" + label
			}
			out = append(out, protocol.MessagePart{
				Type:       protocol.MessagePartTypeReference,
				RefType:    "mention",
				RefSubType: match[2],
				RefID:      match[3],
				Label:      label,
			})
		}
	}
	return out
}

func normalizeChannelMentionMarkdownParts(parts []protocol.MessagePart) []protocol.MessagePart {
	if len(parts) == 0 {
		return parts
	}
	var out []protocol.MessagePart
	for i, part := range parts {
		if part.Type != protocol.MessagePartTypeText || !strings.Contains(part.Text, "mention://") {
			continue
		}
		if out == nil {
			out = append([]protocol.MessagePart{}, parts...)
		}
		out[i].Text = normalizeChannelMentionMarkdownText(part.Text)
	}
	if out != nil {
		return out
	}
	return parts
}

func normalizeChannelMentionMarkdownText(text string) string {
	if !strings.Contains(text, "mention://") {
		return text
	}
	return util.MentionRe.ReplaceAllStringFunc(text, func(match string) string {
		parsed := util.MentionRe.FindStringSubmatch(match)
		if len(parsed) < 4 {
			return match
		}
		mentionType := parsed[2]
		if mentionType == "issue" {
			return match
		}
		label := strings.ReplaceAll(parsed[1], `\[`, `[`)
		label = strings.ReplaceAll(label, `\]`, `]`)
		if !strings.HasPrefix(label, "@") {
			label = "@" + label
		}
		return label
	})
}
