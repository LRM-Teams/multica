package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type researchSeedRole struct {
	Name         string
	Description  string
	Instructions string
	Role         string
	IsLead       bool
}

func researchSeedRoles() []researchSeedRole {
	return []researchSeedRole{
		{Name: ronaldoAgentName, Description: ronaldoDescription, Instructions: ronaldoInstructions, Role: "lead", IsLead: true},
		{Name: scoutAgentName, Description: scoutDescription, Instructions: scoutInstructions, Role: "scout", IsLead: false},
		{Name: readerAgentName, Description: readerDescription, Instructions: readerInstructions, Role: "reader", IsLead: false},
		{Name: validatorAgentName, Description: validatorDescription, Instructions: validatorInstructions, Role: "validator", IsLead: false},
		{Name: reporterAgentName, Description: reporterDescription, Instructions: reporterInstructions, Role: "reporter", IsLead: false},
	}
}

type ResearchFleetResponse struct {
	ID          string                    `json:"id"`
	WorkspaceID string                    `json:"workspace_id"`
	LeadAgentID *string                   `json:"lead_agent_id"`
	Members     []ResearchFleetMemberResp `json:"members"`
	CreatedAt   string                    `json:"created_at"`
	UpdatedAt   string                    `json:"updated_at"`
}

type ResearchFleetMemberResp struct {
	ID          string `json:"id"`
	AgentID     string `json:"agent_id"`
	Role        string `json:"role"`
	Status      string `json:"status"`
	IsLead      bool   `json:"is_lead"`
	Name        string `json:"name,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
}

func (h *Handler) EnsureResearchFleet(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id")
	if !ok {
		return
	}
	userID, userOK := requireUserID(w, r)
	if !userOK {
		return
	}
	fleet, members, err := h.ensureResearchFleet(r.Context(), wsUUID, parseUUID(userID))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, h.researchFleetToResponse(r.Context(), fleet, members))
}

func (h *Handler) GetResearchFleet(w http.ResponseWriter, r *http.Request) {
	h.EnsureResearchFleet(w, r)
}

func (h *Handler) ensureResearchFleet(ctx context.Context, workspaceID, userID pgtype.UUID) (db.ResearchFleet, []db.ResearchFleetMember, error) {
	fleet, err := h.Queries.GetResearchFleetByWorkspace(ctx, workspaceID)
	if err == nil {
		members, merr := h.Queries.ListResearchFleetMembers(ctx, db.ListResearchFleetMembersParams{
			FleetID:     fleet.ID,
			WorkspaceID: workspaceID,
		})
		if merr != nil {
			return db.ResearchFleet{}, nil, merr
		}
		if len(members) > 0 && fleet.LeadAgentID.Valid {
			return fleet, members, nil
		}
		// Repair incomplete fleet.
		return h.seedResearchFleetMembers(ctx, fleet, workspaceID, userID)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return db.ResearchFleet{}, nil, err
	}

	fleet, err = h.Queries.CreateResearchFleet(ctx, db.CreateResearchFleetParams{
		WorkspaceID: workspaceID,
		LeadAgentID: pgtype.UUID{},
	})
	if err != nil {
		// Race: another request created it.
		fleet, err = h.Queries.GetResearchFleetByWorkspace(ctx, workspaceID)
		if err != nil {
			return db.ResearchFleet{}, nil, err
		}
	}
	return h.seedResearchFleetMembers(ctx, fleet, workspaceID, userID)
}

func (h *Handler) seedResearchFleetMembers(ctx context.Context, fleet db.ResearchFleet, workspaceID, userID pgtype.UUID) (db.ResearchFleet, []db.ResearchFleetMember, error) {
	runtime, ok := h.pickVisibleAgentRuntime(ctx, workspaceID, userID)
	if !ok {
		return db.ResearchFleet{}, nil, errors.New("no agent runtime available to seed research fleet")
	}

	existing, err := h.Queries.ListResearchFleetMembers(ctx, db.ListResearchFleetMembersParams{
		FleetID:     fleet.ID,
		WorkspaceID: workspaceID,
	})
	if err != nil {
		return db.ResearchFleet{}, nil, err
	}
	byRole := map[string]db.ResearchFleetMember{}
	for _, m := range existing {
		byRole[m.Role] = m
	}

	var leadID pgtype.UUID
	for _, seed := range researchSeedRoles() {
		if _, exists := byRole[seed.Role]; exists {
			if seed.IsLead {
				leadID = byRole[seed.Role].AgentID
			}
			continue
		}
		agent, err := h.createAgentWithIdentity(ctx, h.Queries, db.CreateAgentParams{
			WorkspaceID:        workspaceID,
			Description:        seed.Description,
			Instructions:       seed.Instructions,
			AvatarUrl:          pgtype.Text{},
			AvatarSource:       agentAvatarSourceAssigned,
			RuntimeMode:        runtime.RuntimeMode,
			RuntimeConfig:      []byte("{}"),
			RuntimeID:          runtime.ID,
			Visibility:         agentVisibilityPrivate,
			MaxConcurrentTasks: 4,
			OwnerID:            userID,
			CustomEnv:          []byte("{}"),
			CustomArgs:         []byte("[]"),
			McpConfig:          nil,
			Model:              pgtype.Text{},
			ThinkingLevel:      pgtype.Text{},
		}, seed.Name, seed.Name)
		if err != nil {
			return db.ResearchFleet{}, nil, fmt.Errorf("create fleet agent %s: %w", seed.Name, err)
		}
		if err := h.Queries.SetAgentManagedRoleResearchFleet(ctx, db.SetAgentManagedRoleResearchFleetParams{
			ID:          agent.ID,
			WorkspaceID: workspaceID,
		}); err != nil {
			return db.ResearchFleet{}, nil, err
		}
		member, err := h.Queries.CreateResearchFleetMember(ctx, db.CreateResearchFleetMemberParams{
			WorkspaceID: workspaceID,
			FleetID:     fleet.ID,
			AgentID:     agent.ID,
			Role:        seed.Role,
			Status:      "active",
			IsLead:      seed.IsLead,
		})
		if err != nil {
			return db.ResearchFleet{}, nil, err
		}
		byRole[seed.Role] = member
		if seed.IsLead {
			leadID = agent.ID
		}
	}

	if leadID.Valid {
		fleet, err = h.Queries.SetResearchFleetLead(ctx, db.SetResearchFleetLeadParams{
			ID:          fleet.ID,
			LeadAgentID: leadID,
			WorkspaceID: workspaceID,
		})
		if err != nil {
			return db.ResearchFleet{}, nil, err
		}
	}

	members, err := h.Queries.ListResearchFleetMembers(ctx, db.ListResearchFleetMembersParams{
		FleetID:     fleet.ID,
		WorkspaceID: workspaceID,
	})
	if err != nil {
		return db.ResearchFleet{}, nil, err
	}
	h.seedResearchFleetPlaybooks(ctx, workspaceID, fleet.ID)
	return fleet, members, nil
}

func (h *Handler) seedResearchFleetPlaybooks(ctx context.Context, workspaceID, fleetID pgtype.UUID) {
	for domain, content := range researchDomainPlaybooks() {
		if _, err := h.Queries.GetLatestResearchPlaybook(ctx, db.GetLatestResearchPlaybookParams{
			FleetID:     fleetID,
			WorkspaceID: workspaceID,
			Domain:      domain,
		}); err == nil {
			continue
		}
		_, _ = h.Queries.CreateResearchPlaybook(ctx, db.CreateResearchPlaybookParams{
			WorkspaceID: workspaceID,
			FleetID:     fleetID,
			Domain:      domain,
			Version:     1,
			ContentMd:   content,
		})
	}
}

func (h *Handler) researchFleetToResponse(ctx context.Context, fleet db.ResearchFleet, members []db.ResearchFleetMember) ResearchFleetResponse {
	out := ResearchFleetResponse{
		ID:          uuidToString(fleet.ID),
		WorkspaceID: uuidToString(fleet.WorkspaceID),
		LeadAgentID: uuidToPtr(fleet.LeadAgentID),
		Members:     make([]ResearchFleetMemberResp, 0, len(members)),
		CreatedAt:   timestampToString(fleet.CreatedAt),
		UpdatedAt:   timestampToString(fleet.UpdatedAt),
	}
	for _, m := range members {
		item := ResearchFleetMemberResp{
			ID:      uuidToString(m.ID),
			AgentID: uuidToString(m.AgentID),
			Role:    m.Role,
			Status:  m.Status,
			IsLead:  m.IsLead,
		}
		if agent, err := h.Queries.GetAgent(ctx, m.AgentID); err == nil {
			item.Name = agent.Name
			item.DisplayName = agent.DisplayName
		}
		out.Members = append(out.Members, item)
	}
	return out
}

func (h *Handler) requireResearchLeadActor(w http.ResponseWriter, r *http.Request, workspaceID pgtype.UUID) (db.ResearchFleetMember, bool) {
	agentIDRaw := r.Header.Get("X-Agent-ID")
	if agentIDRaw == "" {
		writeError(w, http.StatusForbidden, "research lead privileges require agent actor")
		return db.ResearchFleetMember{}, false
	}
	agentID, ok := parseUUIDOrBadRequest(w, agentIDRaw, "X-Agent-ID")
	if !ok {
		return db.ResearchFleetMember{}, false
	}
	member, err := h.Queries.GetResearchFleetMemberByAgent(r.Context(), db.GetResearchFleetMemberByAgentParams{
		WorkspaceID: workspaceID,
		AgentID:     agentID,
	})
	if err != nil || !member.IsLead || member.Status != "active" {
		writeError(w, http.StatusForbidden, "only active research lead (罗纳尔多) may perform this action")
		return db.ResearchFleetMember{}, false
	}
	return member, true
}

func (h *Handler) requireActiveFleetMember(w http.ResponseWriter, r *http.Request, workspaceID pgtype.UUID) (db.ResearchFleetMember, bool) {
	agentIDRaw := r.Header.Get("X-Agent-ID")
	if agentIDRaw == "" {
		writeError(w, http.StatusForbidden, "fleet member action requires agent actor")
		return db.ResearchFleetMember{}, false
	}
	agentID, ok := parseUUIDOrBadRequest(w, agentIDRaw, "X-Agent-ID")
	if !ok {
		return db.ResearchFleetMember{}, false
	}
	member, err := h.Queries.GetResearchFleetMemberByAgent(r.Context(), db.GetResearchFleetMemberByAgentParams{
		WorkspaceID: workspaceID,
		AgentID:     agentID,
	})
	if err != nil || member.Status != "active" {
		writeError(w, http.StatusForbidden, "only active research fleet members may write research artifacts")
		return db.ResearchFleetMember{}, false
	}
	return member, true
}

func marshalJSONRaw(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		return []byte("{}")
	}
	return b
}
