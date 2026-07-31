package handler

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type ResearchSessionResponse struct {
	ID                 string  `json:"id"`
	WorkspaceID        string  `json:"workspace_id"`
	FleetID            string  `json:"fleet_id"`
	CreatedBy          string  `json:"created_by"`
	Title              string  `json:"title"`
	Goal               string  `json:"goal"`
	Status             string  `json:"status"`
	CurrentStage       string  `json:"current_stage"`
	DepthTier           string  `json:"depth_tier"`
	ProductRound       int32   `json:"product_round"`
	ProductRoundBudget int32   `json:"product_round_budget"`
	ProjectID          *string `json:"project_id"`
	ChannelID          *string `json:"channel_id"`
	HandoffSummary     *string `json:"handoff_summary"`
	CreatedAt          string  `json:"created_at"`
	UpdatedAt          string  `json:"updated_at"`
}

// ResearchFleetPreviewMember is a list-row avatar stack item (LRM-805).
type ResearchFleetPreviewMember struct {
	AgentID     string  `json:"agent_id"`
	Name        string  `json:"name,omitempty"`
	DisplayName string  `json:"display_name,omitempty"`
	AvatarURL   *string `json:"avatar_url"`
	Role        string  `json:"role,omitempty"`
	IsLead      bool    `json:"is_lead,omitempty"`
}

// ResearchSessionListItem extends the session row with a workspace fleet preview.
type ResearchSessionListItem struct {
	ResearchSessionResponse
	FleetPreview []ResearchFleetPreviewMember `json:"fleet_preview"`
}

// ResearchPresenceEntry is one agent's live activity caption for a session.
type ResearchPresenceEntry struct {
	Activity  string `json:"activity"`
	UpdatedAt int64  `json:"updated_at"` // unix ms
}

type ResearchSessionSnapshot struct {
	Session       ResearchSessionResponse      `json:"session"`
	Fleet         ResearchFleetResponse        `json:"fleet"`
	Nodes         []ResearchGraphNodeResp      `json:"nodes"`
	Edges         []ResearchGraphEdgeResp      `json:"edges"`
	Sources       []ResearchSourceResp         `json:"sources"`
	Report        *ResearchReportResp          `json:"report"`
	Evals         []ResearchStageEvalResp      `json:"evals"`
	Messages      []ResearchMessageResp        `json:"messages"`
	ProductRounds []ResearchProductRoundCardResp `json:"product_rounds"`
}

type ResearchGraphNodeResp struct {
	ID           string          `json:"id"`
	SessionID    string          `json:"session_id"`
	NodeType     string          `json:"node_type"`
	Title        string          `json:"title"`
	Summary      string          `json:"summary"`
	Status       string          `json:"status"`
	ActorAgentID *string         `json:"actor_agent_id"`
	Payload      json.RawMessage `json:"payload"`
	// Confidence is projected from payload.confidence when present (LRM-806).
	Confidence *float64 `json:"confidence,omitempty"`
	CreatedAt  string   `json:"created_at"`
	UpdatedAt  string   `json:"updated_at"`
}

type ResearchGraphEdgeResp struct {
	ID         string `json:"id"`
	SessionID  string `json:"session_id"`
	FromNodeID string `json:"from_node_id"`
	ToNodeID   string `json:"to_node_id"`
	EdgeType   string `json:"edge_type"`
	CreatedAt  string `json:"created_at"`
}

type ResearchSourceResp struct {
	ID                string          `json:"id"`
	SessionID         string          `json:"session_id"`
	URL               string          `json:"url"`
	Title             string          `json:"title"`
	SourceClass       string          `json:"source_class"`
	CredibilityWeight float64         `json:"credibility_weight"`
	Stance            string          `json:"stance"`
	Relevance         float64         `json:"relevance"`
	Summary           string          `json:"summary"`
	Excerpt           string          `json:"excerpt"`
	Payload           json.RawMessage `json:"payload"`
	CreatedAt         string          `json:"created_at"`
	UpdatedAt         string          `json:"updated_at"`
}

type ResearchReportResp struct {
	ID         string          `json:"id"`
	SessionID  string          `json:"session_id"`
	Revision   int32           `json:"revision"`
	ContentMD  string          `json:"content_md"`
	Structured json.RawMessage `json:"structured"`
	CreatedAt  string          `json:"created_at"`
	UpdatedAt  string          `json:"updated_at"`
}

type ResearchStageEvalResp struct {
	ID          string          `json:"id"`
	SessionID   string          `json:"session_id"`
	Stage       string          `json:"stage"`
	Passed      bool            `json:"passed"`
	Score       float64         `json:"score"`
	Findings    json.RawMessage `json:"findings"`
	Remediation string          `json:"remediation"`
	CreatedAt   string          `json:"created_at"`
}

type ResearchMessageResp struct {
	ID            string          `json:"id"`
	SessionID     string          `json:"session_id"`
	SenderType    string          `json:"sender_type"`
	SenderID      *string         `json:"sender_id"`
	TargetAgentID *string         `json:"target_agent_id"`
	Body          string          `json:"body"`
	CardKind      string          `json:"card_kind"`
	Meta          json.RawMessage `json:"meta"`
	CreatedAt     string          `json:"created_at"`
}

func researchSessionToResponse(s db.ResearchSession) ResearchSessionResponse {
	return ResearchSessionResponse{
		ID:                 uuidToString(s.ID),
		WorkspaceID:        uuidToString(s.WorkspaceID),
		FleetID:            uuidToString(s.FleetID),
		CreatedBy:          uuidToString(s.CreatedBy),
		Title:              s.Title,
		Goal:               s.Goal,
		Status:             s.Status,
		CurrentStage:       s.CurrentStage,
		DepthTier:           s.DepthTier,
		ProductRound:       s.ProductRound,
		ProductRoundBudget: s.ProductRoundBudget,
		ProjectID:          uuidToPtr(s.ProjectID),
		ChannelID:          uuidToPtr(s.ChannelID),
		HandoffSummary:     textToPtr(s.HandoffSummary),
		CreatedAt:          timestampToString(s.CreatedAt),
		UpdatedAt:          timestampToString(s.UpdatedAt),
	}
}

func (h *Handler) ListResearchSessions(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id")
	if !ok {
		return
	}
	var preview []ResearchFleetPreviewMember
	if userID := requestUserID(r); userID != "" {
		fleet, members, err := h.ensureResearchFleet(r.Context(), wsUUID, parseUUID(userID))
		if err == nil {
			preview = h.researchFleetPreview(r.Context(), fleet, members, 5)
		}
	} else if fleet, err := h.Queries.GetResearchFleetByWorkspace(r.Context(), wsUUID); err == nil {
		members, _ := h.Queries.ListResearchFleetMembers(r.Context(), db.ListResearchFleetMembersParams{
			FleetID:     fleet.ID,
			WorkspaceID: wsUUID,
		})
		preview = h.researchFleetPreview(r.Context(), fleet, members, 5)
	}
	if preview == nil {
		preview = []ResearchFleetPreviewMember{}
	}
	rows, err := h.Queries.ListResearchSessions(r.Context(), wsUUID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list research sessions")
		return
	}
	out := make([]ResearchSessionListItem, 0, len(rows))
	for _, row := range rows {
		out = append(out, ResearchSessionListItem{
			ResearchSessionResponse: researchSessionToResponse(row),
			FleetPreview:            preview,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": out})
}

type createResearchSessionRequest struct {
	Goal      string `json:"goal"`
	Title     string `json:"title"`
	DepthTier string `json:"depth_tier"` // shallow|standard|deep — LRM-676 product-round caps
}

func (h *Handler) CreateResearchSession(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id")
	if !ok {
		return
	}
	userID, userOK := requireUserID(w, r)
	if !userOK {
		return
	}
	var req createResearchSessionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.Goal = strings.TrimSpace(req.Goal)
	if req.Goal == "" {
		writeError(w, http.StatusBadRequest, "goal is required")
		return
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = truncateRunes(req.Goal, 80)
	}
	fleet, members, err := h.ensureResearchFleet(r.Context(), wsUUID, parseUUID(userID))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	depthTier := normalizeResearchDepthTier(req.DepthTier)
	session, err := h.Queries.CreateResearchSession(r.Context(), db.CreateResearchSessionParams{
		WorkspaceID:        wsUUID,
		FleetID:            fleet.ID,
		CreatedBy:          parseUUID(userID),
		Title:              title,
		Goal:               req.Goal,
		Status:             "running",
		CurrentStage:       "s1_plan",
		DepthTier:           depthTier,
		ProductRound:       1,
		ProductRoundBudget: productRoundBudgetForTier(depthTier),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create research session")
		return
	}
	h.publish(protocol.EventResearchSessionStatusChanged, workspaceID, "user", userID, map[string]any{
		"session": researchSessionToResponse(session),
	})
	// Fan-out goal + subquestions + per-member activity, process cards, and wakes.
	h.seedResearchSessionKickoff(r.Context(), workspaceID, wsUUID, session, fleet, members, userID)

	// Return a fresh snapshot so the client can paint the kickoff graph without waiting on WS.
	nodes, _ := h.Queries.ListResearchGraphNodes(r.Context(), db.ListResearchGraphNodesParams{SessionID: session.ID, WorkspaceID: wsUUID})
	edges, _ := h.Queries.ListResearchGraphEdges(r.Context(), db.ListResearchGraphEdgesParams{SessionID: session.ID, WorkspaceID: wsUUID})
	messages, _ := h.Queries.ListResearchMessages(r.Context(), db.ListResearchMessagesParams{SessionID: session.ID, WorkspaceID: wsUUID})
	writeJSON(w, http.StatusCreated, map[string]any{
		"session":  researchSessionToResponse(session),
		"fleet":    h.researchFleetToResponse(r.Context(), fleet, members),
		"nodes":    mapNodes(nodes),
		"edges":    mapEdges(edges),
		"messages": mapMessages(messages),
	})
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func (h *Handler) GetResearchSessionSnapshot(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id")
	if !ok {
		return
	}
	sessionID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	session, err := h.Queries.GetResearchSession(r.Context(), db.GetResearchSessionParams{
		ID:          sessionID,
		WorkspaceID: wsUUID,
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "research session not found")
		return
	}
	fleet, err := h.Queries.GetResearchFleetByWorkspace(r.Context(), wsUUID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "research fleet missing")
		return
	}
	members, _ := h.Queries.ListResearchFleetMembers(r.Context(), db.ListResearchFleetMembersParams{
		FleetID:     fleet.ID,
		WorkspaceID: wsUUID,
	})
	nodes, _ := h.Queries.ListResearchGraphNodes(r.Context(), db.ListResearchGraphNodesParams{SessionID: sessionID, WorkspaceID: wsUUID})
	edges, _ := h.Queries.ListResearchGraphEdges(r.Context(), db.ListResearchGraphEdgesParams{SessionID: sessionID, WorkspaceID: wsUUID})
	sources, _ := h.Queries.ListResearchSources(r.Context(), db.ListResearchSourcesParams{SessionID: sessionID, WorkspaceID: wsUUID})
	evals, _ := h.Queries.ListResearchStageEvals(r.Context(), db.ListResearchStageEvalsParams{SessionID: sessionID, WorkspaceID: wsUUID})
	messages, _ := h.Queries.ListResearchMessages(r.Context(), db.ListResearchMessagesParams{SessionID: sessionID, WorkspaceID: wsUUID})
	productRounds, _ := h.Queries.ListResearchProductRoundCards(r.Context(), db.ListResearchProductRoundCardsParams{SessionID: sessionID, WorkspaceID: wsUUID})

	var report *ResearchReportResp
	if rep, err := h.Queries.GetLatestResearchReport(r.Context(), db.GetLatestResearchReportParams{SessionID: sessionID, WorkspaceID: wsUUID}); err == nil {
		rr := researchReportToResp(rep)
		report = &rr
	}

	writeJSON(w, http.StatusOK, ResearchSessionSnapshot{
		Session:       researchSessionToResponse(session),
		Fleet:         h.researchFleetToResponse(r.Context(), fleet, members),
		Nodes:         mapNodes(nodes),
		Edges:         mapEdges(edges),
		Sources:       mapSources(sources),
		Report:        report,
		Evals:         mapEvals(evals),
		Messages:      mapMessages(messages),
		ProductRounds: mapProductRoundCards(productRounds),
	})
}

func mapNodes(rows []db.ResearchGraphNode) []ResearchGraphNodeResp {
	out := make([]ResearchGraphNodeResp, 0, len(rows))
	for _, n := range rows {
		payload := json.RawMessage(n.Payload)
		out = append(out, ResearchGraphNodeResp{
			ID:           uuidToString(n.ID),
			SessionID:    uuidToString(n.SessionID),
			NodeType:     n.NodeType,
			Title:        n.Title,
			Summary:      n.Summary,
			Status:       n.Status,
			ActorAgentID: uuidToPtr(n.ActorAgentID),
			Payload:      payload,
			Confidence:   confidenceFromPayload(payload),
			CreatedAt:    timestampToString(n.CreatedAt),
			UpdatedAt:    timestampToString(n.UpdatedAt),
		})
	}
	return out
}

func confidenceFromPayload(payload json.RawMessage) *float64 {
	if len(payload) == 0 {
		return nil
	}
	var obj map[string]any
	if err := json.Unmarshal(payload, &obj); err != nil {
		return nil
	}
	raw, ok := obj["confidence"]
	if !ok || raw == nil {
		return nil
	}
	switch v := raw.(type) {
	case float64:
		return &v
	case json.Number:
		f, err := v.Float64()
		if err != nil {
			return nil
		}
		return &f
	default:
		return nil
	}
}

// buildResearchPresenceMap rebuilds ephemeral presence from the latest
// agent_activity graph node per actor (GET bootstrap for LRM-804/775).
func buildResearchPresenceMap(nodes []db.ResearchGraphNode) map[string]ResearchPresenceEntry {
	out := map[string]ResearchPresenceEntry{}
	for _, n := range nodes {
		if n.NodeType != "agent_activity" || !n.ActorAgentID.Valid {
			continue
		}
		agentID := uuidToString(n.ActorAgentID)
		activity := strings.TrimSpace(n.Title)
		if activity == "" {
			activity = strings.TrimSpace(n.Summary)
		}
		if activity == "" {
			continue
		}
		updatedAt := n.UpdatedAt.Time.UnixMilli()
		if !n.UpdatedAt.Valid {
			updatedAt = n.CreatedAt.Time.UnixMilli()
		}
		prev, ok := out[agentID]
		if !ok || updatedAt >= prev.UpdatedAt {
			out[agentID] = ResearchPresenceEntry{Activity: activity, UpdatedAt: updatedAt}
		}
	}
	return out
}

func mapEdges(rows []db.ResearchGraphEdge) []ResearchGraphEdgeResp {
	out := make([]ResearchGraphEdgeResp, 0, len(rows))
	for _, e := range rows {
		out = append(out, ResearchGraphEdgeResp{
			ID:         uuidToString(e.ID),
			SessionID:  uuidToString(e.SessionID),
			FromNodeID: uuidToString(e.FromNodeID),
			ToNodeID:   uuidToString(e.ToNodeID),
			EdgeType:   e.EdgeType,
			CreatedAt:  timestampToString(e.CreatedAt),
		})
	}
	return out
}

func mapSources(rows []db.ResearchSource) []ResearchSourceResp {
	out := make([]ResearchSourceResp, 0, len(rows))
	for _, s := range rows {
		out = append(out, ResearchSourceResp{
			ID:                uuidToString(s.ID),
			SessionID:         uuidToString(s.SessionID),
			URL:               s.Url,
			Title:             s.Title,
			SourceClass:       s.SourceClass,
			CredibilityWeight: s.CredibilityWeight,
			Stance:            s.Stance,
			Relevance:         s.Relevance,
			Summary:           s.Summary,
			Excerpt:           s.Excerpt,
			Payload:           json.RawMessage(s.Payload),
			CreatedAt:         timestampToString(s.CreatedAt),
			UpdatedAt:         timestampToString(s.UpdatedAt),
		})
	}
	return out
}

func researchReportToResp(r db.ResearchReport) ResearchReportResp {
	return ResearchReportResp{
		ID:         uuidToString(r.ID),
		SessionID:  uuidToString(r.SessionID),
		Revision:   r.Revision,
		ContentMD:  r.ContentMd,
		Structured: json.RawMessage(r.Structured),
		CreatedAt:  timestampToString(r.CreatedAt),
		UpdatedAt:  timestampToString(r.UpdatedAt),
	}
}

func mapEvals(rows []db.ResearchStageEval) []ResearchStageEvalResp {
	out := make([]ResearchStageEvalResp, 0, len(rows))
	for _, e := range rows {
		out = append(out, ResearchStageEvalResp{
			ID:          uuidToString(e.ID),
			SessionID:   uuidToString(e.SessionID),
			Stage:       e.Stage,
			Passed:      e.Passed,
			Score:       e.Score,
			Findings:    json.RawMessage(e.Findings),
			Remediation: e.Remediation,
			CreatedAt:   timestampToString(e.CreatedAt),
		})
	}
	return out
}

func mapMessages(rows []db.ResearchMessage) []ResearchMessageResp {
	out := make([]ResearchMessageResp, 0, len(rows))
	for _, m := range rows {
		cardKind := m.CardKind
		if cardKind == "" {
			cardKind = "chat"
		}
		meta := json.RawMessage(m.Meta)
		if len(meta) == 0 {
			meta = json.RawMessage(`{}`)
		}
		out = append(out, ResearchMessageResp{
			ID:            uuidToString(m.ID),
			SessionID:     uuidToString(m.SessionID),
			SenderType:    m.SenderType,
			SenderID:      uuidToPtr(m.SenderID),
			TargetAgentID: uuidToPtr(m.TargetAgentID),
			Body:          m.Body,
			CardKind:      cardKind,
			Meta:          meta,
			CreatedAt:     timestampToString(m.CreatedAt),
		})
	}
	return out
}
