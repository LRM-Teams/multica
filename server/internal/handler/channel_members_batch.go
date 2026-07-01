package handler

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
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
	if !h.requireGroupChannel(w, r.Context(), workspaceID, channelID) {
		return
	}
	if !h.requireChannelWritable(w, r.Context(), workspaceID, channelID) {
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
		if !h.validateChannelMemberTarget(w, r, workspaceID, m.MemberType, memberID) {
			return
		}
		types = append(types, m.MemberType)
		ids = append(ids, m.MemberID)
	}
	if len(ids) == 0 {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		return
	}

	// Insert every valid target in one statement. The EXISTS guards keep us from
	// adding ids that aren't a workspace member / workspace agent.
	if _, err := h.DB.Exec(r.Context(), `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		SELECT $1, $2, t.mt, t.mid::uuid
		FROM unnest($3::text[], $4::text[]) AS t(mt, mid)
		WHERE (t.mt = 'user'  AND EXISTS (SELECT 1 FROM member m WHERE m.workspace_id = $2 AND m.user_id = t.mid::uuid))
		   OR (t.mt = 'agent' AND EXISTS (SELECT 1 FROM agent  a WHERE a.workspace_id = $2 AND a.id      = t.mid::uuid))
		ON CONFLICT DO NOTHING`,
		channelID, parseUUID(workspaceID), types, ids,
	); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to add channel members")
		return
	}

	h.publish(protocol.EventChannelUpdated, workspaceID, "member", userID, map[string]any{"id": uuidToString(channelID)})
	writeJSON(w, http.StatusCreated, map[string]string{"status": "ok"})
}
