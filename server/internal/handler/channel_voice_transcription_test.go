package handler

import (
	"bytes"
	"context"
	"encoding/binary"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/integrations/doubaospeech"
	"github.com/multica-ai/multica/server/internal/messageparts"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestChannelVoicePartsWithTranscriptMarksRecordingCompleted(t *testing.T) {
	parts := channelVoicePartsWithTranscript([]protocol.MessagePart{{
		Type:                protocol.MessagePartTypeVoice,
		AttachmentID:        "11111111-1111-1111-1111-111111111111",
		DurationMS:          1800,
		TranscriptionStatus: protocol.VoiceTranscriptionPending,
	}}, "  spoken question  ")
	if len(parts) != 2 || parts[0].Type != protocol.MessagePartTypeText || parts[0].Text != "spoken question" {
		t.Fatalf("parts = %+v, want canonical transcript first", parts)
	}
	if parts[1].TranscriptionStatus != protocol.VoiceTranscriptionCompleted {
		t.Fatalf("voice status = %q, want completed", parts[1].TranscriptionStatus)
	}
}

func TestPendingChannelVoiceAttachmentIDRequiresServerPendingState(t *testing.T) {
	id := "11111111-1111-1111-1111-111111111111"
	got, ok := pendingChannelVoiceAttachmentID([]protocol.MessagePart{{
		Type:                protocol.MessagePartTypeVoice,
		AttachmentID:        id,
		TranscriptionStatus: protocol.VoiceTranscriptionPending,
	}})
	if !ok || got != id {
		t.Fatalf("pending attachment = %q %v, want %q true", got, ok, id)
	}
	if _, ok := pendingChannelVoiceAttachmentID([]protocol.MessagePart{{
		Type:                protocol.MessagePartTypeVoice,
		AttachmentID:        id,
		TranscriptionStatus: protocol.VoiceTranscriptionCompleted,
	}}); ok {
		t.Fatal("completed voice must not be enqueued again")
	}
}

func TestChannelPartsAllowEmptyContentForPendingRecordingOnly(t *testing.T) {
	pending := protocol.MessagePart{
		Type:                protocol.MessagePartTypeVoice,
		AttachmentID:        "11111111-1111-1111-1111-111111111111",
		TranscriptionStatus: protocol.VoiceTranscriptionPending,
	}
	if !channelPartsAllowEmptyContent([]protocol.MessagePart{pending}) {
		t.Fatal("pending recorded voice should be accepted before transcript exists")
	}
	pending.AttachmentID = ""
	if channelPartsAllowEmptyContent([]protocol.MessagePart{pending}) {
		t.Fatal("attachment-free voice must still require accessible text")
	}
}

func TestReadChannelVoicePCMAcceptsGoDetectedWAVMediaType(t *testing.T) {
	pcm := []byte{0x00, 0x00, 0xff, 0x7f}
	wav := testPCM16MonoWAV(pcm, 16000)
	h := &Handler{Storage: &mockStorage{files: map[string][]byte{
		"recording.wav": wav,
	}}}

	got, err := h.readChannelVoicePCM(context.Background(), channelVoiceRecording{
		URL:         "recording.wav",
		ContentType: "audio/wave",
		SizeBytes:   int64(len(wav)),
	})
	if err != nil {
		t.Fatalf("read Go-detected WAV: %v", err)
	}
	if !bytes.Equal(got, pcm) {
		t.Fatalf("PCM = %v, want %v", got, pcm)
	}
}

func TestCreateUserChannelMessagePersistsVoiceTranscriptionJob(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	channelID := seedChannelForTest(t, "voice-transcription-job-"+uuid.NewString(), testUserID)
	var attachmentID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO attachment (
		  workspace_id, channel_id, uploader_type, uploader_id,
		  filename, url, content_type, size_bytes
		)
		VALUES ($1, $2, 'member', $3, 'voice.wav', 'voice-job-test.wav', 'audio/wav', 48)
		RETURNING id`,
		testWorkspaceID, channelID, testUserID).Scan(&attachmentID); err != nil {
		t.Fatalf("seed recorded voice attachment: %v", err)
	}
	parts := []protocol.MessagePart{{
		Type:                protocol.MessagePartTypeVoice,
		AttachmentID:        attachmentID,
		DurationMS:          1000,
		TranscriptionStatus: protocol.VoiceTranscriptionPending,
	}}
	result, err := testHandler.createUserChannelMessageWithIdempotency(ctx, channelMessageInsertInput{
		ChannelID:   parseUUID(channelID),
		WorkspaceID: parseUUID(testWorkspaceID),
		AuthorID:    parseUUID(testUserID),
		AuthorName:  "Voice Tester",
		Parts:       parts,
		ClientMessageID: func() *string {
			value := "voice-job-" + uuid.NewString()
			return &value
		}(),
	}, []pgtype.UUID{parseUUID(attachmentID)})
	if err != nil {
		t.Fatalf("create recorded voice message: %v", err)
	}
	var status, queuedAttachmentID string
	if err := testPool.QueryRow(ctx, `
		SELECT status, attachment_id::text
		FROM channel_voice_transcription
		WHERE message_id = $1`,
		result.Message.ID).Scan(&status, &queuedAttachmentID); err != nil {
		t.Fatalf("load voice transcription job: %v", err)
	}
	if status != "pending" || queuedAttachmentID != attachmentID {
		t.Fatalf("voice job = %q %q, want pending %q", status, queuedAttachmentID, attachmentID)
	}

	pcm := []byte{0x00, 0x00, 0xff, 0x7f}
	store := &mockStorage{files: map[string][]byte{
		"voice-job-test.wav": testPCM16MonoWAV(pcm, 16000),
	}}
	provider := &fakeVoiceProvider{
		configured: true,
		transcript: doubaospeech.Transcript{Text: "spoken question"},
	}
	processor := *testHandler
	processor.Storage = store
	processor.VoiceProvider = provider
	if err := processor.processChannelVoiceTranscription(ctx, result.Message.ID); err != nil {
		t.Fatalf("process recorded voice message: %v", err)
	}

	var content, completedStatus string
	var rawParts []byte
	if err := testPool.QueryRow(ctx, `
		SELECT message.content, message.parts, transcription.status
		FROM channel_message message
		JOIN channel_voice_transcription transcription
		  ON transcription.message_id = message.id
		WHERE message.id = $1`,
		result.Message.ID).Scan(&content, &rawParts, &completedStatus); err != nil {
		t.Fatalf("load transcribed voice message: %v", err)
	}
	completedParts := messageparts.Decode(rawParts)
	if content != "spoken question" || completedStatus != "completed" {
		t.Fatalf("completed message = %q %q, want spoken question completed", content, completedStatus)
	}
	if len(completedParts) != 2 ||
		completedParts[0].Type != protocol.MessagePartTypeText ||
		completedParts[1].Type != protocol.MessagePartTypeVoice ||
		completedParts[1].TranscriptionStatus != protocol.VoiceTranscriptionCompleted {
		t.Fatalf("completed parts = %+v, want text plus completed voice", completedParts)
	}
	if !bytes.Equal(provider.transcriptionRequest.PCM, pcm) || provider.transcriptionRequest.SampleRate != 16000 {
		t.Fatalf("provider request = %+v, want exact stored PCM at 16 kHz", provider.transcriptionRequest)
	}
}

func TestDelayedChannelVoiceTranscriptDispatchesAfterAgentCursorAdvanced(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	channelID := seedChannelForTest(t, "late-voice-dispatch-"+uuid.NewString(), testUserID)
	agentID := createHandlerTestAgent(t, "Late Voice Agent "+uuid.NewString()[:8], nil)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3)
ON CONFLICT DO NOTHING`,
		channelID, testWorkspaceID, agentID); err != nil {
		t.Fatalf("seed channel agent: %v", err)
	}

	var attachmentID string
	recordingURL := "late-voice-" + uuid.NewString() + ".wav"
	if err := testPool.QueryRow(ctx, `
		INSERT INTO attachment (
		  workspace_id, channel_id, uploader_type, uploader_id,
		  filename, url, content_type, size_bytes
		)
		VALUES ($1, $2, 'member', $3, 'voice.wav', $4, 'audio/wave', 48)
		RETURNING id`,
		testWorkspaceID, channelID, testUserID, recordingURL).Scan(&attachmentID); err != nil {
		t.Fatalf("seed delayed voice attachment: %v", err)
	}
	voice, err := testHandler.createUserChannelMessageWithIdempotency(ctx, channelMessageInsertInput{
		ChannelID:   parseUUID(channelID),
		WorkspaceID: parseUUID(testWorkspaceID),
		AuthorID:    parseUUID(testUserID),
		AuthorName:  "Voice Tester",
		Parts: []protocol.MessagePart{{
			Type:                protocol.MessagePartTypeVoice,
			AttachmentID:        attachmentID,
			DurationMS:          1000,
			TranscriptionStatus: protocol.VoiceTranscriptionPending,
		}},
		ClientMessageID: strPtr("late-voice-" + uuid.NewString()),
	}, []pgtype.UUID{parseUUID(attachmentID)})
	if err != nil {
		t.Fatalf("create delayed voice message: %v", err)
	}

	later, err := testHandler.insertChannelMessage(
		ctx,
		parseUUID(channelID),
		parseUUID(testWorkspaceID),
		"user",
		parseUUID(testUserID),
		"Voice Tester",
		"message sent while speech recognition is delayed",
		"multica",
		nil,
		pgtype.UUID{},
		pgtype.UUID{},
		strPtr("later-message-"+uuid.NewString()),
		0,
	)
	if err != nil {
		t.Fatalf("create later channel message: %v", err)
	}
	if later.Seq <= voice.Message.Seq {
		t.Fatalf("later message sequence = %d, want greater than delayed voice sequence %d", later.Seq, voice.Message.Seq)
	}

	pcm := []byte{0x00, 0x00, 0xff, 0x7f}
	processor := *testHandler
	processor.Storage = &mockStorage{files: map[string][]byte{
		recordingURL: testPCM16MonoWAV(pcm, 16000),
	}}
	processor.VoiceProvider = &fakeVoiceProvider{
		configured: true,
		transcript: doubaospeech.Transcript{Text: "can any agent hear this voice message"},
	}
	if err := processor.processChannelVoiceTranscription(ctx, voice.Message.ID); err != nil {
		t.Fatalf("process delayed voice message: %v", err)
	}

	var deliveryCount, inboxCount int
	if err := testPool.QueryRow(ctx, `
		SELECT
		  (SELECT count(*) FROM agent_message_delivery WHERE agent_id = $1 AND message_id = $2),
		  (SELECT count(*) FROM agent_inbox_event WHERE agent_id = $1 AND source_message_id = $2)`,
		agentID, voice.Message.ID).Scan(&deliveryCount, &inboxCount); err != nil {
		t.Fatalf("count delayed voice delivery: %v", err)
	}
	if deliveryCount != 1 || inboxCount != 0 {
		t.Fatalf("delayed voice delivery/inbox = %d/%d, want canonical delivery only", deliveryCount, inboxCount)
	}
}

func testPCM16MonoWAV(pcm []byte, sampleRate uint32) []byte {
	wav := make([]byte, 44+len(pcm))
	copy(wav[0:4], "RIFF")
	binary.LittleEndian.PutUint32(wav[4:8], uint32(len(wav)-8))
	copy(wav[8:12], "WAVE")
	copy(wav[12:16], "fmt ")
	binary.LittleEndian.PutUint32(wav[16:20], 16)
	binary.LittleEndian.PutUint16(wav[20:22], 1)
	binary.LittleEndian.PutUint16(wav[22:24], 1)
	binary.LittleEndian.PutUint32(wav[24:28], sampleRate)
	binary.LittleEndian.PutUint32(wav[28:32], sampleRate*2)
	binary.LittleEndian.PutUint16(wav[32:34], 2)
	binary.LittleEndian.PutUint16(wav[34:36], 16)
	copy(wav[36:40], "data")
	binary.LittleEndian.PutUint32(wav[40:44], uint32(len(pcm)))
	copy(wav[44:], pcm)
	return wav
}
