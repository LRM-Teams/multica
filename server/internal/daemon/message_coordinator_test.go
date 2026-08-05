package daemon

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
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

func TestRuntimePoolRetainsAdmissionUntilAcceptedMessageTurnCompletes(t *testing.T) {
	backend := &blockingResidentMessageRuntime{done: make(chan error, 1)}
	pool := newCanonicalAgentRuntimePool()
	pool.slots["agent-1\x00runtime-1"] = &canonicalAgentRuntimeSlot{
		mode: canonicalRuntimeResident, backend: backend,
	}
	messages := []protocol.AgentMessageProjection{{ID: "message-1", Target: "channel:one", Seq: 1, Content: "hello"}}
	if err := pool.handoffIdleMessages(context.Background(), "agent-1", "runtime-1", messages); err != nil {
		t.Fatalf("first handoff: %v", err)
	}
	if err := pool.handoffIdleMessages(context.Background(), "agent-1", "runtime-1", messages); !errors.Is(err, ErrCanonicalAgentRuntimeBusy) {
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
	wantRoot := multicaAgentRoot(daemon.cfg, workspaceID, agentID)
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
	daemon := &Daemon{}
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
	if !coordinator.FreshnessKnown() {
		t.Fatal("freshness remains unknown after terminal page")
	}
	if err := coordinator.Flush(context.Background()); err != nil {
		t.Fatalf("Flush: %v", err)
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
	if !coordinator.FreshnessKnown() {
		t.Fatal("current recovery page did not complete after stale page was ignored")
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
