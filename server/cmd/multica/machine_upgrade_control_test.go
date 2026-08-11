package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestMachineUpgradeControlTokenStaysInsideOwnerProfile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	first, err := ensureMachineUpgradeControlToken("")
	if err != nil {
		t.Fatal(err)
	}
	second, err := ensureMachineUpgradeControlToken("")
	if err != nil {
		t.Fatal(err)
	}
	if first == "" || second != first {
		t.Fatalf("control token replay = %q then %q, want one durable owner secret", first, second)
	}

	path := machineUpgradeControlTokenPath("")
	wantPath := filepath.Join(home, ".multica", "computer", machineUpgradeControlTokenFile)
	if path != wantPath {
		t.Fatalf("control token path = %q, want owner profile path %q", path, wantPath)
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
