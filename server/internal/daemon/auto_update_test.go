package daemon

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// #2379 regression guard: the detection path (autoUpdateLoop/checkForNewerRelease)
// and the legacy tryAutoUpdate helper have no authority to fetch, stage,
// activate, or restart a release. Detection only records a target version on
// the update observation; install belongs to a canonical explicit Machine
// Upgrade operation only.
func TestAutoUpdateDetectionNeverMutatesRelease(t *testing.T) {
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
	// updateObservation is nil, so the detection path returns without doing
	// anything (no manifest fetch, no observation write); tryAutoUpdate is a
	// no-op. Neither should ever touch the release-mutation seams.
	for range 100 {
		d.checkForNewerRelease(context.Background())
		d.tryAutoUpdate(context.Background())
	}
	if got := mutations.Load(); got != 0 {
		t.Fatalf("detection/legacy path mutated %d times", got)
	}
}

func TestAutoUpdateLoopSkipsWhenIntervalDisabled(t *testing.T) {
	// An unconfigured daemon (interval <= 0) must not start a detection
	// loop that blocks on a timer/ticker; it returns immediately.
	done := make(chan struct{})
	go func() {
		(&Daemon{cfg: Config{AutoUpdateCheckInterval: 0}}).autoUpdateLoop(context.Background())
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("autoUpdateLoop scheduled a delay/ticker when interval is disabled")
	}
}
