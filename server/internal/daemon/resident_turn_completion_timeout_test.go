package daemon

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/pkg/agent"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type residentTurnDeadlineBackend struct {
	mu        sync.Mutex
	done      chan error
	messages  chan agent.Message
	captures  chan agent.ResidentTurnCapture
	forceErr  error
	forceCall int
}

func (b *residentTurnDeadlineBackend) Execute(context.Context, string, agent.ExecOptions) (*agent.Session, error) {
	return nil, errors.New("deadline test backend does not execute prompt turns")
}

func (b *residentTurnDeadlineBackend) AcceptMessageBatch(context.Context, []agent.ResidentMessage) (agent.ResidentMessageAcceptance, error) {
	return agent.ResidentMessageAcceptance{Done: b.done, Messages: b.messages, Capture: b.captures}, nil
}

func (b *residentTurnDeadlineBackend) ForceKill() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.forceCall++
	return b.forceErr
}

func (b *residentTurnDeadlineBackend) forceKillCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.forceCall
}

type nonKillableResidentTurnDeadlineBackend struct {
	done chan error
}

func (b *nonKillableResidentTurnDeadlineBackend) Execute(context.Context, string, agent.ExecOptions) (*agent.Session, error) {
	return nil, errors.New("deadline test backend does not execute prompt turns")
}

func (b *nonKillableResidentTurnDeadlineBackend) AcceptMessageBatch(context.Context, []agent.ResidentMessage) (agent.ResidentMessageAcceptance, error) {
	return agent.ResidentMessageAcceptance{Done: b.done}, nil
}

func installResidentTurnDeadlineBackend(pool *agentRuntimePool, backend agent.Backend, cleanup func()) *agentRuntimeSlot {
	slot := &agentRuntimeSlot{backend: backend, close: cleanup, terminated: make(chan struct{})}
	pool.slots["agent-1\x00runtime-1"] = slot
	return slot
}

func awaitResidentTurnDeadlineCompletion(t *testing.T, completed <-chan error) *residentTurnCompletionTimeout {
	t.Helper()
	select {
	case err := <-completed:
		timeout, ok := asResidentTurnCompletionTimeout(err)
		if !ok {
			t.Fatalf("completion error = %v, want residentTurnCompletionTimeout", err)
		}
		return timeout
	case <-time.After(2 * time.Second):
		t.Fatal("accepted resident turn did not complete after its deadline")
		return nil
	}
}

func assertResidentTurnDeadlineReleased(t *testing.T, slot *agentRuntimeSlot) {
	t.Helper()
	slot.mu.Lock()
	defer slot.mu.Unlock()
	if slot.running || slot.messageInputDone != nil || slot.backend != nil {
		t.Fatalf("timed-out slot = running %v done %v backend %v; want fully released", slot.running, slot.messageInputDone != nil, slot.backend != nil)
	}
}

func TestResidentTurnsHaveNoAbsoluteCompletionDeadlineByDefault(t *testing.T) {
	pool := newAgentRuntimePool()
	if pool.residentTurnCompletionTimeout != 0 {
		t.Fatalf("default resident turn completion timeout = %v, want disabled", pool.residentTurnCompletionTimeout)
	}
	if deadline := pool.residentTurnDeadline(time.Now()); !deadline.IsZero() {
		t.Fatalf("default resident turn deadline = %v, want none", deadline)
	}

	backend := &residentTurnDeadlineBackend{done: make(chan error, 1), messages: make(chan agent.Message)}
	installResidentTurnDeadlineBackend(pool, backend, func() {})
	completed := make(chan error, 1)
	if err := pool.deliverIdleMessages(context.Background(), "agent-1", "runtime-1", nil, nil, nil, nil, func(err error, _ uint64, _ *agent.ResidentTurnCapture) {
		completed <- err
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-completed:
		t.Fatalf("healthy long-running resident turn completed without provider completion: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	backend.done <- nil
	close(backend.messages)
	select {
	case err := <-completed:
		if err != nil {
			t.Fatalf("provider-completed resident turn = %v, want nil", err)
		}
	case <-time.After(time.Second):
		t.Fatal("provider-completed resident turn did not release")
	}
}

func TestResidentTurnCompletionDeadlineBoundsEveryAcceptedSettlementStage(t *testing.T) {
	tests := []struct {
		name      string
		phase     string
		configure func(*residentTurnDeadlineBackend)
	}{
		{
			name: "Done never closes", phase: "provider completion",
			configure: func(b *residentTurnDeadlineBackend) {
				b.done = make(chan error, 1)
				b.messages = make(chan agent.Message, 1)
			},
		},
		{
			name: "Activity never closes", phase: "provider activity drain",
			configure: func(b *residentTurnDeadlineBackend) {
				b.done = make(chan error, 1)
				b.done <- nil
				b.messages = make(chan agent.Message)
			},
		},
		{
			name: "Capture never closes", phase: "provider capture drain",
			configure: func(b *residentTurnDeadlineBackend) {
				b.done = make(chan error, 1)
				b.done <- nil
				b.messages = make(chan agent.Message)
				close(b.messages)
				b.captures = make(chan agent.ResidentTurnCapture)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			pool := newAgentRuntimePool()
			pool.setResidentTurnCompletionTimeout(25 * time.Millisecond)
			backend := &residentTurnDeadlineBackend{}
			test.configure(backend)
			cleanupDone := make(chan struct{})
			var cleanupOnce sync.Once
			slot := installResidentTurnDeadlineBackend(pool, backend, func() { cleanupOnce.Do(func() { close(cleanupDone) }) })
			completed := make(chan error, 2)
			observedMessages := make(chan agent.Message, 1)
			if err := pool.deliverIdleMessages(context.Background(), "agent-1", "runtime-1", []protocol.AgentMessageProjection{{
				ID: "message-1", Target: "channel:one", Seq: 1, Content: "remember this",
			}}, nil, nil, func(message agent.Message) { observedMessages <- message }, func(err error, _ uint64, _ *agent.ResidentTurnCapture) { completed <- err }); err != nil {
				t.Fatalf("deliver accepted turn: %v", err)
			}

			timeout := awaitResidentTurnDeadlineCompletion(t, completed)
			if timeout.Phase != test.phase || !timeout.RestartSafe {
				t.Fatalf("timeout = %+v, want phase %q and restart-safe", timeout, test.phase)
			}
			wantForceKill := 0
			if test.phase == "provider completion" {
				wantForceKill = 1
			}
			if backend.forceKillCount() != wantForceKill {
				t.Fatalf("ForceKill calls = %d, want %d", backend.forceKillCount(), wantForceKill)
			}
			assertResidentTurnDeadlineReleased(t, slot)

			// A provider may report Done after the timeout. It no longer owns the
			// slot or a callback. Cleanup waits for this original reader boundary
			// instead of racing Close/Wait against it.
			if test.phase == "provider completion" {
				select {
				case <-cleanupDone:
					t.Fatal("backend cleanup raced provider Done settlement")
				default:
				}
			}
			select {
			case backend.done <- nil:
			default:
			}
			select {
			case <-cleanupDone:
			case <-time.After(time.Second):
				t.Fatal("timed-out backend cleanup did not run after Done settlement")
			}
			select {
			case err := <-completed:
				t.Fatalf("late provider completion invoked callback again: %v", err)
			case <-time.After(30 * time.Millisecond):
			}
			if test.phase == "provider completion" {
				backend.messages <- agent.Message{Type: agent.MessageThinking, Content: "late"}
				select {
				case message := <-observedMessages:
					t.Fatalf("late provider Activity crossed timeout fence: %+v", message)
				case <-time.After(30 * time.Millisecond):
				}
			}
		})
	}
}

func TestResidentTurnCompletionDeadlineDetachesWhenForceKillIsUnsafe(t *testing.T) {
	tests := []struct {
		name    string
		backend agent.Backend
	}{
		{
			name: "ForceKill fails",
			backend: &residentTurnDeadlineBackend{
				done: make(chan error), forceErr: errors.New("secret provider detail must stay redacted"),
			},
		},
		{
			name:    "ForceKill unsupported",
			backend: &nonKillableResidentTurnDeadlineBackend{done: make(chan error)},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			pool := newAgentRuntimePool()
			pool.setResidentTurnCompletionTimeout(20 * time.Millisecond)
			slot := installResidentTurnDeadlineBackend(pool, test.backend, nil)
			completed := make(chan error, 1)
			if err := pool.deliverIdleMessages(context.Background(), "agent-1", "runtime-1", nil, nil, nil, nil,
				func(err error, _ uint64, _ *agent.ResidentTurnCapture) { completed <- err }); err != nil {
				t.Fatal(err)
			}
			timeout := awaitResidentTurnDeadlineCompletion(t, completed)
			if timeout.RestartSafe {
				t.Fatal("unsafe ForceKill marked timeout restart-safe")
			}
			if strings.Contains(timeout.Error(), "secret provider detail") {
				t.Fatalf("timeout exposed raw provider error: %q", timeout.Error())
			}
			assertResidentTurnDeadlineReleased(t, slot)
			slot.mu.Lock()
			blocked := slot.replacementBlocked
			terminated := slot.terminated
			slot.mu.Unlock()
			if !blocked {
				t.Fatal("unsafe teardown did not retain replacement fence")
			}
			select {
			case <-terminated:
				t.Fatal("unsafe teardown falsely confirmed process termination")
			default:
			}
		})
	}
}

func TestResidentTurnTimeoutActivityReplacesWorkingWithoutRetiringRecoverableLaunch(t *testing.T) {
	d := New(Config{WorkspacesRoot: t.TempDir()}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	d.mu.Lock()
	d.runtimeIndex["runtime-1"] = Runtime{ID: "runtime-1", WorkspaceID: "workspace-1"}
	d.mu.Unlock()
	runner, _ := attachTestWorkspaceDaemon(t, d, "workspace-1", nil)
	startManagedLaunch(t, runner, "agent-1", "runtime-1")
	callback := currentTestAgentProcessCallback(t, runner, "agent-1")
	if err := runner.activity.SetManaged(callback.AgentInstanceID,
		protocol.AgentStatusPayload{AgentID: callback.AgentID, Status: protocol.AgentStatusActive},
		protocol.AgentSessionPayload{AgentID: callback.AgentID}); err != nil {
		t.Fatal(err)
	}
	if err := runner.activity.Observe(AgentObservation{
		AgentID: callback.AgentID, AgentInstanceID: callback.AgentInstanceID, Kind: AgentObservationRuntimeWorking,
		Data: AgentRuntimeStageObservationData{RuntimeID: "runtime-1"}, At: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	runner.observeMessageTurnCompletionForProcess(callback, "runtime-1", &residentTurnCompletionTimeout{
		Timeout: 15 * time.Minute, Phase: "provider completion", RestartSafe: true,
	})

	runner.activity.mu.Lock()
	state := runner.activity.states[agentActivityProducerKey{agentID: callback.AgentID, agentInstanceID: callback.AgentInstanceID}]
	var snapshot protocol.AgentActivitySnapshot
	if state != nil {
		snapshot = state.snapshot
	}
	runner.activity.mu.Unlock()
	if snapshot.ActivityKind != protocol.ActivityKindError || snapshot.DetailKind != "runtime_stalled" {
		t.Fatalf("timeout Activity = %+v, want error/runtime_stalled", snapshot)
	}
	if current, ok := runner.processes.Snapshot(callback.AgentID); !ok || current.ProcessInstanceID != callback.ProcessInstanceID {
		t.Fatalf("recoverable timeout retired launch: %+v found=%v", current, ok)
	}
}

func TestManagedTurnTimeoutRestartFlushesRetainedPendingEval(t *testing.T) {
	d := New(Config{WorkspacesRoot: t.TempDir()}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	d.mu.Lock()
	d.runtimeIndex["runtime-1"] = Runtime{ID: "runtime-1", WorkspaceID: "workspace-1"}
	d.mu.Unlock()

	var mu sync.Mutex
	var delivered []string
	coordinator, err := newTestMessageCoordinator(t, t.TempDir(), func(_ context.Context, messages []protocol.AgentMessageProjection) error {
		mu.Lock()
		defer mu.Unlock()
		for _, message := range messages {
			delivered = append(delivered, message.ID)
		}
		return nil
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	completeCoordinatorRecovery(t, coordinator)
	runner := registerTestInbox(t, d, InboxKey{WorkspaceID: "workspace-1", AgentID: "agent-1"}, "runtime-1", coordinator)
	markTestLaunchRunning(t, runner, "agent-1")
	old := currentTestAgentProcessCallback(t, runner, "agent-1")
	runner.processes.completeManagedStart(old)

	eval := testDelivery("eval-message", "channel:memory-eval", 11, "delivery-eval")
	if accepted, err := coordinator.Accept(context.Background(), eval); err != nil || !accepted {
		t.Fatalf("queue pending eval: accepted=%v err=%v", accepted, err)
	}
	starts := 0
	runner.ensureResidentRuntime = func(context.Context, string, string, *agent.PiRunIdentity) error {
		starts++
		return nil
	}

	runner.restartManagedAgentAfterTurnTimeout(old, "runtime-1")

	if starts != 1 {
		t.Fatalf("replacement provider starts = %d, want 1", starts)
	}
	mu.Lock()
	gotDelivered := append([]string(nil), delivered...)
	mu.Unlock()
	if len(gotDelivered) != 1 || gotDelivered[0] != "eval-message" {
		t.Fatalf("replacement flush delivered = %v, want retained eval exactly once", gotDelivered)
	}
	if pending := coordinator.PendingCount(); pending != 0 {
		t.Fatalf("Pending after replacement-ready flush = %d, want 0", pending)
	}
	current, ok := runner.processes.Snapshot("agent-1")
	if !ok || current.QueueState != protocol.AgentStartQueueRunning || current.ProcessInstanceID == "" || current.ProcessInstanceID == old.ProcessInstanceID {
		t.Fatalf("replacement managed process = %+v found=%v", current, ok)
	}
}

func TestResidentTurnCompletionDeadlineStartsAtNativeAcceptance(t *testing.T) {
	pool := newAgentRuntimePool()
	pool.setResidentTurnCompletionTimeout(20 * time.Millisecond)
	backend := &residentTurnDeadlineBackend{done: make(chan error, 1), messages: make(chan agent.Message)}
	backend.done <- nil
	slot := installResidentTurnDeadlineBackend(pool, backend, func() {})
	completed := make(chan error, 1)
	if err := pool.deliverIdleMessages(context.Background(), "agent-1", "runtime-1", nil, nil, func() {
		// Synchronous acceptance publication is part of the accepted-turn wall
		// clock. A delayed callback must not mint a fresh deadline afterward.
		time.Sleep(40 * time.Millisecond)
	}, nil, func(err error, _ uint64, _ *agent.ResidentTurnCapture) { completed <- err }); err != nil {
		t.Fatal(err)
	}
	timeout := awaitResidentTurnDeadlineCompletion(t, completed)
	if timeout.Phase != "provider activity drain" || !timeout.RestartSafe {
		t.Fatalf("timeout = %+v, want acceptance-origin Activity-gate timeout", timeout)
	}
	assertResidentTurnDeadlineReleased(t, slot)
}

func TestManagedTurnTimeoutRestartRearmsStopQuiescenceFence(t *testing.T) {
	d := New(Config{WorkspacesRoot: t.TempDir()}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	d.mu.Lock()
	d.runtimeIndex["runtime-1"] = Runtime{ID: "runtime-1", WorkspaceID: "workspace-1"}
	d.mu.Unlock()
	runner, _ := attachTestWorkspaceDaemon(t, d, "workspace-1", nil)
	startManagedLaunch(t, runner, "agent-1", "runtime-1")
	old := currentTestAgentProcessCallback(t, runner, "agent-1")
	runner.processes.completeManagedStart(old)

	ensureStarted := make(chan struct{})
	releaseEnsure := make(chan struct{})
	var once sync.Once
	runner.ensureResidentRuntime = func(context.Context, string, string, *agent.PiRunIdentity) error {
		once.Do(func() { close(ensureStarted) })
		<-releaseEnsure
		return nil
	}
	restartDone := make(chan struct{})
	go func() {
		runner.restartManagedAgentAfterTurnTimeout(old, "runtime-1")
		close(restartDone)
	}()
	select {
	case <-ensureStarted:
	case <-time.After(time.Second):
		t.Fatal("timeout replacement startup did not begin")
	}

	stopDone := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		stopDone <- runner.stopManagedAgent(ctx, protocol.AgentStopPayload{AgentID: "agent-1"}, nil, func(string, any) error { return nil })
	}()
	select {
	case err := <-stopDone:
		t.Fatalf("Stop crossed replacement startup fence before startup settled: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	close(releaseEnsure)
	select {
	case <-restartDone:
	case <-time.After(time.Second):
		t.Fatal("replacement startup did not settle after release")
	}
	select {
	case err := <-stopDone:
		if err != nil {
			t.Fatalf("Stop after replacement startup settled: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Stop did not resume after replacement startup settled")
	}
}

func TestResidentActivityDrainStopsAtDeadlineWithoutFinishScheduling(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	messages := make(chan agent.Message, 1)
	observed := make(chan agent.Message, 1)
	done := drainResidentActivity(ctx, messages, func(message agent.Message) { observed <- message }, time.Now().Add(20*time.Millisecond))
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Activity drain did not stop at its absolute deadline")
	}
	messages <- agent.Message{Type: agent.MessageThinking, Content: "after deadline"}
	select {
	case message := <-observed:
		t.Fatalf("Activity after deadline reached observer: %+v", message)
	case <-time.After(30 * time.Millisecond):
	}
}

func TestManagedTurnTimeoutRestartIsolatesPriorStartupTail(t *testing.T) {
	d := New(Config{WorkspacesRoot: t.TempDir()}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	d.mu.Lock()
	d.runtimeIndex["runtime-1"] = Runtime{ID: "runtime-1", WorkspaceID: "workspace-1"}
	d.mu.Unlock()
	runner, _ := attachTestWorkspaceDaemon(t, d, "workspace-1", nil)
	startManagedLaunch(t, runner, "agent-1", "runtime-1")
	old := currentTestAgentProcessCallback(t, runner, "agent-1")

	ensureStarted := make(chan struct{})
	releaseEnsure := make(chan struct{})
	runner.ensureResidentRuntime = func(context.Context, string, string, *agent.PiRunIdentity) error {
		close(ensureStarted)
		<-releaseEnsure
		return nil
	}
	restartDone := make(chan struct{})
	go func() {
		runner.restartManagedAgentAfterTurnTimeout(old, "runtime-1")
		close(restartDone)
	}()
	select {
	case <-ensureStarted:
	case <-time.After(time.Second):
		t.Fatal("replacement was blocked by prior startup tail")
	}
	current, ok := runner.processes.Snapshot("agent-1")
	if !ok || current.AgentInstanceID == old.AgentInstanceID {
		t.Fatalf("replacement Agent instance = %+v found=%v, want identity distinct from %s", current, ok, old.AgentInstanceID)
	}
	newCallback := agentProcessCallback{AgentID: current.AgentID, AgentInstanceID: current.AgentInstanceID}
	newStartupDone, found := runner.processes.managedStartupDone(newCallback)
	if !found {
		t.Fatal("replacement startup fence is missing")
	}
	runner.processes.completeManagedStart(old)
	select {
	case <-newStartupDone:
		t.Fatal("prior startup defer closed replacement startup fence")
	case <-time.After(30 * time.Millisecond):
	}
	close(releaseEnsure)
	select {
	case <-restartDone:
	case <-time.After(time.Second):
		t.Fatal("replacement did not finish")
	}
	select {
	case <-newStartupDone:
	case <-time.After(time.Second):
		t.Fatal("replacement startup fence did not settle")
	}
}

func TestResidentTurnDeadlineDoesNotWaitForBlockedActivityObserver(t *testing.T) {
	pool := newAgentRuntimePool()
	pool.setResidentTurnCompletionTimeout(25 * time.Millisecond)
	backend := &residentTurnDeadlineBackend{
		done:     make(chan error, 1),
		messages: make(chan agent.Message, 1),
	}
	backend.messages <- agent.Message{Type: agent.MessageThinking, Content: "before deadline"}
	slot := installResidentTurnDeadlineBackend(pool, backend, func() {})
	observerStarted := make(chan struct{})
	releaseObserver := make(chan struct{})
	completed := make(chan error, 1)
	if err := pool.deliverIdleMessages(context.Background(), "agent-1", "runtime-1", nil, nil, nil, func(agent.Message) {
		close(observerStarted)
		<-releaseObserver
	}, func(err error, _ uint64, _ *agent.ResidentTurnCapture) { completed <- err }); err != nil {
		t.Fatal(err)
	}
	select {
	case <-observerStarted:
	case <-time.After(time.Second):
		t.Fatal("Activity observer did not block")
	}
	timeout := awaitResidentTurnDeadlineCompletion(t, completed)
	if !timeout.RestartSafe {
		t.Fatalf("timeout = %+v, want restart-safe ForceKill", timeout)
	}
	assertResidentTurnDeadlineReleased(t, slot)
	close(releaseObserver)
	backend.done <- nil
}

type forceKillPiSettlementBackend struct {
	*piRPCRaceBackend
	mu        sync.Mutex
	killCalls int
}

func (b *forceKillPiSettlementBackend) ForceKill() error {
	b.mu.Lock()
	b.killCalls++
	b.mu.Unlock()
	return nil
}

func TestResidentTurnDeadlineDefersCleanupUntilPiSettlementQuiesces(t *testing.T) {
	pool := newAgentRuntimePool()
	pool.setResidentTurnCompletionTimeout(25 * time.Millisecond)
	base := newPiRPCRaceBackend()
	backend := &forceKillPiSettlementBackend{piRPCRaceBackend: base}
	identity := agent.PiRunIdentity{RunID: "run-1", RunAgentID: "run-agent-1"}
	if _, err := backend.BindRunIdentity(identity); err != nil {
		t.Fatal(err)
	}
	settleBlock := make(chan struct{})
	base.settleBlock = settleBlock
	base.turnDone <- nil
	cleanupDone := make(chan struct{})
	slot := installResidentTurnDeadlineBackend(pool, backend, func() { close(cleanupDone) })
	slot.piRunIdentity = &identity
	completed := make(chan error, 1)
	if err := pool.deliverIdleMessages(context.Background(), "agent-1", "runtime-1", nil, nil, nil, nil,
		func(err error, _ uint64, _ *agent.ResidentTurnCapture) { completed <- err }); err != nil {
		t.Fatal(err)
	}
	timeout := awaitResidentTurnDeadlineCompletion(t, completed)
	if timeout.Phase != "Pi turn settlement" || !timeout.RestartSafe {
		t.Fatalf("timeout = %+v, want restart-safe Pi settlement timeout", timeout)
	}
	select {
	case <-cleanupDone:
		t.Fatal("backend cleanup raced hung Pi settlement")
	default:
	}
	close(settleBlock)
	select {
	case <-cleanupDone:
	case <-time.After(time.Second):
		t.Fatal("backend cleanup did not run after Pi settlement quiesced")
	}
}

func TestResidentTurnDeadlineArmedBeforeBlockedAcceptanceReporting(t *testing.T) {
	pool := newAgentRuntimePool()
	pool.setResidentTurnCompletionTimeout(25 * time.Millisecond)
	backend := &residentTurnDeadlineBackend{done: make(chan error, 1), messages: make(chan agent.Message)}
	backend.done <- nil
	slot := installResidentTurnDeadlineBackend(pool, backend, func() {})
	acceptanceStarted := make(chan struct{})
	releaseAcceptance := make(chan struct{})
	completed := make(chan error, 1)
	deliveryDone := make(chan error, 1)
	go func() {
		deliveryDone <- pool.deliverIdleMessages(context.Background(), "agent-1", "runtime-1", nil, nil, func() {
			close(acceptanceStarted)
			<-releaseAcceptance
		}, nil, func(err error, _ uint64, _ *agent.ResidentTurnCapture) { completed <- err })
	}()
	select {
	case <-acceptanceStarted:
	case <-time.After(time.Second):
		t.Fatal("acceptance reporting did not block")
	}
	timeout := awaitResidentTurnDeadlineCompletion(t, completed)
	if !timeout.RestartSafe {
		t.Fatalf("timeout = %+v, want restart-safe", timeout)
	}
	assertResidentTurnDeadlineReleased(t, slot)
	select {
	case err := <-deliveryDone:
		t.Fatalf("delivery returned before acceptance reporting was released: %v", err)
	default:
	}
	close(releaseAcceptance)
	select {
	case err := <-deliveryDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("delivery did not return after acceptance reporting release")
	}
}

func TestResidentTurnDeadlineBoundsInvalidatedBackendCleanup(t *testing.T) {
	pool := newAgentRuntimePool()
	pool.setResidentTurnCompletionTimeout(30 * time.Millisecond)
	backend := &residentTurnDeadlineBackend{done: make(chan error, 1)}
	cleanupStarted := make(chan struct{})
	releaseCleanup := make(chan struct{})
	slot := installResidentTurnDeadlineBackend(pool, backend, func() {
		close(cleanupStarted)
		<-releaseCleanup
	})
	completed := make(chan error, 1)
	if err := pool.deliverIdleMessages(context.Background(), "agent-1", "runtime-1", nil, nil, nil, nil,
		func(err error, _ uint64, _ *agent.ResidentTurnCapture) { completed <- err }); err != nil {
		t.Fatal(err)
	}
	if err := pool.beginResidentTermination("agent-1", "runtime-1"); err != nil {
		t.Fatal(err)
	}
	backend.done <- nil
	select {
	case <-cleanupStarted:
	case <-time.After(time.Second):
		t.Fatal("invalidated backend cleanup did not start")
	}
	timeout := awaitResidentTurnDeadlineCompletion(t, completed)
	if timeout.Phase != "provider cleanup" || timeout.RestartSafe {
		t.Fatalf("timeout = %+v, want unsafe provider cleanup timeout", timeout)
	}
	assertResidentTurnDeadlineReleased(t, slot)
	slot.mu.Lock()
	blocked := slot.replacementBlocked
	terminated := slot.terminated
	slot.mu.Unlock()
	if !blocked {
		t.Fatal("hung invalidated cleanup did not retain replacement fence")
	}
	close(releaseCleanup)
	select {
	case <-terminated:
	case <-time.After(time.Second):
		t.Fatal("termination was not confirmed after cleanup returned")
	}
}
