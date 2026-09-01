package daemon

import (
	"context"
	"testing"
	"time"
)

func TestWorkspaceDaemonProviderStartGateBoundsConcurrencyAndCancelsWaiter(t *testing.T) {
	gate := newWorkspaceDaemonProviderStartGate(1)
	release, err := gate.acquire(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	canceled := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := gate.acquire(context.Background(), canceled)
		done <- err
	}()
	close(canceled)
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("canceled provider start waiter acquired a slot")
		}
	case <-time.After(time.Second):
		t.Fatal("canceled provider start waiter remained blocked")
	}
	release()
}
