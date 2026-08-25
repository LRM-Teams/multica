// SPDX-License-Identifier: Apache-2.0

package scheduler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/arealrl"
)

// Spec §5: Dive worker and reward-outbox jobs are nil-safe no-ops; a failing
// outbox store surfaces as a retryable handler error.

type failingRewardStore struct{}

func (failingRewardStore) ClaimPending(context.Context, int) ([]arealrl.PendingReward, error) {
	return nil, errors.New("reward store unavailable")
}
func (failingRewardStore) MarkDelivered(context.Context, string) error { return nil }
func (failingRewardStore) MarkRetry(context.Context, string, int, time.Time, error) error {
	return nil
}
func (failingRewardStore) MarkFailed(context.Context, string, error) error { return nil }

func TestGraphMemoryDiveJobsNilSafe(t *testing.T) {
	jobs := GraphMemoryDiveJobs(nil, nil, nil, nil)
	if len(jobs) != 2 {
		t.Fatalf("GraphMemoryDiveJobs len = %d, want 2", len(jobs))
	}
	ctx := context.Background()
	for _, job := range jobs {
		if job.Handler == nil {
			t.Fatalf("job %s has nil handler", job.Name)
		}
		if _, err := job.Handler(ctx, HandlerInput{}); err != nil {
			t.Fatalf("nil-safe job %s: %v", job.Name, err)
		}
	}
}

func TestGraphMemoryRewardOutboxHandlerStoreError(t *testing.T) {
	sink := arealrl.NewRewardSink(failingRewardStore{}, arealrl.New("http://127.0.0.1:1", "k"))
	jobs := GraphMemoryDiveJobs(nil, nil, sink, nil)
	var outbox JobSpec
	for _, job := range jobs {
		if job.Name == "graph_memory_reward_outbox" {
			outbox = job
		}
	}
	if outbox.Handler == nil {
		t.Fatal("reward outbox job missing")
	}
	_, err := outbox.Handler(context.Background(), HandlerInput{})
	if err == nil {
		t.Fatal("failing store must surface as a retryable handler error")
	}
}
