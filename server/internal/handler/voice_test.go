package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/integrations/doubaospeech"
)

type fakeVoiceProvider struct {
	configured           bool
	synthesisRequest     doubaospeech.SynthesisRequest
	transcriptionRequest doubaospeech.TranscriptionRequest
	audio                doubaospeech.Audio
	transcript           doubaospeech.Transcript
	err                  error
}

func (f *fakeVoiceProvider) IsConfigured() bool { return f.configured }

func (f *fakeVoiceProvider) Synthesize(_ context.Context, request doubaospeech.SynthesisRequest) (doubaospeech.Audio, error) {
	f.synthesisRequest = request
	return f.audio, f.err
}

func (f *fakeVoiceProvider) Transcribe(_ context.Context, request doubaospeech.TranscriptionRequest) (doubaospeech.Transcript, error) {
	f.transcriptionRequest = request
	return f.transcript, f.err
}

func TestSynthesizeVoiceWrapsProviderPCMAsWAV(t *testing.T) {
	pcm := []byte{0x00, 0x00, 0xff, 0x7f}
	provider := &fakeVoiceProvider{
		configured: true,
		audio:      doubaospeech.Audio{Data: pcm, Format: "pcm", SampleRate: 24000},
	}
	h := &Handler{VoiceProvider: provider}
	request := httptest.NewRequest(http.MethodPost, "/api/voice/tts", strings.NewReader(`{"text":" 你好 "}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	h.SynthesizeVoice(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if got := response.Header().Get("Content-Type"); got != "audio/wav" {
		t.Fatalf("Content-Type = %q", got)
	}
	if got := response.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q", got)
	}
	if !bytes.Equal(response.Body.Bytes()[:4], []byte("RIFF")) ||
		!bytes.Equal(response.Body.Bytes()[8:12], []byte("WAVE")) ||
		!bytes.Equal(response.Body.Bytes()[44:], pcm) {
		t.Fatalf("body is not a PCM WAV: %x", response.Body.Bytes())
	}
	if got := response.Header().Get("X-Voice-Duration-Ms"); got != "0" {
		t.Fatalf("X-Voice-Duration-Ms = %q", got)
	}
	if provider.synthesisRequest.Text != "你好" || provider.synthesisRequest.Format != "pcm" || provider.synthesisRequest.SampleRate != 24000 {
		t.Fatalf("unexpected provider request: %+v", provider.synthesisRequest)
	}
}

func TestSynthesizeVoiceRejectsAmbiguousJSON(t *testing.T) {
	h := &Handler{VoiceProvider: &fakeVoiceProvider{configured: true}}
	for _, body := range []string{
		`{"text":"hello","unknown":true}`,
		`{"text":"hello"}{"text":"second"}`,
	} {
		request := httptest.NewRequest(http.MethodPost, "/api/voice/tts", strings.NewReader(body))
		response := httptest.NewRecorder()
		h.SynthesizeVoice(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("body %q: status = %d, response = %s", body, response.Code, response.Body.String())
		}
	}
}

func TestTranscribeVoicePassesExplicitPCMContract(t *testing.T) {
	pcm := []byte{0, 1, 2, 3}
	provider := &fakeVoiceProvider{configured: true, transcript: doubaospeech.Transcript{Text: "你好"}}
	h := &Handler{VoiceProvider: provider}
	request := httptest.NewRequest(http.MethodPost, "/api/voice/asr", bytes.NewReader(pcm))
	request.Header.Set("Content-Type", "audio/pcm; rate=16000")
	response := httptest.NewRecorder()

	h.TranscribeVoice(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if got := response.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q", got)
	}
	if !bytes.Equal(provider.transcriptionRequest.PCM, pcm) || provider.transcriptionRequest.SampleRate != 16000 {
		t.Fatalf("unexpected provider request: %+v", provider.transcriptionRequest)
	}
	var body voiceASRResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Text != "你好" {
		t.Fatalf("text = %q", body.Text)
	}
}

func TestVoiceHandlersExposeConfigurationAndProviderFailures(t *testing.T) {
	t.Run("not configured", func(t *testing.T) {
		h := &Handler{VoiceProvider: &fakeVoiceProvider{}}
		response := httptest.NewRecorder()
		h.SynthesizeVoice(response, httptest.NewRequest(http.MethodPost, "/api/voice/tts", strings.NewReader(`{"text":"hello"}`)))
		if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), "voice_not_configured") {
			t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
		}
	})

	t.Run("provider failure is sanitized", func(t *testing.T) {
		h := &Handler{VoiceProvider: &fakeVoiceProvider{configured: true, err: errors.New("internal provider detail")}}
		response := httptest.NewRecorder()
		h.SynthesizeVoice(response, httptest.NewRequest(http.MethodPost, "/api/voice/tts", strings.NewReader(`{"text":"hello"}`)))
		if response.Code != http.StatusBadGateway || strings.Contains(response.Body.String(), "internal provider detail") {
			t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
		}
	})
}

func TestTranscribeVoiceRejectsWrongMediaTypeAndOddSamples(t *testing.T) {
	h := &Handler{VoiceProvider: &fakeVoiceProvider{configured: true}}

	wrongType := httptest.NewRequest(http.MethodPost, "/api/voice/asr", strings.NewReader("audio"))
	wrongType.Header.Set("Content-Type", "audio/wav")
	wrongTypeResponse := httptest.NewRecorder()
	h.TranscribeVoice(wrongTypeResponse, wrongType)
	if wrongTypeResponse.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("wrong media status = %d", wrongTypeResponse.Code)
	}

	wrongRate := httptest.NewRequest(http.MethodPost, "/api/voice/asr", bytes.NewReader([]byte{0, 1}))
	wrongRate.Header.Set("Content-Type", "audio/pcm; rate=8000")
	wrongRateResponse := httptest.NewRecorder()
	h.TranscribeVoice(wrongRateResponse, wrongRate)
	if wrongRateResponse.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("wrong rate status = %d", wrongRateResponse.Code)
	}

	oddPCM := httptest.NewRequest(http.MethodPost, "/api/voice/asr", bytes.NewReader([]byte{0, 1, 2}))
	oddPCM.Header.Set("Content-Type", doubaospeech.PCMContentType)
	oddPCMResponse := httptest.NewRecorder()
	h.TranscribeVoice(oddPCMResponse, oddPCM)
	if oddPCMResponse.Code != http.StatusBadRequest {
		t.Fatalf("odd PCM status = %d", oddPCMResponse.Code)
	}
}
