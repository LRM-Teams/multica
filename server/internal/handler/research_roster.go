package handler

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// researchFleetMaxActiveMembers caps non-archived roster size (lead + seeds +
// specialty hires). Aligns with depth budget (LRM-676 / LRM-888): seed five
// (lead+4) plus up to seven specialty hires — enough for gap coverage without
// unbounded expansion.
const researchFleetMaxActiveMembers = 12

// researchRosterShellArchiveWindow rejects archiving a freshly hired member
// that produced no observable work (LRM-918 H2/H4).
const researchRosterShellArchiveWindow = 30 * time.Minute

// researchRosterFixtureHeader allows capacity / 409 fixture hires that must not
// paint user-visible sessions (LRM-918 H3).
const researchRosterFixtureHeader = "X-Research-Roster-Fixture"

var researchShellPadPattern = regexp.MustCompile(`(?i)(cap[_-]?pad|lrm904-cap|shell[_-]?pad|roster[_-]?pad|nonlead_probe|fixture[_-]?hire|test[_-]?pad)`)

func countNonArchivedFleetMembers(members []db.ResearchFleetMember) int {
	n := 0
	for _, m := range members {
		if m.Status != "archived" {
			n++
		}
	}
	return n
}

func researchRosterAtCap(activeCount int) bool {
	return activeCount >= researchFleetMaxActiveMembers
}

func resolveResearchHireModel(requested string, runtimeProvider string) pgtype.Text {
	model := strings.TrimSpace(requested)
	if model == "" {
		return pgTextModelForRuntime(runtimeProvider)
	}
	return pgtype.Text{String: model, Valid: true}
}

// researchAgentMayMutateSessionGoal is always false for fleet agents.
// Mid-flight goal edits are user-only (LRM-898); agents must not rewrite
// research_session.goal during the investigation.
func researchAgentMayMutateSessionGoal() bool {
	return false
}

func researchRosterFixtureRequested(headerValue string, bodyFixture bool) bool {
	if bodyFixture {
		return true
	}
	v := strings.TrimSpace(strings.ToLower(headerValue))
	return v == "1" || v == "true" || v == "yes"
}

func looksLikeResearchShellPad(name, role, reason string) bool {
	blob := strings.Join([]string{name, role, reason}, " ")
	return researchShellPadPattern.MatchString(blob)
}

// validateResearchHireGap enforces H1/H3: no hire without a real specialty gap;
// shell/pad capacity fills require fixture mode.
func validateResearchHireGap(name, role, reason string, members []db.ResearchFleetMember, fixture bool) error {
	role = strings.TrimSpace(role)
	reason = strings.TrimSpace(reason)
	name = strings.TrimSpace(name)

	if fixture {
		if !looksLikeResearchShellPad(name, role, reason) && reason == "" {
			return fmt.Errorf("fixture hire still needs a reason (capacity test)")
		}
		return nil
	}

	if looksLikeResearchShellPad(name, role, reason) {
		return fmt.Errorf("shell/pad hire rejected on user path; set %s: 1 for capacity/409 fixtures (no canvas projection)", researchRosterFixtureHeader)
	}
	if reason == "" {
		return fmt.Errorf("specialty gap reason required (no hire without a gap)")
	}
	if len([]rune(reason)) < 12 {
		return fmt.Errorf("specialty gap reason too vague; describe the missing specialty")
	}
	for _, m := range members {
		if m.Status == "archived" {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(m.Role), role) {
			return fmt.Errorf("role %q already filled; no specialty gap", role)
		}
	}
	return nil
}

func isResearchProductiveNodeType(nodeType string) bool {
	switch strings.TrimSpace(nodeType) {
	case "probe", "finding", "conflict", "dead_end", "refuted", "pivot",
		"agent_activity", "subquestion", "source_note", "report_section":
		return true
	default:
		return false
	}
}

func researchMemberHasObservableWork(nodes []db.ResearchGraphNode, agentID pgtype.UUID) bool {
	if !agentID.Valid {
		return false
	}
	for _, n := range nodes {
		if n.ActorAgentID != agentID {
			continue
		}
		if isResearchProductiveNodeType(n.NodeType) {
			return true
		}
	}
	return false
}

// validateResearchArchiveAntiChurn enforces H2/H4: refuse short-window shell
// archive without observable work unless fixture mode.
func validateResearchArchiveAntiChurn(
	member db.ResearchFleetMember,
	hiredAt time.Time,
	hasWork bool,
	fixture bool,
	now time.Time,
) error {
	if fixture {
		return nil
	}
	if member.IsLead || member.Status == "archived" {
		return nil
	}
	if hasWork {
		return nil
	}
	if hiredAt.IsZero() {
		return nil
	}
	age := now.Sub(hiredAt)
	if age >= 0 && age < researchRosterShellArchiveWindow {
		return fmt.Errorf(
			"cannot archive shell hire within %s without observable work (probe/finding/activity); use %s: 1 only for capacity fixtures",
			researchRosterShellArchiveWindow,
			researchRosterFixtureHeader,
		)
	}
	return nil
}

// researchRosterGraphStatus maps roster actions to canvas node status so archive
// cards are not shown as ACTIVE (LRM-918 H5 / LRM-904 AC).
func researchRosterGraphStatus(action string) string {
	switch strings.TrimSpace(action) {
	case "archive":
		return "archived"
	case "hire":
		return "pending"
	default:
		return "active"
	}
}

func (h *Handler) researchAgentHasObservableWork(
	ctx context.Context,
	wsUUID, fleetID, agentID pgtype.UUID,
) bool {
	sessions, err := h.Queries.ListResearchSessions(ctx, wsUUID)
	if err != nil {
		return false
	}
	for _, s := range sessions {
		if s.FleetID != fleetID {
			continue
		}
		nodes, nerr := h.Queries.ListResearchGraphNodes(ctx, db.ListResearchGraphNodesParams{
			SessionID:   s.ID,
			WorkspaceID: wsUUID,
		})
		if nerr != nil {
			continue
		}
		if researchMemberHasObservableWork(nodes, agentID) {
			return true
		}
	}
	return false
}

func (h *Handler) assignWorkAfterRosterActivate(
	ctx context.Context,
	workspaceID string,
	wsUUID pgtype.UUID,
	lead db.ResearchFleetMember,
	member db.ResearchFleetMember,
	agentName string,
	initiatorUserID pgtype.UUID,
) {
	sessions, err := h.Queries.ListResearchSessions(ctx, wsUUID)
	if err != nil {
		return
	}
	brief := fmt.Sprintf(
		"你已被罗纳尔多激活（角色 %s）。立即在本会话开探查：用 graph-append 记 probe/finding，禁止空转。",
		member.Role,
	)
	for _, s := range sessions {
		if s.FleetID != lead.FleetID {
			continue
		}
		if s.Status != "running" && s.Status != "awaiting_user_confirm" {
			continue
		}
		_, _, _ = h.createResearchGraphNodePublished(ctx, workspaceID, wsUUID, s.ID, "agent", uuidToString(lead.AgentID), db.CreateResearchGraphNodeParams{
			WorkspaceID:  wsUUID,
			SessionID:    s.ID,
			NodeType:     "agent_activity",
			Title:        fmt.Sprintf("已派活 · %s", agentName),
			Summary:      brief,
			Status:       "active",
			ActorAgentID: member.AgentID,
			Payload: marshalJSONRaw(map[string]any{
				"role":      member.Role,
				"phase":     "post_hire_assign",
				"action":    "assign_work",
				"member_id": uuidToString(member.ID),
			}),
		}, pgtype.UUID{}, "leads_to")
		h.emitResearchProcessCard(ctx, workspaceID, wsUUID, s.ID, "agent", uuidToString(lead.AgentID), researchProcessEvent{
			Op:      "roster_assign_work",
			Title:   agentName,
			Body:    fmt.Sprintf("编制派活 · %s（%s）须立即探查产出", agentName, member.Role),
			ActorID: lead.AgentID,
			Meta: map[string]any{
				"action":        "assign_work",
				"member_id":     uuidToString(member.ID),
				"member_status": member.Status,
				"role":          member.Role,
			},
		})
		if initiatorUserID.Valid {
			_ = h.enqueueResearchAgentWake(ctx, wsUUID, s, member.AgentID, initiatorUserID, brief, "system")
		}
	}
}
