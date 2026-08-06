package handler

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// Source discriminator for visible DM list items. New Messages DMs are
// kind='dm' channels; legacy chat_sessions may still exist for history
// migration, but they are not a visible DM source.
const (
	dmSourceChannel = "dm_channel"
)

// dmDefaultLimit / dmMaxLimit bound the DM page.
const (
	dmDefaultLimit = 50
	dmMaxLimit     = 100
)

type DMPeer struct {
	Type      string  `json:"type"` // "user" | "agent"
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	AvatarURL *string `json:"avatar_url,omitempty"`
	// Archived is true when the peer agent has been archived. The DM stays
	// visible and readable (history is never hidden by archiving the peer),
	// but the client should show a read-only/"archived" state and block new
	// sends rather than 404 the whole conversation.
	Archived bool `json:"archived,omitempty"`
}

// DMItem is one row in the unified DM list and the create-or-find response.
// Unread is a count for dm_channel items.
type DMItem struct {
	ID             string                      `json:"id"` // dm channel id
	Source         string                      `json:"source"`
	Mode           string                      `json:"mode,omitempty"`
	Peer           DMPeer                      `json:"peer"`
	Participants   []DMPeer                    `json:"participants,omitempty"`
	Supervised     bool                        `json:"supervised,omitempty"`
	LastMessage    *ChannelLastMessage         `json:"last_message,omitempty"`
	Unread         int                         `json:"unread"`
	RealUnread     int                         `json:"real_unread"`
	ManuallyUnread bool                        `json:"manually_unread,omitempty"`
	PinnedAt       *string                     `json:"pinned_at,omitempty"`
	MutedAt        *string                     `json:"muted_at,omitempty"`
	Muted          bool                        `json:"muted,omitempty"`
	HasMention     bool                        `json:"has_mention,omitempty"`
	LastReadSeq    *int64                      `json:"last_read_seq,omitempty"`
	RuntimeStats   *protocol.RuntimeTokenStats `json:"runtime_stats,omitempty"`
	UpdatedAt      string                      `json:"updated_at"`
}

type CreateOrFindDirectMessageRequest struct {
	PeerType string `json:"peer_type"` // "user" | "agent"
	PeerID   string `json:"peer_id"`
}

// dmCanonicalName builds the deterministic, collision-free channel name that
// uniquely identifies a 1-on-1 DM between two members. Tokens are "type:id"
// (lowercased uuid), sorted so the pair maps to one name regardless of who
// initiates, then joined under the "dm:" prefix. It is stored in channel.name,
// whose UNIQUE(workspace_id, name) constraint is what makes create-or-find
// idempotent without a dedicated member-pair index.
func dmCanonicalName(typeA, idA, typeB, idB string) string {
	a := typeA + ":" + strings.ToLower(idA)
	b := typeB + ":" + strings.ToLower(idB)
	if b < a {
		a, b = b, a
	}
	return "dm:" + a + "|" + b
}

// CreateOrFindDirectMessage (POST /api/dm) returns the existing dm_channel with
// the peer or creates one, idempotently. Legacy chat_sessions are kept only for
// history migration and must not block a new visible DM.
func (h *Handler) CreateOrFindDirectMessage(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	var req CreateOrFindDirectMessageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	peerID, ok := parseUUIDOrBadRequest(w, req.PeerID, "peer_id")
	if !ok {
		return
	}
	switch strings.TrimSpace(req.PeerType) {
	case "user":
		h.createOrFindUserDM(w, r, workspaceID, userID, peerID)
	case "agent":
		h.createOrFindAgentDM(w, r, workspaceID, userID, peerID)
	default:
		writeError(w, http.StatusBadRequest, "peer_type must be user or agent")
	}
}

func (h *Handler) createOrFindUserDM(w http.ResponseWriter, r *http.Request, workspaceID, userID string, peerID pgtype.UUID) {
	if uuidToString(peerID) == userID {
		writeError(w, http.StatusBadRequest, "cannot start a direct message with yourself")
		return
	}
	if _, err := h.getWorkspaceMember(r.Context(), uuidToString(peerID), workspaceID); err != nil {
		writeError(w, http.StatusNotFound, "workspace member not found")
		return
	}
	peer := DMPeer{Type: "user", ID: uuidToString(peerID), Name: h.channelAuthorName(r.Context(), uuidToString(peerID))}

	canonical := dmCanonicalName("user", userID, "user", uuidToString(peerID))
	if ch, found := h.findDMChannel(r.Context(), workspaceID, canonical); found {
		h.clearDMPeerHidden(r.Context(), workspaceID, userID, dmPeerRef{Type: "user", ID: peerID})
		writeJSON(w, http.StatusOK, dmItemForChannel(ch, peer))
		return
	}
	ch, created := h.createDMChannel(r.Context(), w, workspaceID, userID, canonical, []dmMember{
		{memberType: "user", memberID: parseUUID(userID)},
		{memberType: "user", memberID: peerID},
	})
	if !created {
		return // createDMChannel already wrote the error (or returned an existing one)
	}
	writeJSON(w, http.StatusCreated, dmItemForChannel(ch, peer))
}

func (h *Handler) createOrFindAgentDM(w http.ResponseWriter, r *http.Request, workspaceID, userID string, agentID pgtype.UUID) {
	agent, err := h.Queries.GetAgentInWorkspace(r.Context(), db.GetAgentInWorkspaceParams{ID: agentID, WorkspaceID: parseUUID(workspaceID)})
	if err != nil || agent.ArchivedAt.Valid {
		writeError(w, http.StatusNotFound, "agent not found")
		return
	}
	peer := DMPeer{Type: "agent", ID: uuidToString(agent.ID), Name: agentDisplayName(agent), AvatarURL: textToPtr(agent.AvatarUrl)}

	canonical := dmCanonicalName("user", userID, "agent", uuidToString(agentID))
	if ch, found := h.findDMChannel(r.Context(), workspaceID, canonical); found {
		h.clearDMPeerHidden(r.Context(), workspaceID, userID, dmPeerRef{Type: "agent", ID: agentID})
		writeJSON(w, http.StatusOK, dmItemForChannel(ch, peer))
		return
	}
	ch, created := h.createDMChannel(r.Context(), w, workspaceID, userID, canonical, []dmMember{
		{memberType: "user", memberID: parseUUID(userID)},
		{memberType: "agent", memberID: agentID},
	})
	if !created {
		return
	}
	h.clearDMPeerHidden(r.Context(), workspaceID, userID, dmPeerRef{Type: "agent", ID: agentID})
	writeJSON(w, http.StatusCreated, dmItemForChannel(ch, peer))
}

type dmMember struct {
	memberType string
	memberID   pgtype.UUID
}

// findDMChannel looks up the dm channel for a canonical name. Returns false when
// none exists (or the row is somehow not kind='dm').
func (h *Handler) findDMChannel(ctx context.Context, workspaceID, canonical string) (ChannelResponse, bool) {
	row := h.DB.QueryRow(ctx, `SELECT id, workspace_id, name, description, lark_chat_id, project_id, created_by, created_at, updated_at, kind, system_key, archived_at, archived_by, avatar_url FROM channel WHERE workspace_id = $1 AND name = $2 AND kind = 'dm' LIMIT 1`, parseUUID(workspaceID), canonical)
	ch, err := scanChannel(row)
	return ch, err == nil
}

// createDMChannel inserts a kind='dm' channel with the canonical name and its
// members. ON CONFLICT (workspace_id, name) DO NOTHING makes it race-safe: if a
// concurrent request created the same DM between our find and insert, we detect
// the empty RETURNING and re-find the existing channel instead of failing.
func (h *Handler) createDMChannel(ctx context.Context, w http.ResponseWriter, workspaceID, creatorID, canonical string, members []dmMember) (ChannelResponse, bool) {
	// Channel + both members must be all-or-nothing: a partial write would leave
	// a member-less dm channel that is invisible (listDMChannels' peer join drops
	// it) AND unrecoverable (create-or-find returns it by name without back-filling
	// members), so it must never be committed half-built.
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		writeDMCreateError(w)
		return ChannelResponse{}, false
	}
	defer tx.Rollback(ctx) // no-op once committed
	actor := channelMemberUserActor(parseUUID(creatorID))
	if err := validateChannelMemberActorWithExec(ctx, tx, workspaceID, actor); err != nil {
		writeDMCreateError(w)
		return ChannelResponse{}, false
	}

	row := tx.QueryRow(ctx, `
		INSERT INTO channel (workspace_id, name, created_by, kind)
		VALUES ($1, $2, $3, 'dm')
		ON CONFLICT (workspace_id, name) DO NOTHING
		RETURNING id, workspace_id, name, description, lark_chat_id, project_id, created_by, created_at, updated_at, kind, system_key, archived_at, archived_by, avatar_url`,
		parseUUID(workspaceID), canonical, parseUUID(creatorID))
	ch, err := scanChannel(row)
	if err != nil {
		// ON CONFLICT DO NOTHING returns no row when the DM already exists (race
		// between find and insert) — abandon this tx and re-find the committed one.
		if errorsIsNoRows(err) {
			if existing, found := h.findDMChannel(ctx, workspaceID, canonical); found {
				return existing, true
			}
		}
		writeDMCreateError(w)
		return ChannelResponse{}, false
	}
	for _, m := range members {
		if _, err := tx.Exec(ctx, `
			INSERT INTO channel_member (
			  channel_id, workspace_id, member_type, member_id,
			  added_by_type, added_by_id, join_source
			)
			VALUES ($1, $2, $3, $4, $5, $6, 'manual')
			ON CONFLICT DO NOTHING`,
			parseUUID(ch.ID), parseUUID(workspaceID), m.memberType, m.memberID,
			actor.Type, actor.ID,
		); err != nil {
			writeDMCreateError(w)
			return ChannelResponse{}, false
		}
	}
	if err := tx.Commit(ctx); err != nil {
		writeDMCreateError(w)
		return ChannelResponse{}, false
	}
	h.publish(protocol.EventChannelUpdated, workspaceID, "member", creatorID, ch)
	return ch, true
}

func writeDMCreateError(w http.ResponseWriter) {
	if w != nil {
		writeError(w, http.StatusInternalServerError, "failed to create direct message")
	}
}

// dmItemForChannel builds a create-or-find response item for a dm channel.
// last_message/unread are intentionally omitted here (the caller navigates into
// the DM and the unified list endpoint computes them) — keeping create-or-find
// a single write with no extra read.
func dmItemForChannel(ch ChannelResponse, peer DMPeer) DMItem {
	return DMItem{ID: ch.ID, Source: dmSourceChannel, Mode: "direct", Peer: peer, UpdatedAt: ch.UpdatedAt}
}

type dmPeerRef struct {
	Type string
	ID   pgtype.UUID
}

func (h *Handler) resolveDMChannelPeerOnly(ctx context.Context, workspaceID, userID string, channelID pgtype.UUID) (dmPeerRef, bool) {
	ch, found := h.getChannel(ctx, workspaceID, channelID)
	if !found || ch.Kind != "dm" {
		return dmPeerRef{}, false
	}
	if !h.channelUserIsMember(ctx, workspaceID, channelID, parseUUID(userID)) {
		return dmPeerRef{}, false
	}
	var peer dmPeerRef
	err := h.DB.QueryRow(ctx, `
		SELECT member_type, member_id
		FROM channel_member
		WHERE channel_id = $1
		  AND NOT (member_type = 'user' AND member_id = $2)
		ORDER BY created_at ASC
		LIMIT 1`, channelID, parseUUID(userID)).Scan(&peer.Type, &peer.ID)
	return peer, err == nil
}

func (h *Handler) resolveDMChannelPeer(w http.ResponseWriter, ctx context.Context, workspaceID, userID string, channelID pgtype.UUID) (dmPeerRef, bool) {
	ch, found := h.getChannel(ctx, workspaceID, channelID)
	if !found || ch.Kind != "dm" {
		writeError(w, http.StatusNotFound, "direct message not found")
		return dmPeerRef{}, false
	}
	if !h.channelUserIsMember(ctx, workspaceID, channelID, parseUUID(userID)) {
		writeError(w, http.StatusForbidden, "not a direct message member")
		return dmPeerRef{}, false
	}
	peer, ok := h.resolveDMChannelPeerOnly(ctx, workspaceID, userID, channelID)
	if !ok {
		writeError(w, http.StatusNotFound, "direct message peer not found")
		return dmPeerRef{}, false
	}
	return peer, true
}

// resolveDMChannelPeerForPreference resolves the dm_peer_state key for list
// preference writes (pin/mute/unread/close). Channel members keep the existing
// 1:1 peer key. Supervised agent_pair owners are not channel members; their
// viewer prefs are keyed by the channel itself so they never collide with a
// personal 1:1 DM against one of the pair participants.
func (h *Handler) resolveDMChannelPeerForPreference(w http.ResponseWriter, ctx context.Context, workspaceID, userID string, channelID pgtype.UUID) (dmPeerRef, bool) {
	ch, found := h.getChannel(ctx, workspaceID, channelID)
	if !found || ch.Kind != "dm" {
		writeError(w, http.StatusNotFound, "direct message not found")
		return dmPeerRef{}, false
	}
	userUUID := parseUUID(userID)
	if h.channelUserIsMember(ctx, workspaceID, channelID, userUUID) {
		peer, ok := h.resolveDMChannelPeerOnly(ctx, workspaceID, userID, channelID)
		if !ok {
			writeError(w, http.StatusNotFound, "direct message peer not found")
			return dmPeerRef{}, false
		}
		return peer, true
	}
	if h.channelUserIsAgentDMSupervisor(ctx, workspaceID, channelID, userUUID) {
		return dmPeerRef{Type: "channel", ID: channelID}, true
	}
	writeError(w, http.StatusForbidden, "not a direct message member")
	return dmPeerRef{}, false
}

// requireDMChannelAgentAccess gates access to a DM channel whose peer is an
// agent. An archived peer never 404s the conversation — history stays
// readable — but write operations (send/reply/choose-option) must pass
// requireWrite=true so an archived peer still blocks new activity.
func (h *Handler) requireDMChannelAgentAccess(w http.ResponseWriter, r *http.Request, workspaceID, userID string, ch ChannelResponse, requireWrite bool) bool {
	if ch.Kind != "dm" {
		return true
	}
	peer, ok := h.resolveDMChannelPeer(w, r.Context(), workspaceID, userID, parseUUID(ch.ID))
	if !ok {
		return false
	}
	if peer.Type != "agent" {
		return true
	}
	agent, err := h.Queries.GetAgentInWorkspace(r.Context(), db.GetAgentInWorkspaceParams{
		ID:          peer.ID,
		WorkspaceID: parseUUID(workspaceID),
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "direct message peer not found")
		return false
	}
	if agent.ArchivedAt.Valid && requireWrite {
		writeError(w, http.StatusForbidden, "direct message peer has been archived and can no longer receive messages")
		return false
	}
	return true
}

func (h *Handler) resolveDMSessionPeer(w http.ResponseWriter, r *http.Request, userID, workspaceID, sessionID string) (db.ChatSession, dmPeerRef, bool) {
	session, ok := h.gateChatSessionForUser(w, r, userID, workspaceID, sessionID)
	if !ok {
		return db.ChatSession{}, dmPeerRef{}, false
	}
	if session.Status != "active" {
		writeError(w, http.StatusNotFound, "direct message not found")
		return db.ChatSession{}, dmPeerRef{}, false
	}
	return session, dmPeerRef{Type: "agent", ID: session.AgentID}, true
}

func (h *Handler) setDMPeerPin(ctx context.Context, workspaceID, userID string, peer dmPeerRef, pinned bool) error {
	if pinned {
		_, err := h.DB.Exec(ctx, `
			INSERT INTO dm_peer_state (workspace_id, user_id, peer_type, peer_id, pinned_at, updated_at)
			VALUES ($1, $2, $3, $4, now(), now())
			ON CONFLICT (workspace_id, user_id, peer_type, peer_id)
			DO UPDATE SET pinned_at = now(), updated_at = now()`,
			parseUUID(workspaceID), parseUUID(userID), peer.Type, peer.ID)
		return err
	}
	_, err := h.DB.Exec(ctx, `
		UPDATE dm_peer_state
		SET pinned_at = NULL, updated_at = now()
		WHERE workspace_id = $1 AND user_id = $2 AND peer_type = $3 AND peer_id = $4`,
		parseUUID(workspaceID), parseUUID(userID), peer.Type, peer.ID)
	return err
}

func (h *Handler) setDMPeerMute(ctx context.Context, workspaceID, userID string, peer dmPeerRef, muted bool) error {
	_, err := h.DB.Exec(ctx, `
		WITH peer_state AS (
		  INSERT INTO dm_peer_state (workspace_id, user_id, peer_type, peer_id, muted_at, updated_at)
		  VALUES ($1, $2, $3, $4, CASE WHEN $5 THEN now() ELSE NULL END, now())
		  ON CONFLICT (workspace_id, user_id, peer_type, peer_id)
		  DO UPDATE SET muted_at = EXCLUDED.muted_at, updated_at = now()
		  RETURNING workspace_id, user_id, peer_type, peer_id, muted_at
		),
		dm_conv AS (
		  SELECT c.id
		  FROM conversation c
		  JOIN channel_member self
		    ON self.channel_id = c.channel_id
		   AND self.member_type = 'user'
		   AND self.member_id = $2
		  JOIN channel_member peer
		    ON peer.channel_id = c.channel_id
		   AND peer.member_type = $3
		   AND peer.member_id = $4
		  WHERE c.workspace_id = $1
		    AND c.kind = 'dm'
		)
		UPDATE conversation_member cm
		SET muted_at = peer_state.muted_at,
		    updated_at = now()
		FROM peer_state, dm_conv
		WHERE cm.conversation_id = dm_conv.id
		  AND cm.workspace_id = peer_state.workspace_id
		  AND cm.member_type = 'user'
		  AND cm.member_id = peer_state.user_id`,
		parseUUID(workspaceID), parseUUID(userID), peer.Type, peer.ID, muted)
	return err
}

func (h *Handler) markDMPeerUnread(ctx context.Context, workspaceID, userID string, peer dmPeerRef) error {
	_, err := h.DB.Exec(ctx, `
		INSERT INTO dm_peer_state (workspace_id, user_id, peer_type, peer_id, manual_unread_at, updated_at)
		VALUES ($1, $2, $3, $4, now(), now())
		ON CONFLICT (workspace_id, user_id, peer_type, peer_id)
		DO UPDATE SET manual_unread_at = now(), updated_at = now()`,
		parseUUID(workspaceID), parseUUID(userID), peer.Type, peer.ID)
	return err
}

func (h *Handler) closeDMPeer(ctx context.Context, workspaceID, userID string, peer dmPeerRef) error {
	_, err := h.DB.Exec(ctx, `
		WITH peer_state AS (
		  INSERT INTO dm_peer_state (workspace_id, user_id, peer_type, peer_id, hidden_at, manual_unread_at, updated_at)
		  VALUES ($1, $2, $3, $4, now(), NULL, now())
		  ON CONFLICT (workspace_id, user_id, peer_type, peer_id)
		  DO UPDATE SET hidden_at = now(), manual_unread_at = NULL, updated_at = now()
		  RETURNING workspace_id, user_id, peer_type, peer_id, hidden_at
		),
		dm_conv AS (
		  SELECT c.id
		  FROM conversation c
		  JOIN channel_member self
		    ON self.channel_id = c.channel_id
		   AND self.member_type = 'user'
		   AND self.member_id = $2
		  JOIN channel_member peer
		    ON peer.channel_id = c.channel_id
		   AND peer.member_type = $3
		   AND peer.member_id = $4
		  WHERE c.workspace_id = $1
		    AND c.kind = 'dm'
		)
		UPDATE conversation_member cm
		SET closed_at = peer_state.hidden_at,
		    updated_at = now()
		FROM peer_state, dm_conv
		WHERE cm.conversation_id = dm_conv.id
		  AND cm.workspace_id = peer_state.workspace_id
		  AND cm.member_type = 'user'
		  AND cm.member_id = peer_state.user_id`,
		parseUUID(workspaceID), parseUUID(userID), peer.Type, peer.ID)
	return err
}

func (h *Handler) clearDMPeerHidden(ctx context.Context, workspaceID, userID string, peer dmPeerRef) {
	if _, err := h.DB.Exec(ctx, `
		WITH cleared_peer AS (
		  UPDATE dm_peer_state
		  SET hidden_at = NULL, updated_at = now()
		  WHERE workspace_id = $1 AND user_id = $2 AND peer_type = $3 AND peer_id = $4 AND hidden_at IS NOT NULL
		  RETURNING workspace_id, user_id, peer_type, peer_id
		),
		dm_conv AS (
		  SELECT c.id, $2::uuid AS user_id
		  FROM conversation c
		  JOIN channel_member self
		    ON self.channel_id = c.channel_id
		   AND self.member_type = 'user'
		   AND self.member_id = $2
		  JOIN channel_member peer
		    ON peer.channel_id = c.channel_id
		   AND peer.member_type = $3
		   AND peer.member_id = $4
		  WHERE c.workspace_id = $1
		    AND c.kind = 'dm'
		)
		UPDATE conversation_member cm
		SET closed_at = NULL,
		    updated_at = now()
		FROM dm_conv
		LEFT JOIN cleared_peer
		  ON cleared_peer.user_id = dm_conv.user_id
		WHERE cm.conversation_id = dm_conv.id
		  AND cm.workspace_id = $1
		  AND cm.member_type = 'user'
		  AND cm.member_id = dm_conv.user_id
		  AND cm.closed_at IS NOT NULL`,
		parseUUID(workspaceID), parseUUID(userID), peer.Type, peer.ID); err != nil {
		slog.Warn("dm: clear hidden state failed", "workspace", workspaceID, "user", userID, "peer_type", peer.Type, "error", err)
	}
}

func (h *Handler) clearDMPeerManualUnread(ctx context.Context, workspaceID, userID string, peer dmPeerRef) {
	if _, err := h.DB.Exec(ctx, `
		UPDATE dm_peer_state
		SET manual_unread_at = NULL, updated_at = now()
		WHERE workspace_id = $1 AND user_id = $2 AND peer_type = $3 AND peer_id = $4 AND manual_unread_at IS NOT NULL`,
		parseUUID(workspaceID), parseUUID(userID), peer.Type, peer.ID); err != nil {
		slog.Warn("dm: clear manual unread failed", "workspace", workspaceID, "user", userID, "peer_type", peer.Type, "error", err)
	}
}

func (h *Handler) clearDMPeerManualUnreadForChannel(ctx context.Context, workspaceID, userID string, channelID pgtype.UUID) {
	peer, ok := h.resolveDMChannelPeerOnly(ctx, workspaceID, userID, channelID)
	if ok {
		h.clearDMPeerManualUnread(ctx, workspaceID, userID, peer)
		return
	}
	if h.channelUserIsAgentDMSupervisor(ctx, workspaceID, channelID, parseUUID(userID)) {
		h.clearDMPeerManualUnread(ctx, workspaceID, userID, dmPeerRef{Type: "channel", ID: channelID})
	}
}

func (h *Handler) clearDMPeerHiddenForChatSession(ctx context.Context, workspaceID, userID string, agentID pgtype.UUID) {
	h.clearDMPeerHidden(ctx, workspaceID, userID, dmPeerRef{Type: "agent", ID: agentID})
}

func (h *Handler) clearDMHiddenForChannelMembers(ctx context.Context, workspaceID string, channelID pgtype.UUID) {
	if _, err := h.DB.Exec(ctx, `
		WITH user_peers AS (
			SELECT cm.member_id AS user_id, peer.member_type AS peer_type, peer.member_id AS peer_id
			FROM channel_member cm
			JOIN LATERAL (
				SELECT member_type, member_id
				FROM channel_member m2
				WHERE m2.channel_id = cm.channel_id
				  AND NOT (m2.member_type = 'user' AND m2.member_id = cm.member_id)
				ORDER BY m2.created_at ASC
				LIMIT 1
			) peer ON true
			WHERE cm.channel_id = $1 AND cm.workspace_id = $2 AND cm.member_type = 'user'
		),
		cleared_peer AS (
		  UPDATE dm_peer_state s
		  SET hidden_at = NULL, updated_at = now()
		  FROM user_peers p
		  WHERE s.workspace_id = $2
		    AND s.user_id = p.user_id
		    AND s.peer_type = p.peer_type
		    AND s.peer_id = p.peer_id
		    AND s.hidden_at IS NOT NULL
		  RETURNING s.user_id
		),
		conv AS (
		  SELECT id
		  FROM conversation
		  WHERE channel_id = $1
		)
		UPDATE conversation_member cm
		SET closed_at = NULL,
		    updated_at = now()
		FROM conv
		WHERE cm.conversation_id = conv.id
		  AND cm.workspace_id = $2
		  AND cm.member_type = 'user'
		  AND cm.closed_at IS NOT NULL`, channelID, parseUUID(workspaceID)); err != nil {
		slog.Warn("dm: clear channel hidden state failed", "workspace", workspaceID, "channel", uuidToString(channelID), "error", err)
	}
	// Supervised agent_pair owners key prefs by channel, not a peer agent.
	if _, err := h.DB.Exec(ctx, `
		UPDATE dm_peer_state
		SET hidden_at = NULL, updated_at = now()
		WHERE workspace_id = $2
		  AND peer_type = 'channel'
		  AND peer_id = $1
		  AND hidden_at IS NOT NULL`, channelID, parseUUID(workspaceID)); err != nil {
		slog.Warn("dm: clear supervised channel hidden state failed", "workspace", workspaceID, "channel", uuidToString(channelID), "error", err)
	}
}

func (h *Handler) mutateDMChannelPeer(w http.ResponseWriter, r *http.Request, action func(context.Context, string, string, dmPeerRef) error) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	channelID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "channelId"), "channel id")
	if !ok {
		return
	}
	peer, ok := h.resolveDMChannelPeerForPreference(w, r.Context(), workspaceID, userID, channelID)
	if !ok {
		return
	}
	if err := action(r.Context(), workspaceID, userID, peer); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update direct message")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (h *Handler) mutateDMSessionPeer(w http.ResponseWriter, r *http.Request, action func(context.Context, string, string, dmPeerRef) error) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	_, peer, ok := h.resolveDMSessionPeer(w, r, userID, workspaceID, chi.URLParam(r, "sessionId"))
	if !ok {
		return
	}
	if err := action(r.Context(), workspaceID, userID, peer); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update direct message")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (h *Handler) PinDMChannel(w http.ResponseWriter, r *http.Request) {
	h.mutateDMChannelPeer(w, r, func(ctx context.Context, workspaceID, userID string, peer dmPeerRef) error {
		return h.setDMPeerPin(ctx, workspaceID, userID, peer, true)
	})
}

func (h *Handler) UnpinDMChannel(w http.ResponseWriter, r *http.Request) {
	h.mutateDMChannelPeer(w, r, func(ctx context.Context, workspaceID, userID string, peer dmPeerRef) error {
		return h.setDMPeerPin(ctx, workspaceID, userID, peer, false)
	})
}

func (h *Handler) MuteDMChannel(w http.ResponseWriter, r *http.Request) {
	h.mutateDMChannelPeer(w, r, func(ctx context.Context, workspaceID, userID string, peer dmPeerRef) error {
		return h.setDMPeerMute(ctx, workspaceID, userID, peer, true)
	})
}

func (h *Handler) UnmuteDMChannel(w http.ResponseWriter, r *http.Request) {
	h.mutateDMChannelPeer(w, r, func(ctx context.Context, workspaceID, userID string, peer dmPeerRef) error {
		return h.setDMPeerMute(ctx, workspaceID, userID, peer, false)
	})
}

func (h *Handler) MarkDMChannelUnread(w http.ResponseWriter, r *http.Request) {
	h.mutateDMChannelPeer(w, r, h.markDMPeerUnread)
}

func (h *Handler) CloseDMChannel(w http.ResponseWriter, r *http.Request) {
	h.mutateDMChannelPeer(w, r, h.closeDMPeer)
}

func (h *Handler) PinDMSession(w http.ResponseWriter, r *http.Request) {
	h.mutateDMSessionPeer(w, r, func(ctx context.Context, workspaceID, userID string, peer dmPeerRef) error {
		return h.setDMPeerPin(ctx, workspaceID, userID, peer, true)
	})
}

func (h *Handler) UnpinDMSession(w http.ResponseWriter, r *http.Request) {
	h.mutateDMSessionPeer(w, r, func(ctx context.Context, workspaceID, userID string, peer dmPeerRef) error {
		return h.setDMPeerPin(ctx, workspaceID, userID, peer, false)
	})
}

func (h *Handler) MuteDMSession(w http.ResponseWriter, r *http.Request) {
	h.mutateDMSessionPeer(w, r, func(ctx context.Context, workspaceID, userID string, peer dmPeerRef) error {
		return h.setDMPeerMute(ctx, workspaceID, userID, peer, true)
	})
}

func (h *Handler) UnmuteDMSession(w http.ResponseWriter, r *http.Request) {
	h.mutateDMSessionPeer(w, r, func(ctx context.Context, workspaceID, userID string, peer dmPeerRef) error {
		return h.setDMPeerMute(ctx, workspaceID, userID, peer, false)
	})
}

func (h *Handler) MarkDMSessionUnread(w http.ResponseWriter, r *http.Request) {
	h.mutateDMSessionPeer(w, r, h.markDMPeerUnread)
}

func (h *Handler) CloseDMSession(w http.ResponseWriter, r *http.Request) {
	h.mutateDMSessionPeer(w, r, h.closeDMPeer)
}

// ListDirectMessages (GET /api/dm) is the sole data source for the DM section.
// It returns visible kind='dm' channels the caller is in, filters agent peers by
// accessibility, sorts by recency, and paginates. Legacy chat_sessions are not
// a visible source; they are reserved for migration/backfill only.
func (h *Handler) ListDirectMessages(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())

	// Mirror ListChatSessions: drop sessions/DMs whose agent the caller can no
	// longer access (e.g. role downgrade on a private agent).
	member, ok := h.workspaceMember(w, r, workspaceID)
	if !ok {
		return
	}
	actorType, actorID := h.resolveActor(r, userID, workspaceID)
	allowed, ok := h.accessibleAgentIDs(r.Context(), workspaceID, actorType, actorID, member.Role)
	if !ok {
		writeError(w, http.StatusInternalServerError, "failed to resolve agent access")
		return
	}

	items := []DMItem{}
	items = append(items, h.listDMChannels(r.Context(), workspaceID, userID, allowed)...)
	items = append(items, h.listSupervisedAgentDMChannels(r.Context(), workspaceID, userID)...)
	sort.SliceStable(items, func(i, j int) bool { return preferDMItem(items[i], items[j]) })

	limit, offset := dmPageParams(r)
	total := len(items)
	if offset > total {
		offset = total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	writeJSON(w, http.StatusOK, items[offset:end])
}

func dmPageParams(r *http.Request) (limit, offset int) {
	limit = dmDefaultLimit
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
			if limit > dmMaxLimit {
				limit = dmMaxLimit
			}
		}
	}
	if v := r.URL.Query().Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			offset = n
		}
	}
	return limit, offset
}

func preferDMItem(a, b DMItem) bool {
	if a.PinnedAt != nil || b.PinnedAt != nil {
		if a.PinnedAt == nil {
			return false
		}
		if b.PinnedAt == nil {
			return true
		}
		if *a.PinnedAt != *b.PinnedAt {
			return *a.PinnedAt > *b.PinnedAt
		}
	}
	if a.UpdatedAt != b.UpdatedAt {
		return a.UpdatedAt > b.UpdatedAt
	}
	return a.Source < b.Source
}

// listDMChannels returns the caller's kind='dm' channels as DM items, resolving
// the peer via a lateral that picks the single non-caller member, plus last
// message and unread count (same accounting as ListChannels).
func (h *Handler) listDMChannels(ctx context.Context, workspaceID, userID string, allowed map[string]struct{}) []DMItem {
	uid := parseUUID(userID)
	rows, err := h.DB.Query(ctx, `
		SELECT ch.id, ch.updated_at,
		       peer.member_type, peer.member_id,
		       COALESCE(NULLIF(u.display_name, ''), u.name, u.email, NULLIF(a.display_name, ''), a.name, '') AS peer_name,
		       a.avatar_url AS peer_avatar,
		       a.archived_at AS peer_archived_at,
		       lm.author_type, lm.author_name, lm.content, lm.parts, lm.created_at,
		       COALESCE(vcm.main_unread_count, 0)::int AS real_unread,
		       state.pinned_at, state.manual_unread_at, COALESCE(vcm.muted_at, state.muted_at),
		       (COALESCE(vcm.mention_unread_count, 0) > 0) AS has_mention,
		       NULLIF(COALESCE(vcm.last_read_seq, cr.last_read_seq, 0), 0)::bigint,
		       cs.runtime_token_stats
		FROM channel ch
		JOIN channel_member cm ON cm.channel_id = ch.id AND cm.member_type = 'user' AND cm.member_id = $2
		JOIN conversation conv ON conv.channel_id = ch.id
		LEFT JOIN conversation_member vcm
		  ON vcm.conversation_id = conv.id
		 AND vcm.member_type = 'user'
		 AND vcm.member_id = $2
		JOIN LATERAL (
			SELECT member_type, member_id
			FROM channel_member m2
			WHERE m2.channel_id = ch.id AND NOT (m2.member_type = 'user' AND m2.member_id = $2)
			ORDER BY m2.created_at ASC
			LIMIT 1
		) peer ON true
		LEFT JOIN "user" u ON peer.member_type = 'user' AND u.id = peer.member_id
		LEFT JOIN agent a ON peer.member_type = 'agent' AND a.id = peer.member_id
		LEFT JOIN channel_agent_session cas ON cas.channel_id = ch.id AND peer.member_type = 'agent' AND cas.agent_id = peer.member_id
		LEFT JOIN chat_session cs ON cs.id = cas.chat_session_id
		LEFT JOIN LATERAL (
			SELECT author_type, author_name, content, parts, created_at
			FROM channel_message m WHERE m.channel_id = ch.id
			ORDER BY m.seq DESC LIMIT 1
		) lm ON true
		LEFT JOIN channel_read cr ON cr.channel_id = ch.id AND cr.user_id = $2
		LEFT JOIN dm_peer_state state
		  ON state.workspace_id = ch.workspace_id
		 AND state.user_id = $2
		 AND state.peer_type = peer.member_type
		 AND state.peer_id = peer.member_id
		WHERE ch.workspace_id = $1 AND ch.kind = 'dm' AND COALESCE(vcm.closed_at, state.hidden_at) IS NULL
		ORDER BY ch.updated_at DESC`, parseUUID(workspaceID), uid)
	if err != nil {
		slog.Warn("list dm channels failed", "workspace", workspaceID, "error", err)
		return nil
	}
	defer rows.Close()
	out := []DMItem{}
	for rows.Next() {
		var id, peerID pgtype.UUID
		var updatedAt, lastAt, pinnedAt, manualUnreadAt, mutedAt, peerArchivedAt pgtype.Timestamptz
		var peerType, peerName string
		var peerAvatar, lastType, lastName, lastContent pgtype.Text
		var lastParts, runtimeStatsRaw []byte
		var unread int
		var hasMention bool
		var lastReadSeq *int64
		if err := rows.Scan(&id, &updatedAt, &peerType, &peerID, &peerName, &peerAvatar, &peerArchivedAt,
			&lastType, &lastName, &lastContent, &lastParts, &lastAt, &unread, &pinnedAt, &manualUnreadAt, &mutedAt, &hasMention, &lastReadSeq, &runtimeStatsRaw); err != nil {
			continue
		}
		if peerType == "agent" {
			if _, okAgent := allowed[uuidToString(peerID)]; !okAgent {
				continue
			}
		}
		item := DMItem{
			ID:             uuidToString(id),
			Source:         dmSourceChannel,
			Mode:           "direct",
			Peer:           DMPeer{Type: peerType, ID: uuidToString(peerID), Name: peerName, AvatarURL: textToPtr(peerAvatar), Archived: peerArchivedAt.Valid},
			Unread:         unread,
			RealUnread:     unread,
			ManuallyUnread: manualUnreadAt.Valid,
			PinnedAt:       timestampToPtr(pinnedAt),
			MutedAt:        timestampToPtr(mutedAt),
			Muted:          mutedAt.Valid,
			HasMention:     hasMention,
			LastReadSeq:    lastReadSeq,
			UpdatedAt:      timestampToString(updatedAt),
		}
		if item.ManuallyUnread && item.Unread == 0 {
			item.Unread = 1
		}
		if len(runtimeStatsRaw) > 0 {
			var stats protocol.RuntimeTokenStats
			if json.Unmarshal(runtimeStatsRaw, &stats) == nil {
				item.RuntimeStats = &stats
			}
		}
		if lastContent.Valid {
			item.LastMessage = channelLastMessage(lastType.String, lastName.String, lastContent.String, lastParts, lastAt)
		}
		out = append(out, item)
	}
	return out
}

// listSupervisedAgentDMChannels returns agent-to-agent DMs for which the caller
// owns at least one participant. The owner deliberately is not a channel
// member: this is a read-only supervision projection, not an invitation to
// speak as a third participant. Viewer list prefs (pin/mute/unread/close) are
// keyed by channel in dm_peer_state (peer_type='channel').
func (h *Handler) listSupervisedAgentDMChannels(ctx context.Context, workspaceID, userID string) []DMItem {
	uid := parseUUID(userID)
	rows, err := h.DB.Query(ctx, `
		SELECT ch.id, ch.updated_at,
		       lm.author_type, lm.author_name, lm.content, lm.parts, lm.created_at,
		       COALESCE(uc.cnt, 0) AS real_unread,
		       NULLIF(COALESCE(cr.last_read_seq, 0), 0)::bigint,
		       COALESCE(hm.has_mention, false),
		       state.pinned_at, state.manual_unread_at, state.muted_at
		FROM channel ch
		LEFT JOIN LATERAL (
			SELECT author_type, author_name, content, parts, created_at
			FROM channel_message message
			WHERE message.channel_id = ch.id
			ORDER BY message.seq DESC
			LIMIT 1
		) lm ON true
		LEFT JOIN channel_read cr ON cr.channel_id = ch.id AND cr.user_id = $2
		LEFT JOIN LATERAL (
			SELECT count(*) AS cnt FROM channel_message m
			WHERE m.channel_id = ch.id
			  AND m.seq > COALESCE(cr.last_read_seq, 0)
			  AND m.author_type <> 'system'
			  AND m.thread_root_message_id IS NULL
			  AND m.deleted_at IS NULL
		) uc ON true
		LEFT JOIN LATERAL (
			SELECT EXISTS (
				SELECT 1 FROM channel_message m
				WHERE m.channel_id = ch.id
				  AND m.seq > COALESCE(cr.last_read_seq, 0)
				  AND m.deleted_at IS NULL
				  AND (m.content ILIKE '%@' || (
				    SELECT name FROM "user" WHERE id = $2
				  ) || '%' OR m.parts::text ILIKE '%mention://' || $2::text || '%')
			) AS has_mention
		) hm ON true
		LEFT JOIN dm_peer_state state
		  ON state.workspace_id = ch.workspace_id
		 AND state.user_id = $2
		 AND state.peer_type = 'channel'
		 AND state.peer_id = ch.id
		WHERE ch.workspace_id = $1
		  AND ch.kind = 'dm'
		  AND state.hidden_at IS NULL
		  AND (
		    SELECT count(*)
		    FROM channel_member member
		    WHERE member.channel_id = ch.id
		      AND member.workspace_id = ch.workspace_id
		      AND member.member_type = 'agent'
		  ) = 2
		  AND NOT EXISTS (
		    SELECT 1
		    FROM channel_member member
		    WHERE member.channel_id = ch.id
		      AND member.workspace_id = ch.workspace_id
		      AND member.member_type <> 'agent'
		  )
		  AND EXISTS (
		    SELECT 1
		    FROM channel_member member
		    JOIN agent owned
		      ON owned.id = member.member_id
		     AND owned.workspace_id = member.workspace_id
		    WHERE member.channel_id = ch.id
		      AND member.workspace_id = ch.workspace_id
		      AND member.member_type = 'agent'
		      AND owned.owner_id = $2
		  )
		ORDER BY ch.updated_at DESC`,
		parseUUID(workspaceID), uid)
	if err != nil {
		slog.Warn("list supervised agent dm channels failed", "workspace", workspaceID, "user", userID, "error", err)
		return nil
	}
	type supervisedDMRow struct {
		channelID                                            pgtype.UUID
		updatedAt, lastAt, pinnedAt, manualUnreadAt, mutedAt pgtype.Timestamptz
		lastType, lastName, lastContent                      pgtype.Text
		lastParts                                            []byte
		unread                                               int
		lastReadSeq                                          *int64
		hasMention                                           bool
	}
	// Scan every row into memory and close the cursor before issuing the
	// participant query. This keeps bounded connection pools safe while also
	// avoiding one participant query for every visible supervised DM.
	var scanned []supervisedDMRow
	for rows.Next() {
		var row supervisedDMRow
		if err := rows.Scan(
			&row.channelID, &row.updatedAt,
			&row.lastType, &row.lastName, &row.lastContent, &row.lastParts, &row.lastAt,
			&row.unread, &row.lastReadSeq, &row.hasMention,
			&row.pinnedAt, &row.manualUnreadAt, &row.mutedAt,
		); err != nil {
			continue
		}
		scanned = append(scanned, row)
	}
	rows.Close()

	channelIDs := make([]pgtype.UUID, 0, len(scanned))
	for _, row := range scanned {
		channelIDs = append(channelIDs, row.channelID)
	}
	participantsByChannel := h.agentDMParticipantsForChannels(ctx, parseUUID(workspaceID), channelIDs)

	out := []DMItem{}
	for _, row := range scanned {
		participants := participantsByChannel[uuidToString(row.channelID)]
		if len(participants) != 2 {
			continue
		}
		item := DMItem{
			ID:             uuidToString(row.channelID),
			Source:         dmSourceChannel,
			Mode:           "agent_pair",
			Peer:           participants[0],
			Participants:   participants,
			Supervised:     true,
			Unread:         row.unread,
			RealUnread:     row.unread,
			ManuallyUnread: row.manualUnreadAt.Valid,
			HasMention:     row.hasMention,
			LastReadSeq:    row.lastReadSeq,
			PinnedAt:       timestampToPtr(row.pinnedAt),
			MutedAt:        timestampToPtr(row.mutedAt),
			Muted:          row.mutedAt.Valid,
			UpdatedAt:      timestampToString(row.updatedAt),
		}
		if item.ManuallyUnread && item.Unread == 0 {
			item.Unread = 1
		}
		if row.lastContent.Valid {
			item.LastMessage = channelLastMessage(
				row.lastType.String, row.lastName.String, row.lastContent.String, row.lastParts, row.lastAt,
			)
		}
		out = append(out, item)
	}
	return out
}

func (h *Handler) agentDMParticipantsForChannels(ctx context.Context, workspaceID pgtype.UUID, channelIDs []pgtype.UUID) map[string][]DMPeer {
	participantsByChannel := make(map[string][]DMPeer, len(channelIDs))
	if len(channelIDs) == 0 {
		return participantsByChannel
	}

	rows, err := h.DB.Query(ctx, `
		SELECT member.channel_id,
		       agent.id,
		       COALESCE(NULLIF(agent.display_name, ''), agent.name),
		       agent.avatar_url
		FROM channel_member member
		JOIN agent
		  ON agent.id = member.member_id
		 AND agent.workspace_id = member.workspace_id
		WHERE member.workspace_id = $1
		  AND member.channel_id = ANY($2::uuid[])
		  AND member.member_type = 'agent'
		ORDER BY member.channel_id, agent.id::text`,
		workspaceID, channelIDs)
	if err != nil {
		return participantsByChannel
	}
	defer rows.Close()

	for rows.Next() {
		var channelID, agentID pgtype.UUID
		var name string
		var avatarURL pgtype.Text
		if rows.Scan(&channelID, &agentID, &name, &avatarURL) != nil {
			continue
		}
		key := uuidToString(channelID)
		participantsByChannel[key] = append(participantsByChannel[key], DMPeer{
			Type:      "agent",
			ID:        uuidToString(agentID),
			Name:      name,
			AvatarURL: textToPtr(avatarURL),
		})
	}
	return participantsByChannel
}

// channelAgentMembers loads every non-archived Agent member of a channel as a
// full db.Agent. Canonical Message delivery uses it to resolve normal-channel
// recipients; it is not a task dispatch helper.
func (h *Handler) channelAgentMembers(ctx context.Context, workspaceID, channelID string) []db.Agent {
	rows, err := h.DB.Query(ctx, `
		SELECT a.id, a.workspace_id, a.name, a.avatar_url, a.runtime_mode, a.runtime_config, a.status,
		       a.max_concurrent_tasks, a.owner_id, a.created_at, a.updated_at, a.description, a.runtime_id,
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
		if err := rows.Scan(&a.ID, &a.WorkspaceID, &a.Name, &a.AvatarUrl, &a.RuntimeMode, &a.RuntimeConfig, &a.Status, &a.MaxConcurrentTasks, &a.OwnerID, &a.CreatedAt, &a.UpdatedAt, &a.Description, &a.RuntimeID, &a.Instructions, &a.ArchivedAt, &a.DisplayName, &a.Model, &a.ThinkingLevel); err != nil {
			continue
		}
		out = append(out, a)
	}
	return out
}
