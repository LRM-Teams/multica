package computer

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestControlTokenStaysInsideOwnerComputerRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	first, err := EnsureControlToken("")
	if err != nil {
		t.Fatal(err)
	}
	second, err := EnsureControlToken("")
	if err != nil {
		t.Fatal(err)
	}
	if first == "" || second != first {
		t.Fatalf("control token replay = %q then %q, want one durable owner secret", first, second)
	}

	path := ControlTokenPath("")
	wantPath := filepath.Join(home, ".multica", "computer", controlTokenFile)
	if path != wantPath {
		t.Fatalf("control token path = %q, want Computer root path %q", path, wantPath)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Fatalf("control token mode = %#o, want 0600", got)
		}
	}
}
