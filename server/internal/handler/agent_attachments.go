package handler

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// GetAgentAttachment — GET /api/agent/attachments/{id}
// Metadata only when agent has a current visible reference (re-checked).
// download_url points at the agent download route so subsequent byte fetches
// re-run the visibility gate (not a human/workspace-only URL).
func (h *Handler) GetAgentAttachment(w http.ResponseWriter, r *http.Request) {
	p, ok := h.requireAgentPrincipal(w, r)
	if !ok {
		return
	}
	attID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "attachment id")
	if !ok {
		return
	}
	ws, wok := p.WorkspaceUUID()
	agentID, aok := p.AgentUUID()
	if !wok || !aok {
		writeError(w, http.StatusForbidden, "access denied")
		return
	}
	att, err := h.Queries.GetAttachment(r.Context(), db.GetAttachmentParams{
		ID:          attID,
		WorkspaceID: ws,
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "attachment not found")
		return
	}
	if !h.agentAttachmentVisible(r.Context(), ws, agentID, att.ID) {
		writeError(w, http.StatusNotFound, "attachment not found")
		return
	}
	resp := h.attachmentToResponse(att)
	// Force agent-authenticated download path so content/download re-checks
	// membership/references even when CloudFront signing would bypass ACL.
	id := uuidToString(att.ID)
	resp.DownloadURL = "/api/agent/attachments/" + id + "/download"
	writeJSON(w, http.StatusOK, resp)
}

// DownloadAgentAttachment — GET /api/agent/attachments/{id}/download
// Re-checks visible references on every download (Barry #801).
func (h *Handler) DownloadAgentAttachment(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAgentPrincipal(w, r); !ok {
		return
	}
	// loadAttachmentForDownload already applies agentAttachmentVisible when
	// AgentPrincipal is present (file.go #801 gate).
	h.DownloadAttachment(w, r)
}

// GetAgentAttachmentContent — GET /api/agent/attachments/{id}/content
// Text preview; same ACL as metadata/download, re-checked every request.
func (h *Handler) GetAgentAttachmentContent(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAgentPrincipal(w, r); !ok {
		return
	}
	// GetAttachmentContent uses loadAttachmentForRequest which has the agent gate.
	h.GetAttachmentContent(w, r)
}

// UploadAgentAttachment — POST /api/agent/attachments
// Reuses UploadFile after principal gate; body/contract matches agent runtime.
func (h *Handler) UploadAgentAttachment(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAgentPrincipal(w, r); !ok {
		return
	}
	h.UploadFile(w, r)
}
