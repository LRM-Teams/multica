package duplexcall

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/integrations/doubaodialog"
)

func TestMapProviderEventEmitsFEContract(t *testing.T) {
	var mu sync.Mutex
	var got []ServerEvent
	const wantWelcomeMessage = "你好，江先生。我是贝克汉姆。你想聊什么？"
	welcomeMessage := ""
	session := &Session{
		CallID: "call-1",
		emit: func(event ServerEvent) error {
			mu.Lock()
			got = append(got, event)
			mu.Unlock()
			return nil
		},
		speak: func(_ context.Context, text string) error {
			welcomeMessage = text
			return nil
		},
		welcomeMessage: wantWelcomeMessage,
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
	if welcomeMessage != wantWelcomeMessage {
		t.Fatalf("welcome message = %q, want %q", welcomeMessage, wantWelcomeMessage)
	}
	if got[1].Type != ServerAudioDelta || got[1].Audio != base64.StdEncoding.EncodeToString(pcm) {
		t.Fatalf("audio event = %#v", got[1])
	}
	if got[2].Type != ServerASR || got[2].Phase != "completed" || got[2].Transcript != "你好" {
		t.Fatalf("asr event = %#v", got[2])
	}
}

func TestMapProviderEventReportsWelcomeFailure(t *testing.T) {
	wantErr := errors.New("upstream write failed")
	session := &Session{
		CallID: "call-1",
		emit: func(ServerEvent) error {
			return nil
		},
		speak: func(context.Context, string) error {
			return wantErr
		},
		welcomeMessage: "Hello, Jiang. This is Beckham. What would you like to discuss?",
	}

	err := MapProviderEventForTest(session, context.Background(), doubaodialog.ServerEvent{
		Type:      doubaodialog.EventSessionCreated,
		SessionID: "dlg-1",
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want wrapped %v", err, wantErr)
	}
}

func TestGatewayStartUsesPendingAgentInstructions(t *testing.T) {
	client := &recordingDialogClient{err: errors.New("stop after capture")}
	gateway, err := NewGateway(client, doubaodialog.Config{
		APIKey:   "test-key",
		Endpoint: doubaodialog.DefaultDuplexEndpoint,
		Model:    "model-1",
		Voice:    "voice-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	const (
		welcome      = "你好，我是贝克汉姆。"
		instructions = "base rules\n\nAgent identity\nYou are 贝克汉姆.\nRecent DM\n修登录"
	)
	gateway.MarkPending("call-ctx", welcome, instructions)

	_, err = gateway.Start(
		context.Background(),
		"call-ctx",
		&doubaodialog.RecordingExecutor{Result: "ok"},
		func(ServerEvent) error { return nil },
	)
	if err == nil || !strings.Contains(err.Error(), "stop after capture") {
		t.Fatalf("start error = %v, want capture stop", err)
	}
	if client.cfg.Instructions != instructions {
		t.Fatalf("dialog instructions = %q, want agent context injected", client.cfg.Instructions)
	}
	if client.cfg.Model != "model-1" || client.cfg.Audio.Output.Voice != "voice-1" {
		t.Fatalf("unexpected session config: %+v", client.cfg)
	}
	if len(client.cfg.Tools) != 4 {
		t.Fatalf("tools=%d want default dialog tools", len(client.cfg.Tools))
	}
}

type recordingDialogClient struct {
	cfg doubaodialog.SessionConfig
	err error
}

func (c *recordingDialogClient) OpenSession(_ context.Context, session doubaodialog.SessionConfig) (*doubaodialog.Session, error) {
	c.cfg = session
	return nil, c.err
}

func TestFunctionCallRunsAsyncWithoutBlockingPump(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	web := &blockingWebToolkit{
		started: started,
		release: release,
		result:  "天气晴。",
	}
	sender := &asyncRecordingSender{}
	bridge, err := doubaodialog.NewMulticaToolBridgeWithWeb(
		&doubaodialog.RecordingExecutor{Result: "ok"},
		web,
		sender,
	)
	if err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var toolStatuses []string
	session := &Session{
		CallID: "call-async",
		bridge: bridge,
		emit: func(event ServerEvent) error {
			if event.Type == ServerTool {
				mu.Lock()
				toolStatuses = append(toolStatuses, event.Status)
				mu.Unlock()
			}
			return nil
		},
	}

	done := make(chan error, 1)
	go func() {
		done <- MapProviderEventForTest(session, context.Background(), doubaodialog.ServerEvent{
			Type: doubaodialog.EventFunctionCallArgumentsDone,
			FunctionCalls: []doubaodialog.FunctionCall{{
				CallID:    "call-search",
				Name:      doubaodialog.WebSearchToolName,
				Arguments: `{"query":"北京天气"}`,
			}},
		})
	}()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("web_search did not start")
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("map provider event returned early error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("function-call handling blocked the provider event loop")
	}

	close(release)
	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		statuses := append([]string(nil), toolStatuses...)
		mu.Unlock()
		hasDone := false
		for _, status := range statuses {
			if status == "done" {
				hasDone = true
				break
			}
		}
		if hasDone {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("tool statuses = %#v, want done", statuses)
		}
		time.Sleep(20 * time.Millisecond)
	}

	outputs := sender.snapshot()
	if len(outputs) != 1 || outputs[0].Output != "天气晴。" {
		t.Fatalf("tool outputs = %#v", outputs)
	}
}

type blockingWebToolkit struct {
	started chan struct{}
	release chan struct{}
	result  string
	once    sync.Once
}

func (t *blockingWebToolkit) Search(ctx context.Context, query string) (string, error) {
	t.once.Do(func() { close(t.started) })
	select {
	case <-t.release:
		return t.result, nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func (t *blockingWebToolkit) Fetch(context.Context, string) (string, error) {
	return "", errors.New("fetch not used")
}

type asyncRecordingSender struct {
	mu      sync.Mutex
	outputs []doubaodialog.FunctionCallOutput
}

func (s *asyncRecordingSender) SendFunctionCallOutputs(_ context.Context, outputs []doubaodialog.FunctionCallOutput) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.outputs = append(s.outputs, outputs...)
	return nil
}

func (s *asyncRecordingSender) CancelResponse(context.Context) error { return nil }

func (s *asyncRecordingSender) snapshot() []doubaodialog.FunctionCallOutput {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]doubaodialog.FunctionCallOutput, len(s.outputs))
	copy(out, s.outputs)
	return out
}

type noopSender struct{}

func (noopSender) SendFunctionCallOutputs(context.Context, []doubaodialog.FunctionCallOutput) error {
	return nil
}

func (noopSender) CancelResponse(context.Context) error { return nil }
