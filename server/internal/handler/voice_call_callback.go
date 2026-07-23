package handler

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
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

	status, err := volcenginertc.DecodeConversationStatusCallback(envelope.Message)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid voice call callback payload")
		return
	}
	if err := h.VoiceCallCallbackProcessor.HandleConversationStatus(r.Context(), status); err != nil {
		slog.Error(
			"process voice call callback",
			"provider_task_id",
			status.TaskID,
			"stage_code",
			status.Stage.Code,
			"error",
			err,
		)
		writeError(w, http.StatusInternalServerError, "voice call callback processing failed")
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
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
