package volcenginertc

import (
	"encoding/base64"
	"encoding/binary"
	"strings"
	"testing"
)

func TestDecodeConversationStatusCallbackUsesDocumentedTLV(t *testing.T) {
	encoded := encodeCallbackTLVForTest(
		"conv",
		`{"TaskId":"task-1","UserID":"agent-1","RoundID":3,"EventTime":1765769502847,"Stage":{"Code":5,"Description":"answerFinish"}}`,
	)

	status, err := DecodeConversationStatusCallback(encoded)
	if err != nil {
		t.Fatalf("decode conversation callback: %v", err)
	}
	if status.TaskID != "task-1" ||
		status.UserID != "agent-1" ||
		status.RoundID != 3 ||
		status.EventTime != 1765769502847 ||
		status.Stage.Code != ConversationStageAnswerFinished ||
		status.Stage.Description != "answerFinish" {
		t.Fatalf("decoded status = %#v", status)
	}
}

func TestDecodeConversationStatusCallbackValidatesEnvelopeAndStatus(t *testing.T) {
	tests := []struct {
		name    string
		encoded string
		want    string
	}{
		{
			name:    "invalid base64",
			encoded: "not-base64",
			want:    "base64",
		},
		{
			name:    "short TLV",
			encoded: base64.StdEncoding.EncodeToString([]byte("conv")),
			want:    "header",
		},
		{
			name:    "wrong magic",
			encoded: encodeCallbackTLVForTest("ctrl", `{}`),
			want:    "magic",
		},
		{
			name: "declared length mismatch",
			encoded: func() string {
				data, _ := base64.StdEncoding.DecodeString(
					encodeCallbackTLVForTest("conv", `{}`),
				)
				binary.BigEndian.PutUint32(data[4:8], 99)
				return base64.StdEncoding.EncodeToString(data)
			}(),
			want: "length",
		},
		{
			name:    "blank task",
			encoded: encodeCallbackTLVForTest("conv", `{"TaskId":"","UserID":"agent-1","RoundID":0,"EventTime":1,"Stage":{"Code":1,"Description":"listening"}}`),
			want:    "TaskId",
		},
		{
			name:    "invalid stage",
			encoded: encodeCallbackTLVForTest("conv", `{"TaskId":"task-1","UserID":"agent-1","RoundID":0,"EventTime":1,"Stage":{"Code":9,"Description":"future"}}`),
			want:    "Stage.Code",
		},
		{
			name:    "error without details",
			encoded: encodeCallbackTLVForTest("conv", `{"TaskId":"task-1","UserID":"agent-1","RoundID":0,"EventTime":1,"Stage":{"Code":0,"Description":"error"}}`),
			want:    "ErrorInfo",
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := DecodeConversationStatusCallback(testCase.encoded)
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("error = %v, want %q", err, testCase.want)
			}
		})
	}
}

func TestDecodeConversationStatusCallbackAcceptsProviderErrorDetails(t *testing.T) {
	encoded := encodeCallbackTLVForTest(
		"conv",
		`{"TaskId":"task-1","UserID":"agent-1","RoundID":0,"EventTime":1765769502847,"Stage":{"Code":0,"Description":"errorOccurred"},"ErrorInfo":{"ErrorCode":1005002,"Reason":"TTS request failed"}}`,
	)
	status, err := DecodeConversationStatusCallback(encoded)
	if err != nil {
		t.Fatalf("decode error callback: %v", err)
	}
	if status.ErrorInfo == nil ||
		status.ErrorInfo.ErrorCode != 1005002 ||
		status.ErrorInfo.Reason != "TTS request failed" {
		t.Fatalf("error details = %#v", status.ErrorInfo)
	}
}

func TestDecodeServerCallbackUsesDocumentedSubtitleTLV(t *testing.T) {
	encoded := encodeCallbackTLVForTest(
		"subv",
		`{"type":"subtitle","data":[{"text":"你好。","language":"zh-CN","userId":"voice-member-nonce-1","sequence":2,"definite":true,"paragraph":true,"roundId":3,"firstCharPos":4,"lastCharPos":6}]}`,
	)

	callback, err := DecodeServerCallback(encoded)
	if err != nil {
		t.Fatalf("decode server callback: %v", err)
	}
	if callback.Kind != ServerCallbackSubtitle ||
		callback.ConversationStatus != nil ||
		callback.Subtitle == nil ||
		len(callback.Subtitle.Data) != 1 {
		t.Fatalf("callback = %#v", callback)
	}
	segment := callback.Subtitle.Data[0]
	if callback.Subtitle.Type != "subtitle" ||
		segment.Text != "你好。" ||
		segment.Language != "zh-CN" ||
		segment.UserID != "voice-member-nonce-1" ||
		segment.Sequence != 2 ||
		!segment.Definite ||
		!segment.Paragraph ||
		segment.RoundID != 3 ||
		segment.FirstCharPos != 4 ||
		segment.LastCharPos != 6 {
		t.Fatalf("subtitle = %#v", callback.Subtitle)
	}
}

func TestDecodeServerCallbackUsesDocumentedFunctionToolTLV(t *testing.T) {
	encoded := encodeCallbackTLVForTest(
		"tool",
		`{"subscriber_user_id":"voice-member-nonce-1","tool_calls":[{"function":{"arguments":"{\"request\":\"创建一个 issue，修复登录页报错。\"}","name":"delegate_work_to_multica_agent"},"id":"call_py400kek0e3pczrqdxgnb3lo","type":"function"}]}`,
	)

	callback, err := DecodeServerCallback(encoded)
	if err != nil {
		t.Fatalf("decode function callback: %v", err)
	}
	if callback.Kind != ServerCallbackFunctionCall ||
		callback.FunctionCall == nil ||
		callback.ConversationStatus != nil ||
		callback.Subtitle != nil {
		t.Fatalf("callback = %#v", callback)
	}
	message := callback.FunctionCall
	if message.SubscriberUserID != "voice-member-nonce-1" ||
		len(message.ToolCalls) != 1 {
		t.Fatalf("function message = %#v", message)
	}
	tool := message.ToolCalls[0]
	if tool.ID != "call_py400kek0e3pczrqdxgnb3lo" ||
		tool.Type != "function" ||
		tool.Function.Name != "delegate_work_to_multica_agent" ||
		tool.Function.Arguments != `{"request":"创建一个 issue，修复登录页报错。"}` {
		t.Fatalf("tool call = %#v", tool)
	}
}

func TestDecodeServerCallbackRejectsInvalidFunctionTools(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		want    string
	}{
		{
			name:    "empty tools",
			payload: `{"tool_calls":[]}`,
			want:    "tool_calls",
		},
		{
			name:    "blank tool ID",
			payload: `{"tool_calls":[{"id":" ","type":"function","function":{"name":"delegate_work_to_multica_agent","arguments":"{}"}}]}`,
			want:    ".id",
		},
		{
			name:    "unsupported type",
			payload: `{"tool_calls":[{"id":"call-1","type":"plugin","function":{"name":"delegate_work_to_multica_agent","arguments":"{}"}}]}`,
			want:    ".type",
		},
		{
			name:    "non-object arguments",
			payload: `{"tool_calls":[{"id":"call-1","type":"function","function":{"name":"delegate_work_to_multica_agent","arguments":"[]"}}]}`,
			want:    "arguments",
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := DecodeServerCallback(
				encodeCallbackTLVForTest("tool", testCase.payload),
			)
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("error = %v, want %q", err, testCase.want)
			}
		})
	}
}

func TestDecodeConversationSubtitleCallbackAcceptsEmptyFinalMarker(t *testing.T) {
	encoded := encodeCallbackTLVForTest(
		"subv",
		`{"type":"subtitle","data":[{"text":"","language":"","userId":"voice-agent-nonce-1","sequence":0,"definite":true,"paragraph":true,"roundId":0}]}`,
	)

	subtitle, err := DecodeConversationSubtitleCallback(encoded)
	if err != nil {
		t.Fatalf("decode empty final marker: %v", err)
	}
	if len(subtitle.Data) != 1 || subtitle.Data[0].Text != "" {
		t.Fatalf("subtitle = %#v", subtitle)
	}
}

func TestDecodeConversationSubtitleCallbackValidatesPayload(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		want    string
	}{
		{
			name:    "wrong type",
			payload: `{"type":"function","data":[{"userId":"voice-agent-1"}]}`,
			want:    "type",
		},
		{
			name:    "empty data",
			payload: `{"type":"subtitle","data":[]}`,
			want:    "data",
		},
		{
			name:    "blank user",
			payload: `{"type":"subtitle","data":[{"userId":" ","sequence":0,"roundId":0}]}`,
			want:    "userId",
		},
		{
			name:    "negative sequence",
			payload: `{"type":"subtitle","data":[{"userId":"voice-agent-1","sequence":-1,"roundId":0}]}`,
			want:    "sequence",
		},
		{
			name:    "negative round",
			payload: `{"type":"subtitle","data":[{"userId":"voice-agent-1","sequence":0,"roundId":-1}]}`,
			want:    "roundId",
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := DecodeConversationSubtitleCallback(
				encodeCallbackTLVForTest("subv", testCase.payload),
			)
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("error = %v, want %q", err, testCase.want)
			}
		})
	}
}

func TestDecodeServerCallbackRejectsUnsupportedMagic(t *testing.T) {
	_, err := DecodeServerCallback(encodeCallbackTLVForTest("ctrl", `{}`))
	if err == nil || !strings.Contains(err.Error(), "magic") {
		t.Fatalf("error = %v, want unsupported magic", err)
	}
}

func encodeCallbackTLVForTest(magic, payload string) string {
	data := make([]byte, 8+len(payload))
	copy(data[:4], magic)
	binary.BigEndian.PutUint32(data[4:8], uint32(len(payload)))
	copy(data[8:], payload)
	return base64.StdEncoding.EncodeToString(data)
}
