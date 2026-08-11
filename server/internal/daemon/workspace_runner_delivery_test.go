package daemon

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestWorkspaceRunnerDeliveryAttemptsAckBeforeRuntimeHandoff(t *testing.T) {
	var order []string
	d := New(Config{}, nil)
	coordinator, err := newTestMessageCoordinator(t, t.TempDir(), func(context.Context, []protocol.AgentMessageProjection) error {
		order = append(order, "handoff")
		return nil
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	completeCoordinatorRecovery(t, coordinator)
	d.mu.Lock()
	d.runtimeIndex["runtime-1"] = Runtime{ID: "runtime-1", WorkspaceID: "workspace-1"}
	d.mu.Unlock()
	registerTestInbox(t, d, InboxKey{WorkspaceID: "workspace-1", AgentID: "agent-1"}, "runtime-1", coordinator)
	d.canonicalRuntimes.slots["agent-1\x00runtime-1"] = &canonicalAgentRuntimeSlot{
		mode:    canonicalRuntimeResident,
		backend: &idleMessageFakeRuntime{},
	}
	delivery := protocol.AgentDeliverPayload{
		AgentID: "agent-1", Target: "channel:one", Seq: 1, DeliveryID: "delivery-1",
		Message: protocol.AgentMessageProjection{ID: "message-1", Target: "channel:one", Seq: 1},
	}

	err = d.handleWorkspaceRunnerDelivery(context.Background(), "workspace-1", delivery, func(eventType string, payload any) error {
		if eventType != protocol.EventAgentDeliverAck {
			t.Fatalf("frame type = %q, want %q", eventType, protocol.EventAgentDeliverAck)
		}
		order = append(order, "ack")
		return nil
	})
	if err != nil {
		t.Fatalf("handle delivery: %v", err)
	}
	if want := []string{"ack", "handoff"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("delivery order = %v, want %v", order, want)
	}
}

func TestWorkspaceRunnerDeliveryWriterFailureRetainsAcceptedPending(t *testing.T) {
	var handoffs int
	d := New(Config{}, nil)
	coordinator, err := newTestMessageCoordinator(t, t.TempDir(), func(context.Context, []protocol.AgentMessageProjection) error {
		handoffs++
		return nil
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	d.mu.Lock()
	d.runtimeIndex["runtime-1"] = Runtime{ID: "runtime-1", WorkspaceID: "workspace-1"}
	d.mu.Unlock()
	registerTestInbox(t, d, InboxKey{WorkspaceID: "workspace-1", AgentID: "agent-1"}, "runtime-1", coordinator)
	delivery := protocol.AgentDeliverPayload{
		AgentID: "agent-1", Target: "channel:one", Seq: 1, DeliveryID: "delivery-1",
		Message: protocol.AgentMessageProjection{ID: "message-1", Target: "channel:one", Seq: 1},
	}
	writeErr := errors.New("injected ACK writer failure")

	err = d.handleWorkspaceRunnerDelivery(context.Background(), "workspace-1", delivery, func(string, any) error {
		return writeErr
	})
	if !errors.Is(err, writeErr) {
		t.Fatalf("handle delivery error = %v, want %v", err, writeErr)
	}
	if handoffs != 0 {
		t.Fatalf("runtime handoffs after ACK writer failure = %d, want 0", handoffs)
	}
	coordinator.mu.Lock()
	pending := coordinator.pendingBatchLocked()
	coordinator.mu.Unlock()
	if len(pending) != 1 || pending[0].ID != "message-1" {
		t.Fatalf("Pending after ACK writer failure = %+v, want exactly message-1", pending)
	}
}

func TestWorkspaceRunnerDeliveryAcknowledgesBusyRuntime(t *testing.T) {
	d := New(Config{}, nil)
	coordinator, err := newTestMessageCoordinator(t, t.TempDir(), func(context.Context, []protocol.AgentMessageProjection) error {
		return ErrCanonicalAgentRuntimeBusy
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	completeCoordinatorRecovery(t, coordinator)
	d.mu.Lock()
	d.runtimeIndex["runtime-1"] = Runtime{ID: "runtime-1", WorkspaceID: "workspace-1"}
	d.mu.Unlock()
	registerTestInbox(t, d, InboxKey{WorkspaceID: "workspace-1", AgentID: "agent-1"}, "runtime-1", coordinator)
	d.canonicalRuntimes.slots["agent-1\x00runtime-1"] = &canonicalAgentRuntimeSlot{
		mode:    canonicalRuntimeResident,
		backend: &idleMessageFakeRuntime{},
	}
	delivery := protocol.AgentDeliverPayload{
		AgentID: "agent-1", Target: "channel:one", Seq: 1, DeliveryID: "delivery-1",
		Message: protocol.AgentMessageProjection{ID: "message-1", Target: "channel:one", Seq: 1},
	}
	var acknowledgements int

	err = d.handleWorkspaceRunnerDelivery(context.Background(), "workspace-1", delivery, func(eventType string, _ any) error {
		if eventType == protocol.EventAgentDeliverAck {
			acknowledgements++
		}
		return nil
	})
	if err != nil {
		t.Fatalf("handle delivery: %v", err)
	}
	if acknowledgements != 1 {
		t.Fatalf("acknowledgements = %d, want 1", acknowledgements)
	}
	coordinator.mu.Lock()
	pending := coordinator.pendingBatchLocked()
	coordinator.mu.Unlock()
	if len(pending) != 1 || pending[0].ID != "message-1" {
		t.Fatalf("Pending after busy Runtime = %+v, want exactly message-1", pending)
	}
}

func TestWorkspaceRunnerDeliveryDispatcherDoesNotBlockAnotherAgent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	agentAStarted := make(chan struct{})
	releaseAgentA := make(chan struct{})
	agentBHandled := make(chan struct{})
	dispatcher := newWorkspaceRunnerDeliveryDispatcher(ctx, func(_ context.Context, delivery protocol.AgentDeliverPayload) {
		switch delivery.AgentID {
		case "agent-a":
			close(agentAStarted)
			<-releaseAgentA
		case "agent-b":
			close(agentBHandled)
		}
	})

	dispatcher.Enqueue(protocol.AgentDeliverPayload{AgentID: "agent-a", DeliveryID: "delivery-a"})
	<-agentAStarted
	dispatcher.Enqueue(protocol.AgentDeliverPayload{AgentID: "agent-b", DeliveryID: "delivery-b"})
	select {
	case <-agentBHandled:
	case <-time.After(time.Second):
		t.Fatal("agent-b was blocked behind agent-a")
	}
	close(releaseAgentA)
}

func TestWorkspaceRunnerDeliveryDispatcherPreservesPerAgentOrder(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var mu sync.Mutex
	var handled []string
	done := make(chan struct{})
	dispatcher := newWorkspaceRunnerDeliveryDispatcher(ctx, func(_ context.Context, delivery protocol.AgentDeliverPayload) {
		mu.Lock()
		handled = append(handled, delivery.DeliveryID)
		if len(handled) == 3 {
			close(done)
		}
		mu.Unlock()
	})
	for _, deliveryID := range []string{"delivery-1", "delivery-2", "delivery-3"} {
		dispatcher.Enqueue(protocol.AgentDeliverPayload{AgentID: "agent-a", DeliveryID: deliveryID})
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for deliveries")
	}
	mu.Lock()
	defer mu.Unlock()
	if want := []string{"delivery-1", "delivery-2", "delivery-3"}; !reflect.DeepEqual(handled, want) {
		t.Fatalf("handled deliveries = %v, want %v", handled, want)
	}
}

func TestDeliveryRepairsCoordinatorFromDurableResidency(t *testing.T) {
	const (
		workspaceID = "11111111-1111-4111-8111-111111111111"
		agentID     = "22222222-2222-4222-8222-222222222222"
		runtimeID   = "33333333-3333-4333-8333-333333333333"
	)
	root := t.TempDir()
	d := New(Config{WorkspacesRoot: root}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	d.mu.Lock()
	d.runtimeIndex[runtimeID] = Runtime{ID: runtimeID, WorkspaceID: workspaceID}
	d.mu.Unlock()
	if _, err := d.agentAttachments.Apply(workspaceID, AgentAttachmentEvent{
		Kind: AgentAttachmentEventAttach, AgentID: agentID, RuntimeID: runtimeID,
		AttachmentGeneration: 1, LifecycleSeq: 1,
	}); err != nil {
		t.Fatalf("persist Attachment: %v", err)
	}

	var recovery protocol.AgentRecoveryRequest
	attachTestWorkspaceRunner(t, d, workspaceID, func(eventType string, payload any) error {
		if eventType == protocol.EventAgentRecoveryRequest {
			recovery = payload.(protocol.AgentRecoveryRequest)
		}
		return nil
	})
	if err := d.ensureIdleMessageCoordinatorForDelivery(workspaceID, agentID); err != nil {
		t.Fatalf("repair coordinator: %v", err)
	}
	coordinator, gotRuntimeID := resolveTestInbox(t, d, InboxKey{WorkspaceID: workspaceID, AgentID: agentID})
	if coordinator == nil || gotRuntimeID != runtimeID {
		t.Fatalf("coordinator=%v runtime_id=%q", coordinator, gotRuntimeID)
	}
	if recovery.AgentID != agentID || recovery.RecoveryID == "" {
		t.Fatalf("recovery request = %+v", recovery)
	}
}

func TestDeliveryDoesNotRepairCoordinatorAcrossWorkspace(t *testing.T) {
	const (
		attachedWorkspaceID = "11111111-1111-4111-8111-111111111111"
		runnerWorkspaceID   = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
		agentID             = "22222222-2222-4222-8222-222222222222"
		runtimeID           = "33333333-3333-4333-8333-333333333333"
	)
	d := New(Config{WorkspacesRoot: t.TempDir()}, nil)
	d.mu.Lock()
	d.runtimeIndex[runtimeID] = Runtime{ID: runtimeID, WorkspaceID: attachedWorkspaceID}
	d.mu.Unlock()
	if _, err := d.agentAttachments.Apply(attachedWorkspaceID, AgentAttachmentEvent{
		Kind: AgentAttachmentEventAttach, AgentID: agentID, RuntimeID: runtimeID,
		AttachmentGeneration: 1, LifecycleSeq: 1,
	}); err != nil {
		t.Fatal(err)
	}
	delivery := protocol.AgentDeliverPayload{
		AgentID: agentID, Target: "channel:one", Seq: 1, DeliveryID: "delivery-1",
		Message: protocol.AgentMessageProjection{ID: "message-1", Target: "channel:one", Seq: 1},
	}
	var acknowledgements int
	if err := d.handleWorkspaceRunnerDelivery(context.Background(), runnerWorkspaceID, delivery, func(eventType string, _ any) error {
		if eventType == protocol.EventAgentDeliverAck {
			acknowledgements++
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if acknowledgements != 0 {
		t.Fatalf("cross-Workspace acknowledgements = %d, want 0", acknowledgements)
	}
	if runner := d.currentWorkspaceRunner(runnerWorkspaceID); runner != nil {
		if _, _, ok := runner.inboxes.Resolve(agentID); ok {
			t.Fatal("cross-Workspace Delivery recreated an Inbox coordinator")
		}
	}
}

func TestDeliveryDoesNotRepairCoordinatorForDetachedAgent(t *testing.T) {
	const (
		workspaceID = "11111111-1111-4111-8111-111111111111"
		agentID     = "22222222-2222-4222-8222-222222222222"
		runtimeID   = "33333333-3333-4333-8333-333333333333"
	)
	d := New(Config{WorkspacesRoot: t.TempDir()}, nil)
	d.mu.Lock()
	d.runtimeIndex[runtimeID] = Runtime{ID: runtimeID, WorkspaceID: workspaceID}
	d.mu.Unlock()
	for sequence, kind := range []AgentAttachmentEventKind{AgentAttachmentEventAttach, AgentAttachmentEventDetach} {
		if _, err := d.agentAttachments.Apply(workspaceID, AgentAttachmentEvent{
			Kind: kind, AgentID: agentID, RuntimeID: runtimeID,
			AttachmentGeneration: 1, LifecycleSeq: AttachmentLifecycleSequence(sequence + 1),
		}); err != nil {
			t.Fatal(err)
		}
	}
	delivery := protocol.AgentDeliverPayload{
		AgentID: agentID, Target: "channel:one", Seq: 1, DeliveryID: "delivery-1",
		Message: protocol.AgentMessageProjection{ID: "message-1", Target: "channel:one", Seq: 1},
	}
	var acknowledgements int
	if err := d.handleWorkspaceRunnerDelivery(context.Background(), workspaceID, delivery, func(eventType string, _ any) error {
		if eventType == protocol.EventAgentDeliverAck {
			acknowledgements++
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if acknowledgements != 0 {
		t.Fatalf("detached Agent acknowledgements = %d, want 0", acknowledgements)
	}
	if runner := d.currentWorkspaceRunner(workspaceID); runner != nil {
		if _, _, ok := runner.inboxes.Resolve(agentID); ok {
			t.Fatal("detached Agent Delivery recreated an Inbox coordinator")
		}
	}
}

func TestWorkspaceRunnerWriterFencesReplacedConnection(t *testing.T) {
	d := New(Config{}, nil)
	d.mu.Lock()
	d.runtimeIndex["runtime-1"] = Runtime{ID: "runtime-1", WorkspaceID: "workspace-1"}
	d.mu.Unlock()

	var first, second int
	runner, firstConnection := attachTestWorkspaceRunner(t, d, "workspace-1", func(string, any) error {
		first++
		return nil
	})
	coordinator, err := newTestMessageCoordinator(t, t.TempDir(), func(context.Context, []protocol.AgentMessageProjection) error { return nil }, nil)
	if err != nil {
		t.Fatal(err)
	}
	registerTestInbox(t, d, InboxKey{WorkspaceID: "workspace-1", AgentID: "agent-1"}, "runtime-1", coordinator)
	staleSend := func() error {
		return runner.sendOnConnection(firstConnection, "agent:recovery:request", map[string]any{"request": 0})
	}
	secondCtx, secondCancel := context.WithCancel(context.Background())
	secondConnection := &workspaceRunnerConnection{workspaceID: "workspace-1", ctx: secondCtx, cancel: secondCancel, write: func(string, any) error {
		second++
		return nil
	}, close: func() {}}
	runner.replaceConnection(secondConnection)
	defer runner.releaseConnection(secondConnection)
	if err := staleSend(); err == nil {
		t.Fatal("callback from replaced connection remained writable")
	}
	if !d.sendAgentMessageRunnerFrame("agent-1", "agent:recovery:request", map[string]any{"request": 1}) {
		t.Fatal("current Runner connection did not receive Message frame")
	}
	if first != 0 || second != 1 {
		t.Fatalf("Message frame delivery first=%d second=%d, want 0/1", first, second)
	}
	d.detachWorkspaceRunner(runner)
	if d.sendAgentMessageRunnerFrame("agent-1", "agent:recovery:request", nil) {
		t.Fatal("detached Runner accepted Message frame")
	}
}

func TestAgentMessageRecoveryUsesCurrentWorkspaceRunnerConnection(t *testing.T) {
	d := New(Config{}, nil)
	coordinator, err := newTestMessageCoordinator(t, t.TempDir(), func(context.Context, []protocol.AgentMessageProjection) error { return nil }, nil)
	if err != nil {
		t.Fatal(err)
	}
	d.mu.Lock()
	d.runtimeIndex["runtime-1"] = Runtime{ID: "runtime-1", WorkspaceID: "workspace-1"}
	d.mu.Unlock()

	var eventType string
	var request protocol.AgentRecoveryRequest
	attachTestWorkspaceRunner(t, d, "workspace-1", func(gotType string, payload any) error {
		eventType = gotType
		request = payload.(protocol.AgentRecoveryRequest)
		return nil
	})
	registerTestInbox(t, d, InboxKey{WorkspaceID: "workspace-1", AgentID: "agent-1"}, "runtime-1", coordinator)

	d.beginAgentMessageRecovery("workspace-1", "agent-1")
	if eventType != protocol.EventAgentRecoveryRequest || request.AgentID != "agent-1" || request.RecoveryID == "" {
		t.Fatalf("recovery frame type=%q request=%+v", eventType, request)
	}
}

func TestLifecycleReplayDoesNotRestartExistingMessageRecovery(t *testing.T) {
	root := t.TempDir()
	d := New(Config{WorkspacesRoot: root}, nil)
	d.agentAttachments = newLocalAgentAttachmentRegistry(root, nil)
	d.mu.Lock()
	d.runtimeIndex["runtime-1"] = Runtime{ID: "runtime-1", WorkspaceID: "workspace-1"}
	d.mu.Unlock()

	requests := 0
	statuses := 0
	sessions := 0
	attachTestWorkspaceRunner(t, d, "workspace-1", func(eventType string, _ any) error {
		switch eventType {
		case protocol.EventAgentRecoveryRequest:
			requests++
		case protocol.EventAgentStatus:
			statuses++
		case protocol.EventAgentSession:
			sessions++
		}
		return nil
	})
	payload := protocol.DaemonAgentStartPayload{
		AgentID: "agent-1", RuntimeID: "runtime-1", WorkspaceID: "workspace-1",
		PlacementGeneration: 1, LifecycleSeq: 1, Replay: true,
	}
	if err := d.handleDaemonAgentStartFrame(payload); err != nil {
		t.Fatal(err)
	}
	if err := d.handleDaemonAgentStartFrame(payload); err != nil {
		t.Fatal(err)
	}
	if requests != 1 {
		t.Fatalf("Message recovery requests=%d, want one for the newly created coordinator", requests)
	}
	if statuses != 1 || sessions != 1 {
		t.Fatalf("Workspace Runner lifecycle frames status=%d session=%d, want one stable managed launch", statuses, sessions)
	}
}

func TestRestoreResidentAgentsRebuildsRunnerPresenceAndMessageRecovery(t *testing.T) {
	const (
		workspaceID = "11111111-1111-4111-8111-111111111111"
		agentID     = "22222222-2222-4222-8222-222222222222"
		runtimeID   = "33333333-3333-4333-8333-333333333333"
	)
	root := t.TempDir()
	persisting := New(Config{WorkspacesRoot: root}, nil)
	if _, accepted, err := persisting.reminderAgents.applyStart(agentID, runtimeID, workspaceID, 1); err != nil || !accepted {
		t.Fatalf("persist residency accepted=%v err=%v", accepted, err)
	}

	restarted := New(Config{WorkspacesRoot: root}, nil)
	restarted.mu.Lock()
	restarted.runtimeIndex[runtimeID] = Runtime{ID: runtimeID, WorkspaceID: workspaceID}
	restarted.mu.Unlock()
	if err := restarted.restoreResidentAgents(); err != nil {
		t.Fatalf("restore residents: %v", err)
	}

	restarted.messageCoordinatorMu.RLock()
	coordinator := restarted.messageCoordinators[agentID]
	gotRuntimeID := restarted.messageRuntimeIDs[agentID]
	restarted.messageCoordinatorMu.RUnlock()
	if coordinator == nil || gotRuntimeID != runtimeID {
		t.Fatalf("restored coordinator=%v runtime_id=%q", coordinator, gotRuntimeID)
	}
	producer := restarted.workspaceAgentActivityProducer(workspaceID)
	_, frames := producer.AttachTransport(func(protocol.AgentActivityPayload) {})
	if len(frames) != 2 || frames[0].EventType != protocol.EventAgentStatus || frames[1].EventType != protocol.EventAgentSession {
		t.Fatalf("restored Runner lifecycle frames = %#v, want status then session", frames)
	}
	var recovery protocol.AgentRecoveryRequest
	restarted.beginMessageRecoveryWithSend(func(request protocol.AgentRecoveryRequest) error {
		recovery = request
		return nil
	})
	if recovery.AgentID != agentID || recovery.RecoveryID == "" {
		t.Fatalf("restored Message recovery request = %+v", recovery)
	}
}
