package memoryflush

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func TestBeforeCompactionWritesSignalWhenNoDurableWrites(t *testing.T) {
	root := t.TempDir()
	body := []byte("- old rule\n")
	write(t, filepath.Join(root, "memory", "MEMORY.md"), string(body))
	sum := sha256Hex(body)
	write(t, filepath.Join(root, ".multica", "memory-write-hashes.json"), `{"files":{"memory/MEMORY.md":"`+sum+`"}}`)

	got := BeforeCompaction(root)
	if got.Error != "" {
		t.Fatalf("error = %q", got.Error)
	}
	if !got.WroteSignal {
		t.Fatalf("expected compaction_flush signal, got %+v", got)
	}
	data, err := os.ReadFile(filepath.Join(root, "sync_queue", "memory-signal.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"action":"compaction_flush"`) {
		t.Fatalf("signal file = %s", data)
	}

	again := BeforeCompaction(root)
	if again.WroteSignal {
		t.Fatal("second flush must not duplicate the signal")
	}
}

func TestBeforeCompactionSkipsWhenDurableWritePresent(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "memory", "MEMORY.md"), "- new rule\n")

	got := BeforeCompaction(root)
	if got.WroteSignal {
		t.Fatalf("wrote signal despite durable write: %+v", got)
	}
	if got.DurableWrites == 0 {
		t.Fatal("expected a durable write against empty snapshot")
	}
	if _, err := os.Stat(filepath.Join(root, "sync_queue", "memory-signal.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("signal file should be absent, err=%v", err)
	}
}

func TestBeforeCompactionNeverFailsEmptyRoot(t *testing.T) {
	got := BeforeCompaction("")
	if got.WroteSignal || got.Error != "" {
		t.Fatalf("%+v", got)
	}
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
