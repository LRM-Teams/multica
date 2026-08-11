package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/agentworkspace"
)

func TestMessageDraftStoreLoadsCurrentFormat(t *testing.T) {
	root := t.TempDir()
	store := NewMessageDraftStore(root)
	key := DraftKey{WorkspaceID: "workspace-1", AgentID: "agent-1", Target: "#one"}
	path := filepath.Join(agentworkspace.Root(root, key.WorkspaceID, key.AgentID), messageDraftsFileName)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	fixture, err := os.ReadFile("testdata/message-draft-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, fixture, 0o600); err != nil {
		t.Fatal(err)
	}

	draft, found, err := store.Load(key, time.Date(2026, time.August, 10, 12, 5, 0, 0, time.UTC))
	if err != nil || !found {
		t.Fatalf("Load() found=%v err=%v", found, err)
	}
	if draft.Target != key.Target || draft.ContextTarget != "channel:one" || draft.Content != "hello" ||
		draft.IdempotencyKey != "client-message-1" || draft.SeenUpToSeq != 7 || draft.HoldCount != 0 || draft.Kind != "note" {
		t.Fatalf("loaded Draft = %+v", draft)
	}
	if want := []string{"attachment-2", "attachment-1"}; !equalStrings(draft.AttachmentIDs, want) {
		t.Fatalf("attachment IDs = %v, want %v", draft.AttachmentIDs, want)
	}
}

func TestMessageDraftStoreIsolatesWorkspaceAgentAndTargetKeys(t *testing.T) {
	now := time.Date(2026, time.August, 10, 12, 0, 0, 0, time.UTC)
	store := newMessageDraftStore(t.TempDir(), func() time.Time { return now })
	keys := []DraftKey{
		{WorkspaceID: "workspace-1", AgentID: "agent-1", Target: "#same"},
		{WorkspaceID: "workspace-1", AgentID: "agent-2", Target: "#same"},
		{WorkspaceID: "workspace-2", AgentID: "agent-1", Target: "#same"},
		{WorkspaceID: "workspace-1", AgentID: "agent-1", Target: "#other"},
	}
	for i, key := range keys {
		if err := store.Save(key, MessageDraft{Content: key.WorkspaceID + key.AgentID + key.Target, IdempotencyKey: "key-" + string(rune('a'+i))}); err != nil {
			t.Fatalf("Save(%+v): %v", key, err)
		}
	}
	for _, key := range keys {
		draft, found, err := store.Load(key, now.Add(time.Minute))
		if err != nil || !found || draft.Content != key.WorkspaceID+key.AgentID+key.Target {
			t.Fatalf("Load(%+v) = %+v found=%v err=%v", key, draft, found, err)
		}
	}
	for _, key := range []DraftKey{
		{WorkspaceID: "../workspace", AgentID: "agent-1", Target: "#one"},
		{WorkspaceID: "workspace-1", AgentID: "../agent", Target: "#one"},
		{WorkspaceID: "workspace-1", AgentID: "agent-1"},
	} {
		if err := store.Save(key, MessageDraft{IdempotencyKey: "invalid"}); err == nil {
			t.Fatalf("Save accepted invalid key %+v", key)
		}
	}
}

func TestMessageDraftStoreExpiresAndRemovesDraft(t *testing.T) {
	now := time.Date(2026, time.August, 10, 12, 0, 0, 0, time.UTC)
	root := t.TempDir()
	store := newMessageDraftStore(root, func() time.Time { return now })
	key := DraftKey{WorkspaceID: "workspace-1", AgentID: "agent-1", Target: "#one"}
	if err := store.Save(key, MessageDraft{Content: "hello", IdempotencyKey: "key-1"}); err != nil {
		t.Fatal(err)
	}
	if _, found, err := store.Load(key, now.Add(messageDraftTTL)); err != nil || found {
		t.Fatalf("expired Load() found=%v err=%v", found, err)
	}
	raw, err := os.ReadFile(filepath.Join(agentworkspace.Root(root, key.WorkspaceID, key.AgentID), messageDraftsFileName))
	if err != nil {
		t.Fatal(err)
	}
	var state messageDraftState
	if err := json.Unmarshal(raw, &state); err != nil {
		t.Fatalf("decode state after expiry: %v", err)
	}
	if _, found := state.Drafts[key.Target]; found {
		t.Fatalf("expired Draft remained in state: %+v", state)
	}
}

func TestMessageDraftStoreClearDoesNotDeleteReplacement(t *testing.T) {
	now := time.Date(2026, time.August, 10, 12, 0, 0, 0, time.UTC)
	store := newMessageDraftStore(t.TempDir(), func() time.Time { return now })
	key := DraftKey{WorkspaceID: "workspace-1", AgentID: "agent-1", Target: "#one"}
	if err := store.Save(key, MessageDraft{Content: "first", IdempotencyKey: "key-1"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(key, MessageDraft{Content: "second", IdempotencyKey: "key-2"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Clear(key, "key-1"); err != nil {
		t.Fatal(err)
	}
	if draft, found, err := store.Load(key, now.Add(time.Minute)); err != nil || !found || draft.IdempotencyKey != "key-2" {
		t.Fatalf("replacement after stale Clear() = %+v found=%v err=%v", draft, found, err)
	}
	if err := store.Clear(key, "key-2"); err != nil {
		t.Fatal(err)
	}
	if _, found, err := store.Load(key, now.Add(time.Minute)); err != nil || found {
		t.Fatalf("matching Clear() found=%v err=%v", found, err)
	}
}

func TestMessageDraftStoreAtomicWriteFailurePreservesPriorDraft(t *testing.T) {
	now := time.Date(2026, time.August, 10, 12, 0, 0, 0, time.UTC)
	store := newMessageDraftStore(t.TempDir(), func() time.Time { return now })
	key := DraftKey{WorkspaceID: "workspace-1", AgentID: "agent-1", Target: "#one"}
	if err := store.Save(key, MessageDraft{Content: "first", IdempotencyKey: "key-1"}); err != nil {
		t.Fatal(err)
	}
	writeErr := errors.New("injected atomic write failure")
	writeState := store.writeState
	store.writeState = func(string, messageDraftState) error { return writeErr }
	if err := store.Save(key, MessageDraft{Content: "second", IdempotencyKey: "key-2"}); !errors.Is(err, writeErr) {
		t.Fatalf("Save() error = %v, want %v", err, writeErr)
	}
	store.writeState = writeState
	draft, found, err := store.Load(key, now.Add(time.Minute))
	if err != nil || !found || draft.Content != "first" || draft.IdempotencyKey != "key-1" {
		t.Fatalf("prior Draft after failed write = %+v found=%v err=%v", draft, found, err)
	}
}

func TestMessageDraftStoreConcurrentSavesRemainReadable(t *testing.T) {
	now := time.Date(2026, time.August, 10, 12, 0, 0, 0, time.UTC)
	store := newMessageDraftStore(t.TempDir(), func() time.Time { return now })
	const count = 64
	errs := make(chan error, count)
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			key := DraftKey{WorkspaceID: "workspace-1", AgentID: "agent-1", Target: fmt.Sprintf("target-%02d", i)}
			errs <- store.Save(key, MessageDraft{
				Content: "content-" + key.Target, AttachmentIDs: []string{"second", "first"},
				IdempotencyKey: "key-" + key.Target, HoldCount: i,
			})
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent Save(): %v", err)
		}
	}
	for i := 0; i < count; i++ {
		key := DraftKey{WorkspaceID: "workspace-1", AgentID: "agent-1", Target: fmt.Sprintf("target-%02d", i)}
		draft, found, err := store.Load(key, now.Add(time.Minute))
		if err != nil || !found || draft.HoldCount != i || !equalStrings(draft.AttachmentIDs, []string{"second", "first"}) {
			t.Fatalf("Load(%q) = %+v found=%v err=%v", key.Target, draft, found, err)
		}
	}
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
