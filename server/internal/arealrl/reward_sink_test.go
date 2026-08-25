// SPDX-License-Identifier: Apache-2.0

package arealrl

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeOutboxRow is one in-memory outbox row for RewardSink tests.
type fakeOutboxRow struct {
	id           string
	trajectoryID string
	proxyKey     string
	reward       float64
	status       string
	attempts     int
	lastErr      string
	nextAt       time.Time
	updatedAt    time.Time
}

// fakeOutboxStore implements RewardStore in memory, mirroring the pg-backed
// semantics: claim moves due rows to delivering; delivered rows are never
// claimed again; stale delivering rows are reclaimable (crash recovery).
type fakeOutboxStore struct {
	mu   sync.Mutex
	rows []*fakeOutboxRow
	now  time.Time
}

func (f *fakeOutboxStore) ClaimPending(_ context.Context, limit int) ([]PendingReward, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []PendingReward
	for _, r := range f.rows {
		if len(out) >= limit {
			break
		}
		due := r.status == "pending" && !r.nextAt.After(f.now)
		stale := r.status == "delivering" && f.now.Sub(r.updatedAt) > 2*time.Minute
		if !due && !stale {
			continue
		}
		r.status = "delivering"
		r.updatedAt = f.now
		out = append(out, PendingReward{
			OutboxID:     r.id,
			TrajectoryID: r.trajectoryID,
			ProxyKey:     r.proxyKey,
			Reward:       r.reward,
			Attempts:     r.attempts,
		})
	}
	return out, nil
}

func (f *fakeOutboxStore) row(id string) *fakeOutboxRow {
	for _, r := range f.rows {
		if r.id == id {
			return r
		}
	}
	return nil
}

func (f *fakeOutboxStore) MarkDelivered(_ context.Context, outboxID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	r := f.row(outboxID)
	if r == nil || r.status != "delivering" {
		return fmt.Errorf("fake store: cannot deliver %s", outboxID)
	}
	r.status = "delivered"
	r.proxyKey = ""
	r.updatedAt = f.now
	return nil
}

func (f *fakeOutboxStore) MarkRetry(_ context.Context, outboxID string, attempts int, nextAt time.Time, cause error) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	r := f.row(outboxID)
	if r == nil || r.status != "delivering" {
		return fmt.Errorf("fake store: cannot retry %s", outboxID)
	}
	r.status = "pending"
	r.attempts = attempts
	r.nextAt = nextAt
	r.lastErr = cause.Error()
	r.updatedAt = f.now
	return nil
}

func (f *fakeOutboxStore) MarkFailed(_ context.Context, outboxID string, cause error) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	r := f.row(outboxID)
	if r == nil || r.status != "delivering" {
		return fmt.Errorf("fake store: cannot fail %s", outboxID)
	}
	r.status = "failed"
	r.lastErr = cause.Error()
	r.updatedAt = f.now
	return nil
}

// setRewardBridge is a scripted /rl/set_reward endpoint recording every call.
type setRewardBridge struct {
	mu      sync.Mutex
	status  []int
	bodies  []map[string]any
	callNum int
	key     string
}

func (b *setRewardBridge) handler(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var parsed map[string]any
	_ = json.Unmarshal(body, &parsed)
	b.mu.Lock()
	b.bodies = append(b.bodies, parsed)
	idx := b.callNum
	b.callNum++
	status := b.status[len(b.status)-1]
	if idx < len(b.status) {
		status = b.status[idx]
	}
	key := b.key
	b.mu.Unlock()
	w.WriteHeader(status)
	if status >= 400 {
		// The bridge echoes the offending key in its error body: the sink must
		// never let this reach stored errors (A29).
		_, _ = w.Write([]byte(`{"detail":"rejected key ` + key + `"}`))
		return
	}
	_, _ = w.Write([]byte(`{"message":"success"}`))
}

func (b *setRewardBridge) calls() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.bodies)
}

func (b *setRewardBridge) rewards() []float64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]float64, 0, len(b.bodies))
	for _, m := range b.bodies {
		out = append(out, m["reward"].(float64))
	}
	return out
}

const sinkTestProxyKey = "sk-canary-9f8e7d6c5b"

func TestRewardSinkTransientFailureRetried(t *testing.T) {
	store := &fakeOutboxStore{now: time.Now()}
	bridge := &setRewardBridge{status: []int{500, 500, 200}, key: sinkTestProxyKey}
	srv := httptest.NewServer(http.HandlerFunc(bridge.handler))
	defer srv.Close()
	sink := NewRewardSink(store, New(srv.URL, testAdminKey),
		WithRewardSinkMaxAttempts(8),
		WithRewardSinkBackoff(func(int) time.Duration { return 0 }),
		WithRewardSinkNow(func() time.Time { return store.now }),
	)
	store.rows = append(store.rows, &fakeOutboxRow{
		id: "ob1", trajectoryID: "t1", proxyKey: sinkTestProxyKey,
		reward: 0.5, status: "pending", nextAt: store.now,
	})

	for i := 0; i < 5 && store.row("ob1").status != "delivered"; i++ {
		if _, err := sink.DeliverOnce(context.Background(), 10); err != nil {
			t.Fatalf("DeliverOnce: %v", err)
		}
	}

	if got := bridge.calls(); got != 3 {
		t.Fatalf("set_reward calls = %d, want 3 (two transient 5xx then success)", got)
	}
	row := store.row("ob1")
	if row.status != "delivered" {
		t.Fatalf("status = %q, want delivered", row.status)
	}
	if row.attempts != 2 {
		t.Fatalf("attempts = %d, want 2 recorded transient failures", row.attempts)
	}
	if row.proxyKey != "" {
		t.Fatal("proxy key must be cleared after durable terminal ack")
	}
}

func TestRewardSinkExactlyOneEffectiveReward(t *testing.T) {
	store := &fakeOutboxStore{now: time.Now()}
	bridge := &setRewardBridge{status: []int{200}, key: sinkTestProxyKey}
	srv := httptest.NewServer(http.HandlerFunc(bridge.handler))
	defer srv.Close()
	sink := NewRewardSink(store, New(srv.URL, testAdminKey),
		WithRewardSinkMaxAttempts(8),
		WithRewardSinkBackoff(func(int) time.Duration { return 0 }),
		WithRewardSinkNow(func() time.Time { return store.now }),
	)
	// Crash-after-ack: the row was claimed (delivering) long ago but the
	// delivery outcome was never recorded. The reaper-style reclaim must
	// re-deliver the SAME reward value — one effective reward.
	store.rows = append(store.rows, &fakeOutboxRow{
		id: "ob1", trajectoryID: "t1", proxyKey: sinkTestProxyKey,
		reward: 0.25, status: "delivering", updatedAt: store.now.Add(-10 * time.Minute),
	})

	if _, err := sink.DeliverOnce(context.Background(), 10); err != nil {
		t.Fatalf("DeliverOnce: %v", err)
	}
	// Subsequent runs must never touch a delivered row.
	for i := 0; i < 3; i++ {
		if _, err := sink.DeliverOnce(context.Background(), 10); err != nil {
			t.Fatalf("DeliverOnce: %v", err)
		}
	}

	if got := bridge.calls(); got != 1 {
		t.Fatalf("set_reward calls = %d, want exactly 1", got)
	}
	for _, r := range bridge.rewards() {
		if r != 0.25 {
			t.Fatalf("delivered reward = %v, want the durable row value 0.25", r)
		}
	}
	if store.row("ob1").status != "delivered" {
		t.Fatalf("status = %q, want delivered", store.row("ob1").status)
	}
}

func TestRewardSinkKeyClearedOnlyAfterAck(t *testing.T) {
	store := &fakeOutboxStore{now: time.Now()}
	bridge := &setRewardBridge{status: []int{500, 200}, key: sinkTestProxyKey}
	srv := httptest.NewServer(http.HandlerFunc(bridge.handler))
	defer srv.Close()
	sink := NewRewardSink(store, New(srv.URL, testAdminKey),
		WithRewardSinkMaxAttempts(8),
		WithRewardSinkBackoff(func(int) time.Duration { return 0 }),
		WithRewardSinkNow(func() time.Time { return store.now }),
	)
	store.rows = append(store.rows, &fakeOutboxRow{
		id: "ob1", trajectoryID: "t1", proxyKey: sinkTestProxyKey,
		reward: 0.5, status: "pending", nextAt: store.now,
	})

	if _, err := sink.DeliverOnce(context.Background(), 10); err != nil {
		t.Fatalf("DeliverOnce: %v", err)
	}
	if got := store.row("ob1").proxyKey; got != sinkTestProxyKey {
		t.Fatal("proxy key must survive transient failures (needed for retry)")
	}

	if _, err := sink.DeliverOnce(context.Background(), 10); err != nil {
		t.Fatalf("DeliverOnce: %v", err)
	}
	if got := store.row("ob1").proxyKey; got != "" {
		t.Fatal("proxy key must be cleared only after the durable terminal ack")
	}
}

func TestRewardSinkRedactsProxyKeyInErrors(t *testing.T) {
	store := &fakeOutboxStore{now: time.Now()}
	bridge := &setRewardBridge{status: []int{500}, key: sinkTestProxyKey}
	srv := httptest.NewServer(http.HandlerFunc(bridge.handler))
	defer srv.Close()
	sink := NewRewardSink(store, New(srv.URL, testAdminKey),
		WithRewardSinkMaxAttempts(8),
		WithRewardSinkBackoff(func(int) time.Duration { return 0 }),
		WithRewardSinkNow(func() time.Time { return store.now }),
	)
	store.rows = append(store.rows, &fakeOutboxRow{
		id: "ob1", trajectoryID: "t1", proxyKey: sinkTestProxyKey,
		reward: 0.5, status: "pending", nextAt: store.now,
	})

	_, err := sink.DeliverOnce(context.Background(), 10)
	if err != nil && strings.Contains(err.Error(), sinkTestProxyKey) {
		t.Fatalf("returned error leaks proxy key: %v", err)
	}
	if last := store.row("ob1").lastErr; strings.Contains(last, sinkTestProxyKey) {
		t.Fatalf("stored last_error leaks proxy key: %q", last)
	}
	if store.row("ob1").status != "pending" {
		t.Fatalf("status = %q, want pending (retryable 5xx)", store.row("ob1").status)
	}
}

func TestRewardSinkClientErrorFailsTerminally(t *testing.T) {
	store := &fakeOutboxStore{now: time.Now()}
	bridge := &setRewardBridge{status: []int{400}, key: sinkTestProxyKey}
	srv := httptest.NewServer(http.HandlerFunc(bridge.handler))
	defer srv.Close()
	sink := NewRewardSink(store, New(srv.URL, testAdminKey),
		WithRewardSinkMaxAttempts(8),
		WithRewardSinkBackoff(func(int) time.Duration { return 0 }),
		WithRewardSinkNow(func() time.Time { return store.now }),
	)
	store.rows = append(store.rows, &fakeOutboxRow{
		id: "ob1", trajectoryID: "t1", proxyKey: sinkTestProxyKey,
		reward: 0.5, status: "pending", nextAt: store.now,
	})

	if _, err := sink.DeliverOnce(context.Background(), 10); err != nil {
		t.Fatalf("DeliverOnce: %v", err)
	}
	if store.row("ob1").status != "failed" {
		t.Fatalf("status = %q, want failed (non-retryable 4xx)", store.row("ob1").status)
	}
	if strings.Contains(store.row("ob1").lastErr, sinkTestProxyKey) {
		t.Fatalf("stored last_error leaks proxy key: %q", store.row("ob1").lastErr)
	}
	// Terminal rows are never claimed again.
	if _, err := sink.DeliverOnce(context.Background(), 10); err != nil {
		t.Fatalf("DeliverOnce: %v", err)
	}
	if got := bridge.calls(); got != 1 {
		t.Fatalf("set_reward calls = %d, want exactly 1", got)
	}
}

func TestRewardSinkMaxAttemptsExhausted(t *testing.T) {
	store := &fakeOutboxStore{now: time.Now()}
	bridge := &setRewardBridge{status: []int{500}, key: sinkTestProxyKey}
	srv := httptest.NewServer(http.HandlerFunc(bridge.handler))
	defer srv.Close()
	sink := NewRewardSink(store, New(srv.URL, testAdminKey),
		WithRewardSinkMaxAttempts(8),
		WithRewardSinkBackoff(func(int) time.Duration { return 0 }),
		WithRewardSinkNow(func() time.Time { return store.now }),
	)
	store.rows = append(store.rows, &fakeOutboxRow{
		id: "ob1", trajectoryID: "t1", proxyKey: sinkTestProxyKey,
		reward: 0.5, status: "pending", nextAt: store.now, attempts: 7,
	})

	if _, err := sink.DeliverOnce(context.Background(), 10); err != nil {
		t.Fatalf("DeliverOnce: %v", err)
	}
	if store.row("ob1").status != "failed" {
		t.Fatalf("status = %q, want failed (attempts exhausted)", store.row("ob1").status)
	}
}

func TestRewardSinkMissingKeyFailsWithoutCall(t *testing.T) {
	store := &fakeOutboxStore{now: time.Now()}
	bridge := &setRewardBridge{status: []int{200}, key: sinkTestProxyKey}
	srv := httptest.NewServer(http.HandlerFunc(bridge.handler))
	defer srv.Close()
	sink := NewRewardSink(store, New(srv.URL, testAdminKey),
		WithRewardSinkMaxAttempts(8),
		WithRewardSinkBackoff(func(int) time.Duration { return 0 }),
		WithRewardSinkNow(func() time.Time { return store.now }),
	)
	store.rows = append(store.rows, &fakeOutboxRow{
		id: "ob1", trajectoryID: "t1", proxyKey: "",
		reward: 0.5, status: "pending", nextAt: store.now,
	})

	if _, err := sink.DeliverOnce(context.Background(), 10); err != nil {
		t.Fatalf("DeliverOnce: %v", err)
	}
	if got := bridge.calls(); got != 0 {
		t.Fatalf("set_reward calls = %d, want 0 (no key, no request)", got)
	}
	if store.row("ob1").status != "failed" {
		t.Fatalf("status = %q, want failed (missing proxy key)", store.row("ob1").status)
	}
}

func TestSetRewardTypedHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"detail":"busy"}`))
	}))
	defer srv.Close()

	c := New(srv.URL, testAdminKey)
	err := c.SetReward(context.Background(), sinkTestProxyKey, 1.0)
	if err == nil {
		t.Fatal("expected error on 503, got nil")
	}
	var he *HTTPError
	if !errors.As(err, &he) {
		t.Fatalf("error type = %T, want *HTTPError", err)
	}
	if he.Status != http.StatusServiceUnavailable {
		t.Fatalf("HTTPError.Status = %d, want 503", he.Status)
	}
}
