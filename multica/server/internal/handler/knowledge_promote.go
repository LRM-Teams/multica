package handler

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type promoteKnowledgeRequest struct {
	SourceType    string `json:"source_type"`
	SourceID      string `json:"source_id"`
	TargetKind    string `json:"target_kind"`
	Title         string `json:"title"`
	Content       string `json:"content"`
	SubjectID     string `json:"subject_id"`
	SupersedesID  string `json:"supersedes_id,omitempty"`
	SharedToAgent string `json:"shared_to_agent_id,omitempty"`
}

type knowledgeEdgeResponse struct {
	ID            string `json:"id"`
	EdgeType      string `json:"edge_type"`
	FromKind      string `json:"from_kind"`
	FromID        string `json:"from_id"`
	ToKind        string `json:"to_kind"`
	ToID          string `json:"to_id"`
	CreatedByType string `json:"created_by_type"`
	CreatedByID   string `json:"created_by_id,omitempty"`
	CreatedAt     string `json:"created_at"`
}

type promoteKnowledgeResponse struct {
	ID        string                  `json:"id"`
	Kind      string                  `json:"kind"`
	Title     string                  `json:"title"`
	Content   string                  `json:"content"`
	Status    string                  `json:"status"`
	Metadata  json.RawMessage         `json:"metadata"`
	Edges     []knowledgeEdgeResponse `json:"edges"`
	CreatedAt string                  `json:"created_at"`
}

type knowledgeNeighborsResponse struct {
	PageID string                  `json:"page_id"`
	Edges  []knowledgeEdgeResponse `json:"edges"`
	Hops   int                     `json:"hops"`
}

// PromoteKnowledgePage elevates an issue/channel conclusion to CONTEXT/DECISION
// and writes derived_from (+ about/owned_by/supersedes) edges (LRM-1000).
func (h *Handler) PromoteKnowledgePage(w http.ResponseWriter, r *http.Request) {
	workspaceID := workspaceIDFromURL(r, "id")
	if workspaceID == "" {
		writeError(w, http.StatusBadRequest, "workspace id is required")
		return
	}
	member, ok := h.workspaceMember(w, r, workspaceID)
	if !ok {
		return
	}
	var req promoteKnowledgeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	sourceID, ok := parseUUIDOrBadRequest(w, req.SourceID, "source_id")
	if !ok {
		return
	}
	subjectID := pgtype.UUID{}
	if strings.TrimSpace(req.SubjectID) != "" {
		subjectID, ok = parseUUIDOrBadRequest(w, req.SubjectID, "subject_id")
		if !ok {
			return
		}
	}
	supersedesID := pgtype.UUID{}
	if strings.TrimSpace(req.SupersedesID) != "" {
		supersedesID, ok = parseUUIDOrBadRequest(w, req.SupersedesID, "supersedes_id")
		if !ok {
			return
		}
	}
	sharedTo := pgtype.UUID{}
	if strings.TrimSpace(req.SharedToAgent) != "" {
		sharedTo, ok = parseUUIDOrBadRequest(w, req.SharedToAgent, "shared_to_agent_id")
		if !ok {
			return
		}
	}

	userID := uuidToString(member.UserID)
	actorType, actorIDStr := h.resolveActor(r, userID, workspaceID)
	if actorIDStr == "" {
		actorType, actorIDStr = "member", userID
	}
	if actorType == "user" {
		actorType = "member"
	}
	actorID := parseUUID(actorIDStr)

	result, err := h.TaskService.PromoteKnowledgePage(r.Context(), service.KnowledgePromoteInput{
		WorkspaceID:   parseUUID(workspaceID),
		SourceType:    req.SourceType,
		SourceID:      sourceID,
		TargetKind:    req.TargetKind,
		Title:         req.Title,
		Content:       req.Content,
		SubjectID:     subjectID,
		SupersedesID:  supersedesID,
		ActorType:     actorType,
		ActorID:       actorID,
		SharedToAgent: sharedTo,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	edges := make([]knowledgeEdgeResponse, 0, len(result.Edges))
	for _, edge := range result.Edges {
		edges = append(edges, knowledgeEdgeToResponse(edge))
	}
	meta := json.RawMessage(result.Page.Metadata)
	if len(meta) == 0 {
		meta = json.RawMessage(`{}`)
	}
	writeJSON(w, http.StatusCreated, promoteKnowledgeResponse{
		ID:        uuidToString(result.Page.ID),
		Kind:      result.Page.Kind,
		Title:     result.Page.Title,
		Content:   result.Page.Content,
		Status:    result.Page.Status,
		Metadata:  meta,
		Edges:     edges,
		CreatedAt: timestampToString(result.Page.CreatedAt),
	})
}

// ListKnowledgeNeighbors returns explicit edges for a wiki page (LRM-1000).
func (h *Handler) ListKnowledgeNeighbors(w http.ResponseWriter, r *http.Request) {
	workspaceID := workspaceIDFromURL(r, "id")
	if workspaceID == "" {
		writeError(w, http.StatusBadRequest, "workspace id is required")
		return
	}
	if _, ok := h.workspaceMember(w, r, workspaceID); !ok {
		return
	}
	pageIDParam := chi.URLParam(r, "itemId")
	pageID, ok := parseUUIDOrBadRequest(w, pageIDParam, "item id")
	if !ok {
		return
	}
	hops := 1
	if raw := strings.TrimSpace(r.URL.Query().Get("hops")); raw != "" {
		if raw == "2" {
			hops = 2
		} else if raw != "1" {
			writeError(w, http.StatusBadRequest, "hops must be 1 or 2")
			return
		}
	}

	edges, err := h.TaskService.ListKnowledgeNeighbors(r.Context(), parseUUID(workspaceID), pageID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list neighbors")
		return
	}
	if hops == 2 {
		seen := map[string]struct{}{}
		for _, edge := range edges {
			seen[uuidToString(edge.ID)] = struct{}{}
		}
		extraIDs := map[string]pgtype.UUID{}
		for _, edge := range edges {
			if edge.FromKind == service.KnowledgeNodeTeamKnowledge && uuidToString(edge.FromID) != uuidToString(pageID) {
				extraIDs[uuidToString(edge.FromID)] = edge.FromID
			}
			if edge.ToKind == service.KnowledgeNodeTeamKnowledge && uuidToString(edge.ToID) != uuidToString(pageID) {
				extraIDs[uuidToString(edge.ToID)] = edge.ToID
			}
		}
		for _, neighborID := range extraIDs {
			more, err := h.TaskService.ListKnowledgeNeighbors(r.Context(), parseUUID(workspaceID), neighborID)
			if err != nil {
				continue
			}
			for _, edge := range more {
				id := uuidToString(edge.ID)
				if _, ok := seen[id]; ok {
					continue
				}
				seen[id] = struct{}{}
				edges = append(edges, edge)
			}
		}
	}

	out := make([]knowledgeEdgeResponse, 0, len(edges))
	for _, edge := range edges {
		out = append(out, knowledgeEdgeToResponse(edge))
	}
	writeJSON(w, http.StatusOK, knowledgeNeighborsResponse{
		PageID: uuidToString(pageID),
		Edges:  out,
		Hops:   hops,
	})
}

func knowledgeEdgeToResponse(edge db.TeamKnowledgeEdge) knowledgeEdgeResponse {
	return knowledgeEdgeResponse{
		ID:            uuidToString(edge.ID),
		EdgeType:      edge.EdgeType,
		FromKind:      edge.FromKind,
		FromID:        uuidToString(edge.FromID),
		ToKind:        edge.ToKind,
		ToID:          uuidToString(edge.ToID),
		CreatedByType: edge.CreatedByType,
		CreatedByID:   uuidToString(edge.CreatedByID),
		CreatedAt:     timestampToString(edge.CreatedAt),
	}
}
