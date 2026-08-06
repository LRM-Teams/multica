package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	lifecycleDiagnosticFileBytes       = 5 << 20
	lifecycleDiagnosticRetention       = 7 * 24 * time.Hour
	lifecycleDiagnosticCapBytes        = 200 << 20
	lifecycleDiagnosticCleanupInterval = 24 * time.Hour
)

// lifecycleDiagnosticWriter is deliberately local-only. Its record type has
// no prompt, command, path, credential, environment, stderr, or provider
// payload field, so those values cannot enter the JSONL serialization path.
type lifecycleDiagnosticWriter struct {
	mu sync.Mutex

	dir string
	now func() time.Time

	currentPath string
	currentDay  string
	currentSize int64
}

type lifecycleDiagnosticRecord struct {
	StateInstanceID string    `json:"state_instance_id"`
	LaunchID        string    `json:"launch_id"`
	Sequence        int64     `json:"sequence"`
	Phase           string    `json:"phase"`
	State           string    `json:"state"`
	Event           string    `json:"event"`
	Result          string    `json:"result,omitempty"`
	At              time.Time `json:"at"`
}

func newLifecycleDiagnosticWriter(dir string, now func() time.Time) *lifecycleDiagnosticWriter {
	if now == nil {
		now = time.Now
	}
	w := &lifecycleDiagnosticWriter{dir: dir, now: now}
	// Cleanup is best-effort by contract. A permissions or disk issue must
	// never stop a managed Agent lifecycle.
	_ = w.Cleanup()
	return w
}

// Record is safe for an Agent Process Manager transition callback. Errors are
// returned only for local observability; callers must log and continue.
func (w *lifecycleDiagnosticWriter) Record(transition agentLifecycleTransition) error {
	if w == nil || strings.TrimSpace(w.dir) == "" {
		return nil
	}
	record := lifecycleDiagnosticRecord{StateInstanceID: transition.StateInstanceID, LaunchID: transition.LaunchID, Sequence: transition.Sequence, Phase: transition.Phase, State: transition.State, Event: transition.Event, Result: transition.Result, At: transition.At.UTC()}
	encoded, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("marshal lifecycle diagnostic: %w", err)
	}
	encoded = append(encoded, '\n')
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.rotateLocked(int64(len(encoded))); err != nil {
		return err
	}
	file, err := os.OpenFile(w.currentPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("open lifecycle diagnostic: %w", err)
	}
	defer file.Close()
	if _, err := file.Write(encoded); err != nil {
		return fmt.Errorf("write lifecycle diagnostic: %w", err)
	}
	w.currentSize += int64(len(encoded))
	return nil
}

func (w *lifecycleDiagnosticWriter) Cleanup() error {
	if w == nil || strings.TrimSpace(w.dir) == "" {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.cleanupLocked()
}

// lifecycleDiagnosticsCleanupLoop repeats the constructor's best-effort
// cleanup once a day. Diagnostic retention is never allowed to block daemon
// startup, process management, or shutdown.
func (d *Daemon) lifecycleDiagnosticsCleanupLoop(ctx context.Context) {
	if d == nil || d.lifecycleDiagnostics == nil {
		return
	}
	ticker := time.NewTicker(lifecycleDiagnosticCleanupInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := d.lifecycleDiagnostics.Cleanup(); err != nil && d.logger != nil {
				d.logger.Debug("lifecycle diagnostic cleanup failed", "error", err)
			}
		}
	}
}

func (w *lifecycleDiagnosticWriter) rotateLocked(nextBytes int64) error {
	if err := os.MkdirAll(w.dir, 0700); err != nil {
		return fmt.Errorf("create lifecycle diagnostic directory: %w", err)
	}
	now := w.now().UTC()
	day := now.Format("2006-01-02")
	if w.currentPath != "" && w.currentDay == day && w.currentSize+nextBytes <= lifecycleDiagnosticFileBytes {
		return nil
	}
	sequence := 0
	for {
		candidate := filepath.Join(w.dir, fmt.Sprintf("lifecycle-%s-%03d.jsonl", day, sequence))
		info, err := os.Stat(candidate)
		if os.IsNotExist(err) {
			w.currentPath, w.currentDay, w.currentSize = candidate, day, 0
			break
		}
		if err != nil {
			return fmt.Errorf("stat lifecycle diagnostic: %w", err)
		}
		if info.Size()+nextBytes <= lifecycleDiagnosticFileBytes && w.currentPath == candidate {
			w.currentPath, w.currentDay, w.currentSize = candidate, day, info.Size()
			break
		}
		sequence++
	}
	_ = w.cleanupLocked()
	return nil
}

func (w *lifecycleDiagnosticWriter) cleanupLocked() error {
	entries, err := os.ReadDir(w.dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read lifecycle diagnostics: %w", err)
	}
	now := w.now().UTC()
	type candidate struct {
		path string
		info fs.FileInfo
	}
	files := make([]candidate, 0, len(entries))
	var total int64
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "lifecycle-") || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		path := filepath.Join(w.dir, entry.Name())
		info, statErr := entry.Info()
		if statErr != nil {
			continue
		}
		if path != w.currentPath && now.Sub(info.ModTime().UTC()) > lifecycleDiagnosticRetention {
			_ = os.Remove(path)
			continue
		}
		files = append(files, candidate{path: path, info: info})
		total += info.Size()
	}
	sort.Slice(files, func(i, j int) bool { return files[i].info.ModTime().Before(files[j].info.ModTime()) })
	for _, file := range files {
		if total <= lifecycleDiagnosticCapBytes {
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
