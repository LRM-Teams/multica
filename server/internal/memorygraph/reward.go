package memorygraph

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// RewardSink pushes a composed reward to the training side (design §5.3,
// Q28). The production implementation wraps arealrl.Client with the session
// proxy key; the memorygraph package depends only on this narrow interface.
type RewardSink interface {
	SetReward(ctx context.Context, key string, reward float64) error
}

// RewardTraceSink is the optional trajectory-persistence hook of the reward
// composer: after a reward is composed (judge write-back or timeout sweep),
// the reward record is appended to every persisted explore trajectory file
// of the trace, so exported trajectories carry their training signal. The
// production implementation is *TraceRecorder over the trace's graph dir;
// tests fake it. Appends are best-effort: failures are logged, never
// returned.
type RewardTraceSink interface {
	AppendRewardTrace(rec RewardTraceRecord) error
}

// RewardParams holds the tunable constants of the explore-trajectory reward
// formula (design §2 补充):
//
//	judge_score < τ   →  MissPenalty
//	otherwise         →  Base - WeightRound * explore_rounds
//
// Correctness cannot be offset by cost: below τ the reward is MissPenalty no
// matter how few rounds the trajectory used (same philosophy as Q9).
type RewardParams struct {
	Tau         float64 // relevance threshold; below it the reward is MissPenalty
	Base        float64 // base reward for a judge-passed trajectory
	WeightRound float64 // per-explore-round cost weight
	MissPenalty float64 // reward for judge-failed or timed-out trajectories
}

// DefaultRewardParams returns the design §2/§6 constants.
func DefaultRewardParams() RewardParams {
	return RewardParams{Tau: 0.6, Base: 1.0, WeightRound: 0.1, MissPenalty: -1.0}
}

// DefaultRewardTimeout is the default delay after which a pending trace whose
// judge result never arrived is swept with MissPenalty (design Q28).
const DefaultRewardTimeout = 15 * time.Minute

// pendingTrace is a buffered explore trajectory awaiting its judge result.
type pendingTrace struct {
	recall      *RecallResult
	traceSink   RewardTraceSink // nil → no trajectory reward record
	submittedAt time.Time
}

// RewardComposer composes delayed rewards (design Q28): explore trajectories
// are buffered by trace id at recall time; when the asynchronous judge result
// arrives, the full reward is composed and pushed to the RewardSink. Traces
// whose judge result never arrives are swept with MissPenalty after a
// timeout.
type RewardComposer struct {
	sink    RewardSink
	params  RewardParams
	pending map[string]*pendingTrace
	mu      sync.Mutex
	timeout time.Duration
	now     func() time.Time // test hook; defaults to time.Now
}

// NewRewardComposer returns a RewardComposer pushing to sink. A non-positive
// timeout falls back to DefaultRewardTimeout.
func NewRewardComposer(sink RewardSink, params RewardParams, timeout time.Duration) *RewardComposer {
	if timeout <= 0 {
		timeout = DefaultRewardTimeout
	}
	return &RewardComposer{
		sink:    sink,
		params:  params,
		pending: make(map[string]*pendingTrace),
		timeout: timeout,
		now:     time.Now,
	}
}

// Submit buffers the trajectory of one recall under traceID. There is one
// entry per trace; a duplicate trace id replaces the previous entry.
// traceSink is optional (nil): when set, the composed reward is later
// appended to the trace's persisted trajectory files.
func (c *RewardComposer) Submit(ctx context.Context, traceID string, recall *RecallResult, traceSink RewardTraceSink) error {
	if traceID == "" {
		return fmt.Errorf("reward: empty trace id")
	}
	if recall == nil {
		return fmt.Errorf("reward: nil recall result")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pending[traceID] = &pendingTrace{recall: recall, traceSink: traceSink, submittedAt: c.now()}
	return nil
}

// OnJudgeResult composes the reward for the pending trace and pushes it via
// the sink, removing the entry from pending. A result for an unknown or
// already-composed trace is logged and ignored.
func (c *RewardComposer) OnJudgeResult(ctx context.Context, traceID string, res *JudgeResult) error {
	if res == nil {
		return fmt.Errorf("reward: nil judge result")
	}
	c.mu.Lock()
	pt, ok := c.pending[traceID]
	if ok {
		delete(c.pending, traceID)
	}
	c.mu.Unlock()
	if !ok {
		slog.Warn("reward: judge result for unknown or already-composed trace", "trace_id", traceID)
		return nil
	}
	reward := c.composeReward(pt.recall, res.Score)
	c.recordRewardTrace(pt, RewardTraceRecord{
		TraceID:    traceID,
		JudgeScore: res.Score,
		Reward:     reward,
		Rounds:     pt.recall.Rounds,
		Miss:       reward == c.params.MissPenalty,
	})
	if err := c.sink.SetReward(ctx, traceID, reward); err != nil {
		return fmt.Errorf("reward: push reward for trace %s: %w", traceID, err)
	}
	return nil
}

// SweepTimeouts pushes MissPenalty for every pending entry older than the
// composer timeout and removes it. It returns the number of swept entries.
func (c *RewardComposer) SweepTimeouts(ctx context.Context) (int, error) {
	c.mu.Lock()
	type staleTrace struct {
		id string
		pt *pendingTrace
	}
	var stale []staleTrace
	cutoff := c.now().Add(-c.timeout)
	for id, pt := range c.pending {
		if pt.submittedAt.Before(cutoff) {
			stale = append(stale, staleTrace{id: id, pt: pt})
			delete(c.pending, id)
		}
	}
	c.mu.Unlock()

	swept := 0
	for _, s := range stale {
		c.recordRewardTrace(s.pt, RewardTraceRecord{
			TraceID: s.id,
			Reward:  c.params.MissPenalty,
			Rounds:  s.pt.recall.Rounds,
			Miss:    true,
		})
		if err := c.sink.SetReward(ctx, s.id, c.params.MissPenalty); err != nil {
			return swept, fmt.Errorf("reward: sweep trace %s: %w", s.id, err)
		}
		swept++
	}
	return swept, nil
}

// recordRewardTrace appends a composed reward to the trace's persisted
// trajectory files via the optional per-trace sink. Best-effort: failures
// are logged and never fail reward delivery.
func (c *RewardComposer) recordRewardTrace(pt *pendingTrace, rec RewardTraceRecord) {
	if pt.traceSink == nil {
		return
	}
	if err := pt.traceSink.AppendRewardTrace(rec); err != nil {
		slog.Warn("reward: trajectory reward append failed", "trace_id", rec.TraceID, "error", err)
	}
}

// PendingCount returns the number of traces awaiting a judge result.
func (c *RewardComposer) PendingCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.pending)
}

// composeReward applies the reward formula. The judge scores per query and
// the score applies to every run of that query: below τ the trajectory gets
// MissPenalty; otherwise each successful run gets Base - WeightRound*rounds
// and the trajectory reward is the mean over the successful runs (design
// Q17/Q28). Runs that errored are assigned MissPenalty individually and
// excluded from the mean (review R9: a crashed trajectory has Rounds=0 and
// would otherwise collect the maximum round-cost bonus, rewarding failure);
// when every run errored the trajectory gets MissPenalty. A recall with no
// recorded runs (non-TTT) is treated as a single run of recall.Rounds.
func (c *RewardComposer) composeReward(recall *RecallResult, score float64) float64 {
	p := c.params
	if score < p.Tau {
		return p.MissPenalty
	}
	if len(recall.AgentRuns) == 0 {
		return p.Base - p.WeightRound*float64(recall.Rounds)
	}
	total := 0.0
	successful := 0
	for _, run := range recall.AgentRuns {
		if run.Error != "" {
			continue // per-run reward is MissPenalty; excluded from the mean
		}
		total += p.Base - p.WeightRound*float64(run.Rounds)
		successful++
	}
	if successful == 0 {
		return p.MissPenalty
	}
	return total / float64(successful)
}
