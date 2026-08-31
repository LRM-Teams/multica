package daemon

import (
	"context"
	"testing"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestInboxReadDoesNotRetireAndItemACKIsIdempotent(t *testing.T) {
	c, err := NewMessageCoordinator(InboxKey{WorkspaceID: "workspace-1", AgentID: "agent-1"}, t.TempDir(), func(context.Context, []protocol.AgentMessageProjection) error { return nil }, nil)
	if err != nil {
		t.Fatal(err)
	}
	message := testDelivery("message-1", "channel:one", 1, "delivery-1")
	if _, err := c.Accept(context.Background(), message); err != nil {
		t.Fatal(err)
	}
	items := c.MessageItemsSnapshot()
	if len(items) != 1 {
		t.Fatalf("read projection items=%d, want 1", len(items))
	}
	if !c.inboxStore.Ack(items[0].ItemID) || c.inboxStore.Ack(items[0].ItemID) {
		t.Fatal("item ACK was not exactly-once")
	}
	if got := len(c.MessageItemsSnapshot()); got != 0 {
		t.Fatalf("retired items=%d, want 0", got)
	}
}

func TestInboxACKRejectsUnknownItemID(t *testing.T) {
	c, err := NewMessageCoordinator(InboxKey{WorkspaceID: "workspace-1", AgentID: "agent-1"}, t.TempDir(), func(context.Context, []protocol.AgentMessageProjection) error { return nil }, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Accept(context.Background(), testDelivery("message-1", "channel:one", 1, "delivery-1")); err != nil {
		t.Fatal(err)
	}
	if c.inboxStore.Ack("message:missing:1") {
		t.Fatal("unknown item id was acknowledged")
	}
}
