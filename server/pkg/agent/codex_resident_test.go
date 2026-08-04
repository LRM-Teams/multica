package agent

import (
	"context"
	"log/slog"
	"runtime"
	"testing"
	"time"
)

func TestNewCodexAppServerBackendImplementsResidentInterfaces(t *testing.T) {
	t.Parallel()
	b := NewCodexAppServerBackend(Config{})
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
