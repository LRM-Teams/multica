package handler

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/multica-ai/multica/server/internal/integrations/volcenginertc"
	"github.com/multica-ai/multica/server/internal/service/voicecall"
)

const maxVoiceCallCallbackRequestBytes = 128 << 10

type VoiceCallCallbackProcessor interface {
	HandleConversationStatus(
		ctx context.Context,
		status volcenginertc.ConversationStatus,
	) (voicecall.Session, error)
	HandleConversationSubtitle(
		ctx context.Context,
		subtitle volcenginertc.ConversationSubtitle,
	) error
	HandleVoiceChatTaskEvent(
		ctx context.Context,
		event volcenginertc.VoiceChatTaskEvent,
	) (voicecall.Session, bool, error)
}

type VoiceCallFunctionProcessor interface {
	HandleFunctionCalls(
		ctx context.Context,
		roomID string,
		message volcenginertc.FunctionCallMessage,
	) error
}

type voiceCallCallbackEnvelope struct {
	Message   string `json:"message"`
	Binary    bool   `json:"binary"`
	Signature string `json:"signature"`
}

func (h *Handler) HandleVoiceCallCallback(w http.ResponseWriter, r *http.Request) {
	if h.VoiceCallCallbackProcessor == nil || h.VoiceCallCallbackSignature == "" {
		writeError(
			w,
			http.StatusServiceUnavailable,
			"voice call callback is not configured",
		)
		return
	}
	if r.Method == http.MethodGet {
		writeVoiceCallCallbackOK(w)
		return
	}
	if r.ContentLength > maxVoiceCallCallbackRequestBytes {
		writeError(
			w,
			http.StatusRequestEntityTooLarge,
			"voice call callback exceeds 128 KiB",
		)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxVoiceCallCallbackRequestBytes)
	defer r.Body.Close()

	body, err := io.ReadAll(r.Body)
	if err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			writeError(
				w,
				http.StatusRequestEntityTooLarge,
				"voice call callback exceeds 128 KiB",
			)
			return
		}
		writeError(w, http.StatusBadRequest, "invalid voice call callback")
		return
	}
	var discriminator struct {
		EventType string `json:"EventType"`
	}
	if err := json.Unmarshal(body, &discriminator); err != nil {
		writeError(w, http.StatusBadRequest, "invalid voice call callback")
		return
	}
	if discriminator.EventType == "VoiceChat" {
		h.handleVoiceChatTaskEvent(w, r, body)
		return
	}

	var envelope voiceCallCallbackEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		writeError(w, http.StatusBadRequest, "invalid voice call callback")
		return
	}
	if subtle.ConstantTimeCompare(
		[]byte(envelope.Signature),
		[]byte(h.VoiceCallCallbackSignature),
	) != 1 {
		writeError(w, http.StatusUnauthorized, "invalid voice call callback signature")
		return
	}

	callback, err := volcenginertc.DecodeServerCallback(envelope.Message)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid voice call callback payload")
		return
	}
	if err := h.processVoiceCallServerCallback(
		r.Context(),
		callback,
		r.URL.Query().Get(volcenginertc.FunctionCallbackRoomIDQuery),
	); err != nil {
		slog.Error(
			"process voice call callback",
			"callback_kind", callback.Kind,
			"error", err,
		)
		writeError(w, http.StatusInternalServerError, "voice call callback processing failed")
		return
	}

	writeVoiceCallCallbackOK(w)
}

func (h *Handler) handleVoiceChatTaskEvent(
	w http.ResponseWriter,
	r *http.Request,
	body []byte,
) {
	var envelope volcenginertc.VoiceChatEventEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		writeError(w, http.StatusBadRequest, "invalid voice call callback")
		return
	}
	if !envelope.VerifySignature(h.VoiceCallCallbackSignature) {
		writeError(w, http.StatusUnauthorized, "invalid voice call callback signature")
		return
	}
	event, err := volcenginertc.DecodeVoiceChatTaskEvent(envelope)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid voice call task event")
		return
	}
	session, changed, err := h.VoiceCallCallbackProcessor.HandleVoiceChatTaskEvent(
		r.Context(),
		event,
	)
	if err != nil {
		slog.Error(
			"process voice call task event",
			"run_stage", event.RunStage,
			"error", err,
		)
		writeError(w, http.StatusInternalServerError, "voice call callback processing failed")
		return
	}
	if changed {
		h.publishVoiceCallUpdated(session)
	}
	writeVoiceCallCallbackOK(w)
}

func (h *Handler) processVoiceCallServerCallback(
	ctx context.Context,
	callback volcenginertc.ServerCallback,
	roomID string,
) error {
	switch callback.Kind {
	case volcenginertc.ServerCallbackConversationStatus:
		if callback.ConversationStatus == nil {
			return errors.New("voice call status callback is missing its payload")
		}
		session, err := h.VoiceCallCallbackProcessor.HandleConversationStatus(
			ctx,
			*callback.ConversationStatus,
		)
		if err != nil {
			return err
		}
		h.publishVoiceCallUpdated(session)
		return nil
	case volcenginertc.ServerCallbackSubtitle:
		if callback.Subtitle == nil {
			return errors.New("voice call subtitle callback is missing its payload")
		}
		return h.VoiceCallCallbackProcessor.HandleConversationSubtitle(
			ctx,
			*callback.Subtitle,
		)
	case volcenginertc.ServerCallbackFunctionCall:
		if callback.FunctionCall == nil {
			return errors.New("voice call function callback is missing its payload")
		}
		if h.VoiceCallFunctionProcessor == nil {
			return errors.New("voice call function processor is not configured")
		}
		roomID = strings.TrimSpace(roomID)
		if roomID == "" {
			return errors.New("voice call function callback is missing its room scope")
		}
		return h.VoiceCallFunctionProcessor.HandleFunctionCalls(
			ctx,
			roomID,
			*callback.FunctionCall,
		)
	default:
		return fmt.Errorf("unsupported voice call callback kind %q", callback.Kind)
	}
}

func writeVoiceCallCallbackOK(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}
