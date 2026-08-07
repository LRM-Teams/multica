package computer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/multica-ai/multica/server/internal/cli"
)

func TestLaunchBinaryNoActiveVersionFallsBackToExecutable(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

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

func TestLaunchBinaryPrefersVersionStoreActive(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	storeRoot := filepath.Join(home, ".local", "share", "multica")
	store, err := cli.NewVersionStore(storeRoot, "linux", func(context.Context, string, string) error { return nil })
	if err != nil {
		t.Fatalf("NewVersionStore: %v", err)
	}
	data := []byte("multica-v0.3.88")
	sum := sha256.Sum256(data)
	staged, err := store.StageBinary(context.Background(), "v0.3.88", data, hex.EncodeToString(sum[:]), 0o755)
	if err != nil {
		t.Fatalf("StageBinary: %v", err)
	}
	if _, err := store.CompareAndSwapActivation(context.Background(), 0, "v0.3.88"); err != nil {
		t.Fatalf("CompareAndSwapActivation: %v", err)
	}

	got, err := LaunchBinary()
	if err != nil {
		t.Fatalf("LaunchBinary: %v", err)
	}
	if got != staged.BinaryPath {
		t.Fatalf("LaunchBinary = %q, want staged Active path %q", got, staged.BinaryPath)
	}
}
