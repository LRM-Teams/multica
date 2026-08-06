package handler

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
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

func TestVoiceCallCallbackAuthenticatesAndDispatchesFunctionToolCall(t *testing.T) {
	callbackProcessor := &fakeVoiceCallCallbackProcessor{}
	functionProcessor := &fakeVoiceCallFunctionProcessor{}
	handler := &Handler{
		VoiceCallCallbackProcessor: callbackProcessor,
		VoiceCallFunctionProcessor: functionProcessor,
		VoiceCallCallbackSignature: "expected-signature",
	}
	body := `{"message":"` + voiceCallCallbackPayloadForTest(
		"tool",
		`{"subscriber_user_id":"voice-member-nonce-1","tool_calls":[{"function":{"arguments":"{\"request\":\"创建 issue 修复登录失败。\"}","name":"delegate_work_to_multica_agent"},"id":"call-1","type":"function"}]}`,
	) + `","binary":true,"signature":"expected-signature"}`
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/voice-calls/callback?voice_call_room_id=voice-call-nonce-1",
		strings.NewReader(body),
	)
	response := httptest.NewRecorder()

	handler.HandleVoiceCallCallback(response, request)

	if response.Code != http.StatusOK || response.Body.String() != "ok" {
		t.Fatalf("response = %d %q", response.Code, response.Body.String())
	}
	if functionProcessor.calls != 1 ||
		functionProcessor.roomID != "voice-call-nonce-1" ||
		len(functionProcessor.message.ToolCalls) != 1 ||
		functionProcessor.message.ToolCalls[0].ID != "call-1" {
		t.Fatalf("function processor = %#v", functionProcessor)
	}
	if callbackProcessor.statusCalls != 0 ||
		callbackProcessor.subtitleCalls != 0 ||
		callbackProcessor.taskEventCalls != 0 {
		t.Fatalf("lifecycle processor = %#v", callbackProcessor)
	}
}

func TestVoiceCallCallbackRejectsFunctionCallWithoutScopedRoom(t *testing.T) {
	callbackProcessor := &fakeVoiceCallCallbackProcessor{}
	functionProcessor := &fakeVoiceCallFunctionProcessor{}
	handler := &Handler{
		VoiceCallCallbackProcessor: callbackProcessor,
		VoiceCallFunctionProcessor: functionProcessor,
		VoiceCallCallbackSignature: "expected-signature",
	}
	body := `{"message":"` + voiceCallCallbackPayloadForTest(
		"tool",
		`{"subscriber_user_id":"voice-member-nonce-1","tool_calls":[{"function":{"arguments":"{\"request\":\"创建 issue。\"}","name":"delegate_work_to_multica_agent"},"id":"call-1","type":"function"}]}`,
	) + `","binary":true,"signature":"expected-signature"}`
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/voice-calls/callback",
		strings.NewReader(body),
	)
	response := httptest.NewRecorder()

	handler.HandleVoiceCallCallback(response, request)

	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", response.Code)
	}
	if functionProcessor.calls != 0 {
		t.Fatalf("unscoped function processor called %d times", functionProcessor.calls)
	}
}

func TestVoiceCallCallbackAuthenticatesAndProcessesVoiceChatTaskStart(t *testing.T) {
	processor := &fakeVoiceCallCallbackProcessor{
		taskEventResult: voicecall.Session{
			ID:          testVoiceAPICallID,
			WorkspaceID: testVoiceAPIWorkspaceID,
			UserID:      testVoiceAPIUserID,
		},
		taskEventChanged: true,
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
	body := voiceCallTaskEventBodyForTest(
		t,
		"expected-signature",
		`{"AppId":"rtc-app","BusinessId":"","RoomId":"voice-call-abc","TaskId":"voice-task-abc","UserID":"voice-agent-abc","RoundID":0,"EventTime":1785391200000,"EventType":0,"RunStage":"taskStart"}`,
	)
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
	if processor.taskEventCalls != 1 ||
		processor.taskEvent.TaskID != "voice-task-abc" ||
		processor.taskEvent.RunStage !=
			volcenginertc.VoiceChatRunStageTaskStart {
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

func TestVoiceCallCallbackReturnsOKForConnectivityCheck(t *testing.T) {
	handler := &Handler{
		VoiceCallCallbackProcessor: &fakeVoiceCallCallbackProcessor{},
		VoiceCallCallbackSignature: "expected-signature",
	}
	request := httptest.NewRequest(
		http.MethodGet,
		"/api/voice-calls/callback",
		nil,
	)
	response := httptest.NewRecorder()

	handler.HandleVoiceCallCallback(response, request)

	if response.Code != http.StatusOK || response.Body.String() != "ok" {
		t.Fatalf("response = %d %q", response.Code, response.Body.String())
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

func TestVoiceCallCallbackRejectsWrongVoiceChatSignature(t *testing.T) {
	processor := &fakeVoiceCallCallbackProcessor{}
	handler := &Handler{
		VoiceCallCallbackProcessor: processor,
		VoiceCallCallbackSignature: "expected-signature",
	}
	body := voiceCallTaskEventBodyForTest(
		t,
		"wrong-signature",
		`{"AppId":"rtc-app","RoomId":"voice-call-abc","TaskId":"voice-task-abc","EventTime":1785391200000,"EventType":0,"RunStage":"taskStart"}`,
	)
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/voice-calls/callback",
		strings.NewReader(body),
	)
	response := httptest.NewRecorder()

	handler.HandleVoiceCallCallback(response, request)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", response.Code)
	}
	if processor.taskEventCalls != 0 {
		t.Fatalf("task event processor called %d times", processor.taskEventCalls)
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

type fakeVoiceCallFunctionProcessor struct {
	calls   int
	roomID  string
	message volcenginertc.FunctionCallMessage
	err     error
}

func (processor *fakeVoiceCallFunctionProcessor) HandleFunctionCalls(
	_ context.Context,
	roomID string,
	message volcenginertc.FunctionCallMessage,
) error {
	processor.calls++
	processor.roomID = roomID
	processor.message = message
	return processor.err
}

type fakeVoiceCallCallbackProcessor struct {
	statusCalls      int
	subtitleCalls    int
	taskEventCalls   int
	status           volcenginertc.ConversationStatus
	subtitle         volcenginertc.ConversationSubtitle
	taskEvent        volcenginertc.VoiceChatTaskEvent
	statusResult     voicecall.Session
	taskEventResult  voicecall.Session
	taskEventChanged bool
	err              error
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

func (processor *fakeVoiceCallCallbackProcessor) HandleVoiceChatTaskEvent(
	_ context.Context,
	event volcenginertc.VoiceChatTaskEvent,
) (voicecall.Session, bool, error) {
	processor.taskEventCalls++
	processor.taskEvent = event
	return processor.taskEventResult, processor.taskEventChanged, processor.err
}

func voiceCallCallbackPayloadForTest(magic string, payload string) string {
	data := make([]byte, 8+len(payload))
	copy(data[:4], magic)
	binary.BigEndian.PutUint32(data[4:8], uint32(len(payload)))
	copy(data[8:], payload)
	return base64.StdEncoding.EncodeToString(data)
}

func voiceCallTaskEventBodyForTest(
	t *testing.T,
	secret string,
	eventData string,
) string {
	t.Helper()
	envelope := volcenginertc.VoiceChatEventEnvelope{
		EventType: "VoiceChat",
		EventData: eventData,
		EventTime: "2026-07-30T14:00:00+08:00",
		EventID:   "event-1",
		AppID:     "rtc-app",
		Version:   "2020-12-01",
		Nonce:     "aB12",
	}
	values := []string{
		envelope.EventType,
		envelope.EventData,
		envelope.EventTime,
		envelope.EventID,
		envelope.AppID,
		envelope.Version,
		envelope.Nonce,
		secret,
	}
	sort.Strings(values)
	sum := sha256.Sum256([]byte(strings.Join(values, "")))
	envelope.Signature = hex.EncodeToString(sum[:])
	body, err := json.Marshal(envelope)
	if err != nil {
		t.Fatalf("marshal task event: %v", err)
	}
	return string(body)
}
