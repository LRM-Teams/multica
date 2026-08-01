package doubaodialog

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestConfigFromEnvDefaultsAndValidation(t *testing.T) {
	t.Setenv(EnvAPIKey, "")
	t.Setenv(EnvAppID, "")
	t.Setenv(EnvAccessKey, "")
	t.Setenv(EnvEndpoint, "")
	cfg := ConfigFromEnv()
	if cfg.Endpoint != DefaultDuplexEndpoint || cfg.Model != DefaultModel {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
	if err := cfg.ValidateForDial(); err == nil || !strings.Contains(err.Error(), EnvAPIKey) {
		t.Fatalf("expected missing API key error, got %v", err)
	}

	t.Setenv(EnvAPIKey, "dialog-key")
	cfg = ConfigFromEnv()
	if err := cfg.ValidateForDial(); err != nil {
		t.Fatal(err)
	}
	if !cfg.IsDuplex() {
		t.Fatal("default endpoint should be duplex")
	}

	classic := Config{
		AppID:     "app",
		AccessKey: "ak",
		Endpoint:  ClassicDialogueEndpoint,
	}
	if classic.IsDuplex() {
		t.Fatal("classic endpoint must not report duplex")
	}
	if err := classic.ValidateForDial(); err != nil {
		t.Fatal(err)
	}
	if _, err := New(classic); err == nil || !strings.Contains(err.Error(), "not implemented") {
		t.Fatalf("expected classic not-implemented error, got %v", err)
	}
}

func TestParseServerEventFunctionCallAndAudio(t *testing.T) {
	raw := []byte(`{
		"type":"response.function_call_arguments.done",
		"items":[{"call_id":"call-1","name":"delegate_work_to_multica_agent","arguments":"{\"request\":\"开 issue\"}"}]
	}`)
	event, err := ParseServerEvent(raw)
	if err != nil {
		t.Fatal(err)
	}
	if event.Type != EventFunctionCallArgumentsDone ||
		len(event.FunctionCalls) != 1 ||
		event.FunctionCalls[0].CallID != "call-1" {
		t.Fatalf("unexpected event: %+v", event)
	}

	pcm := []byte{1, 2, 3, 4}
	audioEvent, err := ParseServerEvent([]byte(
		`{"type":"response.output_audio.delta","audio":"` + base64.StdEncoding.EncodeToString(pcm) + `"}`,
	))
	if err != nil {
		t.Fatal(err)
	}
	if string(audioEvent.Audio) != string(pcm) {
		t.Fatalf("audio = %v", audioEvent.Audio)
	}
}

func TestOpenSessionCreatesAndBridgesFunctionCall(t *testing.T) {
	const apiKey = "test-dialog-key"
	upgrader := websocket.Upgrader{}
	var createdSessionID string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Api-Key") != apiKey {
			t.Errorf("missing/wrong API key header")
		}
		conn, err := upgrader.Upgrade(w, r, http.Header{"X-Tt-Logid": []string{"log-spike-1"}})
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer conn.Close()

		_, raw, err := conn.ReadMessage()
		if err != nil {
			t.Errorf("read create: %v", err)
			return
		}
		var create ClientEvent
		if err := json.Unmarshal(raw, &create); err != nil {
			t.Errorf("unmarshal create: %v", err)
			return
		}
		if create.Type != EventSessionCreate ||
			create.Session == nil ||
			create.Session.Model != DefaultModel ||
			strings.TrimSpace(create.Session.ID) == "" ||
			len(create.Session.Tools) != 1 ||
			create.Session.Tools[0].Name != MulticaDelegateToolName {
			t.Errorf("unexpected create payload: %s", raw)
			return
		}
		createdSessionID = create.Session.ID
		createdPayload, _ := json.Marshal(map[string]any{
			"type":    EventSessionCreated,
			"session": map[string]string{"id": create.Session.ID},
		})
		_ = conn.WriteMessage(websocket.TextMessage, createdPayload)
		_ = conn.WriteMessage(websocket.TextMessage, []byte(
			`{"type":"response.function_call_arguments.done","items":[{"call_id":"call-42","name":"delegate_work_to_multica_agent","arguments":"{\"request\":\"开一个修复登录的 issue\"}"}]}`,
		))

		_, toolRaw, err := conn.ReadMessage()
		if err != nil {
			t.Errorf("read tool result: %v", err)
			return
		}
		var toolEvent ClientEvent
		if err := json.Unmarshal(toolRaw, &toolEvent); err != nil {
			t.Errorf("unmarshal tool: %v", err)
			return
		}
		if toolEvent.Type != EventConversationCreate ||
			len(toolEvent.Items) != 1 ||
			toolEvent.Items[0].CallID != "call-42" ||
			toolEvent.Items[0].Role != "tool" ||
			len(toolEvent.Items[0].Content) != 1 ||
			toolEvent.Items[0].Content[0].Text != "已创建 issue。" {
			t.Errorf("unexpected tool result: %s", toolRaw)
		}
	}))
	defer server.Close()

	client, err := New(Config{
		APIKey:   apiKey,
		Endpoint: "ws" + strings.TrimPrefix(server.URL, "http"),
		Timeout:  3 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	sessionCfg := DefaultSessionConfig(DefaultModel, DefaultVoice, DefaultDialogInstructions(), []Tool{MulticaDelegateTool()})
	session, err := client.OpenSession(context.Background(), sessionCfg)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close(context.Background())
	if session.LogID() != "log-spike-1" {
		t.Fatalf("logID = %q", session.LogID())
	}

	created, err := session.Recv(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if created.Type != EventSessionCreated || created.SessionID == "" || created.SessionID != createdSessionID {
		t.Fatalf("created = %+v want session id %q", created, createdSessionID)
	}

	executor := &RecordingExecutor{Result: "已创建 issue。"}
	bridge, err := NewMulticaToolBridge(executor, session)
	if err != nil {
		t.Fatal(err)
	}
	fc, err := session.Recv(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	handled, err := bridge.HandleServerEvent(context.Background(), fc)
	if err != nil || !handled {
		t.Fatalf("bridge err=%v handled=%v", err, handled)
	}
	if len(executor.Calls) != 1 {
		t.Fatalf("calls = %#v", executor.Calls)
	}
}

func TestCancelResponseEvent(t *testing.T) {
	payload, err := EncodeClientEvent(ClientEvent{Type: EventResponseCancel, EventID: "e1"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(payload), EventResponseCancel) {
		t.Fatalf("payload = %s", payload)
	}
}
