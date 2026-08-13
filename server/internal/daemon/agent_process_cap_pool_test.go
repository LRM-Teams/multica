package daemon

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/pkg/agent"
)

func identityForAgent(t *testing.T, agentID, runtimeID string) canonicalAgentRuntimeIdentity {
	t.Helper()
	identity, err := newCanonicalAgentRuntimeIdentity(canonicalAgentRuntimeIdentityParams{
		AgentID:     agentID,
		RuntimeID:   runtimeID,
		Provider:    "pi",
		Executable:  "/usr/local/bin/pi",
		WorkDir:     "/tmp/" + agentID,
		Environment: map[string]string{},
	})
	if err != nil {
		t.Fatalf("identity: %v", err)
	}
	return identity
}

func acquireResident(t *testing.T, pool *canonicalAgentRuntimePool, probe *canonicalRuntimeFactoryProbe, agentID, runtimeID string, ctx context.Context) *canonicalAgentRuntimeLease {
	t.Helper()
	if ctx == nil {
		ctx = context.Background()
	}
	lease, err := pool.acquire(canonicalAgentRuntimeAcquireRequest{
		Identity: identityForAgent(t, agentID, runtimeID),
		Factory:  probe.factory,
		Context:  ctx,
		Now:      time.Now(),
	})
	if err != nil {
		t.Fatalf("acquire %s/%s: %v", agentID, runtimeID, err)
	}
	return lease
}

// 1. cap=N + N+k agents → steady-state live ≤ N
func TestAgentProcessCapSteadyStateAtMostN(t *testing.T) {
	pool := newCanonicalAgentRuntimePool()
	pool.setMaxAgentProcesses(2)
	probe := &canonicalRuntimeFactoryProbe{}

	// Fill cap with idle residents.
	l1 := acquireResident(t, pool, probe, "agent-1", "rt-a", nil)
	l1.release(true)
	l2 := acquireResident(t, pool, probe, "agent-2", "rt-a", nil)
	l2.release(true)

	// Third agent should evict oldest idle and succeed.
	l3 := acquireResident(t, pool, probe, "agent-3", "rt-a", nil)
	l3.release(true)

	if got := pool.countLiveAgentsForTest(); got > 2 {
		t.Fatalf("live agents = %d, want ≤ 2", got)
	}
	// Exactly 2: agent-1 evicted, agent-2+3 remain (or agent-2 evicted if order differs by idleSince).
	if got := pool.countLiveAgentsForTest(); got != 2 {
		t.Fatalf("live agents = %d, want 2 after third acquire", got)
	}
}

// 2. Same agent rebinds runtime_id → still counts as 1; old process closed.
func TestAgentProcessCapRebindSameAgentCountsOnce(t *testing.T) {
	pool := newCanonicalAgentRuntimePool()
	pool.setMaxAgentProcesses(4)
	probe := &canonicalRuntimeFactoryProbe{}

	l1 := acquireResident(t, pool, probe, "agent-x", "rt-old", nil)
	l1.release(true)
	if got := pool.countLiveAgentsForTest(); got != 1 {
		t.Fatalf("after first: live=%d", got)
	}
	created1, closed1 := probe.counts()

	l2 := acquireResident(t, pool, probe, "agent-x", "rt-new", nil)
	l2.release(true)

	if got := pool.countLiveAgentsForTest(); got != 1 {
		t.Fatalf("after rebind: live=%d want 1", got)
	}
	// Old slot should be gone.
	if pool.slotCount() != 1 {
		t.Fatalf("slotCount=%d want 1 after rebind", pool.slotCount())
	}
	created2, closed2 := probe.counts()
	if created2 != created1+1 {
		t.Fatalf("created %d→%d, want +1 for new runtime", created1, created2)
	}
	if closed2 < closed1+1 {
		t.Fatalf("closed %d→%d, want old process closed", closed1, closed2)
	}
}

// 3. Pool full + idle present → kick oldest idle; never kick running.
func TestAgentProcessCapEvictsIdleNotRunning(t *testing.T) {
	pool := newCanonicalAgentRuntimePool()
	pool.setMaxAgentProcesses(2)
	probe := &canonicalRuntimeFactoryProbe{}

	// agent-idle becomes idle first.
	li := acquireResident(t, pool, probe, "agent-idle", "rt", nil)
	li.releaseAt(true, time.Unix(1, 0))

	// agent-run stays running.
	lr := acquireResident(t, pool, probe, "agent-run", "rt", nil)

	// New agent needs a slot → must evict idle, not running.
	ln := acquireResident(t, pool, probe, "agent-new", "rt", nil)

	// agent-run still running with backend.
	if !lr.slot.running {
		t.Fatal("running agent was kicked")
	}
	if lr.slot.backend == nil {
		t.Fatal("running agent backend closed")
	}
	// agent-idle should be gone.
	if pool.agentHasLiveForTest("agent-idle") {
		t.Fatal("idle agent should have been evicted")
	}
	if !pool.agentHasLiveForTest("agent-new") {
		t.Fatal("new agent should be live")
	}
	ln.release(true)
	lr.release(true)
}

// 4. All running → new acquire waits / does not steal.
func TestAgentProcessCapWaitsWhenAllRunning(t *testing.T) {
	pool := newCanonicalAgentRuntimePool()
	pool.setMaxAgentProcesses(1)
	probe := &canonicalRuntimeFactoryProbe{}

	l1 := acquireResident(t, pool, probe, "agent-busy", "rt", nil)

	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err := pool.acquire(canonicalAgentRuntimeAcquireRequest{
		Identity: identityForAgent(t, "agent-waiter", "rt"),
		Factory:  probe.factory,
		Context:  ctx,
	})
	if err == nil {
		t.Fatal("expected capacity wait to fail/cancel while sole slot running")
	}
	if time.Since(started) < 50*time.Millisecond {
		t.Fatalf("returned too fast (%v); expected to wait on capacity", time.Since(started))
	}

	// Free capacity → waiter can proceed.
	l1.release(false) // unhealthy closes backend, frees cap
	l2 := acquireResident(t, pool, probe, "agent-waiter", "rt", context.Background())
	l2.release(true)
}

// 5. Env absolute override — covered in agent_process_cap_test; also pool uses set value.
func TestAgentProcessCapEnvAbsoluteAppliedViaSetMax(t *testing.T) {
	n := 3
	in := AgentProcessCapInputs{NumCPU: 64, Absolute: &n}
	if got := resolveMaxAgentProcesses(in); got != 3 {
		t.Fatalf("got %d", got)
	}
	pool := newCanonicalAgentRuntimePool()
	got := resolveMaxAgentProcesses(in)
	pool.setMaxAgentProcesses(got)
	if pool.maxAgentProcesses != 3 {
		t.Fatalf("max=%d", pool.maxAgentProcesses)
	}
}

// 6. NumCPU injection → clamp FLOOR/CEIL (formula unit tests already; double-check floor).
func TestAgentProcessCapFloorCeilWithInjectedCPU(t *testing.T) {
	if got := resolveMaxAgentProcesses(AgentProcessCapInputs{NumCPU: 1, PerCPU: 1, Floor: 4, Ceil: 64}); got != 4 {
		t.Fatalf("floor: got %d", got)
	}
	if got := resolveMaxAgentProcesses(AgentProcessCapInputs{NumCPU: 200, PerCPU: 1, Floor: 4, Ceil: 64}); got != 64 {
		t.Fatalf("ceil: got %d", got)
	}
}

// Concurrent fill under cap never exceeds N live agents for long.
func TestAgentProcessCapConcurrentDoesNotExceed(t *testing.T) {
	pool := newCanonicalAgentRuntimePool()
	pool.setMaxAgentProcesses(3)
	probe := &canonicalRuntimeFactoryProbe{}

	var wg sync.WaitGroup
	var maxLive atomic.Int64
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := "agent-" + string(rune('a'+i%12))
			// unique agent ids
			id = "agent-" + itoa(i)
			lease, err := pool.acquire(canonicalAgentRuntimeAcquireRequest{
				Identity: identityForAgent(t, id, "rt"),
				Factory:  probe.factory,
				Context:  context.Background(),
			})
			if err != nil {
				return
			}
			live := int64(pool.countLiveAgentsForTest())
			for {
				cur := maxLive.Load()
				if live <= cur || maxLive.CompareAndSwap(cur, live) {
					break
				}
			}
			lease.release(true)
		}(i)
	}
	wg.Wait()
	if maxLive.Load() > 3 {
		// pending can briefly make live+pending == max while live <= max.
		// Live alone should never exceed max under our reserve semantics.
		t.Fatalf("observed live peak %d > cap 3", maxLive.Load())
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [16]byte
	n := len(b)
	for i > 0 {
		n--
		b[n] = byte('0' + i%10)
		i /= 10
	}
	return string(b[n:])
}

func (p *canonicalAgentRuntimePool) countLiveAgentsForTest() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.countLiveAgentsLocked()
}

func (p *canonicalAgentRuntimePool) agentHasLiveForTest(agentID string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.agentHasLiveBackendLocked(agentID)
}

// silence unused import if agent not referenced
var _ agent.Backend

// Explicit invalidate then failed create must free capacity so another
// agent can acquire (Alice #1923 nit). Live-process reuse must not kill
// just because the next factory would fail.
func TestAgentProcessCapInvalidateCreateFailFreesCapacity(t *testing.T) {
	pool := newCanonicalAgentRuntimePool()
	pool.setMaxAgentProcesses(1)
	probe := &canonicalRuntimeFactoryProbe{}

	l1 := acquireResident(t, pool, probe, "agent-a", "rt", nil)
	l1.release(true)
	if got := pool.countLiveAgentsForTest(); got != 1 {
		t.Fatalf("live=%d want 1", got)
	}
	if err := pool.invalidateSession("agent-a", "rt"); err != nil {
		t.Fatalf("invalidateSession: %v", err)
	}

	failFactory := func(_ agent.Config) (agent.Backend, func(), error) {
		return nil, nil, errors.New("boom create")
	}
	id := identityForAgent(t, "agent-a", "rt")
	_, err := pool.acquire(canonicalAgentRuntimeAcquireRequest{
		Identity: id,
		Factory:  failFactory,
		Context:  context.Background(),
	})
	if err == nil {
		t.Fatal("expected create failure after invalidate")
	}
	if got := pool.countLiveAgentsForTest(); got != 0 {
		t.Fatalf("after invalidate-fail live=%d want 0 (capacity must free)", got)
	}

	// Cap free: a different agent can acquire immediately.
	l2 := acquireResident(t, pool, probe, "agent-b", "rt", nil)
	l2.release(true)
	if got := pool.countLiveAgentsForTest(); got != 1 {
		t.Fatalf("agent-b live=%d want 1", got)
	}
}
