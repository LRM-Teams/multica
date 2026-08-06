package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

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

func agentAttachmentUploadSessionRequest(t *testing.T, method, path, taskID, agentID string, body any) *http.Request {
	t.Helper()
	req := agentTransportRequest(t, method, path, taskID, agentID, body)
	return withAgentAttachmentSessionID(req)
}

func agentAttachmentUploadRawRequest(t *testing.T, method, path, taskID, agentID string, body []byte) *http.Request {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	req = withChatTestWorkspaceCtx(t, req)
	req.Header.Set("X-Actor-Source", "task_token")
	req.Header.Set("X-Agent-ID", agentID)
	req.Header.Set("X-Task-ID", taskID)
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
