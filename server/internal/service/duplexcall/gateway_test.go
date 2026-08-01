package duplexcall

import (
	"context"
	"encoding/base64"
	"sync"
	"testing"

	"github.com/multica-ai/multica/server/internal/integrations/doubaodialog"
)

func TestMapProviderEventEmitsFEContract(t *testing.T) {
	var mu sync.Mutex
	var got []ServerEvent
	session := &Session{
		CallID: "call-1",
		emit: func(event ServerEvent) error {
			mu.Lock()
			got = append(got, event)
			mu.Unlock()
			return nil
		},
	}
	bridge, err := doubaodialog.NewMulticaToolBridge(
		&doubaodialog.RecordingExecutor{Result: "ok"},
		&noopSender{},
	)
	if err != nil {
		t.Fatal(err)
	}
	session.bridge = bridge

	if err := MapProviderEventForTest(session, context.Background(), doubaodialog.ServerEvent{
		Type:      doubaodialog.EventSessionCreated,
		SessionID: "dlg-1",
	}); err != nil {
		t.Fatal(err)
	}
	pcm := []byte{1, 2, 3, 4}
	if err := MapProviderEventForTest(session, context.Background(), doubaodialog.ServerEvent{
		Type:  doubaodialog.EventOutputAudioDelta,
		Audio: pcm,
	}); err != nil {
		t.Fatal(err)
	}
	if err := MapProviderEventForTest(session, context.Background(), doubaodialog.ServerEvent{
		Type:       doubaodialog.EventASRCompleted,
		Transcript: "你好",
	}); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 3 {
		t.Fatalf("events=%d want 3: %#v", len(got), got)
	}
	if got[0].Type != ServerReady || got[0].SessionID != "dlg-1" || got[0].SampleRate != 24000 {
		t.Fatalf("ready event = %#v", got[0])
	}
	if got[1].Type != ServerAudioDelta || got[1].Audio != base64.StdEncoding.EncodeToString(pcm) {
		t.Fatalf("audio event = %#v", got[1])
	}
	if got[2].Type != ServerASR || got[2].Phase != "completed" || got[2].Transcript != "你好" {
		t.Fatalf("asr event = %#v", got[2])
	}
}

func TestParseClientEvent(t *testing.T) {
	event, err := ParseClientEvent([]byte(`{"type":"client.audio.commit"}`))
	if err != nil {
		t.Fatal(err)
	}
	if event.Type != ClientAudioCommit {
		t.Fatalf("type=%q", event.Type)
	}
}

type noopSender struct{}

func (noopSender) SendFunctionCallOutputs(context.Context, []doubaodialog.FunctionCallOutput) error {
	return nil
}

func (noopSender) CancelResponse(context.Context) error { return nil }
