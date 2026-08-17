package memorygraph

import (
	"slices"
	"testing"
)

func TestOpLogAppendAndRead(t *testing.T) {
	s := newTestStore(t)
	l := NewOpLogger(s)

	if err := l.Append(1, CreatorConsolidator, "add_node", "n1", map[string]any{"body_len": 12}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := l.Append(1, CreatorConsolidator, "add_edge", "h1", nil); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := l.Append(2, CreatorIngester, "add_node", "n9", nil); err != nil {
		t.Fatalf("Append: %v", err)
	}

	entries, err := l.Read(1)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("Read = %d entries; want 2", len(entries))
	}
	if entries[0].Seq != 1 || entries[1].Seq != 2 {
		t.Fatalf("seqs = %d, %d; want 1, 2", entries[0].Seq, entries[1].Seq)
	}
	if entries[0].Version != 1 || entries[0].Actor != CreatorConsolidator ||
		entries[0].Op != "add_node" || entries[0].Target != "n1" ||
		entries[0].Detail["body_len"] != float64(12) {
		t.Fatalf("entry = %+v", entries[0])
	}
	if entries[0].Timestamp.IsZero() {
		t.Fatal("timestamp not set")
	}

	other, err := l.Read(2)
	if err != nil || len(other) != 1 || other[0].Seq != 1 {
		t.Fatalf("Read(2) = %v, %v", other, err)
	}
	if missing, err := l.Read(99); err != nil || len(missing) != 0 {
		t.Fatalf("Read(99) = %v, %v; want empty", missing, err)
	}
}

func TestOpLogSeqMonotonicAcrossReopen(t *testing.T) {
	s := newTestStore(t)
	l := NewOpLogger(s)
	for i := 0; i < 3; i++ {
		if err := l.Append(1, "ttt-run-1", "update_node", "n1", nil); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}

	// A fresh logger on the same store must continue the sequence.
	reopened := NewOpLogger(s)
	if err := reopened.Append(1, "ttt-run-2", "delete_edge", "h1", nil); err != nil {
		t.Fatalf("Append after reopen: %v", err)
	}
	entries, err := reopened.Read(1)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	seqs := make([]int, len(entries))
	for i, e := range entries {
		seqs[i] = e.Seq
	}
	if !slices.Equal(seqs, []int{1, 2, 3, 4}) {
		t.Fatalf("seqs = %v; want [1 2 3 4]", seqs)
	}
	if entries[3].Actor != "ttt-run-2" {
		t.Fatalf("last entry = %+v", entries[3])
	}
}
