package handler

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/mention"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type channelMentionCandidate struct {
	Type  string
	ID    string
	Label string
}

type channelMentionOccurrence struct {
	Candidate channelMentionCandidate
	Start     int
	End       int
}

func (h *Handler) enrichChannelMessageMentions(ctx context.Context, ch ChannelResponse, content string, parts []protocol.MessagePart) (string, []protocol.MessagePart, error) {
	if ch.Kind != "group" {
		return content, parts, nil
	}
	if containsLegacyChannelActorMention(content) || channelPartsContainLegacyActorMention(parts) {
		return "", nil, fmt.Errorf("legacy actor mention syntax is unsupported; use @handle")
	}
	candidates := h.channelMentionCandidates(ctx, ch.WorkspaceID, ch.ID)
	if len(candidates) > 0 {
		mentions := h.resolveBareChannelMentions(content, parts, candidates)
		parts = appendReferenceOccurrences(parts, mentions)
	}
	parts = appendReferenceOccurrences(parts, h.resolveBareChannelIssueReferences(ctx, ch.WorkspaceID, content, parts))
	return content, parts, nil
}

func containsLegacyChannelActorMention(text string) bool {
	return strings.Contains(text, "mention://member/") ||
		strings.Contains(text, "mention://agent/") ||
		strings.Contains(text, "mention://squad/") ||
		strings.Contains(text, "mention://all/")
}

func channelPartsContainLegacyActorMention(parts []protocol.MessagePart) bool {
	for _, part := range parts {
		if part.Type == protocol.MessagePartTypeText && containsLegacyChannelActorMention(part.Text) {
			return true
		}
	}
	return false
}

// resolveBareChannelIssueReferences attaches durable issue IDs to bare issue
// identifiers in group messages. The visible text stays exactly as authored;
// clients render the typed issue-ref rather than trying to decorate prose.
func (h *Handler) resolveBareChannelIssueReferences(ctx context.Context, workspaceID, content string, parts []protocol.MessagePart) []protocol.MessagePart {
	workspaceUUID := parseUUID(workspaceID)
	workspace, err := h.Queries.GetWorkspace(ctx, workspaceUUID)
	if err != nil {
		return nil
	}

	out := make([]protocol.MessagePart, 0)
	for _, identifier := range mention.FindBareIssueIdentifiers(workspace.IssuePrefix, content) {
		issue, err := h.Queries.GetIssueByNumber(ctx, db.GetIssueByNumberParams{
			WorkspaceID: workspaceUUID,
			Number:      identifier.Number,
		})
		if err != nil {
			continue
		}
		start, end := contentUTF16Span(content, identifier.Start, identifier.End)
		out = append(out, protocol.MessagePart{
			Type:              protocol.MessagePartTypeReference,
			RefType:           "issue-ref",
			RefSubType:        "issue",
			RefID:             uuidToString(issue.ID),
			Label:             identifier.Label,
			RefTitle:          issue.Title,
			RefStatus:         issue.Status,
			ContentStartUTF16: &start,
			ContentEndUTF16:   &end,
		})
	}
	return out
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

	candidates := map[string]channelMentionCandidate{}
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
	out := make([]protocol.MessagePart, 0)
	for _, occurrence := range findBareMentionCandidates(content, candidates) {
		start, end := contentUTF16Span(content, occurrence.Start, occurrence.End)
		out = append(out, protocol.MessagePart{
			Type:              protocol.MessagePartTypeReference,
			RefType:           "mention",
			RefSubType:        occurrence.Candidate.Type,
			RefID:             occurrence.Candidate.ID,
			Label:             content[occurrence.Start:occurrence.End],
			ContentStartUTF16: &start,
			ContentEndUTF16:   &end,
		})
	}
	return out
}

func findBareMentionCandidates(content string, candidates map[string]channelMentionCandidate) []channelMentionOccurrence {
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
	out := make([]channelMentionOccurrence, 0)
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
			out = append(out, channelMentionOccurrence{Candidate: candidates[label], Start: at, End: end})
			matchEnd = end
			break
		}
		start = matchEnd
	}
	return out
}

func contentUTF16Span(content string, start, end int) (int, int) {
	return contentUTF16Offset(content, start), contentUTF16Offset(content, end)
}

func contentUTF16Offset(content string, byteOffset int) int {
	units := 0
	for offset, r := range content {
		if offset >= byteOffset {
			break
		}
		if r > 0xFFFF {
			units += 2
		} else {
			units++
		}
	}
	return units
}

func normalizeMentionCandidateLabel(label string) string {
	return strings.ToLower(strings.TrimSpace(label))
}

// appendReferenceOccurrences keeps one reference for every verified source
// occurrence. Notification/routing consumers remain responsible for deduping by
// actor or entity ID; display needs the per-occurrence anchor.
func appendReferenceOccurrences(parts []protocol.MessagePart, references []protocol.MessagePart) []protocol.MessagePart {
	out := append([]protocol.MessagePart{}, parts...)
	for _, reference := range references {
		if reference.ContentStartUTF16 == nil || reference.ContentEndUTF16 == nil {
			continue
		}
		duplicate := false
		for _, existing := range out {
			if existing.Type != protocol.MessagePartTypeReference || existing.RefType != reference.RefType || existing.RefSubType != reference.RefSubType || existing.RefID != reference.RefID {
				continue
			}
			if existing.ContentStartUTF16 != nil && existing.ContentEndUTF16 != nil &&
				*existing.ContentStartUTF16 == *reference.ContentStartUTF16 && *existing.ContentEndUTF16 == *reference.ContentEndUTF16 {
				duplicate = true
				break
			}
		}
		if !duplicate {
			out = append(out, reference)
		}
	}
	return out
}
