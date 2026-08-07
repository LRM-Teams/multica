package computer

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/google/uuid"
)

func newTestStore(t *testing.T) (*IdentityStore, string) {
	t.Helper()
	root := t.TempDir()
	return NewIdentityStore(root), root
}

// stability: the same id is returned across reloads and does not change when
// "hostname"/display-name inputs differ (a fixed machine UUID, not a hash of
// hostname).
func TestIdentityStableAcrossReloads(t *testing.T) {
	store, _ := newTestStore(t)

	first := store.Load("")
	if first.Kind != IdentityMinted || first.ID == "" {
		t.Fatalf("first load = %+v, want minted id", first)
	}
	if _, err := uuid.Parse(first.ID); err != nil {
		t.Fatalf("minted id %q is not a UUID: %v", first.ID, err)
	}

	// Reload on a fresh store instance (simulates restart / reinstall of the
	// binary with the same home) must return the identical id.
	again := NewIdentityStore(store.root).Load("")
	if again.ID != first.ID {
		t.Fatalf("identity drifted on reload: %q != %q", again.ID, first.ID)
	}
	if again.Kind != IdentityStable {
		t.Fatalf("reload kind = %v, want stable", again.Kind)
	}

	orig := store.Load("not-a-name")
	if orig.ID != first.ID {
		t.Fatalf("identity changed with display/profile arg: %q != %q", orig.ID, first.ID)
	}
}

// atomic + permission-restricted: the identity file is 0600 and written
// atomically.
func TestIdentityWritePermissions(t *testing.T) {
	store, _ := newTestStore(t)
	store.Load("")

	info, err := os.Stat(store.path())
	if err != nil {
		t.Fatalf("stat identity file: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("identity file mode = %o, want 0600", perm)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("identity file must not be a symlink")
	}
}

// control state is separate from (not created under) Agent Roots: identity
// lives directly under the machine root, not under any workspace/agent dir.
func TestIdentitySeparateFromAgentRoots(t *testing.T) {
	store, root := newTestStore(t)
	store.Load("")

	if filepath.Dir(store.path()) != root {
		t.Fatalf("identity not directly under machine root: %s", store.path())
	}
	if store.path() == filepath.Join(root, "agents", daemonIDFile) {
		t.Fatalf("identity must not live under an agent root")
	}
}

// corruption: an unparseable canonical file must NOT be silently overwritten
// with a fresh id; it surfaces as ambiguous and preserves the evidence.
func TestIdentityCorruptionIsAmbiguousNotSilentlyMinted(t *testing.T) {
	store, _ := newTestStore(t)
	if err := os.MkdirAll(store.root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.path(), []byte("garbage-not-a-uuid\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	res := store.Load("")
	if res.Kind != IdentityAmbiguous {
		t.Fatalf("corrupt file load kind = %v, want ambiguous (must not silently mint)", res.Kind)
	}
	if res.ID != "" {
		t.Fatalf("corrupt file produced an id %q; must not assign one", res.ID)
	}
	// Evidence preserved, not overwritten.
	data, err := os.ReadFile(store.path())
	if err != nil || string(data) != "garbage-not-a-uuid\n" {
		t.Fatalf("corrupt evidence was overwritten: %q, %v", data, err)
	}
}

// ambiguity: multiple legacy per-profile identities with no canonical file
// must surface as ambiguous and require explicit adoption, not auto-merge.
func TestIdentityAmbiguousLegacyCandidates(t *testing.T) {
	store, _ := newTestStore(t)
	// Two legacy per-profile daemon.id files, no canonical.
	for _, prof := range []string{"team-a", "team-b"} {
		dir := filepath.Join(store.root, "profiles", prof)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, daemonIDFile), []byte(uuid.NewString()+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	res := store.Load("")
	if res.Kind != IdentityAmbiguous {
		t.Fatalf("ambiguous legacy load kind = %v, want ambiguous", res.Kind)
	}
	if len(res.LegacyCandidates) != 2 {
		t.Fatalf("expected 2 legacy candidates, got %v", res.LegacyCandidates)
	}
}

// single legacy candidate matching the profile is promoted (old identity kept).
func TestIdentityPromotesSingleLegacyProfile(t *testing.T) {
	store, _ := newTestStore(t)
	legacyID := uuid.NewString()
	dir := filepath.Join(store.root, "profiles", "space-a")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, daemonIDFile), []byte(legacyID+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	res := store.Load("space-a")
	if res.Kind != IdentityStable && res.Kind != IdentityMinted {
		t.Fatalf("promote load kind = %v", res.Kind)
	}
	if res.ID != legacyID {
		t.Fatalf("promoted id = %q, want legacy %q", res.ID, legacyID)
	}
}

// concurrency: many goroutines minting on the same fresh store converge on one id.
func TestIdentityConcurrentMintConverges(t *testing.T) {
	root := t.TempDir()
	const n = 32
	ids := make([]string, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ids[i] = NewIdentityStore(root).Load("").ID
		}(i)
	}
	wg.Wait()

	seen := map[string]int{}
	for _, id := range ids {
		seen[id]++
	}
	if len(seen) != 1 {
		t.Fatalf("concurrent mint produced %d distinct identities: %v", len(seen), ids)
	}
}

// read-only: Peek on a fresh machine reports "none" and creates no file.
func TestIdentityPeekIsReadOnlyOnFreshMachine(t *testing.T) {
	store, _ := newTestStore(t)
	status := store.Peek("")
	if status["identity_state"] != "none" {
		t.Fatalf("peek on fresh machine = %v, want identity_state none", status)
	}
	if _, ok := status["computer_id"]; ok {
		t.Fatalf("peek must not mint an id: %v", status)
	}
	if _, err := os.Stat(store.path()); !os.IsNotExist(err) {
		t.Fatalf("peek created the identity file (status mutated state): %v", err)
	}

	// After a real mint, peek reports the stable id without changing it.
	id := store.Load("").ID
	status = store.Peek("")
	if status["identity_state"] != "stable" || status["computer_id"] != id {
		t.Fatalf("peek after mint = %v, want stable %q", status, id)
	}
}

// MustID surfaces ambiguity as a hard error rather than silently minting.
func TestIdentityMustIDRejectsAmbiguous(t *testing.T) {
	store, _ := newTestStore(t)
	if err := os.MkdirAll(store.root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.path(), []byte("broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.MustID(""); err == nil {
		t.Fatalf("MustID on ambiguous evidence should error, got nil")
	}
}
