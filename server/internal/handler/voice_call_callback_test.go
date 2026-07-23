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

	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/integrations/volcenginertc"
	"github.com/multica-ai/multica/server/internal/service/voicecall"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestVoiceCallCallbackAuthenticatesAndProcessesStatusWithoutContentType(t *testing.T) {
	processor := &fakeVoiceCallCallbackProcessor{
		statusResult: voicecall.Session{
			ID:          testVoiceAPICallID,
			WorkspaceID: testVoiceAPIWorkspaceID,
			UserID:      testVoiceAPIUserID,
		},
	}
	bus := events.New()
	var published events.Event
	bus.Subscribe(protocol.EventVoiceCallUpdated, func(event events.Event) {
		published = event
	})
	handler := &Handler{
		VoiceCallCallbackProcessor: processor,
		VoiceCallCallbackSignature: "expected-signature",
		Bus:                        bus,
	}
	body := `{"message":"` + voiceCallCallbackPayloadForTest(
		"conv",
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
	if processor.statusCalls != 1 ||
		processor.status.TaskID != "task-1" ||
		processor.status.Stage.Code != volcenginertc.ConversationStageListening {
		t.Fatalf("processor = %#v", processor)
	}
	assertVoiceCallUpdatedEvent(
		t,
		published,
		testVoiceAPIWorkspaceID,
		testVoiceAPICallID,
		testVoiceAPIUserID,
	)
}

func TestVoiceCallCallbackAuthenticatesAndProcessesSubtitleWithoutContentType(t *testing.T) {
	processor := &fakeVoiceCallCallbackProcessor{}
	handler := &Handler{
		VoiceCallCallbackProcessor: processor,
		VoiceCallCallbackSignature: "expected-signature",
	}
	body := `{"message":"` + voiceCallCallbackPayloadForTest(
		"subv",
		`{"type":"subtitle","data":[{"definite":true,"paragraph":true,"language":"zh-CN","sequence":5,"text":"请检查群聊。","userId":"voice-member-call-1","roundId":2,"firstCharPos":0,"lastCharPos":5}]}`,
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
	if processor.subtitleCalls != 1 ||
		processor.subtitle.Type != "subtitle" ||
		len(processor.subtitle.Data) != 1 ||
		processor.subtitle.Data[0].Text != "请检查群聊。" {
		t.Fatalf("processor = %#v", processor)
	}
	if processor.statusCalls != 0 {
		t.Fatalf("status processor called %d times", processor.statusCalls)
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
	if processor.statusCalls != 0 || processor.subtitleCalls != 0 {
		t.Fatalf("processor = %#v", processor)
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
	statusCalls   int
	subtitleCalls int
	status        volcenginertc.ConversationStatus
	subtitle      volcenginertc.ConversationSubtitle
	statusResult  voicecall.Session
	err           error
}

func (processor *fakeVoiceCallCallbackProcessor) HandleConversationStatus(
	_ context.Context,
	status volcenginertc.ConversationStatus,
) (voicecall.Session, error) {
	processor.statusCalls++
	processor.status = status
	return processor.statusResult, processor.err
}

func (processor *fakeVoiceCallCallbackProcessor) HandleConversationSubtitle(
	_ context.Context,
	subtitle volcenginertc.ConversationSubtitle,
) error {
	processor.subtitleCalls++
	processor.subtitle = subtitle
	return processor.err
}

func voiceCallCallbackPayloadForTest(magic string, payload string) string {
	data := make([]byte, 8+len(payload))
	copy(data[:4], magic)
	binary.BigEndian.PutUint32(data[4:8], uint32(len(payload)))
	copy(data[8:], payload)
	return base64.StdEncoding.EncodeToString(data)
}
