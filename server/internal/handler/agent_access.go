package handler

import (
	"context"

	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// canAccessAgentInternals gates the agent's internal/control surfaces (task
// #908 batch 2): run history, instructions/runtime config/custom args,
// diagnostic logs, memory growth, and cancelling its system-generated tasks.
// Existence/usage (chat, DM, @-mention, issue assignment, inviting to a
// channel, etc.) is unconditional for every workspace member as of #908 —
// this predicate is only for the surfaces Parker's principle (2026-07-30,
// #multica thread f83df812) keeps gated: "使用面放开；内部面和控制面归
// admin|owner" (usage widens; internal state and control actions stay
// admin-or-owner). Agent-to-agent traffic still bypasses, mirroring the
// historical A2A carve-out in the now-retired canAccessPrivateAgent.
func (h *Handler) canAccessAgentInternals(ctx context.Context, agent db.Agent, actorType, actorID, workspaceID string) bool {
	if actorType == "agent" {
		return true
	}
	if uuidToString(agent.OwnerID) == actorID {
		return true
	}
	member, err := h.getWorkspaceMember(ctx, actorID, workspaceID)
	if err != nil {
		return false
	}
	return roleAllowed(member.Role, "owner", "admin")
}

// memberAllowedForPrivateAgent is the pure predicate used by
// accessibleAgentIDs for the one remaining owner-only agent class
// (privateAgentOwnerOnly — Windy/Wendy) now that task #908 retired
// agent.visibility itself.
func memberAllowedForPrivateAgent(agent db.Agent, userID, role string) bool {
	if privateAgentOwnerOnly(agent) {
		return uuidToString(agent.OwnerID) == userID
	}
	if roleAllowed(role, "owner", "admin") {
		return true
	}
	return uuidToString(agent.OwnerID) == userID
}

func privateAgentOwnerOnly(agent db.Agent) bool {
	return isWindyAgentName(agentDisplayName(agent))
}

func (h *Handler) publishAgentVisibilityEvent(eventType, workspaceID, actorType, actorID string, agent db.Agent, payload any) {
	if privateAgentOwnerOnly(agent) {
		h.publishToUsers(eventType, workspaceID, actorType, actorID, []string{uuidToString(agent.OwnerID)}, payload)
		return
	}
	h.publish(eventType, workspaceID, actorType, actorID, payload)
}

// accessibleAgentIDs returns the set of agent IDs in the workspace the actor
// is allowed to see, for use by workspace-wide aggregation endpoints
// (run counts, activity histograms, task snapshots). Task #908 retired
// agent.visibility for usage/aggregate surfaces (Parker, #multica thread
// f83df812, 2026-07-30 18:31: "accessibleAgentIDs 不动，喂列表页聚合，全员
// 可见") — the only remaining owner-only class is Windy/Wendy
// (privateAgentOwnerOnly), not a general private-agent concept. Returns nil
// and false on error.
func (h *Handler) accessibleAgentIDs(ctx context.Context, workspaceID, actorType, actorID, role string) (map[string]struct{}, bool) {
	wsUUID, err := util.ParseUUID(workspaceID)
	if err != nil {
		return nil, false
	}
	agents, err := h.Queries.ListAllAgents(ctx, wsUUID)
	if err != nil {
		return nil, false
	}
	allowed := make(map[string]struct{}, len(agents))
	for _, a := range agents {
		if actorType == "member" && privateAgentOwnerOnly(a) {
			if !memberAllowedForPrivateAgent(a, actorID, role) {
				continue
			}
		}
		allowed[uuidToString(a.ID)] = struct{}{}
	}
	return allowed, true
}
