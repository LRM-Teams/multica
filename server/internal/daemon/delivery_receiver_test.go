package daemon

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestDaemonDeliveryReceiverPersistsBoundaryAndInbox(t *testing.T) {
	root := t.TempDir()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	d := New(Config{WorkspacesRoot: root}, logger)

	rec := d.deliveryReceiver("ws-1", "agent-1")
	if rec == nil {
		t.Fatal("deliveryReceiver returned nil")
	}

	hf := protocol.AgentDeliverHandoffPayload{
		WorkspaceID: "ws-1",
		AgentID:     "agent-1",
		RuntimeID:   "rt-1",
		Target:      "dm:alice",
		Messages: []protocol.AgentDeliverPayload{
			{MessageID: "m-1", Seq: 1, Role: "user", Content: "first"},
			{MessageID: "m-2", Seq: 2, Role: "user", Content: "second"},
		},
	}
	ack, stage, err := rec.HandleHandoff(hf)
	if err != nil {
		t.Fatalf("HandleHandoff stage=%s err=%v", stage, err)
	}
	if !ack.Accepted || ack.BoundaryAfter != 2 {
		t.Fatalf("handoff ack accepted=%v boundary_after=%d, want accepted boundary 2", ack.Accepted, ack.BoundaryAfter)
	}

	// Machine-local Context Boundary persisted under the agent root.
	agentRoot := multicaAgentRoot(d.cfg, "ws-1", "agent-1")
	boundaryPath := filepath.Join(agentRoot, filepath.FromSlash(deliveryBoundaryRelPath))
	if _, err := os.Stat(boundaryPath); err != nil {
		t.Fatalf("boundary file not persisted: %v", err)
	}
	if cur, _ := rec.BoundaryCurrent("dm:alice"); cur != 2 {
		t.Fatalf("boundary current = %d, want 2", cur)
	}

	// Runtime inbox got both concrete bodies in seq order.
	inboxPath := filepath.Join(agentRoot, filepath.FromSlash(deliveryInboxRelPath))
	raw, err := os.ReadFile(inboxPath)
	if err != nil {
		t.Fatalf("inbox file not written: %v", err)
	}
	// Two newline-delimited JSON records.
	if count := countLines(string(raw)); count != 2 {
		t.Fatalf("inbox records = %d, want 2 (got: %s)", count, string(raw))
	}
}

func countLines(s string) int {
	n := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			n++
		}
	}
	return n
}
