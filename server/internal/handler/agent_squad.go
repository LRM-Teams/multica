package handler

import (
	"net/http"
)

// AgentSquadMemberSetRole — PATCH /api/agent/squads/{id}/members/role
// Squad product surface was removed (410 on human routes). Agent dedicated
// path exists for CLI contract cutover; still fail-closed with 410 until a
// future squad runtime product re-lands with leader-authority checks.
func (h *Handler) AgentSquadMemberSetRole(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAgentPrincipal(w, r); !ok {
		return
	}
	h.SquadFeatureRemoved(w, r)
}
