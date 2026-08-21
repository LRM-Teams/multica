package daemon

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/pkg/agent"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// stalledRecoveryPool returns a pool holding one idle slot with a live test
// backend, plus the backend itself, so a test can age the slot's activity
// stamp and drive recoverStalledSlotForQueuedMessage directly.
func stalledRecoveryPool(t *testing.T, window time.Duration) (*canonicalAgentRuntimePool, *canonicalRuntimeTestBackend, *canonicalAgentRuntimeSlot) {
	t.Helper()
	pool := newCanonicalAgentRuntimePool()
	pool.setResidentStallWatchdog(window)
	probe := &canonicalRuntimeFactoryProbe{}
	identity := canonicalRuntimeIdentityForTest(t, "model-a", map[string]string{
		"MULTICA_SERVER_URL":   "https://multica.example",
		"MULTICA_WORKSPACE_ID": "workspace-a",
		"MULTICA_AGENT_ID":     "agent-a",
		"MULTICA_TASK_ID":      "turn-a",
	})
	lease, err := pool.acquire(canonicalAgentRuntimeAcquireRequest{
		Identity: identity,
		Factory:  probe.factory,
	})
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	lease.release(true)

	pool.mu.Lock()
	slot := pool.slots[identity.slotKey()]
	pool.mu.Unlock()
	if slot == nil {
		t.Fatal("slot was not registered")
	}
	if len(probe.backends) != 1 {
		t.Fatalf("factory created %d backends, want 1", len(probe.backends))
	}
	return pool, probe.backends[0], slot
}

func ageSlotActivity(slot *canonicalAgentRuntimeSlot, age time.Duration) {
	slot.mu.Lock()
	slot.lastRuntimeActivityAt = time.Now().Add(-age)
	slot.mu.Unlock()
}

func TestRecoverStalledSlotForQueuedMessageKillsSilentButLiveRuntime(t *testing.T) {
	pool, backend, slot := stalledRecoveryPool(t, 15*time.Minute)
	ageSlotActivity(slot, 16*time.Minute)

	recovered, err := pool.recoverStalledSlotForQueuedMessage("agent-a", "runtime-a")
	if err != nil {
		t.Fatalf("recover: %v", err)
	}
	if !recovered {
		t.Fatal("recovered = false, want true for a runtime silent past the stall window")
	}
	// The slot is idle, so forceInvalidateSession closes the backend outright
	// rather than force-killing an in-flight turn.
	slot.mu.Lock()
	backendPresent := slot.backend != nil
	slot.mu.Unlock()
	if backendPresent {
		t.Fatal("stalled recovery left the resident backend attached")
	}
	if backend.forceKillCount() != 0 {
		t.Fatalf("idle stalled recovery force-killed %d times, want 0", backend.forceKillCount())
	}
}

func TestRecoverStalledSlotForQueuedMessageSparesRecentActivity(t *testing.T) {
	pool, _, slot := stalledRecoveryPool(t, 15*time.Minute)
	ageSlotActivity(slot, time.Minute)

	recovered, err := pool.recoverStalledSlotForQueuedMessage("agent-a", "runtime-a")
	if err != nil {
		t.Fatalf("recover: %v", err)
	}
	if recovered {
		t.Fatal("recovered = true for a runtime that produced activity one minute ago")
	}
}

// A freshly created process must never be killed by the first deferred
// delivery: the activity stamp is set at spawn, not left zero-valued.
func TestRecoverStalledSlotForQueuedMessageSparesFreshProcess(t *testing.T) {
	pool, _, _ := stalledRecoveryPool(t, 15*time.Minute)

	recovered, err := pool.recoverStalledSlotForQueuedMessage("agent-a", "runtime-a")
	if err != nil {
		t.Fatalf("recover: %v", err)
	}
	if recovered {
		t.Fatal("recovered = true for a process that just started")
	}
}

func TestRecoverStalledSlotForQueuedMessageSparesOutstandingToolCall(t *testing.T) {
	pool, _, slot := stalledRecoveryPool(t, 15*time.Minute)
	pool.observeResidentRuntimeMessage(slot, agent.Message{Type: agent.MessageToolUse, CallID: "call-1"})
	ageSlotActivity(slot, 16*time.Minute)

	recovered, err := pool.recoverStalledSlotForQueuedMessage("agent-a", "runtime-a")
	if err != nil {
		t.Fatalf("recover: %v", err)
	}
	if recovered {
		t.Fatal("recovered = true while a tool call is still outstanding")
	}
}

func TestRecoverStalledSlotForQueuedMessageResumesAfterToolResult(t *testing.T) {
	pool, _, slot := stalledRecoveryPool(t, 15*time.Minute)
	pool.observeResidentRuntimeMessage(slot, agent.Message{Type: agent.MessageToolUse, CallID: "call-1"})
	pool.observeResidentRuntimeMessage(slot, agent.Message{Type: agent.MessageToolResult, CallID: "call-1"})
	ageSlotActivity(slot, 16*time.Minute)

	recovered, err := pool.recoverStalledSlotForQueuedMessage("agent-a", "runtime-a")
	if err != nil {
		t.Fatalf("recover: %v", err)
	}
	if !recovered {
		t.Fatal("recovered = false after the outstanding tool call completed")
	}
}

func TestRecoverStalledSlotForQueuedMessageSparesCompactingRuntime(t *testing.T) {
	pool, _, slot := stalledRecoveryPool(t, 15*time.Minute)
	pool.observeResidentRuntimeMessage(slot, agent.Message{Type: agent.MessageCompactionStarted})
	ageSlotActivity(slot, 16*time.Minute)

	recovered, err := pool.recoverStalledSlotForQueuedMessage("agent-a", "runtime-a")
	if err != nil {
		t.Fatalf("recover: %v", err)
	}
	if recovered {
		t.Fatal("recovered = true while the runtime is compacting")
	}
}

func TestRecoverStalledSlotForQueuedMessageIsDisabledWithoutWindow(t *testing.T) {
	pool, _, slot := stalledRecoveryPool(t, 0)
	ageSlotActivity(slot, 48*time.Hour)

	recovered, err := pool.recoverStalledSlotForQueuedMessage("agent-a", "runtime-a")
	if err != nil {
		t.Fatalf("recover: %v", err)
	}
	if recovered {
		t.Fatal("recovered = true with the stall window disabled")
	}
}

// The server redelivers every ~20s, so the deferred branch re-evaluates
// continuously. Without a re-fire guard that cadence would fire one kill per
// redelivery while the first teardown is still in flight.
func TestRecoverStalledSlotForQueuedMessageDoesNotRefireWhileRecovering(t *testing.T) {
	pool := newCanonicalAgentRuntimePool()
	pool.setResidentStallWatchdog(15 * time.Minute)
	probe := &canonicalRuntimeFactoryProbe{}
	identity := canonicalRuntimeIdentityForTest(t, "model-a", map[string]string{
		"MULTICA_SERVER_URL":   "https://multica.example",
		"MULTICA_WORKSPACE_ID": "workspace-a",
		"MULTICA_AGENT_ID":     "agent-a",
		"MULTICA_TASK_ID":      "turn-a",
	})
	lease, err := pool.acquire(canonicalAgentRuntimeAcquireRequest{
		Identity: identity,
		Factory:  probe.factory,
	})
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer lease.release(true)

	// Keep the lease so the slot stays running: forceInvalidateSession then
	// takes the force-kill path and admission is held until the turn resolves.
	pool.mu.Lock()
	slot := pool.slots[identity.slotKey()]
	pool.mu.Unlock()
	ageSlotActivity(slot, 16*time.Minute)

	recovered, err := pool.recoverStalledSlotForQueuedMessage("agent-a", "runtime-a")
	if err != nil {
		t.Fatalf("first recover: %v", err)
	}
	if !recovered {
		t.Fatal("first recovered = false, want true")
	}
	backend := probe.backends[0]
	if backend.forceKillCount() != 1 {
		t.Fatalf("force kills after first recovery = %d, want 1", backend.forceKillCount())
	}

	for i := 0; i < 3; i++ {
		again, err := pool.recoverStalledSlotForQueuedMessage("agent-a", "runtime-a")
		if err != nil {
			t.Fatalf("repeat recover %d: %v", i, err)
		}
		if again {
			t.Fatalf("repeat recover %d = true, want false while recovery is in flight", i)
		}
	}
	if backend.forceKillCount() != 1 {
		t.Fatalf("force kills after repeats = %d, want 1", backend.forceKillCount())
	}
}

// wedgedResidentBackend accepts a Message batch but never completes the turn
// until its process is force-killed — the pi-shaped failure this PR recovers.
// It proves the existing self-heal chain (ForceKill → provider exits →
// completion receipt resolves → slot released) actually closes, so no second
// timeout mechanism is needed around finishResidentMessageInput.
type wedgedResidentBackend struct {
	mu   sync.Mutex
	done chan error
}

func newWedgedResidentBackend() *wedgedResidentBackend {
	return &wedgedResidentBackend{done: make(chan error, 1)}
}

func (b *wedgedResidentBackend) Execute(context.Context, string, agent.ExecOptions) (*agent.Session, error) {
	return nil, errors.New("wedged resident backend does not run prompt turns")
}

func (b *wedgedResidentBackend) AcceptMessageBatch(_ context.Context, _ []agent.ResidentMessage) (agent.ResidentMessageAcceptance, error) {
	messages := make(chan agent.Message)
	close(messages)
	return agent.ResidentMessageAcceptance{Messages: messages, Done: b.done}, nil
}

func (b *wedgedResidentBackend) ForceKill() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	select {
	case b.done <- errors.New("resident process force killed"):
	default:
	}
	return nil
}

func (b *wedgedResidentBackend) Close() {}

func TestForceInvalidateReleasesWedgedRunningSlot(t *testing.T) {
	pool := newCanonicalAgentRuntimePool()
	backend := newWedgedResidentBackend()
	identity := canonicalRuntimeIdentityForTest(t, "model-a", map[string]string{
		"MULTICA_SERVER_URL":   "https://multica.example",
		"MULTICA_WORKSPACE_ID": "workspace-a",
		"MULTICA_AGENT_ID":     "agent-a",
		"MULTICA_TASK_ID":      "turn-a",
	})
	lease, err := pool.acquire(canonicalAgentRuntimeAcquireRequest{
		Identity: identity,
		Factory: func(agent.Config) (agent.Backend, func(), error) {
			return backend, func() {}, nil
		},
	})
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	lease.release(true)

	if err := pool.deliverIdleMessages(context.Background(), "agent-a", "runtime-a",
		[]protocol.AgentMessageProjection{{ID: "m1", Target: "channel:c", Seq: 1, Content: "hi"}},
		nil, nil, nil, nil); err != nil {
		t.Fatalf("deliverIdleMessages: %v", err)
	}

	pool.mu.Lock()
	slot := pool.slots[identity.slotKey()]
	pool.mu.Unlock()
	slot.mu.Lock()
	running := slot.running
	slot.mu.Unlock()
	if !running {
		t.Fatal("slot is not running after an accepted batch; the wedge is not reproduced")
	}

	if err := pool.forceInvalidateSession("agent-a", "runtime-a"); err != nil {
		t.Fatalf("forceInvalidateSession: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		slot.mu.Lock()
		released := !slot.running && slot.backend == nil
		slot.mu.Unlock()
		if released {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("wedged slot never released admission after ForceKill")
}
