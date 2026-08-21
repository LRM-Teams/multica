package memoryorigin

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTaintAndSkipLine(t *testing.T) {
	got := Taint("- Prefer TypeScript", Owner)
	if !IsInjected(got) {
		t.Fatalf("taint missing: %q", got)
	}
	if !SkipLine(got) {
		t.Fatal("tainted line must be skipped by L2")
	}
	if SkipLine("- Prefer TypeScript") {
		t.Fatal("plain fact must not be skipped")
	}
	if Taint(got, Agent) != got {
		t.Fatal("double-taint")
	}
}

func TestClassifyScope(t *testing.T) {
	if ClassifyScope("user", "Current user preferences") != Owner {
		t.Fatal("user")
	}
	if ClassifyScope("workspace", "Graph memory recall") != Untrusted {
		t.Fatal("graph")
	}
	if ClassifyScope("system", "Recent memory curation") != System {
		t.Fatal("system")
	}
	if ClassifyScope("agent", "MEMORY.md") != Agent {
		t.Fatal("agent")
	}
}

func TestSkipDurableCandidates(t *testing.T) {
	if !SkipDurableCandidates("attention_probe") || !SkipDurableCandidates("protocol_turn") {
		t.Fatal("restricted profiles")
	}
	if SkipDurableCandidates("full") || SkipDurableCandidates("") {
		t.Fatal("full profile")
	}
}

func TestNoticeRoundTrip(t *testing.T) {
	root := t.TempDir()
	if err := WriteNotice(root, 2); err != nil {
		t.Fatal(err)
	}
	got, ok := ConsumeNotice(root)
	if !ok || got.ChangedFiles != 2 {
		t.Fatalf("%+v ok=%v", got, ok)
	}
	if _, ok := ConsumeNotice(root); ok {
		t.Fatal("notice must be one-shot")
	}
	if _, err := os.Stat(filepath.Join(root, NoticeRel)); !os.IsNotExist(err) {
		t.Fatal("file should be gone")
	}
}
