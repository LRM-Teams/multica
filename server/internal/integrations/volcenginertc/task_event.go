package volcenginertc

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// VoiceChatEventEnvelope is the server-level RTC callback envelope.
type VoiceChatEventEnvelope struct {
	EventType  string `json:"EventType"`
	EventData  string `json:"EventData"`
	EventTime  string `json:"EventTime"`
	EventID    string `json:"EventId"`
	AppID      string `json:"AppId"`
	Version    string `json:"Version"`
	Signature  string `json:"Signature"`
	BusinessID string `json:"BusinessId"`
	Nonce      string `json:"Nonce"`
}

type VoiceChatTaskEventType int64

const (
	VoiceChatTaskStateChanged VoiceChatTaskEventType = 0
	VoiceChatTaskError        VoiceChatTaskEventType = 1
)

type VoiceChatRunStage string

const (
	VoiceChatRunStagePreParamCheck  VoiceChatRunStage = "preParamCheck"
	VoiceChatRunStageTaskStart      VoiceChatRunStage = "taskStart"
	VoiceChatRunStageBeginAsking    VoiceChatRunStage = "beginAsking"
	VoiceChatRunStageASRFinish      VoiceChatRunStage = "asrFinish"
	VoiceChatRunStageLLMOutput      VoiceChatRunStage = "llmOutput"
	VoiceChatRunStageAnswerStart    VoiceChatRunStage = "answerStart"
	VoiceChatRunStageAnswerFinish   VoiceChatRunStage = "answerFinish"
	VoiceChatRunStageInterrupted    VoiceChatRunStage = "interrupted"
	VoiceChatRunStageReasoningStart VoiceChatRunStage = "reasoningStart"
	VoiceChatRunStageASR            VoiceChatRunStage = "asr"
	VoiceChatRunStageLLM            VoiceChatRunStage = "llm"
	VoiceChatRunStageTTS            VoiceChatRunStage = "tts"
	VoiceChatRunStageTaskStop       VoiceChatRunStage = "taskStop"
	VoiceChatRunStageTaskUsage      VoiceChatRunStage = "taskUsage"
)

type VoiceChatTaskErrorInfo struct {
	ErrorCode int64  `json:"Errorcode"`
	Reason    string `json:"Reason"`
}

type VoiceChatTaskEvent struct {
	AppID      string                  `json:"AppId"`
	BusinessID string                  `json:"BusinessId"`
	RoomID     string                  `json:"RoomId"`
	TaskID     string                  `json:"TaskId"`
	UserID     string                  `json:"UserID"`
	RoundID    int64                   `json:"RoundID"`
	EventTime  int64                   `json:"EventTime"`
	EventType  VoiceChatTaskEventType  `json:"EventType"`
	RunStage   VoiceChatRunStage       `json:"RunStage"`
	ErrorInfo  *VoiceChatTaskErrorInfo `json:"ErrorInfo"`
	ExtraInfo  string                  `json:"ExtraInfo"`
}

// VerifySignature implements the signature algorithm documented for
// Volcengine RTC server callbacks.
func (envelope VoiceChatEventEnvelope) VerifySignature(secret string) bool {
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
	expected := make([]byte, hex.EncodedLen(len(sum)))
	hex.Encode(expected, sum[:])
	return subtle.ConstantTimeCompare(
		expected,
		[]byte(strings.ToLower(envelope.Signature)),
	) == 1
}

func DecodeVoiceChatTaskEvent(
	envelope VoiceChatEventEnvelope,
) (VoiceChatTaskEvent, error) {
	if strings.TrimSpace(envelope.EventType) != "VoiceChat" {
		return VoiceChatTaskEvent{}, errors.New(
			"Volcengine RTC callback EventType must be VoiceChat",
		)
	}
	if strings.TrimSpace(envelope.EventData) == "" {
		return VoiceChatTaskEvent{}, errors.New(
			"Volcengine RTC VoiceChat EventData is required",
		)
	}
	var event VoiceChatTaskEvent
	if err := json.Unmarshal([]byte(envelope.EventData), &event); err != nil {
		return VoiceChatTaskEvent{}, fmt.Errorf(
			"decode Volcengine RTC VoiceChat EventData: %w",
			err,
		)
	}
	if strings.TrimSpace(event.AppID) == "" {
		return VoiceChatTaskEvent{}, errors.New(
			"Volcengine RTC VoiceChat AppId is required",
		)
	}
	if strings.TrimSpace(event.RoomID) == "" {
		return VoiceChatTaskEvent{}, errors.New(
			"Volcengine RTC VoiceChat RoomId is required",
		)
	}
	if strings.TrimSpace(event.TaskID) == "" {
		return VoiceChatTaskEvent{}, errors.New(
			"Volcengine RTC VoiceChat TaskId is required",
		)
	}
	if event.EventTime <= 0 {
		return VoiceChatTaskEvent{}, errors.New(
			"Volcengine RTC VoiceChat EventTime must be positive",
		)
	}
	if event.EventType != VoiceChatTaskStateChanged &&
		event.EventType != VoiceChatTaskError {
		return VoiceChatTaskEvent{}, errors.New(
			"Volcengine RTC VoiceChat EventType is unsupported",
		)
	}
	if strings.TrimSpace(string(event.RunStage)) == "" {
		return VoiceChatTaskEvent{}, errors.New(
			"Volcengine RTC VoiceChat RunStage is required",
		)
	}
	if event.EventType == VoiceChatTaskError {
		if event.ErrorInfo == nil || event.ErrorInfo.ErrorCode <= 0 {
			return VoiceChatTaskEvent{}, errors.New(
				"Volcengine RTC VoiceChat ErrorInfo is required",
			)
		}
		if strings.TrimSpace(event.ErrorInfo.Reason) == "" {
			return VoiceChatTaskEvent{}, errors.New(
				"Volcengine RTC VoiceChat ErrorInfo.Reason is required",
			)
		}
	}
	return event, nil
}
