package memorygraph

import (
	"fmt"
	"os"
	"sync"
	"time"
)

// OpLogger is the append-only audit log of consolidation operations
// (design §5.4, Q16/Q20). Entries are written to op_log/<version>.jsonl
// with a monotonically increasing Seq per version.
type OpLogger struct {
	store *Store

	mu      sync.Mutex
	lastSeq map[int]int // version -> last written seq, lazily scanned from disk
}

// NewOpLogger returns an OpLogger bound to store.
func NewOpLogger(store *Store) *OpLogger {
	return &OpLogger{store: store, lastSeq: make(map[int]int)}
}

// Append writes one entry to op_log/<version>.jsonl. Seq is assigned
// automatically: the first call for a version scans the existing file so
// sequences stay monotonic across process restarts.
func (l *OpLogger) Append(version int, actor, op, target string, detail map[string]any) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	last, ok := l.lastSeq[version]
	if !ok {
		scanned, err := l.scanLastSeq(version)
		if err != nil {
			return err
		}
		last = scanned
	}
	entry := &OpLogEntry{
		Seq:       last + 1,
		Version:   version,
		Timestamp: time.Now().UTC(),
		Actor:     actor,
		Op:        op,
		Target:    target,
		Detail:    detail,
	}
	if err := appendJSONL(l.store.OpLogPath(version), entry); err != nil {
		return fmt.Errorf("append op log v%d: %w", version, err)
	}
	l.lastSeq[version] = entry.Seq
	return nil
}

// Read returns all audit entries of one version in append order. A missing
// log yields an empty list.
func (l *OpLogger) Read(version int) ([]*OpLogEntry, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	var entries []*OpLogEntry
	if err := readJSONL(l.store.OpLogPath(version), &entries); err != nil {
		return nil, fmt.Errorf("read op log v%d: %w", version, err)
	}
	return entries, nil
}

// scanLastSeq returns the highest Seq already present in the version's log.
func (l *OpLogger) scanLastSeq(version int) (int, error) {
	path := l.store.OpLogPath(version)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return 0, nil
	}
	var entries []*OpLogEntry
	if err := readJSONL(path, &entries); err != nil {
		return 0, fmt.Errorf("scan op log v%d: %w", version, err)
	}
	last := 0
	for _, e := range entries {
		if e.Seq > last {
			last = e.Seq
		}
	}
	return last, nil
}
