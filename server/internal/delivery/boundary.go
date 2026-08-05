package delivery

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// Boundary is the machine-local, target-scoped Context Boundary. It records
// the highest canonical Message sequence whose concrete body has been handed
// to the runtime input boundary for a given Target. The Server never stores
// this cursor; the machine owns it and reports it to an internal recovery
// read on startup/reconnect.
type Boundary struct {
	// Target maps a conversation surface (channel / DM / thread) to the
	// highest covered canonical Message sequence.
	Target map[string]int `json:"target"`
}

// BoundaryStore reads and writes the Context Boundary file with atomic
// replacement. Atomic replace means readers never observe a partially written
// cursor, and a corrupt/regressed file fails closed rather than skipping
// sequences.
type BoundaryStore struct {
	path string
	mu   sync.Mutex
}

// NewBoundaryStore returns a store rooted at path (the boundary file). The
// parent directory is created on first write.
func NewBoundaryStore(path string) *BoundaryStore {
	return &BoundaryStore{path: path}
}

var errBoundaryCorrupt = errors.New("delivery: corrupt context boundary")

// Load reads the boundary file. A missing file yields an empty Boundary (no
// coverage). A file that does not parse, or whose target map is structurally
// invalid, fails closed with errBoundaryCorrupt so callers can decide between
// conservative replay and held behavior.
func (s *BoundaryStore) Load() (Boundary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadLocked()
}

func (s *BoundaryStore) loadLocked() (Boundary, error) {
	raw, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Boundary{Target: map[string]int{}}, nil
		}
		return Boundary{}, err
	}
	var b Boundary
	if err := json.Unmarshal(raw, &b); err != nil {
		return Boundary{}, fmt.Errorf("%w: %v", errBoundaryCorrupt, err)
	}
	if b.Target == nil {
		b.Target = map[string]int{}
	}
	for t, seq := range b.Target {
		if t == "" || seq < 0 {
			return Boundary{}, fmt.Errorf("%w: invalid target %q seq %d", errBoundaryCorrupt, t, seq)
		}
	}
	return b, nil
}

// Advance atomically replaces the boundary file so target's coverage moves to
// at least seq. The write is atomic: a temp file in the same directory is
// renamed over the target file. If seq would regress the existing coverage,
// Advance is a no-op (a boundary must never move backwards).
func (s *BoundaryStore) Advance(target string, seq int) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	b, err := s.loadLocked()
	if err != nil {
		return 0, err
	}
	if cur, ok := b.Target[target]; ok && seq <= cur {
		return cur, nil
	}
	b.Target[target] = seq

	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return 0, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".boundary-*")
	if err != nil {
		return 0, err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after successful rename

	if _, err := tmp.Write(jsonMust(b)); err != nil {
		_ = tmp.Close()
		return 0, err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return 0, err
	}
	if err := tmp.Close(); err != nil {
		return 0, err
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		return 0, err
	}
	return seq, nil
}

// Current returns the current coverage for target from the boundary file.
func (s *BoundaryStore) Current(target string) (int, error) {
	b, err := s.Load()
	if err != nil {
		return 0, err
	}
	return b.Target[target], nil
}

func jsonMust(v any) []byte {
	raw, err := json.Marshal(v)
	if err != nil {
		// Marshal of a plain struct cannot fail.
		panic(err)
	}
	return raw
}
