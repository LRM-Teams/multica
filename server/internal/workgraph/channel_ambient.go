package workgraph

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// AmbientDebounce is how long Wendy waits after the latest triggering group
// message (human or non-Wendy agent) before reviewing. Tests may shorten it.
var AmbientDebounce = 10 * time.Minute

// AmbientMaxWait caps debounce starvation (#1): under continuous chatter the
// per-message debounce would keep pushing review_not_before forward forever, so
// the busiest channels — the ones most likely to need coordination — would never
// be reviewed. A dirty channel is reviewed at most this long after its first
// unreviewed message, regardless of ongoing chatter. Tests may shorten it.
var AmbientMaxWait = 30 * time.Minute

// AmbientClaimStaleAfter re-arms ambient rows stuck in claimed/running (e.g. the
// radar run never completed because the daemon went away) so a review is not
// lost forever. Tests may shorten it.
var AmbientClaimStaleAfter = 15 * time.Minute

// AmbientRetryBackoff delays re-claim after a failed ambient review so a broken
// run does not hot-loop. Tests may shorten it.
var AmbientRetryBackoff = time.Minute

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
// review deadline to messageAt+debounce. Repeated human/agent chatter resets the
// per-message debounce, but the deadline is capped at
// first_dirty_message_at+AmbientMaxWait so a continuously busy channel is still
// reviewed (#1 debounce starvation).
func (s *Store) TouchChannelAmbient(ctx context.Context, workspaceID, channelID, wendyAgentID, messageID pgtype.UUID, messageAt time.Time) error {
	if messageAt.IsZero() {
		messageAt = time.Now()
	}
	notBefore := messageAt.Add(AmbientDebounce)
	_, err := s.pool.Exec(ctx, `
		INSERT INTO wendy_channel_ambient (
		  channel_id, workspace_id, wendy_agent_id,
		  last_human_message_at, last_human_message_id,
		  review_not_before, first_dirty_message_at, dirty, status
		) VALUES ($1, $2, $3, $4, $5, $6, $4, TRUE, 'idle')
		ON CONFLICT (channel_id) DO UPDATE SET
		  wendy_agent_id = EXCLUDED.wendy_agent_id,
		  last_human_message_at = GREATEST(wendy_channel_ambient.last_human_message_at, EXCLUDED.last_human_message_at),
		  last_human_message_id = CASE
		    WHEN EXCLUDED.last_human_message_at >= wendy_channel_ambient.last_human_message_at
		      THEN EXCLUDED.last_human_message_id
		    ELSE wendy_channel_ambient.last_human_message_id
		  END,
		  -- Anchor the current unreviewed streak: keep the earliest anchor while
		  -- still dirty, restart it when the channel was clean.
		  first_dirty_message_at = CASE
		    WHEN wendy_channel_ambient.dirty
		      THEN COALESCE(wendy_channel_ambient.first_dirty_message_at, EXCLUDED.first_dirty_message_at)
		    ELSE EXCLUDED.first_dirty_message_at
		  END,
		  -- Debounce, but never later than the streak anchor + max staleness.
		  review_not_before = LEAST(
		    GREATEST(wendy_channel_ambient.review_not_before, EXCLUDED.review_not_before),
		    (CASE
		      WHEN wendy_channel_ambient.dirty
		        THEN COALESCE(wendy_channel_ambient.first_dirty_message_at, EXCLUDED.first_dirty_message_at)
		      ELSE EXCLUDED.first_dirty_message_at
		    END) + make_interval(secs => $7)
		  ),
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
	`, channelID, workspaceID, wendyAgentID, messageAt, nullableUUID(messageID), notBefore, AmbientMaxWait.Seconds())
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

// ClaimDueChannelAmbient claims channels that have new triggering chatter past
// the debounce window and have not been reviewed since that chatter.
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

// MarkChannelAmbientRunning transitions a claimed row to running once the review
// radar run has been enqueued. Unlike the old enqueue-time clear, it keeps dirty
// set and records reviewing_message_at (the watermark this run covers) so the
// review can be settled precisely on completion — and re-armed on failure (#2).
func (s *Store) MarkChannelAmbientRunning(ctx context.Context, channelID, claimToken pgtype.UUID, reviewingAt time.Time, radarRunID pgtype.UUID) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE wendy_channel_ambient
		SET status = 'running',
		    active_radar_run_id = $4,
		    reviewing_message_at = $3,
		    updated_at = now()
		WHERE channel_id = $1
		  AND claim_token = $2
		  AND status = 'claimed'
	`, channelID, claimToken, reviewingAt, nullableUUID(radarRunID))
	if err != nil {
		return fmt.Errorf("mark wendy channel ambient running: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return errors.New("ambient claim lost")
	}
	return nil
}

// ReconcileChannelAmbientRun settles a running ambient review when its radar run
// reaches a terminal state (#2). On success it clears dirty up to the watermark
// the run covered (keeping dirty if newer chatter arrived mid-review). On failure
// it re-arms without advancing last_reviewed_message_at, with a short backoff so a
// broken run does not hot-loop.
func (s *Store) ReconcileChannelAmbientRun(ctx context.Context, radarRunID pgtype.UUID, success bool) error {
	if !radarRunID.Valid {
		return nil
	}
	var query string
	var args []any
	if success {
		query = `
			UPDATE wendy_channel_ambient
			SET last_reviewed_message_at = GREATEST(
			      COALESCE(last_reviewed_message_at, reviewing_message_at),
			      reviewing_message_at
			    ),
			    dirty = (last_human_message_at > reviewing_message_at),
			    first_dirty_message_at = CASE
			      WHEN last_human_message_at > reviewing_message_at THEN reviewing_message_at
			      ELSE NULL
			    END,
			    status = 'idle',
			    claim_token = NULL,
			    claimed_at = NULL,
			    reviewing_message_at = NULL,
			    active_radar_run_id = NULL,
			    updated_at = now()
			WHERE active_radar_run_id = $1
			  AND status = 'running'`
		args = []any{radarRunID}
	} else {
		query = `
			UPDATE wendy_channel_ambient
			SET status = 'idle',
			    dirty = TRUE,
			    claim_token = NULL,
			    claimed_at = NULL,
			    reviewing_message_at = NULL,
			    active_radar_run_id = NULL,
			    review_not_before = now() + make_interval(secs => $2),
			    updated_at = now()
			WHERE active_radar_run_id = $1
			  AND status = 'running'`
		args = []any{radarRunID, AmbientRetryBackoff.Seconds()}
	}
	if _, err := s.pool.Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("reconcile wendy channel ambient run: %w", err)
	}
	return nil
}

// ReclaimStaleChannelAmbient re-arms rows stuck in claimed/running past the stale
// window (e.g. the review run never completed because the daemon went away) so a
// review is never lost forever (#2). last_reviewed_message_at is left untouched so
// the row is only re-claimed when there is still unreviewed chatter.
func (s *Store) ReclaimStaleChannelAmbient(ctx context.Context) (int64, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE wendy_channel_ambient
		SET status = 'idle',
		    dirty = TRUE,
		    claim_token = NULL,
		    claimed_at = NULL,
		    reviewing_message_at = NULL,
		    active_radar_run_id = NULL,
		    updated_at = now()
		WHERE status IN ('claimed', 'running')
		  AND claimed_at IS NOT NULL
		  AND claimed_at < now() - make_interval(secs => $1)
	`, AmbientClaimStaleAfter.Seconds())
	if err != nil {
		return 0, fmt.Errorf("reclaim stale wendy channel ambient: %w", err)
	}
	return tag.RowsAffected(), nil
}
