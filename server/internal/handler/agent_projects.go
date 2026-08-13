package handler

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// ListAgentProjects — GET /api/agent/projects
func (h *Handler) ListAgentProjects(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAgentPrincipal(w, r); !ok {
		return
	}
	h.ListProjects(w, r)
}

// ListAgentProjectResources — GET /api/agent/projects/{id}/resources
func (h *Handler) ListAgentProjectResources(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAgentPrincipal(w, r); !ok {
		return
	}
	// Reuse ListProjectResources (loads project in workspace via loadProjectForResource).
	// Ensure chi param "id" is the project id (same as human route).
	_ = chi.URLParam(r, "id")
	h.ListProjectResources(w, r)
}
