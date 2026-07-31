package handler

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/mention"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type channelMentionCandidate struct {
	Type   string
	ID     string
	Handle string
	Label  string
}

type channelMentionOccurrence struct {
	Candidate channelMentionCandidate
	Start     int
	End       int
}

// finalizeAgentChannelMessage resolves the server-authored structured facts for
// an agent's visible message in its final destination. Callers must resolve a
// destination before calling this helper: mention eligibility is a property of
// the receiving channel, never of the channel that woke the agent.
//
// messageparts.Normalize clears caller-provided source ranges before this point,
// and enrichChannelMessageMentions only appends verified destination-scoped
// anchors. This keeps persistence, dispatch, realtime, and Feishu on one
// canonical set of parts.
func (h *Handler) finalizeAgentChannelMessage(ctx context.Context, ch ChannelResponse, content string, parts []protocol.MessagePart) (string, []protocol.MessagePart, error) {
	parts = markAgentVoiceSynthesisPending(parts)
	if ch.Kind != "group" {
		// Direct messages have no group-member resolver. Keep ordinary parts, but
		// never persist a caller-supplied reference sidecar without a server
		// verified source anchor.
		return content, appendReferenceOccurrences(parts, nil), nil
	}
	return h.enrichChannelMessageMentions(ctx, ch, content, parts)
}

func markAgentVoiceSynthesisPending(parts []protocol.MessagePart) []protocol.MessagePart {
	if !channelMessageHasVoicePart(parts) {
		return parts
	}
	out := append([]protocol.MessagePart(nil), parts...)
	for i := range out {
		if out[i].Type != protocol.MessagePartTypeVoice {
			continue
		}
		// Runtime-supplied audio is not a trusted TTS artifact. The canonical
		// server synthesizes from the finalized transcript and owns metadata.
		out[i].AttachmentID = ""
		out[i].Filename = ""
		out[i].ContentType = ""
		out[i].SizeBytes = 0
		out[i].DurationMS = 0
		out[i].TranscriptionStatus = ""
		out[i].SynthesisStatus = protocol.VoiceSynthesisPending
	}
	return out
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
	parts = appendReferenceOccurrences(parts, h.resolveChannelReferenceLinks(ctx, ch.WorkspaceID, content))
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
			ContentStartUTF16: &start,
			ContentEndUTF16:   &end,
		})
	}
	return out
}

// channelReferenceLinkRe matches the composer's [Label](mention://channel/<id>)
// markdown link (ChannelReferenceExtension.renderMarkdown, packages/views).
// Unlike @mention/#issue, the composer already resolved a real channel ID
// client-side (picked from its own cached channel list) — there is no bare-text
// form to fuzzy-match here, only a link to verify and anchor.
var channelReferenceLinkRe = regexp.MustCompile(`\[((?:\\.|[^\]])+)\]\(mention://channel/([0-9a-fA-F-]+)\)`)

// resolveChannelReferenceLinks verifies each composer-authored channel-reference
// link still points at a real, non-DM channel in this workspace and anchors it
// to its exact source span. task #912: server-resolved counterpart to
// packages/views' ChannelReferenceExtension (Felix, PR #1607, FE-only until
// this lands). A dangling reference (channel deleted, or somehow cross-
// workspace) is silently dropped — the composer already prevents authoring
// one, and messageparts.Normalize's own reference-verification discipline
// (see appendReferenceOccurrences) is "never persist what we can't verify",
// not "error the whole send over a stale link".
func (h *Handler) resolveChannelReferenceLinks(ctx context.Context, workspaceID, content string) []protocol.MessagePart {
	matches := channelReferenceLinkRe.FindAllStringSubmatchIndex(content, -1)
	if len(matches) == 0 {
		return nil
	}
	out := make([]protocol.MessagePart, 0, len(matches))
	for _, match := range matches {
		start, end := match[0], match[1]
		rawLabel := content[match[2]:match[3]]
		channelIDStr := content[match[4]:match[5]]
		channelID, err := util.ParseUUID(channelIDStr)
		if err != nil {
			continue
		}
		ch, ok := h.getChannel(ctx, workspaceID, channelID)
		if !ok || ch.Kind != "group" {
			continue
		}
		startUTF16, endUTF16 := contentUTF16Span(content, start, end)
		out = append(out, protocol.MessagePart{
			Type:              protocol.MessagePartTypeReference,
			RefType:           "channel-ref",
			RefID:             ch.ID,
			Label:             unescapeChannelReferenceLabel(rawLabel),
			ContentStartUTF16: &startUTF16,
			ContentEndUTF16:   &endUTF16,
		})
	}
	return out
}

// unescapeChannelReferenceLabel reverses the composer's renderMarkdown escaping
// of literal brackets in a label (ChannelReferenceExtension, packages/views).
func unescapeChannelReferenceLabel(label string) string {
	label = strings.ReplaceAll(label, `\[`, "[")
	label = strings.ReplaceAll(label, `\]`, "]")
	return label
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
		handle := strings.TrimSpace(name)
		if mentionType == "agent" && validateIdentityHandle(handle) != nil {
			// Agent handles are canonical ASCII usernames. Invalid historical
			// values deliberately remain plain text in old messages after the
			// one-time backfill; do not retain a second mention-routing path.
			continue
		}
		key := normalizeMentionCandidateLabel(handle)
		if key == "" || key == "all" {
			continue
		}
		candidate := channelMentionCandidate{Type: mentionType, ID: uuidToString(memberID), Handle: handle, Label: firstNonEmpty(displayName, name)}
		if existing, ok := candidates[key]; ok && (existing.Type != candidate.Type || existing.ID != candidate.ID) {
			delete(candidates, key)
			ambiguous[key] = true
			continue
		}
		if !ambiguous[key] {
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
		return utf8.RuneCountInString(candidates[labels[i]].Handle) > utf8.RuneCountInString(candidates[labels[j]].Handle)
	})

	out := make([]channelMentionOccurrence, 0)
	for start := 0; start < len(content); {
		at := strings.IndexByte(content[start:], '@')
		if at < 0 {
			break
		}
		at += start
		if !mentionHandleBoundaryBefore(content, at) {
			start = at + 1
			continue
		}
		matchEnd := at + 1
		for _, label := range labels {
			candidate := candidates[label]
			end, ok := mentionHandlePrefix(content, at+1, candidate.Handle)
			if !ok {
				continue
			}
			if !mentionHandleBoundaryAfter(content, end) {
				continue
			}
			out = append(out, channelMentionOccurrence{Candidate: candidate, Start: at, End: end})
			matchEnd = end
			break
		}
		start = matchEnd
	}
	return out
}

// mentionHandlePrefix compares one handle at a time without changing original
// byte offsets. Agent candidates are canonical ASCII usernames; member
// candidates remain Unicode-capable because user handles are a separate
// identity contract.
func mentionHandlePrefix(content string, start int, handle string) (int, bool) {
	offset := start
	for _, want := range handle {
		if offset >= len(content) {
			return 0, false
		}
		got, size := utf8.DecodeRuneInString(content[offset:])
		if size == 0 || !strings.EqualFold(string(got), string(want)) {
			return 0, false
		}
		offset += size
	}
	return offset, true
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
	// Normalization intentionally clears caller-supplied source ranges: a
	// reference becomes displayable only after this server-side resolver has
	// verified both its target and its exact source occurrence. Drop those
	// unanchored sidecars rather than persisting a stale duplicate next to the
	// verified reference below.
	out := make([]protocol.MessagePart, 0, len(parts)+len(references))
	for _, part := range parts {
		if part.Type == protocol.MessagePartTypeReference && (part.ContentStartUTF16 == nil || part.ContentEndUTF16 == nil) {
			continue
		}
		out = append(out, part)
	}
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
