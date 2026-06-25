package handler

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type EvolutionDeliveryResponse struct {
	ID               string                       `json:"id"`
	WorkspaceID      string                       `json:"workspace_id"`
	UnitID           string                       `json:"unit_id"`
	VersionID        string                       `json:"version_id"`
	TargetAgentID    string                       `json:"target_agent_id"`
	DeliveryType     string                       `json:"delivery_type"`
	Status           string                       `json:"status"`
	Reason           string                       `json:"reason"`
	MatcherScore     float64                      `json:"matcher_score"`
	UnitType         string                       `json:"unit_type"`
	Title            string                       `json:"title"`
	CanonicalSummary string                       `json:"canonical_summary"`
	Content          string                       `json:"content"`
	Metadata         json.RawMessage              `json:"metadata"`
	Applies          json.RawMessage              `json:"applies"`
	Tags             []string                     `json:"tags,omitempty"`
	Tools            []string                     `json:"tools,omitempty"`
	TaskTypes        []string                     `json:"task_types,omitempty"`
	ProjectTypes     []string                     `json:"project_types,omitempty"`
	Languages        []string                     `json:"languages,omitempty"`
	Frameworks       []string                     `json:"frameworks,omitempty"`
	Files            []EvolutionDeliveryFileReply `json:"files,omitempty"`
}

type EvolutionDeliveryFileReply struct {
	Path        string `json:"path"`
	Content     string `json:"content"`
	ContentHash string `json:"content_hash"`
	MimeType    string `json:"mime_type"`
}

type evolutionDeliveryStatusRequest struct {
	AgentID       string `json:"agent_id"`
	DeliveredPath string `json:"delivered_path,omitempty"`
	Error         string `json:"error,omitempty"`
	Decision      string `json:"decision,omitempty"`
}

func (h *Handler) ListEvolutionDeliveries(w http.ResponseWriter, r *http.Request) {
	rt, agent, ok := h.requireRuntimeBoundAgentFromRequest(w, r)
	if !ok {
		return
	}
	limit := int32(20)
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 && parsed <= 100 {
			limit = int32(parsed)
		}
	}
	rows, err := h.Queries.ListPendingEvolutionDeliveriesByAgent(r.Context(), db.ListPendingEvolutionDeliveriesByAgentParams{WorkspaceID: rt.WorkspaceID, TargetAgentID: agent.ID, LimitCount: limit})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list evolution deliveries")
		return
	}
	items := make([]EvolutionDeliveryResponse, 0, len(rows))
	for _, row := range rows {
		files, err := h.Queries.ListSharedEvolutionUnitFiles(r.Context(), db.ListSharedEvolutionUnitFilesParams{WorkspaceID: rt.WorkspaceID, UnitID: row.UnitID, VersionID: row.VersionID})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list evolution delivery files")
			return
		}
		items = append(items, evolutionDeliveryResponse(row, files))
	}
	writeJSON(w, http.StatusOK, map[string]any{"deliveries": items})
}

func (h *Handler) MarkEvolutionDeliveryDelivered(w http.ResponseWriter, r *http.Request) {
	rt, agent, ok := h.requireRuntimeBoundAgentFromRequest(w, r)
	if !ok {
		return
	}
	var req evolutionDeliveryStatusRequest
	_ = json.NewDecoder(r.Body).Decode(&req)
	deliveryID, ok := parseDeliveryIDOrBadRequest(w, chi.URLParam(r, "deliveryId"))
	if !ok {
		return
	}
	delivery, err := h.Queries.MarkEvolutionDeliveryDelivered(r.Context(), db.MarkEvolutionDeliveryDeliveredParams{ID: deliveryID, WorkspaceID: rt.WorkspaceID, TargetAgentID: agent.ID, DeliveredPath: sanitizeNullBytes(req.DeliveredPath)})
	if err != nil {
		writeDeliveryMutationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"delivery": delivery})
}

func (h *Handler) FailEvolutionDelivery(w http.ResponseWriter, r *http.Request) {
	rt, agent, ok := h.requireRuntimeBoundAgentFromRequest(w, r)
	if !ok {
		return
	}
	var req evolutionDeliveryStatusRequest
	_ = json.NewDecoder(r.Body).Decode(&req)
	deliveryID, ok := parseDeliveryIDOrBadRequest(w, chi.URLParam(r, "deliveryId"))
	if !ok {
		return
	}
	delivery, err := h.Queries.FailEvolutionDelivery(r.Context(), db.FailEvolutionDeliveryParams{ID: deliveryID, WorkspaceID: rt.WorkspaceID, TargetAgentID: agent.ID, Error: sanitizeNullBytes(req.Error)})
	if err != nil {
		writeDeliveryMutationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"delivery": delivery})
}

func (h *Handler) DecideEvolutionDelivery(w http.ResponseWriter, r *http.Request) {
	rt, agent, ok := h.requireRuntimeBoundAgentFromRequest(w, r)
	if !ok {
		return
	}
	var req evolutionDeliveryStatusRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	decision := strings.TrimSpace(req.Decision)
	if decision != "accepted" && decision != "ignored" && decision != "rejected" {
		writeError(w, http.StatusBadRequest, "invalid decision")
		return
	}
	deliveryID, ok := parseDeliveryIDOrBadRequest(w, chi.URLParam(r, "deliveryId"))
	if !ok {
		return
	}
	delivery, err := h.Queries.UpdateEvolutionDeliveryDecision(r.Context(), db.UpdateEvolutionDeliveryDecisionParams{ID: deliveryID, WorkspaceID: rt.WorkspaceID, TargetAgentID: agent.ID, Status: decision})
	if err != nil {
		writeDeliveryMutationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"delivery": delivery})
}

func evolutionDeliveryResponse(row db.ListPendingEvolutionDeliveriesByAgentRow, files []db.SharedEvolutionUnitFile) EvolutionDeliveryResponse {
	outFiles := make([]EvolutionDeliveryFileReply, 0, len(files))
	for _, file := range files {
		outFiles = append(outFiles, EvolutionDeliveryFileReply{Path: file.Path, Content: file.Content, ContentHash: file.ContentHash, MimeType: file.MimeType})
	}
	return EvolutionDeliveryResponse{
		ID:               uuidToString(row.ID),
		WorkspaceID:      uuidToString(row.WorkspaceID),
		UnitID:           uuidToString(row.UnitID),
		VersionID:        uuidToString(row.VersionID),
		TargetAgentID:    uuidToString(row.TargetAgentID),
		DeliveryType:     row.DeliveryType,
		Status:           row.Status,
		Reason:           row.Reason,
		MatcherScore:     row.MatcherScore,
		UnitType:         row.UnitType,
		Title:            row.Title,
		CanonicalSummary: row.CanonicalSummary,
		Content:          row.Content,
		Metadata:         json.RawMessage(row.Metadata),
		Applies:          json.RawMessage(row.Applies),
		Tags:             row.Tags,
		Tools:            row.Tools,
		TaskTypes:        row.TaskTypes,
		ProjectTypes:     row.ProjectTypes,
		Languages:        row.Languages,
		Frameworks:       row.Frameworks,
		Files:            outFiles,
	}
}

func writeDeliveryMutationError(w http.ResponseWriter, err error) {
	if err == pgx.ErrNoRows {
		writeError(w, http.StatusNotFound, "delivery not found")
		return
	}
	writeError(w, http.StatusInternalServerError, "failed to update evolution delivery")
}

func (h *Handler) requireRuntimeBoundAgentFromRequest(w http.ResponseWriter, r *http.Request) (db.AgentRuntime, db.Agent, bool) {
	runtimeID := chi.URLParam(r, "runtimeId")
	rt, ok := h.requireDaemonRuntimeAccess(w, r, runtimeID)
	if !ok {
		return db.AgentRuntime{}, db.Agent{}, false
	}
	agentIDRaw := strings.TrimSpace(r.URL.Query().Get("agent_id"))
	resp := RuntimeSharedSkillSyncResponse{}
	agent, ok := h.loadRuntimeBoundAgentForSync(r.Context(), &resp, rt, agentIDRaw)
	if !ok {
		msg := "agent not found"
		if len(resp.Errors) > 0 {
			msg = resp.Errors[0].Error
		}
		writeError(w, http.StatusBadRequest, msg)
		return db.AgentRuntime{}, db.Agent{}, false
	}
	return rt, agent, true
}

func parseDeliveryIDOrBadRequest(w http.ResponseWriter, raw string) (pgtype.UUID, bool) {
	id, err := util.ParseUUID(strings.TrimSpace(raw))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid delivery id")
		return pgtype.UUID{}, false
	}
	return id, true
}
