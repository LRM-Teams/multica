// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/multica-ai/multica/server/internal/memorygraph"
)

// fakeGraphRewardSink records SetReward calls (memorygraph.RewardSink).
type fakeGraphRewardSink struct {
	mu      sync.Mutex
	keys    []string
	rewards []float64
}

func (f *fakeGraphRewardSink) SetReward(_ context.Context, key string, reward float64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.keys = append(f.keys, key)
	f.rewards = append(f.rewards, reward)
	return nil
}

func (f *fakeGraphRewardSink) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.keys)
}

func (f *fakeGraphRewardSink) first() (string, float64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.keys[0], f.rewards[0]
}

// TestGraphMemoryJudgeRunRewardSweep (review R15): the sweep ticker fires on
// its interval and pushes the miss penalty for pending traces whose judge
// result never arrived; it stops cleanly when the context is cancelled.
func TestGraphMemoryJudgeRunRewardSweep(t *testing.T) {
	sink := &fakeGraphRewardSink{}
	// A 1ms composer timeout makes the pending trace stale almost immediately.
	rewards := memorygraph.NewRewardComposer(sink, memorygraph.DefaultRewardParams(), time.Millisecond)
	svc := &GraphMemoryJudgeService{rewards: rewards}

	ctx := context.Background()
	require.NoError(t, rewards.Submit(ctx, "trace-stale", &memorygraph.RecallResult{TraceID: "trace-stale", Rounds: 1}))

	sweepCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		svc.RunRewardSweep(sweepCtx, 5*time.Millisecond)
		close(done)
	}()

	deadline := time.Now().Add(2 * time.Second)
	for sink.count() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done

	require.Equal(t, 1, sink.count(), "sweep must push the timed-out trace")
	key, reward := sink.first()
	assert.Equal(t, "trace-stale", key)
	assert.Equal(t, memorygraph.DefaultRewardParams().MissPenalty, reward)
	assert.Equal(t, 0, rewards.PendingCount())
}

// TestGraphMemoryJudgeRunRewardSweepNoopWithoutComposer: with no reward
// sink wired (RL bridge unconfigured) the sweep returns immediately.
func TestGraphMemoryJudgeRunRewardSweepNoopWithoutComposer(t *testing.T) {
	svc := &GraphMemoryJudgeService{}
	done := make(chan struct{})
	go func() {
		svc.RunRewardSweep(context.Background(), time.Millisecond)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("RunRewardSweep did not return without a composer")
	}
}

// TestGraphMemoryHistoryProvider: the production HistoryProvider wraps
// MessagesForTaskInRange and maps task_message rows to judge-visible
// role/content pairs (tool outputs appended to the content).
func TestGraphMemoryHistoryProvider(t *testing.T) {
	msgs := newFakeMessageStore()
	taskID := "task-hist-1"
	msgs.addTaskMessage(taskID, makeFakeTaskMessage(taskID, 1, "user", "", "what broke the deploy?", "", ""))
	msgs.addTaskMessage(taskID, makeFakeTaskMessage(taskID, 2, "tool", "read_file", "", `{"path":"/x"}`, "file contents here"))

	history, err := NewGraphMemoryHistoryProvider(msgs, taskID).DownstreamHistory(context.Background(), "trace-1")
	require.NoError(t, err)
	require.Len(t, history, 2)
	assert.Equal(t, memorygraph.Message{Role: "user", Content: "what broke the deploy?"}, history[0])
	assert.Equal(t, memorygraph.Message{Role: "tool", Content: "file contents here"}, history[1])
}
