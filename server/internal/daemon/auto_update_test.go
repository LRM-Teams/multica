package daemon

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// #2379 regression guard: time advancing, whether through the compatibility
// loop seam or direct legacy helper, has no authority to fetch, stage,
// activate, or restart a release. Those effects belong to a canonical
// explicit Machine Upgrade operation only.
func TestAutoUpdateCompatibilitySeamsNeverMutate(t *testing.T) {
	var mutations atomic.Int64
	d := &Daemon{
		cfg: Config{CLIVersion: "v9.9.9", AutoUpdateEnabled: true, AutoUpdateCheckInterval: time.Nanosecond},
		runUpdateFn: func(string) (string, error) {
			mutations.Add(1)
			return "", nil
		},
		verifyUpdatedBinaryFn: func(string, string) (string, error) {
			mutations.Add(1)
			return "", nil
		},
		activateStagedFn: func(context.Context, string, string) (string, error) {
			mutations.Add(1)
			return "", nil
		},
		cancelFunc: func() { mutations.Add(1) },
	}
	for range 100 {
		d.autoUpdateLoop(context.Background())
		d.tryAutoUpdate(context.Background())
	}
	if got := mutations.Load(); got != 0 {
		t.Fatalf("legacy elapsed-time update path mutated %d times", got)
	}
}

func TestAutoUpdateCompatibilityLoopDoesNotBlockOnTime(t *testing.T) {
	done := make(chan struct{})
	go func() {
		(&Daemon{}).autoUpdateLoop(context.Background())
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("deprecated loop scheduled a delay or ticker")
	}
}
