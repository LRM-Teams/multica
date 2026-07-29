package handler

import (
	"net/http"
)

// AddChannelMembersRequest is the batch form of AddChannelMemberRequest: invite
// several members/agents to a channel in one call.
type AddChannelMembersRequest struct {
	Members []AddChannelMemberRequest `json:"members"`
}

// AddChannelMembers is the human transport adapter. Authentication and parsing
// stay transport-specific; authorization and mutation use the same
// principal-neutral service as the dedicated agent route.
func (h *Handler) AddChannelMembers(w http.ResponseWriter, r *http.Request) {
	if rejectAgentOnHumanRoute(w, r, "AddChannelMembers") {
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	h.addChannelMembersAdapter(w, r, humanMemberManagementActor(workspaceID, userID))
}
