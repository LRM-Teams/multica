package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/storage"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const agentAttachmentUploadSessionTTL = 15 * time.Minute

type AgentAttachmentUploadCapabilitiesResponse struct {
	MaxSizeBytes      int64 `json:"max_size_bytes"`
	SessionTTLSeconds int64 `json:"session_ttl_seconds"`
}

type AgentAttachmentUploadSessionCreateRequest struct {
	Target      string `json:"target"`
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	SizeBytes   int64  `json:"size_bytes"`
}

type AgentAttachmentUploadSessionResponse struct {
	ID        string            `json:"id"`
	Target    string            `json:"target"`
	UploadURL string            `json:"upload_url"`
	Method    string            `json:"method"`
	Headers   map[string]string `json:"headers,omitempty"`
	ExpiresAt string            `json:"expires_at"`
}

type agentAttachmentUploadSession struct {
	ID                  pgtype.UUID
	WorkspaceID         pgtype.UUID
	AgentID             pgtype.UUID
	ChannelID           pgtype.UUID
	ThreadRootMessageID pgtype.UUID
	ContextTarget       string
	ObjectKey           string
	Filename            string
	ContentType         string
	SizeBytes           int64
	UploadMode          string
	State               string
	ExpiresAt           pgtype.Timestamptz
	AttachmentID        pgtype.UUID
}

// AgentAttachmentUploadCapabilities reports the server-owned size and expiry
// limits before an Agent starts a direct upload session.
func (h *Handler) AgentAttachmentUploadCapabilities(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAgentTransportSource(w, r); !ok {
		return
	}
	if _, ok := h.Storage.(storage.UploadSessionStorage); !ok {
		writeError(w, http.StatusServiceUnavailable, "attachment upload sessions are not configured")
		return
	}
	writeJSON(w, http.StatusOK, AgentAttachmentUploadCapabilitiesResponse{
		MaxSizeBytes:      maxUploadSize,
		SessionTTLSeconds: int64(agentAttachmentUploadSessionTTL / time.Second),
	})
}

// CreateAgentAttachmentUploadSession authorizes one target-bound object key
// before the Agent receives any direct upload destination.
func (h *Handler) CreateAgentAttachmentUploadSession(w http.ResponseWriter, r *http.Request) {
	source, ok := h.requireAgentTransportSource(w, r)
	if !ok {
		return
	}
	sessionStorage, ok := h.Storage.(storage.UploadSessionStorage)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "attachment upload sessions are not configured")
		return
	}
	var req AgentAttachmentUploadSessionCreateRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	filename, contentType, err := validateAgentAttachmentUploadSessionCreate(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	target, err := h.resolveAgentTransportTarget(r.Context(), source.task, source.origin, req.Target, true)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid or ambiguous target")
		return
	}
	sessionID, err := uuid.NewV7()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create upload session")
		return
	}
	objectKey := "agent-upload-sessions/" + uuidToString(source.origin.workspaceID) + "/" + uuidToString(source.origin.agentID) + "/" + sessionID.String() + path.Ext(filename)
	expiresAt := time.Now().UTC().Add(agentAttachmentUploadSessionTTL)
	destination, err := sessionStorage.PresignUpload(r.Context(), objectKey, agentAttachmentUploadSessionTTL, contentType, filename)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "failed to create upload destination")
		return
	}
	if strings.TrimSpace(destination.Method) == "" {
		destination.Method = http.MethodPut
	}
	if destination.Headers == nil {
		destination.Headers = map[string]string{}
	}
	if _, found := destination.Headers["Content-Type"]; !found {
		destination.Headers["Content-Type"] = contentType
	}
	uploadMode := "presigned"
	if strings.TrimSpace(destination.URL) == "" {
		uploadMode = "local"
		destination.URL = "/api/agent/attachment-upload-sessions/" + sessionID.String() + "/object"
	}
	if _, err := h.DB.Exec(r.Context(), `
		INSERT INTO agent_attachment_upload_session (
			id, workspace_id, agent_id, channel_id, thread_root_message_id,
			context_target, object_key, filename, content_type, size_bytes,
			upload_mode, state, expires_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, 'pending', $12)`,
		pgtype.UUID{Bytes: sessionID, Valid: true}, source.origin.workspaceID, source.origin.agentID,
		parseUUID(target.channel.ID), nullableUUID(target.threadRootMessageID), agentTransportCanonicalMessageTarget(target),
		objectKey, filename, contentType, req.SizeBytes, uploadMode, expiresAt,
	); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create upload session")
		return
	}
	writeJSON(w, http.StatusCreated, AgentAttachmentUploadSessionResponse{
		ID:        sessionID.String(),
		Target:    agentTransportCanonicalMessageTarget(target),
		UploadURL: destination.URL,
		Method:    destination.Method,
		Headers:   destination.Headers,
		ExpiresAt: expiresAt.Format(time.RFC3339),
	})
}

func validateAgentAttachmentUploadSessionCreate(req AgentAttachmentUploadSessionCreateRequest) (string, string, error) {
	filename := strings.TrimSpace(req.Filename)
	if filename == "" || filename != path.Base(filename) || filename == "." || filename == ".." {
		return "", "", errors.New("filename is required")
	}
	if req.SizeBytes <= 0 {
		return "", "", errors.New("attachment size must be positive")
	}
	if req.SizeBytes > maxUploadSize {
		return "", "", errors.New("attachment exceeds the server upload size limit")
	}
	contentType, _, err := mime.ParseMediaType(strings.TrimSpace(req.ContentType))
	if err != nil || contentType == "" {
		return "", "", errors.New("invalid content_type")
	}
	return filename, strings.ToLower(contentType), nil
}

// UploadAgentAttachmentSessionObject is the local-storage direct destination.
// S3 sessions bypass this handler and PUT to their presigned object URL.
func (h *Handler) UploadAgentAttachmentSessionObject(w http.ResponseWriter, r *http.Request) {
	source, ok := h.requireAgentTransportSource(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodPut {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	sessionID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "sessionId"), "upload session id")
	if !ok {
		return
	}
	session, err := h.loadAgentAttachmentUploadSession(r.Context(), h.DB, sessionID, source.origin.workspaceID, source.origin.agentID, false)
	if err != nil {
		writeAgentAttachmentUploadSessionLoadError(w, err)
		return
	}
	if err := agentAttachmentUploadSessionPending(session, time.Now()); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	if session.UploadMode != "local" {
		writeError(w, http.StatusNotFound, "upload destination not found")
		return
	}
	if r.ContentLength >= 0 && r.ContentLength != session.SizeBytes {
		writeError(w, http.StatusBadRequest, "uploaded object size does not match session")
		return
	}
	if contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || !strings.EqualFold(contentType, session.ContentType) {
		writeError(w, http.StatusBadRequest, "uploaded object content type does not match session")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, session.SizeBytes)
	data, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read uploaded object")
		return
	}
	if int64(len(data)) != session.SizeBytes {
		writeError(w, http.StatusBadRequest, "uploaded object size does not match session")
		return
	}
	if _, err := h.Storage.Upload(r.Context(), session.ObjectKey, data, session.ContentType, session.Filename); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to store uploaded object")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// CompleteAgentAttachmentUploadSession verifies the object in storage and
// atomically creates the canonical Attachment resource for a completed session.
func (h *Handler) CompleteAgentAttachmentUploadSession(w http.ResponseWriter, r *http.Request) {
	source, ok := h.requireAgentTransportSource(w, r)
	if !ok {
		return
	}
	sessionStorage, ok := h.Storage.(storage.UploadSessionStorage)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "attachment upload sessions are not configured")
		return
	}
	sessionID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "sessionId"), "upload session id")
	if !ok {
		return
	}
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to complete upload session")
		return
	}
	session, err := h.loadAgentAttachmentUploadSession(r.Context(), tx, sessionID, source.origin.workspaceID, source.origin.agentID, true)
	if err != nil {
		_ = tx.Rollback(r.Context())
		writeAgentAttachmentUploadSessionLoadError(w, err)
		return
	}
	if session.State == "completed" && session.AttachmentID.Valid {
		att, err := h.Queries.WithTx(tx).GetAttachment(r.Context(), db.GetAttachmentParams{ID: session.AttachmentID, WorkspaceID: session.WorkspaceID})
		if err != nil {
			_ = tx.Rollback(r.Context())
			writeError(w, http.StatusInternalServerError, "failed to load completed attachment")
			return
		}
		if err := tx.Commit(r.Context()); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to complete upload session")
			return
		}
		writeJSON(w, http.StatusOK, h.attachmentToResponse(att))
		return
	}
	if err := agentAttachmentUploadSessionPending(session, time.Now()); err != nil {
		_ = tx.Rollback(r.Context())
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	object, err := sessionStorage.VerifyUpload(r.Context(), session.ObjectKey)
	if err != nil {
		_ = tx.Rollback(r.Context())
		writeError(w, http.StatusConflict, "uploaded object is not available for verification")
		return
	}
	if object.SizeBytes != session.SizeBytes || !sameUploadContentType(object.ContentType, session.ContentType) || strings.TrimSpace(object.URL) == "" {
		_ = tx.Rollback(r.Context())
		writeError(w, http.StatusConflict, "uploaded object does not match upload session")
		return
	}
	attachmentID, err := uuid.NewV7()
	if err != nil {
		_ = tx.Rollback(r.Context())
		writeError(w, http.StatusInternalServerError, "failed to complete upload session")
		return
	}
	att, err := h.Queries.WithTx(tx).CreateAttachment(r.Context(), db.CreateAttachmentParams{
		ID:           pgtype.UUID{Bytes: attachmentID, Valid: true},
		WorkspaceID:  session.WorkspaceID,
		ChannelID:    session.ChannelID,
		UploaderType: "agent",
		UploaderID:   session.AgentID,
		Filename:     session.Filename,
		Url:          object.URL,
		ContentType:  session.ContentType,
		SizeBytes:    session.SizeBytes,
	})
	if err != nil {
		_ = tx.Rollback(r.Context())
		writeError(w, http.StatusInternalServerError, "failed to create attachment")
		return
	}
	if _, err := tx.Exec(r.Context(), `
		UPDATE agent_attachment_upload_session
		SET state = 'completed', attachment_id = $2, completed_at = now()
		WHERE id = $1 AND state = 'pending'`, session.ID, att.ID); err != nil {
		_ = tx.Rollback(r.Context())
		writeError(w, http.StatusInternalServerError, "failed to complete upload session")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to complete upload session")
		return
	}
	writeJSON(w, http.StatusCreated, h.attachmentToResponse(att))
}

func (h *Handler) loadAgentAttachmentUploadSession(ctx context.Context, exec dbExecutor, sessionID, workspaceID, agentID pgtype.UUID, lock bool) (agentAttachmentUploadSession, error) {
	query := `
		SELECT id, workspace_id, agent_id, channel_id, thread_root_message_id,
		       context_target, object_key, filename, content_type, size_bytes,
		       upload_mode, state, expires_at, attachment_id
		FROM agent_attachment_upload_session
		WHERE id = $1 AND workspace_id = $2 AND agent_id = $3`
	if lock {
		query += " FOR UPDATE"
	}
	var session agentAttachmentUploadSession
	err := exec.QueryRow(ctx, query, sessionID, workspaceID, agentID).Scan(
		&session.ID, &session.WorkspaceID, &session.AgentID, &session.ChannelID, &session.ThreadRootMessageID,
		&session.ContextTarget, &session.ObjectKey, &session.Filename, &session.ContentType, &session.SizeBytes,
		&session.UploadMode, &session.State, &session.ExpiresAt, &session.AttachmentID,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return agentAttachmentUploadSession{}, pgx.ErrNoRows
	}
	return session, err
}

func writeAgentAttachmentUploadSessionLoadError(w http.ResponseWriter, err error) {
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "upload session not found")
		return
	}
	writeError(w, http.StatusInternalServerError, "failed to load upload session")
}

func agentAttachmentUploadSessionPending(session agentAttachmentUploadSession, now time.Time) error {
	if session.State != "pending" {
		return errors.New("upload session is already completed")
	}
	if !session.ExpiresAt.Valid || !now.Before(session.ExpiresAt.Time) {
		return errors.New("upload session has expired")
	}
	return nil
}

func sameUploadContentType(got, want string) bool {
	got, _, gotErr := mime.ParseMediaType(strings.TrimSpace(got))
	want, _, wantErr := mime.ParseMediaType(strings.TrimSpace(want))
	return gotErr == nil && wantErr == nil && strings.EqualFold(got, want)
}

// linkVerifiedAgentUploadAttachmentsToChannelMessage proves that every
// requested Attachment was completed by this Agent for the exact canonical
// target and remains within its session expiry. It runs inside the Message
// transaction before the attachment references are linked.
func (h *Handler) linkVerifiedAgentUploadAttachmentsToChannelMessage(ctx context.Context, tx pgx.Tx, source agentTransportSource, target agentTransportTarget, messageID pgtype.UUID, attachmentIDs []pgtype.UUID) error {
	expected := len(channelAttachmentIDSet(attachmentIDs))
	if expected == 0 {
		return nil
	}
	var verified int
	err := tx.QueryRow(ctx, `
		SELECT count(DISTINCT session.attachment_id)
		FROM agent_attachment_upload_session session
		WHERE session.workspace_id = $1
		  AND session.agent_id = $2
		  AND session.channel_id = $3
		  AND session.thread_root_message_id IS NOT DISTINCT FROM $4
		  AND session.context_target = $5
		  AND session.state = 'completed'
		  AND session.expires_at > now()
		  AND session.attachment_id = ANY($6::uuid[])`,
		source.origin.workspaceID, source.origin.agentID, parseUUID(target.channel.ID), nullableUUID(target.threadRootMessageID),
		agentTransportCanonicalMessageTarget(target), attachmentIDs,
	).Scan(&verified)
	if err != nil {
		return err
	}
	if verified != expected {
		return errChannelAttachmentUnavailable
	}
	return linkOwnedAttachmentsToChannelMessage(ctx, h.Queries.WithTx(tx), messageID, source.origin.workspaceID, "agent", source.origin.agentID, attachmentIDs)
}
