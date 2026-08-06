package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
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
	Target          string `json:"target"`
	Filename        string `json:"filename"`
	ContentType     string `json:"content_type"`
	SizeBytes       int64  `json:"size_bytes"`
	ChecksumSHA256  string `json:"checksum_sha256"`
	ClientRequestID string `json:"client_request_id,omitempty"`
}

type AgentAttachmentUploadSessionResponse struct {
	ID             string            `json:"id"`
	Target         string            `json:"target"`
	UploadURL      string            `json:"upload_url,omitempty"`
	Method         string            `json:"method,omitempty"`
	Headers        map[string]string `json:"headers,omitempty"`
	ExpiresAt      string            `json:"expires_at"`
	State          string            `json:"state"`
	FailureCode    string            `json:"failure_code,omitempty"`
	ContentType    string            `json:"content_type"`
	SizeBytes      int64             `json:"size_bytes"`
	ChecksumSHA256 string            `json:"checksum_sha256"`
	AttachmentID   string            `json:"attachment_id,omitempty"`
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
	ChecksumSHA256      string
	IdempotencyKey      pgtype.UUID
	UploadMode          string
	State               string
	ExpiresAt           pgtype.Timestamptz
	AttachmentID        pgtype.UUID
	CancelledAt         pgtype.Timestamptz
	FailureCode         string
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
	if _, ok := h.Storage.(storage.UploadSessionStorage); !ok {
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
	filename, contentType, checksumSHA256, err := validateAgentAttachmentUploadSessionCreate(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	idempotencyKey, err := optionalAgentAttachmentUploadSessionID(req.ClientRequestID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid client_request_id")
		return
	}
	target, err := h.resolveAgentTransportTarget(r.Context(), source.origin, req.Target, true)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid or ambiguous target")
		return
	}
	now := h.agentAttachmentUploadSessionNow()
	canonicalTarget := agentTransportCanonicalMessageTarget(target)
	if idempotencyKey.Valid {
		existing, err := h.loadAgentAttachmentUploadSessionByIdempotencyKey(r.Context(), h.DB, source.origin.workspaceID, source.origin.agentID, idempotencyKey)
		if err == nil {
			if !sameAgentAttachmentUploadSessionRequest(existing, canonicalTarget, filename, contentType, req.SizeBytes, checksumSHA256) {
				writeError(w, http.StatusConflict, "client_request_id was already used with different upload metadata")
				return
			}
			h.writeExistingAgentAttachmentUploadSession(w, r, existing, now)
			return
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusInternalServerError, "failed to load upload session")
			return
		}
	}
	sessionID, err := uuid.NewV7()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create upload session")
		return
	}
	objectKey := "agent-upload-sessions/" + uuidToString(source.origin.workspaceID) + "/" + uuidToString(source.origin.agentID) + "/" + sessionID.String() + path.Ext(filename)
	expiresAt := now.Add(agentAttachmentUploadSessionTTL)
	session := agentAttachmentUploadSession{
		ID:                  pgtype.UUID{Bytes: sessionID, Valid: true},
		WorkspaceID:         source.origin.workspaceID,
		AgentID:             source.origin.agentID,
		ChannelID:           parseUUID(target.channel.ID),
		ThreadRootMessageID: target.threadRootMessageID,
		ContextTarget:       canonicalTarget,
		ObjectKey:           objectKey,
		Filename:            filename,
		ContentType:         contentType,
		SizeBytes:           req.SizeBytes,
		ChecksumSHA256:      checksumSHA256,
		IdempotencyKey:      idempotencyKey,
		UploadMode:          "presigned",
		State:               "pending",
		ExpiresAt:           pgtype.Timestamptz{Time: expiresAt, Valid: true},
	}
	tag, err := h.DB.Exec(r.Context(), `
		INSERT INTO agent_attachment_upload_session (
			id, workspace_id, agent_id, channel_id, thread_root_message_id,
			context_target, object_key, filename, content_type, size_bytes,
			checksum_sha256, idempotency_key, upload_mode, state, expires_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, 'presigned', 'pending', $13)
		ON CONFLICT (workspace_id, agent_id, idempotency_key) WHERE idempotency_key IS NOT NULL DO NOTHING`,
		session.ID, session.WorkspaceID, session.AgentID, session.ChannelID, session.ThreadRootMessageID,
		session.ContextTarget, session.ObjectKey, session.Filename, session.ContentType, session.SizeBytes,
		session.ChecksumSHA256, session.IdempotencyKey, expiresAt,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create upload session")
		return
	}
	if tag.RowsAffected() == 0 {
		existing, err := h.loadAgentAttachmentUploadSessionByIdempotencyKey(r.Context(), h.DB, source.origin.workspaceID, source.origin.agentID, idempotencyKey)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load upload session")
			return
		}
		if !sameAgentAttachmentUploadSessionRequest(existing, canonicalTarget, filename, contentType, req.SizeBytes, checksumSHA256) {
			writeError(w, http.StatusConflict, "client_request_id was already used with different upload metadata")
			return
		}
		h.writeExistingAgentAttachmentUploadSession(w, r, existing, now)
		return
	}
	destination, uploadMode, err := h.agentAttachmentUploadSessionDestination(r.Context(), session, now)
	if err != nil {
		_, _ = h.DB.Exec(r.Context(), `DELETE FROM agent_attachment_upload_session WHERE id = $1 AND state = 'pending'`, session.ID)
		writeError(w, http.StatusServiceUnavailable, "failed to create upload destination")
		return
	}
	if uploadMode != session.UploadMode {
		session.UploadMode = uploadMode
		if _, err := h.DB.Exec(r.Context(), `UPDATE agent_attachment_upload_session SET upload_mode = 'local' WHERE id = $1`, session.ID); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to create upload session")
			return
		}
	}
	logAgentAttachmentUploadSessionState(session, "pending", "created")
	writeJSON(w, http.StatusCreated, agentAttachmentUploadSessionResponse(session, destination, now))
}

func (h *Handler) agentAttachmentUploadSessionNow() time.Time {
	if h.UploadSessionNow != nil {
		return h.UploadSessionNow().UTC()
	}
	return time.Now().UTC()
}

func canonicalAgentAttachmentUploadChecksum(raw string) (string, error) {
	checksum := strings.ToLower(strings.TrimSpace(raw))
	decoded, err := hex.DecodeString(checksum)
	if err != nil || len(decoded) != sha256.Size {
		return "", errors.New("checksum_sha256 must be a SHA-256 hex digest")
	}
	return checksum, nil
}

func optionalAgentAttachmentUploadSessionID(raw string) (pgtype.UUID, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return pgtype.UUID{}, nil
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return pgtype.UUID{}, err
	}
	return pgtype.UUID{Bytes: id, Valid: true}, nil
}

func sameAgentAttachmentUploadSessionRequest(session agentAttachmentUploadSession, target, filename, contentType string, sizeBytes int64, checksumSHA256 string) bool {
	return session.ContextTarget == target &&
		session.Filename == filename &&
		session.ContentType == contentType &&
		session.SizeBytes == sizeBytes &&
		strings.EqualFold(session.ChecksumSHA256, checksumSHA256)
}

func (h *Handler) agentAttachmentUploadSessionDestination(ctx context.Context, session agentAttachmentUploadSession, now time.Time) (storage.UploadSessionDestination, string, error) {
	if err := agentAttachmentUploadSessionPending(session, now); err != nil {
		return storage.UploadSessionDestination{}, "", err
	}
	sessionStorage, ok := h.Storage.(storage.UploadSessionStorage)
	if !ok {
		return storage.UploadSessionDestination{}, "", errors.New("attachment upload sessions are not configured")
	}
	destination, err := sessionStorage.PresignUpload(ctx, session.ObjectKey, session.ExpiresAt.Time.Sub(now), session.ContentType, session.Filename, session.ChecksumSHA256)
	if err != nil {
		return storage.UploadSessionDestination{}, "", err
	}
	if strings.TrimSpace(destination.Method) == "" {
		destination.Method = http.MethodPut
	}
	if destination.Headers == nil {
		destination.Headers = map[string]string{}
	}
	if _, found := destination.Headers["Content-Type"]; !found {
		destination.Headers["Content-Type"] = session.ContentType
	}
	if strings.TrimSpace(destination.URL) == "" {
		destination.URL = "/api/agent/attachment-upload-sessions/" + uuidToString(session.ID) + "/object"
		return destination, "local", nil
	}
	return destination, "presigned", nil
}

func agentAttachmentUploadSessionResponse(session agentAttachmentUploadSession, destination storage.UploadSessionDestination, now time.Time) AgentAttachmentUploadSessionResponse {
	response := AgentAttachmentUploadSessionResponse{
		ID:             uuidToString(session.ID),
		Target:         session.ContextTarget,
		UploadURL:      destination.URL,
		Method:         destination.Method,
		Headers:        destination.Headers,
		ExpiresAt:      session.ExpiresAt.Time.UTC().Format(time.RFC3339),
		State:          agentAttachmentUploadSessionState(session, now),
		FailureCode:    session.FailureCode,
		ContentType:    session.ContentType,
		SizeBytes:      session.SizeBytes,
		ChecksumSHA256: session.ChecksumSHA256,
	}
	if session.AttachmentID.Valid {
		response.AttachmentID = uuidToString(session.AttachmentID)
	}
	return response
}

func agentAttachmentUploadSessionState(session agentAttachmentUploadSession, now time.Time) string {
	if session.State == "pending" && (!session.ExpiresAt.Valid || !now.Before(session.ExpiresAt.Time)) {
		return "expired"
	}
	return session.State
}

func logAgentAttachmentUploadSessionState(session agentAttachmentUploadSession, state, reasonCode string) {
	slog.Info("agent attachment upload session state", "session_id", uuidToString(session.ID), "state", state, "reason_code", reasonCode)
}

func validateAgentAttachmentUploadSessionCreate(req AgentAttachmentUploadSessionCreateRequest) (string, string, string, error) {
	filename := strings.TrimSpace(req.Filename)
	if filename == "" || filename != path.Base(filename) || filename == "." || filename == ".." {
		return "", "", "", errors.New("filename is required")
	}
	if req.SizeBytes <= 0 {
		return "", "", "", errors.New("attachment size must be positive")
	}
	if req.SizeBytes > maxUploadSize {
		return "", "", "", errors.New("attachment exceeds the server upload size limit")
	}
	contentType, _, err := mime.ParseMediaType(strings.TrimSpace(req.ContentType))
	if err != nil || contentType == "" {
		return "", "", "", errors.New("invalid content_type")
	}
	checksum, err := canonicalAgentAttachmentUploadChecksum(req.ChecksumSHA256)
	if err != nil {
		return "", "", "", err
	}
	return filename, strings.ToLower(contentType), checksum, nil
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
	if err := agentAttachmentUploadSessionPending(session, h.agentAttachmentUploadSessionNow()); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	if session.UploadMode != "local" {
		writeError(w, http.StatusNotFound, "upload destination not found")
		return
	}
	if r.ContentLength >= 0 && r.ContentLength != session.SizeBytes {
		h.recordAgentAttachmentUploadSessionFailure(r.Context(), h.DB, session, "size_mismatch")
		writeError(w, http.StatusBadRequest, "uploaded object size does not match session")
		return
	}
	if contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || !strings.EqualFold(contentType, session.ContentType) {
		h.recordAgentAttachmentUploadSessionFailure(r.Context(), h.DB, session, "content_type_mismatch")
		writeError(w, http.StatusBadRequest, "uploaded object content type does not match session")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, session.SizeBytes)
	data, err := io.ReadAll(r.Body)
	if err != nil {
		h.recordAgentAttachmentUploadSessionFailure(r.Context(), h.DB, session, "upload_read_failed")
		writeError(w, http.StatusBadRequest, "failed to read uploaded object")
		return
	}
	if int64(len(data)) != session.SizeBytes {
		h.recordAgentAttachmentUploadSessionFailure(r.Context(), h.DB, session, "size_mismatch")
		writeError(w, http.StatusBadRequest, "uploaded object size does not match session")
		return
	}
	digest := sha256.Sum256(data)
	if !strings.EqualFold(hex.EncodeToString(digest[:]), session.ChecksumSHA256) {
		h.recordAgentAttachmentUploadSessionFailure(r.Context(), h.DB, session, "checksum_mismatch")
		writeError(w, http.StatusBadRequest, "uploaded object checksum does not match session")
		return
	}
	if _, err := h.Storage.Upload(r.Context(), session.ObjectKey, data, session.ContentType, session.Filename); err != nil {
		h.recordAgentAttachmentUploadSessionFailure(r.Context(), h.DB, session, "storage_write_failed")
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
	now := h.agentAttachmentUploadSessionNow()
	if session.State == "completed" && session.AttachmentID.Valid {
		if !session.ExpiresAt.Valid || !now.Before(session.ExpiresAt.Time) {
			_ = tx.Rollback(r.Context())
			writeError(w, http.StatusConflict, "upload session has expired")
			return
		}
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
	if err := agentAttachmentUploadSessionPending(session, now); err != nil {
		_ = tx.Rollback(r.Context())
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	if _, err := canonicalAgentAttachmentUploadChecksum(session.ChecksumSHA256); err != nil {
		h.recordAgentAttachmentUploadSessionFailure(r.Context(), tx, session, "integrity_metadata_missing")
		if err := tx.Commit(r.Context()); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to complete upload session")
			return
		}
		writeError(w, http.StatusConflict, "upload session is missing integrity metadata")
		return
	}
	object, err := sessionStorage.VerifyUpload(r.Context(), session.ObjectKey)
	if err != nil {
		h.recordAgentAttachmentUploadSessionFailure(r.Context(), tx, session, "object_unavailable")
		if err := tx.Commit(r.Context()); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to complete upload session")
			return
		}
		writeError(w, http.StatusConflict, "uploaded object is not available for verification")
		return
	}
	failureCode := ""
	switch {
	case object.SizeBytes != session.SizeBytes:
		failureCode = "size_mismatch"
	case !sameUploadContentType(object.ContentType, session.ContentType):
		failureCode = "content_type_mismatch"
	case !strings.EqualFold(strings.TrimSpace(object.ChecksumSHA256), session.ChecksumSHA256):
		failureCode = "checksum_mismatch"
	case strings.TrimSpace(object.URL) == "":
		failureCode = "object_unavailable"
	}
	if failureCode != "" {
		h.recordAgentAttachmentUploadSessionFailure(r.Context(), tx, session, failureCode)
		if err := tx.Commit(r.Context()); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to complete upload session")
			return
		}
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
		SET state = 'completed', attachment_id = $2, completed_at = $3, failure_code = NULL
		WHERE id = $1 AND state = 'pending'`, session.ID, att.ID, now); err != nil {
		_ = tx.Rollback(r.Context())
		writeError(w, http.StatusInternalServerError, "failed to complete upload session")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to complete upload session")
		return
	}
	session.State = "completed"
	session.AttachmentID = att.ID
	logAgentAttachmentUploadSessionState(session, "completed", "verified")
	writeJSON(w, http.StatusCreated, h.attachmentToResponse(att))
}

// GetAgentAttachmentUploadSessionStatus returns the owner-scoped recovery
// state. It intentionally does not issue a fresh direct-upload capability;
// callers must use the explicit retry endpoint for that action.
func (h *Handler) GetAgentAttachmentUploadSessionStatus(w http.ResponseWriter, r *http.Request) {
	source, ok := h.requireAgentTransportSource(w, r)
	if !ok {
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
	writeJSON(w, http.StatusOK, agentAttachmentUploadSessionResponse(session, storage.UploadSessionDestination{}, h.agentAttachmentUploadSessionNow()))
}

// RetryAgentAttachmentUploadSession returns a newly bounded direct-upload
// capability for an unfinished session after an interrupted upload.
func (h *Handler) RetryAgentAttachmentUploadSession(w http.ResponseWriter, r *http.Request) {
	source, ok := h.requireAgentTransportSource(w, r)
	if !ok {
		return
	}
	if _, ok := h.Storage.(storage.UploadSessionStorage); !ok {
		writeError(w, http.StatusServiceUnavailable, "attachment upload sessions are not configured")
		return
	}
	sessionID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "sessionId"), "upload session id")
	if !ok {
		return
	}
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to retry upload session")
		return
	}
	session, err := h.loadAgentAttachmentUploadSession(r.Context(), tx, sessionID, source.origin.workspaceID, source.origin.agentID, true)
	if err != nil {
		_ = tx.Rollback(r.Context())
		writeAgentAttachmentUploadSessionLoadError(w, err)
		return
	}
	now := h.agentAttachmentUploadSessionNow()
	if err := agentAttachmentUploadSessionPending(session, now); err != nil {
		_ = tx.Rollback(r.Context())
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	destination, uploadMode, err := h.agentAttachmentUploadSessionDestination(r.Context(), session, now)
	if err != nil {
		_ = tx.Rollback(r.Context())
		writeError(w, http.StatusServiceUnavailable, "failed to create upload destination")
		return
	}
	if uploadMode != session.UploadMode {
		_ = tx.Rollback(r.Context())
		writeError(w, http.StatusConflict, "upload storage mode changed for session")
		return
	}
	if _, err := tx.Exec(r.Context(), `UPDATE agent_attachment_upload_session SET failure_code = NULL WHERE id = $1 AND state = 'pending'`, session.ID); err != nil {
		_ = tx.Rollback(r.Context())
		writeError(w, http.StatusInternalServerError, "failed to retry upload session")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to retry upload session")
		return
	}
	session.FailureCode = ""
	logAgentAttachmentUploadSessionState(session, "pending", "retry")
	writeJSON(w, http.StatusOK, agentAttachmentUploadSessionResponse(session, destination, now))
}

// CancelAgentAttachmentUploadSession permanently prevents completion and
// attachment use for a pending session. Storage cleanup is best effort; the
// database state remains the authority for send eligibility.
func (h *Handler) CancelAgentAttachmentUploadSession(w http.ResponseWriter, r *http.Request) {
	source, ok := h.requireAgentTransportSource(w, r)
	if !ok {
		return
	}
	sessionID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "sessionId"), "upload session id")
	if !ok {
		return
	}
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to cancel upload session")
		return
	}
	session, err := h.loadAgentAttachmentUploadSession(r.Context(), tx, sessionID, source.origin.workspaceID, source.origin.agentID, true)
	if err != nil {
		_ = tx.Rollback(r.Context())
		writeAgentAttachmentUploadSessionLoadError(w, err)
		return
	}
	if session.State == "completed" {
		_ = tx.Rollback(r.Context())
		writeError(w, http.StatusConflict, "completed upload session cannot be cancelled")
		return
	}
	if session.State == "cancelled" {
		if err := tx.Commit(r.Context()); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to cancel upload session")
			return
		}
		writeJSON(w, http.StatusOK, agentAttachmentUploadSessionResponse(session, storage.UploadSessionDestination{}, h.agentAttachmentUploadSessionNow()))
		return
	}
	now := h.agentAttachmentUploadSessionNow()
	if _, err := tx.Exec(r.Context(), `
		UPDATE agent_attachment_upload_session
		SET state = 'cancelled', cancelled_at = $2, failure_code = 'cancelled'
		WHERE id = $1 AND state = 'pending'`, session.ID, now); err != nil {
		_ = tx.Rollback(r.Context())
		writeError(w, http.StatusInternalServerError, "failed to cancel upload session")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to cancel upload session")
		return
	}
	session.State = "cancelled"
	session.CancelledAt = pgtype.Timestamptz{Time: now, Valid: true}
	session.FailureCode = "cancelled"
	h.Storage.Delete(r.Context(), session.ObjectKey)
	logAgentAttachmentUploadSessionState(session, "cancelled", "cancelled")
	writeJSON(w, http.StatusOK, agentAttachmentUploadSessionResponse(session, storage.UploadSessionDestination{}, now))
}

func (h *Handler) writeExistingAgentAttachmentUploadSession(w http.ResponseWriter, r *http.Request, session agentAttachmentUploadSession, now time.Time) {
	if err := agentAttachmentUploadSessionPending(session, now); err != nil {
		writeJSON(w, http.StatusOK, agentAttachmentUploadSessionResponse(session, storage.UploadSessionDestination{}, now))
		return
	}
	destination, uploadMode, err := h.agentAttachmentUploadSessionDestination(r.Context(), session, now)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "failed to create upload destination")
		return
	}
	if uploadMode != session.UploadMode {
		writeError(w, http.StatusConflict, "upload storage mode changed for session")
		return
	}
	writeJSON(w, http.StatusOK, agentAttachmentUploadSessionResponse(session, destination, now))
}

func (h *Handler) recordAgentAttachmentUploadSessionFailure(ctx context.Context, exec dbExecutor, session agentAttachmentUploadSession, failureCode string) {
	if _, err := exec.Exec(ctx, `UPDATE agent_attachment_upload_session SET failure_code = $2 WHERE id = $1 AND state = 'pending'`, session.ID, failureCode); err != nil {
		slog.Error("failed to record agent attachment upload session state", "session_id", uuidToString(session.ID), "state", "pending", "reason_code", failureCode)
		return
	}
	logAgentAttachmentUploadSessionState(session, "pending", failureCode)
}

func (h *Handler) loadAgentAttachmentUploadSession(ctx context.Context, exec dbExecutor, sessionID, workspaceID, agentID pgtype.UUID, lock bool) (agentAttachmentUploadSession, error) {
	query := `
		SELECT id, workspace_id, agent_id, channel_id, thread_root_message_id,
		       context_target, object_key, filename, content_type, size_bytes,
		       COALESCE(checksum_sha256, ''), idempotency_key, upload_mode, state, expires_at,
		       attachment_id, cancelled_at, COALESCE(failure_code, '')
		FROM agent_attachment_upload_session
		WHERE id = $1 AND workspace_id = $2 AND agent_id = $3`
	if lock {
		query += " FOR UPDATE"
	}
	var session agentAttachmentUploadSession
	err := exec.QueryRow(ctx, query, sessionID, workspaceID, agentID).Scan(
		&session.ID, &session.WorkspaceID, &session.AgentID, &session.ChannelID, &session.ThreadRootMessageID,
		&session.ContextTarget, &session.ObjectKey, &session.Filename, &session.ContentType, &session.SizeBytes,
		&session.ChecksumSHA256, &session.IdempotencyKey, &session.UploadMode, &session.State, &session.ExpiresAt,
		&session.AttachmentID, &session.CancelledAt, &session.FailureCode,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return agentAttachmentUploadSession{}, pgx.ErrNoRows
	}
	return session, err
}

func (h *Handler) loadAgentAttachmentUploadSessionByIdempotencyKey(ctx context.Context, exec dbExecutor, workspaceID, agentID, idempotencyKey pgtype.UUID) (agentAttachmentUploadSession, error) {
	var session agentAttachmentUploadSession
	err := exec.QueryRow(ctx, `
		SELECT id, workspace_id, agent_id, channel_id, thread_root_message_id,
		       context_target, object_key, filename, content_type, size_bytes,
		       COALESCE(checksum_sha256, ''), idempotency_key, upload_mode, state, expires_at,
		       attachment_id, cancelled_at, COALESCE(failure_code, '')
		FROM agent_attachment_upload_session
		WHERE workspace_id = $1 AND agent_id = $2 AND idempotency_key = $3`,
		workspaceID, agentID, idempotencyKey,
	).Scan(
		&session.ID, &session.WorkspaceID, &session.AgentID, &session.ChannelID, &session.ThreadRootMessageID,
		&session.ContextTarget, &session.ObjectKey, &session.Filename, &session.ContentType, &session.SizeBytes,
		&session.ChecksumSHA256, &session.IdempotencyKey, &session.UploadMode, &session.State, &session.ExpiresAt,
		&session.AttachmentID, &session.CancelledAt, &session.FailureCode,
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
	switch session.State {
	case "pending":
	case "completed":
		return errors.New("upload session is already completed")
	case "cancelled":
		return errors.New("upload session was cancelled")
	default:
		return errors.New("upload session is unavailable")
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
		  AND session.expires_at > $7
		  AND session.checksum_sha256 IS NOT NULL
		  AND session.attachment_id = ANY($6::uuid[])`,
		source.origin.workspaceID, source.origin.agentID, parseUUID(target.channel.ID), nullableUUID(target.threadRootMessageID),
		agentTransportCanonicalMessageTarget(target), attachmentIDs, h.agentAttachmentUploadSessionNow(),
	).Scan(&verified)
	if err != nil {
		return err
	}
	if verified != expected {
		return errChannelAttachmentUnavailable
	}
	return linkOwnedAttachmentsToChannelMessage(ctx, h.Queries.WithTx(tx), messageID, source.origin.workspaceID, "agent", source.origin.agentID, attachmentIDs)
}
