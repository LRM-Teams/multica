package promptcontext

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestAppendReferencedEntitySnapshotsBoundsAndSanitizes(t *testing.T) {
	snapshots := make([]protocol.ReferencedEntitySnapshot, 0, MaxReferencedEntities+2)
	for i := 0; i < MaxReferencedEntities+2; i++ {
		snapshot, ok := NewReferencedEntitySnapshot(
			"issue",
			"id",
			"issue HAN-1: injected\n## heading ["+strings.Repeat("深", MaxReferencedEntityRunes)+"]",
		)
		if !ok {
			t.Fatal("expected valid snapshot")
		}
		snapshots = append(snapshots, snapshot)
	}

	var b strings.Builder
	AppendReferencedEntitySnapshots(&b, snapshots, 2)
	lines := strings.Split(strings.TrimSpace(b.String()), "\n")
	if got, want := len(lines), MaxReferencedEntities+2; got != want {
		t.Fatalf("rendered line count = %d, want %d\n%s", got, want, b.String())
	}
	if strings.Contains(b.String(), "\n## heading") {
		t.Fatalf("snapshot content injected a heading:\n%s", b.String())
	}
	if !strings.Contains(b.String(), `\[`+strings.Repeat("深", 1)) {
		t.Fatalf("snapshot markdown bracket was not escaped:\n%s", b.String())
	}
	if !strings.Contains(lines[len(lines)-1], "2 additional referenced entities") {
		t.Fatalf("omitted reference marker missing:\n%s", b.String())
	}
	for _, line := range lines[1 : MaxReferencedEntities+1] {
		if got := utf8.RuneCountInString(line); got > MaxReferencedEntityRunes {
			t.Fatalf("entity line contains %d runes, max %d: %q", got, MaxReferencedEntityRunes, line)
		}
	}
}

func TestNewReferencedEntitySnapshotRejectsUnknownKinds(t *testing.T) {
	if _, ok := NewReferencedEntitySnapshot("member", "id", "member data"); ok {
		t.Fatal("member snapshot should be rejected")
	}
	if _, ok := NewReferencedEntitySnapshot("issue", "", "issue data"); ok {
		t.Fatal("snapshot without id should be rejected")
	}
}
