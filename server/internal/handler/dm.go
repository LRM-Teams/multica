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

// Source discriminators for a unified DM list item. A 1-on-1 DM is either a
// kind='dm' channel (the new model, used for human↔human and freshly created
// human↔agent DMs) or a pre-existing standalone chat_session (the legacy
// human↔agent "Chat", soft-unified here so a user sees one DM list).
const (
	dmSourceChannel = "dm_channel"
	dmSourceLegacy  = "legacy_session"
)

// dmDefaultLimit / dmMaxLimit bound the unified DM page. The list is merged and
// deduped in memory across the two sources, so pagination is applied after the
// merge rather than per-source in SQL.
const (
	dmDefaultLimit = 50
	dmMaxLimit     = 100
)

type DMPeer struct {
	Type      string  `json:"type"` // "user" | "agent"
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	AvatarURL *string `json:"avatar_url,omitempty"`
}

// DMItem is one row in the unified DM list and the create-or-find response.
// Unread is a count for dm_channel items and 0/1 for legacy_session items
// (legacy sessions only track a per-session has-unread flag, not a count).
type DMItem struct {
	ID             string              `json:"id"` // dm channel id OR legacy chat_session id
	Source         string              `json:"source"`
	Peer           DMPeer              `json:"peer"`
	LastMessage    *ChannelLastMessage `json:"last_message,omitempty"`
	Unread         int                 `json:"unread"`
	RealUnread     int                 `json:"real_unread"`
	ManuallyUnread bool                `json:"manually_unread,omitempty"`
	PinnedAt       *string             `json:"pinned_at,omitempty"`
	MutedAt        *string             `json:"muted_at,omitempty"`
	Muted          bool                `json:"muted,omitempty"`
	UpdatedAt      string              `json:"updated_at"`
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

// CreateOrFindDirectMessage (POST /api/dm) returns the existing DM with the
// peer or creates one, idempotently. For an agent peer it is legacy-first: an
// existing dm channel wins, else an unbound legacy chat_session the caller owns
// with that agent is reused (so the same agent never shows twice), else a new
// dm channel is created. Human↔human always uses a dm channel.
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
	actorType, actorID := h.resolveActor(r, userID, workspaceID)
	if !h.canAccessPrivateAgent(r.Context(), agent, actorType, actorID, workspaceID) {
		writeError(w, http.StatusForbidden, "you do not have access to this agent")
		return
	}
	peer := DMPeer{Type: "agent", ID: uuidToString(agent.ID), Name: agent.Name, AvatarURL: textToPtr(agent.AvatarUrl)}

	// ① existing dm channel wins.
	canonical := dmCanonicalName("user", userID, "agent", uuidToString(agentID))
	if ch, found := h.findDMChannel(r.Context(), workspaceID, canonical); found {
		h.clearDMPeerHidden(r.Context(), workspaceID, userID, dmPeerRef{Type: "agent", ID: agentID})
		writeJSON(w, http.StatusOK, dmItemForChannel(ch, peer))
		return
	}
	// ② reuse an unbound legacy chat_session the caller owns with this agent.
	if sessionID, updatedAt, found := h.findUnboundLegacySession(r.Context(), workspaceID, userID, agentID); found {
		h.clearDMPeerHidden(r.Context(), workspaceID, userID, dmPeerRef{Type: "agent", ID: agentID})
		writeJSON(w, http.StatusOK, DMItem{ID: uuidToString(sessionID), Source: dmSourceLegacy, Peer: peer, UpdatedAt: updatedAt})
		return
	}
	// ③ otherwise create a fresh dm channel.
	ch, created := h.createDMChannel(r.Context(), w, workspaceID, userID, canonical, []dmMember{
		{memberType: "user", memberID: parseUUID(userID)},
		{memberType: "agent", memberID: agentID},
	})
	if !created {
		return
	}
	writeJSON(w, http.StatusCreated, dmItemForChannel(ch, peer))
}

type dmMember struct {
	memberType string
	memberID   pgtype.UUID
}

// findDMChannel looks up the dm channel for a canonical name. Returns false when
// none exists (or the row is somehow not kind='dm').
func (h *Handler) findDMChannel(ctx context.Context, workspaceID, canonical string) (ChannelResponse, bool) {
	row := h.DB.QueryRow(ctx, `SELECT id, workspace_id, name, description, lark_chat_id, created_by, created_at, updated_at, kind, archived_at, archived_by FROM channel WHERE workspace_id = $1 AND name = $2 AND kind = 'dm' LIMIT 1`, parseUUID(workspaceID), canonical)
	ch, err := scanChannel(row)
	return ch, err == nil
}

// findUnboundLegacySession finds the most recent standalone chat_session the
// caller created with an agent that is NOT bound to a channel (i.e. a genuine
// 1:1 "Chat", not a channel agent session). Used by legacy-first agent DM
// create-or-find so the same agent is never offered as two separate DMs.
func (h *Handler) findUnboundLegacySession(ctx context.Context, workspaceID, userID string, agentID pgtype.UUID) (pgtype.UUID, string, bool) {
	var id pgtype.UUID
	var updatedAt pgtype.Timestamptz
	err := h.DB.QueryRow(ctx, `
		SELECT id, updated_at FROM chat_session
		WHERE workspace_id = $1 AND creator_id = $2 AND agent_id = $3 AND status = 'active'
		  AND id NOT IN (SELECT chat_session_id FROM channel_agent_session)
		ORDER BY updated_at DESC
		LIMIT 1`, parseUUID(workspaceID), parseUUID(userID), agentID).Scan(&id, &updatedAt)
	if err != nil {
		return pgtype.UUID{}, "", false
	}
	return id, timestampToString(updatedAt), true
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
		writeError(w, http.StatusInternalServerError, "failed to create direct message")
		return ChannelResponse{}, false
	}
	defer tx.Rollback(ctx) // no-op once committed

	row := tx.QueryRow(ctx, `
		INSERT INTO channel (workspace_id, name, created_by, kind)
		VALUES ($1, $2, $3, 'dm')
		ON CONFLICT (workspace_id, name) DO NOTHING
		RETURNING id, workspace_id, name, description, lark_chat_id, created_by, created_at, updated_at, kind, archived_at, archived_by`,
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
		writeError(w, http.StatusInternalServerError, "failed to create direct message")
		return ChannelResponse{}, false
	}
	for _, m := range members {
		if _, err := tx.Exec(ctx, `
			INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT DO NOTHING`, parseUUID(ch.ID), parseUUID(workspaceID), m.memberType, m.memberID); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to create direct message")
			return ChannelResponse{}, false
		}
	}
	if err := tx.Commit(ctx); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create direct message")
		return ChannelResponse{}, false
	}
	h.publish(protocol.EventChannelUpdated, workspaceID, "member", creatorID, ch)
	return ch, true
}

// dmItemForChannel builds a create-or-find response item for a dm channel.
// last_message/unread are intentionally omitted here (the caller navigates into
// the DM and the unified list endpoint computes them) — keeping create-or-find
// a single write with no extra read.
func dmItemForChannel(ch ChannelResponse, peer DMPeer) DMItem {
	return DMItem{ID: ch.ID, Source: dmSourceChannel, Peer: peer, UpdatedAt: ch.UpdatedAt}
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
	if muted {
		_, err := h.DB.Exec(ctx, `
			INSERT INTO dm_peer_state (workspace_id, user_id, peer_type, peer_id, muted_at, updated_at)
			VALUES ($1, $2, $3, $4, now(), now())
			ON CONFLICT (workspace_id, user_id, peer_type, peer_id)
			DO UPDATE SET muted_at = now(), updated_at = now()`,
			parseUUID(workspaceID), parseUUID(userID), peer.Type, peer.ID)
		return err
	}
	_, err := h.DB.Exec(ctx, `
		UPDATE dm_peer_state
		SET muted_at = NULL, updated_at = now()
		WHERE workspace_id = $1 AND user_id = $2 AND peer_type = $3 AND peer_id = $4`,
		parseUUID(workspaceID), parseUUID(userID), peer.Type, peer.ID)
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
		INSERT INTO dm_peer_state (workspace_id, user_id, peer_type, peer_id, hidden_at, manual_unread_at, updated_at)
		VALUES ($1, $2, $3, $4, now(), NULL, now())
		ON CONFLICT (workspace_id, user_id, peer_type, peer_id)
		DO UPDATE SET hidden_at = now(), manual_unread_at = NULL, updated_at = now()`,
		parseUUID(workspaceID), parseUUID(userID), peer.Type, peer.ID)
	return err
}

func (h *Handler) clearDMPeerHidden(ctx context.Context, workspaceID, userID string, peer dmPeerRef) {
	if _, err := h.DB.Exec(ctx, `
		UPDATE dm_peer_state
		SET hidden_at = NULL, updated_at = now()
		WHERE workspace_id = $1 AND user_id = $2 AND peer_type = $3 AND peer_id = $4 AND hidden_at IS NOT NULL`,
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
		)
		UPDATE dm_peer_state s
		SET hidden_at = NULL, updated_at = now()
		FROM user_peers p
		WHERE s.workspace_id = $2
		  AND s.user_id = p.user_id
		  AND s.peer_type = p.peer_type
		  AND s.peer_id = p.peer_id
		  AND s.hidden_at IS NOT NULL`, channelID, parseUUID(workspaceID)); err != nil {
		slog.Warn("dm: clear channel hidden state failed", "workspace", workspaceID, "channel", uuidToString(channelID), "error", err)
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
	peer, ok := h.resolveDMChannelPeer(w, r.Context(), workspaceID, userID, channelID)
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
// It merges kind='dm' channels the caller is in with the caller's unbound
// legacy chat_sessions, dedupes by peer (a peer never appears twice), filters
// agent peers by accessibility, sorts by recency, and paginates.
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
	items = append(items, h.listLegacyDMSessions(r.Context(), workspaceID, userID, allowed)...)

	// Dedupe by peer, keeping pinned entries first and then the freshest row. Legacy-first
	// create-or-find normally keeps a peer in exactly one source, but a DM
	// channel and an old session for the same agent can coexist if the channel
	// was created before this unification.
	byPeer := map[string]int{}
	deduped := []DMItem{}
	for _, it := range items {
		key := it.Peer.Type + ":" + it.Peer.ID
		if idx, seen := byPeer[key]; seen {
			if preferDMItem(it, deduped[idx]) {
				deduped[idx] = it
			}
			continue
		}
		byPeer[key] = len(deduped)
		deduped = append(deduped, it)
	}
	sort.SliceStable(deduped, func(i, j int) bool { return preferDMItem(deduped[i], deduped[j]) })

	limit, offset := dmPageParams(r)
	total := len(deduped)
	if offset > total {
		offset = total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	writeJSON(w, http.StatusOK, deduped[offset:end])
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
		       COALESCE(u.name, u.email, a.name, '') AS peer_name,
		       a.avatar_url AS peer_avatar,
		       lm.author_type, lm.author_name, lm.content, lm.created_at,
		       COALESCE(uc.cnt, 0) AS real_unread,
		       state.pinned_at, state.manual_unread_at, state.muted_at
		FROM channel ch
		JOIN channel_member cm ON cm.channel_id = ch.id AND cm.member_type = 'user' AND cm.member_id = $2
		JOIN LATERAL (
			SELECT member_type, member_id
			FROM channel_member m2
			WHERE m2.channel_id = ch.id AND NOT (m2.member_type = 'user' AND m2.member_id = $2)
			ORDER BY m2.created_at ASC
			LIMIT 1
		) peer ON true
		LEFT JOIN "user" u ON peer.member_type = 'user' AND u.id = peer.member_id
		LEFT JOIN agent a ON peer.member_type = 'agent' AND a.id = peer.member_id
		LEFT JOIN LATERAL (
			SELECT author_type, author_name, content, created_at
			FROM channel_message m WHERE m.channel_id = ch.id
			ORDER BY m.created_at DESC LIMIT 1
		) lm ON true
		LEFT JOIN channel_read cr ON cr.channel_id = ch.id AND cr.user_id = $2
		LEFT JOIN LATERAL (
			SELECT count(*) AS cnt FROM channel_message m
			WHERE m.channel_id = ch.id
			  AND m.created_at > COALESCE(cr.last_read_at, '-infinity'::timestamptz)
			  AND NOT (m.author_type = 'user' AND m.author_id = $2)
		) uc ON true
		LEFT JOIN dm_peer_state state
		  ON state.workspace_id = ch.workspace_id
		 AND state.user_id = $2
		 AND state.peer_type = peer.member_type
		 AND state.peer_id = peer.member_id
		WHERE ch.workspace_id = $1 AND ch.kind = 'dm' AND state.hidden_at IS NULL
		ORDER BY ch.updated_at DESC`, parseUUID(workspaceID), uid)
	if err != nil {
		slog.Warn("list dm channels failed", "workspace", workspaceID, "error", err)
		return nil
	}
	defer rows.Close()
	out := []DMItem{}
	for rows.Next() {
		var id, peerID pgtype.UUID
		var updatedAt, lastAt, pinnedAt, manualUnreadAt, mutedAt pgtype.Timestamptz
		var peerType, peerName string
		var peerAvatar, lastType, lastName, lastContent pgtype.Text
		var unread int
		if err := rows.Scan(&id, &updatedAt, &peerType, &peerID, &peerName, &peerAvatar,
			&lastType, &lastName, &lastContent, &lastAt, &unread, &pinnedAt, &manualUnreadAt, &mutedAt); err != nil {
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
			Peer:           DMPeer{Type: peerType, ID: uuidToString(peerID), Name: peerName, AvatarURL: textToPtr(peerAvatar)},
			Unread:         unread,
			RealUnread:     unread,
			ManuallyUnread: manualUnreadAt.Valid,
			PinnedAt:       timestampToPtr(pinnedAt),
			MutedAt:        timestampToPtr(mutedAt),
			Muted:          mutedAt.Valid,
			UpdatedAt:      timestampToString(updatedAt),
		}
		if item.ManuallyUnread && item.Unread == 0 {
			item.Unread = 1
		}
		if lastContent.Valid {
			item.LastMessage = &ChannelLastMessage{
				AuthorType: lastType.String, AuthorName: lastName.String,
				Content: lastContent.String, CreatedAt: timestampToString(lastAt),
			}
		}
		out = append(out, item)
	}
	return out
}

// listLegacyDMSessions returns the caller's standalone (non-channel-bound)
// chat_sessions as DM items. Unread maps the per-session has-unread flag to
// 0/1; the agent peer's name/avatar and last message come from joins.
func (h *Handler) listLegacyDMSessions(ctx context.Context, workspaceID, userID string, allowed map[string]struct{}) []DMItem {
	rows, err := h.DB.Query(ctx, `
		SELECT cs.id, cs.agent_id, cs.updated_at,
		       a.name, a.avatar_url,
		       lm.role, lm.content, lm.created_at,
		       (cs.unread_since IS NOT NULL)::bool AS has_unread,
		       state.pinned_at, state.manual_unread_at, state.muted_at
		FROM chat_session cs
		JOIN agent a ON a.id = cs.agent_id
		LEFT JOIN LATERAL (
			SELECT role, content, created_at
			FROM chat_message m WHERE m.chat_session_id = cs.id
			ORDER BY m.created_at DESC LIMIT 1
		) lm ON true
		LEFT JOIN dm_peer_state state
		  ON state.workspace_id = cs.workspace_id
		 AND state.user_id = cs.creator_id
		 AND state.peer_type = 'agent'
		 AND state.peer_id = cs.agent_id
		WHERE cs.workspace_id = $1 AND cs.creator_id = $2 AND cs.status = 'active'
		  AND state.hidden_at IS NULL
		  AND cs.id NOT IN (SELECT chat_session_id FROM channel_agent_session)
		ORDER BY cs.updated_at DESC`, parseUUID(workspaceID), parseUUID(userID))
	if err != nil {
		slog.Warn("list legacy dm sessions failed", "workspace", workspaceID, "error", err)
		return nil
	}
	defer rows.Close()
	out := []DMItem{}
	for rows.Next() {
		var id, agentID pgtype.UUID
		var updatedAt, lastAt, pinnedAt, manualUnreadAt, mutedAt pgtype.Timestamptz
		var agentName string
		var agentAvatar, lastRole, lastContent pgtype.Text
		var hasUnread bool
		if err := rows.Scan(&id, &agentID, &updatedAt, &agentName, &agentAvatar,
			&lastRole, &lastContent, &lastAt, &hasUnread, &pinnedAt, &manualUnreadAt, &mutedAt); err != nil {
			continue
		}
		if _, okAgent := allowed[uuidToString(agentID)]; !okAgent {
			continue
		}
		realUnread := 0
		if hasUnread {
			realUnread = 1
		}
		unread := realUnread
		if manualUnreadAt.Valid {
			unread = 1
		}
		item := DMItem{
			ID:             uuidToString(id),
			Source:         dmSourceLegacy,
			Peer:           DMPeer{Type: "agent", ID: uuidToString(agentID), Name: agentName, AvatarURL: textToPtr(agentAvatar)},
			Unread:         unread,
			RealUnread:     realUnread,
			ManuallyUnread: manualUnreadAt.Valid,
			PinnedAt:       timestampToPtr(pinnedAt),
			MutedAt:        timestampToPtr(mutedAt),
			Muted:          mutedAt.Valid,
			UpdatedAt:      timestampToString(updatedAt),
		}
		if lastContent.Valid {
			// chat_message has a role (user/assistant), not an author name —
			// map it to the unified author shape so the list renders uniformly.
			authorType, authorName := "user", "You"
			if lastRole.String == "assistant" {
				authorType, authorName = "agent", agentName
			}
			item.LastMessage = &ChannelLastMessage{
				AuthorType: authorType, AuthorName: authorName,
				Content: lastContent.String, CreatedAt: timestampToString(lastAt),
			}
		}
		out = append(out, item)
	}
	return out
}

// dispatchDMAgentReply dispatches a 1-on-1 DM's user message to the channel's
// agent peer (if any) without requiring an @-mention. Human↔human DMs have no
// agent member, so this is a no-op. The trigger-depth and self-trigger guards
// live in dispatchChannelAgentReply, so an agent's own reply never re-triggers.
func (h *Handler) dispatchDMAgentReply(ctx context.Context, ch ChannelResponse, trigger ChannelMessageResponse, initiatorUserID pgtype.UUID) {
	for _, agent := range h.channelAgentMembers(ctx, ch.WorkspaceID, ch.ID) {
		h.dispatchChannelAgentReply(ctx, ch, agent, trigger, initiatorUserID)
	}
}

// channelAgentMembers loads every (non-archived) agent member of a channel as a
// full db.Agent, regardless of @-mentions. Mirrors the agent load in
// channelMentionedAgents minus the mention filter; used by DM auto-dispatch.
func (h *Handler) channelAgentMembers(ctx context.Context, workspaceID, channelID string) []db.Agent {
	rows, err := h.DB.Query(ctx, `
		SELECT a.id, a.workspace_id, a.name, a.avatar_url, a.runtime_mode, a.runtime_config, a.visibility, a.status,
		       a.max_concurrent_tasks, a.owner_id, a.created_at, a.updated_at, a.description, a.runtime_id,
		       a.archived_at
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
		if err := rows.Scan(&a.ID, &a.WorkspaceID, &a.Name, &a.AvatarUrl, &a.RuntimeMode, &a.RuntimeConfig, &a.Visibility, &a.Status, &a.MaxConcurrentTasks, &a.OwnerID, &a.CreatedAt, &a.UpdatedAt, &a.Description, &a.RuntimeID, &a.ArchivedAt); err != nil {
			continue
		}
		out = append(out, a)
	}
	return out
}
