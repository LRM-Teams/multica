package daemonws

import (
	"fmt"
	"testing"
)

func TestClientOutboxAbsorbsAgentReconcileBurstWithoutDisconnectSemantics(t *testing.T) {
	outbox := newClientOutbox()
	for i := 0; i < 128; i++ {
		if !outbox.enqueue([]byte(fmt.Sprintf("agent:start:%d", i)), false) {
			t.Fatalf("enqueue %d rejected", i)
		}
	}
	for i := 0; i < 128; i++ {
		frame, ok := outbox.pop()
		if !ok || string(frame) != fmt.Sprintf("agent:start:%d", i) {
			t.Fatalf("pop %d = %q, %v", i, frame, ok)
		}
	}
}

func TestClientOutboxPrioritizesRareComputerControl(t *testing.T) {
	outbox := newClientOutbox()
	if !outbox.enqueue([]byte("agent:start"), false) || !outbox.enqueue([]byte("computer:upgrade"), true) {
		t.Fatal("enqueue failed")
	}
	frame, ok := outbox.pop()
	if !ok || string(frame) != "computer:upgrade" {
		t.Fatalf("first frame = %q, %v", frame, ok)
	}
}

func TestClientOutboxCapacityRejectsFrameWithoutClosingQueue(t *testing.T) {
	outbox := newClientOutbox()
	for i := 0; i < clientOutboxMaxFrames; i++ {
		if !outbox.enqueue([]byte("agent:start"), false) {
			t.Fatalf("enqueue %d rejected", i)
		}
	}
	if outbox.enqueue([]byte("overflow"), false) {
		t.Fatal("overflow frame unexpectedly accepted")
	}
	if _, ok := outbox.pop(); !ok {
		t.Fatal("capacity rejection closed or cleared the outbox")
	}
	if !outbox.enqueue([]byte("after-pop"), false) {
		t.Fatal("outbox remained unavailable after capacity was released")
	}
}

func TestClientOutboxByteBudgetRejectsFrameWithoutClosingQueue(t *testing.T) {
	outbox := newClientOutbox()
	if outbox.enqueue(make([]byte, clientOutboxMaxBytes+1), false) {
		t.Fatal("oversized frame unexpectedly accepted")
	}
	if !outbox.enqueue([]byte("agent:start"), false) {
		t.Fatal("byte-budget rejection closed the outbox")
	}
}
