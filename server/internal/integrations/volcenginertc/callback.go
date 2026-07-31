package volcenginertc

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const (
	conversationStatusMagic   = "conv"
	conversationSubtitleMagic = "subv"
	functionToolMagic         = "tool"
	callbackHeaderBytes       = 8
	maxCallbackEncodedBytes   = 128 << 10
	maxCallbackPayloadBytes   = 64 << 10
	maxFunctionToolCalls      = 8
)

type ServerCallbackKind string

const (
	ServerCallbackConversationStatus ServerCallbackKind = "conversation_status"
	ServerCallbackSubtitle           ServerCallbackKind = "subtitle"
	ServerCallbackFunctionCall       ServerCallbackKind = "function_call"
)

type ServerCallback struct {
	Kind               ServerCallbackKind
	ConversationStatus *ConversationStatus
	Subtitle           *ConversationSubtitle
	FunctionCall       *FunctionCallMessage
}

type ConversationStageCode int

const (
	ConversationStageError ConversationStageCode = iota
	ConversationStageListening
	ConversationStageThinking
	ConversationStageAnswering
	ConversationStageInterrupted
	ConversationStageAnswerFinished
)

type ConversationStatus struct {
	TaskID    string             `json:"TaskId"`
	UserID    string             `json:"UserID"`
	RoundID   int64              `json:"RoundID"`
	EventTime int64              `json:"EventTime"`
	Stage     ConversationStage  `json:"Stage"`
	ErrorInfo *ConversationError `json:"ErrorInfo,omitempty"`
}

type ConversationStage struct {
	Code        ConversationStageCode `json:"Code"`
	Description string                `json:"Description"`
}

type ConversationError struct {
	ErrorCode int64  `json:"ErrorCode"`
	Reason    string `json:"Reason"`
}

type ConversationSubtitle struct {
	Type string                        `json:"type"`
	Data []ConversationSubtitleSegment `json:"data"`
}

type ConversationSubtitleSegment struct {
	Definite     bool   `json:"definite"`
	Paragraph    bool   `json:"paragraph"`
	Language     string `json:"language"`
	Sequence     int64  `json:"sequence"`
	Text         string `json:"text"`
	UserID       string `json:"userId"`
	RoundID      int64  `json:"roundId"`
	FirstCharPos int64  `json:"firstCharPos"`
	LastCharPos  int64  `json:"lastCharPos"`
}

type FunctionCallMessage struct {
	SubscriberUserID string             `json:"subscriber_user_id"`
	ToolCalls        []FunctionToolCall `json:"tool_calls"`
}

type FunctionToolCall struct {
	ID       string                   `json:"id"`
	Type     string                   `json:"type"`
	Function FunctionToolCallFunction `json:"function"`
}

type FunctionToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

func DecodeServerCallback(encoded string) (ServerCallback, error) {
	magic, payload, err := decodeCallbackFrame(encoded)
	if err != nil {
		return ServerCallback{}, err
	}
	switch magic {
	case conversationStatusMagic:
		status, err := decodeConversationStatus(payload)
		if err != nil {
			return ServerCallback{}, err
		}
		return ServerCallback{
			Kind:               ServerCallbackConversationStatus,
			ConversationStatus: &status,
		}, nil
	case conversationSubtitleMagic:
		subtitle, err := decodeConversationSubtitle(payload)
		if err != nil {
			return ServerCallback{}, err
		}
		return ServerCallback{
			Kind:     ServerCallbackSubtitle,
			Subtitle: &subtitle,
		}, nil
	case functionToolMagic:
		functionCall, err := decodeFunctionCallMessage(payload)
		if err != nil {
			return ServerCallback{}, err
		}
		return ServerCallback{
			Kind:         ServerCallbackFunctionCall,
			FunctionCall: &functionCall,
		}, nil
	default:
		return ServerCallback{}, fmt.Errorf(
			"Volcengine RTC callback magic %q is unsupported",
			magic,
		)
	}
}

func decodeFunctionCallMessage(payload []byte) (FunctionCallMessage, error) {
	var message FunctionCallMessage
	if err := json.Unmarshal(payload, &message); err != nil {
		return FunctionCallMessage{}, fmt.Errorf(
			"decode Volcengine RTC function call: %w",
			err,
		)
	}
	message.SubscriberUserID = strings.TrimSpace(message.SubscriberUserID)
	for index := range message.ToolCalls {
		tool := &message.ToolCalls[index]
		tool.ID = strings.TrimSpace(tool.ID)
		tool.Type = strings.TrimSpace(tool.Type)
		tool.Function.Name = strings.TrimSpace(tool.Function.Name)
		tool.Function.Arguments = strings.TrimSpace(tool.Function.Arguments)
	}
	if err := validateFunctionCallMessage(message); err != nil {
		return FunctionCallMessage{}, err
	}
	return message, nil
}

func DecodeConversationStatusCallback(encoded string) (ConversationStatus, error) {
	callback, err := DecodeServerCallback(encoded)
	if err != nil {
		return ConversationStatus{}, err
	}
	if callback.Kind != ServerCallbackConversationStatus ||
		callback.ConversationStatus == nil {
		return ConversationStatus{}, errors.New("Volcengine RTC callback magic is not conv")
	}
	return *callback.ConversationStatus, nil
}

func DecodeConversationSubtitleCallback(encoded string) (ConversationSubtitle, error) {
	callback, err := DecodeServerCallback(encoded)
	if err != nil {
		return ConversationSubtitle{}, err
	}
	if callback.Kind != ServerCallbackSubtitle || callback.Subtitle == nil {
		return ConversationSubtitle{}, errors.New("Volcengine RTC callback magic is not subv")
	}
	return *callback.Subtitle, nil
}

func decodeCallbackFrame(encoded string) (string, []byte, error) {
	encoded = strings.TrimSpace(encoded)
	if encoded == "" {
		return "", nil, errors.New("Volcengine RTC callback message is required")
	}
	if len(encoded) > maxCallbackEncodedBytes {
		return "", nil, fmt.Errorf(
			"Volcengine RTC callback base64 exceeds %d bytes",
			maxCallbackEncodedBytes,
		)
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", nil, fmt.Errorf("decode Volcengine RTC callback base64: %w", err)
	}
	if len(data) < callbackHeaderBytes {
		return "", nil, errors.New("Volcengine RTC callback is shorter than its TLV header")
	}
	payloadLength := binary.BigEndian.Uint32(data[4:8])
	if payloadLength > maxCallbackPayloadBytes {
		return "", nil, fmt.Errorf(
			"Volcengine RTC callback payload exceeds %d bytes",
			maxCallbackPayloadBytes,
		)
	}
	if uint64(payloadLength)+callbackHeaderBytes != uint64(len(data)) {
		return "", nil, errors.New("Volcengine RTC callback TLV length does not match payload")
	}
	return string(data[:4]), data[callbackHeaderBytes:], nil
}

func decodeConversationStatus(payload []byte) (ConversationStatus, error) {
	var status ConversationStatus
	if err := json.Unmarshal(payload, &status); err != nil {
		return ConversationStatus{}, fmt.Errorf("decode Volcengine RTC conversation status: %w", err)
	}
	status.TaskID = strings.TrimSpace(status.TaskID)
	status.UserID = strings.TrimSpace(status.UserID)
	status.Stage.Description = strings.TrimSpace(status.Stage.Description)
	if status.ErrorInfo != nil {
		status.ErrorInfo.Reason = strings.TrimSpace(status.ErrorInfo.Reason)
	}
	if err := validateConversationStatus(status); err != nil {
		return ConversationStatus{}, err
	}
	return status, nil
}

func decodeConversationSubtitle(payload []byte) (ConversationSubtitle, error) {
	var subtitle ConversationSubtitle
	if err := json.Unmarshal(payload, &subtitle); err != nil {
		return ConversationSubtitle{}, fmt.Errorf(
			"decode Volcengine RTC conversation subtitle: %w",
			err,
		)
	}
	subtitle.Type = strings.TrimSpace(subtitle.Type)
	for index := range subtitle.Data {
		subtitle.Data[index].Language = strings.TrimSpace(subtitle.Data[index].Language)
		subtitle.Data[index].UserID = strings.TrimSpace(subtitle.Data[index].UserID)
	}
	if err := validateConversationSubtitle(subtitle); err != nil {
		return ConversationSubtitle{}, err
	}
	return subtitle, nil
}

func validateConversationStatus(status ConversationStatus) error {
	if status.TaskID == "" {
		return errors.New("Volcengine RTC callback TaskId is required")
	}
	if status.UserID == "" {
		return errors.New("Volcengine RTC callback UserID is required")
	}
	if status.RoundID < 0 {
		return errors.New("Volcengine RTC callback RoundID must not be negative")
	}
	if status.EventTime <= 0 {
		return errors.New("Volcengine RTC callback EventTime must be positive")
	}
	if status.Stage.Code < ConversationStageError ||
		status.Stage.Code > ConversationStageAnswerFinished {
		return errors.New("Volcengine RTC callback Stage.Code is unsupported")
	}
	if status.Stage.Description == "" {
		return errors.New("Volcengine RTC callback Stage.Description is required")
	}
	if status.Stage.Code == ConversationStageError {
		if status.ErrorInfo == nil {
			return errors.New("Volcengine RTC error callback ErrorInfo is required")
		}
		if status.ErrorInfo.ErrorCode <= 0 {
			return errors.New("Volcengine RTC callback ErrorInfo.ErrorCode must be positive")
		}
		if status.ErrorInfo.Reason == "" {
			return errors.New("Volcengine RTC callback ErrorInfo.Reason is required")
		}
	}
	return nil
}

func validateConversationSubtitle(subtitle ConversationSubtitle) error {
	if subtitle.Type != "subtitle" {
		return errors.New("Volcengine RTC subtitle callback type must be subtitle")
	}
	if len(subtitle.Data) == 0 {
		return errors.New("Volcengine RTC subtitle callback data is required")
	}
	for index, segment := range subtitle.Data {
		if segment.UserID == "" {
			return fmt.Errorf(
				"Volcengine RTC subtitle callback data[%d].userId is required",
				index,
			)
		}
		if segment.Sequence < 0 {
			return fmt.Errorf(
				"Volcengine RTC subtitle callback data[%d].sequence must not be negative",
				index,
			)
		}
		if segment.RoundID < 0 {
			return fmt.Errorf(
				"Volcengine RTC subtitle callback data[%d].roundId must not be negative",
				index,
			)
		}
	}
	return nil
}

func validateFunctionCallMessage(message FunctionCallMessage) error {
	if len(message.ToolCalls) == 0 {
		return errors.New("Volcengine RTC function callback tool_calls is required")
	}
	if len(message.ToolCalls) > maxFunctionToolCalls {
		return fmt.Errorf(
			"Volcengine RTC function callback exceeds %d tool calls",
			maxFunctionToolCalls,
		)
	}
	for index, tool := range message.ToolCalls {
		if tool.ID == "" || len(tool.ID) > 256 {
			return fmt.Errorf(
				"Volcengine RTC function callback tool_calls[%d].id is invalid",
				index,
			)
		}
		if tool.Type != "function" {
			return fmt.Errorf(
				"Volcengine RTC function callback tool_calls[%d].type is unsupported",
				index,
			)
		}
		if tool.Function.Name == "" || len(tool.Function.Name) > 256 {
			return fmt.Errorf(
				"Volcengine RTC function callback tool_calls[%d].function.name is invalid",
				index,
			)
		}
		if len(tool.Function.Arguments) > maxCallbackPayloadBytes {
			return fmt.Errorf(
				"Volcengine RTC function callback tool_calls[%d].function.arguments is too large",
				index,
			)
		}
		if err := requireJSONObject(
			"function arguments",
			json.RawMessage(tool.Function.Arguments),
		); err != nil {
			return fmt.Errorf(
				"Volcengine RTC function callback tool_calls[%d]: %w",
				index,
				err,
			)
		}
	}
	return nil
}
