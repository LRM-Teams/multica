package daemon

import (
	"context"
	"errors"
	"testing"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestInboxACKUsesExactRevisionAndIsIdempotent(t *testing.T) {
	c, err := NewMessageCoordinator(InboxKey{WorkspaceID: "workspace-1", AgentID: "agent-1"}, t.TempDir(), func(context.Context, []protocol.AgentMessageProjection) error { return nil }, nil)
	if err != nil {
		t.Fatal(err)
	}
	message := testDelivery("message-1", "channel:one", 1, "delivery-1")
	if _, err := c.Accept(context.Background(), message); err != nil {
		t.Fatal(err)
	}
	revision := c.InboxRevision()
	if err := c.AcknowledgeInbox("channel:one", 1, revision); err != nil {
		t.Fatal(err)
	}
	if err := c.AcknowledgeInbox("channel:one", 1, revision); err != nil {
		t.Fatal(err)
	}
	if got := c.Boundaries()["channel:one"]; got != 1 {
		t.Fatalf("boundary=%d", got)
	}
}

func TestInboxACKRejectsStaleRevision(t *testing.T) {
	c, err := NewMessageCoordinator(InboxKey{WorkspaceID: "workspace-1", AgentID: "agent-1"}, t.TempDir(), func(context.Context, []protocol.AgentMessageProjection) error { return nil }, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Accept(context.Background(), testDelivery("message-1", "channel:one", 1, "delivery-1")); err != nil {
		t.Fatal(err)
	}
	old := c.InboxRevision()
	if _, err := c.Accept(context.Background(), testDelivery("message-2", "channel:one", 2, "delivery-2")); err != nil {
		t.Fatal(err)
	}
	if err := c.AcknowledgeInbox("channel:one", 1, old); err == nil || !errors.Is(err, errStaleInboxRevision) {
		t.Fatalf("stale ACK error=%v", err)
	}
}
