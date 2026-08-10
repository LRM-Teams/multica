package diagnosticlog

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Lumberjack owns per-stream size rotation, backup count, retention, and gzip
// compression. This file adds only Multica's hard contracts around that sink:
// private paths, complete JSONL tails, 24-hour low-volume rotation, physical
// per-stream/global budgets, and health accounting.

var lumberjackBackupPattern = regexp.MustCompile(`^(.+)-\d{4}-\d{2}-\d{2}T\d{2}-\d{2}-\d{2}\.\d{3}\.log(?:\.gz)?$`)

const compressionSettleTimeout = 5 * time.Second

type segment struct {
	path      string
	stream    string
	size      int64
	modTime   time.Time
	closed    bool
	evictable bool
}

func (l *Logger) append(data []byte, now time.Time) (written, rotated bool, err error) {
	l.writeMu.Lock()
	defer l.writeMu.Unlock()

	if err := ensureDestinationDir(l.store.root, l.dest); err != nil {
		return false, false, err
	}
	info, exists, err := inspectActiveSegment(l.dest.activePath)
	if err != nil {
		return false, false, err
	}
	if exists {
		if err := os.Chmod(l.dest.activePath, 0o600); err != nil {
			return false, false, fmt.Errorf("restrict %s diagnostic stream: %w", l.dest.scope, err)
		}
		if info.Size() > 0 {
			complete, tailErr := hasCompleteTail(l.dest.activePath, info.Size())
			if tailErr != nil {
				return false, false, tailErr
			}
			if !complete {
				if err := l.rotateLocked(now); err != nil {
					return false, false, err
				}
				rotated = true
				info = nil
				exists = false
			}
		}
	}

	if exists && info.Size() > 0 {
		if l.segmentStartedAt.IsZero() {
			l.segmentStartedAt = segmentStart(l.dest.activePath, info.ModTime())
		}
		if now.Sub(l.segmentStartedAt) >= l.store.limits.SegmentAge {
			if err := l.rotateLocked(now); err != nil {
				return false, rotated, err
			}
			rotated = true
			info = nil
			exists = false
		}
	}

	willSizeRotate := exists && info.Size()+int64(len(data)) >= int64(l.sink.MaxSize)*(1<<20)
	n, err := l.sink.Write(data)
	if err != nil {
		return n > 0, rotated, fmt.Errorf("write %s diagnostic stream: %w", l.dest.scope, err)
	}
	if n != len(data) {
		return n > 0, rotated, fmt.Errorf("write %s diagnostic stream: short write %d/%d", l.dest.scope, n, len(data))
	}
	if err := os.Chmod(l.dest.activePath, 0o600); err != nil {
		return true, rotated, fmt.Errorf("restrict %s diagnostic stream: %w", l.dest.scope, err)
	}
	if l.segmentStartedAt.IsZero() || willSizeRotate {
		l.segmentStartedAt = now
	}
	return true, rotated || willSizeRotate, nil
}

func (l *Logger) rotateLocked(now time.Time) error {
	if err := l.sink.Rotate(); err != nil {
		return fmt.Errorf("rotate %s diagnostic stream: %w", l.dest.scope, err)
	}
	l.segmentStartedAt = now
	return nil
}

func (l *Logger) rotateIfAged(now time.Time) (bool, error) {
	l.writeMu.Lock()
	defer l.writeMu.Unlock()
	info, exists, err := inspectActiveSegment(l.dest.activePath)
	if err != nil || !exists || info.Size() == 0 {
		return false, err
	}
	if l.segmentStartedAt.IsZero() {
		l.segmentStartedAt = segmentStart(l.dest.activePath, info.ModTime())
	}
	if now.Sub(l.segmentStartedAt) < l.store.limits.SegmentAge {
		return false, nil
	}
	return true, l.rotateLocked(now)
}

func (l *Logger) forceRotate(now time.Time) (bool, error) {
	l.writeMu.Lock()
	defer l.writeMu.Unlock()
	info, exists, err := inspectActiveSegment(l.dest.activePath)
	if err != nil || !exists || info.Size() == 0 {
		return false, err
	}
	return true, l.rotateLocked(now)
}

func inspectActiveSegment(path string) (os.FileInfo, bool, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("inspect active diagnostic segment: %w", err)
	}
	if pathIsUnsafe(path, info.Mode()) {
		return nil, false, fmt.Errorf("active diagnostic segment is a symlink or reparse point: %s", path)
	}
	if !info.Mode().IsRegular() {
		return nil, false, fmt.Errorf("active diagnostic segment is not a regular file: %s", path)
	}
	return info, true, nil
}

func segmentStart(path string, fallback time.Time) time.Time {
	file, err := openNoFollow(path, os.O_RDONLY, 0)
	if err != nil {
		return fallback.UTC()
	}
	defer file.Close()
	scanner := bufio.NewScanner(io.LimitReader(file, MaxRecordBytes))
	if !scanner.Scan() {
		return fallback.UTC()
	}
	var record struct {
		At string `json:"at"`
	}
	if json.Unmarshal(scanner.Bytes(), &record) != nil {
		return fallback.UTC()
	}
	at, err := time.Parse(time.RFC3339Nano, record.At)
	if err != nil {
		return fallback.UTC()
	}
	return at.UTC()
}

func hasCompleteTail(path string, size int64) (bool, error) {
	file, err := openNoFollow(path, os.O_RDONLY, 0)
	if err != nil {
		return false, fmt.Errorf("open active diagnostic tail: %w", err)
	}
	defer file.Close()
	if _, err := file.Seek(size-1, io.SeekStart); err != nil {
		return false, fmt.Errorf("seek active diagnostic tail: %w", err)
	}
	one := []byte{0}
	if _, err := io.ReadFull(file, one); err != nil {
		return false, fmt.Errorf("read active diagnostic tail: %w", err)
	}
	return one[0] == '\n', nil
}

func (s *Store) Cleanup() error {
	now := s.now().UTC()
	s.cleanupMu.Lock()
	defer s.cleanupMu.Unlock()

	s.mu.Lock()
	loggers := append([]*Logger(nil), s.loggers...)
	s.mu.Unlock()
	for _, logger := range loggers {
		if _, err := logger.rotateIfAged(now); err != nil {
			s.observeCleanupFailure(loggers, err, now)
			return err
		}
	}
	if err := s.cleanupBudgets(now, loggers); err != nil {
		s.observeCleanupFailure(loggers, err, now)
		return err
	}
	return nil
}

func (s *Store) observeCleanupFailure(loggers []*Logger, err error, now time.Time) {
	for _, logger := range loggers {
		logger.observeFailure(err, false, now)
	}
}

func (s *Store) cleanupBudgets(now time.Time, loggers []*Logger) error {
	settleDeadline := time.Now().Add(compressionSettleTimeout)

cleanupLoop:
	for {
		segments, err := discoverSegments(s.root)
		if err != nil {
			return err
		}
		if hasCompressionInProgress(segments) {
			if time.Now().Before(settleDeadline) {
				timer := time.NewTimer(10 * time.Millisecond)
				<-timer.C
				continue cleanupLoop
			}
			s.requestCleanup()
			return fmt.Errorf("diagnostic compression did not settle before quota enforcement")
		}
		for _, item := range segments {
			if item.closed && item.evictable && now.Sub(item.modTime.UTC()) > s.limits.Retention {
				if err := os.Remove(item.path); err != nil && !os.IsNotExist(err) {
					return fmt.Errorf("remove expired diagnostic segment: %w", err)
				}
			}
		}
		segments, err = discoverSegments(s.root)
		if err != nil {
			return err
		}
		if err := enforceStreamBudgets(segments, s.limits.StreamBytes); err != nil {
			return err
		}
		segments, err = discoverSegments(s.root)
		if err != nil {
			return err
		}
		if err := enforceGlobalBudget(segments, s.limits.GlobalBytes); err != nil {
			return err
		}
		segments, err = discoverSegments(s.root)
		if err != nil {
			return err
		}
		if overBudgetStream(segments, s.limits.StreamBytes) == "" && totalSegmentBytes(segments) <= s.limits.GlobalBytes {
			return nil
		}
		if hasCompressionInProgress(segments) {
			if time.Now().Before(settleDeadline) {
				timer := time.NewTimer(10 * time.Millisecond)
				<-timer.C
				continue cleanupLoop
			}
			s.requestCleanup()
			return fmt.Errorf("diagnostic compression did not settle before quota enforcement")
		}

		activeByPath := make(map[string]*Logger, len(loggers))
		for _, logger := range loggers {
			activeByPath[logger.dest.activePath] = logger
		}
		var candidates []segment
		for _, item := range segments {
			if !item.closed && activeByPath[item.path] != nil && item.size > 0 {
				candidates = append(candidates, item)
			}
		}
		if len(candidates) == 0 {
			return fmt.Errorf("diagnostic active segments exceed storage budget")
		}
		sort.Slice(candidates, func(i, j int) bool { return candidates[i].modTime.Before(candidates[j].modTime) })
		rotated, err := activeByPath[candidates[0].path].forceRotate(now)
		if err != nil {
			return err
		}
		if !rotated {
			return fmt.Errorf("diagnostic active segments exceed storage budget")
		}
	}
}

func hasCompressionInProgress(segments []segment) bool {
	for _, item := range segments {
		if item.closed && !item.evictable {
			return true
		}
	}
	return false
}

func overBudgetStream(segments []segment, limit int64) string {
	usage := make(map[string]int64)
	for _, item := range segments {
		usage[item.stream] += item.size
	}
	for stream, bytes := range usage {
		if bytes > limit {
			return stream
		}
	}
	return ""
}

func totalSegmentBytes(segments []segment) int64 {
	var total int64
	for _, item := range segments {
		total += item.size
	}
	return total
}

func discoverSegments(root string) ([]segment, error) {
	var segments []segment
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == root {
			return nil
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			if os.IsNotExist(infoErr) {
				// Lumberjack may finish an asynchronous source-to-gzip rename
				// between WalkDir reading the directory and this stat.
				return nil
			}
			return infoErr
		}
		if pathIsUnsafe(path, info.Mode()) {
			return fmt.Errorf("diagnostic tree contains symlink or reparse point: %s", path)
		}
		if entry.IsDir() {
			return nil
		}
		name := entry.Name()
		if matches := lumberjackBackupPattern.FindStringSubmatch(name); len(matches) == 2 {
			segments = append(segments, segment{
				path: path, stream: filepath.Join(filepath.Dir(path), matches[1]),
				size: info.Size(), modTime: info.ModTime().UTC(), closed: true, evictable: true,
			})
			return nil
		}
		if !strings.HasSuffix(name, ".log") {
			return nil
		}
		segments = append(segments, segment{
			path: path, stream: filepath.Join(filepath.Dir(path), strings.TrimSuffix(name, ".log")),
			size: info.Size(), modTime: info.ModTime().UTC(), closed: false,
		})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scan diagnostic tree: %w", err)
	}
	// During lumberjack's asynchronous gzip pass both the plain rotated source
	// and its .gz target exist. Count the source once and protect it from quota
	// eviction until compression completes; deleting either side mid-copy would
	// race the mature sink's ownership.
	byPath := make(map[string]struct{}, len(segments))
	for _, item := range segments {
		byPath[item.path] = struct{}{}
	}
	settled := segments[:0]
	for _, item := range segments {
		if strings.HasSuffix(item.path, ".gz") {
			if _, compressing := byPath[strings.TrimSuffix(item.path, ".gz")]; compressing {
				continue
			}
		}
		if item.closed && strings.HasSuffix(item.path, ".log") {
			if _, compressing := byPath[item.path+".gz"]; compressing {
				item.evictable = false
			}
		}
		settled = append(settled, item)
	}
	return settled, nil
}

func enforceStreamBudgets(segments []segment, limit int64) error {
	byStream := make(map[string][]segment)
	for _, item := range segments {
		byStream[item.stream] = append(byStream[item.stream], item)
	}
	for _, items := range byStream {
		var total int64
		var closed []segment
		for _, item := range items {
			total += item.size
			if item.closed && item.evictable {
				closed = append(closed, item)
			}
		}
		sort.Slice(closed, func(i, j int) bool { return closed[i].modTime.Before(closed[j].modTime) })
		for _, item := range closed {
			if total <= limit {
				break
			}
			if err := os.Remove(item.path); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("evict over-budget diagnostic segment: %w", err)
			}
			total -= item.size
		}
	}
	return nil
}

func enforceGlobalBudget(segments []segment, limit int64) error {
	var total int64
	var closed []segment
	for _, item := range segments {
		total += item.size
		if item.closed && item.evictable {
			closed = append(closed, item)
		}
	}
	sort.Slice(closed, func(i, j int) bool { return closed[i].modTime.Before(closed[j].modTime) })
	for _, item := range closed {
		if total <= limit {
			break
		}
		if err := os.Remove(item.path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("evict global diagnostic segment: %w", err)
		}
		total -= item.size
	}
	return nil
}

func streamStats(dest destination) (bytes int64, oldest, newest time.Time, err error) {
	segments, err := discoverSegments(filepath.Dir(dest.activePath))
	if err != nil {
		if os.IsNotExist(err) {
			return 0, time.Time{}, time.Time{}, nil
		}
		return 0, time.Time{}, time.Time{}, err
	}
	key := filepath.Join(filepath.Dir(dest.activePath), dest.base)
	for _, item := range segments {
		if item.stream != key {
			continue
		}
		bytes += item.size
		if oldest.IsZero() || item.modTime.Before(oldest) {
			oldest = item.modTime
		}
		if newest.IsZero() || item.modTime.After(newest) {
			newest = item.modTime
		}
	}
	return bytes, oldest, newest, nil
}
