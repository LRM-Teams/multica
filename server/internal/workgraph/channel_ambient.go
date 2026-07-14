package workgraph

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// AmbientDebounce is how long Wendy waits after the latest human message
// before reviewing a group channel. Tests may shorten it.
var AmbientDebounce = 10 * time.Minute

type ChannelAmbientWatch struct {
	ChannelID             pgtype.UUID
	WorkspaceID           pgtype.UUID
	WendyAgentID          pgtype.UUID
	LastHumanMessageAt    time.Time
	LastHumanMessageID    pgtype.UUID
	LastReviewedMessageAt pgtype.Timestamptz
	ReviewNotBefore       time.Time
	Dirty                 bool
	Status                string
}

// TouchChannelAmbient marks a Wendy-watched group as dirty and pushes the
// review deadline to now+debounce. Repeated chatter resets the timer.
func (s *Store) TouchChannelAmbient(ctx context.Context, workspaceID, channelID, wendyAgentID, messageID pgtype.UUID, messageAt time.Time) error {
	if messageAt.IsZero() {
		messageAt = time.Now()
	}
	notBefore := messageAt.Add(AmbientDebounce)
	_, err := s.pool.Exec(ctx, `
		INSERT INTO wendy_channel_ambient (
		  channel_id, workspace_id, wendy_agent_id,
		  last_human_message_at, last_human_message_id,
		  review_not_before, dirty, status
		) VALUES ($1, $2, $3, $4, $5, $6, TRUE, 'idle')
		ON CONFLICT (channel_id) DO UPDATE SET
		  wendy_agent_id = EXCLUDED.wendy_agent_id,
		  last_human_message_at = GREATEST(wendy_channel_ambient.last_human_message_at, EXCLUDED.last_human_message_at),
		  last_human_message_id = CASE
		    WHEN EXCLUDED.last_human_message_at >= wendy_channel_ambient.last_human_message_at
		      THEN EXCLUDED.last_human_message_id
		    ELSE wendy_channel_ambient.last_human_message_id
		  END,
		  review_not_before = GREATEST(wendy_channel_ambient.review_not_before, EXCLUDED.review_not_before),
		  dirty = TRUE,
		  status = CASE
		    WHEN wendy_channel_ambient.status = 'running' THEN wendy_channel_ambient.status
		    ELSE 'idle'
		  END,
		  claim_token = CASE
		    WHEN wendy_channel_ambient.status = 'running' THEN wendy_channel_ambient.claim_token
		    ELSE NULL
		  END,
		  claimed_at = CASE
		    WHEN wendy_channel_ambient.status = 'running' THEN wendy_channel_ambient.claimed_at
		    ELSE NULL
		  END,
		  updated_at = now()
	`, channelID, workspaceID, wendyAgentID, messageAt, nullableUUID(messageID), notBefore)
	if err != nil {
		return fmt.Errorf("touch wendy channel ambient: %w", err)
	}
	return nil
}

func nullableUUID(id pgtype.UUID) any {
	if !id.Valid {
		return nil
	}
	return id
}

// ClaimDueChannelAmbient claims channels that have new human chatter past the
// debounce window and have not been reviewed since that chatter.
func (s *Store) ClaimDueChannelAmbient(ctx context.Context, limit int32) ([]ChannelAmbientWatch, pgtype.UUID, error) {
	if limit <= 0 {
		return nil, pgtype.UUID{}, nil
	}
	claimToken := pgtype.UUID{Bytes: uuid.New(), Valid: true}
	rows, err := s.pool.Query(ctx, `
		UPDATE wendy_channel_ambient w
		SET status = 'claimed',
		    claim_token = $1,
		    claimed_at = now(),
		    updated_at = now()
		WHERE w.channel_id IN (
		  SELECT channel_id
		  FROM wendy_channel_ambient
		  WHERE dirty = TRUE
		    AND status = 'idle'
		    AND review_not_before <= now()
		    AND (
		      last_reviewed_message_at IS NULL
		      OR last_human_message_at > last_reviewed_message_at
		    )
		  ORDER BY review_not_before ASC, updated_at ASC
		  LIMIT $2
		  FOR UPDATE SKIP LOCKED
		)
		RETURNING
		  w.channel_id, w.workspace_id, w.wendy_agent_id,
		  w.last_human_message_at, w.last_human_message_id,
		  w.last_reviewed_message_at, w.review_not_before,
		  w.dirty, w.status
	`, claimToken, limit)
	if err != nil {
		return nil, pgtype.UUID{}, fmt.Errorf("claim due wendy channel ambient: %w", err)
	}
	defer rows.Close()

	var watches []ChannelAmbientWatch
	for rows.Next() {
		var watch ChannelAmbientWatch
		if err := rows.Scan(
			&watch.ChannelID,
			&watch.WorkspaceID,
			&watch.WendyAgentID,
			&watch.LastHumanMessageAt,
			&watch.LastHumanMessageID,
			&watch.LastReviewedMessageAt,
			&watch.ReviewNotBefore,
			&watch.Dirty,
			&watch.Status,
		); err != nil {
			return nil, pgtype.UUID{}, fmt.Errorf("scan claimed ambient: %w", err)
		}
		watches = append(watches, watch)
	}
	if err := rows.Err(); err != nil {
		return nil, pgtype.UUID{}, err
	}
	return watches, claimToken, nil
}

// MarkChannelAmbientReviewed clears dirty after a successful review enqueue.
func (s *Store) MarkChannelAmbientReviewed(ctx context.Context, channelID, claimToken pgtype.UUID, reviewedAt time.Time, radarRunID pgtype.UUID) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE wendy_channel_ambient
		SET dirty = FALSE,
		    status = 'idle',
		    claim_token = NULL,
		    claimed_at = NULL,
		    last_reviewed_message_at = $3,
		    active_radar_run_id = $4,
		    updated_at = now()
		WHERE channel_id = $1
		  AND claim_token = $2
		  AND status = 'claimed'
	`, channelID, claimToken, reviewedAt, nullableUUID(radarRunID))
	if err != nil {
		return fmt.Errorf("mark wendy channel ambient reviewed: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return errors.New("ambient claim lost")
	}
	return nil
}

// ReturnChannelAmbientForRetry releases a claimed ambient row back to idle/dirty.
func (s *Store) ReturnChannelAmbientForRetry(ctx context.Context, channelID, claimToken pgtype.UUID) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE wendy_channel_ambient
		SET status = 'idle',
		    claim_token = NULL,
		    claimed_at = NULL,
		    dirty = TRUE,
		    updated_at = now()
		WHERE channel_id = $1
		  AND claim_token = $2
		  AND status = 'claimed'
	`, channelID, claimToken)
	if err != nil {
		return fmt.Errorf("return wendy channel ambient for retry: %w", err)
	}
	return nil
}

// CancelChannelAmbientClaim drops a claimed ambient without retrying.
func (s *Store) CancelChannelAmbientClaim(ctx context.Context, channelID, claimToken pgtype.UUID, reason string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE wendy_channel_ambient
		SET dirty = FALSE,
		    status = 'idle',
		    claim_token = NULL,
		    claimed_at = NULL,
		    last_reviewed_message_at = COALESCE(last_human_message_at, now()),
		    updated_at = now()
		WHERE channel_id = $1
		  AND claim_token = $2
		  AND status = 'claimed'
	`, channelID, claimToken)
	if err != nil {
		return fmt.Errorf("cancel wendy channel ambient (%s): %w", reason, err)
	}
	return nil
}
