package handler

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/service/voicecall"
)

const (
	maxVoiceCallLLMRequestBytes = 256 << 10
	maxVoiceCallLLMRoundIDBytes = 128
	voiceCallLLMModel           = "multica-beckham"
)

type VoiceCallLLMInput struct {
	VoiceCallID string
	RoundID     string
	Transcript  string
}

type VoiceCallLLMReply struct {
	Content string
}

type VoiceCallLLMProcessor interface {
	Reply(context.Context, VoiceCallLLMInput) (VoiceCallLLMReply, error)
}

type voiceCallLLMMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type voiceCallLLMRequest struct {
	Messages    []voiceCallLLMMessage `json:"messages"`
	Stream      bool                  `json:"stream"`
	VoiceCallID string                `json:"voice_call_id"`
	RoundID     json.RawMessage       `json:"round_id"`
}

type voiceCallLLMChunk struct {
	ID      string               `json:"id"`
	Object  string               `json:"object"`
	Created int64                `json:"created"`
	Model   string               `json:"model"`
	Choices []voiceCallLLMChoice `json:"choices"`
}

type voiceCallLLMChoice struct {
	Index        int                    `json:"index"`
	Delta        voiceCallLLMChunkDelta `json:"delta"`
	FinishReason *string                `json:"finish_reason"`
}

type voiceCallLLMChunkDelta struct {
	Role    string `json:"role,omitempty"`
	Content string `json:"content,omitempty"`
}

func (h *Handler) HandleVoiceCallLLM(w http.ResponseWriter, r *http.Request) {
	if h.VoiceCallLLMProcessor == nil || strings.TrimSpace(h.VoiceCallLLMAPIKey) == "" {
		writeError(w, http.StatusServiceUnavailable, "voice call agent bridge is not configured")
		return
	}
	if !voiceCallLLMBearerMatches(r.Header.Get("Authorization"), h.VoiceCallLLMAPIKey) {
		writeError(w, http.StatusUnauthorized, "invalid voice call agent credential")
		return
	}
	if r.ContentLength > maxVoiceCallLLMRequestBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "voice call request exceeds 256 KiB")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxVoiceCallLLMRequestBytes)
	defer r.Body.Close()

	var request voiceCallLLMRequest
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(&request); err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			writeError(w, http.StatusRequestEntityTooLarge, "voice call request exceeds 256 KiB")
			return
		}
		writeError(w, http.StatusBadRequest, "invalid voice call request")
		return
	}
	if err := requireVoiceCallLLMEOF(decoder); err != nil {
		writeError(w, http.StatusBadRequest, "invalid voice call request")
		return
	}

	input, err := validateVoiceCallLLMRequest(request)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	reply, err := h.VoiceCallLLMProcessor.Reply(r.Context(), input)
	if err != nil {
		slog.Error(
			"process voice call agent turn",
			"voice_call_id", input.VoiceCallID,
			"round_id", input.RoundID,
			"error", err,
		)
		writeVoiceCallLLMProcessorError(w, err)
		return
	}
	reply.Content = strings.TrimSpace(reply.Content)
	if reply.Content == "" {
		slog.Error(
			"process voice call agent turn returned empty reply",
			"voice_call_id", input.VoiceCallID,
			"round_id", input.RoundID,
		)
		writeError(w, http.StatusBadGateway, "voice call agent returned no reply")
		return
	}

	if err := writeVoiceCallLLMSSE(w, input, reply); err != nil {
		slog.Error(
			"stream voice call agent reply",
			"voice_call_id", input.VoiceCallID,
			"round_id", input.RoundID,
			"error", err,
		)
	}
}

func writeVoiceCallLLMProcessorError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errVoiceCallAgentTurnTimeout),
		errors.Is(err, context.DeadlineExceeded):
		writeError(w, http.StatusGatewayTimeout, "voice call agent turn timed out")
	case errors.Is(err, voicecall.ErrCallNotFound),
		errors.Is(err, voicecall.ErrScopeNotFound):
		writeError(w, http.StatusNotFound, "voice call agent turn was not found")
	case errors.Is(err, voicecall.ErrScopeForbidden):
		writeError(w, http.StatusForbidden, "voice call agent turn is forbidden")
	case errors.Is(err, errVoiceCallAgentTurnUnavailable),
		errors.Is(err, voicecall.ErrScopeUnavailable),
		errors.Is(err, errVoiceCallAgentTurnConflict):
		writeError(w, http.StatusConflict, "voice call agent turn is unavailable")
	case errors.Is(err, errVoiceCallAgentNoReply):
		writeError(w, http.StatusBadGateway, "voice call agent returned no spoken reply")
	case errors.Is(err, errVoiceCallAgentHeld):
		writeError(w, http.StatusConflict, "voice call agent reply was held for fresh context")
	default:
		writeError(w, http.StatusBadGateway, "voice call agent turn failed")
	}
}

func voiceCallLLMBearerMatches(header, expected string) bool {
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return false
	}
	token := strings.TrimSpace(strings.TrimPrefix(header, prefix))
	if token == "" || strings.TrimSpace(expected) == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(token), []byte(expected)) == 1
}

func validateVoiceCallLLMRequest(request voiceCallLLMRequest) (VoiceCallLLMInput, error) {
	if !request.Stream {
		return VoiceCallLLMInput{}, errors.New("voice call request must enable streaming")
	}
	callID := strings.TrimSpace(request.VoiceCallID)
	if _, err := uuid.Parse(callID); err != nil {
		return VoiceCallLLMInput{}, errors.New("voice_call_id must be a UUID")
	}
	roundID, err := normalizeVoiceCallLLMRoundID(request.RoundID)
	if err != nil {
		return VoiceCallLLMInput{}, err
	}

	transcript := ""
	for index := len(request.Messages) - 1; index >= 0; index-- {
		if request.Messages[index].Role != "user" {
			continue
		}
		transcript = strings.TrimSpace(request.Messages[index].Content)
		if transcript != "" {
			break
		}
	}
	if transcript == "" {
		return VoiceCallLLMInput{}, errors.New("voice call request has no user transcript")
	}
	return VoiceCallLLMInput{
		VoiceCallID: callID,
		RoundID:     roundID,
		Transcript:  transcript,
	}, nil
}

func normalizeVoiceCallLLMRoundID(raw json.RawMessage) (string, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return "", errors.New("voice call request is missing round_id")
	}
	if len(raw) > maxVoiceCallLLMRoundIDBytes+2 {
		return "", errors.New("voice call round_id is too long")
	}

	if raw[0] == '"' {
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return "", errors.New("voice call round_id is invalid")
		}
		value = strings.TrimSpace(value)
		if value == "" || len(value) > maxVoiceCallLLMRoundIDBytes {
			return "", errors.New("voice call round_id is invalid")
		}
		if strings.IndexFunc(value, unicode.IsControl) >= 0 {
			return "", errors.New("voice call round_id is invalid")
		}
		return value, nil
	}

	value, err := strconv.ParseUint(string(raw), 10, 64)
	if err != nil {
		return "", errors.New("voice call round_id is invalid")
	}
	return strconv.FormatUint(value, 10), nil
}

func requireVoiceCallLLMEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("voice call request has trailing JSON")
		}
		return err
	}
	return nil
}

func writeVoiceCallLLMSSE(
	w http.ResponseWriter,
	input VoiceCallLLMInput,
	reply VoiceCallLLMReply,
) error {
	contentChunk := voiceCallLLMChunk{
		ID:      fmt.Sprintf("voice-call-%s-%s", input.VoiceCallID, input.RoundID),
		Object:  "chat.completion.chunk",
		Created: time.Now().Unix(),
		Model:   voiceCallLLMModel,
		Choices: []voiceCallLLMChoice{{
			Index: 0,
			Delta: voiceCallLLMChunkDelta{
				Role:    "assistant",
				Content: reply.Content,
			},
		}},
	}
	contentPayload, err := json.Marshal(contentChunk)
	if err != nil {
		return err
	}
	stop := "stop"
	finishChunk := contentChunk
	finishChunk.Choices = []voiceCallLLMChoice{{
		Index:        0,
		Delta:        voiceCallLLMChunkDelta{},
		FinishReason: &stop,
	}}
	finishPayload, err := json.Marshal(finishChunk)
	if err != nil {
		return err
	}

	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	if _, err := fmt.Fprintf(
		w,
		"data: %s\n\ndata: %s\n\ndata: [DONE]\n\n",
		contentPayload,
		finishPayload,
	); err != nil {
		return err
	}
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
	return nil
}
