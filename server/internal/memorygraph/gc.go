package memorygraph

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

// ErrGCLockBusy is returned when another process holds a fresh gc.lock.
var ErrGCLockBusy = errors.New("gc lock busy")

const gcLockStaleAfter = 30 * time.Minute

type gcLockFile struct {
	PID int       `json:"pid"`
	TS  time.Time `json:"ts"`
}

func (s *Store) gcLockPath() string { return filepath.Join(s.Root, "gc.lock") }

// GCWithPinned is GC(keep) plus a set of versions that must not be deleted
// even when they fall outside the keep window (spec §15, A26). The current
// version is never deleted. A store-root gc.lock coordinates cross-process
// runs: a lock younger than 30 minutes fails with ErrGCLockBusy; a stale
// lock is reclaimed. Version-dir deletes are retryable: a failed RemoveAll
// is logged and skipped so a later run can finish a partial directory.
func (s *Store) GCWithPinned(keep int, pinned map[int]bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.acquireGCLock(); err != nil {
		return err
	}
	defer s.releaseGCLock()

	versions, err := s.listVersionsLocked()
	if err != nil {
		return err
	}
	if len(versions) == 0 {
		return nil
	}
	current, err := s.currentVersionLocked()
	if err != nil {
		return fmt.Errorf("gc: read current version: %w", err)
	}
	keepSet := map[int]bool{current: true}
	for i := len(versions) - 1; i >= 0 && keep > 0; i-- {
		if !keepSet[versions[i]] {
			keepSet[versions[i]] = true
			keep--
		}
	}
	for v, ok := range pinned {
		if ok {
			keepSet[v] = true
		}
	}

	var removeErrs []error
	for _, v := range versions {
		if keepSet[v] {
			continue
		}
		if err := os.RemoveAll(s.VersionDir(v)); err != nil {
			slog.Warn("gc: remove version failed; will retry", "version", v, "error", err)
			removeErrs = append(removeErrs, fmt.Errorf("gc: remove version v%d: %w", v, err))
		}
	}
	return errors.Join(removeErrs...)
}

func (s *Store) acquireGCLock() error {
	err := s.tryCreateGCLock()
	if err == nil {
		return nil
	}
	if !os.IsExist(err) {
		return fmt.Errorf("gc: create lock: %w", err)
	}
	b, readErr := os.ReadFile(s.gcLockPath())
	if readErr != nil {
		return fmt.Errorf("gc: %w", ErrGCLockBusy)
	}
	var lock gcLockFile
	if json.Unmarshal(b, &lock) == nil && !lock.TS.IsZero() && time.Since(lock.TS) < gcLockStaleAfter {
		return fmt.Errorf("gc: %w", ErrGCLockBusy)
	}
	_ = os.Remove(s.gcLockPath())
	if err := s.tryCreateGCLock(); err != nil {
		if os.IsExist(err) {
			return fmt.Errorf("gc: %w", ErrGCLockBusy)
		}
		return fmt.Errorf("gc: reclaim lock: %w", err)
	}
	return nil
}

func (s *Store) tryCreateGCLock() error {
	f, err := os.OpenFile(s.gcLockPath(), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	body, err := json.Marshal(gcLockFile{PID: os.Getpid(), TS: time.Now().UTC()})
	if err != nil {
		_ = os.Remove(s.gcLockPath())
		return err
	}
	if _, err := f.Write(append(body, '\n')); err != nil {
		_ = os.Remove(s.gcLockPath())
		return err
	}
	return nil
}

func (s *Store) releaseGCLock() {
	_ = os.Remove(s.gcLockPath())
}
