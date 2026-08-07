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

func TestHealthPortDeterministicOffset(t *testing.T) {
	a := HealthPort("staging")
	b := HealthPort("staging")
	if a != b {
		t.Fatalf("HealthPort not deterministic: %d vs %d", a, b)
	}
	if a <= 19514 || a > 19514+1000 {
		t.Fatalf("HealthPort(\"staging\") = %d, want in (19514, 20514]", a)
	}
	if HealthPort("staging") == HealthPort("prod") {
		t.Fatalf("distinct profiles collided on the same health port")
	}
}

func TestLayoutPathsUseProfileDir(t *testing.T) {
	t.Setenv("HOME", "/tmp/multica-computer-test-home")

	pid := PIDPath("acme")
	if want := filepath.Join("/tmp/multica-computer-test-home", ".multica", "profiles", "acme", "daemon.pid"); pid != want {
		t.Fatalf("PIDPath = %q, want %q", pid, want)
	}
	log := LogPath("acme")
	if want := filepath.Join("/tmp/multica-computer-test-home", ".multica", "profiles", "acme", "daemon.log"); log != want {
		t.Fatalf("LogPath = %q, want %q", log, want)
	}
	root := RootDir("acme")
	if want := filepath.Join("/tmp/multica-computer-test-home", ".multica", "profiles", "acme"); root != want {
		t.Fatalf("RootDir = %q, want %q", root, want)
	}
}
