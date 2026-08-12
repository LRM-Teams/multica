package service

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/multica-ai/multica/server/internal/util"
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
