package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestNewKiroACPBackendImplementsResidentInterfaces(t *testing.T) {
	t.Parallel()
	b := NewKiroACPBackend(Config{})
	if _, ok := b.(ResidentMessageInput); !ok {
		t.Fatal("KiroACPBackend must implement ResidentMessageInput")
	}
	if _, ok := b.(ResidentRuntimeForceKillable); !ok {
		t.Fatal("KiroACPBackend must implement ResidentRuntimeForceKillable")
	}
	if _, ok := b.(ResidentRuntimeLivenessChecker); !ok {
		t.Fatal("KiroACPBackend must implement ResidentRuntimeLivenessChecker")
	}
	// ForceKill with no process is a no-op success.
	if err := b.(ResidentRuntimeForceKillable).ForceKill(); err != nil {
		t.Fatalf("ForceKill empty: %v", err)
	}
	alive, known := b.(ResidentRuntimeLivenessChecker).RuntimeAlive()
	if known || alive {
		t.Fatalf("empty process: alive=%v known=%v", alive, known)
	}
}

func TestKiroAcceptsCanonicalIdleMessageAtNativePromptBoundary(t *testing.T) {
	writer := &requestCaptureWriter{lines: make(chan []byte, 1)}
	client := &acpClient{stdin: writer, pending: make(map[int]*pendingRPC)}
	process := &kiroACPProcess{client: client, sessionID: "session-kiro"}
	backend := newKiroACPBackend(Config{})
	backend.process.Store(process)

	type result struct {
		acceptance ResidentMessageAcceptance
		err        error
	}
	resultCh := make(chan result, 1)
	go func() {
		acceptance, err := backend.AcceptMessageBatch(context.Background(), []ResidentMessage{{
			ID:          "message-1",
			Target:      "channel:internal-id",
			ReplyTarget: "#general",
			Seq:         7,
			Content:     "please reply",
			PartsJSON:   json.RawMessage(`[{"type":"text","text":"please reply"}]`),
		}})
		resultCh <- result{acceptance: acceptance, err: err}
	}()

	request := decodeCapturedRequest(t, writer.lines)
	if request["method"] != "session/prompt" {
		t.Fatalf("Kiro idle Message method = %#v, want session/prompt", request["method"])
	}
	params, ok := request["params"].(map[string]any)
	if !ok {
		t.Fatalf("Kiro idle Message params = %T, want object", request["params"])
	}
	if _, ok := params["content"]; !ok {
		t.Fatalf("Kiro idle Message params omit Kiro content field: %#v", params)
	}
	if _, ok := params["prompt"]; !ok {
		t.Fatalf("Kiro idle Message params omit standard ACP prompt field: %#v", params)
	}
	raw, err := json.Marshal(request["params"])
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Canonical Messages received while the runtime was idle", "message-1", "#general", "please reply"} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("Kiro idle Message prompt %s does not contain %q", raw, want)
		}
	}
	if strings.Contains(string(raw), "channel:internal-id") {
		t.Fatalf("Kiro idle Message prompt exposed internal target: %s", raw)
	}

	var got result
	select {
	case got = <-resultCh:
		if got.err != nil {
			t.Fatalf("AcceptMessageBatch: %v", got.err)
		}
	case <-time.After(time.Second):
		t.Fatal("Kiro did not acknowledge the native prompt write")
	}
	select {
	case err := <-got.acceptance.Done:
		t.Fatalf("Kiro idle Message turn completed before native response: %v", err)
	case <-time.After(30 * time.Millisecond):
	}

	respondToNoticeRequest(t, request, client.handleLine)
	for range got.acceptance.Messages {
	}
	if err := <-got.acceptance.Done; err != nil {
		t.Fatalf("Kiro idle Message turn: %v", err)
	}
}

func TestKiroRejectsIdleMessageDuringActiveTurn(t *testing.T) {
	backend := newKiroACPBackend(Config{})
	backend.running.Store(true)
	_, err := backend.AcceptMessageBatch(context.Background(), []ResidentMessage{{
		ID: "message-1", Target: "channel:internal-id", ReplyTarget: "#general", Seq: 1, Content: "hello",
	}})
	if err == nil || !strings.Contains(err.Error(), ErrKiroACPTurnBusy.Error()) {
		t.Fatalf("overlapping idle Message error = %v, want busy", err)
	}
}

func TestKiroCanonicalResidentCapability(t *testing.T) {
	t.Parallel()
	if !Capabilities("kiro").CanonicalResident {
		t.Fatal("kiro must advertise CanonicalResident after resident PR")
	}
	if !Capabilities("kiro").ForceRestart {
		t.Fatal("kiro ForceRestart must derive true from resident ForceKill")
	}
}
