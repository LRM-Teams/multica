package handler

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/integrations/doubaospeech"
	"github.com/multica-ai/multica/server/internal/messageparts"
	"github.com/multica-ai/multica/server/internal/voiceaudio"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

const (
	channelVoiceTranscriptionBatchLimit  = int32(4)
	channelVoiceTranscriptionMaxAttempts = 3
	channelVoiceClaimStaleAfter          = 2 * time.Minute
	maxVoiceWAVBytes                     = maxVoiceRecordingPCMBytes + 64*1024
)

var errInvalidChannelVoiceTranscript = errors.New("invalid channel voice transcript")

type claimedChannelVoiceTranscription struct {
	MessageID    pgtype.UUID
	WorkspaceID  pgtype.UUID
	ChannelID    pgtype.UUID
	AttachmentID pgtype.UUID
	Status       string
	Attempts     int
}

type channelVoiceRecording struct {
	URL         string
	ContentType string
	SizeBytes   int64
}

func pendingChannelVoiceAttachmentID(parts []protocol.MessagePart) (string, bool) {
	for _, part := range parts {
		if part.Type == protocol.MessagePartTypeVoice &&
			part.TranscriptionStatus == protocol.VoiceTranscriptionPending &&
			strings.TrimSpace(part.AttachmentID) != "" {
			return strings.TrimSpace(part.AttachmentID), true
		}
	}
	return "", false
}

func channelMessageNeedsVoiceTranscription(parts []protocol.MessagePart) bool {
	_, ok := pendingChannelVoiceAttachmentID(parts)
	return ok
}

// ProcessDueChannelVoiceTranscriptions drains the persisted voice queue. New
// messages also call the same processor immediately after their HTTP ack; this
// sweep owns crash recovery and provider retry.
func (h *Handler) ProcessDueChannelVoiceTranscriptions(ctx context.Context, limit int32) (int, error) {
	if h == nil || h.DB == nil {
		return 0, errors.New("voice transcription database is unavailable")
	}
	if limit <= 0 || limit > channelVoiceTranscriptionBatchLimit {
		limit = channelVoiceTranscriptionBatchLimit
	}
	rows, err := h.DB.Query(ctx, `
		SELECT message_id
		FROM channel_voice_transcription
		WHERE
		  (status IN ('pending', 'retry') AND next_attempt_at <= now())
		  OR status = 'transcribed'
		  OR (status IN ('processing', 'dispatching') AND claimed_at < now() - $1::interval)
		ORDER BY next_attempt_at, created_at, message_id
		LIMIT $2`,
		channelVoiceClaimStaleAfter.String(), limit)
	if err != nil {
		return 0, fmt.Errorf("list due channel voice transcriptions: %w", err)
	}
	var messageIDs []string
	for rows.Next() {
		var messageID pgtype.UUID
		if err := rows.Scan(&messageID); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scan due channel voice transcription: %w", err)
		}
		messageIDs = append(messageIDs, uuidToString(messageID))
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, fmt.Errorf("iterate due channel voice transcriptions: %w", err)
	}
	rows.Close()

	processed := 0
	for _, messageID := range messageIDs {
		if err := h.processChannelVoiceTranscription(ctx, messageID); err != nil {
			slog.Error("channel voice transcription processing failed", "message_id", messageID, "error", err)
			continue
		}
		processed++
	}
	return processed, nil
}

func (h *Handler) processChannelVoiceTranscription(ctx context.Context, messageID string) error {
	job, claimed, err := h.claimChannelVoiceTranscription(ctx, parseUUID(messageID))
	if err != nil || !claimed {
		return err
	}
	if job.Status == "dispatching" {
		return h.dispatchTranscribedChannelVoiceMessage(ctx, job)
	}
	if h.VoiceProvider == nil || !h.VoiceProvider.IsConfigured() || h.Storage == nil {
		return h.rescheduleUnavailableChannelVoiceTranscription(ctx, job)
	}

	recording, err := h.loadChannelVoiceRecording(ctx, job)
	if err != nil {
		return h.failChannelVoiceTranscription(ctx, job, "recording_unavailable", true, err)
	}
	pcm, err := h.readChannelVoicePCM(ctx, recording)
	if err != nil {
		return h.failChannelVoiceTranscription(ctx, job, "invalid_recording", false, err)
	}
	transcript, err := h.VoiceProvider.Transcribe(ctx, doubaospeech.TranscriptionRequest{
		PCM: pcm, SampleRate: 16000,
	})
	if err != nil {
		return h.failChannelVoiceTranscription(ctx, job, "provider_failed", true, err)
	}
	text := strings.TrimSpace(transcript.Text)
	if text == "" {
		return h.failChannelVoiceTranscription(ctx, job, "no_speech", false, errors.New("voice provider returned an empty transcript"))
	}
	if utf8.RuneCountInString(text) > protocol.VoiceTranscriptMaxRunes {
		return h.failChannelVoiceTranscription(ctx, job, "transcript_too_large", false, errors.New("voice transcript exceeds message limit"))
	}
	if err := h.persistChannelVoiceTranscript(ctx, job, text); err != nil {
		if errors.Is(err, errInvalidChannelVoiceTranscript) {
			return h.failChannelVoiceTranscription(ctx, job, "invalid_transcript", false, err)
		}
		return err
	}
	// Claim the durable transcribed state before dispatching. If the process
	// exits after persistence, the scheduler resumes from this exact boundary.
	return h.processChannelVoiceTranscription(ctx, messageID)
}

func (h *Handler) claimChannelVoiceTranscription(ctx context.Context, messageID pgtype.UUID) (claimedChannelVoiceTranscription, bool, error) {
	row := h.DB.QueryRow(ctx, `
		UPDATE channel_voice_transcription
		SET
		  status = CASE
		    WHEN status IN ('transcribed', 'dispatching') THEN 'dispatching'
		    ELSE 'processing'
		  END,
		  attempts = CASE
		    WHEN status IN ('transcribed', 'dispatching') THEN attempts
		    ELSE attempts + 1
		  END,
		  claimed_at = now(),
		  updated_at = now()
		WHERE message_id = $1
		  AND (
		    (status IN ('pending', 'retry') AND next_attempt_at <= now())
		    OR status = 'transcribed'
		    OR (status IN ('processing', 'dispatching') AND claimed_at < now() - $2::interval)
		  )
		RETURNING message_id, workspace_id, channel_id, attachment_id, status, attempts`,
		messageID, channelVoiceClaimStaleAfter.String())
	var job claimedChannelVoiceTranscription
	err := row.Scan(&job.MessageID, &job.WorkspaceID, &job.ChannelID, &job.AttachmentID, &job.Status, &job.Attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return claimedChannelVoiceTranscription{}, false, nil
	}
	if err != nil {
		return claimedChannelVoiceTranscription{}, false, fmt.Errorf("claim channel voice transcription: %w", err)
	}
	return job, true, nil
}

func (h *Handler) loadChannelVoiceRecording(ctx context.Context, job claimedChannelVoiceTranscription) (channelVoiceRecording, error) {
	var recording channelVoiceRecording
	err := h.DB.QueryRow(ctx, `
		SELECT attachment.url, attachment.content_type, attachment.size_bytes
		FROM channel_voice_transcription transcription
		JOIN attachment
		  ON attachment.id = transcription.attachment_id
		 AND attachment.workspace_id = transcription.workspace_id
		JOIN channel_message_attachment reference
		  ON reference.attachment_id = attachment.id
		 AND reference.workspace_id = transcription.workspace_id
		 AND reference.channel_message_id = transcription.message_id
		WHERE transcription.message_id = $1
		  AND transcription.attachment_id = $2`,
		job.MessageID, job.AttachmentID).Scan(&recording.URL, &recording.ContentType, &recording.SizeBytes)
	if err != nil {
		return channelVoiceRecording{}, fmt.Errorf("load recorded voice attachment: %w", err)
	}
	return recording, nil
}

func (h *Handler) readChannelVoicePCM(ctx context.Context, recording channelVoiceRecording) ([]byte, error) {
	mediaType, _, err := mime.ParseMediaType(recording.ContentType)
	if err != nil || !isWAVMediaType(mediaType) {
		return nil, fmt.Errorf("recorded voice content type %q is not audio/wav", recording.ContentType)
	}
	if recording.SizeBytes <= 0 || recording.SizeBytes > maxVoiceWAVBytes {
		return nil, fmt.Errorf("recorded voice size %d is outside the accepted range", recording.SizeBytes)
	}
	reader, err := h.Storage.GetReader(ctx, h.Storage.KeyFromURL(recording.URL))
	if err != nil {
		return nil, fmt.Errorf("open recorded voice: %w", err)
	}
	defer reader.Close()
	body, err := io.ReadAll(io.LimitReader(reader, maxVoiceWAVBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read recorded voice: %w", err)
	}
	if len(body) > maxVoiceWAVBytes {
		return nil, errors.New("recorded voice exceeds size limit")
	}
	return voiceaudio.DecodePCM16MonoWAV(body, 16000, maxVoiceRecordingPCMBytes)
}

func isWAVMediaType(mediaType string) bool {
	switch strings.ToLower(strings.TrimSpace(mediaType)) {
	case "audio/wav", "audio/wave", "audio/x-wav", "audio/vnd.wave":
		return true
	default:
		return false
	}
}

func (h *Handler) persistChannelVoiceTranscript(ctx context.Context, job claimedChannelVoiceTranscription, transcript string) error {
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin channel voice transcript transaction: %w", err)
	}
	defer tx.Rollback(ctx)
	msg, err := scanChannelMessage(tx.QueryRow(ctx, `
		SELECT id, channel_id, workspace_id, author_type, author_id, author_name, content, parts, source, external_message_id, client_message_id, reply_to_message_id, quote_message_id, quote_snapshot, thread_root_message_id, thread_id, trigger_depth, seq, created_at, edited_at, deleted_at
		FROM channel_message
		WHERE id = $1 AND workspace_id = $2 AND channel_id = $3
		FOR UPDATE`, job.MessageID, job.WorkspaceID, job.ChannelID))
	if err != nil {
		return fmt.Errorf("load channel voice message for transcript: %w", err)
	}
	ch, found := h.getChannel(ctx, uuidToString(job.WorkspaceID), job.ChannelID)
	if !found {
		return errors.New("channel voice message destination no longer exists")
	}
	msg.Content, msg.Parts, err = h.enrichChannelMessageMentions(
		ctx,
		ch,
		transcript,
		channelVoicePartsWithTranscript(msg.Parts, transcript),
	)
	if err != nil {
		return fmt.Errorf("%w: %v", errInvalidChannelVoiceTranscript, err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE channel_message
		SET content = $2, parts = $3::jsonb
		WHERE id = $1`,
		job.MessageID, transcript, messageparts.MustJSON(msg.Parts)); err != nil {
		return fmt.Errorf("persist channel voice transcript: %w", err)
	}
	tag, err := tx.Exec(ctx, `
		UPDATE channel_voice_transcription
		SET status = 'transcribed', transcript = $2, claimed_at = NULL,
		    last_error_code = '', updated_at = now()
		WHERE message_id = $1 AND status = 'processing'`,
		job.MessageID, transcript)
	if err != nil {
		return fmt.Errorf("mark channel voice transcription transcribed: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return errors.New("channel voice transcription claim was lost before persistence")
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit channel voice transcript: %w", err)
	}
	h.publishChannelVoiceMessageUpdate(ctx, msg)
	return nil
}

func channelVoicePartsWithTranscript(parts []protocol.MessagePart, transcript string) []protocol.MessagePart {
	out := make([]protocol.MessagePart, 0, len(parts)+1)
	if text := strings.TrimSpace(transcript); text != "" {
		out = append(out, protocol.MessagePart{Type: protocol.MessagePartTypeText, Text: text})
	}
	for _, part := range parts {
		if part.Type == protocol.MessagePartTypeText {
			continue
		}
		if part.Type == protocol.MessagePartTypeVoice && part.AttachmentID != "" {
			part.TranscriptionStatus = protocol.VoiceTranscriptionCompleted
		}
		out = append(out, part)
	}
	return out
}

func (h *Handler) failChannelVoiceTranscription(ctx context.Context, job claimedChannelVoiceTranscription, errorCode string, retryable bool, cause error) error {
	terminal := !retryable || job.Attempts >= channelVoiceTranscriptionMaxAttempts
	if !terminal {
		delay := 5 * time.Second
		if job.Attempts > 1 {
			delay = 30 * time.Second
		}
		_, err := h.DB.Exec(ctx, `
			UPDATE channel_voice_transcription
			SET status = 'retry', next_attempt_at = now() + $2::interval,
			    claimed_at = NULL, last_error_code = $3, updated_at = now()
			WHERE message_id = $1 AND status = 'processing'`,
			job.MessageID, delay.String(), errorCode)
		if err != nil {
			return fmt.Errorf("reschedule channel voice transcription: %w", err)
		}
		slog.Warn("channel voice transcription scheduled for retry",
			"message_id", uuidToString(job.MessageID), "attempt", job.Attempts, "error_code", errorCode)
		return nil
	}

	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin failed channel voice transcription transaction: %w", err)
	}
	defer tx.Rollback(ctx)
	msg, err := scanChannelMessage(tx.QueryRow(ctx, `
		SELECT id, channel_id, workspace_id, author_type, author_id, author_name, content, parts, source, external_message_id, client_message_id, reply_to_message_id, quote_message_id, quote_snapshot, thread_root_message_id, thread_id, trigger_depth, seq, created_at, edited_at, deleted_at
		FROM channel_message
		WHERE id = $1 AND workspace_id = $2 AND channel_id = $3
		FOR UPDATE`, job.MessageID, job.WorkspaceID, job.ChannelID))
	if err != nil {
		return fmt.Errorf("load failed channel voice message: %w", err)
	}
	for i := range msg.Parts {
		if msg.Parts[i].Type == protocol.MessagePartTypeVoice && msg.Parts[i].AttachmentID != "" {
			msg.Parts[i].TranscriptionStatus = protocol.VoiceTranscriptionFailed
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE channel_message SET parts = $2::jsonb WHERE id = $1`,
		job.MessageID, messageparts.MustJSON(msg.Parts)); err != nil {
		return fmt.Errorf("persist failed channel voice state: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE channel_voice_transcription
		SET status = 'failed', claimed_at = NULL, last_error_code = $2, updated_at = now()
		WHERE message_id = $1 AND status = 'processing'`,
		job.MessageID, errorCode); err != nil {
		return fmt.Errorf("mark channel voice transcription failed: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit failed channel voice transcription: %w", err)
	}
	h.publishChannelVoiceMessageUpdate(ctx, msg)
	slog.Error("channel voice transcription exhausted",
		"message_id", uuidToString(job.MessageID), "attempt", job.Attempts, "error_code", errorCode, "error", cause)
	return nil
}

func (h *Handler) rescheduleUnavailableChannelVoiceTranscription(ctx context.Context, job claimedChannelVoiceTranscription) error {
	tag, err := h.DB.Exec(ctx, `
		UPDATE channel_voice_transcription
		SET status = 'retry', attempts = GREATEST(attempts - 1, 0),
		    next_attempt_at = now() + interval '30 seconds', claimed_at = NULL,
		    last_error_code = 'service_unavailable', updated_at = now()
		WHERE message_id = $1 AND status = 'processing'`,
		job.MessageID)
	if err != nil {
		return fmt.Errorf("reschedule unavailable channel voice transcription: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return errors.New("channel voice transcription claim was lost while rescheduling")
	}
	return nil
}

func (h *Handler) publishChannelVoiceMessageUpdate(ctx context.Context, msg ChannelMessageResponse) {
	if msg.AuthorID == nil {
		return
	}
	msg = h.attachSingleChannelMessageDetails(ctx, msg.WorkspaceID, parseUUID(*msg.AuthorID), msg)
	h.publishChannelToMembers(ctx, protocol.EventChannelMessageUpdated, msg.WorkspaceID, "member", *msg.AuthorID, parseUUID(msg.ChannelID), msg)
}

func (h *Handler) dispatchTranscribedChannelVoiceMessage(ctx context.Context, job claimedChannelVoiceTranscription) error {
	msg, found := h.channelMessageByID(ctx, uuidToString(job.WorkspaceID), uuidToString(job.ChannelID), uuidToString(job.MessageID))
	if !found {
		return errors.New("transcribed channel voice message no longer exists")
	}
	if msg.AuthorID == nil || msg.Type != "user" || strings.TrimSpace(msg.Content) == "" {
		return errors.New("transcribed channel voice message is not dispatchable")
	}
	ch, found := h.getChannel(ctx, uuidToString(job.WorkspaceID), job.ChannelID)
	if !found {
		return errors.New("transcribed channel voice channel no longer exists")
	}
	h.followChannelThreadMentionedUsers(ctx, ch, msg)
	h.dispatchTranscribedChannelVoiceMessageSideEffects(ctx, ch, msg, parseUUID(*msg.AuthorID))
	tag, err := h.DB.Exec(ctx, `
		UPDATE channel_voice_transcription
		SET status = 'completed', claimed_at = NULL, updated_at = now()
		WHERE message_id = $1 AND status = 'dispatching'`,
		job.MessageID)
	if err != nil {
		return fmt.Errorf("complete channel voice transcription dispatch: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return errors.New("channel voice dispatch claim was lost")
	}
	return nil
}

func (h *Handler) dispatchHumanChannelMessageSideEffects(ctx context.Context, ch ChannelResponse, msg ChannelMessageResponse, initiatorUserID pgtype.UUID) {
	h.dispatchHumanChannelMessageSideEffectsWithVoiceReplay(ctx, ch, msg, initiatorUserID, false)
}

func (h *Handler) dispatchTranscribedChannelVoiceMessageSideEffects(ctx context.Context, ch ChannelResponse, msg ChannelMessageResponse, initiatorUserID pgtype.UUID) {
	h.dispatchHumanChannelMessageSideEffectsWithVoiceReplay(ctx, ch, msg, initiatorUserID, true)
}

func (h *Handler) dispatchHumanChannelMessageSideEffectsWithVoiceReplay(ctx context.Context, ch ChannelResponse, msg ChannelMessageResponse, initiatorUserID pgtype.UUID, replayTranscribedVoice bool) {
	if msg.ThreadRootMessageID != nil {
		h.dispatchChannelThreadReplyMentions(ctx, ch, msg, initiatorUserID)
	} else if ch.Kind != "dm" {
		h.ingestWendyHumanGroupMessage(ctx, ch, msg)
		if replayTranscribedVoice {
			h.dispatchTranscribedChannelMessageToAgents(ctx, ch, msg, initiatorUserID)
		} else {
			h.dispatchChannelMessageToAgents(ctx, ch, msg, initiatorUserID)
		}
	}
	h.sendChannelMessageToFeishu(ctx, ch, msg.AuthorName, msg.Content)
}
