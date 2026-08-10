package computer

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

// daemonIDFile is the file that stores the one machine-wide Computer
// identity. It is intentionally the same path the legacy daemon used
// ( ~/.multica/daemon.id ) so existing installs keep their identity across
// the refactor. The id is machine-scoped, never profile-scoped.
const daemonIDFile = "daemon.id"

// identityLockFile is an advisory lock used only while minting a fresh id so
// two concurrently starting residents cannot mint two different identities.
const identityLockFile = "identity.lock"

// IdentityKind describes how the stored identity evidence was resolved.
type IdentityKind int

const (
	// IdentityStable: a valid stored identity was read.
	IdentityStable IdentityKind = iota
	// IdentityMinted: no identity existed; a fresh one was created.
	IdentityMinted
	// IdentityAmbiguous: identity evidence is missing/unresolvable in a way
	// that must not silently mint a duplicate (corrupt canonical file, or
	// conflicting legacy candidates). The caller must adopt explicitly.
	IdentityAmbiguous
)

// IdentityResult is the outcome of resolving the machine identity.
type IdentityResult struct {
	ID   string
	Kind IdentityKind
	// LegacyCandidates lists preserved per-profile identity files found, for
	// a later explicit adoption (they are never auto-merged ambiguously).
	LegacyCandidates []string
}

// IdentityStore owns the one machine-wide Computer identity. Root is the
// machine-wide state root (e.g. ~/.multica for the default profile); the
// identity file lives directly under it, separate from canonical Agent Roots.
type IdentityStore struct {
	root string
}

// NewIdentityStore returns a store rooted at the machine-wide state dir.
func NewIdentityStore(root string) *IdentityStore {
	return &IdentityStore{root: root}
}

func (s *IdentityStore) path() string { return filepath.Join(s.root, daemonIDFile) }

// Load resolves the machine identity, minting once only when there is no
// existing evidence. Legacy profile evidence is never adopted here: an
// apparently unambiguous UUID is still not proof of canonical origin, signed-in
// user, immutable Workspace identity, and server-side Computer ownership. A
// verified migration orchestrator must call Adopt explicitly (#2486, #2492).
func (s *IdentityStore) Load(_ string) IdentityResult {
	// Bounded retry so concurrent first-starts converge on one id: transient
	// lock contention on a genuinely fresh machine must not resolve to a
	// spurious "ambiguous" result.
	for attempt := 0; attempt < 40; attempt++ {
		if id, ok := s.read(); ok {
			return IdentityResult{ID: id, Kind: IdentityStable}
		}
		if !s.isFresh() {
			break // existing-but-invalid or conflicting legacy evidence
		}
		if id, ok := s.mintWithLock(); ok {
			return IdentityResult{ID: id, Kind: IdentityMinted}
		}
		// Contention/race: another writer is in flight. Wait briefly and retry.
		time.Sleep(10 * time.Millisecond)
	}
	// Final read attempt before concluding ambiguous.
	if id, ok := s.read(); ok {
		return IdentityResult{ID: id, Kind: IdentityStable}
	}

	// Existing-but-invalid, or conflicting legacy evidence: do not silently
	// mint a duplicate. Preserve candidates for explicit adoption.
	return IdentityResult{Kind: IdentityAmbiguous, LegacyCandidates: s.LegacyCandidates()}
}

// MustID returns the stable id, minting when absent, and panics on ambiguous
// evidence. Intended for paths that require a concrete identity and treat
// ambiguity as a hard error (surfaced by the caller as a diagnostic).
func (s *IdentityStore) MustID(legacyProfile string) (string, error) {
	res := s.Load(legacyProfile)
	if res.Kind == IdentityAmbiguous {
		return "", fmt.Errorf("computer identity ambiguous: existing identity evidence cannot be resolved without explicit adoption")
	}
	return res.ID, nil
}

// Status reports the machine identity and whether local identity evidence is
// stable, without mutating any state (read-only projection).
func (s *IdentityStore) Status(legacyProfile string) map[string]any { return s.Peek(legacyProfile) }

// Peek reports the identity and identity-evidence state WITHOUT minting or
// writing anything — a strictly read-only projection used by status so that
// reporting state never mutates it. On a fresh machine it reports stable=false
// and no id (nothing exists yet), rather than creating one.
func (s *IdentityStore) Peek(legacyProfile string) map[string]any {
	out := map[string]any{"identity_state": "unknown"}
	if id, ok := s.read(); ok {
		out["computer_id"] = id
		out["identity_state"] = "stable"
		return out
	}
	cands := s.LegacyCandidates()
	if len(cands) > 0 {
		out["identity_state"] = "ambiguous"
		out["legacy_identity_candidates"] = cands
		return out
	}
	if _, err := os.Stat(s.path()); err == nil {
		// File exists but does not parse: corrupt evidence, needs adoption.
		out["identity_state"] = "ambiguous"
		return out
	}
	// Nothing exists at all — a genuinely fresh machine.
	out["identity_state"] = "none"
	return out
}

func (s *IdentityStore) read() (string, bool) {
	data, err := os.ReadFile(s.path())
	if err != nil {
		return "", false
	}
	id := strings.TrimSpace(string(data))
	if _, err := uuid.Parse(id); err != nil {
		return "", false
	}
	return id, true
}

// isFresh reports whether there is genuinely no identity evidence on this
// machine — no canonical file and no adoptable legacy candidate.
func (s *IdentityStore) isFresh() bool {
	if _, err := os.Stat(s.path()); err == nil {
		return false
	}
	return len(s.LegacyCandidates()) == 0
}

// mintWithLock creates the identity under an exclusive lock so concurrent
// first-starts converge on one id. Returns (id, true) on success.
func (s *IdentityStore) mintWithLock() (string, bool) {
	lockPath := filepath.Join(s.root, identityLockFile)
	if err := os.MkdirAll(s.root, 0o755); err != nil {
		return "", false
	}
	lock, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		if os.IsExist(err) {
			// Another process is minting; wait briefly then hand back whatever
			// it wrote.
			for i := 0; i < 50; i++ {
				if id, ok := s.read(); ok {
					return id, true
				}
				time.Sleep(20 * time.Millisecond)
			}
			return "", false
		}
		return "", false
	}
	defer func() {
		_ = lock.Close()
		_ = os.Remove(lockPath)
	}()

	// Re-check under the lock: another winner may have just written while we
	// waited to acquire it.
	if id, ok := s.read(); ok {
		return id, true
	}

	id := uuid.NewString()
	if err := s.write(id); err != nil {
		return "", false
	}
	return id, true
}

// write persists id atomically with 0600 permissions.
func (s *IdentityStore) write(id string) error {
	dir := filepath.Dir(s.path())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create identity directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".daemon-*.id.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.WriteString(id + "\n"); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}
	if err := os.Chmod(tmpPath, 0o600); err != nil {
		os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, s.path()); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return nil
}

// LegacyCandidates returns the preserved default/profile daemon.id values that
// could be adopted. It never writes or promotes — strictly read-only so it is
// safe to call from read-only status projections.
func (s *IdentityStore) LegacyCandidates() []string {
	var ids []string
	if data, err := os.ReadFile(filepath.Join(s.legacyBase(), daemonIDFile)); err == nil {
		id := strings.TrimSpace(string(data))
		if _, err := uuid.Parse(id); err == nil {
			ids = append(ids, id)
		}
	}

	profilesDir := filepath.Join(s.legacyBase(), "profiles")
	entries, err := os.ReadDir(profilesDir)
	if err != nil {
		return uniqueStrings(ids)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(profilesDir, e.Name(), daemonIDFile))
		if err != nil {
			continue
		}
		id := strings.TrimSpace(string(data))
		if _, err := uuid.Parse(id); err != nil {
			continue
		}
		ids = append(ids, id)
	}
	return uniqueStrings(ids)
}

// Adopt persists candidate as the one machine-wide Computer identity. The
// candidate must be present in preserved legacy evidence; callers that perform
// automatic adoption are responsible for proving origin, user, Workspace, and
// server-side Computer ownership before calling this method. Existing evidence
// is never deleted.
func (s *IdentityStore) Adopt(candidate string) (IdentityResult, error) {
	candidate = strings.TrimSpace(candidate)
	if _, err := uuid.Parse(candidate); err != nil {
		return IdentityResult{}, fmt.Errorf("invalid Computer identity: %w", err)
	}
	if current, ok := s.read(); ok {
		if current == candidate {
			return IdentityResult{ID: current, Kind: IdentityStable}, nil
		}
		return IdentityResult{}, fmt.Errorf("Computer identity is already %s; refusing to replace it", current)
	}
	found := false
	for _, legacy := range s.LegacyCandidates() {
		if legacy == candidate {
			found = true
			break
		}
	}
	if !found {
		return IdentityResult{}, fmt.Errorf("Computer identity %s is not present in preserved legacy evidence", candidate)
	}
	if err := s.preserveCanonicalEvidence(); err != nil {
		return IdentityResult{}, err
	}
	if err := s.write(candidate); err != nil {
		return IdentityResult{}, fmt.Errorf("adopt Computer identity: %w", err)
	}
	return IdentityResult{ID: candidate, Kind: IdentityStable, LegacyCandidates: s.LegacyCandidates()}, nil
}

// CreateFresh explicitly creates a new Computer identity despite preserved
// legacy candidates. It is intentionally separate from Load so ambiguity can
// never be resolved as a side effect of setup/status. Corrupt canonical bytes
// are copied aside before the new identity is written.
func (s *IdentityStore) CreateFresh() (IdentityResult, error) {
	if current, ok := s.read(); ok {
		return IdentityResult{ID: current, Kind: IdentityStable}, nil
	}
	if err := s.preserveCanonicalEvidence(); err != nil {
		return IdentityResult{}, err
	}
	id := uuid.NewString()
	if err := s.write(id); err != nil {
		return IdentityResult{}, fmt.Errorf("create fresh Computer identity: %w", err)
	}
	return IdentityResult{ID: id, Kind: IdentityMinted, LegacyCandidates: s.LegacyCandidates()}, nil
}

func (s *IdentityStore) preserveCanonicalEvidence() error {
	data, err := os.ReadFile(s.path())
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read existing Computer identity evidence: %w", err)
	}
	if _, ok := s.read(); ok {
		return nil
	}
	backup := s.path() + ".legacy-evidence"
	if _, err := os.Stat(backup); err == nil {
		return nil
	}
	if err := os.WriteFile(backup, data, 0o600); err != nil {
		return fmt.Errorf("preserve existing Computer identity evidence: %w", err)
	}
	return nil
}

func (s *IdentityStore) legacyBase() string {
	if filepath.Base(filepath.Clean(s.root)) == "computer" {
		return filepath.Dir(filepath.Clean(s.root))
	}
	return s.root
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
