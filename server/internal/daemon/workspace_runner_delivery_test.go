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

func TestWorkspaceRunnerIdleDeliveryAcknowledgesAfterRuntimeAcceptance(t *testing.T) {
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
	runner := registerTestInbox(t, d, InboxKey{WorkspaceID: "workspace-1", AgentID: "agent-1"}, "runtime-1", coordinator)
	d.canonicalRuntimes.slots["agent-1\x00runtime-1"] = &canonicalAgentRuntimeSlot{
		mode:    canonicalRuntimeResident,
		backend: &idleMessageFakeRuntime{},
	}
	delivery := protocol.AgentDeliverPayload{
		AgentID: "agent-1", Target: "channel:one", Seq: 1, DeliveryID: "delivery-1",
		Message: protocol.AgentMessageProjection{ID: "message-1", Target: "channel:one", Seq: 1},
	}

	err = runner.handleMessageDelivery(context.Background(), delivery, func(eventType string, payload any) error {
		if eventType != protocol.EventAgentDeliverAck {
			t.Fatalf("frame type = %q, want %q", eventType, protocol.EventAgentDeliverAck)
		}
		order = append(order, "ack")
		return nil
	})
	if err != nil {
		t.Fatalf("handle delivery: %v", err)
	}
	if want := []string{"handoff", "ack"}; !reflect.DeepEqual(order, want) {
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
	runner := registerTestInbox(t, d, InboxKey{WorkspaceID: "workspace-1", AgentID: "agent-1"}, "runtime-1", coordinator)
	runner.ensureResidentRuntime = func(context.Context, string, string) error { return nil }
	delivery := protocol.AgentDeliverPayload{
		AgentID: "agent-1", Target: "channel:one", Seq: 1, DeliveryID: "delivery-1",
		Message: protocol.AgentMessageProjection{ID: "message-1", Target: "channel:one", Seq: 1},
	}
	writeErr := errors.New("injected ACK writer failure")

	err = runner.handleMessageDelivery(context.Background(), delivery, func(string, any) error {
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

func TestWorkspaceRunnerAckWriterFailureDoesNotRepeatProviderAcceptance(t *testing.T) {
	var handoffs int
	d := New(Config{}, nil)
	coordinator, err := newTestMessageCoordinator(t, t.TempDir(), func(context.Context, []protocol.AgentMessageProjection) error {
		handoffs++
		return nil
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	completeCoordinatorRecovery(t, coordinator)
	d.mu.Lock()
	d.runtimeIndex["runtime-1"] = Runtime{ID: "runtime-1", WorkspaceID: "workspace-1"}
	d.mu.Unlock()
	runner := registerTestInbox(t, d, InboxKey{WorkspaceID: "workspace-1", AgentID: "agent-1"}, "runtime-1", coordinator)
	d.canonicalRuntimes.slots["agent-1\x00runtime-1"] = &canonicalAgentRuntimeSlot{
		mode:    canonicalRuntimeResident,
		backend: &idleMessageFakeRuntime{},
	}
	delivery := protocol.AgentDeliverPayload{
		AgentID: "agent-1", Target: "channel:one", Seq: 1, DeliveryID: "delivery-1",
		Message: protocol.AgentMessageProjection{ID: "message-1", Target: "channel:one", Seq: 1},
	}
	writeErr := errors.New("injected ACK writer failure")

	if err := runner.handleMessageDelivery(context.Background(), delivery, func(string, any) error {
		return writeErr
	}); !errors.Is(err, writeErr) {
		t.Fatalf("first delivery error = %v, want %v", err, writeErr)
	}
	if handoffs != 1 {
		t.Fatalf("provider handoffs after first acceptance = %d, want 1", handoffs)
	}
	if got := coordinator.Boundaries()["channel:one"]; got != 1 {
		t.Fatalf("boundary after provider acceptance = %d, want 1", got)
	}

	var acknowledgements int
	if err := runner.handleMessageDelivery(context.Background(), delivery, func(eventType string, _ any) error {
		if eventType == protocol.EventAgentDeliverAck {
			acknowledgements++
		}
		return nil
	}); err != nil {
		t.Fatalf("retried delivery: %v", err)
	}
	if acknowledgements != 1 {
		t.Fatalf("retry acknowledgements = %d, want 1", acknowledgements)
	}
	if handoffs != 1 {
		t.Fatalf("provider handoffs after retry = %d, want still 1", handoffs)
	}
}

func TestWorkspaceRunnerUnacceptedDeliveryIsRetriedAfterManagedStart(t *testing.T) {
	var handoffs int
	d := New(Config{}, nil)
	coordinator, err := newTestMessageCoordinator(t, t.TempDir(), func(context.Context, []protocol.AgentMessageProjection) error {
		handoffs++
		return nil
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	completeCoordinatorRecovery(t, coordinator)
	d.mu.Lock()
	d.runtimeIndex["runtime-1"] = Runtime{ID: "runtime-1", WorkspaceID: "workspace-1"}
	d.mu.Unlock()
	runner := registerTestInbox(t, d, InboxKey{WorkspaceID: "workspace-1", AgentID: "agent-1"}, "runtime-1", coordinator)
	if err := runner.processes.Stop(agentProcessCallback{AgentID: "agent-1", LaunchID: "test-launch-agent-1"}); err != nil {
		t.Fatalf("remove managed launch before first delivery: %v", err)
	}
	runner.ensureResidentRuntime = func(context.Context, string, string) error { return nil }
	delivery := protocol.AgentDeliverPayload{
		AgentID: "agent-1", Target: "channel:one", Seq: 1, DeliveryID: "delivery-1",
		Message: protocol.AgentMessageProjection{ID: "message-1", Target: "channel:one", Seq: 1},
	}
	var acknowledgements int
	write := func(eventType string, _ any) error {
		if eventType == protocol.EventAgentDeliverAck {
			acknowledgements++
		}
		return nil
	}
	if err := runner.handleMessageDelivery(context.Background(), delivery, write); err != nil {
		t.Fatal(err)
	}
	if acknowledgements != 0 || handoffs != 0 {
		t.Fatalf("unaccepted delivery acknowledgements=%d handoffs=%d, want 0/0", acknowledgements, handoffs)
	}
	if _, err := runner.processes.Start(agentProcessStartRequest{AgentID: "agent-1", RuntimeID: "runtime-1", LaunchID: "launch-1"}); err != nil {
		t.Fatalf("accept managed start: %v", err)
	}
	if err := runner.handleMessageDelivery(context.Background(), delivery, write); err != nil {
		t.Fatal(err)
	}
	if acknowledgements != 1 || handoffs != 1 {
		t.Fatalf("retry acknowledgements=%d handoffs=%d, want 1/1", acknowledgements, handoffs)
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
	runner := registerTestInbox(t, d, InboxKey{WorkspaceID: "workspace-1", AgentID: "agent-1"}, "runtime-1", coordinator)
	d.canonicalRuntimes.slots["agent-1\x00runtime-1"] = &canonicalAgentRuntimeSlot{
		mode:    canonicalRuntimeResident,
		backend: &idleMessageFakeRuntime{},
	}
	delivery := protocol.AgentDeliverPayload{
		AgentID: "agent-1", Target: "channel:one", Seq: 1, DeliveryID: "delivery-1",
		Message: protocol.AgentMessageProjection{ID: "message-1", Target: "channel:one", Seq: 1},
	}
	var acknowledgements int

	err = runner.handleMessageDelivery(context.Background(), delivery, func(eventType string, _ any) error {
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

func TestWorkspaceRunnerDeliveryDispatcherBuffersOnlyStartingAgent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	handled := make(chan string, 2)
	dispatcher := newWorkspaceRunnerDeliveryDispatcher(ctx, func(_ context.Context, delivery protocol.AgentDeliverPayload) {
		handled <- delivery.AgentID
	})
	if !dispatcher.Pause("agent-a", "launch-a") {
		t.Fatal("failed to establish Agent start buffer")
	}
	dispatcher.Enqueue(protocol.AgentDeliverPayload{AgentID: "agent-a", DeliveryID: "delivery-a"})
	dispatcher.Enqueue(protocol.AgentDeliverPayload{AgentID: "agent-b", DeliveryID: "delivery-b"})
	select {
	case got := <-handled:
		if got != "agent-b" {
			t.Fatalf("handled %q before start acceptance, want agent-b", got)
		}
	case <-time.After(time.Second):
		t.Fatal("unrelated Agent was blocked by Agent start")
	}
	select {
	case got := <-handled:
		t.Fatalf("starting Agent %q delivered before start acceptance", got)
	case <-time.After(20 * time.Millisecond):
	}
	dispatcher.Resume("agent-a", "launch-a")
	select {
	case got := <-handled:
		if got != "agent-a" {
			t.Fatalf("handled %q after resume, want agent-a", got)
		}
	case <-time.After(time.Second):
		t.Fatal("starting Agent buffer did not drain after acceptance")
	}
}

func TestWorkspaceRunnerDeliveryDispatcherFencesSupersededStart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	handled := make(chan string, 1)
	dispatcher := newWorkspaceRunnerDeliveryDispatcher(ctx, func(_ context.Context, delivery protocol.AgentDeliverPayload) {
		handled <- delivery.DeliveryID
	})
	if !dispatcher.Pause("agent-a", "launch-old") || !dispatcher.Pause("agent-a", "launch-new") {
		t.Fatal("failed to replace Agent start fence")
	}
	dispatcher.Enqueue(protocol.AgentDeliverPayload{AgentID: "agent-a", DeliveryID: "delivery-a"})
	dispatcher.RejectStart("agent-a", "launch-old")
	dispatcher.Resume("agent-a", "launch-old")
	select {
	case got := <-handled:
		t.Fatalf("old launch released new launch buffer: %q", got)
	case <-time.After(20 * time.Millisecond):
	}
	dispatcher.Resume("agent-a", "launch-new")
	select {
	case got := <-handled:
		if got != "delivery-a" {
			t.Fatalf("released delivery = %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("current launch did not release buffered delivery")
	}
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
	runner, _ := attachTestWorkspaceRunner(t, d, runnerWorkspaceID, nil)
	if err := runner.handleMessageDelivery(context.Background(), delivery, func(eventType string, _ any) error {
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
		if runner.hasMessageInbox(agentID) {
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
	runner, _ := attachTestWorkspaceRunner(t, d, workspaceID, nil)
	if err := runner.handleMessageDelivery(context.Background(), delivery, func(eventType string, _ any) error {
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
		if runner.hasMessageInbox(agentID) {
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
	if !d.sendWorkspaceRunnerAgentFrame("agent-1", "agent:recovery:request", map[string]any{"request": 1}) {
		t.Fatal("current Runner connection did not receive Message frame")
	}
	if first != 0 || second != 1 {
		t.Fatalf("Message frame delivery first=%d second=%d, want 0/1", first, second)
	}
	d.detachWorkspaceRunner(runner)
	if d.sendWorkspaceRunnerAgentFrame("agent-1", "agent:recovery:request", nil) {
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

	runner := d.currentWorkspaceRunner("workspace-1")
	runner.beginMessageRecovery("agent-1")
	if eventType != protocol.EventAgentRecoveryRequest || request.AgentID != "agent-1" || request.RecoveryID == "" {
		t.Fatalf("recovery frame type=%q request=%+v", eventType, request)
	}
}

func TestLateAttachmentStartsRecoveryOnlyForAttachedAgent(t *testing.T) {
	d := New(Config{}, nil)
	d.mu.Lock()
	d.runtimeIndex["runtime-1"] = Runtime{ID: "runtime-1", WorkspaceID: "workspace-1"}
	d.runtimeIndex["runtime-2"] = Runtime{ID: "runtime-2", WorkspaceID: "workspace-1"}
	d.mu.Unlock()

	runner, _ := attachTestWorkspaceRunner(t, d, "workspace-1", nil)
	coordinators := make(map[string]*MessageCoordinator, 2)
	for _, agent := range []struct {
		id        string
		runtimeID string
	}{
		{id: "agent-1", runtimeID: "runtime-1"},
		{id: "agent-2", runtimeID: "runtime-2"},
	} {
		coordinator, err := newTestMessageCoordinator(t, t.TempDir(), func(context.Context, []protocol.AgentMessageProjection) error { return nil }, nil)
		if err != nil {
			t.Fatal(err)
		}
		coordinators[agent.id] = coordinator
		registerTestRunnerInbox(t, runner, InboxKey{WorkspaceID: "workspace-1", AgentID: agent.id}, agent.runtimeID, coordinator)
	}

	initialRequests := make(map[string]protocol.AgentRecoveryRequest, 2)
	runner.beginMessageRecoveryForAll(func(request protocol.AgentRecoveryRequest) error {
		initialRequests[request.AgentID] = request
		return nil
	})
	if len(initialRequests) != 2 {
		t.Fatalf("initial recovery requests=%+v, want both Agents", initialRequests)
	}

	var requests []protocol.AgentRecoveryRequest
	runner.beginMessageRecoveryForAgent("agent-2", func(request protocol.AgentRecoveryRequest) error {
		requests = append(requests, request)
		return nil
	})
	if len(requests) != 1 || requests[0].AgentID != "agent-2" {
		t.Fatalf("late Attachment recovery requests=%+v, want only agent-2", requests)
	}
	firstRequest := initialRequests["agent-1"]
	if err := coordinators["agent-1"].MergeRecoveryPage(protocol.AgentRecoveryPage{
		AgentID: "agent-1", RecoveryID: firstRequest.RecoveryID, SnapshotID: "snapshot-1", HighWatermark: "fence-1",
	}); err != nil {
		t.Fatalf("late Attachment invalidated unrelated Agent recovery: %v", err)
	}
}

func TestAttachmentReplayStartsMessageRecoveryOnlyAfterCompletion(t *testing.T) {
	root := t.TempDir()
	d := New(Config{WorkspacesRoot: root}, nil)
	d.agentAttachments = newLocalAgentAttachmentRegistry(root, nil)
	d.mu.Lock()
	d.runtimeIndex["runtime-1"] = Runtime{ID: "runtime-1", WorkspaceID: "workspace-1"}
	d.mu.Unlock()

	requests := 0
	statuses := 0
	sessions := 0
	runner, connection := attachTestWorkspaceRunner(t, d, "workspace-1", func(eventType string, _ any) error {
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
	payload := protocol.WorkspaceRunnerAgentAttachPayload{
		AgentID: "agent-1", RuntimeID: "runtime-1", AttachmentGeneration: 1, LifecycleSeq: 1,
	}
	if _, err := runner.applyAttachmentAttach(payload); err != nil {
		t.Fatal(err)
	}
	if _, err := runner.applyAttachmentAttach(payload); err != nil {
		t.Fatal(err)
	}
	if requests != 0 {
		t.Fatalf("Attachment commands started Message recovery before replay end: %d", requests)
	}
	if _, err := runner.completeAttachmentReplay(runner.attachmentRuntimeSet(), protocol.WorkspaceRunnerAttachmentReplayEnd{RuntimeCursors: map[string]int64{"runtime-1": 1}}); err != nil {
		t.Fatal(err)
	}
	runner.inboxes.BeginRecovery(func(request protocol.AgentRecoveryRequest) error {
		return runner.sendOnConnection(connection, protocol.EventAgentRecoveryRequest, request)
	})
	if requests != 1 {
		t.Fatalf("Message recovery requests=%d, want one for the newly created coordinator", requests)
	}
	if statuses != 0 || sessions != 0 {
		t.Fatalf("Attachment replay invented Workspace Runner launch frames: status=%d session=%d", statuses, sessions)
	}
}

func TestRestoreResidentAgentsRebuildsRunnerPresenceAndMessageRecovery(t *testing.T) {
	const (
		workspaceID = "11111111-1111-4111-8111-111111111111"
		agentID     = "22222222-2222-4222-8222-222222222222"
		runtimeID   = "33333333-3333-4333-8333-333333333333"
	)
	root := t.TempDir()
	persisting := New(Config{DaemonID: "daemon-test", WorkspacesRoot: root}, nil)
	if _, err := persisting.agentAttachments.Apply(workspaceID, AgentAttachmentEvent{Kind: AgentAttachmentEventAttach, AgentID: agentID, RuntimeID: runtimeID, AttachmentGeneration: 1, LifecycleSeq: 1}); err != nil {
		t.Fatalf("persist Attachment: %v", err)
	}

	restarted := New(Config{DaemonID: "daemon-test", WorkspacesRoot: root}, nil)
	restarted.mu.Lock()
	restarted.runtimeIndex[runtimeID] = Runtime{ID: runtimeID, WorkspaceID: workspaceID}
	restarted.mu.Unlock()
	if err := restarted.restoreResidentAgents(); err != nil {
		t.Fatalf("restore residents: %v", err)
	}

	runner := restarted.currentWorkspaceRunner(workspaceID)
	if runner == nil {
		t.Fatal("restore did not create Workspace Runner")
	}
	coordinator, gotRuntimeID, _ := runner.inboxes.Resolve(agentID)
	if coordinator == nil || gotRuntimeID != runtimeID {
		t.Fatalf("restored coordinator=%v runtime_id=%q", coordinator, gotRuntimeID)
	}
	producer := runner.activity
	_, frames := producer.AttachTransport(func(protocol.AgentActivityPayload) {})
	if len(frames) != 0 {
		t.Fatalf("restored Attachment invented Runner lifecycle frames = %#v", frames)
	}
	var recovery protocol.AgentRecoveryRequest
	runner.inboxes.BeginRecovery(func(request protocol.AgentRecoveryRequest) error {
		recovery = request
		return nil
	})
	if recovery.AgentID != agentID || recovery.RecoveryID == "" {
		t.Fatalf("restored Message recovery request = %+v", recovery)
	}
}
