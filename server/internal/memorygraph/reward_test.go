package memorygraph

import (
	"context"
	"math"
	"sync"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// fake reward sink
// ---------------------------------------------------------------------------

type rewardCall struct {
	key    string
	reward float64
}

type fakeRewardSink struct {
	mu    sync.Mutex
	calls []rewardCall
	err   error
}

func (f *fakeRewardSink) SetReward(_ context.Context, key string, reward float64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, rewardCall{key: key, reward: reward})
	return f.err
}

func (f *fakeRewardSink) captured() []rewardCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]rewardCall, len(f.calls))
	copy(out, f.calls)
	return out
}

// ---------------------------------------------------------------------------
// reward composer tests
// ---------------------------------------------------------------------------

func newTestComposer(sink *fakeRewardSink, timeout time.Duration) *RewardComposer {
	return NewRewardComposer(sink, DefaultRewardParams(), timeout)
}

func TestRewardBelowTauGetsMissPenalty(t *testing.T) {
	sink := &fakeRewardSink{}
	c := newTestComposer(sink, time.Minute)
	ctx := context.Background()

	recall := &RecallResult{TraceID: "t1", Rounds: 2}
	if err := c.Submit(ctx, "t1", recall); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if err := c.OnJudgeResult(ctx, "t1", &JudgeResult{Score: 0.5}); err != nil {
		t.Fatalf("OnJudgeResult: %v", err)
	}

	calls := sink.captured()
	if len(calls) != 1 || calls[0].key != "t1" {
		t.Fatalf("sink calls = %+v", calls)
	}
	if calls[0].reward != -1.0 {
		t.Fatalf("reward = %v, want miss penalty -1.0", calls[0].reward)
	}
	if c.PendingCount() != 0 {
		t.Fatalf("PendingCount = %d, want 0", c.PendingCount())
	}
}

func TestRewardAtTauBoundaryUsesRoundCost(t *testing.T) {
	sink := &fakeRewardSink{}
	c := newTestComposer(sink, time.Minute)
	ctx := context.Background()

	recall := &RecallResult{TraceID: "t1", Rounds: 2}
	if err := c.Submit(ctx, "t1", recall); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	// Exactly at τ the judge passes: reward = base - w_round * rounds.
	if err := c.OnJudgeResult(ctx, "t1", &JudgeResult{Score: 0.6}); err != nil {
		t.Fatalf("OnJudgeResult: %v", err)
	}

	calls := sink.captured()
	if len(calls) != 1 {
		t.Fatalf("sink calls = %+v", calls)
	}
	if want := 1.0 - 0.1*2; math.Abs(calls[0].reward-want) > 1e-9 {
		t.Fatalf("reward = %v, want %v", calls[0].reward, want)
	}
}

func TestRewardTTTMeanOverRuns(t *testing.T) {
	sink := &fakeRewardSink{}
	c := newTestComposer(sink, time.Minute)
	ctx := context.Background()

	recall := &RecallResult{
		TraceID: "t1",
		AgentRuns: []ExploreRun{
			{RunID: "r0", Rounds: 1},
			{RunID: "r1", Rounds: 2},
			{RunID: "r2", Rounds: 3},
		},
	}
	if err := c.Submit(ctx, "t1", recall); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if err := c.OnJudgeResult(ctx, "t1", &JudgeResult{Score: 0.9}); err != nil {
		t.Fatalf("OnJudgeResult: %v", err)
	}

	calls := sink.captured()
	if len(calls) != 1 {
		t.Fatalf("sink calls = %+v", calls)
	}
	// mean(1.0-0.1, 1.0-0.2, 1.0-0.3) = 0.8
	if want := 0.8; math.Abs(calls[0].reward-want) > 1e-9 {
		t.Fatalf("reward = %v, want %v", calls[0].reward, want)
	}
}

func TestRewardTTTBelowTauPenalizesAllRuns(t *testing.T) {
	sink := &fakeRewardSink{}
	c := newTestComposer(sink, time.Minute)
	ctx := context.Background()

	recall := &RecallResult{
		TraceID: "t1",
		AgentRuns: []ExploreRun{
			{RunID: "r0", Rounds: 1},
			{RunID: "r1", Rounds: 1},
			{RunID: "r2", Rounds: 1},
		},
	}
	if err := c.Submit(ctx, "t1", recall); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if err := c.OnJudgeResult(ctx, "t1", &JudgeResult{Score: 0.2}); err != nil {
		t.Fatalf("OnJudgeResult: %v", err)
	}

	calls := sink.captured()
	if len(calls) != 1 || calls[0].reward != -1.0 {
		t.Fatalf("sink calls = %+v, want one miss-penalty call", calls)
	}
}

func TestRewardUnknownTraceIsNoOp(t *testing.T) {
	sink := &fakeRewardSink{}
	c := newTestComposer(sink, time.Minute)

	if err := c.OnJudgeResult(context.Background(), "never-submitted", &JudgeResult{Score: 0.9}); err != nil {
		t.Fatalf("OnJudgeResult unknown trace: %v", err)
	}
	if calls := sink.captured(); len(calls) != 0 {
		t.Fatalf("sink calls = %+v, want none", calls)
	}
}

func TestRewardDoubleJudgeResultSecondIsNoOp(t *testing.T) {
	sink := &fakeRewardSink{}
	c := newTestComposer(sink, time.Minute)
	ctx := context.Background()

	if err := c.Submit(ctx, "t1", &RecallResult{TraceID: "t1", Rounds: 1}); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if err := c.OnJudgeResult(ctx, "t1", &JudgeResult{Score: 0.9}); err != nil {
		t.Fatalf("first OnJudgeResult: %v", err)
	}
	if err := c.OnJudgeResult(ctx, "t1", &JudgeResult{Score: 0.1}); err != nil {
		t.Fatalf("second OnJudgeResult: %v", err)
	}

	calls := sink.captured()
	if len(calls) != 1 {
		t.Fatalf("sink calls = %+v, want exactly one", calls)
	}
	if want := 1.0 - 0.1; math.Abs(calls[0].reward-want) > 1e-9 {
		t.Fatalf("reward = %v, want %v", calls[0].reward, want)
	}
}

func TestRewardSweepTimeoutsSweepsStaleOnly(t *testing.T) {
	sink := &fakeRewardSink{}
	c := newTestComposer(sink, 10*time.Minute)
	ctx := context.Background()

	t0 := time.Now()
	c.now = func() time.Time { return t0 }
	if err := c.Submit(ctx, "stale", &RecallResult{TraceID: "stale", Rounds: 1}); err != nil {
		t.Fatalf("Submit stale: %v", err)
	}

	c.now = func() time.Time { return t0.Add(20 * time.Minute) }
	if err := c.Submit(ctx, "fresh", &RecallResult{TraceID: "fresh", Rounds: 1}); err != nil {
		t.Fatalf("Submit fresh: %v", err)
	}

	c.now = func() time.Time { return t0.Add(25 * time.Minute) }
	swept, err := c.SweepTimeouts(ctx)
	if err != nil {
		t.Fatalf("SweepTimeouts: %v", err)
	}
	if swept != 1 {
		t.Fatalf("swept = %d, want 1", swept)
	}
	calls := sink.captured()
	if len(calls) != 1 || calls[0].key != "stale" || calls[0].reward != -1.0 {
		t.Fatalf("sink calls = %+v, want one miss-penalty call for stale", calls)
	}
	if c.PendingCount() != 1 {
		t.Fatalf("PendingCount = %d, want 1 (fresh remains)", c.PendingCount())
	}

	// A second sweep with no newly stale entries pushes nothing.
	swept, err = c.SweepTimeouts(ctx)
	if err != nil || swept != 0 {
		t.Fatalf("second sweep: swept=%d err=%v", swept, err)
	}
	if calls := sink.captured(); len(calls) != 1 {
		t.Fatalf("sink calls after second sweep = %+v", calls)
	}
}

func TestRewardSubmitValidation(t *testing.T) {
	c := newTestComposer(&fakeRewardSink{}, time.Minute)
	if err := c.Submit(context.Background(), "", &RecallResult{}); err == nil {
		t.Fatal("Submit with empty trace id: expected error")
	}
	if err := c.Submit(context.Background(), "t1", nil); err == nil {
		t.Fatal("Submit with nil recall: expected error")
	}
}

// TestRewardTTTErroredRunsExcludedFromMean (review R9): a crashed run has
// Rounds=0 and would otherwise collect the maximum round-cost bonus,
// rewarding failure. Errored runs are assigned MissPenalty individually and
// excluded from the mean over the remaining successful runs.
func TestRewardTTTErroredRunsExcludedFromMean(t *testing.T) {
	sink := &fakeRewardSink{}
	c := newTestComposer(sink, time.Minute)
	ctx := context.Background()

	recall := &RecallResult{
		TraceID: "t1",
		AgentRuns: []ExploreRun{
			{RunID: "r0", Rounds: 2},
			{RunID: "r1", Error: "agent crashed"}, // would be Base at Rounds=0 pre-R9
			{RunID: "r2", Rounds: 4},
		},
	}
	if err := c.Submit(ctx, "t1", recall); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if err := c.OnJudgeResult(ctx, "t1", &JudgeResult{Score: 0.9}); err != nil {
		t.Fatalf("OnJudgeResult: %v", err)
	}

	calls := sink.captured()
	if len(calls) != 1 {
		t.Fatalf("sink calls = %+v", calls)
	}
	// mean over the successful runs only: mean(1.0-0.2, 1.0-0.4) = 0.7
	if want := 0.7; math.Abs(calls[0].reward-want) > 1e-9 {
		t.Fatalf("reward = %v, want %v (errored run excluded from the mean)", calls[0].reward, want)
	}
}

// TestRewardTTTAllRunsErroredGetsMissPenalty (review R9): when every run of
// a judge-passed query errored, the trajectory gets MissPenalty, not the
// Base bonus of a zero-round crashed run.
func TestRewardTTTAllRunsErroredGetsMissPenalty(t *testing.T) {
	sink := &fakeRewardSink{}
	c := newTestComposer(sink, time.Minute)
	ctx := context.Background()

	recall := &RecallResult{
		TraceID: "t1",
		AgentRuns: []ExploreRun{
			{RunID: "r0", Error: "execute: backend down"},
			{RunID: "r1", Error: "agent session ended without a result"},
		},
	}
	if err := c.Submit(ctx, "t1", recall); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if err := c.OnJudgeResult(ctx, "t1", &JudgeResult{Score: 0.9}); err != nil {
		t.Fatalf("OnJudgeResult: %v", err)
	}

	calls := sink.captured()
	if len(calls) != 1 || calls[0].reward != -1.0 {
		t.Fatalf("sink calls = %+v, want one miss-penalty call", calls)
	}
}
