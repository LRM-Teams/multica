package handler

import (
	"context"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// ActorIdentity is the stable actor snapshot exposed on Activity / Working
// surfaces so clients do not invent display fallbacks such as "Unknown Agent".
type ActorIdentity struct {
	ActorID     string  `json:"actor_id"`
	ActorType   string  `json:"actor_type"`
	DisplayName string  `json:"display_name"`
	AvatarURL   *string `json:"avatar_url,omitempty"`
	Handle      *string `json:"handle,omitempty"`
	Status      string  `json:"status"` // visible, hidden, or deleted
}

type actorIdentityResolver struct {
	agents map[string]db.Agent
	users  map[string]actorUserIdentity

	viewerActorType string
	viewerActorID   string
	viewerRole      string
}

type actorUserIdentity struct {
	ID          string
	Name        string
	DisplayName string
	AvatarURL   *string
}

func (h *Handler) newActorIdentityResolver(ctx context.Context, workspaceID, viewerActorType, viewerActorID, viewerRole string) actorIdentityResolver {
	return h.newActorIdentityResolverOpts(ctx, workspaceID, viewerActorType, viewerActorID, viewerRole, true)
}

// newAgentOnlyActorIdentityResolver loads workspace agents only. Snapshot
// rows are always agent-authored, so skipping the members query removes an
// avoidable round-trip on the presence hot path (LRM-1261).
func (h *Handler) newAgentOnlyActorIdentityResolver(ctx context.Context, workspaceID, viewerActorType, viewerActorID, viewerRole string) actorIdentityResolver {
	return h.newActorIdentityResolverOpts(ctx, workspaceID, viewerActorType, viewerActorID, viewerRole, false)
}

func (h *Handler) newActorIdentityResolverOpts(ctx context.Context, workspaceID, viewerActorType, viewerActorID, viewerRole string, includeUsers bool) actorIdentityResolver {
	resolver := actorIdentityResolver{
		agents:          map[string]db.Agent{},
		users:           map[string]actorUserIdentity{},
		viewerActorType: viewerActorType,
		viewerActorID:   viewerActorID,
		viewerRole:      viewerRole,
	}
	ws := parseUUID(workspaceID)
	if ws.Valid {
		if agents, err := h.Queries.ListAllAgents(ctx, ws); err == nil {
			for _, agent := range agents {
				resolver.agents[uuidToString(agent.ID)] = agent
			}
		}
		if includeUsers {
			resolver.users = h.listWorkspaceActorUsers(ctx, ws)
		}
	}
	return resolver
}

func (h *Handler) listWorkspaceActorUsers(ctx context.Context, workspaceID pgtype.UUID) map[string]actorUserIdentity {
	rows, err := h.DB.Query(ctx, `
		SELECT u.id, COALESCE(NULLIF(u.name, ''), ''), COALESCE(NULLIF(u.display_name, ''), ''), u.avatar_url
		FROM workspace_member wm
		JOIN "user" u ON u.id = wm.user_id
		WHERE wm.workspace_id = $1`, workspaceID)
	if err != nil {
		return map[string]actorUserIdentity{}
	}
	defer rows.Close()

	users := map[string]actorUserIdentity{}
	for rows.Next() {
		var id pgtype.UUID
		var name, displayName string
		var avatar pgtype.Text
		if err := rows.Scan(&id, &name, &displayName, &avatar); err != nil {
			continue
		}
		uid := uuidToString(id)
		users[uid] = actorUserIdentity{
			ID:          uid,
			Name:        strings.TrimSpace(name),
			DisplayName: strings.TrimSpace(displayName),
			AvatarURL:   textToPtr(avatar),
		}
	}
	return users
}

func (r actorIdentityResolver) resolve(actorType, actorID string) ActorIdentity {
	actorType = strings.TrimSpace(actorType)
	actorID = strings.TrimSpace(actorID)
	identity := ActorIdentity{ActorID: actorID, ActorType: actorType, Status: "deleted"}
	if actorID == "" {
		identity.DisplayName = deletedActorDisplayName(actorType)
		return identity
	}

	switch actorType {
	case "agent":
		agent, ok := r.agents[actorID]
		if !ok {
			identity.DisplayName = "Deleted agent"
			return identity
		}
		identity.Status = "visible"
		if agent.ArchivedAt.Valid {
			identity.Status = "deleted"
		}
		identity.DisplayName = nonEmptyAgentDisplayName(agent)
		identity.AvatarURL = &agent.AvatarUrl
		identity.Handle = optionalHandle(agent.Name)
		return identity
	case "member":
		user, ok := r.users[actorID]
		if !ok {
			identity.DisplayName = "Deleted member"
			return identity
		}
		identity.Status = "visible"
		identity.DisplayName = nonEmptyDisplayName(user.DisplayName, user.Name, "Deleted member")
		identity.AvatarURL = user.AvatarURL
		identity.Handle = optionalHandle(user.Name)
		return identity
	default:
		identity.DisplayName = deletedActorDisplayName(actorType)
		return identity
	}
}

func applyActorIdentityToTask(task *AgentTaskResponse, identity ActorIdentity) {
	task.ActorID = identity.ActorID
	task.ActorType = identity.ActorType
	task.DisplayName = identity.DisplayName
	task.AvatarURL = identity.AvatarURL
	task.Handle = identity.Handle
	task.ActorStatus = identity.Status
	task.Actor = &identity
}

func applyActorIdentityToFeedItem(item *AgentTaskFeedItem, identity ActorIdentity) {
	item.ActorID = identity.ActorID
	item.ActorType = identity.ActorType
	item.DisplayName = identity.DisplayName
	item.AvatarURL = identity.AvatarURL
	item.Handle = identity.Handle
	item.ActorStatus = identity.Status
	item.Actor = &identity
}

func nonEmptyAgentDisplayName(agent db.Agent) string {
	return nonEmptyDisplayName(agentDisplayName(agent), agent.Name, "Deleted agent")
}

func nonEmptyDisplayName(values ...string) string {
	fallback := "Deleted actor"
	if len(values) > 0 {
		fallback = values[len(values)-1]
		values = values[:len(values)-1]
	}
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return fallback
}

func optionalHandle(value string) *string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func deletedActorDisplayName(actorType string) string {
	switch actorType {
	case "agent":
		return "Deleted agent"
	case "member":
		return "Deleted member"
	default:
		return "Deleted actor"
	}
}
