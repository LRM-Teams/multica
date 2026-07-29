package handler

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type ResearchSessionResponse struct {
	ID            string  `json:"id"`
	WorkspaceID   string  `json:"workspace_id"`
	FleetID       string  `json:"fleet_id"`
	CreatedBy     string  `json:"created_by"`
	Title         string  `json:"title"`
	Goal          string  `json:"goal"`
	Status        string  `json:"status"`
	CurrentStage  string  `json:"current_stage"`
	ProjectID     *string `json:"project_id"`
	ChannelID     *string `json:"channel_id"`
	HandoffSummary *string `json:"handoff_summary"`
	CreatedAt     string  `json:"created_at"`
	UpdatedAt     string  `json:"updated_at"`
}

type ResearchSessionSnapshot struct {
	Session  ResearchSessionResponse   `json:"session"`
	Fleet    ResearchFleetResponse     `json:"fleet"`
	Nodes    []ResearchGraphNodeResp   `json:"nodes"`
	Edges    []ResearchGraphEdgeResp   `json:"edges"`
	Sources  []ResearchSourceResp      `json:"sources"`
	Report   *ResearchReportResp       `json:"report"`
	Evals    []ResearchStageEvalResp   `json:"evals"`
	Messages []ResearchMessageResp     `json:"messages"`
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
	CreatedAt    string          `json:"created_at"`
	UpdatedAt    string          `json:"updated_at"`
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
	ID                 string          `json:"id"`
	SessionID          string          `json:"session_id"`
	URL                string          `json:"url"`
	Title              string          `json:"title"`
	SourceClass        string          `json:"source_class"`
	CredibilityWeight  float64         `json:"credibility_weight"`
	Stance             string          `json:"stance"`
	Relevance          float64         `json:"relevance"`
	Summary            string          `json:"summary"`
	Excerpt            string          `json:"excerpt"`
	Payload            json.RawMessage `json:"payload"`
	CreatedAt          string          `json:"created_at"`
	UpdatedAt          string          `json:"updated_at"`
}

type ResearchReportResp struct {
	ID          string          `json:"id"`
	SessionID   string          `json:"session_id"`
	Revision    int32           `json:"revision"`
	ContentMD   string          `json:"content_md"`
	Structured  json.RawMessage `json:"structured"`
	CreatedAt   string          `json:"created_at"`
	UpdatedAt   string          `json:"updated_at"`
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
	ID            string  `json:"id"`
	SessionID     string  `json:"session_id"`
	SenderType    string  `json:"sender_type"`
	SenderID      *string `json:"sender_id"`
	TargetAgentID *string `json:"target_agent_id"`
	Body          string  `json:"body"`
	CreatedAt     string  `json:"created_at"`
}

func researchSessionToResponse(s db.ResearchSession) ResearchSessionResponse {
	return ResearchSessionResponse{
		ID:             uuidToString(s.ID),
		WorkspaceID:    uuidToString(s.WorkspaceID),
		FleetID:        uuidToString(s.FleetID),
		CreatedBy:      uuidToString(s.CreatedBy),
		Title:          s.Title,
		Goal:           s.Goal,
		Status:         s.Status,
		CurrentStage:   s.CurrentStage,
		ProjectID:      uuidToPtr(s.ProjectID),
		ChannelID:      uuidToPtr(s.ChannelID),
		HandoffSummary: textToPtr(s.HandoffSummary),
		CreatedAt:      timestampToString(s.CreatedAt),
		UpdatedAt:      timestampToString(s.UpdatedAt),
	}
}

func (h *Handler) ListResearchSessions(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id")
	if !ok {
		return
	}
	if userID := requestUserID(r); userID != "" {
		_, _, _ = h.ensureResearchFleet(r.Context(), wsUUID, parseUUID(userID))
	}
	rows, err := h.Queries.ListResearchSessions(r.Context(), wsUUID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list research sessions")
		return
	}
	out := make([]ResearchSessionResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, researchSessionToResponse(row))
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": out})
}

type createResearchSessionRequest struct {
	Goal  string `json:"goal"`
	Title string `json:"title"`
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
	session, err := h.Queries.CreateResearchSession(r.Context(), db.CreateResearchSessionParams{
		WorkspaceID:  wsUUID,
		FleetID:      fleet.ID,
		CreatedBy:    parseUUID(userID),
		Title:        title,
		Goal:         req.Goal,
		Status:       "running",
		CurrentStage: "s1_plan",
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create research session")
		return
	}
	leadID := fleet.LeadAgentID
	_, _ = h.Queries.CreateResearchGraphNode(r.Context(), db.CreateResearchGraphNodeParams{
		WorkspaceID:  wsUUID,
		SessionID:    session.ID,
		NodeType:     "goal",
		Title:        title,
		Summary:      req.Goal,
		Status:       "active",
		ActorAgentID: leadID,
		Payload:      []byte(`{}`),
	})
	h.publish(protocol.EventResearchSessionStatusChanged, workspaceID, "user", userID, map[string]any{
		"session": researchSessionToResponse(session),
	})
	// Kick off 罗纳尔多 with the session goal so the exploration graph starts moving.
	if leadID.Valid {
		kickoff := "New research session created. Begin S1 planning for the goal. Coordinate the fleet; talk to the user as 罗纳尔多."
		if wakeErr := h.enqueueResearchAgentWake(r.Context(), wsUUID, session, leadID, parseUUID(userID), kickoff, "user"); wakeErr != nil {
			slog.Warn("research session kickoff wake failed",
				"session_id", uuidToString(session.ID),
				"error", wakeErr,
			)
		}
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"session": researchSessionToResponse(session),
		"fleet":   h.researchFleetToResponse(r.Context(), fleet, members),
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

	var report *ResearchReportResp
	if rep, err := h.Queries.GetLatestResearchReport(r.Context(), db.GetLatestResearchReportParams{SessionID: sessionID, WorkspaceID: wsUUID}); err == nil {
		rr := researchReportToResp(rep)
		report = &rr
	}

	writeJSON(w, http.StatusOK, ResearchSessionSnapshot{
		Session:  researchSessionToResponse(session),
		Fleet:    h.researchFleetToResponse(r.Context(), fleet, members),
		Nodes:    mapNodes(nodes),
		Edges:    mapEdges(edges),
		Sources:  mapSources(sources),
		Report:   report,
		Evals:    mapEvals(evals),
		Messages: mapMessages(messages),
	})
}

func mapNodes(rows []db.ResearchGraphNode) []ResearchGraphNodeResp {
	out := make([]ResearchGraphNodeResp, 0, len(rows))
	for _, n := range rows {
		out = append(out, ResearchGraphNodeResp{
			ID:           uuidToString(n.ID),
			SessionID:    uuidToString(n.SessionID),
			NodeType:     n.NodeType,
			Title:        n.Title,
			Summary:      n.Summary,
			Status:       n.Status,
			ActorAgentID: uuidToPtr(n.ActorAgentID),
			Payload:      json.RawMessage(n.Payload),
			CreatedAt:    timestampToString(n.CreatedAt),
			UpdatedAt:    timestampToString(n.UpdatedAt),
		})
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
		out = append(out, ResearchMessageResp{
			ID:            uuidToString(m.ID),
			SessionID:     uuidToString(m.SessionID),
			SenderType:    m.SenderType,
			SenderID:      uuidToPtr(m.SenderID),
			TargetAgentID: uuidToPtr(m.TargetAgentID),
			Body:          m.Body,
			CreatedAt:     timestampToString(m.CreatedAt),
		})
	}
	return out
}
