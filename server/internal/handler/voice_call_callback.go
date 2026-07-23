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

	"github.com/multica-ai/multica/server/internal/integrations/volcenginertc"
)

const maxVoiceCallCallbackRequestBytes = 128 << 10

type VoiceCallCallbackProcessor interface {
	HandleConversationStatus(
		ctx context.Context,
		status volcenginertc.ConversationStatus,
	) error
	HandleConversationSubtitle(
		ctx context.Context,
		subtitle volcenginertc.ConversationSubtitle,
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

	var envelope voiceCallCallbackEnvelope
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(&envelope); err != nil {
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
	if err := requireVoiceCallCallbackEOF(decoder); err != nil {
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
	if err := h.processVoiceCallServerCallback(r.Context(), callback); err != nil {
		slog.Error(
			"process voice call callback",
			"callback_kind", callback.Kind,
			"error", err,
		)
		writeError(w, http.StatusInternalServerError, "voice call callback processing failed")
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func (h *Handler) processVoiceCallServerCallback(
	ctx context.Context,
	callback volcenginertc.ServerCallback,
) error {
	switch callback.Kind {
	case volcenginertc.ServerCallbackConversationStatus:
		if callback.ConversationStatus == nil {
			return errors.New("voice call status callback is missing its payload")
		}
		return h.VoiceCallCallbackProcessor.HandleConversationStatus(
			ctx,
			*callback.ConversationStatus,
		)
	case volcenginertc.ServerCallbackSubtitle:
		if callback.Subtitle == nil {
			return errors.New("voice call subtitle callback is missing its payload")
		}
		return h.VoiceCallCallbackProcessor.HandleConversationSubtitle(
			ctx,
			*callback.Subtitle,
		)
	default:
		return fmt.Errorf("unsupported voice call callback kind %q", callback.Kind)
	}
}

func requireVoiceCallCallbackEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("voice call callback has trailing JSON")
		}
		return err
	}
	return nil
}
