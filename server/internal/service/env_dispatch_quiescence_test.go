package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestEvaluateMixedRLQuiescence_AllActivitySourcesBlockQuietCandidate(t *testing.T) {
	now := time.Date(2026, time.August, 12, 9, 0, 0, 0, time.UTC)
	for name, activity := range map[string]ActivityCounterDelta{
		"active resident turn":       {ActiveTurns: 1},
		"pending delivery":           {PendingDeliveries: 1},
		"queued coordinator message": {QueuedMessages: 1},
		"inflight tool":              {InflightTools: 1},
		"unfinished capture batch":   {UnfinishedCapture: 1},
	} {
		t.Run(name, func(t *testing.T) {
			run := quiescenceRun(now)
			run.ActiveTurnCount = activity.ActiveTurns
			run.PendingDeliveryCount = activity.PendingDeliveries
			run.QueuedMessageCount = activity.QueuedMessages
			run.InflightToolCount = activity.InflightTools
			run.UnfinishedCaptureBatchCount = activity.UnfinishedCapture

			assert.Equal(t, MixedRLQuiescenceKeepRunning, EvaluateMixedRLQuiescence(run, now))
		})
	}
}

func TestEvaluateMixedRLQuiescence_QuietWindowStartsResetsAndExpires(t *testing.T) {
	now := time.Date(2026, time.August, 12, 9, 0, 0, 0, time.UTC)
	run := quiescenceRun(now)

	assert.Equal(t, MixedRLQuiescenceStartCandidate, EvaluateMixedRLQuiescence(run, now))

	run.Status = "quiet_candidate"
	run.QuietCandidateSince = now
	assert.Equal(t, MixedRLQuiescenceWait, EvaluateMixedRLQuiescence(run, now.Add(1999*time.Millisecond)))
	assert.Equal(t, MixedRLQuiescenceFreezeCompleted, EvaluateMixedRLQuiescence(run, now.Add(2*time.Second)))

	run.PendingDeliveryCount = 1
	assert.Equal(t, MixedRLQuiescenceResumeRunning, EvaluateMixedRLQuiescence(run, now.Add(2*time.Second)))
}

func TestEvaluateMixedRLQuiescence_DeadlineWinsAndFreezeIsDueWithinQuietWindow(t *testing.T) {
	now := time.Date(2026, time.August, 12, 9, 0, 0, 0, time.UTC)
	run := quiescenceRun(now)
	run.TimeoutDeadlineAt = now.Add(time.Second)
	run.ActiveTurnCount = 1
	assert.Equal(t, MixedRLQuiescenceFreezeTimeout, EvaluateMixedRLQuiescence(run, now.Add(time.Second)))

	run = quiescenceRun(now)
	run.Status = "quiet_candidate"
	run.QuietCandidateSince = now
	assert.Equal(t, MixedRLQuiescenceFreezeCompleted, EvaluateMixedRLQuiescence(run, now.Add(2*time.Second)))
}

func quiescenceRun(now time.Time) EnvDispatchRunRecord {
	return EnvDispatchRunRecord{
		Status:                    "running",
		QuietWindowMS:             2000,
		TimeoutDeadlineAt:         now.Add(time.Hour),
		InitialMessageSubmittedAt: now,
	}
}
