package memorygraph

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func TestGCWithPinnedKeepsPinnedAndCurrent(t *testing.T) {
	s := newTestStore(t)
	parent := 1
	for i := 0; i < 4; i++ {
		v, err := s.CreateVersionFrom(parent, CreatorConsolidator)
		if err != nil {
			t.Fatalf("CreateVersionFrom: %v", err)
		}
		parent = v
	}
	// Versions 1..5 exist; current is v2 (outside the keep window). Pin v1,
	// which is also outside the keep window, and leave v3 unpinned.
	if err := s.SwitchCurrent(2); err != nil {
		t.Fatalf("SwitchCurrent: %v", err)
	}
	if err := s.GCWithPinned(2, map[int]bool{1: true}); err != nil {
		t.Fatalf("GCWithPinned: %v", err)
	}
	versions, err := s.ListVersions()
	if err != nil {
		t.Fatalf("ListVersions: %v", err)
	}
	want := []int{1, 2, 4, 5}
	if !slices.Equal(versions, want) {
		t.Fatalf("ListVersions = %v; want %v (pinned 1, current 2, keep window 4+5)", versions, want)
	}
	if v, _ := s.CurrentVersion(); v != 2 {
		t.Fatalf("CurrentVersion = %d; want 2", v)
	}
}

func TestGCLockBusyAndStaleReclaim(t *testing.T) {
	s := newTestStore(t)
	parent := 1
	for i := 0; i < 4; i++ {
		v, err := s.CreateVersionFrom(parent, CreatorConsolidator)
		if err != nil {
			t.Fatalf("CreateVersionFrom: %v", err)
		}
		parent = v
	}
	if err := s.SwitchCurrent(2); err != nil {
		t.Fatalf("SwitchCurrent: %v", err)
	}
	before, err := s.ListVersions()
	if err != nil {
		t.Fatalf("ListVersions: %v", err)
	}

	writeLock := func(ts time.Time) {
		t.Helper()
		body, err := json.Marshal(struct {
			PID int       `json:"pid"`
			TS  time.Time `json:"ts"`
		}{PID: 1, TS: ts.UTC()})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(s.Root, "gc.lock"), body, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	writeLock(time.Now())
	err = s.GCWithPinned(2, nil)
	if !errors.Is(err, ErrGCLockBusy) {
		t.Fatalf("fresh lock: err = %v, want ErrGCLockBusy", err)
	}
	afterBusy, err := s.ListVersions()
	if err != nil {
		t.Fatalf("ListVersions after busy: %v", err)
	}
	if !slices.Equal(afterBusy, before) {
		t.Fatalf("busy GC deleted versions: got %v, want %v", afterBusy, before)
	}

	writeLock(time.Now().Add(-time.Hour))
	if err := s.GCWithPinned(2, nil); err != nil {
		t.Fatalf("stale lock reclaim: %v", err)
	}
	versions, err := s.ListVersions()
	if err != nil {
		t.Fatalf("ListVersions after reclaim: %v", err)
	}
	want := []int{2, 4, 5}
	if !slices.Equal(versions, want) {
		t.Fatalf("ListVersions after stale reclaim = %v; want %v", versions, want)
	}
}

func TestGCPartialDirCollectible(t *testing.T) {
	s := newTestStore(t)
	parent := 1
	for i := 0; i < 2; i++ {
		v, err := s.CreateVersionFrom(parent, CreatorConsolidator)
		if err != nil {
			t.Fatalf("CreateVersionFrom: %v", err)
		}
		parent = v
	}
	if err := s.SwitchCurrent(3); err != nil {
		t.Fatalf("SwitchCurrent: %v", err)
	}

	partial := filepath.Join(s.Root, "versions", "v0", "nodes")
	if err := os.MkdirAll(partial, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(partial, "orphan.md"), []byte("no manifest"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := s.GCWithPinned(1, nil); err != nil {
		t.Fatalf("GCWithPinned partial dir: %v", err)
	}
	versions, err := s.ListVersions()
	if err != nil {
		t.Fatalf("ListVersions: %v", err)
	}
	if slices.Contains(versions, 0) {
		t.Fatalf("partial v0 still present: %v", versions)
	}

	if err := s.GCWithPinned(1, nil); err != nil {
		t.Fatalf("second GCWithPinned: %v", err)
	}
}

func TestOpenSnapshotImmutableWatermarkedSources(t *testing.T) {
	s := newTestStore(t)
	if err := s.SaveNode(1, &Node{NodeID: "n1", Body: "hello", CreatedBy: CreatorIngester, CreatedVersion: 1}); err != nil {
		t.Fatalf("SaveNode: %v", err)
	}
	if _, err := s.AppendSourceSegment("src-a", "source A"); err != nil {
		t.Fatalf("append src-a: %v", err)
	}
	v2, err := s.CreateVersionFrom(1, "test")
	if err != nil {
		t.Fatalf("CreateVersionFrom: %v", err)
	}
	if v2 != 2 {
		t.Fatalf("CreateVersionFrom = %d; want 2", v2)
	}
	if _, err := s.AppendSourceSegment("src-b", "source B"); err != nil {
		t.Fatalf("append src-b: %v", err)
	}

	snap, err := s.OpenSnapshot(2)
	if err != nil {
		t.Fatalf("OpenSnapshot(2): %v", err)
	}
	if snap == nil || snap.Manifest == nil || snap.Graph == nil {
		t.Fatal("OpenSnapshot returned a nil snapshot, manifest, or graph")
	}
	if snap.Manifest.SourceWatermark != 1 {
		t.Fatalf("manifest watermark = %d, want 1", snap.Manifest.SourceWatermark)
	}
	if snap.Graph.Node("n1") == nil {
		t.Fatal("snapshot graph missing copied node n1")
	}
	if len(snap.SourceNodes) != 1 || snap.SourceNodes[0].NodeID != "src-a" {
		t.Fatalf("source nodes = %+v, want exactly src-a (seq<=1)", snap.SourceNodes)
	}

	if _, err := s.OpenSnapshot(99); err == nil {
		t.Fatal("OpenSnapshot(missing) must fail")
	}

	if err := snap.Graph.AddNode(&Node{NodeID: "mutated", Body: "should not persist"}); err != nil {
		t.Fatalf("AddNode on snapshot: %v", err)
	}
	again, err := s.OpenSnapshot(2)
	if err != nil {
		t.Fatalf("re-load OpenSnapshot: %v", err)
	}
	if again.Graph.Node("mutated") != nil {
		t.Fatal("mutating the snapshot graph changed a subsequent load")
	}
	if again.Graph.Node("n1") == nil {
		t.Fatal("re-load lost n1")
	}
}
