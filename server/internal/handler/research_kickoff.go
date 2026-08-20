package handler

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// seedResearchSessionKickoff fans out a visible starting graph + process cards
// so the canvas immediately shows the fleet standing up around the goal.
func (h *Handler) seedResearchSessionKickoff(
	ctx context.Context,
	workspaceID string,
	wsUUID pgtype.UUID,
	session db.ResearchSession,
	fleet db.ResearchFleet,
	members []db.ResearchFleetMember,
	userID string,
) {
	leadID := fleet.LeadAgentID
	goal, _, err := h.createResearchGraphNodeWithPassport(ctx, wsUUID, session.ID, db.CreateResearchGraphNodeParams{
		WorkspaceID:  wsUUID,
		SessionID:    session.ID,
		NodeType:     "goal",
		Title:        session.Title,
		Summary:      session.Goal,
		Status:       "active",
		ActorAgentID: leadID,
		Payload:      marshalJSONRaw(map[string]any{"phase": "kickoff"}),
	}, pgtype.UUID{}, "")
	if err != nil {
		slog.Warn("research kickoff goal node failed", "error", err)
		return
	}
	h.publishResearchGraph(workspaceID, "user", userID, session.ID, goal, nil)

	plan := buildResearchAdaptivePlan(session.Goal)
	for i, dim := range plan.Dimensions {
		node, _, nerr := h.createResearchGraphNodePublished(ctx, workspaceID, wsUUID, session.ID, "user", userID, db.CreateResearchGraphNodeParams{
			WorkspaceID:  wsUUID,
			SessionID:    session.ID,
			NodeType:     "subquestion",
			Title:        dim.Title,
			Summary:      dim.Summary,
			Status:       "active",
			ActorAgentID: leadID,
			Payload: marshalJSONRaw(map[string]any{
				"seed":             true,
				"index":            i,
				"phase":            "s1_plan",
				"dimension_family": dim.Family,
				"required":         dim.Required,
				"source_hints":     dim.SourceHints,
				"fine_domain":      plan.FineDomain,
				"coarse_domains":   plan.CoarseDomains,
				"delivery_like":    plan.DeliveryLike,
			}),
		}, goal.ID, "leads_to")
		if nerr != nil {
			slog.Warn("research kickoff subquestion failed", "error", nerr)
			continue
		}
		_ = node
	}

	activeCount := 0
	seenRole := map[string]bool{}
	uniqueMembers := make([]db.ResearchFleetMember, 0, len(members))
	for _, m := range members {
		if m.Status == "archived" || seenRole[m.Role] {
			continue
		}
		seenRole[m.Role] = true
		uniqueMembers = append(uniqueMembers, m)
	}
	for _, m := range uniqueMembers {
		activeCount++
		name, display := "", ""
		if agent, aerr := h.Queries.GetAgent(ctx, m.AgentID); aerr == nil {
			name = agent.Name
			display = agent.DisplayName
		}
		label := researchMemberLabel(m, name, display)
		brief := researchRoleKickoffBrief(m.Role)
		activity, _, aerr := h.createResearchGraphNodePublished(ctx, workspaceID, wsUUID, session.ID, "user", userID, db.CreateResearchGraphNodeParams{
			WorkspaceID:  wsUUID,
			SessionID:    session.ID,
			NodeType:     "agent_activity",
			Title:        label + " 已就位",
			Summary:      brief,
			Status:       "active",
			ActorAgentID: m.AgentID,
			Payload: marshalJSONRaw(map[string]any{
				"role":    m.Role,
				"phase":   "kickoff",
				"is_lead": m.IsLead,
			}),
		}, goal.ID, "leads_to")
		if aerr != nil {
			slog.Warn("research kickoff activity node failed", "agent_id", uuidToString(m.AgentID), "error", aerr)
			continue
		}
		_ = activity

		h.publish(protocol.EventResearchSessionPresence, workspaceID, "system", "", map[string]any{
			"session_id": uuidToString(session.ID),
			"agent_id":   uuidToString(m.AgentID),
			"activity":   brief,
		})
	}

	h.emitResearchProcessCard(ctx, workspaceID, wsUUID, session.ID, "user", userID, researchProcessEvent{
		Op:    "session_kickoff",
		Title: "调研团已就位",
		Body: fmt.Sprintf(
			"「%s」开题：%d 名成员已上画布；领域=%s，已按自适应维度树播种（非固定题库）。罗纳尔多开始 S1。",
			session.Title, activeCount, plan.FineDomain,
		),
		Meta: map[string]any{
			"member_count":   activeCount,
			"stage":          "s1_plan",
			"fine_domain":    plan.FineDomain,
			"coarse_domains": plan.CoarseDomains,
			"delivery_like":  plan.DeliveryLike,
			"dimensions":     len(plan.Dimensions),
		},
	})

	// Wake lead with adaptive-depth fan-out; wake other active members to stand by.
	initiator := parseUUID(userID)
	if leadID.Valid {
		leadPrompt := adaptiveKickoffLeadPrompt(session.Goal, plan)
		if err := h.enqueueResearchAgentWake(ctx, wsUUID, session, leadID, initiator, leadPrompt, "user", true); err != nil {
			slog.Warn("research kickoff lead wake failed", "error", err)
			h.emitResearchProcessCard(ctx, workspaceID, wsUUID, session.ID, "user", userID, researchWakeFailureEvent(leadID, err))
		}
	}
	for _, m := range uniqueMembers {
		if m.Status != "active" || m.IsLead || !m.AgentID.Valid {
			continue
		}
		standby := fmt.Sprintf(
			"Research session %s — you are on the sealed fleet. Stand by for S1 dispatch from 罗纳尔多. "+
				"Record work with multica research graph-append / source-upsert / presence. Session id: %s",
			session.Title, uuidToString(session.ID),
		)
		if err := h.enqueueResearchAgentWake(ctx, wsUUID, session, m.AgentID, initiator, standby, "system", true); err != nil {
			slog.Warn("research kickoff member wake failed", "agent_id", uuidToString(m.AgentID), "error", err)
			h.emitResearchProcessCard(ctx, workspaceID, wsUUID, session.ID, "user", userID, researchWakeFailureEvent(m.AgentID, err))
		}
	}
}
