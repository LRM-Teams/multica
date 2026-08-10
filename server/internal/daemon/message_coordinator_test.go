package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/agentworkspace"
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
	pool := newCanonicalAgentRuntimePool()
	pool.slots["agent-1\x00runtime-1"] = &canonicalAgentRuntimeSlot{
		mode: canonicalRuntimeResident, backend: backend,
	}
	handoffErr := make(chan error, 1)
	go func() {
		handoffErr <- pool.handoffIdleMessages(
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
	go func() { restartDone <- pool.forceInvalidateSession("agent-1", "runtime-1") }()
	select {
	case err := <-restartDone:
		if err != nil {
			t.Fatalf("forceInvalidateSession: %v", err)
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

type pendingNoticeRuntime struct {
	mu      sync.Mutex
	notices []agent.ResidentPendingNotice
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
	pool := newCanonicalAgentRuntimePool()
	pool.slots["agent-1\x00runtime-1"] = &canonicalAgentRuntimeSlot{
		mode: canonicalRuntimeResident, backend: backend,
	}
	messages := []protocol.AgentMessageProjection{{ID: "message-1", Target: "channel:one", Seq: 1, Content: "hello"}}
	if err := pool.handoffIdleMessages(context.Background(), "agent-1", "runtime-1", messages, nil, nil, nil, nil); err != nil {
		t.Fatalf("first handoff: %v", err)
	}
	if err := pool.handoffIdleMessages(context.Background(), "agent-1", "runtime-1", messages, nil, nil, nil, nil); !errors.Is(err, ErrCanonicalAgentRuntimeBusy) {
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

func TestRuntimePoolPublishesAcceptanceBeforeResidentRuntimeActivity(t *testing.T) {
	backend := &activityResidentMessageRuntime{done: make(chan error, 1), messages: make(chan agent.Message, 1)}
	backend.messages <- agent.Message{Type: agent.MessageThinking}
	close(backend.messages)
	pool := newCanonicalAgentRuntimePool()
	pool.slots["agent-1\x00runtime-1"] = &canonicalAgentRuntimeSlot{mode: canonicalRuntimeResident, backend: backend}

	var mu sync.Mutex
	var observed []string
	completionObserved := make(chan struct{})
	err := pool.handoffIdleMessages(
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
		func(error, uint64) {
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
	if !reflect.DeepEqual(observed, []string{"starting", "accepted", "activity", "complete"}) {
		t.Fatalf("callback order = %v, want starting, acceptance, runtime Activity, then completion", observed)
	}
}

func TestRuntimePoolDrainsResidentActivityWithoutObserver(t *testing.T) {
	backend := &activityResidentMessageRuntime{done: make(chan error, 1), messages: make(chan agent.Message)}
	pool := newCanonicalAgentRuntimePool()
	pool.slots["agent-1\x00runtime-1"] = &canonicalAgentRuntimeSlot{mode: canonicalRuntimeResident, backend: backend}

	completed := make(chan struct{})
	if err := pool.handoffIdleMessages(
		context.Background(), "agent-1", "runtime-1",
		[]protocol.AgentMessageProjection{{ID: "message-1", Target: "dm:one", Seq: 1}},
		nil, nil, nil, func(error, uint64) { close(completed) },
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
	pool := newCanonicalAgentRuntimePool()
	pool.slots["agent-1\x00runtime-1"] = &canonicalAgentRuntimeSlot{
		mode: canonicalRuntimeResident, backend: backend,
	}
	messages := []protocol.AgentMessageProjection{{ID: "message-1", Target: "channel:one", Seq: 1}}
	result := make(chan bool, 1)
	if err := pool.handoffIdleMessages(context.Background(), "agent-1", "runtime-1", messages, nil, nil, nil, func(_ error, generation uint64) {
		if err := pool.handoffIdleMessages(context.Background(), "agent-1", "runtime-1", messages, nil, nil, nil, nil); err != nil {
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

// TestResidentMessageTurnCompletionDoesNotAutoHandoffPending locks Raft
// alignment: after a turn ends, Pending must not auto body-handoff into a new
// turn (former FlushOnTurnCompletion). Idle Accept→Flush or explicit Flush /
// message check advances bodies later.
func TestResidentMessageTurnCompletionDoesNotAutoHandoffPending(t *testing.T) {
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
	backend := &sequencedResidentMessageRuntime{accepted: make(chan chan error, 2)}
	d.canonicalRuntimes.slots[agentID+"\x00"+runtimeID] = &canonicalAgentRuntimeSlot{
		mode: canonicalRuntimeResident, backend: backend,
	}
	if _, err := d.ensureIdleMessageCoordinator(agentID, runtimeID, agentworkspace.Root(root, workspaceID, agentID)); err != nil {
		t.Fatalf("ensure coordinator: %v", err)
	}
	d.attachWorkspaceRunnerMessageTransport(workspaceID, func(string, any) error { return nil })
	producer := d.workspaceAgentActivityProducer(workspaceID)
	activities := make(chan protocol.AgentActivityPayload, 8)
	producer.AttachTransport(func(activity protocol.AgentActivityPayload) { activities <- activity })
	d.messageCoordinatorMu.RLock()
	coordinator := d.messageCoordinators[agentID]
	d.messageCoordinatorMu.RUnlock()
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

func TestResidentMessageTurnErrorDoesNotAutoHandoffPending(t *testing.T) {
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
	backend := &sequencedResidentMessageRuntime{accepted: make(chan chan error, 2)}
	d.canonicalRuntimes.slots[agentID+"\x00"+runtimeID] = &canonicalAgentRuntimeSlot{
		mode: canonicalRuntimeResident, backend: backend,
	}
	if _, err := d.ensureIdleMessageCoordinator(agentID, runtimeID, agentworkspace.Root(root, workspaceID, agentID)); err != nil {
		t.Fatalf("ensure coordinator: %v", err)
	}
	d.attachWorkspaceRunnerMessageTransport(workspaceID, func(string, any) error { return nil })
	d.messageCoordinatorMu.RLock()
	coordinator := d.messageCoordinators[agentID]
	d.messageCoordinatorMu.RUnlock()
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
	pool := newCanonicalAgentRuntimePool()
	pool.slots["agent-1\x00runtime-1"] = &canonicalAgentRuntimeSlot{
		mode: canonicalRuntimeResident, backend: backend, running: true,
	}
	first := PendingNoticeSnapshot{
		Notice: agent.ResidentPendingNotice{TotalPending: 2, ChangedTargets: []agent.ResidentPendingTarget{
			{Target: "channel:one", PendingCount: 1},
			{Target: "dm:two", PendingCount: 1},
		}},
		Fingerprint:        "all-v1",
		TargetFingerprints: map[string]string{"channel:one": "one-v1", "dm:two": "two-v1"},
	}
	if err := pool.handoffBusyNotice(context.Background(), "agent-1", "runtime-1", first); err != nil {
		t.Fatalf("first Notice: %v", err)
	}
	if err := pool.handoffBusyNotice(context.Background(), "agent-1", "runtime-1", first); err != nil {
		t.Fatalf("duplicate Notice: %v", err)
	}
	second := PendingNoticeSnapshot{
		Notice: agent.ResidentPendingNotice{TotalPending: 3, ChangedTargets: []agent.ResidentPendingTarget{
			{Target: "channel:one", PendingCount: 2},
			{Target: "dm:two", PendingCount: 1},
		}},
		Fingerprint:        "all-v2",
		TargetFingerprints: map[string]string{"channel:one": "one-v2", "dm:two": "two-v1"},
	}
	if err := pool.handoffBusyNotice(context.Background(), "agent-1", "runtime-1", second); err != nil {
		t.Fatalf("changed Notice: %v", err)
	}
	got := backend.snapshot()
	if len(got) != 2 {
		t.Fatalf("runtime Notices = %+v, want first plus one changed Notice", got)
	}
	if !reflect.DeepEqual(got[0].ChangedTargets, first.Notice.ChangedTargets) {
		t.Fatalf("first changed targets = %+v", got[0].ChangedTargets)
	}
	if want := []agent.ResidentPendingTarget{{Target: "channel:one", PendingCount: 2}}; !reflect.DeepEqual(got[1].ChangedTargets, want) {
		t.Fatalf("second changed targets = %+v, want %+v", got[1].ChangedTargets, want)
	}
}

func TestMessageCoordinatorCoalescesContentFreeBusyNoticeWithoutConsumption(t *testing.T) {
	root := t.TempDir()
	var mu sync.Mutex
	var notices []agent.ResidentPendingNotice
	activityCount := 0
	coordinator, err := NewMessageCoordinator(root, func(context.Context, []protocol.AgentMessageProjection) error {
		return ErrCanonicalAgentRuntimeBusy
	}, func([]protocol.AgentMessageProjection) {
		activityCount++
	})
	if err != nil {
		t.Fatalf("NewMessageCoordinator: %v", err)
	}
	coordinator.ConfigurePendingNotices(func(_ context.Context, snapshot PendingNoticeSnapshot) error {
		mu.Lock()
		defer mu.Unlock()
		notices = append(notices, snapshot.Notice)
		return nil
	}, 25*time.Millisecond, 40*time.Millisecond)
	t.Cleanup(coordinator.Close)
	completeCoordinatorRecovery(t, coordinator)

	deliveries := []protocol.AgentDeliverPayload{
		testDelivery("message-1", "channel:one", 1, "delivery-1"),
		testDelivery("message-2", "channel:one", 2, "delivery-2"),
		testDelivery("message-3", "dm:two", 1, "delivery-3"),
	}
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
		{Target: "channel:one", PendingCount: 2},
		{Target: "dm:two", PendingCount: 1},
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
	if _, err := os.Stat(filepath.Join(root, consumedSeqsFileName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Notice persisted Context Boundary, stat error = %v", err)
	}
}

func TestMessageCoordinatorRetriesFailedBusyNoticeWithoutLosingDebt(t *testing.T) {
	root := t.TempDir()
	var mu sync.Mutex
	attempts := 0
	coordinator, err := NewMessageCoordinator(root, func(context.Context, []protocol.AgentMessageProjection) error {
		return ErrCanonicalAgentRuntimeBusy
	}, func([]protocol.AgentMessageProjection) {
		t.Fatal("Pending Notice must not emit Message received Activity")
	})
	if err != nil {
		t.Fatalf("NewMessageCoordinator: %v", err)
	}
	coordinator.ConfigurePendingNotices(func(_ context.Context, snapshot PendingNoticeSnapshot) error {
		mu.Lock()
		defer mu.Unlock()
		attempts++
		if snapshot.Notice.TotalPending != 1 {
			t.Errorf("Notice total = %d, want 1", snapshot.Notice.TotalPending)
		}
		if attempts == 1 {
			return errors.New("unsafe provider receipt")
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
	if _, err := os.Stat(filepath.Join(root, consumedSeqsFileName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("boundary file after Notice retry: %v", err)
	}
	coordinator.mu.Lock()
	got := coordinator.pendingBatchLocked()
	coordinator.mu.Unlock()
	if len(got) != 1 || got[0].ID != delivery.Message.ID {
		t.Fatalf("Pending after Notice retry = %+v", got)
	}
}

func TestClosedMessageCoordinatorRejectsPendingWork(t *testing.T) {
	handoffs := 0
	coordinator, err := NewMessageCoordinator(t.TempDir(), func(context.Context, []protocol.AgentMessageProjection) error {
		handoffs++
		return nil
	}, nil)
	if err != nil {
		t.Fatalf("NewMessageCoordinator: %v", err)
	}
	completeCoordinatorRecovery(t, coordinator)
	if _, err := coordinator.Accept(context.Background(), testDelivery("message-1", "channel:one", 1, "delivery-1")); err != nil {
		t.Fatalf("Accept: %v", err)
	}
	coordinator.Close()
	if err := coordinator.Flush(context.Background()); err == nil {
		t.Fatal("Flush succeeded after Close")
	}
	if result, err := coordinator.Check(0); err == nil {
		t.Fatalf("Check succeeded after Close: %+v", result)
	}
	if handoffs != 0 {
		t.Fatalf("runtime handoffs after Close = %d", handoffs)
	}
}

func TestMessageCoordinatorCheckReturnsBoundedPendingAndAdvancesOnlyReturnedBoundaries(t *testing.T) {
	root := t.TempDir()
	activityCount := 0
	coordinator, err := NewMessageCoordinator(root, func(context.Context, []protocol.AgentMessageProjection) error {
		t.Fatal("message check must not use idle runtime handoff")
		return nil
	}, func([]protocol.AgentMessageProjection) {
		activityCount++
	})
	if err != nil {
		t.Fatalf("NewMessageCoordinator: %v", err)
	}
	completeCoordinatorRecovery(t, coordinator)
	for _, delivery := range []protocol.AgentDeliverPayload{
		testDelivery("message-1", "channel:one", 1, "delivery-1"),
		testDelivery("message-2", "channel:one", 2, "delivery-2"),
		testDelivery("message-3", "dm:two", 1, "delivery-3"),
	} {
		if _, err := coordinator.Accept(context.Background(), delivery); err != nil {
			t.Fatalf("Accept: %v", err)
		}
	}

	result, err := coordinator.Check(2)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if got := []string{result.Messages[0].ID, result.Messages[1].ID}; !reflect.DeepEqual(got, []string{"message-1", "message-2"}) {
		t.Fatalf("checked Messages = %v", got)
	}
	if !result.HasMore || result.Remaining != 1 || result.Status != messageCheckStatusMore {
		t.Fatalf("first Check result = %+v", result)
	}
	if got := coordinator.Boundaries(); !reflect.DeepEqual(got, map[string]int64{"channel:one": 2}) {
		t.Fatalf("boundaries after bounded Check = %+v", got)
	}
	if activityCount != 0 {
		t.Fatalf("Message received Activity count = %d, want 0", activityCount)
	}

	result, err = coordinator.Check(2)
	if err != nil {
		t.Fatalf("second Check: %v", err)
	}
	if len(result.Messages) != 1 || result.Messages[0].ID != "message-3" || result.HasMore || result.Remaining != 0 || result.Status != messageCheckStatusComplete {
		t.Fatalf("second Check result = %+v", result)
	}
	if got := coordinator.Boundaries(); !reflect.DeepEqual(got, map[string]int64{"channel:one": 2, "dm:two": 1}) {
		t.Fatalf("final boundaries = %+v", got)
	}
}

func TestMessageCoordinatorCheckRetainsPendingWhenBoundaryWriteFails(t *testing.T) {
	root := filepath.Join(t.TempDir(), "agent-root")
	coordinator, err := NewMessageCoordinator(root, func(context.Context, []protocol.AgentMessageProjection) error {
		return nil
	}, nil)
	if err != nil {
		t.Fatalf("NewMessageCoordinator: %v", err)
	}
	completeCoordinatorRecovery(t, coordinator)
	delivery := testDelivery("message-1", "channel:one", 1, "delivery-1")
	if _, err := coordinator.Accept(context.Background(), delivery); err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if err := os.WriteFile(root, []byte("blocks agent root directory"), 0o600); err != nil {
		t.Fatalf("block agent root: %v", err)
	}

	result, err := coordinator.Check(0)
	if err == nil {
		t.Fatalf("Check result = %+v, want boundary failure", result)
	}
	coordinator.mu.Lock()
	pending := coordinator.pendingBatchLocked()
	coordinator.mu.Unlock()
	if len(pending) != 1 || pending[0].ID != delivery.Message.ID {
		t.Fatalf("Pending after boundary failure = %+v", pending)
	}
	if got := coordinator.Boundaries(); len(got) != 0 {
		t.Fatalf("boundaries after failure = %+v", got)
	}

	if err := os.Remove(root); err != nil {
		t.Fatalf("unblock agent root: %v", err)
	}
	result, err = coordinator.Check(0)
	if err != nil {
		t.Fatalf("retry Check: %v", err)
	}
	if len(result.Messages) != 1 || result.Messages[0].ID != delivery.Message.ID {
		t.Fatalf("retry result = %+v", result)
	}
	coordinator.mu.Lock()
	pending = coordinator.pendingBatchLocked()
	coordinator.mu.Unlock()
	if len(pending) != 0 {
		t.Fatalf("Pending after successful retry = %+v", pending)
	}
}

func TestDaemonAgentLifecycleRegistersCoordinatorAtAgentRoot(t *testing.T) {
	const workspaceID = "11111111-1111-4111-8111-111111111111"
	const agentID = "22222222-2222-4222-8222-222222222222"
	const runtimeID = "33333333-3333-4333-8333-333333333333"
	root := t.TempDir()
	daemon := New(Config{WorkspacesRoot: root}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	daemon.runtimeIndex[runtimeID] = Runtime{ID: runtimeID, WorkspaceID: workspaceID}

	if err := daemon.handleDaemonAgentStart(protocol.DaemonAgentStartPayload{
		AgentID: agentID, RuntimeID: runtimeID, WorkspaceID: workspaceID, PlacementGeneration: 1,
	}); err != nil {
		t.Fatalf("handleDaemonAgentStart: %v", err)
	}
	daemon.messageCoordinatorMu.RLock()
	coordinator := daemon.messageCoordinators[agentID]
	registeredRuntimeID := daemon.messageRuntimeIDs[agentID]
	daemon.messageCoordinatorMu.RUnlock()
	if coordinator == nil || registeredRuntimeID != runtimeID {
		t.Fatalf("coordinator=%v runtime_id=%q", coordinator, registeredRuntimeID)
	}
	wantRoot := agentworkspace.Root(daemon.cfg.WorkspacesRoot, workspaceID, agentID)
	if coordinator.root != wantRoot {
		t.Fatalf("coordinator root = %q, want Agent root %q", coordinator.root, wantRoot)
	}
	if _, err := os.Stat(wantRoot); err != nil {
		t.Fatalf("Agent root was not provisioned: %v", err)
	}

	if err := daemon.handleDaemonAgentStop(protocol.DaemonAgentStopPayload{
		AgentID: agentID, RuntimeID: runtimeID, PlacementGeneration: 1,
	}); err != nil {
		t.Fatalf("handleDaemonAgentStop: %v", err)
	}
	daemon.messageCoordinatorMu.RLock()
	coordinator = daemon.messageCoordinators[agentID]
	daemon.messageCoordinatorMu.RUnlock()
	if coordinator != nil {
		t.Fatal("coordinator remained registered after accepted placement stop")
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
	request := coordinator.BeginRecovery("agent-1", 2)
	if err := coordinator.MergeRecoveryPage(protocol.AgentRecoveryPage{AgentID: "agent-1", RecoveryID: request.RecoveryID, SnapshotID: "snapshot-1", HighWatermark: "fence-1"}); err != nil {
		t.Fatalf("complete recovery: %v", err)
	}
	if err := coordinator.Flush(context.Background()); err != nil {
		t.Fatalf("finalize recovery: %v", err)
	}
}

func TestMessageCoordinatorAcceptsBeforeAckWithoutAdvancingBoundary(t *testing.T) {
	root := t.TempDir()
	coordinator, err := NewMessageCoordinator(root, func(context.Context, []protocol.AgentMessageProjection) error { return nil }, nil)
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
	if _, err := os.Stat(filepath.Join(root, consumedSeqsFileName)); !os.IsNotExist(err) {
		t.Fatalf("boundary file after acceptance: %v", err)
	}
}

func TestMessageCoordinatorMarkReadAdvancesOnlyRequestedTarget(t *testing.T) {
	root := t.TempDir()
	var handoffs, activities int
	coordinator, err := NewMessageCoordinator(root, func(context.Context, []protocol.AgentMessageProjection) error {
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
	if err := coordinator.MarkRead("channel:one", 5); err != nil {
		t.Fatalf("MarkRead: %v", err)
	}
	if got, want := coordinator.Boundaries(), map[string]int64{"channel:one": 5}; !reflect.DeepEqual(got, want) {
		t.Fatalf("boundaries = %v, want %v", got, want)
	}
	if _, found := coordinator.pending["channel:one"]; found {
		t.Fatalf("read target pending remains: %+v", coordinator.pending)
	}
	if got := coordinator.pending["channel:two"][8].ID; got != "two-8" {
		t.Fatalf("other target pending = %q, want two-8", got)
	}
	if handoffs != 0 || activities != 0 {
		t.Fatalf("MarkRead caused handoffs=%d activities=%d, want neither", handoffs, activities)
	}
	boundaries, healthy, err := loadConsumedSeqs(filepath.Join(root, consumedSeqsFileName))
	if err != nil || !healthy || !reflect.DeepEqual(boundaries, map[string]int64{"channel:one": 5}) {
		t.Fatalf("durable boundaries = %v healthy=%v err=%v", boundaries, healthy, err)
	}
}

func TestMessageCoordinatorFlushesTargetInSequenceAndRecordsOneActivityBatch(t *testing.T) {
	var handedOff [][]string
	var activities int
	coordinator, err := NewMessageCoordinator(t.TempDir(), func(_ context.Context, messages []protocol.AgentMessageProjection) error {
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

func TestMessageCoordinatorDeduplicatesDeliveryWithoutSecondHandoffOrActivity(t *testing.T) {
	var handoffs, activities int
	coordinator, err := NewMessageCoordinator(t.TempDir(), func(context.Context, []protocol.AgentMessageProjection) error { handoffs++; return nil }, func([]protocol.AgentMessageProjection) { activities++ })
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

func TestDaemonAcknowledgesAfterPendingAcceptanceBeforeIdleHandoff(t *testing.T) {
	root := t.TempDir()
	var handoffs int
	coordinator, err := NewMessageCoordinator(root, func(context.Context, []protocol.AgentMessageProjection) error { handoffs++; return nil }, nil)
	if err != nil {
		t.Fatalf("NewMessageCoordinator: %v", err)
	}
	runtimePool := newCanonicalAgentRuntimePool()
	runtimePool.slots["agent-1\x00runtime-1"] = &canonicalAgentRuntimeSlot{
		mode:    canonicalRuntimeResident,
		backend: &canonicalRuntimeTestBackend{},
	}
	daemon := &Daemon{
		canonicalRuntimes: runtimePool,
		messageRuntimeIDs: map[string]string{"agent-1": "runtime-1"},
	}
	if err := daemon.registerIdleMessageCoordinator("agent-1", coordinator); err != nil {
		t.Fatalf("registerIdleMessageCoordinator: %v", err)
	}
	completeCoordinatorRecovery(t, coordinator)
	delivery := testDelivery("message-1", "channel-1", 1, "delivery-1")
	ack, err := daemon.acceptIdleAgentDelivery(context.Background(), delivery)
	if err != nil {
		t.Fatalf("acceptIdleAgentDelivery: %v", err)
	}
	if handoffs != 0 {
		t.Fatalf("handoffs = %d, want none before acknowledgement", handoffs)
	}
	if got, want := ack, (protocol.AgentDeliverAckPayload{AgentID: "agent-1", Seq: 1, DeliveryID: "delivery-1"}); !reflect.DeepEqual(got, want) {
		t.Fatalf("ack = %+v, want %+v", got, want)
	}
	if got := coordinator.Boundaries()["channel-1"]; got != 0 {
		t.Fatalf("boundary = %d, want 0 before handoff", got)
	}
	if err := daemon.flushIdleAgentDelivery(context.Background(), "agent-1"); err != nil {
		t.Fatalf("flushIdleAgentDelivery: %v", err)
	}
	if handoffs != 1 {
		t.Fatalf("handoffs = %d, want 1 after flush", handoffs)
	}
	if got := coordinator.Boundaries()["channel-1"]; got != 1 {
		t.Fatalf("boundary = %d, want 1", got)
	}
}

func TestCoordinatorReplacementWaitsForInFlightHandoff(t *testing.T) {
	oldRoot := t.TempDir()
	newRoot := t.TempDir()
	handoffStarted := make(chan struct{})
	releaseHandoff := make(chan struct{})
	var startOnce sync.Once
	oldCoordinator, err := NewMessageCoordinator(oldRoot, func(context.Context, []protocol.AgentMessageProjection) error {
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
	daemon := &Daemon{
		messageCoordinators: map[string]*MessageCoordinator{"agent-1": oldCoordinator},
		messageRuntimeIDs:   map[string]string{"agent-1": "runtime-old"},
		canonicalRuntimes:   newCanonicalAgentRuntimePool(),
	}
	flushDone := make(chan error, 1)
	go func() { flushDone <- daemon.flushIdleAgentDelivery(context.Background(), "agent-1") }()
	<-handoffStarted

	replacementDone := make(chan error, 1)
	go func() {
		_, err := daemon.ensureIdleMessageCoordinator("agent-1", "runtime-new", newRoot)
		replacementDone <- err
	}()
	select {
	case err := <-replacementDone:
		t.Fatalf("coordinator replaced during in-flight handoff: %v", err)
	case <-time.After(25 * time.Millisecond):
	}
	close(releaseHandoff)
	if err := <-flushDone; err != nil {
		t.Fatalf("old coordinator flush: %v", err)
	}
	if err := <-replacementDone; err != nil {
		t.Fatalf("replace coordinator: %v", err)
	}
	if got := oldCoordinator.Boundaries()["channel-1"]; got != 1 {
		t.Fatalf("old coordinator boundary = %d, want 1", got)
	}
	daemon.messageCoordinatorMu.RLock()
	replacement := daemon.messageCoordinators["agent-1"]
	runtimeID := daemon.messageRuntimeIDs["agent-1"]
	daemon.messageCoordinatorMu.RUnlock()
	if replacement == nil {
		t.Fatal("replacement coordinator is nil")
	}
	if replacement == oldCoordinator || replacement.root != newRoot || runtimeID != "runtime-new" {
		t.Fatalf("replacement=%p old=%p root=%q runtime=%q", replacement, oldCoordinator, replacement.root, runtimeID)
	}
}

func TestMessageCoordinatorTreatsMalformedBoundaryAsUnknownCoverage(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, consumedSeqsFileName), []byte("not-json"), 0o600); err != nil {
		t.Fatalf("write corrupt boundary: %v", err)
	}
	coordinator, err := NewMessageCoordinator(root, func(context.Context, []protocol.AgentMessageProjection) error { return nil }, nil)
	if err != nil {
		t.Fatalf("NewMessageCoordinator: %v", err)
	}
	if got := coordinator.Boundaries(); len(got) != 0 {
		t.Fatalf("boundaries = %v, want unknown empty coverage", got)
	}
}

func TestMessageCoordinatorDeletedBoundaryReplaysConservatively(t *testing.T) {
	root := t.TempDir()
	// A prior run had durably advanced coverage; the file is then deleted
	// (unstable volume, manual removal, or partial restore). Deletion must be
	// treated as unknown coverage, never as permission to skip context.
	boundaryPath := filepath.Join(root, consumedSeqsFileName)
	if err := os.WriteFile(boundaryPath, []byte(`{"channel-1":5}`), 0o600); err != nil {
		t.Fatalf("write prior boundary: %v", err)
	}
	if err := os.Remove(boundaryPath); err != nil {
		t.Fatalf("delete boundary: %v", err)
	}
	var handedOff []protocol.AgentMessageProjection
	coordinator, err := NewMessageCoordinator(root, func(_ context.Context, messages []protocol.AgentMessageProjection) error {
		handedOff = append(handedOff, messages...)
		return nil
	}, nil)
	if err != nil {
		t.Fatalf("NewMessageCoordinator: %v", err)
	}
	if got := coordinator.Boundaries(); len(got) != 0 {
		t.Fatalf("boundaries after deletion = %v, want unknown empty coverage", got)
	}
	if _, ok := coordinator.ContextBoundary("channel-1"); ok {
		t.Fatal("boundary available before recovery after deletion")
	}
	request := coordinator.BeginRecovery("agent-1", 2)
	message := protocol.AgentMessageProjection{ID: "message-6", Target: "channel-1", Seq: 6, Content: "replayed-after-deletion"}
	if err := coordinator.MergeRecoveryPage(protocol.AgentRecoveryPage{
		AgentID: "agent-1", RecoveryID: request.RecoveryID, SnapshotID: "restart", HighWatermark: "restart", Messages: []protocol.AgentMessageProjection{message},
	}); err != nil {
		t.Fatalf("MergeRecoveryPage: %v", err)
	}
	if err := coordinator.Flush(context.Background()); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if len(handedOff) != 1 || handedOff[0].ID != message.ID || coordinator.Boundaries()[message.Target] != message.Seq {
		t.Fatalf("handoff=%+v boundaries=%v after deletion recovery", handedOff, coordinator.Boundaries())
	}
}

func TestMessageCoordinatorLowerBoundaryConservativelyReplays(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, consumedSeqsFileName), []byte(`{"channel-1":1}`), 0o600); err != nil {
		t.Fatalf("write lower boundary: %v", err)
	}
	var handedOff []protocol.AgentMessageProjection
	coordinator, err := NewMessageCoordinator(root, func(_ context.Context, messages []protocol.AgentMessageProjection) error {
		handedOff = append(handedOff, messages...)
		return nil
	}, nil)
	if err != nil {
		t.Fatalf("NewMessageCoordinator: %v", err)
	}
	request := coordinator.BeginRecovery("agent-1", 2)
	message := protocol.AgentMessageProjection{ID: "message-2", Target: "channel-1", Seq: 2, Content: "replayed"}
	if err := coordinator.MergeRecoveryPage(protocol.AgentRecoveryPage{
		AgentID: "agent-1", RecoveryID: request.RecoveryID, SnapshotID: "lower", HighWatermark: "lower", Messages: []protocol.AgentMessageProjection{message},
	}); err != nil {
		t.Fatalf("MergeRecoveryPage: %v", err)
	}
	if err := coordinator.Flush(context.Background()); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if len(handedOff) != 1 || handedOff[0].ID != message.ID || coordinator.Boundaries()[message.Target] != message.Seq {
		t.Fatalf("handoff=%+v boundaries=%v", handedOff, coordinator.Boundaries())
	}
}

func TestMessageCoordinatorRecoveryMergesLiveAndPagedMessagesBeforeFlush(t *testing.T) {
	var got []string
	coordinator, err := NewMessageCoordinator(t.TempDir(), func(_ context.Context, messages []protocol.AgentMessageProjection) error {
		for _, message := range messages {
			got = append(got, message.ID)
		}
		return nil
	}, nil)
	if err != nil {
		t.Fatalf("NewMessageCoordinator: %v", err)
	}
	request := coordinator.BeginRecovery("agent-1", 1)
	if request.Limit != 1 || request.AgentID != "agent-1" {
		t.Fatalf("request = %+v", request)
	}
	if _, err := coordinator.Accept(context.Background(), testDelivery("message-2", "channel-1", 2, "live-2")); err != nil {
		t.Fatalf("live Accept: %v", err)
	}
	if err := coordinator.MergeRecoveryPage(protocol.AgentRecoveryPage{AgentID: "agent-1", RecoveryID: request.RecoveryID, SnapshotID: "snap", HighWatermark: "42", Messages: []protocol.AgentMessageProjection{{ID: "message-1", Target: "channel-1", Seq: 1, Content: "one"}}, NextCursor: "cursor-1", HasMore: true}); err != nil {
		t.Fatalf("page 1: %v", err)
	}
	if coordinator.FreshnessKnown() {
		t.Fatal("freshness became known before terminal page")
	}
	if err := coordinator.Flush(context.Background()); err == nil {
		t.Fatal("Flush succeeded before recovery fence completed")
	}
	if err := coordinator.MergeRecoveryPage(protocol.AgentRecoveryPage{AgentID: "agent-1", RecoveryID: request.RecoveryID, SnapshotID: "snap", HighWatermark: "42", Messages: []protocol.AgentMessageProjection{{ID: "message-2", Target: "channel-1", Seq: 2, Content: "two"}}}); err != nil {
		t.Fatalf("page 2: %v", err)
	}
	if coordinator.FreshnessKnown() {
		t.Fatal("freshness became known before terminal recovery handoff")
	}
	if err := coordinator.Flush(context.Background()); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if !coordinator.FreshnessKnown() {
		t.Fatal("freshness remains unknown after terminal recovery handoff")
	}
	if want := []string{"message-1", "message-2"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("handoff = %v, want %v", got, want)
	}
}

func TestMessageCoordinatorRejectsDelayedPageFromPreviousReconnect(t *testing.T) {
	coordinator, err := NewMessageCoordinator(t.TempDir(), func(context.Context, []protocol.AgentMessageProjection) error { return nil }, nil)
	if err != nil {
		t.Fatalf("NewMessageCoordinator: %v", err)
	}
	stale := coordinator.BeginRecovery("agent-1", 2)
	current := coordinator.BeginRecovery("agent-1", 2)
	if stale.RecoveryID == current.RecoveryID {
		t.Fatal("two recovery attempts reused recovery_id")
	}
	if err := coordinator.MergeRecoveryPage(protocol.AgentRecoveryPage{
		AgentID: "agent-1", RecoveryID: stale.RecoveryID, SnapshotID: "stale", HighWatermark: "stale",
	}); err == nil {
		t.Fatal("delayed page from the previous reconnect completed the current recovery")
	}
	if coordinator.FreshnessKnown() {
		t.Fatal("freshness became known after stale recovery page")
	}
	if err := coordinator.MergeRecoveryPage(protocol.AgentRecoveryPage{
		AgentID:       current.AgentID,
		RecoveryID:    current.RecoveryID,
		SnapshotID:    "current-snapshot",
		HighWatermark: "current-snapshot",
	}); err != nil {
		t.Fatalf("current recovery page was poisoned by stale page: %v", err)
	}
	if coordinator.FreshnessKnown() {
		t.Fatal("current recovery became fresh before terminal handoff")
	}
	if err := coordinator.Flush(context.Background()); err != nil {
		t.Fatalf("finalize current recovery: %v", err)
	}
	if !coordinator.FreshnessKnown() {
		t.Fatal("current recovery did not complete after terminal handoff")
	}
}

func TestMessageCoordinatorRestartRecoversAcceptedMessageBeforeHandoff(t *testing.T) {
	root := t.TempDir()
	first, err := NewMessageCoordinator(root, func(context.Context, []protocol.AgentMessageProjection) error {
		t.Fatal("first coordinator handed off before simulated crash")
		return nil
	}, nil)
	if err != nil {
		t.Fatalf("first NewMessageCoordinator: %v", err)
	}
	completeCoordinatorRecovery(t, first)
	delivery := testDelivery("message-1", "channel-1", 1, "delivery-1")
	if accepted, err := first.Accept(context.Background(), delivery); err != nil || !accepted {
		t.Fatalf("Accept before crash = %v, %v", accepted, err)
	}
	if _, err := os.Stat(filepath.Join(root, consumedSeqsFileName)); !os.IsNotExist(err) {
		t.Fatalf("acceptance persisted receive state before handoff: %v", err)
	}

	var recovered []protocol.AgentMessageProjection
	restarted, err := NewMessageCoordinator(root, func(_ context.Context, messages []protocol.AgentMessageProjection) error {
		recovered = append(recovered, messages...)
		return nil
	}, nil)
	if err != nil {
		t.Fatalf("restarted NewMessageCoordinator: %v", err)
	}
	request := restarted.BeginRecovery("agent-1", 2)
	if err := restarted.MergeRecoveryPage(protocol.AgentRecoveryPage{
		AgentID: "agent-1", RecoveryID: request.RecoveryID, SnapshotID: "restart", HighWatermark: "restart",
		Messages: []protocol.AgentMessageProjection{delivery.Message},
	}); err != nil {
		t.Fatalf("merge restart recovery: %v", err)
	}
	if err := restarted.Flush(context.Background()); err != nil {
		t.Fatalf("flush restart recovery: %v", err)
	}
	if len(recovered) != 1 || recovered[0].ID != delivery.Message.ID || restarted.Boundaries()[delivery.Target] != delivery.Seq {
		t.Fatalf("recovered=%+v boundaries=%v", recovered, restarted.Boundaries())
	}
}

func TestMessageCoordinatorUpgradeRestartRecoversBusyPendingMessage(t *testing.T) {
	root := t.TempDir()
	firstDelivery := testDelivery("message-1", "channel-1", 1, "delivery-1")
	pendingDelivery := testDelivery("message-2", "channel-1", 2, "delivery-2")
	handoffCalls := 0
	first, err := NewMessageCoordinator(root, func(context.Context, []protocol.AgentMessageProjection) error {
		handoffCalls++
		if handoffCalls == 1 {
			return nil
		}
		return ErrCanonicalAgentRuntimeBusy
	}, nil)
	if err != nil {
		t.Fatalf("first NewMessageCoordinator: %v", err)
	}
	completeCoordinatorRecovery(t, first)
	if _, err := first.Accept(context.Background(), firstDelivery); err != nil {
		t.Fatalf("accept first: %v", err)
	}
	if err := first.Flush(context.Background()); err != nil {
		t.Fatalf("flush first: %v", err)
	}
	if _, err := first.Accept(context.Background(), pendingDelivery); err != nil {
		t.Fatalf("accept pending: %v", err)
	}
	if err := first.Flush(context.Background()); !errors.Is(err, ErrCanonicalAgentRuntimeBusy) {
		t.Fatalf("busy flush = %v", err)
	}
	if got := first.Boundaries()[firstDelivery.Target]; got != firstDelivery.Seq {
		t.Fatalf("pre-upgrade boundary = %d, want %d", got, firstDelivery.Seq)
	}
	first.Close()

	var recovered []protocol.AgentMessageProjection
	restarted, err := NewMessageCoordinator(root, func(_ context.Context, messages []protocol.AgentMessageProjection) error {
		recovered = append(recovered, messages...)
		return nil
	}, nil)
	if err != nil {
		t.Fatalf("restarted NewMessageCoordinator: %v", err)
	}
	request := restarted.BeginRecovery("agent-1", 10)
	if err := restarted.MergeRecoveryPage(protocol.AgentRecoveryPage{
		AgentID: "agent-1", RecoveryID: request.RecoveryID, SnapshotID: "upgrade", HighWatermark: "upgrade",
		Messages: []protocol.AgentMessageProjection{firstDelivery.Message, pendingDelivery.Message},
	}); err != nil {
		t.Fatalf("merge upgrade recovery: %v", err)
	}
	if err := restarted.Flush(context.Background()); err != nil {
		t.Fatalf("flush upgrade recovery: %v", err)
	}
	if len(recovered) != 1 || recovered[0].ID != pendingDelivery.Message.ID {
		t.Fatalf("recovered = %+v, want only busy pending Message", recovered)
	}
	if got := restarted.Boundaries()[pendingDelivery.Target]; got != pendingDelivery.Seq {
		t.Fatalf("post-upgrade boundary = %d, want %d", got, pendingDelivery.Seq)
	}
}

func TestMessageCoordinatorCredentialBoundaryFailsClosedAfterRecoveryFailure(t *testing.T) {
	coordinator, err := NewMessageCoordinator(t.TempDir(), func(context.Context, []protocol.AgentMessageProjection) error { return nil }, nil)
	if err != nil {
		t.Fatalf("NewMessageCoordinator: %v", err)
	}
	request := coordinator.BeginRecovery("agent-1", 2)
	if _, ok := coordinator.ContextBoundary("channel-1"); ok {
		t.Fatal("boundary available during recovery")
	}
	if err := coordinator.MergeRecoveryPage(protocol.AgentRecoveryPage{AgentID: "agent-1", RecoveryID: request.RecoveryID, SnapshotID: "snap", HighWatermark: "one", NextCursor: "cursor", HasMore: true}); err != nil {
		t.Fatalf("page 1: %v", err)
	}
	if err := coordinator.MergeRecoveryPage(protocol.AgentRecoveryPage{AgentID: "agent-1", RecoveryID: request.RecoveryID, SnapshotID: "other", HighWatermark: "two"}); err == nil {
		t.Fatal("changed fence accepted")
	}
	if _, ok := coordinator.ContextBoundary("channel-1"); ok {
		t.Fatal("boundary available after recovery failure")
	}
}

func TestMessageCoordinatorRetriesBoundaryWithoutDuplicateHandoffOrActivity(t *testing.T) {
	root := t.TempDir()
	var handoffs, activities int
	coordinator, err := NewMessageCoordinator(root, func(context.Context, []protocol.AgentMessageProjection) error {
		handoffs++
		return nil
	}, func([]protocol.AgentMessageProjection) { activities++ })
	if err != nil {
		t.Fatalf("NewMessageCoordinator: %v", err)
	}
	completeCoordinatorRecovery(t, coordinator)
	if _, err := coordinator.Accept(context.Background(), testDelivery("message-1", "channel-1", 1, "delivery-1")); err != nil {
		t.Fatalf("Accept: %v", err)
	}
	boundaryPath := filepath.Join(root, consumedSeqsFileName)
	if err := os.Mkdir(boundaryPath, 0o700); err != nil {
		t.Fatalf("make boundary path unwritable: %v", err)
	}
	if err := coordinator.Flush(context.Background()); err == nil {
		t.Fatal("Flush succeeded with a directory at the boundary path")
	}
	if _, ok := coordinator.ContextBoundary("channel-1"); ok {
		t.Fatal("Credential Proxy boundary stayed available after persistence failure")
	}
	if err := os.Remove(boundaryPath); err != nil {
		t.Fatalf("remove boundary obstruction: %v", err)
	}
	if err := coordinator.Flush(context.Background()); err != nil {
		t.Fatalf("retry Flush: %v", err)
	}
	if handoffs != 1 || activities != 1 {
		t.Fatalf("handoffs=%d activities=%d, want one each", handoffs, activities)
	}
}

func TestMessageCoordinatorRetriesRuntimeHandoffSafely(t *testing.T) {
	var attempts, activities int
	coordinator, err := NewMessageCoordinator(t.TempDir(), func(context.Context, []protocol.AgentMessageProjection) error {
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

func TestMessageCoordinatorPreflightHoldsCompletePendingRangeWithNewestThree(t *testing.T) {
	root := t.TempDir()
	coordinator, err := NewMessageCoordinator(root, func(context.Context, []protocol.AgentMessageProjection) error {
		return nil
	}, nil)
	if err != nil {
		t.Fatalf("NewMessageCoordinator: %v", err)
	}
	completeCoordinatorRecovery(t, coordinator)
	for sequence := int64(1); sequence <= 5; sequence++ {
		if _, err := coordinator.Accept(context.Background(), testDelivery(fmt.Sprintf("message-%d", sequence), "channel:one", sequence, fmt.Sprintf("delivery-%d", sequence))); err != nil {
			t.Fatalf("Accept %d: %v", sequence, err)
		}
	}

	result, err := coordinator.PreflightMessageSend("channel:one")
	if err != nil {
		t.Fatalf("PreflightMessageSend: %v", err)
	}
	if !result.Held || result.NewMessageCount != 5 || result.Omitted != 2 || result.LatestSeq != 5 {
		t.Fatalf("preflight result = %+v", result)
	}
	if got, want := []string{result.Messages[0].ID, result.Messages[1].ID, result.Messages[2].ID}, []string{"message-3", "message-4", "message-5"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("shown messages = %v, want %v", got, want)
	}
	if _, found := coordinator.pending["channel:one"]; found {
		t.Fatalf("held Pending remains: %+v", coordinator.pending)
	}
	if got, ok := coordinator.ContextBoundary("channel:one"); !ok || got != 5 {
		t.Fatalf("boundary = %d, known=%v; want 5, true", got, ok)
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
