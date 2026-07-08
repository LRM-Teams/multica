package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/multica-ai/multica/server/internal/service"
)

// CreateEnvRequest is the body of POST /api/v1/env (spec §6.1).
type CreateEnvRequest struct {
	ImageRef string `json:"image_ref"`
}

// CreateEnvResponse is the 201 response.
type CreateEnvResponse struct {
	EnvID     string `json:"env_id"`
	SandboxID string `json:"sandbox_id"`
}

// CreateEnv handles POST /api/v1/env. Boots a sandbox from image_ref via the
// cloud-runtime proxy, creates an environment row (mode='base'), returns env_id.
func (h *Handler) CreateEnv(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireUserID(w, r); !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	if workspaceID == "" {
		writeError(w, http.StatusBadRequest, "workspace ID required")
		return
	}
	var req CreateEnvRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}
	if req.ImageRef == "" || len(req.ImageRef) > 256 {
		writeError(w, http.StatusBadRequest, "image_ref must be 1..256 chars")
		return
	}

	svc := service.NewEnvDispatchService(newEnvDispatchDepsAdapter(h), 8)
	envID, sandboxID, err := svc.CreateBaseEnv(r.Context(), workspaceID, req.ImageRef)
	if err != nil {
		status := http.StatusServiceUnavailable
		if strings.Contains(err.Error(), "validation_failed") {
			status = http.StatusBadRequest
		}
		writeError(w, status, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, CreateEnvResponse{EnvID: envID, SandboxID: sandboxID})
}

// DeleteEnv handles DELETE /api/v1/env/{envID} (spec §6.2). Idempotent on 404.
func (h *Handler) DeleteEnv(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireUserID(w, r); !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	if workspaceID == "" {
		writeError(w, http.StatusBadRequest, "workspace ID required")
		return
	}
	envID := chi.URLParam(r, "envID")
	if _, ok := parseUUIDOrBadRequest(w, envID, "envID"); !ok {
		return
	}
	svc := service.NewEnvDispatchService(newEnvDispatchDepsAdapter(h), 8)
	if err := svc.DeleteEnv(r.Context(), envID, workspaceID); err != nil {
		switch {
		case errors.Is(err, service.ErrEnvInUse):
			writeError(w, http.StatusConflict, "env_in_use: delete its project(s) first")
		case strings.Contains(err.Error(), "not found"):
			writeError(w, http.StatusNotFound, err.Error())
		default:
			writeError(w, http.StatusServiceUnavailable, err.Error())
		}
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
