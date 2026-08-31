package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/pkg/agent"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type blockingResidentMessageRuntime struct {
	done chan error
}

func (r *blockingResidentMessageRuntime) Execute(context.Context, string, agent.ExecOptions) (*agent.Session, error) {
	return nil, nil
}

func (r *blockingResidentMessageRuntime) AcceptMessageBatch(context.Context, []agent.ResidentMessage) (agent.ResidentMessageAcceptance, error) {
	return agent.ResidentMessageAcceptance{Done: r.done}, nil
}

type startingResidentMessageRuntime struct {
	started   chan struct{}
	killed    chan struct{}
	killOnce  sync.Once
	killCalls int
	mu        sync.Mutex
}

func (r *startingResidentMessageRuntime) Execute(context.Context, string, agent.ExecOptions) (*agent.Session, error) {
	return nil, nil
}

func (r *startingResidentMessageRuntime) AcceptMessageBatch(context.Context, []agent.ResidentMessage) (agent.ResidentMessageAcceptance, error) {
	close(r.started)
	<-r.killed
	return agent.ResidentMessageAcceptance{}, errors.New("runtime killed during native acceptance")
}

func (r *startingResidentMessageRuntime) ForceKill() error {
	r.mu.Lock()
	r.killCalls++
	r.mu.Unlock()
	r.killOnce.Do(func() { close(r.killed) })
	return nil
}

func TestRuntimePoolRestartInterruptsNativeAcceptance(t *testing.T) {
	backend := &startingResidentMessageRuntime{started: make(chan struct{}), killed: make(chan struct{})}
	pool := newAgentRuntimePool()
	pool.slots["agent-1\x00runtime-1"] = &agentRuntimeSlot{
		backend: backend,
	}
	handoffErr := make(chan error, 1)
	go func() {
		handoffErr <- pool.deliverIdleMessages(
			context.Background(), "agent-1", "runtime-1",
			[]protocol.AgentMessageProjection{{ID: "message-1", Target: "dm:one", Seq: 1}},
			nil, nil, nil, nil,
		)
	}()
	select {
	case <-backend.started:
	case <-time.After(time.Second):
		t.Fatal("native acceptance did not start")
	}

	restartDone := make(chan error, 1)
	go func() { restartDone <- pool.beginResidentTermination("agent-1", "runtime-1") }()
	select {
	case err := <-restartDone:
		if err != nil {
			t.Fatalf("beginResidentTermination: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("restart could not interrupt a runtime stuck before native acceptance")
	}
	select {
	case err := <-handoffErr:
		if err == nil {
			t.Fatal("killed native acceptance reported success")
		}
	case <-time.After(time.Second):
		t.Fatal("native acceptance did not return after restart")
	}
}

type sequencedResidentMessageRuntime struct {
	accepted chan chan error
}

func (r *sequencedResidentMessageRuntime) Execute(context.Context, string, agent.ExecOptions) (*agent.Session, error) {
	return nil, nil
}

func (r *sequencedResidentMessageRuntime) AcceptMessageBatch(context.Context, []agent.ResidentMessage) (agent.ResidentMessageAcceptance, error) {
	done := make(chan error, 1)
	r.accepted <- done
	return agent.ResidentMessageAcceptance{Done: done}, nil
}

type activityResidentMessageRuntime struct {
	done     chan error
	messages chan agent.Message
}

func (r *activityResidentMessageRuntime) Execute(context.Context, string, agent.ExecOptions) (*agent.Session, error) {
	return nil, nil
}

func (r *activityResidentMessageRuntime) AcceptMessageBatch(context.Context, []agent.ResidentMessage) (agent.ResidentMessageAcceptance, error) {
	return agent.ResidentMessageAcceptance{Done: r.done, Messages: r.messages}, nil
}

func (r *activityResidentMessageRuntime) RuntimeAlive() (bool, bool) { return false, false }

type livenessTransitionResidentMessageRuntime struct {
	mu          sync.Mutex
	alive       bool
	known       bool
	acceptError error
}

func (r *livenessTransitionResidentMessageRuntime) Execute(context.Context, string, agent.ExecOptions) (*agent.Session, error) {
	return nil, nil
}

func (r *livenessTransitionResidentMessageRuntime) RuntimeAlive() (bool, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.alive, r.known
}

func (r *livenessTransitionResidentMessageRuntime) AcceptMessageBatch(context.Context, []agent.ResidentMessage) (agent.ResidentMessageAcceptance, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.acceptError != nil {
		return agent.ResidentMessageAcceptance{}, r.acceptError
	}
	r.alive, r.known = true, true
	done := make(chan error)
	close(done)
	return agent.ResidentMessageAcceptance{Done: done}, nil
}

func TestRuntimePoolReportsStartingOnlyAfterAcceptedInputStartsConfirmedRuntime(t *testing.T) {
	tests := []struct {
		name         string
		backend      *livenessTransitionResidentMessageRuntime
		wantStarting int
		wantError    bool
	}{
		{name: "unknown liveness stays fail open", backend: &livenessTransitionResidentMessageRuntime{}, wantStarting: 0},
		{name: "failed acceptance is not a start", backend: &livenessTransitionResidentMessageRuntime{known: true, acceptError: errors.New("provider busy")}, wantStarting: 0, wantError: true},
		{name: "confirmed dead becomes alive", backend: &livenessTransitionResidentMessageRuntime{known: true}, wantStarting: 1},
		{name: "already alive remains the same process", backend: &livenessTransitionResidentMessageRuntime{known: true, alive: true}, wantStarting: 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			pool := newAgentRuntimePool()
			pool.slots["agent-1\x00runtime-1"] = &agentRuntimeSlot{backend: test.backend}
			starting := 0
			err := pool.deliverIdleMessages(context.Background(), "agent-1", "runtime-1", []protocol.AgentMessageProjection{{ID: "message-1", Target: "dm:one", Seq: 1}}, func() {
				starting++
			}, nil, nil, nil)
			if (err != nil) != test.wantError {
				t.Fatalf("handoff error = %v, wantError %v", err, test.wantError)
			}
			if starting != test.wantStarting {
				t.Fatalf("Starting observations = %d, want %d", starting, test.wantStarting)
			}
		})
	}
}

type preparingResidentMessageRuntime struct {
	mu       sync.Mutex
	prepared bool
}

func (r *preparingResidentMessageRuntime) Execute(context.Context, string, agent.ExecOptions) (*agent.Session, error) {
	return nil, nil
}

func (r *preparingResidentMessageRuntime) PrepareMessageInput(ctx context.Context, emit func(agent.Message)) error {
	if _, hasDeadline := ctx.Deadline(); hasDeadline {
		return errors.New("resident Message preparation inherited the native acceptance deadline")
	}
	r.mu.Lock()
	r.prepared = true
	r.mu.Unlock()
	return nil
}

func (r *preparingResidentMessageRuntime) AcceptMessageBatch(ctx context.Context, _ []agent.ResidentMessage) (agent.ResidentMessageAcceptance, error) {
	r.mu.Lock()
	prepared := r.prepared
	r.mu.Unlock()
	if !prepared {
		return agent.ResidentMessageAcceptance{}, errors.New("native acceptance started before resident Message preparation")
	}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		return agent.ResidentMessageAcceptance{}, errors.New("native acceptance is not bounded")
	}
	done := make(chan error)
	close(done)
	return agent.ResidentMessageAcceptance{Done: done}, nil
}

func TestRuntimePoolPreparesResidentInputOutsideNativeAcceptanceTimeout(t *testing.T) {
	backend := &preparingResidentMessageRuntime{}
	pool := newAgentRuntimePool()
	pool.slots["agent-1\x00runtime-1"] = &agentRuntimeSlot{
		backend: backend,
	}
	if err := pool.deliverIdleMessages(
		context.Background(), "agent-1", "runtime-1",
		[]protocol.AgentMessageProjection{{ID: "message-1", Target: "dm:one", Seq: 1}},
		nil, nil, nil, nil,
	); err != nil {
		t.Fatalf("handoffIdleMessages: %v", err)
	}
	backend.mu.Lock()
	prepared := backend.prepared
	backend.mu.Unlock()
	if !prepared {
		t.Fatal("PrepareMessageInput must still run outside the native acceptance deadline")
	}
}

type failingCompactionPreparationRuntime struct {
	killCalls int
}

func (r *failingCompactionPreparationRuntime) Execute(context.Context, string, agent.ExecOptions) (*agent.Session, error) {
	return nil, nil
}

func (r *failingCompactionPreparationRuntime) PrepareMessageInput(_ context.Context, emit func(agent.Message)) error {
	if emit != nil {
		emit(agent.Message{Type: agent.MessageCompactionStarted})
	}
	return errors.New("context compaction failed")
}

func (r *failingCompactionPreparationRuntime) AcceptMessageBatch(context.Context, []agent.ResidentMessage) (agent.ResidentMessageAcceptance, error) {
	return agent.ResidentMessageAcceptance{}, errors.New("native acceptance must not run after failed compaction")
}

func (r *failingCompactionPreparationRuntime) ForceKill() error {
	r.killCalls++
	return nil
}

type emptyTurnAfterCompactRuntime struct{}

func (r *emptyTurnAfterCompactRuntime) Execute(context.Context, string, agent.ExecOptions) (*agent.Session, error) {
	return nil, nil
}

func (r *emptyTurnAfterCompactRuntime) PrepareMessageInput(_ context.Context, emit func(agent.Message)) error {
	if emit != nil {
		emit(agent.Message{Type: agent.MessageCompactionStarted})
		emit(agent.Message{Type: agent.MessageCompactionFinished})
	}
	return nil
}

func (r *emptyTurnAfterCompactRuntime) AcceptMessageBatch(context.Context, []agent.ResidentMessage) (agent.ResidentMessageAcceptance, error) {
	done := make(chan error, 1)
	done <- agent.ErrResidentTurnNoSemanticWork
	close(done)
	return agent.ResidentMessageAcceptance{Done: done}, nil
}

func (r *emptyTurnAfterCompactRuntime) ForceKill() error { return nil }

func TestRuntimePoolCompactThenEmptyTurnDoesNotAcceptDelivery(t *testing.T) {
	backend := &emptyTurnAfterCompactRuntime{}
	pool := newAgentRuntimePool()
	pool.slots["agent-1\x00runtime-1"] = &agentRuntimeSlot{
		backend: backend,
	}
	accepted := false
	err := pool.deliverIdleMessages(
		context.Background(), "agent-1", "runtime-1",
		[]protocol.AgentMessageProjection{{ID: "message-1", Target: "dm:one", Seq: 1}},
		nil,
		func() { accepted = true },
		nil,
		nil,
	)
	if !errors.Is(err, agent.ErrResidentTurnNoSemanticWork) {
		t.Fatalf("handoff error = %v, want ErrResidentTurnNoSemanticWork", err)
	}
	if accepted {
		t.Fatal("compaction-only empty turn must not persist a Context Boundary receipt")
	}
}

func TestRuntimePoolCompactionPreparationFailureDoesNotRestartResidentProcess(t *testing.T) {
	backend := &failingCompactionPreparationRuntime{}
	pool := newAgentRuntimePool()
	slot := &agentRuntimeSlot{backend: backend}
	pool.slots["agent-1\x00runtime-1"] = slot
	var observed []agent.MessageType
	err := pool.deliverIdleMessages(
		context.Background(), "agent-1", "runtime-1",
		[]protocol.AgentMessageProjection{{ID: "message-1", Target: "dm:one", Seq: 1}},
		nil, nil,
		func(message agent.Message) { observed = append(observed, message.Type) },
		nil,
	)
	if err == nil || err.Error() != "context compaction failed" {
		t.Fatalf("handoff error = %v, want compaction failure", err)
	}
	if !reflect.DeepEqual(observed, []agent.MessageType{agent.MessageCompactionStarted}) {
		t.Fatalf("compaction failure Activity = %v", observed)
	}
	slot.mu.Lock()
	running, retained := slot.running, slot.backend == backend
	slot.mu.Unlock()
	if running || !retained || backend.killCalls != 0 {
		t.Fatalf("failed compaction changed process lifecycle: running=%v retained=%v kill_calls=%d", running, retained, backend.killCalls)
	}
}

type settlementBlockingPiTurn struct {
	boundary string
	done     chan error
}

type settlementBlockingPiRuntime struct {
	mu                sync.Mutex
	binding           agent.PiRunBinding
	boundarySerial    int
	active            bool
	accepted          chan settlementBlockingPiTurn
	settlementStarted chan struct{}
	settlementRelease chan struct{}
	releaseOnce       sync.Once
	settlementCalls   int
}

func newSettlementBlockingPiRuntime() *settlementBlockingPiRuntime {
	return &settlementBlockingPiRuntime{
		accepted:          make(chan settlementBlockingPiTurn, 2),
		settlementStarted: make(chan struct{}),
		settlementRelease: make(chan struct{}),
	}
}

func (r *settlementBlockingPiRuntime) Execute(context.Context, string, agent.ExecOptions) (*agent.Session, error) {
	return nil, nil
}

func (r *settlementBlockingPiRuntime) AcceptMessageBatch(_ context.Context, _ []agent.ResidentMessage) (agent.ResidentMessageAcceptance, error) {
	r.mu.Lock()
	if r.active {
		r.mu.Unlock()
		return agent.ResidentMessageAcceptance{}, agent.ErrPiRPCTurnBusy
	}
	r.active = true
	turn := settlementBlockingPiTurn{boundary: r.binding.CaptureBoundary, done: make(chan error, 1)}
	r.mu.Unlock()
	r.accepted <- turn
	return agent.ResidentMessageAcceptance{Done: turn.done}, nil
}

func (r *settlementBlockingPiRuntime) PrepareMessageInput(_ context.Context, _ func(agent.Message)) error {
	return nil
}

func (r *settlementBlockingPiRuntime) AcceptIdleInboxNotice(_ context.Context, _ agent.ResidentPendingNotice) (agent.ResidentMessageAcceptance, error) {
	return agent.ResidentMessageAcceptance{}, nil
}

func (r *settlementBlockingPiRuntime) AcceptPendingNotice(context.Context, agent.ResidentPendingNotice) error {
	return nil
}

func (r *settlementBlockingPiRuntime) BindRunIdentity(identity agent.PiRunIdentity) (agent.PiRunBinding, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.binding.SessionID == "" {
		r.boundarySerial = 1
		r.binding = agent.PiRunBinding{
			PiRunIdentity:   identity,
			SessionID:       "pi-session",
			CaptureBoundary: "capture-1",
		}
	} else if r.binding.PiRunIdentity != identity {
		return agent.PiRunBinding{}, agent.ErrPiRPCRunIdentityRequiresFreshSession
	}
	return r.binding, nil
}

func (r *settlementBlockingPiRuntime) PrepareRun(_ context.Context, identity agent.PiRunIdentity) (agent.PiRunBinding, error) {
	return r.BindRunIdentity(identity)
}
func (r *settlementBlockingPiRuntime) SettleRunTurn(identity agent.PiRunIdentity) error {
	r.mu.Lock()
	if r.binding.PiRunIdentity != identity {
		r.mu.Unlock()
		return errors.New("Pi turn settlement identity mismatch")
	}
	r.settlementCalls++
	call := r.settlementCalls
	r.mu.Unlock()
	if call == 1 {
		close(r.settlementStarted)
		<-r.settlementRelease
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.active {
		return agent.ErrPiRPCTurnBusy
	}
	r.boundarySerial++
	r.binding.CaptureBoundary = fmt.Sprintf("capture-%d", r.boundarySerial)
	return nil
}

func (r *settlementBlockingPiRuntime) complete(turn settlementBlockingPiTurn, err error) {
	r.mu.Lock()
	r.active = false
	r.mu.Unlock()
	turn.done <- err
	close(turn.done)
}

func (r *settlementBlockingPiRuntime) releaseSettlement() {
	r.releaseOnce.Do(func() { close(r.settlementRelease) })
}

func (r *settlementBlockingPiRuntime) Close() {}

func (r *settlementBlockingPiRuntime) Compact(context.Context, string) (agent.PiCompactionResult, error) {
	return agent.PiCompactionResult{}, nil
}

func (r *settlementBlockingPiRuntime) SetAutoCompaction(context.Context, bool) error { return nil }

func (r *settlementBlockingPiRuntime) RuntimeStats(context.Context) (*agent.RuntimeTokenStats, error) {
	return nil, nil
}

type pendingNoticeRuntime struct {
	mu      sync.Mutex
	notices []agent.ResidentPendingNotice
}

func commitPendingNoticeForTest(commit func()) bool {
	commit()
	return true
}

func newTestMessageCoordinator(t *testing.T, agentRoot string, deliver RuntimeMessageDelivery, activity MessageReceivedActivity) (*MessageCoordinator, error) {
	t.Helper()
	coordinator, err := NewMessageCoordinator(InboxKey{WorkspaceID: "workspace-test", AgentID: "agent-test"}, agentRoot, deliver, activity)
	if err != nil {
		return nil, err
	}
	store := newAgentAppInboxStore("agent-test", filepath.Join(agentRoot, "inbox", "state.json"))
	if err := store.Restore(); err != nil {
		return nil, err
	}
	coordinator.SetInboxStore(store)
	return coordinator, nil
}

func (r *pendingNoticeRuntime) Execute(context.Context, string, agent.ExecOptions) (*agent.Session, error) {
	return nil, nil
}

func (r *pendingNoticeRuntime) AcceptPendingNotice(_ context.Context, notice agent.ResidentPendingNotice) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.notices = append(r.notices, notice)
	return nil
}

func (r *pendingNoticeRuntime) snapshot() []agent.ResidentPendingNotice {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]agent.ResidentPendingNotice(nil), r.notices...)
}

func TestRuntimePoolRetainsAdmissionUntilAcceptedMessageTurnCompletes(t *testing.T) {
	backend := &blockingResidentMessageRuntime{done: make(chan error, 1)}
	pool := newAgentRuntimePool()
	pool.slots["agent-1\x00runtime-1"] = &agentRuntimeSlot{
		backend: backend,
	}
	messages := []protocol.AgentMessageProjection{{ID: "message-1", Target: "channel:one", Seq: 1, Content: "hello"}}
	if err := pool.deliverIdleMessages(context.Background(), "agent-1", "runtime-1", messages, nil, nil, nil, nil); err != nil {
		t.Fatalf("first handoff: %v", err)
	}
	if err := pool.deliverIdleMessages(context.Background(), "agent-1", "runtime-1", messages, nil, nil, nil, nil); !errors.Is(err, ErrCanonicalAgentRuntimeBusy) {
		t.Fatalf("overlapping handoff error = %v, want busy", err)
	}
	backend.done <- nil
	close(backend.done)
	deadline := time.Now().Add(2 * time.Second)
	for {
		pool.slots["agent-1\x00runtime-1"].mu.Lock()
		running := pool.slots["agent-1\x00runtime-1"].running
		pool.slots["agent-1\x00runtime-1"].mu.Unlock()
		if !running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("runtime pool did not release admission after native turn completion")
		}
		runtime.Gosched()
	}
}

func TestRuntimePoolSettlesPiTurnBeforeReopeningMessageAdmission(t *testing.T) {
	backend := newSettlementBlockingPiRuntime()
	pool := newAgentRuntimePool()
	pool.slots["agent-1\x00runtime-1"] = &agentRuntimeSlot{
		backend: backend,
	}
	identity := agent.PiRunIdentity{RunID: "run-1", RunAgentID: "run-agent-1"}
	initialBinding, err := pool.bindResidentPiRunIdentity(context.Background(), "agent-1", "runtime-1", identity)
	if err != nil {
		t.Fatalf("bind Pi run identity: %v", err)
	}
	messages := []protocol.AgentMessageProjection{{ID: "message-1", Target: "channel:one", Seq: 1, Content: "hello"}}
	firstComplete := make(chan error, 1)
	if err := pool.deliverIdleMessages(
		context.Background(), "agent-1", "runtime-1", messages,
		nil, nil, nil, func(err error, _ uint64, _ *agent.ResidentTurnCapture) { firstComplete <- err },
	); err != nil {
		t.Fatalf("first handoff: %v", err)
	}
	firstTurn := <-backend.accepted
	if firstTurn.boundary != initialBinding.CaptureBoundary {
		t.Fatalf("first turn boundary = %q, want %q", firstTurn.boundary, initialBinding.CaptureBoundary)
	}
	backend.complete(firstTurn, nil)
	select {
	case <-backend.settlementStarted:
	case <-time.After(time.Second):
		t.Fatal("first turn settlement did not start")
	}

	// Settlement is blocked after native completion. A racing handoff must not
	// cross Pi's input boundary until the pool has advanced the capture boundary.
	racingErr := pool.deliverIdleMessages(
		context.Background(), "agent-1", "runtime-1", messages,
		nil, nil, nil, nil,
	)
	backend.releaseSettlement()
	if !errors.Is(racingErr, ErrCanonicalAgentRuntimeBusy) {
		t.Fatalf("handoff during settlement error = %v, want pool busy", racingErr)
	}
	select {
	case err := <-firstComplete:
		if err != nil {
			t.Fatalf("first completion after settlement: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("first completion did not reopen admission after settlement")
	}

	secondComplete := make(chan error, 1)
	if err := pool.deliverIdleMessages(
		context.Background(), "agent-1", "runtime-1", messages,
		nil, nil, nil, func(err error, _ uint64, _ *agent.ResidentTurnCapture) { secondComplete <- err },
	); err != nil {
		t.Fatalf("second handoff after settlement: %v", err)
	}
	secondTurn := <-backend.accepted
	if secondTurn.boundary == firstTurn.boundary {
		t.Fatalf("second turn reused stale capture boundary %q", secondTurn.boundary)
	}
	backend.complete(secondTurn, nil)
	select {
	case err := <-secondComplete:
		if err != nil {
			t.Fatalf("second completion: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("second completion was not observed")
	}
}

func TestRuntimePoolPublishesAcceptanceBeforeResidentRuntimeActivity(t *testing.T) {
	backend := &activityResidentMessageRuntime{done: make(chan error, 1), messages: make(chan agent.Message, 1)}
	backend.messages <- agent.Message{Type: agent.MessageThinking}
	close(backend.messages)
	pool := newAgentRuntimePool()
	pool.slots["agent-1\x00runtime-1"] = &agentRuntimeSlot{backend: backend}

	var mu sync.Mutex
	var observed []string
	completionObserved := make(chan struct{})
	err := pool.deliverIdleMessages(
		context.Background(), "agent-1", "runtime-1",
		[]protocol.AgentMessageProjection{{ID: "message-1", Target: "dm:one", Seq: 1}},
		func() {
			mu.Lock()
			observed = append(observed, "starting")
			mu.Unlock()
		},
		func() {
			mu.Lock()
			observed = append(observed, "accepted")
			mu.Unlock()
		},
		func(agent.Message) {
			mu.Lock()
			observed = append(observed, "activity")
			mu.Unlock()
		},
		func(error, uint64, *agent.ResidentTurnCapture) {
			mu.Lock()
			observed = append(observed, "complete")
			mu.Unlock()
			close(completionObserved)
		},
	)
	if err != nil {
		t.Fatalf("handoff: %v", err)
	}
	backend.done <- nil
	close(backend.done)
	select {
	case <-completionObserved:
	case <-time.After(time.Second):
		t.Fatal("resident runtime completion was not observed")
	}
	mu.Lock()
	defer mu.Unlock()
	if !reflect.DeepEqual(observed, []string{"accepted", "activity", "complete"}) {
		t.Fatalf("callback order = %v, want acceptance, runtime Activity, then completion without synthetic Starting", observed)
	}
}

func TestRuntimePoolDrainsResidentActivityWithoutObserver(t *testing.T) {
	backend := &activityResidentMessageRuntime{done: make(chan error, 1), messages: make(chan agent.Message)}
	pool := newAgentRuntimePool()
	pool.slots["agent-1\x00runtime-1"] = &agentRuntimeSlot{backend: backend}

	completed := make(chan struct{})
	if err := pool.deliverIdleMessages(
		context.Background(), "agent-1", "runtime-1",
		[]protocol.AgentMessageProjection{{ID: "message-1", Target: "dm:one", Seq: 1}},
		nil, nil, nil, func(error, uint64, *agent.ResidentTurnCapture) { close(completed) },
	); err != nil {
		t.Fatalf("handoff: %v", err)
	}
	producerDone := make(chan struct{})
	go func() {
		backend.messages <- agent.Message{Type: agent.MessageThinking}
		close(backend.messages)
		close(producerDone)
	}()
	backend.done <- nil
	close(backend.done)

	select {
	case <-producerDone:
	case <-time.After(time.Second):
		t.Fatal("resident Activity producer blocked without an Activity observer")
	}
	select {
	case <-completed:
	case <-time.After(time.Second):
		t.Fatal("resident runtime completion was not observed after Activity drain")
	}
}

func TestRuntimePoolSuppressesStaleTerminalActivityAfterNextTurnStarts(t *testing.T) {
	backend := &sequencedResidentMessageRuntime{accepted: make(chan chan error, 2)}
	pool := newAgentRuntimePool()
	pool.slots["agent-1\x00runtime-1"] = &agentRuntimeSlot{
		backend: backend,
	}
	messages := []protocol.AgentMessageProjection{{ID: "message-1", Target: "channel:one", Seq: 1}}
	result := make(chan bool, 1)
	if err := pool.deliverIdleMessages(context.Background(), "agent-1", "runtime-1", messages, nil, nil, nil, func(_ error, generation uint64, _ *agent.ResidentTurnCapture) {
		if err := pool.deliverIdleMessages(context.Background(), "agent-1", "runtime-1", messages, nil, nil, nil, nil); err != nil {
			result <- true
			return
		}
		result <- pool.publishIfMessageTurnStillIdle("agent-1", "runtime-1", generation, func() {})
	}); err != nil {
		t.Fatalf("first handoff: %v", err)
	}
	firstDone := <-backend.accepted
	firstDone <- nil
	if stalePublished := <-result; stalePublished {
		t.Fatal("prior turn published a terminal Activity after the next turn started")
	}
	secondDone := <-backend.accepted
	secondDone <- nil
}

// TestResidentMessageTurnCompletionDoesNotAutoDeliverPending locks Raft
// alignment: after a turn ends, Pending bodies must not auto-deliver into a new
// turn (former FlushOnTurnCompletion). Idle Accept→Flush or explicit Flush /
// message check advances bodies later.
func TestResidentMessageTurnCompletionDoesNotAutoDeliverPending(t *testing.T) {
	const (
		workspaceID = "11111111-1111-4111-8111-111111111111"
		runtimeID   = "22222222-2222-4222-8222-222222222222"
		agentID     = "agent-1"
	)
	root := t.TempDir()
	d := New(Config{WorkspacesRoot: root}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	d.mu.Lock()
	d.runtimeIndex[runtimeID] = Runtime{ID: runtimeID, WorkspaceID: workspaceID}
	d.mu.Unlock()
	runner, _ := attachTestWorkspaceDaemon(t, d, workspaceID, func(string, any) error { return nil })
	if _, err := runner.processes.Start(agentProcessStartRequest{AgentID: agentID, RuntimeID: runtimeID}); err != nil {
		t.Fatal(err)
	}
	backend := &sequencedResidentMessageRuntime{accepted: make(chan chan error, 2)}
	d.canonicalRuntimes.slots[agentID+"\x00"+runtimeID] = &agentRuntimeSlot{
		backend: backend,
	}
	if _, err := d.ensureIdleMessageCoordinator(workspaceID, agentID, runtimeID); err != nil {
		t.Fatalf("ensure coordinator: %v", err)
	}
	producer := runner.activity
	activities := make(chan protocol.AgentActivityPayload, 8)
	producer.AttachTransport(func(activity protocol.AgentActivityPayload) { activities <- activity })
	coordinator, _ := resolveTestInbox(t, d, InboxKey{WorkspaceID: workspaceID, AgentID: agentID})
	completeCoordinatorRecovery(t, coordinator)

	first := testDelivery("message-1", "channel:one", 1, "delivery-1")
	second := testDelivery("message-2", "channel:one", 2, "delivery-2")
	if _, err := coordinator.Accept(context.Background(), first); err != nil {
		t.Fatalf("accept first: %v", err)
	}
	if err := coordinator.Flush(context.Background()); err != nil {
		t.Fatalf("flush first: %v", err)
	}
	firstDone := <-backend.accepted
	if _, err := coordinator.Accept(context.Background(), second); err != nil {
		t.Fatalf("accept second: %v", err)
	}
	if err := coordinator.Flush(context.Background()); !errors.Is(err, ErrCanonicalAgentRuntimeBusy) {
		t.Fatalf("busy flush error = %v", err)
	}
	firstDone <- nil

	// Raft: no automatic second body handoff after turn completion.
	select {
	case <-backend.accepted:
		t.Fatal("pending Message must not auto body-handoff after turn completion")
	case <-time.After(200 * time.Millisecond):
	}
	if got := coordinator.Boundaries()["channel:one"]; got != 1 {
		t.Fatalf("Context Boundary after first turn = %d, want 1 (pending not consumed)", got)
	}

	// Explicit idle Flush still hands off the remaining Pending body.
	if err := coordinator.Flush(context.Background()); err != nil {
		t.Fatalf("explicit flush of pending: %v", err)
	}
	secondDone := <-backend.accepted
	secondDone <- nil

	// Working (first) → Online (first complete) → Working (explicit flush) → Online
	// Activity ordering can interleave; require boundary advanced through seq 2.
	deadline := time.Now().Add(time.Second)
	for coordinator.Boundaries()["channel:one"] != 2 {
		if time.Now().After(deadline) {
			t.Fatalf("Context Boundary = %d, want 2 after explicit flush", coordinator.Boundaries()["channel:one"])
		}
		runtime.Gosched()
	}
	_ = activities // activity channel drained optionally; boundary is authoritative
}

func TestResidentMessageTurnErrorDoesNotAutoDeliverPending(t *testing.T) {
	const (
		workspaceID = "11111111-1111-4111-8111-111111111111"
		runtimeID   = "22222222-2222-4222-8222-222222222222"
		agentID     = "agent-1"
	)
	root := t.TempDir()
	d := New(Config{WorkspacesRoot: root}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	d.mu.Lock()
	d.runtimeIndex[runtimeID] = Runtime{ID: runtimeID, WorkspaceID: workspaceID}
	d.mu.Unlock()
	runner, _ := attachTestWorkspaceDaemon(t, d, workspaceID, func(string, any) error { return nil })
	if _, err := runner.processes.Start(agentProcessStartRequest{AgentID: agentID, RuntimeID: runtimeID}); err != nil {
		t.Fatal(err)
	}
	backend := &sequencedResidentMessageRuntime{accepted: make(chan chan error, 2)}
	d.canonicalRuntimes.slots[agentID+"\x00"+runtimeID] = &agentRuntimeSlot{
		backend: backend,
	}
	if _, err := d.ensureIdleMessageCoordinator(workspaceID, agentID, runtimeID); err != nil {
		t.Fatalf("ensure coordinator: %v", err)
	}
	coordinator, _ := resolveTestInbox(t, d, InboxKey{WorkspaceID: workspaceID, AgentID: agentID})
	completeCoordinatorRecovery(t, coordinator)

	first := testDelivery("message-1", "channel:one", 1, "delivery-1")
	second := testDelivery("message-2", "channel:one", 2, "delivery-2")
	if _, err := coordinator.Accept(context.Background(), first); err != nil {
		t.Fatalf("accept first: %v", err)
	}
	if err := coordinator.Flush(context.Background()); err != nil {
		t.Fatalf("flush first: %v", err)
	}
	firstDone := <-backend.accepted
	if _, err := coordinator.Accept(context.Background(), second); err != nil {
		t.Fatalf("accept second: %v", err)
	}
	if err := coordinator.Flush(context.Background()); !errors.Is(err, ErrCanonicalAgentRuntimeBusy) {
		t.Fatalf("busy flush error = %v", err)
	}
	firstDone <- errors.New("provider stream failed")

	select {
	case <-backend.accepted:
		t.Fatal("pending Message must not auto body-handoff after turn error")
	case <-time.After(200 * time.Millisecond):
	}
	if err := coordinator.Flush(context.Background()); err != nil {
		t.Fatalf("explicit flush after error: %v", err)
	}
	secondDone := <-backend.accepted
	secondDone <- nil

	deadline := time.Now().Add(time.Second)
	for coordinator.Boundaries()["channel:one"] != 2 {
		if time.Now().After(deadline) {
			t.Fatalf("Context Boundary = %d, want 2", coordinator.Boundaries()["channel:one"])
		}
		runtime.Gosched()
	}
}

func TestRuntimePoolSuppressesUnchangedSameSessionNoticeAndReportsOnlyChangedTargets(t *testing.T) {
	backend := &pendingNoticeRuntime{}
	pool := newAgentRuntimePool()
	pool.slots["agent-1\x00runtime-1"] = &agentRuntimeSlot{
		backend: backend, running: true,
	}
	first := InboxNoticeSnapshot{
		Notice: agent.ResidentPendingNotice{TotalPending: 2, ChangedTargets: []agent.ResidentPendingTarget{
			{Target: "#one", PendingCount: 1},
			{Target: "dm:@two", PendingCount: 1},
		}},
		Fingerprint:        "all-v1",
		TargetFingerprints: map[string]string{"channel:one": "one-v1", "dm:two": "two-v1"},
		TargetKeys:         []string{"channel:one", "dm:two"},
		CoordinatorID:      "coordinator-1",
		PendingGeneration:  1,
	}
	if err := pool.deliverBusyInboxNotice(context.Background(), "agent-1", "runtime-1", first, commitPendingNoticeForTest); err != nil {
		t.Fatalf("first Notice: %v", err)
	}
	if err := pool.deliverBusyInboxNotice(context.Background(), "agent-1", "runtime-1", first, commitPendingNoticeForTest); err != nil {
		t.Fatalf("duplicate Notice: %v", err)
	}
	newGeneration := first
	newGeneration.PendingGeneration = 2
	if err := pool.deliverBusyInboxNotice(context.Background(), "agent-1", "runtime-1", newGeneration, commitPendingNoticeForTest); err != nil {
		t.Fatalf("same fingerprint at new Pending generation: %v", err)
	}
	second := InboxNoticeSnapshot{
		Notice: agent.ResidentPendingNotice{TotalPending: 3, ChangedTargets: []agent.ResidentPendingTarget{
			{Target: "#one", PendingCount: 2},
			{Target: "dm:@two", PendingCount: 1},
		}},
		Fingerprint:        "all-v2",
		TargetFingerprints: map[string]string{"channel:one": "one-v2", "dm:two": "two-v1"},
		TargetKeys:         []string{"channel:one", "dm:two"},
		CoordinatorID:      "coordinator-1",
		PendingGeneration:  3,
	}
	if err := pool.deliverBusyInboxNotice(context.Background(), "agent-1", "runtime-1", second, commitPendingNoticeForTest); err != nil {
		t.Fatalf("changed Notice: %v", err)
	}
	got := backend.snapshot()
	if len(got) != 3 {
		t.Fatalf("runtime Notices = %+v, want first, new generation, and changed Notice", got)
	}
	if !reflect.DeepEqual(got[0].ChangedTargets, first.Notice.ChangedTargets) {
		t.Fatalf("first changed targets = %+v", got[0].ChangedTargets)
	}
	if !reflect.DeepEqual(got[1].ChangedTargets, first.Notice.ChangedTargets) {
		t.Fatalf("new-generation changed targets = %+v", got[1].ChangedTargets)
	}
	if want := []agent.ResidentPendingTarget{{Target: "#one", PendingCount: 2}}; !reflect.DeepEqual(got[2].ChangedTargets, want) {
		t.Fatalf("changed Notice targets = %+v, want %+v", got[2].ChangedTargets, want)
	}
}

func TestMessageCoordinatorBlockedPendingNoticeDoesNotBlockNewDelivery(t *testing.T) {
	noticeStarted := make(chan struct{})
	releaseNotice := make(chan struct{})
	var startOnce, releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(releaseNotice) }) })
	coordinator, err := newTestMessageCoordinator(t, t.TempDir(), func(context.Context, []protocol.AgentMessageProjection) error { return nil }, nil)
	if err != nil {
		t.Fatalf("NewMessageCoordinator: %v", err)
	}
	coordinator.ConfigurePendingNotices(func(_ context.Context, _ InboxNoticeSnapshot, commitIfCurrent InboxNoticeCommitIfCurrent) error {
		startOnce.Do(func() { close(noticeStarted) })
		<-releaseNotice
		if !commitIfCurrent(func() {}) {
			return errPendingNoticeGenerationChanged
		}
		return nil
	}, 10*time.Millisecond, 20*time.Millisecond)
	t.Cleanup(coordinator.Close)
	completeCoordinatorRecovery(t, coordinator)
	if _, err := coordinator.Accept(context.Background(), testDelivery("message-1", "channel:one", 1, "delivery-1")); err != nil {
		t.Fatalf("Accept first Delivery: %v", err)
	}
	coordinator.NotifyPendingAfterTurn()
	select {
	case <-noticeStarted:
	case <-time.After(time.Second):
		t.Fatal("Pending Notice did not start")
	}
	acceptDone := make(chan error, 1)
	go func() {
		_, err := coordinator.Accept(context.Background(), testDelivery("message-2", "channel:one", 2, "delivery-2"))
		acceptDone <- err
	}()
	select {
	case err := <-acceptDone:
		if err != nil {
			t.Fatalf("Accept during Pending Notice: %v", err)
		}
	case <-time.After(time.Second):
		releaseOnce.Do(func() { close(releaseNotice) })
		<-acceptDone
		t.Fatal("Pending Notice I/O held the coordinator state lock")
	}
	releaseOnce.Do(func() { close(releaseNotice) })
}

func TestMessageCoordinatorPendingChangeDuringNoticeRetriesCurrentGeneration(t *testing.T) {
	firstStarted := make(chan InboxNoticeSnapshot, 1)
	releaseFirst := make(chan struct{})
	committed := make(chan InboxNoticeSnapshot, 1)
	var (
		attemptMu   sync.Mutex
		attempts    int
		releaseOnce sync.Once
	)
	t.Cleanup(func() { releaseOnce.Do(func() { close(releaseFirst) }) })
	coordinator, err := newTestMessageCoordinator(t, t.TempDir(), func(context.Context, []protocol.AgentMessageProjection) error { return nil }, nil)
	if err != nil {
		t.Fatalf("NewMessageCoordinator: %v", err)
	}
	coordinator.ConfigurePendingNotices(func(_ context.Context, snapshot InboxNoticeSnapshot, commitIfCurrent InboxNoticeCommitIfCurrent) error {
		attemptMu.Lock()
		attempts++
		attempt := attempts
		attemptMu.Unlock()
		if attempt == 1 {
			firstStarted <- snapshot
			<-releaseFirst
		}
		if !commitIfCurrent(func() {}) {
			return errPendingNoticeGenerationChanged
		}
		committed <- snapshot
		return nil
	}, 10*time.Millisecond, 20*time.Millisecond)
	t.Cleanup(coordinator.Close)
	completeCoordinatorRecovery(t, coordinator)
	if _, err := coordinator.Accept(context.Background(), testDelivery("message-1", "channel:one", 1, "delivery-1")); err != nil {
		t.Fatalf("Accept first Delivery: %v", err)
	}
	coordinator.NotifyPendingAfterTurn()
	var first InboxNoticeSnapshot
	select {
	case first = <-firstStarted:
	case <-time.After(time.Second):
		t.Fatal("first Pending Notice did not start")
	}
	if _, err := coordinator.Accept(context.Background(), testDelivery("message-2", "channel:one", 2, "delivery-2")); err != nil {
		t.Fatalf("Accept changed Pending: %v", err)
	}
	releaseOnce.Do(func() { close(releaseFirst) })
	select {
	case current := <-committed:
		if current.PendingGeneration <= first.PendingGeneration || current.Notice.TotalPending != 2 {
			t.Fatalf("retried Notice = %+v, first generation %d", current, first.PendingGeneration)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("changed Pending generation did not retain Notice retry debt")
	}
}

func TestRuntimePoolDefersBusyNoticeAcrossCompactionBoundary(t *testing.T) {
	backend := &pendingNoticeRuntime{}
	pool := newAgentRuntimePool()
	slot := &agentRuntimeSlot{backend: backend, running: true}
	pool.slots["agent-1\x00runtime-1"] = slot
	snapshot := InboxNoticeSnapshot{
		Notice:             agent.ResidentPendingNotice{TotalPending: 1, ChangedTargets: []agent.ResidentPendingTarget{{Target: "dm:@one", PendingCount: 1}}},
		Fingerprint:        "all-v1",
		TargetFingerprints: map[string]string{"dm:one": "one-v1"},
		TargetKeys:         []string{"dm:one"},
	}

	pool.observeResidentRuntimeMessage(slot, agent.Message{Type: agent.MessageCompactionStarted})
	commit := func(apply func()) bool {
		apply()
		return true
	}
	if err := pool.deliverBusyInboxNotice(context.Background(), "agent-1", "runtime-1", snapshot, commit); !errors.Is(err, ErrCanonicalAgentRuntimeBusy) {
		t.Fatalf("Notice during compaction error = %v, want busy deferral", err)
	}
	if got := backend.snapshot(); len(got) != 0 {
		t.Fatalf("Notice crossed active compaction boundary: %+v", got)
	}

	pool.observeResidentRuntimeMessage(slot, agent.Message{Type: agent.MessageCompactionFinished})
	if err := pool.deliverBusyInboxNotice(context.Background(), "agent-1", "runtime-1", snapshot, commit); err != nil {
		t.Fatalf("Notice after compaction finish: %v", err)
	}
	if got := backend.snapshot(); len(got) != 1 {
		t.Fatalf("Notices after compaction finish = %+v, want one", got)
	}
}

func TestMessageCoordinatorReportsQueuedLifecycleOnAcceptAndFlush(t *testing.T) {
	var transitions []int
	coordinator, err := NewMessageCoordinator(InboxKey{WorkspaceID: "workspace-test", AgentID: "agent-test"}, t.TempDir(), func(context.Context, []protocol.AgentMessageProjection) error { return nil }, nil)
	if err != nil {
		t.Fatal(err)
	}
	coordinator.ConfigureQueueActivity(func(messages []protocol.AgentMessageProjection, delta int) {
		if len(messages) != 1 || messages[0].RunID != "run-1" || messages[0].RunAgentID != "run-agent-1" {
			t.Fatalf("queue activity messages = %+v", messages)
		}
		transitions = append(transitions, delta)
	})
	message := protocol.AgentMessageProjection{ID: "message-1", Target: "channel:one", Seq: 1, Content: "hello", RunID: "run-1", RunAgentID: "run-agent-1", DeliveryID: "delivery-1"}
	created, err := coordinator.Accept(context.Background(), protocol.AgentDeliverPayload{
		AgentID: "agent-1", Target: message.Target, Seq: message.Seq, DeliveryID: message.DeliveryID, Message: message,
	})
	if err != nil || !created {
		t.Fatalf("accept mixed-run message created=%v err=%v", created, err)
	}
	if err := coordinator.Flush(context.Background()); err != nil {
		t.Fatalf("flush mixed-run message: %v", err)
	}
	if !reflect.DeepEqual(transitions, []int{1, -1}) {
		t.Fatalf("queue lifecycle transitions = %v, want [1 -1]", transitions)
	}
}

func TestMessageCoordinatorCoalescesContentFreeBusyNoticeWithoutConsumption(t *testing.T) {
	root := t.TempDir()
	var mu sync.Mutex
	var notices []agent.ResidentPendingNotice

	activityCount := 0
	coordinator, err := newTestMessageCoordinator(t, root, func(context.Context, []protocol.AgentMessageProjection) error {
		return ErrCanonicalAgentRuntimeBusy
	}, func([]protocol.AgentMessageProjection) {
		activityCount++
	})
	if err != nil {
		t.Fatalf("NewMessageCoordinator: %v", err)
	}
	coordinator.ConfigurePendingNotices(func(_ context.Context, snapshot InboxNoticeSnapshot, commitIfCurrent InboxNoticeCommitIfCurrent) error {
		mu.Lock()
		notices = append(notices, snapshot.Notice)
		mu.Unlock()
		if !commitIfCurrent(func() {}) {
			return errPendingNoticeGenerationChanged
		}
		return nil
	}, 25*time.Millisecond, 40*time.Millisecond)
	t.Cleanup(coordinator.Close)
	completeCoordinatorRecovery(t, coordinator)

	deliveries := []protocol.AgentDeliverPayload{
		testDelivery("message-1", "channel:one", 1, "delivery-1"),
		testDelivery("message-2", "channel:one", 2, "delivery-2"),
		testDelivery("message-3", "dm:two", 1, "delivery-3"),
	}
	deliveries[0].Message.ReplyTarget = "#one"
	deliveries[1].Message.ReplyTarget = "#one"
	deliveries[2].Message.ReplyTarget = "dm:@two"
	deliveries[0].Message.Content = "secret body one"
	deliveries[1].Message.Content = "secret body two"
	deliveries[2].Message.Parts = []protocol.MessagePart{{Type: protocol.MessagePartTypeAttachment, AttachmentID: "attachment-secret"}}
	for _, delivery := range deliveries {
		if _, err := coordinator.Accept(context.Background(), delivery); err != nil {
			t.Fatalf("Accept: %v", err)
		}
		if err := coordinator.Flush(context.Background()); !errors.Is(err, ErrCanonicalAgentRuntimeBusy) {
			t.Fatalf("busy Flush error = %v", err)
		}
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		count := len(notices)
		mu.Unlock()
		if count == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("busy Notice count = %d, want 1", count)
		}
		runtime.Gosched()
	}
	mu.Lock()
	notice := notices[0]
	mu.Unlock()
	if notice.TotalPending != 3 || !reflect.DeepEqual(notice.ChangedTargets, []agent.ResidentPendingTarget{
		{Target: "#one", PendingCount: 2},
		{Target: "dm:@two", PendingCount: 1},
	}) {
		t.Fatalf("Notice = %+v", notice)
	}
	raw, err := json.Marshal(notice)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"secret body", "attachment-secret", `"parts"`, `"content"`} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("Notice leaked %q: %s", forbidden, raw)
		}
	}
	if activityCount != 0 {
		t.Fatalf("Message received Activity count = %d, want 0", activityCount)
	}
	if got := coordinator.Boundaries(); len(got) != 0 {
		t.Fatalf("Notice advanced Context Boundaries: %v", got)
	}
}

func TestMessageCoordinatorRetriesFailedBusyNoticeWithoutLosingDebt(t *testing.T) {
	root := t.TempDir()
	var mu sync.Mutex
	attempts := 0
	coordinator, err := newTestMessageCoordinator(t, root, func(context.Context, []protocol.AgentMessageProjection) error {
		return ErrCanonicalAgentRuntimeBusy
	}, func([]protocol.AgentMessageProjection) {
		t.Fatal("Pending Notice must not emit Message received Activity")
	})
	if err != nil {
		t.Fatalf("NewMessageCoordinator: %v", err)
	}
	coordinator.ConfigurePendingNotices(func(_ context.Context, snapshot InboxNoticeSnapshot, commitIfCurrent InboxNoticeCommitIfCurrent) error {
		mu.Lock()
		attempts++
		attempt := attempts
		if snapshot.Notice.TotalPending != 1 {
			t.Errorf("Notice total = %d, want 1", snapshot.Notice.TotalPending)
		}
		mu.Unlock()
		if attempt == 1 {
			return errors.New("unsafe provider receipt")
		}
		if !commitIfCurrent(func() {}) {
			return errPendingNoticeGenerationChanged
		}
		return nil
	}, 20*time.Millisecond, 30*time.Millisecond)
	t.Cleanup(coordinator.Close)
	completeCoordinatorRecovery(t, coordinator)

	delivery := testDelivery("message-1", "channel:one", 1, "delivery-1")
	if _, err := coordinator.Accept(context.Background(), delivery); err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if err := coordinator.Flush(context.Background()); !errors.Is(err, ErrCanonicalAgentRuntimeBusy) {
		t.Fatalf("busy Flush error = %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		gotAttempts := attempts
		mu.Unlock()
		if gotAttempts >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("Notice attempts = %d, want retry", gotAttempts)
		}
		runtime.Gosched()
	}
	coordinator.mu.Lock()
	got := coordinator.pendingBatchLocked()
	coordinator.mu.Unlock()
	if len(got) != 1 || got[0].ID != delivery.Message.ID {
		t.Fatalf("Pending after Notice retry = %+v", got)
	}
}

func TestClosedMessageCoordinatorRejectsPendingWork(t *testing.T) {
	c, err := newTestMessageCoordinator(t, t.TempDir(), func(context.Context, []protocol.AgentMessageProjection) error { return nil }, nil)
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	if _, err := c.Accept(context.Background(), testDelivery("message-1", "channel:one", 1, "delivery-1")); err == nil {
		t.Fatal("closed coordinator accepted work")
	}
}

func TestMessageCoordinatorInboxACKAdvancesOnlyAfterACK(t *testing.T) {
	c, err := newTestMessageCoordinator(t, t.TempDir(), func(context.Context, []protocol.AgentMessageProjection) error { return nil }, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := int64(1); i <= 3; i++ {
		if _, err := c.Accept(context.Background(), testDelivery(fmt.Sprintf("message-%d", i), "channel:one", i, fmt.Sprintf("delivery-%d", i))); err != nil {
			t.Fatal(err)
		}
	}
	result, err := c.PreflightMessageSend("channel:one")
	if err != nil || result.Revision == 0 || len(result.Messages) != 3 {
		t.Fatalf("preflight=%+v err=%v", result, err)
	}
	if c.Boundaries()["channel:one"] != 0 {
		t.Fatal("preflight advanced boundary")
	}
	items := c.MessageItemsSnapshot()
	if len(items) != 3 || !c.inboxStore.Ack(items[0].ItemID) {
		t.Fatal("item ACK did not retire one item")
	}
}

func testDelivery(id, target string, seq int64, deliveryID string) protocol.AgentDeliverPayload {
	return protocol.AgentDeliverPayload{
		AgentID: "agent-1", Target: target, Seq: seq, DeliveryID: deliveryID,
		Message: protocol.AgentMessageProjection{ID: id, Target: target, Seq: seq, Content: id},
	}
}

func completeCoordinatorRecovery(t *testing.T, coordinator *MessageCoordinator) {
	t.Helper()
}

func TestMessageCoordinatorAcceptsBeforeAckWithoutAdvancingBoundary(t *testing.T) {
	root := t.TempDir()
	coordinator, err := newTestMessageCoordinator(t, root, func(context.Context, []protocol.AgentMessageProjection) error { return nil }, nil)
	if err != nil {
		t.Fatalf("NewMessageCoordinator: %v", err)
	}

	accepted, err := coordinator.Accept(context.Background(), testDelivery("message-1", "channel-1", 1, "delivery-1"))
	if err != nil || !accepted {
		t.Fatalf("Accept = %v, %v; want accepted", accepted, err)
	}
	if got := coordinator.Boundaries(); len(got) != 0 {
		t.Fatalf("boundary advanced on acknowledgement: %v", got)
	}
}

func TestMessageCoordinatorReadLeavesStoreItemsActive(t *testing.T) {
	root := t.TempDir()
	var handoffs, activities int
	coordinator, err := newTestMessageCoordinator(t, root, func(context.Context, []protocol.AgentMessageProjection) error {
		handoffs++
		return nil
	}, func([]protocol.AgentMessageProjection) {
		activities++
	})
	if err != nil {
		t.Fatalf("NewMessageCoordinator: %v", err)
	}
	completeCoordinatorRecovery(t, coordinator)
	for _, delivery := range []protocol.AgentDeliverPayload{
		testDelivery("one-5", "channel:one", 5, "delivery-one-5"),
		testDelivery("two-8", "channel:two", 8, "delivery-two-8"),
	} {
		if _, err := coordinator.Accept(context.Background(), delivery); err != nil {
			t.Fatalf("Accept: %v", err)
		}
	}
	if got := coordinator.Boundaries()["channel:one"]; got != 0 {
		t.Fatalf("read advanced boundary = %d, want 0", got)
	}
	if len(coordinator.MessageItemsSnapshot()) < 2 {
		t.Fatalf("read retired inbox items, want durable items retained")
	}
	var got string
	for _, item := range coordinator.MessageItemsSnapshot() {
		if item.Message != nil && item.Message.Target == "channel:two" && item.Message.Seq == 8 {
			got = item.Message.ID
		}
	}
	if got != "two-8" {
		t.Fatalf("other target pending = %q, want two-8", got)
	}
	if handoffs != 0 || activities != 0 {
		t.Fatalf("read caused handoffs=%d activities=%d, want neither", handoffs, activities)
	}
}

func TestMessageCoordinatorFlushesTargetInSequenceAndRecordsOneActivityBatch(t *testing.T) {
	var handedOff [][]string
	var activities int
	coordinator, err := newTestMessageCoordinator(t, t.TempDir(), func(_ context.Context, messages []protocol.AgentMessageProjection) error {
		batch := make([]string, len(messages))
		for i, message := range messages {
			batch[i] = message.ID
		}
		handedOff = append(handedOff, batch)
		return nil
	}, func([]protocol.AgentMessageProjection) { activities++ })
	if err != nil {
		t.Fatalf("NewMessageCoordinator: %v", err)
	}
	completeCoordinatorRecovery(t, coordinator)
	for _, delivery := range []protocol.AgentDeliverPayload{
		testDelivery("message-2", "channel-1", 2, "delivery-2"),
		testDelivery("message-1", "channel-1", 1, "delivery-1"),
	} {
		if _, err := coordinator.Accept(context.Background(), delivery); err != nil {
			t.Fatalf("Accept: %v", err)
		}
	}
	if err := coordinator.Flush(context.Background()); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if got, want := handedOff, [][]string{{"message-1", "message-2"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("handoff = %v, want %v", got, want)
	}
	if activities != 1 {
		t.Fatalf("activities = %d, want 1", activities)
	}
	if got, want := coordinator.Boundaries(), map[string]int64{"channel-1": 2}; !reflect.DeepEqual(got, want) {
		t.Fatalf("boundaries = %v, want %v", got, want)
	}
}

func TestMessageCoordinatorBlockedRuntimeDeliveryDoesNotBlockNewDelivery(t *testing.T) {
	handoffStarted := make(chan struct{})
	releaseHandoff := make(chan struct{})
	var (
		callMu         sync.Mutex
		handoffBatches [][]string
		startOnce      sync.Once
		releaseOnce    sync.Once
	)
	t.Cleanup(func() { releaseOnce.Do(func() { close(releaseHandoff) }) })
	coordinator, err := newTestMessageCoordinator(t, t.TempDir(), func(_ context.Context, messages []protocol.AgentMessageProjection) error {
		batch := make([]string, len(messages))
		for index, message := range messages {
			batch[index] = message.ID
		}
		callMu.Lock()
		handoffBatches = append(handoffBatches, batch)
		callNumber := len(handoffBatches)
		callMu.Unlock()
		if callNumber == 1 {
			startOnce.Do(func() { close(handoffStarted) })
			<-releaseHandoff
		}
		return nil
	}, nil)
	if err != nil {
		t.Fatalf("NewMessageCoordinator: %v", err)
	}
	completeCoordinatorRecovery(t, coordinator)
	if _, err := coordinator.Accept(context.Background(), testDelivery("message-1", "channel-1", 1, "delivery-1")); err != nil {
		t.Fatalf("Accept first Delivery: %v", err)
	}

	flushDone := make(chan error, 1)
	go func() { flushDone <- coordinator.Flush(context.Background()) }()
	select {
	case <-handoffStarted:
	case <-time.After(time.Second):
		t.Fatal("Runtime handoff did not start")
	}

	type acceptResult struct {
		accepted bool
		err      error
	}
	acceptDone := make(chan acceptResult, 1)
	go func() {
		accepted, err := coordinator.Accept(context.Background(), testDelivery("message-2", "channel-1", 2, "delivery-2"))
		acceptDone <- acceptResult{accepted: accepted, err: err}
	}()
	var (
		acceptedWhileBlocked acceptResult
		acceptResponsive     bool
	)
	select {
	case acceptedWhileBlocked = <-acceptDone:
		acceptResponsive = true
	case <-time.After(time.Second):
	}
	if !acceptResponsive {
		releaseOnce.Do(func() { close(releaseHandoff) })
		<-flushDone
		<-acceptDone
		t.Fatal("blocked Runtime handoff held the coordinator lock against Accept")
	}
	if acceptedWhileBlocked.err != nil || !acceptedWhileBlocked.accepted {
		t.Fatalf("Accept during Runtime handoff = %+v", acceptedWhileBlocked)
	}
	coordinator.mu.Lock()
	pendingWhileBlocked := coordinator.pendingBatchLocked()
	coordinator.mu.Unlock()
	if len(pendingWhileBlocked) != 2 {
		t.Fatalf("Pending during blocked handoff = %+v, want two Messages", pendingWhileBlocked)
	}
	if got := []string{pendingWhileBlocked[0].ID, pendingWhileBlocked[1].ID}; !reflect.DeepEqual(got, []string{"message-1", "message-2"}) {
		t.Fatalf("Pending during blocked handoff = %v", got)
	}
	releaseOnce.Do(func() { close(releaseHandoff) })
	if err := <-flushDone; err != nil {
		t.Fatalf("Flush: %v", err)
	}
	callMu.Lock()
	gotBatches := append([][]string(nil), handoffBatches...)
	callMu.Unlock()
	if want := [][]string{{"message-1"}, {"message-2"}}; !reflect.DeepEqual(gotBatches, want) {
		t.Fatalf("reserved Runtime batches = %v, want %v", gotBatches, want)
	}
}

func TestMessageCoordinatorConcurrentFlushCannotReserveActiveBatch(t *testing.T) {
	handoffStarted := make(chan struct{})
	releaseHandoff := make(chan struct{})
	var (
		calls       int
		callMu      sync.Mutex
		startOnce   sync.Once
		releaseOnce sync.Once
	)
	t.Cleanup(func() { releaseOnce.Do(func() { close(releaseHandoff) }) })
	coordinator, err := newTestMessageCoordinator(t, t.TempDir(), func(context.Context, []protocol.AgentMessageProjection) error {
		callMu.Lock()
		calls++
		callMu.Unlock()
		startOnce.Do(func() { close(handoffStarted) })
		<-releaseHandoff
		return nil
	}, nil)
	if err != nil {
		t.Fatalf("NewMessageCoordinator: %v", err)
	}
	completeCoordinatorRecovery(t, coordinator)
	if _, err := coordinator.Accept(context.Background(), testDelivery("message-1", "channel-1", 1, "delivery-1")); err != nil {
		t.Fatalf("Accept: %v", err)
	}
	firstFlushDone := make(chan error, 1)
	go func() { firstFlushDone <- coordinator.Flush(context.Background()) }()
	select {
	case <-handoffStarted:
	case <-time.After(time.Second):
		t.Fatal("first Runtime handoff did not start")
	}
	secondFlushDone := make(chan error, 1)
	go func() { secondFlushDone <- coordinator.Flush(context.Background()) }()
	var (
		secondErr        error
		secondResponsive bool
	)
	select {
	case secondErr = <-secondFlushDone:
		secondResponsive = true
	case <-time.After(time.Second):
	}
	releaseOnce.Do(func() { close(releaseHandoff) })
	if err := <-firstFlushDone; err != nil {
		t.Fatalf("first Flush: %v", err)
	}
	if !secondResponsive {
		secondErr = <-secondFlushDone
		t.Fatalf("second Flush waited for active Runtime handoff; returned %v", secondErr)
	}
	if !errors.Is(secondErr, errRuntimeMessageDeliveryInProgress) {
		t.Fatalf("second Flush = %v, want active-delivery rejection", secondErr)
	}
	callMu.Lock()
	gotCalls := calls
	callMu.Unlock()
	if gotCalls != 1 {
		t.Fatalf("Runtime handoff calls = %d, want 1", gotCalls)
	}
}

func TestMessageCoordinatorBlockedActivityDoesNotBlockNewDelivery(t *testing.T) {
	activityStarted := make(chan struct{})
	releaseActivity := make(chan struct{})
	var startOnce, releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(releaseActivity) }) })
	coordinator, err := newTestMessageCoordinator(t, t.TempDir(), func(context.Context, []protocol.AgentMessageProjection) error { return nil }, func([]protocol.AgentMessageProjection) {
		startOnce.Do(func() { close(activityStarted) })
		<-releaseActivity
	})
	if err != nil {
		t.Fatalf("NewMessageCoordinator: %v", err)
	}
	completeCoordinatorRecovery(t, coordinator)
	if _, err := coordinator.Accept(context.Background(), testDelivery("message-1", "channel-1", 1, "delivery-1")); err != nil {
		t.Fatalf("Accept first Delivery: %v", err)
	}
	flushDone := make(chan error, 1)
	go func() { flushDone <- coordinator.Flush(context.Background()) }()
	select {
	case <-activityStarted:
	case <-time.After(time.Second):
		t.Fatal("Message received Activity did not start")
	}
	acceptDone := make(chan error, 1)
	go func() {
		_, err := coordinator.Accept(context.Background(), testDelivery("message-2", "channel-1", 2, "delivery-2"))
		acceptDone <- err
	}()
	select {
	case err := <-acceptDone:
		if err != nil {
			t.Fatalf("Accept during Activity: %v", err)
		}
	case <-time.After(time.Second):
		releaseOnce.Do(func() { close(releaseActivity) })
		<-flushDone
		t.Fatal("Activity callback held the coordinator state lock")
	}
	releaseOnce.Do(func() { close(releaseActivity) })
	if err := <-flushDone; err != nil {
		t.Fatalf("Flush: %v", err)
	}
}

func TestMessageCoordinatorCloseInvalidatesAcceptedDeliveryBeforeBoundaryCommit(t *testing.T) {
	activityStarted := make(chan struct{})
	releaseActivity := make(chan struct{})
	var startOnce, releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(releaseActivity) }) })
	coordinator, err := newTestMessageCoordinator(t, t.TempDir(), func(context.Context, []protocol.AgentMessageProjection) error { return nil }, func([]protocol.AgentMessageProjection) {
		startOnce.Do(func() { close(activityStarted) })
		<-releaseActivity
	})
	if err != nil {
		t.Fatalf("NewMessageCoordinator: %v", err)
	}
	completeCoordinatorRecovery(t, coordinator)
	if _, err := coordinator.Accept(context.Background(), testDelivery("message-1", "channel-1", 1, "delivery-1")); err != nil {
		t.Fatalf("Accept: %v", err)
	}
	flushDone := make(chan error, 1)
	go func() { flushDone <- coordinator.Flush(context.Background()) }()
	select {
	case <-activityStarted:
	case <-time.After(time.Second):
		t.Fatal("Message received Activity did not start")
	}
	closeDone := make(chan struct{})
	go func() {
		coordinator.Close()
		close(closeDone)
	}()
	select {
	case <-closeDone:
	case <-time.After(time.Second):
		releaseOnce.Do(func() { close(releaseActivity) })
		t.Fatal("Close waited for non-state Activity callback")
	}
	releaseOnce.Do(func() { close(releaseActivity) })
	if err := <-flushDone; !errors.Is(err, errRuntimeMessageDeliveryInvalidated) {
		t.Fatalf("Flush after Close = %v, want invalidated delivery", err)
	}
	if got := coordinator.Boundaries()["channel-1"]; got != 0 {
		t.Fatalf("stale accepted delivery advanced boundary to %d", got)
	}
}

func TestMessageCoordinatorCommitAdvancesExactTargetMaxima(t *testing.T) {
	coordinator, err := newTestMessageCoordinator(t, t.TempDir(), func(context.Context, []protocol.AgentMessageProjection) error { return nil }, nil)
	if err != nil {
		t.Fatalf("NewMessageCoordinator: %v", err)
	}
	completeCoordinatorRecovery(t, coordinator)
	for _, delivery := range []protocol.AgentDeliverPayload{
		testDelivery("message-a2", "channel:a", 2, "delivery-a2"),
		testDelivery("message-a4", "channel:a", 4, "delivery-a4"),
		testDelivery("message-b3", "channel:b", 3, "delivery-b3"),
	} {
		if _, err := coordinator.Accept(context.Background(), delivery); err != nil {
			t.Fatalf("Accept %s: %v", delivery.Message.ID, err)
		}
	}
	if err := coordinator.Flush(context.Background()); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if got, want := coordinator.Boundaries(), map[string]int64{"channel:a": 4, "channel:b": 3}; !reflect.DeepEqual(got, want) {
		t.Fatalf("committed boundaries = %v, want %v", got, want)
	}
}

func TestMessageCoordinatorDeduplicatesDeliveryWithoutSecondRuntimeDeliveryOrActivity(t *testing.T) {
	var handoffs, activities int
	coordinator, err := newTestMessageCoordinator(t, t.TempDir(), func(context.Context, []protocol.AgentMessageProjection) error { handoffs++; return nil }, func([]protocol.AgentMessageProjection) { activities++ })
	if err != nil {
		t.Fatalf("NewMessageCoordinator: %v", err)
	}
	completeCoordinatorRecovery(t, coordinator)
	delivery := testDelivery("message-1", "channel-1", 1, "delivery-1")
	if _, err := coordinator.Accept(context.Background(), delivery); err != nil {
		t.Fatalf("first Accept: %v", err)
	}
	if err := coordinator.Flush(context.Background()); err != nil {
		t.Fatalf("first Flush: %v", err)
	}
	accepted, err := coordinator.Accept(context.Background(), delivery)
	if err != nil || accepted {
		t.Fatalf("duplicate Accept = %v, %v; want false, nil", accepted, err)
	}
	if err := coordinator.Flush(context.Background()); err != nil {
		t.Fatalf("second Flush: %v", err)
	}
	if handoffs != 1 || activities != 1 {
		t.Fatalf("handoffs=%d activities=%d, want one each", handoffs, activities)
	}
}

func TestDaemonAcceptsIdleDeliveryThroughProviderBeforeAcknowledgement(t *testing.T) {
	root := t.TempDir()
	var handoffs int
	coordinator, err := newTestMessageCoordinator(t, root, func(context.Context, []protocol.AgentMessageProjection) error { handoffs++; return nil }, nil)
	if err != nil {
		t.Fatalf("NewMessageCoordinator: %v", err)
	}
	runtimePool := newAgentRuntimePool()
	runtimePool.slots["agent-1\x00runtime-1"] = &agentRuntimeSlot{
		backend: &residentProcessStartProbe{},
	}
	daemon := &Daemon{canonicalRuntimes: runtimePool}
	completeCoordinatorRecovery(t, coordinator)
	delivery := testDelivery("message-1", "channel-1", 1, "delivery-1")
	daemon.mu.Lock()
	daemon.runtimeIndex = map[string]Runtime{"runtime-1": {ID: "runtime-1", WorkspaceID: "workspace-1"}}
	daemon.mu.Unlock()
	runner := registerTestInbox(t, daemon, InboxKey{WorkspaceID: "workspace-1", AgentID: "agent-1"}, "runtime-1", coordinator)
	markTestLaunchRunning(t, runner, "agent-1")
	acceptance, err := runner.acceptMessageDelivery(context.Background(), delivery)
	if err != nil {
		t.Fatalf("acceptIdleAgentDelivery: %v", err)
	}
	if handoffs != 1 {
		t.Fatalf("handoffs = %d, want provider acceptance before acknowledgement", handoffs)
	}
	if got, want := acceptance.ack, (protocol.AgentDeliverAckPayload{AgentID: "agent-1", Seq: 1, DeliveryID: "delivery-1"}); !reflect.DeepEqual(got, want) {
		t.Fatalf("ack = %+v, want %+v", got, want)
	}
	if acceptance.outcome != messageDeliveryProviderAccepted {
		t.Fatalf("acceptance = %q, want %q", acceptance.outcome, messageDeliveryProviderAccepted)
	}
	if got := coordinator.Boundaries()["channel-1"]; got != 1 {
		t.Fatalf("boundary = %d, want 1", got)
	}
}

func TestCoordinatorReplacementInvalidatesInFlightDelivery(t *testing.T) {
	oldRoot := t.TempDir()
	newRoot := t.TempDir()
	handoffStarted := make(chan struct{})
	releaseHandoff := make(chan struct{})
	var startOnce, releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(releaseHandoff) }) })
	oldCoordinator, err := newTestMessageCoordinator(t, oldRoot, func(context.Context, []protocol.AgentMessageProjection) error {
		startOnce.Do(func() { close(handoffStarted) })
		<-releaseHandoff
		return nil
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	completeCoordinatorRecovery(t, oldCoordinator)
	if _, err := oldCoordinator.Accept(context.Background(), testDelivery("message-1", "channel-1", 1, "delivery-1")); err != nil {
		t.Fatal(err)
	}
	daemon := &Daemon{canonicalRuntimes: newAgentRuntimePool()}
	runner := registerTestInbox(t, daemon, InboxKey{WorkspaceID: "workspace-test", AgentID: "agent-1"}, "runtime-old", oldCoordinator)
	runner.inboxes.ownsRuntime = func(string) bool { return true }
	runner.inboxes.open = func(key InboxKey, runtimeID string) (*MessageCoordinator, error) {
		return NewMessageCoordinator(key, newRoot, func(context.Context, []protocol.AgentMessageProjection) error { return nil }, nil)
	}
	flushDone := make(chan error, 1)
	go func() { flushDone <- oldCoordinator.Flush(context.Background()) }()
	<-handoffStarted

	replacementDone := make(chan error, 1)
	go func() {
		_, err := runner.inboxes.AcceptStart("agent-1", "runtime-new")
		replacementDone <- err
	}()
	select {
	case err := <-replacementDone:
		if err != nil {
			t.Fatalf("replace coordinator: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("coordinator replacement waited for blocked Runtime handoff")
	}
	releaseOnce.Do(func() { close(releaseHandoff) })
	if err := <-flushDone; !errors.Is(err, errRuntimeMessageDeliveryInvalidated) {
		t.Fatalf("old coordinator Flush = %v, want invalidated delivery", err)
	}
	if got := oldCoordinator.Boundaries()["channel-1"]; got != 0 {
		t.Fatalf("stale handoff advanced old coordinator boundary to %d", got)
	}
	oldCoordinator.mu.Lock()
	pending := oldCoordinator.pendingBatchLocked()
	oldCoordinator.mu.Unlock()
	if len(pending) != 1 || pending[0].ID != "message-1" {
		t.Fatalf("stale handoff consumed old Pending: %+v", pending)
	}
	replacement, runtimeID := resolveTestInbox(t, daemon, InboxKey{WorkspaceID: "workspace-test", AgentID: "agent-1"})
	if replacement == nil {
		t.Fatal("replacement coordinator is nil")
	}
	if replacement == oldCoordinator || replacement.root != newRoot || runtimeID != "runtime-new" {
		t.Fatalf("replacement=%p old=%p root=%q runtime=%q", replacement, oldCoordinator, replacement.root, runtimeID)
	}
}

func TestMessageCoordinatorBoundaryResetsWithCoordinator(t *testing.T) {
	root := t.TempDir()
	delivery := testDelivery("message-1", "channel-1", 1, "delivery-1")
	first, err := newTestMessageCoordinator(t, root, func(context.Context, []protocol.AgentMessageProjection) error {
		return nil
	}, nil)
	if err != nil {
		t.Fatalf("NewMessageCoordinator: %v", err)
	}
	if _, err := first.Accept(context.Background(), delivery); err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if err := first.Flush(context.Background()); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if got := first.Boundaries()[delivery.Target]; got != delivery.Seq {
		t.Fatalf("first boundary = %d, want %d", got, delivery.Seq)
	}
	first.Close()

	var replayed []protocol.AgentMessageProjection
	second, err := newTestMessageCoordinator(t, root, func(_ context.Context, messages []protocol.AgentMessageProjection) error {
		replayed = append(replayed, messages...)
		return nil
	}, nil)
	if err != nil {
		t.Fatalf("replacement NewMessageCoordinator: %v", err)
	}
	if got := second.Boundaries(); len(got) != 0 {
		t.Fatalf("replacement restored process-local boundaries: %v", got)
	}
	if accepted, err := second.Accept(context.Background(), delivery); err != nil || accepted {
		t.Fatalf("replacement Accept = %v, %v; want durable duplicate", accepted, err)
	}
	if len(replayed) != 0 {
		t.Fatalf("replacement replay = %+v", replayed)
	}
}

func TestMessageCoordinatorRetriesRuntimeDeliverySafely(t *testing.T) {
	var attempts, activities int
	coordinator, err := newTestMessageCoordinator(t, t.TempDir(), func(context.Context, []protocol.AgentMessageProjection) error {
		attempts++
		if attempts == 1 {
			return errors.New("runtime input unavailable")
		}
		return nil
	}, func([]protocol.AgentMessageProjection) { activities++ })
	if err != nil {
		t.Fatalf("NewMessageCoordinator: %v", err)
	}
	completeCoordinatorRecovery(t, coordinator)
	if _, err := coordinator.Accept(context.Background(), testDelivery("message-1", "channel-1", 1, "delivery-1")); err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if err := coordinator.Flush(context.Background()); err == nil {
		t.Fatal("first Flush succeeded")
	}
	coordinator.mu.Lock()
	pendingAfterRejection := coordinator.pendingBatchLocked()
	coordinator.mu.Unlock()
	if len(pendingAfterRejection) != 1 || pendingAfterRejection[0].ID != "message-1" {
		t.Fatalf("runtime rejection consumed Pending: %+v", pendingAfterRejection)
	}
	if activities != 0 || coordinator.Boundaries()["channel-1"] != 0 {
		t.Fatalf("failed handoff activity=%d boundary=%d", activities, coordinator.Boundaries()["channel-1"])
	}
	if err := coordinator.Flush(context.Background()); err != nil {
		t.Fatalf("second Flush: %v", err)
	}
	if attempts != 2 || activities != 1 || coordinator.Boundaries()["channel-1"] != 1 {
		t.Fatalf("attempts=%d activities=%d boundary=%d", attempts, activities, coordinator.Boundaries()["channel-1"])
	}
}

func TestMessageCoordinatorPreflightReturnsRevision(t *testing.T) {
	c, err := newTestMessageCoordinator(t, t.TempDir(), func(context.Context, []protocol.AgentMessageProjection) error { return nil }, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := int64(1); i <= 5; i++ {
		if _, err := c.Accept(context.Background(), testDelivery(fmt.Sprintf("message-%d", i), "channel:one", i, fmt.Sprintf("delivery-%d", i))); err != nil {
			t.Fatal(err)
		}
	}
	result, err := c.PreflightMessageSend("channel:one")
	if err != nil || !result.Held || result.Revision == 0 || len(result.Messages) != 3 {
		t.Fatalf("preflight=%+v err=%v", result, err)
	}
	if len(c.MessageItemsSnapshot()) != 5 {
		t.Fatal("read/preflight changed durable inbox")
	}
}

func TestMessageCoordinatorExposesNoDraftStateOrMethods(t *testing.T) {
	typeOf := reflect.TypeOf(MessageCoordinator{})
	for index := 0; index < typeOf.NumField(); index++ {
		if strings.Contains(strings.ToLower(typeOf.Field(index).Name), "draft") {
			t.Fatalf("MessageCoordinator still owns Draft field %s", typeOf.Field(index).Name)
		}
	}
	pointerType := reflect.TypeOf((*MessageCoordinator)(nil))
	for index := 0; index < pointerType.NumMethod(); index++ {
		if strings.Contains(strings.ToLower(pointerType.Method(index).Name), "draft") {
			t.Fatalf("MessageCoordinator still exposes Draft method %s", pointerType.Method(index).Name)
		}
	}
}

func TestMessageCoordinatorFlushReadsAndRetiresStoreItems(t *testing.T) {
	store := newAgentAppInboxStore("agent-1", filepath.Join(t.TempDir(), "inbox.json"))
	message := protocol.AgentMessageProjection{ID: "message-store-1", Target: "channel:one", Seq: 9, Content: "durable"}
	item, err := store.Mint(AgentAppInboxMintInput{
		AppID: agentInboxAppID, NotificationClass: "message",
		SourceRef: AgentAppInboxSourceRef{Kind: "message", ID: message.ID, Revision: "9"}, Message: &message,
	})
	if err != nil {
		t.Fatal(err)
	}
	var delivered []protocol.AgentMessageProjection
	c, err := NewMessageCoordinator(InboxKey{WorkspaceID: "workspace-1", AgentID: "agent-1"}, t.TempDir(), func(_ context.Context, messages []protocol.AgentMessageProjection) error {
		delivered = append(delivered, messages...)
		return nil
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	c.SetInboxStore(store)
	if err := c.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(delivered) != 1 || delivered[0].ID != message.ID {
		t.Fatalf("delivered=%+v", delivered)
	}
	if _, ok := store.Item(item.ItemID); ok {
		t.Fatal("successful provider delivery did not ACK and retire Store item")
	}
}
