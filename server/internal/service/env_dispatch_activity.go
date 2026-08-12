package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// activityExec is the narrow executor used to settle obligations from handlers
// that expose either pgxpool/pgx.Tx or the handler dbExecutor facade.
type activityExec interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

// Mixed-run quiescence observes exactly these five counters. Daemon-reported
// MixedRunActivityTransition events cover active turns, queued messages,
// in-flight tools, and unfinished capture batches. Pending deliveries are
// owned by synchronous Create/SettleDeliveryObligation on this service so
// canonical sends cannot race quiet evaluation.
const (
	MixedRLActivityActiveTurn        = "active_turn_count"
	MixedRLActivityPendingDelivery   = "pending_delivery_count"
	MixedRLActivityQueuedMessage     = "queued_message_count"
	MixedRLActivityInflightTool      = "inflight_tool_count"
	MixedRLActivityUnfinishedCapture = "unfinished_capture_batch_count"
)

// EnvDispatchActivityTracker mirrors the idempotent state transition required
// by persistent run delivery obligations. It is used for in-process ordering
// checks; the database remains authoritative across restarts.
type EnvDispatchActivityTracker struct {
	mu         sync.Mutex
	deliveries map[string]bool
	pending    int64
}

func NewEnvDispatchActivityTracker() *EnvDispatchActivityTracker {
	return &EnvDispatchActivityTracker{deliveries: make(map[string]bool)}
}

func (t *EnvDispatchActivityTracker) CreateDeliveryObligation(deliveryID string) bool {
	if t == nil || deliveryID == "" {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, exists := t.deliveries[deliveryID]; exists {
		return false
	}
	t.deliveries[deliveryID] = false
	t.pending++
	return true
}

func (t *EnvDispatchActivityTracker) SettleDeliveryObligation(deliveryID string) bool {
	if t == nil || deliveryID == "" {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	settled, exists := t.deliveries[deliveryID]
	if !exists || settled {
		return false
	}
	t.deliveries[deliveryID] = true
	if t.pending > 0 {
		t.pending--
	}
	return true
}

func (t *EnvDispatchActivityTracker) PendingDeliveries() int64 {
	if t == nil {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.pending
}

// EnvDispatchActivity is the service-side API for mixed-run activity counters
// and run-scoped delivery obligations. Durable writes go through
// EnvDispatchRunStore; the optional tracker only guards local races.
type EnvDispatchActivity struct {
	store   *EnvDispatchRunStore
	tracker *EnvDispatchActivityTracker
}

func NewEnvDispatchActivity(store *EnvDispatchRunStore) *EnvDispatchActivity {
	return &EnvDispatchActivity{store: store, tracker: NewEnvDispatchActivityTracker()}
}

func NewEnvDispatchActivityWithTracker(store *EnvDispatchRunStore, tracker *EnvDispatchActivityTracker) *EnvDispatchActivity {
	if tracker == nil {
		tracker = NewEnvDispatchActivityTracker()
	}
	return &EnvDispatchActivity{store: store, tracker: tracker}
}

// NewEnvDispatchActivityFromQueries is a convenience constructor for handlers
// that already hold sqlc Queries (including WithTx scopes).
func NewEnvDispatchActivityFromQueries(queries *db.Queries) *EnvDispatchActivity {
	return NewEnvDispatchActivity(NewEnvDispatchRunStore(queries))
}

// CreateDeliveryObligation persists a queued obligation and increments
// pending_delivery_count exactly once per (message, run-agent). created is
// true only when the durable counter advanced.
func (a *EnvDispatchActivity) CreateDeliveryObligation(ctx context.Context, input CreateDeliveryObligationInput) (DeliveryObligationRecord, bool, error) {
	if a == nil || a.store == nil {
		return DeliveryObligationRecord{}, false, errors.New("mixed-run activity store unavailable")
	}
	if !input.RunID.Valid || !input.ChannelMessageID.Valid || !input.SourceRecipientAgentID.Valid || !input.RunAgentID.Valid {
		return DeliveryObligationRecord{}, false, errors.New("delivery obligation requires run, message, source, and run-agent IDs")
	}
	if input.State == "" {
		input.State = "queued"
	}
	if !input.DeliveryID.Valid {
		generated := uuid.New()
		input.DeliveryID = pgtype.UUID{Bytes: generated, Valid: true}
	}
	before, err := a.store.GetRun(ctx, input.RunID)
	if err != nil {
		return DeliveryObligationRecord{}, false, err
	}
	record, err := a.store.CreateDeliveryObligation(ctx, input)
	if err != nil {
		return DeliveryObligationRecord{}, false, err
	}
	after, err := a.store.GetRun(ctx, input.RunID)
	if err != nil {
		return DeliveryObligationRecord{}, false, err
	}
	created := after.PendingDeliveryCount > before.PendingDeliveryCount
	if created {
		a.tracker.CreateDeliveryObligation(uuidString(record.DeliveryID))
	}
	return record, created, nil
}

// SettleDeliveryObligation settles by durable delivery_id and decrements
// pending_delivery_count at most once. settled is true when this activity
// instance observed the first local transition via the in-memory tracker
// (create must have run on the same instance). Durable idempotency remains in
// EnvDispatchRunStore; acknowledgement paths should use
// SettleDeliveryObligationForExecutionAgent for RowsAffected semantics.
func (a *EnvDispatchActivity) SettleDeliveryObligation(ctx context.Context, deliveryID pgtype.UUID, state string, settledAt time.Time) (DeliveryObligationRecord, bool, error) {
	if a == nil || a.store == nil {
		return DeliveryObligationRecord{}, false, errors.New("mixed-run activity store unavailable")
	}
	if !deliveryID.Valid {
		return DeliveryObligationRecord{}, false, errors.New("delivery_id is required")
	}
	record, err := a.store.SettleDeliveryObligation(ctx, deliveryID, state, settledAt)
	if err != nil {
		return DeliveryObligationRecord{}, false, err
	}
	return record, a.tracker.SettleDeliveryObligation(uuidString(record.DeliveryID)), nil
}

// SettleDeliveryObligationForExecutionAgent settles the obligation matching a
// canonical delivery acknowledgement (channel message + execution agent).
// exec must be the same transaction/connection that observed the ack.
func SettleDeliveryObligationForExecutionAgent(ctx context.Context, exec activityExec, channelMessageID, executionAgentID pgtype.UUID) (bool, error) {
	if exec == nil {
		return false, errors.New("mixed-run activity executor unavailable")
	}
	if !channelMessageID.Valid || !executionAgentID.Valid {
		return false, errors.New("settlement requires channel message and execution agent IDs")
	}
	tag, err := exec.Exec(ctx, `
		WITH settled AS (
		  UPDATE env_dispatch_delivery_obligation AS obligation
		  SET state = 'completed', settled_at = now()
		  FROM env_dispatch_run_agent AS run_agent
		  WHERE obligation.run_id = run_agent.run_id
		    AND obligation.run_agent_id = run_agent.run_agent_id
		    AND obligation.channel_message_id = $1
		    AND run_agent.execution_agent_id = $2
		    AND obligation.state IN ('pending', 'queued', 'accepted')
		  RETURNING obligation.run_id
		)
		UPDATE env_dispatch_run AS run
		SET pending_delivery_count = GREATEST(run.pending_delivery_count - 1, 0),
		    updated_at = now()
		FROM settled
		WHERE run.run_id = settled.run_id`, channelMessageID, executionAgentID)
	if err != nil {
		return false, fmt.Errorf("settle mixed-run delivery obligation: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// AdjustActivity applies a signed delta to the five quiescence counters through
// EnvDispatchRunStore. Prefer obligation Create/Settle for pending deliveries
// and daemon MixedRunActivityTransition for the other four dimensions so
// transition idempotency stays intact.
func (a *EnvDispatchActivity) AdjustActivity(ctx context.Context, runID pgtype.UUID, delta ActivityCounterDelta) (EnvDispatchRunRecord, error) {
	if a == nil || a.store == nil {
		return EnvDispatchRunRecord{}, errors.New("mixed-run activity store unavailable")
	}
	return a.store.AdjustActivity(ctx, runID, delta)
}

func (a *EnvDispatchActivity) PendingDeliveries() int64 {
	if a == nil {
		return 0
	}
	return a.tracker.PendingDeliveries()
}

func uuidString(id pgtype.UUID) string {
	if !id.Valid {
		return ""
	}
	return id.String()
}
