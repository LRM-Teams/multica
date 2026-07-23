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
	conversationStatusMagic           = "conv"
	conversationStatusHeaderBytes     = 8
	maxConversationStatusEncodedBytes = 128 << 10
	maxConversationStatusPayloadBytes = 64 << 10
)

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

func DecodeConversationStatusCallback(encoded string) (ConversationStatus, error) {
	encoded = strings.TrimSpace(encoded)
	if encoded == "" {
		return ConversationStatus{}, errors.New("Volcengine RTC callback message is required")
	}
	if len(encoded) > maxConversationStatusEncodedBytes {
		return ConversationStatus{}, fmt.Errorf(
			"Volcengine RTC callback base64 exceeds %d bytes",
			maxConversationStatusEncodedBytes,
		)
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return ConversationStatus{}, fmt.Errorf("decode Volcengine RTC callback base64: %w", err)
	}
	if len(data) < conversationStatusHeaderBytes {
		return ConversationStatus{}, errors.New("Volcengine RTC callback is shorter than its TLV header")
	}
	if string(data[:4]) != conversationStatusMagic {
		return ConversationStatus{}, errors.New("Volcengine RTC callback magic is not conv")
	}
	payloadLength := binary.BigEndian.Uint32(data[4:8])
	if payloadLength > maxConversationStatusPayloadBytes {
		return ConversationStatus{}, fmt.Errorf(
			"Volcengine RTC callback payload exceeds %d bytes",
			maxConversationStatusPayloadBytes,
		)
	}
	if uint64(payloadLength)+conversationStatusHeaderBytes != uint64(len(data)) {
		return ConversationStatus{}, errors.New("Volcengine RTC callback TLV length does not match payload")
	}

	var status ConversationStatus
	if err := json.Unmarshal(data[conversationStatusHeaderBytes:], &status); err != nil {
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
