package doubaospeech

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// TestLiveSpeechRoundTrip is opt-in because it consumes provider quota. It
// synthesizes 16 kHz PCM, then sends those exact samples to streaming ASR so
// both production protocols and the selected speaker are tested together.
func TestLiveSpeechRoundTrip(t *testing.T) {
	apiKey := strings.TrimSpace(os.Getenv("DOUBAO_SPEECH_API_KEY"))
	speakerID := strings.TrimSpace(os.Getenv("DOUBAO_TTS_SPEAKER_ID"))
	if apiKey == "" || speakerID == "" {
		t.Skip("DOUBAO_SPEECH_API_KEY and DOUBAO_TTS_SPEAKER_ID are required")
	}

	client := New(Config{APIKey: apiKey, SpeakerID: speakerID, Timeout: 60 * time.Second})
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	audio, err := client.Synthesize(ctx, SynthesisRequest{
		Text: "你好，我是贝克汉姆。", Format: "pcm", SampleRate: 16000,
	})
	if err != nil {
		t.Fatalf("live TTS failed: %v", err)
	}
	if len(audio.Data) == 0 {
		t.Fatal("live TTS returned no PCM audio")
	}

	transcript, err := client.Transcribe(ctx, TranscriptionRequest{PCM: audio.Data, SampleRate: 16000})
	if err != nil {
		t.Fatalf("live ASR failed: %v", err)
	}
	if !strings.Contains(transcript.Text, "贝克汉姆") {
		t.Fatalf("live ASR transcript %q does not contain the spoken name", transcript.Text)
	}
	t.Logf("round trip succeeded: %d PCM bytes, transcript %q", len(audio.Data), transcript.Text)
}
