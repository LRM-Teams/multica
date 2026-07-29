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
	goal, err := h.Queries.CreateResearchGraphNode(ctx, db.CreateResearchGraphNodeParams{
		WorkspaceID:  wsUUID,
		SessionID:    session.ID,
		NodeType:     "goal",
		Title:        session.Title,
		Summary:      session.Goal,
		Status:       "active",
		ActorAgentID: leadID,
		Payload:      marshalJSONRaw(map[string]any{"phase": "kickoff"}),
	})
	if err != nil {
		slog.Warn("research kickoff goal node failed", "error", err)
		return
	}
	h.publishResearchGraph(workspaceID, "user", userID, session.ID, goal, nil)

	subquestions := []struct {
		Title   string
		Summary string
	}{
		{"市场与竞品切入点", "谁在做同类产品？差异化与定价信号在哪里？"},
		{"技术栈与架构路径", "可行技术路线、关键依赖与环境约束是什么？"},
		{"人力与节奏风险", "需要哪些角色？常见坑与死胡同有哪些？"},
		{"交付边界与验收", "第一版可交付范围与验证标准是什么？"},
	}
	for i, sq := range subquestions {
		node, _, nerr := h.createResearchGraphNodePublished(ctx, workspaceID, wsUUID, session.ID, "user", userID, db.CreateResearchGraphNodeParams{
			WorkspaceID:  wsUUID,
			SessionID:    session.ID,
			NodeType:     "subquestion",
			Title:        sq.Title,
			Summary:      sq.Summary,
			Status:       "active",
			ActorAgentID: leadID,
			Payload:      marshalJSONRaw(map[string]any{"seed": true, "index": i, "phase": "s1_plan"}),
		}, goal.ID, "leads_to")
		if nerr != nil {
			slog.Warn("research kickoff subquestion failed", "error", nerr)
			continue
		}
		_ = node
	}

	activeCount := 0
	for _, m := range members {
		if m.Status == "archived" {
			continue
		}
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
				"role":   m.Role,
				"phase":  "kickoff",
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
		Body:  fmt.Sprintf("「%s」开题：%d 名成员已上画布，罗纳尔多开始 S1 作战规划。", session.Title, activeCount),
		Meta: map[string]any{
			"member_count": activeCount,
			"stage":        "s1_plan",
		},
	})

	// Wake lead with explicit fan-out instructions; wake other active members to stand by.
	initiator := parseUUID(userID)
	if leadID.Valid {
		leadPrompt := fmt.Sprintf(
			"New research session ready on the exploration canvas.\nGoal: %s\n"+
				"Seeded subquestions and agent_activity nodes are already visible — refine them, "+
				"dispatch probes via multica research graph-append / message --target, "+
				"and keep the user updated through 罗纳尔多 voice only.",
			session.Goal,
		)
		if err := h.enqueueResearchAgentWake(ctx, wsUUID, session, leadID, initiator, leadPrompt, "user"); err != nil {
			slog.Warn("research kickoff lead wake failed", "error", err)
		}
	}
	for _, m := range members {
		if m.Status != "active" || m.IsLead || !m.AgentID.Valid {
			continue
		}
		standby := fmt.Sprintf(
			"Research session %s — you are on the sealed fleet. Stand by for S1 dispatch from 罗纳尔多. "+
				"Record work with multica research graph-append / source-upsert / presence. Session id: %s",
			session.Title, uuidToString(session.ID),
		)
		if err := h.enqueueResearchAgentWake(ctx, wsUUID, session, m.AgentID, initiator, standby, "system"); err != nil {
			slog.Warn("research kickoff member wake failed", "agent_id", uuidToString(m.AgentID), "error", err)
		}
	}
}
