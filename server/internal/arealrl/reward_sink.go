// SPDX-License-Identifier: Apache-2.0

package arealrl

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/internal/diagnosticlog"
)

// PendingReward is one claimed reward-outbox row awaiting delivery to AReaL
// (spec §6/§7, acceptance A12/A25/A29). ProxyKey authenticates set_reward; it
// is used only as a bearer token and must never be logged, embedded in error
// messages, or otherwise exposed (A29).
type PendingReward struct {
	OutboxID     string
	TrajectoryID string
	ProxyKey     string
	Reward       float64
	Attempts     int
}

// RewardStore is the durable reward-outbox backend. Implementations must
// guarantee: ClaimPending atomically moves due rows to an in-flight state so
// competing workers never claim the same row; delivered/failed rows are never
// claimed again; stale in-flight rows (crashed worker) become claimable after
// the store's reclamation window. MarkDelivered records the durable terminal
// ack and clears the session proxy key in the same transaction.
type RewardStore interface {
	ClaimPending(ctx context.Context, limit int) ([]PendingReward, error)
	MarkDelivered(ctx context.Context, outboxID string) error
	MarkRetry(ctx context.Context, outboxID string, attempts int, nextAt time.Time, cause error) error
	MarkFailed(ctx context.Context, outboxID string, cause error) error
}

// RewardSink drains the durable reward outbox: every claimed row delivers its
// reward exactly once in effect — a crash after a successful set_reward but
// before the ack is recorded re-delivers the SAME value from the durable row,
// which AReaL treats as setting the same reward again. Transient failures
// (transport errors, 429, 5xx) are retried with backoff up to MaxAttempts;
// other 4xx responses fail the row terminally. Proxy keys are redacted from
// every error the sink stores or returns (A29).
type RewardSink struct {
	store       RewardStore
	client      *Client
	maxAttempts int
	backoff     func(attempts int) time.Duration
	now         func() time.Time
}

// RewardSinkOption customizes a RewardSink.
type RewardSinkOption func(*RewardSink)

// WithRewardSinkMaxAttempts sets the delivery attempt ceiling (default 8).
func WithRewardSinkMaxAttempts(n int) RewardSinkOption {
	return func(s *RewardSink) {
		if n >= 1 {
			s.maxAttempts = n
		}
	}
}

// WithRewardSinkBackoff overrides the retry backoff schedule.
func WithRewardSinkBackoff(fn func(attempts int) time.Duration) RewardSinkOption {
	return func(s *RewardSink) {
		if fn != nil {
			s.backoff = fn
		}
	}
}

// WithRewardSinkNow overrides the clock (tests).
func WithRewardSinkNow(fn func() time.Time) RewardSinkOption {
	return func(s *RewardSink) {
		if fn != nil {
			s.now = fn
		}
	}
}

// NewRewardSink builds a sink delivering rewards from store via client.
func NewRewardSink(store RewardStore, client *Client, opts ...RewardSinkOption) *RewardSink {
	s := &RewardSink{
		store:       store,
		client:      client,
		maxAttempts: 8,
		backoff:     defaultRewardBackoff,
		now:         time.Now,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// defaultRewardBackoff is exponential with a 5-minute ceiling.
func defaultRewardBackoff(attempts int) time.Duration {
	shift := attempts
	if shift > 8 {
		shift = 8
	}
	d := time.Duration(1<<shift) * time.Second
	if d > 5*time.Minute {
		d = 5 * time.Minute
	}
	return d
}

// DeliverOnce claims up to limit due rows and delivers each. Per-row delivery
// outcomes are recorded in the store; the returned error covers only
// store-level failures (claim/ack bookkeeping), never individual deliveries.
func (s *RewardSink) DeliverOnce(ctx context.Context, limit int) (int, error) {
	rows, err := s.store.ClaimPending(ctx, limit)
	if err != nil {
		return 0, fmt.Errorf("arealrl: claim pending rewards: %w", err)
	}
	delivered := 0
	for _, row := range rows {
		ok, err := s.deliver(ctx, row)
		if err != nil {
			return delivered, err
		}
		if ok {
			delivered++
		}
	}
	return delivered, nil
}

// deliver processes one claimed row and reports whether it was delivered.
func (s *RewardSink) deliver(ctx context.Context, row PendingReward) (bool, error) {
	if row.ProxyKey == "" {
		// The key is cleared only after a durable ack, so a claimed row
		// without one is an inconsistency — fail terminally, never guess.
		err := s.store.MarkFailed(ctx, row.OutboxID, errors.New(
			"arealrl: reward outbox row has no session proxy key"))
		return false, err
	}
	err := s.client.SetReward(ctx, row.ProxyKey, row.Reward)
	if err == nil {
		if err := s.store.MarkDelivered(ctx, row.OutboxID); err != nil {
			return false, fmt.Errorf("arealrl: record reward ack: %w", err)
		}
		return true, nil
	}
	cause := sanitizeRewardError(err, row.ProxyKey)
	attempts := row.Attempts + 1
	if !retryableRewardError(err) || attempts >= s.maxAttempts {
		if err := s.store.MarkFailed(ctx, row.OutboxID, cause); err != nil {
			return false, fmt.Errorf("arealrl: record reward failure: %w", err)
		}
		return false, nil
	}
	nextAt := s.now().Add(s.backoff(attempts))
	if err := s.store.MarkRetry(ctx, row.OutboxID, attempts, nextAt, cause); err != nil {
		return false, fmt.Errorf("arealrl: record reward retry: %w", err)
	}
	return false, nil
}

// retryableRewardError classifies delivery failures: transport errors and
// 429/5xx responses are transient; other 4xx responses are terminal.
func retryableRewardError(err error) bool {
	var he *HTTPError
	if errors.As(err, &he) {
		return he.Status == 429 || he.Status >= 500
	}
	return true
}

// sanitizeRewardError strips every occurrence of the proxy key from an error
// so stored/returned failures can never leak it (A29): the bridge may echo
// the offending key in its response body.
func sanitizeRewardError(err error, proxyKey string) error {
	msg := err.Error()
	if proxyKey != "" {
		msg = strings.ReplaceAll(msg, proxyKey, "[REDACTED]")
	}
	return errors.New(diagnosticlog.SanitizeText(msg))
}
