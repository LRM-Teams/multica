package agent

import (
	"testing"
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
	if Capabilities("claude").CanonicalResident {
		t.Fatal("claude must not be CanonicalResident until ACP shell exists")
	}
}
