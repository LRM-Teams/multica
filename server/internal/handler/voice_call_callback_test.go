package handler

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/integrations/volcenginertc"
)

func TestVoiceCallCallbackAuthenticatesAndProcessesStatusWithoutContentType(t *testing.T) {
	processor := &fakeVoiceCallCallbackProcessor{}
	handler := &Handler{
		VoiceCallCallbackProcessor: processor,
		VoiceCallCallbackSignature: "expected-signature",
	}
	body := `{"message":"` + voiceCallCallbackPayloadForTest(
		`{"TaskId":"task-1","UserID":"agent-1","RoundID":2,"EventTime":1765769502847,"Stage":{"Code":1,"Description":"listening"}}`,
	) + `","binary":true,"signature":"expected-signature"}`
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/voice-calls/callback",
		strings.NewReader(body),
	)
	response := httptest.NewRecorder()

	handler.HandleVoiceCallCallback(response, request)

	if response.Code != http.StatusOK || response.Body.String() != "ok" {
		t.Fatalf("response = %d %q", response.Code, response.Body.String())
	}
	if processor.calls != 1 ||
		processor.status.TaskID != "task-1" ||
		processor.status.Stage.Code != volcenginertc.ConversationStageListening {
		t.Fatalf("processor = %#v", processor)
	}
}

func TestVoiceCallCallbackRejectsWrongSignatureBeforeDecoding(t *testing.T) {
	processor := &fakeVoiceCallCallbackProcessor{}
	handler := &Handler{
		VoiceCallCallbackProcessor: processor,
		VoiceCallCallbackSignature: "expected-signature",
	}
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/voice-calls/callback",
		strings.NewReader(
			`{"message":"not-base64","signature":"wrong-signature"}`,
		),
	)
	response := httptest.NewRecorder()

	handler.HandleVoiceCallCallback(response, request)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", response.Code)
	}
	if processor.calls != 0 {
		t.Fatalf("processor called %d times", processor.calls)
	}
}

func TestVoiceCallCallbackRejectsMalformedAndOversizedBodies(t *testing.T) {
	tests := []struct {
		name string
		body []byte
		want int
	}{
		{
			name: "malformed JSON",
			body: []byte(`{"message":`),
			want: http.StatusBadRequest,
		},
		{
			name: "trailing JSON",
			body: []byte(`{"message":"x","signature":"expected-signature"} {}`),
			want: http.StatusBadRequest,
		},
		{
			name: "invalid TLV",
			body: []byte(`{"message":"bm90LXRsdg==","signature":"expected-signature"}`),
			want: http.StatusBadRequest,
		},
		{
			name: "oversized",
			body: bytes.Repeat([]byte("x"), maxVoiceCallCallbackRequestBytes+1),
			want: http.StatusRequestEntityTooLarge,
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			handler := &Handler{
				VoiceCallCallbackProcessor: &fakeVoiceCallCallbackProcessor{},
				VoiceCallCallbackSignature: "expected-signature",
			}
			request := httptest.NewRequest(
				http.MethodPost,
				"/api/voice-calls/callback",
				bytes.NewReader(testCase.body),
			)
			response := httptest.NewRecorder()

			handler.HandleVoiceCallCallback(response, request)

			if response.Code != testCase.want {
				t.Fatalf("status = %d, want %d", response.Code, testCase.want)
			}
		})
	}
}

func TestVoiceCallCallbackReturnsUnavailableUntilConfigured(t *testing.T) {
	handler := &Handler{}
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/voice-calls/callback",
		strings.NewReader(`{}`),
	)
	response := httptest.NewRecorder()

	handler.HandleVoiceCallCallback(response, request)

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", response.Code)
	}
}

type fakeVoiceCallCallbackProcessor struct {
	calls  int
	status volcenginertc.ConversationStatus
	err    error
}

func (processor *fakeVoiceCallCallbackProcessor) HandleConversationStatus(
	_ context.Context,
	status volcenginertc.ConversationStatus,
) error {
	processor.calls++
	processor.status = status
	return processor.err
}

func voiceCallCallbackPayloadForTest(payload string) string {
	data := make([]byte, 8+len(payload))
	copy(data[:4], "conv")
	binary.BigEndian.PutUint32(data[4:8], uint32(len(payload)))
	copy(data[8:], payload)
	return base64.StdEncoding.EncodeToString(data)
}
