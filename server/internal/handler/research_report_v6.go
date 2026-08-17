package handler

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/multica-ai/multica/server/internal/researchrun"
)

func (h *Handler) PutAgentResearchV6ReportUpload(w http.ResponseWriter, r *http.Request) {
	if h.Storage == nil {
		writeRonaldoV6Error(w, 503, "research.v6.capability_unavailable", "storage unavailable", true)
		return
	}
	access, ok := h.authorizeResearchV6Attempt(w, r)
	if !ok {
		return
	}
	id := chi.URLParam(r, "uploadId")
	var key, media, hash string
	var size int64
	err := h.DB.QueryRow(r.Context(), `SELECT storage_key,media_type,content_hash,byte_size FROM research_report_upload_session WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$3::uuid AND work_item_id=$4::uuid AND work_item_attempt_id=$5::uuid AND agent_id=$6::uuid AND status='pending' AND expires_at>now()`, access.WorkspaceID, access.RunID, id, access.WorkItemID, access.AttemptID, access.AgentID).Scan(&key, &media, &hash, &size)
	if err != nil {
		writeRonaldoV6Error(w, 403, "research.v6.principal_mismatch", "upload unavailable", false)
		return
	}
	if r.Header.Get("Content-Type") != media {
		writeRonaldoV6Error(w, 422, "research.v6.upload_mismatch", "media type mismatch", false)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, size+1))
	if err != nil || int64(len(body)) != size {
		writeRonaldoV6Error(w, 422, "research.v6.upload_mismatch", "size mismatch", false)
		return
	}
	sum := sha256.Sum256(body)
	if "sha256:"+hex.EncodeToString(sum[:]) != hash {
		writeRonaldoV6Error(w, 422, "research.v6.upload_mismatch", "hash mismatch", false)
		return
	}
	if _, err = h.Storage.Upload(r.Context(), key, body, media, ""); err != nil {
		writeRonaldoV6Error(w, 503, "research.v6.capability_unavailable", "storage unavailable", true)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type createV6ReportUploadRequest struct {
	ClientRequestID string `json:"client_request_id"`
	Path            string `json:"path"`
	Role            string `json:"role"`
	MediaType       string `json:"media_type"`
	ContentHash     string `json:"content_hash"`
	ByteSize        int64  `json:"byte_size"`
}
type completeV6ReportUploadRequest struct {
	ClientRequestID string `json:"client_request_id"`
}

func (h *Handler) CreateAgentResearchV6ReportUpload(w http.ResponseWriter, r *http.Request) {
	service, ok := h.ResearchRun.(researchrun.ResearchReportV6)
	if !ok {
		writeRonaldoV6Error(w, 503, "research.v6.capability_unavailable", "report upload unavailable", true)
		return
	}
	access, ok := h.authorizeResearchV6Attempt(w, r)
	if !ok {
		return
	}
	var req createV6ReportUploadRequest
	if !decodeResearchJSON(w, r, &req) {
		return
	}
	cap, err := service.CreateV6ReportUpload(r.Context(), access, researchrun.ReportUploadDeclaration{ClientRequestID: req.ClientRequestID, Path: req.Path, Role: req.Role, MediaType: req.MediaType, ContentHash: req.ContentHash, ByteSize: req.ByteSize})
	if err != nil {
		writeResearchV6DomainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, cap)
}
func (h *Handler) CompleteAgentResearchV6ReportUpload(w http.ResponseWriter, r *http.Request) {
	service, ok := h.ResearchRun.(researchrun.ResearchReportV6)
	if !ok {
		writeRonaldoV6Error(w, 503, "research.v6.capability_unavailable", "report upload unavailable", true)
		return
	}
	access, ok := h.authorizeResearchV6Attempt(w, r)
	if !ok {
		return
	}
	var req completeV6ReportUploadRequest
	if !decodeResearchJSON(w, r, &req) {
		return
	}
	object, err := service.CompleteV6ReportUpload(r.Context(), access, chi.URLParam(r, "uploadId"), req.ClientRequestID)
	if err != nil {
		writeResearchV6DomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"resource_id": chi.URLParam(r, "uploadId"), "verified": true, "content_hash": object.ContentHash})
}
