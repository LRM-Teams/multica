package handler

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/researchrun"
)

func (h *Handler) loadV6SessionListPreviews(ctx context.Context, workspaceID pgtype.UUID) map[string]struct {
	director *string
	members  []ResearchFleetPreviewMember
} {
	out := map[string]struct {
		director *string
		members  []ResearchFleetPreviewMember
	}{}
	if h.DB == nil {
		return out
	}
	rows, err := h.DB.Query(ctx, `
		SELECT s.id::text, a.director_agent_id::text,
		       COALESCE(ag.name, ''), COALESCE(ag.display_name, ''), ag.avatar_url
		FROM research_session s
		JOIN research_director_assignment a
		  ON a.id = s.current_director_assignment_id AND a.status = 'active'
		JOIN agent ag ON ag.id = a.director_agent_id AND ag.workspace_id = s.workspace_id
		WHERE s.workspace_id = $1 AND s.orchestrator_version = $2
	`, workspaceID, researchrun.OrchestratorVersionV6)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var sessionID, directorID, name, display string
		var avatar *string
		if err := rows.Scan(&sessionID, &directorID, &name, &display, &avatar); err != nil {
			continue
		}
		director := directorID
		out[sessionID] = struct {
			director *string
			members  []ResearchFleetPreviewMember
		}{
			director: &director,
			members: []ResearchFleetPreviewMember{{
				AgentID: directorID, Name: name, DisplayName: display, AvatarURL: avatar, Role: "director", IsLead: true,
			}},
		}
	}
	teamRows, err := h.DB.Query(ctx, `
		SELECT s.id::text, m.agent_id::text, COALESCE(ag.name, ''), COALESCE(ag.display_name, ''), ag.avatar_url
		FROM research_session s
		JOIN research_team_membership m ON m.session_id = s.id AND m.workspace_id = s.workspace_id AND m.state IN ('idle', 'working', 'offline')
		JOIN agent ag ON ag.id = m.agent_id AND ag.workspace_id = s.workspace_id
		WHERE s.workspace_id = $1 AND s.orchestrator_version = $2
		  AND m.agent_id <> (
		    SELECT a.director_agent_id FROM research_director_assignment a
		    WHERE a.id = s.current_director_assignment_id
		  )
		ORDER BY m.joined_at
	`, workspaceID, researchrun.OrchestratorVersionV6)
	if err != nil {
		return out
	}
	defer teamRows.Close()
	for teamRows.Next() {
		var sessionID, agentID, name, display string
		var avatar *string
		if err := teamRows.Scan(&sessionID, &agentID, &name, &display, &avatar); err != nil {
			continue
		}
		entry := out[sessionID]
		if len(entry.members) >= 5 {
			continue
		}
		entry.members = append(entry.members, ResearchFleetPreviewMember{
			AgentID: agentID, Name: name, DisplayName: display, AvatarURL: avatar, Role: "member",
		})
		out[sessionID] = entry
	}
	return out
}
