package daemon

import (
	"context"
	"sync"
)

const workspaceDaemonProviderStartConcurrency = 8

type workspaceDaemonProviderStartGate struct {
	slots chan struct{}
}

func newWorkspaceDaemonProviderStartGate(limit int) *workspaceDaemonProviderStartGate {
	if limit <= 0 {
		limit = workspaceDaemonProviderStartConcurrency
	}
	return &workspaceDaemonProviderStartGate{slots: make(chan struct{}, limit)}
}

func (gate *workspaceDaemonProviderStartGate) acquire(ctx context.Context, canceled <-chan struct{}) (func(), error) {
	if gate == nil || gate.slots == nil {
		return func() {}, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case gate.slots <- struct{}{}:
		var once sync.Once
		return func() { once.Do(func() { <-gate.slots }) }, nil
	case <-canceled:
		return nil, errManagedAgentStartStopped
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
