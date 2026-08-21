package handler

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/daemonws"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// fakePiRunClient is a scriptable mixedDispatchPiRunClient used to exercise the
// PreparePiRun settle/retry path without a daemon hub or a database.
type fakePiRunClient struct {
	// respond is invoked for every RequestPreparePiRun call with the 1-based
	// attempt count and returns the response/error for that attempt.
	respond func(attempt int) (*protocol.PreparePiRunResponsePayload, error)
	attempts int32
}

func (f *fakePiRunClient) RequestPreparePiRun(_ context.Context, _ protocol.PreparePiRunRequestPayload) (*protocol.PreparePiRunResponsePayload, error) {
	attempt := int(atomic.AddInt32(&f.attempts, 1))
	return f.respond(attempt)
}

func (f *fakePiRunClient) RequestRevokePiRun(context.Context, protocol.RevokePiRunRequestPayload) error {
	return nil
}

// shrinkPreparePiRunSettle swaps the settle window for a fast one and returns a
// restore func. Callers must not run these tests in parallel.
func shrinkPreparePiRunSettle(t *testing.T, timeout, delay time.Duration) {
	t.Helper()
	prevTimeout, prevDelay := envDispatchPreparePiRunSettleTimeout, envDispatchPreparePiRunRetryDelay
	envDispatchPreparePiRunSettleTimeout, envDispatchPreparePiRunRetryDelay = timeout, delay
	t.Cleanup(func() {
		envDispatchPreparePiRunSettleTimeout, envDispatchPreparePiRunRetryDelay = prevTimeout, prevDelay
	})
}

func newSettleTestAdapter(client mixedDispatchPiRunClient) *envDispatchDepsAdapter {
	// A bare Handler is enough: the test hook is nil (so the real path runs) and
	// piRuns short-circuits before DaemonHub is ever consulted. No DB required.
	return &envDispatchDepsAdapter{h: &Handler{}, piRuns: client}
}

// TestPrepareMixedDispatchRunAgentSettlesTransientOffline proves the fix for the
// provision -> PreparePiRun race: the daemon registers over REST (runtime online
// in the DB) before its WebSocket connects, so the first PreparePiRun hits a hub
// with no socket and returns ErrRuntimeOffline. The adapter must retry until the
// socket lands instead of failing the rollout.
func TestPrepareMixedDispatchRunAgentSettlesTransientOffline(t *testing.T) {
	shrinkPreparePiRunSettle(t, 2*time.Second, time.Millisecond)

	const failuresBeforeConnect = 3
	client := &fakePiRunClient{respond: func(attempt int) (*protocol.PreparePiRunResponsePayload, error) {
		if attempt <= failuresBeforeConnect {
			return nil, daemonws.ErrRuntimeOffline
		}
		return &protocol.PreparePiRunResponsePayload{RequestID: "r", SessionID: "pi-session", CaptureBoundary: "boundary"}, nil
	}}
	adapter := newSettleTestAdapter(client)

	got, err := adapter.PrepareMixedDispatchRunAgent(context.Background(), "run-1", service.MixedDispatchRunAgent{
		SourceAgentID: "src", ExecutionAgentID: "exec", RuntimeID: "rt",
	})
	if err != nil {
		t.Fatalf("PrepareMixedDispatchRunAgent = %v, want success after transient offline", err)
	}
	if attempts := atomic.LoadInt32(&client.attempts); attempts != failuresBeforeConnect+1 {
		t.Fatalf("attempts = %d, want %d (retried through the offline window)", attempts, failuresBeforeConnect+1)
	}
	if got.PiSessionID != "pi-session" || got.CaptureBoundary != "boundary" {
		t.Fatalf("run agent binding = %+v, want session/boundary from daemon", got)
	}
	if got.RunAgentID == "" {
		t.Fatal("RunAgentID not allocated")
	}
}

// TestPrepareMixedDispatchRunAgentNoRetryOnOtherErrors ensures only the transient
// offline signal is retried; any other daemon error surfaces immediately.
func TestPrepareMixedDispatchRunAgentNoRetryOnOtherErrors(t *testing.T) {
	shrinkPreparePiRunSettle(t, 2*time.Second, time.Millisecond)

	boom := errors.New("daemon rejected: bad token")
	client := &fakePiRunClient{respond: func(int) (*protocol.PreparePiRunResponsePayload, error) {
		return nil, boom
	}}
	adapter := newSettleTestAdapter(client)

	_, err := adapter.PrepareMixedDispatchRunAgent(context.Background(), "run-1", service.MixedDispatchRunAgent{
		SourceAgentID: "src", ExecutionAgentID: "exec", RuntimeID: "rt",
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want wrapped %v", err, boom)
	}
	if attempts := atomic.LoadInt32(&client.attempts); attempts != 1 {
		t.Fatalf("attempts = %d, want 1 (no retry for non-offline errors)", attempts)
	}
}

// TestPrepareMixedDispatchRunAgentSettleDeadline bounds the retry loop: a daemon
// that never connects must fail the rollout within the settle window rather than
// stall it.
func TestPrepareMixedDispatchRunAgentSettleDeadline(t *testing.T) {
	shrinkPreparePiRunSettle(t, 20*time.Millisecond, 5*time.Millisecond)

	client := &fakePiRunClient{respond: func(int) (*protocol.PreparePiRunResponsePayload, error) {
		return nil, daemonws.ErrRuntimeOffline
	}}
	adapter := newSettleTestAdapter(client)

	start := time.Now()
	_, err := adapter.PrepareMixedDispatchRunAgent(context.Background(), "run-1", service.MixedDispatchRunAgent{
		SourceAgentID: "src", ExecutionAgentID: "exec", RuntimeID: "rt",
	})
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("err = nil, want settle deadline failure")
	}
	if !errors.Is(err, daemonws.ErrRuntimeOffline) {
		t.Fatalf("err = %v, want it to wrap ErrRuntimeOffline", err)
	}
	if !strings.Contains(err.Error(), "daemon WebSocket not connected within") {
		t.Fatalf("err = %v, want settle-deadline message", err)
	}
	if attempts := atomic.LoadInt32(&client.attempts); attempts < 2 {
		t.Fatalf("attempts = %d, want at least one retry before the deadline", attempts)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("elapsed = %s, want bounded by the settle window", elapsed)
	}
}

// TestPrepareMixedDispatchRunAgentContextCancel stops the settle loop promptly
// when the dispatch context is cancelled mid-retry.
func TestPrepareMixedDispatchRunAgentContextCancel(t *testing.T) {
	shrinkPreparePiRunSettle(t, 5*time.Second, 10*time.Millisecond)

	client := &fakePiRunClient{respond: func(int) (*protocol.PreparePiRunResponsePayload, error) {
		return nil, daemonws.ErrRuntimeOffline
	}}
	adapter := newSettleTestAdapter(client)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(25 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	_, err := adapter.PrepareMixedDispatchRunAgent(ctx, "run-1", service.MixedDispatchRunAgent{
		SourceAgentID: "src", ExecutionAgentID: "exec", RuntimeID: "rt",
	})
	elapsed := time.Since(start)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("elapsed = %s, want prompt cancellation", elapsed)
	}
}
