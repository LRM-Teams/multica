package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/multica-ai/multica/server/internal/service"
)

// envCheckpointsEnabled reports whether the env-checkpoint APIs are enabled.
// When disabled, all checkpoint routes return 404 so AReaL clients can detect
// the feature gate without distinguishing routes.
func envCheckpointsEnabled() bool {
	v := os.Getenv("ENV_CHECKPOINTS_ENABLED")
	if v == "" {
		return false
	}
	b, _ := strconv.ParseBool(v)
	return b
}

// CreateEnvCheckpointRequest is the HTTP request body for POST /api/v1/env-checkpoints.
type CreateEnvCheckpointRequest struct {
	ProjectID     string                       `json:"project_id"`
	EventRef      string                       `json:"event_ref"`
	Kind          string                       `json:"kind"`
	EnvIDMap      map[string]string            `json:"env_id_map,omitempty"`
	SandboxRefs   []service.SandboxInstanceRef `json:"sandbox_refs,omitempty"`
	EntropyScore  *float64                     `json:"entropy_score,omitempty"`
	SaveTimeoutMs *int                         `json:"save_timeout_ms,omitempty"`
}

// EnvCheckpointResponse is the HTTP response body for a single checkpoint.
type EnvCheckpointResponse struct {
	ID            string                       `json:"id"`
	WorkspaceID   string                       `json:"workspace_id"`
	ProjectID     string                       `json:"project_id"`
	EventRef      string                       `json:"event_ref"`
	Kind          string                       `json:"kind"`
	EnvIDMap      map[string]string            `json:"env_id_map,omitempty"`
	SandboxRefs   []service.SandboxInstanceRef `json:"sandbox_refs,omitempty"`
	DBSnapshot    json.RawMessage              `json:"db_snapshot,omitempty"`
	ResumeTrigger json.RawMessage              `json:"resume_trigger,omitempty"`
	EntropyScore  *float64                     `json:"entropy_score,omitempty"`
	SaveTimeoutMs int                          `json:"save_timeout_ms"`
	SaveStatus    string                       `json:"save_status"`
	SaveError     string                       `json:"save_error,omitempty"`
	CreatedAt     time.Time                    `json:"created_at"`
	UpdatedAt     time.Time                    `json:"updated_at"`
}

// EnvCheckpointListResponse wraps a checkpoint slice for list responses.
type EnvCheckpointListResponse struct {
	Checkpoints []EnvCheckpointResponse `json:"checkpoints"`
}

const defaultEnvCheckpointSaveTimeoutMs = 30_000

// CreateEnvCheckpoint handles POST /api/v1/env-checkpoints.
func (h *Handler) CreateEnvCheckpoint(w http.ResponseWriter, r *http.Request) {
	if !envCheckpointsEnabled() {
		writeError(w, http.StatusNotFound, "env checkpoints are not enabled")
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	if workspaceID == "" {
		writeError(w, http.StatusBadRequest, "workspace ID required")
		return
	}
	if h.EnvCheckpointService == nil {
		writeError(w, http.StatusServiceUnavailable, "checkpoint service not configured")
		return
	}

	var req CreateEnvCheckpointRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}
	if req.ProjectID == "" {
		writeError(w, http.StatusBadRequest, "project_id is required")
		return
	}
	if _, ok := parseUUIDOrBadRequest(w, req.ProjectID, "project_id"); !ok {
		return
	}

	saveTimeoutMs := defaultEnvCheckpointSaveTimeoutMs
	if req.SaveTimeoutMs != nil && *req.SaveTimeoutMs > 0 {
		saveTimeoutMs = *req.SaveTimeoutMs
	}

	cp, err := h.EnvCheckpointService.Create(r.Context(), service.EnvCheckpointCreateInput{
		WorkspaceID:  workspaceID,
		ProjectID:    req.ProjectID,
		EventRef:     req.EventRef,
		Kind:         req.Kind,
		EnvIDMap:     req.EnvIDMap,
		SandboxRefs:  req.SandboxRefs,
		EntropyScore: req.EntropyScore,
		ActorUserID:  userID,
		SaveTimeout:  time.Duration(saveTimeoutMs) * time.Millisecond,
	})
	if err != nil {
		writeEnvCheckpointError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, mapEnvCheckpointResponse(cp))
}

// GetEnvCheckpoint handles GET /api/v1/env-checkpoints/{checkpointID}.
func (h *Handler) GetEnvCheckpoint(w http.ResponseWriter, r *http.Request) {
	if !envCheckpointsEnabled() {
		writeError(w, http.StatusNotFound, "env checkpoints are not enabled")
		return
	}
	if _, ok := requireUserID(w, r); !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	if workspaceID == "" {
		writeError(w, http.StatusBadRequest, "workspace ID required")
		return
	}
	if h.EnvCheckpointService == nil {
		writeError(w, http.StatusServiceUnavailable, "checkpoint service not configured")
		return
	}
	checkpointID := chi.URLParam(r, "checkpointID")
	if _, ok := parseUUIDOrBadRequest(w, checkpointID, "checkpointID"); !ok {
		return
	}

	cp, err := h.EnvCheckpointService.Get(r.Context(), checkpointID, workspaceID)
	if err != nil {
		if isNotFoundErr(err) {
			writeError(w, http.StatusNotFound, "checkpoint not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, mapEnvCheckpointResponse(cp))
}

// ListEnvCheckpoints handles GET /api/v1/projects/{projectID}/env-checkpoints.
func (h *Handler) ListEnvCheckpoints(w http.ResponseWriter, r *http.Request) {
	if !envCheckpointsEnabled() {
		writeError(w, http.StatusNotFound, "env checkpoints are not enabled")
		return
	}
	if _, ok := requireUserID(w, r); !ok {
		return
	}
	h.listEnvCheckpointsForProject(w, r, chi.URLParam(r, "projectID"))
}

// listEnvCheckpointsForProject serves the project-scoped checkpoint list for
// both the project-first route (ListEnvCheckpoints) and the channel-first
// facade (ListChannelEnvCheckpoints). The caller resolves and supplies the
// project id; no URL param is read here.
func (h *Handler) listEnvCheckpointsForProject(w http.ResponseWriter, r *http.Request, projectID string) {
	workspaceID := ctxWorkspaceID(r.Context())
	if workspaceID == "" {
		writeError(w, http.StatusBadRequest, "workspace ID required")
		return
	}
	if h.EnvCheckpointService == nil {
		writeError(w, http.StatusServiceUnavailable, "checkpoint service not configured")
		return
	}
	if _, ok := parseUUIDOrBadRequest(w, projectID, "projectID"); !ok {
		return
	}

	list, err := h.EnvCheckpointService.List(r.Context(), workspaceID, projectID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]EnvCheckpointResponse, 0, len(list))
	for _, cp := range list {
		out = append(out, mapEnvCheckpointResponse(cp))
	}
	writeJSON(w, http.StatusOK, EnvCheckpointListResponse{Checkpoints: out})
}

func mapEnvCheckpointResponse(cp service.EnvCheckpoint) EnvCheckpointResponse {
	return EnvCheckpointResponse{
		ID:            cp.ID,
		WorkspaceID:   cp.WorkspaceID,
		ProjectID:     cp.ProjectID,
		EventRef:      cp.EventRef,
		Kind:          cp.Kind,
		EnvIDMap:      cp.EnvIDMap,
		SandboxRefs:   cp.SandboxRefs,
		DBSnapshot:    cp.DBSnapshot,
		ResumeTrigger: cp.ResumeTrigger,
		EntropyScore:  cp.EntropyScore,
		SaveTimeoutMs: cp.SaveTimeoutMs,
		SaveStatus:    string(cp.SaveStatus),
		SaveError:     cp.SaveError,
		CreatedAt:     cp.CreatedAt,
		UpdatedAt:     cp.UpdatedAt,
	}
}

// writeEnvCheckpointError maps service errors to HTTP status codes. Validation
// errors → 400; save timeout/failure → 409 (non-resumable terminal state);
// other errors → 500. Sandboxd internals are omitted from error bodies.
func writeEnvCheckpointError(w http.ResponseWriter, err error) {
	msg := err.Error()
	if strings.Contains(msg, "validation_failed") {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	if strings.Contains(msg, "timed_out") {
		writeError(w, http.StatusConflict, "checkpoint save timed out")
		return
	}
	if strings.Contains(msg, "failed") {
		writeError(w, http.StatusConflict, "checkpoint save failed")
		return
	}
	writeError(w, http.StatusInternalServerError, "checkpoint create failed")
}

func isNotFoundErr(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "not found") || strings.Contains(msg, "no rows")
}

// EnvCheckpointServiceAPI is the handler-level interface for the checkpoint
// service. *service.EnvCheckpointService satisfies it; tests inject a fake.
type EnvCheckpointServiceAPI interface {
	Create(ctx context.Context, in service.EnvCheckpointCreateInput) (service.EnvCheckpoint, error)
	Get(ctx context.Context, checkpointID, workspaceID string) (service.EnvCheckpoint, error)
	List(ctx context.Context, workspaceID, projectID string) ([]service.EnvCheckpoint, error)
	ResumeFromCheckpoint(ctx context.Context, workspaceID, checkpointID, actorUserID string) (service.ResumeFromCheckpointResult, error)
}

// ResumeFromCheckpointResponse is the HTTP response body for POST /api/v1/env-checkpoints/{checkpointID}/resume.
type ResumeFromCheckpointResponse struct {
	CheckpointID  string                       `json:"checkpoint_id"`
	ProjectID     string                       `json:"project_id"`
	EnvIDMap      map[string]string            `json:"env_id_map,omitempty"`
	SandboxRefs   []service.SandboxInstanceRef `json:"sandbox_refs,omitempty"`
	RolloutHandle string                       `json:"rollout_handle"`
	TriggerStatus string                       `json:"trigger_status"`
}

// ResumeEnvCheckpoint handles POST /api/v1/env-checkpoints/{checkpointID}/resume.
func (h *Handler) ResumeEnvCheckpoint(w http.ResponseWriter, r *http.Request) {
	if !envCheckpointsEnabled() {
		writeError(w, http.StatusNotFound, "env checkpoints are not enabled")
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	if workspaceID == "" {
		writeError(w, http.StatusBadRequest, "workspace ID required")
		return
	}
	if h.EnvCheckpointService == nil {
		writeError(w, http.StatusServiceUnavailable, "checkpoint service not configured")
		return
	}
	checkpointID := chi.URLParam(r, "checkpointID")
	if _, ok := parseUUIDOrBadRequest(w, checkpointID, "checkpointID"); !ok {
		return
	}

	res, err := h.EnvCheckpointService.ResumeFromCheckpoint(r.Context(), workspaceID, checkpointID, userID)
	if err != nil {
		msg := err.Error()
		if strings.Contains(msg, "not found") {
			writeError(w, http.StatusNotFound, "checkpoint not found")
			return
		}
		if strings.Contains(msg, "validation_failed") {
			writeError(w, http.StatusConflict, "checkpoint is not resumable")
			return
		}
		writeError(w, http.StatusInternalServerError, "resume failed")
		return
	}
	writeJSON(w, http.StatusOK, ResumeFromCheckpointResponse{
		CheckpointID:  res.CheckpointID,
		ProjectID:     res.ProjectID,
		EnvIDMap:      res.EnvIDMap,
		SandboxRefs:   res.SandboxRefs,
		RolloutHandle: res.RolloutHandle,
		TriggerStatus: string(res.TriggerStatus),
	})
}
