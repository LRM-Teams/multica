package handler

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

func TestAgentAttachmentUploadSessionCompletesAndBuildsCanonicalMessageParts(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	store := &mockStorage{}
	previousStorage := testHandler.Storage
	testHandler.Storage = store
	t.Cleanup(func() { testHandler.Storage = previousStorage })

	taskID, channelID := createChannelCompletionTask(t, "group")
	agentID := agentIDForTask(t, taskID)
	target := "#" + channelNameForTransportTest(t, channelID)

	create := agentAttachmentUploadSessionRequest(t, http.MethodPost, "/api/agent/attachment-upload-sessions", taskID, agentID, map[string]any{
		"target": target, "filename": "report.png", "content_type": "image/png", "size_bytes": 4,
		"checksum_sha256": checksumSHA256ForTest([]byte("data")), "client_request_id": uuid.NewString(),
	})
	createRec := httptest.NewRecorder()
	testHandler.CreateAgentAttachmentUploadSession(createRec, create)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create upload session: status=%d body=%s", createRec.Code, createRec.Body.String())
	}
	var session AgentAttachmentUploadSessionResponse
	if err := json.Unmarshal(createRec.Body.Bytes(), &session); err != nil {
		t.Fatalf("decode upload session: %v", err)
	}
	if session.ID == "" || session.UploadURL == "" || session.Method != http.MethodPut || session.Headers["Content-Type"] != "image/png" {
		t.Fatalf("upload session response = %+v", session)
	}

	upload := agentAttachmentUploadRawRequest(t, http.MethodPut, session.UploadURL, taskID, agentID, []byte("data"))
	upload.Header.Set("Content-Type", "image/png")
	uploadRec := httptest.NewRecorder()
	testHandler.UploadAgentAttachmentSessionObject(uploadRec, upload)
	if uploadRec.Code != http.StatusNoContent {
		t.Fatalf("upload session object: status=%d body=%s", uploadRec.Code, uploadRec.Body.String())
	}

	complete := agentAttachmentUploadSessionRequest(t, http.MethodPost, "/api/agent/attachment-upload-sessions/"+session.ID+"/complete", taskID, agentID, map[string]any{})
	completeRec := httptest.NewRecorder()
	testHandler.CompleteAgentAttachmentUploadSession(completeRec, complete)
	if completeRec.Code != http.StatusCreated {
		t.Fatalf("complete upload session: status=%d body=%s", completeRec.Code, completeRec.Body.String())
	}
	var attachment AttachmentResponse
	if err := json.Unmarshal(completeRec.Body.Bytes(), &attachment); err != nil {
		t.Fatalf("decode completed attachment: %v", err)
	}
	if attachment.ID == "" || attachment.Filename != "report.png" || attachment.ChannelID == nil || *attachment.ChannelID != channelID {
		t.Fatalf("completed attachment = %+v", attachment)
	}

	idempotent := agentAttachmentUploadSessionRequest(t, http.MethodPost, "/api/agent/attachment-upload-sessions/"+session.ID+"/complete", taskID, agentID, map[string]any{})
	idempotentRec := httptest.NewRecorder()
	testHandler.CompleteAgentAttachmentUploadSession(idempotentRec, idempotent)
	if idempotentRec.Code != http.StatusOK {
		t.Fatalf("repeat completion: status=%d body=%s", idempotentRec.Code, idempotentRec.Body.String())
	}

	message := agentTransportSendForTest(t, taskID, agentID, map[string]any{
		"target": target, "content": "Please review the report.", "attachment_ids": []string{attachment.ID},
		"client_message_id": "agent-upload-session-" + uuid.NewString(), "bypass_freshness": true,
	})
	if message.Code != http.StatusCreated {
		t.Fatalf("send completed attachment: status=%d body=%s", message.Code, message.Body.String())
	}
	var response AgentTransportSendResponse
	if err := json.Unmarshal(message.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode sent message: %v", err)
	}
	if len(response.Message.Parts) != 2 || response.Message.Parts[0].Type != "text" || response.Message.Parts[0].Text != "Please review the report." || response.Message.Parts[1].Type != "attachment" || response.Message.Parts[1].AttachmentID != attachment.ID {
		t.Fatalf("canonical message parts = %+v", response.Message.Parts)
	}
}

func TestAgentAttachmentUploadSessionRetriesChecksumMismatchAndCancels(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	store := &mockStorage{}
	previousStorage := testHandler.Storage
	testHandler.Storage = store
	t.Cleanup(func() { testHandler.Storage = previousStorage })

	taskID, channelID := createChannelCompletionTask(t, "group")
	agentID := agentIDForTask(t, taskID)
	target := "#" + channelNameForTransportTest(t, channelID)
	data := []byte("data")
	session := createAgentAttachmentUploadSessionForTest(t, taskID, agentID, target, data, uuid.NewString())

	badUpload := agentAttachmentUploadRawRequest(t, http.MethodPut, session.UploadURL, taskID, agentID, []byte("nope"))
	badUpload.Header.Set("Content-Type", "image/png")
	badUploadRec := httptest.NewRecorder()
	testHandler.UploadAgentAttachmentSessionObject(badUploadRec, badUpload)
	if badUploadRec.Code != http.StatusBadRequest {
		t.Fatalf("checksum mismatch upload: status=%d body=%s", badUploadRec.Code, badUploadRec.Body.String())
	}

	statusReq := agentAttachmentUploadSessionRequest(t, http.MethodGet, "/api/agent/attachment-upload-sessions/"+session.ID, taskID, agentID, nil)
	statusRec := httptest.NewRecorder()
	testHandler.GetAgentAttachmentUploadSessionStatus(statusRec, statusReq)
	if statusRec.Code != http.StatusOK {
		t.Fatalf("get retryable session: status=%d body=%s", statusRec.Code, statusRec.Body.String())
	}
	var status AgentAttachmentUploadSessionResponse
	if err := json.Unmarshal(statusRec.Body.Bytes(), &status); err != nil {
		t.Fatalf("decode retryable status: %v", err)
	}
	if status.State != "pending" || status.FailureCode != "checksum_mismatch" || status.UploadURL != "" {
		t.Fatalf("retryable status = %+v", status)
	}

	retryReq := agentAttachmentUploadSessionRequest(t, http.MethodPost, "/api/agent/attachment-upload-sessions/"+session.ID+"/retry", taskID, agentID, map[string]any{})
	retryRec := httptest.NewRecorder()
	testHandler.RetryAgentAttachmentUploadSession(retryRec, retryReq)
	if retryRec.Code != http.StatusOK {
		t.Fatalf("retry upload session: status=%d body=%s", retryRec.Code, retryRec.Body.String())
	}
	var retry AgentAttachmentUploadSessionResponse
	if err := json.Unmarshal(retryRec.Body.Bytes(), &retry); err != nil {
		t.Fatalf("decode retry upload session: %v", err)
	}
	if retry.ID != session.ID || retry.State != "pending" || retry.FailureCode != "" || retry.UploadURL == "" {
		t.Fatalf("retry response = %+v", retry)
	}

	goodUpload := agentAttachmentUploadRawRequest(t, http.MethodPut, retry.UploadURL, taskID, agentID, data)
	goodUpload.Header.Set("Content-Type", "image/png")
	goodUploadRec := httptest.NewRecorder()
	testHandler.UploadAgentAttachmentSessionObject(goodUploadRec, goodUpload)
	if goodUploadRec.Code != http.StatusNoContent {
		t.Fatalf("retry upload object: status=%d body=%s", goodUploadRec.Code, goodUploadRec.Body.String())
	}
	completeReq := agentAttachmentUploadSessionRequest(t, http.MethodPost, "/api/agent/attachment-upload-sessions/"+session.ID+"/complete", taskID, agentID, map[string]any{})
	completeRec := httptest.NewRecorder()
	testHandler.CompleteAgentAttachmentUploadSession(completeRec, completeReq)
	if completeRec.Code != http.StatusCreated {
		t.Fatalf("complete retried session: status=%d body=%s", completeRec.Code, completeRec.Body.String())
	}

	cancelled := createAgentAttachmentUploadSessionForTest(t, taskID, agentID, target, data, uuid.NewString())
	cancelReq := agentAttachmentUploadSessionRequest(t, http.MethodPost, "/api/agent/attachment-upload-sessions/"+cancelled.ID+"/cancel", taskID, agentID, map[string]any{})
	cancelRec := httptest.NewRecorder()
	testHandler.CancelAgentAttachmentUploadSession(cancelRec, cancelReq)
	if cancelRec.Code != http.StatusOK {
		t.Fatalf("cancel upload session: status=%d body=%s", cancelRec.Code, cancelRec.Body.String())
	}
	var cancelStatus AgentAttachmentUploadSessionResponse
	if err := json.Unmarshal(cancelRec.Body.Bytes(), &cancelStatus); err != nil {
		t.Fatalf("decode cancelled status: %v", err)
	}
	if cancelStatus.State != "cancelled" || cancelStatus.FailureCode != "cancelled" {
		t.Fatalf("cancel response = %+v", cancelStatus)
	}

	cancelUpload := agentAttachmentUploadRawRequest(t, http.MethodPut, cancelled.UploadURL, taskID, agentID, data)
	cancelUpload.Header.Set("Content-Type", "image/png")
	cancelUploadRec := httptest.NewRecorder()
	testHandler.UploadAgentAttachmentSessionObject(cancelUploadRec, cancelUpload)
	if cancelUploadRec.Code != http.StatusConflict {
		t.Fatalf("cancelled session upload: status=%d body=%s", cancelUploadRec.Code, cancelUploadRec.Body.String())
	}
	cancelComplete := agentAttachmentUploadSessionRequest(t, http.MethodPost, "/api/agent/attachment-upload-sessions/"+cancelled.ID+"/complete", taskID, agentID, map[string]any{})
	cancelCompleteRec := httptest.NewRecorder()
	testHandler.CompleteAgentAttachmentUploadSession(cancelCompleteRec, cancelComplete)
	if cancelCompleteRec.Code != http.StatusConflict {
		t.Fatalf("cancelled session completion: status=%d body=%s", cancelCompleteRec.Code, cancelCompleteRec.Body.String())
	}
}

func TestAgentAttachmentUploadSessionIsIdempotentAndExpiresWithFakeTime(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	store := &mockStorage{}
	previousStorage := testHandler.Storage
	previousNow := testHandler.UploadSessionNow
	testHandler.Storage = store
	now := time.Date(2035, 5, 6, 7, 8, 9, 0, time.UTC)
	testHandler.UploadSessionNow = func() time.Time { return now }
	t.Cleanup(func() {
		testHandler.Storage = previousStorage
		testHandler.UploadSessionNow = previousNow
	})

	taskID, channelID := createChannelCompletionTask(t, "group")
	agentID := agentIDForTask(t, taskID)
	target := "#" + channelNameForTransportTest(t, channelID)
	data := []byte("data")
	requestID := uuid.NewString()
	first := createAgentAttachmentUploadSessionForTest(t, taskID, agentID, target, data, requestID)
	duplicateReq := agentAttachmentUploadSessionRequest(t, http.MethodPost, "/api/agent/attachment-upload-sessions", taskID, agentID, map[string]any{
		"target": target, "filename": "report.png", "content_type": "image/png", "size_bytes": len(data),
		"checksum_sha256": checksumSHA256ForTest(data), "client_request_id": requestID,
	})
	duplicateRec := httptest.NewRecorder()
	testHandler.CreateAgentAttachmentUploadSession(duplicateRec, duplicateReq)
	if duplicateRec.Code != http.StatusOK {
		t.Fatalf("idempotent creation: status=%d body=%s", duplicateRec.Code, duplicateRec.Body.String())
	}
	var duplicate AgentAttachmentUploadSessionResponse
	if err := json.Unmarshal(duplicateRec.Body.Bytes(), &duplicate); err != nil {
		t.Fatalf("decode idempotent session: %v", err)
	}
	if duplicate.ID != first.ID {
		t.Fatalf("idempotent session id=%s, want %s", duplicate.ID, first.ID)
	}

	upload := agentAttachmentUploadRawRequest(t, http.MethodPut, first.UploadURL, taskID, agentID, data)
	upload.Header.Set("Content-Type", "image/png")
	uploadRec := httptest.NewRecorder()
	testHandler.UploadAgentAttachmentSessionObject(uploadRec, upload)
	if uploadRec.Code != http.StatusNoContent {
		t.Fatalf("upload idempotent session: status=%d body=%s", uploadRec.Code, uploadRec.Body.String())
	}
	complete := agentAttachmentUploadSessionRequest(t, http.MethodPost, "/api/agent/attachment-upload-sessions/"+first.ID+"/complete", taskID, agentID, map[string]any{})
	completeRec := httptest.NewRecorder()
	testHandler.CompleteAgentAttachmentUploadSession(completeRec, complete)
	if completeRec.Code != http.StatusCreated {
		t.Fatalf("complete idempotent session: status=%d body=%s", completeRec.Code, completeRec.Body.String())
	}
	var attachment AttachmentResponse
	if err := json.Unmarshal(completeRec.Body.Bytes(), &attachment); err != nil {
		t.Fatalf("decode attachment: %v", err)
	}
	repeatComplete := agentAttachmentUploadSessionRequest(t, http.MethodPost, "/api/agent/attachment-upload-sessions/"+first.ID+"/complete", taskID, agentID, map[string]any{})
	repeatCompleteRec := httptest.NewRecorder()
	testHandler.CompleteAgentAttachmentUploadSession(repeatCompleteRec, repeatComplete)
	if repeatCompleteRec.Code != http.StatusOK {
		t.Fatalf("repeat completion: status=%d body=%s", repeatCompleteRec.Code, repeatCompleteRec.Body.String())
	}

	now = now.Add(agentAttachmentUploadSessionTTL + time.Second)
	expiredComplete := agentAttachmentUploadSessionRequest(t, http.MethodPost, "/api/agent/attachment-upload-sessions/"+first.ID+"/complete", taskID, agentID, map[string]any{})
	expiredCompleteRec := httptest.NewRecorder()
	testHandler.CompleteAgentAttachmentUploadSession(expiredCompleteRec, expiredComplete)
	if expiredCompleteRec.Code != http.StatusConflict {
		t.Fatalf("complete expired session: status=%d body=%s", expiredCompleteRec.Code, expiredCompleteRec.Body.String())
	}
	expiredSend := agentTransportSendForTest(t, taskID, agentID, map[string]any{
		"target": target, "content": "expired attachment", "attachment_ids": []string{attachment.ID},
		"client_message_id": "expired-agent-upload-session-" + uuid.NewString(), "bypass_freshness": true,
	})
	if expiredSend.Code != http.StatusBadRequest {
		t.Fatalf("send expired attachment: status=%d body=%s", expiredSend.Code, expiredSend.Body.String())
	}

	expired := createAgentAttachmentUploadSessionForTest(t, taskID, agentID, target, data, uuid.NewString())
	now = now.Add(agentAttachmentUploadSessionTTL + time.Second)
	expiredRetry := agentAttachmentUploadSessionRequest(t, http.MethodPost, "/api/agent/attachment-upload-sessions/"+expired.ID+"/retry", taskID, agentID, map[string]any{})
	expiredRetryRec := httptest.NewRecorder()
	testHandler.RetryAgentAttachmentUploadSession(expiredRetryRec, expiredRetry)
	if expiredRetryRec.Code != http.StatusConflict {
		t.Fatalf("retry expired session: status=%d body=%s", expiredRetryRec.Code, expiredRetryRec.Body.String())
	}
}

func createAgentAttachmentUploadSessionForTest(t *testing.T, taskID, agentID, target string, data []byte, clientRequestID string) AgentAttachmentUploadSessionResponse {
	t.Helper()
	create := agentAttachmentUploadSessionRequest(t, http.MethodPost, "/api/agent/attachment-upload-sessions", taskID, agentID, map[string]any{
		"target": target, "filename": "report.png", "content_type": "image/png", "size_bytes": len(data),
		"checksum_sha256": checksumSHA256ForTest(data), "client_request_id": clientRequestID,
	})
	createRec := httptest.NewRecorder()
	testHandler.CreateAgentAttachmentUploadSession(createRec, create)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create upload session: status=%d body=%s", createRec.Code, createRec.Body.String())
	}
	var session AgentAttachmentUploadSessionResponse
	if err := json.Unmarshal(createRec.Body.Bytes(), &session); err != nil {
		t.Fatalf("decode upload session: %v", err)
	}
	return session
}

func checksumSHA256ForTest(data []byte) string {
	digest := sha256.Sum256(data)
	return fmt.Sprintf("%x", digest)
}

func agentAttachmentUploadSessionRequest(t *testing.T, method, path, taskID, agentID string, body any) *http.Request {
	t.Helper()
	req := agentTransportRequest(t, method, path, taskID, agentID, body)
	return withAgentAttachmentSessionID(req)
}

func agentAttachmentUploadRawRequest(t *testing.T, method, path, _ string, agentID string, body []byte) *http.Request {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	req = withChatTestWorkspaceCtx(t, req)
	req.Header.Set("X-Actor-Source", "agent_credential")
	req.Header.Set("X-Agent-ID", agentID)
	return withAgentAttachmentSessionID(req)
}

func withAgentAttachmentSessionID(req *http.Request) *http.Request {
	const prefix = "/api/agent/attachment-upload-sessions/"
	path := req.URL.Path
	if len(path) < len(prefix) || path[:len(prefix)] != prefix {
		return req
	}
	rest := path[len(prefix):]
	for i, c := range rest {
		if c == '/' {
			rest = rest[:i]
			break
		}
	}
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("sessionId", rest)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}
