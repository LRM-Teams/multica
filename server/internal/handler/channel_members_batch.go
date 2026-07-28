package handler

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// AddChannelMembersRequest is the batch form of AddChannelMemberRequest: invite
// several members/agents to a channel in one call.
type AddChannelMembersRequest struct {
	Members []AddChannelMemberRequest `json:"members"`
}

// AddChannelMembers adds many members to a group channel at once. Entries with
// a bad type or malformed id are skipped; targets must pass the same workspace
// and private agent visibility checks as single-member invites.
// Already-present members are no-ops (ON CONFLICT DO NOTHING). One
// channel-updated event fires for the whole batch.
func (h *Handler) AddChannelMembers(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	channelID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "channelId"), "channel id")
	if !ok {
		return
	}
	var req AddChannelMembersRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if !h.requireChannelUserMember(w, r.Context(), workspaceID, channelID, parseUUID(userID)) {
		return
	}
	if !h.requireChannelAgentCallerMember(w, r, workspaceID, channelID, userID) {
		return
	}
	if !h.requireGroupChannel(w, r.Context(), workspaceID, channelID) {
		return
	}
	if !h.requireChannelWritable(w, r.Context(), workspaceID, channelID) {
		return
	}
	if !h.requireChannelNotSystem(w, r.Context(), workspaceID, channelID) {
		return
	}

	types := make([]string, 0, len(req.Members))
	ids := make([]string, 0, len(req.Members))
	for _, m := range req.Members {
		if m.MemberType != "user" && m.MemberType != "agent" {
			continue
		}
		memberID, err := util.ParseUUID(m.MemberID)
		if err != nil {
			continue
		}
		if !h.validateChannelMemberTarget(w, r, workspaceID, channelID, m.MemberType, memberID) {
			return
		}
		types = append(types, m.MemberType)
		ids = append(ids, m.MemberID)
	}
	if len(ids) == 0 {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		return
	}
	actor := channelMemberUserActor(parseUUID(userID))
	if err := validateChannelMemberActorWithExec(r.Context(), h.DB, workspaceID, actor); err != nil {
		slog.Warn("channel members batch: validate actor failed", "workspace", workspaceID, "actor", userID, "error", err)
		writeError(w, http.StatusForbidden, "channel member actor is not available")
		return
	}

	// Insert every valid target in one statement. The EXISTS guards keep us from
	// adding ids that aren't a workspace member / workspace agent.
	rows, err := h.DB.Query(r.Context(), `
		INSERT INTO channel_member (
		  channel_id, workspace_id, member_type, member_id,
		  added_by_type, added_by_id, join_source
		)
		SELECT $1, $2, t.mt, t.mid::uuid, $5, $6, 'manual'
		FROM unnest($3::text[], $4::text[]) AS t(mt, mid)
		WHERE (t.mt = 'user'  AND EXISTS (SELECT 1 FROM member m WHERE m.workspace_id = $2 AND m.user_id = t.mid::uuid))
		   OR (t.mt = 'agent' AND EXISTS (SELECT 1 FROM agent  a WHERE a.workspace_id = $2 AND a.id      = t.mid::uuid))
		ON CONFLICT DO NOTHING
		RETURNING member_type, member_id, generation_id`,
		channelID, parseUUID(workspaceID), types, ids, actor.Type, actor.ID,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to add channel members")
		return
	}
	defer rows.Close()
	type insertedMember struct {
		memberType   string
		memberID     pgtype.UUID
		generationID pgtype.UUID
	}
	inserted := []insertedMember{}
	for rows.Next() {
		var member insertedMember
		if err := rows.Scan(&member.memberType, &member.memberID, &member.generationID); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to read added channel members")
			return
		}
		inserted = append(inserted, member)
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read added channel members")
		return
	}
	rows.Close()

	h.publish(protocol.EventChannelUpdated, workspaceID, "member", userID, map[string]any{"id": uuidToString(channelID)})
	for _, member := range inserted {
		if member.memberType == "user" {
			h.emitChannelMemberSystemEvent(r.Context(), workspaceID, channelID, channelMemberAddedEvent, actor.Type, actor.ID, member.memberType, member.memberID)
			continue
		}
		if err := h.publishChannelOnboardingSystemMessageForGeneration(r.Context(), member.generationID); err != nil {
			// The canonical system row remains durable and the server-side
			// publication worker will retry it independently of the target runtime.
			slog.Warn("channel members batch: publish agent membership system event failed", "channel", uuidToString(channelID), "agent", uuidToString(member.memberID), "generation", uuidToString(member.generationID), "error", err)
			continue
		}
	}
	writeJSON(w, http.StatusCreated, map[string]string{"status": "ok"})
}
