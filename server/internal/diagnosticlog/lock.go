package diagnosticlog

import (
	"fmt"
	"os"
	"path/filepath"
)

func (s *Store) withTreeLock(fn func() error) error {
	if err := ensurePrivateDir(s.root); err != nil {
		return err
	}
	path := filepath.Join(s.root, lockFileName)
	file, err := openNoFollow(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("open diagnostic tree lock: %w", err)
	}
	defer file.Close()
	if err := lockFile(file); err != nil {
		return fmt.Errorf("lock diagnostic tree: %w", err)
	}
	defer unlockFile(file)
	return fn()
}
