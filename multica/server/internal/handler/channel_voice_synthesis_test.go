package handler

import (
	"bytes"
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/integrations/doubaospeech"
	"github.com/multica-ai/multica/server/internal/messageparts"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestChannelVoicePartsWithSynthesisCompletesServerArtifact(t *testing.T) {
	parts := channelVoicePartsWithSynthesis(
		[]protocol.MessagePart{
			{Type: protocol.MessagePartTypeText, Text: "spoken answer"},
			{
				Type:            protocol.MessagePartTypeVoice,
				SynthesisStatus: protocol.VoiceSynthesisPending,
			},
		},
		"11111111-1111-1111-1111-111111111111",
		"voice.wav",
		2048,
		1250,
	)
	if len(parts) != 2 {
		t.Fatalf("parts = %+v, want text and voice", parts)
	}
	voice := parts[1]
	if voice.SynthesisStatus != protocol.VoiceSynthesisCompleted ||
		voice.AttachmentID != "11111111-1111-1111-1111-111111111111" ||
		voice.Filename != "voice.wav" ||
		voice.ContentType != "audio/wav" ||
		voice.SizeBytes != 2048 ||
		voice.DurationMS != 1250 {
		t.Fatalf("completed voice part = %+v", voice)
	}
}

func TestChannelMessageNeedsVoiceSynthesisRequiresPendingState(t *testing.T) {
	if !channelMessageNeedsVoiceSynthesis([]protocol.MessagePart{{
		Type:            protocol.MessagePartTypeVoice,
		SynthesisStatus: protocol.VoiceSynthesisPending,
	}}) {
		t.Fatal("pending Agent voice should require synthesis")
	}
	if channelMessageNeedsVoiceSynthesis([]protocol.MessagePart{{
		Type:            protocol.MessagePartTypeVoice,
		SynthesisStatus: protocol.VoiceSynthesisCompleted,
	}}) {
		t.Fatal("completed Agent voice must not be synthesized again")
	}
}

func TestEncodeSynthesizedPCM16WAVAcceptsAudioAboveRecordingUploadLimit(t *testing.T) {
	pcm := bytes.Repeat([]byte{0x00, 0x00}, maxVoiceRecordingPCMBytes/2+1)

	wav, durationMS, err := encodeSynthesizedPCM16WAV(doubaospeech.Audio{
		Data:       pcm,
		Format:     "pcm",
		SampleRate: 24000,
	})
	if err != nil {
		t.Fatalf("encode long synthesized audio: %v", err)
	}
	if len(wav) != len(pcm)+44 {
		t.Fatalf("WAV bytes = %d, want %d", len(wav), len(pcm)+44)
	}
	if durationMS <= 43_000 {
		t.Fatalf("duration = %dms, want audio beyond the old recording limit", durationMS)
	}
}

func TestAgentVoiceMessagePersistsSynthesizedAttachment(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	channelID := seedChannelForTest(t, "agent-voice-synthesis-"+uuid.NewString(), testUserID)
	agentID := createHandlerTestAgent(t, "Voice Synthesis "+uuid.NewString(), nil)
	content := "server generated answer"
	parts := []protocol.MessagePart{
		{Type: protocol.MessagePartTypeText, Text: content},
		{Type: protocol.MessagePartTypeVoice},
	}
	ch, found := testHandler.getChannel(ctx, testWorkspaceID, parseUUID(channelID))
	if !found {
		t.Fatal("seeded channel not found")
	}
	content, parts, err := testHandler.finalizeAgentChannelMessage(ctx, ch, content, parts)
	if err != nil {
		t.Fatalf("finalize Agent voice: %v", err)
	}
	msg, err := testHandler.insertChannelMessageWithParts(
		ctx,
		parseUUID(channelID),
		parseUUID(testWorkspaceID),
		"agent",
		parseUUID(agentID),
		"Voice Agent",
		content,
		parts,
		"multica",
		nil,
		pgtype.UUID{},
		pgtype.UUID{},
		nil,
		0,
	)
	if err != nil {
		t.Fatalf("insert Agent voice message: %v", err)
	}

	var queuedStatus string
	if err := testPool.QueryRow(ctx, `
		SELECT status
		FROM channel_voice_synthesis
		WHERE message_id = $1`, msg.ID).Scan(&queuedStatus); err != nil {
		t.Fatalf("load Agent voice synthesis job: %v", err)
	}
	if queuedStatus != "pending" {
		t.Fatalf("queued status = %q, want pending", queuedStatus)
	}

	pcm := bytes.Repeat([]byte{0x00, 0x00}, 2400)
	store := &mockStorage{}
	provider := &fakeVoiceProvider{
		configured: true,
		audio: doubaospeech.Audio{
			Data:       pcm,
			Format:     "pcm",
			SampleRate: 24000,
		},
	}
	processor := *testHandler
	processor.Storage = store
	processor.VoiceProvider = provider
	if err := processor.processChannelVoiceSynthesis(ctx, msg.ID); err != nil {
		t.Fatalf("process Agent voice synthesis: %v", err)
	}

	var rawParts []byte
	var completedStatus, attachmentID, contentType string
	var sizeBytes int64
	if err := testPool.QueryRow(ctx, `
		SELECT message.parts, synthesis.status, attachment.id::text,
		       attachment.content_type, attachment.size_bytes
		FROM channel_message message
		JOIN channel_voice_synthesis synthesis ON synthesis.message_id = message.id
		JOIN attachment ON attachment.id = synthesis.attachment_id
		WHERE message.id = $1`, msg.ID).Scan(
		&rawParts,
		&completedStatus,
		&attachmentID,
		&contentType,
		&sizeBytes,
	); err != nil {
		t.Fatalf("load synthesized Agent voice: %v", err)
	}
	completedParts := messageparts.Decode(rawParts)
	if completedStatus != "completed" || len(completedParts) != 2 {
		t.Fatalf("completed status/parts = %q %+v", completedStatus, completedParts)
	}
	voice := completedParts[1]
	if voice.SynthesisStatus != protocol.VoiceSynthesisCompleted ||
		voice.AttachmentID != attachmentID ||
		voice.ContentType != "audio/wav" ||
		voice.DurationMS != 100 ||
		contentType != "audio/wav" ||
		sizeBytes != int64(44+len(pcm)) {
		t.Fatalf("persisted voice artifact = %+v type=%q size=%d", voice, contentType, sizeBytes)
	}
	if provider.synthesisRequest.Text != content ||
		provider.synthesisRequest.Format != "pcm" ||
		provider.synthesisRequest.SampleRate != 24000 {
		t.Fatalf("provider request = %+v", provider.synthesisRequest)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.files) != 1 {
		t.Fatalf("stored files = %d, want 1", len(store.files))
	}
	for _, body := range store.files {
		if len(body) < 44 || string(body[:4]) != "RIFF" || string(body[8:12]) != "WAVE" {
			t.Fatalf("stored body is not WAV: %x", body)
		}
	}
}
