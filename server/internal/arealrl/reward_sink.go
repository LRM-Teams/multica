// SPDX-License-Identifier: Apache-2.0

package arealrl

import (
	"context"
	"sync"
)

// RewardSink adapts the RL bridge SetReward endpoint to the memorygraph
// RewardSink interface (design §5.3, Q28). The memorygraph reward composer
// pushes one composed reward per explore trace, keyed by trace id; the sink
// resolves each trace id to the session proxy key registered for it and
// forwards Client.SetReward.
//
// The proxy key of an explore trace comes from the owning task's
// context.areal_proxy (api_key, see training.go maybeOpenTrainingSession):
// the caller registers it at recall time via RegisterTrace. A trace with no
// registered key — a recall of a non-training task — has nowhere to push a
// reward to and is skipped silently (SetReward returns nil).
type RewardSink struct {
	client *Client

	mu   sync.Mutex
	keys map[string]string // trace id -> session proxy key
}

// NewRewardSink returns a RewardSink forwarding to client. The client's
// admin key is unused: SetReward authenticates with the per-session proxy
// key, mirroring the session-close path.
func NewRewardSink(client *Client) *RewardSink {
	return &RewardSink{client: client, keys: make(map[string]string)}
}

// RegisterTrace binds an explore trace id to the session proxy key of the
// task the recall served. Registration happens once per trace at recall
// time; the binding is consumed by the first SetReward for the trace (the
// composer pushes exactly one reward per trace — composed on judge
// write-back or swept on timeout).
func (s *RewardSink) RegisterTrace(traceID, proxyKey string) {
	if traceID == "" || proxyKey == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.keys[traceID] = proxyKey
}

// SetReward pushes the composed reward for traceID to the RL bridge. Traces
// without a registered proxy key (non-training tasks) are skipped silently.
func (s *RewardSink) SetReward(ctx context.Context, traceID string, reward float64) error {
	s.mu.Lock()
	key, ok := s.keys[traceID]
	if ok {
		delete(s.keys, traceID)
	}
	s.mu.Unlock()
	if !ok {
		return nil
	}
	return s.client.SetReward(ctx, key, reward)
}
