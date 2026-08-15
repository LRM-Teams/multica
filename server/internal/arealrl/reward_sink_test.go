// SPDX-License-Identifier: Apache-2.0

package arealrl

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// rewardServerStub captures /rl/set_reward requests.
type rewardServerStub struct {
	mu     sync.Mutex
	calls  []rewardCall
	status int
}

type rewardCall struct {
	auth   string
	reward float64
}

func (s *rewardServerStub) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /rl/set_reward", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var parsed struct {
			Reward float64 `json:"reward"`
		}
		_ = json.Unmarshal(body, &parsed)
		s.mu.Lock()
		s.calls = append(s.calls, rewardCall{auth: r.Header.Get("Authorization"), reward: parsed.Reward})
		s.mu.Unlock()
		w.WriteHeader(s.status)
	})
	return mux
}

func (s *rewardServerStub) captured() []rewardCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]rewardCall, len(s.calls))
	copy(out, s.calls)
	return out
}

// TestRewardSinkForwardsComposedReward: a registered trace resolves to its
// session proxy key (task context areal_proxy) and the composed reward is
// posted with session-key auth.
func TestRewardSinkForwardsComposedReward(t *testing.T) {
	stub := &rewardServerStub{status: http.StatusOK}
	srv := httptest.NewServer(stub.handler())
	defer srv.Close()

	sink := NewRewardSink(New(srv.URL, "admin-key"))
	sink.RegisterTrace("trace-1", "proxy-key-1")

	if err := sink.SetReward(context.Background(), "trace-1", 0.7); err != nil {
		t.Fatalf("SetReward: %v", err)
	}
	calls := stub.captured()
	if len(calls) != 1 {
		t.Fatalf("set_reward calls = %d, want 1", len(calls))
	}
	if calls[0].auth != "Bearer proxy-key-1" {
		t.Fatalf("auth = %q, want session-key bearer of the trace's proxy key", calls[0].auth)
	}
	if calls[0].reward != 0.7 {
		t.Fatalf("reward = %v, want 0.7", calls[0].reward)
	}

	// The registration is consumed: a second reward for the same trace (or a
	// trace that never registered — a non-training task recall) is skipped
	// silently without an HTTP call.
	for _, traceID := range []string{"trace-1", "never-registered"} {
		if err := sink.SetReward(context.Background(), traceID, -1.0); err != nil {
			t.Fatalf("SetReward %s: %v", traceID, err)
		}
	}
	if calls := stub.captured(); len(calls) != 1 {
		t.Fatalf("set_reward calls = %d, want still 1 (unregistered traces skipped silently)", len(calls))
	}
}

// TestRewardSinkPropagatesBridgeError: a non-2xx bridge response surfaces as
// an error so the composer logs the failed push.
func TestRewardSinkPropagatesBridgeError(t *testing.T) {
	stub := &rewardServerStub{status: http.StatusInternalServerError}
	srv := httptest.NewServer(stub.handler())
	defer srv.Close()

	sink := NewRewardSink(New(srv.URL, "admin-key"))
	sink.RegisterTrace("trace-1", "proxy-key-1")
	if err := sink.SetReward(context.Background(), "trace-1", 1.0); err == nil {
		t.Fatal("SetReward = nil, want a bridge status error")
	}
}
