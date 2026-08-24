package daemon

import (
	"context"
	"sync"
	"time"
)

const (
	// residentCrashCheckInterval is how often idle resident provider
	// processes are polled for liveness (task #42②). It runs independently
	// of any dispatched task, so a crash is caught even if nothing gets
	// dispatched to the dead agent for hours — the exact gap that let the
	// ui-designer agent's opencode process sit dead for 8 hours unnoticed.
	residentCrashCheckInterval = 20 * time.Second
	// residentCrashBackoffWindow bounds how far back repeated crashes are
	// counted toward the retry cap. A crash outside this window does not
	// count against a currently-healthy runtime.
	residentCrashBackoffWindow = 10 * time.Minute
	// residentCrashRetryCap is how many crashes within the window are
	// treated as recoverable (silently evicted, left for the next task to
	// recreate) before the runtime is flagged terminal and stops being
	// auto-recovered.
	residentCrashRetryCap = 5
)

// residentCrashWatchLoop periodically checks every idle resident canonical
// runtime slot for a dead process and evicts/reports any it finds — see
// agentRuntimePool.checkResidentLiveness. Detection here is
// deliberately decoupled from task dispatch: the pool's existing
// reuse-or-recreate logic already self-heals the next time a turn is
// attempted against a slot, but nothing previously noticed a crash before
// that next attempt, which is exactly what let a dead resident process sit
// undetected for hours.
//
// ⚠️ Load-bearing precondition for whoever builds task #50 ("don't dispatch
// to a dead agent"): the actual relaunch in ② only happens on the NEXT
// acquire() — this loop only detects+evicts, it does not itself recreate a
// process. If #50 responds to "agent crashed" by blanket-blocking dispatch
// to that agent, no task ever arrives to trigger the lazy recreate, and the
// agent never comes back — the self-heal and the dispatch-gate deadlock each
// other. #50 must distinguish "provider process died but the daemon/machine
// is alive" (recoverable — keep dispatching, that's what triggers recovery)
// from "the whole daemon/machine is offline" (not recoverable by
// recreating a process — don't dispatch). Barry's signal for telling them
// apart: whether every runtime under the same daemon_id went silent at the
// same instant (whole-machine/daemon down) vs. just this one agent's
// resident process (individually recoverable).
func (d *Daemon) residentCrashWatchLoop(ctx context.Context) {
	if d.canonicalRuntimes == nil {
		return
	}
	ticker := time.NewTicker(residentCrashCheckInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.canonicalRuntimes.checkResidentLiveness(time.Now())
		}
	}
}

// onResidentProcessEvent is the daemon's single subscriber to the
// agentRuntimePool resident process event bus
// (resident_process_event.go). It routes each kind to its own handler; see
// §3 of the change that introduced this file for why exited/recovered/
// stalled are handled here instead of each having their own subscription.
func (d *Daemon) onResidentProcessEvent(ev residentProcessEvent) {
	if d == nil {
		return
	}
	switch ev.Kind {
	case residentProcessExited:
		d.onResidentRuntimeExited(ev)
	case residentProcessRecovered:
		d.clearAgentProviderCrashedOnServer(ev.RuntimeID, ev.AgentID)
	case residentProcessStalled:
		d.observeResidentRuntimeStalled(ev.AgentID, ev.RuntimeID, ev.SilentFor)
	}
}

// onResidentRuntimeExited is the daemon's handler for a resident provider
// process the pool found dead — it tracks the retry-cap/backoff bookkeeping,
// tells agentProcessManager (APM) the launch it believes is still running
// has in fact exited, and best-effort reports the crash fact to the server
// so GET /agents can show "crashed" (Parker Raft status ②). Mid-turn
// process_failure deliberately does NOT call this path.
//
// Order matters: the local launch fact (APM) must settle before the
// server-facing report, so a concurrent Snapshot/dispatch decision on this
// daemon never observes "server knows it crashed" before "APM knows the
// launch is no longer running".
func (d *Daemon) onResidentRuntimeExited(ev residentProcessEvent) {
	if d.residentCrashBackoff == nil {
		return
	}
	attempt, backoff, terminal := d.residentCrashBackoff.recordCrash(ev.AgentID, ev.RuntimeID, ev.At)

	// 1. Local launch fact: tell APM the process behind this launch exited.
	// A resident slot with no matching APM launch (runtime detached, launch
	// never reached APM) is not an error — there is simply nothing to route
	// to, so neither runner verb below is called unless the lookup
	// succeeds. terminal decides which of the two single-purpose verbs
	// runs — the decision lives here because this is where terminal is
	// computed; the runner verbs themselves carry no branch.
	if launch, runner, found := d.resolveManagedLaunch(ev.AgentID, ev.RuntimeID); found {
		var routeErr error
		if terminal {
			routeErr = runner.retireManagedLaunchAfterExit(ev.AgentID, ev.RuntimeID, launch, "provider_crash_looping")
		} else {
			routeErr = runner.retryManagedLaunchAfterExit(ev.AgentID, launch)
		}
		if routeErr != nil {
			d.logger.Debug("resident process exited callback rejected (stale launch)",
				"agent_id", ev.AgentID, "runtime_id", ev.RuntimeID, "error", routeErr)
		}
	}

	// 2. Server-facing report. Best-effort: local recovery continues even if
	// the server call fails.
	if d.client != nil && ev.AgentID != "" && ev.RuntimeID != "" {
		if err := d.client.ReportAgentProviderCrashed(context.Background(), ev.RuntimeID, ev.AgentID); err != nil {
			d.logger.Debug("report agent provider crashed failed; continuing",
				"agent_id", ev.AgentID, "runtime_id", ev.RuntimeID, "error", err)
		}
	}

	// 3. Logging, unchanged from the original crash-only subscriber.
	if terminal {
		d.logger.Warn("resident runtime crash-looping, auto-recovery stopped",
			"agent_id", ev.AgentID, "runtime_id", ev.RuntimeID, "provider", ev.Provider,
			"attempt", attempt, "window", residentCrashBackoffWindow, "cap", residentCrashRetryCap)
		return
	}
	d.logger.Warn("resident runtime crashed, will recover on next dispatch",
		"agent_id", ev.AgentID, "runtime_id", ev.RuntimeID, "provider", ev.Provider,
		"attempt", attempt, "backoff", backoff)
}

// clearAgentProviderCrashedOnServer clears the server-side crashed_since after
// local recovery (successful resident recreate or lifecycle restart).
func (d *Daemon) clearAgentProviderCrashedOnServer(runtimeID, agentID string) {
	if d == nil || d.client == nil || runtimeID == "" || agentID == "" {
		return
	}
	if err := d.client.ClearAgentProviderCrashed(context.Background(), runtimeID, agentID); err != nil {
		d.logger.Debug("clear agent provider crashed failed; continuing",
			"agent_id", agentID, "runtime_id", runtimeID, "error", err)
	}
}

// residentCrashBackoffTracker counts crashes per agent×runtime within a
// rolling window and computes the backoff/retry-cap decision for task #42②.
// It intentionally does not itself relaunch anything — the pool already
// recreates a resident backend lazily on the next acquire() after an evicted
// slot; this tracker's job is only to decide when repeated crashes stop
// being "transient, let it recover" and become "crash-looping, needs a human
// via manual restart (#42①)".
type residentCrashBackoffTracker struct {
	mu     sync.Mutex
	window time.Duration
	cap    int
	// crashes maps agentID\x00runtimeID -> crash timestamps within window,
	// oldest first.
	crashes map[string][]time.Time
	// terminal marks slots that exceeded the retry cap, until cleared by a
	// manual restart (#42①) or a crash outside the window ages the count
	// back under the cap.
	terminal map[string]bool
}

func newResidentCrashBackoffTracker(window time.Duration, cap int) *residentCrashBackoffTracker {
	return &residentCrashBackoffTracker{
		window:   window,
		cap:      cap,
		crashes:  make(map[string][]time.Time),
		terminal: make(map[string]bool),
	}
}

// residentCrashBackoffSchedule is the delay suggested before the next
// automatic recovery attempt, indexed by (1-based) attempt number within the
// current window. The last entry repeats for any attempt beyond its index.
var residentCrashBackoffSchedule = []time.Duration{
	5 * time.Second,
	15 * time.Second,
	60 * time.Second,
}

func backoffForAttempt(attempt int) time.Duration {
	if attempt <= 0 {
		return 0
	}
	if attempt > len(residentCrashBackoffSchedule) {
		return residentCrashBackoffSchedule[len(residentCrashBackoffSchedule)-1]
	}
	return residentCrashBackoffSchedule[attempt-1]
}

// recordCrash records a crash at now, prunes crashes older than the window,
// and returns the attempt number (crashes within the window, including this
// one), the suggested backoff before the next recovery attempt, and whether
// the retry cap has now been exceeded (terminal — auto-recovery should stop
// until a manual restart).
func (t *residentCrashBackoffTracker) recordCrash(agentID, runtimeID string, now time.Time) (attempt int, backoff time.Duration, terminal bool) {
	if t == nil {
		return 0, 0, false
	}
	key := agentID + "\x00" + runtimeID
	t.mu.Lock()
	defer t.mu.Unlock()

	cutoff := now.Add(-t.window)
	kept := t.crashes[key][:0]
	for _, ts := range t.crashes[key] {
		if ts.After(cutoff) {
			kept = append(kept, ts)
		}
	}
	kept = append(kept, now)
	t.crashes[key] = kept

	attempt = len(kept)
	terminal = attempt > t.cap
	t.terminal[key] = terminal
	return attempt, backoffForAttempt(attempt), terminal
}

// isTerminal reports whether agentID/runtimeID is currently flagged
// crash-looping (exceeded the retry cap and not yet manually restarted).
// Dispatch-gating (task #50) and status reporting (task #42③) can consult
// this instead of re-deriving it from raw crash history.
func (t *residentCrashBackoffTracker) isTerminal(agentID, runtimeID string) bool {
	if t == nil {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.terminal[agentID+"\x00"+runtimeID]
}

// clear resets the crash history for agentID/runtimeID — called after a
// manual restart (task #42①) so the runtime gets a fresh retry budget
// instead of immediately re-tripping the cap from stale crash history.
func (t *residentCrashBackoffTracker) clear(agentID, runtimeID string) {
	if t == nil {
		return
	}
	key := agentID + "\x00" + runtimeID
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.crashes, key)
	delete(t.terminal, key)
}
