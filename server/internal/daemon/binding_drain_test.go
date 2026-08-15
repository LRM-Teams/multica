package daemon

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestBindingDrainWaitsThenForcesOnlyManagedProcess(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	backend := &canonicalRuntimeTestBackend{}
	gracefulCancelled := false
	pool := newCanonicalAgentRuntimePool()
	pool.slots["agent-1\x00runtime-1"] = &canonicalAgentRuntimeSlot{backend: backend, running: true}
	d := &Daemon{
		canonicalRuntimes: pool,
		logger:            slog.New(slog.NewTextHandler(io.Discard, nil)),
		bindingDrainNow:   func() time.Time { return now },
		bindingDrainWait: func(ctx context.Context, delay time.Duration) error {
			if !gracefulCancelled {
				return fmt.Errorf("managed task was not asked to stop before force deadline")
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}
			now = now.Add(delay)
			return nil
		},
	}
	d.activeTasks.Store(1)
	d.registerManagedTask(1, func() { gracefulCancelled = true })
	if err := d.beginBindingDrain(context.Background()); err != nil {
		t.Fatal(err)
	}
	if elapsed := now.Sub(time.Unix(1_700_000_000, 0)); elapsed != bindingDrainGracefulTimeout {
		t.Fatalf("force elapsed = %s, want %s", elapsed, bindingDrainGracefulTimeout)
	}
	if got := backend.forceKillCount(); got != 1 {
		t.Fatalf("managed backend force kills = %d, want 1", got)
	}
	if !d.pauseClaims {
		t.Fatal("successful Binding drain released claim barrier")
	}
}

func TestBindingDrainNeverForcesUnownedBackend(t *testing.T) {
	pool := newCanonicalAgentRuntimePool()
	pool.slots["agent-1\x00runtime-1"] = &canonicalAgentRuntimeSlot{backend: &canonicalRuntimeNonForceKillableTestBackend{}, running: true}
	now := time.Unix(1_700_000_010, 0)
	d := &Daemon{
		canonicalRuntimes: pool,
		logger:            slog.New(slog.NewTextHandler(io.Discard, nil)),
		bindingDrainNow:   func() time.Time { return now },
		bindingDrainWait: func(_ context.Context, delay time.Duration) error {
			now = now.Add(delay)
			return nil
		},
	}
	d.activeTasks.Store(1)
	if err := d.beginBindingDrain(context.Background()); err == nil {
		t.Fatal("unowned backend force path unexpectedly succeeded")
	}
}

func TestBindingDrainFailsClosedWhenClaimIsStillInFlight(t *testing.T) {
	now := time.Unix(1_700_000_020, 0)
	d := &Daemon{
		logger:          slog.New(slog.NewTextHandler(io.Discard, nil)),
		bindingDrainNow: func() time.Time { return now },
		bindingDrainWait: func(_ context.Context, delay time.Duration) error {
			now = now.Add(delay)
			return nil
		},
	}
	if !d.tryEnterClaim() {
		t.Fatal("pre-barrier claim was not admitted")
	}
	err := d.beginBindingDrain(context.Background())
	if err == nil || !strings.Contains(err.Error(), "claim") {
		t.Fatalf("drain error = %v, want an in-flight claim failure", err)
	}
	if d.pauseClaims {
		t.Fatal("failed Binding drain left the claim barrier set")
	}
	if d.claimsInFlight != 1 {
		t.Fatalf("claimsInFlight = %d, want the admitted claim to remain accounted", d.claimsInFlight)
	}
	d.exitClaim()
}
