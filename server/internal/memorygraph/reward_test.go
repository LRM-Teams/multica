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
	if err := c.Submit(ctx, "t1", recall, nil); err != nil {
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
	if err := c.Submit(ctx, "t1", recall, nil); err != nil {
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
	if err := c.Submit(ctx, "t1", recall, nil); err != nil {
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
	if err := c.Submit(ctx, "t1", recall, nil); err != nil {
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

	if err := c.Submit(ctx, "t1", &RecallResult{TraceID: "t1", Rounds: 1}, nil); err != nil {
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
	if err := c.Submit(ctx, "stale", &RecallResult{TraceID: "stale", Rounds: 1}, nil); err != nil {
		t.Fatalf("Submit stale: %v", err)
	}

	c.now = func() time.Time { return t0.Add(20 * time.Minute) }
	if err := c.Submit(ctx, "fresh", &RecallResult{TraceID: "fresh", Rounds: 1}, nil); err != nil {
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
	if err := c.Submit(context.Background(), "", &RecallResult{}, nil); err == nil {
		t.Fatal("Submit with empty trace id: expected error")
	}
	if err := c.Submit(context.Background(), "t1", nil, nil); err == nil {
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
	if err := c.Submit(ctx, "t1", recall, nil); err != nil {
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
	if err := c.Submit(ctx, "t1", recall, nil); err != nil {
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

// ---------------------------------------------------------------------------
// reward trace join (trajectory persistence hook)
// ---------------------------------------------------------------------------

// fakeRewardTraceSink captures appended reward trace records.
type fakeRewardTraceSink struct {
	mu   sync.Mutex
	recs []RewardTraceRecord
	err  error
}

func (f *fakeRewardTraceSink) AppendRewardTrace(rec RewardTraceRecord) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recs = append(f.recs, rec)
	return f.err
}

func (f *fakeRewardTraceSink) captured() []RewardTraceRecord {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]RewardTraceRecord, len(f.recs))
	copy(out, f.recs)
	return out
}

func TestRewardJudgeResultAppendsTraceRecord(t *testing.T) {
	sink := &fakeRewardSink{}
	traces := &fakeRewardTraceSink{}
	c := newTestComposer(sink, time.Minute)
	ctx := context.Background()

	if err := c.Submit(ctx, "t1", &RecallResult{TraceID: "t1", Rounds: 2}, traces); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if err := c.OnJudgeResult(ctx, "t1", &JudgeResult{Score: 0.9}); err != nil {
		t.Fatalf("OnJudgeResult: %v", err)
	}

	recs := traces.captured()
	if len(recs) != 1 {
		t.Fatalf("trace records = %+v, want one", recs)
	}
	rec := recs[0]
	if rec.TraceID != "t1" || rec.JudgeScore != 0.9 || rec.Rounds != 2 {
		t.Fatalf("trace record = %+v", rec)
	}
	if want := 1.0 - 0.1*2; math.Abs(rec.Reward-want) > 1e-9 || rec.Miss {
		t.Fatalf("trace record = %+v, want reward %v miss=false", rec, want)
	}

	// Below tau the record carries the miss penalty and miss=true.
	if err := c.Submit(ctx, "t2", &RecallResult{TraceID: "t2", Rounds: 1}, traces); err != nil {
		t.Fatalf("Submit t2: %v", err)
	}
	if err := c.OnJudgeResult(ctx, "t2", &JudgeResult{Score: 0.2}); err != nil {
		t.Fatalf("OnJudgeResult t2: %v", err)
	}
	recs = traces.captured()
	if len(recs) != 2 || recs[1].TraceID != "t2" || recs[1].Reward != -1.0 || !recs[1].Miss {
		t.Fatalf("trace records = %+v, want t2 miss-penalty record", recs)
	}
}

// TestRewardTraceAppendFailureIsBestEffort: a failing trace sink must not
// break reward delivery to the RL sink.
func TestRewardTraceAppendFailureIsBestEffort(t *testing.T) {
	sink := &fakeRewardSink{}
	traces := &fakeRewardTraceSink{err: context.DeadlineExceeded}
	c := newTestComposer(sink, time.Minute)
	ctx := context.Background()

	if err := c.Submit(ctx, "t1", &RecallResult{TraceID: "t1", Rounds: 1}, traces); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if err := c.OnJudgeResult(ctx, "t1", &JudgeResult{Score: 0.9}); err != nil {
		t.Fatalf("OnJudgeResult: %v", err)
	}
	if calls := sink.captured(); len(calls) != 1 || calls[0].key != "t1" {
		t.Fatalf("sink calls = %+v, want the reward push to succeed", calls)
	}
}

func TestRewardSweepAppendsTraceRecord(t *testing.T) {
	sink := &fakeRewardSink{}
	traces := &fakeRewardTraceSink{}
	c := newTestComposer(sink, 10*time.Minute)
	ctx := context.Background()

	t0 := time.Now()
	c.now = func() time.Time { return t0 }
	if err := c.Submit(ctx, "stale", &RecallResult{TraceID: "stale", Rounds: 3}, traces); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	c.now = func() time.Time { return t0.Add(20 * time.Minute) }
	swept, err := c.SweepTimeouts(ctx)
	if err != nil || swept != 1 {
		t.Fatalf("SweepTimeouts = %d, %v", swept, err)
	}

	recs := traces.captured()
	if len(recs) != 1 {
		t.Fatalf("trace records = %+v, want one", recs)
	}
	rec := recs[0]
	if rec.TraceID != "stale" || rec.Reward != -1.0 || !rec.Miss || rec.Rounds != 3 || rec.JudgeScore != 0 {
		t.Fatalf("sweep trace record = %+v, want miss-penalty record with zero judge score", rec)
	}
}

// TestRewardNilTraceSinkKeepsFlow: a nil trace sink leaves both composition
// paths untouched.
func TestRewardNilTraceSinkKeepsFlow(t *testing.T) {
	sink := &fakeRewardSink{}
	c := newTestComposer(sink, time.Minute)
	ctx := context.Background()

	if err := c.Submit(ctx, "t1", &RecallResult{TraceID: "t1", Rounds: 1}, nil); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if err := c.OnJudgeResult(ctx, "t1", &JudgeResult{Score: 0.9}); err != nil {
		t.Fatalf("OnJudgeResult: %v", err)
	}
	if calls := sink.captured(); len(calls) != 1 {
		t.Fatalf("sink calls = %+v, want one", calls)
	}
}

// TestRewardJoinWritesIntoTraceFiles exercises the real wiring: a composer
// whose per-trace sink is a TraceRecorder over the graph dir appends the
// reward line to every explore trajectory file of the trace, on both the
// judge write-back and the timeout sweep path.
func TestRewardJoinWritesIntoTraceFiles(t *testing.T) {
	dir := t.TempDir()
	recorder := NewTraceRecorder(dir)

	// Two explore runs of trace "judge" and one of trace "swept".
	recorder.WriteExploreTrace(ExploreTraceMeta{TraceID: "judge", RunID: "r1", StartedAt: time.Now().UTC()}, nil, ExploreRun{Found: true, Rounds: 1})
	recorder.WriteExploreTrace(ExploreTraceMeta{TraceID: "judge", RunID: "r2", StartedAt: time.Now().UTC()}, nil, ExploreRun{Found: true, Rounds: 2})
	recorder.WriteExploreTrace(ExploreTraceMeta{TraceID: "swept", RunID: "r3", StartedAt: time.Now().UTC()}, nil, ExploreRun{Found: true, Rounds: 3})

	sink := &fakeRewardSink{}
	c := newTestComposer(sink, 10*time.Minute)
	ctx := context.Background()

	t0 := time.Now()
	c.now = func() time.Time { return t0 }
	if err := c.Submit(ctx, "judge", &RecallResult{TraceID: "judge", Rounds: 1}, recorder); err != nil {
		t.Fatalf("Submit judge: %v", err)
	}
	if err := c.Submit(ctx, "swept", &RecallResult{TraceID: "swept", Rounds: 3}, recorder); err != nil {
		t.Fatalf("Submit swept: %v", err)
	}

	if err := c.OnJudgeResult(ctx, "judge", &JudgeResult{Score: 0.9}); err != nil {
		t.Fatalf("OnJudgeResult: %v", err)
	}
	c.now = func() time.Time { return t0.Add(20 * time.Minute) }
	if swept, err := c.SweepTimeouts(ctx); err != nil || swept != 1 {
		t.Fatalf("SweepTimeouts = %d, %v", swept, err)
	}

	for _, runID := range []string{"r1", "r2"} {
		records := readTraceRecords(t, exploreTracePath(dir, "judge", runID))
		last := records[len(records)-1]
		want := 1.0 - 0.1*1 // recall.Rounds=1
		if last["kind"] != "reward" || last["trace_id"] != "judge" || last["judge_score"] != 0.9 || last["miss"] != false {
			t.Fatalf("run %s reward record = %v", runID, last)
		}
		if math.Abs(last["reward"].(float64)-want) > 1e-9 {
			t.Fatalf("run %s reward = %v, want %v", runID, last["reward"], want)
		}
	}
	records := readTraceRecords(t, exploreTracePath(dir, "swept", "r3"))
	last := records[len(records)-1]
	if last["kind"] != "reward" || last["trace_id"] != "swept" || last["reward"] != -1.0 || last["miss"] != true || last["rounds"] != 3.0 {
		t.Fatalf("swept reward record = %v, want miss penalty", last)
	}
}
