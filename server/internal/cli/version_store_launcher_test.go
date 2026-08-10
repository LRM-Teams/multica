package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestVersionStoreLauncherPathIsStableAndRejectsVersionBinary(t *testing.T) {
	root := t.TempDir()
	store, err := NewVersionStore(filepath.Join(root, "store"), "linux", func(context.Context, string, string) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	launcher := filepath.Join(root, "bin", "multica")
	if err := os.MkdirAll(filepath.Dir(launcher), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(launcher, []byte("launcher"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := store.RememberLauncherPath(launcher); err != nil {
		t.Fatal(err)
	}
	got, ok, err := store.LauncherPath()
	if err != nil || !ok || got != launcher {
		t.Fatalf("LauncherPath = %q, %v, %v", got, ok, err)
	}

	binary := []byte("version")
	digest := sha256.Sum256(binary)
	staged, err := store.StageBinary(context.Background(), "v1.2.3", binary, hex.EncodeToString(digest[:]), 0o755)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RememberLauncherPath(staged.BinaryPath); err == nil {
		t.Fatal("version-specific binary was accepted as stable launcher")
	}
	got, ok, err = store.LauncherPath()
	if err != nil || !ok || got != launcher {
		t.Fatalf("rejected update changed launcher = %q, %v, %v", got, ok, err)
	}
}
