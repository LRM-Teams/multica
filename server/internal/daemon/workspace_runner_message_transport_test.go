package daemon

import (
	"context"
	"io"
	"log/slog"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

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
	if _, accepted, err := d.reminderAgents.applyStart(agentID, runtimeID, workspaceID, 1); err != nil || !accepted {
		t.Fatalf("persist residency accepted=%v err=%v", accepted, err)
	}

	var recovery protocol.AgentRecoveryRequest
	d.attachWorkspaceRunnerMessageTransport(workspaceID, func(eventType string, payload any) error {
		if eventType == protocol.EventAgentRecoveryRequest {
			recovery = payload.(protocol.AgentRecoveryRequest)
		}
		return nil
	})
	if err := d.ensureIdleMessageCoordinatorForDelivery(agentID); err != nil {
		t.Fatalf("repair coordinator: %v", err)
	}
	d.messageCoordinatorMu.RLock()
	coordinator := d.messageCoordinators[agentID]
	gotRuntimeID := d.messageRuntimeIDs[agentID]
	d.messageCoordinatorMu.RUnlock()
	if coordinator == nil || gotRuntimeID != runtimeID {
		t.Fatalf("coordinator=%v runtime_id=%q", coordinator, gotRuntimeID)
	}
	if recovery.AgentID != agentID || recovery.RecoveryID == "" {
		t.Fatalf("recovery request = %+v", recovery)
	}
}

func TestWorkspaceRunnerMessageTransportFencesReplacedConnection(t *testing.T) {
	d := New(Config{}, nil)
	d.messageCoordinatorMu.Lock()
	d.messageRuntimeIDs["agent-1"] = "runtime-1"
	d.messageCoordinatorMu.Unlock()
	d.mu.Lock()
	d.runtimeIndex["runtime-1"] = Runtime{ID: "runtime-1", WorkspaceID: "workspace-1"}
	d.mu.Unlock()

	var first, second int
	firstGeneration := d.attachWorkspaceRunnerMessageTransport("workspace-1", func(string, any) error {
		first++
		return nil
	})
	secondGeneration := d.attachWorkspaceRunnerMessageTransport("workspace-1", func(string, any) error {
		second++
		return nil
	})
	d.detachWorkspaceRunnerMessageTransport("workspace-1", firstGeneration)
	if !d.sendAgentMessageRunnerFrame("agent-1", "agent:recovery:request", map[string]any{"request": 1}) {
		t.Fatal("current Runner transport did not receive Message frame")
	}
	if first != 0 || second != 1 {
		t.Fatalf("Message transport delivery first=%d second=%d, want 0/1", first, second)
	}
	d.detachWorkspaceRunnerMessageTransport("workspace-1", secondGeneration)
	if d.sendAgentMessageRunnerFrame("agent-1", "agent:recovery:request", nil) {
		t.Fatal("detached Runner transport accepted Message frame")
	}
}

func TestAgentMessageRecoveryUsesWorkspaceRunnerTransport(t *testing.T) {
	d := New(Config{}, nil)
	coordinator, err := NewMessageCoordinator(t.TempDir(), func(context.Context, []protocol.AgentMessageProjection) error { return nil }, nil)
	if err != nil {
		t.Fatal(err)
	}
	d.messageCoordinatorMu.Lock()
	d.messageCoordinators["agent-1"] = coordinator
	d.messageRuntimeIDs["agent-1"] = "runtime-1"
	d.messageCoordinatorMu.Unlock()
	d.mu.Lock()
	d.runtimeIndex["runtime-1"] = Runtime{ID: "runtime-1", WorkspaceID: "workspace-1"}
	d.mu.Unlock()

	var eventType string
	var request protocol.AgentRecoveryRequest
	d.attachWorkspaceRunnerMessageTransport("workspace-1", func(gotType string, payload any) error {
		eventType = gotType
		request = payload.(protocol.AgentRecoveryRequest)
		return nil
	})

	d.beginAgentMessageRecovery("agent-1")
	if eventType != protocol.EventAgentRecoveryRequest || request.AgentID != "agent-1" || request.RecoveryID == "" {
		t.Fatalf("recovery frame type=%q request=%+v", eventType, request)
	}
}

func TestLifecycleReplayDoesNotRestartExistingMessageRecovery(t *testing.T) {
	root := t.TempDir()
	d := New(Config{WorkspacesRoot: root}, nil)
	d.reminderAgents = newReminderAgentManager(root, nil)
	d.mu.Lock()
	d.runtimeIndex["runtime-1"] = Runtime{ID: "runtime-1", WorkspaceID: "workspace-1"}
	d.mu.Unlock()

	requests := 0
	d.attachWorkspaceRunnerMessageTransport("workspace-1", func(eventType string, _ any) error {
		if eventType == protocol.EventAgentRecoveryRequest {
			requests++
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
}
