package handler

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type AgentMemoryResponse struct {
	ID          string  `json:"id"`
	WorkspaceID string  `json:"workspace_id"`
	AgentID     string  `json:"agent_id"`
	Name        string  `json:"name"`
	Content     string  `json:"content"`
	Config      any     `json:"config"`
	SyncKey     string  `json:"sync_key"`
	ContentHash string  `json:"content_hash"`
	CreatedBy   *string `json:"created_by"`
	CreatedAt   string  `json:"created_at"`
	UpdatedAt   string  `json:"updated_at"`
}

func agentMemoryToResponse(m db.AgentMemory) AgentMemoryResponse {
	return AgentMemoryResponse{
		ID:          uuidToString(m.ID),
		WorkspaceID: uuidToString(m.WorkspaceID),
		AgentID:     uuidToString(m.AgentID),
		Name:        m.Name,
		Content:     m.Content,
		Config:      decodeSkillConfig(m.Config),
		SyncKey:     m.SyncKey,
		ContentHash: m.ContentHash,
		CreatedBy:   uuidToPtr(m.CreatedBy),
		CreatedAt:   timestampToString(m.CreatedAt),
		UpdatedAt:   timestampToString(m.UpdatedAt),
	}
}

func (h *Handler) ListAgentMemories(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	agent, ok := h.loadAgentForUser(w, r, id)
	if !ok {
		return
	}

	memories, err := h.Queries.ListAgentMemoriesByAgent(r.Context(), agent.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list agent memories")
		return
	}

	resp := make([]AgentMemoryResponse, len(memories))
	for i, memory := range memories {
		resp[i] = agentMemoryToResponse(memory)
	}
	writeJSON(w, http.StatusOK, resp)
}
