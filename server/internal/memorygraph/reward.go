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
func (c *RewardComposer) Submit(ctx context.Context, traceID string, recall *RecallResult) error {
	if traceID == "" {
		return fmt.Errorf("reward: empty trace id")
	}
	if recall == nil {
		return fmt.Errorf("reward: nil recall result")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pending[traceID] = &pendingTrace{recall: recall, submittedAt: c.now()}
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
	if err := c.sink.SetReward(ctx, traceID, reward); err != nil {
		return fmt.Errorf("reward: push reward for trace %s: %w", traceID, err)
	}
	return nil
}

// SweepTimeouts pushes MissPenalty for every pending entry older than the
// composer timeout and removes it. It returns the number of swept entries.
func (c *RewardComposer) SweepTimeouts(ctx context.Context) (int, error) {
	c.mu.Lock()
	var stale []string
	cutoff := c.now().Add(-c.timeout)
	for id, pt := range c.pending {
		if pt.submittedAt.Before(cutoff) {
			stale = append(stale, id)
			delete(c.pending, id)
		}
	}
	c.mu.Unlock()

	swept := 0
	for _, id := range stale {
		if err := c.sink.SetReward(ctx, id, c.params.MissPenalty); err != nil {
			return swept, fmt.Errorf("reward: sweep trace %s: %w", id, err)
		}
		swept++
	}
	return swept, nil
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
