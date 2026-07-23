package voiceaudio

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
)

func testPCM16WAV(pcm []byte, sampleRate int) []byte {
	wav := make([]byte, 44+len(pcm))
	copy(wav[0:4], "RIFF")
	binary.LittleEndian.PutUint32(wav[4:8], uint32(len(wav)-8))
	copy(wav[8:12], "WAVE")
	copy(wav[12:16], "fmt ")
	binary.LittleEndian.PutUint32(wav[16:20], 16)
	binary.LittleEndian.PutUint16(wav[20:22], 1)
	binary.LittleEndian.PutUint16(wav[22:24], 1)
	binary.LittleEndian.PutUint32(wav[24:28], uint32(sampleRate))
	binary.LittleEndian.PutUint32(wav[28:32], uint32(sampleRate*2))
	binary.LittleEndian.PutUint16(wav[32:34], 2)
	binary.LittleEndian.PutUint16(wav[34:36], 16)
	copy(wav[36:40], "data")
	binary.LittleEndian.PutUint32(wav[40:44], uint32(len(pcm)))
	copy(wav[44:], pcm)
	return wav
}

func TestDecodePCM16MonoWAVRoundTrip(t *testing.T) {
	pcm := []byte{0x00, 0x00, 0xff, 0x7f, 0x00, 0x80, 0x34, 0x12}
	got, err := DecodePCM16MonoWAV(testPCM16WAV(pcm, 16000), 16000, 2<<20)
	if err != nil {
		t.Fatalf("DecodePCM16MonoWAV: %v", err)
	}
	if string(got) != string(pcm) {
		t.Fatalf("decoded PCM = %v, want %v", got, pcm)
	}
}

func TestDecodePCM16MonoWAVRejectsNoiseFormats(t *testing.T) {
	wav := testPCM16WAV([]byte{0, 0, 1, 0}, 16000)
	binary.LittleEndian.PutUint16(wav[22:24], 2)
	if _, err := DecodePCM16MonoWAV(wav, 16000, 2<<20); err == nil || !strings.Contains(err.Error(), "16 kHz mono PCM16") {
		t.Fatalf("stereo WAV error = %v, want explicit format rejection", err)
	}
	if _, err := DecodePCM16MonoWAV([]byte("not a wav"), 16000, 2<<20); err == nil {
		t.Fatal("expected non-WAV bytes to be rejected")
	}
}

func TestDecodePCM16MonoWAVRejectsOversizedPCM(t *testing.T) {
	wav := testPCM16WAV([]byte{0, 0, 1, 0}, 16000)
	if _, err := DecodePCM16MonoWAV(wav, 16000, 2); err == nil {
		t.Fatal("expected PCM larger than the caller limit to be rejected")
	}
}

func TestEncodePCM16MonoWAVDoesNotApplyRecordingUploadLimit(t *testing.T) {
	const recordingUploadLimit = 2 << 20
	pcm := bytes.Repeat([]byte{0x00, 0x00}, recordingUploadLimit/2+1)

	wav, durationMS, err := EncodePCM16MonoWAV(pcm, 24000)
	if err != nil {
		t.Fatalf("EncodePCM16MonoWAV: %v", err)
	}
	if len(wav) != len(pcm)+44 {
		t.Fatalf("WAV bytes = %d, want %d", len(wav), len(pcm)+44)
	}
	if durationMS <= 43_000 {
		t.Fatalf("duration = %dms, want audio beyond the recording limit", durationMS)
	}
}
