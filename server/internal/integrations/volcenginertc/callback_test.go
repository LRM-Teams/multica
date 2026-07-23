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

func encodeCallbackTLVForTest(magic, payload string) string {
	data := make([]byte, 8+len(payload))
	copy(data[:4], magic)
	binary.BigEndian.PutUint32(data[4:8], uint32(len(payload)))
	copy(data[8:], payload)
	return base64.StdEncoding.EncodeToString(data)
}
