package daemon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/pkg/agent"
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
	markTestLaunchRunning(t, runner, "agent-1")
	d.canonicalRuntimes.slots["agent-1\x00runtime-1"] = &canonicalAgentRuntimeSlot{
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

func TestWorkspaceRunnerDeliveryWriterFailureRetainsDurableConsumedBoundary(t *testing.T) {
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
	markTestLaunchRunning(t, runner, "agent-1")
	runner.ensureResidentRuntime = func(context.Context, string, string, *agent.PiRunIdentity) error { return nil }
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
	if handoffs != 1 {
		t.Fatalf("runtime handoffs after ACK writer failure = %d, want 1", handoffs)
	}
	coordinator.mu.Lock()
	pending := coordinator.pendingBatchLocked()
	coordinator.mu.Unlock()
	if len(pending) != 0 || coordinator.Boundaries()["channel:one"] != 1 {
		t.Fatalf("state after ACK writer failure pending=%+v boundaries=%v, want consumed seq 1", pending, coordinator.Boundaries())
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
	markTestLaunchRunning(t, runner, "agent-1")
	d.canonicalRuntimes.slots["agent-1\x00runtime-1"] = &canonicalAgentRuntimeSlot{
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
	runner.ensureResidentRuntime = func(context.Context, string, string, *agent.PiRunIdentity) error { return nil }
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
	if _, err := runner.acceptMessageDelivery(context.Background(), delivery); !errors.Is(err, errDeliveryRejectedNoProcess) {
		t.Fatalf("unaccepted error = %v, want rejected_no_process", err)
	}
	if _, err := runner.processes.Start(agentProcessStartRequest{AgentID: "agent-1", RuntimeID: "runtime-1", LaunchID: "launch-1", StartDispatchID: "launch-1" + "-dispatch"}); err != nil {
		t.Fatalf("accept managed start: %v", err)
	}
	markTestLaunchRunning(t, runner, "agent-1")
	if err := runner.handleMessageDelivery(context.Background(), delivery, write); err != nil {
		t.Fatal(err)
	}
	if acknowledgements != 1 || handoffs != 1 {
		t.Fatalf("retry acknowledgements=%d handoffs=%d, want 1/1", acknowledgements, handoffs)
	}
}

func TestWorkspaceRunnerConsumedDeliveryAcknowledgesWithoutProcess(t *testing.T) {
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
	if err := coordinator.MarkRead("channel:one", 1); err != nil {
		t.Fatalf("cover seq 1: %v", err)
	}
	if err := runner.processes.Stop(agentProcessCallback{AgentID: "agent-1", LaunchID: "test-launch-agent-1"}); err != nil {
		t.Fatalf("remove managed launch: %v", err)
	}
	delivery := protocol.AgentDeliverPayload{
		AgentID: "agent-1", Target: "channel:one", Seq: 1, DeliveryID: "delivery-1",
		Message: protocol.AgentMessageProjection{ID: "message-1", Target: "channel:one", Seq: 1},
	}
	var acknowledgements int
	if err := runner.handleMessageDelivery(context.Background(), delivery, func(eventType string, _ any) error {
		if eventType == protocol.EventAgentDeliverAck {
			acknowledgements++
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if acknowledgements != 1 || handoffs != 0 {
		t.Fatalf("consumed delivery acknowledgements=%d handoffs=%d, want 1/0", acknowledgements, handoffs)
	}
	coordinator.mu.Lock()
	pending := coordinator.pendingBatchLocked()
	coordinator.mu.Unlock()
	if len(pending) != 0 {
		t.Fatalf("consumed delivery left Pending %+v", pending)
	}
}

func TestWorkspaceRunnerTerminalFailureDeliveryAcknowledgesAndKeepsPending(t *testing.T) {
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
	var activities []protocol.AgentActivityPayload
	runner := registerTestInbox(t, d, InboxKey{WorkspaceID: "workspace-1", AgentID: "agent-1"}, "runtime-1", coordinator)
	runner.activity.AttachTransport(func(payload protocol.AgentActivityPayload) { activities = append(activities, payload) })
	runner.failManagedRuntime("agent-1", "runtime-1", "test-launch-agent-1", managedRuntimeFailureRuntime, "provider_turn_failed", time.Now().UTC())
	delivery := protocol.AgentDeliverPayload{
		AgentID: "agent-1", Target: "channel:one", Seq: 1, DeliveryID: "delivery-1",
		Message: protocol.AgentMessageProjection{ID: "message-1", Target: "channel:one", Seq: 1, Content: "hi"},
	}
	var acknowledgements int
	if err := runner.handleMessageDelivery(context.Background(), delivery, func(eventType string, _ any) error {
		if eventType == protocol.EventAgentDeliverAck {
			acknowledgements++
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if acknowledgements != 1 || handoffs != 0 {
		t.Fatalf("terminal failure delivery acknowledgements=%d handoffs=%d, want 1/0", acknowledgements, handoffs)
	}
	coordinator.mu.Lock()
	pending := coordinator.pendingBatchLocked()
	coordinator.mu.Unlock()
	if len(pending) != 1 || pending[0].ID != "message-1" {
		t.Fatalf("Pending after terminal failure = %+v, want message-1", pending)
	}
	if len(activities) == 0 || activities[len(activities)-1].Snapshot.ActivityKind != protocol.ActivityKindError {
		t.Fatalf("terminal failure Activity = %+v, want error", activities)
	}
}

func TestWorkspaceRunnerIdleSnapshotDeliveryRestartsAndAcknowledges(t *testing.T) {
	d := New(Config{WorkspacesRoot: t.TempDir()}, nil)
	d.mu.Lock()
	d.runtimeIndex["runtime-1"] = Runtime{ID: "runtime-1", WorkspaceID: "workspace-1"}
	d.mu.Unlock()
	providerStarts := 0
	runner, _ := attachTestWorkspaceRunner(t, d, "workspace-1", nil)
	runner.ensureResidentRuntime = func(context.Context, string, string, *agent.PiRunIdentity) error {
		providerStarts++
		return nil
	}
	if _, _, _, err := runner.startManagedAgent(context.Background(), protocol.WorkspaceRunnerAgentStartPayload{
		AgentID: "agent-1", RuntimeID: "runtime-1", LaunchID: "launch-1", StartDispatchID: "dispatch-1",
	}); err != nil {
		t.Fatalf("start managed Agent: %v", err)
	}
	if err := runner.processes.Stop(agentProcessCallback{AgentID: "agent-1", LaunchID: "launch-1"}); err != nil {
		t.Fatalf("drop live process: %v", err)
	}
	delivery := protocol.AgentDeliverPayload{
		AgentID: "agent-1", Target: "channel:one", Seq: 1, DeliveryID: "delivery-1",
		Message: protocol.AgentMessageProjection{ID: "message-1", Target: "channel:one", Seq: 1, Content: "hi"},
	}
	var acknowledgements int
	if err := runner.handleMessageDelivery(context.Background(), delivery, func(eventType string, _ any) error {
		if eventType == protocol.EventAgentDeliverAck {
			acknowledgements++
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if acknowledgements != 1 {
		t.Fatalf("idle snapshot acknowledgements=%d, want 1", acknowledgements)
	}
	launch, managed := runner.processes.Snapshot("agent-1")
	if !managed {
		t.Fatal("idle snapshot delivery did not restart APM launch")
	}
	if launch.LaunchID != "launch-1" {
		t.Fatalf("idle restore launch = %q, want original launch-1", launch.LaunchID)
	}
}

func TestWorkspaceRunnerIdleSnapshotCompleteRespectsCancel(t *testing.T) {
	d := New(Config{WorkspacesRoot: t.TempDir()}, nil)
	d.mu.Lock()
	d.runtimeIndex["runtime-1"] = Runtime{ID: "runtime-1", WorkspaceID: "workspace-1"}
	d.mu.Unlock()
	providerStarts := 0
	runner, _ := attachTestWorkspaceRunner(t, d, "workspace-1", nil)
	runner.ensureResidentRuntime = func(context.Context, string, string, *agent.PiRunIdentity) error {
		providerStarts++
		return nil
	}
	if _, _, _, err := runner.startManagedAgent(context.Background(), protocol.WorkspaceRunnerAgentStartPayload{
		AgentID: "agent-1", RuntimeID: "runtime-1", LaunchID: "launch-1", StartDispatchID: "dispatch-1",
	}); err != nil {
		t.Fatalf("start managed Agent: %v", err)
	}
	starts := providerStarts
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	runner.completeIdleSnapshotStart(ctx, "agent-1", agentResidency{
		runtimeID: "runtime-1", launchID: "launch-1", startDispatchID: "dispatch-1",
	})
	if providerStarts != starts {
		t.Fatalf("cancelled idle complete started provider %d times, want %d", providerStarts, starts)
	}
}

func TestWorkspaceRunnerIdleSnapshotFailureLogsLifecycleIdentity(t *testing.T) {
	var logs strings.Builder
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	d := New(Config{DaemonID: "computer-1", WorkspacesRoot: t.TempDir()}, logger)
	d.mu.Lock()
	d.runtimeIndex["runtime-1"] = Runtime{ID: "runtime-1", WorkspaceID: "workspace-1"}
	d.mu.Unlock()
	runner, _ := attachTestWorkspaceRunner(t, d, "workspace-1", nil)
	runner.ensureResidentRuntime = func(context.Context, string, string, *agent.PiRunIdentity) error { return nil }
	if _, _, _, err := runner.startManagedAgent(context.Background(), protocol.WorkspaceRunnerAgentStartPayload{
		AgentID: "agent-1", RuntimeID: "runtime-1", LaunchID: "launch-1", StartDispatchID: "dispatch-1",
	}); err != nil {
		t.Fatalf("seed managed Agent: %v", err)
	}
	if err := runner.processes.Stop(agentProcessCallback{AgentID: "agent-1", LaunchID: "launch-1"}); err != nil {
		t.Fatalf("stop seed launch: %v", err)
	}
	res := agentResidency{runtimeID: "runtime-1", launchID: "launch-1", startDispatchID: "dispatch-1"}
	if err := runner.restartFromIdleSnapshot("agent-1", res); err != nil {
		t.Fatalf("restore idle snapshot: %v", err)
	}
	runner.ensureResidentRuntime = func(context.Context, string, string, *agent.PiRunIdentity) error {
		return errors.New("provider unavailable")
	}
	logs.Reset()

	runner.completeIdleSnapshotStart(context.Background(), "agent-1", res)

	got := logs.String()
	for _, want := range []string{
		"computer_id=computer-1", "workspace_id=workspace-1", "agent_id=agent-1",
		"runtime_id=runtime-1", "launch_id=launch-1", "start_dispatch_id=dispatch-1",
		"queue_state=starting", "reason=provider_start_failed", "outcome=failed",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("idle snapshot failure log missing %q: %s", want, got)
		}
	}
}

func TestWorkspaceRunnerSpawnCooldownDeliveryAcknowledgesWithoutRestart(t *testing.T) {
	d := New(Config{WorkspacesRoot: t.TempDir()}, nil)
	d.mu.Lock()
	d.runtimeIndex["runtime-1"] = Runtime{ID: "runtime-1", WorkspaceID: "workspace-1"}
	d.mu.Unlock()
	providerStarts := 0
	runner, _ := attachTestWorkspaceRunner(t, d, "workspace-1", nil)
	runner.ensureResidentRuntime = func(context.Context, string, string, *agent.PiRunIdentity) error {
		providerStarts++
		return fmt.Errorf("spawn cursor: executable unavailable")
	}
	if _, _, _, err := runner.startManagedAgent(context.Background(), protocol.WorkspaceRunnerAgentStartPayload{
		AgentID: "agent-1", RuntimeID: "runtime-1", LaunchID: "launch-1", StartDispatchID: "dispatch-1",
	}); err == nil {
		t.Fatal("spawn failure was accepted")
	}
	startsAfterFailure := providerStarts
	delivery := protocol.AgentDeliverPayload{
		AgentID: "agent-1", Target: "channel:one", Seq: 1, DeliveryID: "delivery-1",
		Message: protocol.AgentMessageProjection{ID: "message-1", Target: "channel:one", Seq: 1, Content: "hi"},
	}
	var acknowledgements int
	if err := runner.handleMessageDelivery(context.Background(), delivery, func(eventType string, _ any) error {
		if eventType == protocol.EventAgentDeliverAck {
			acknowledgements++
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if acknowledgements != 1 || providerStarts != startsAfterFailure {
		t.Fatalf("cooldown delivery acknowledgements=%d provider_starts=%d, want 1/%d", acknowledgements, providerStarts, startsAfterFailure)
	}
}

func TestWorkspaceRunnerQueuedAPMAcceptsDeliveryWithoutStartingProvider(t *testing.T) {
	var handoffs int
	d := New(Config{MaxAgentProcesses: 1}, nil)
	d.mu.Lock()
	d.runtimeIndex["runtime-1"] = Runtime{ID: "runtime-1", WorkspaceID: "workspace-1"}
	d.runtimeIndex["runtime-2"] = Runtime{ID: "runtime-2", WorkspaceID: "workspace-1"}
	d.mu.Unlock()
	firstCoordinator, err := newTestMessageCoordinator(t, t.TempDir(), func(context.Context, []protocol.AgentMessageProjection) error { return nil }, nil)
	if err != nil {
		t.Fatal(err)
	}
	completeCoordinatorRecovery(t, firstCoordinator)
	first := registerTestInbox(t, d, InboxKey{WorkspaceID: "workspace-1", AgentID: "agent-1"}, "runtime-1", firstCoordinator)
	coordinator, err := newTestMessageCoordinator(t, t.TempDir(), func(context.Context, []protocol.AgentMessageProjection) error {
		handoffs++
		return nil
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	completeCoordinatorRecovery(t, coordinator)
	runner := registerTestInbox(t, d, InboxKey{WorkspaceID: "workspace-1", AgentID: "agent-2"}, "runtime-2", coordinator)
	if first != runner {
		t.Fatal("test Agents did not share the Workspace Runner")
	}
	queued, ok := runner.processes.Snapshot("agent-2")
	if !ok || queued.QueueState != protocol.AgentStartQueueQueued {
		t.Fatalf("queued launch = %+v, exists=%v", queued, ok)
	}
	providerStarts := 0
	runner.ensureResidentRuntime = func(context.Context, string, string, *agent.PiRunIdentity) error {
		providerStarts++
		return nil
	}
	delivery := protocol.AgentDeliverPayload{AgentID: "agent-2", Target: "channel:one", Seq: 1, DeliveryID: "delivery-queued", Message: protocol.AgentMessageProjection{ID: "message-queued", Target: "channel:one", Seq: 1}}
	acceptance, err := runner.acceptMessageDelivery(context.Background(), delivery)
	if err != nil {
		t.Fatal(err)
	}
	if acceptance.outcome != messageDeliveryPendingBuffered || providerStarts != 0 || handoffs != 0 {
		t.Fatalf("queued acceptance=%+v provider_starts=%d handoffs=%d", acceptance, providerStarts, handoffs)
	}
}

func TestWorkspaceRunnerStartingLaunchBuffersDeliveryWithoutHandoff(t *testing.T) {
	var handoffs int
	d := New(Config{}, nil)
	coordinator, err := newTestMessageCoordinator(t, t.TempDir(), func(context.Context, []protocol.AgentMessageProjection) error {
		handoffs++
		return errors.New("provider not ready")
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	completeCoordinatorRecovery(t, coordinator)
	d.mu.Lock()
	d.runtimeIndex["runtime-1"] = Runtime{ID: "runtime-1", WorkspaceID: "workspace-1"}
	d.mu.Unlock()
	runner := registerTestInbox(t, d, InboxKey{WorkspaceID: "workspace-1", AgentID: "agent-1"}, "runtime-1", coordinator)
	launch, ok := runner.processes.Snapshot("agent-1")
	if !ok || launch.QueueState != protocol.AgentStartQueueStarting {
		t.Fatalf("starting launch = %+v exists=%v", launch, ok)
	}
	providerStarts := 0
	runner.ensureResidentRuntime = func(context.Context, string, string, *agent.PiRunIdentity) error {
		providerStarts++
		return errors.New("provider still starting")
	}
	delivery := protocol.AgentDeliverPayload{
		AgentID: "agent-1", Target: "channel:one", Seq: 1, DeliveryID: "delivery-1",
		Message: protocol.AgentMessageProjection{ID: "message-1", Target: "channel:one", Seq: 1, Content: "hi"},
	}
	var acknowledgements int
	if err := runner.handleMessageDelivery(context.Background(), delivery, func(eventType string, _ any) error {
		if eventType == protocol.EventAgentDeliverAck {
			acknowledgements++
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if acknowledgements != 1 || handoffs != 0 || providerStarts != 0 {
		t.Fatalf("starting delivery acknowledgements=%d handoffs=%d provider_starts=%d, want 1/0/0", acknowledgements, handoffs, providerStarts)
	}
	coordinator.mu.Lock()
	pending := coordinator.pendingBatchLocked()
	coordinator.mu.Unlock()
	if len(pending) != 1 || pending[0].ID != "message-1" {
		t.Fatalf("Pending while starting = %+v, want message-1", pending)
	}
}

func TestWorkspaceRunnerDeliveryAfterManagedStartReachesProvider(t *testing.T) {
	var handoffs int
	d := New(Config{WorkspacesRoot: t.TempDir()}, nil)
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
	runner.ensureResidentRuntime = func(context.Context, string, string, *agent.PiRunIdentity) error { return nil }
	if _, _, _, err := runner.startManagedAgent(context.Background(), protocol.WorkspaceRunnerAgentStartPayload{
		AgentID: "agent-1", RuntimeID: "runtime-1", LaunchID: "test-launch-agent-1", StartDispatchID: "test-launch-agent-1-dispatch",
	}); err != nil {
		t.Fatalf("managed start: %v", err)
	}
	delivery := protocol.AgentDeliverPayload{
		AgentID: "agent-1", Target: "channel:one", Seq: 1, DeliveryID: "delivery-1",
		Message: protocol.AgentMessageProjection{ID: "message-1", Target: "channel:one", Seq: 1, Content: "hi"},
	}
	if err := runner.handleMessageDelivery(context.Background(), delivery, func(string, any) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if handoffs != 1 {
		t.Fatalf("handoffs after managed start = %d, want 1 (Raft delivers once the process exists)", handoffs)
	}
}

func TestWorkspaceRunnerMissingProcessReportsRestartRequired(t *testing.T) {
	d := New(Config{}, nil)
	coordinator, err := newTestMessageCoordinator(t, t.TempDir(), func(context.Context, []protocol.AgentMessageProjection) error {
		t.Fatal("missing process must not hand off")
		return nil
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	completeCoordinatorRecovery(t, coordinator)
	d.mu.Lock()
	d.runtimeIndex["runtime-1"] = Runtime{ID: "runtime-1", WorkspaceID: "workspace-1"}
	d.mu.Unlock()
	var statuses []protocol.AgentStatusPayload
	var activities []protocol.AgentActivityPayload
	runner, _ := attachTestWorkspaceRunner(t, d, "workspace-1", func(eventType string, payload any) error {
		switch eventType {
		case protocol.EventAgentStatus:
			statuses = append(statuses, payload.(protocol.AgentStatusPayload))
		case protocol.EventAgentActivity:
			activities = append(activities, payload.(protocol.AgentActivityPayload))
		}
		return nil
	})
	registerTestRunnerInbox(t, runner, InboxKey{WorkspaceID: "workspace-1", AgentID: "agent-1"}, "runtime-1", coordinator)
	if err := runner.processes.Stop(agentProcessCallback{AgentID: "agent-1", LaunchID: "test-launch-agent-1"}); err != nil {
		t.Fatalf("remove managed launch: %v", err)
	}
	if err := runner.handleMessageDelivery(context.Background(), protocol.AgentDeliverPayload{
		AgentID: "agent-1", Target: "channel:one", Seq: 1, DeliveryID: "delivery-1",
		Message: protocol.AgentMessageProjection{ID: "message-1", Target: "channel:one", Seq: 1, Content: "hi"},
	}, func(string, any) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if len(statuses) == 0 || statuses[len(statuses)-1].Status != protocol.AgentStatusInactive {
		t.Fatalf("missing-process status = %+v, want inactive", statuses)
	}
	if len(activities) == 0 {
		t.Fatal("missing-process Activity was not published")
	}
	last := activities[len(activities)-1].Snapshot
	if last.ActivityKind != protocol.ActivityKindOffline || last.DetailKind != "runtime_unavailable" {
		t.Fatalf("missing-process Activity = %+v, want offline/runtime_unavailable", last)
	}
	if statuses[len(statuses)-1].LaunchID != "test-launch-agent-1" || last.LaunchID != "test-launch-agent-1" {
		t.Fatalf("missing-process launch IDs status=%q activity=%q, want test-launch-agent-1", statuses[len(statuses)-1].LaunchID, last.LaunchID)
	}
}

func TestWorkspaceRunnerDeliveryAcknowledgesBusyRuntime(t *testing.T) {
	var logs strings.Builder
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	d := New(Config{}, logger)
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
	markTestLaunchRunning(t, runner, "agent-1")
	d.canonicalRuntimes.slots["agent-1\x00runtime-1"] = &canonicalAgentRuntimeSlot{
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
	if got := logs.String(); strings.Contains(got, "level=WARN") || !strings.Contains(got, "level=DEBUG") || !strings.Contains(got, "outcome=deferred") {
		t.Fatalf("expected busy delivery to be a deferred debug event, got logs: %s", got)
	}
}

func TestWorkspaceRunnerDeliveryDoesNotAcknowledgeProviderRejection(t *testing.T) {
	d := New(Config{}, nil)
	attempts := 0
	coordinator, err := newTestMessageCoordinator(t, t.TempDir(), func(context.Context, []protocol.AgentMessageProjection) error {
		attempts++
		if attempts == 1 {
			return errors.New("cursor authenticate: TLS connection failed")
		}
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
	markTestLaunchRunning(t, runner, "agent-1")
	d.canonicalRuntimes.slots["agent-1\x00runtime-1"] = &canonicalAgentRuntimeSlot{backend: &idleMessageFakeRuntime{}}
	delivery := protocol.AgentDeliverPayload{
		AgentID: "agent-1", Target: "dm:one", Seq: 1, DeliveryID: "delivery-1",
		Message: protocol.AgentMessageProjection{ID: "message-1", Target: "dm:one", Seq: 1, Content: "hi"},
	}
	acknowledgements := 0
	err = runner.handleMessageDelivery(context.Background(), delivery, func(eventType string, _ any) error {
		if eventType == protocol.EventAgentDeliverAck {
			acknowledgements++
		}
		return nil
	})
	if err != nil {
		t.Fatalf("wire handler must keep the Runner connection alive after provider rejection: %v", err)
	}
	if acknowledgements != 0 {
		t.Fatalf("acknowledgements = %d, want none before APM accepts provider input", acknowledgements)
	}
	coordinator.mu.Lock()
	pending := coordinator.pendingBatchLocked()
	coordinator.mu.Unlock()
	if len(pending) != 1 || pending[0].ID != delivery.Message.ID {
		t.Fatalf("Pending after provider rejection = %+v, want message retained for redelivery", pending)
	}
	if err := coordinator.Flush(context.Background()); err != nil {
		t.Fatalf("retry pending delivery after replacement start: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("provider attempts = %d, want one failed start and one successful retry", attempts)
	}
	coordinator.mu.Lock()
	pending = coordinator.pendingBatchLocked()
	boundary := coordinator.boundaries[delivery.Target]
	coordinator.mu.Unlock()
	if len(pending) != 0 || boundary != delivery.Seq {
		t.Fatalf("successful retry pending=%+v boundary=%d, want consumed exactly once through seq %d", pending, boundary, delivery.Seq)
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

func TestDeliveryRepairsCoordinatorFromAcceptedStart(t *testing.T) {
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
	runner, _ := attachTestWorkspaceRunner(t, d, workspaceID, func(string, any) error { return nil })
	if _, err := runner.processes.Start(agentProcessStartRequest{AgentID: agentID, RuntimeID: runtimeID, LaunchID: "launch-1", StartDispatchID: "dispatch-1"}); err != nil {
		t.Fatalf("accept start: %v", err)
	}
	if err := d.ensureIdleMessageCoordinatorForDelivery(workspaceID, agentID); err != nil {
		t.Fatalf("repair coordinator: %v", err)
	}
	coordinator, gotRuntimeID := resolveTestInbox(t, d, InboxKey{WorkspaceID: workspaceID, AgentID: agentID})
	if coordinator == nil || gotRuntimeID != runtimeID {
		t.Fatalf("coordinator=%v runtime_id=%q", coordinator, gotRuntimeID)
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

func TestDeliveryDoesNotRepairCoordinatorWithoutAcceptedStart(t *testing.T) {
	const (
		workspaceID = "11111111-1111-4111-8111-111111111111"
		agentID     = "22222222-2222-4222-8222-222222222222"
		runtimeID   = "33333333-3333-4333-8333-333333333333"
	)
	d := New(Config{WorkspacesRoot: t.TempDir()}, nil)
	d.mu.Lock()
	d.runtimeIndex[runtimeID] = Runtime{ID: runtimeID, WorkspaceID: workspaceID}
	d.mu.Unlock()
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
		t.Fatalf("unstarted Agent acknowledgements = %d, want 0", acknowledgements)
	}
	if runner := d.currentWorkspaceRunner(workspaceID); runner != nil {
		if runner.hasMessageInbox(agentID) {
			t.Fatal("unstarted Agent Delivery recreated an Inbox coordinator")
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
		return runner.sendOnConnection(firstConnection, protocol.EventAgentDeliverAck, map[string]any{"request": 0})
	}
	secondConnection := newDaemonConnection("workspace-1", context.Background(), func(string, any) error {
		second++
		return nil
	}, func() {})
	runner.replaceConnection(secondConnection)
	defer runner.releaseConnection(secondConnection)
	if err := staleSend(); err == nil {
		t.Fatal("callback from replaced connection remained writable")
	}
	if !d.sendWorkspaceRunnerAgentFrame("agent-1", protocol.EventAgentDeliverAck, map[string]any{"request": 1}) {
		t.Fatal("current Runner connection did not receive Message frame")
	}
	if first != 0 || second != 1 {
		t.Fatalf("Message frame delivery first=%d second=%d, want 0/1", first, second)
	}
	d.detachWorkspaceRunner(runner)
	if d.sendWorkspaceRunnerAgentFrame("agent-1", protocol.EventAgentDeliverAck, nil) {
		t.Fatal("detached Runner accepted Message frame")
	}
}

func TestWorkspaceRunnerDeliveryStopFenceRejectsLateStartResume(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	handled := make(chan string, 2)
	dispatcher := newWorkspaceRunnerDeliveryDispatcher(ctx, func(_ context.Context, delivery protocol.AgentDeliverPayload) {
		handled <- delivery.DeliveryID
	})
	if !dispatcher.Pause("agent-1", "launch-1") {
		t.Fatal("pause start deliveries")
	}
	if !dispatcher.Enqueue(protocol.AgentDeliverPayload{AgentID: "agent-1", DeliveryID: "old-delivery"}) {
		t.Fatal("enqueue old delivery")
	}
	dispatcher.FenceStop("agent-1", "launch-1")
	dispatcher.Resume("agent-1", "launch-1")
	select {
	case deliveryID := <-handled:
		t.Fatalf("late start Resume released %q after stop fence", deliveryID)
	case <-time.After(30 * time.Millisecond):
	}
	if !dispatcher.Pause("agent-1", "launch-2") {
		t.Fatal("pause replacement start deliveries")
	}
	if !dispatcher.Enqueue(protocol.AgentDeliverPayload{AgentID: "agent-1", DeliveryID: "new-delivery"}) {
		t.Fatal("enqueue replacement delivery")
	}
	dispatcher.Resume("agent-1", "launch-2")
	select {
	case deliveryID := <-handled:
		if deliveryID != "new-delivery" {
			t.Fatalf("replacement delivery=%q, want new-delivery", deliveryID)
		}
	case <-time.After(time.Second):
		t.Fatal("replacement delivery remained fenced")
	}
}
