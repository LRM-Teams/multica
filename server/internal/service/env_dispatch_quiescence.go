package service

import "time"

// MixedRLQuiescenceDecision is the next lifecycle action derived from the
// server-observed run counters. Persistence remains authoritative; this pure
// decision function makes timer scheduling deterministic and testable.
type MixedRLQuiescenceDecision string

const (
	MixedRLQuiescenceKeepRunning     MixedRLQuiescenceDecision = "keep_running"
	MixedRLQuiescenceStartCandidate  MixedRLQuiescenceDecision = "start_candidate"
	MixedRLQuiescenceResumeRunning   MixedRLQuiescenceDecision = "resume_running"
	MixedRLQuiescenceWait            MixedRLQuiescenceDecision = "wait"
	MixedRLQuiescenceFreezeCompleted MixedRLQuiescenceDecision = "freeze_completed"
	MixedRLQuiescenceFreezeTimeout   MixedRLQuiescenceDecision = "freeze_timeout"
)

// EvaluateMixedRLQuiescence evaluates the mixed-run lifecycle. A total timeout
// always wins, while a normal freeze only occurs after every server-observed
// activity source has remained zero for the configured quiet window.
func EvaluateMixedRLQuiescence(run EnvDispatchRunRecord, now time.Time) MixedRLQuiescenceDecision {
	if !run.TimeoutDeadlineAt.IsZero() && !now.Before(run.TimeoutDeadlineAt) {
		return MixedRLQuiescenceFreezeTimeout
	}
	if run.ActiveTurnCount != 0 ||
		run.PendingDeliveryCount != 0 ||
		run.QueuedMessageCount != 0 ||
		run.InflightToolCount != 0 ||
		run.UnfinishedCaptureBatchCount != 0 {
		if run.Status == "quiet_candidate" {
			return MixedRLQuiescenceResumeRunning
		}
		return MixedRLQuiescenceKeepRunning
	}
	if run.Status != "quiet_candidate" || run.QuietCandidateSince.IsZero() {
		return MixedRLQuiescenceStartCandidate
	}
	quietWindow := time.Duration(run.QuietWindowMS) * time.Millisecond
	if !now.Before(run.QuietCandidateSince.Add(quietWindow)) {
		return MixedRLQuiescenceFreezeCompleted
	}
	return MixedRLQuiescenceWait
}
