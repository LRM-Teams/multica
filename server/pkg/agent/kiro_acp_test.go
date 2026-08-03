package agent

import (
	"testing"
)

func TestNewKiroACPBackendImplementsResidentInterfaces(t *testing.T) {
	t.Parallel()
	b := NewKiroACPBackend(Config{})
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

func TestKiroCanonicalResidentCapability(t *testing.T) {
	t.Parallel()
	if !Capabilities("kiro").CanonicalResident {
		t.Fatal("kiro must advertise CanonicalResident after resident PR")
	}
	if !Capabilities("kiro").ForceRestart {
		t.Fatal("kiro ForceRestart must derive true from resident ForceKill")
	}
}
