package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/multica-ai/multica/server/internal/service"
)

// Training governance API (spec 14.1, Task 18). Owner/admin surfaces for
// grants, the global switches and manifest lifecycle; nothing here can
// activate a historical workspace silently — acknowledgement is always an
// explicit CAS transition.

type trainingGrantUpdateRequest struct {
	Purpose         string `json:"purpose"`
	Action          string `json:"action"` // ack | opt_in | revoke
	ExpectedVersion int64  `json:"expected_version"`
}

type trainingGovernanceResponse struct {
	Grant  service.TrainingGrant  `json:"grant"`
	Policy service.TrainingPolicy `json:"policy"`
}

// writeTrainingGovernanceError maps the governance sentinels to HTTP codes.
func writeTrainingGovernanceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, service.ErrTrainingGrantVersion):
		writeError(w, http.StatusConflict, "training grant version conflict")
	case errors.Is(err, service.ErrTrainingGrantPendingOwnerAck):
		writeError(w, http.StatusConflict, "training grant awaits owner acknowledgement")
	case errors.Is(err, service.ErrTrainingGrantRevoked):
		writeError(w, http.StatusConflict, "training grant revoked")
	case errors.Is(err, service.ErrTrainingPooledNotEnabled):
		writeError(w, http.StatusConflict, "pooled training requires explicit opt-in")
	case errors.Is(err, service.ErrTrainingGrantNotFound):
		writeError(w, http.StatusNotFound, "training grant not found")
	case errors.Is(err, service.ErrTrainingSelectionDisabled):
		writeError(w, http.StatusServiceUnavailable, "training selection is globally disabled")
	case errors.Is(err, service.ErrTrainingExecutionDisabled):
		writeError(w, http.StatusServiceUnavailable, "training execution is globally disabled")
	case errors.Is(err, service.ErrTrainingManifestNotFound):
		writeError(w, http.StatusNotFound, "training manifest not found")
	case errors.Is(err, service.ErrTrainingWorkspaceMismatch):
		writeError(w, http.StatusForbidden, "training manifest belongs to another workspace")
	case errors.Is(err, service.ErrTrainingManifestState):
		writeError(w, http.StatusConflict, "training manifest state conflict")
	case errors.Is(err, service.ErrTrainingDuplicate):
		writeError(w, http.StatusConflict, "no new training samples are selectable")
	case errors.Is(err, service.ErrTrainingFenced):
		writeError(w, http.StatusConflict, "a selected training sample was retracted")
	case errors.Is(err, service.ErrTrainingRewardUnavailable):
		writeError(w, http.StatusConflict, "a selected training reward became unavailable")
	case errors.Is(err, service.ErrTrainingExecutionTaskMismatch):
		writeError(w, http.StatusForbidden, "training execution task mismatch")
	default:
		writeError(w, http.StatusInternalServerError, "training governance: "+err.Error())
	}
}

func (h *Handler) requireTrainingGovernance(w http.ResponseWriter) *service.TrainingGovernanceService {
	if h.TrainingGovernance == nil {
		writeError(w, http.StatusServiceUnavailable, "training governance is not configured")
		return nil
	}
	return h.TrainingGovernance
}

// trainingWorkspaceScope resolves the {id} workspace and enforces
// owner/admin membership.
func (h *Handler) trainingWorkspaceScope(w http.ResponseWriter, r *http.Request) (string, bool) {
	workspaceID := workspaceIDFromURL(r, "id")
	member, ok := h.workspaceMember(w, r, workspaceID)
	if !ok {
		return "", false
	}
	if !h.isWorkspaceOwnerOrAdmin(r.Context(), workspaceID, member.UserID) {
		writeError(w, http.StatusForbidden, "workspace owner or admin required")
		return "", false
	}
	return workspaceID, true
}

// GetTrainingGrant serves GET /api/workspaces/{id}/training/grant
// (owner/admin): the grant pair plus the global switches.
func (h *Handler) GetTrainingGrant(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := h.trainingWorkspaceScope(w, r)
	if !ok {
		return
	}
	svc := h.requireTrainingGovernance(w)
	if svc == nil {
		return
	}
	grant, err := svc.CurrentGrant(r.Context(), workspaceID)
	if err != nil {
		writeTrainingGovernanceError(w, err)
		return
	}
	policy, err := svc.TrainingPolicy(r.Context())
	if err != nil {
		writeTrainingGovernanceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, trainingGovernanceResponse{Grant: grant, Policy: policy})
}

// UpdateTrainingGrant serves PUT /api/workspaces/{id}/training/grant
// (owner/admin): the explicit CAS transitions — tenant acknowledgement,
// pooled opt-in, revoke. The actor is the requesting user.
func (h *Handler) UpdateTrainingGrant(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := h.trainingWorkspaceScope(w, r)
	if !ok {
		return
	}
	svc := h.requireTrainingGovernance(w)
	if svc == nil {
		return
	}
	var req trainingGrantUpdateRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	actor := "user:" + userID
	var grant service.TrainingGrant
	var err error
	switch {
	case req.Purpose == service.TrainingPurposeTenant && req.Action == "ack":
		grant, err = svc.AckTenantGrant(r.Context(), workspaceID, actor, req.ExpectedVersion)
	case req.Purpose == service.TrainingPurposePooled && req.Action == "opt_in":
		grant, err = svc.OptInPooledTraining(r.Context(), workspaceID, actor, req.ExpectedVersion)
	case req.Action == "revoke" && (req.Purpose == service.TrainingPurposeTenant || req.Purpose == service.TrainingPurposePooled):
		var report service.TrainingRevokeReport
		report, err = svc.RevokeTrainingGrant(r.Context(), workspaceID, req.Purpose, actor)
		if err == nil {
			writeJSON(w, http.StatusOK, map[string]any{
				"workspace_id":         workspaceID,
				"purpose":              req.Purpose,
				"invalidated":          report.InvalidatedManifests,
				"revoked_samples":      report.RevokedSamples,
				"deletion_ledger_rows": report.LedgerEntries,
			})
			return
		}
	default:
		writeError(w, http.StatusBadRequest, "purpose must be tenant or pooled; action must be ack, opt_in, or revoke")
		return
	}
	if err != nil {
		writeTrainingGovernanceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, trainingGovernanceResponse{Grant: grant})
}

// GetTrainingPolicyRoute serves GET /api/workspaces/{id}/training/policy
// (owner/admin): the global switches and sampling caps.
func (h *Handler) GetTrainingPolicyRoute(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.trainingWorkspaceScope(w, r); !ok {
		return
	}
	svc := h.requireTrainingGovernance(w)
	if svc == nil {
		return
	}
	policy, err := svc.TrainingPolicy(r.Context())
	if err != nil {
		writeTrainingGovernanceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"policy": policy})
}

type trainingPolicyUpdateRequest struct {
	SelectionEnabled      *bool  `json:"selection_enabled"`
	ExecutionEnabled      *bool  `json:"execution_enabled"`
	RewardPolicyVersion   *int64 `json:"reward_policy_version"`
	PerAgentSampleCap     *int32 `json:"per_agent_sample_cap"`
	PerChannelSampleCap   *int32 `json:"per_channel_sample_cap"`
	PerWorkspaceSampleCap *int32 `json:"per_workspace_sample_cap"`
}

// UpdateTrainingPolicyRoute serves PUT /api/workspaces/{id}/training/policy
// (owner/admin): the global reward shadow/calibration switches and the
// sampling caps. Both switches stay OFF until explicitly enabled here.
func (h *Handler) UpdateTrainingPolicyRoute(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.trainingWorkspaceScope(w, r); !ok {
		return
	}
	svc := h.requireTrainingGovernance(w)
	if svc == nil {
		return
	}
	var req trainingPolicyUpdateRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	if (req.PerAgentSampleCap != nil && *req.PerAgentSampleCap <= 0) ||
		(req.PerChannelSampleCap != nil && *req.PerChannelSampleCap <= 0) ||
		(req.PerWorkspaceSampleCap != nil && *req.PerWorkspaceSampleCap <= 0) {
		writeError(w, http.StatusUnprocessableEntity, "sampling caps must be positive")
		return
	}
	policy, err := svc.SetTrainingPolicy(r.Context(), service.TrainingPolicyPatch{
		SelectionEnabled:      req.SelectionEnabled,
		ExecutionEnabled:      req.ExecutionEnabled,
		RewardPolicyVersion:   req.RewardPolicyVersion,
		PerAgentSampleCap:     req.PerAgentSampleCap,
		PerChannelSampleCap:   req.PerChannelSampleCap,
		PerWorkspaceSampleCap: req.PerWorkspaceSampleCap,
	}, "user:"+userID)
	if err != nil {
		writeTrainingGovernanceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"policy": policy})
}

type trainingManifestCreateRequest struct {
	Purpose string `json:"purpose"`
	Family  string `json:"family"` // segments (default) | graph_trajectories
	Limit   int    `json:"limit"`
}

// CreateTrainingManifest serves POST /api/workspaces/{id}/training/manifests
// (owner/admin): one selection pass under the current grant and switches.
func (h *Handler) CreateTrainingManifest(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := h.trainingWorkspaceScope(w, r)
	if !ok {
		return
	}
	svc := h.requireTrainingGovernance(w)
	if svc == nil {
		return
	}
	var req trainingManifestCreateRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Purpose != service.TrainingPurposeTenant && req.Purpose != service.TrainingPurposePooled {
		writeError(w, http.StatusBadRequest, "purpose must be tenant or pooled")
		return
	}
	if req.Family != "" && req.Family != "segments" && req.Family != "graph_trajectories" {
		writeError(w, http.StatusBadRequest, "family must be segments or graph_trajectories")
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	selection := service.TrainingSelectionRequest{
		WorkspaceID: workspaceID, Purpose: req.Purpose,
		Actor: "user:" + userID, Limit: req.Limit,
	}
	var manifest *service.TrainingManifest
	var err error
	if req.Family == "graph_trajectories" {
		manifest, err = svc.SelectGraphTrainingManifest(r.Context(), selection)
	} else {
		manifest, err = svc.SelectTrainingManifest(r.Context(), selection)
	}
	if err != nil {
		writeTrainingGovernanceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, manifest)
}

// ListTrainingManifestsRoute serves GET /api/workspaces/{id}/training/manifests.
func (h *Handler) ListTrainingManifestsRoute(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := h.trainingWorkspaceScope(w, r)
	if !ok {
		return
	}
	svc := h.requireTrainingGovernance(w)
	if svc == nil {
		return
	}
	purpose := r.URL.Query().Get("purpose")
	if purpose != "" && purpose != service.TrainingPurposeTenant && purpose != service.TrainingPurposePooled {
		writeError(w, http.StatusBadRequest, "purpose must be tenant or pooled")
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	manifests, err := svc.ListTrainingManifests(r.Context(), workspaceID, purpose, limit)
	if err != nil {
		writeTrainingGovernanceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"manifests": manifests})
}

// GetTrainingManifestRoute serves GET .../training/manifests/{manifestId}
// with the full item snapshot.
func (h *Handler) GetTrainingManifestRoute(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := h.trainingWorkspaceScope(w, r)
	if !ok {
		return
	}
	svc := h.requireTrainingGovernance(w)
	if svc == nil {
		return
	}
	manifestID := chi.URLParam(r, "manifestId")
	manifest, err := svc.GetTrainingManifest(r.Context(), manifestID, true)
	if err != nil {
		writeTrainingGovernanceError(w, err)
		return
	}
	if manifest.WorkspaceID != workspaceID {
		writeTrainingGovernanceError(w, service.ErrTrainingWorkspaceMismatch)
		return
	}
	writeJSON(w, http.StatusOK, manifest)
}

// ExportTrainingManifestRoute serves POST .../training/manifests/{manifestId}/export:
// the exactly-once selected -> exported transition with full rechecks.
func (h *Handler) ExportTrainingManifestRoute(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := h.trainingWorkspaceScope(w, r)
	if !ok {
		return
	}
	svc := h.requireTrainingGovernance(w)
	if svc == nil {
		return
	}
	manifest, err := svc.ExportTrainingManifest(r.Context(), workspaceID, chi.URLParam(r, "manifestId"))
	if err != nil {
		writeTrainingGovernanceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, manifest)
}

type trainingExecutionRequest struct {
	AgentID string `json:"agent_id"`
}

// BeginTrainingExecutionRoute serves POST .../training/manifests/{manifestId}/execute:
// creates the distinct replay/training task under the global execution
// switch (off until reward calibration).
func (h *Handler) BeginTrainingExecutionRoute(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := h.trainingWorkspaceScope(w, r)
	if !ok {
		return
	}
	svc := h.requireTrainingGovernance(w)
	if svc == nil {
		return
	}
	var req trainingExecutionRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	execution, err := svc.BeginTrainingExecution(r.Context(), workspaceID, chi.URLParam(r, "manifestId"), req.AgentID)
	if err != nil {
		writeTrainingGovernanceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, execution)
}

// ConsumeTrainingExecutionRoute serves POST .../training/manifests/{manifestId}/consume:
// the terminal consumed state of the replay execution.
func (h *Handler) ConsumeTrainingExecutionRoute(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := h.trainingWorkspaceScope(w, r)
	if !ok {
		return
	}
	svc := h.requireTrainingGovernance(w)
	if svc == nil {
		return
	}
	if err := svc.ConsumeTrainingExecution(r.Context(), workspaceID, chi.URLParam(r, "manifestId")); err != nil {
		writeTrainingGovernanceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "consumed"})
}

// ListTrainingDeletionLedgerRoute serves GET .../training/deletion-ledger:
// the deletion/unlearning ledger for owner/admin audit.
func (h *Handler) ListTrainingDeletionLedgerRoute(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := h.trainingWorkspaceScope(w, r)
	if !ok {
		return
	}
	svc := h.requireTrainingGovernance(w)
	if svc == nil {
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	rows, err := svc.ListTrainingDeletionLedgerRows(r.Context(), workspaceID, limit)
	if err != nil {
		writeTrainingGovernanceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": rows})
}

// AuditTrainingSelectionRoute serves GET .../training/selection-audit: why
// each workspace segment is or is not selectable right now.
func (h *Handler) AuditTrainingSelectionRoute(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := h.trainingWorkspaceScope(w, r)
	if !ok {
		return
	}
	svc := h.requireTrainingGovernance(w)
	if svc == nil {
		return
	}
	audit, err := svc.AuditTrainingSelection(r.Context(), workspaceID)
	if err != nil {
		writeTrainingGovernanceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"segments": audit})
}
