package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/memorysync"
)

func TestQueueLocalMemoryChangesPersistsPortableOutbox(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "users", "member-1", "USER.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	body := "# User Preferences\n\n- 长任务开始前先反馈进度\n- This checkout is /home/alice/multica\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	state, err := loadMemorySyncState(root)
	if err != nil {
		t.Fatal(err)
	}
	outbox, err := loadMemorySyncOutbox(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := queueLocalMemoryChanges(root, "task-1", &state, &outbox); err != nil {
		t.Fatal(err)
	}
	if len(outbox.Batches) != 1 || len(outbox.Batches[0].Entries) != 1 {
		t.Fatalf("outbox=%+v", outbox)
	}
	if got := outbox.Batches[0].Entries[0].Content; got != "长任务开始前先反馈进度" {
		t.Fatalf("portable entry=%q", got)
	}
	persisted, err := loadMemorySyncOutbox(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(persisted.Batches) != 1 || persisted.Batches[0].ID == "" {
		t.Fatalf("persisted outbox=%+v", persisted)
	}
	info, err := os.Stat(filepath.Join(root, filepath.FromSlash(memorySyncOutboxRel)))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("outbox mode=%o", info.Mode().Perm())
	}
}

func TestQueueLocalMemoryChangesEmitsDeletionAfterAcknowledgedSnapshot(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "memory", "MEMORY.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("# Memory\n\n- Prefer non-destructive git operations\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	state, _ := loadMemorySyncState(root)
	outbox := memorySyncOutbox{}
	if err := queueLocalMemoryChanges(root, "task-1", &state, &outbox); err != nil {
		t.Fatal(err)
	}
	outbox.Batches = nil
	if err := saveMemorySyncOutbox(root, outbox); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("# Memory\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := queueLocalMemoryChanges(root, "task-2", &state, &outbox); err != nil {
		t.Fatal(err)
	}
	if len(outbox.Batches) != 1 || len(outbox.Batches[0].DeletedIdentityKeys) != 1 {
		t.Fatalf("deletion outbox=%+v", outbox)
	}
	want := memorysync.IdentityKey("agent", "", memorysync.KindFact, "", "Prefer non-destructive git operations")
	if got := outbox.Batches[0].DeletedIdentityKeys[0]; got != want {
		t.Fatalf("deleted identity=%q, want %q", got, want)
	}
}

func TestApplyMemoryCenterDeltaReplacesAndDeletesPortableBullets(t *testing.T) {
	root := t.TempDir()
	rel := "users/member-1/USER.md"
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("# User Preferences\n\n- 先报进度\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	identity := memorysync.IdentityKey("user", "member-1", memorysync.KindPreference, "progress_feedback", "先报进度")
	state := memorySyncState{
		LocalAtoms:   map[string]AgentMemoryCenterSyncAtom{},
		RemoteActive: map[string]AgentMemoryHydrateEntry{},
	}
	updated := AgentMemoryHydrateEntry{
		IdentityKey: identity,
		RelPath:     rel,
		Scope:       "user",
		SubjectID:   "member-1",
		Kind:        memorysync.KindPreference,
		Topic:       "progress_feedback",
		Content:     "长任务开始前先报进度并持续汇报",
		Status:      memorysync.StatusActive,
		ChangeSeq:   7,
	}
	if err := applyMemoryCenterDelta(root, &state, AgentMemoryHydrateResponse{Active: []AgentMemoryHydrateEntry{updated}, Cursor: 7}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "- 先报进度\n") || !strings.Contains(string(data), updated.Content) {
		t.Fatalf("updated file=%s", data)
	}
	if state.Cursor != 7 {
		t.Fatalf("cursor=%d", state.Cursor)
	}

	deleted := updated
	deleted.Status = memorysync.StatusSuperseded
	deleted.ChangeSeq = 8
	if err := applyMemoryCenterDelta(root, &state, AgentMemoryHydrateResponse{Deleted: []AgentMemoryHydrateEntry{deleted}, Cursor: 8}); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), updated.Content) {
		t.Fatalf("deleted content remains: %s", data)
	}
	if state.Cursor != 8 {
		t.Fatalf("cursor=%d", state.Cursor)
	}
}

func TestValidateMemorySyncResponseRequiresTombstoneProtocolAck(t *testing.T) {
	deleteBatch := memorySyncBatch{DeletedIdentityKeys: []string{"agent+fact+topic"}}
	if err := validateMemorySyncResponse(deleteBatch, AgentMemoryCenterSyncResponse{}); err == nil {
		t.Fatal("legacy response must not acknowledge a deletion batch")
	}
	if err := validateMemorySyncResponse(deleteBatch, AgentMemoryCenterSyncResponse{ProtocolVersion: 2}); err != nil {
		t.Fatalf("protocol v2 deletion ack rejected: %v", err)
	}
	entryOnly := memorySyncBatch{Entries: []AgentMemoryCenterSyncAtom{{Content: "portable"}}}
	if err := validateMemorySyncResponse(entryOnly, AgentMemoryCenterSyncResponse{}); err != nil {
		t.Fatalf("legacy server still supports entry-only sync: %v", err)
	}
}
