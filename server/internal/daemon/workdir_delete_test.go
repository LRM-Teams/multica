package daemon

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestHandleDeleteDirRequest_ConfinedAndIdempotent(t *testing.T) {
	root := t.TempDir()
	d := &Daemon{
		cfg:    Config{WorkspacesRoot: root},
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	writes := make(chan []byte, 1)

	targetRel := filepath.ToSlash(filepath.Join("ws", "deadbeef-dead-beef-dead-beefdeadbeef"))
	abs := filepath.Join(root, filepath.FromSlash(targetRel))
	if err := os.MkdirAll(abs, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(abs, "note.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	d.handleDeleteDirRequest(protocol.DeleteWorkdirDirRequestPayload{
		RequestID: "req-1",
		RelPath:   targetRel,
	}, writes)
	select {
	case <-writes:
	case <-time.After(time.Second):
		t.Fatal("expected delete response")
	}
	if _, err := os.Stat(abs); !os.IsNotExist(err) {
		t.Fatalf("dir should be gone, stat err=%v", err)
	}

	writes = make(chan []byte, 1)
	d.handleDeleteDirRequest(protocol.DeleteWorkdirDirRequestPayload{
		RequestID: "req-2",
		RelPath:   "../outside",
	}, writes)
	select {
	case <-writes:
	case <-time.After(time.Second):
		t.Fatal("expected escape response")
	}

	writes = make(chan []byte, 1)
	d.handleDeleteDirRequest(protocol.DeleteWorkdirDirRequestPayload{
		RequestID: "req-3",
		RelPath:   targetRel,
	}, writes)
	select {
	case frame := <-writes:
		if len(frame) == 0 {
			t.Fatal("empty frame")
		}
	case <-time.After(time.Second):
		t.Fatal("expected missing response")
	}
}
