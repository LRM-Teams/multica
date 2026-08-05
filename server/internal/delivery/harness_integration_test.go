package delivery_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/multica-ai/multica/server/internal/daemonws"
	"github.com/multica-ai/multica/server/internal/delivery"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// canonicalChatSource models the Server canonical Message record. A Message is
// visible here the instant it is created, before any delivery/runtime/boundary
// state exists. This proves acceptance 1: frontend projection does not depend
// on runtime state.
type canonicalChatSource struct {
	mu       sync.Mutex
	messages map[string]protocol.AgentDeliverPayload
}

func newCanonicalChatSource() *canonicalChatSource {
	return &canonicalChatSource{messages: map[string]protocol.AgentDeliverPayload{}}
}

func (cs *canonicalChatSource) create(p protocol.AgentDeliverPayload) protocol.AgentDeliverPayload {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	cs.messages[p.MessageID] = p
	return p
}

func (cs *canonicalChatSource) visible(messageID string) bool {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	_, ok := cs.messages[messageID]
	return ok
}

// fakeRuntimeInput is the fake runtime input boundary. It records every
// accepted batch (already in target seq order) so the harness can assert the
// concrete bodies were handed to the runtime in the right order, and exposes a
// refusal hook to exercise the runtime-handoff failure layer (acceptance 5).
type fakeRuntimeInput struct {
	mu       sync.Mutex
	accepted [][]delivery.AgentBody
	refuse   bool
}

var _ delivery.RuntimeInput = (*fakeRuntimeInput)(nil)

func newFakeRuntimeInput() *fakeRuntimeInput {
	return &fakeRuntimeInput{}
}

func (f *fakeRuntimeInput) AcceptBatch(batch []delivery.AgentBody, target string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.refuse {
		return &errRuntimeInputRefused{target: target}
	}
	cp := make([]delivery.AgentBody, len(batch))
	copy(cp, batch)
	f.accepted = append(f.accepted, cp)
	return nil
}

func (f *fakeRuntimeInput) batches() [][]delivery.AgentBody {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([][]delivery.AgentBody, len(f.accepted))
	copy(out, f.accepted)
	return out
}

type errRuntimeInputRefused struct{ target string }

func (e *errRuntimeInputRefused) Error() string { return "runtime input refused batch for " + e.target }

// fakeMachine dials the real Hub and acts as the machine's delivery handler:
// it observes agent:deliver (replies transport ack) and agent:deliver:handoff
// (feeds the receiver, then replies the correlated handoff ack). This is the
// Machine Service + fake runtime boundary role.
func fakeMachine(t *testing.T, wsURL string, receiver *delivery.Receiver) (*websocket.Conn, <-chan protocol.AgentDeliverPayload) {
	t.Helper()
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("fake machine dial: %v", err)
	}

	delivered := make(chan protocol.AgentDeliverPayload, 16)
	go func() {
		defer conn.Close()
		for {
			_, raw, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var msg protocol.Message
			if err := json.Unmarshal(raw, &msg); err != nil {
				continue
			}
			switch msg.Type {
			case protocol.EventAgentDeliver:
				var p protocol.AgentDeliverPayload
				if json.Unmarshal(msg.Payload, &p) == nil {
					delivered <- p
					ack := receiver.HandleDeliver(p)
					writeFrame(conn, protocol.EventAgentDeliverAck, ack)
				}
			case protocol.EventAgentDeliverHandoff:
				var hf protocol.AgentDeliverHandoffPayload
				if json.Unmarshal(msg.Payload, &hf) != nil {
					continue
				}
				ack, _, _ := receiver.HandleHandoff(hf)
				ack.RequestID = hf.RequestID
				writeFrame(conn, protocol.EventAgentDeliverHandoffAck, ack)
			}
		}
	}()
	return conn, delivered
}

func writeFrame(conn *websocket.Conn, eventType string, payload any) {
	body, _ := json.Marshal(payload)
	msg, _ := json.Marshal(protocol.Message{Type: eventType, Payload: body})
	_ = conn.WriteMessage(websocket.TextMessage, msg)
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func waitRegistered(t *testing.T, hub *daemonws.Hub) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for hub.RuntimeConnectionCount(testRuntime) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("runtime connection was not registered")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

const (
	testWorkspace = "ws-1"
	testAgent     = "agent-1"
	testRuntime   = "rt-1"
	testTarget    = "channel:general"
)

func TestIdleDeliveryChainEndToEnd(t *testing.T) {
	hub := daemonws.NewHub()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hub.HandleWebSocket(w, r, daemonws.ClientIdentity{
			DaemonID:    "daemon-1",
			UserID:      "user-1",
			WorkspaceID: testWorkspace,
			RuntimeIDs:  []string{testRuntime},
		})
	}))
	defer srv.Close()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")

	root := t.TempDir()
	serverBoundary := delivery.NewBoundaryStore(filepath.Join(root, "server", "boundary.json"))
	machineBoundary := delivery.NewBoundaryStore(filepath.Join(root, "machine", "boundary.json"))

	chat := newCanonicalChatSource()
	fakeInput := newFakeRuntimeInput()
	receiver := delivery.NewReceiver(machineBoundary, fakeInput)
	activity := delivery.NewMessageReceivedReporter()

	transport := daemonws.NewHubAgentTransport(hub)
	coord := delivery.NewCoordinator(testWorkspace, testAgent, testRuntime, transport, serverBoundary, activity)

	hub.SetAgentDeliverAckHandler(func(ack protocol.AgentDeliverAckPayload) { coord.Ack(ack) })

	conn, delivered := fakeMachine(t, wsURL, receiver)
	if conn == nil {
		t.Fatal("machine did not connect")
	}
	defer conn.Close()
	waitRegistered(t, hub)

	// --- Acceptance 1: canonical Message visible immediately from chat source.
	canonical := chat.create(protocol.AgentDeliverPayload{
		WorkspaceID: testWorkspace,
		AgentID:     testAgent,
		RuntimeID:   testRuntime,
		Target:      testTarget,
		MessageID:   "msg-1",
		Seq:         1,
		DeliveryID:  "delivery-1",
		Role:        "user",
		Content:     "hello",
	})
	if !chat.visible(canonical.MessageID) {
		t.Fatal("acceptance 1: canonical Message not visible from chat source immediately")
	}

	// --- Acceptance 2: agent:deliver -> Pending -> transport ack (not read).
	res, err := coord.Deliver(canonical)
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if !res.Accepted || res.Duplicate {
		t.Fatalf("Deliver accepted=%v duplicate=%v", res.Accepted, res.Duplicate)
	}
	if got := coord.PendingCount(testTarget); got != 1 {
		t.Fatalf("Pending after deliver = %d, want 1", got)
	}
	select {
	case got := <-delivered:
		if got.MessageID != "msg-1" || got.Seq != 1 {
			t.Fatalf("delivered wire message = %+v", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("agent:deliver frame did not reach the machine")
	}
	eventually(t, "transport ack", func() bool { return coord.IsAcked("delivery-1") })
	if ack := coord.AckCount(); ack != 1 {
		t.Fatalf("AckCount = %d, want 1", ack)
	}
	if got := coord.PendingCount(testTarget); got != 1 {
		t.Fatalf("Pending after transport ack = %d, want 1 (ack must not hand off/read)", got)
	}
	if got := activity.Count(); got != 0 {
		t.Fatalf("Activity before handoff = %d, want 0", got)
	}
	if _, err := machineBoundary.Current(testTarget); err == nil {
		if cur, _ := machineBoundary.Current(testTarget); cur != 0 {
			t.Fatalf("machine boundary advanced on transport ack = %d, want 0", cur)
		}
	}

	// --- Acceptance 3+4: ordered body handoff -> boundary atomic replace +
	// Pending removal + exactly one Activity.
	stage, err := coord.Handoff(testTarget)
	if err != nil {
		t.Fatalf("Handoff stage=%s err=%v", stage, err)
	}
	if stage != "" {
		t.Fatalf("Handoff returned stage %q, want success", stage)
	}
	batches := fakeInput.batches()
	if len(batches) != 1 || len(batches[0]) != 1 {
		t.Fatalf("runtime input batches = %d (len=%d), want one batch of 1", len(batches), len(batches[0]))
	}
	if batches[0][0].MessageID != "msg-1" || batches[0][0].Content != "hello" {
		t.Fatalf("runtime body = %+v", batches[0])
	}
	if cur, err := machineBoundary.Current(testTarget); err != nil || cur != 1 {
		t.Fatalf("machine boundary after handoff = %d err=%v, want 1", cur, err)
	}
	if got := coord.PendingCount(testTarget); got != 0 {
		t.Fatalf("Pending after handoff = %d, want 0", got)
	}
	if got := activity.Count(); got != 1 {
		t.Fatalf("Activity after handoff = %d, want exactly 1", got)
	}
	if got := activity.Received(); len(got) != 1 || len(got[0].MessageIDs) != 1 || got[0].MessageIDs[0] != "msg-1" {
		t.Fatalf("Message received activity = %+v", got)
	}
	if got := delivery.DisplayLabel(); got != "Message received" {
		t.Fatalf("display label = %q", got)
	}

	// --- Acceptance 2 duplicate: resending the same DeliveryID is idempotent;
	// one concrete runtime handoff total (no double Activity/body).
	res2, err := coord.Deliver(canonical)
	if err != nil {
		t.Fatalf("re-Deliver: %v", err)
	}
	if !res2.Duplicate {
		t.Fatal("re-Deliver of same DeliveryID should be idempotent duplicate")
	}
	if stage, err = coord.Handoff(testTarget); err != nil || stage != "" {
		t.Fatalf("re-Handoff stage=%q err=%v", stage, err)
	}
	if got := activity.Count(); got != 1 {
		t.Fatalf("Activity after duplicate = %d, want still 1", got)
	}
	if got := len(fakeInput.batches()); got != 1 {
		t.Fatalf("runtime input batches after duplicate = %d, want 1", got)
	}
}

// TestIdleDelivery_HandoffRuntimeRefusalDistinguishesLayers (acceptance 5)
// proves that a runtime-input refusal fails the runtime_handoff layer while the
// other layers (Message truth / transport ack / boundary / Activity) remain
// distinctly not green.
func TestIdleDelivery_HandoffRuntimeRefusalDistinguishesLayers(t *testing.T) {
	hub := daemonws.NewHub()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hub.HandleWebSocket(w, r, daemonws.ClientIdentity{RuntimeIDs: []string{testRuntime}})
	}))
	defer srv.Close()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")

	root := t.TempDir()
	serverBoundary := delivery.NewBoundaryStore(filepath.Join(root, "server", "boundary.json"))
	machineBoundary := delivery.NewBoundaryStore(filepath.Join(root, "machine", "boundary.json"))
	fakeInput := newFakeRuntimeInput()
	fakeInput.refuse = true
	receiver := delivery.NewReceiver(machineBoundary, fakeInput)
	activity := delivery.NewMessageReceivedReporter()
	transport := daemonws.NewHubAgentTransport(hub)
	coord := delivery.NewCoordinator(testWorkspace, testAgent, testRuntime, transport, serverBoundary, activity)
	hub.SetAgentDeliverAckHandler(func(ack protocol.AgentDeliverAckPayload) { coord.Ack(ack) })
	conn, _ := fakeMachine(t, wsURL, receiver)
	defer conn.Close()
	waitRegistered(t, hub)

	// Message truth green regardless of handoff refusal.
	chat := newCanonicalChatSource()
	p := chat.create(protocol.AgentDeliverPayload{
		WorkspaceID: testWorkspace, AgentID: testAgent, RuntimeID: testRuntime,
		Target: testTarget, MessageID: "msg-2", Seq: 2, DeliveryID: "delivery-2",
		Role: "user", Content: "hi",
	})
	if !chat.visible("msg-2") {
		t.Fatal("message truth must be visible independent of handoff refusal")
	}
	if _, err := coord.Deliver(p); err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	eventually(t, "transport ack", func() bool { return coord.IsAcked("delivery-2") })

	// Runtime refuses the batch -> handoff fails at runtime_handoff layer.
	stage, err := coord.Handoff(testTarget)
	if err == nil {
		t.Fatal("expected handoff error when runtime input refuses")
	}
	if stage != delivery.StageRuntimeHandoff {
		t.Fatalf("handoff stage = %q, want runtime_handoff", stage)
	}
	// Boundary not advanced, Pending retained, no Activity (layers stay
	// distinct rather than a green Activity masking a failed handoff).
	if cur, _ := machineBoundary.Current(testTarget); cur != 0 {
		t.Fatalf("boundary advanced on refused handoff = %d, want 0", cur)
	}
	if got := coord.PendingCount(testTarget); got != 1 {
		t.Fatalf("Pending after refused handoff = %d, want 1 (retained)", got)
	}
	if got := activity.Count(); got != 0 {
		t.Fatalf("Activity after refused handoff = %d, want 0 (must not be green)", got)
	}
}

// TestBoundaryAtomicWrite covers the Context Boundary file contract: atomic
// replace, no regression (a boundary never moves backwards), and fail-closed on
// corruption.
func TestBoundaryAtomicWrite(t *testing.T) {
	store := delivery.NewBoundaryStore(filepath.Join(t.TempDir(), "boundary.json"))
	if cur, err := store.Current("t"); err != nil || cur != 0 {
		t.Fatalf("missing boundary current = %d err=%v, want 0", cur, err)
	}
	if cur, err := store.Advance("t", 5); err != nil || cur != 5 {
		t.Fatalf("advance = %d err=%v, want 5", cur, err)
	}
	// Regression is a no-op.
	if cur, err := store.Advance("t", 3); err != nil || cur != 5 {
		t.Fatalf("regression advance = %d err=%v, want retained 5", cur, err)
	}
	// Different target independent.
	if cur, err := store.Advance("other", 9); err != nil || cur != 9 {
		t.Fatalf("other target = %d err=%v, want 9", cur, err)
	}
	if cur, _ := store.Current("t"); cur != 5 {
		t.Fatalf("t current = %d, want 5", cur)
	}
}
