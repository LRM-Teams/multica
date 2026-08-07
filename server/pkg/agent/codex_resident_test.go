package agent

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestNewCodexAppServerBackendImplementsResidentInterfaces(t *testing.T) {
	t.Parallel()
	b := NewCodexAppServerBackend(Config{})
	if _, ok := b.(ResidentMessageInput); !ok {
		t.Fatal("CodexAppServerBackend must implement idle Message input")
	}
	if _, ok := b.(ResidentRuntimeForceKillable); !ok {
		t.Fatal("CodexAppServerBackend must implement ResidentRuntimeForceKillable")
	}
	if _, ok := b.(ResidentRuntimeLivenessChecker); !ok {
		t.Fatal("CodexAppServerBackend must implement ResidentRuntimeLivenessChecker")
	}
	if err := b.(ResidentRuntimeForceKillable).ForceKill(); err != nil {
		t.Fatalf("ForceKill empty: %v", err)
	}
	alive, known := b.(ResidentRuntimeLivenessChecker).RuntimeAlive()
	if known || alive {
		t.Fatalf("empty process: alive=%v known=%v", alive, known)
	}
}

func TestCodexCanonicalResidentCapability(t *testing.T) {
	t.Parallel()
	if !Capabilities("codex").CanonicalResident {
		t.Fatal("codex must advertise CanonicalResident after resident PR")
	}
	if !Capabilities("codex").ForceRestart {
		t.Fatal("codex ForceRestart must derive true from resident ForceKill")
	}
}

func TestCodexResidentAcceptsCanonicalMessageAtNativeTurnBoundary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fixture is POSIX-only")
	}

	inputPath := t.TempDir() + "/turn-start.json"
	fakePath := writeFakeCodexAppServer(t, ""+
		`read line`+"\n"+
		`echo '{"jsonrpc":"2.0","id":1,"result":{}}'`+"\n"+
		`read line`+"\n"+
		`read line`+"\n"+
		`echo '{"jsonrpc":"2.0","id":2,"result":{"thread":{"id":"thr-message"}}}'`+"\n"+
		`read line`+"\n"+
		`printf '%s\n' "$line" > "$CODEX_RESIDENT_TEST_INPUT"`+"\n"+
		`echo '{"jsonrpc":"2.0","id":3,"result":{}}'`+"\n"+
		`echo '{"jsonrpc":"2.0","method":"turn/started","params":{"threadId":"thr-message","turn":{"id":"turn-message"}}}'`+"\n"+
		`echo '{"jsonrpc":"2.0","method":"error","params":{"threadId":"thr-message","error":{"message":"temporary reconnect"},"willRetry":true}}'`+"\n"+
		`echo '{"jsonrpc":"2.0","method":"item/completed","params":{"threadId":"thr-message","item":{"type":"agentMessage","id":"msg-message","text":"acknowledged"}}}'`+"\n"+
		`echo '{"jsonrpc":"2.0","method":"turn/completed","params":{"threadId":"thr-message","turn":{"id":"turn-message","status":"completed"}}}'`+"\n"+
		`while read line; do :; done`+"\n")

	backend := NewCodexAppServerBackend(Config{
		ExecutablePath: fakePath,
		Logger:         slog.Default(),
		Env:            map[string]string{"CODEX_RESIDENT_TEST_INPUT": inputPath},
	})
	defer backend.Close()

	acceptance, err := backend.AcceptMessageBatch(context.Background(), []ResidentMessage{{
		ID: "message-9", Target: "dm:user-1", Seq: 9, Content: "leader message",
		PartsJSON: json.RawMessage(`[{"type":"text","text":"leader message"}]`),
	}})
	if err != nil {
		t.Fatalf("AcceptMessageBatch: %v", err)
	}
	select {
	case err := <-acceptance.Done:
		if err != nil {
			t.Fatalf("canonical Message turn: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for canonical Message turn completion")
	}
	var sawReconnect, sawText bool
	for message := range acceptance.Messages {
		sawReconnect = sawReconnect || message.Type == MessageStatus && message.Status == "reconnecting"
		sawText = sawText || message.Type == MessageText && message.Content == "acknowledged"
	}
	if !sawReconnect || !sawText {
		t.Fatalf("resident lifecycle events reconnect=%v text=%v", sawReconnect, sawText)
	}

	raw, err := os.ReadFile(inputPath)
	if err != nil {
		t.Fatalf("read native turn/start request: %v", err)
	}
	if !strings.Contains(string(raw), "leader message") {
		t.Fatalf("native turn/start omitted canonical Message body: %s", raw)
	}
}

func TestCodexResidentSteersContentFreePendingNoticeIntoBusyTurn(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fixture is POSIX-only")
	}

	steerPath := t.TempDir() + "/turn-steer.json"
	fakePath := writeFakeCodexAppServer(t, ""+
		`read line`+"\n"+
		`echo '{"jsonrpc":"2.0","id":1,"result":{}}'`+"\n"+
		`read line`+"\n"+
		`read line`+"\n"+
		`echo '{"jsonrpc":"2.0","id":2,"result":{"thread":{"id":"thr-busy"}}}'`+"\n"+
		`read line`+"\n"+
		`echo '{"jsonrpc":"2.0","id":3,"result":{}}'`+"\n"+
		`echo '{"jsonrpc":"2.0","method":"turn/started","params":{"threadId":"thr-busy","turn":{"id":"turn-busy"}}}'`+"\n"+
		`read line`+"\n"+
		`printf '%s\n' "$line" > "$CODEX_RESIDENT_TEST_STEER"`+"\n"+
		`echo '{"jsonrpc":"2.0","id":4,"result":{}}'`+"\n"+
		`echo '{"jsonrpc":"2.0","method":"item/completed","params":{"threadId":"thr-busy","item":{"type":"agentMessage","id":"msg-busy","text":"done"}}}'`+"\n"+
		`echo '{"jsonrpc":"2.0","method":"turn/completed","params":{"threadId":"thr-busy","turn":{"id":"turn-busy","status":"completed"}}}'`+"\n"+
		`while read line; do :; done`+"\n")

	backend := NewCodexAppServerBackend(Config{
		ExecutablePath: fakePath,
		Logger:         slog.Default(),
		Env:            map[string]string{"CODEX_RESIDENT_TEST_STEER": steerPath},
	})
	defer backend.Close()
	session, err := backend.Execute(context.Background(), "concrete private body", ExecOptions{Timeout: 2 * time.Second})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	select {
	case message := <-session.Messages:
		if message.Type != MessageStatus || message.Status != "running" {
			t.Fatalf("expected active turn status, got %#v", message)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for active Codex turn")
	}

	err = backend.AcceptPendingNotice(context.Background(), ResidentPendingNotice{
		TotalPending: 2,
		ChangedTargets: []ResidentPendingTarget{{
			Target:       "channel:general",
			PendingCount: 2,
		}},
	})
	if err != nil {
		t.Fatalf("AcceptPendingNotice: %v", err)
	}
	for range session.Messages {
	}
	result := <-session.Result
	if result.Status != "completed" {
		t.Fatalf("busy turn status=%q error=%q", result.Status, result.Error)
	}

	raw, err := os.ReadFile(steerPath)
	if err != nil {
		t.Fatalf("read native turn/steer request: %v", err)
	}
	request := string(raw)
	for _, want := range []string{`"method":"turn/steer"`, `"expectedTurnId":"turn-busy"`, "Content-free Message Notice", `channel:general`} {
		if !strings.Contains(request, want) {
			t.Fatalf("native turn/steer request omitted %q: %s", want, request)
		}
	}
	if strings.Contains(request, "concrete private body") {
		t.Fatalf("Pending Notice leaked a canonical Message body: %s", request)
	}
}

func TestCodexResidentAllowsInitialProgressAfterFormerFirstTurnWatchdog(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fixture is POSIX-only")
	}

	fakePath := writeFakeCodexAppServer(t, ""+
		`read line`+"\n"+
		`echo '{"jsonrpc":"2.0","id":1,"result":{}}'`+"\n"+
		`read line`+"\n"+
		`read line`+"\n"+
		`echo '{"jsonrpc":"2.0","id":2,"result":{"thread":{"id":"thr-resident-delayed"}}}'`+"\n"+
		`read line`+"\n"+
		`echo '{"jsonrpc":"2.0","id":3,"result":{}}'`+"\n"+
		`echo '{"jsonrpc":"2.0","method":"turn/started","params":{"threadId":"thr-resident-delayed","turn":{"id":"turn-resident-delayed"}}}'`+"\n"+
		`sleep 0.45`+"\n"+
		`echo '{"jsonrpc":"2.0","method":"item/completed","params":{"threadId":"thr-resident-delayed","item":{"type":"agentMessage","id":"msg-resident-delayed","text":"resident delayed but healthy"}}}'`+"\n"+
		`sleep 0.02`+"\n"+
		`echo '{"jsonrpc":"2.0","method":"turn/completed","params":{"threadId":"thr-resident-delayed","turn":{"id":"turn-resident-delayed","status":"completed"}}}'`+"\n"+
		`sleep 5`+"\n")

	backend := NewCodexAppServerBackend(Config{ExecutablePath: fakePath, Logger: slog.Default()})
	defer backend.Close()
	session, err := backend.Execute(context.Background(), "prompt", ExecOptions{
		Timeout:                   5 * time.Second,
		SemanticInactivityTimeout: 500 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("execute resident codex: %v", err)
	}
	go func() {
		for range session.Messages {
		}
	}()
	result, ok := <-session.Result
	if !ok {
		t.Fatal("result channel closed without a value")
	}
	if result.Status != "completed" {
		t.Fatalf("expected completed delayed resident turn, got status=%q error=%q", result.Status, result.Error)
	}
	if result.Output != "resident delayed but healthy" {
		t.Fatalf("expected delayed resident output, got %q", result.Output)
	}
}
