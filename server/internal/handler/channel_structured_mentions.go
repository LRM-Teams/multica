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
	if ch.Kind == "group" {
		if containsLegacyChannelActorMention(content) || channelPartsContainLegacyActorMention(parts) {
			return "", nil, fmt.Errorf("legacy actor mention syntax is unsupported; use @handle")
		}
		candidates := h.channelMentionCandidates(ctx, ch.WorkspaceID, ch.ID)
		if len(candidates) > 0 {
			mentions := h.resolveBareChannelMentions(content, parts, candidates)
			parts = appendReferenceOccurrences(parts, mentions)
		}
	}
	// Issue and channel references are workspace-scoped reading affordances, not
	// group-membership delivery. Resolve them in DMs too so a structured
	// channel link or a known bare #channel renders identically on both surfaces.
	// appendReferenceOccurrences also strips unverified caller sidecars.
	if strings.TrimSpace(ch.WorkspaceID) == "" {
		return content, appendReferenceOccurrences(parts, nil), nil
	}
	if ch.Kind != "group" {
		// DMs accept only an explicit channel wire link that the server can verify
		// into a channel-ref MessagePart. Bare #names stay plain text by product
		// contract; actor mention delivery remains group-only.
		return content, appendReferenceOccurrences(parts, h.resolveChannelReferenceLinks(ctx, ch.WorkspaceID, content)), nil
	}
	parts = appendReferenceOccurrences(parts, h.resolveBareChannelIssueReferences(ctx, ch.WorkspaceID, content, parts))
	parts = appendReferenceOccurrences(parts, h.resolveChannelReferenceLinks(ctx, ch.WorkspaceID, content))
	// Bare `#name` runs after the link form so an explicit composer link always
	// wins the span it already owns (appendReferenceOccurrences keeps the first
	// verified anchor per span).
	parts = appendReferenceOccurrences(parts, h.resolveBareChannelReferences(ctx, ch.WorkspaceID, content))
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

type channelReferenceCandidate struct {
	ID   string
	Name string
}

type channelReferenceOccurrence struct {
	Candidate channelReferenceCandidate
	Start     int
	End       int
}

// resolveBareChannelReferences attaches durable channel IDs to bare `#name`
// tokens in group messages (LRM-1153). The composer's picker path
// (resolveChannelReferenceLinks) only covers text a human authored through the
// suggestion menu; agents and hand-typed prose write the plain `#name` that
// every reader already reads as a channel, and before this resolver those
// occurrences carried no parts at all — so the FE's ChannelRefLink had nothing
// to anchor and the raw `#name` shipped as prose next to correctly chipped
// @mentions and issue refs from the very same message.
//
// Discovery is candidate-driven rather than regex-driven, exactly like
// resolveBareChannelMentions: channel names are unique per workspace but may
// contain Unicode, so matching known names (longest first) is what keeps
// `#1973` (a PR number), `#todo` (no such channel), and a DM's synthetic name
// from being guessed into links. The visible text stays exactly as authored.
func (h *Handler) resolveBareChannelReferences(ctx context.Context, workspaceID, content string) []protocol.MessagePart {
	if !strings.Contains(content, "#") {
		return nil
	}
	candidates := h.channelReferenceCandidates(ctx, workspaceID)
	if len(candidates) == 0 {
		return nil
	}
	out := make([]protocol.MessagePart, 0)
	for _, occurrence := range findBareChannelReferences(content, candidates) {
		start, end := contentUTF16Span(content, occurrence.Start, occurrence.End)
		out = append(out, protocol.MessagePart{
			Type:              protocol.MessagePartTypeReference,
			RefType:           "channel-ref",
			RefID:             occurrence.Candidate.ID,
			Label:             occurrence.Candidate.Name,
			ContentStartUTF16: &start,
			ContentEndUTF16:   &end,
		})
	}
	return out
}

// channelReferenceCandidates lists the linkable group channels of a workspace
// keyed by lowercased name. DMs are excluded for the same reason
// resolveChannelReferenceLinks drops them (private 1:1s are not a shareable
// target, and their names are synthetic `dm:...` keys), and archived channels
// are excluded because a chip that navigates nowhere useful is worse than the
// author's own plain text.
func (h *Handler) channelReferenceCandidates(ctx context.Context, workspaceID string) map[string]channelReferenceCandidate {
	rows, err := h.DB.Query(ctx, `
		SELECT id::text, name
		FROM channel
		WHERE workspace_id = $1 AND kind = 'group' AND archived_at IS NULL`,
		parseUUID(workspaceID))
	if err != nil {
		return nil
	}
	defer rows.Close()

	candidates := map[string]channelReferenceCandidate{}
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			continue
		}
		name = strings.TrimSpace(name)
		key := normalizeMentionCandidateLabel(name)
		if key == "" {
			continue
		}
		candidates[key] = channelReferenceCandidate{ID: id, Name: name}
	}
	return candidates
}

// findBareChannelReferences walks each `#` in content and takes the longest
// candidate name that matches at that position with handle boundaries on both
// sides, so `#pr` never steals the front of `#pr-frontend`. Code spans and text
// already owned by a markdown link are skipped: the explicit
// `[Label](mention://channel/<id>)` form is resolved by
// resolveChannelReferenceLinks and must not be anchored twice.
func findBareChannelReferences(content string, candidates map[string]channelReferenceCandidate) []channelReferenceOccurrence {
	keys := make([]string, 0, len(candidates))
	for key := range candidates {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		return utf8.RuneCountInString(candidates[keys[i]].Name) > utf8.RuneCountInString(candidates[keys[j]].Name)
	})
	skipRegions := mention.FindLiteralSkipRegions(content)

	out := make([]channelReferenceOccurrence, 0)
	for start := 0; start < len(content); {
		hash := strings.IndexByte(content[start:], '#')
		if hash < 0 {
			break
		}
		hash += start
		if !mentionHandleBoundaryBefore(content, hash) || mention.InLiteralSkipRegion(hash, skipRegions) {
			start = hash + 1
			continue
		}
		matchEnd := hash + 1
		for _, key := range keys {
			candidate := candidates[key]
			end, ok := mentionHandlePrefix(content, hash+1, candidate.Name)
			if !ok || !mentionHandleBoundaryAfter(content, end) {
				continue
			}
			if mention.IsInsideMarkdownLink(content, hash, end) {
				continue
			}
			out = append(out, channelReferenceOccurrence{Candidate: candidate, Start: hash, End: end})
			matchEnd = end
			break
		}
		start = matchEnd
	}
	return out
}

func (h *Handler) channelMentionCandidates(ctx context.Context, workspaceID, channelID string) map[string]channelMentionCandidate {
	// Workspace-scoped candidates for group @ resolution (Raft-style):
	// resolve workspace members/agents even when not yet in the channel.
	// Delivery still requires membership — see undeliveredMentionsForMessage.
	// channelID is unused for group candidate expansion (workspace scope).
	_ = channelID
	// Workspace members (users)
	userRows, err := h.DB.Query(ctx, `
		SELECT m.user_id, COALESCE(u.name, ''), COALESCE(NULLIF(u.display_name, ''), '')
		FROM member m
		JOIN "user" u ON u.id = m.user_id
		WHERE m.workspace_id = $1`, parseUUID(workspaceID))
	if err != nil {
		return nil
	}
	defer userRows.Close()

	candidates := map[string]channelMentionCandidate{}
	ambiguous := map[string]bool{}
	add := func(mentionType, id, name, displayName string) {
		handle := strings.TrimSpace(name)
		if mentionType == "agent" && validateIdentityHandle(handle) != nil {
			return
		}
		key := normalizeMentionCandidateLabel(handle)
		if key == "" || key == "all" {
			return
		}
		candidate := channelMentionCandidate{Type: mentionType, ID: id, Handle: handle, Label: firstNonEmpty(displayName, name)}
		if existing, ok := candidates[key]; ok && (existing.Type != candidate.Type || existing.ID != candidate.ID) {
			delete(candidates, key)
			ambiguous[key] = true
			return
		}
		if !ambiguous[key] {
			candidates[key] = candidate
		}
	}
	for userRows.Next() {
		var userID pgtype.UUID
		var name, displayName string
		if err := userRows.Scan(&userID, &name, &displayName); err != nil {
			continue
		}
		add("member", uuidToString(userID), name, displayName)
	}

	// Workspace agents (non-archived)
	agentRows, err := h.DB.Query(ctx, `
		SELECT id, COALESCE(name, ''), COALESCE(NULLIF(display_name, ''), '')
		FROM agent
		WHERE workspace_id = $1 AND archived_at IS NULL`, parseUUID(workspaceID))
	if err != nil {
		return candidates
	}
	defer agentRows.Close()
	for agentRows.Next() {
		var agentID pgtype.UUID
		var name, displayName string
		if err := agentRows.Scan(&agentID, &name, &displayName); err != nil {
			continue
		}
		add("agent", uuidToString(agentID), name, displayName)
	}
	return candidates
}

// channelMemberMentionIDs returns user and agent IDs that are already members
// of the channel (delivery-eligible).
func (h *Handler) channelMemberMentionIDs(ctx context.Context, workspaceID, channelID string) (users, agents map[string]bool) {
	users = map[string]bool{}
	agents = map[string]bool{}
	rows, err := h.DB.Query(ctx, `
		SELECT member_type, member_id
		FROM channel_member
		WHERE channel_id = $1 AND workspace_id = $2`,
		parseUUID(channelID), parseUUID(workspaceID))
	if err != nil {
		return users, agents
	}
	defer rows.Close()
	for rows.Next() {
		var memberType string
		var memberID pgtype.UUID
		if err := rows.Scan(&memberType, &memberID); err != nil {
			continue
		}
		id := uuidToString(memberID)
		switch memberType {
		case "user":
			users[id] = true
		case "agent":
			agents[id] = true
		}
	}
	return users, agents
}

// UndeliveredMention is returned on message create when a structured @ targets
// a workspace member/agent who is not currently in the channel. Delivery is
// withheld until the sender invites (or a future notify path). Aligns with
// Raft mention undelivered / invite — no silent auto-add.
type UndeliveredMention struct {
	Type    string   `json:"type"` // member | agent
	ID      string   `json:"id"`
	Handle  string   `json:"handle,omitempty"`
	Label   string   `json:"label,omitempty"`
	Reason  string   `json:"reason"`  // not_channel_member
	Actions []string `json:"actions"` // invite (notify reserved)
}

func (h *Handler) undeliveredMentionsForMessage(ctx context.Context, ch ChannelResponse, content string, parts []protocol.MessagePart) []UndeliveredMention {
	if ch.Kind != "group" {
		return nil
	}
	mentions := util.ParseMentionsFromContentAndParts(content, parts)
	if len(mentions) == 0 {
		return nil
	}
	memberUsers, memberAgents := h.channelMemberMentionIDs(ctx, ch.WorkspaceID, ch.ID)
	// Prefer handles from workspace candidates when available
	candidates := h.channelMentionCandidates(ctx, ch.WorkspaceID, ch.ID)
	byID := map[string]channelMentionCandidate{}
	for _, c := range candidates {
		byID[c.Type+":"+c.ID] = c
	}
	seen := map[string]bool{}
	var out []UndeliveredMention
	for _, m := range mentions {
		if m.Type != "member" && m.Type != "agent" {
			continue
		}
		key := m.Type + ":" + m.ID
		if seen[key] {
			continue
		}
		inChannel := false
		if m.Type == "member" {
			inChannel = memberUsers[m.ID]
		} else {
			inChannel = memberAgents[m.ID]
		}
		if inChannel {
			continue
		}
		seen[key] = true
		u := UndeliveredMention{
			Type:    m.Type,
			ID:      m.ID,
			Reason:  "not_channel_member",
			Actions: []string{"invite"},
		}
		if c, ok := byID[key]; ok {
			u.Handle = c.Handle
			u.Label = c.Label
		}
		out = append(out, u)
	}
	return out
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

// validateDMMentionMembership rejects @ of non-participants on DM surfaces
// (private). Group channels allow workspace-scoped resolution with undelivered
// mentions instead.
func (h *Handler) validateDMMentionMembership(ctx context.Context, ch ChannelResponse, content string, parts []protocol.MessagePart) error {
	if ch.Kind != "dm" {
		return nil
	}
	mentions := util.ParseMentionsFromContentAndParts(content, parts)
	if len(mentions) == 0 {
		return nil
	}
	memberUsers, memberAgents := h.channelMemberMentionIDs(ctx, ch.WorkspaceID, ch.ID)
	for _, m := range mentions {
		switch m.Type {
		case "member":
			if !memberUsers[m.ID] {
				return fmt.Errorf("cannot mention non-participants in a DM")
			}
		case "agent":
			if !memberAgents[m.ID] {
				return fmt.Errorf("cannot mention non-participants in a DM")
			}
		}
	}
	return nil
}
