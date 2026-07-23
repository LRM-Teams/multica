package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/multica-ai/multica/server/internal/integrations/doubaospeech"
	"github.com/multica-ai/multica/server/internal/voiceaudio"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

const (
	maxVoiceTTSBodyBytes      = 32 << 10
	maxVoiceTextRunes         = protocol.VoiceTranscriptMaxRunes
	maxVoiceRecordingPCMBytes = 2 << 20
)

type VoiceProvider interface {
	IsConfigured() bool
	Synthesize(ctx context.Context, request doubaospeech.SynthesisRequest) (doubaospeech.Audio, error)
	Transcribe(ctx context.Context, request doubaospeech.TranscriptionRequest) (doubaospeech.Transcript, error)
}

type voiceTTSRequest struct {
	Text string `json:"text"`
}

type voiceASRResponse struct {
	Text string `json:"text"`
}

// SynthesizeVoice converts response text into a self-describing PCM WAV using
// the configured server-side speech provider. The API key never crosses the
// HTTP boundary, and browsers never have to guess raw PCM parameters.
func (h *Handler) SynthesizeVoice(w http.ResponseWriter, r *http.Request) {
	if h.VoiceProvider == nil || !h.VoiceProvider.IsConfigured() {
		writeCodedError(w, http.StatusServiceUnavailable, "voice_not_configured", "voice service is not configured")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxVoiceTTSBodyBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var request voiceTTSRequest
	if err := decoder.Decode(&request); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			writeCodedError(w, http.StatusRequestEntityTooLarge, "voice_text_too_large", "request body exceeds 32 KiB")
			return
		}
		writeCodedError(w, http.StatusBadRequest, "invalid_voice_request", "invalid request body")
		return
	}
	if err := ensureJSONEOF(decoder); err != nil {
		writeCodedError(w, http.StatusBadRequest, "invalid_voice_request", "request body must contain one JSON object")
		return
	}
	request.Text = strings.TrimSpace(request.Text)
	if request.Text == "" {
		writeCodedError(w, http.StatusBadRequest, "voice_text_required", "text is required")
		return
	}
	if utf8.RuneCountInString(request.Text) > maxVoiceTextRunes {
		writeCodedError(w, http.StatusRequestEntityTooLarge, "voice_text_too_large", "text exceeds 4096 characters")
		return
	}

	audio, err := h.VoiceProvider.Synthesize(r.Context(), doubaospeech.SynthesisRequest{
		Text: request.Text, Format: "pcm", SampleRate: 24000,
	})
	if err != nil {
		writeVoiceProviderError(w, "tts", err)
		return
	}
	wav, durationMS, err := encodeSynthesizedPCM16WAV(audio)
	if err != nil {
		writeVoiceProviderError(w, "tts", errors.New("voice provider returned invalid PCM audio"))
		return
	}
	w.Header().Set("Content-Type", "audio/wav")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Length", strconv.Itoa(len(wav)))
	w.Header().Set("X-Voice-Sample-Rate", strconv.Itoa(audio.SampleRate))
	w.Header().Set("X-Voice-Duration-Ms", strconv.FormatInt(durationMS, 10))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(wav)
}

func encodeSynthesizedPCM16WAV(audio doubaospeech.Audio) ([]byte, int64, error) {
	if audio.Format != "pcm" {
		return nil, 0, errors.New("voice provider returned non-PCM audio")
	}
	return voiceaudio.EncodePCM16MonoWAV(audio.Data, audio.SampleRate)
}

// TranscribeVoice accepts signed 16-bit little-endian mono PCM at 16 kHz and
// returns the final transcript. Keeping one explicit format avoids hidden
// transcoding differences between browser, LiveKit, and provider clients.
func (h *Handler) TranscribeVoice(w http.ResponseWriter, r *http.Request) {
	if h.VoiceProvider == nil || !h.VoiceProvider.IsConfigured() {
		writeCodedError(w, http.StatusServiceUnavailable, "voice_not_configured", "voice service is not configured")
		return
	}
	mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != doubaospeech.PCMContentType || !validPCMParameters(params) {
		writeCodedError(w, http.StatusUnsupportedMediaType, "invalid_voice_audio_type", "Content-Type must be audio/pcm with rate=16000 when rate is specified")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxVoiceRecordingPCMBytes)
	pcm, err := io.ReadAll(r.Body)
	if err != nil {
		writeCodedError(w, http.StatusRequestEntityTooLarge, "voice_audio_too_large", "PCM audio exceeds 2 MiB")
		return
	}
	if len(pcm) == 0 {
		writeCodedError(w, http.StatusBadRequest, "voice_audio_required", "PCM audio is required")
		return
	}
	if len(pcm)%2 != 0 {
		writeCodedError(w, http.StatusBadRequest, "invalid_voice_audio", "PCM audio must contain complete 16-bit samples")
		return
	}

	transcript, err := h.VoiceProvider.Transcribe(r.Context(), doubaospeech.TranscriptionRequest{
		PCM: pcm, SampleRate: 16000,
	})
	if err != nil {
		writeVoiceProviderError(w, "asr", err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, voiceASRResponse{Text: transcript.Text})
}

func validPCMParameters(params map[string]string) bool {
	for key, value := range params {
		if key != "rate" || value != "16000" {
			return false
		}
	}
	return true
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	err := decoder.Decode(&extra)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err != nil {
		return err
	}
	return errors.New("unexpected JSON value after request object")
}

func writeVoiceProviderError(w http.ResponseWriter, operation string, err error) {
	var providerErr *doubaospeech.ProviderError
	if errors.As(err, &providerErr) {
		slog.Error("voice provider request failed", "operation", operation, "provider_code", providerErr.Code, "provider_log_id", providerErr.LogID)
	} else {
		slog.Error("voice provider request failed", "operation", operation, "error", err)
	}
	writeCodedError(w, http.StatusBadGateway, "voice_provider_failed", "voice provider request failed")
}
