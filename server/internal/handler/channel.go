package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/integrations/lark"
	"github.com/multica-ai/multica/server/internal/messageparts"
	"github.com/multica-ai/multica/server/internal/promptcontext"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

const channelNameMaxLen = 80

// Channel avatar references an uploaded-file link (same persisted shape as
// agent avatars); cap it well above any signed URL we issue.
const channelAvatarURLMaxLen = 2048
const channelMessageMaxLen = 20000
const channelContextMessageLimit = 12
const channelAgentDirectedContextMessageLimit = 4
const channelRunTriggerLimit = 10
const channelDirectedIssueCooldown = 30 * time.Minute
const channelUserTypingExpiresInMS = 5000
const channelAgentTypingExpiresInMS = 10 * 60 * 1000
const channelMessagesDefaultLimit = 50
const channelMessagesMaxLimit = 100
const channelListMemberAvatarLimit = 5
const channelThreadDefaultLimit = 50
const channelThreadMaxLimit = 100
const channelClientMessageIDMaxLen = 128
const channelQuoteSelectedTextMaxLen = 4000
const channelOutputContractInstruction = "Channel output contract: use the runtime brief as the source of truth for visible output. Never print JSON envelopes, action objects, no_reply/stay_silent tokens, tool intent, analysis, missing-tool diagnostics, or described commands as the final answer."
const channelDirectedReplyInstruction = "This run is directly addressed to you. Human DMs, human @mentions, direct questions, assigned tasks, and DM-style continuations require a visible result. Agent-to-agent channel @mentions are weak notifications unless they ask for an immediate deliverable, review, decision, or direct answer; weak notifications should finish without visible output. Reply only to the Message target for chat transport supplied below: it is the current message's source location. A top-level group message stays in the main channel, a thread message stays in that thread, and a DM stays in that DM. Never create or switch to a thread based on message content or tone. Pure greetings (hi/你好/在吗) get a greeting sticker only. Substantive requests get a helpful answer using the requested supported delivery modality (no acknowledgement sticker first). Never return no_reply, stay_silent, JSON, or other protocol text."
const channelAmbientNoReplyInstruction = "If you should not reply, finish without a visible reply. Do not use the visible-output path, and do not print no_reply, stay_silent, JSON, or CLI/protocol text."
const channelAmbientGreetingReactionInstruction = "If the current channel message or unread bundle is only a casual greeting or small talk (for example hi, hello, hey, 你好, 在吗) with no @-mention, no question, and no task request, respond with a 👋 reaction to the reaction target only and do not create a text reply. This also applies when you are the only agent in the channel: treat the greeting as directed to you, but keep the action reaction-only unless the user includes a question or request. If reactions are unavailable, finish without visible output rather than explaining that no reply is needed."
const channelAmbientAlreadyDelegatedInstruction = "If the current message or unread bundle already @-mentions one or more specific other agents to do something, and you are not one of the mentioned agents, treat that task as already claimed. By default do not restate, duplicate, or race the same claim (for example \"收到，我也去查/处理\", \"我也确认一下\") — it adds noise and makes it unclear who is actually doing the work. Finish without visible output unless you are separately and directly asked to participate (for example a vote, a decision that specifically needs your role, or a direct question addressed to you)."
const channelStickerReplyInstruction = "Sticker replies: for directed short social beats (hi/你好, ok/好的, 收到/明白, 谢谢, 赞), use a sticker OR a short reply — not both. For substantive answers, do not add an acknowledgement sticker; preserve the requested supported delivery modality. For ambient/unaddressed runs, use stickers only when explicitly requested or genuinely welcoming someone; otherwise react or stay silent. Follow the runtime output path and never print protocol text."
const channelContinuationInstruction = "Collaborative discussion rule: reply only when you move the topic toward a decision, owner, or completed action. For a requested completion/blocker summary in a group chat, you may @-mention the responsible human once. Use @-mentions only for concrete actions, unresolved questions, human escalation, or requested completion/blocker delivery; never for thanks, generic status, future handoffs, or generic opinion invites. If the topic already has an owner and you add nothing immediate, finish without visible output."
const channelVoiceInputReplyInstruction = "Voice delivery: the current human message came from voice input. If you send a visible answer, use `multica message send --voice` and include the complete answer text as its accessible transcript."
const channelTypedVoiceReplyInstruction = "Voice delivery is supported: if the current human message explicitly asks for a spoken/voice reply, follow the runtime brief's voice-delivery path and include the complete answer text as its accessible transcript. Otherwise preserve the requested non-voice modality. Never claim that Multica group chat lacks voice delivery."
const channelMessageWakeReason = "channel_message"
const channelMessageWakePriority int32 = 1
const channelThreadReplyPriority int32 = 1
const channelDirectedWakePriority int32 = 10
const channelNameTakenCode = "channel_name_taken"
const channelNameUniqueConstraint = "channel_workspace_id_name_key"
const systemChannelProtectedCode = "system_channel_protected"

var errChannelClientMessageConflict = errors.New("client_message_id already used for different channel message payload")
var errChannelAttachmentUnavailable = errors.New("one or more attachments are unavailable")

var channelIssueLabelPattern = regexp.MustCompile(`\b[A-Z][A-Z0-9]+-\d+\b`)

type ChannelResponse struct {
	ID          string  `json:"id"`
	WorkspaceID string  `json:"workspace_id"`
	ProjectID   *string `json:"project_id,omitempty"`
	Name        string  `json:"name"`
	Kind        string  `json:"kind"`
	SystemKey   *string `json:"system_key,omitempty"`
	Description *string `json:"description"`
	LarkChatID  *string `json:"lark_chat_id"`
	AvatarURL   *string `json:"avatar_url"`
	CreatedBy   string  `json:"created_by"`
	CreatedAt   string  `json:"created_at"`
	UpdatedAt   string  `json:"updated_at"`
	ArchivedAt  *string `json:"archived_at,omitempty"`
	ArchivedBy  *string `json:"archived_by,omitempty"`
	// List-only enrichments (zero/omitted on create/update/get responses).
	UnreadCount     int     `json:"unread_count"`
	RealUnreadCount int     `json:"real_unread_count"`
	ManuallyUnread  bool    `json:"manually_unread,omitempty"`
	PinnedAt        *string `json:"pinned_at,omitempty"`
	MutedAt         *string `json:"muted_at,omitempty"`
	Muted           bool    `json:"muted,omitempty"`
	// NotifyLevel is the viewer's channel notification preference (LRM-769).
	// Always one of default|all|mentions|muted; DB NULL maps to "default".
	NotifyLevel        string               `json:"notify_level"`
	MentionUnreadCount int                  `json:"mention_unread_count,omitempty"`
	LastReadSeq        *int64               `json:"last_read_seq,omitempty"`
	LastMessage        *ChannelLastMessage  `json:"last_message,omitempty"`
	Members            []ChannelMemberBrief `json:"members,omitempty"`
}

type GlobalSearchCounts struct {
	Messages int `json:"messages"`
	Channels int `json:"channels"`
	DMs      int `json:"dms"`
	People   int `json:"people"`
}

type GlobalSearchHighlightRange struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

type GlobalSearchMessageResult struct {
	ResultType          string                       `json:"result_type"`
	MessageID           string                       `json:"message_id"`
	ChannelID           string                       `json:"channel_id"`
	ChannelName         string                       `json:"channel_name"`
	ChannelKind         string                       `json:"channel_kind"`
	ThreadRootMessageID *string                      `json:"thread_root_message_id,omitempty"`
	InThread            bool                         `json:"in_thread"`
	HitCount            int                          `json:"hit_count"`
	AuthorType          string                       `json:"author_type"`
	AuthorID            *string                      `json:"author_id"`
	AuthorName          string                       `json:"author_name"`
	Content             string                       `json:"content"`
	Snippet             string                       `json:"snippet"`
	HighlightRanges     []GlobalSearchHighlightRange `json:"highlight_ranges"`
	CreatedAt           string                       `json:"created_at"`
}

type GlobalSearchChannelResult struct {
	ChannelID   string  `json:"channel_id"`
	Name        string  `json:"name"`
	Kind        string  `json:"kind"`
	Description *string `json:"description,omitempty"`
}

type GlobalSearchPersonResult struct {
	ActorType   string  `json:"actor_type"`
	ActorID     string  `json:"actor_id"`
	Name        string  `json:"name"`
	DisplayName string  `json:"display_name"`
	AvatarURL   *string `json:"avatar_url,omitempty"`
}

type GlobalSearchResponse struct {
	Query    string                      `json:"query"`
	Scope    string                      `json:"scope"`
	Counts   GlobalSearchCounts          `json:"counts"`
	Messages []GlobalSearchMessageResult `json:"messages"`
	Channels []GlobalSearchChannelResult `json:"channels"`
	DMs      []GlobalSearchChannelResult `json:"dms"`
	People   []GlobalSearchPersonResult  `json:"people"`
}

type ChannelLastMessage struct {
	Type       string                 `json:"type"`
	AuthorName string                 `json:"author_name"`
	Content    string                 `json:"content"`
	Parts      []protocol.MessagePart `json:"parts,omitempty"`
	CreatedAt  string                 `json:"created_at"`
}

type ChannelMemberBrief struct {
	MemberType  string  `json:"member_type"`
	MemberID    string  `json:"member_id"`
	Name        string  `json:"name"`
	DisplayName string  `json:"display_name"`
	AvatarURL   *string `json:"avatar_url"`
	// Role is channel-level: owner | manager | member (not workspace role).
	Role         string                      `json:"role,omitempty"`
	RuntimeStats *protocol.RuntimeTokenStats `json:"runtime_stats,omitempty"`
}

type ChannelMemberResponse struct {
	MemberType  string  `json:"member_type"`
	MemberID    string  `json:"member_id"`
	Name        string  `json:"name"`
	DisplayName string  `json:"display_name"`
	AvatarURL   *string `json:"avatar_url"`
	// Role is channel-level: owner | manager | member (Beckham v2 §4).
	// Distinct from workspace MemberRole. FE badges: 群主 / 群管|管理员 / 成员.
	Role         string                      `json:"role"`
	RuntimeStats *protocol.RuntimeTokenStats `json:"runtime_stats,omitempty"`
	CreatedAt    string                      `json:"created_at"`
}

type ChannelMessageResponse struct {
	ID          string `json:"id"`
	ChannelID   string `json:"channel_id"`
	WorkspaceID string `json:"workspace_id"`
	Seq         int64  `json:"seq"`
	Type        string `json:"type"`
	// Kind is the structured message classification (LRM-1523 L1). Empty means
	// the row predates kind persistence; dispatch falls back to the runtime
	// classifier in that case. populated value is one of protocol.ChannelMessageKind*.
	Kind string `json:"kind,omitempty"`
	// KindSource records how Kind was derived (LRM-1529): structured|system|lexicon|default.
	KindSource          string                 `json:"kind_source,omitempty"`
	AuthorID            *string                `json:"author_id"`
	AuthorName          string                 `json:"author_name"`
	Content             string                 `json:"content"`
	Parts               []protocol.MessagePart `json:"parts,omitempty"`
	Source              string                 `json:"source"`
	ExternalMessageID   *string                `json:"external_message_id"`
	ClientMessageID     *string                `json:"client_message_id"`
	ReplyToMessageID    *string                `json:"reply_to_message_id,omitempty"`
	ReplyTo             *ChannelMessageReply   `json:"reply_to,omitempty"`
	QuoteMessageID      *string                `json:"quote_message_id,omitempty"`
	Quote               *ChannelMessageQuote   `json:"quote,omitempty"`
	quoteSnapshotRaw    []byte
	ThreadRootMessageID *string                    `json:"thread_root_message_id,omitempty"`
	ThreadRoot          *ChannelMessageReply       `json:"thread_root,omitempty"`
	ThreadReplyCount    int                        `json:"thread_reply_count,omitempty"`
	ThreadLastReplyAt   *string                    `json:"thread_last_reply_at,omitempty"`
	ThreadUnreadCount   int                        `json:"thread_unread_count,omitempty"`
	ThreadFollowed      bool                       `json:"thread_followed,omitempty"`
	ThreadParticipants  []ChannelThreadParticipant `json:"thread_participants,omitempty"`
	ThreadID            *string                    `json:"thread_id,omitempty"`
	TriggerDepth        int                        `json:"trigger_depth"`
	Reactions           []ChannelReactionResponse  `json:"reactions,omitempty"`
	CreatedAt           string                     `json:"created_at"`
	EditedAt            *string                    `json:"edited_at,omitempty"`
	DeletedAt           *string                    `json:"deleted_at,omitempty"`
	// Attachments referenced by this message. The chat bubble renders
	// file/image cards from these canonical associations.
	Attachments []AttachmentResponse `json:"attachments,omitempty"`
	// GraphMemoryCitationCount exposes only immutable snapshot availability;
	// citation bodies are loaded lazily through the member-authorized endpoint.
	GraphMemoryCitationCount int `json:"graph_memory_citation_count,omitempty"`
	// UndeliveredMentions lists structured @ targets who are not channel
	// members yet. Message is still stored; delivery is withheld until the
	// sender invites (Raft undelivered / invite). Omit when empty.
	UndeliveredMentions []UndeliveredMention `json:"undelivered_mentions,omitempty"`
}

type ChannelThreadParticipant struct {
	Key         string `json:"key"`
	MemberType  string `json:"member_type"`
	MemberID    string `json:"member_id"`
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Followed    bool   `json:"followed"`
}

type ChannelMessagesCursorResponse struct {
	CreatedAt string `json:"created_at"`
	ID        string `json:"id"`
	Seq       int64  `json:"seq"`
}

type ChannelMessagesPageResponse struct {
	Messages   []ChannelMessageResponse       `json:"messages"`
	Limit      int                            `json:"limit"`
	HasMore    bool                           `json:"has_more"`
	NextCursor *ChannelMessagesCursorResponse `json:"next_cursor,omitempty"`

	// around_seq mode only:
	AnchorIndex  int                            `json:"anchor_index"`
	HasMoreAfter bool                           `json:"has_more_after,omitempty"`
	AfterCursor  *ChannelMessagesCursorResponse `json:"after_cursor,omitempty"`
	UnreadTotal  int                            `json:"unread_total,omitempty"`
}

type ChannelThreadMessagesCursorResponse struct {
	BeforeSeq int64  `json:"before_seq,omitempty"`
	Before    string `json:"before"`
	BeforeID  string `json:"before_id"`
}

type ChannelThreadMessagesPageResponse struct {
	Messages   []ChannelMessageResponse             `json:"messages"`
	NextCursor *ChannelThreadMessagesCursorResponse `json:"next_cursor"`
}

type ChannelMessageReply struct {
	ID         string                 `json:"id"`
	Type       string                 `json:"type"`
	AuthorID   *string                `json:"author_id"`
	AuthorName string                 `json:"author_name"`
	Content    string                 `json:"content"`
	Parts      []protocol.MessagePart `json:"parts,omitempty"`
	CreatedAt  string                 `json:"created_at"`
}

type ChannelMessageQuote struct {
	MessageID string                       `json:"messageId"`
	Snapshot  *ChannelMessageQuoteSnapshot `json:"snapshot,omitempty"`
	Status    string                       `json:"status"`
}

type ChannelMessageQuoteSnapshot struct {
	Type         string                 `json:"type"`
	AuthorID     *string                `json:"authorId,omitempty"`
	AuthorName   string                 `json:"authorName"`
	Content      string                 `json:"content"`
	Parts        []protocol.MessagePart `json:"parts,omitempty"`
	CreatedAt    string                 `json:"createdAt"`
	SelectedText *string                `json:"selectedText,omitempty"`
}

type ChannelReactionResponse struct {
	ID        string `json:"id"`
	ChannelID string `json:"channel_id"`
	MessageID string `json:"message_id"`
	ActorType string `json:"actor_type"`
	ActorID   string `json:"actor_id"`
	Emoji     string `json:"emoji"`
	CreatedAt string `json:"created_at"`
}

type ChannelMessageSearchResponse struct {
	Query         string                       `json:"query"`
	Total         int                          `json:"total"`
	IncludeThread bool                         `json:"include_thread"`
	AuthorType    string                       `json:"author_type,omitempty"`
	AuthorID      string                       `json:"author_id,omitempty"`
	Scope         string                       `json:"scope"`
	Results       []ChannelMessageSearchResult `json:"results"`
}

type ChannelMessageSearchResult struct {
	MessageID           string  `json:"message_id"`
	ChannelID           string  `json:"channel_id"`
	ThreadRootMessageID *string `json:"thread_root_message_id,omitempty"`
	InThread            bool    `json:"in_thread"`
	Type                string  `json:"type"`
	AuthorID            *string `json:"author_id"`
	AuthorName          string  `json:"author_name"`
	Content             string  `json:"content"`
	CreatedAt           string  `json:"created_at"`
}

type CreateChannelRequest struct {
	Name        string  `json:"name"`
	Description *string `json:"description"`
	LarkChatID  *string `json:"lark_chat_id"`
	// ProjectID is optional. A UUID binds the new group to a project; null or
	// an empty string leaves it unbound.
	ProjectID json.RawMessage `json:"project_id"`
}

type UpdateChannelRequest struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
	LarkChatID  *string `json:"lark_chat_id"`
	AvatarURL   *string `json:"avatar_url"`
}

type AddChannelMemberRequest struct {
	MemberType string `json:"member_type"`
	MemberID   string `json:"member_id"`
}

type ChannelInviteCandidateResponse struct {
	MemberType  string  `json:"member_type"`
	MemberID    string  `json:"member_id"`
	Name        string  `json:"name"`
	DisplayName string  `json:"display_name"`
	Email       string  `json:"email,omitempty"`
	AvatarURL   *string `json:"avatar_url"`
	Role        string  `json:"role,omitempty"`
}

type ChannelInviteCandidatesResponse struct {
	Candidates []ChannelInviteCandidateResponse `json:"candidates"`
}

// ChannelMentionCandidate is one @ picker row. Type matches the composer
// mention vocabulary (member | agent), not channel_member.member_type.
type ChannelMentionCandidate struct {
	Type   string `json:"type"`
	ID     string `json:"id"`
	Handle string `json:"handle"`
	Label  string `json:"label"`
	// One-line blurb: the user's self-description or the agent's configured
	// description. Always a string; empty when unset.
	Description string  `json:"description"`
	AvatarURL   *string `json:"avatar_url,omitempty"`
}

// ChannelMentionCandidatesResponse is GET /api/channels/:id/mention-candidates.
// in_channel is always the full matching membership. not_in_channel is a page
// of workspace users/agents who are not members yet.
type ChannelMentionCandidatesResponse struct {
	InChannel    []ChannelMentionCandidate `json:"in_channel"`
	NotInChannel []ChannelMentionCandidate `json:"not_in_channel"`
	HasMore      bool                      `json:"has_more"`
	NextOffset   *int                      `json:"next_offset,omitempty"`
}

type SendChannelMessageRequest struct {
	Content             string                          `json:"content"`
	Parts               []protocol.MessagePart          `json:"parts"`
	AttachmentIDs       []string                        `json:"attachment_ids"`
	ReplyToMessageID    *string                         `json:"reply_to_message_id"`
	QuoteMessageID      *string                         `json:"quote_message_id"`
	QuoteMessageIDCamel *string                         `json:"quoteMessageId"`
	Quote               *SendChannelMessageQuoteRequest `json:"quote"`
	ClientMessageID     *string                         `json:"client_message_id"`
}

type SendChannelMessageQuoteRequest struct {
	MessageID    string  `json:"message_id"`
	SelectedText *string `json:"selected_text"`
}

type UpdateChannelMessageRequest struct {
	Content string                 `json:"content"`
	Parts   []protocol.MessagePart `json:"parts"`
}

type ChannelTypingRequest struct {
	IsTyping bool `json:"is_typing"`
}

type ImportLarkChannelMessageRequest struct {
	LarkChatID        string `json:"lark_chat_id"`
	ExternalMessageID string `json:"external_message_id"`
	AuthorName        string `json:"author_name"`
	Content           string `json:"content"`
}

func (h *Handler) StartChannelBridge() {
	if h.Bus == nil {
		return
	}
	h.Bus.Subscribe(protocol.EventChatDone, h.handleChannelChatDone)
	h.Bus.Subscribe(protocol.EventTaskFailed, h.handleChannelChatStopped)
	h.Bus.Subscribe(protocol.EventTaskCancelled, h.handleChannelChatStopped)
}

func (h *Handler) ListChannels(w http.ResponseWriter, r *http.Request) {
	// #801: no human-route alias. Agents must use GET /api/agent/channels.
	if rejectAgentOnHumanRoute(w, r, "ListChannels") {
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	channels, err := h.listConversationGroupChannels(r.Context(), ctxWorkspaceID(r.Context()), userID, queryBool(r, "archived"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list channels")
		return
	}
	writeJSON(w, http.StatusOK, channels)
}

func (h *Handler) MarkChannelRead(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	channelID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "channelId"), "channel id")
	if !ok {
		return
	}
	userUUID := parseUUID(userID)
	isMember := h.channelUserIsMember(r.Context(), workspaceID, channelID, userUUID)
	isSupervisor := !isMember && h.channelUserIsAgentDMSupervisor(r.Context(), workspaceID, channelID, userUUID)
	if !isMember && !isSupervisor {
		if !h.channelExists(r.Context(), workspaceID, channelID) {
			writeError(w, http.StatusNotFound, "channel not found")
			return
		}
		writeError(w, http.StatusForbidden, "not a channel member")
		return
	}

	// Optional: caller may pass a specific last_read_seq (e.g. the seq of the
	// last message scrolled into view). When omitted, mark up to the
	// conversation's current last_seq (original behavior).
	var req struct {
		LastReadSeq *int64 `json:"last_read_seq"`
		Rewind      bool   `json:"rewind"`
	}
	// Body is optional — ignore decode errors (empty body = mark to latest).
	_ = json.NewDecoder(r.Body).Decode(&req)

	// rewind=true: owner/admin may set last_read_seq to any value (including
	// backwards) in both channel_read and conversation_member, bypassing the
	// GREATEST monotonic guard. For staging/testing use only.
	if req.Rewind {
		if req.LastReadSeq == nil {
			writeError(w, http.StatusBadRequest, "rewind requires last_read_seq")
			return
		}
		if !h.isWorkspaceOwnerOrAdmin(r.Context(), workspaceID, parseUUID(userID)) {
			writeError(w, http.StatusForbidden, "rewind requires workspace owner or admin role")
			return
		}
		h.rewindChannelRead(w, r, workspaceID, channelID, parseUUID(userID), *req.LastReadSeq)
		return
	}

	// LRM-762: supervised agent_pair owners are not channel_members. Persist
	// their read cursor on channel_read only — never upsert conversation_member
	// (that would imply a speakable seat). Clear channel-keyed manual unread.
	if isSupervisor {
		h.markSupervisedDMChannelRead(w, r, workspaceID, userID, channelID, req.LastReadSeq)
		return
	}

	// Read the previous last_read_seq before upsert, so the response can echo
	// it back for FE race-free divider positioning (Frank's requirement).
	var previousLastReadSeq *int64
	err := h.DB.QueryRow(r.Context(), `
		SELECT NULLIF(COALESCE(cr.last_read_seq, vcm.last_read_seq, 0), 0)::bigint
		FROM conversation conv
		LEFT JOIN channel_read cr ON cr.channel_id = conv.channel_id AND cr.user_id = $2
		LEFT JOIN conversation_member vcm
		  ON vcm.conversation_id = conv.id AND vcm.member_type = 'user' AND vcm.member_id = $2
		WHERE conv.channel_id = $1`, channelID, parseUUID(userID)).Scan(&previousLastReadSeq)
	if err != nil {
		// No conversation or no existing read state — that's fine, old is nil.
		previousLastReadSeq = nil
	}

	_, err = h.DB.Exec(r.Context(), `
		WITH conv AS (
		  SELECT id, workspace_id, last_seq
		  FROM conversation
		  WHERE channel_id = $1
		),
		new_state AS (
		  SELECT conv.id AS conversation_id, conv.workspace_id,
		    CASE WHEN $3::bigint IS NOT NULL
		         THEN LEAST($3::bigint, conv.last_seq)
		         ELSE conv.last_seq END AS last_read_seq
		  FROM conv
		),
		read_state AS (
		  INSERT INTO channel_read (channel_id, user_id, last_read_at, last_read_seq)
		  SELECT $1, $2, now(), last_read_seq
		  FROM new_state
		  ON CONFLICT (channel_id, user_id)
		  DO UPDATE SET last_read_at = now(), last_read_seq = GREATEST(channel_read.last_read_seq, EXCLUDED.last_read_seq)
		  RETURNING channel_id, user_id, last_read_seq
		),
		unread_counts AS (
		  SELECT ns.conversation_id, count(m.id)::bigint AS main_unread_count
		  FROM new_state ns
		  JOIN channel_message m ON m.channel_id = $1
		   AND m.seq > ns.last_read_seq
		   AND m.author_type <> 'system'
		   AND NOT (m.author_type = 'user' AND m.author_id = $2)
		   AND m.thread_root_message_id IS NULL
		   AND m.deleted_at IS NULL
		  GROUP BY ns.conversation_id
		),
		mention_counts AS (
		  SELECT ns.conversation_id, count(m.id)::bigint AS mention_unread_count
		  FROM new_state ns
		  JOIN channel_message m ON m.channel_id = $1
		   AND m.seq > ns.last_read_seq
		   AND NOT (m.author_type = 'user' AND m.author_id = $2)
		   AND m.deleted_at IS NULL
		   AND EXISTS (
		     SELECT 1
		     FROM jsonb_array_elements(m.parts) part
		     WHERE part->>'type' = 'reference'
		       AND part->>'ref_type' = 'mention'
		       AND part->>'ref_subtype' = 'member'
		       AND part->>'ref_id' = $2::text
		   )
		  GROUP BY ns.conversation_id
		)
		INSERT INTO conversation_member (conversation_id, workspace_id, member_type, member_id, last_read_seq, main_unread_count, mention_unread_count, followed_at, updated_at)
		SELECT ns.conversation_id, ns.workspace_id, 'user', $2, ns.last_read_seq,
		       COALESCE(uc.main_unread_count, 0), COALESCE(mc.mention_unread_count, 0), now(), now()
		FROM new_state ns
		LEFT JOIN unread_counts uc ON uc.conversation_id = ns.conversation_id
		LEFT JOIN mention_counts mc ON mc.conversation_id = ns.conversation_id
		ON CONFLICT (conversation_id, member_type, member_id)
		DO UPDATE SET
		  last_read_seq = GREATEST(conversation_member.last_read_seq, EXCLUDED.last_read_seq),
		  main_unread_count = CASE
		    WHEN EXCLUDED.last_read_seq >= conversation_member.last_read_seq THEN EXCLUDED.main_unread_count
		    ELSE conversation_member.main_unread_count
		  END,
		  mention_unread_count = CASE
		    WHEN EXCLUDED.last_read_seq >= conversation_member.last_read_seq THEN EXCLUDED.mention_unread_count
		    ELSE conversation_member.mention_unread_count
		  END,
		  updated_at = now()`,
		channelID, parseUUID(userID), req.LastReadSeq)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to mark channel read")
		return
	}
	_, _ = h.DB.Exec(r.Context(), `
		UPDATE channel_member
		SET manual_unread_at = NULL
		WHERE channel_id = $1 AND workspace_id = $2 AND member_type = 'user' AND member_id = $3`,
		channelID, parseUUID(workspaceID), parseUUID(userID))
	h.clearDMPeerManualUnreadForChannel(r.Context(), workspaceID, userID, channelID)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "previous_last_read_seq": previousLastReadSeq})
}

// markSupervisedDMChannelRead advances a supervised agent_pair owner's read
// cursor without making them a conversation_member (LRM-762). Write paths stay
// gated on channel membership / agent_pair mode elsewhere.
func (h *Handler) markSupervisedDMChannelRead(w http.ResponseWriter, r *http.Request, workspaceID, userID string, channelID pgtype.UUID, lastReadSeq *int64) {
	var previousLastReadSeq *int64
	err := h.DB.QueryRow(r.Context(), `
		SELECT NULLIF(cr.last_read_seq, 0)::bigint
		FROM channel_read cr
		WHERE cr.channel_id = $1 AND cr.user_id = $2`,
		channelID, parseUUID(userID)).Scan(&previousLastReadSeq)
	if err != nil {
		previousLastReadSeq = nil
	}

	_, err = h.DB.Exec(r.Context(), `
		WITH conv AS (
		  SELECT last_seq FROM conversation WHERE channel_id = $1
		),
		new_state AS (
		  SELECT CASE WHEN $3::bigint IS NOT NULL
		              THEN LEAST($3::bigint, conv.last_seq)
		              ELSE conv.last_seq END AS last_read_seq
		  FROM conv
		)
		INSERT INTO channel_read (channel_id, user_id, last_read_at, last_read_seq)
		SELECT $1, $2, now(), last_read_seq
		FROM new_state
		ON CONFLICT (channel_id, user_id)
		DO UPDATE SET last_read_at = now(),
		              last_read_seq = GREATEST(channel_read.last_read_seq, EXCLUDED.last_read_seq)`,
		channelID, parseUUID(userID), lastReadSeq)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to mark channel read")
		return
	}
	h.clearDMPeerManualUnreadForChannel(r.Context(), workspaceID, userID, channelID)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "previous_last_read_seq": previousLastReadSeq})
}

func (h *Handler) PinChannel(w http.ResponseWriter, r *http.Request) {
	h.setChannelPinned(w, r, true)
}

func (h *Handler) UnpinChannel(w http.ResponseWriter, r *http.Request) {
	h.setChannelPinned(w, r, false)
}

func (h *Handler) MuteChannel(w http.ResponseWriter, r *http.Request) {
	h.setChannelMuted(w, r, true)
}

func (h *Handler) UnmuteChannel(w http.ResponseWriter, r *http.Request) {
	h.setChannelMuted(w, r, false)
}

// Channel notify preference levels (LRM-769). "default" is API-only — DB stores NULL.
const (
	channelNotifyLevelDefault  = "default"
	channelNotifyLevelAll      = "all"
	channelNotifyLevelMentions = "mentions"
	channelNotifyLevelMuted    = "muted"
)

var validChannelNotifyLevels = map[string]bool{
	channelNotifyLevelDefault:  true,
	channelNotifyLevelAll:      true,
	channelNotifyLevelMentions: true,
	channelNotifyLevelMuted:    true,
}

func channelNotifyLevelAPI(dbLevel pgtype.Text) string {
	if !dbLevel.Valid || strings.TrimSpace(dbLevel.String) == "" {
		return channelNotifyLevelDefault
	}
	switch dbLevel.String {
	case channelNotifyLevelAll, channelNotifyLevelMentions, channelNotifyLevelMuted:
		return dbLevel.String
	default:
		return channelNotifyLevelDefault
	}
}

type setChannelNotifyPreferenceRequest struct {
	Level string `json:"level"`
}

// SetChannelNotifyPreference — PUT /api/channels/{channelId}/notify-preference
// Body: { "level": "default"|"all"|"mentions"|"muted" }
// Dual-writes muted_at: mentions|muted → COALESCE(muted_at, now()); default|all → NULL.
func (h *Handler) SetChannelNotifyPreference(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	channelID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "channelId"), "channel id")
	if !ok {
		return
	}
	var req setChannelNotifyPreferenceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	level := strings.TrimSpace(req.Level)
	if !validChannelNotifyLevels[level] {
		writeError(w, http.StatusBadRequest, "invalid notify level")
		return
	}
	userUUID := parseUUID(userID)
	if !h.requireChannelUserMember(w, r.Context(), workspaceID, channelID, userUUID) {
		return
	}
	storedLevel := any(nil)
	if level != channelNotifyLevelDefault {
		storedLevel = level
	}
	keepMutedAt := level == channelNotifyLevelMentions || level == channelNotifyLevelMuted
	if _, err := h.DB.Exec(r.Context(), `
		WITH updated_channel_member AS (
		  UPDATE channel_member
		  SET notify_level = $4::text,
		      muted_at = CASE
		        WHEN $5 THEN COALESCE(muted_at, now())
		        ELSE NULL
		      END
		  WHERE channel_id = $1 AND workspace_id = $2 AND member_type = 'user' AND member_id = $3
		  RETURNING channel_id, workspace_id, member_id, muted_at
		),
		conv AS (
		  SELECT id
		  FROM conversation
		  WHERE channel_id = $1
		)
		UPDATE conversation_member cm
		SET muted_at = updated_channel_member.muted_at,
		    updated_at = now()
		FROM updated_channel_member, conv
		WHERE cm.conversation_id = conv.id
		  AND cm.workspace_id = updated_channel_member.workspace_id
		  AND cm.member_type = 'user'
		  AND cm.member_id = updated_channel_member.member_id`,
		channelID, parseUUID(workspaceID), userUUID, storedLevel, keepMutedAt); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update notify preference")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "notify_level": level})
}

func (h *Handler) setChannelPinned(w http.ResponseWriter, r *http.Request, pinned bool) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	channelID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "channelId"), "channel id")
	if !ok {
		return
	}
	userUUID := parseUUID(userID)
	if !h.requireChannelUserMember(w, r.Context(), workspaceID, channelID, userUUID) {
		return
	}
	if !h.requireChannelWritable(w, r.Context(), workspaceID, channelID) {
		return
	}
	value := "now()"
	if !pinned {
		value = "NULL"
	}
	if _, err := h.DB.Exec(r.Context(), fmt.Sprintf(`
		UPDATE channel_member
		SET pinned_at = %s
		WHERE channel_id = $1 AND workspace_id = $2 AND member_type = 'user' AND member_id = $3`, value),
		channelID, parseUUID(workspaceID), userUUID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update channel pin")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (h *Handler) setChannelMuted(w http.ResponseWriter, r *http.Request, muted bool) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	channelID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "channelId"), "channel id")
	if !ok {
		return
	}
	userUUID := parseUUID(userID)
	if !h.requireChannelUserMember(w, r.Context(), workspaceID, channelID, userUUID) {
		return
	}
	// Legacy mute ↔ mentions / unmute ↔ default (LRM-769 dual-write).
	if _, err := h.DB.Exec(r.Context(), `
		WITH updated_channel_member AS (
		  UPDATE channel_member
		  SET muted_at = CASE WHEN $4 THEN now() ELSE NULL END,
		      notify_level = CASE WHEN $4 THEN 'mentions' ELSE NULL END
		  WHERE channel_id = $1 AND workspace_id = $2 AND member_type = 'user' AND member_id = $3
		  RETURNING channel_id, workspace_id, member_id, muted_at
		),
		conv AS (
		  SELECT id
		  FROM conversation
		  WHERE channel_id = $1
		)
		UPDATE conversation_member cm
		SET muted_at = updated_channel_member.muted_at,
		    updated_at = now()
		FROM updated_channel_member, conv
		WHERE cm.conversation_id = conv.id
		  AND cm.workspace_id = updated_channel_member.workspace_id
		  AND cm.member_type = 'user'
		  AND cm.member_id = updated_channel_member.member_id`,
		channelID, parseUUID(workspaceID), userUUID, muted); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update channel mute")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (h *Handler) MarkChannelUnread(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	channelID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "channelId"), "channel id")
	if !ok {
		return
	}
	userUUID := parseUUID(userID)
	if !h.requireChannelUserMember(w, r.Context(), workspaceID, channelID, userUUID) {
		return
	}
	if !h.requireChannelWritable(w, r.Context(), workspaceID, channelID) {
		return
	}
	if _, err := h.DB.Exec(r.Context(), `
		UPDATE channel_member
		SET manual_unread_at = now()
		WHERE channel_id = $1 AND workspace_id = $2 AND member_type = 'user' AND member_id = $3`,
		channelID, parseUUID(workspaceID), userUUID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to mark channel unread")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (h *Handler) CreateChannel(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	var req CreateChannelRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if name == "general" {
		writeCodedError(w, http.StatusConflict, systemChannelProtectedCode, "general is reserved for the system channel")
		return
	}
	if len([]rune(name)) > channelNameMaxLen {
		writeError(w, http.StatusBadRequest, "name is too long")
		return
	}
	projectID, ok := h.parseChannelProjectBinding(w, r, workspaceID, req.ProjectID)
	if !ok {
		return
	}
	desc := trimTextPtr(req.Description)
	larkChatID := trimTextPtr(req.LarkChatID)

	// Ordinary groups always go through createOrdinaryGroupWithOwnerTx so
	// channel + human owner cannot diverge (Parker: single create funnel).
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create channel")
		return
	}
	defer tx.Rollback(r.Context())

	channelID, err := createOrdinaryGroupWithOwnerTx(
		r.Context(), tx,
		parseUUID(workspaceID), parseUUID(userID),
		name, desc, larkChatID, projectID,
	)
	if err != nil {
		if isChannelNameTakenError(err) {
			writeCodedError(w, http.StatusConflict, channelNameTakenCode, "channel name already exists")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to create channel")
		return
	}
	// Re-read full row for response (helper only returns id).
	ch, err := scanChannel(tx.QueryRow(r.Context(), `
		SELECT id, workspace_id, name, description, lark_chat_id, project_id, created_by, created_at, updated_at, kind, system_key, archived_at, archived_by, avatar_url
		FROM channel WHERE id = $1::uuid`, channelID))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create channel")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create channel")
		return
	}

	// LRM-397/398: do NOT auto-provision a group-manager agent on channel
	// create. #1436 replaced the agent-based group manager (贝克汉姆) with a
	// channel-role assignment instead — see agent_workspace_role.go /
	// channel_manager_role_wake.go.
	if projectID.Valid {
		// Creating an already-bound group is still a project/channel association.
		// Record the same typed system fact as a later settings change so the
		// timeline has one durable projection regardless of entry point.
		h.emitChannelProjectSystemEvent(r.Context(), workspaceID, parseUUID(ch.ID), parseUUID(userID), pgtype.UUID{}, projectID)
	}
	h.publish(protocol.EventChannelUpdated, workspaceID, "member", userID, ch)
	writeJSON(w, http.StatusCreated, ch)
}

func isChannelNameTakenError(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) &&
		pgErr.Code == "23505" &&
		pgErr.ConstraintName == channelNameUniqueConstraint
}

func isSystemGeneralGuardError(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) &&
		pgErr.Code == "P0001" &&
		strings.HasPrefix(pgErr.Message, "system_general_")
}

func writeSystemChannelProtected(w http.ResponseWriter) {
	writeCodedError(w, http.StatusConflict, systemChannelProtectedCode, "system channel is managed automatically")
}

// ensureSystemGeneralRosterIfNeeded re-runs ensure_system_general_channel when
// the target is the workspace #general channel. Best-effort: failures are
// logged and the caller still serves the current roster. Used to heal agents
// left out by migration 251's deliberate no-backfill (LRM-915).
func (h *Handler) ensureSystemGeneralRosterIfNeeded(ctx context.Context, workspaceID string, channelID pgtype.UUID, actorUserID string) {
	var systemKey pgtype.Text
	if err := h.DB.QueryRow(ctx, `
		SELECT system_key
		FROM channel
		WHERE id = $1 AND workspace_id = $2`, channelID, parseUUID(workspaceID)).Scan(&systemKey); err != nil {
		return
	}
	if !systemKey.Valid || systemKey.String != "general" {
		return
	}
	var ignored pgtype.UUID
	if err := h.DB.QueryRow(ctx,
		`SELECT ensure_system_general_channel($1, $2)`,
		parseUUID(workspaceID), parseUUID(actorUserID),
	).Scan(&ignored); err != nil {
		slog.Warn("ensure_system_general_channel heal failed",
			"workspace_id", workspaceID,
			"channel_id", uuidToString(channelID),
			"error", err)
	}
}

func (h *Handler) requireChannelNotSystem(w http.ResponseWriter, ctx context.Context, workspaceID string, channelID pgtype.UUID) bool {
	var systemKey pgtype.Text
	err := h.DB.QueryRow(ctx, `
		SELECT system_key
		FROM channel
		WHERE id = $1 AND workspace_id = $2`, channelID, parseUUID(workspaceID)).Scan(&systemKey)
	if err != nil {
		if errorsIsNoRows(err) {
			writeError(w, http.StatusNotFound, "channel not found")
		} else {
			writeError(w, http.StatusInternalServerError, "failed to load channel")
		}
		return false
	}
	if systemKey.Valid && systemKey.String == "general" {
		writeSystemChannelProtected(w)
		return false
	}
	return true
}

func (h *Handler) UpdateChannel(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	channelID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "channelId"), "channel id")
	if !ok {
		return
	}
	if !h.requireChannelUserMember(w, r.Context(), workspaceID, channelID, parseUUID(userID)) {
		return
	}
	if !h.requireChannelWritable(w, r.Context(), workspaceID, channelID) {
		return
	}
	if !h.requireChannelNotSystem(w, r.Context(), workspaceID, channelID) {
		return
	}
	var req UpdateChannelRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	var name *string
	if req.Name != nil {
		trimmed := strings.TrimSpace(*req.Name)
		if trimmed == "" {
			writeError(w, http.StatusBadRequest, "name is required")
			return
		}
		if len([]rune(trimmed)) > channelNameMaxLen {
			writeError(w, http.StatusBadRequest, "name is too long")
			return
		}
		name = &trimmed
	}
	if req.AvatarURL != nil {
		if len(*req.AvatarURL) > channelAvatarURLMaxLen {
			writeError(w, http.StatusBadRequest, "avatar_url is too long")
			return
		}
	}
	row := h.DB.QueryRow(r.Context(), `
		UPDATE channel
		SET name = COALESCE($3, name), description = COALESCE($4, description), lark_chat_id = COALESCE($5, lark_chat_id), avatar_url = COALESCE($6, avatar_url), updated_at = now()
		WHERE id = $1 AND workspace_id = $2
		RETURNING id, workspace_id, name, description, lark_chat_id, project_id, created_by, created_at, updated_at, kind, system_key, archived_at, archived_by, avatar_url`,
		channelID, parseUUID(workspaceID), name, trimTextPtr(req.Description), trimTextPtr(req.LarkChatID), trimTextPtr(req.AvatarURL))
	ch, err := scanChannel(row)
	if err != nil {
		if errorsIsNoRows(err) {
			writeError(w, http.StatusNotFound, "channel not found")
			return
		}
		if isSystemGeneralGuardError(err) {
			writeSystemChannelProtected(w)
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to update channel")
		return
	}
	h.publish(protocol.EventChannelUpdated, workspaceID, "member", userID, ch)
	writeJSON(w, http.StatusOK, ch)
}

// DeleteChannel permanently removes a group channel and its cascaded rows
// (messages, members, attachments, …). Unlike ArchiveChannel this is
// unrecoverable. Only workspace owner/admin may delete; system #general and
// DMs are rejected with explicit errors. Archived channels may still be
// deleted (Slack-aligned).
func (h *Handler) DeleteChannel(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	channelID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "channelId"), "channel id")
	if !ok {
		return
	}

	var kind string
	var systemKey pgtype.Text
	err := h.DB.QueryRow(r.Context(), `
		SELECT kind, system_key
		FROM channel
		WHERE id = $1 AND workspace_id = $2`, channelID, parseUUID(workspaceID)).Scan(&kind, &systemKey)
	if err != nil {
		if errorsIsNoRows(err) {
			writeError(w, http.StatusNotFound, "channel not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load channel")
		return
	}
	if systemKey.Valid && systemKey.String == "general" {
		writeSystemChannelProtected(w)
		return
	}
	if kind == "dm" {
		writeError(w, http.StatusForbidden, "direct messages cannot be permanently deleted")
		return
	}
	if kind != "group" {
		writeError(w, http.StatusForbidden, "only group channels can be permanently deleted")
		return
	}
	// Permanent delete is stricter than archive: workspace owner/admin only
	// (channel creator who is a plain member cannot delete).
	if _, ok := h.requireWorkspaceRole(w, r, workspaceID, "workspace not found", "owner", "admin"); !ok {
		return
	}

	ctx := r.Context()
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete channel")
		return
	}
	defer tx.Rollback(ctx)

	wsUUID := parseUUID(workspaceID)

	// voice_call_session.channel_id has no ON DELETE clause (default RESTRICT).
	if _, err := tx.Exec(ctx, `
		DELETE FROM voice_call_session
		WHERE channel_id = $1 AND workspace_id = $2`,
		channelID, wsUUID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to clear voice call sessions for channel")
		return
	}

	tag, err := tx.Exec(ctx, `
		DELETE FROM channel
		WHERE id = $1 AND workspace_id = $2 AND kind = 'group'`,
		channelID, wsUUID)
	if err != nil {
		if isSystemGeneralGuardError(err) {
			writeSystemChannelProtected(w)
			return
		}
		if fkViolation23503(err) {
			var pgErr *pgconn.PgError
			_ = errors.As(err, &pgErr)
			msg := "cannot delete channel: it is still referenced by other records"
			if pgErr != nil && pgErr.ConstraintName != "" {
				msg = fmt.Sprintf("cannot delete channel: blocked by %s", pgErr.ConstraintName)
			}
			writeCodedError(w, http.StatusConflict, "channel_delete_blocked", msg)
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to delete channel")
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "channel not found")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete channel")
		return
	}

	h.publish(protocol.EventChannelDeleted, workspaceID, "member", userID, map[string]any{"id": uuidToString(channelID)})
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) ArchiveChannel(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	channelID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "channelId"), "channel id")
	if !ok {
		return
	}
	if !h.requireChannelManager(w, r, workspaceID, channelID, parseUUID(userID)) {
		return
	}
	if !h.requireChannelNotSystem(w, r.Context(), workspaceID, channelID) {
		return
	}
	ch, ok := h.archiveChannel(w, r, workspaceID, channelID, parseUUID(userID))
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, ch)
}

func (h *Handler) RestoreChannel(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	channelID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "channelId"), "channel id")
	if !ok {
		return
	}
	if !h.requireChannelManager(w, r, workspaceID, channelID, parseUUID(userID)) {
		return
	}
	if !h.requireChannelNotSystem(w, r.Context(), workspaceID, channelID) {
		return
	}
	row := h.DB.QueryRow(r.Context(), `
		UPDATE channel
		SET archived_at = NULL, archived_by = NULL, updated_at = now()
		WHERE id = $1 AND workspace_id = $2 AND kind = 'group'
		RETURNING id, workspace_id, name, description, lark_chat_id, project_id, created_by, created_at, updated_at, kind, system_key, archived_at, archived_by, avatar_url`,
		channelID, parseUUID(workspaceID))
	ch, err := scanChannel(row)
	if err != nil {
		if errorsIsNoRows(err) {
			writeError(w, http.StatusNotFound, "channel not found")
			return
		}
		if isSystemGeneralGuardError(err) {
			writeSystemChannelProtected(w)
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to restore channel")
		return
	}
	h.publish(protocol.EventChannelUpdated, workspaceID, "member", userID, ch)
	writeJSON(w, http.StatusOK, ch)
}

func (h *Handler) archiveChannel(w http.ResponseWriter, r *http.Request, workspaceID string, channelID, userID pgtype.UUID) (ChannelResponse, bool) {
	row := h.DB.QueryRow(r.Context(), `
		UPDATE channel
		SET archived_at = COALESCE(archived_at, now()), archived_by = COALESCE(archived_by, $3), updated_at = now()
		WHERE id = $1 AND workspace_id = $2 AND kind = 'group'
		RETURNING id, workspace_id, name, description, lark_chat_id, project_id, created_by, created_at, updated_at, kind, system_key, archived_at, archived_by, avatar_url`,
		channelID, parseUUID(workspaceID), userID)
	ch, err := scanChannel(row)
	if err != nil {
		if errorsIsNoRows(err) {
			writeError(w, http.StatusNotFound, "channel not found")
			return ChannelResponse{}, false
		}
		if isSystemGeneralGuardError(err) {
			writeSystemChannelProtected(w)
			return ChannelResponse{}, false
		}
		writeError(w, http.StatusInternalServerError, "failed to archive channel")
		return ChannelResponse{}, false
	}
	// LRM-485 — Archive ≠ Delete. Clients must refresh both active and
	// archived lists; never emit channel:deleted here (that left the row in
	// Archived while other tabs thought it was gone).
	h.publish(protocol.EventChannelUpdated, workspaceID, "member", uuidToString(userID), ch)
	return ch, true
}

// ListChannelInviteCandidates returns workspace members + agents that can be
// invited into an ordinary group channel.
//
// Visibility contract (LRM-915 / #908 / #1613):
//   - Every non-archived workspace agent is inviteable by any channel member.
//     There is no agent.visibility / owner-only / Wendy-name silent filter —
//     agent.visibility was retired (#908) and the Wendy SQL carve-out was
//     removed (#1613). Candidates must not disappear without an explicit
//     API error or UI empty-state reason.
//   - Agents already in the channel are omitted (already members).
//   - Archived agents are omitted (not inviteable).
func (h *Handler) ListChannelInviteCandidates(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	channelID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "channelId"), "channel id")
	if !ok {
		return
	}
	if !h.requireChannelUserMember(w, r.Context(), workspaceID, channelID, parseUUID(userID)) {
		return
	}
	if !h.requireGroupChannel(w, r.Context(), workspaceID, channelID) {
		return
	}
	if !h.requireChannelWritable(w, r.Context(), workspaceID, channelID) {
		return
	}

	q := strings.TrimSpace(r.URL.Query().Get("q"))
	qLower := strings.ToLower(q)
	qLike := "%" + qLower + "%"
	includeAll := qLower == ""
	args := []any{parseUUID(workspaceID), channelID, includeAll, qLike}
	limitClause := ""
	if rawLimit := strings.TrimSpace(r.URL.Query().Get("limit")); rawLimit != "" {
		limit := boundedQueryInt(r, "limit", 200, 500)
		args = append(args, limit)
		limitClause = " LIMIT $5"
	}

	query := `
		WITH visible_agents AS (
			SELECT a.id, a.name, COALESCE(NULLIF(a.display_name, ''), a.name) AS display_name,
			       a.avatar_url, a.created_at
			FROM agent a
			WHERE a.workspace_id = $1
			  AND a.archived_at IS NULL
			  AND NOT EXISTS (
				SELECT 1 FROM channel_member cm
				WHERE cm.channel_id = $2 AND cm.member_type = 'agent' AND cm.member_id = a.id
			  )
			  AND (
				$3::boolean
				OR lower(a.name) LIKE $4
				OR lower(COALESCE(NULLIF(a.display_name, ''), a.name)) LIKE $4
			  )
		), candidates AS (
			SELECT 'user'::text AS member_type, m.user_id AS member_id,
			       u.name, COALESCE(NULLIF(u.display_name, ''), u.name, u.email) AS display_name,
			       u.email, u.avatar_url, m.role, m.created_at
			FROM member m
			JOIN "user" u ON u.id = m.user_id
			WHERE m.workspace_id = $1
			  AND NOT EXISTS (
				SELECT 1 FROM channel_member cm
				WHERE cm.channel_id = $2 AND cm.member_type = 'user' AND cm.member_id = m.user_id
			  )
			  AND (
				$3::boolean
				OR lower(u.name) LIKE $4
				OR lower(COALESCE(NULLIF(u.display_name, ''), u.name, u.email)) LIKE $4
				OR lower(u.email) LIKE $4
			  )
			UNION ALL
			SELECT 'agent'::text AS member_type, va.id AS member_id,
			       va.name, va.display_name, ''::text AS email, va.avatar_url, ''::text AS role, va.created_at
			FROM visible_agents va
		)
		SELECT member_type, member_id, name, display_name, email, avatar_url, role
		FROM candidates
		ORDER BY lower(display_name), lower(name), member_id` + limitClause
	rows, err := h.DB.Query(r.Context(), query, args...)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list channel invite candidates")
		return
	}
	defer rows.Close()

	out := []ChannelInviteCandidateResponse{}
	for rows.Next() {
		var c ChannelInviteCandidateResponse
		var id pgtype.UUID
		var avatar pgtype.Text
		if err := rows.Scan(&c.MemberType, &id, &c.Name, &c.DisplayName, &c.Email, &avatar, &c.Role); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to read channel invite candidates")
			return
		}
		c.MemberID = uuidToString(id)
		c.AvatarURL = textToPtr(avatar)
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read channel invite candidates")
		return
	}

	writeJSON(w, http.StatusOK, ChannelInviteCandidatesResponse{Candidates: out})
}

const mentionCandidatePageDefault = 20
const mentionCandidatePageMax = 100

// ListChannelMentionCandidates returns the group @ picker roster.
// Channel members are never paginated. Outsiders are a limit/offset page so
// a large workspace cannot hide in-channel people behind ASCII sort order.
func (h *Handler) ListChannelMentionCandidates(w http.ResponseWriter, r *http.Request) {
	if rejectAgentOnHumanRoute(w, r, "ListChannelMentionCandidates") {
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	channelID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "channelId"), "channel id")
	if !ok {
		return
	}
	if !h.requireChannelUserMember(w, r.Context(), workspaceID, channelID, parseUUID(userID)) {
		return
	}
	if !h.requireGroupChannel(w, r.Context(), workspaceID, channelID) {
		return
	}

	q := strings.TrimSpace(r.URL.Query().Get("q"))
	qLower := strings.ToLower(q)
	qLike := "%" + qLower + "%"
	includeAll := qLower == ""
	limit := boundedQueryInt(r, "limit", mentionCandidatePageDefault, mentionCandidatePageMax)
	offset := 0
	if raw := strings.TrimSpace(r.URL.Query().Get("offset")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n >= 0 {
			offset = n
		}
	}

	inChannel, err := h.listChannelMentionInChannel(r.Context(), parseUUID(workspaceID), channelID, includeAll, qLike)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list channel mention candidates")
		return
	}
	inChannel = dropViewerMentionCandidate(inChannel, userID)
	outsiders, hasMore, err := h.listChannelMentionOutsiders(r.Context(), parseUUID(workspaceID), channelID, includeAll, qLike, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list channel mention candidates")
		return
	}

	resp := ChannelMentionCandidatesResponse{
		InChannel:    inChannel,
		NotInChannel: outsiders,
		HasMore:      hasMore,
	}
	if hasMore {
		next := offset + len(outsiders)
		resp.NextOffset = &next
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) listChannelMentionInChannel(ctx context.Context, workspaceID, channelID pgtype.UUID, includeAll bool, qLike string) ([]ChannelMentionCandidate, error) {
	rows, err := h.DB.Query(ctx, `
		SELECT CASE WHEN cm.member_type = 'user' THEN 'member' ELSE 'agent' END,
		       cm.member_id,
		       COALESCE(u.name, a.name, ''),
		       COALESCE(NULLIF(u.display_name, ''), u.name, u.email, NULLIF(a.display_name, ''), a.name, ''),
		       COALESCE(NULLIF(u.profile_description, ''), NULLIF(a.description, ''), ''),
		       CASE WHEN cm.member_type = 'user' THEN u.avatar_url ELSE a.avatar_url END
		FROM channel_member cm
		LEFT JOIN "user" u ON cm.member_type = 'user' AND u.id = cm.member_id
		LEFT JOIN agent a ON cm.member_type = 'agent' AND a.id = cm.member_id
		WHERE cm.channel_id = $1
		  AND cm.workspace_id = $2
		  AND (
			$3::boolean
			OR lower(COALESCE(u.name, a.name, '')) LIKE $4
			OR lower(COALESCE(NULLIF(u.display_name, ''), u.name, u.email, NULLIF(a.display_name, ''), a.name, '')) LIKE $4
		  )
		ORDER BY lower(COALESCE(NULLIF(u.display_name, ''), u.name, u.email, NULLIF(a.display_name, ''), a.name, '')),
		         lower(COALESCE(u.name, a.name, '')),
		         cm.member_id`,
		channelID, workspaceID, includeAll, qLike)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanChannelMentionCandidates(rows)
}

func (h *Handler) listChannelMentionOutsiders(ctx context.Context, workspaceID, channelID pgtype.UUID, includeAll bool, qLike string, limit, offset int) ([]ChannelMentionCandidate, bool, error) {
	rows, err := h.DB.Query(ctx, `
		WITH visible_agents AS (
			SELECT a.id, a.name, COALESCE(NULLIF(a.display_name, ''), a.name) AS display_name,
			       COALESCE(a.description, '') AS description,
			       a.avatar_url
			FROM agent a
			WHERE a.workspace_id = $1
			  AND a.archived_at IS NULL
			  AND NOT EXISTS (
				SELECT 1 FROM channel_member cm
				WHERE cm.channel_id = $2 AND cm.member_type = 'agent' AND cm.member_id = a.id
			  )
			  AND (
				$3::boolean
				OR lower(a.name) LIKE $4
				OR lower(COALESCE(NULLIF(a.display_name, ''), a.name)) LIKE $4
			  )
		), candidates AS (
			SELECT 'member'::text AS type, m.user_id AS id,
			       COALESCE(u.name, '') AS handle,
			       COALESCE(NULLIF(u.display_name, ''), u.name, u.email) AS label,
			       COALESCE(u.profile_description, '') AS description,
			       u.avatar_url
			FROM member m
			JOIN "user" u ON u.id = m.user_id
			WHERE m.workspace_id = $1
			  AND NOT EXISTS (
				SELECT 1 FROM channel_member cm
				WHERE cm.channel_id = $2 AND cm.member_type = 'user' AND cm.member_id = m.user_id
			  )
			  AND (
				$3::boolean
				OR lower(u.name) LIKE $4
				OR lower(COALESCE(NULLIF(u.display_name, ''), u.name, u.email)) LIKE $4
				OR lower(u.email) LIKE $4
			  )
			UNION ALL
			SELECT 'agent'::text AS type, va.id AS id,
			       va.name AS handle, va.display_name AS label,
			       va.description, va.avatar_url
			FROM visible_agents va
		)
		SELECT type, id, handle, label, description, avatar_url
		FROM candidates
		ORDER BY lower(label), lower(handle), id
		LIMIT $5 OFFSET $6`,
		workspaceID, channelID, includeAll, qLike, limit+1, offset)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	all, err := scanChannelMentionCandidates(rows)
	if err != nil {
		return nil, false, err
	}
	hasMore := len(all) > limit
	if hasMore {
		all = all[:limit]
	}
	return all, hasMore, nil
}

func dropViewerMentionCandidate(rows []ChannelMentionCandidate, viewerUserID string) []ChannelMentionCandidate {
	if viewerUserID == "" || len(rows) == 0 {
		return rows
	}
	out := make([]ChannelMentionCandidate, 0, len(rows))
	for _, row := range rows {
		if row.Type == "member" && row.ID == viewerUserID {
			continue
		}
		out = append(out, row)
	}
	return out
}

func scanChannelMentionCandidates(rows pgx.Rows) ([]ChannelMentionCandidate, error) {
	out := []ChannelMentionCandidate{}
	for rows.Next() {
		var c ChannelMentionCandidate
		var id pgtype.UUID
		var avatar pgtype.Text
		if err := rows.Scan(&c.Type, &id, &c.Handle, &c.Label, &c.Description, &avatar); err != nil {
			return nil, err
		}
		c.ID = uuidToString(id)
		c.AvatarURL = textToPtr(avatar)
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (h *Handler) ListChannelMembers(w http.ResponseWriter, r *http.Request) {
	// #801: no human-route alias. Agents must use GET /api/agent/channels/{id}/members.
	if rejectAgentOnHumanRoute(w, r, "ListChannelMembers") {
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	channelID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "channelId"), "channel id")
	if !ok {
		return
	}
	if !h.requireChannelUserMember(w, r.Context(), workspaceID, channelID, parseUUID(userID)) {
		return
	}
	// LRM-915: migration 251 skipped historical #general backfill; heal on read
	// so formerly-private agents (Wendy) appear in the member list/search
	// without waiting for an agent-row UPDATE to re-fire the sync trigger.
	h.ensureSystemGeneralRosterIfNeeded(r.Context(), workspaceID, channelID, userID)
	rows, err := h.DB.Query(r.Context(), `
		SELECT cm.member_type, cm.member_id,
		       COALESCE(u.name, a.name, ''),
		       COALESCE(NULLIF(u.display_name, ''), u.name, u.email, NULLIF(a.display_name, ''), a.name, ''),
		       CASE WHEN cm.member_type = 'user' THEN u.avatar_url ELSE a.avatar_url END,
		       cs.runtime_token_stats,
		       cm.created_at,
		       cm.role
		FROM channel_member cm
		LEFT JOIN "user" u ON cm.member_type = 'user' AND u.id = cm.member_id
		LEFT JOIN agent a ON cm.member_type = 'agent' AND a.id = cm.member_id
		LEFT JOIN channel_agent_session cas ON cm.member_type = 'agent' AND cas.channel_id = cm.channel_id AND cas.agent_id = cm.member_id
		LEFT JOIN chat_session cs ON cs.id = cas.chat_session_id
		WHERE cm.channel_id = $1 AND cm.workspace_id = $2
		ORDER BY
		  CASE cm.role
		    WHEN 'owner' THEN 0
		    WHEN 'manager' THEN 1
		    ELSE 2
		  END,
		  cm.created_at ASC,
		  cm.member_type ASC,
		  cm.member_id ASC`, channelID, parseUUID(workspaceID))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list channel members")
		return
	}
	defer rows.Close()
	out := []ChannelMemberResponse{}
	for rows.Next() {
		var typ, name, displayName, role string
		var id pgtype.UUID
		var avatarURL pgtype.Text
		var runtimeStatsRaw []byte
		var createdAt pgtype.Timestamptz
		if err := rows.Scan(&typ, &id, &name, &displayName, &avatarURL, &runtimeStatsRaw, &createdAt, &role); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to read channel members")
			return
		}
		if role == "" {
			role = "member"
		}
		member := ChannelMemberResponse{MemberType: typ, MemberID: uuidToString(id), Name: name, DisplayName: firstNonEmpty(displayName, name), AvatarURL: textToPtr(avatarURL), Role: role, CreatedAt: timestampToString(createdAt)}
		if len(runtimeStatsRaw) > 0 {
			var stats protocol.RuntimeTokenStats
			if json.Unmarshal(runtimeStatsRaw, &stats) == nil {
				member.RuntimeStats = &stats
			}
		}
		out = append(out, member)
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) AddChannelMember(w http.ResponseWriter, r *http.Request) {
	if rejectAgentOnHumanRoute(w, r, "AddChannelMember") {
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	h.addChannelMemberAdapter(w, r, humanMemberManagementActor(workspaceID, userID))
}

func (h *Handler) RemoveChannelMember(w http.ResponseWriter, r *http.Request) {
	if rejectAgentOnHumanRoute(w, r, "RemoveChannelMember") {
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	h.removeChannelMemberAdapter(w, r, humanMemberManagementActor(workspaceID, userID))
}

const channelOwnerChangedCode = "owner_changed"

// Test-only race controls (never set in production):
//
//	entry gate  — after entry owner snapshot, before Begin
//	post-lock   — after channel FOR UPDATE (transfer only), before mutations
//
// so tests can force transfer-first / true lock contention.
var (
	testRoleMutationEntryGate          chan struct{}
	testRoleMutationEntryEntered       int32
	testRoleMutationPreBeginGate       chan struct{}
	testRoleMutationPreBeginEntered    int32
	testRoleMutationPostLockGate       chan struct{}
	testRoleMutationPostLockEntered    int32
	testRoleMutationLockAttemptEntered int32
)

func roleMutationEntryBarrier() {
	if testRoleMutationEntryGate == nil {
		return
	}
	atomic.AddInt32(&testRoleMutationEntryEntered, 1)
	<-testRoleMutationEntryGate
}

func roleMutationPreBeginBarrier() {
	if testRoleMutationPreBeginGate == nil {
		return
	}
	atomic.AddInt32(&testRoleMutationPreBeginEntered, 1)
	<-testRoleMutationPreBeginGate
}

func roleMutationPostLockBarrier() {
	if testRoleMutationPostLockGate == nil {
		return
	}
	atomic.AddInt32(&testRoleMutationPostLockEntered, 1)
	<-testRoleMutationPostLockGate
}

// noteRoleMutationLockAttempt fires immediately before channel FOR UPDATE so
// tests can prove a waiter entered the lock attempt (not only pre-begin).
func noteRoleMutationLockAttempt() {
	atomic.AddInt32(&testRoleMutationLockAttemptEntered, 1)
}

type updateChannelMemberRoleRequest struct {
	Role string `json:"role"`
}

// lockOrdinaryGroupChannelTx locks the channel row and validates ordinary
// non-system, non-archived group. Caller must already hold a transaction.
func (h *Handler) lockOrdinaryGroupChannelTx(
	ctx context.Context,
	tx dbExecutor,
	workspaceID string,
	channelID pgtype.UUID,
) error {
	noteRoleMutationLockAttempt()
	var kind string
	var systemKey pgtype.Text
	var archivedAt pgtype.Timestamptz
	err := tx.QueryRow(ctx, `
		SELECT kind, system_key, archived_at
		FROM channel
		WHERE id = $1 AND workspace_id = $2
		FOR UPDATE`,
		channelID, parseUUID(workspaceID),
	).Scan(&kind, &systemKey, &archivedAt)
	if err != nil {
		return errChannelNotFound
	}
	if kind != "group" {
		return errChannelNotGroup
	}
	if systemKey.Valid {
		return errChannelSystemProtected
	}
	if archivedAt.Valid {
		return errChannelArchived
	}
	return nil
}

var (
	errChannelNotFound        = errors.New("channel not found")
	errChannelNotGroup        = errors.New("channel is not a group")
	errChannelSystemProtected = errors.New("system channel is managed automatically")
	errChannelArchived        = errors.New("channel is archived")
	errNotChannelOwner        = errors.New("not channel owner")
	errOwnerChanged           = errors.New("channel owner changed")
	errChannelMemberNotFound  = errors.New("channel member not found")
)

func writeChannelRoleMutationErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errChannelNotFound):
		writeError(w, http.StatusNotFound, "channel not found")
	case errors.Is(err, errChannelNotGroup):
		writeError(w, http.StatusNotFound, "channel not found")
	case errors.Is(err, errChannelSystemProtected):
		writeSystemChannelProtected(w)
	case errors.Is(err, errChannelArchived):
		writeError(w, http.StatusConflict, "channel is archived")
	case errors.Is(err, errOwnerChanged):
		// Race loser who entered as owner but lost ownership under lock.
		// FE: code=owner_changed → refresh roster, do not retry as generic 403.
		writeCodedError(w, http.StatusForbidden, channelOwnerChangedCode,
			"群主权限已变更，成员列表已刷新。")
	case errors.Is(err, errNotChannelOwner):
		// Plain non-owner — no owner_changed code (Wren must not collapse).
		writeError(w, http.StatusForbidden, "only the channel owner can manage member roles")
	case errors.Is(err, errChannelMemberNotFound):
		writeError(w, http.StatusNotFound, "channel member not found")
	default:
		writeError(w, http.StatusInternalServerError, "failed to update channel member role")
	}
}

// actorIsChannelOwnerRead is a non-locking entry snapshot used only to
// classify 403 responses (plain vs owner_changed). Authorization still uses
// the locked in-tx recheck.
// Returns (isOwner, err). Only pgx.ErrNoRows → (false, nil) plain non-owner.
// Other DB errors must surface as 500 — never disguise infra failure as deny.
func (h *Handler) actorIsChannelOwnerRead(ctx context.Context, workspaceID string, channelID, actorID pgtype.UUID) (bool, error) {
	var role string
	err := h.DB.QueryRow(ctx, `
		SELECT role FROM channel_member
		WHERE channel_id = $1 AND workspace_id = $2
		  AND member_type = 'user' AND member_id = $3`,
		channelID, parseUUID(workspaceID), actorID,
	).Scan(&role)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	return role == "owner", nil
}

func mapOwnerAuthErr(entryWasOwner bool, lockErr error) error {
	if lockErr == nil {
		return nil
	}
	if !errors.Is(lockErr, errNotChannelOwner) {
		return lockErr
	}
	if entryWasOwner {
		return errOwnerChanged
	}
	return errNotChannelOwner
}

// requireActorChannelOwnerTx locks actor membership and requires role=owner.
// Must run after channel row lock in the same transaction.
func requireActorChannelOwnerTx(
	ctx context.Context,
	tx dbExecutor,
	workspaceID string,
	channelID, actorID pgtype.UUID,
) error {
	var role string
	err := tx.QueryRow(ctx, `
		SELECT role FROM channel_member
		WHERE channel_id = $1 AND workspace_id = $2
		  AND member_type = 'user' AND member_id = $3
		FOR UPDATE`,
		channelID, parseUUID(workspaceID), actorID,
	).Scan(&role)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return errNotChannelOwner
		}
		return err
	}
	if role != "owner" {
		return errNotChannelOwner
	}
	return nil
}

// UpdateChannelMemberRole — PATCH /api/channels/{channelId}/members/{memberType}/{memberId}
// Promote/demote only: role must be manager|member.
// Ownership transfer uses POST …/transfer-ownership (Iris/Barry separate op).
func (h *Handler) UpdateChannelMemberRole(w http.ResponseWriter, r *http.Request) {
	if rejectAgentOnHumanRoute(w, r, "UpdateChannelMemberRole") {
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	channelID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "channelId"), "channel id")
	if !ok {
		return
	}
	memberType := chi.URLParam(r, "memberType")
	memberID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "memberId"), "member id")
	if !ok {
		return
	}
	if memberType != "user" && memberType != "agent" {
		writeError(w, http.StatusBadRequest, "member_type must be user or agent")
		return
	}
	var req updateChannelMemberRoleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	newRole := strings.TrimSpace(req.Role)
	if newRole == "owner" {
		writeError(w, http.StatusBadRequest, "use POST .../transfer-ownership to transfer channel ownership")
		return
	}
	if newRole != "manager" && newRole != "member" {
		writeError(w, http.StatusBadRequest, "role must be manager or member")
		return
	}

	entryWasOwner, entryErr := h.actorIsChannelOwnerRead(r.Context(), workspaceID, channelID, parseUUID(userID))
	if entryErr != nil {
		writeError(w, http.StatusInternalServerError, "failed to load channel membership")
		return
	}
	roleMutationEntryBarrier()
	roleMutationPreBeginBarrier()

	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update member role")
		return
	}
	defer tx.Rollback(r.Context())

	if err := h.lockOrdinaryGroupChannelTx(r.Context(), tx, workspaceID, channelID); err != nil {
		writeChannelRoleMutationErr(w, err)
		return
	}
	if err := requireActorChannelOwnerTx(r.Context(), tx, workspaceID, channelID, parseUUID(userID)); err != nil {
		writeChannelRoleMutationErr(w, mapOwnerAuthErr(entryWasOwner, err))
		return
	}

	var currentRole string
	err = tx.QueryRow(r.Context(), `
		SELECT role FROM channel_member
		WHERE channel_id = $1 AND workspace_id = $2 AND member_type = $3 AND member_id = $4
		FOR UPDATE`,
		channelID, parseUUID(workspaceID), memberType, memberID,
	).Scan(&currentRole)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "channel member not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load channel member")
		return
	}
	if currentRole == newRole {
		if err := tx.Commit(r.Context()); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to update member role")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"status":      "ok",
			"member_type": memberType,
			"member_id":   uuidToString(memberID),
			"role":        newRole,
		})
		return
	}
	if currentRole == "owner" {
		var otherOwners int
		_ = tx.QueryRow(r.Context(), `
			SELECT count(*) FROM channel_member
			WHERE channel_id = $1 AND workspace_id = $2
			  AND role = 'owner' AND member_type = 'user' AND member_id <> $3`,
			channelID, parseUUID(workspaceID), memberID,
		).Scan(&otherOwners)
		if otherOwners == 0 {
			writeError(w, http.StatusConflict, "transfer channel ownership before changing the only owner's role")
			return
		}
	}
	tag, err := tx.Exec(r.Context(), `
		UPDATE channel_member
		SET role = $5
		WHERE channel_id = $1 AND workspace_id = $2 AND member_type = $3 AND member_id = $4`,
		channelID, parseUUID(workspaceID), memberType, memberID, newRole)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid channel member role")
		return
	}
	if tag.RowsAffected() != 1 {
		writeError(w, http.StatusNotFound, "channel member not found")
		return
	}
	roleEvent, err := h.insertChannelMemberRoleChangedSystemEventExec(
		r.Context(),
		tx,
		workspaceID,
		channelID,
		parseUUID(userID),
		memberType,
		memberID,
		currentRole,
		newRole,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to record member role change")
		return
	}
	var roleWakeID pgtype.UUID
	if memberType == "agent" {
		roleWakeID, err = insertChannelManagerRoleWakeExec(
			r.Context(),
			tx,
			parseUUID(workspaceID),
			channelID,
			memberID,
			parseUUID(userID),
		)
		if err != nil {
			slog.Error("channel member role change: failed to insert agent wake",
				"workspace_id", workspaceID,
				"channel_id", uuidToString(channelID),
				"agent_id", uuidToString(memberID),
				"error", err,
			)
			writeError(w, http.StatusInternalServerError, "failed to wake agent after role change")
			return
		}
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update member role")
		return
	}
	h.publishChannelToMembers(
		r.Context(),
		protocol.EventChannelMessage,
		workspaceID,
		"system",
		"",
		channelID,
		roleEvent,
	)
	if roleWakeID.Valid {
		h.publishChannelManagerRoleWake(r.Context(), roleWakeID)
	}
	h.publish(protocol.EventChannelUpdated, workspaceID, "member", userID, map[string]any{"id": uuidToString(channelID)})
	writeJSON(w, http.StatusOK, map[string]any{
		"status":      "ok",
		"member_type": memberType,
		"member_id":   uuidToString(memberID),
		"role":        newRole,
	})
}

// TransferChannelOwnership — POST /api/channels/{channelId}/members/{memberType}/{memberId}/transfer-ownership
// Explicit ownership handoff (Iris). Target must be human. Prior owner(s) → manager.
// Ownership UPDATEs and channel_ownership_transferred system row share one transaction.
func (h *Handler) TransferChannelOwnership(w http.ResponseWriter, r *http.Request) {
	if rejectAgentOnHumanRoute(w, r, "TransferChannelOwnership") {
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	channelID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "channelId"), "channel id")
	if !ok {
		return
	}
	memberType := chi.URLParam(r, "memberType")
	memberID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "memberId"), "member id")
	if !ok {
		return
	}
	if memberType != "user" {
		writeError(w, http.StatusBadRequest, "only human members can receive channel ownership")
		return
	}

	entryWasOwner, entryErr := h.actorIsChannelOwnerRead(r.Context(), workspaceID, channelID, parseUUID(userID))
	if entryErr != nil {
		writeError(w, http.StatusInternalServerError, "failed to load channel membership")
		return
	}
	roleMutationEntryBarrier()
	roleMutationPreBeginBarrier()

	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to transfer ownership")
		return
	}
	defer tx.Rollback(r.Context())

	if err := h.lockOrdinaryGroupChannelTx(r.Context(), tx, workspaceID, channelID); err != nil {
		writeChannelRoleMutationErr(w, err)
		return
	}
	// Test-only: hold after channel lock so PATCH can queue on the same lock
	// (deterministic transfer-first → PATCH owner_changed).
	roleMutationPostLockBarrier()
	// Serialize all membership mutations for this channel, then re-check actor
	// is still owner after waiting for any concurrent transfer/role lock.
	rows, err := tx.Query(r.Context(), `
		SELECT member_type, member_id, role
		FROM channel_member
		WHERE channel_id = $1 AND workspace_id = $2
		FOR UPDATE`,
		channelID, parseUUID(workspaceID))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to lock channel members")
		return
	}
	var (
		actorRole      string
		actorFound     bool
		targetFound    bool
		targetRole     string
		previousOwners []string
	)
	actorUUID := parseUUID(userID)
	for rows.Next() {
		var typ, role string
		var id pgtype.UUID
		if err := rows.Scan(&typ, &id, &role); err != nil {
			rows.Close()
			writeError(w, http.StatusInternalServerError, "failed to read channel members")
			return
		}
		if typ == "user" && id == actorUUID {
			actorFound = true
			actorRole = role
		}
		if typ == "user" && id == memberID {
			targetFound = true
			targetRole = role
		}
		if role == "owner" && typ == "user" {
			previousOwners = append(previousOwners, uuidToString(id))
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read channel members")
		return
	}
	if !actorFound || actorRole != "owner" {
		writeChannelRoleMutationErr(w, mapOwnerAuthErr(entryWasOwner, errNotChannelOwner))
		return
	}
	if !targetFound {
		writeError(w, http.StatusNotFound, "channel member not found")
		return
	}
	if targetRole == "owner" {
		// Idempotent: already owner.
		if err := tx.Commit(r.Context()); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to transfer ownership")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"status":            "ok",
			"member_type":       "user",
			"member_id":         uuidToString(memberID),
			"role":              "owner",
			"previous_owner_id": uuidToString(memberID),
		})
		return
	}

	if _, err := tx.Exec(r.Context(), `
		UPDATE channel_member
		SET role = 'manager'
		WHERE channel_id = $1 AND workspace_id = $2 AND role = 'owner'`,
		channelID, parseUUID(workspaceID)); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to transfer ownership")
		return
	}
	tag, err := tx.Exec(r.Context(), `
		UPDATE channel_member
		SET role = 'owner'
		WHERE channel_id = $1 AND workspace_id = $2 AND member_type = 'user' AND member_id = $3`,
		channelID, parseUUID(workspaceID), memberID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to transfer ownership")
		return
	}
	if tag.RowsAffected() != 1 {
		writeError(w, http.StatusNotFound, "channel member not found")
		return
	}

	// Durable audit in the same transaction (Parker/Iris: transfer ⇔ audit).
	auditMsg, err := h.insertChannelMemberSystemEventExec(
		r.Context(), tx, workspaceID, channelID,
		channelOwnershipTransferredEvent, channelMemberActorUser, actorUUID, "user", memberID,
	)
	if err != nil {
		// Fail closed: do not commit ownership without audit row.
		writeError(w, http.StatusInternalServerError, "failed to record ownership transfer")
		return
	}

	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to transfer ownership")
		return
	}

	// Realtime only after durable commit.
	h.publishChannelToMembers(r.Context(), protocol.EventChannelMessage, workspaceID, "system", "", channelID, auditMsg)
	h.publish(protocol.EventChannelUpdated, workspaceID, "member", userID, map[string]any{"id": uuidToString(channelID)})
	prev := userID
	if len(previousOwners) > 0 {
		prev = previousOwners[0]
	}
	slog.Info("channel ownership transferred",
		"workspace_id", workspaceID,
		"channel_id", uuidToString(channelID),
		"previous_owner_id", prev,
		"new_owner_id", uuidToString(memberID),
		"actor_id", userID,
	)
	writeJSON(w, http.StatusOK, map[string]any{
		"status":            "ok",
		"member_type":       "user",
		"member_id":         uuidToString(memberID),
		"role":              "owner",
		"previous_owner_id": prev,
	})
}

func (h *Handler) ListChannelMessages(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	channelID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "channelId"), "channel id")
	if !ok {
		return
	}
	userUUID := parseUUID(userID)
	supervisor := h.channelUserIsAgentDMSupervisor(r.Context(), workspaceID, channelID, userUUID)
	if !h.requireChannelUserViewer(w, r.Context(), workspaceID, channelID, userUUID) {
		return
	}
	ch, found := h.getChannel(r.Context(), workspaceID, channelID)
	if !found {
		writeError(w, http.StatusNotFound, "channel not found")
		return
	}
	if !supervisor && !h.requireDMChannelAgentAccess(w, r, workspaceID, userID, ch, false) {
		return
	}
	limit, beforeSeq, beforeCreatedAt, beforeID, aroundSeq, err := parseChannelMessagesPageParams(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if aroundSeq > 0 {
		h.listChannelMessagesAround(w, r, channelID, workspaceID, userID, limit, aroundSeq)
		return
	}

	rows, err := h.DB.Query(r.Context(), `
			SELECT m.id, m.channel_id, m.workspace_id, m.author_type, m.author_id, m.author_name, m.content, m.parts, m.source, m.external_message_id, m.client_message_id, m.reply_to_message_id, m.quote_message_id, m.quote_snapshot, m.thread_root_message_id, m.thread_id, m.trigger_depth, m.seq, m.created_at, m.edited_at, m.deleted_at
		FROM channel_message m
		WHERE m.channel_id = $1 AND m.workspace_id = $2
		  AND m.thread_root_message_id IS NULL
		  AND (
		    m.deleted_at IS NULL
		    OR EXISTS (
		      SELECT 1
		      FROM channel_message replies
		      WHERE replies.workspace_id = m.workspace_id
		        AND replies.channel_id = m.channel_id
		        AND replies.thread_root_message_id = m.id
		        AND replies.deleted_at IS NULL
		    )
		  )
		  AND (
		    ($3::bigint = 0 AND $4::timestamptz IS NULL AND $5::uuid IS NULL)
		    OR ($3::bigint > 0 AND m.seq < $3::bigint)
		    OR ($3::bigint = 0 AND (m.created_at, m.id) < ($4::timestamptz, $5::uuid))
		  )
		ORDER BY m.seq DESC
		LIMIT $6`, channelID, parseUUID(workspaceID), beforeSeq, beforeCreatedAt, beforeID, limit+1)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list channel messages")
		return
	}
	defer rows.Close()
	desc := []ChannelMessageResponse{}
	for rows.Next() {
		msg, err := scanChannelMessage(rows)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to read channel messages")
			return
		}
		desc = append(desc, msg)
	}
	hasMore := len(desc) > limit
	if hasMore {
		desc = desc[:limit]
	}
	var nextCursor *ChannelMessagesCursorResponse
	if hasMore && len(desc) > 0 {
		oldest := desc[len(desc)-1]
		nextCursor = &ChannelMessagesCursorResponse{
			CreatedAt: oldest.CreatedAt,
			ID:        oldest.ID,
			Seq:       oldest.Seq,
		}
	}
	out := make([]ChannelMessageResponse, 0, len(desc))
	for i := len(desc) - 1; i >= 0; i-- {
		out = append(out, desc[i])
	}
	attachChannelMessageExtras := func(msgs []ChannelMessageResponse) {
		h.attachChannelMessageAttachments(r.Context(), workspaceID, msgs)
		h.attachGraphMemoryCitationCounts(r.Context(), workspaceID, msgs)
		h.attachChannelMessageReactions(r.Context(), workspaceID, msgs)
		h.attachChannelMessageReplySummaries(r.Context(), workspaceID, msgs)
		h.attachChannelMessageQuotes(r.Context(), workspaceID, msgs)
		h.attachChannelMessageThreadRootSummaries(r.Context(), workspaceID, msgs)
		h.attachChannelMessageThreadMetadata(r.Context(), workspaceID, parseUUID(userID), msgs)
		h.attachChannelMessageThreadReadModel(r.Context(), workspaceID, msgs)
		applyChannelMessageTombstoneReadModel(msgs)
	}
	attachChannelMessageExtras(out)
	writeJSON(w, http.StatusOK, ChannelMessagesPageResponse{
		Messages:   out,
		Limit:      limit,
		HasMore:    hasMore,
		NextCursor: nextCursor,
	})
}

// channelMessageColumnList is the SELECT column list for channel_message queries.
const channelMessageColumnList = `m.id, m.channel_id, m.workspace_id, m.author_type, m.author_id, m.author_name, m.content, m.parts, m.source, m.external_message_id, m.client_message_id, m.reply_to_message_id, m.quote_message_id, m.quote_snapshot, m.thread_root_message_id, m.thread_id, m.trigger_depth, m.seq, m.created_at, m.edited_at, m.deleted_at`

// channelMessageWhereClause is the WHERE clause for listing visible channel messages (shared by before/around paths).
const channelMessageWhereClause = `m.channel_id = $1 AND m.workspace_id = $2
	  AND m.thread_root_message_id IS NULL
	  AND (
	    m.deleted_at IS NULL
	    OR EXISTS (
	      SELECT 1
	      FROM channel_message replies
	      WHERE replies.workspace_id = m.workspace_id
	        AND replies.channel_id = m.channel_id
	        AND replies.thread_root_message_id = m.id
	        AND replies.deleted_at IS NULL
	    )
	  )`

func (h *Handler) queryChannelMessages(ctx context.Context, channelID, workspaceID pgtype.UUID, whereExtra string, args ...interface{}) ([]ChannelMessageResponse, error) {
	query := fmt.Sprintf(`SELECT `+channelMessageColumnList+`
		FROM channel_message m
		WHERE `+channelMessageWhereClause+`
		AND %s`, whereExtra)
	allArgs := make([]interface{}, 0, 2+len(args))
	allArgs = append(allArgs, channelID, workspaceID)
	allArgs = append(allArgs, args...)
	rows, err := h.DB.Query(ctx, query, allArgs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var msgs []ChannelMessageResponse
	for rows.Next() {
		msg, err := scanChannelMessage(rows)
		if err != nil {
			return nil, err
		}
		msgs = append(msgs, msg)
	}
	return msgs, rows.Err()
}

func (h *Handler) listChannelMessagesAround(w http.ResponseWriter, r *http.Request, channelID pgtype.UUID, workspaceIDStr string, userIDStr string, limit int, aroundSeq int64) {
	workspaceID := parseUUID(workspaceIDStr)
	userID := parseUUID(userIDStr)
	limitBefore := limit / 2

	// Query A: messages before or at anchor (seq <= aroundSeq), newest-first
	beforeMsgs, err := h.queryChannelMessages(r.Context(), channelID, workspaceID,
		`m.seq <= $3::bigint
		ORDER BY m.seq DESC
		LIMIT $4`, pgtype.Int8{Int64: aroundSeq, Valid: true}, pgtype.Int8{Int64: int64(limitBefore + 1), Valid: true})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list channel messages")
		return
	}

	hasMore := len(beforeMsgs) > limitBefore
	if hasMore {
		beforeMsgs = beforeMsgs[:limitBefore]
	}

	// Symmetric backfill: if before side is short, after side gets the remaining capacity
	actualBefore := len(beforeMsgs)
	remainingAfter := limit - actualBefore

	// Query B: messages after anchor (seq > aroundSeq), oldest-first
	afterMsgs, err := h.queryChannelMessages(r.Context(), channelID, workspaceID,
		`m.seq > $3::bigint
		ORDER BY m.seq ASC
		LIMIT $4`, pgtype.Int8{Int64: aroundSeq, Valid: true}, pgtype.Int8{Int64: int64(remainingAfter + 1), Valid: true})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list channel messages")
		return
	}

	hasMoreAfter := len(afterMsgs) > remainingAfter
	if hasMoreAfter {
		afterMsgs = afterMsgs[:remainingAfter]
	}

	actualAfter := len(afterMsgs)

	// Reverse symmetric backfill: if after side is short AND before side was at full capacity,
	// re-query before side with the unused capacity (anchor near end of history)
	if actualAfter < remainingAfter && actualBefore == limitBefore {
		extraBefore := remainingAfter - actualAfter
		limitBeforeExtra := limitBefore + extraBefore
		beforeMsgs, err = h.queryChannelMessages(r.Context(), channelID, workspaceID,
			`m.seq <= $3::bigint
			ORDER BY m.seq DESC
			LIMIT $4`, pgtype.Int8{Int64: aroundSeq, Valid: true}, pgtype.Int8{Int64: int64(limitBeforeExtra + 1), Valid: true})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list channel messages")
			return
		}
		hasMore = len(beforeMsgs) > limitBeforeExtra
		if hasMore {
			beforeMsgs = beforeMsgs[:limitBeforeExtra]
		}
		actualBefore = len(beforeMsgs)
	}

	// Unread total: visible messages after the anchor, excluding the caller's
	// own (matching sidebar real_unread_count exactly — same filter, same SQL).
	// Serves as the primary divider count source in around mode (single-response
	// snapshot = entry freeze is automatic).
	var unreadTotal int
	if err := h.DB.QueryRow(r.Context(), `
		SELECT COUNT(*) FROM channel_message m
		WHERE m.channel_id = $1 AND m.workspace_id = $2
		  AND m.seq > $3::bigint
		  AND m.author_type <> 'system'
		  AND m.thread_root_message_id IS NULL
		  AND m.deleted_at IS NULL
		  AND NOT (m.author_type = 'user' AND m.author_id = $4::uuid)`, channelID, workspaceID, pgtype.Int8{Int64: aroundSeq, Valid: true}, parseUUID(userIDStr)).Scan(&unreadTotal); err != nil {
		unreadTotal = 0
	}
	// Calculate cursor for the before (older) direction
	var nextCursor *ChannelMessagesCursorResponse
	if hasMore && actualBefore > 0 {
		oldestBefore := beforeMsgs[actualBefore-1] // last in DESC order = oldest seq
		nextCursor = &ChannelMessagesCursorResponse{
			CreatedAt: oldestBefore.CreatedAt,
			ID:        oldestBefore.ID,
			Seq:       oldestBefore.Seq,
		}
	}

	// Calculate cursor for the after (newer) direction
	var afterCursor *ChannelMessagesCursorResponse
	if hasMoreAfter && actualAfter > 0 {
		newestAfter := afterMsgs[len(afterMsgs)-1] // last in ASC order = newest seq
		afterCursor = &ChannelMessagesCursorResponse{
			CreatedAt: newestAfter.CreatedAt,
			ID:        newestAfter.ID,
			Seq:       newestAfter.Seq,
		}
	}

	// Reverse beforeMsgs to ASC order for the merged result
	out := make([]ChannelMessageResponse, 0, len(beforeMsgs)+len(afterMsgs))
	for i := len(beforeMsgs) - 1; i >= 0; i-- {
		out = append(out, beforeMsgs[i])
	}
	out = append(out, afterMsgs...)

	// Anchor index: last message with seq <= aroundSeq (0-based in the ASC-ordered merged array)
	anchorIndex := actualBefore - 1

	// Attach extras
	attachChannelMessageExtras := func(msgs []ChannelMessageResponse) {
		h.attachChannelMessageAttachments(r.Context(), workspaceIDStr, msgs)
		h.attachGraphMemoryCitationCounts(r.Context(), workspaceIDStr, msgs)
		h.attachChannelMessageReactions(r.Context(), workspaceIDStr, msgs)
		h.attachChannelMessageReplySummaries(r.Context(), workspaceIDStr, msgs)
		h.attachChannelMessageQuotes(r.Context(), workspaceIDStr, msgs)
		h.attachChannelMessageThreadRootSummaries(r.Context(), workspaceIDStr, msgs)
		h.attachChannelMessageThreadMetadata(r.Context(), workspaceIDStr, userID, msgs)
		h.attachChannelMessageThreadReadModel(r.Context(), workspaceIDStr, msgs)
		applyChannelMessageTombstoneReadModel(msgs)
	}
	attachChannelMessageExtras(out)

	writeJSON(w, http.StatusOK, ChannelMessagesPageResponse{
		Messages:     out,
		Limit:        limit,
		HasMore:      hasMore,
		NextCursor:   nextCursor,
		AnchorIndex:  anchorIndex,
		HasMoreAfter: hasMoreAfter,
		AfterCursor:  afterCursor,
		UnreadTotal:  unreadTotal,
	})
}

func (h *Handler) SearchGlobal(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	if _, ok := h.requireWorkspaceMember(w, r, workspaceID, "workspace not found"); !ok {
		return
	}
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	scope := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("scope")))
	if scope == "" {
		scope = "all"
	}
	switch scope {
	case "all", "messages", "channels", "dms", "people":
	default:
		writeCodedError(w, http.StatusBadRequest, "invalid_search_scope", "invalid search scope")
		return
	}
	authorType, authorID, hasAuthor, ok := parseMessageSearchAuthorFilter(w, r)
	if !ok {
		return
	}
	includeThread := parseIncludeThreadQuery(r)
	limit := boundedQueryInt(r, "limit", 20, 50)
	resp := GlobalSearchResponse{
		Query:    query,
		Scope:    scope,
		Messages: []GlobalSearchMessageResult{},
		Channels: []GlobalSearchChannelResult{},
		DMs:      []GlobalSearchChannelResult{},
		People:   []GlobalSearchPersonResult{},
	}
	// Author/from: search may omit q when scope=messages; other scopes still need q.
	if query == "" && !(hasAuthor && scope == "messages") {
		writeJSON(w, http.StatusOK, resp)
		return
	}
	pattern := ""
	if query != "" {
		pattern = "%" + escapeLike(query) + "%"
	}
	wsID := parseUUID(workspaceID)
	uid := parseUUID(userID)

	if scope == "all" || scope == "messages" {
		if !h.globalSearchMessages(w, r, wsID, uid, pattern, query, limit, includeThread, authorType, authorID, hasAuthor, &resp) {
			return
		}
	}
	if query != "" && (scope == "all" || scope == "channels") {
		if !h.globalSearchChannels(w, r, wsID, uid, pattern, "group", limit, &resp) {
			return
		}
	}
	if query != "" && (scope == "all" || scope == "dms") {
		if !h.globalSearchChannels(w, r, wsID, uid, pattern, "dm", limit, &resp) {
			return
		}
	}
	if query != "" && (scope == "all" || scope == "people") {
		if !h.globalSearchPeople(w, r, wsID, uid, pattern, limit, &resp) {
			return
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) globalSearchMessages(
	w http.ResponseWriter,
	r *http.Request,
	workspaceID, userID pgtype.UUID,
	pattern, query string,
	limit int,
	includeThread bool,
	authorType, authorID string,
	hasAuthor bool,
	resp *GlobalSearchResponse,
) bool {
	args := []any{workspaceID, userID}
	where := []string{
		"m.workspace_id = $1",
		"m.author_type IN ('user', 'agent')",
		"m.deleted_at IS NULL",
	}
	if !includeThread {
		where = append(where, "m.thread_root_message_id IS NULL")
	}
	if hasAuthor {
		args = append(args, authorType, parseUUID(authorID))
		where = append(where, fmt.Sprintf("m.author_type = $%d AND m.author_id = $%d", len(args)-1, len(args)))
	}
	if pattern != "" {
		args = append(args, pattern)
		where = append(where, fmt.Sprintf("m.content ILIKE $%d ESCAPE '\\'", len(args)))
	}
	whereSQL := strings.Join(where, " AND ")
	viewerJoin := `
		JOIN channel ch ON ch.id = m.channel_id AND ch.workspace_id = m.workspace_id
		JOIN channel_member viewer ON viewer.channel_id = ch.id
		 AND viewer.workspace_id = ch.workspace_id
		 AND viewer.member_type = 'user'
		 AND viewer.member_id = $2`

	countSQL := `SELECT count(*) FROM channel_message m` + viewerJoin + ` WHERE ` + whereSQL
	if err := h.DB.QueryRow(r.Context(), countSQL, args...).Scan(&resp.Counts.Messages); err != nil {
		writeCodedError(w, http.StatusInternalServerError, "global_search_message_count_failed", "failed to count global message search results")
		return false
	}
	args = append(args, limit)
	listSQL := `
		SELECT m.id, m.channel_id, ch.name, ch.kind, m.thread_root_message_id,
		       m.author_type, m.author_id, m.author_name, m.content, m.created_at,
		       count(*) OVER (PARTITION BY COALESCE(m.thread_root_message_id, m.id))::int AS hit_count
		FROM channel_message m` + viewerJoin + `
		WHERE ` + whereSQL + `
		ORDER BY m.created_at DESC, m.id DESC
		LIMIT $` + strconv.Itoa(len(args))
	rows, err := h.DB.Query(r.Context(), listSQL, args...)
	if err != nil {
		writeCodedError(w, http.StatusInternalServerError, "global_search_messages_failed", "failed to search messages")
		return false
	}
	defer rows.Close()
	for rows.Next() {
		var id, chID, threadRootID, rowAuthorID pgtype.UUID
		var channelName, channelKind, rowAuthorType, authorName, content string
		var createdAt pgtype.Timestamptz
		var hitCount int
		if err := rows.Scan(&id, &chID, &channelName, &channelKind, &threadRootID, &rowAuthorType, &rowAuthorID, &authorName, &content, &createdAt, &hitCount); err != nil {
			writeCodedError(w, http.StatusInternalServerError, "global_search_message_read_failed", "failed to read message search results")
			return false
		}
		resp.Messages = append(resp.Messages, GlobalSearchMessageResult{
			ResultType:          "message",
			MessageID:           uuidToString(id),
			ChannelID:           uuidToString(chID),
			ChannelName:         channelName,
			ChannelKind:         channelKind,
			ThreadRootMessageID: uuidToPtr(threadRootID),
			InThread:            threadRootID.Valid,
			HitCount:            hitCount,
			AuthorType:          rowAuthorType,
			AuthorID:            uuidToPtr(rowAuthorID),
			AuthorName:          authorName,
			Content:             content,
			Snippet:             searchSnippet(content, query),
			HighlightRanges:     searchHighlightRanges(content, query),
			CreatedAt:           timestampToString(createdAt),
		})
	}
	return true
}

func (h *Handler) globalSearchChannels(w http.ResponseWriter, r *http.Request, workspaceID, userID pgtype.UUID, pattern, kind string, limit int, resp *GlobalSearchResponse) bool {
	countTarget := &resp.Counts.Channels
	if kind == "dm" {
		countTarget = &resp.Counts.DMs
	}
	if err := h.DB.QueryRow(r.Context(), `
		SELECT count(*)
		FROM channel ch
		JOIN channel_member viewer ON viewer.channel_id = ch.id
		 AND viewer.workspace_id = ch.workspace_id
		 AND viewer.member_type = 'user'
		 AND viewer.member_id = $2
		WHERE ch.workspace_id = $1
		  AND ch.kind = $3
		  AND ch.archived_at IS NULL
		  AND (ch.name ILIKE $4 ESCAPE '\' OR COALESCE(ch.description, '') ILIKE $4 ESCAPE '\')`, workspaceID, userID, kind, pattern).Scan(countTarget); err != nil {
		writeCodedError(w, http.StatusInternalServerError, "global_search_channel_count_failed", "failed to count channel search results")
		return false
	}
	rows, err := h.DB.Query(r.Context(), `
		SELECT ch.id, ch.name, ch.kind, ch.description
		FROM channel ch
		JOIN channel_member viewer ON viewer.channel_id = ch.id
		 AND viewer.workspace_id = ch.workspace_id
		 AND viewer.member_type = 'user'
		 AND viewer.member_id = $2
		WHERE ch.workspace_id = $1
		  AND ch.kind = $3
		  AND ch.archived_at IS NULL
		  AND (ch.name ILIKE $4 ESCAPE '\' OR COALESCE(ch.description, '') ILIKE $4 ESCAPE '\')
		ORDER BY ch.updated_at DESC, ch.name ASC
		LIMIT $5`, workspaceID, userID, kind, pattern, limit)
	if err != nil {
		writeCodedError(w, http.StatusInternalServerError, "global_search_channels_failed", "failed to search channels")
		return false
	}
	defer rows.Close()
	for rows.Next() {
		var id pgtype.UUID
		var name, rowKind string
		var description pgtype.Text
		if err := rows.Scan(&id, &name, &rowKind, &description); err != nil {
			writeCodedError(w, http.StatusInternalServerError, "global_search_channel_read_failed", "failed to read channel search results")
			return false
		}
		item := GlobalSearchChannelResult{ChannelID: uuidToString(id), Name: name, Kind: rowKind, Description: textToPtr(description)}
		if kind == "dm" {
			resp.DMs = append(resp.DMs, item)
		} else {
			resp.Channels = append(resp.Channels, item)
		}
	}
	return true
}

func (h *Handler) globalSearchPeople(w http.ResponseWriter, r *http.Request, workspaceID, userID pgtype.UUID, pattern string, limit int, resp *GlobalSearchResponse) bool {
	if err := h.DB.QueryRow(r.Context(), `
		SELECT count(*) FROM (
			SELECT u.id
			FROM member m
			JOIN "user" u ON u.id = m.user_id
			WHERE m.workspace_id = $1
			  AND (u.name ILIKE $2 ESCAPE '\' OR COALESCE(u.display_name, '') ILIKE $2 ESCAPE '\' OR u.email ILIKE $2 ESCAPE '\')
			UNION ALL
			SELECT a.id
			FROM agent a
			WHERE a.workspace_id = $1
			  AND a.archived_at IS NULL
			  AND (a.name ILIKE $2 ESCAPE '\' OR COALESCE(a.display_name, '') ILIKE $2 ESCAPE '\')
		) people`, workspaceID, pattern).Scan(&resp.Counts.People); err != nil {
		writeCodedError(w, http.StatusInternalServerError, "global_search_people_count_failed", "failed to count people search results")
		return false
	}
	rows, err := h.DB.Query(r.Context(), `
		SELECT actor_type, actor_id, name, display_name, avatar_url
		FROM (
			SELECT 'user'::text AS actor_type, u.id AS actor_id, u.name, COALESCE(NULLIF(u.display_name, ''), u.name, u.email) AS display_name, u.avatar_url, u.updated_at AS sort_at
			FROM member m
			JOIN "user" u ON u.id = m.user_id
			WHERE m.workspace_id = $1
			  AND (u.name ILIKE $2 ESCAPE '\' OR COALESCE(u.display_name, '') ILIKE $2 ESCAPE '\' OR u.email ILIKE $2 ESCAPE '\')
			UNION ALL
			SELECT 'agent'::text AS actor_type, a.id AS actor_id, a.name, COALESCE(NULLIF(a.display_name, ''), a.name) AS display_name, a.avatar_url, a.updated_at AS sort_at
			FROM agent a
			WHERE a.workspace_id = $1
			  AND a.archived_at IS NULL
			  AND (a.name ILIKE $2 ESCAPE '\' OR COALESCE(a.display_name, '') ILIKE $2 ESCAPE '\')
		) people
		ORDER BY sort_at DESC, display_name ASC
		LIMIT $3`, workspaceID, pattern, limit)
	if err != nil {
		writeCodedError(w, http.StatusInternalServerError, "global_search_people_failed", "failed to search people")
		return false
	}
	defer rows.Close()
	for rows.Next() {
		var actorType, name, displayName string
		var actorID pgtype.UUID
		var avatarURL pgtype.Text
		if err := rows.Scan(&actorType, &actorID, &name, &displayName, &avatarURL); err != nil {
			writeCodedError(w, http.StatusInternalServerError, "global_search_people_read_failed", "failed to read people search results")
			return false
		}
		resp.People = append(resp.People, GlobalSearchPersonResult{ActorType: actorType, ActorID: uuidToString(actorID), Name: name, DisplayName: displayName, AvatarURL: textToPtr(avatarURL)})
	}
	return true
}

func searchSnippet(content, query string) string {
	content = strings.TrimSpace(content)
	query = strings.TrimSpace(query)
	if content == "" || query == "" {
		return content
	}
	contentRunes := []rune(content)
	lowerContent := []rune(strings.ToLower(content))
	lowerQuery := []rune(strings.ToLower(query))
	matchStart := indexRunes(lowerContent, lowerQuery, 0)
	if matchStart < 0 {
		if len(contentRunes) <= 160 {
			return content
		}
		return string(contentRunes[:160]) + "…"
	}
	matchEnd := matchStart + len(lowerQuery)
	start := matchStart - 60
	if start < 0 {
		start = 0
	}
	end := matchEnd + 60
	if end > len(contentRunes) {
		end = len(contentRunes)
	}
	snippet := strings.TrimSpace(string(contentRunes[start:end]))
	if start > 0 {
		snippet = "…" + snippet
	}
	if end < len(contentRunes) {
		snippet += "…"
	}
	return snippet
}

func searchHighlightRanges(content, query string) []GlobalSearchHighlightRange {
	content = strings.TrimSpace(content)
	query = strings.TrimSpace(query)
	if content == "" || query == "" {
		return []GlobalSearchHighlightRange{}
	}
	lowerContent := []rune(strings.ToLower(content))
	lowerQuery := []rune(strings.ToLower(query))
	ranges := []GlobalSearchHighlightRange{}
	for offset := 0; offset < len(lowerContent); {
		idx := indexRunes(lowerContent, lowerQuery, offset)
		if idx < 0 {
			break
		}
		end := idx + len(lowerQuery)
		ranges = append(ranges, GlobalSearchHighlightRange{Start: idx, End: end})
		offset = end
	}
	return ranges
}

func indexRunes(haystack, needle []rune, offset int) int {
	if len(needle) == 0 || len(haystack) < len(needle) || offset > len(haystack)-len(needle) {
		return -1
	}
	for i := offset; i <= len(haystack)-len(needle); i++ {
		matched := true
		for j := range needle {
			if haystack[i+j] != needle[j] {
				matched = false
				break
			}
		}
		if matched {
			return i
		}
	}
	return -1
}

func (h *Handler) SearchChannelMessages(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	channelID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "channelId"), "channel id")
	if !ok {
		return
	}
	if !h.requireChannelUserViewer(w, r.Context(), workspaceID, channelID, parseUUID(userID)) {
		return
	}

	query := strings.TrimSpace(r.URL.Query().Get("q"))
	includeThread := parseIncludeThreadQuery(r)
	authorType, authorID, hasAuthor, ok := parseMessageSearchAuthorFilter(w, r)
	if !ok {
		return
	}
	resp := ChannelMessageSearchResponse{
		Query:         query,
		IncludeThread: includeThread,
		AuthorType:    authorType,
		AuthorID:      authorID,
		Scope:         "channel",
		Results:       []ChannelMessageSearchResult{},
	}
	// Text search still requires q; author/from: search may omit q.
	if query == "" && !hasAuthor {
		writeJSON(w, http.StatusOK, resp)
		return
	}

	limit := boundedQueryInt(r, "limit", 50, 100)
	offset := 0
	if raw := strings.TrimSpace(r.URL.Query().Get("offset")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n >= 0 {
			offset = n
			if offset > 10_000 {
				offset = 10_000
			}
		}
	}

	args := []any{channelID, parseUUID(workspaceID)}
	where := []string{
		"m.channel_id = $1",
		"m.workspace_id = $2",
		"m.author_type IN ('user', 'agent')",
		"m.deleted_at IS NULL",
	}
	if !includeThread {
		where = append(where, "m.thread_root_message_id IS NULL")
	}
	if hasAuthor {
		args = append(args, authorType, parseUUID(authorID))
		where = append(where, fmt.Sprintf("m.author_type = $%d AND m.author_id = $%d", len(args)-1, len(args)))
	}
	if query != "" {
		args = append(args, "%"+escapeLike(query)+"%")
		where = append(where, fmt.Sprintf("m.content ILIKE $%d ESCAPE '\\'", len(args)))
	}
	whereSQL := strings.Join(where, " AND ")

	countSQL := `SELECT count(*) FROM channel_message m WHERE ` + whereSQL
	if err := h.DB.QueryRow(r.Context(), countSQL, args...).Scan(&resp.Total); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to search channel messages")
		return
	}

	args = append(args, limit, offset)
	listSQL := `
		SELECT m.id, m.channel_id, m.thread_root_message_id, m.author_type, m.author_id, m.author_name,
		       m.content, m.created_at
		FROM channel_message m
		WHERE ` + whereSQL + `
		ORDER BY m.seq ASC
		LIMIT $` + strconv.Itoa(len(args)-1) + ` OFFSET $` + strconv.Itoa(len(args))
	rows, err := h.DB.Query(r.Context(), listSQL, args...)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to search channel messages")
		return
	}
	defer rows.Close()
	for rows.Next() {
		var id, chID, threadRootID, rowAuthorID pgtype.UUID
		var rowAuthorType, authorName, content string
		var createdAt pgtype.Timestamptz
		if err := rows.Scan(&id, &chID, &threadRootID, &rowAuthorType, &rowAuthorID, &authorName, &content, &createdAt); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to read channel message search results")
			return
		}
		resp.Results = append(resp.Results, ChannelMessageSearchResult{
			MessageID:           uuidToString(id),
			ChannelID:           uuidToString(chID),
			ThreadRootMessageID: uuidToPtr(threadRootID),
			InThread:            threadRootID.Valid,
			Type:                rowAuthorType,
			AuthorID:            uuidToPtr(rowAuthorID),
			AuthorName:          authorName,
			Content:             content,
			CreatedAt:           timestampToString(createdAt),
		})
	}
	writeJSON(w, http.StatusOK, resp)
}

// parseIncludeThreadQuery defaults to true (LRM-874 / LRM-862: 默认含 thread).
func parseIncludeThreadQuery(r *http.Request) bool {
	raw := strings.TrimSpace(r.URL.Query().Get("include_thread"))
	if raw == "" {
		return true
	}
	switch strings.ToLower(raw) {
	case "0", "false", "no", "off":
		return false
	default:
		return true
	}
}

// parseMessageSearchAuthorFilter reads author_type + author_id for from:@ search.
// Both must be present together; author_type is user|agent.
func parseMessageSearchAuthorFilter(w http.ResponseWriter, r *http.Request) (authorType, authorID string, hasAuthor, ok bool) {
	authorType = strings.ToLower(strings.TrimSpace(r.URL.Query().Get("author_type")))
	authorID = strings.TrimSpace(r.URL.Query().Get("author_id"))
	if authorType == "" && authorID == "" {
		return "", "", false, true
	}
	if authorType == "" || authorID == "" {
		writeError(w, http.StatusBadRequest, "author_type and author_id must be provided together")
		return "", "", false, false
	}
	if authorType != "user" && authorType != "agent" {
		writeError(w, http.StatusBadRequest, "author_type must be user or agent")
		return "", "", false, false
	}
	if _, parseOK := parseUUIDOrBadRequest(w, authorID, "author_id"); !parseOK {
		return "", "", false, false
	}
	return authorType, authorID, true, true
}

func (h *Handler) validateChannelReplyTarget(w http.ResponseWriter, ctx context.Context, workspaceID string, channelID pgtype.UUID, raw *string) (pgtype.UUID, bool) {
	if raw == nil || strings.TrimSpace(*raw) == "" {
		return pgtype.UUID{}, true
	}
	replyToID, ok := parseUUIDOrBadRequest(w, strings.TrimSpace(*raw), "reply_to_message_id")
	if !ok {
		return pgtype.UUID{}, false
	}
	var exists bool
	if err := h.DB.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM channel_message
			WHERE id = $1 AND channel_id = $2 AND workspace_id = $3 AND author_type <> 'system'
		)`, replyToID, channelID, parseUUID(workspaceID)).Scan(&exists); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to validate reply target")
		return pgtype.UUID{}, false
	}
	if !exists {
		writeError(w, http.StatusBadRequest, "reply_to_message_id must reference a message in this channel")
		return pgtype.UUID{}, false
	}
	return replyToID, true
}

func (h *Handler) resolveChannelQuoteTarget(w http.ResponseWriter, ctx context.Context, workspaceID string, channelID pgtype.UUID, request SendChannelMessageRequest, threadRootID pgtype.UUID) (pgtype.UUID, []byte, bool) {
	rawSnake, rawCamel := request.QuoteMessageID, request.QuoteMessageIDCamel
	raw := rawSnake
	if rawCamel != nil {
		if raw != nil && strings.TrimSpace(*raw) != strings.TrimSpace(*rawCamel) {
			writeError(w, http.StatusBadRequest, "quote_message_id conflicts with quoteMessageId")
			return pgtype.UUID{}, nil, false
		}
		raw = rawCamel
	}
	if request.Quote != nil {
		structuredID := strings.TrimSpace(request.Quote.MessageID)
		if raw != nil && strings.TrimSpace(*raw) != structuredID {
			writeError(w, http.StatusBadRequest, "quote.message_id conflicts with legacy quote message ID")
			return pgtype.UUID{}, nil, false
		}
		raw = &request.Quote.MessageID
	}
	if raw == nil || strings.TrimSpace(*raw) == "" {
		if request.Quote != nil && request.Quote.SelectedText != nil {
			writeError(w, http.StatusBadRequest, "quote.selected_text requires quote.message_id")
			return pgtype.UUID{}, nil, false
		}
		return pgtype.UUID{}, nil, true
	}
	var selectedText *string
	if request.Quote != nil && request.Quote.SelectedText != nil {
		trimmed := strings.TrimSpace(*request.Quote.SelectedText)
		if trimmed == "" {
			writeError(w, http.StatusBadRequest, "quote.selected_text must not be blank")
			return pgtype.UUID{}, nil, false
		}
		if len([]rune(trimmed)) > channelQuoteSelectedTextMaxLen {
			writeError(w, http.StatusBadRequest, "quote.selected_text is too long")
			return pgtype.UUID{}, nil, false
		}
		selectedText = &trimmed
	}
	quoteID, ok := parseUUIDOrBadRequest(w, strings.TrimSpace(*raw), "quote_message_id")
	if !ok {
		return pgtype.UUID{}, nil, false
	}

	whereContext := "AND thread_root_message_id IS NULL"
	args := []any{quoteID, channelID, parseUUID(workspaceID)}
	if threadRootID.Valid {
		whereContext = "AND (id = $4 OR thread_root_message_id = $4)"
		args = append(args, threadRootID)
	}
	row := h.DB.QueryRow(ctx, `
		SELECT author_type, author_id, author_name, content, parts, created_at
		FROM channel_message
		WHERE id = $1 AND channel_id = $2 AND workspace_id = $3
		  AND author_type <> 'system'
		  AND deleted_at IS NULL
		  `+whereContext, args...)
	var authorType, authorName, content string
	var authorID pgtype.UUID
	var parts []byte
	var createdAt pgtype.Timestamptz
	if err := row.Scan(&authorType, &authorID, &authorName, &content, &parts, &createdAt); err != nil {
		if errorsIsNoRows(err) {
			if threadRootID.Valid {
				writeError(w, http.StatusBadRequest, "quote_message_id must reference this thread")
			} else {
				writeError(w, http.StatusBadRequest, "quote_message_id must reference a visible message in this channel")
			}
			return pgtype.UUID{}, nil, false
		}
		writeError(w, http.StatusInternalServerError, "failed to validate quote target")
		return pgtype.UUID{}, nil, false
	}
	decodedParts := messageparts.Decode(parts)
	if authorType == "agent" {
		if unwrappedContent, unwrappedParts, unwrapped, err := messageparts.UnwrapStructuredMessageSend(content, decodedParts); err == nil && unwrapped {
			content = unwrappedContent
			decodedParts = unwrappedParts
		}
	}
	snapshot := ChannelMessageQuoteSnapshot{
		Type:         authorType,
		AuthorID:     uuidToPtr(authorID),
		AuthorName:   authorName,
		Content:      content,
		Parts:        decodedParts,
		CreatedAt:    timestampToString(createdAt),
		SelectedText: selectedText,
	}
	rawSnapshot, err := json.Marshal(snapshot)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to capture quote snapshot")
		return pgtype.UUID{}, nil, false
	}
	return quoteID, rawSnapshot, true
}

func (h *Handler) attachChannelMessageReplySummary(ctx context.Context, workspaceID string, msg ChannelMessageResponse) ChannelMessageResponse {
	messages := []ChannelMessageResponse{msg}
	h.attachChannelMessageReplySummaries(ctx, workspaceID, messages)
	h.attachChannelMessageReactions(ctx, workspaceID, messages)
	h.attachChannelMessageQuotes(ctx, workspaceID, messages)
	return messages[0]
}

func (h *Handler) attachSingleChannelMessageDetails(ctx context.Context, workspaceID string, userID pgtype.UUID, msg ChannelMessageResponse) ChannelMessageResponse {
	messages := []ChannelMessageResponse{msg}
	h.attachChannelMessageReplySummaries(ctx, workspaceID, messages)
	h.attachChannelMessageReactions(ctx, workspaceID, messages)
	h.attachChannelMessageQuotes(ctx, workspaceID, messages)
	h.attachChannelMessageThreadMetadata(ctx, workspaceID, userID, messages)
	h.attachChannelMessageThreadReadModel(ctx, workspaceID, messages)
	h.attachChannelMessageAttachments(ctx, workspaceID, messages)
	h.attachGraphMemoryCitationCounts(ctx, workspaceID, messages)
	msg = messages[0]
	applyChannelMessageTombstone(&msg)
	return msg
}

// attachChannelMessageAttachments hydrates message.Attachments from the
// canonical message-resource references. Message parts preserve presentation
// order; the association table owns which messages may project each resource.
func (h *Handler) attachChannelMessageAttachments(ctx context.Context, workspaceID string, messages []ChannelMessageResponse) {
	if len(messages) == 0 {
		return
	}
	messageIDs := make([]pgtype.UUID, len(messages))
	for i, msg := range messages {
		messageIDs[i] = parseUUID(msg.ID)
	}
	grouped := h.groupChannelMessageAttachments(ctx, workspaceID, messageIDs)
	for i := range messages {
		messages[i].Attachments = grouped[messages[i].ID]
	}
}

func (h *Handler) attachGraphMemoryCitationCounts(ctx context.Context, workspaceID string, messages []ChannelMessageResponse) {
	if len(messages) == 0 {
		return
	}
	ids := make([]pgtype.UUID, 0, len(messages))
	for _, message := range messages {
		if message.DeletedAt == nil {
			ids = append(ids, parseUUID(message.ID))
		}
	}
	if len(ids) == 0 {
		return
	}
	rows, err := h.DB.Query(ctx, `
		SELECT message_id,count(*)::int
		FROM graph_memory_agent_citation
		WHERE workspace_id=$1::uuid AND message_id=ANY($2::uuid[])
		GROUP BY message_id`, workspaceID, ids)
	if err != nil {
		slog.Warn("graph memory citation count hydration failed", "workspace_id", workspaceID, "error", err)
		return
	}
	defer rows.Close()
	counts := make(map[string]int)
	for rows.Next() {
		var messageID pgtype.UUID
		var count int
		if rows.Scan(&messageID, &count) == nil {
			counts[uuidToString(messageID)] = count
		}
	}
	for i := range messages {
		messages[i].GraphMemoryCitationCount = counts[messages[i].ID]
	}
}

func applyChannelMessageTombstoneReadModel(messages []ChannelMessageResponse) {
	for i := range messages {
		applyChannelMessageTombstone(&messages[i])
	}
}

func applyChannelMessageTombstone(msg *ChannelMessageResponse) {
	if msg == nil || msg.DeletedAt == nil {
		return
	}
	msg.Content = ""
	msg.Parts = nil
	msg.Attachments = nil
	msg.Reactions = nil
	msg.ReplyTo = nil
	msg.Quote = nil
	msg.ThreadRoot = nil
	msg.ThreadParticipants = nil
}

func (h *Handler) attachChannelMessageReactions(ctx context.Context, workspaceID string, messages []ChannelMessageResponse) {
	messageIDs := []pgtype.UUID{}
	channelIDs := map[string]string{}
	for _, msg := range messages {
		if msg.DeletedAt != nil {
			continue
		}
		messageIDs = append(messageIDs, parseUUID(msg.ID))
		channelIDs[msg.ID] = msg.ChannelID
	}
	if len(messageIDs) == 0 {
		return
	}
	rows, err := h.DB.Query(ctx, `
		SELECT id, channel_message_id, actor_type, actor_id, emoji, created_at
		FROM channel_message_reaction
		WHERE workspace_id = $1 AND channel_message_id = ANY($2::uuid[])
		ORDER BY created_at ASC`, parseUUID(workspaceID), messageIDs)
	if err != nil {
		slog.Warn("channel reactions: load failed", "workspace", workspaceID, "error", err)
		return
	}
	defer rows.Close()
	byMessage := map[string][]ChannelReactionResponse{}
	for rows.Next() {
		var id, messageID, actorID pgtype.UUID
		var actorType, emoji string
		var createdAt pgtype.Timestamptz
		if err := rows.Scan(&id, &messageID, &actorType, &actorID, &emoji, &createdAt); err != nil {
			continue
		}
		key := uuidToString(messageID)
		byMessage[key] = append(byMessage[key], ChannelReactionResponse{
			ID:        uuidToString(id),
			ChannelID: channelIDs[key],
			MessageID: key,
			ActorType: actorType,
			ActorID:   uuidToString(actorID),
			Emoji:     emoji,
			CreatedAt: timestampToString(createdAt),
		})
	}
	for i := range messages {
		if messages[i].DeletedAt != nil {
			messages[i].Reactions = nil
			continue
		}
		messages[i].Reactions = byMessage[messages[i].ID]
	}
}

func (h *Handler) attachChannelMessageReplySummaries(ctx context.Context, workspaceID string, messages []ChannelMessageResponse) {
	replyIDs := []pgtype.UUID{}
	seen := map[string]bool{}
	for _, msg := range messages {
		if msg.DeletedAt != nil {
			continue
		}
		if msg.ReplyToMessageID == nil || seen[*msg.ReplyToMessageID] {
			continue
		}
		seen[*msg.ReplyToMessageID] = true
		replyIDs = append(replyIDs, parseUUID(*msg.ReplyToMessageID))
	}
	if len(replyIDs) == 0 {
		return
	}
	rows, err := h.DB.Query(ctx, `
		SELECT m.id, m.author_type, m.author_id, m.author_name,
		       m.content, m.parts, m.created_at
		FROM channel_message m
		WHERE m.workspace_id = $1 AND m.id = ANY($2::uuid[]) AND m.deleted_at IS NULL`,
		parseUUID(workspaceID), replyIDs)
	if err != nil {
		slog.Warn("channel reply summary: load failed", "workspace", workspaceID, "error", err)
		return
	}
	defer rows.Close()
	byID := map[string]ChannelMessageReply{}
	for rows.Next() {
		var id, authorID pgtype.UUID
		var authorType, authorName, content string
		var parts []byte
		var createdAt pgtype.Timestamptz
		if err := rows.Scan(&id, &authorType, &authorID, &authorName, &content, &parts, &createdAt); err != nil {
			continue
		}
		key := uuidToString(id)
		byID[key] = ChannelMessageReply{
			ID:         key,
			Type:       authorType,
			AuthorID:   uuidToPtr(authorID),
			AuthorName: authorName,
			Content:    content,
			Parts:      messageparts.Decode(parts),
			CreatedAt:  timestampToString(createdAt),
		}
	}
	for i := range messages {
		if messages[i].ReplyToMessageID == nil {
			continue
		}
		if reply, ok := byID[*messages[i].ReplyToMessageID]; ok {
			messages[i].ReplyTo = &reply
		}
	}
}

func (h *Handler) attachChannelMessageQuotes(ctx context.Context, workspaceID string, messages []ChannelMessageResponse) {
	quoteIDs := []pgtype.UUID{}
	seen := map[string]bool{}
	for _, msg := range messages {
		if msg.DeletedAt != nil || msg.QuoteMessageID == nil || seen[*msg.QuoteMessageID] {
			continue
		}
		seen[*msg.QuoteMessageID] = true
		quoteIDs = append(quoteIDs, parseUUID(*msg.QuoteMessageID))
	}
	if len(quoteIDs) == 0 {
		return
	}
	rows, err := h.DB.Query(ctx, `
		SELECT m.id, m.channel_id, m.author_type, m.author_id, m.author_name,
		       m.content, m.parts, m.created_at, m.deleted_at
		FROM channel_message m
		WHERE m.workspace_id = $1 AND m.id = ANY($2::uuid[])`, parseUUID(workspaceID), quoteIDs)
	if err != nil {
		slog.Warn("channel quote: load failed", "workspace", workspaceID, "error", err)
		return
	}
	defer rows.Close()
	type quoteSource struct {
		channelID string
		deleted   bool
		snapshot  ChannelMessageQuoteSnapshot
	}
	byID := map[string]quoteSource{}
	for rows.Next() {
		var id, channelID, authorID pgtype.UUID
		var authorType, authorName, content string
		var parts []byte
		var createdAt, deletedAt pgtype.Timestamptz
		if err := rows.Scan(&id, &channelID, &authorType, &authorID, &authorName, &content, &parts, &createdAt, &deletedAt); err != nil {
			continue
		}
		decodedParts := messageparts.Decode(parts)
		if !deletedAt.Valid && authorType == "agent" {
			if unwrappedContent, unwrappedParts, unwrapped, err := messageparts.UnwrapStructuredMessageSend(content, decodedParts); err == nil && unwrapped {
				content = unwrappedContent
				decodedParts = unwrappedParts
			}
		}
		byID[uuidToString(id)] = quoteSource{
			channelID: uuidToString(channelID),
			deleted:   deletedAt.Valid,
			snapshot: ChannelMessageQuoteSnapshot{
				Type:       authorType,
				AuthorID:   uuidToPtr(authorID),
				AuthorName: authorName,
				Content:    content,
				Parts:      decodedParts,
				CreatedAt:  timestampToString(createdAt),
			},
		}
	}
	for i := range messages {
		if messages[i].QuoteMessageID == nil {
			continue
		}
		quote := ChannelMessageQuote{MessageID: *messages[i].QuoteMessageID, Status: "inaccessible"}
		source, ok := byID[*messages[i].QuoteMessageID]
		if ok && source.channelID == messages[i].ChannelID {
			if source.deleted {
				quote.Status = "deleted"
			} else {
				quote.Status = "active"
				snapshot := source.snapshot
				if len(messages[i].quoteSnapshotRaw) > 0 {
					if err := json.Unmarshal(messages[i].quoteSnapshotRaw, &snapshot); err != nil {
						slog.Warn("channel quote: invalid stored snapshot", "message", messages[i].ID, "error", err)
					}
				}
				quote.Snapshot = &snapshot
			}
		}
		messages[i].Quote = &quote
	}
}

func (h *Handler) attachChannelMessageThreadRootSummaries(ctx context.Context, workspaceID string, messages []ChannelMessageResponse) {
	rootIDs := []pgtype.UUID{}
	seen := map[string]bool{}
	for _, msg := range messages {
		if msg.DeletedAt != nil {
			continue
		}
		if msg.ThreadRootMessageID == nil || seen[*msg.ThreadRootMessageID] {
			continue
		}
		seen[*msg.ThreadRootMessageID] = true
		rootIDs = append(rootIDs, parseUUID(*msg.ThreadRootMessageID))
	}
	if len(rootIDs) == 0 {
		return
	}
	rows, err := h.DB.Query(ctx, `
		SELECT m.id, m.author_type, m.author_id, m.author_name,
		       m.content, m.parts, m.created_at
		FROM channel_message m
		WHERE m.workspace_id = $1 AND m.id = ANY($2::uuid[]) AND m.deleted_at IS NULL`,
		parseUUID(workspaceID), rootIDs)
	if err != nil {
		slog.Warn("channel thread root summary: load failed", "workspace", workspaceID, "error", err)
		return
	}
	defer rows.Close()
	byID := map[string]ChannelMessageReply{}
	for rows.Next() {
		var id, authorID pgtype.UUID
		var authorType, authorName, content string
		var parts []byte
		var createdAt pgtype.Timestamptz
		if err := rows.Scan(&id, &authorType, &authorID, &authorName, &content, &parts, &createdAt); err != nil {
			continue
		}
		key := uuidToString(id)
		byID[key] = ChannelMessageReply{
			ID:         key,
			Type:       authorType,
			AuthorID:   uuidToPtr(authorID),
			AuthorName: authorName,
			Content:    content,
			Parts:      messageparts.Decode(parts),
			CreatedAt:  timestampToString(createdAt),
		}
	}
	for i := range messages {
		if messages[i].ThreadRootMessageID == nil {
			continue
		}
		if root, ok := byID[*messages[i].ThreadRootMessageID]; ok {
			messages[i].ThreadRoot = &root
		}
	}
}

// attachChannelMessageThreadMetadata hydrates the mainline thread read model
// (reply count, last reply, follow flag, unread count) for root messages.
//
// LRM-1145: thread_unread_count must answer the same question as the Activity
// feed (loadActivityThreads) — a member has unread thread replies when they are
// following the thread, already spoke in it, or were personally @-mentioned
// anywhere in it (root included), and an explicit unfollow silences it. The old
// rule counted only `followed_at IS NOT NULL`, so a member @-mentioned in a
// root message (mention-follow only fires for mentions inside replies) saw
// Activity flag the thread as unread while the channel preview rendered a bare
// 「N 条回复」with no「M 条新」. `thread_followed` stays a strict follow-state
// flag for the follow toggle; only the unread badge uses the broader rule.
func (h *Handler) attachChannelMessageThreadMetadata(ctx context.Context, workspaceID string, userID pgtype.UUID, messages []ChannelMessageResponse) {
	rootIDs := []pgtype.UUID{}
	for _, msg := range messages {
		if msg.ThreadRootMessageID != nil {
			continue
		}
		rootIDs = append(rootIDs, parseUUID(msg.ID))
	}
	if len(rootIDs) == 0 {
		return
	}
	rows, err := h.DB.Query(ctx, `
		SELECT roots.id,
		       count(replies.id)::int AS reply_count,
		       max(replies.created_at) AS last_reply_at,
		       COALESCE(tp.followed_at IS NOT NULL, false) AS followed,
	       CASE
	         WHEN COALESCE(tp.wake_state, 'no_wake') = 'unfollowed' THEN 0
	         WHEN tp.followed_at IS NOT NULL
	           OR EXISTS (
	             SELECT 1
	             FROM channel_message mine
	             WHERE mine.thread_root_message_id = roots.id
	               AND mine.author_type = 'user'
	               AND mine.author_id = $3
	               AND mine.deleted_at IS NULL
	           )
	           OR EXISTS (
	             SELECT 1
	             FROM channel_message mentioning
	             WHERE (mentioning.id = roots.id OR mentioning.thread_root_message_id = roots.id)
	               AND mentioning.deleted_at IS NULL
	               AND EXISTS (
	                 SELECT 1
	                 FROM jsonb_array_elements(COALESCE(mentioning.parts, '[]'::jsonb)) AS part(value)
	                 WHERE part.value->>'type' = 'reference'
	                   AND part.value->>'ref_type' = 'mention'
	                   AND part.value->>'ref_subtype' = 'member'
	                   AND CASE
	                         WHEN part.value->>'ref_id' ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
	                           THEN (part.value->>'ref_id')::uuid = $3
	                         ELSE false
	                       END
	               )
	           )
	         THEN count(replies.id) FILTER (
	           WHERE replies.seq > COALESCE(tp.last_read_seq, 0)
	             AND replies.author_type <> 'system'
	             AND NOT (replies.author_type = 'user' AND replies.author_id = $3)
	         )::int
	         ELSE 0
	       END AS unread_count
		FROM channel_message roots
		LEFT JOIN channel_message replies ON replies.channel_id = roots.channel_id
			AND replies.thread_root_message_id = roots.id
			AND replies.deleted_at IS NULL
		LEFT JOIN thread_participant tp
		  ON tp.root_message_id = roots.id
		 AND tp.member_type = 'user'
		 AND tp.member_id = $3
		WHERE roots.workspace_id = $2 AND roots.id = ANY($1::uuid[])
		GROUP BY roots.id, tp.followed_at, tp.wake_state, tp.last_read_seq`,
		rootIDs, parseUUID(workspaceID), userID)
	if err != nil {
		slog.Warn("channel thread metadata: load failed", "workspace", workspaceID, "error", err)
		return
	}
	defer rows.Close()
	type threadMeta struct {
		replyCount  int
		lastReplyAt *string
		followed    bool
		unreadCount int
	}
	byID := map[string]threadMeta{}
	for rows.Next() {
		var id pgtype.UUID
		var count, unread int
		var lastReplyAt pgtype.Timestamptz
		var followed bool
		if err := rows.Scan(&id, &count, &lastReplyAt, &followed, &unread); err != nil {
			continue
		}
		byID[uuidToString(id)] = threadMeta{
			replyCount:  count,
			lastReplyAt: timestampToPtr(lastReplyAt),
			followed:    followed,
			unreadCount: unread,
		}
	}
	for i := range messages {
		meta, ok := byID[messages[i].ID]
		if !ok {
			continue
		}
		messages[i].ThreadReplyCount = meta.replyCount
		messages[i].ThreadLastReplyAt = meta.lastReplyAt
		messages[i].ThreadFollowed = meta.followed
		messages[i].ThreadUnreadCount = meta.unreadCount
	}
}

func (h *Handler) attachChannelMessageThreadReadModel(ctx context.Context, workspaceID string, messages []ChannelMessageResponse) {
	rootIDs := []pgtype.UUID{}
	for _, msg := range messages {
		if msg.ThreadRootMessageID != nil {
			continue
		}
		rootIDs = append(rootIDs, parseUUID(msg.ID))
	}
	if len(rootIDs) == 0 {
		return
	}
	rows, err := h.DB.Query(ctx, `
		SELECT tp.root_message_id,
		       tp.member_type,
		       tp.member_id,
		       COALESCE(u.name, a.name, '') AS name,
		       COALESCE(NULLIF(u.display_name, ''), u.name, u.email, NULLIF(a.display_name, ''), a.name, '') AS display_name,
		       COALESCE(tp.followed_at IS NOT NULL, false) AS followed
		FROM thread_participant tp
		LEFT JOIN "user" u ON tp.member_type = 'user' AND u.id = tp.member_id
		LEFT JOIN agent a ON tp.member_type = 'agent' AND a.id = tp.member_id
		WHERE tp.root_message_id = ANY($1::uuid[])
		  AND tp.wake_state <> 'removed'
		ORDER BY tp.root_message_id, tp.created_at ASC`, rootIDs)
	if err != nil {
		slog.Warn("channel thread read-model: load failed", "workspace", workspaceID, "error", err)
		return
	}
	defer rows.Close()

	participantsByRoot := map[string][]ChannelThreadParticipant{}
	for rows.Next() {
		var rootID, memberID pgtype.UUID
		var memberType, name, displayName string
		var followed bool
		if err := rows.Scan(&rootID, &memberType, &memberID, &name, &displayName, &followed); err != nil {
			continue
		}
		memberIDText := uuidToString(memberID)
		key := channelThreadParticipantKey(memberType, memberIDText)
		rootKey := uuidToString(rootID)
		displayName = firstNonEmpty(displayName, name, memberIDText)
		participantsByRoot[rootKey] = append(participantsByRoot[rootKey], ChannelThreadParticipant{
			Key:         key,
			MemberType:  memberType,
			MemberID:    memberIDText,
			Name:        firstNonEmpty(name, displayName),
			DisplayName: displayName,
			Followed:    followed,
		})
	}
	for i := range messages {
		if messages[i].ThreadRootMessageID != nil {
			continue
		}
		if participants := participantsByRoot[messages[i].ID]; len(participants) > 0 {
			messages[i].ThreadParticipants = participants
		}
	}
}

func channelThreadParticipantKey(memberType, memberID string) string {
	return memberType + ":" + memberID
}

func uuidStringPtr(id pgtype.UUID) *string {
	if !id.Valid {
		return nil
	}
	value := uuidToString(id)
	return &value
}

func boolPtr(value bool) *bool {
	return &value
}

func (h *Handler) UpdateChannelMessage(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	channelID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "channelId"), "channel id")
	if !ok {
		return
	}
	messageID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "messageId"), "message id")
	if !ok {
		return
	}
	if !h.requireChannelUserMember(w, r.Context(), workspaceID, channelID, parseUUID(userID)) {
		return
	}
	if !h.requireChannelWritable(w, r.Context(), workspaceID, channelID) {
		return
	}
	var req UpdateChannelMessageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	content, parts, err := messageparts.Normalize(req.Content, req.Parts)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid message parts: "+err.Error())
		return
	}
	if content == "" && !channelPartsAllowEmptyContent(parts) {
		writeError(w, http.StatusBadRequest, "content is required")
		return
	}
	if len([]rune(content)) > channelMessageMaxLen {
		writeError(w, http.StatusBadRequest, "content is too long")
		return
	}
	attachmentIDs, ok := parseUUIDSliceOrBadRequest(w, attachmentIDsFromParts(parts), "attachment_id")
	if !ok {
		return
	}
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update channel message")
		return
	}
	defer tx.Rollback(r.Context())
	msg, err := scanChannelMessage(tx.QueryRow(r.Context(), `
		UPDATE channel_message
		SET content = $5, parts = $6::jsonb, edited_at = now()
		WHERE id = $1
		  AND channel_id = $2
		  AND workspace_id = $3
		  AND author_type = 'user'
		  AND author_id = $4
		  AND deleted_at IS NULL
		RETURNING id, channel_id, workspace_id, author_type, author_id, author_name, content, parts, source, external_message_id, client_message_id, reply_to_message_id, quote_message_id, quote_snapshot, thread_root_message_id, thread_id, trigger_depth, seq, created_at, edited_at, deleted_at`,
		messageID, channelID, parseUUID(workspaceID), parseUUID(userID), content, messageparts.MustJSON(parts)))
	if err != nil {
		if errorsIsNoRows(err) {
			writeError(w, http.StatusNotFound, "message not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to update channel message")
		return
	}
	qtx := h.Queries.WithTx(tx)
	if err := linkOwnedAttachmentsToChannelMessage(r.Context(), qtx, messageID, parseUUID(workspaceID), "member", parseUUID(userID), attachmentIDs); err != nil {
		if errors.Is(err, errChannelAttachmentUnavailable) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to update channel message")
		return
	}
	if _, err := tx.Exec(r.Context(), `
		DELETE FROM channel_message_attachment
		WHERE workspace_id = $1
		  AND channel_message_id = $2
		  AND NOT (attachment_id = ANY($3::uuid[]))`, parseUUID(workspaceID), messageID, attachmentIDs); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update channel message")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update channel message")
		return
	}
	msg = h.attachSingleChannelMessageDetails(r.Context(), workspaceID, parseUUID(userID), msg)
	h.publishChannelToMembers(r.Context(), protocol.EventChannelMessage, workspaceID, "member", userID, channelID, msg)
	writeJSON(w, http.StatusOK, msg)
}

func (h *Handler) AddChannelMessageReaction(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	channelID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "channelId"), "channel id")
	if !ok {
		return
	}
	messageID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "messageId"), "message id")
	if !ok {
		return
	}
	if !h.requireChannelUserMember(w, r.Context(), workspaceID, channelID, parseUUID(userID)) {
		return
	}
	if !h.requireChannelWritable(w, r.Context(), workspaceID, channelID) {
		return
	}
	var req struct {
		Emoji string `json:"emoji"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	emoji := strings.TrimSpace(req.Emoji)
	if emoji == "" {
		writeError(w, http.StatusBadRequest, "emoji is required")
		return
	}
	var id, msgID, actorID pgtype.UUID
	var createdAt pgtype.Timestamptz
	reaction := ChannelReactionResponse{ChannelID: uuidToString(channelID), ActorType: "member", Emoji: emoji}
	err := h.DB.QueryRow(r.Context(), `
		INSERT INTO channel_message_reaction (channel_message_id, workspace_id, actor_type, actor_id, emoji)
		SELECT id, workspace_id, 'member', $4::uuid, $5
		FROM channel_message
		WHERE id = $1 AND channel_id = $2 AND workspace_id = $3 AND author_type <> 'system' AND deleted_at IS NULL
		ON CONFLICT (channel_message_id, actor_type, actor_id, emoji) DO UPDATE SET created_at = channel_message_reaction.created_at
		RETURNING id, channel_message_id, actor_id, created_at`, messageID, channelID, parseUUID(workspaceID), parseUUID(userID), emoji).Scan(&id, &msgID, &actorID, &createdAt)
	if err != nil {
		if errorsIsNoRows(err) {
			writeError(w, http.StatusNotFound, "message not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to add reaction")
		return
	}
	reaction.ID = uuidToString(id)
	reaction.MessageID = uuidToString(msgID)
	reaction.ActorID = uuidToString(actorID)
	reaction.CreatedAt = timestampToString(createdAt)
	h.publishChannelToMembers(r.Context(), protocol.EventChannelReactionAdded, workspaceID, "member", userID, channelID, map[string]any{"reaction": reaction, "channel_id": uuidToString(channelID), "message_id": uuidToString(messageID)})
	writeJSON(w, http.StatusCreated, reaction)
}

func (h *Handler) RemoveChannelMessageReaction(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	channelID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "channelId"), "channel id")
	if !ok {
		return
	}
	messageID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "messageId"), "message id")
	if !ok {
		return
	}
	if !h.requireChannelUserMember(w, r.Context(), workspaceID, channelID, parseUUID(userID)) {
		return
	}
	if !h.requireChannelWritable(w, r.Context(), workspaceID, channelID) {
		return
	}
	var req struct {
		Emoji string `json:"emoji"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	emoji := strings.TrimSpace(req.Emoji)
	if emoji == "" {
		writeError(w, http.StatusBadRequest, "emoji is required")
		return
	}

	tag, err := h.DB.Exec(r.Context(), `
		DELETE FROM channel_message_reaction r
		USING channel_message m
		WHERE r.channel_message_id = m.id
		  AND r.channel_message_id = $1
		  AND m.channel_id = $2
		  AND m.author_type <> 'system'
		  AND m.deleted_at IS NULL
		  AND r.workspace_id = $3
		  AND r.actor_type = 'member'
		  AND r.actor_id = $4
		  AND r.emoji = $5`, messageID, channelID, parseUUID(workspaceID), parseUUID(userID), emoji)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to remove reaction")
		return
	}
	if tag.RowsAffected() == 0 {
		var exists bool
		if err := h.DB.QueryRow(r.Context(), `
			SELECT EXISTS (
				SELECT 1 FROM channel_message
				WHERE id = $1 AND channel_id = $2 AND workspace_id = $3 AND author_type <> 'system' AND deleted_at IS NULL
			)`, messageID, channelID, parseUUID(workspaceID)).Scan(&exists); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to remove reaction")
			return
		}
		if !exists {
			writeError(w, http.StatusNotFound, "message not found")
			return
		}
	}

	h.publishChannelToMembers(r.Context(), protocol.EventChannelReactionRemoved, workspaceID, "member", userID, channelID, map[string]any{
		"channel_id": uuidToString(channelID),
		"message_id": uuidToString(messageID),
		"emoji":      emoji,
		"actor_type": "member",
		"actor_id":   userID,
	})
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) ListChannelMessageThread(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	channelID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "channelId"), "channel id")
	if !ok {
		return
	}
	rootID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "messageId"), "message id")
	if !ok {
		return
	}
	userUUID := parseUUID(userID)
	supervisor := h.channelUserIsAgentDMSupervisor(r.Context(), workspaceID, channelID, userUUID)
	if !h.requireChannelUserViewer(w, r.Context(), workspaceID, channelID, userUUID) {
		return
	}
	ch, found := h.getChannel(r.Context(), workspaceID, channelID)
	if !found {
		writeError(w, http.StatusNotFound, "channel not found")
		return
	}
	if !supervisor && !h.requireDMChannelAgentAccess(w, r, workspaceID, userID, ch, false) {
		return
	}
	root, ok := h.loadChannelThreadRoot(w, r.Context(), workspaceID, channelID, rootID)
	if !ok {
		return
	}
	limit, beforeSeq, before, beforeID, useSeqCursor, hasCursor, ok := parseChannelThreadPageParams(w, r)
	if !ok {
		return
	}
	rows, err := h.DB.Query(r.Context(), `
			SELECT id, channel_id, workspace_id, author_type, author_id, author_name, content, parts, source, external_message_id, client_message_id, reply_to_message_id, quote_message_id, quote_snapshot, thread_root_message_id, thread_id, trigger_depth, seq, created_at, edited_at, deleted_at
		FROM channel_message
		WHERE channel_id = $1 AND workspace_id = $2 AND thread_root_message_id = $3
		  AND deleted_at IS NULL
		  AND (
		    NOT $4::boolean
		    OR ($5::boolean AND seq < $6::bigint)
		    OR (NOT $5::boolean AND (created_at, id) < ($7::timestamptz, $8::uuid))
		)
		ORDER BY seq DESC
		LIMIT $9`,
		channelID, parseUUID(workspaceID), rootID, hasCursor, useSeqCursor, beforeSeq, before, beforeID, limit+1)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list channel thread messages")
		return
	}
	defer rows.Close()
	repliesDesc := []ChannelMessageResponse{}
	for rows.Next() {
		msg, err := scanChannelMessage(rows)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to read channel thread messages")
			return
		}
		repliesDesc = append(repliesDesc, msg)
	}
	hasMore := len(repliesDesc) > limit
	if hasMore {
		repliesDesc = repliesDesc[:limit]
	}
	var nextCursor *ChannelThreadMessagesCursorResponse
	if hasMore && len(repliesDesc) > 0 {
		oldest := repliesDesc[len(repliesDesc)-1]
		nextCursor = &ChannelThreadMessagesCursorResponse{
			BeforeSeq: oldest.Seq,
			Before:    oldest.CreatedAt,
			BeforeID:  oldest.ID,
		}
		w.Header().Set("X-Next-Before-Seq", strconv.FormatInt(oldest.Seq, 10))
		w.Header().Set("X-Next-Before", oldest.CreatedAt)
		w.Header().Set("X-Next-Before-Id", oldest.ID)
	}
	if root.DeletedAt != nil && len(repliesDesc) == 0 {
		writeError(w, http.StatusNotFound, "message not found")
		return
	}
	out := make([]ChannelMessageResponse, 0, 1+len(repliesDesc))
	out = append(out, root)
	for i := len(repliesDesc) - 1; i >= 0; i-- {
		out = append(out, repliesDesc[i])
	}
	h.attachChannelMessageAttachments(r.Context(), workspaceID, out)
	h.attachGraphMemoryCitationCounts(r.Context(), workspaceID, out)
	h.attachChannelMessageReactions(r.Context(), workspaceID, out)
	h.attachChannelMessageReplySummaries(r.Context(), workspaceID, out)
	h.attachChannelMessageQuotes(r.Context(), workspaceID, out)
	h.attachChannelMessageThreadMetadata(r.Context(), workspaceID, parseUUID(userID), out[:1])
	h.attachChannelMessageThreadReadModel(r.Context(), workspaceID, out[:1])
	applyChannelMessageTombstoneReadModel(out)
	writeJSON(w, http.StatusOK, ChannelThreadMessagesPageResponse{
		Messages:   out,
		NextCursor: nextCursor,
	})
}

func (h *Handler) SendChannelMessageThreadReply(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	channelID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "channelId"), "channel id")
	if !ok {
		return
	}
	rootID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "messageId"), "message id")
	if !ok {
		return
	}
	var req SendChannelMessageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	content, parts, err := messageparts.Normalize(req.Content, req.Parts)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid message parts: "+err.Error())
		return
	}
	if content == "" && !channelPartsAllowEmptyContent(parts) {
		writeError(w, http.StatusBadRequest, "content is required")
		return
	}
	if len([]rune(content)) > channelMessageMaxLen {
		writeError(w, http.StatusBadRequest, "content is too long")
		return
	}
	clientMessageID, ok := normalizeChannelClientMessageID(w, req.ClientMessageID)
	if !ok {
		return
	}
	// Reference attachment resources from parts only (parts win; do not
	// dual-merge attachment_ids).
	attachmentIDs, ok := parseUUIDSliceOrBadRequest(w, attachmentIDsFromParts(parts), "attachment_id")
	if !ok {
		return
	}
	ch, found := h.getChannel(r.Context(), workspaceID, channelID)
	if !found {
		writeError(w, http.StatusNotFound, "channel not found")
		return
	}
	if !h.requireChannelUserMember(w, r.Context(), workspaceID, channelID, parseUUID(userID)) {
		return
	}
	if !h.requireChannelWritable(w, r.Context(), workspaceID, channelID) {
		return
	}
	if !h.requireDMChannelAgentAccess(w, r, workspaceID, userID, ch, true) {
		return
	}
	content, parts, err = h.enrichChannelMessageMentions(r.Context(), ch, content, parts)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	root, ok := h.loadChannelThreadRoot(w, r.Context(), workspaceID, channelID, rootID)
	if !ok {
		return
	}
	replyToMessageID, ok := h.validateChannelThreadReplyTarget(w, r.Context(), workspaceID, channelID, rootID, req.ReplyToMessageID)
	if !ok {
		return
	}
	quoteMessageID, quoteSnapshot, ok := h.resolveChannelQuoteTarget(w, r.Context(), workspaceID, channelID, req, rootID)
	if !ok {
		return
	}
	threadID := root.ThreadID
	if threadID == nil || strings.TrimSpace(*threadID) == "" {
		fresh := uuid.NewString()
		threadID = &fresh
	}
	result, err := h.sendPreparedCanonicalChannelMessage(r.Context(), canonicalChannelMessageInput{
		Channel: ch, WorkspaceID: workspaceID, UserID: userID,
		Content: content, Parts: parts, AttachmentIDs: attachmentIDs,
		ReplyToMessageID: replyToMessageID, QuoteMessageID: quoteMessageID,
		QuoteSnapshot: quoteSnapshot, ThreadRootMessageID: rootID,
		ThreadID: threadID, ClientMessageID: clientMessageID,
		BeforeRecipientPlanning: func(txHandler *Handler, ctx context.Context, msg ChannelMessageResponse, created bool) error {
			if !created {
				return nil
			}
			txHandler.followChannelThreadUser(ctx, channelID, rootID, parseUUID(userID), true)
			if root.Type == "user" && root.AuthorID != nil {
				txHandler.followChannelThreadUserUnlessExplicitlyUnfollowed(ctx, channelID, rootID, parseUUID(*root.AuthorID), false)
			}
			if root.Type == "agent" && root.AuthorID != nil {
				txHandler.followChannelThreadAgentUnlessExplicitlyUnfollowed(ctx, channelID, rootID, parseUUID(*root.AuthorID))
			}
			if ch.Kind == "dm" && root.Type == "user" {
				for _, agent := range txHandler.channelAgentMembers(ctx, ch.WorkspaceID, ch.ID) {
					txHandler.followChannelThreadAgentUnlessExplicitlyUnfollowed(ctx, channelID, rootID, agent.ID)
				}
			}
			txHandler.followChannelThreadMentionedUsers(ctx, ch, msg)
			return nil
		},
	})
	if err != nil {
		if errors.Is(err, errChannelClientMessageConflict) {
			writeError(w, http.StatusConflict, "client_message_id conflicts with an existing channel message")
			return
		}
		if errors.Is(err, errChannelAttachmentUnavailable) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to create channel thread message")
		return
	}
	msg := result.Message.Message
	if !result.Created {
		writeJSON(w, http.StatusOK, msg)
		result.Acknowledge(r.Context())
		return
	}
	h.noteMemberActivity(workspaceID, userID, true)
	// Ack first: delivery notification, agent wake, and Feishu work are all
	// post-response behavior owned by the shared continuation.
	writeJSON(w, http.StatusCreated, msg)
	result.Acknowledge(r.Context())
}

func (h *Handler) MarkChannelThreadRead(w http.ResponseWriter, r *http.Request) {
	h.setChannelThreadReadOrFollow(w, r, true, nil)
}

func (h *Handler) FollowChannelThread(w http.ResponseWriter, r *http.Request) {
	followed := true
	h.setChannelThreadReadOrFollow(w, r, false, &followed)
}

func (h *Handler) UnfollowChannelThread(w http.ResponseWriter, r *http.Request) {
	followed := false
	h.setChannelThreadReadOrFollow(w, r, false, &followed)
}

func (h *Handler) setChannelThreadReadOrFollow(w http.ResponseWriter, r *http.Request, markRead bool, followed *bool) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	channelID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "channelId"), "channel id")
	if !ok {
		return
	}
	rootID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "messageId"), "message id")
	if !ok {
		return
	}
	if !h.requireChannelUserMember(w, r.Context(), workspaceID, channelID, parseUUID(userID)) {
		return
	}
	if _, ok := h.loadChannelThreadRoot(w, r.Context(), workspaceID, channelID, rootID); !ok {
		return
	}
	actorType, actorID := h.resolveActor(r, userID, workspaceID)
	if actorType == "agent" {
		if followed == nil {
			writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
			return
		}
		agentID := parseUUID(actorID)
		if !h.requireChannelAgentMember(w, r.Context(), workspaceID, channelID, agentID) {
			return
		}
		if *followed {
			h.followChannelThreadAgent(r.Context(), channelID, rootID, agentID)
		} else {
			changed, err := h.unfollowChannelThreadAgent(r.Context(), channelID, rootID, agentID)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "failed to unfollow channel thread")
				return
			}
			if changed {
				h.emitAgentThreadUnfollowedEvent(w, r.Context(), workspaceID, channelID, rootID, agentID)
			}
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		return
	}
	if markRead {
		h.markChannelThreadUserRead(r.Context(), channelID, rootID, parseUUID(userID))
	}
	if followed != nil && *followed {
		h.followChannelThreadUser(r.Context(), channelID, rootID, parseUUID(userID), false)
	} else if followed != nil {
		if err := h.unfollowChannelThreadUser(r.Context(), channelID, rootID, parseUUID(userID)); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to unfollow channel thread")
			return
		}

		// Human thread unfollow is currently a private UI state change. #329's
		// system event is only for agent-initiated explicit unfollow.
	}

	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (h *Handler) loadChannelThreadRoot(w http.ResponseWriter, ctx context.Context, workspaceID string, channelID, rootID pgtype.UUID) (ChannelMessageResponse, bool) {
	row := h.DB.QueryRow(ctx, `
			SELECT id, channel_id, workspace_id, author_type, author_id, author_name, content, parts, source, external_message_id, client_message_id, reply_to_message_id, quote_message_id, quote_snapshot, thread_root_message_id, thread_id, trigger_depth, seq, created_at, edited_at, deleted_at
		FROM channel_message
		WHERE id = $1 AND channel_id = $2 AND workspace_id = $3`,
		rootID, channelID, parseUUID(workspaceID))
	msg, err := scanChannelMessage(row)
	if err != nil {
		if errorsIsNoRows(err) {
			writeError(w, http.StatusNotFound, "message not found")
			return ChannelMessageResponse{}, false
		}
		writeError(w, http.StatusInternalServerError, "failed to load channel message")
		return ChannelMessageResponse{}, false
	}
	if msg.ThreadRootMessageID != nil {
		writeError(w, http.StatusBadRequest, "thread replies cannot be thread roots")
		return ChannelMessageResponse{}, false
	}
	if msg.Type == "system" {
		writeError(w, http.StatusBadRequest, "system messages cannot be thread roots")
		return ChannelMessageResponse{}, false
	}
	return msg, true
}

func (h *Handler) validateChannelThreadReplyTarget(w http.ResponseWriter, ctx context.Context, workspaceID string, channelID, rootID pgtype.UUID, raw *string) (pgtype.UUID, bool) {
	if raw == nil || strings.TrimSpace(*raw) == "" {
		return pgtype.UUID{}, true
	}
	replyToID, ok := parseUUIDOrBadRequest(w, strings.TrimSpace(*raw), "reply_to_message_id")
	if !ok {
		return pgtype.UUID{}, false
	}
	var exists bool
	if err := h.DB.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM channel_message
			WHERE id = $1 AND channel_id = $2 AND workspace_id = $3
			  AND author_type <> 'system'
			  AND (id = $4 OR thread_root_message_id = $4)
		)`, replyToID, channelID, parseUUID(workspaceID), rootID).Scan(&exists); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to validate reply target")
		return pgtype.UUID{}, false
	}
	if !exists {
		writeError(w, http.StatusBadRequest, "reply_to_message_id must reference this thread")
		return pgtype.UUID{}, false
	}
	return replyToID, true
}

func (h *Handler) followChannelThreadUser(ctx context.Context, channelID, rootID, userID pgtype.UUID, markRead bool) {
	h.setChannelThreadUserFollow(ctx, channelID, rootID, userID, markRead, false)
}

// followChannelThreadUserUnlessExplicitlyUnfollowed applies implicit human
// follow transitions such as a root-author reply or personal mention. A
// durable explicit unfollow remains sticky until the user posts in the thread
// or manually follows it again.
func (h *Handler) followChannelThreadUserUnlessExplicitlyUnfollowed(ctx context.Context, channelID, rootID, userID pgtype.UUID, markRead bool) {
	h.setChannelThreadUserFollow(ctx, channelID, rootID, userID, markRead, true)
}

func (h *Handler) setChannelThreadUserFollow(ctx context.Context, channelID, rootID, userID pgtype.UUID, markRead, preserveExplicitUnfollow bool) {
	if _, err := h.DB.Exec(ctx, `
		WITH root AS (
		  SELECT conversation_id, seq
		  FROM channel_message
		  WHERE id = $2 AND channel_id = $1
		),
		thread_max AS (
		  SELECT max(m.seq) AS max_seq
		  FROM channel_message m
		  WHERE m.channel_id = $1
		    AND (m.id = $2 OR m.thread_root_message_id = $2)
		),
		upsert_participant AS (
		  INSERT INTO thread_participant (conversation_id, root_message_id, member_type, member_id, wake_state, last_read_seq, followed_at, updated_at)
		  SELECT root.conversation_id, $2, 'user', $3, 'active', CASE WHEN $4 THEN COALESCE(thread_max.max_seq, root.seq) ELSE 0 END, now(), now()
		  FROM root, thread_max
		  ON CONFLICT (root_message_id, member_type, member_id) DO UPDATE
		  SET followed_at = CASE
		        WHEN $5 AND thread_participant.wake_state = 'unfollowed' THEN thread_participant.followed_at
		        ELSE COALESCE(thread_participant.followed_at, EXCLUDED.followed_at)
		      END,
		      wake_state = CASE
		        WHEN $5 AND thread_participant.wake_state = 'unfollowed' THEN 'unfollowed'
		        ELSE 'active'
		      END,
		      last_read_seq = CASE WHEN $4 THEN GREATEST(thread_participant.last_read_seq, EXCLUDED.last_read_seq) ELSE thread_participant.last_read_seq END,
		      updated_at = now()
		  RETURNING root_message_id, member_id, last_read_seq, followed_at, updated_at
		)
		INSERT INTO channel_thread_state (channel_id, root_message_id, user_id, last_read_at, last_read_seq, followed_at, updated_at)
		SELECT $1, upsert_participant.root_message_id, upsert_participant.member_id, CASE WHEN $4 THEN now() ELSE NULL END, upsert_participant.last_read_seq, upsert_participant.followed_at, upsert_participant.updated_at
		FROM upsert_participant
		ON CONFLICT (root_message_id, user_id) DO UPDATE
		SET followed_at = EXCLUDED.followed_at,
		    last_read_at = CASE WHEN $4 THEN now() ELSE channel_thread_state.last_read_at END,
		    last_read_seq = CASE WHEN $4 THEN GREATEST(channel_thread_state.last_read_seq, EXCLUDED.last_read_seq) ELSE channel_thread_state.last_read_seq END,
		    updated_at = now()`,
		channelID, rootID, userID, markRead, preserveExplicitUnfollow); err != nil {
		slog.Warn("channel thread follow failed", "root", uuidToString(rootID), "user", uuidToString(userID), "error", err)
	}
}

func (h *Handler) markChannelThreadUserRead(ctx context.Context, channelID, rootID, userID pgtype.UUID) {
	if _, err := h.DB.Exec(ctx, `
		WITH root AS (
		  SELECT conversation_id, seq
		  FROM channel_message
		  WHERE id = $2 AND channel_id = $1
		),
		thread_max AS (
		  SELECT max(m.seq) AS max_seq
		  FROM channel_message m
		  WHERE m.channel_id = $1
		    AND (m.id = $2 OR m.thread_root_message_id = $2)
		),
		upsert_participant AS (
		  INSERT INTO thread_participant (conversation_id, root_message_id, member_type, member_id, wake_state, last_read_seq, followed_at, updated_at)
		  SELECT root.conversation_id, $2, 'user', $3, 'no_wake', COALESCE(thread_max.max_seq, root.seq), NULL, now()
		  FROM root, thread_max
		  ON CONFLICT (root_message_id, member_type, member_id) DO UPDATE
		  SET last_read_seq = GREATEST(thread_participant.last_read_seq, EXCLUDED.last_read_seq),
		      updated_at = now()
		  RETURNING root_message_id, member_id, last_read_seq, followed_at, updated_at
		)
		INSERT INTO channel_thread_state (channel_id, root_message_id, user_id, last_read_at, last_read_seq, followed_at, updated_at)
		SELECT $1, upsert_participant.root_message_id, upsert_participant.member_id, now(), upsert_participant.last_read_seq, upsert_participant.followed_at, upsert_participant.updated_at
		FROM upsert_participant
		ON CONFLICT (root_message_id, user_id) DO UPDATE
		SET followed_at = EXCLUDED.followed_at,
		    last_read_at = now(),
		    last_read_seq = GREATEST(channel_thread_state.last_read_seq, EXCLUDED.last_read_seq),
		    updated_at = now()`,
		channelID, rootID, userID); err != nil {
		slog.Warn("channel thread mark read failed", "root", uuidToString(rootID), "user", uuidToString(userID), "error", err)
	}
}

func (h *Handler) unfollowChannelThreadUser(ctx context.Context, channelID, rootID, userID pgtype.UUID) error {
	_, err := h.DB.Exec(ctx, `
		WITH root AS (
		  SELECT conversation_id
		  FROM channel_message
		  WHERE id = $2 AND channel_id = $1
		),
		upsert_participant AS (
		  INSERT INTO thread_participant (conversation_id, root_message_id, member_type, member_id, wake_state, followed_at, updated_at)
		  SELECT root.conversation_id, $2, 'user', $3, 'unfollowed', NULL, now()
		  FROM root
		  ON CONFLICT (root_message_id, member_type, member_id) DO UPDATE
		  SET followed_at = NULL,
		      wake_state = 'unfollowed',
		      updated_at = now()
		  WHERE thread_participant.followed_at IS NOT NULL
		     OR thread_participant.wake_state <> 'unfollowed'
		  RETURNING root_message_id, member_id, followed_at, updated_at
		),
		participant AS (
		  SELECT root_message_id, member_id, followed_at, updated_at
		  FROM upsert_participant
		  UNION ALL
		  SELECT participant.root_message_id, participant.member_id, participant.followed_at, participant.updated_at
		  FROM root
		  JOIN thread_participant participant
		    ON participant.root_message_id = $2
		   AND participant.member_type = 'user'
		   AND participant.member_id = $3
		  WHERE NOT EXISTS (SELECT 1 FROM upsert_participant)
		)
		INSERT INTO channel_thread_state (channel_id, root_message_id, user_id, followed_at, updated_at)
		SELECT $1, participant.root_message_id, participant.member_id, participant.followed_at, participant.updated_at
		FROM participant
		ON CONFLICT (root_message_id, user_id) DO UPDATE
		SET followed_at = NULL,
		    updated_at = now()
		WHERE channel_thread_state.followed_at IS NOT NULL`,
		channelID, rootID, userID)
	if err != nil {
		slog.Warn("channel thread human unfollow failed", "root", uuidToString(rootID), "user", uuidToString(userID), "error", err)
	}
	return err
}

func (h *Handler) followChannelThreadAgent(ctx context.Context, channelID, rootID, agentID pgtype.UUID) {
	if _, err := h.DB.Exec(ctx, `
		WITH root AS (
		  SELECT conversation_id
		  FROM channel_message
		  WHERE id = $2 AND channel_id = $1
		)
		INSERT INTO thread_participant (conversation_id, root_message_id, member_type, member_id, followed_at, updated_at)
		SELECT root.conversation_id, $2, 'agent', $3, now(), now()
		FROM root
		ON CONFLICT (root_message_id, member_type, member_id) DO UPDATE
		SET followed_at = COALESCE(thread_participant.followed_at, EXCLUDED.followed_at),
		    wake_state = 'active',
		    updated_at = now()`,
		channelID, rootID, agentID); err != nil {
		slog.Warn("channel thread agent follow failed", "root", uuidToString(rootID), "agent", uuidToString(agentID), "error", err)
	}
}

// followChannelThreadAgentUnlessExplicitlyUnfollowed applies implicit follow
// transitions such as an agent-authored root's first reply or a personal
// mention. A recorded explicit unfollow remains sticky until the agent posts
// in the thread or manually follows it again.
func (h *Handler) followChannelThreadAgentUnlessExplicitlyUnfollowed(ctx context.Context, channelID, rootID, agentID pgtype.UUID) {
	if _, err := h.DB.Exec(ctx, `
		WITH root AS (
		  SELECT conversation_id
		  FROM channel_message
		  WHERE id = $2 AND channel_id = $1
		)
		INSERT INTO thread_participant (conversation_id, root_message_id, member_type, member_id, followed_at, updated_at)
		SELECT root.conversation_id, $2, 'agent', $3, now(), now()
		FROM root
		ON CONFLICT (root_message_id, member_type, member_id) DO UPDATE
		SET followed_at = CASE
		      WHEN thread_participant.wake_state = 'unfollowed' THEN thread_participant.followed_at
		      ELSE COALESCE(thread_participant.followed_at, EXCLUDED.followed_at)
		    END,
		    wake_state = CASE
		      WHEN thread_participant.wake_state = 'unfollowed' THEN 'unfollowed'
		      ELSE 'active'
		    END,
		    updated_at = now()`,
		channelID, rootID, agentID); err != nil {
		slog.Warn("channel thread implicit agent follow failed", "root", uuidToString(rootID), "agent", uuidToString(agentID), "error", err)
	}
}

func (h *Handler) ensureChannelThreadAgentWakeParticipant(ctx context.Context, channelID, rootID, agentID pgtype.UUID) {
	if _, err := h.DB.Exec(ctx, `
		WITH root AS (
		  SELECT conversation_id
		  FROM channel_message
		  WHERE id = $2 AND channel_id = $1
		)
		INSERT INTO thread_participant (conversation_id, root_message_id, member_type, member_id, wake_state, updated_at)
		SELECT root.conversation_id, $2, 'agent', $3, 'no_wake', now()
		FROM root
		ON CONFLICT (root_message_id, member_type, member_id) DO UPDATE
		SET wake_state = CASE
		      WHEN thread_participant.followed_at IS NULL AND thread_participant.wake_state NOT IN ('removed', 'unfollowed') THEN 'no_wake'
		      ELSE thread_participant.wake_state
		    END,
		    updated_at = now()`,
		channelID, rootID, agentID); err != nil {
		slog.Warn("channel thread agent wake participant failed", "root", uuidToString(rootID), "agent", uuidToString(agentID), "error", err)
	}
}

func (h *Handler) unfollowChannelThreadAgent(ctx context.Context, channelID, rootID, agentID pgtype.UUID) (bool, error) {
	var changed bool
	err := h.DB.QueryRow(ctx, `
		WITH root AS (
		  SELECT conversation_id
		  FROM channel_message
		  WHERE id = $1 AND channel_id = $3
		),
		changed AS (
		  INSERT INTO thread_participant (conversation_id, root_message_id, member_type, member_id, wake_state, followed_at, updated_at)
		  SELECT root.conversation_id, $1, 'agent', $2, 'unfollowed', NULL, now()
		  FROM root
		  ON CONFLICT (root_message_id, member_type, member_id) DO UPDATE
		  SET followed_at = NULL,
		      wake_state = 'unfollowed',
		      updated_at = now()
		  WHERE thread_participant.followed_at IS NOT NULL
		     OR thread_participant.wake_state <> 'unfollowed'
		  RETURNING 1
		)
		SELECT EXISTS (SELECT 1 FROM changed)`, rootID, agentID, channelID).Scan(&changed)
	if err != nil {
		slog.Warn("channel thread agent unfollow failed", "root", uuidToString(rootID), "agent", uuidToString(agentID), "error", err)
	}
	return changed, err
}

func (h *Handler) followChannelThreadMentionedUsers(ctx context.Context, ch ChannelResponse, msg ChannelMessageResponse) {
	if msg.ThreadRootMessageID == nil {
		return
	}
	mentions := util.ParseMentionsFromContentAndParts(msg.Content, msg.Parts)
	if len(mentions) == 0 {
		return
	}
	members := h.channelHumanMemberIDs(ctx, ch.WorkspaceID, ch.ID)
	recipients := map[string]bool{}
	for _, m := range mentions {
		switch m.Type {
		case "member":
			if members[m.ID] {
				recipients[m.ID] = true
			}
		}
	}
	for id := range recipients {
		h.followChannelThreadUserUnlessExplicitlyUnfollowed(ctx, parseUUID(ch.ID), parseUUID(*msg.ThreadRootMessageID), parseUUID(id), false)
	}
}

func parseChannelThreadPageParams(w http.ResponseWriter, r *http.Request) (int, int64, pgtype.Timestamptz, pgtype.UUID, bool, bool, bool) {
	limit := boundedQueryInt(r, "limit", channelThreadDefaultLimit, channelThreadMaxLimit)
	beforeSeqRaw := strings.TrimSpace(r.URL.Query().Get("before_seq"))
	if beforeSeqRaw != "" {
		beforeSeq, err := strconv.ParseInt(beforeSeqRaw, 10, 64)
		if err != nil || beforeSeq < 1 {
			writeError(w, http.StatusBadRequest, "invalid before_seq parameter")
			return 0, 0, pgtype.Timestamptz{}, pgtype.UUID{}, false, false, false
		}
		return limit, beforeSeq, pgtype.Timestamptz{}, pgtype.UUID{}, true, true, true
	}
	beforeRaw := strings.TrimSpace(r.URL.Query().Get("before"))
	beforeIDRaw := strings.TrimSpace(r.URL.Query().Get("before_id"))
	if beforeIDRaw == "" {
		beforeIDRaw = strings.TrimSpace(r.URL.Query().Get("before-id"))
	}
	if beforeRaw == "" && beforeIDRaw == "" {
		return limit, 0, pgtype.Timestamptz{}, pgtype.UUID{}, false, false, true
	}
	if beforeRaw == "" || beforeIDRaw == "" {
		writeError(w, http.StatusBadRequest, "before and before_id must be set together")
		return 0, 0, pgtype.Timestamptz{}, pgtype.UUID{}, false, false, false
	}
	t, err := time.Parse(time.RFC3339Nano, beforeRaw)
	if err != nil {
		t, err = time.Parse(time.RFC3339, beforeRaw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid before parameter; expected RFC3339 format")
			return 0, 0, pgtype.Timestamptz{}, pgtype.UUID{}, false, false, false
		}
	}
	id, err := util.ParseUUID(beforeIDRaw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid before_id parameter; expected UUID")
		return 0, 0, pgtype.Timestamptz{}, pgtype.UUID{}, false, false, false
	}
	return limit, 0, pgtype.Timestamptz{Time: t, Valid: true}, id, false, true, true
}

func parseChannelMessagesPageParams(r *http.Request) (int, int64, pgtype.Timestamptz, pgtype.UUID, int64, error) {
	limit := channelMessagesDefaultLimit
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > channelMessagesMaxLimit {
			return 0, 0, pgtype.Timestamptz{}, pgtype.UUID{}, 0, errors.New("invalid limit")
		}
		limit = parsed
	}

	aroundSeqStr := strings.TrimSpace(r.URL.Query().Get("around_seq"))
	beforeSeqStr := strings.TrimSpace(r.URL.Query().Get("before_seq"))
	rawBeforeCreatedAt := strings.TrimSpace(r.URL.Query().Get("before_created_at"))
	rawBeforeID := strings.TrimSpace(r.URL.Query().Get("before_id"))

	// around_seq is mutually exclusive with before_seq / before_created_at+before_id
	if aroundSeqStr != "" {
		if beforeSeqStr != "" || rawBeforeCreatedAt != "" || rawBeforeID != "" {
			return 0, 0, pgtype.Timestamptz{}, pgtype.UUID{}, 0, errors.New("around_seq is mutually exclusive with before_* cursors")
		}
		aroundSeq, err := strconv.ParseInt(aroundSeqStr, 10, 64)
		if err != nil || aroundSeq < 1 {
			return 0, 0, pgtype.Timestamptz{}, pgtype.UUID{}, 0, errors.New("invalid around_seq")
		}
		return limit, 0, pgtype.Timestamptz{}, pgtype.UUID{}, aroundSeq, nil
	}

	if beforeSeqStr != "" {
		beforeSeq, err := strconv.ParseInt(beforeSeqStr, 10, 64)
		if err != nil || beforeSeq < 1 {
			return 0, 0, pgtype.Timestamptz{}, pgtype.UUID{}, 0, errors.New("invalid cursor")
		}
		return limit, beforeSeq, pgtype.Timestamptz{}, pgtype.UUID{}, 0, nil
	}

	if rawBeforeCreatedAt == "" && rawBeforeID == "" {
		return limit, 0, pgtype.Timestamptz{}, pgtype.UUID{}, 0, nil
	}
	if rawBeforeCreatedAt == "" || rawBeforeID == "" {
		return 0, 0, pgtype.Timestamptz{}, pgtype.UUID{}, 0, errors.New("invalid cursor")
	}
	beforeTime, err := time.Parse(time.RFC3339Nano, rawBeforeCreatedAt)
	if err != nil {
		return 0, 0, pgtype.Timestamptz{}, pgtype.UUID{}, 0, errors.New("invalid cursor")
	}
	beforeID, err := util.ParseUUID(rawBeforeID)
	if err != nil {
		return 0, 0, pgtype.Timestamptz{}, pgtype.UUID{}, 0, errors.New("invalid cursor")
	}
	return limit, 0, pgtype.Timestamptz{Time: beforeTime, Valid: true}, beforeID, 0, nil
}

type ChannelAuthorStat struct {
	AuthorType string  `json:"author_type"`
	AuthorID   *string `json:"author_id"`
	AuthorName string  `json:"author_name"`
	Count      int     `json:"count"`
}

type ChannelStatsResponse struct {
	TotalMessages int                 `json:"total_messages"`
	FileCount     int                 `json:"file_count"`
	MemberCount   int                 `json:"member_count"`
	ByAuthor      []ChannelAuthorStat `json:"by_author"`
}

func (h *Handler) ListChannelAttachments(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	channelID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "channelId"), "channel id")
	if !ok {
		return
	}
	if !h.requireChannelUserMember(w, r.Context(), workspaceID, channelID, parseUUID(userID)) {
		return
	}
	attachments, err := h.Queries.ListAttachmentsByChannel(r.Context(), db.ListAttachmentsByChannelParams{
		ChannelID:   channelID,
		WorkspaceID: parseUUID(workspaceID),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list channel attachments")
		return
	}
	out := make([]AttachmentResponse, 0, len(attachments))
	for _, a := range attachments {
		out = append(out, h.attachmentToResponse(a))
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) GetChannelStats(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	channelID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "channelId"), "channel id")
	if !ok {
		return
	}
	if !h.requireChannelUserMember(w, r.Context(), workspaceID, channelID, parseUUID(userID)) {
		return
	}
	resp := ChannelStatsResponse{ByAuthor: []ChannelAuthorStat{}}
	wsID := parseUUID(workspaceID)

	_ = h.DB.QueryRow(r.Context(), `SELECT count(*) FROM channel_message WHERE channel_id = $1 AND workspace_id = $2`, channelID, wsID).Scan(&resp.TotalMessages)
	_ = h.DB.QueryRow(r.Context(), `SELECT count(*) FROM attachment WHERE channel_id = $1 AND workspace_id = $2`, channelID, wsID).Scan(&resp.FileCount)
	_ = h.DB.QueryRow(r.Context(), `SELECT count(*) FROM channel_member WHERE channel_id = $1 AND workspace_id = $2`, channelID, wsID).Scan(&resp.MemberCount)

	rows, err := h.DB.Query(r.Context(), `
		SELECT author_type, author_id, author_name, count(*)
		FROM channel_message
		WHERE channel_id = $1 AND workspace_id = $2
		GROUP BY author_type, author_id, author_name
		ORDER BY count(*) DESC`, channelID, wsID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to compute channel stats")
		return
	}
	defer rows.Close()
	for rows.Next() {
		var stat ChannelAuthorStat
		var authorID pgtype.UUID
		if err := rows.Scan(&stat.AuthorType, &authorID, &stat.AuthorName, &stat.Count); err != nil {
			continue
		}
		stat.AuthorID = uuidToPtr(authorID)
		resp.ByAuthor = append(resp.ByAuthor, stat)
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) SetChannelTyping(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	channelID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "channelId"), "channel id")
	if !ok {
		return
	}
	var req ChannelTypingRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if !h.requireChannelUserMember(w, r.Context(), workspaceID, channelID, parseUUID(userID)) {
		return
	}
	if !h.requireChannelWritable(w, r.Context(), workspaceID, channelID) {
		return
	}
	h.publishChannelToMembers(r.Context(), protocol.EventChannelTyping, workspaceID, "member", userID, channelID, protocol.ChannelTypingPayload{
		ChannelID:   uuidToString(channelID),
		ActorType:   "user",
		ActorID:     userID,
		ActorName:   h.channelAuthorName(r.Context(), userID),
		IsTyping:    req.IsTyping,
		ExpiresInMS: channelUserTypingExpiresInMS,
	})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

type canonicalChannelMessageInput struct {
	Channel                 ChannelResponse
	WorkspaceID             string
	UserID                  string
	Content                 string
	Parts                   []protocol.MessagePart
	AttachmentIDs           []pgtype.UUID
	ReplyToMessageID        pgtype.UUID
	QuoteMessageID          pgtype.UUID
	QuoteSnapshot           []byte
	ThreadRootMessageID     pgtype.UUID
	ThreadID                *string
	ClientMessageID         *string
	BeforeRecipientPlanning func(*Handler, context.Context, ChannelMessageResponse, bool) error
}

func (h *Handler) sendPreparedCanonicalChannelMessage(ctx context.Context, input canonicalChannelMessageInput) (service.CanonicalChannelMessageResult[channelMessageCreateResult, *canonicalMessageDeliveryPlan], error) {
	threadID := uuid.NewString()
	if input.ThreadID != nil && strings.TrimSpace(*input.ThreadID) != "" {
		threadID = *input.ThreadID
	}
	return service.SendCanonicalChannelMessage(ctx, service.CanonicalChannelMessageOperation[channelMessageCreateResult, *canonicalMessageDeliveryPlan]{
		Validate: func(context.Context) error {
			if h == nil || h.DB == nil || strings.TrimSpace(input.Channel.ID) == "" || strings.TrimSpace(input.WorkspaceID) == "" || strings.TrimSpace(input.UserID) == "" {
				return errors.New("canonical channel message context is incomplete")
			}
			if input.Content == "" && !channelPartsAllowEmptyContent(input.Parts) {
				return errors.New("content is required")
			}
			if len([]rune(input.Content)) > channelMessageMaxLen {
				return errors.New("content is too long")
			}
			return nil
		},
		PersistAtomically: func(ctx context.Context) (service.CanonicalChannelMessagePersistence[channelMessageCreateResult, *canonicalMessageDeliveryPlan], error) {
			return h.persistPreparedCanonicalChannelMessage(ctx, input, threadID)
		},
		Publish: func(ctx context.Context, result channelMessageCreateResult) error {
			msg := result.Message
			_, _ = h.DB.Exec(ctx, `UPDATE channel SET updated_at = now() WHERE id = $1`, parseUUID(input.Channel.ID))
			if input.Channel.Kind == "dm" {
				h.clearDMHiddenForChannelMembers(ctx, input.WorkspaceID, parseUUID(input.Channel.ID))
			}
			// Do not call publishChannelToMembers here: its compatibility path
			// schedules recipient selection again. This send already persisted the
			// exact complete recipient set through the application service.
			recipientIDs := recipientUserIDsFromSet(h.channelHumanMemberIDs(ctx, input.WorkspaceID, input.Channel.ID))
			h.publishToUsers(protocol.EventChannelMessage, input.WorkspaceID, "member", input.UserID, recipientIDs, msg)
			h.observeChannelAgentTriggerDepth(protocol.EventChannelMessage, msg)
			return nil
		},
		PostAck: func(ctx context.Context, result channelMessageCreateResult, created bool, plans []*canonicalMessageDeliveryPlan) {
			msg := result.Message
			h.runAfterChannelMessageAck(ctx, func(ctx context.Context) {
				h.notifyCanonicalMessageDeliveryPlans(ctx, input.Channel, plans)
				if channelMessageNeedsVoiceTranscription(msg.Parts) {
					if err := h.processChannelVoiceTranscription(ctx, msg.ID); err != nil {
						slog.Error("channel voice transcription failed", "message_id", msg.ID, "error", err)
					}
					return
				}
				if created {
					h.dispatchHumanChannelMessageSideEffects(ctx, input.Channel, msg, parseUUID(input.UserID))
				}
			})
		},
	})
}

// sendCanonicalChannelMessage is the env-dispatch adapter for the same
// application service used by the frontend send path. Env dispatch sends a

// persistPreparedCanonicalChannelMessage owns the single durable boundary for
// a canonical send. A unique-key replay rolls back PostgreSQL's aborted insert
// transaction, validates the existing payload, then starts a fresh transaction
// that idempotently repairs any missing delivery or mixed-run obligation rows.
func (h *Handler) persistPreparedCanonicalChannelMessage(ctx context.Context, input canonicalChannelMessageInput, threadID string) (service.CanonicalChannelMessagePersistence[channelMessageCreateResult, *canonicalMessageDeliveryPlan], error) {
	var zero service.CanonicalChannelMessagePersistence[channelMessageCreateResult, *canonicalMessageDeliveryPlan]
	insertInput := channelMessageInsertInput{
		ChannelID: parseUUID(input.Channel.ID), WorkspaceID: parseUUID(input.WorkspaceID),
		AuthorID: parseUUID(input.UserID), AuthorName: h.channelAuthorName(ctx, input.UserID),
		Content: input.Content, Parts: input.Parts,
		ReplyToMessageID: input.ReplyToMessageID, QuoteMessageID: input.QuoteMessageID,
		QuoteSnapshot: input.QuoteSnapshot, ThreadRootMessageID: input.ThreadRootMessageID,
		ThreadID: &threadID, ClientMessageID: input.ClientMessageID,
	}
	if h.TxStarter == nil {
		return zero, errors.New("canonical channel message transaction unavailable")
	}
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return zero, err
	}
	result, err := h.createUserChannelMessageTx(ctx, tx, insertInput, input.AttachmentIDs)
	if err != nil {
		_ = tx.Rollback(ctx)
		if input.ClientMessageID == nil || !isUniqueViolation(err) {
			return zero, err
		}
		result, err = h.resolveDuplicateUserChannelMessage(ctx, insertInput, input.AttachmentIDs)
		if err != nil {
			return zero, err
		}
		tx, err = h.TxStarter.Begin(ctx)
		if err != nil {
			return zero, fmt.Errorf("begin canonical replay repair transaction: %w", err)
		}
	}
	defer tx.Rollback(ctx)
	txHandler := *h
	txHandler.DB = tx
	if h.Queries != nil {
		txHandler.Queries = h.Queries.WithTx(tx)
	}

	if result.Created {
		txHandler.observeGraphMemoryChannelActivity(ctx, input.Channel, result.Message)
	}
	if input.BeforeRecipientPlanning != nil {
		if err := input.BeforeRecipientPlanning(&txHandler, ctx, result.Message, result.Created); err != nil {
			return zero, err
		}
	}
	plans, err := txHandler.planCanonicalMessageDeliveryRecipients(ctx, input.Channel, result.Message)
	if err != nil {
		return zero, err
	}
	if err := persistCanonicalMessageDeliveryPlansTx(ctx, tx, input.Channel, result.Message, plans); err != nil {
		return zero, err
	}
	if err := tx.Commit(ctx); err != nil {
		return zero, fmt.Errorf("commit atomic canonical message: %w", err)
	}
	result.Message = h.attachSingleChannelMessageDetails(ctx, input.WorkspaceID, parseUUID(input.UserID), result.Message)
	result.Message.UndeliveredMentions = h.undeliveredMentionsForMessage(ctx, input.Channel, result.Message.Content, result.Message.Parts)
	return service.CanonicalChannelMessagePersistence[channelMessageCreateResult, *canonicalMessageDeliveryPlan]{
		Message: result, Created: result.Created, Recipients: plans,
	}, nil
}

// new top-level text message, so reply, quote, attachment, and idempotency
// fields are intentionally empty here.
func (h *Handler) prepareCanonicalChannelMessage(ctx context.Context, ch ChannelResponse, req SendChannelMessageRequest, userID string) (service.CanonicalChannelMessageResult[channelMessageCreateResult, *canonicalMessageDeliveryPlan], error) {
	content, parts, err := messageparts.Normalize(req.Content, req.Parts)
	if err != nil {
		return service.CanonicalChannelMessageResult[channelMessageCreateResult, *canonicalMessageDeliveryPlan]{}, err
	}
	if content == "" && !channelPartsAllowEmptyContent(parts) {
		return service.CanonicalChannelMessageResult[channelMessageCreateResult, *canonicalMessageDeliveryPlan]{}, errors.New("content is required")
	}
	content, parts, err = h.enrichChannelMessageMentions(ctx, ch, content, parts)
	if err != nil {
		return service.CanonicalChannelMessageResult[channelMessageCreateResult, *canonicalMessageDeliveryPlan]{}, err
	}
	if err := h.validateDMMentionMembership(ctx, ch, content, parts); err != nil {
		return service.CanonicalChannelMessageResult[channelMessageCreateResult, *canonicalMessageDeliveryPlan]{}, err
	}
	return h.sendPreparedCanonicalChannelMessage(ctx, canonicalChannelMessageInput{
		Channel: ch, WorkspaceID: ch.WorkspaceID, UserID: userID,
		Content: content, Parts: parts,
	})
}

func (h *Handler) sendCanonicalChannelMessage(ctx context.Context, ch ChannelResponse, req SendChannelMessageRequest, userID string) (ChannelMessageResponse, error) {
	result, err := h.prepareCanonicalChannelMessage(ctx, ch, req, userID)
	if err != nil {
		return ChannelMessageResponse{}, err
	}
	result.Acknowledge(ctx)
	return result.Message.Message, nil
}

func (h *Handler) SendChannelMessage(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	channelID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "channelId"), "channel id")
	if !ok {
		return
	}
	var req SendChannelMessageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	content, parts, err := messageparts.Normalize(req.Content, req.Parts)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid message parts: "+err.Error())
		return
	}
	if content == "" && !channelPartsAllowEmptyContent(parts) {
		writeError(w, http.StatusBadRequest, "content is required")
		return
	}
	if len([]rune(content)) > channelMessageMaxLen {
		writeError(w, http.StatusBadRequest, "content is too long")
		return
	}
	clientMessageID, ok := normalizeChannelClientMessageID(w, req.ClientMessageID)
	if !ok {
		return
	}
	// Reference attachment resources from parts only (parts win; do not
	// dual-merge attachment_ids).
	// Pre-validate ids early so invalid input returns 400 before any state mutation.
	// The association is created after insert so it has a message id.
	attachmentIDs, ok := parseUUIDSliceOrBadRequest(w, attachmentIDsFromParts(parts), "attachment_id")
	if !ok {
		return
	}
	ch, found := h.getChannel(r.Context(), workspaceID, channelID)
	if !found {
		writeError(w, http.StatusNotFound, "channel not found")
		return
	}
	if !h.requireChannelUserMember(w, r.Context(), workspaceID, channelID, parseUUID(userID)) {
		return
	}
	if !h.requireChannelWritable(w, r.Context(), workspaceID, channelID) {
		return
	}
	if !h.requireDMChannelAgentAccess(w, r, workspaceID, userID, ch, true) {
		return
	}
	content, parts, err = h.enrichChannelMessageMentions(r.Context(), ch, content, parts)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.validateDMMentionMembership(r.Context(), ch, content, parts); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	replyToMessageID, ok := h.validateChannelReplyTarget(w, r.Context(), workspaceID, channelID, req.ReplyToMessageID)
	if !ok {
		return
	}
	quoteMessageID, quoteSnapshot, ok := h.resolveChannelQuoteTarget(w, r.Context(), workspaceID, channelID, req, pgtype.UUID{})
	if !ok {
		return
	}
	result, err := h.sendPreparedCanonicalChannelMessage(r.Context(), canonicalChannelMessageInput{
		Channel:          ch,
		WorkspaceID:      workspaceID,
		UserID:           userID,
		Content:          content,
		Parts:            parts,
		AttachmentIDs:    attachmentIDs,
		ReplyToMessageID: replyToMessageID,
		QuoteMessageID:   quoteMessageID,
		QuoteSnapshot:    quoteSnapshot,
		ClientMessageID:  clientMessageID,
	})
	if err != nil {
		if errors.Is(err, errChannelClientMessageConflict) {
			writeError(w, http.StatusConflict, "client_message_id conflicts with an existing channel message")
			return
		}
		if errors.Is(err, errChannelAttachmentUnavailable) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to create channel message")
		return
	}
	msg := result.Message.Message
	if !result.Created {
		writeJSON(w, http.StatusOK, msg)
		result.Acknowledge(r.Context())
		return
	}
	// LRM-717: message send is HTTP; WS presence alone can lag/expire. Force an
	// online lease + member:presence so message-stream avatars heal Offline.
	h.noteMemberActivity(workspaceID, userID, true)
	// Ack first: agent wake fanout (and Feishu sync) are O(agents)/network and
	// must not inflate the client's send latency / Sending... state.
	writeJSON(w, http.StatusCreated, msg)
	h.awardHonorXP(r.Context(), parseUUID(userID), "channel.message", msg.ID)
	result.Acknowledge(r.Context())
}

func (h *Handler) ingestWendyHumanGroupMessage(ctx context.Context, ch ChannelResponse, msg ChannelMessageResponse) {
	if h.WorkGraph == nil || ch.Kind != "group" {
		return
	}
	mentions := util.ParseMentionsFromContentAndParts(msg.Content, msg.Parts)
	agentIDs := make([]pgtype.UUID, 0, len(mentions))
	for _, mention := range mentions {
		if mention.Type == "agent" {
			agentIDs = append(agentIDs, parseUUID(mention.ID))
		}
	}
	if len(agentIDs) == 0 {
		return
	}
	content := strings.ToLower(msg.Content)
	if channelMessageSignalsRework(content) {
		if err := h.WorkGraph.HandleHumanRework(ctx, parseUUID(ch.WorkspaceID), parseUUID(ch.ID), agentIDs); err != nil {
			slog.Warn("ingest Wendy rework signal failed", "channel_id", ch.ID, "message_id", msg.ID, "error", err)
		}
		return
	}
	if channelMessageSignalsCommitment(content) {
		for _, agentID := range agentIDs {
			if err := h.WorkGraph.UpsertChatCommitment(ctx, parseUUID(ch.WorkspaceID), parseUUID(ch.ID), agentID, strings.TrimSpace(msg.Content)); err != nil {
				slog.Warn("ingest Wendy chat commitment failed", "channel_id", ch.ID, "message_id", msg.ID, "error", err)
			}
		}
	}
}

func channelMessageSignalsRework(content string) bool {
	for _, signal := range []string{"修改", "返工", "重做", "不对", "先别做", "停下", "stop", "rework", "fix"} {
		if strings.Contains(content, signal) {
			return true
		}
	}
	return false
}

func channelMessageSignalsCommitment(content string) bool {
	for _, signal := range []string{"去做", "负责", "你来", "please handle"} {
		if strings.Contains(content, signal) {
			return true
		}
	}
	return false
}

func (h *Handler) ImportLarkChannelMessage(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	var req ImportLarkChannelMessageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	larkChatID := strings.TrimSpace(req.LarkChatID)
	content := strings.TrimSpace(req.Content)
	if larkChatID == "" || content == "" {
		writeError(w, http.StatusBadRequest, "lark_chat_id and content are required")
		return
	}
	if len([]rune(content)) > channelMessageMaxLen {
		writeError(w, http.StatusBadRequest, "content is too long")
		return
	}
	authorName := strings.TrimSpace(req.AuthorName)
	if authorName == "" {
		authorName = "Feishu"
	}
	externalID := strings.TrimSpace(req.ExternalMessageID)
	var external *string
	if externalID != "" {
		external = &externalID
	}

	ch, found := h.getChannelByLarkChatID(r.Context(), workspaceID, larkChatID)
	if !found {
		writeError(w, http.StatusNotFound, "channel not found")
		return
	}
	if ch.ArchivedAt != nil {
		writeError(w, http.StatusConflict, "channel is archived")
		return
	}
	if !h.requireChannelUserMember(w, r.Context(), workspaceID, parseUUID(ch.ID), parseUUID(userID)) {
		return
	}
	content, parts, err := h.enrichChannelMessageMentions(r.Context(), ch, content, nil)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	threadID := uuid.NewString()
	if h.TxStarter == nil {
		writeError(w, http.StatusInternalServerError, "transaction starter unavailable")
		return
	}
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to begin lark message import")
		return
	}
	defer tx.Rollback(r.Context())
	inserted, err := insertChannelMessageWithPartsExec(
		r.Context(), tx, parseUUID(ch.ID), parseUUID(workspaceID), "lark", pgtype.UUID{},
		authorName, content, parts, "lark", external, nil, pgtype.UUID{}, pgtype.UUID{}, nil,
		pgtype.UUID{}, &threadID, 0, channelMessageKindHint{},
	)
	if err != nil {
		if errorsIsNoRows(err) || isUniqueViolation(err) {
			writeError(w, http.StatusNotFound, "channel not found or message already imported")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to import lark message")
		return
	}
	msg := inserted.Message
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit lark message import")
		return
	}
	msg = h.attachSingleChannelMessageDetails(r.Context(), workspaceID, parseUUID(userID), msg)
	_, _ = h.DB.Exec(r.Context(), `UPDATE channel SET updated_at = now() WHERE id = $1`, parseUUID(msg.ChannelID))
	h.publishChannelToMembers(r.Context(), protocol.EventChannelMessage, workspaceID, "member", userID, parseUUID(ch.ID), msg)
	writeJSON(w, http.StatusCreated, msg)
	h.runAfterChannelMessageAck(r.Context(), func(ctx context.Context) {
		h.dispatchChannelMessageToAgents(ctx, ch, msg, parseUUID(userID))
	})
}

func (h *Handler) validateChannelMemberTarget(w http.ResponseWriter, r *http.Request, workspaceID string, channelID pgtype.UUID, memberType string, memberID pgtype.UUID) bool {
	switch memberType {
	case "user":
		if _, err := h.getWorkspaceMember(r.Context(), uuidToString(memberID), workspaceID); err != nil {
			writeError(w, http.StatusNotFound, "workspace member not found")
			return false
		}
		return true
	case "agent":
		agent, err := h.Queries.GetAgentInWorkspace(r.Context(), db.GetAgentInWorkspaceParams{ID: memberID, WorkspaceID: parseUUID(workspaceID)})
		if err != nil || agent.ArchivedAt.Valid {
			writeError(w, http.StatusNotFound, "agent not found")
			return false
		}
		return true
	default:
		writeError(w, http.StatusBadRequest, "member_type must be user or agent")
		return false
	}
}

func (h *Handler) dispatchChannelMessageToAgents(ctx context.Context, ch ChannelResponse, trigger ChannelMessageResponse, initiatorUserID pgtype.UUID) {
	h.dispatchChannelMessageToAgentsWithCursorPolicy(ctx, ch, trigger, initiatorUserID, false)
}

// dispatchTranscribedChannelMessageToAgents treats the newly available
// transcript as unread even when later channel traffic already advanced an
// agent's normal ambient cursor beyond this message's sequence.
func (h *Handler) dispatchTranscribedChannelMessageToAgents(ctx context.Context, ch ChannelResponse, trigger ChannelMessageResponse, initiatorUserID pgtype.UUID) {
	h.dispatchChannelMessageToAgentsWithCursorPolicy(ctx, ch, trigger, initiatorUserID, true)
	// A pending voice recording is intentionally not delivered until its
	// transcript becomes canonical. This path is therefore the committed
	// delivery boundary for the completed transcript.
	h.deliverCanonicalMessageToChannelAgents(ctx, ch, trigger)
}

func (h *Handler) dispatchChannelMessageToAgentsWithCursorPolicy(ctx context.Context, ch ChannelResponse, trigger ChannelMessageResponse, initiatorUserID pgtype.UUID, replayTrigger bool) {
	// #2295 hard-cut: ordinary channel/DM Agent chat no longer creates
	// task-shaped agent_inbox_event wakes. Canonical Message delivery
	// (scheduleCanonicalMessageDelivery → agent_message_delivery +
	// agent:deliver → MessageCoordinator) is the sole chat receive path.
	// Product-task inbox (issues, training, research, collaboration, voice
	// bridge, onboarding) still uses enqueueChannelAgentPrompt* via dedicated
	// callers.
	//
	// Keep this literal stable — ops greps production binaries for it to prove the
	// #2539 wake restore is actually present in the deployed image.
	slog.Debug("channel human wake dispatch restored", "channel", ch.ID, "replay", replayTrigger)
	_ = initiatorUserID
	_ = replayTrigger

	// LRM-1523 echo suppression: confirmation no-wakes still skip non-delivery
	// bookkeeping so facilitator / metrics stay quiet for pure acknowledgements.
	if !channelMessageIsHumanAuthored(trigger.Type) {
		if channelMessageIsConfirmationNoWake(trigger) {
			if h.Metrics != nil {
				h.Metrics.RecordChannelAmbientGateDecision(channelAmbientGateActionRelevanceSkipped, channelAmbientSkipReasonNonAction)
			}
			return
		}
	}

	// Notify mentioned humans — independent of Agent delivery.
	h.notifyChannelMemberMentions(ctx, ch, trigger)
	mentionedAgents := h.channelMentionedAgents(ctx, ch.WorkspaceID, ch.ID, trigger.Content, trigger.Parts)
	groupCommand := channelMessageIsHumanAuthored(trigger.Type) && channelMessageIsGroupCommand(trigger.Content, trigger.Parts)
	if len(mentionedAgents) > 0 && !groupCommand {
		for _, agent := range mentionedAgents {
			if len(mentionedAgents) == 1 {
				h.markTriggerFacilitatorIfNeeded(ctx, ch, agent, trigger)
			}
			if h.Metrics != nil {
				h.Metrics.RecordChannelFullExecutionWake("explicit_mention")
			}
		}
		return
	}
	if channelMessageIsHumanAuthored(trigger.Type) {
		h.recordChannelUnmentionedMessage()
	}
	if groupCommand || channelMessageIsHumanAuthored(trigger.Type) {
		if h.Metrics != nil {
			h.Metrics.RecordChannelFullExecutionWake("legacy_full")
		}
		h.recordChannelUnmentionedFullWake()
	}
}

func (h *Handler) dispatchChannelMentions(ctx context.Context, ch ChannelResponse, trigger ChannelMessageResponse, initiatorUserID pgtype.UUID) {
	h.dispatchChannelMessageToAgents(ctx, ch, trigger, initiatorUserID)
}

func (h *Handler) dispatchChannelThreadReplyMentions(ctx context.Context, ch ChannelResponse, trigger ChannelMessageResponse, initiatorUserID pgtype.UUID) {
	// #2295: thread replies use canonical Message delivery only. Keep human
	// mentions, thread-follower bookkeeping, and facilitator marks; do not
	// enqueue task-shaped agent_inbox_event wakes.
	_ = initiatorUserID
	h.notifyChannelMemberMentions(ctx, ch, trigger)

	// LRM-1523 echo suppression mirrors the main-channel path.
	if !channelMessageIsHumanAuthored(trigger.Type) {
		if channelMessageIsConfirmationNoWake(trigger) {
			return
		}
	}

	mentionedAgents := h.channelMentionedAgents(ctx, ch.WorkspaceID, ch.ID, trigger.Content, trigger.Parts)
	if len(mentionedAgents) > 0 {
		for _, agent := range mentionedAgents {
			if trigger.ThreadRootMessageID != nil {
				h.followChannelThreadAgentUnlessExplicitlyUnfollowed(ctx, parseUUID(ch.ID), parseUUID(*trigger.ThreadRootMessageID), agent.ID)
			}
			if len(mentionedAgents) == 1 {
				h.markTriggerFacilitatorIfNeeded(ctx, ch, agent, trigger)
			}
			if h.Metrics != nil {
				h.Metrics.RecordChannelFullExecutionWake("explicit_mention")
			}
		}
		return
	}
	if trigger.ThreadRootMessageID == nil {
		return
	}
	for _, agent := range h.channelThreadFollowerAgents(ctx, ch.WorkspaceID, ch.ID, *trigger.ThreadRootMessageID) {
		if h.isChannelAgentMuted(ctx, parseUUID(ch.ID), parseUUID(ch.WorkspaceID), agent.ID) {
			continue
		}
		if h.Metrics != nil {
			h.Metrics.RecordChannelFullExecutionWake("thread_reply")
		}
	}
}

func (h *Handler) dispatchChannelMessageWake(ctx context.Context, ch ChannelResponse, trigger ChannelMessageResponse, initiatorUserID pgtype.UUID) {
	h.dispatchChannelMessageWakeExcept(ctx, ch, trigger, initiatorUserID, nil)
}

func (h *Handler) dispatchChannelMessageWakeExcept(ctx context.Context, ch ChannelResponse, trigger ChannelMessageResponse, initiatorUserID pgtype.UUID, skipAgentIDs map[string]struct{}) {
	h.dispatchChannelMessageWakeExceptWithCursorPolicy(ctx, ch, trigger, initiatorUserID, skipAgentIDs, false)
}

func (h *Handler) dispatchTranscribedChannelMessageWakeExcept(ctx context.Context, ch ChannelResponse, trigger ChannelMessageResponse, initiatorUserID pgtype.UUID, skipAgentIDs map[string]struct{}) {
	h.dispatchChannelMessageWakeExceptWithCursorPolicy(ctx, ch, trigger, initiatorUserID, skipAgentIDs, true)
}

func (h *Handler) dispatchChannelMessageWakeExceptWithCursorPolicy(ctx context.Context, ch ChannelResponse, trigger ChannelMessageResponse, initiatorUserID pgtype.UUID, skipAgentIDs map[string]struct{}, replayTrigger bool) {
	// #2295: ambient/full channel wakes no longer mint agent_inbox_event rows.
	// Canonical delivery already covers unmuted recipients on publish.
	_ = ctx
	_ = ch
	_ = trigger
	_ = initiatorUserID
	_ = skipAgentIDs
	_ = replayTrigger
}

func (h *Handler) dispatchSingleChannelMessageWake(ctx context.Context, ch ChannelResponse, trigger ChannelMessageResponse, initiatorUserID pgtype.UUID, agent db.Agent) {
	h.dispatchSingleChannelMessageWakeWithCursorPolicy(ctx, ch, trigger, initiatorUserID, agent, false)
}

func (h *Handler) dispatchSingleChannelMessageWakeWithCursorPolicy(ctx context.Context, ch ChannelResponse, trigger ChannelMessageResponse, initiatorUserID pgtype.UUID, agent db.Agent, replayTrigger bool) {
	if h.TxStarter == nil {
		slog.Warn("channel message wake: transaction starter missing", "channel", ch.ID, "agent", uuidToString(agent.ID))
		return
	}
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		slog.Warn("channel message wake: begin transaction failed", "channel", ch.ID, "agent", uuidToString(agent.ID), "error", err)
		return
	}
	defer tx.Rollback(ctx)
	if err := h.lockChannelAmbientGate(ctx, tx, ch, agent); err != nil {
		slog.Warn("channel message wake: advisory lock failed", "channel", ch.ID, "agent", uuidToString(agent.ID), "error", err)
		return
	}
	conversationID, workspaceID, cursorSeq, pendingToSeq, ok := h.channelAmbientWakeCursorTx(ctx, tx, ch, agent, trigger)
	if !ok {
		return
	}
	if replayTrigger {
		if trigger.Seq <= 0 {
			slog.Warn("transcribed channel message wake: trigger sequence missing", "channel", ch.ID, "message", trigger.ID)
			return
		}
		cursorSeq = trigger.Seq - 1
		pendingToSeq = trigger.Seq
	} else if pendingToSeq <= cursorSeq {
		if err := tx.Commit(ctx); err != nil {
			slog.Warn("channel message wake: commit empty cursor failed", "channel", ch.ID, "agent", uuidToString(agent.ID), "error", err)
		}
		return
	}
	qtx := h.Queries.WithTx(tx)
	txResult, err := h.enqueueOrCoalesceChannelMessageWakeWithTx(ctx, qtx, tx, ch, agent, trigger, initiatorUserID, conversationID, workspaceID, cursorSeq, pendingToSeq)
	if err != nil {
		slog.Warn("channel message wake: persist prompt and inbox event failed", "channel", ch.ID, "agent", uuidToString(agent.ID), "error", err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		slog.Warn("channel message wake: commit failed", "channel", ch.ID, "agent", uuidToString(agent.ID), "inbox_event", uuidToString(txResult.Event.ID), "error", err)
		return
	}
	if !txResult.Coalesced {
		h.recordChannelAgentPromptWake(ctx, ch, agent, trigger, channelMessageWakeReason, txResult)
	}
}

// dispatchChannelAgentReply runs one agent's reply to a triggering message:
// ensure the channel<->agent chat session, persist the user-role prompt, create
// an agent session, and write a wake-required inbox event. Shared by @-mention
// dispatch (group channels) and DM auto-dispatch (1-on-1 channel whose peer is
// an agent).
//
// Two guards keep the agent-reply loop bounded and prevent self-conversation and
// MUST be preserved for both callers:
//   - trigger-depth limit: an agent reply that itself re-triggers stops at the limit.
//   - self-trigger skip: an agent's own message never re-triggers that same agent
//     (otherwise a 1-on-1 agent DM would loop on the agent's own replies forever).

func (h *Handler) dispatchChannelAgentReply(ctx context.Context, ch ChannelResponse, agent db.Agent, trigger ChannelMessageResponse, initiatorUserID pgtype.UUID) {
	_, _ = h.dispatchChannelAgentReplyWithReason(ctx, ch, agent, trigger, initiatorUserID, "")
}

func (h *Handler) dispatchChannelAgentReplyWithReason(ctx context.Context, ch ChannelResponse, agent db.Agent, trigger ChannelMessageResponse, initiatorUserID pgtype.UUID, reason string) (db.AgentInboxEvent, error) {
	if trigger.TriggerDepth >= channelRunTriggerLimit {
		slog.Warn("channel agent reply: trigger limit reached", "channel", ch.ID, "thread_id", ptrString(trigger.ThreadID), "depth", trigger.TriggerDepth)
		return db.AgentInboxEvent{}, errors.New("channel agent reply trigger limit reached")
	}
	if trigger.Type == "agent" && trigger.AuthorID != nil && *trigger.AuthorID == uuidToString(agent.ID) {
		return db.AgentInboxEvent{}, errors.New("agent cannot trigger itself")
	}
	rootID := h.channelThreadRootForTrigger(ch, trigger)
	facilitatorState := h.loadChannelFacilitatorState(ctx, rootID, agent.ID, trigger)
	if trigger.ThreadRootMessageID != nil {
		h.ensureChannelThreadAgentWakeParticipant(ctx, parseUUID(ch.ID), parseUUID(*trigger.ThreadRootMessageID), agent.ID)
	}
	if strings.TrimSpace(reason) == "" {
		reason = "mention"
		if ch.Kind == "dm" {
			reason = "dm"
		}
	}
	actorType, actorID := channelPromptActor(trigger, initiatorUserID)
	return h.enqueueChannelAgentPrompt(ctx, ch, agent, trigger, initiatorUserID, h.buildChannelMentionPromptForActor(ctx, ch, trigger, facilitatorState, actorType, actorID), "channel agent reply", true, reason, channelDirectedWakePriority)
}

func (h *Handler) dispatchChannelThreadContinuation(ctx context.Context, ch ChannelResponse, agent db.Agent, trigger ChannelMessageResponse, initiatorUserID pgtype.UUID) (db.AgentInboxEvent, error) {
	if trigger.TriggerDepth >= channelRunTriggerLimit {
		slog.Warn("channel thread continuation: trigger limit reached", "channel", ch.ID, "thread_id", ptrString(trigger.ThreadID), "depth", trigger.TriggerDepth)
		return db.AgentInboxEvent{}, errors.New("channel agent reply trigger limit reached")
	}
	if trigger.Type == "agent" && trigger.AuthorID != nil && *trigger.AuthorID == uuidToString(agent.ID) {
		return db.AgentInboxEvent{}, errors.New("agent cannot trigger itself")
	}
	if trigger.ThreadRootMessageID != nil {
		h.ensureChannelThreadAgentWakeParticipant(ctx, parseUUID(ch.ID), parseUUID(*trigger.ThreadRootMessageID), agent.ID)
	}
	prompt := h.buildChannelThreadContinuationPrompt(ctx, ch, agent, trigger)
	return h.enqueueChannelAgentPrompt(ctx, ch, agent, trigger, initiatorUserID, prompt, "channel thread continuation", true, "thread_reply", channelThreadReplyPriority)
}

func channelPromptActor(trigger ChannelMessageResponse, initiatorUserID pgtype.UUID) (string, string) {
	if trigger.Type == "agent" && trigger.AuthorID != nil {
		return "agent", *trigger.AuthorID
	}
	if initiatorUserID.Valid {
		return "member", uuidToString(initiatorUserID)
	}
	if channelMessageIsHumanAuthored(trigger.Type) && trigger.AuthorID != nil {
		return "member", *trigger.AuthorID
	}
	return "", ""
}

type channelAgentPromptTxResult struct {
	Event        db.AgentInboxEvent
	AgentSession db.AgentSession
	Coalesced    bool
}

// channelWakeContextType is stored on agent_inbox_event.context for ordinary
// channel wakes that no longer create a chat_session / chat_message prompt
// (LRM-1079). Claim hydrates Task.ChatMessage from context.prompt.
const channelWakeContextType = "channel_wake"

type channelWakeContext struct {
	Type                    string `json:"type"`
	Prompt                  string `json:"prompt"`
	ChannelID               string `json:"channel_id,omitempty"`
	ThreadID                string `json:"thread_id,omitempty"`
	ThreadRootMessageID     string `json:"thread_root_message_id,omitempty"`
	TriggerDepth            int    `json:"trigger_depth,omitempty"`
	ReactionTargetMessageID string `json:"reaction_target_message_id,omitempty"`
}

func buildChannelWakeContext(ch ChannelResponse, trigger ChannelMessageResponse, prompt string) ([]byte, error) {
	payload := channelWakeContext{
		Type:      channelWakeContextType,
		Prompt:    prompt,
		ChannelID: ch.ID,
	}
	if trigger.ThreadID != nil {
		payload.ThreadID = strings.TrimSpace(*trigger.ThreadID)
	}
	if trigger.ThreadRootMessageID != nil {
		payload.ThreadRootMessageID = strings.TrimSpace(*trigger.ThreadRootMessageID)
	}
	payload.TriggerDepth = trigger.TriggerDepth
	if trigger.ID != "" {
		payload.ReactionTargetMessageID = trigger.ID
	}
	return json.Marshal(payload)
}

func channelWakePromptFromContext(raw []byte) (string, bool) {
	if len(raw) == 0 {
		return "", false
	}
	var payload channelWakeContext
	if err := json.Unmarshal(raw, &payload); err != nil {
		return "", false
	}
	if payload.Type != channelWakeContextType {
		return "", false
	}
	prompt := strings.TrimSpace(payload.Prompt)
	return prompt, prompt != ""
}

func (h *Handler) enqueueChannelAgentPrompt(ctx context.Context, ch ChannelResponse, agent db.Agent, trigger ChannelMessageResponse, initiatorUserID pgtype.UUID, prompt, logScope string, showTyping bool, reason string, priority int32) (db.AgentInboxEvent, error) {
	typingActive := false
	if showTyping {
		h.publishChannelAgentTyping(ctx, ch, agent, true)
		typingActive = true
	}
	defer func() {
		if typingActive {
			h.publishChannelAgentTyping(ctx, ch, agent, false)
		}
	}()
	if h.TxStarter == nil {
		slog.Warn(logScope+": transaction starter missing", "channel", ch.ID, "agent", uuidToString(agent.ID))
		return db.AgentInboxEvent{}, errors.New("channel transaction starter missing")
	}
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		slog.Warn(logScope+": begin transaction failed", "channel", ch.ID, "agent", uuidToString(agent.ID), "error", err)
		return db.AgentInboxEvent{}, fmt.Errorf("begin channel agent prompt transaction: %w", err)
	}
	defer tx.Rollback(ctx)
	qtx := h.Queries.WithTx(tx)
	txResult, err := h.enqueueChannelAgentPromptWithTx(ctx, qtx, tx, ch, agent, trigger, initiatorUserID, prompt, reason, priority)
	if err != nil {
		slog.Warn(logScope+": persist prompt and inbox event failed", "channel", ch.ID, "agent", uuidToString(agent.ID), "error", err)
		return db.AgentInboxEvent{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		slog.Warn(logScope+": commit failed", "channel", ch.ID, "agent", uuidToString(agent.ID), "inbox_event", uuidToString(txResult.Event.ID), "error", err)
		return db.AgentInboxEvent{}, fmt.Errorf("commit channel agent prompt: %w", err)
	}
	typingActive = false
	if !txResult.Coalesced {
		h.recordChannelAgentPromptWake(ctx, ch, agent, trigger, reason, txResult)
	}
	return txResult.Event, nil
}

// enqueueChannelAgentPromptWithTx writes the prompt and directed inbox event
// using the caller's transaction. It deliberately emits no realtime or daemon
// side effects; the caller must commit first and then call
// recordChannelAgentPromptWake.
func (h *Handler) enqueueChannelAgentPromptWithTx(ctx context.Context, qtx *db.Queries, exec db.DBTX, ch ChannelResponse, agent db.Agent, trigger ChannelMessageResponse, initiatorUserID pgtype.UUID, prompt, reason string, priority int32) (channelAgentPromptTxResult, error) {
	seq := trigger.Seq
	return h.enqueueChannelAgentPromptRangeWithTx(ctx, qtx, exec, ch, agent, trigger, initiatorUserID, prompt, reason, priority, seq, seq)
}

func (h *Handler) enqueueChannelAgentPromptRangeWithTx(ctx context.Context, qtx *db.Queries, exec db.DBTX, ch ChannelResponse, agent db.Agent, trigger ChannelMessageResponse, initiatorUserID pgtype.UUID, prompt, reason string, priority int32, seqFrom, seqTo int64) (channelAgentPromptTxResult, error) {
	if !initiatorUserID.Valid {
		initiatorUserID = channelMessageTriggerCreatorID(trigger)
	}
	session, binding, handled, err := h.routeEnvDispatchChannelAgent(ctx, qtx, exec, ch.ID, ch.WorkspaceID, agent.ID, initiatorUserID)
	if err != nil {
		return channelAgentPromptTxResult{}, fmt.Errorf("route env-dispatch channel agent: %w", err)
	}
	// LRM-1079: ordinary channel wakes are channel-only (no chat_session).
	// Explicit keeps (not silent fallbacks):
	// - env-dispatch collaboration still stores triggers against chat_session
	// - live voice-call turns still wait on chat_message assistant rows
	var chatSessionID pgtype.UUID
	if handled {
		if binding.DerivedAgentID != nil && *binding.DerivedAgentID != "" {
			agent, err = qtx.GetAgent(ctx, parseUUID(*binding.DerivedAgentID))
			if err != nil {
				return channelAgentPromptTxResult{}, fmt.Errorf("resolve channel execution agent session: %w", err)
			}
		}
		chatSessionID = session.ID
	} else if reason == protocol.AgentInboxReasonVoiceCall && strings.Contains(prompt, "Live voice call delivery:") {
		voiceSession, ensureErr := h.ensureChannelAgentSessionWithDB(ctx, qtx, exec, ch, agent.ID, initiatorUserID)
		if ensureErr != nil {
			return channelAgentPromptTxResult{}, fmt.Errorf("ensure voice-call chat session: %w", ensureErr)
		}
		chatSessionID = voiceSession.ID
		session = voiceSession
	}
	conversationID, err := h.channelConversationIDWithDB(ctx, exec, parseUUID(ch.ID))
	if err != nil {
		return channelAgentPromptTxResult{}, fmt.Errorf("load channel conversation: %w", err)
	}
	agentSession, err := qtx.UpsertAgentSession(ctx, db.UpsertAgentSessionParams{
		WorkspaceID:    parseUUID(ch.WorkspaceID),
		AgentID:        agent.ID,
		ConversationID: conversationID,
		Scope:          channelAgentSessionScope(ch.Kind),
		ChannelID:      parseUUID(ch.ID),
		ChatSessionID:  chatSessionID,
	})
	if err != nil {
		return channelAgentPromptTxResult{}, fmt.Errorf("upsert channel agent session: %w", err)
	}
	if seqFrom <= 0 {
		seqFrom = trigger.Seq
	}
	if seqTo <= 0 || seqTo < seqFrom {
		seqTo = seqFrom
	}
	if priority >= channelDirectedWakePriority {
		if absorbedFrom, absorbedTo, err := h.absorbPendingSilentChannelWakesTx(ctx, exec, conversationID, agent.ID); err != nil {
			return channelAgentPromptTxResult{}, err
		} else {
			if absorbedFrom > 0 && (seqFrom == 0 || absorbedFrom < seqFrom) {
				seqFrom = absorbedFrom
			}
			if absorbedTo > seqTo {
				seqTo = absorbedTo
			}
		}
	} else if folded, ok, err := h.foldSilentUnreadIntoPendingDirectedWakeTx(ctx, qtx, exec, conversationID, agent.ID, seqFrom, seqTo); err != nil {
		return channelAgentPromptTxResult{}, err
	} else if ok {
		return folded, nil
	}
	if coalesced, ok, err := h.coalesceDirectedIssueInboxEventTx(ctx, qtx, exec, ch, agent, trigger, prompt, reason, priority, seqFrom, seqTo, conversationID); err != nil {
		return channelAgentPromptTxResult{}, err
	} else if ok {
		return coalesced, nil
	}
	if err := service.RequireAgentModel(agent.Model.String); err != nil {
		return channelAgentPromptTxResult{}, fmt.Errorf("channel agent wake: %w", err)
	}
	var promptMsgID pgtype.UUID
	if chatSessionID.Valid {
		promptMsg, err := h.createChannelAgentPromptMessageWithDB(ctx, exec, chatSessionID, prompt, trigger)
		if err != nil {
			return channelAgentPromptTxResult{}, fmt.Errorf("create channel agent prompt: %w", err)
		}
		promptMsgID = promptMsg.ID
	}
	event, err := qtx.CreateAgentInboxEvent(ctx, db.CreateAgentInboxEventParams{
		WorkspaceID:     parseUUID(ch.WorkspaceID),
		AgentSessionID:  agentSession.ID,
		ConversationID:  conversationID,
		AgentID:         agent.ID,
		Reason:          reason,
		RequiresWake:    true,
		Priority:        priority,
		SeqFrom:         seqFrom,
		SeqTo:           seqTo,
		ChannelID:       parseUUID(ch.ID),
		ChatSessionID:   chatSessionID,
		SourceMessageID: channelMessageTriggerID(trigger),
	})
	if err != nil {
		return channelAgentPromptTxResult{}, fmt.Errorf("create channel agent inbox event: %w", err)
	}
	if chatSessionID.Valid {
		if _, err := exec.Exec(ctx, `UPDATE chat_message SET task_id = $1 WHERE id = $2`, event.ID, promptMsgID); err != nil {
			return channelAgentPromptTxResult{}, fmt.Errorf("tag channel prompt with inbox event: %w", err)
		}
	} else {
		wakeContext, err := buildChannelWakeContext(ch, trigger, prompt)
		if err != nil {
			return channelAgentPromptTxResult{}, fmt.Errorf("encode channel wake context: %w", err)
		}
		if _, err := exec.Exec(ctx, `
			UPDATE agent_inbox_event
			SET context = $2,
			    initiator_user_id = $3,
			    updated_at = now()
			WHERE id = $1`, event.ID, wakeContext, nullableUUID(initiatorUserID)); err != nil {
			return channelAgentPromptTxResult{}, fmt.Errorf("attach channel wake context: %w", err)
		}
		event.Context = wakeContext
		event.InitiatorUserID = initiatorUserID
	}
	if handled {
		if binding.RuntimeID == nil {
			return channelAgentPromptTxResult{}, fmt.Errorf("ready env-dispatch binding has no runtime")
		}
		kind := "mention"
		switch reason {
		case channelMessageWakeReason:
			kind = "channel_message"
		case "handoff":
			kind = "handoff"
		case "continuation":
			kind = "continuation"
		}
		if err := (envDispatchChannelStore{}).saveTrigger(ctx, exec, binding.EnvID, envCollaborationTrigger{
			AgentID:             binding.SourceAgentID,
			Kind:                kind,
			ChannelID:           ch.ID,
			ProjectID:           uuidToString(session.ProjectID),
			ChatSessionID:       uuidToString(session.ID),
			SourceMessageID:     trigger.ID,
			ThreadRootMessageID: trigger.ThreadRootMessageID,
			TaskID:              uuidToString(event.ID),
			RuntimeID:           *binding.RuntimeID,
		}); err != nil {
			return channelAgentPromptTxResult{}, fmt.Errorf("save env-dispatch collaboration trigger: %w", err)
		}
	}
	return channelAgentPromptTxResult{Event: event, AgentSession: agentSession}, nil
}

func channelDirectedCoalesceReason(reason string) bool {
	switch strings.TrimSpace(reason) {
	case "mention", "thread_reply", "handoff":
		return true
	default:
		return false
	}
}

// absorbPendingSilentChannelWakesTx cancels pending priority<2 channel wakes
// (channel_message / thread_reply) for the same conversation+agent so a directed
// must-reply run does not follow a redundant silent ambient pass. Returns the
// absorbed seq span so the directed event can cover the same unread range.
func (h *Handler) absorbPendingSilentChannelWakesTx(ctx context.Context, exec db.DBTX, conversationID, agentID pgtype.UUID) (int64, int64, error) {
	rows, err := exec.Query(ctx, `
		SELECT id, agent_session_id, seq_from, seq_to
		FROM agent_inbox_event
		WHERE conversation_id = $1
		  AND agent_id = $2
		  AND requires_wake = true
		  AND status = 'pending'
		  AND priority < $3
		  AND reason IN ('channel_message', 'thread_reply')
		FOR UPDATE`, conversationID, agentID, channelDirectedWakePriority)
	if err != nil {
		return 0, 0, fmt.Errorf("load pending silent channel wakes: %w", err)
	}
	defer rows.Close()

	var minFrom, maxTo int64
	var sessionID pgtype.UUID
	var eventIDs []pgtype.UUID
	for rows.Next() {
		var eventID, agentSessionID pgtype.UUID
		var seqFrom, seqTo int64
		if err := rows.Scan(&eventID, &agentSessionID, &seqFrom, &seqTo); err != nil {
			return 0, 0, fmt.Errorf("scan pending silent channel wake: %w", err)
		}
		eventIDs = append(eventIDs, eventID)
		sessionID = agentSessionID
		if minFrom == 0 || seqFrom < minFrom {
			minFrom = seqFrom
		}
		if seqTo > maxTo {
			maxTo = seqTo
		}
	}
	if err := rows.Err(); err != nil {
		return 0, 0, err
	}
	if len(eventIDs) == 0 {
		return 0, 0, nil
	}
	if _, err := exec.Exec(ctx, `
		UPDATE agent_inbox_event
		SET status = 'acked',
		    acked_at = now(),
		    terminal_outcome = 'no_reply',
		    retryable = false,
		    terminal_at = now(),
		    updated_at = now()
		WHERE id = ANY($1::uuid[])`, eventIDs); err != nil {
		return 0, 0, fmt.Errorf("absorb pending silent channel wakes: %w", err)
	}
	if sessionID.Valid && maxTo > 0 {
		if _, err := exec.Exec(ctx, `
			UPDATE agent_session
			SET last_drained_seq = GREATEST(last_drained_seq, $2),
			    updated_at = now()
			WHERE id = $1`, sessionID, maxTo); err != nil {
			return 0, 0, fmt.Errorf("advance session cursor after silent wake absorb: %w", err)
		}
	}
	return minFrom, maxTo, nil
}

// foldSilentUnreadIntoPendingDirectedWakeTx extends a pending/draining directed
// wake's seq range when newer ambient unread arrives, instead of enqueueing a
// separate priority-1 ambient run.
func (h *Handler) foldSilentUnreadIntoPendingDirectedWakeTx(ctx context.Context, qtx *db.Queries, exec db.DBTX, conversationID, agentID pgtype.UUID, seqFrom, seqTo int64) (channelAgentPromptTxResult, bool, error) {
	var existingEventID, existingAgentSessionID pgtype.UUID
	var existingSeqFrom, existingSeqTo int64
	err := exec.QueryRow(ctx, `
		SELECT id, agent_session_id, seq_from, seq_to
		FROM agent_inbox_event
		WHERE conversation_id = $1
		  AND agent_id = $2
		  AND requires_wake = true
		  AND status IN ('pending', 'draining')
		  AND priority >= $3
		ORDER BY CASE status WHEN 'draining' THEN 0 WHEN 'pending' THEN 1 ELSE 2 END, created_at ASC
		LIMIT 1
		FOR UPDATE`, conversationID, agentID, channelDirectedWakePriority).Scan(&existingEventID, &existingAgentSessionID, &existingSeqFrom, &existingSeqTo)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return channelAgentPromptTxResult{}, false, nil
		}
		return channelAgentPromptTxResult{}, false, fmt.Errorf("load pending directed wake for ambient fold: %w", err)
	}
	if existingSeqFrom > 0 && existingSeqFrom < seqFrom {
		seqFrom = existingSeqFrom
	}
	if existingSeqTo > seqTo {
		seqTo = existingSeqTo
	}
	if _, err := exec.Exec(ctx, `
		UPDATE agent_inbox_event
		SET seq_from = $2,
		    seq_to = $3,
		    updated_at = now()
		WHERE id = $1`, existingEventID, seqFrom, seqTo); err != nil {
		return channelAgentPromptTxResult{}, false, fmt.Errorf("fold ambient unread into directed wake: %w", err)
	}
	event, err := qtx.GetAgentInboxEvent(ctx, existingEventID)
	if err != nil {
		return channelAgentPromptTxResult{}, false, fmt.Errorf("reload directed wake after ambient fold: %w", err)
	}
	agentSession, err := qtx.GetAgentSession(ctx, existingAgentSessionID)
	if err != nil {
		return channelAgentPromptTxResult{}, false, fmt.Errorf("reload directed wake agent session after ambient fold: %w", err)
	}
	return channelAgentPromptTxResult{Event: event, AgentSession: agentSession, Coalesced: true}, true, nil
}

func channelIssueReferenceTerms(trigger ChannelMessageResponse) ([]string, []string) {
	ids := map[string]struct{}{}
	labels := map[string]struct{}{}
	for _, part := range trigger.Parts {
		if part.Type != protocol.MessagePartTypeReference || part.RefType != "issue-ref" {
			continue
		}
		if refID := strings.TrimSpace(part.RefID); refID != "" {
			ids[refID] = struct{}{}
		}
		if label := strings.ToUpper(strings.TrimSpace(part.Label)); label != "" {
			labels[label] = struct{}{}
		}
	}
	for _, match := range channelIssueLabelPattern.FindAllString(strings.ToUpper(trigger.Content), -1) {
		labels[match] = struct{}{}
	}
	return sortedStringKeys(ids), sortedStringKeys(labels)
}

func sortedStringKeys(values map[string]struct{}) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	return out
}

func (h *Handler) coalesceDirectedIssueInboxEventTx(ctx context.Context, qtx *db.Queries, exec db.DBTX, ch ChannelResponse, agent db.Agent, trigger ChannelMessageResponse, prompt, reason string, priority int32, seqFrom, seqTo int64, conversationID pgtype.UUID) (channelAgentPromptTxResult, bool, error) {
	if !channelDirectedCoalesceReason(reason) {
		return channelAgentPromptTxResult{}, false, nil
	}
	issueIDs, issueLabels := channelIssueReferenceTerms(trigger)
	rootID := pgtype.UUID{}
	if trigger.ThreadRootMessageID != nil && strings.TrimSpace(*trigger.ThreadRootMessageID) != "" {
		rootID = parseUUID(*trigger.ThreadRootMessageID)
	}
	if len(issueIDs) == 0 && len(issueLabels) == 0 && !rootID.Valid {
		return channelAgentPromptTxResult{}, false, nil
	}

	var existingEventID, existingAgentSessionID, existingChatSessionID pgtype.UUID
	var existingSeqFrom, existingSeqTo int64
	err := exec.QueryRow(ctx, `
		SELECT e.id, e.agent_session_id, e.chat_session_id, e.seq_from, e.seq_to
		FROM agent_inbox_event e
		LEFT JOIN channel_message cm ON cm.id = e.source_message_id
		WHERE e.conversation_id = $1
		  AND e.agent_id = $2
		  AND e.reason = $3
		  AND e.requires_wake = true
		  AND e.status = 'pending'
		  AND (
		    (cardinality($4::text[]) > 0 AND EXISTS (
		      SELECT 1 FROM jsonb_array_elements(COALESCE(cm.parts, '[]'::jsonb)) part
		      WHERE part->>'type' = 'reference'
		        AND part->>'ref_type' = 'issue-ref'
		        AND part->>'ref_id' = ANY($4::text[])
		    ))
		    OR (cardinality($5::text[]) > 0 AND (
		      EXISTS (
		        SELECT 1 FROM jsonb_array_elements(COALESCE(cm.parts, '[]'::jsonb)) part
		        WHERE part->>'type' = 'reference'
		          AND part->>'ref_type' = 'issue-ref'
		          AND upper(part->>'label') = ANY($5::text[])
		      )
		      OR EXISTS (
		        SELECT 1 FROM unnest($5::text[]) label
		        WHERE upper(COALESCE(cm.content, '')) LIKE '%' || label || '%'
		      )
		    ))
		    OR ($6::uuid IS NOT NULL AND (cm.thread_root_message_id = $6::uuid OR cm.id = $6::uuid))
		  )
		  AND e.created_at >= now() - make_interval(secs => $7::double precision)
		ORDER BY e.created_at ASC
		LIMIT 1
		FOR UPDATE OF e`, conversationID, agent.ID, reason, issueIDs, issueLabels, nullableUUID(rootID), channelDirectedIssueCooldown.Seconds()).Scan(&existingEventID, &existingAgentSessionID, &existingChatSessionID, &existingSeqFrom, &existingSeqTo)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return channelAgentPromptTxResult{}, false, nil
		}
		return channelAgentPromptTxResult{}, false, fmt.Errorf("load coalescable directed inbox event: %w", err)
	}
	if existingSeqFrom > 0 && existingSeqFrom < seqFrom {
		seqFrom = existingSeqFrom
	}
	if existingSeqTo > seqTo {
		seqTo = existingSeqTo
	}
	wakeContext, err := buildChannelWakeContext(ch, trigger, prompt)
	if err != nil {
		return channelAgentPromptTxResult{}, false, fmt.Errorf("encode coalesced channel wake context: %w", err)
	}
	if _, err := exec.Exec(ctx, `
		UPDATE agent_inbox_event
		SET source_message_id = COALESCE($2, source_message_id),
		    status = CASE WHEN status = 'failed' THEN 'pending' ELSE status END,
		    priority = GREATEST(priority, $3),
		    seq_from = $4,
		    seq_to = $5,
		    context = CASE
		      WHEN chat_session_id IS NULL THEN $6::jsonb
		      ELSE context
		    END,
		    updated_at = now()
		WHERE id = $1`, existingEventID, nullableUUID(channelMessageTriggerID(trigger)), priority, seqFrom, seqTo, wakeContext); err != nil {
		return channelAgentPromptTxResult{}, false, fmt.Errorf("coalesce directed inbox event: %w", err)
	}
	if existingChatSessionID.Valid {
		if _, err := exec.Exec(ctx, `
			UPDATE chat_message
			SET content = $3
			WHERE chat_session_id = $1
			  AND task_id = $2
			  AND role = 'user'`, existingChatSessionID, existingEventID, prompt); err != nil {
			return channelAgentPromptTxResult{}, false, fmt.Errorf("refresh directed inbox prompt: %w", err)
		}
	}
	event, err := qtx.GetAgentInboxEvent(ctx, existingEventID)
	if err != nil {
		return channelAgentPromptTxResult{}, false, fmt.Errorf("reload coalesced inbox event: %w", err)
	}
	agentSession, err := qtx.GetAgentSession(ctx, existingAgentSessionID)
	if err != nil {
		return channelAgentPromptTxResult{}, false, fmt.Errorf("reload coalesced agent session: %w", err)
	}
	return channelAgentPromptTxResult{Event: event, AgentSession: agentSession, Coalesced: true}, true, nil
}

func (h *Handler) enqueueOrCoalesceChannelMessageWakeWithTx(ctx context.Context, qtx *db.Queries, exec db.DBTX, ch ChannelResponse, agent db.Agent, trigger ChannelMessageResponse, initiatorUserID, conversationID, workspaceID pgtype.UUID, cursorSeq, pendingToSeq int64) (channelAgentPromptTxResult, error) {
	// If a directed must-reply wake is already pending/draining, fold this
	// ambient unread into that run instead of starting a second (p1 then p10)
	// LLM pass for the same agent.
	if folded, ok, err := h.foldSilentUnreadIntoPendingDirectedWakeTx(ctx, qtx, exec, conversationID, agent.ID, cursorSeq+1, pendingToSeq); err != nil {
		return channelAgentPromptTxResult{}, err
	} else if ok {
		return folded, nil
	}
	prompt := h.buildChannelAmbientUnreadPromptWithDB(ctx, exec, ch, agent, trigger, cursorSeq, pendingToSeq)
	var existingEventID, existingChatSessionID pgtype.UUID
	var existingAgentSessionID pgtype.UUID
	var existingSeqFrom, existingSeqTo int64
	// Coalesce only pending/failed — not draining. Extending a draining event's
	// seq_to would mark mid-turn arrivals as covered when the agent still holds
	// the original MULTICA_TURN_SEQ_* context and does not re-read (Alice:
	// never swallow true-new messages). Residual mid-turn messages stay as a
	// separate pending wake after the active lease ends (serial, not concurrent).
	err := exec.QueryRow(ctx, `
		SELECT id, agent_session_id, chat_session_id, seq_from, seq_to
		FROM agent_inbox_event
		WHERE conversation_id = $1
		  AND agent_id = $2
		  AND reason = $3
		  AND status IN ('pending', 'failed')
		  AND requires_wake = true
		ORDER BY created_at ASC
		LIMIT 1
		FOR UPDATE`, conversationID, agent.ID, channelMessageWakeReason).Scan(&existingEventID, &existingAgentSessionID, &existingChatSessionID, &existingSeqFrom, &existingSeqTo)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return channelAgentPromptTxResult{}, fmt.Errorf("load pending channel message wake: %w", err)
	}
	if err == nil {
		seqFrom := existingSeqFrom
		if cursorSeq+1 < seqFrom {
			seqFrom = cursorSeq + 1
		}
		seqTo := existingSeqTo
		if pendingToSeq > seqTo {
			seqTo = pendingToSeq
		}
		wakeContext, err := buildChannelWakeContext(ch, trigger, prompt)
		if err != nil {
			return channelAgentPromptTxResult{}, fmt.Errorf("encode coalesced channel message wake context: %w", err)
		}
		if _, err := exec.Exec(ctx, `
			UPDATE agent_inbox_event
			SET agent_session_id = $2,
			    chat_session_id = $3,
			    channel_id = $4,
			    workspace_id = $5,
			    source_message_id = COALESCE($6, source_message_id),
			    status = 'pending',
			    priority = GREATEST(priority, $7),
			    seq_from = $8,
			    seq_to = $9,
			    context = CASE
			      WHEN $3::uuid IS NULL THEN $10::jsonb
			      ELSE context
			    END,
			    updated_at = now()
			WHERE id = $1`,
			existingEventID, existingAgentSessionID, existingChatSessionID, parseUUID(ch.ID), workspaceID, nullableUUID(channelAmbientTriggerID(trigger)), channelMessageWakePriority, seqFrom, seqTo, wakeContext); err != nil {
			return channelAgentPromptTxResult{}, fmt.Errorf("coalesce channel message wake: %w", err)
		}
		if existingChatSessionID.Valid {
			if _, err := exec.Exec(ctx, `
				UPDATE chat_message
				SET content = $3
				WHERE chat_session_id = $1
				  AND task_id = $2
				  AND role = 'user'`,
				existingChatSessionID, existingEventID, prompt); err != nil {
				return channelAgentPromptTxResult{}, fmt.Errorf("refresh channel message wake prompt: %w", err)
			}
		}
		event, err := qtx.GetAgentInboxEvent(ctx, existingEventID)
		if err != nil {
			return channelAgentPromptTxResult{}, fmt.Errorf("reload channel message wake: %w", err)
		}
		agentSession, err := qtx.GetAgentSession(ctx, existingAgentSessionID)
		if err != nil {
			return channelAgentPromptTxResult{}, fmt.Errorf("reload channel message wake agent session: %w", err)
		}
		return channelAgentPromptTxResult{Event: event, AgentSession: agentSession, Coalesced: true}, nil
	}
	return h.enqueueChannelAgentPromptRangeWithTx(ctx, qtx, exec, ch, agent, trigger, initiatorUserID, prompt, channelMessageWakeReason, channelMessageWakePriority, cursorSeq+1, pendingToSeq)
}

func (h *Handler) recordChannelAgentPromptWake(ctx context.Context, ch ChannelResponse, agent db.Agent, trigger ChannelMessageResponse, reason string, result channelAgentPromptTxResult) {
	h.publishAgentInboxTaskLifecycle(protocol.EventTaskQueued, result.Event, agent.RuntimeID, "queued")
}

// notifyChannelMemberMentions creates a "mentioned" inbox item for every human
// channel member @-mentioned in a channel message (by an agent, another member,
// or another member), so the mention surfaces in the recipient's overview "for me"
// list with a deep link back to the message. The message author is never
// notified about their own mention.
func (h *Handler) notifyChannelMemberMentions(ctx context.Context, ch ChannelResponse, msg ChannelMessageResponse) {
	mentions := util.ParseMentionsFromContentAndParts(msg.Content, msg.Parts)
	if len(mentions) == 0 {
		return
	}
	members := h.channelHumanMemberIDs(ctx, ch.WorkspaceID, ch.ID)
	if len(members) == 0 {
		return
	}

	recipients := map[string]bool{}
	for _, m := range mentions {
		switch m.Type {
		case "member":
			if members[m.ID] {
				recipients[m.ID] = true
			}
		}
	}
	// Never notify the author about their own message.
	if msg.AuthorID != nil {
		delete(recipients, *msg.AuthorID)
	}
	if len(recipients) == 0 {
		return
	}
	if msg.ThreadRootMessageID != nil {
		for id := range recipients {
			h.followChannelThreadUserUnlessExplicitlyUnfollowed(ctx, parseUUID(ch.ID), parseUUID(*msg.ThreadRootMessageID), parseUUID(id), false)
		}
	}

	// inbox_item.actor_type is constrained to member|agent|system.
	actorType := "system"
	var actorID pgtype.UUID
	switch msg.Type {
	case "user":
		actorType = "member"
		if msg.AuthorID != nil {
			actorID = parseUUID(*msg.AuthorID)
		}
	case "agent":
		actorType = "agent"
		if msg.AuthorID != nil {
			actorID = parseUUID(*msg.AuthorID)
		}
	}

	details, _ := json.Marshal(map[string]string{
		"channel_id":   ch.ID,
		"channel_name": ch.Name,
		"message_id":   msg.ID,
		"actor_name":   msg.AuthorName,
	})
	body := strings.TrimSpace(msg.Content)
	if runes := []rune(body); len(runes) > 280 {
		body = string(runes[:280])
	}
	title := fmt.Sprintf("%s mentioned you in #%s", msg.AuthorName, ch.Name)

	for id := range recipients {
		item, err := h.Queries.CreateInboxItem(ctx, db.CreateInboxItemParams{
			WorkspaceID:   parseUUID(ch.WorkspaceID),
			RecipientType: "member",
			RecipientID:   parseUUID(id),
			Type:          "mentioned",
			Severity:      "info",
			Title:         title,
			Body:          strToText(body),
			ActorType:     strToText(actorType),
			ActorID:       actorID,
			Details:       details,
		})
		if err != nil {
			slog.Warn("channel mention: inbox creation failed", "channel", ch.ID, "recipient", id, "error", err)
			continue
		}
		h.publish(protocol.EventInboxNew, ch.WorkspaceID, actorType, uuidToString(actorID), map[string]any{"item": inboxToResponse(item)})
	}
}

// channelHumanMemberIDs returns the set of human (user) member IDs in a channel.
func (h *Handler) channelHumanMemberIDs(ctx context.Context, workspaceID, channelID string) map[string]bool {
	rows, err := h.DB.Query(ctx, `
		SELECT member_id
		FROM channel_member
		WHERE channel_id = $1 AND workspace_id = $2 AND member_type = 'user'`,
		parseUUID(channelID), parseUUID(workspaceID))
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var id pgtype.UUID
		if err := rows.Scan(&id); err != nil {
			continue
		}
		out[uuidToString(id)] = true
	}
	return out
}

func (h *Handler) publishChannelToMembers(ctx context.Context, eventType, workspaceID, actorType, actorID string, channelID pgtype.UUID, payload any) {
	recipientIDs := recipientUserIDsFromSet(h.channelHumanMemberIDs(ctx, workspaceID, uuidToString(channelID)))
	h.publishToUsers(eventType, workspaceID, actorType, actorID, recipientIDs, payload)
	h.observeChannelAgentTriggerDepth(eventType, payload)
	h.scheduleCanonicalMessageDelivery(ctx, eventType, payload)
	if eventType != protocol.EventChannelMessage || h.DB == nil {
		return
	}
	msg, ok := payload.(ChannelMessageResponse)
	if !ok || msg.Type != "agent" || !channelMessageNeedsVoiceSynthesis(msg.Parts) {
		return
	}
	h.runAfterChannelMessageAck(ctx, func(ctx context.Context) {
		if err := h.processChannelVoiceSynthesis(ctx, msg.ID); err != nil {
			slog.Error("immediate channel voice synthesis failed", "message_id", msg.ID, "error", err)
		}
	})
}

// observeChannelAgentTriggerDepth records only committed agent chat messages
// after their publication. It intentionally excludes content from logs and
// keeps high-cardinality identifiers out of Prometheus labels.
func (h *Handler) observeChannelAgentTriggerDepth(eventType string, payload any) {
	if eventType != protocol.EventChannelMessage {
		return
	}
	msg, ok := payload.(ChannelMessageResponse)
	if !ok || msg.Type != "agent" {
		return
	}
	if h != nil && h.Metrics != nil {
		h.Metrics.ObserveChannelTriggerDepth(msg.TriggerDepth)
	}
	slog.Info("channel agent trigger depth observed",
		"workspace_id", msg.WorkspaceID,
		"channel_id", msg.ChannelID,
		"message_id", msg.ID,
		"thread_id", ptrString(msg.ThreadID),
		"thread_root_message_id", ptrString(msg.ThreadRootMessageID),
		"agent_id", ptrString(msg.AuthorID),
		"trigger_depth", msg.TriggerDepth,
	)
}

// runAfterChannelMessageAck runs send side effects that must not block the HTTP
// create acknowledgment (agent wake fanout, Feishu sync). Used by human
// SendChannelMessage* (LRM-272) and agent transport / chat-output inserts
// (LRM-297). Production detaches the request context and runs asynchronously;
// tests set SyncChannelMessageSideEffects so assertions after the handler
// still observe inbox/session rows.
func (h *Handler) runAfterChannelMessageAck(ctx context.Context, fn func(context.Context)) {
	if fn == nil {
		return
	}
	bg := context.WithoutCancel(ctx)
	run := fn
	if h != nil && h.channelMessagePostAckTestHook != nil {
		hook := h.channelMessagePostAckTestHook
		userFn := fn
		run = func(ctx context.Context) {
			hook(ctx)
			userFn(ctx)
		}
	}
	if h != nil && h.SyncChannelMessageSideEffects {
		run(bg)
		return
	}
	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				slog.Error("channel message post-ack side effect panicked", "recover", rec)
			}
		}()
		run(bg)
	}()
}

func (h *Handler) publishChannelToMembersWithID(ctx context.Context, eventType, workspaceID, actorType, actorID string, channelID pgtype.UUID, payload any, realtimeEventID string) error {
	recipientIDs := recipientUserIDsFromSet(h.channelHumanMemberIDs(ctx, workspaceID, uuidToString(channelID)))
	if err := h.publishToUsersWithID(eventType, workspaceID, actorType, actorID, recipientIDs, payload, realtimeEventID); err != nil {
		return err
	}
	h.scheduleCanonicalMessageDelivery(ctx, eventType, payload)
	return nil
}

func (h *Handler) publishChannelAgentTyping(ctx context.Context, ch ChannelResponse, agent db.Agent, isTyping bool) {
	h.publishChannelToMembers(ctx, protocol.EventChannelTyping, ch.WorkspaceID, "agent", uuidToString(agent.ID), parseUUID(ch.ID), protocol.ChannelTypingPayload{
		ChannelID:   ch.ID,
		ActorType:   "agent",
		ActorID:     uuidToString(agent.ID),
		ActorName:   agentDisplayName(agent),
		IsTyping:    isTyping,
		ExpiresInMS: channelAgentTypingExpiresInMS,
	})
}

func (h *Handler) channelMentionedAgents(ctx context.Context, workspaceID, channelID, content string, parts ...[]protocol.MessagePart) []db.Agent {
	var messageParts []protocol.MessagePart
	if len(parts) > 0 {
		messageParts = parts[0]
	}
	mentions := util.ParseMentionsFromContentAndParts(content, messageParts)
	mentionedAgents := map[string]struct{}{}
	for _, mention := range mentions {
		if mention.Type == "agent" {
			mentionedAgents[mention.ID] = struct{}{}
		}
	}
	// Structured references above carry explicit actor identity. For legacy
	// messages with no references, reuse the same finite-member longest-prefix
	// resolver that creates structured parts for newly written messages.
	bareMentionedAgents := map[string]struct{}{}
	for _, occurrence := range findBareMentionCandidates(content, h.channelMentionCandidates(ctx, workspaceID, channelID)) {
		if occurrence.Candidate.Type == "agent" {
			bareMentionedAgents[occurrence.Candidate.ID] = struct{}{}
		}
	}
	rows, err := h.DB.Query(ctx, `
		SELECT a.id, a.workspace_id, a.name, a.avatar_url, a.runtime_mode, a.runtime_config, a.status,
		       a.owner_id, a.created_at, a.updated_at, a.description, a.runtime_id,
		       a.instructions, a.archived_at, a.display_name, a.model, a.thinking_level
		FROM channel_member cm
		JOIN agent a ON cm.member_type = 'agent' AND a.id = cm.member_id
		WHERE cm.channel_id = $1 AND cm.workspace_id = $2 AND a.archived_at IS NULL`, parseUUID(channelID), parseUUID(workspaceID))
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []db.Agent
	for rows.Next() {
		var a db.Agent
		if err := rows.Scan(&a.ID, &a.WorkspaceID, &a.Name, &a.AvatarUrl, &a.RuntimeMode, &a.RuntimeConfig, &a.Status, &a.OwnerID, &a.CreatedAt, &a.UpdatedAt, &a.Description, &a.RuntimeID, &a.Instructions, &a.ArchivedAt, &a.DisplayName, &a.Model, &a.ThinkingLevel); err != nil {
			continue
		}
		_, mentionedByID := mentionedAgents[uuidToString(a.ID)]
		_, mentionedByBareHandle := bareMentionedAgents[uuidToString(a.ID)]
		if mentionedByID || mentionedByBareHandle {
			out = append(out, a)
		}
	}
	return out
}

func (h *Handler) channelThreadFollowerAgents(ctx context.Context, workspaceID, channelID, rootMessageID string) []db.Agent {
	return h.channelThreadAgentsFromQuery(ctx, `
		SELECT a.id, a.workspace_id, a.name, a.avatar_url, a.runtime_mode, a.runtime_config, a.status,
		       a.owner_id, a.created_at, a.updated_at, a.description, a.runtime_id,
		       a.instructions, a.archived_at, a.display_name, a.model, a.thinking_level
		FROM thread_participant tp
		JOIN channel_message root ON root.id = tp.root_message_id
		JOIN agent a ON tp.member_type = 'agent' AND a.id = tp.member_id
		JOIN channel_member cm ON cm.channel_id = root.channel_id AND cm.workspace_id = root.workspace_id AND cm.member_type = 'agent' AND cm.member_id = a.id
		WHERE tp.root_message_id = $3
		  AND root.channel_id = $1
		  AND root.workspace_id = $2
		  AND tp.followed_at IS NOT NULL
		  AND tp.wake_state = 'active'
		  AND a.archived_at IS NULL
		ORDER BY tp.updated_at DESC, a.id ASC`, parseUUID(channelID), parseUUID(workspaceID), parseUUID(rootMessageID))
}

func (h *Handler) channelThreadAgentsFromQuery(ctx context.Context, query string, args ...any) []db.Agent {
	rows, err := h.DB.Query(ctx, query, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []db.Agent
	for rows.Next() {
		var a db.Agent
		if err := rows.Scan(&a.ID, &a.WorkspaceID, &a.Name, &a.AvatarUrl, &a.RuntimeMode, &a.RuntimeConfig, &a.Status, &a.OwnerID, &a.CreatedAt, &a.UpdatedAt, &a.Description, &a.RuntimeID, &a.Instructions, &a.ArchivedAt, &a.DisplayName, &a.Model, &a.ThinkingLevel); err != nil {
			continue
		}
		out = append(out, a)
	}
	return out
}

func mentionHandleBoundaryBefore(content string, index int) bool {
	if index == 0 {
		return true
	}
	previous, _ := utf8.DecodeLastRuneInString(content[:index])
	return !isMentionHandleRune(previous)
}

func mentionHandleBoundaryAfter(content string, index int) bool {
	if index >= len(content) {
		return true
	}
	next, _ := utf8.DecodeRuneInString(content[index:])
	return !isMentionHandleRune(next)
}

func isMentionHandleRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsNumber(r) || r == '_' || r == '-'
}

type channelFacilitatorState struct {
	Active                         bool
	FacilitatorAgentID             string
	FacilitatorName                string
	CurrentAgentIsFacilitator      bool
	CurrentTriggerFromFacilitator  bool
	CurrentTriggerIsDirectAgentAsk bool
}

func detectFacilitatorIntent(content string) bool {
	lower := strings.ToLower(strings.TrimSpace(content))
	if lower == "" {
		return false
	}
	for _, needle := range []string{
		"主持", "协调", "收敛", "推进", "带大家讨论", "带大家聊", "主持一下", "帮大家收敛",
		"facilitat", "moderat", "coordinate", "drive this discussion", "drive to a decision",
	} {
		if strings.Contains(lower, needle) {
			return true
		}
	}
	return false
}

func looksLikeConcreteFacilitatorRequest(content string) bool {
	lower := strings.ToLower(strings.TrimSpace(content))
	if lower == "" {
		return false
	}
	if strings.Contains(lower, "?") || strings.Contains(lower, "？") {
		return true
	}
	for _, needle := range []string{
		"请", "帮", "给我", "列", "评估", "判断", "比较", "选择", "推荐", "总结", "回复",
		"please", "can you", "could you", "review", "compare", "pick", "summarize", "reply",
	} {
		if strings.Contains(lower, needle) {
			return true
		}
	}
	return false
}

func (h *Handler) channelThreadRootForTrigger(ch ChannelResponse, trigger ChannelMessageResponse) pgtype.UUID {
	if trigger.ThreadRootMessageID != nil && strings.TrimSpace(*trigger.ThreadRootMessageID) != "" {
		return parseUUID(*trigger.ThreadRootMessageID)
	}
	if ch.Kind == "group" && strings.TrimSpace(trigger.ID) != "" {
		return parseUUID(trigger.ID)
	}
	return pgtype.UUID{}
}

func (h *Handler) setChannelThreadAgentRole(ctx context.Context, channelID, rootID, agentID pgtype.UUID, role string) {
	if !rootID.Valid || !agentID.Valid || strings.TrimSpace(role) == "" {
		return
	}
	if _, err := h.DB.Exec(ctx, `
		WITH root AS (
		  SELECT conversation_id
		  FROM channel_message
		  WHERE id = $2 AND channel_id = $1
		)
		INSERT INTO thread_participant (conversation_id, root_message_id, member_type, member_id, role, followed_at, updated_at)
		SELECT root.conversation_id, $2, 'agent', $3, $4, now(), now()
		FROM root
		ON CONFLICT (root_message_id, member_type, member_id) DO UPDATE
		SET role = EXCLUDED.role,
		    followed_at = CASE
		      WHEN thread_participant.wake_state = 'unfollowed' THEN thread_participant.followed_at
		      ELSE COALESCE(thread_participant.followed_at, EXCLUDED.followed_at)
		    END,
		    wake_state = CASE
		      WHEN thread_participant.wake_state = 'unfollowed' THEN 'unfollowed'
		      ELSE 'active'
		    END,
		    updated_at = now()`,
		channelID, rootID, agentID, role); err != nil {
		slog.Warn("channel thread agent role update failed", "root", uuidToString(rootID), "agent", uuidToString(agentID), "role", role, "error", err)
	}
}

func (h *Handler) markTriggerFacilitatorIfNeeded(ctx context.Context, ch ChannelResponse, agent db.Agent, trigger ChannelMessageResponse) {
	if trigger.Type != "user" || !detectFacilitatorIntent(trigger.Content) {
		return
	}
	rootID := h.channelThreadRootForTrigger(ch, trigger)
	if !rootID.Valid {
		return
	}
	if _, err := h.DB.Exec(ctx, `
		UPDATE thread_participant
		SET role = 'participant', updated_at = now()
		WHERE root_message_id = $1 AND member_type = 'agent' AND role = 'facilitator' AND member_id <> $2`,
		rootID, agent.ID); err != nil {
		slog.Warn("channel facilitator reset failed", "root", uuidToString(rootID), "agent", uuidToString(agent.ID), "error", err)
	}
	h.setChannelThreadAgentRole(ctx, parseUUID(ch.ID), rootID, agent.ID, "facilitator")
}

func (h *Handler) loadChannelFacilitatorState(ctx context.Context, rootID, targetAgentID pgtype.UUID, trigger ChannelMessageResponse) channelFacilitatorState {
	if !rootID.Valid || !targetAgentID.Valid {
		return channelFacilitatorState{}
	}
	var facilitatorID pgtype.UUID
	var facilitatorName string
	if err := h.DB.QueryRow(ctx, `
		SELECT tp.member_id, COALESCE(NULLIF(a.display_name, ''), a.name, '')
		FROM thread_participant tp
		JOIN agent a ON a.id = tp.member_id
		WHERE tp.root_message_id = $1
		  AND tp.member_type = 'agent'
		  AND tp.role = 'facilitator'
		ORDER BY tp.updated_at DESC
		LIMIT 1`, rootID).Scan(&facilitatorID, &facilitatorName); err != nil {
		return channelFacilitatorState{}
	}
	state := channelFacilitatorState{
		Active:                    facilitatorID.Valid,
		FacilitatorAgentID:        uuidToString(facilitatorID),
		FacilitatorName:           firstNonEmpty(strings.TrimSpace(facilitatorName), uuidToString(facilitatorID)),
		CurrentAgentIsFacilitator: facilitatorID == targetAgentID,
	}
	if trigger.Type == "agent" && trigger.AuthorID != nil && *trigger.AuthorID == state.FacilitatorAgentID {
		state.CurrentTriggerFromFacilitator = true
		state.CurrentTriggerIsDirectAgentAsk = state.FacilitatorAgentID != uuidToString(targetAgentID) && looksLikeConcreteFacilitatorRequest(trigger.Content)
	}
	return state
}

func appendChannelFacilitatorPromptSection(b *strings.Builder, state channelFacilitatorState) {
	if !state.Active {
		return
	}
	if state.CurrentAgentIsFacilitator {
		b.WriteString("Facilitator mode is active for you in this thread.\n")
		b.WriteString("- You are the current facilitator/owner for this discussion thread.\n")
		b.WriteString("- While the discussion is still open, you may send 2-4 short purposeful follow-ups to collect input, narrow options, and conclude.\n")
		b.WriteString("- Each follow-up must move the thread forward: ask a concrete person a concrete question, summarize missing input, converge the shortlist, or post the conclusion.\n")
		b.WriteString("- End facilitator mode once you have a conclusion, a clear owner, or you hit the automatic-turn limit.\n\n")
		return
	}
	if state.CurrentTriggerIsDirectAgentAsk {
		fmt.Fprintf(b, "Facilitator request: %s is the active facilitator for this thread and is directly asking you for concrete input.\n", state.FacilitatorName)
		b.WriteString("- Treat this as a direct request, not a weak agent-to-agent notification.\n")
		b.WriteString("- Answer once with the requested input, then return to normal thread behavior.\n\n")
	}
}

func channelAgentSessionScope(channelKind string) string {
	if channelKind == "dm" {
		return "dm"
	}
	return "channel"
}

func channelMessageAmbientSkipReason(trigger ChannelMessageResponse) (bool, string) {
	if !channelMessageIsHumanAuthored(trigger.Type) {
		return true, "non_human_trigger"
	}
	if channelMessageHasAgentMention(trigger.Content, trigger.Parts) {
		return true, "directed_agent_mention"
	}
	if channelMessageHasOnlyNonTextNoiseParts(trigger.Parts) {
		return true, channelAmbientGateReasonNonTextNoise
	}
	// LRM-1523: a pure confirmation / acknowledgement (收到 / 明白 / OK / …)
	// carries no new information or action and must not be delivered as ambient
	// context (nor wake anyone). Treat it as a non_action no-op with an
	// observable silence reason.
	if channelContentIsPureConfirmation(trigger.Content, trigger.Parts) {
		return true, channelAmbientSkipReasonNonAction
	}
	if skip, reason := deterministicChannelAmbientRelevanceSkip(trigger.Content); skip {
		return true, reason
	}
	return false, ""
}

func channelMessageHasOnlyNonTextNoiseParts(parts []protocol.MessagePart) bool {
	if len(parts) == 0 {
		return false
	}
	for _, part := range parts {
		switch part.Type {
		case protocol.MessagePartTypeSticker, protocol.MessagePartTypeSystemEvent:
			continue
		case protocol.MessagePartTypeText:
			if strings.TrimSpace(part.Text) == "" {
				continue
			}
		}
		return false
	}
	return true
}

func (h *Handler) dispatchChannelAmbientObservation(ctx context.Context, ch ChannelResponse, trigger ChannelMessageResponse, initiatorUserID pgtype.UUID) {
	if skip, _ := channelMessageAmbientSkipReason(trigger); skip {
		return
	}
	for _, agent := range h.channelAgentMembers(ctx, ch.WorkspaceID, ch.ID) {
		// Skip agents who have muted this channel (#313 gate).
		if h.isChannelAgentMuted(ctx, parseUUID(ch.ID), parseUUID(ch.WorkspaceID), agent.ID) {
			continue
		}
		h.dispatchSingleChannelAmbientObservation(ctx, ch, trigger, initiatorUserID, agent)
	}
}

func (h *Handler) dispatchChannelAmbientDelivery(ctx context.Context, ch ChannelResponse, trigger ChannelMessageResponse) {
	h.dispatchChannelAmbientDeliveryExcept(ctx, ch, trigger, nil)
}

func (h *Handler) dispatchChannelAmbientDeliveryExcept(ctx context.Context, ch ChannelResponse, trigger ChannelMessageResponse, skipAgentIDs map[string]struct{}) {
	// #2295: observe-mode ambient inbox events removed. Agents learn about
	// channel traffic solely through canonical Message delivery.
	_ = ctx
	_ = ch
	_ = trigger
	_ = skipAgentIDs
}

func (h *Handler) recordChannelAmbientInboxEvent(ctx context.Context, ch ChannelResponse, trigger ChannelMessageResponse, agent db.Agent) {
	if h.TxStarter == nil {
		slog.Warn("channel ambient delivery: transaction starter missing", "channel", ch.ID, "agent", uuidToString(agent.ID))
		return
	}
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		slog.Warn("channel ambient delivery: begin failed", "channel", ch.ID, "agent", uuidToString(agent.ID), "error", err)
		return
	}
	defer tx.Rollback(ctx)

	conversationID, workspaceID, cursorSeq, pendingToSeq, ok := h.channelAmbientWakeCursorTx(ctx, tx, ch, agent, trigger)
	if !ok {
		return
	}
	if pendingToSeq <= cursorSeq {
		if err := tx.Commit(ctx); err != nil {
			slog.Warn("channel ambient delivery: commit empty cursor failed", "channel", ch.ID, "agent", uuidToString(agent.ID), "error", err)
		}
		return
	}
	qtx := h.Queries.WithTx(tx)
	agentSession, err := qtx.UpsertAgentSession(ctx, db.UpsertAgentSessionParams{
		WorkspaceID:    workspaceID,
		AgentID:        agent.ID,
		ConversationID: conversationID,
		Scope:          channelAgentSessionScope(ch.Kind),
		ChannelID:      parseUUID(ch.ID),
	})
	if err != nil {
		slog.Warn("channel ambient delivery: upsert agent session failed", "channel", ch.ID, "agent", uuidToString(agent.ID), "error", err)
		return
	}
	if err := upsertChannelObserveInboxEventTx(ctx, tx, workspaceID, parseUUID(ch.ID), agent.ID,
		agentSession.ID, conversationID, channelAmbientTriggerID(trigger), cursorSeq+1, pendingToSeq); err != nil {
		slog.Warn("channel ambient delivery: upsert inbox failed", "channel", ch.ID, "agent", uuidToString(agent.ID), "error", err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		slog.Warn("channel ambient delivery: commit failed", "channel", ch.ID, "agent", uuidToString(agent.ID), "error", err)
	}
}

func buildChannelAmbientObservationPrompt(ch ChannelResponse, agent db.Agent, trigger ChannelMessageResponse) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are a member of the Multica group chat #%s. A user sent a message without @-mentioning anyone.\n", ch.Name)
	b.WriteString("You can see ONLY the current message below. Do not assume any prior channel context.\n")
	b.WriteString(channelOutputContractInstruction)
	b.WriteString("\n")
	b.WriteString(channelAmbientNoReplyInstruction)
	b.WriteString("\n")
	b.WriteString(channelAmbientGreetingReactionInstruction)
	b.WriteString("\n")
	b.WriteString("Decide whether your own role/profile makes a response useful. If it is not clearly relevant to you, finish without visible output; do not print no_reply or protocol text.\n")
	b.WriteString("If the message directly addresses your agent name, role, description, instructions, or an unmistakable task for you, treat it as directed to you: write a visible reply or acknowledgement using the requested supported delivery modality, and do not return no_reply.\n")
	b.WriteString("If the message asks a category of members to react (for example directors, reviewers, designers, backend engineers), respond only if your agent name/description/instructions match that category.\n")
	b.WriteString("If a lightweight acknowledgement is enough outside an all-hands welcome/greeting request, use a reaction when the runtime brief supports reactions and a reaction is sufficient; otherwise send a short acknowledgement.\n")
	b.WriteString(channelStickerReplyInstruction)
	b.WriteString("\n")
	b.WriteString(channelContinuationInstruction)
	if instruction := channelVoiceReplyInstruction(trigger); instruction != "" {
		b.WriteString("\n")
		b.WriteString(instruction)
	}
	b.WriteString("\nDo not @-mention anyone from this ambient observation.\n\n")
	fmt.Fprintf(&b, "Reaction target message id: %s\n", trigger.ID)
	fmt.Fprintf(&b, "Your agent name: %s\n", agentDisplayName(agent))
	if strings.TrimSpace(agent.Description) != "" {
		fmt.Fprintf(&b, "Your agent description: %s\n", strings.TrimSpace(agent.Description))
	}
	if strings.TrimSpace(agent.Instructions) != "" {
		fmt.Fprintf(&b, "Your agent instructions: %s\n", strings.TrimSpace(agent.Instructions))
	}
	b.WriteString("\nCurrent message only:\n")
	fmt.Fprintf(&b, "%s (%s): %s", trigger.AuthorName, trigger.Type, trigger.Content)
	return b.String()
}

func (h *Handler) channelConversationIDWithDB(ctx context.Context, exec db.DBTX, channelID pgtype.UUID) (pgtype.UUID, error) {
	var conversationID pgtype.UUID
	err := exec.QueryRow(ctx, `SELECT id FROM conversation WHERE channel_id = $1`, channelID).Scan(&conversationID)
	return conversationID, err
}

func (h *Handler) buildChannelThreadContinuationPrompt(ctx context.Context, ch ChannelResponse, agent db.Agent, trigger ChannelMessageResponse) string {
	limit := channelAgentDirectedContextMessageLimit
	var messages []ChannelMessageResponse
	if trigger.ThreadRootMessageID != nil {
		messages = h.channelThreadContextMessages(ctx, ch.WorkspaceID, ch.ID, *trigger.ThreadRootMessageID, limit)
	}
	messages = channelContextMessagesExcludingTrigger(messages, trigger.ID)

	var b strings.Builder
	if ch.Kind == "dm" {
		b.WriteString("You are a participant in a thread inside a Multica DM. A follow-up arrived without @-mentioning you.\n")
	} else {
		fmt.Fprintf(&b, "You are a participant in a thread inside Multica group chat #%s. A follow-up arrived without @-mentioning you.\n", ch.Name)
	}
	b.WriteString("This is participant delivery, not a must-reply directed mention: reply only when you add a concrete decision, owner, answer, or completed action; otherwise finish without visible output.\n")
	b.WriteString("Use ONLY the thread context below for this decision. Do not assume older channel context unless you explicitly fetch/search it.\n")
	b.WriteString(channelOutputContractInstruction)
	b.WriteString("\n")
	b.WriteString(channelAmbientNoReplyInstruction)
	b.WriteString("\n")
	b.WriteString(channelStickerReplyInstruction)
	b.WriteString("\n")
	b.WriteString(channelContinuationInstruction)
	if instruction := channelVoiceReplyInstruction(trigger); instruction != "" {
		b.WriteString("\n")
		b.WriteString(instruction)
	}
	b.WriteString("\nDo not @-mention anyone unless a concrete action or human escalation is required.\n\n")
	fmt.Fprintf(&b, "Reaction target message id: %s\n", trigger.ID)
	if target := h.agentMessageTargetForPrompt(ctx, ch, trigger); target != "" {
		fmt.Fprintf(&b, "Message target for chat transport: %s\n", target)
	}
	fmt.Fprintf(&b, "Your agent name: %s\n", agentDisplayName(agent))
	if strings.TrimSpace(agent.Description) != "" {
		fmt.Fprintf(&b, "Your agent description: %s\n", strings.TrimSpace(agent.Description))
	}
	if strings.TrimSpace(agent.Instructions) != "" {
		fmt.Fprintf(&b, "Your agent instructions: %s\n", strings.TrimSpace(agent.Instructions))
	}
	if len(messages) > 0 {
		b.WriteString("\nThread context:\n")
		for _, msg := range messages {
			fmt.Fprintf(&b, "%s\n", formatChannelMessageLine(msg))
		}
	}
	b.WriteString("\nCurrent follow-up:\n")
	fmt.Fprintf(&b, "%s\n", formatChannelMessageLine(trigger))
	return b.String()
}

func channelMentionPromptNeedsMemberDirectory(trigger ChannelMessageResponse) bool {
	// Human-authored prompts often need exact mention links for escalation. Agent
	// handoffs already carry structured mention parts, so omit the roster there to
	// keep repeated coordination prompts small.
	return channelMessageIsHumanAuthored(trigger.Type)
}

func (h *Handler) buildChannelMentionPrompt(ctx context.Context, ch ChannelResponse, trigger ChannelMessageResponse, facilitatorState channelFacilitatorState) string {
	actorType, actorID := channelPromptActor(trigger, pgtype.UUID{})
	return h.buildChannelMentionPromptForActor(ctx, ch, trigger, facilitatorState, actorType, actorID)
}

func (h *Handler) buildChannelMentionPromptForActor(ctx context.Context, ch ChannelResponse, trigger ChannelMessageResponse, facilitatorState channelFacilitatorState, actorType, actorID string) string {
	limit := channelContextMessageLimit
	if trigger.Type == "agent" || trigger.ThreadRootMessageID != nil {
		limit = channelAgentDirectedContextMessageLimit
	}
	var members []ChannelMemberResponse
	includeMembers := channelMentionPromptNeedsMemberDirectory(trigger)
	if includeMembers {
		members = h.channelMemberSummaries(ctx, ch.WorkspaceID, ch.ID)
	}
	messages := h.recentChannelMessages(ctx, ch.WorkspaceID, ch.ID, limit)
	if trigger.ThreadRootMessageID != nil {
		messages = h.channelThreadContextMessages(ctx, ch.WorkspaceID, ch.ID, *trigger.ThreadRootMessageID, limit)
	}
	messages = channelContextMessagesExcludingTrigger(messages, trigger.ID)

	var b strings.Builder
	fmt.Fprintf(&b, "You are participating in the Multica group chat #%s. Respond only as yourself.\n", ch.Name)
	b.WriteString("Use the bounded context below; fetch/search more only if needed to answer the current mention.\n")
	b.WriteString(channelOutputContractInstruction)
	b.WriteString("\n")
	b.WriteString(channelDirectedReplyInstruction)
	b.WriteString("\n")
	b.WriteString(channelStickerReplyInstruction)
	b.WriteString("\n")
	b.WriteString(channelContinuationInstruction)
	b.WriteString("\n")
	if instruction := channelVoiceReplyInstruction(trigger); instruction != "" {
		b.WriteString(instruction)
		b.WriteString("\n")
	}
	appendChannelFacilitatorPromptSection(&b, facilitatorState)
	// A @mention from any current agent manager is an authoritative coordination
	// directive, not a weak agent-to-agent ping.
	if trigger.Type == "agent" && trigger.AuthorID != nil {
		if h.isChannelAgentManager(ctx, parseUUID(ch.WorkspaceID), parseUUID(ch.ID), parseUUID(*trigger.AuthorID)) {
			b.WriteString("- This @mention is from a group manager coordinating this channel. Treat it as a DIRECTED request (not a weak agent-to-agent notification): do the work now and produce a concrete deliverable — make the change / run it / advance the referenced issue and update its status — then report the result. A bare acknowledgment (\"收到\"/\"我这就做\") without actually progressing the work does not satisfy this; if you are blocked, say the specific blocker and who can unblock you.\n")
		}
	}
	fmt.Fprintf(&b, "To prevent runaway loops, this channel run is limited to %d automatic agent turns; current trigger depth is %d. As you near the limit, steer the discussion toward a concrete conclusion.\n\n", channelRunTriggerLimit, trigger.TriggerDepth)
	if len(members) > 0 {
		// Give the exact mention link per member (humans included), not just a
		// name. The model reliably linkifies agents because their links recur in
		// channel history, but humans were getting bare "@name" text — which the
		// UI can't colorize or route. Handing over the verbatim link makes every
		// mention a real, colored, routable tag.
		b.WriteString("Channel members — to @-mention one, copy its mention link verbatim (never write a bare @name):\n")
		for _, member := range members {
			mentionType := member.MemberType
			if mentionType == "user" {
				mentionType = "member"
			}
			displayName := firstNonEmpty(member.DisplayName, member.Name)
			handle := firstNonEmpty(member.Name, displayName)
			fmt.Fprintf(&b, "- %s (%s, @%s): [@%s](mention://%s/%s)\n", displayName, member.MemberType, handle, handle, mentionType, member.MemberID)
		}
		b.WriteString("\n")
	}
	if len(messages) > 0 {
		if trigger.ThreadRootMessageID != nil {
			b.WriteString("Thread context (root message first, then bounded recent replies from this thread only):\n")
		} else {
			b.WriteString("Recent channel messages from this channel only (bounded window):\n")
		}
		for _, msg := range messages {
			fmt.Fprintf(&b, "%s\n", formatChannelMessageLine(msg))
		}
		b.WriteString("\n")
	}
	if trigger.ReplyToMessageID != nil {
		if trigger.ReplyTo != nil {
			b.WriteString("Direct reply target for the current message:\n")
			fmt.Fprintf(&b, "%s\n\n", formatChannelMessageReplyLine(*trigger.ReplyTo))
		} else if parent, ok := h.channelMessageByID(ctx, ch.WorkspaceID, ch.ID, *trigger.ReplyToMessageID); ok {
			b.WriteString("Direct reply target for the current message:\n")
			fmt.Fprintf(&b, "%s\n\n", formatChannelMessageLine(parent))
		}
	}
	if trigger.QuoteMessageID != nil {
		b.WriteString("Direct quote target for the current message:\n")
		if trigger.Quote != nil && trigger.Quote.Status == "active" && trigger.Quote.Snapshot != nil {
			fmt.Fprintf(&b, "%s\n\n", formatChannelMessageQuoteSnapshotLine(*trigger.Quote.Snapshot))
		} else if quoted, ok := h.channelMessageByID(ctx, ch.WorkspaceID, ch.ID, *trigger.QuoteMessageID); ok {
			fmt.Fprintf(&b, "%s\n\n", formatChannelMessageLine(quoted))
		} else if trigger.Quote != nil && strings.TrimSpace(trigger.Quote.Status) != "" {
			fmt.Fprintf(&b, "[%s] unavailable (%s)\n\n", *trigger.QuoteMessageID, trigger.Quote.Status)
		} else {
			fmt.Fprintf(&b, "[%s] unavailable\n\n", *trigger.QuoteMessageID)
		}
	}
	if trigger.ReplyToMessageID != nil || trigger.QuoteMessageID != nil {
		b.WriteString("When answering, treat the current message text as the user's question/request and the direct reply/quote target as the referenced message content. Do not confuse the two.\n\n")
	}
	if strings.TrimSpace(trigger.ID) != "" {
		fmt.Fprintf(&b, "Current message id: %s\n", trigger.ID)
	}
	if target := h.agentMessageTargetForPrompt(ctx, ch, trigger); target != "" {
		fmt.Fprintf(&b, "Message target for chat transport: %s\n", target)
	}
	b.WriteString("Current message to respond to:\n")
	fmt.Fprintf(&b, "%s (%s): %s", trigger.AuthorName, trigger.Type, trigger.Content)
	references := h.hydrateReferencedEntities(ctx, ch.WorkspaceID, actorType, actorID, referencedEntitySource{
		Content: trigger.Content,
		Parts:   trigger.Parts,
	})
	promptcontext.AppendReferencedEntitySnapshots(
		&b,
		references.Snapshots,
		references.OmittedCount,
	)
	return b.String()
}

func (h *Handler) agentMessageTargetForPrompt(ctx context.Context, ch ChannelResponse, trigger ChannelMessageResponse) string {
	kind := ch.Kind
	if kind == "" {
		kind = "group"
	}
	rootID := ""
	if trigger.ThreadRootMessageID != nil {
		rootID = strings.TrimSpace(*trigger.ThreadRootMessageID)
	}
	switch kind {
	case "group":
		if strings.TrimSpace(ch.Name) == "" {
			return ""
		}
		target := "#" + strings.TrimSpace(ch.Name)
		if rootID != "" {
			target += ":" + rootID[:min(8, len(rootID))]
		}
		return target
	case "dm":
		handle := h.dmUserHandleForAgentTarget(ctx, ch)
		if handle == "" {
			return ""
		}
		target := "dm:@" + handle
		if rootID != "" {
			target += ":" + rootID[:min(8, len(rootID))]
		}
		return target
	default:
		return ""
	}
}

// agentMessageThreadTargetForPrompt appends the visible thread root for a
// worker reply so the response lands back on the originating thread rather than
// the DM mainline.
func (h *Handler) agentMessageThreadTargetForPrompt(ctx context.Context, ch ChannelResponse, trigger ChannelMessageResponse) string {
	target := h.agentMessageTargetForPrompt(ctx, ch, trigger)
	if target == "" {
		return ""
	}
	threadRootID := strings.TrimSpace(trigger.ID)
	if threadRootID == "" {
		return target
	}
	return target + ":" + threadRootID[:min(8, len(threadRootID))]
}

func (h *Handler) dmUserHandleForAgentTarget(ctx context.Context, ch ChannelResponse) string {
	var handle string
	err := h.DB.QueryRow(ctx, `
		SELECT u.name
		FROM channel_member cm
		JOIN "user" u ON u.id = cm.member_id
		WHERE cm.channel_id = $1 AND cm.workspace_id = $2 AND cm.member_type = 'user'
		ORDER BY cm.created_at ASC
		LIMIT 1`, parseUUID(ch.ID), parseUUID(ch.WorkspaceID)).Scan(&handle)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(handle)
}

func (h *Handler) channelMessageByID(ctx context.Context, workspaceID, channelID, messageID string) (ChannelMessageResponse, bool) {
	if strings.TrimSpace(messageID) == "" {
		return ChannelMessageResponse{}, false
	}
	row := h.DB.QueryRow(ctx, `
			SELECT id, channel_id, workspace_id, author_type, author_id, author_name, content, parts, source, external_message_id, client_message_id, reply_to_message_id, quote_message_id, quote_snapshot, thread_root_message_id, thread_id, trigger_depth, seq, created_at, edited_at, deleted_at
		FROM channel_message
		WHERE id = $1 AND channel_id = $2 AND workspace_id = $3`, parseUUID(messageID), parseUUID(channelID), parseUUID(workspaceID))
	msg, err := scanChannelMessage(row)
	if err != nil {
		return ChannelMessageResponse{}, false
	}
	return msg, true
}

func (h *Handler) channelMemberSummaries(ctx context.Context, workspaceID, channelID string) []ChannelMemberResponse {
	rows, err := h.DB.Query(ctx, `
		SELECT cm.member_type, cm.member_id,
		       COALESCE(u.name, a.name, ''),
		       COALESCE(NULLIF(u.display_name, ''), u.name, u.email, NULLIF(a.display_name, ''), a.name, ''),
		       CASE WHEN cm.member_type = 'user' THEN u.avatar_url ELSE a.avatar_url END,
		       cm.created_at,
		       cm.role
		FROM channel_member cm
		LEFT JOIN "user" u ON cm.member_type = 'user' AND u.id = cm.member_id
		LEFT JOIN agent a ON cm.member_type = 'agent' AND a.id = cm.member_id
		WHERE cm.channel_id = $1 AND cm.workspace_id = $2
		ORDER BY
		  CASE cm.role
		    WHEN 'owner' THEN 0
		    WHEN 'manager' THEN 1
		    ELSE 2
		  END,
		  cm.created_at ASC,
		  cm.member_type ASC,
		  cm.member_id ASC`, parseUUID(channelID), parseUUID(workspaceID))
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []ChannelMemberResponse
	for rows.Next() {
		var typ, name, displayName, role string
		var id pgtype.UUID
		var avatarURL pgtype.Text
		var createdAt pgtype.Timestamptz
		if err := rows.Scan(&typ, &id, &name, &displayName, &avatarURL, &createdAt, &role); err != nil {
			continue
		}
		if role == "" {
			role = "member"
		}
		out = append(out, ChannelMemberResponse{MemberType: typ, MemberID: uuidToString(id), Name: name, DisplayName: firstNonEmpty(displayName, name), AvatarURL: textToPtr(avatarURL), Role: role, CreatedAt: timestampToString(createdAt)})
	}
	return out
}

func (h *Handler) recentChannelMessages(ctx context.Context, workspaceID, channelID string, limit int) []ChannelMessageResponse {
	messages, _ := h.recentChannelMessagesWithError(ctx, workspaceID, channelID, limit)
	return messages
}

func (h *Handler) recentChannelMessagesWithError(ctx context.Context, workspaceID, channelID string, limit int) ([]ChannelMessageResponse, error) {
	rows, err := h.DB.Query(ctx, `
			SELECT id, channel_id, workspace_id, author_type, author_id, author_name, content, parts, source, external_message_id, client_message_id, reply_to_message_id, quote_message_id, quote_snapshot, thread_root_message_id, thread_id, trigger_depth, seq, created_at, edited_at, deleted_at
		FROM (
			SELECT id, channel_id, workspace_id, author_type, author_id, author_name, content, parts, source, external_message_id, client_message_id, reply_to_message_id, quote_message_id, quote_snapshot, thread_root_message_id, thread_id, trigger_depth, seq, created_at, edited_at, deleted_at
			FROM channel_message
			WHERE channel_id = $1
			  AND workspace_id = $2
			  AND thread_root_message_id IS NULL
			  AND deleted_at IS NULL
			ORDER BY seq DESC
			LIMIT $3
		) recent
		ORDER BY seq ASC`, parseUUID(channelID), parseUUID(workspaceID), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ChannelMessageResponse
	for rows.Next() {
		msg, err := scanChannelMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, msg)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (h *Handler) channelThreadContextMessages(ctx context.Context, workspaceID, channelID, rootMessageID string, limit int) []ChannelMessageResponse {
	if limit < 2 {
		limit = 2
	}
	rows, err := h.DB.Query(ctx, `
		WITH replies AS (
			SELECT id, channel_id, workspace_id, author_type, author_id, author_name, content, parts, source, external_message_id, client_message_id, reply_to_message_id, quote_message_id, quote_snapshot, thread_root_message_id, thread_id, trigger_depth, seq, created_at, edited_at, deleted_at
			FROM channel_message
			WHERE channel_id = $1
			  AND workspace_id = $2
			  AND thread_root_message_id = $3
			  AND deleted_at IS NULL
			ORDER BY seq DESC
			LIMIT $4
		)
			SELECT id, channel_id, workspace_id, author_type, author_id, author_name, content, parts, source, external_message_id, client_message_id, reply_to_message_id, quote_message_id, quote_snapshot, thread_root_message_id, thread_id, trigger_depth, seq, created_at, edited_at, deleted_at
		FROM (
			SELECT id, channel_id, workspace_id, author_type, author_id, author_name, content, parts, source, external_message_id, client_message_id, reply_to_message_id, quote_message_id, quote_snapshot, thread_root_message_id, thread_id, trigger_depth, seq, created_at, edited_at, deleted_at
			FROM channel_message
			WHERE id = $3
			  AND channel_id = $1
			  AND workspace_id = $2
			  AND author_type <> 'system'
			  AND deleted_at IS NULL
			UNION ALL
			SELECT id, channel_id, workspace_id, author_type, author_id, author_name, content, parts, source, external_message_id, client_message_id, reply_to_message_id, quote_message_id, quote_snapshot, thread_root_message_id, thread_id, trigger_depth, seq, created_at, edited_at, deleted_at
			FROM replies
		) thread_context
		ORDER BY seq ASC`, parseUUID(channelID), parseUUID(workspaceID), parseUUID(rootMessageID), limit-1)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []ChannelMessageResponse
	for rows.Next() {
		msg, err := scanChannelMessage(rows)
		if err != nil {
			continue
		}
		out = append(out, msg)
	}
	return out
}

func (h *Handler) createChannelAgentPromptMessage(ctx context.Context, chatSessionID pgtype.UUID, prompt string, trigger ChannelMessageResponse) (db.ChatMessage, error) {
	return h.createChannelAgentPromptMessageWithDB(ctx, h.DB, chatSessionID, prompt, trigger)
}

func (h *Handler) createChannelAgentPromptMessageWithDB(ctx context.Context, exec db.DBTX, chatSessionID pgtype.UUID, prompt string, trigger ChannelMessageResponse) (db.ChatMessage, error) {
	threadID := trigger.ThreadID
	if threadID == nil || strings.TrimSpace(*threadID) == "" {
		fresh := uuid.NewString()
		threadID = &fresh
	}
	var threadRootMessageID any
	if trigger.ThreadRootMessageID != nil {
		threadRootMessageID = parseUUID(*trigger.ThreadRootMessageID)
	}
	row := exec.QueryRow(ctx, `
		INSERT INTO chat_message (chat_session_id, role, content, thread_id, channel_thread_root_message_id, trigger_depth)
		VALUES ($1, 'user', $2, $3, $4, $5)
		RETURNING id, chat_session_id, role, content, task_id, created_at, failure_reason, elapsed_ms, thread_id, trigger_depth, parts`,
		chatSessionID, prompt, threadID, threadRootMessageID, trigger.TriggerDepth)
	var msg db.ChatMessage
	err := row.Scan(&msg.ID, &msg.ChatSessionID, &msg.Role, &msg.Content, &msg.TaskID, &msg.CreatedAt, &msg.FailureReason, &msg.ElapsedMs, &msg.ThreadID, &msg.TriggerDepth, &msg.Parts)
	return msg, err
}

func (h *Handler) ensureChannelAgentSession(ctx context.Context, ch ChannelResponse, agentID pgtype.UUID, creatorID pgtype.UUID) (db.ChatSession, error) {
	return h.ensureChannelAgentSessionWithDB(ctx, h.Queries, h.DB, ch, agentID, creatorID)
}

func (h *Handler) ensureChannelAgentSessionWithDB(ctx context.Context, q *db.Queries, exec db.DBTX, ch ChannelResponse, agentID pgtype.UUID, creatorID pgtype.UUID) (db.ChatSession, error) {
	var sessionID pgtype.UUID
	err := exec.QueryRow(ctx, `SELECT chat_session_id FROM channel_agent_session WHERE channel_id = $1 AND agent_id = $2`, parseUUID(ch.ID), agentID).Scan(&sessionID)
	if err == nil {
		return q.GetChatSession(ctx, sessionID)
	}
	if !errorsIsNoRows(err) {
		return db.ChatSession{}, err
	}
	session, err := q.CreateChatSession(ctx, db.CreateChatSessionParams{
		WorkspaceID: parseUUID(ch.WorkspaceID),
		AgentID:     agentID,
		CreatorID:   creatorID,
		Title:       "#" + ch.Name,
	})
	if err != nil {
		return db.ChatSession{}, err
	}
	tag, err := exec.Exec(ctx, `
		INSERT INTO channel_agent_session (channel_id, agent_id, chat_session_id)
		VALUES ($1, $2, $3)
		ON CONFLICT (channel_id, agent_id) DO NOTHING`, parseUUID(ch.ID), agentID, session.ID)
	if err != nil {
		return db.ChatSession{}, err
	}
	if tag.RowsAffected() == 0 {
		// A concurrent transaction won the unique (channel_id, agent_id)
		// insert. Do not return the orphan session we just created: callers
		// would attach prompts to a session that is not linked to the channel.
		if err := q.DeleteChatSession(ctx, db.DeleteChatSessionParams{
			ID:          session.ID,
			WorkspaceID: parseUUID(ch.WorkspaceID),
		}); err != nil {
			return db.ChatSession{}, fmt.Errorf("delete losing channel session: %w", err)
		}
		if err := exec.QueryRow(ctx, `
			SELECT chat_session_id
			FROM channel_agent_session
			WHERE channel_id = $1 AND agent_id = $2
		`, parseUUID(ch.ID), agentID).Scan(&sessionID); err != nil {
			return db.ChatSession{}, fmt.Errorf("load winning channel session: %w", err)
		}
		return q.GetChatSession(ctx, sessionID)
	}
	return session, nil
}

func (h *Handler) handleChannelChatStopped(e events.Event) {
	payload, _ := e.Payload.(map[string]any)
	chatSessionID, _ := payload["chat_session_id"].(string)
	if chatSessionID == "" {
		return
	}
	channelID, workspaceID, agentID, ok := h.channelAgentForChatSession(context.Background(), chatSessionID)
	if !ok {
		return
	}
	h.publishChannelToMembers(context.Background(), protocol.EventChannelTyping, uuidToString(workspaceID), "agent", uuidToString(agentID), channelID, protocol.ChannelTypingPayload{
		ChannelID: uuidToString(channelID),
		ActorType: "agent",
		ActorID:   uuidToString(agentID),
		ActorName: h.agentName(context.Background(), agentID),
		IsTyping:  false,
	})
}

// publishChannelTypingStopForInboxEvent clears the channel typing indicator for
// channel-only inbox completions/failures that never emit ChatDone.
func (h *Handler) publishChannelTypingStopForInboxEvent(ctx context.Context, event db.AgentInboxEvent) {
	if !event.ChannelID.Valid || !event.AgentID.Valid || !event.WorkspaceID.Valid {
		return
	}
	h.publishChannelToMembers(ctx, protocol.EventChannelTyping, uuidToString(event.WorkspaceID), "agent", uuidToString(event.AgentID), event.ChannelID, protocol.ChannelTypingPayload{
		ChannelID: uuidToString(event.ChannelID),
		ActorType: "agent",
		ActorID:   uuidToString(event.AgentID),
		ActorName: h.agentName(ctx, event.AgentID),
		IsTyping:  false,
	})
}

func (h *Handler) handleChannelChatDone(e events.Event) {
	payload, ok := e.Payload.(protocol.ChatDonePayload)
	if !ok || payload.ChatSessionID == "" {
		return
	}
	ctx := context.Background()
	outputType, err := protocol.NormalizeChatOutputType(payload.Type, strings.TrimSpace(payload.Content) != "" || len(payload.Parts) > 0, payload.Reaction != nil)
	if err != nil {
		slog.Warn("channel bridge: invalid chat output type", "chat_session_id", payload.ChatSessionID, "error", err)
		return
	}
	channelID, workspaceID, agentID, ok := h.channelAgentForChatSession(ctx, payload.ChatSessionID)
	if !ok {
		return
	}
	agentName := h.agentName(ctx, agentID)
	h.publishChannelToMembers(ctx, protocol.EventChannelTyping, uuidToString(workspaceID), "agent", uuidToString(agentID), channelID, protocol.ChannelTypingPayload{
		ChannelID: uuidToString(channelID),
		ActorType: "agent",
		ActorID:   uuidToString(agentID),
		ActorName: agentName,
		IsTyping:  false,
	})
	var taskID pgtype.UUID
	if strings.TrimSpace(payload.TaskID) != "" {
		taskID = parseUUID(payload.TaskID)
	}
	threadID, threadRootMessageID, triggerDepth := h.channelThreadForChatTask(ctx, parseUUID(payload.ChatSessionID), taskID)
	reactionTargetID := h.channelReactionTargetFromPrompt(ctx, parseUUID(payload.ChatSessionID), taskID)
	if !reactionTargetID.Valid {
		reactionTargetID = threadRootMessageID
	}
	if outputType == protocol.ChatOutputKindReaction {
		h.handleChannelReactionPayload(ctx, taskID, channelID, workspaceID, agentID, reactionTargetID, payload.Reaction)
		return
	}
	if outputType == protocol.ChatOutputKindNoReply {
		slog.Debug("channel bridge: suppressed channel output", "chat_session_id", payload.ChatSessionID, "output_suppressed_reason", payload.OutputSuppressedReason)
		return
	}
	if outputType != protocol.ChatOutputKindMessage {
		return
	}
	content, parts, err := messageparts.Normalize(payload.Content, payload.Parts)
	if err != nil {
		slog.Warn("channel bridge: invalid message parts", "chat_session_id", payload.ChatSessionID, "error", err)
		return
	}
	if strings.TrimSpace(content) == "" {
		return
	}
	initiatorID := h.channelInitiatorForChatSession(ctx, parseUUID(payload.ChatSessionID))
	h.handleResolvedChannelChatDone(ctx, taskID, chatOutputOrigin{channelID: channelID, workspaceID: workspaceID, agentID: agentID}, payload, content, parts, initiatorID, threadRootMessageID, threadID, triggerDepth)
}

func (h *Handler) channelReactionTargetFromPrompt(ctx context.Context, chatSessionID, taskID pgtype.UUID) pgtype.UUID {
	if !taskID.Valid {
		return pgtype.UUID{}
	}
	var content string
	if err := h.DB.QueryRow(ctx, `
		SELECT content FROM chat_message
		WHERE chat_session_id = $1 AND task_id = $2 AND role = 'user'
		ORDER BY created_at DESC
		LIMIT 1`, chatSessionID, taskID).Scan(&content); err != nil {
		return pgtype.UUID{}
	}
	for _, line := range strings.Split(content, "\n") {
		if target, ok := strings.CutPrefix(strings.TrimSpace(line), "Reaction target message id: "); ok {
			return parseUUID(strings.TrimSpace(target))
		}
	}
	return pgtype.UUID{}
}

func (h *Handler) handleChannelReactionPayload(ctx context.Context, taskID, channelID, workspaceID, agentID, triggerMessageID pgtype.UUID, reaction *protocol.ChatReactionPayload) bool {
	if reaction == nil {
		return true
	}
	messageID := triggerMessageID
	messageIDText := strings.TrimSpace(reaction.MessageID)
	if messageIDText != "" && !strings.EqualFold(messageIDText, "CURRENT_MESSAGE") {
		parsed, err := util.ParseUUID(messageIDText)
		if err != nil {
			return true
		}
		if !h.channelMessageBelongsToChannel(ctx, channelID, workspaceID, parsed) {
			return true
		}
		messageID = parsed
	}
	return h.insertChannelReactionCommand(ctx, taskID, channelID, workspaceID, agentID, messageID, strings.TrimSpace(reaction.Emoji))
}

func (h *Handler) insertChannelReactionCommand(ctx context.Context, taskID, channelID, workspaceID, agentID, messageID pgtype.UUID, emoji string) bool {
	if !messageID.Valid || strings.TrimSpace(emoji) == "" {
		return true
	}
	if h == nil || h.TxStarter == nil {
		return true
	}
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		slog.Warn("channel reaction command begin failed", "channel", uuidToString(channelID), "agent", uuidToString(agentID), "error", err)
		return true
	}
	defer tx.Rollback(ctx)
	reaction, found, created, err := h.insertAgentChannelReaction(ctx, tx, channelID, workspaceID, agentID, messageID, emoji)
	if err != nil {
		slog.Warn("channel reaction command failed", "channel", uuidToString(channelID), "agent", uuidToString(agentID), "error", err)
		return true
	}
	if !found {
		return true
	}
	if created && taskID.Valid {
		if err := h.recordVisibleTaskActionTx(ctx, tx, taskID, service.DAGCloseReaction, parseUUID(reaction.ID), reaction.Emoji, channelID); err != nil {
			slog.Warn("channel reaction universal boundary failed", "channel", uuidToString(channelID), "agent", uuidToString(agentID), "error", err)
			return true
		}
	}
	if err := tx.Commit(ctx); err != nil {
		slog.Warn("channel reaction command commit failed", "channel", uuidToString(channelID), "agent", uuidToString(agentID), "error", err)
		return true
	}
	h.publishChannelToMembers(ctx, protocol.EventChannelReactionAdded, uuidToString(workspaceID), "agent", uuidToString(agentID), channelID, map[string]any{"reaction": reaction, "channel_id": uuidToString(channelID), "message_id": uuidToString(messageID)})
	return true
}

func (h *Handler) insertAgentChannelReaction(ctx context.Context, exec dbExecutor, channelID, workspaceID, agentID, messageID pgtype.UUID, emoji string) (ChannelReactionResponse, bool, bool, error) {
	var id, returnedMessageID, actorID pgtype.UUID
	var createdAt pgtype.Timestamptz
	var created bool
	err := exec.QueryRow(ctx, `
		INSERT INTO channel_message_reaction (channel_message_id, workspace_id, actor_type, actor_id, emoji)
		SELECT cm.id, cm.workspace_id, 'agent', $3, $4
		FROM channel_message cm
		WHERE cm.id = $1
		  AND cm.channel_id = $2
		  AND cm.workspace_id = $5
		  AND cm.author_type <> 'system'
		  AND cm.deleted_at IS NULL
		ON CONFLICT (channel_message_id, actor_type, actor_id, emoji) DO UPDATE SET created_at = channel_message_reaction.created_at
		RETURNING id, channel_message_id, actor_id, created_at, (xmax = 0)`, messageID, channelID, agentID, emoji, workspaceID).Scan(&id, &returnedMessageID, &actorID, &createdAt, &created)
	if errors.Is(err, pgx.ErrNoRows) {
		return ChannelReactionResponse{}, false, false, nil
	}
	if err != nil {
		return ChannelReactionResponse{}, false, false, err
	}
	reaction := ChannelReactionResponse{
		ID:        uuidToString(id),
		ChannelID: uuidToString(channelID),
		MessageID: uuidToString(returnedMessageID),
		ActorType: "agent",
		ActorID:   uuidToString(actorID),
		Emoji:     emoji,
		CreatedAt: timestampToString(createdAt),
	}
	return reaction, true, created, nil
}

func (h *Handler) channelMessageBelongsToChannel(ctx context.Context, channelID, workspaceID, messageID pgtype.UUID) bool {
	var exists bool
	if err := h.DB.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM channel_message
			WHERE id = $1 AND channel_id = $2 AND workspace_id = $3 AND author_type <> 'system'
			  AND deleted_at IS NULL
		)`, messageID, channelID, workspaceID).Scan(&exists); err != nil {
		return false
	}
	return exists
}

func (h *Handler) channelAgentForChatSession(ctx context.Context, chatSessionID string) (pgtype.UUID, pgtype.UUID, pgtype.UUID, bool) {
	var channelID, workspaceID, agentID pgtype.UUID
	err := h.DB.QueryRow(ctx, `
		SELECT cas.channel_id, ch.workspace_id, cas.agent_id
		FROM channel_agent_session cas
		JOIN channel ch ON ch.id = cas.channel_id
		WHERE cas.chat_session_id = $1`, parseUUID(chatSessionID)).Scan(&channelID, &workspaceID, &agentID)
	if err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, pgtype.UUID{}, false
	}
	return channelID, workspaceID, agentID, true
}

func (h *Handler) channelThreadForChatTask(ctx context.Context, chatSessionID, taskID pgtype.UUID) (*string, pgtype.UUID, int) {
	if taskID.Valid {
		if threadID, threadRootMessageID, depth, ok := h.channelThreadFromQuery(ctx, `
			SELECT thread_id, channel_thread_root_message_id, trigger_depth
			FROM chat_message
			WHERE chat_session_id = $1 AND task_id = $2 AND role = 'user'
			ORDER BY created_at DESC
			LIMIT 1`, chatSessionID, taskID); ok {
			return threadID, threadRootMessageID, depth
		}
	}
	if threadID, threadRootMessageID, depth, ok := h.channelThreadFromQuery(ctx, `
		SELECT thread_id, channel_thread_root_message_id, trigger_depth
		FROM chat_message
		WHERE chat_session_id = $1 AND role = 'user'
		ORDER BY created_at DESC
		LIMIT 1`, chatSessionID); ok {
		return threadID, threadRootMessageID, depth
	}
	fresh := uuid.NewString()
	return &fresh, pgtype.UUID{}, 0
}

func (h *Handler) channelThreadFromQuery(ctx context.Context, query string, args ...any) (*string, pgtype.UUID, int, bool) {
	var thread pgtype.Text
	var threadRootMessageID pgtype.UUID
	var depth int
	err := h.DB.QueryRow(ctx, query, args...).Scan(&thread, &threadRootMessageID, &depth)
	if err != nil || !thread.Valid || strings.TrimSpace(thread.String) == "" {
		return nil, pgtype.UUID{}, 0, false
	}
	return &thread.String, threadRootMessageID, depth, true
}

func (h *Handler) channelInitiatorForChatSession(ctx context.Context, chatSessionID pgtype.UUID) pgtype.UUID {
	if !chatSessionID.Valid {
		return pgtype.UUID{}
	}
	var initiator pgtype.UUID
	err := h.DB.QueryRow(ctx, `
		SELECT initiator_user_id
		FROM agent_inbox_event
		WHERE chat_session_id = $1 AND initiator_user_id IS NOT NULL
		ORDER BY created_at DESC
		LIMIT 1`, chatSessionID).Scan(&initiator)
	if err == nil && initiator.Valid {
		return initiator
	}
	var creator pgtype.UUID
	if err := h.DB.QueryRow(ctx, `SELECT creator_id FROM chat_session WHERE id = $1`, chatSessionID).Scan(&creator); err == nil {
		return creator
	}
	return pgtype.UUID{}
}

// channelInitiatorForTask prefers the wake's own initiator_user_id so
// channel-bound tasks without chat_session_id still resolve a human initiator
// for DM create / provenance (LRM-1079 / LRM-1080).
func (h *Handler) channelInitiatorForTask(ctx context.Context, task db.AgentInboxEvent) pgtype.UUID {
	if task.InitiatorUserID.Valid {
		return task.InitiatorUserID
	}
	return h.channelInitiatorForChatSession(ctx, task.ChatSessionID)
}

type channelMessageInsertInput struct {
	ChannelID           pgtype.UUID
	WorkspaceID         pgtype.UUID
	AuthorID            pgtype.UUID
	AuthorName          string
	Content             string
	Parts               []protocol.MessagePart
	ReplyToMessageID    pgtype.UUID
	QuoteMessageID      pgtype.UUID
	QuoteSnapshot       []byte
	ThreadRootMessageID pgtype.UUID
	ThreadID            *string
	TriggerDepth        int
	ClientMessageID     *string
	// KindHint carries an optional structured/system kind into persistence (LRM-1529).
	KindHint channelMessageKindHint
}

type channelMessageCreateResult struct {
	Message ChannelMessageResponse
	Created bool
}

func normalizeChannelClientMessageID(w http.ResponseWriter, raw *string) (*string, bool) {
	if raw == nil {
		return nil, true
	}
	value := strings.TrimSpace(*raw)
	if value == "" {
		return nil, true
	}
	if len([]rune(value)) > channelClientMessageIDMaxLen {
		writeError(w, http.StatusBadRequest, "client_message_id is too long")
		return nil, false
	}
	return &value, true
}

// attachmentIDsFromParts collects attachment_id values from file and recorded
// voice parts in order. This is the sole reference source for
// channel/DM/thread sends.
func attachmentIDsFromParts(parts []protocol.MessagePart) []string {
	var ids []string
	for _, p := range parts {
		if (p.Type == protocol.MessagePartTypeAttachment || p.Type == protocol.MessagePartTypeVoice) && p.AttachmentID != "" {
			ids = append(ids, p.AttachmentID)
		}
	}
	return ids
}

// channelPartsAllowEmptyContent reports whether parts carry their own visible
// payload, including a recorded voice that is waiting for server-side ASR.
func channelPartsAllowEmptyContent(parts []protocol.MessagePart) bool {
	for _, p := range parts {
		switch p.Type {
		case protocol.MessagePartTypeSticker, protocol.MessagePartTypeAttachment, protocol.MessagePartTypeChoice, protocol.MessagePartTypeChoiceReply:
			return true
		case protocol.MessagePartTypeVoice:
			if p.TranscriptionStatus == protocol.VoiceTranscriptionPending && p.AttachmentID != "" {
				return true
			}
		}
	}
	return false
}

func channelMessageHasVoicePart(parts []protocol.MessagePart) bool {
	for _, part := range parts {
		if part.Type == protocol.MessagePartTypeVoice {
			return true
		}
	}
	return false
}

func channelVoiceReplyInstruction(trigger ChannelMessageResponse) string {
	if !channelMessageIsHumanAuthored(trigger.Type) {
		return ""
	}
	if channelMessageHasVoicePart(trigger.Parts) {
		return channelVoiceInputReplyInstruction
	}
	return channelTypedVoiceReplyInstruction
}

func (h *Handler) createUserChannelMessageWithIdempotency(ctx context.Context, in channelMessageInsertInput, attachmentIDs []pgtype.UUID) (channelMessageCreateResult, error) {
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return channelMessageCreateResult{}, err
	}
	result, err := h.createUserChannelMessageTx(ctx, tx, in, attachmentIDs)
	if err != nil {
		_ = tx.Rollback(ctx)
		if in.ClientMessageID != nil && isUniqueViolation(err) {
			return h.resolveDuplicateUserChannelMessage(ctx, in, attachmentIDs)
		}
		return channelMessageCreateResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return channelMessageCreateResult{}, err
	}
	return result, nil
}

// createUserChannelMessageTx performs every canonical message write but does
// not commit. Its caller owns rollback/commit and may append recipient delivery
// rows and mixed-run obligations to the same transaction.
func (h *Handler) createUserChannelMessageTx(ctx context.Context, tx pgx.Tx, in channelMessageInsertInput, attachmentIDs []pgtype.UUID) (channelMessageCreateResult, error) {
	inserted, err := insertChannelMessageWithPartsExec(ctx, tx, in.ChannelID, in.WorkspaceID, "user", in.AuthorID, in.AuthorName, in.Content, in.Parts, "multica", nil, in.ClientMessageID, in.ReplyToMessageID, in.QuoteMessageID, in.QuoteSnapshot, in.ThreadRootMessageID, in.ThreadID, in.TriggerDepth, in.KindHint)
	if err != nil {
		return channelMessageCreateResult{}, err
	}
	msg := inserted.Message
	if len(attachmentIDs) > 0 {
		qtx := h.Queries.WithTx(tx)
		if err := linkOwnedAttachmentsToChannelMessage(ctx, qtx, parseUUID(msg.ID), in.WorkspaceID, "member", in.AuthorID, attachmentIDs); err != nil {
			return channelMessageCreateResult{}, err
		}
	}
	if voiceAttachmentID, ok := pendingChannelVoiceAttachmentID(in.Parts); ok {
		tag, err := tx.Exec(ctx, `
			INSERT INTO channel_voice_transcription (
			  message_id, workspace_id, channel_id, attachment_id
			)
			SELECT $1, $2, $3, attachment.id
			FROM attachment
			JOIN channel_message_attachment reference
			  ON reference.attachment_id = attachment.id
			WHERE attachment.id = $4
			  AND attachment.workspace_id = $2
			  AND reference.workspace_id = $2
			  AND reference.channel_message_id = $1`,
			parseUUID(msg.ID), in.WorkspaceID, in.ChannelID, parseUUID(voiceAttachmentID))
		if err != nil {
			return channelMessageCreateResult{}, fmt.Errorf("enqueue channel voice transcription: %w", err)
		}
		if tag.RowsAffected() != 1 {
			return channelMessageCreateResult{}, errors.New("recorded voice attachment was not bound to the created message")
		}
	}
	return channelMessageCreateResult{Message: msg, Created: true}, nil
}

func (h *Handler) resolveDuplicateUserChannelMessage(ctx context.Context, in channelMessageInsertInput, attachmentIDs []pgtype.UUID) (channelMessageCreateResult, error) {
	existing, found, err := h.findUserChannelMessageByClientID(ctx, in.WorkspaceID, in.ChannelID, in.AuthorID, *in.ClientMessageID)
	if err != nil {
		return channelMessageCreateResult{}, err
	}
	if !found {
		return channelMessageCreateResult{}, errChannelClientMessageConflict
	}
	ok, err := h.matchesChannelMessageIdempotencyPayload(ctx, existing, in, attachmentIDs)
	if err != nil {
		return channelMessageCreateResult{}, err
	}
	if !ok {
		return channelMessageCreateResult{}, errChannelClientMessageConflict
	}
	return channelMessageCreateResult{Message: existing, Created: false}, nil
}

func (h *Handler) findUserChannelMessageByClientID(ctx context.Context, workspaceID, channelID, authorID pgtype.UUID, clientMessageID string) (ChannelMessageResponse, bool, error) {
	row := h.DB.QueryRow(ctx, `
		SELECT id, channel_id, workspace_id, author_type, author_id, author_name, content, parts, source, external_message_id, client_message_id, reply_to_message_id, quote_message_id, quote_snapshot, thread_root_message_id, thread_id, trigger_depth, seq, created_at, edited_at, deleted_at
		FROM channel_message
		WHERE workspace_id = $1 AND channel_id = $2 AND author_type = 'user' AND author_id = $3 AND client_message_id = $4`,
		workspaceID, channelID, authorID, clientMessageID)
	msg, err := scanChannelMessage(row)
	if err != nil {
		if errorsIsNoRows(err) {
			return ChannelMessageResponse{}, false, nil
		}
		return ChannelMessageResponse{}, false, err
	}
	return msg, true, nil
}

// messagePartsSemanticallyEqual compares two message part slices as JSON,
// ignoring object-key ordering. This is required because PostgreSQL jsonb
// round-trips object keys in canonical length-then-byte order while Go's
// json.Marshal sorts keys lexicographically, so structured parts carrying a
// Params/EventParams map never byte-match across a store-and-reload cycle.
func messagePartsSemanticallyEqual(a, b []protocol.MessagePart) bool {
	ja, errA := json.Marshal(a)
	jb, errB := json.Marshal(b)
	if errA != nil || errB != nil {
		return string(messageparts.MustJSON(a)) == string(messageparts.MustJSON(b))
	}
	var va, vb any
	if json.Unmarshal(ja, &va) != nil || json.Unmarshal(jb, &vb) != nil {
		return false
	}
	return reflect.DeepEqual(va, vb)
}

func (h *Handler) matchesChannelMessageIdempotencyPayload(ctx context.Context, existing ChannelMessageResponse, in channelMessageInsertInput, attachmentIDs []pgtype.UUID) (bool, error) {
	if existing.Content != in.Content {
		return false, nil
	}
	if !messagePartsSemanticallyEqual(existing.Parts, in.Parts) {
		return false, nil
	}
	if !sameNullableUUID(existing.ReplyToMessageID, in.ReplyToMessageID) || !sameNullableUUID(existing.QuoteMessageID, in.QuoteMessageID) || !sameNullableUUID(existing.ThreadRootMessageID, in.ThreadRootMessageID) {
		return false, nil
	}
	if !sameQuoteSelectedText(existing.quoteSnapshotRaw, in.QuoteSnapshot) {
		return false, nil
	}
	expectedAttachments := channelAttachmentIDSet(attachmentIDs)
	existingAttachments, err := h.channelMessageAttachmentIDSet(ctx, in.WorkspaceID, parseUUID(existing.ID))
	if err != nil {
		return false, err
	}
	return sameStringSet(existingAttachments, expectedAttachments), nil
}

// sameQuoteSelectedText deliberately compares only immutable request data.
// Source content and author fields in the snapshot may change independently.
func sameQuoteSelectedText(a, b []byte) bool {
	var left, right ChannelMessageQuoteSnapshot
	if len(a) > 0 && json.Unmarshal(a, &left) != nil {
		return false
	}
	if len(b) > 0 && json.Unmarshal(b, &right) != nil {
		return false
	}
	if left.SelectedText == nil || right.SelectedText == nil {
		return left.SelectedText == nil && right.SelectedText == nil
	}
	return *left.SelectedText == *right.SelectedText
}

func (h *Handler) channelMessageAttachmentIDSet(ctx context.Context, workspaceID, messageID pgtype.UUID) (map[string]struct{}, error) {
	rows, err := h.DB.Query(ctx, `
		SELECT attachment_id
		FROM channel_message_attachment
		WHERE channel_message_id = $1 AND workspace_id = $2`, messageID, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]struct{}{}
	for rows.Next() {
		var id pgtype.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[uuidToString(id)] = struct{}{}
	}
	return out, rows.Err()
}

func linkOwnedAttachmentsToChannelMessage(
	ctx context.Context,
	queries *db.Queries,
	messageID pgtype.UUID,
	workspaceID pgtype.UUID,
	uploaderType string,
	uploaderID pgtype.UUID,
	attachmentIDs []pgtype.UUID,
) error {
	expected := len(channelAttachmentIDSet(attachmentIDs))
	if expected == 0 {
		return nil
	}
	linked, err := queries.LinkOwnedAttachmentsToChannelMessage(ctx, db.LinkOwnedAttachmentsToChannelMessageParams{
		ChannelMessageID: messageID,
		WorkspaceID:      workspaceID,
		UploaderType:     uploaderType,
		UploaderID:       uploaderID,
		AttachmentIds:    attachmentIDs,
	})
	if err != nil {
		return err
	}
	if linked != int64(expected) {
		return errChannelAttachmentUnavailable
	}
	return nil
}

func channelAttachmentIDSet(ids []pgtype.UUID) map[string]struct{} {
	out := map[string]struct{}{}
	for _, id := range ids {
		if id.Valid {
			out[uuidToString(id)] = struct{}{}
		}
	}
	return out
}

func sameNullableUUID(got *string, want pgtype.UUID) bool {
	if !want.Valid {
		return got == nil || strings.TrimSpace(*got) == ""
	}
	return got != nil && *got == uuidToString(want)
}

func sameStringSet(a, b map[string]struct{}) bool {
	if len(a) != len(b) {
		return false
	}
	for key := range a {
		if _, ok := b[key]; !ok {
			return false
		}
	}
	return true
}

func (h *Handler) insertChannelMessage(ctx context.Context, channelID, workspaceID pgtype.UUID, authorType string, authorID pgtype.UUID, authorName, content, source string, externalID *string, replyToMessageID, threadRootMessageID pgtype.UUID, threadID *string, triggerDepth int) (ChannelMessageResponse, error) {
	return h.insertChannelMessageWithParts(ctx, channelID, workspaceID, authorType, authorID, authorName, content, nil, source, externalID, replyToMessageID, threadRootMessageID, threadID, triggerDepth)
}

func (h *Handler) insertChannelMessageWithParts(ctx context.Context, channelID, workspaceID pgtype.UUID, authorType string, authorID pgtype.UUID, authorName, content string, parts []protocol.MessagePart, source string, externalID *string, replyToMessageID, threadRootMessageID pgtype.UUID, threadID *string, triggerDepth int) (ChannelMessageResponse, error) {
	return h.insertChannelMessageWithPartsAndTask(ctx, pgtype.UUID{}, channelID, workspaceID, authorType, authorID, authorName, content, parts, source, externalID, replyToMessageID, threadRootMessageID, threadID, triggerDepth)
}

func (h *Handler) insertChannelMessageWithPartsAndTask(ctx context.Context, taskID, channelID, workspaceID pgtype.UUID, authorType string, authorID pgtype.UUID, authorName, content string, parts []protocol.MessagePart, source string, externalID *string, replyToMessageID, threadRootMessageID pgtype.UUID, threadID *string, triggerDepth int) (ChannelMessageResponse, error) {
	if h == nil || h.TxStarter == nil {
		return ChannelMessageResponse{}, errors.New("channel transaction starter unavailable")
	}
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return ChannelMessageResponse{}, err
	}
	defer tx.Rollback(ctx)
	inserted, err := insertChannelMessageWithPartsExec(ctx, tx, channelID, workspaceID, authorType, authorID, authorName, content, parts, source, externalID, nil, replyToMessageID, pgtype.UUID{}, nil, threadRootMessageID, threadID, triggerDepth, channelMessageKindHint{})
	if err != nil {
		return ChannelMessageResponse{}, err
	}
	if taskID.Valid {
		if err := h.recordVisibleTaskActionTx(ctx, tx, taskID, service.DAGCloseMessage, parseUUID(inserted.Message.ID), inserted.Message.Content, channelID); err != nil {
			return ChannelMessageResponse{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return ChannelMessageResponse{}, err
	}
	return inserted.Message, nil
}

func (h *Handler) recordVisibleTaskActionTx(ctx context.Context, tx pgx.Tx, taskID pgtype.UUID, kind service.DAGCloseActionKind, actionID pgtype.UUID, content string, channelID pgtype.UUID) error {
	if h == nil || h.TaskService == nil || tx == nil {
		return errors.New("task visible action requires handler task service and transaction")
	}
	qtx := h.Queries.WithTx(tx)
	task, err := qtx.GetAgentTask(ctx, taskID)
	if err != nil {
		return fmt.Errorf("load visible action task: %w", err)
	}
	if _, err := h.TaskService.RecordVisibleTaskActionTx(ctx, qtx, tx, task, kind, actionID, content, pgtype.UUID{}, channelID, pgtype.UUID{}, pgtype.UUID{}, ""); err != nil {
		return fmt.Errorf("record visible task action: %w", err)
	}
	return nil
}

type channelMessageInsertResult struct {
	Message ChannelMessageResponse
}

// insertChannelMessageWithPartsExec mutates only transactional state.
func insertChannelMessageWithPartsExec(ctx context.Context, exec dbExecutor, channelID, workspaceID pgtype.UUID, authorType string, authorID pgtype.UUID, authorName, content string, parts []protocol.MessagePart, source string, externalID, clientMessageID *string, replyToMessageID, quoteMessageID pgtype.UUID, quoteSnapshot []byte, threadRootMessageID pgtype.UUID, threadID *string, triggerDepth int, kindHint channelMessageKindHint) (channelMessageInsertResult, error) {
	// LRM-1523/1529: derive and persist kind + kind_source so dispatch can enforce
	// observe-only no-wake structurally. Priority: structured → system → lexicon → default.
	resolved := resolveChannelMessageKind(authorType, content, parts, kindHint)
	row := exec.QueryRow(ctx, `
			INSERT INTO channel_message (channel_id, workspace_id, author_type, author_id, author_name, content, parts, source, external_message_id, client_message_id, reply_to_message_id, quote_message_id, quote_snapshot, thread_root_message_id, thread_id, trigger_depth, kind, kind_source)
			VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb, $8, $9, $10, $11, $12, $13::jsonb, $14, $15, $16, $17, $18)
			RETURNING id, channel_id, workspace_id, author_type, author_id, author_name, content, parts, source, external_message_id, client_message_id, reply_to_message_id, quote_message_id, quote_snapshot, thread_root_message_id, thread_id, trigger_depth, seq, created_at, edited_at, deleted_at`,
		channelID, workspaceID, authorType, nullableUUID(authorID), authorName, content, messageparts.MustJSON(parts), source, externalID, clientMessageID, nullableUUID(replyToMessageID), nullableUUID(quoteMessageID), nullableJSONB(quoteSnapshot), nullableUUID(threadRootMessageID), threadID, triggerDepth, resolved.Kind, resolved.Source)
	msg, err := scanChannelMessage(row)
	if err != nil {
		return channelMessageInsertResult{}, err
	}
	msg.Kind = resolved.Kind
	msg.KindSource = resolved.Source
	if err := incrementChannelMainUnreadCounters(ctx, exec, channelID, authorType, authorID, msg.Seq, threadRootMessageID); err != nil {
		return channelMessageInsertResult{}, err
	}
	if err := incrementChannelMentionUnreadCounters(ctx, exec, channelID, authorType, authorID, msg.Seq, content, parts); err != nil {
		return channelMessageInsertResult{}, err
	}
	return channelMessageInsertResult{Message: msg}, nil
}

func incrementChannelMainUnreadCounters(ctx context.Context, exec dbExecutor, channelID pgtype.UUID, authorType string, authorID pgtype.UUID, seq int64, threadRootMessageID pgtype.UUID) error {
	if authorType == "system" || threadRootMessageID.Valid {
		return nil
	}
	_, err := exec.Exec(ctx, `
		WITH conv AS (
		  SELECT id FROM conversation WHERE channel_id = $1
		)
		UPDATE conversation_member cm
		SET main_unread_count = main_unread_count + 1,
		    updated_at = now()
		FROM conv
		WHERE cm.conversation_id = conv.id
		  AND cm.member_type = 'user'
		  AND cm.last_read_seq < $2
		  AND ($3 <> 'user' OR cm.member_id <> $4)`, channelID, seq, authorType, authorID)
	return err
}

func decrementChannelMainUnreadCounters(ctx context.Context, exec dbExecutor, channelID pgtype.UUID, authorType string, authorID pgtype.UUID, seq int64, threadRootMessageID pgtype.UUID) error {
	if authorType == "system" || threadRootMessageID.Valid {
		return nil
	}
	_, err := exec.Exec(ctx, `
		WITH conv AS (
		  SELECT id FROM conversation WHERE channel_id = $1
		)
		UPDATE conversation_member cm
		SET main_unread_count = GREATEST(main_unread_count - 1, 0),
		    updated_at = now()
		FROM conv
		WHERE cm.conversation_id = conv.id
		  AND cm.member_type = 'user'
		  AND cm.last_read_seq < $2
		  AND ($3 <> 'user' OR cm.member_id <> $4)`, channelID, seq, authorType, authorID)
	return err
}

func channelMentionedMemberIDs(content string, parts []protocol.MessagePart, authorType string, authorID pgtype.UUID) []pgtype.UUID {
	mentions := util.ParseMentionsFromContentAndParts(content, parts)
	if len(mentions) == 0 {
		return nil
	}
	memberIDs := make([]pgtype.UUID, 0, len(mentions))
	seen := make(map[string]struct{}, len(mentions))
	for _, mention := range mentions {
		if mention.Type != "member" {
			continue
		}
		memberID, err := util.ParseUUID(mention.ID)
		if err != nil {
			continue
		}
		if authorType == "user" && authorID.Valid && mention.ID == uuidToString(authorID) {
			continue
		}
		if _, ok := seen[mention.ID]; ok {
			continue
		}
		seen[mention.ID] = struct{}{}
		memberIDs = append(memberIDs, memberID)
	}
	return memberIDs
}

func incrementChannelMentionUnreadCounters(ctx context.Context, exec dbExecutor, channelID pgtype.UUID, authorType string, authorID pgtype.UUID, seq int64, content string, parts []protocol.MessagePart) error {
	memberIDs := channelMentionedMemberIDs(content, parts, authorType, authorID)
	if len(memberIDs) == 0 {
		return nil
	}
	_, err := exec.Exec(ctx, `
		WITH conv AS (
		  SELECT id FROM conversation WHERE channel_id = $1
		)
		UPDATE conversation_member cm
		SET mention_unread_count = mention_unread_count + 1,
		    updated_at = now()
		FROM conv
		WHERE cm.conversation_id = conv.id
		  AND cm.member_type = 'user'
		  AND cm.member_id = ANY($2::uuid[])
		  AND cm.last_read_seq < $3`, channelID, memberIDs, seq)
	return err
}

func decrementChannelMentionUnreadCounters(ctx context.Context, exec dbExecutor, channelID pgtype.UUID, authorType string, authorID pgtype.UUID, seq int64, content string, parts []protocol.MessagePart) error {
	memberIDs := channelMentionedMemberIDs(content, parts, authorType, authorID)
	if len(memberIDs) == 0 {
		return nil
	}
	_, err := exec.Exec(ctx, `
		WITH conv AS (
		  SELECT id FROM conversation WHERE channel_id = $1
		)
		UPDATE conversation_member cm
		SET mention_unread_count = GREATEST(mention_unread_count - 1, 0),
		    updated_at = now()
		FROM conv
		WHERE cm.conversation_id = conv.id
		  AND cm.member_type = 'user'
		  AND cm.member_id = ANY($2::uuid[])
		  AND cm.last_read_seq < $3`, channelID, memberIDs, seq)
	return err
}

func (h *Handler) sendChannelMessageToFeishu(ctx context.Context, ch ChannelResponse, authorName, content string) {
	if ch.LarkChatID == nil || h.LarkAPIClient == nil || h.LarkInstallations == nil || !h.LarkAPIClient.IsConfigured() {
		return
	}
	inst, ok := h.firstActiveFeishuInstallation(ctx, ch.WorkspaceID)
	if !ok {
		return
	}
	secret, err := h.LarkInstallations.DecryptAppSecret(inst)
	if err != nil {
		slog.Warn("channel feishu sync: decrypt app secret failed", "error", err)
		return
	}
	creds := lark.InstallationCredentials{AppID: inst.AppID, AppSecret: secret, TenantKey: inst.TenantKey.String, Region: lark.RegionOrDefault(inst.Region)}
	text := strings.TrimSpace(authorName + ": " + content)
	_, err = h.LarkAPIClient.SendTextMessage(ctx, lark.SendTextParams{InstallationID: creds, ChatID: lark.ChatID(*ch.LarkChatID), Text: text})
	if err != nil {
		slog.Warn("channel feishu sync: send text failed", "channel", ch.ID, "error", err)
	}
}

func (h *Handler) firstActiveFeishuInstallation(ctx context.Context, workspaceID string) (db.LarkInstallation, bool) {
	rows, err := h.Queries.ListLarkInstallationsByWorkspace(ctx, parseUUID(workspaceID))
	if err != nil {
		return db.LarkInstallation{}, false
	}
	for _, row := range rows {
		if row.Status == "active" && lark.RegionOrDefault(row.Region) == lark.RegionFeishu {
			return row, true
		}
	}
	return db.LarkInstallation{}, false
}

func (h *Handler) channelExists(ctx context.Context, workspaceID string, channelID pgtype.UUID) bool {
	var id pgtype.UUID
	return h.DB.QueryRow(ctx, `SELECT id FROM channel WHERE id = $1 AND workspace_id = $2`, channelID, parseUUID(workspaceID)).Scan(&id) == nil
}

func (h *Handler) channelIsArchived(ctx context.Context, workspaceID string, channelID pgtype.UUID) (bool, bool) {
	var archivedAt pgtype.Timestamptz
	err := h.DB.QueryRow(ctx, `SELECT archived_at FROM channel WHERE id = $1 AND workspace_id = $2`, channelID, parseUUID(workspaceID)).Scan(&archivedAt)
	if err != nil {
		return false, false
	}
	return archivedAt.Valid, true
}

func (h *Handler) channelUserIsMember(ctx context.Context, workspaceID string, channelID, userID pgtype.UUID) bool {
	var exists bool
	err := h.DB.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM channel_member
			WHERE channel_id = $1 AND workspace_id = $2 AND member_type = 'user' AND member_id = $3
		)`, channelID, parseUUID(workspaceID), userID).Scan(&exists)
	return err == nil && exists
}

func (h *Handler) requireChannelUserMember(w http.ResponseWriter, ctx context.Context, workspaceID string, channelID, userID pgtype.UUID) bool {
	if !h.channelExists(ctx, workspaceID, channelID) {
		writeError(w, http.StatusNotFound, "channel not found")
		return false
	}
	if !h.channelUserIsMember(ctx, workspaceID, channelID, userID) {
		writeError(w, http.StatusForbidden, "not a channel member")
		return false
	}
	return true
}

func (h *Handler) requireChannelAgentMember(w http.ResponseWriter, ctx context.Context, workspaceID string, channelID, agentID pgtype.UUID) bool {
	if !h.channelExists(ctx, workspaceID, channelID) {
		writeError(w, http.StatusNotFound, "channel not found")
		return false
	}
	if !h.channelHasAgentMember(ctx, parseUUID(workspaceID), channelID, agentID) {
		writeError(w, http.StatusForbidden, "not a channel member")
		return false
	}
	return true
}

func (h *Handler) requireGroupChannel(w http.ResponseWriter, ctx context.Context, workspaceID string, channelID pgtype.UUID) bool {
	var id pgtype.UUID
	if err := h.DB.QueryRow(ctx, `SELECT id FROM channel WHERE id = $1 AND workspace_id = $2 AND kind = 'group'`, channelID, parseUUID(workspaceID)).Scan(&id); err != nil {
		writeError(w, http.StatusNotFound, "channel not found")
		return false
	}
	return true
}

func (h *Handler) requireChannelWritable(w http.ResponseWriter, ctx context.Context, workspaceID string, channelID pgtype.UUID) bool {
	archived, found := h.channelIsArchived(ctx, workspaceID, channelID)
	if !found {
		writeError(w, http.StatusNotFound, "channel not found")
		return false
	}
	if archived {
		writeError(w, http.StatusConflict, "channel is archived")
		return false
	}
	return true
}

func (h *Handler) requireChannelManager(w http.ResponseWriter, r *http.Request, workspaceID string, channelID, userID pgtype.UUID) bool {
	var createdBy pgtype.UUID
	err := h.DB.QueryRow(r.Context(), `
		SELECT created_by
		FROM channel
		WHERE id = $1 AND workspace_id = $2 AND kind = 'group'`, channelID, parseUUID(workspaceID)).Scan(&createdBy)
	if err != nil {
		writeError(w, http.StatusNotFound, "channel not found")
		return false
	}
	if uuidToString(createdBy) == uuidToString(userID) {
		return true
	}
	member, ok := h.workspaceMember(w, r, workspaceID)
	if !ok {
		return false
	}
	if roleAllowed(member.Role, "owner", "admin") {
		return true
	}
	writeError(w, http.StatusForbidden, "insufficient permissions")
	return false
}

func (h *Handler) getChannel(ctx context.Context, workspaceID string, channelID pgtype.UUID) (ChannelResponse, bool) {
	row := h.DB.QueryRow(ctx, `SELECT id, workspace_id, name, description, lark_chat_id, project_id, created_by, created_at, updated_at, kind, system_key, archived_at, archived_by, avatar_url FROM channel WHERE id = $1 AND workspace_id = $2`, channelID, parseUUID(workspaceID))
	ch, err := scanChannel(row)
	return ch, err == nil
}

func (h *Handler) getChannelByLarkChatID(ctx context.Context, workspaceID, larkChatID string) (ChannelResponse, bool) {
	row := h.DB.QueryRow(ctx, `SELECT id, workspace_id, name, description, lark_chat_id, project_id, created_by, created_at, updated_at, kind, system_key, archived_at, archived_by, avatar_url FROM channel WHERE workspace_id = $1 AND lark_chat_id = $2 LIMIT 1`, parseUUID(workspaceID), larkChatID)
	ch, err := scanChannel(row)
	return ch, err == nil
}

func (h *Handler) channelAuthorName(ctx context.Context, userID string) string {
	user, err := h.Queries.GetUser(ctx, parseUUID(userID))
	if err == nil && strings.TrimSpace(userDisplayName(user)) != "" {
		return userDisplayName(user)
	}
	if err == nil && strings.TrimSpace(user.Email) != "" {
		return user.Email
	}
	return "You"
}

func (h *Handler) agentName(ctx context.Context, agentID pgtype.UUID) string {
	agent, err := h.Queries.GetAgent(ctx, agentID)
	if err == nil && strings.TrimSpace(agentDisplayName(agent)) != "" {
		return agentDisplayName(agent)
	}
	return "Agent"
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanChannel(row rowScanner) (ChannelResponse, error) {
	var id, wsID, projectID, createdBy, archivedBy pgtype.UUID
	var name string
	var desc, lark, systemKey, avatarURL pgtype.Text
	var createdAt, updatedAt, archivedAt pgtype.Timestamptz
	var kind string
	if err := row.Scan(&id, &wsID, &name, &desc, &lark, &projectID, &createdBy, &createdAt, &updatedAt, &kind, &systemKey, &archivedAt, &archivedBy, &avatarURL); err != nil {
		return ChannelResponse{}, err
	}
	return ChannelResponse{
		ID:          uuidToString(id),
		WorkspaceID: uuidToString(wsID),
		ProjectID:   uuidToPtr(projectID),
		Name:        name,
		Kind:        kind,
		SystemKey:   textToPtr(systemKey),
		Description: textToPtr(desc),
		LarkChatID:  textToPtr(lark),
		AvatarURL:   textToPtr(avatarURL),
		CreatedBy:   uuidToString(createdBy),
		CreatedAt:   timestampToString(createdAt),
		UpdatedAt:   timestampToString(updatedAt),
		ArchivedAt:  timestampToPtr(archivedAt),
		ArchivedBy:  uuidToPtr(archivedBy),
	}, nil
}

func channelLastMessage(authorType, authorName, content string, rawParts []byte, createdAt pgtype.Timestamptz) *ChannelLastMessage {
	parts := messageparts.Decode(rawParts)
	if authorType == "agent" {
		if unwrappedContent, unwrappedParts, unwrapped, err := messageparts.UnwrapStructuredMessageSend(content, parts); err == nil && unwrapped {
			content = unwrappedContent
			parts = unwrappedParts
		}
	}
	return &ChannelLastMessage{
		Type:       authorType,
		AuthorName: authorName,
		Content:    content,
		Parts:      parts,
		CreatedAt:  timestampToString(createdAt),
	}
}

func scanChannelMessage(row rowScanner) (ChannelMessageResponse, error) {
	var id, channelID, wsID, authorID, replyToMessageID, quoteMessageID, threadRootMessageID pgtype.UUID
	var authorType, authorName, content, source string
	var parts, quoteSnapshot []byte
	var external, client, thread pgtype.Text
	var triggerDepth int
	var seq int64
	var createdAt, editedAt, deletedAt pgtype.Timestamptz
	if err := row.Scan(&id, &channelID, &wsID, &authorType, &authorID, &authorName, &content, &parts, &source, &external, &client, &replyToMessageID, &quoteMessageID, &quoteSnapshot, &threadRootMessageID, &thread, &triggerDepth, &seq, &createdAt, &editedAt, &deletedAt); err != nil {
		return ChannelMessageResponse{}, err
	}
	decodedParts := messageparts.Decode(parts)
	deletedAtPtr := timestampToPtr(deletedAt)
	if deletedAtPtr != nil {
		content = ""
		decodedParts = nil
	} else if authorType == "agent" {
		if unwrappedContent, unwrappedParts, unwrapped, err := messageparts.UnwrapStructuredMessageSend(content, decodedParts); err == nil && unwrapped {
			content = unwrappedContent
			decodedParts = unwrappedParts
		}
	}
	return ChannelMessageResponse{ID: uuidToString(id), ChannelID: uuidToString(channelID), WorkspaceID: uuidToString(wsID), Seq: seq, Type: authorType, AuthorID: uuidToPtr(authorID), AuthorName: authorName, Content: content, Parts: decodedParts, Source: source, ExternalMessageID: textToPtr(external), ClientMessageID: textToPtr(client), ReplyToMessageID: uuidToPtr(replyToMessageID), QuoteMessageID: uuidToPtr(quoteMessageID), quoteSnapshotRaw: quoteSnapshot, ThreadRootMessageID: uuidToPtr(threadRootMessageID), ThreadID: textToPtr(thread), TriggerDepth: triggerDepth, CreatedAt: timestampToString(createdAt), EditedAt: timestampToPtr(editedAt), DeletedAt: deletedAtPtr}, nil
}

func trimTextPtr(s *string) *string {
	if s == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*s)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func nullableUUID(u pgtype.UUID) any {
	if !u.Valid {
		return nil
	}
	return u
}

func nullableJSONB(raw []byte) any {
	if len(raw) == 0 {
		return nil
	}
	return string(raw)
}

func ptrString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func queryBool(r *http.Request, key string) bool {
	switch strings.ToLower(strings.TrimSpace(r.URL.Query().Get(key))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func boundedQueryInt(r *http.Request, key string, def, max int) int {
	out := def
	if raw := strings.TrimSpace(r.URL.Query().Get(key)); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			out = n
		}
	}
	if out > max {
		return max
	}
	return out
}

func errorsIsNoRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }
