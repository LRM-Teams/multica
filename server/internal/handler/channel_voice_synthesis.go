package handler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/integrations/doubaospeech"
	"github.com/multica-ai/multica/server/internal/messageparts"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

const (
	channelVoiceSynthesisBatchLimit  = int32(4)
	channelVoiceSynthesisMaxAttempts = 3
	channelVoiceSynthesisStaleAfter  = 2 * time.Minute
)

type claimedChannelVoiceSynthesis struct {
	MessageID    pgtype.UUID
	WorkspaceID  pgtype.UUID
	ChannelID    pgtype.UUID
	AttachmentID pgtype.UUID
	Attempts     int
}

func channelMessageNeedsVoiceSynthesis(parts []protocol.MessagePart) bool {
	for _, part := range parts {
		if part.Type == protocol.MessagePartTypeVoice &&
			part.SynthesisStatus == protocol.VoiceSynthesisPending {
			return true
		}
	}
	return false
}

// ProcessDueChannelVoiceSyntheses drains the persisted Agent TTS queue.
// Realtime publication also starts the same processor immediately; this sweep
// owns restart recovery and provider retry.
func (h *Handler) ProcessDueChannelVoiceSyntheses(ctx context.Context, limit int32) (int, error) {
	if h == nil || h.DB == nil {
		return 0, errors.New("voice synthesis database is unavailable")
	}
	if limit <= 0 || limit > channelVoiceSynthesisBatchLimit {
		limit = channelVoiceSynthesisBatchLimit
	}
	rows, err := h.DB.Query(ctx, `
		SELECT message_id
		FROM channel_voice_synthesis
		WHERE
		  (status IN ('pending', 'retry') AND next_attempt_at <= now())
		  OR (status = 'processing' AND claimed_at < now() - $1::interval)
		ORDER BY next_attempt_at, created_at, message_id
		LIMIT $2`,
		channelVoiceSynthesisStaleAfter.String(), limit)
	if err != nil {
		return 0, fmt.Errorf("list due channel voice syntheses: %w", err)
	}
	var messageIDs []string
	for rows.Next() {
		var messageID pgtype.UUID
		if err := rows.Scan(&messageID); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scan due channel voice synthesis: %w", err)
		}
		messageIDs = append(messageIDs, uuidToString(messageID))
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, fmt.Errorf("iterate due channel voice syntheses: %w", err)
	}
	rows.Close()

	processed := 0
	for _, messageID := range messageIDs {
		if err := h.processChannelVoiceSynthesis(ctx, messageID); err != nil {
			slog.Error("channel voice synthesis processing failed", "message_id", messageID, "error", err)
			continue
		}
		processed++
	}
	return processed, nil
}

func (h *Handler) processChannelVoiceSynthesis(ctx context.Context, messageID string) error {
	job, claimed, err := h.claimChannelVoiceSynthesis(ctx, parseUUID(messageID))
	if err != nil || !claimed {
		return err
	}
	if h.VoiceProvider == nil || !h.VoiceProvider.IsConfigured() || h.Storage == nil || h.TxStarter == nil {
		return h.rescheduleUnavailableChannelVoiceSynthesis(ctx, job)
	}

	msg, err := h.loadChannelVoiceSynthesisMessage(ctx, job)
	if err != nil {
		return h.failChannelVoiceSynthesis(ctx, job, "message_unavailable", false, err)
	}
	text := strings.TrimSpace(msg.Content)
	if msg.Type != "agent" || msg.AuthorID == nil || text == "" || !channelMessageNeedsVoiceSynthesis(msg.Parts) {
		return h.failChannelVoiceSynthesis(ctx, job, "invalid_message", false, errors.New("queued message is not pending Agent voice output"))
	}
	audio, err := h.VoiceProvider.Synthesize(ctx, doubaospeech.SynthesisRequest{
		Text: text, Format: "pcm", SampleRate: 24000,
	})
	if err != nil {
		return h.failChannelVoiceSynthesis(ctx, job, "provider_failed", true, err)
	}
	wav, durationMS, err := encodeSynthesizedPCM16WAV(audio)
	if err != nil {
		return h.failChannelVoiceSynthesis(ctx, job, "invalid_audio", false, err)
	}
	filename := "voice-" + uuidToString(job.MessageID) + ".wav"
	key := fmt.Sprintf(
		"workspaces/%s/channel-voice/%s.wav",
		uuidToString(job.WorkspaceID),
		uuidToString(job.AttachmentID),
	)
	link, err := h.Storage.Upload(ctx, key, wav, "audio/wav", filename)
	if err != nil {
		return h.failChannelVoiceSynthesis(ctx, job, "storage_failed", true, err)
	}
	if err := h.persistChannelVoiceSynthesis(ctx, job, msg, link, filename, int64(len(wav)), durationMS); err != nil {
		return err
	}
	return nil
}

func (h *Handler) claimChannelVoiceSynthesis(ctx context.Context, messageID pgtype.UUID) (claimedChannelVoiceSynthesis, bool, error) {
	row := h.DB.QueryRow(ctx, `
		UPDATE channel_voice_synthesis
		SET status = 'processing', attempts = attempts + 1,
		    claimed_at = now(), updated_at = now()
		WHERE message_id = $1
		  AND (
		    (status IN ('pending', 'retry') AND next_attempt_at <= now())
		    OR (status = 'processing' AND claimed_at < now() - $2::interval)
		  )
		RETURNING message_id, workspace_id, channel_id, attachment_id, attempts`,
		messageID, channelVoiceSynthesisStaleAfter.String())
	var job claimedChannelVoiceSynthesis
	err := row.Scan(&job.MessageID, &job.WorkspaceID, &job.ChannelID, &job.AttachmentID, &job.Attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return claimedChannelVoiceSynthesis{}, false, nil
	}
	if err != nil {
		return claimedChannelVoiceSynthesis{}, false, fmt.Errorf("claim channel voice synthesis: %w", err)
	}
	return job, true, nil
}

func (h *Handler) loadChannelVoiceSynthesisMessage(ctx context.Context, job claimedChannelVoiceSynthesis) (ChannelMessageResponse, error) {
	msg, err := scanChannelMessage(h.DB.QueryRow(ctx, `
		SELECT id, channel_id, workspace_id, author_type, author_id, author_name, content, parts, source, external_message_id, client_message_id, reply_to_message_id, quote_message_id, quote_snapshot, thread_root_message_id, thread_id, trigger_depth, seq, created_at, edited_at, deleted_at
		FROM channel_message
		WHERE id = $1 AND workspace_id = $2 AND channel_id = $3`,
		job.MessageID, job.WorkspaceID, job.ChannelID))
	if err != nil {
		return ChannelMessageResponse{}, fmt.Errorf("load Agent voice message: %w", err)
	}
	return msg, nil
}

func (h *Handler) persistChannelVoiceSynthesis(
	ctx context.Context,
	job claimedChannelVoiceSynthesis,
	msg ChannelMessageResponse,
	link string,
	filename string,
	sizeBytes int64,
	durationMS int64,
) error {
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin channel voice synthesis transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	locked, err := scanChannelMessage(tx.QueryRow(ctx, `
		SELECT id, channel_id, workspace_id, author_type, author_id, author_name, content, parts, source, external_message_id, client_message_id, reply_to_message_id, quote_message_id, quote_snapshot, thread_root_message_id, thread_id, trigger_depth, seq, created_at, edited_at, deleted_at
		FROM channel_message
		WHERE id = $1 AND workspace_id = $2 AND channel_id = $3
		FOR UPDATE`, job.MessageID, job.WorkspaceID, job.ChannelID))
	if err != nil {
		return fmt.Errorf("lock Agent voice message: %w", err)
	}
	if locked.AuthorID == nil || msg.AuthorID == nil || *locked.AuthorID != *msg.AuthorID ||
		locked.Content != msg.Content || locked.DeletedAt != nil ||
		!channelMessageNeedsVoiceSynthesis(locked.Parts) {
		return errors.New("Agent voice message changed before synthesis persistence")
	}
	locked.Parts = channelVoicePartsWithSynthesis(
		locked.Parts,
		uuidToString(job.AttachmentID),
		filename,
		sizeBytes,
		durationMS,
	)
	if _, err := tx.Exec(ctx, `
		INSERT INTO attachment (
		  id, workspace_id, channel_id, uploader_type,
		  uploader_id, filename, url, content_type, size_bytes
		)
		VALUES ($1, $2, $3, 'agent', $4, $5, $6, 'audio/wav', $7)`,
		job.AttachmentID, job.WorkspaceID, job.ChannelID,
		parseUUID(*locked.AuthorID), filename, link, sizeBytes); err != nil {
		return fmt.Errorf("persist Agent voice attachment: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO channel_message_attachment (
		  workspace_id, channel_message_id, attachment_id
		) VALUES ($1, $2, $3)`,
		job.WorkspaceID, job.MessageID, job.AttachmentID); err != nil {
		return fmt.Errorf("link Agent voice attachment: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE channel_message
		SET parts = $2::jsonb
		WHERE id = $1`,
		job.MessageID, messageparts.MustJSON(locked.Parts)); err != nil {
		return fmt.Errorf("persist Agent voice message parts: %w", err)
	}
	tag, err := tx.Exec(ctx, `
		UPDATE channel_voice_synthesis
		SET status = 'completed', claimed_at = NULL, last_error_code = '',
		    updated_at = now()
		WHERE message_id = $1 AND status = 'processing'`,
		job.MessageID)
	if err != nil {
		return fmt.Errorf("complete channel voice synthesis: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return errors.New("channel voice synthesis claim was lost before persistence")
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit channel voice synthesis: %w", err)
	}

	locked = h.attachSingleChannelMessageDetails(ctx, locked.WorkspaceID, parseUUID(*locked.AuthorID), locked)
	h.publishChannelToMembers(
		ctx,
		protocol.EventChannelMessageUpdated,
		locked.WorkspaceID,
		"agent",
		*locked.AuthorID,
		parseUUID(locked.ChannelID),
		locked,
	)
	return nil
}

func channelVoicePartsWithSynthesis(
	parts []protocol.MessagePart,
	attachmentID string,
	filename string,
	sizeBytes int64,
	durationMS int64,
) []protocol.MessagePart {
	out := append([]protocol.MessagePart(nil), parts...)
	for i := range out {
		if out[i].Type != protocol.MessagePartTypeVoice ||
			out[i].SynthesisStatus != protocol.VoiceSynthesisPending {
			continue
		}
		out[i].AttachmentID = attachmentID
		out[i].Filename = filename
		out[i].ContentType = "audio/wav"
		out[i].SizeBytes = sizeBytes
		out[i].DurationMS = durationMS
		out[i].SynthesisStatus = protocol.VoiceSynthesisCompleted
	}
	return out
}

func (h *Handler) failChannelVoiceSynthesis(
	ctx context.Context,
	job claimedChannelVoiceSynthesis,
	errorCode string,
	retryable bool,
	cause error,
) error {
	terminal := !retryable || job.Attempts >= channelVoiceSynthesisMaxAttempts
	if !terminal {
		delay := 5 * time.Second
		if job.Attempts > 1 {
			delay = 30 * time.Second
		}
		_, err := h.DB.Exec(ctx, `
			UPDATE channel_voice_synthesis
			SET status = 'retry', next_attempt_at = now() + $2::interval,
			    claimed_at = NULL, last_error_code = $3, updated_at = now()
			WHERE message_id = $1 AND status = 'processing'`,
			job.MessageID, delay.String(), errorCode)
		if err != nil {
			return fmt.Errorf("reschedule channel voice synthesis: %w", err)
		}
		slog.Warn(
			"channel voice synthesis scheduled for retry",
			"message_id", uuidToString(job.MessageID),
			"attempt", job.Attempts,
			"error_code", errorCode,
		)
		return nil
	}

	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin failed channel voice synthesis transaction: %w", err)
	}
	defer tx.Rollback(ctx)
	msg, err := scanChannelMessage(tx.QueryRow(ctx, `
		SELECT id, channel_id, workspace_id, author_type, author_id, author_name, content, parts, source, external_message_id, client_message_id, reply_to_message_id, quote_message_id, quote_snapshot, thread_root_message_id, thread_id, trigger_depth, seq, created_at, edited_at, deleted_at
		FROM channel_message
		WHERE id = $1 AND workspace_id = $2 AND channel_id = $3
		FOR UPDATE`, job.MessageID, job.WorkspaceID, job.ChannelID))
	if err != nil {
		return fmt.Errorf("load failed Agent voice message: %w", err)
	}
	for i := range msg.Parts {
		if msg.Parts[i].Type == protocol.MessagePartTypeVoice &&
			msg.Parts[i].SynthesisStatus == protocol.VoiceSynthesisPending {
			msg.Parts[i].SynthesisStatus = protocol.VoiceSynthesisFailed
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE channel_message SET parts = $2::jsonb WHERE id = $1`,
		job.MessageID, messageparts.MustJSON(msg.Parts)); err != nil {
		return fmt.Errorf("persist failed Agent voice state: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE channel_voice_synthesis
		SET status = 'failed', claimed_at = NULL, last_error_code = $2,
		    updated_at = now()
		WHERE message_id = $1 AND status = 'processing'`,
		job.MessageID, errorCode); err != nil {
		return fmt.Errorf("mark channel voice synthesis failed: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit failed channel voice synthesis: %w", err)
	}
	if msg.AuthorID != nil {
		msg = h.attachSingleChannelMessageDetails(ctx, msg.WorkspaceID, parseUUID(*msg.AuthorID), msg)
		h.publishChannelToMembers(
			ctx,
			protocol.EventChannelMessageUpdated,
			msg.WorkspaceID,
			"agent",
			*msg.AuthorID,
			parseUUID(msg.ChannelID),
			msg,
		)
	}
	slog.Error(
		"channel voice synthesis exhausted",
		"message_id", uuidToString(job.MessageID),
		"attempt", job.Attempts,
		"error_code", errorCode,
		"error", cause,
	)
	return nil
}

func (h *Handler) rescheduleUnavailableChannelVoiceSynthesis(ctx context.Context, job claimedChannelVoiceSynthesis) error {
	tag, err := h.DB.Exec(ctx, `
		UPDATE channel_voice_synthesis
		SET status = 'retry', attempts = GREATEST(attempts - 1, 0),
		    next_attempt_at = now() + interval '30 seconds', claimed_at = NULL,
		    last_error_code = 'service_unavailable', updated_at = now()
		WHERE message_id = $1 AND status = 'processing'`,
		job.MessageID)
	if err != nil {
		return fmt.Errorf("reschedule unavailable channel voice synthesis: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return errors.New("channel voice synthesis claim was lost while rescheduling")
	}
	return nil
}
