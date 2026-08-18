package computer

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestResidentLeaseIsExclusiveAndIgnoresStaleUnlockedFile(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, residentLockFile), []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	first, err := AcquireResidentLease(context.Background(), root)
	if err != nil {
		t.Fatalf("acquire over stale unlocked file: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := AcquireResidentLease(ctx, root); err == nil {
		t.Fatal("second resident acquired the same machine-wide lease")
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	third, err := AcquireResidentLease(context.Background(), root)
	if err != nil {
		t.Fatalf("reacquire after release: %v", err)
	}
	_ = third.Close()
}
