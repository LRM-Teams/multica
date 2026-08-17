package memorygraph

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

const sampleLegacyMEMORY = `# Project Memory

Stable facts, conventions, and reusable knowledge for this project.

§
[type:preference]
[source:2026-07-01]
[evidence:mem_1,mem_2]
- The team prefers table-driven tests.

§
[type:temporary]
[expires_at:2026-07-07]
[evidence:mem_3]
- Old project blocker.

§
[type:decision]
[source:2026-07-20]
[evidence:mem_4]
- State is stored in Postgres, not SQLite.
`

func TestMigrateLegacyMEMORY(t *testing.T) {
	s := newTestStore(t)
	mdPath := filepath.Join(t.TempDir(), "MEMORY.md")
	if err := os.WriteFile(mdPath, []byte(sampleLegacyMEMORY), 0o644); err != nil {
		t.Fatal(err)
	}

	created, err := MigrateLegacyMEMORY(mdPath, s, "")
	if err != nil {
		t.Fatalf("MigrateLegacyMEMORY: %v", err)
	}
	if created != 3 {
		t.Fatalf("created = %d, want 3", created)
	}

	nodes, err := s.LoadNodes(1)
	if err != nil {
		t.Fatalf("LoadNodes: %v", err)
	}
	if len(nodes) != 3 {
		t.Fatalf("len(nodes) = %d, want 3", len(nodes))
	}
	bodies := map[string]*Node{}
	for _, n := range nodes {
		if n.Level != 0 {
			t.Errorf("node %s level = %d, want 0", n.NodeID, n.Level)
		}
		if n.Epistemic != StatusSupported {
			t.Errorf("node %s epistemic = %q, want %q", n.NodeID, n.Epistemic, StatusSupported)
		}
		if n.CreatedBy != CreatorMigration {
			t.Errorf("node %s created_by = %q, want %q", n.NodeID, n.CreatedBy, CreatorMigration)
		}
		if len(n.Tags) != 1 || n.Tags[0] != "legacy-import" {
			t.Errorf("node %s tags = %v, want [legacy-import]", n.NodeID, n.Tags)
		}
		if n.CreatedVersion != 1 || n.UpdatedVersion != 1 {
			t.Errorf("node %s versions = %d/%d, want 1/1", n.NodeID, n.CreatedVersion, n.UpdatedVersion)
		}
		bodies[n.Body] = n
	}
	for _, want := range []string{
		"The team prefers table-driven tests.",
		"Old project blocker.",
		"State is stored in Postgres, not SQLite.",
	} {
		if bodies[want] == nil {
			t.Errorf("missing imported node with body %q (have %d nodes)", want, len(nodes))
		}
	}

	// The [source:] date lands in ObservedAt.
	wantDate := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	if n := bodies["The team prefers table-driven tests."]; n != nil && !n.ObservedAt.Equal(wantDate) {
		t.Errorf("ObservedAt = %s, want %s", n.ObservedAt, wantDate)
	}

	// The manifest node count tracks the import.
	m, err := s.LoadManifest(1)
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if m.NodeCount != 3 {
		t.Fatalf("manifest NodeCount = %d, want 3", m.NodeCount)
	}
}

func TestMigrateLegacyMEMORY_MissingFile(t *testing.T) {
	s := newTestStore(t)
	created, err := MigrateLegacyMEMORY(filepath.Join(t.TempDir(), "MEMORY.md"), s, CreatorMigration)
	if err != nil {
		t.Fatalf("MigrateLegacyMEMORY: %v", err)
	}
	if created != 0 {
		t.Fatalf("created = %d, want 0", created)
	}
}

func TestMigrateLegacyMEMORY_EmptyBlocksSkipped(t *testing.T) {
	s := newTestStore(t)
	mdPath := filepath.Join(t.TempDir(), "MEMORY.md")
	content := "# Project Memory\n\n§\n[type:preference]\n[source:2026-07-01]\n\n§\n[type:decision]\n[source:2026-07-02]\n- Only real entry.\n"
	if err := os.WriteFile(mdPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	created, err := MigrateLegacyMEMORY(mdPath, s, CreatorMigration)
	if err != nil {
		t.Fatalf("MigrateLegacyMEMORY: %v", err)
	}
	if created != 1 {
		t.Fatalf("created = %d, want 1 (header and body-less block skipped)", created)
	}
}
