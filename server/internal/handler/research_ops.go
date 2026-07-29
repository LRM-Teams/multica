package handler

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type appendGraphNodeRequest struct {
	NodeType string          `json:"node_type"`
	Title    string          `json:"title"`
	Summary  string          `json:"summary"`
	Status   string          `json:"status"`
	Payload  json.RawMessage `json:"payload"`
	FromID   string          `json:"from_node_id"`
	EdgeType string          `json:"edge_type"`
}

func (h *Handler) AppendResearchGraphNode(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id")
	if !ok {
		return
	}
	member, ok := h.requireActiveFleetMember(w, r, wsUUID)
	if !ok {
		return
	}
	sessionID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	if _, err := h.Queries.GetResearchSession(r.Context(), db.GetResearchSessionParams{ID: sessionID, WorkspaceID: wsUUID}); err != nil {
		writeError(w, http.StatusNotFound, "research session not found")
		return
	}
	var req appendGraphNodeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Status == "" {
		req.Status = "active"
	}
	payload := req.Payload
	if len(payload) == 0 {
		payload = json.RawMessage(`{}`)
	}
	node, err := h.Queries.CreateResearchGraphNode(r.Context(), db.CreateResearchGraphNodeParams{
		WorkspaceID:  wsUUID,
		SessionID:    sessionID,
		NodeType:     req.NodeType,
		Title:        strings.TrimSpace(req.Title),
		Summary:      req.Summary,
		Status:       req.Status,
		ActorAgentID: member.AgentID,
		Payload:      payload,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to create graph node")
		return
	}
	var edge *ResearchGraphEdgeResp
	if req.FromID != "" {
		fromID, ok := parseUUIDOrBadRequest(w, req.FromID, "from_node_id")
		if !ok {
			return
		}
		et := req.EdgeType
		if et == "" {
			et = "leads_to"
		}
		e, err := h.Queries.CreateResearchGraphEdge(r.Context(), db.CreateResearchGraphEdgeParams{
			WorkspaceID: wsUUID,
			SessionID:   sessionID,
			FromNodeID:  fromID,
			ToNodeID:    node.ID,
			EdgeType:    et,
		})
		if err == nil {
			er := mapEdges([]db.ResearchGraphEdge{e})[0]
			edge = &er
		}
	}
	nodeResp := mapNodes([]db.ResearchGraphNode{node})[0]
	h.publish(protocol.EventResearchSessionGraphUpdated, workspaceID, "agent", uuidToString(member.AgentID), map[string]any{
		"session_id": uuidToString(sessionID),
		"node":       nodeResp,
		"edge":       edge,
	})
	writeJSON(w, http.StatusCreated, map[string]any{"node": nodeResp, "edge": edge})
}

type upsertSourceRequest struct {
	ID                string          `json:"id"`
	URL               string          `json:"url"`
	Title             string          `json:"title"`
	SourceClass       string          `json:"source_class"`
	CredibilityWeight *float64        `json:"credibility_weight"`
	Stance            string          `json:"stance"`
	Relevance         *float64        `json:"relevance"`
	Summary           string          `json:"summary"`
	Excerpt           string          `json:"excerpt"`
	Payload           json.RawMessage `json:"payload"`
}

func (h *Handler) UpsertResearchSourceHandler(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id")
	if !ok {
		return
	}
	member, ok := h.requireActiveFleetMember(w, r, wsUUID)
	if !ok {
		return
	}
	sessionID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	var req upsertSourceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	weight := 0.5
	if req.CredibilityWeight != nil {
		weight = *req.CredibilityWeight
	}
	rel := 0.5
	if req.Relevance != nil {
		rel = *req.Relevance
	}
	if req.SourceClass == "" {
		req.SourceClass = "other"
	}
	payload := req.Payload
	if len(payload) == 0 {
		payload = json.RawMessage(`{}`)
	}
	var source db.ResearchSource
	var err error
	if req.ID != "" {
		id, ok := parseUUIDOrBadRequest(w, req.ID, "id")
		if !ok {
			return
		}
		source, err = h.Queries.UpdateResearchSource(r.Context(), db.UpdateResearchSourceParams{
			ID:                id,
			WorkspaceID:       wsUUID,
			Url:               pgtype.Text{String: req.URL, Valid: req.URL != ""},
			Title:             pgtype.Text{String: req.Title, Valid: req.Title != ""},
			SourceClass:       pgtype.Text{String: req.SourceClass, Valid: true},
			CredibilityWeight: pgtype.Float8{Float64: weight, Valid: true},
			Stance:            pgtype.Text{String: req.Stance, Valid: true},
			Relevance:         pgtype.Float8{Float64: rel, Valid: true},
			Summary:           pgtype.Text{String: req.Summary, Valid: true},
			Excerpt:           pgtype.Text{String: req.Excerpt, Valid: true},
			Payload:           payload,
		})
	} else {
		source, err = h.Queries.UpsertResearchSource(r.Context(), db.UpsertResearchSourceParams{
			WorkspaceID:       wsUUID,
			SessionID:         sessionID,
			Url:               req.URL,
			Title:             req.Title,
			SourceClass:       req.SourceClass,
			CredibilityWeight: weight,
			Stance:            req.Stance,
			Relevance:         rel,
			Summary:           req.Summary,
			Excerpt:           req.Excerpt,
			Payload:           payload,
		})
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to upsert source")
		return
	}
	resp := mapSources([]db.ResearchSource{source})[0]
	h.publish(protocol.EventResearchSessionSourcesUpdated, workspaceID, "agent", uuidToString(member.AgentID), map[string]any{
		"session_id": uuidToString(sessionID),
		"source":     resp,
	})
	writeJSON(w, http.StatusOK, resp)
}

type patchReportRequest struct {
	ContentMD  string          `json:"content_md"`
	Structured json.RawMessage `json:"structured"`
	NewRevision bool           `json:"new_revision"`
}

func (h *Handler) PatchResearchReport(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id")
	if !ok {
		return
	}
	member, ok := h.requireActiveFleetMember(w, r, wsUUID)
	if !ok {
		return
	}
	sessionID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	var req patchReportRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	structured := req.Structured
	if len(structured) == 0 {
		structured = json.RawMessage(`{}`)
	}
	revision := int32(1)
	if latest, err := h.Queries.GetLatestResearchReport(r.Context(), db.GetLatestResearchReportParams{SessionID: sessionID, WorkspaceID: wsUUID}); err == nil {
		if req.NewRevision {
			revision = latest.Revision + 1
		} else {
			revision = latest.Revision
			// Always create a new row with same revision replaced via unique constraint —
			// use bump revision always for simplicity.
			revision = latest.Revision + 1
		}
	}
	rep, err := h.Queries.CreateResearchReport(r.Context(), db.CreateResearchReportParams{
		WorkspaceID: wsUUID,
		SessionID:   sessionID,
		Revision:    revision,
		ContentMd:   req.ContentMD,
		Structured:  structured,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to patch report")
		return
	}
	resp := researchReportToResp(rep)
	h.publish(protocol.EventResearchSessionReportUpdated, workspaceID, "agent", uuidToString(member.AgentID), map[string]any{
		"session_id": uuidToString(sessionID),
		"report":     resp,
	})
	writeJSON(w, http.StatusOK, resp)
}

type postResearchMessageRequest struct {
	Body          string `json:"body"`
	TargetAgentID string `json:"target_agent_id"`
}

func (h *Handler) PostResearchMessage(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id")
	if !ok {
		return
	}
	sessionID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	session, err := h.Queries.GetResearchSession(r.Context(), db.GetResearchSessionParams{ID: sessionID, WorkspaceID: wsUUID})
	if err != nil {
		writeError(w, http.StatusNotFound, "research session not found")
		return
	}
	var req postResearchMessageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Body) == "" {
		writeError(w, http.StatusBadRequest, "body is required")
		return
	}

	userID, userOK := requireUserID(w, r)
	if !userOK {
		return
	}
	senderType := "user"
	senderID := parseUUID(userID)
	actorType, actorID := h.resolveActor(r, userID, workspaceID)
	if actorType == "agent" {
		senderType = "agent"
		senderID = parseUUID(actorID)
		if _, ok := h.requireActiveFleetMember(w, r, wsUUID); !ok {
			return
		}
	}

	var target pgtype.UUID
	if req.TargetAgentID != "" {
		t, ok := parseUUIDOrBadRequest(w, req.TargetAgentID, "target_agent_id")
		if !ok {
			return
		}
		target = t
	} else if senderType == "user" {
		// Default route to 罗纳尔多
		fleet, err := h.Queries.GetResearchFleetByWorkspace(r.Context(), wsUUID)
		if err == nil && fleet.LeadAgentID.Valid {
			target = fleet.LeadAgentID
		}
	}

	msg, err := h.Queries.CreateResearchMessage(r.Context(), db.CreateResearchMessageParams{
		WorkspaceID:   wsUUID,
		SessionID:     session.ID,
		SenderType:    senderType,
		SenderID:      senderID,
		TargetAgentID: target,
		Body:          strings.TrimSpace(req.Body),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to post message")
		return
	}

	// Wake the target fleet agent (default: 罗纳尔多). Failures are logged but
	// do not roll back the persisted research message — user can retry.
	if target.Valid && senderType == "user" {
		if wakeErr := h.enqueueResearchAgentWake(r.Context(), wsUUID, session, target, parseUUID(userID), req.Body, senderType); wakeErr != nil {
			slog.Warn("research agent wake failed",
				"session_id", uuidToString(sessionID),
				"agent_id", uuidToString(target),
				"error", wakeErr,
			)
		}
	} else if target.Valid && senderType == "agent" {
		// Lead/member dispatch to another fleet member.
		initiator := session.CreatedBy
		if wakeErr := h.enqueueResearchAgentWake(r.Context(), wsUUID, session, target, initiator, req.Body, senderType); wakeErr != nil {
			slog.Warn("research dispatch wake failed",
				"session_id", uuidToString(sessionID),
				"agent_id", uuidToString(target),
				"error", wakeErr,
			)
		}
	}

	resp := mapMessages([]db.ResearchMessage{msg})[0]
	h.publish(protocol.EventResearchSessionMessage, workspaceID, actorType, actorID, map[string]any{
		"session_id": uuidToString(sessionID),
		"message":    resp,
	})
	writeJSON(w, http.StatusCreated, resp)
}

type presenceRequest struct {
	Activity string `json:"activity"`
}

func (h *Handler) PostResearchPresence(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id")
	if !ok {
		return
	}
	member, ok := h.requireActiveFleetMember(w, r, wsUUID)
	if !ok {
		return
	}
	sessionID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	var req presenceRequest
	_ = json.NewDecoder(r.Body).Decode(&req)
	h.publish(protocol.EventResearchSessionPresence, workspaceID, "agent", uuidToString(member.AgentID), map[string]any{
		"session_id": uuidToString(sessionID),
		"agent_id":   uuidToString(member.AgentID),
		"activity":   req.Activity,
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *Handler) RequestResearchStageEval(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id")
	if !ok {
		return
	}
	member, ok := h.requireResearchLeadActor(w, r, wsUUID)
	if !ok {
		return
	}
	sessionID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	session, err := h.Queries.GetResearchSession(r.Context(), db.GetResearchSessionParams{ID: sessionID, WorkspaceID: wsUUID})
	if err != nil {
		writeError(w, http.StatusNotFound, "research session not found")
		return
	}
	eval, nextStage, err := h.evaluateResearchStage(r.Context(), wsUUID, session)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	resp := mapEvals([]db.ResearchStageEval{eval})[0]
	h.publish(protocol.EventResearchSessionStageEval, workspaceID, "agent", uuidToString(member.AgentID), map[string]any{
		"session_id": uuidToString(sessionID),
		"eval":       resp,
	})
	if eval.Passed && nextStage != "" {
		params := db.UpdateResearchSessionParams{
			ID:          sessionID,
			WorkspaceID: wsUUID,
		}
		if nextStage == "done" {
			params.Status = pgtype.Text{String: "awaiting_user_confirm", Valid: true}
		} else {
			params.CurrentStage = pgtype.Text{String: nextStage, Valid: true}
			params.Status = pgtype.Text{String: "running", Valid: true}
		}
		updated, _ := h.Queries.UpdateResearchSession(r.Context(), params)
		h.publish(protocol.EventResearchSessionStatusChanged, workspaceID, "agent", uuidToString(member.AgentID), map[string]any{
			"session": researchSessionToResponse(updated),
		})
	}
	_, _ = h.Queries.CreateResearchGraphNode(r.Context(), db.CreateResearchGraphNodeParams{
		WorkspaceID:  wsUUID,
		SessionID:    sessionID,
		NodeType:     "stage_gate",
		Title:        session.CurrentStage,
		Summary:      eval.Remediation,
		Status:       ternary(eval.Passed, "done", "active"),
		ActorAgentID: member.AgentID,
		Payload:      marshalJSONRaw(map[string]any{"passed": eval.Passed, "score": eval.Score}),
	})
	writeJSON(w, http.StatusOK, resp)
}

func ternary(cond bool, a, b string) string {
	if cond {
		return a
	}
	return b
}

func (h *Handler) ConfirmResearchSession(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id")
	if !ok {
		return
	}
	userID, userOK := requireUserID(w, r)
	if !userOK {
		return
	}
	sessionID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	session, err := h.Queries.GetResearchSession(r.Context(), db.GetResearchSessionParams{ID: sessionID, WorkspaceID: wsUUID})
	if err != nil {
		writeError(w, http.StatusNotFound, "research session not found")
		return
	}
	if session.Status != "awaiting_user_confirm" && session.Status != "running" {
		writeError(w, http.StatusBadRequest, "session cannot be confirmed in current status")
		return
	}
	updated, err := h.Queries.UpdateResearchSession(r.Context(), db.UpdateResearchSessionParams{
		ID:          sessionID,
		WorkspaceID: wsUUID,
		Status:      pgtype.Text{String: "completed", Valid: true},
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to confirm session")
		return
	}
	h.publish(protocol.EventResearchSessionStatusChanged, workspaceID, "user", userID, map[string]any{
		"session": researchSessionToResponse(updated),
	})
	writeJSON(w, http.StatusOK, researchSessionToResponse(updated))
}

type researchHandoffRequest struct {
	CreateProject bool   `json:"create_project"`
	CreateChannel bool   `json:"create_channel"`
	ProjectTitle  string `json:"project_title"`
	ChannelName   string `json:"channel_name"`
}

func (h *Handler) ResearchSessionHandoff(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id")
	if !ok {
		return
	}
	userID, userOK := requireUserID(w, r)
	if !userOK {
		return
	}
	sessionID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	session, err := h.Queries.GetResearchSession(r.Context(), db.GetResearchSessionParams{ID: sessionID, WorkspaceID: wsUUID})
	if err != nil {
		writeError(w, http.StatusNotFound, "research session not found")
		return
	}
	if session.Status != "completed" && session.Status != "awaiting_user_confirm" {
		writeError(w, http.StatusBadRequest, "confirm research completion before handoff")
		return
	}
	var req researchHandoffRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	handoff := h.buildResearchHandoffSummary(r.Context(), wsUUID, session)
	var projectID pgtype.UUID
	var channelID pgtype.UUID

	if req.CreateProject {
		title := strings.TrimSpace(req.ProjectTitle)
		if title == "" {
			title = session.Title
		}
		p, err := h.Queries.CreateProject(r.Context(), db.CreateProjectParams{
			WorkspaceID: wsUUID,
			Title:       title,
			Description: pgtype.Text{String: handoff, Valid: true},
			Icon:        pgtype.Text{},
			Status:      "planned",
			LeadType:    pgtype.Text{},
			LeadID:      pgtype.UUID{},
			Priority:    "none",
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to create project")
			return
		}
		projectID = p.ID
		h.publish(protocol.EventProjectCreated, workspaceID, "user", userID, map[string]any{
			"project": projectToResponse(p),
		})
	}

	if req.CreateChannel {
		name := strings.TrimSpace(req.ChannelName)
		if name == "" {
			name = "开发-" + session.Title
		}
		// Reuse ordinary group channel creation path when available.
		ch, err := h.createOrdinaryGroupChannelForResearch(r, wsUUID, parseUUID(userID), name, handoff)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to create development channel: "+err.Error())
			return
		}
		channelID = ch
	}

	status := "completed"
	updated, err := h.Queries.UpdateResearchSession(r.Context(), db.UpdateResearchSessionParams{
		ID:             sessionID,
		WorkspaceID:    wsUUID,
		Status:         pgtype.Text{String: status, Valid: true},
		ProjectID:      projectID,
		ChannelID:      channelID,
		HandoffSummary: pgtype.Text{String: handoff, Valid: true},
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save handoff")
		return
	}
	h.publish(protocol.EventResearchSessionStatusChanged, workspaceID, "user", userID, map[string]any{
		"session": researchSessionToResponse(updated),
	})
	writeJSON(w, http.StatusOK, researchSessionToResponse(updated))
}

type hireFleetMemberRequest struct {
	Name         string `json:"name"`
	Role         string `json:"role"`
	Description  string `json:"description"`
	Instructions string `json:"instructions"`
}

func (h *Handler) HireResearchFleetMember(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id")
	if !ok {
		return
	}
	lead, ok := h.requireResearchLeadActor(w, r, wsUUID)
	if !ok {
		return
	}
	userID := requestUserID(r)
	var req hireFleetMemberRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Name) == "" || strings.TrimSpace(req.Role) == "" {
		writeError(w, http.StatusBadRequest, "name and role are required")
		return
	}
	runtime, okRuntime := h.pickGroupManagerRuntime(r.Context(), wsUUID, parseUUID(userID))
	if !okRuntime {
		writeError(w, http.StatusBadRequest, "no runtime available for hire")
		return
	}
	instructions := req.Instructions
	if instructions == "" {
		instructions = "Pending prompt optimization by 罗纳尔多. Do not accept tasks until activated."
	}
	agent, err := h.createAgentWithIdentity(r.Context(), h.Queries, db.CreateAgentParams{
		WorkspaceID:        wsUUID,
		Description:        req.Description,
		Instructions:       instructions,
		AvatarUrl:          pgtype.Text{},
		AvatarSource:       agentAvatarSourceAssigned,
		RuntimeMode:        runtime.RuntimeMode,
		RuntimeConfig:      []byte("{}"),
		RuntimeID:          runtime.ID,
		Visibility:         agentVisibilityPrivate,
		MaxConcurrentTasks: 3,
		OwnerID:            parseUUID(userID),
		CustomEnv:          []byte("{}"),
		CustomArgs:         []byte("[]"),
	}, req.Name, req.Name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to hire agent: "+err.Error())
		return
	}
	_ = h.Queries.SetAgentManagedRoleResearchFleet(r.Context(), db.SetAgentManagedRoleResearchFleetParams{ID: agent.ID, WorkspaceID: wsUUID})
	member, err := h.Queries.CreateResearchFleetMember(r.Context(), db.CreateResearchFleetMemberParams{
		WorkspaceID: wsUUID,
		FleetID:     lead.FleetID,
		AgentID:     agent.ID,
		Role:        req.Role,
		Status:      "pending_prompt_review",
		IsLead:      false,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to add fleet member")
		return
	}
	writeJSON(w, http.StatusCreated, ResearchFleetMemberResp{
		ID:      uuidToString(member.ID),
		AgentID: uuidToString(member.AgentID),
		Role:    member.Role,
		Status:  member.Status,
		IsLead:  member.IsLead,
		Name:    agent.Name,
		DisplayName: agent.DisplayName,
	})
}

type optimizeFleetMemberRequest struct {
	Instructions string `json:"instructions"`
	Activate     bool   `json:"activate"`
}

func (h *Handler) OptimizeResearchFleetMember(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id")
	if !ok {
		return
	}
	if _, ok := h.requireResearchLeadActor(w, r, wsUUID); !ok {
		return
	}
	memberID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "memberId"), "memberId")
	if !ok {
		return
	}
	var req optimizeFleetMemberRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Instructions) == "" {
		writeError(w, http.StatusBadRequest, "instructions required")
		return
	}
	fleet, err := h.Queries.GetResearchFleetByWorkspace(r.Context(), wsUUID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "fleet missing")
		return
	}
	members, err := h.Queries.ListResearchFleetMembers(r.Context(), db.ListResearchFleetMembersParams{
		FleetID:     fleet.ID,
		WorkspaceID: wsUUID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list members")
		return
	}
	var target *db.ResearchFleetMember
	for i := range members {
		if members[i].ID == memberID {
			target = &members[i]
			break
		}
	}
	if target == nil {
		writeError(w, http.StatusNotFound, "member not found")
		return
	}
	_, err = h.Queries.UpdateAgent(r.Context(), db.UpdateAgentParams{
		ID:                 target.AgentID,
		Instructions:       pgtype.Text{String: req.Instructions, Valid: true},
		AvatarSelectionSet: false,
		AvatarSource:       agentAvatarSourceAssigned,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update instructions")
		return
	}
	status := target.Status
	if req.Activate {
		status = "active"
	} else if status == "pending_prompt_review" {
		status = "pending_prompt_review"
	}
	updated, err := h.Queries.UpdateResearchFleetMemberStatus(r.Context(), db.UpdateResearchFleetMemberStatusParams{
		ID:          target.ID,
		WorkspaceID: wsUUID,
		Status:      status,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update member status")
		return
	}
	writeJSON(w, http.StatusOK, ResearchFleetMemberResp{
		ID:      uuidToString(updated.ID),
		AgentID: uuidToString(updated.AgentID),
		Role:    updated.Role,
		Status:  updated.Status,
		IsLead:  updated.IsLead,
	})
}

func (h *Handler) ArchiveResearchFleetMemberHandler(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id")
	if !ok {
		return
	}
	lead, ok := h.requireResearchLeadActor(w, r, wsUUID)
	if !ok {
		return
	}
	memberID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "memberId"), "memberId")
	if !ok {
		return
	}
	updated, err := h.Queries.ArchiveResearchFleetMember(r.Context(), db.ArchiveResearchFleetMemberParams{
		ID:          memberID,
		WorkspaceID: wsUUID,
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "member not found")
		return
	}
	if updated.IsLead {
		writeError(w, http.StatusBadRequest, "cannot archive research lead")
		return
	}
	_, _ = h.Queries.ArchiveAgent(r.Context(), db.ArchiveAgentParams{
		ID:         updated.AgentID,
		ArchivedBy: lead.AgentID,
	})
	writeJSON(w, http.StatusOK, ResearchFleetMemberResp{
		ID:      uuidToString(updated.ID),
		AgentID: uuidToString(updated.AgentID),
		Role:    updated.Role,
		Status:  updated.Status,
		IsLead:  updated.IsLead,
	})
}
