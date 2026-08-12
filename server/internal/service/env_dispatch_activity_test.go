package service

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestEnvDispatchActivityTrackerIdempotentPending(t *testing.T) {
	tracker := NewEnvDispatchActivityTracker()
	if !tracker.CreateDeliveryObligation("delivery-1") {
		t.Fatal("first create should succeed")
	}
	if tracker.CreateDeliveryObligation("delivery-1") {
		t.Fatal("duplicate create should be ignored")
	}
	if got := tracker.PendingDeliveries(); got != 1 {
		t.Fatalf("pending=%d, want 1", got)
	}
	if !tracker.SettleDeliveryObligation("delivery-1") {
		t.Fatal("first settle should succeed")
	}
	if tracker.SettleDeliveryObligation("delivery-1") {
		t.Fatal("duplicate settle should be ignored")
	}
	if got := tracker.PendingDeliveries(); got != 0 {
		t.Fatalf("pending=%d, want 0", got)
	}
}

func TestEnvDispatchActivityCreateSettleAndAdjustCounters(t *testing.T) {
	h := newMixedRLRepositoryHarness(t)
	_ = createMixedRLRun(t, h)
	agent := bindMixedRLAgent(t, h, 0, "none")
	activity := NewEnvDispatchActivity(h.runs)

	messageID := util.MustParseUUID(uuid.NewString())
	_, err := h.tx.Exec(h.ctx, `INSERT INTO channel_message (id) VALUES ($1)`, messageID)
	require.NoError(t, err)

	created, ok, err := activity.CreateDeliveryObligation(h.ctx, CreateDeliveryObligationInput{
		RunID: mixedRLRunUUID, ChannelMessageID: messageID,
		SourceRecipientAgentID: agent.SourceAgentID, RunAgentID: agent.RunAgentID, State: "queued",
	})
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, int64(1), activity.PendingDeliveries())

	_, ok, err = activity.CreateDeliveryObligation(h.ctx, CreateDeliveryObligationInput{
		DeliveryID: created.DeliveryID, RunID: mixedRLRunUUID, ChannelMessageID: messageID,
		SourceRecipientAgentID: agent.SourceAgentID, RunAgentID: agent.RunAgentID, State: "queued",
	})
	require.NoError(t, err)
	require.False(t, ok)

	run, err := activity.AdjustActivity(h.ctx, mixedRLRunUUID, ActivityCounterDelta{
		ActiveTurns: 1, QueuedMessages: 2, InflightTools: 3, UnfinishedCapture: 4,
	})
	require.NoError(t, err)
	require.Equal(t, int64(1), run.ActiveTurnCount)
	require.Equal(t, int64(1), run.PendingDeliveryCount)
	require.Equal(t, int64(2), run.QueuedMessageCount)
	require.Equal(t, int64(3), run.InflightToolCount)
	require.Equal(t, int64(4), run.UnfinishedCaptureBatchCount)

	_, settled, err := activity.SettleDeliveryObligation(h.ctx, created.DeliveryID, "completed", time.Now().UTC())
	require.NoError(t, err)
	require.True(t, settled)
	_, settled, err = activity.SettleDeliveryObligation(h.ctx, created.DeliveryID, "completed", time.Now().UTC())
	require.NoError(t, err)
	require.False(t, settled)

	run, err = h.runs.GetRun(h.ctx, mixedRLRunUUID)
	require.NoError(t, err)
	require.Equal(t, int64(0), run.PendingDeliveryCount)
	require.Equal(t, int64(0), activity.PendingDeliveries())
}

func TestEnvDispatchActivityDuplicateStoreInstances(t *testing.T) {
	h := newDeliveryObligationReplayHarness(t)
	installDeliveryObligationReplayTrigger(t, h.observer, h.schema)

	lockConn, err := h.observer.Acquire(h.ctx)
	require.NoError(t, err)
	locked := true
	t.Cleanup(func() {
		if locked {
			releaseDeliveryObligationReplayLock(t, lockConn)
		}
		lockConn.Release()
	})
	_, err = lockConn.Exec(h.ctx, "SELECT pg_advisory_lock($1)", deliveryObligationReplayAdvisoryKey)
	require.NoError(t, err)

	first := NewEnvDispatchActivityFromQueries(db.New(h.pool))
	second := NewEnvDispatchActivityFromQueries(db.New(h.pool))
	inputWithFreshDeliveryID := func() CreateDeliveryObligationInput {
		return CreateDeliveryObligationInput{
			DeliveryID:             pgtype.UUID{Bytes: uuid.New(), Valid: true},
			RunID:                  mixedRLRunUUID,
			ChannelMessageID:       h.messageID,
			SourceRecipientAgentID: h.agent.SourceAgentID,
			RunAgentID:             h.agent.RunAgentID,
			State:                  "queued",
		}
	}
	type createResult struct {
		created bool
		err     error
	}
	results := make(chan createResult, 2)
	for _, activity := range []*EnvDispatchActivity{first, second} {
		go func(activity *EnvDispatchActivity) {
			_, created, err := activity.CreateDeliveryObligation(h.ctx, inputWithFreshDeliveryID())
			results <- createResult{created: created, err: err}
		}(activity)
	}

	waitForDeliveryObligationReplayLockWaiters(t, h.observer, 2)
	releaseDeliveryObligationReplayLock(t, lockConn)
	locked = false
	firstResult, secondResult := <-results, <-results
	require.NoError(t, firstResult.err)
	require.NoError(t, secondResult.err)
	assert.ElementsMatch(t, []bool{true, false}, []bool{firstResult.created, secondResult.created})

	run, err := NewEnvDispatchRunStore(db.New(h.pool)).GetRun(h.ctx, mixedRLRunUUID)
	require.NoError(t, err)
	require.Equal(t, int64(1), run.PendingDeliveryCount)
}

const deliveryObligationReplayAdvisoryKey int64 = 807_246_951

type deliveryObligationReplayHarness struct {
	ctx       context.Context
	schema    string
	pool      *pgxpool.Pool
	observer  *pgxpool.Pool
	messageID pgtype.UUID
	agent     EnvDispatchRunAgentRecord
}

func newDeliveryObligationReplayHarness(t *testing.T) deliveryObligationReplayHarness {
	t.Helper()
	databaseURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if databaseURL == "" {
		t.Skip("integration test requires PostgreSQL at DATABASE_URL")
	}

	ctx := context.Background()
	observer, err := pgxpool.New(ctx, databaseURL)
	require.NoError(t, err)
	t.Cleanup(observer.Close)

	schema := fmt.Sprintf("mixed_rl_delivery_replay_%d", time.Now().UnixNano())
	_, err = observer.Exec(ctx, "CREATE SCHEMA "+schema)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := observer.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")
		require.NoError(t, err)
	})

	tx, err := observer.Begin(ctx)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, "SET LOCAL search_path TO "+schema)
	require.NoError(t, err)
	createMixedRLBaseSchema(t, ctx, tx)
	applyMixedRLMigrations(t, ctx, tx)
	h := mixedRLRepositoryHarness{
		ctx:  ctx,
		tx:   tx,
		runs: NewEnvDispatchRunStore(db.New(tx)),
	}
	_ = createMixedRLRun(t, h)
	agent := bindMixedRLAgent(t, h, 0, "none")
	messageID := util.MustParseUUID(uuid.NewString())
	_, err = tx.Exec(ctx, "INSERT INTO channel_message (id) VALUES ($1)", messageID)
	require.NoError(t, err)
	require.NoError(t, tx.Commit(ctx))

	poolConfig, err := pgxpool.ParseConfig(databaseURL)
	require.NoError(t, err)
	poolConfig.ConnConfig.RuntimeParams["search_path"] = schema
	poolConfig.MaxConns = 2
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	return deliveryObligationReplayHarness{
		ctx: ctx, schema: schema, pool: pool, observer: observer, messageID: messageID, agent: agent,
	}
}

func installDeliveryObligationReplayTrigger(t *testing.T, pool *pgxpool.Pool, schema string) {
	t.Helper()
	_, err := pool.Exec(context.Background(), fmt.Sprintf(`
CREATE FUNCTION %[1]s.wait_for_delivery_obligation_insert() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  PERFORM pg_advisory_lock(%[2]d);
  PERFORM pg_advisory_unlock(%[2]d);
  RETURN NEW;
END;
$$`, schema, deliveryObligationReplayAdvisoryKey))
	require.NoError(t, err)
	_, err = pool.Exec(context.Background(), fmt.Sprintf(`
CREATE TRIGGER wait_for_delivery_obligation_insert
BEFORE INSERT ON %[1]s.env_dispatch_delivery_obligation
FOR EACH ROW EXECUTE FUNCTION %[1]s.wait_for_delivery_obligation_insert()`, schema))
	require.NoError(t, err)
}

func waitForDeliveryObligationReplayLockWaiters(t *testing.T, pool *pgxpool.Pool, want int) {
	t.Helper()
	require.Eventually(t, func() bool {
		var waiters int
		err := pool.QueryRow(context.Background(), `
SELECT count(*)
FROM pg_locks
WHERE locktype = 'advisory'
  AND NOT granted
  AND classid = 0
  AND objid = $1
  AND objsubid = 1`, deliveryObligationReplayAdvisoryKey).Scan(&waiters)
		return err == nil && waiters == want
	}, 5*time.Second, 10*time.Millisecond, "wait for %d delivery insert lock waiters", want)
}

func releaseDeliveryObligationReplayLock(t *testing.T, lockConn *pgxpool.Conn) {
	t.Helper()
	_, err := lockConn.Exec(context.Background(), "SELECT pg_advisory_unlock($1)", deliveryObligationReplayAdvisoryKey)
	require.NoError(t, err)
}
