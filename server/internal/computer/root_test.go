package computer

import (
	"path/filepath"
	"testing"
)

func TestHealthPortDefault(t *testing.T) {
	if got := HealthPort(""); got != 19514 {
		t.Fatalf("HealthPort(\"\") = %d, want 19514", got)
	}
}

func TestLegacyProfilesResolveToOneComputerPort(t *testing.T) {
	if HealthPort("staging") != HealthPort("prod") {
		t.Fatal("legacy profiles selected different Computer health ports")
	}
}

func TestLayoutPathsUseMachineComputerDir(t *testing.T) {
	t.Setenv("HOME", "/tmp/multica-computer-test-home")

	pid := PIDPath("acme")
	if want := filepath.Join("/tmp/multica-computer-test-home", ".multica", "computer", "daemon.pid"); pid != want {
		t.Fatalf("PIDPath = %q, want %q", pid, want)
	}
	log := LogPath("acme")
	if want := filepath.Join("/tmp/multica-computer-test-home", ".multica", "computer", "daemon.log"); log != want {
		t.Fatalf("LogPath = %q, want %q", log, want)
	}
	root := RootDir("acme")
	if want := filepath.Join("/tmp/multica-computer-test-home", ".multica", "computer"); root != want {
		t.Fatalf("RootDir = %q, want %q", root, want)
	}
}
