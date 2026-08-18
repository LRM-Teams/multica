package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestCursorImplementsCanonicalIdleMessageInput(t *testing.T) {
	backend := NewCursorACPBackend(Config{})
	defer backend.Close()
	if _, ok := backend.(ResidentMessageInput); !ok {
		t.Fatal("Cursor resident backend cannot accept an idle canonical Message batch")
	}
}

func TestCursorAcceptsCanonicalIdleMessageAtNativePromptBoundary(t *testing.T) {
	writer := &requestCaptureWriter{lines: make(chan []byte, 1)}
	client := &acpClient{stdin: writer, pending: make(map[int]*pendingRPC)}
	process := &cursorACPProcess{client: client, sessionID: "session-cursor"}
	backend := newCursorACPBackend(Config{})
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
		t.Fatalf("Cursor idle Message method = %#v, want session/prompt", request["method"])
	}
	raw, err := json.Marshal(request["params"])
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Canonical Messages received while the runtime was idle", "message-1", "#general", "please reply"} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("Cursor idle Message prompt %s does not contain %q", raw, want)
		}
	}
	if strings.Contains(string(raw), "channel:internal-id") {
		t.Fatalf("Cursor idle Message prompt exposed internal target: %s", raw)
	}

	var got result
	select {
	case got = <-resultCh:
		if got.err != nil {
			t.Fatalf("AcceptMessageBatch: %v", got.err)
		}
	case <-time.After(time.Second):
		t.Fatal("Cursor did not acknowledge the native prompt write")
	}
	select {
	case err := <-got.acceptance.Done:
		t.Fatalf("Cursor idle Message turn completed before native response: %v", err)
	case <-time.After(30 * time.Millisecond):
	}

	respondToNoticeRequest(t, request, client.handleLine)
	for range got.acceptance.Messages {
	}
	if err := <-got.acceptance.Done; err != nil {
		t.Fatalf("Cursor idle Message turn: %v", err)
	}
}
