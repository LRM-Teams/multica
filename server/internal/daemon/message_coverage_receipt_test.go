package daemon

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func newCoverageTestCoordinator(t *testing.T, key InboxKey) *MessageCoordinator {
	t.Helper()
	coordinator, err := NewMessageCoordinator(key, t.TempDir(), func(context.Context, []protocol.AgentMessageProjection) error { return nil }, nil)
	if err != nil {
		t.Fatalf("NewMessageCoordinator: %v", err)
	}
	t.Cleanup(coordinator.Close)
	completeCoordinatorRecovery(t, coordinator)
	return coordinator
}

func TestMessageCoordinatorRequiresFixedInboxIdentity(t *testing.T) {
	for name, key := range map[string]InboxKey{
		"missing Workspace": {AgentID: "agent-1"},
		"missing Agent":     {WorkspaceID: "workspace-1"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := NewMessageCoordinator(key, t.TempDir(), func(context.Context, []protocol.AgentMessageProjection) error { return nil }, nil); err == nil {
				t.Fatal("NewMessageCoordinator accepted an unscoped Inbox identity")
			}
		})
	}
}

func acceptCoverageTestMessage(t *testing.T, coordinator *MessageCoordinator, id, target string, seq int64) protocol.AgentMessageProjection {
	t.Helper()
	delivery := testDelivery(id, target, seq, "delivery-"+id)
	accepted, err := coordinator.Accept(context.Background(), delivery)
	if err != nil || !accepted {
		t.Fatalf("Accept %s: accepted=%v err=%v", id, accepted, err)
	}
	return delivery.Message
}

func TestMessageCoordinatorCoverageReceiptPrepareCommitAndDuplicateLifecycle(t *testing.T) {
	coordinator := newCoverageTestCoordinator(t, InboxKey{WorkspaceID: "workspace-1", AgentID: "agent-1"})
	first := acceptCoverageTestMessage(t, coordinator, "message-1", "channel:one", 1)
	second := acceptCoverageTestMessage(t, coordinator, "message-2", "channel:one", 2)

	offer, err := coordinator.PrepareCoverage(CoverageRequest{Kind: CoverageCheck, Limit: 1})
	if err != nil {
		t.Fatalf("PrepareCoverage: %v", err)
	}
	if offer.ReceiptID == "" || !offer.HasMore || offer.Remaining != 1 || !reflect.DeepEqual(offer.Messages, []protocol.AgentMessageProjection{first}) {
		t.Fatalf("Coverage offer = %+v, want first Message with has_more", offer)
	}
	if got := coordinator.Boundaries()["channel:one"]; got != 0 {
		t.Fatalf("prepare advanced boundary to %d", got)
	}
	coordinator.mu.Lock()
	pendingBeforeCommit := coordinator.pendingBatchLocked()
	coordinator.mu.Unlock()
	if !reflect.DeepEqual(pendingBeforeCommit, []protocol.AgentMessageProjection{first, second}) {
		t.Fatalf("prepare changed Pending: %+v", pendingBeforeCommit)
	}

	if err := coordinator.CommitCoverage(offer.ReceiptID); err != nil {
		t.Fatalf("CommitCoverage: %v", err)
	}
	if err := coordinator.CommitCoverage(offer.ReceiptID); err != nil {
		t.Fatalf("duplicate CommitCoverage: %v", err)
	}
	if got := coordinator.Boundaries()["channel:one"]; got != 1 {
		t.Fatalf("committed boundary = %d, want 1", got)
	}
	coordinator.mu.Lock()
	pendingAfterCommit := coordinator.pendingBatchLocked()
	coordinator.mu.Unlock()
	if !reflect.DeepEqual(pendingAfterCommit, []protocol.AgentMessageProjection{second}) {
		t.Fatalf("commit removed non-covered Pending: %+v", pendingAfterCommit)
	}
}

func TestMessageCoordinatorCoverageCheckKeepsDeliveryAcceptedBeforeCommit(t *testing.T) {
	coordinator := newCoverageTestCoordinator(t, InboxKey{WorkspaceID: "workspace-1", AgentID: "agent-1"})
	first := acceptCoverageTestMessage(t, coordinator, "message-1", "channel:one", 1)
	offer, err := coordinator.PrepareCoverage(CoverageRequest{Kind: CoverageCheck, Limit: 3})
	if err != nil {
		t.Fatalf("PrepareCoverage: %v", err)
	}
	if offer.HasMore || offer.Remaining != 0 || !reflect.DeepEqual(offer.Messages, []protocol.AgentMessageProjection{first}) {
		t.Fatalf("initial coverage offer = %+v", offer)
	}
	second := acceptCoverageTestMessage(t, coordinator, "message-2", "channel:one", 2)

	if err := coordinator.CommitCoverage(offer.ReceiptID); err != nil {
		t.Fatalf("CommitCoverage: %v", err)
	}
	next, err := coordinator.PrepareCoverage(CoverageRequest{Kind: CoverageCheck, Limit: 3})
	if err != nil {
		t.Fatalf("next PrepareCoverage: %v", err)
	}
	if !reflect.DeepEqual(next.Messages, []protocol.AgentMessageProjection{second}) {
		t.Fatalf("Delivery accepted before commit was not reoffered: %+v", next)
	}
}

func TestMessageCoordinatorCoverageReceiptExpirySafelyReoffersContext(t *testing.T) {
	coordinator := newCoverageTestCoordinator(t, InboxKey{WorkspaceID: "workspace-1", AgentID: "agent-1"})
	now := time.Date(2026, 8, 11, 8, 0, 0, 0, time.UTC)
	coordinator.coverageNow = func() time.Time { return now }
	coordinator.coverageTTL = time.Minute
	message := acceptCoverageTestMessage(t, coordinator, "message-1", "channel:one", 1)

	first, err := coordinator.PrepareCoverage(CoverageRequest{Kind: CoverageCheck, Limit: 1})
	if err != nil {
		t.Fatalf("PrepareCoverage: %v", err)
	}
	now = now.Add(2 * time.Minute)
	if err := coordinator.CommitCoverage(first.ReceiptID); !errors.Is(err, ErrCoverageReceiptExpired) {
		t.Fatalf("expired CommitCoverage error = %v, want %v", err, ErrCoverageReceiptExpired)
	}
	if got := coordinator.Boundaries()["channel:one"]; got != 0 {
		t.Fatalf("expired receipt advanced boundary to %d", got)
	}

	reoffered, err := coordinator.PrepareCoverage(CoverageRequest{Kind: CoverageCheck, Limit: 1})
	if err != nil {
		t.Fatalf("reoffer PrepareCoverage: %v", err)
	}
	if reoffered.ReceiptID == first.ReceiptID || !reflect.DeepEqual(reoffered.Messages, []protocol.AgentMessageProjection{message}) {
		t.Fatalf("reoffered coverage = %+v, first=%+v", reoffered, first)
	}
}

func TestMessageCoordinatorCoverageReceiptRejectsInvalidScopeKindAndTarget(t *testing.T) {
	first := newCoverageTestCoordinator(t, InboxKey{WorkspaceID: "workspace-1", AgentID: "agent-1"})
	second := newCoverageTestCoordinator(t, InboxKey{WorkspaceID: "workspace-2", AgentID: "agent-2"})
	message := acceptCoverageTestMessage(t, first, "message-1", "channel:one", 1)

	offer, err := first.PrepareCoverage(CoverageRequest{Kind: CoverageCheck, Limit: 1})
	if err != nil {
		t.Fatalf("PrepareCoverage: %v", err)
	}
	if err := second.CommitCoverage(offer.ReceiptID); !errors.Is(err, ErrCoverageReceiptInvalid) {
		t.Fatalf("cross-scope CommitCoverage error = %v, want %v", err, ErrCoverageReceiptInvalid)
	}
	if err := first.CommitCoverage("forged-receipt"); !errors.Is(err, ErrCoverageReceiptInvalid) {
		t.Fatalf("forged CommitCoverage error = %v, want %v", err, ErrCoverageReceiptInvalid)
	}
	if _, err := first.PrepareCoverage(CoverageRequest{Kind: CoverageKind("search"), Target: "channel:one", Messages: []protocol.AgentMessageProjection{message}}); !errors.Is(err, ErrCoverageRequestInvalid) {
		t.Fatalf("invalid kind error = %v, want %v", err, ErrCoverageRequestInvalid)
	}
	mismatched := message
	mismatched.Target = "channel:other"
	if _, err := first.PrepareCoverage(CoverageRequest{Kind: CoverageRead, Target: "channel:one", ThroughSeq: 1, Messages: []protocol.AgentMessageProjection{mismatched}}); !errors.Is(err, ErrCoverageRequestInvalid) {
		t.Fatalf("target mismatch error = %v, want %v", err, ErrCoverageRequestInvalid)
	}
	if _, err := first.PrepareCoverage(CoverageRequest{Kind: CoverageHold, Target: "channel:missing", Limit: 3}); !errors.Is(err, ErrCoverageRequestInvalid) {
		t.Fatalf("missing hold target error = %v, want %v", err, ErrCoverageRequestInvalid)
	}
	if got := first.Boundaries()["channel:one"]; got != 0 {
		t.Fatalf("rejected receipt advanced boundary to %d", got)
	}
}

func TestMessageCoordinatorCoverageReceiptCapacityEvictsPreparedSafely(t *testing.T) {
	coordinator := newCoverageTestCoordinator(t, InboxKey{WorkspaceID: "workspace-1", AgentID: "agent-1"})
	coordinator.coverageCapacity = 2
	now := time.Date(2026, 8, 11, 8, 0, 0, 0, time.UTC)
	coordinator.coverageNow = func() time.Time { return now }
	acceptCoverageTestMessage(t, coordinator, "message-1", "channel:one", 1)

	receipts := make([]string, 0, 3)
	for range 3 {
		now = now.Add(time.Second)
		offer, err := coordinator.PrepareCoverage(CoverageRequest{Kind: CoverageCheck, Limit: 1})
		if err != nil {
			t.Fatalf("PrepareCoverage: %v", err)
		}
		receipts = append(receipts, offer.ReceiptID)
	}
	if err := coordinator.CommitCoverage(receipts[0]); !errors.Is(err, ErrCoverageReceiptInvalid) {
		t.Fatalf("evicted CommitCoverage error = %v, want %v", err, ErrCoverageReceiptInvalid)
	}
	if got := coordinator.Boundaries()["channel:one"]; got != 0 {
		t.Fatalf("eviction advanced boundary to %d", got)
	}
	coordinator.mu.Lock()
	receiptCount := len(coordinator.coverageReceipts)
	coordinator.mu.Unlock()
	if receiptCount != 2 {
		t.Fatalf("coverage receipt count = %d, want bounded capacity 2", receiptCount)
	}
}

func TestMessageCoordinatorReadCoverageCanPrepareWhileRecoveryIsIncomplete(t *testing.T) {
	coordinator, err := NewMessageCoordinator(
		InboxKey{WorkspaceID: "workspace-1", AgentID: "agent-1"},
		t.TempDir(),
		func(context.Context, []protocol.AgentMessageProjection) error { return nil },
		nil,
	)
	if err != nil {
		t.Fatalf("NewMessageCoordinator: %v", err)
	}
	t.Cleanup(coordinator.Close)
	message := testDelivery("message-4", "channel:one", 4, "delivery-4").Message
	offer, err := coordinator.PrepareCoverage(CoverageRequest{
		Kind: CoverageRead, Target: "channel:one", ThroughSeq: 4, Messages: []protocol.AgentMessageProjection{message},
	})
	if err != nil {
		t.Fatalf("PrepareCoverage during recovery: %v", err)
	}
	if offer.ReceiptID == "" || !reflect.DeepEqual(offer.Messages, []protocol.AgentMessageProjection{message}) {
		t.Fatalf("read coverage offer = %+v", offer)
	}
	if got := coordinator.Boundaries()["channel:one"]; got != 0 {
		t.Fatalf("read prepare advanced boundary to %d", got)
	}
	if err := coordinator.CommitCoverage(offer.ReceiptID); err != nil {
		t.Fatalf("commit read coverage: %v", err)
	}
	if got := coordinator.Boundaries()["channel:one"]; got != 4 {
		t.Fatalf("committed read boundary = %d, want 4", got)
	}
}
