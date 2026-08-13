package daemon

import (
	"context"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/computer"
)

func TestWorkspaceRunnerReconciliationRetainsSiblingsAndCancelsRemovedBinding(t *testing.T) {
	d := New(Config{DaemonID: "daemon-test"}, nil)
	d.workspaces = map[string]*workspaceState{
		"workspace-a": newWorkspaceState("workspace-a", []string{"runtime-a"}),
		"workspace-b": newWorkspaceState("workspace-b", nil),
	}
	var mu sync.Mutex
	started := make(map[string]context.Context)
	starts := make(map[string]int)
	d.workspaceRunnerRun = func(runner *WorkspaceRunner, ctx context.Context) {
		mu.Lock()
		started[runner.config.WorkspaceID] = ctx
		starts[runner.config.WorkspaceID]++
		mu.Unlock()
		<-ctx.Done()
	}
	d.reconcileWorkspaceRunners(context.Background())
	waitForStarted(t, &mu, started, "workspace-a")
	waitForStarted(t, &mu, started, "workspace-b")
	firstA := d.currentWorkspaceRunner("workspace-a")
	firstB := d.currentWorkspaceRunner("workspace-b")
	if firstA == nil || firstB == nil {
		t.Fatalf("initial Binding reconciliation runners=%p,%p", firstA, firstB)
	}

	// Runtime membership is mutable input. Reconciliation must retain both
	// runner objects, including the zero-Runtime Binding.
	d.mu.Lock()
	d.workspaces["workspace-a"] = newWorkspaceState("workspace-a", []string{"runtime-a", "runtime-a2"})
	d.mu.Unlock()
	d.reconcileWorkspaceRunners(context.Background())
	mu.Lock()
	startCount := len(started)
	mu.Unlock()
	if d.currentWorkspaceRunner("workspace-a") != firstA || d.currentWorkspaceRunner("workspace-b") != firstB || startCount != 2 {
		t.Fatal("runtime change replaced a stable Workspace Runner")
	}

	d.mu.Lock()
	delete(d.workspaces, "workspace-b")
	d.mu.Unlock()
	d.reconcileWorkspaceRunners(context.Background())
	if d.currentWorkspaceRunner("workspace-a") != firstA {
		t.Fatal("removing sibling Binding replaced unchanged Runner")
	}
	if d.currentWorkspaceRunner("workspace-b") != nil {
		t.Fatal("removed Binding retained its Runner")
	}
	mu.Lock()
	ctxB := started["workspace-b"]
	ctxA := started["workspace-a"]
	mu.Unlock()
	select {
	case <-ctxB.Done():
	default:
		t.Fatal("removed Binding did not cancel only its Runner")
	}
	select {
	case <-ctxA.Done():
		t.Fatal("sibling Runner was cancelled")
	default:
	}

	d.mu.Lock()
	d.workspaces["workspace-b"] = newWorkspaceState("workspace-b", nil)
	d.mu.Unlock()
	d.reconcileWorkspaceRunners(context.Background())
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		count := starts["workspace-b"]
		mu.Unlock()
		if count == 2 && d.currentWorkspaceRunner("workspace-b") != nil {
			break
		}
		time.Sleep(time.Millisecond)
	}
	mu.Lock()
	countB := starts["workspace-b"]
	mu.Unlock()
	if countB != 2 {
		t.Fatalf("re-added Binding starts = %d, want 2", countB)
	}
	if rec := d.workspaceRunnerRecords["workspace-b"]; rec == nil || rec.Lifecycle != computer.RunnerLifecycleRunning {
		t.Fatal("re-added Binding stayed degraded after graceful unlink")
	}
}

func TestWorkspaceRunnerStaleObserveDoesNotMutateNextGeneration(t *testing.T) {
	d := New(Config{DaemonID: "daemon-test"}, nil)
	d.workspaces = map[string]*workspaceState{
		"workspace-a": newWorkspaceState("workspace-a", []string{"runtime-a"}),
	}
	d.workspaceRunnerRun = func(_ *WorkspaceRunner, ctx context.Context) {
		<-ctx.Done()
	}
	parent := context.Background()
	d.reconcileWorkspaceRunners(parent)
	waitForRunnerLifecycle(t, d, "workspace-a", computer.RunnerLifecycleRunning)
	firstGen := d.workspaceRunnerRecords["workspace-a"].Generation()

	d.mu.Lock()
	delete(d.workspaces, "workspace-a")
	d.mu.Unlock()
	d.reconcileWorkspaceRunners(parent)

	d.mu.Lock()
	d.workspaces["workspace-a"] = newWorkspaceState("workspace-a", []string{"runtime-a"})
	d.mu.Unlock()
	d.reconcileWorkspaceRunners(parent)
	waitForRunnerLifecycle(t, d, "workspace-a", computer.RunnerLifecycleRunning)
	live := d.currentWorkspaceRunner("workspace-a")
	if live == nil {
		t.Fatal("next generation Runner missing")
	}
	secondGen := d.workspaceRunnerRecords["workspace-a"].Generation()
	if secondGen == firstGen {
		t.Fatal("re-add reused the previous spawn generation")
	}

	// Old supervise observe(nil) — the path without a BindingChild handle.
	d.observeWorkspaceRunnerExit("workspace-a", firstGen, nil, computer.RunnerExitCrash)
	if d.currentWorkspaceRunner("workspace-a") != live {
		t.Fatal("stale observe replaced the live Runner")
	}
	rec := d.workspaceRunnerRecords["workspace-a"]
	if rec.Generation() != secondGen || rec.Lifecycle != computer.RunnerLifecycleRunning || !rec.HasChild() {
		t.Fatalf("stale observe mutated next generation: gen=%d lifecycle=%s child=%v", rec.Generation(), rec.Lifecycle, rec.HasChild())
	}
}

func TestWorkspaceRunnerCrashRestartsAfterBackoffAndThenDegrades(t *testing.T) {
	d := New(Config{DaemonID: "daemon-test"}, nil)
	d.workspaces = map[string]*workspaceState{
		"workspace-a": newWorkspaceState("workspace-a", []string{"runtime-a"}),
	}
	var now atomic.Value
	base := time.Date(2026, 8, 13, 16, 0, 0, 0, time.UTC)
	now.Store(base)
	d.workspaceRunnerNow = func() time.Time { return now.Load().(time.Time) }
	var starts atomic.Int32
	d.workspaceRunnerRun = func(*WorkspaceRunner, context.Context) {
		starts.Add(1)
	}

	parent := context.Background()
	d.reconcileWorkspaceRunners(parent)
	waitForRunnerLifecycle(t, d, "workspace-a", computer.RunnerLifecycleCrashed)
	if starts.Load() != 1 {
		t.Fatalf("starts after first crash = %d, want 1", starts.Load())
	}

	d.reconcileWorkspaceRunners(parent)
	if starts.Load() != 1 {
		t.Fatal("respawned during 2s backoff")
	}

	now.Store(base.Add(computer.RunnerRestartBackoff))
	d.reconcileWorkspaceRunners(parent)
	waitForStartCount(t, &starts, 2)
	waitForRunnerLifecycle(t, d, "workspace-a", computer.RunnerLifecycleCrashed)

	now.Store(base.Add(2 * computer.RunnerRestartBackoff))
	d.reconcileWorkspaceRunners(parent)
	waitForStartCount(t, &starts, 3)
	waitForRunnerLifecycle(t, d, "workspace-a", computer.RunnerLifecycleDegraded)

	now.Store(base.Add(time.Hour))
	d.reconcileWorkspaceRunners(parent)
	if starts.Load() != 3 {
		t.Fatalf("degraded runner auto-spawned, starts=%d", starts.Load())
	}
}

func TestWorkspaceRunnerOSChildCrashLeavesHostAliveThenDegrades(t *testing.T) {
	host := os.Getpid()
	falsePath, err := exec.LookPath("false")
	if err != nil {
		t.Skip("false not on PATH")
	}
	d := New(Config{DaemonID: "daemon-test"}, nil)
	d.workspaces = map[string]*workspaceState{
		"workspace-a": newWorkspaceState("workspace-a", []string{"runtime-a"}),
	}
	var now atomic.Value
	base := time.Date(2026, 8, 13, 17, 0, 0, 0, time.UTC)
	now.Store(base)
	d.workspaceRunnerNow = func() time.Time { return now.Load().(time.Time) }
	var starts atomic.Int32
	d.workspaceRunnerRun = func(*WorkspaceRunner, context.Context) {
		starts.Add(1)
	}
	d.workspaceRunnerSpawn = func(string) (computer.BindingChild, error) {
		return computer.StartBindingCommand(falsePath, nil)
	}

	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	d.reconcileWorkspaceRunners(parent)
	waitForRunnerLifecycle(t, d, "workspace-a", computer.RunnerLifecycleCrashed)
	if os.Getpid() != host {
		t.Fatal("host process died with Binding child")
	}
	if starts.Load() != 1 {
		t.Fatalf("starts after first OS child crash = %d", starts.Load())
	}

	d.reconcileWorkspaceRunners(parent)
	if starts.Load() != 1 {
		t.Fatal("OS child respawned during backoff")
	}

	now.Store(base.Add(computer.RunnerRestartBackoff))
	d.reconcileWorkspaceRunners(parent)
	waitForStartCount(t, &starts, 2)
	waitForRunnerLifecycle(t, d, "workspace-a", computer.RunnerLifecycleCrashed)

	now.Store(base.Add(2 * computer.RunnerRestartBackoff))
	d.reconcileWorkspaceRunners(parent)
	waitForStartCount(t, &starts, 3)
	waitForRunnerLifecycle(t, d, "workspace-a", computer.RunnerLifecycleDegraded)
	if os.Getpid() != host {
		t.Fatal("host process died after degraded Binding child")
	}
}

func TestWorkspaceRunnerCrashRespawnsFreshInstanceNotClosedOne(t *testing.T) {
	falsePath, err := exec.LookPath("false")
	if err != nil {
		t.Skip("false not on PATH")
	}
	d := New(Config{DaemonID: "daemon-test"}, nil)
	d.workspaces = map[string]*workspaceState{
		"workspace-a": newWorkspaceState("workspace-a", []string{"runtime-a"}),
	}
	var now atomic.Value
	base := time.Date(2026, 8, 13, 19, 0, 0, 0, time.UTC)
	now.Store(base)
	d.workspaceRunnerNow = func() time.Time { return now.Load().(time.Time) }
	sleepPath, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip("sleep not on PATH")
	}
	var first, second *WorkspaceRunner
	var starts atomic.Int32
	d.workspaceRunnerRun = func(runner *WorkspaceRunner, ctx context.Context) {
		n := starts.Add(1)
		if n == 1 {
			first = runner
			runner.Close()
			if runner.inboxes != nil {
				runner.inboxes.Close()
			}
			if runner.processes != nil {
				runner.processes.Close()
			}
			if runner.activity != nil {
				runner.activity.Close()
			}
			return
		}
		second = runner
		<-ctx.Done()
	}
	var spawns atomic.Int32
	d.workspaceRunnerSpawn = func(string) (computer.BindingChild, error) {
		if spawns.Add(1) == 1 {
			return computer.StartBindingCommand(falsePath, nil)
		}
		return computer.StartBindingCommand(sleepPath, []string{"30"})
	}

	parent := context.Background()
	d.reconcileWorkspaceRunners(parent)
	waitForRunnerLifecycle(t, d, "workspace-a", computer.RunnerLifecycleCrashed)
	if first == nil {
		t.Fatal("first runner missing")
	}

	now.Store(base.Add(computer.RunnerRestartBackoff))
	d.reconcileWorkspaceRunners(parent)
	waitForStartCount(t, &starts, 2)
	if second == nil || second == first {
		t.Fatal("crash respawn reused the Closed WorkspaceRunner")
	}
	if d.currentWorkspaceRunner("workspace-a") != second {
		t.Fatal("host did not keep the fresh WorkspaceRunner")
	}
}

func TestWorkspaceRunnerChildExitCancelsRunBeforeRespawn(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh not on PATH")
	}
	d := New(Config{DaemonID: "daemon-test"}, nil)
	d.workspaces = map[string]*workspaceState{
		"workspace-a": newWorkspaceState("workspace-a", []string{"runtime-a"}),
	}
	var now atomic.Value
	base := time.Date(2026, 8, 13, 18, 0, 0, 0, time.UTC)
	now.Store(base)
	d.workspaceRunnerNow = func() time.Time { return now.Load().(time.Time) }
	var running, maxRunning, entered atomic.Int32
	released := make(chan struct{})
	d.workspaceRunnerRun = func(_ *WorkspaceRunner, ctx context.Context) {
		n := running.Add(1)
		entered.Add(1)
		for {
			old := maxRunning.Load()
			if n <= old || maxRunning.CompareAndSwap(old, n) {
				break
			}
		}
		defer running.Add(-1)
		select {
		case released <- struct{}{}:
		default:
		}
		<-ctx.Done()
	}
	d.workspaceRunnerSpawn = func(string) (computer.BindingChild, error) {
		return computer.StartBindingCommand(sh, []string{"-c", "sleep 0.05; exit 1"})
	}

	parent := context.Background()
	d.reconcileWorkspaceRunners(parent)
	select {
	case <-released:
	case <-time.After(time.Second):
		t.Fatal("Run did not start")
	}
	waitForRunnerLifecycle(t, d, "workspace-a", computer.RunnerLifecycleCrashed)
	if got := maxRunning.Load(); got != 1 {
		t.Fatalf("concurrent Run loops = %d, want 1", got)
	}
	if entered.Load() != 1 {
		t.Fatalf("Run entered %d times before respawn, want 1", entered.Load())
	}

	now.Store(base.Add(computer.RunnerRestartBackoff))
	d.reconcileWorkspaceRunners(parent)
	waitForStartCount(t, &entered, 2)
	waitForRunnerLifecycle(t, d, "workspace-a", computer.RunnerLifecycleCrashed)
	if got := maxRunning.Load(); got != 1 {
		t.Fatalf("respawn stacked Run loops, max=%d", got)
	}
}

func TestWorkspaceRunnerRunExitStopsLiveChildAndCrashes(t *testing.T) {
	sleepPath, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip("sleep not on PATH")
	}
	d := New(Config{DaemonID: "daemon-test"}, nil)
	d.workspaces = map[string]*workspaceState{
		"workspace-a": newWorkspaceState("workspace-a", []string{"runtime-a"}),
	}
	var now atomic.Value
	base := time.Date(2026, 8, 13, 20, 0, 0, 0, time.UTC)
	now.Store(base)
	d.workspaceRunnerNow = func() time.Time { return now.Load().(time.Time) }
	host := os.Getpid()
	var child computer.BindingChild
	d.workspaceRunnerRun = func(*WorkspaceRunner, context.Context) {}
	d.workspaceRunnerSpawn = func(string) (computer.BindingChild, error) {
		started, err := computer.StartBindingCommand(sleepPath, []string{"30"})
		if err != nil {
			return nil, err
		}
		child = started
		return started, nil
	}

	d.reconcileWorkspaceRunners(context.Background())
	waitForRunnerLifecycle(t, d, "workspace-a", computer.RunnerLifecycleCrashed)
	if os.Getpid() != host {
		t.Fatal("host process died with Binding child")
	}
	if child == nil {
		t.Fatal("child was not spawned")
	}
	if class := child.Wait(); class != computer.RunnerExitGraceful && class != computer.RunnerExitCrash {
		t.Fatalf("live child after Run exit class = %s", class)
	}
}

func waitForStarted(t *testing.T, mu *sync.Mutex, started map[string]context.Context, workspaceID string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		_, ok := started[workspaceID]
		mu.Unlock()
		if ok {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("runner %s did not start", workspaceID)
}

func waitForRunnerLifecycle(t *testing.T, d *Daemon, workspaceID string, want computer.RunnerLifecycle) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		d.workspaceRunnerMu.RLock()
		rec := d.workspaceRunnerRecords[workspaceID]
		got := computer.RunnerLifecycle("")
		if rec != nil {
			got = rec.Lifecycle
		}
		d.workspaceRunnerMu.RUnlock()
		if got == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("runner %s lifecycle did not become %s", workspaceID, want)
}

func waitForStartCount(t *testing.T, starts *atomic.Int32, want int32) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if starts.Load() == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("starts = %d, want %d", starts.Load(), want)
}
