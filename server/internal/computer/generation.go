package computer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	generationFile     = "generation"
	generationLockFile = "generation.lock"
)

// GenerationStore owns the monotonically increasing machine-wide resident
// generation. A successor always gets a larger number; the server can then
// reject an older process even if it retained a credential or socket.
type GenerationStore struct{ root string }

func NewGenerationStore(root string) *GenerationStore { return &GenerationStore{root: root} }

func (s *GenerationStore) path() string { return filepath.Join(s.root, generationFile) }

func (s *GenerationStore) Current() int64 {
	data, err := os.ReadFile(s.path())
	if err != nil {
		return 0
	}
	value, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	if err != nil || value < 1 {
		return 0
	}
	return value
}

func (s *GenerationStore) Next() (int64, error) {
	if err := os.MkdirAll(s.root, 0o755); err != nil {
		return 0, err
	}
	lockPath := filepath.Join(s.root, generationLockFile)
	lock, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return 0, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := lockComputerFile(ctx, lock); err != nil {
		_ = lock.Close()
		return 0, fmt.Errorf("Computer generation lock is busy: %w", err)
	}
	defer func() {
		unlockComputerFile(lock)
		_ = lock.Close()
	}()

	next := s.Current() + 1
	tmp, err := os.CreateTemp(s.root, ".generation-*.tmp")
	if err != nil {
		return 0, err
	}
	tmpPath := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpPath) }
	if _, err := fmt.Fprintf(tmp, "%d\n", next); err != nil {
		_ = tmp.Close()
		cleanup()
		return 0, err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return 0, err
	}
	if err := os.Chmod(tmpPath, 0o600); err != nil {
		cleanup()
		return 0, err
	}
	if err := os.Rename(tmpPath, s.path()); err != nil {
		cleanup()
		return 0, err
	}
	return next, nil
}
