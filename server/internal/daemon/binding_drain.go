package daemon

import (
	"context"
	"fmt"
	"time"
)

const bindingDrainGracefulTimeout = 10 * time.Second

func (d *Daemon) registerManagedTask(slot int, cancel context.CancelFunc) {
	if cancel == nil {
		return
	}
	d.managedTaskMu.Lock()
	if d.managedTaskCancels == nil {
		d.managedTaskCancels = make(map[int64]context.CancelFunc)
	}
	d.managedTaskCancels[int64(slot)] = cancel
	d.managedTaskMu.Unlock()
}

func (d *Daemon) unregisterManagedTask(slot int) {
	d.managedTaskMu.Lock()
	delete(d.managedTaskCancels, int64(slot))
	d.managedTaskMu.Unlock()
}

func (d *Daemon) requestManagedTaskTermination() {
	d.managedTaskMu.Lock()
	cancels := make([]context.CancelFunc, 0, len(d.managedTaskCancels))
	for _, cancel := range d.managedTaskCancels {
		cancels = append(cancels, cancel)
	}
	d.managedTaskMu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
}

func (d *Daemon) forceTerminateManagedAgentProcesses() error {
	if d.canonicalRuntimes != nil {
		if err := d.canonicalRuntimes.forceTerminateAll(); err != nil {
			return fmt.Errorf("canonical managed runtime: %w", err)
		}
	}
	return nil
}

func (d *Daemon) bindingDrainTimeNow() time.Time {
	if d.bindingDrainNow != nil {
		return d.bindingDrainNow()
	}
	return time.Now()
}

func (d *Daemon) waitBindingDrain(ctx context.Context, delay time.Duration) error {
	if d.bindingDrainWait != nil {
		return d.bindingDrainWait(ctx, delay)
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// beginWorkspaceDaemonDrain is the WorkspaceDaemon's complete execution-plane response
// to a Computer prepare request. It closes the claim gate, asks only its own
// turns to stop, and bounds their graceful drain before force-terminating only
// provider processes owned by this WorkspaceDaemon. Machine Upgrade orchestration,
// journaling, activation, and successor convergence remain Computer-owned.
func (d *Daemon) beginWorkspaceDaemonDrain(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	d.setClaimBarrier()
	succeeded := false
	defer func() {
		if !succeeded {
			d.releaseClaimBarrier()
		}
	}()
	d.requestManagedTaskTermination()
	deadline := d.bindingDrainTimeNow().Add(bindingDrainGracefulTimeout)
	for !d.claimBarrierDrained() {
		remaining := deadline.Sub(d.bindingDrainTimeNow())
		if remaining <= 0 {
			break
		}
		if remaining > 100*time.Millisecond {
			remaining = 100 * time.Millisecond
		}
		if err := d.waitBindingDrain(ctx, remaining); err != nil {
			return fmt.Errorf("graceful Binding drain: %w", err)
		}
	}
	if d.claimBarrierDrained() {
		succeeded = true
		return nil
	}
	d.claimMu.Lock()
	claimsInFlight := d.claimsInFlight
	d.claimMu.Unlock()
	if claimsInFlight > 0 {
		return fmt.Errorf("Binding drain deadline elapsed with %d claims still in flight", claimsInFlight)
	}
	if err := d.forceTerminateManagedAgentProcesses(); err != nil {
		return fmt.Errorf("force Binding drain: %w", err)
	}
	succeeded = true
	return nil
}
