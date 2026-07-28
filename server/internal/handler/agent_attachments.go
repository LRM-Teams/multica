package handler

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/middleware"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// GetAgentAttachment — GET /api/agent/attachments/{id}
// Metadata only when agent has a current visible reference (re-checked).
func (h *Handler) GetAgentAttachment(w http.ResponseWriter, r *http.Request) {
	p, ok := h.requireAgentPrincipal(w, r)
	if !ok {
		return
	}
	att, ok := h.loadAttachmentForAgent(w, r, p)
	if !ok {
		return
	}
	resp := h.attachmentToResponse(att)
	id := uuidToString(att.ID)
	resp.DownloadURL = "/api/agent/attachments/" + id + "/download"
	writeJSON(w, http.StatusOK, resp)
}

// DownloadAgentAttachment — GET /api/agent/attachments/{id}/download
// Principal-native ACL; transport shared with human download.
func (h *Handler) DownloadAgentAttachment(w http.ResponseWriter, r *http.Request) {
	p, ok := h.requireAgentPrincipal(w, r)
	if !ok {
		return
	}
	att, ok := h.loadAttachmentForAgent(w, r, p)
	if !ok {
		return
	}
	h.streamOrRedirectAttachmentDownload(w, r, att)
}

// GetAgentAttachmentContent — GET /api/agent/attachments/{id}/content
func (h *Handler) GetAgentAttachmentContent(w http.ResponseWriter, r *http.Request) {
	p, ok := h.requireAgentPrincipal(w, r)
	if !ok {
		return
	}
	att, ok := h.loadAttachmentForAgent(w, r, p)
	if !ok {
		return
	}
	h.writeAttachmentTextPreview(w, r, att)
}

// loadAttachmentForAgent enforces AgentPrincipal visibility (never owner membership).
func (h *Handler) loadAttachmentForAgent(w http.ResponseWriter, r *http.Request, p middleware.AgentPrincipal) (db.Attachment, bool) {
	attID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "attachment id")
	if !ok {
		return db.Attachment{}, false
	}
	ws, wok := p.WorkspaceUUID()
	agentID, aok := p.AgentUUID()
	if !wok || !aok {
		writeError(w, http.StatusForbidden, "access denied")
		return db.Attachment{}, false
	}
	att, err := h.Queries.GetAttachment(r.Context(), db.GetAttachmentParams{
		ID:          attID,
		WorkspaceID: ws,
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "attachment not found")
		return db.Attachment{}, false
	}
	if !h.agentAttachmentVisible(r.Context(), ws, agentID, att.ID) {
		writeError(w, http.StatusNotFound, "attachment not found")
		return db.Attachment{}, false
	}
	return att, true
}

// UploadAgentAttachment — POST /api/agent/attachments
// Principal-native: never uses owner workspace/channel membership
// (Parker/Barry counterfactuals ①②).
func (h *Handler) UploadAgentAttachment(w http.ResponseWriter, r *http.Request) {
	p, ok := h.requireAgentPrincipal(w, r)
	if !ok {
		return
	}
	if h.Storage == nil {
		writeError(w, http.StatusServiceUnavailable, "file upload not configured")
		return
	}
	ws, wok := p.WorkspaceUUID()
	agentID, aok := p.AgentUUID()
	if !wok || !aok {
		writeError(w, http.StatusForbidden, "access denied")
		return
	}
	workspaceID := p.WorkspaceID

	if _, err := h.Queries.GetAgentInWorkspace(r.Context(), db.GetAgentInWorkspaceParams{
		ID:          agentID,
		WorkspaceID: ws,
	}); err != nil {
		writeError(w, http.StatusForbidden, "access denied")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize)
	if err := r.ParseMultipartForm(maxUploadSize); err != nil {
		writeError(w, http.StatusBadRequest, "file too large or invalid multipart form")
		return
	}
	defer r.MultipartForm.RemoveAll()

	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("missing file field: %v", err))
		return
	}
	defer file.Close()

	buf := make([]byte, 512)
	n, err := file.Read(buf)
	if err != nil && err != io.EOF {
		writeError(w, http.StatusBadRequest, "failed to read file")
		return
	}
	contentType := canonicalUploadContentType(http.DetectContentType(buf[:n]), header.Filename)
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read file")
		return
	}
	data, err := io.ReadAll(file)
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read file")
		return
	}

	id, err := uuid.NewV7()
	if err != nil {
		slog.Error("failed to generate uuid", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	filename := id.String() + path.Ext(header.Filename)
	key := "workspaces/" + workspaceID + "/" + filename

	params := db.CreateAttachmentParams{
		ID:           pgtype.UUID{Bytes: id, Valid: true},
		WorkspaceID:  ws,
		UploaderType: "agent",
		UploaderID:   agentID,
		Filename:     header.Filename,
		ContentType:  contentType,
		SizeBytes:    int64(len(data)),
	}

	if issueID := r.FormValue("issue_id"); issueID != "" {
		issueUUID, ok := parseUUIDOrBadRequest(w, issueID, "issue_id")
		if !ok {
			return
		}
		issue, err := h.Queries.GetIssueInWorkspace(r.Context(), db.GetIssueInWorkspaceParams{
			ID:          issueUUID,
			WorkspaceID: ws,
		})
		if err != nil {
			writeError(w, http.StatusForbidden, "invalid issue_id")
			return
		}
		params.IssueID = issue.ID
	}
	if commentID := r.FormValue("comment_id"); commentID != "" {
		commentUUID, ok := parseUUIDOrBadRequest(w, commentID, "comment_id")
		if !ok {
			return
		}
		comment, err := h.Queries.GetComment(r.Context(), commentUUID)
		if err != nil || uuidToString(comment.WorkspaceID) != workspaceID {
			writeError(w, http.StatusForbidden, "invalid comment_id")
			return
		}
		params.CommentID = comment.ID
	}
	if chatSessionID := r.FormValue("chat_session_id"); chatSessionID != "" {
		sessionUUID, ok := parseUUIDOrBadRequest(w, chatSessionID, "chat_session_id")
		if !ok {
			return
		}
		session, err := h.Queries.GetChatSession(r.Context(), sessionUUID)
		if err != nil || uuidToString(session.WorkspaceID) != workspaceID {
			writeError(w, http.StatusForbidden, "invalid chat_session_id")
			return
		}
		// Agent may only attach to its own sessions (not owner sessions).
		if uuidToString(session.AgentID) != uuidToString(agentID) {
			writeError(w, http.StatusForbidden, "chat session not owned by agent")
			return
		}
		params.ChatSessionID = session.ID
	}
	if channelID := r.FormValue("channel_id"); channelID != "" {
		chUUID, ok := parseUUIDOrBadRequest(w, channelID, "channel_id")
		if !ok {
			return
		}
		if !h.channelExists(r.Context(), workspaceID, chUUID) {
			writeError(w, http.StatusForbidden, "invalid channel_id")
			return
		}
		// ① agent direct membership only — never owner human membership.
		if !h.agentHasSurfaceAccess(r.Context(), ws, agentID, chUUID) {
			writeError(w, http.StatusForbidden, "not a channel member")
			return
		}
		if !h.requireChannelWritable(w, r.Context(), workspaceID, chUUID) {
			return
		}
		params.ChannelID = chUUID
	}

	// Unbound allowed as uploader-owned staging (DM/thread bind at send).
	// Visibility: only this agent (uploader_type=agent, uploader_id=self).

	link, err := h.Storage.Upload(r.Context(), key, data, contentType, header.Filename)
	if err != nil {
		slog.Error("agent file upload failed", "error", err)
		writeError(w, http.StatusInternalServerError, "upload failed")
		return
	}
	params.Url = link

	att, err := h.Queries.CreateAttachment(r.Context(), params)
	if err != nil {
		slog.Error("failed to create agent attachment record", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to create attachment record")
		return
	}
	resp := h.attachmentToResponse(att)
	resp.DownloadURL = "/api/agent/attachments/" + uuidToString(att.ID) + "/download"
	writeJSON(w, http.StatusOK, resp)
}
