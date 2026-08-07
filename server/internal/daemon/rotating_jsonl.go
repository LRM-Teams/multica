package daemon

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// rotatingJSONLLimits bounds one machine-local JSONL log directory.
type rotatingJSONLLimits struct {
	fileBytes int64
	retention time.Duration
	capBytes  int64
}

// rotatingJSONLWriter is the shared append-only core for machine-local JSONL
// logs (agent lifecycle diagnostics, Machine Upgrade history). It rotates on
// a per-file size limit or UTC-day boundary, retains files for the configured
// retention, and enforces a directory-wide cap by deleting oldest files
// first.
//
// A fresh writer never appends to a file a previous process may have left
// mid-line: each process generation starts a new sequence, so a torn tail
// stays quarantined in the file that crashed. Append and cleanup failures are
// returned for observability only; they must never block the caller's
// lifecycle.
type rotatingJSONLWriter struct {
	mu sync.Mutex

	dir    string
	prefix string
	limits rotatingJSONLLimits
	now    func() time.Time

	currentPath string
	currentDay  string
	currentSize int64
}

func newRotatingJSONLWriter(dir, prefix string, limits rotatingJSONLLimits, now func() time.Time) *rotatingJSONLWriter {
	if now == nil {
		now = time.Now
	}
	w := &rotatingJSONLWriter{dir: dir, prefix: prefix, limits: limits, now: now}
	// Cleanup is best-effort by contract. A permissions or disk issue must
	// never stop the caller.
	_ = w.Cleanup()
	return w
}

// appendLine rotates if needed, then appends one record. data must already
// carry the trailing newline.
func (w *rotatingJSONLWriter) appendLine(data []byte) error {
	if w == nil || strings.TrimSpace(w.dir) == "" {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.rotateLocked(int64(len(data))); err != nil {
		return err
	}
	file, err := os.OpenFile(w.currentPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open %s log: %w", w.logName(), err)
	}
	defer file.Close()
	if _, err := file.Write(data); err != nil {
		return fmt.Errorf("write %s log: %w", w.logName(), err)
	}
	w.currentSize += int64(len(data))
	return nil
}

// Cleanup removes expired files and enforces the directory cap. It only ever
// touches this writer's own "<prefix>*" .jsonl files; sibling state files in
// the same directory (for example Machine Upgrade recovery journals) are
// preserved.
func (w *rotatingJSONLWriter) Cleanup() error {
	if w == nil || strings.TrimSpace(w.dir) == "" {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.cleanupLocked()
}

func (w *rotatingJSONLWriter) logName() string {
	return strings.TrimSuffix(w.prefix, "-")
}

func (w *rotatingJSONLWriter) rotateLocked(nextBytes int64) error {
	if err := os.MkdirAll(w.dir, 0o700); err != nil {
		return fmt.Errorf("create %s log directory: %w", w.logName(), err)
	}
	now := w.now().UTC()
	day := now.Format("2006-01-02")
	if w.currentPath != "" && w.currentDay == day && w.currentSize+nextBytes <= w.limits.fileBytes {
		return nil
	}
	sequence := 0
	for {
		candidate := filepath.Join(w.dir, fmt.Sprintf("%s%s-%03d.jsonl", w.prefix, day, sequence))
		info, err := os.Stat(candidate)
		if os.IsNotExist(err) {
			w.currentPath, w.currentDay, w.currentSize = candidate, day, 0
			break
		}
		if err != nil {
			return fmt.Errorf("stat %s log: %w", w.logName(), err)
		}
		if info.Size()+nextBytes <= w.limits.fileBytes && w.currentPath == candidate {
			w.currentPath, w.currentDay, w.currentSize = candidate, day, info.Size()
			break
		}
		sequence++
	}
	_ = w.cleanupLocked()
	return nil
}

func (w *rotatingJSONLWriter) cleanupLocked() error {
	entries, err := os.ReadDir(w.dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read %s log directory: %w", w.logName(), err)
	}
	now := w.now().UTC()
	type candidate struct {
		path string
		info fs.FileInfo
	}
	files := make([]candidate, 0, len(entries))
	var total int64
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), w.prefix) || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		path := filepath.Join(w.dir, entry.Name())
		info, statErr := entry.Info()
		if statErr != nil {
			continue
		}
		if path != w.currentPath && now.Sub(info.ModTime().UTC()) > w.limits.retention {
			_ = os.Remove(path)
			continue
		}
		files = append(files, candidate{path: path, info: info})
		total += info.Size()
	}
	sort.Slice(files, func(i, j int) bool { return files[i].info.ModTime().Before(files[j].info.ModTime()) })
	for _, file := range files {
		if total <= w.limits.capBytes {
			break
		}
		if file.path == w.currentPath {
			continue
		}
		if err := os.Remove(file.path); err == nil {
			total -= file.info.Size()
		}
	}
	return nil
}
