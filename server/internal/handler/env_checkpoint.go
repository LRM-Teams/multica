package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
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
	ProjectID string `json:"project_id"`
	EventRef  string `json:"event_ref"`
	Kind      string `json:"kind"`
	// SaveMode is pause_in_place or snapshot. Omitting it keeps the
	// pause_in_place behavior every existing client relies on.
	SaveMode      string                       `json:"save_mode,omitempty"`
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
	SaveMode      string                       `json:"save_mode"`
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
		SaveMode:     service.EnvCheckpointSaveMode(req.SaveMode),
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
		SaveMode:      string(cp.SaveMode),
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
	ResumeFromCheckpoint(ctx context.Context, in service.ResumeFromCheckpointInput) (service.ResumeFromCheckpointResult, error)
	Delete(ctx context.Context, workspaceID, checkpointID, actorUserID string) error
}

// ResumeEnvCheckpointRequest is the optional request body for resume. Both
// fields are optional so a pre-change caller sending no body at all still gets
// its single-lane resume.
type ResumeEnvCheckpointRequest struct {
	LaneCount int    `json:"lane_count,omitempty"`
	LaneKey   string `json:"lane_key,omitempty"`
}

// LaneResponse is one materialized lane in a fan-out resume.
type LaneResponse struct {
	LaneKey       string `json:"lane_key"`
	Status        string `json:"status"`
	InstanceID    string `json:"instance_id,omitempty"`
	ProjectID     string `json:"project_id,omitempty"`
	RuntimeID     string `json:"runtime_id,omitempty"`
	TaskID        string `json:"task_id,omitempty"`
	EnvID         string `json:"env_id,omitempty"`
	ChatSessionID string `json:"chat_session_id,omitempty"`
	TriggerStatus string `json:"trigger_status,omitempty"`
	Error         string `json:"error,omitempty"`
}

// ResumeFromCheckpointResponse is the HTTP response body for POST /api/v1/env-checkpoints/{checkpointID}/resume.
// lanes is omitempty, so a pause-in-place resume serializes exactly as before.
type ResumeFromCheckpointResponse struct {
	CheckpointID  string                       `json:"checkpoint_id"`
	ProjectID     string                       `json:"project_id"`
	EnvIDMap      map[string]string            `json:"env_id_map,omitempty"`
	SandboxRefs   []service.SandboxInstanceRef `json:"sandbox_refs,omitempty"`
	RolloutHandle string                       `json:"rollout_handle"`
	TriggerStatus string                       `json:"trigger_status"`
	Lanes         []LaneResponse               `json:"lanes,omitempty"`
	// Status summarizes a fan-out resume and is absent for pause_in_place, which
	// has one outcome already reported by trigger_status.
	Status string `json:"status,omitempty"`
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

	// An absent body is the pre-change request, so EOF is not an error.
	var req ResumeEnvCheckpointRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}
	if req.LaneCount == 0 {
		req.LaneCount = 1
	}
	// The checkpoint id is the same for every retry of one resume, so it is a
	// safe default anchor for callers that do not supply their own.
	if req.LaneKey == "" {
		req.LaneKey = checkpointID
	}

	res, err := h.EnvCheckpointService.ResumeFromCheckpoint(r.Context(), service.ResumeFromCheckpointInput{
		WorkspaceID:   workspaceID,
		CheckpointID:  checkpointID,
		ActorUserID:   userID,
		LaneCount:     req.LaneCount,
		LaneKeyAnchor: req.LaneKey,
	})
	if err != nil {
		switch {
		// A bad lane count is a bad request, not an unresumable checkpoint.
		case errors.Is(err, service.ErrLaneCountInvalid):
			writeError(w, http.StatusBadRequest, err.Error())
		case errors.Is(err, service.ErrCheckpointNotResumable):
			writeError(w, http.StatusConflict, "checkpoint is not resumable")
		case strings.Contains(err.Error(), "not found"):
			writeError(w, http.StatusNotFound, "checkpoint not found")
		case strings.Contains(err.Error(), "validation_failed"):
			writeError(w, http.StatusConflict, "checkpoint is not resumable")
		default:
			writeError(w, http.StatusInternalServerError, "resume failed")
		}
		return
	}
	lanes := make([]LaneResponse, 0, len(res.Lanes))
	for _, lane := range res.Lanes {
		lanes = append(lanes, LaneResponse{
			LaneKey:       lane.LaneKey,
			Status:        lane.Status,
			InstanceID:    lane.InstanceID,
			ProjectID:     lane.ProjectID,
			RuntimeID:     lane.RuntimeID,
			TaskID:        lane.TaskID,
			EnvID:         lane.EnvID,
			ChatSessionID: lane.ChatSessionID,
			TriggerStatus: string(lane.TriggerStatus),
			Error:         lane.Error,
		})
	}
	writeJSON(w, http.StatusOK, ResumeFromCheckpointResponse{
		CheckpointID:  res.CheckpointID,
		ProjectID:     res.ProjectID,
		EnvIDMap:      res.EnvIDMap,
		SandboxRefs:   res.SandboxRefs,
		RolloutHandle: res.RolloutHandle,
		TriggerStatus: string(res.TriggerStatus),
		Lanes:         lanes,
		Status:        string(res.Status),
	})
}

// DeleteEnvCheckpoint handles DELETE /api/v1/env-checkpoints/{checkpointID}. It
// releases the savepoints the checkpoint owns and removes the row; lanes and
// savepoint ownership cascade with it.
func (h *Handler) DeleteEnvCheckpoint(w http.ResponseWriter, r *http.Request) {
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

	if err := h.EnvCheckpointService.Delete(r.Context(), workspaceID, checkpointID, userID); err != nil {
		switch {
		// A lane still materializing is a conflict, not a failure: retrying once
		// it settles succeeds, so the caller should be told to wait rather than
		// that something broke.
		case errors.Is(err, service.ErrCheckpointHasProvisioningLanes):
			writeError(w, http.StatusConflict, err.Error())
		case strings.Contains(err.Error(), "not found"):
			writeError(w, http.StatusNotFound, "checkpoint not found")
		case strings.Contains(err.Error(), "validation_failed"):
			writeError(w, http.StatusBadRequest, err.Error())
		default:
			writeError(w, http.StatusInternalServerError, "delete failed")
		}
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
