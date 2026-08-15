package computer

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLaunchBinaryFallsBackToExecutableWhenInstallPathMissing(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv(LaunchBinaryEnv, "")

	got, err := LaunchBinary()
	if err != nil {
		t.Fatalf("LaunchBinary: %v", err)
	}
	want, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	if got != want {
		t.Fatalf("LaunchBinary = %q, want unchanged executable %q", got, want)
	}
}

func TestLaunchBinaryPrefersLaunchBinEnvOverInstallPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	install := filepath.Join(home, ".local", "bin", "multica")
	if err := os.MkdirAll(filepath.Dir(install), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(install, []byte("installed"), 0o755); err != nil {
		t.Fatal(err)
	}
	checkout := filepath.Join(home, "server", "bin", "multica")
	if err := os.MkdirAll(filepath.Dir(checkout), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(checkout, []byte("checkout"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(LaunchBinaryEnv, checkout)

	got, err := LaunchBinary()
	if err != nil {
		t.Fatalf("LaunchBinary: %v", err)
	}
	if got != checkout {
		t.Fatalf("LaunchBinary = %q, want checkout binary %q", got, checkout)
	}
}

func TestLaunchBinaryUsesInstallPathNotVersionStore(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(LaunchBinaryEnv, "")
	launcher := filepath.Join(home, ".local", "bin", "multica")
	if err := os.MkdirAll(filepath.Dir(launcher), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(launcher, []byte("path-computer"), 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := LaunchBinary()
	if err != nil {
		t.Fatalf("LaunchBinary: %v", err)
	}
	if got != launcher {
		t.Fatalf("LaunchBinary = %q, want install path %q", got, launcher)
	}
}
