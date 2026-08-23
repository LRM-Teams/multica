package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/researchrun"
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
	session, err := h.Queries.GetResearchSession(r.Context(), db.GetResearchSessionParams{ID: sessionID, WorkspaceID: wsUUID})
	if err != nil {
		writeError(w, http.StatusNotFound, "research session not found")
		return
	}
	if h.rejectLegacyResearchMutation(w, r, wsUUID, sessionID) {
		return
	}
	if session.Status == "paused" {
		writeError(w, http.StatusConflict, "research session is paused")
		return
	}
	var req appendGraphNodeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	// Agents must not rewrite the user's session goal (LRM-898 / LRM-904).
	// Exploration may add subquestions/pivots; the authoritative goal stays on
	// research_session.goal and is user-owned.
	if strings.EqualFold(strings.TrimSpace(req.NodeType), "goal") && !researchAgentMayMutateSessionGoal() {
		writeError(w, http.StatusForbidden, "fleet agents cannot rewrite the user goal; only the user may change it mid-flight")
		return
	}
	// LRM-1076: soft open-branch budget — reject expand past budget + audit.
	if !h.enforceResearchOpenBranchBudget(r.Context(), w, session, req.NodeType) {
		return
	}
	if req.Status == "" {
		req.Status = "active"
	}
	payload := req.Payload
	if len(payload) == 0 {
		payload = json.RawMessage(`{}`)
	}
	fromNodeID := pgtype.UUID{}
	edgeType := ""
	if req.FromID != "" {
		fromID, ok := parseUUIDOrBadRequest(w, req.FromID, "from_node_id")
		if !ok {
			return
		}
		fromNodeID = fromID
		edgeType = req.EdgeType
		if edgeType == "" {
			edgeType = "leads_to"
		}
	}
	node, dbEdge, err := h.createResearchGraphNodeWithPassport(r.Context(), wsUUID, sessionID, db.CreateResearchGraphNodeParams{
		WorkspaceID:  wsUUID,
		SessionID:    sessionID,
		NodeType:     req.NodeType,
		Title:        strings.TrimSpace(req.Title),
		Summary:      req.Summary,
		Status:       req.Status,
		ActorAgentID: member.AgentID,
		Payload:      payload,
	}, fromNodeID, edgeType)
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to create graph node")
		return
	}
	var edge *ResearchGraphEdgeResp
	if dbEdge != nil {
		er := mapEdges([]db.ResearchGraphEdge{*dbEdge})[0]
		edge = &er
	}
	nodeResp := mapGraphNodeWithEdge(node, dbEdge)
	h.publish(protocol.EventResearchSessionGraphUpdated, workspaceID, "agent", uuidToString(member.AgentID), map[string]any{
		"session_id": uuidToString(sessionID),
		"node":       nodeResp,
		"edge":       edge,
	})
	h.emitResearchProcessCard(r.Context(), workspaceID, wsUUID, sessionID, "agent", uuidToString(member.AgentID), researchProcessEvent{
		Op:      "graph_append",
		Title:   node.Title,
		Body:    fmt.Sprintf("图更新 · %s：%s", node.NodeType, node.Title),
		ActorID: member.AgentID,
		Meta: map[string]any{
			"node_id":   uuidToString(node.ID),
			"node_type": node.NodeType,
			"status":    node.Status,
		},
	})
	h.maybeRecordResearchUnattendedMutation(r.Context(), session, "graph_append")
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
	Why               string          `json:"why"`
	DimensionFamily   string          `json:"dimension_family"`
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
	session, sessErr := h.Queries.GetResearchSession(r.Context(), db.GetResearchSessionParams{ID: sessionID, WorkspaceID: wsUUID})
	if sessErr != nil {
		writeError(w, http.StatusNotFound, "research session not found")
		return
	}
	if h.rejectLegacyResearchMutation(w, r, wsUUID, sessionID) {
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
	if req.Why != "" || req.DimensionFamily != "" {
		payload = mergeSourceWhyPayload(payload, req.Why, req.DimensionFamily)
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
	// Project high-signal sources onto the exploration canvas as finding nodes.
	if weight >= 0.7 {
		title := source.Title
		if title == "" {
			title = source.Url
		}
		if title == "" {
			title = source.SourceClass
		}
		_, _, _ = h.createResearchGraphNodePublished(r.Context(), workspaceID, wsUUID, sessionID, "agent", uuidToString(member.AgentID), db.CreateResearchGraphNodeParams{
			WorkspaceID:  wsUUID,
			SessionID:    sessionID,
			NodeType:     "finding",
			Title:        title,
			Summary:      source.Summary,
			Status:       "active",
			ActorAgentID: member.AgentID,
			Payload: marshalJSONRaw(map[string]any{
				"source_id":          uuidToString(source.ID),
				"source_class":       source.SourceClass,
				"credibility_weight": source.CredibilityWeight,
				"url":                source.Url,
			}),
		}, pgtype.UUID{}, "supports")
	}
	h.emitResearchProcessCard(r.Context(), workspaceID, wsUUID, sessionID, "agent", uuidToString(member.AgentID), researchProcessEvent{
		Op:      "source_upsert",
		Title:   source.Title,
		Body:    fmt.Sprintf("来源入库 · %s（权重 %.2f）", firstNonEmpty(source.Title, source.Url, source.SourceClass, "—"), weight),
		ActorID: member.AgentID,
		Meta: map[string]any{
			"source_id":          uuidToString(source.ID),
			"credibility_weight": weight,
			"source_class":       source.SourceClass,
		},
	})
	h.maybeRecordResearchUnattendedMutation(r.Context(), session, "source_upsert")
	writeJSON(w, http.StatusOK, resp)
}

type patchReportRequest struct {
	ContentMD   string          `json:"content_md"`
	Structured  json.RawMessage `json:"structured"`
	NewRevision bool            `json:"new_revision"`
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
	if h.rejectLegacyResearchMutation(w, r, wsUUID, sessionID) {
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
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to begin report revision")
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()
	qtx := h.Queries.WithTx(tx)

	rep, err := qtx.CreateResearchReport(r.Context(), db.CreateResearchReportParams{
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
	if _, err := tx.Exec(r.Context(), `
		UPDATE research_report
		SET author_agent_id = $1
		WHERE workspace_id = $2 AND session_id = $3 AND id = $4
	`, member.AgentID, wsUUID, sessionID, rep.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to bind report author")
		return
	}
	if err := researchrun.RegisterProductionReportRevisionTx(
		r.Context(),
		tx,
		workspaceID,
		uuidToString(sessionID),
		uuidToString(rep.ID),
	); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to register report revision artifact")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit report revision")
		return
	}
	resp := researchReportToResp(rep)
	h.publish(protocol.EventResearchSessionReportUpdated, workspaceID, "agent", uuidToString(member.AgentID), map[string]any{
		"session_id": uuidToString(sessionID),
		"report":     resp,
	})
	_, _, _ = h.createResearchGraphNodePublished(r.Context(), workspaceID, wsUUID, sessionID, "agent", uuidToString(member.AgentID), db.CreateResearchGraphNodeParams{
		WorkspaceID:  wsUUID,
		SessionID:    sessionID,
		NodeType:     "finding",
		Title:        fmt.Sprintf("报告修订 r%d", revision),
		Summary:      truncateRunes(req.ContentMD, 160),
		Status:       "done",
		ActorAgentID: member.AgentID,
		Payload:      marshalJSONRaw(map[string]any{"report_revision": revision, "kind": "report_revision"}),
	}, pgtype.UUID{}, "supports")
	h.emitResearchProcessCard(r.Context(), workspaceID, wsUUID, sessionID, "agent", uuidToString(member.AgentID), researchProcessEvent{
		Op:      "report_patch",
		Title:   fmt.Sprintf("报告 r%d", revision),
		Body:    fmt.Sprintf("报告已更新 · 修订 r%d", revision),
		ActorID: member.AgentID,
		Meta:    map[string]any{"revision": revision},
	})
	writeJSON(w, http.StatusOK, resp)
}

type postResearchMessageRequest struct {
	Body                 string                `json:"body"`
	Content              string                `json:"content"`
	TargetAgentID        string                `json:"target_agent_id"`
	ClientRequestID      string                `json:"client_request_id"`
	SelectedResearchRefs []selectedResearchRef `json:"selected_research_refs"`
}

type selectedResearchRef struct {
	StableID       string `json:"stable_id"`
	Kind           string `json:"kind"`
	EntityID       string `json:"entity_id"`
	Revision       int    `json:"revision"`
	ContentHash    string `json:"content_hash"`
	DisplaySummary string `json:"display_summary"`
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
	var orchestratorVersion string
	if err = h.DB.QueryRow(r.Context(), `SELECT orchestrator_version FROM research_session WHERE workspace_id=$1 AND id=$2`, wsUUID, sessionID).Scan(&orchestratorVersion); err != nil {
		writeError(w, http.StatusNotFound, "research session not found")
		return
	}
	var req postResearchMessageRequest
	if !decodeResearchJSON(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Content) != "" {
		if strings.TrimSpace(req.Body) != "" && strings.TrimSpace(req.Body) != strings.TrimSpace(req.Content) {
			writeError(w, http.StatusBadRequest, "body and content disagree")
			return
		}
		req.Body = req.Content
	}
	if strings.TrimSpace(req.Body) == "" {
		writeError(w, http.StatusBadRequest, "body is required")
		return
	}
	if req.ClientRequestID == "" {
		if orchestratorVersion == researchrun.OrchestratorVersionV6 {
			writeError(w, http.StatusBadRequest, "client_request_id is required")
			return
		}
		req.ClientRequestID = uuid.NewString()
	}
	if _, err = uuid.Parse(req.ClientRequestID); err != nil || len(req.SelectedResearchRefs) > 256 {
		writeError(w, http.StatusBadRequest, "steering identity or selected refs are invalid")
		return
	}
	for _, ref := range req.SelectedResearchRefs {
		allowedKind := map[string]bool{"goal": true, "branch": true, "task": true, "attempt": true, "work_item": true, "agent": true,
			"result": true, "insight": true, "discussion": true, "dispute": true, "integration": true, "report": true,
			"source_snapshot": true, "observation": true, "claim": true, "evidence_link": true}
		if _, parseErr := uuid.Parse(ref.EntityID); strings.TrimSpace(ref.StableID) != ref.Kind+":"+ref.EntityID || !allowedKind[ref.Kind] || parseErr != nil || ref.Revision < 1 ||
			!researchV6SHA256HashPattern.MatchString(ref.ContentHash) || len(ref.DisplaySummary) > 4096 {
			writeError(w, http.StatusBadRequest, "selected research ref is invalid")
			return
		}
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
		if session.Status == "paused" {
			writeError(w, http.StatusConflict, "research session is paused")
			return
		}
	}

	// User chat while paused / awaiting confirm resumes before wake.
	// LRM-840: reject-confirm tips post as chat and must leave awaiting_user_confirm
	// so status updates immediately (approve still uses POST /confirm → completed).
	if senderType == "user" {
		if touched, terr := h.Queries.TouchResearchSessionUserActivity(r.Context(), db.TouchResearchSessionUserActivityParams{
			ID:          sessionID,
			WorkspaceID: wsUUID,
		}); terr == nil {
			session = touched
		}
	}
	if senderType == "user" && (session.Status == "paused" || session.Status == "awaiting_user_confirm") {
		durableRun, ownershipErr := h.hasDurableResearchRun(r.Context(), wsUUID, sessionID)
		if ownershipErr != nil {
			writeError(w, http.StatusInternalServerError, "failed to inspect research run ownership")
			return
		}
		if durableRun {
			if h.ResearchRun == nil {
				writeError(w, http.StatusServiceUnavailable, "research run engine is unavailable")
				return
			}
			var resumeErr error
			if session.Status == "paused" {
				_, resumeErr = h.ResearchRun.Resume(r.Context(), uuidToString(sessionID), workspaceID, userID)
			} else {
				_, resumeErr = h.ResearchRun.Steer(r.Context(), researchrun.SteerInput{
					SessionID:   uuidToString(sessionID),
					WorkspaceID: workspaceID,
					UserID:      userID,
					Goal:        session.Goal,
					Reason:      "delivery_feedback: " + strings.TrimSpace(req.Body),
				})
			}
			if resumeErr != nil {
				writeError(w, http.StatusInternalServerError, "failed to resume session")
				return
			}
			session, _ = h.Queries.GetResearchSession(r.Context(), db.GetResearchSessionParams{ID: sessionID, WorkspaceID: wsUUID})
		} else {
			resumed, resumeErr := h.Queries.UpdateResearchSession(r.Context(), db.UpdateResearchSessionParams{
				ID:          sessionID,
				WorkspaceID: wsUUID,
				Status:      pgtype.Text{String: "running", Valid: true},
			})
			if resumeErr != nil {
				writeError(w, http.StatusInternalServerError, "failed to resume session")
				return
			}
			session = resumed
			h.publish(protocol.EventResearchSessionStatusChanged, workspaceID, "user", userID, map[string]any{
				"session": researchSessionToResponse(session),
			})
			h.emitResearchProcessCard(r.Context(), workspaceID, wsUUID, session.ID, "user", userID, researchProcessEvent{
				Op:    "session_resumed",
				Title: "调研已恢复",
				Body:  "舰队继续推进。",
				Meta:  map[string]any{"status": "running"},
			})
		}
	}

	var requested, fleetLead pgtype.UUID
	if req.TargetAgentID != "" {
		t, ok := parseUUIDOrBadRequest(w, req.TargetAgentID, "target_agent_id")
		if !ok {
			return
		}
		requested = t
	}
	if senderType == "user" && session.OrchestratorVersion != researchrun.OrchestratorVersionV6 {
		if fleet, ferr := h.Queries.GetResearchFleetByWorkspace(r.Context(), wsUUID); ferr == nil {
			fleetLead = fleet.LeadAgentID
		}
	}
	target := requested
	if senderType == "user" {
		target = resolveUserResearchMessageTarget(session.OrchestratorVersion, requested, h.loadActiveV6DirectorAgentID(r.Context(), session), fleetLead)
	}

	selectedRefs, _ := json.Marshal(req.SelectedResearchRefs)
	messageMeta, _ := json.Marshal(map[string]any{"client_request_id": req.ClientRequestID, "selected_research_refs": req.SelectedResearchRefs})
	params := db.CreateResearchMessageParams{
		WorkspaceID:   wsUUID,
		SessionID:     session.ID,
		SenderType:    senderType,
		SenderID:      senderID,
		TargetAgentID: target,
		Body:          strings.TrimSpace(req.Body),
		CardKind:      "chat",
		Meta:          messageMeta,
	}
	var msg db.ResearchMessage
	if senderType == "user" {
		msg, err = h.createResearchMessageWithPassportAndV6Steering(r.Context(), params, req.ClientRequestID, selectedRefs)
	} else {
		msg, err = h.createResearchMessageWithPassport(r.Context(), params)
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to post message")
		return
	}

	// Wake the target fleet agent (default: 罗纳尔多). Failures are logged but
	// do not roll back the persisted research message — surface a process card.
	if target.Valid && senderType == "user" {
		if wakeErr := h.enqueueResearchAgentWake(r.Context(), wsUUID, session, target, parseUUID(userID), req.Body, senderType, session.OrchestratorVersion != researchrun.OrchestratorVersionV6); wakeErr != nil {
			slog.Warn("research agent wake failed",
				"session_id", uuidToString(sessionID),
				"agent_id", uuidToString(target),
				"error", wakeErr,
			)
			h.emitResearchProcessCard(r.Context(), workspaceID, wsUUID, session.ID, actorType, actorID, researchWakeFailureEvent(target, wakeErr))
		}
	} else if target.Valid && senderType == "agent" {
		initiator := session.CreatedBy
		if wakeErr := h.enqueueResearchAgentWake(r.Context(), wsUUID, session, target, initiator, req.Body, senderType, session.OrchestratorVersion != researchrun.OrchestratorVersionV6); wakeErr != nil {
			slog.Warn("research dispatch wake failed",
				"session_id", uuidToString(sessionID),
				"agent_id", uuidToString(target),
				"error", wakeErr,
			)
			ev := researchWakeFailureEvent(target, wakeErr)
			ev.Title = "调度失败"
			h.emitResearchProcessCard(r.Context(), workspaceID, wsUUID, session.ID, actorType, actorID, ev)
		}
	} else if senderType == "user" && !target.Valid {
		h.emitResearchProcessCard(r.Context(), workspaceID, wsUUID, session.ID, actorType, actorID, researchProcessEvent{
			Op:    "wake_failed",
			Title: "无主理人",
			Body:  "当前 Run 没有可接收消息的 Director 或 Fleet Lead，消息已保存但无人接收。",
			Meta:  map[string]any{"reason": "missing_lead"},
		})
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

func (h *Handler) GetResearchPresence(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id")
	if !ok {
		return
	}
	sessionID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	sessionRow, err := h.Queries.GetResearchSession(r.Context(), db.GetResearchSessionParams{ID: sessionID, WorkspaceID: wsUUID})
	if err != nil {
		writeError(w, http.StatusNotFound, "research session not found")
		return
	}
	// V6 runs have a run-scoped team (not workspace fleet members) and track
	// execution in V6 work items — the V5 fleet/task roster below never sees
	// them, so derive presence from the V6 ledger instead.
	if sessionRow.OrchestratorVersion == researchrun.OrchestratorVersionV6 {
		presence, presenceErr := h.buildResearchV6PresenceRoster(r.Context(), wsUUID, sessionID, time.Now().UTC())
		if presenceErr != nil {
			writeError(w, http.StatusInternalServerError, "failed to load presence")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"session_id": uuidToString(sessionID),
			"presence":   presence,
		})
		return
	}
	nodes, err := h.Queries.ListResearchGraphNodes(r.Context(), db.ListResearchGraphNodesParams{
		SessionID:   sessionID,
		WorkspaceID: wsUUID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load presence")
		return
	}

	sessionKey := uuidToString(sessionID)
	var tasks []researchrun.Task
	var attempts []researchrun.Attempt
	runStage := ""
	// Prefer session-scoped fleet + run ledger when durable run-v2 exists.
	// Presence remains a derived view; attempts stay the execution SoT.
	members := []researchPresenceMember{}
	if h.ResearchRun != nil {
		if fm, ferr := h.ResearchRun.ListFleetMembers(r.Context(), sessionKey, workspaceID); ferr == nil && len(fm) > 0 {
			members = researchPresenceMembersFromRunFleet(fm)
		}
		if snap, serr := h.ResearchRun.Snapshot(r.Context(), sessionKey, workspaceID); serr == nil {
			tasks = snap.Tasks
			attempts = snap.Attempts
			runStage = strings.TrimSpace(snap.Run.CurrentStage)
		}
	}
	if len(members) == 0 {
		if fleet, ferr := h.Queries.GetResearchFleetByWorkspace(r.Context(), wsUUID); ferr == nil {
			rows, merr := h.Queries.ListResearchFleetMembers(r.Context(), db.ListResearchFleetMembersParams{
				FleetID:     fleet.ID,
				WorkspaceID: wsUUID,
			})
			if merr == nil {
				members = researchPresenceMembersFromFleet(rows)
			}
		}
	}
	if len(members) == 0 {
		// Fleet unavailable — still surface observed actors (legacy bootstrap).
		seen := map[string]struct{}{}
		for _, n := range nodes {
			if n.NodeType != "agent_activity" || !n.ActorAgentID.Valid {
				continue
			}
			id := uuidToString(n.ActorAgentID)
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			members = append(members, researchPresenceMember{AgentID: id})
		}
		for agentID := range presenceSignalsFromRun(sessionKey, runStage, tasks, attempts) {
			if _, ok := seen[agentID]; ok {
				continue
			}
			seen[agentID] = struct{}{}
			members = append(members, researchPresenceMember{AgentID: agentID})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"session_id": sessionKey,
		"presence": buildResearchPresenceRosterWithRun(
			members, nodes, tasks, attempts, sessionKey, runStage, time.Now().UTC(),
		),
	})
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
	activity := strings.TrimSpace(req.Activity)
	updatedAt := time.Now().UnixMilli()
	h.publish(protocol.EventResearchSessionPresence, workspaceID, "agent", uuidToString(member.AgentID), map[string]any{
		"session_id": uuidToString(sessionID),
		"agent_id":   uuidToString(member.AgentID),
		"activity":   activity,
		"updated_at": updatedAt,
	})
	// Presence drives canvas pulse/captions via WS; do not spam chat cards.
	// Only project a new activity chip when the caption actually changes.
	if activity != "" {
		existing, _ := h.Queries.ListResearchGraphNodes(r.Context(), db.ListResearchGraphNodesParams{
			SessionID:   sessionID,
			WorkspaceID: wsUUID,
		})
		skip := false
		for i := len(existing) - 1; i >= 0; i-- {
			n := existing[i]
			if n.NodeType != "agent_activity" || n.ActorAgentID != member.AgentID {
				continue
			}
			if n.Title == activity {
				skip = true
			}
			break
		}
		if !skip {
			_, _, _ = h.createResearchGraphNodePublished(r.Context(), workspaceID, wsUUID, sessionID, "agent", uuidToString(member.AgentID), db.CreateResearchGraphNodeParams{
				WorkspaceID:  wsUUID,
				SessionID:    sessionID,
				NodeType:     "agent_activity",
				Title:        activity,
				Summary:      activity,
				Status:       "active",
				ActorAgentID: member.AgentID,
				Payload:      marshalJSONRaw(map[string]any{"phase": "presence"}),
			}, pgtype.UUID{}, "leads_to")
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":         true,
		"agent_id":   uuidToString(member.AgentID),
		"activity":   activity,
		"updated_at": updatedAt,
	})
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
	if h.rejectLegacyResearchMutation(w, r, wsUUID, sessionID) {
		return
	}
	session, err := h.Queries.GetResearchSession(r.Context(), db.GetResearchSessionParams{ID: sessionID, WorkspaceID: wsUUID})
	if err != nil {
		writeError(w, http.StatusNotFound, "research session not found")
		return
	}
	// LRM-1076: before running S2→S3 content eval, require ≥2 open branches or single_line_confirmed.
	if session.CurrentStage == "s2_sources" {
		if okBranch, why := h.researchS2ParallelBranchOK(r.Context(), session); !okBranch {
			_, _ = h.Queries.CreateResearchSchedulerEvent(r.Context(), db.CreateResearchSchedulerEventParams{
				WorkspaceID: wsUUID,
				SessionID:   sessionID,
				EventType:   "s2_parallel_branch_gate",
				Detail:      marshalJSONRaw(map[string]any{"ok": false, "reason": why}),
			})
			writeError(w, http.StatusConflict, why)
			return
		}
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
	gate, _, _ := h.createResearchGraphNodePublished(r.Context(), workspaceID, wsUUID, sessionID, "agent", uuidToString(member.AgentID), db.CreateResearchGraphNodeParams{
		WorkspaceID:  wsUUID,
		SessionID:    sessionID,
		NodeType:     "stage_gate",
		Title:        session.CurrentStage,
		Summary:      eval.Remediation,
		Status:       ternary(eval.Passed, "done", "active"),
		ActorAgentID: member.AgentID,
		Payload:      marshalJSONRaw(map[string]any{"passed": eval.Passed, "score": eval.Score}),
	}, pgtype.UUID{}, "leads_to")
	_ = gate
	h.emitResearchProcessCard(r.Context(), workspaceID, wsUUID, sessionID, "agent", uuidToString(member.AgentID), researchProcessEvent{
		Op:      "stage_eval",
		Title:   session.CurrentStage,
		Body:    eval.Remediation,
		ActorID: member.AgentID,
		Meta: map[string]any{
			"stage":  session.CurrentStage,
			"passed": eval.Passed,
			"score":  eval.Score,
		},
	})
	writeJSON(w, http.StatusOK, resp)
}

func ternary(cond bool, a, b string) string {
	if cond {
		return a
	}
	return b
}

func (h *Handler) stopResearchSessionWakes(ctx context.Context, workspaceID, sessionID pgtype.UUID) error {
	title := researchChatSessionTitle(sessionID)
	rows, err := h.Queries.CancelInFlightChatTasksByResearchTitle(ctx, db.CancelInFlightChatTasksByResearchTitleParams{
		WorkspaceID: workspaceID,
		Title:       title,
	})
	if err != nil {
		slog.Warn("research stop: cancel wakes failed",
			"session_id", uuidToString(sessionID),
			"error", err,
		)
		return err
	}
	if h.TaskService == nil {
		return nil
	}
	// Rows are already cancelled in SQL; finalize chat/research snapshot + broadcast.
	h.TaskService.FinalizeCancelledResearchWakes(ctx, rows)
	return nil
}

// StopResearchSession pauses a running research session. The session remains
// readable; the next user chat message resumes it (status → running + wake).
func (h *Handler) StopResearchSession(w http.ResponseWriter, r *http.Request) {
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
	switch session.Status {
	case "paused":
		if err = h.stopResearchSessionWakes(r.Context(), wsUUID, sessionID); err != nil {
			writeError(w, http.StatusInternalServerError, "research session is paused but wake cancellation is still pending")
			return
		}
		writeJSON(w, http.StatusOK, researchSessionToResponse(session))
		return
	case "running", "awaiting_user_confirm", "drafting":
		// ok
	default:
		writeError(w, http.StatusBadRequest, "session cannot be stopped in current status")
		return
	}

	durableRun, ownershipErr := h.hasDurableResearchRun(r.Context(), wsUUID, sessionID)
	if ownershipErr != nil {
		writeError(w, http.StatusInternalServerError, "failed to inspect research run ownership")
		return
	}
	var updated db.ResearchSession
	if durableRun {
		if h.ResearchRun == nil {
			writeError(w, http.StatusServiceUnavailable, "research run engine is unavailable")
			return
		}
		if _, err = h.ResearchRun.Pause(r.Context(), uuidToString(sessionID), workspaceID, userID); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to stop session")
			return
		}
		if err = h.stopResearchSessionWakes(r.Context(), wsUUID, sessionID); err != nil {
			writeError(w, http.StatusInternalServerError, "research session paused but wake cancellation failed")
			return
		}
		updated, err = h.Queries.GetResearchSession(r.Context(), db.GetResearchSessionParams{ID: sessionID, WorkspaceID: wsUUID})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to reload stopped session")
			return
		}
	} else {
		if err = h.stopResearchSessionWakes(r.Context(), wsUUID, sessionID); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to cancel active research tasks")
			return
		}
		updated, err = h.Queries.UpdateResearchSession(r.Context(), db.UpdateResearchSessionParams{
			ID:          sessionID,
			WorkspaceID: wsUUID,
			Status:      pgtype.Text{String: "paused", Valid: true},
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to stop session")
			return
		}
		h.publish(protocol.EventResearchSessionStatusChanged, workspaceID, "user", userID, map[string]any{
			"session": researchSessionToResponse(updated),
		})
		h.emitResearchProcessCard(r.Context(), workspaceID, wsUUID, session.ID, "user", userID, researchProcessEvent{
			Op:    "session_stopped",
			Title: "调研已暂停",
			Body:  "舰队已停止推进。在对话里继续发言即可恢复。",
			Meta:  map[string]any{"status": "paused"},
		})
	}
	writeJSON(w, http.StatusOK, researchSessionToResponse(updated))
}

// DeleteResearchSession permanently removes legacy sessions. V6 sessions are
// archived because their canonical facts are append-only and may only be
// removed by the enclosing Workspace deletion policy.
func (h *Handler) DeleteResearchSession(w http.ResponseWriter, r *http.Request) {
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
	durableRun, ownershipErr := h.hasDurableResearchRun(r.Context(), wsUUID, sessionID)
	if ownershipErr != nil {
		writeError(w, http.StatusInternalServerError, "failed to inspect research run ownership")
		return
	}
	if durableRun && session.Status != string(researchrun.RunStatusCompleted) &&
		session.Status != string(researchrun.RunStatusArchived) &&
		session.Status != string(researchrun.RunStatusCancelled) {
		if h.ResearchRun == nil {
			writeError(w, http.StatusServiceUnavailable, "research run engine is unavailable")
			return
		}
		if _, err = h.ResearchRun.Cancel(r.Context(), uuidToString(sessionID), workspaceID, userID, "research session deleted"); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to cancel research run before deletion")
			return
		}
	}

	if err = h.stopResearchSessionWakes(r.Context(), wsUUID, sessionID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to cancel active research tasks")
		return
	}
	if session.OrchestratorVersion == researchrun.OrchestratorVersionV6 {
		archived, archiveErr := h.Queries.UpdateResearchSession(r.Context(), db.UpdateResearchSessionParams{
			ID:          sessionID,
			WorkspaceID: wsUUID,
			Status:      pgtype.Text{String: "archived", Valid: true},
		})
		if archiveErr != nil {
			writeError(w, http.StatusInternalServerError, "failed to archive research session")
			return
		}
		h.publish(protocol.EventResearchSessionStatusChanged, workspaceID, "user", userID, map[string]any{
			"session":  researchSessionToResponse(archived),
			"archived": true,
		})
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if err := h.Queries.DeleteResearchSession(r.Context(), db.DeleteResearchSessionParams{
		ID:          sessionID,
		WorkspaceID: wsUUID,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete session")
		return
	}
	h.publish(protocol.EventResearchSessionStatusChanged, workspaceID, "user", userID, map[string]any{
		"session_id": uuidToString(session.ID),
		"deleted":    true,
	})
	w.WriteHeader(http.StatusNoContent)
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
	durableRun, ownershipErr := h.hasDurableResearchRun(r.Context(), wsUUID, sessionID)
	if ownershipErr != nil {
		writeError(w, http.StatusInternalServerError, "failed to inspect research run ownership")
		return
	}
	var updated db.ResearchSession
	if durableRun {
		if session.Status != "awaiting_user_confirm" {
			writeError(w, http.StatusBadRequest, "session cannot be confirmed in current status")
			return
		}
		if h.ResearchRun == nil {
			writeError(w, http.StatusServiceUnavailable, "research run engine is unavailable")
			return
		}
		if _, err = h.ResearchRun.Confirm(r.Context(), uuidToString(sessionID), workspaceID, userID); err != nil {
			writeError(w, http.StatusConflict, "research delivery gate no longer passes")
			return
		}
		updated, err = h.Queries.GetResearchSession(r.Context(), db.GetResearchSessionParams{ID: sessionID, WorkspaceID: wsUUID})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to reload completed session")
			return
		}
	} else {
		if session.Status != "awaiting_user_confirm" && session.Status != "running" {
			writeError(w, http.StatusBadRequest, "session cannot be confirmed in current status")
			return
		}
		// LRM-1076: completed requires S4 + non-empty report + evidence gate.
		// Early stop must use POST /archive (status=archived), not completed.
		nodes, _ := h.Queries.ListResearchGraphNodes(r.Context(), db.ListResearchGraphNodesParams{SessionID: sessionID, WorkspaceID: wsUUID})
		edges, _ := h.Queries.ListResearchGraphEdges(r.Context(), db.ListResearchGraphEdgesParams{SessionID: sessionID, WorkspaceID: wsUUID})
		sources, _ := h.Queries.ListResearchSources(r.Context(), db.ListResearchSourcesParams{SessionID: sessionID, WorkspaceID: wsUUID})
		var reportPtr *db.ResearchReport
		if rep, rerr := h.Queries.GetLatestResearchReport(r.Context(), db.GetLatestResearchReportParams{SessionID: sessionID, WorkspaceID: wsUUID}); rerr == nil {
			reportPtr = &rep
		}
		if blockers := researchCompletionBlockers(session, nodes, edges, sources, reportPtr); len(blockers) > 0 {
			_, _ = h.Queries.CreateResearchSchedulerEvent(r.Context(), db.CreateResearchSchedulerEventParams{
				WorkspaceID: wsUUID,
				SessionID:   sessionID,
				EventType:   "completed_rejected",
				Detail:      marshalJSONRaw(map[string]any{"blockers": blockers}),
			})
			writeError(w, http.StatusConflict, "cannot complete research session: "+strings.Join(blockers, "; ")+
				". Use archive for early stop; research truth surface remains graph+sources+stages (not Goal+chat).")
			return
		}
		updated, err = h.Queries.UpdateResearchSession(r.Context(), db.UpdateResearchSessionParams{
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
	}
	strategyVersion := "research-v5-default"
	if h.DB != nil {
		_ = h.DB.QueryRow(r.Context(), `
			SELECT v.version_key FROM research_run_strategy_assignment a
			JOIN research_strategy_version v ON v.workspace_id=a.workspace_id AND v.id=a.strategy_version_id
			WHERE a.workspace_id=$1::uuid AND a.session_id=$2::uuid
		`, workspaceID, uuidToString(sessionID)).Scan(&strategyVersion)
	}
	budget := float64(updated.ProductRoundBudget)
	if budget <= 0 {
		budget = 1
	}
	_ = h.RecordResearchProductionEpisode(r.Context(), workspaceID, uuidToString(sessionID), strategyVersion, 0, budget)
	h.awardHonorXP(r.Context(), parseUUID(userID), "research.session", uuidToString(sessionID))
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
	// LRM-1076: handoff must not mint completed without S4/report/evidence,
	// and must not treat Goal+channel as the research truth surface.
	if session.Status != "awaiting_user_confirm" {
		nodes, _ := h.Queries.ListResearchGraphNodes(r.Context(), db.ListResearchGraphNodesParams{SessionID: sessionID, WorkspaceID: wsUUID})
		edges, _ := h.Queries.ListResearchGraphEdges(r.Context(), db.ListResearchGraphEdgesParams{SessionID: sessionID, WorkspaceID: wsUUID})
		sources, _ := h.Queries.ListResearchSources(r.Context(), db.ListResearchSourcesParams{SessionID: sessionID, WorkspaceID: wsUUID})
		var reportPtr *db.ResearchReport
		if rep, rerr := h.Queries.GetLatestResearchReport(r.Context(), db.GetLatestResearchReportParams{SessionID: sessionID, WorkspaceID: wsUUID}); rerr == nil {
			reportPtr = &rep
		}
		if blockers := researchCompletionBlockers(session, nodes, edges, sources, reportPtr); len(blockers) > 0 {
			writeError(w, http.StatusConflict, "handoff cannot complete research session: "+strings.Join(blockers, "; ")+
				". Archive for early stop; graph+sources+stages remain the truth surface.")
			return
		}
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
	Model        string `json:"model"`
	Reason       string `json:"reason"`  // specialty gap / why hire (audit + canvas)
	Fixture      bool   `json:"fixture"` // capacity/409 test only; skips canvas projection
}

type optimizeFleetMemberRequest struct {
	Instructions string `json:"instructions"`
	Model        string `json:"model"`
	Activate     bool   `json:"activate"`
	Reason       string `json:"reason"`
}

type archiveFleetMemberRequest struct {
	Reason  string `json:"reason"`
	Fixture bool   `json:"fixture"` // capacity fixture cleanup only
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
	role := strings.TrimSpace(req.Role)
	if strings.EqualFold(role, "lead") {
		writeError(w, http.StatusBadRequest, "cannot hire another lead; use existing 罗纳尔多")
		return
	}

	members, err := h.Queries.ListResearchFleetMembers(r.Context(), db.ListResearchFleetMembersParams{
		FleetID:     lead.FleetID,
		WorkspaceID: wsUUID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list members")
		return
	}
	fixture := researchRosterFixtureRequested(r.Header.Get(researchRosterFixtureHeader), req.Fixture)
	if gapErr := validateResearchHireGap(req.Name, role, req.Reason, members, fixture); gapErr != nil {
		writeError(w, http.StatusBadRequest, gapErr.Error())
		return
	}
	activeCount := countNonArchivedFleetMembers(members)
	if researchRosterAtCap(activeCount) {
		writeError(w, http.StatusConflict, fmt.Sprintf(
			"fleet roster at cap (%d active members); archive idle members before hiring (depth budget)",
			researchFleetMaxActiveMembers,
		))
		return
	}

	runtime, okRuntime := h.pickAgentRuntime(r.Context(), wsUUID, parseUUID(userID))
	if !okRuntime {
		writeError(w, http.StatusBadRequest, "no runtime available for hire")
		return
	}
	instructions := strings.TrimSpace(req.Instructions)
	if instructions == "" {
		instructions = "Pending prompt optimization by 罗纳尔多. Do not accept tasks until activated."
	}
	model := resolveResearchHireModel(req.Model, runtime.Provider)
	reason := strings.TrimSpace(req.Reason)
	if fixture && reason == "" {
		reason = "capacity fixture hire"
	}

	agent, err := h.createAgentWithIdentity(r.Context(), h.Queries, db.CreateAgentParams{
		WorkspaceID:   wsUUID,
		Description:   req.Description,
		Instructions:  instructions,
		AvatarUrl:     pgtype.Text{},
		AvatarSource:  agentAvatarSourceAssigned,
		RuntimeMode:   runtime.RuntimeMode,
		RuntimeConfig: []byte("{}"),
		RuntimeID:     runtime.ID,
		OwnerID:       parseUUID(userID),
		CustomEnv:     []byte("{}"),
		CustomArgs:    []byte("[]"),
		Model:         model,
		ThinkingLevel: pgtype.Text{},
	}, req.Name, req.Name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to hire agent: "+err.Error())
		return
	}
	member, err := h.Queries.CreateResearchFleetMember(r.Context(), db.CreateResearchFleetMemberParams{
		WorkspaceID: wsUUID,
		FleetID:     lead.FleetID,
		AgentID:     agent.ID,
		Role:        role,
		Status:      "pending_prompt_review",
		IsLead:      false,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to add fleet member")
		return
	}

	// Fixture hires intentionally skip canvas/process projection (LRM-918 H3).
	if !fixture {
		h.projectRosterChangeToActiveSessions(r.Context(), workspaceID, wsUUID, lead, rosterChangeProjection{
			Action:    "hire",
			Member:    member,
			AgentName: firstNonEmpty(agent.DisplayName, agent.Name),
			Title:     fmt.Sprintf("新成员 · %s", firstNonEmpty(agent.DisplayName, agent.Name)),
			Summary:   fmt.Sprintf("角色 %s 待提示词优化后激活 · %s", role, reason),
			Op:        "roster_hire",
			CardBody:  fmt.Sprintf("编制变更 · 雇佣 %s（%s），待提示词优化。原因：%s", firstNonEmpty(agent.DisplayName, agent.Name), role, reason),
			Reason:    reason,
			Model:     model.String,
			ExtraPayload: map[string]any{
				"status":        member.Status,
				"member_status": member.Status,
			},
		})
	}

	writeJSON(w, http.StatusCreated, ResearchFleetMemberResp{
		ID:          uuidToString(member.ID),
		AgentID:     uuidToString(member.AgentID),
		Role:        member.Role,
		Status:      member.Status,
		IsLead:      member.IsLead,
		Name:        agent.Name,
		DisplayName: agent.DisplayName,
	})
}

func (h *Handler) OptimizeResearchFleetMember(w http.ResponseWriter, r *http.Request) {
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
	if target.Status == "archived" {
		writeError(w, http.StatusConflict, "cannot optimize archived member")
		return
	}

	updateParams := db.UpdateAgentParams{
		ID:                 target.AgentID,
		Instructions:       pgtype.Text{String: req.Instructions, Valid: true},
		AvatarSelectionSet: false,
		AvatarSource:       agentAvatarSourceAssigned,
	}
	if model := strings.TrimSpace(req.Model); model != "" {
		updateParams.Model = pgtype.Text{String: model, Valid: true}
	}
	updatedAgent, err := h.Queries.UpdateAgent(r.Context(), updateParams)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update instructions")
		return
	}

	prevStatus := target.Status
	status := target.Status
	if req.Activate {
		status = "active"
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

	reason := strings.TrimSpace(req.Reason)
	if reason == "" {
		if req.Activate {
			reason = "optimize + activate"
		} else {
			reason = "optimize instructions"
		}
	}
	agentName := firstNonEmpty(updatedAgent.DisplayName, updatedAgent.Name, updated.Role)
	h.projectRosterChangeToActiveSessions(r.Context(), workspaceID, wsUUID, lead, rosterChangeProjection{
		Action:    "optimize",
		Member:    updated,
		AgentName: agentName,
		Title:     fmt.Sprintf("优化 · %s", agentName),
		Summary:   fmt.Sprintf("%s → %s · %s", prevStatus, updated.Status, reason),
		Op:        "roster_optimize",
		CardBody:  fmt.Sprintf("编制变更 · 优化 %s（%s→%s）。原因：%s", agentName, prevStatus, updated.Status, reason),
		Reason:    reason,
		Model:     updatedAgent.Model.String,
		ExtraPayload: map[string]any{
			"prev_status":   prevStatus,
			"status":        updated.Status,
			"member_status": updated.Status,
			"activated":     req.Activate,
		},
	})

	// H2: activation must kick off observable work (activity node + wake).
	if req.Activate && updated.Status == "active" {
		h.assignWorkAfterRosterActivate(
			r.Context(),
			workspaceID,
			wsUUID,
			lead,
			updated,
			agentName,
			parseUUID(requestUserID(r)),
		)
	}

	writeJSON(w, http.StatusOK, ResearchFleetMemberResp{
		ID:          uuidToString(updated.ID),
		AgentID:     uuidToString(updated.AgentID),
		Role:        updated.Role,
		Status:      updated.Status,
		IsLead:      updated.IsLead,
		Name:        updatedAgent.Name,
		DisplayName: updatedAgent.DisplayName,
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
	var req archiveFleetMemberRequest
	_ = json.NewDecoder(r.Body).Decode(&req) // body optional

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
	if target.IsLead {
		writeError(w, http.StatusBadRequest, "cannot archive research lead")
		return
	}
	if target.Status == "archived" {
		writeJSON(w, http.StatusOK, ResearchFleetMemberResp{
			ID:      uuidToString(target.ID),
			AgentID: uuidToString(target.AgentID),
			Role:    target.Role,
			Status:  target.Status,
			IsLead:  target.IsLead,
		})
		return
	}

	fixture := researchRosterFixtureRequested(r.Header.Get(researchRosterFixtureHeader), req.Fixture)
	hasWork := h.researchAgentHasObservableWork(r.Context(), wsUUID, fleet.ID, target.AgentID)
	var hiredAt time.Time
	if target.CreatedAt.Valid {
		hiredAt = target.CreatedAt.Time
	}
	if churnErr := validateResearchArchiveAntiChurn(*target, hiredAt, hasWork, fixture, time.Now().UTC()); churnErr != nil {
		writeError(w, http.StatusConflict, churnErr.Error())
		return
	}

	reason := strings.TrimSpace(req.Reason)
	if reason == "" {
		reason = "idle or low effectiveness"
	}

	updated, err := h.Queries.ArchiveResearchFleetMember(r.Context(), db.ArchiveResearchFleetMemberParams{
		ID:          memberID,
		WorkspaceID: wsUUID,
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "member not found")
		return
	}

	agentName := updated.Role
	if agent, aerr := h.Queries.GetAgent(r.Context(), updated.AgentID); aerr == nil {
		agentName = firstNonEmpty(agent.DisplayName, agent.Name, updated.Role)
	}

	_, _ = h.Queries.ArchiveAgent(r.Context(), db.ArchiveAgentParams{
		ID:         updated.AgentID,
		ArchivedBy: lead.AgentID,
	})
	// Stop further wakes: cancel in-flight inbox tasks for this agent.
	if cancelled, cerr := h.Queries.CancelAgentTasksByAgent(r.Context(), updated.AgentID); cerr != nil {
		slog.Warn("research archive: cancel wakes failed",
			"agent_id", uuidToString(updated.AgentID),
			"error", cerr,
		)
	} else if h.TaskService != nil {
		h.TaskService.CaptureCancelledTasks(r.Context(), cancelled)
		h.TaskService.ReconcileAgentStatus(r.Context(), updated.AgentID)
	}

	if !fixture {
		h.projectRosterChangeToActiveSessions(r.Context(), workspaceID, wsUUID, lead, rosterChangeProjection{
			Action:    "archive",
			Member:    updated,
			AgentName: agentName,
			Title:     fmt.Sprintf("减员 · %s", agentName),
			Summary:   fmt.Sprintf("已归档 · 角色 %s · %s", updated.Role, reason),
			Op:        "roster_archive",
			CardBody:  fmt.Sprintf("编制变更 · 已归档 %s（%s）。原因：%s；已停止唤醒", agentName, updated.Role, reason),
			Reason:    reason,
			ExtraPayload: map[string]any{
				"status":         updated.Status,
				"member_status":  "archived",
				"display_status": "已归档",
			},
		})
	}

	writeJSON(w, http.StatusOK, ResearchFleetMemberResp{
		ID:      uuidToString(updated.ID),
		AgentID: uuidToString(updated.AgentID),
		Role:    updated.Role,
		Status:  updated.Status,
		IsLead:  updated.IsLead,
	})
}

type rosterChangeProjection struct {
	Action       string
	Member       db.ResearchFleetMember
	AgentName    string
	Title        string
	Summary      string
	Op           string
	CardBody     string
	Reason       string
	Model        string
	ExtraPayload map[string]any
}

// projectRosterChangeToActiveSessions writes roster_change graph nodes + process
// cards onto every running / awaiting_user_confirm session for this fleet.
func (h *Handler) projectRosterChangeToActiveSessions(
	ctx context.Context,
	workspaceID string,
	wsUUID pgtype.UUID,
	lead db.ResearchFleetMember,
	p rosterChangeProjection,
) {
	sessions, _ := h.Queries.ListResearchSessions(ctx, wsUUID)
	for _, s := range sessions {
		if s.FleetID != lead.FleetID {
			continue
		}
		if s.Status != "running" && s.Status != "awaiting_user_confirm" {
			continue
		}
		payload := map[string]any{
			"action":    p.Action,
			"member_id": uuidToString(p.Member.ID),
			"agent_id":  uuidToString(p.Member.AgentID),
			"role":      p.Member.Role,
			"reason":    p.Reason,
		}
		if p.Model != "" {
			payload["model"] = p.Model
		}
		for k, v := range p.ExtraPayload {
			payload[k] = v
		}
		nodeStatus := researchRosterGraphStatus(p.Action)
		_, _, _ = h.createResearchGraphNodePublished(ctx, workspaceID, wsUUID, s.ID, "agent", uuidToString(lead.AgentID), db.CreateResearchGraphNodeParams{
			WorkspaceID:  wsUUID,
			SessionID:    s.ID,
			NodeType:     "roster_change",
			Title:        p.Title,
			Summary:      p.Summary,
			Status:       nodeStatus,
			ActorAgentID: lead.AgentID,
			Payload:      marshalJSONRaw(payload),
		}, pgtype.UUID{}, "leads_to")
		cardMeta := map[string]any{
			"action":        p.Action,
			"member_id":     uuidToString(p.Member.ID),
			"role":          p.Member.Role,
			"reason":        p.Reason,
			"member_status": p.Member.Status,
			"node_status":   nodeStatus,
		}
		if p.Action == "archive" {
			cardMeta["display_status"] = "已归档"
		}
		h.emitResearchProcessCard(ctx, workspaceID, wsUUID, s.ID, "agent", uuidToString(lead.AgentID), researchProcessEvent{
			Op:      p.Op,
			Title:   p.AgentName,
			Body:    p.CardBody,
			ActorID: lead.AgentID,
			Meta:    cardMeta,
		})
	}
}
