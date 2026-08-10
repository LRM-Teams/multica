package diagnosticlog

import (
	"bufio"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type segment struct {
	path    string
	stream  string
	size    int64
	modTime time.Time
	closed  bool
}

func (s *Store) append(dest destination, data []byte, now time.Time) (bool, error) {
	written := false
	err := s.withTreeLock(func() error {
		if err := ensureDestinationDir(s.root, dest); err != nil {
			return err
		}
		_, err := s.rotateBeforeAppend(dest, int64(len(data)), now)
		if err != nil {
			return err
		}
		file, err := openNoFollow(dest.activePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			return fmt.Errorf("open %s diagnostic stream: %w", dest.scope, err)
		}
		if err := file.Chmod(0o600); err != nil {
			file.Close()
			return fmt.Errorf("restrict %s diagnostic stream: %w", dest.scope, err)
		}
		n, writeErr := file.Write(data)
		closeErr := file.Close()
		if writeErr != nil {
			return fmt.Errorf("write %s diagnostic stream: %w", dest.scope, writeErr)
		}
		if n != len(data) {
			return fmt.Errorf("write %s diagnostic stream: short write %d/%d", dest.scope, n, len(data))
		}
		if closeErr != nil {
			return fmt.Errorf("close %s diagnostic stream: %w", dest.scope, closeErr)
		}
		written = true
		if err := s.cleanupLocked(now); err != nil {
			return err
		}
		return nil
	})
	return written, err
}

func (s *Store) rotateBeforeAppend(dest destination, nextBytes int64, now time.Time) (bool, error) {
	info, err := os.Lstat(dest.activePath)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect active diagnostic segment: %w", err)
	}
	if pathIsUnsafe(dest.activePath, info.Mode()) {
		return false, fmt.Errorf("active diagnostic segment is a symlink: %s", dest.activePath)
	}
	if !info.Mode().IsRegular() {
		return false, fmt.Errorf("active diagnostic segment is not a regular file: %s", dest.activePath)
	}
	if info.Size() > 0 {
		complete, err := hasCompleteTail(dest.activePath, info.Size())
		if err != nil {
			return false, err
		}
		if !complete {
			if err := s.closeAndCompress(dest.activePath, dest.base, now); err != nil {
				return false, err
			}
			return true, nil
		}
	}
	startedAt := segmentStart(dest.activePath, info.ModTime())
	if info.Size()+nextBytes <= s.limits.SegmentBytes && now.Sub(startedAt) < s.limits.SegmentAge {
		return false, nil
	}
	if info.Size() == 0 {
		if err := os.Remove(dest.activePath); err != nil {
			return false, fmt.Errorf("remove empty active diagnostic segment: %w", err)
		}
		return true, nil
	}
	if err := s.closeAndCompress(dest.activePath, dest.base, now); err != nil {
		return false, err
	}
	return true, nil
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

func (s *Store) closeAndCompress(activePath, base string, now time.Time) error {
	dir := filepath.Dir(activePath)
	stamp := now.UTC().Format("20060102T150405")
	var closedPath string
	for sequence := 1; ; sequence++ {
		candidate := filepath.Join(dir, fmt.Sprintf("%s.%s.%06d.log", base, stamp, sequence))
		_, plainErr := os.Lstat(candidate)
		_, gzipErr := os.Lstat(candidate + ".gz")
		if os.IsNotExist(plainErr) && os.IsNotExist(gzipErr) {
			closedPath = candidate
			break
		}
		if plainErr != nil && !os.IsNotExist(plainErr) {
			return fmt.Errorf("inspect rotated diagnostic segment: %w", plainErr)
		}
		if gzipErr != nil && !os.IsNotExist(gzipErr) {
			return fmt.Errorf("inspect compressed diagnostic segment: %w", gzipErr)
		}
		continue
	}
	if err := os.Rename(activePath, closedPath); err != nil {
		return fmt.Errorf("rotate diagnostic segment: %w", err)
	}
	if err := compressSegment(closedPath, now); err != nil {
		return err
	}
	return nil
}

func compressSegment(path string, now time.Time) error {
	if info, err := os.Lstat(path); err != nil {
		return fmt.Errorf("inspect segment for compression: %w", err)
	} else if pathIsUnsafe(path, info.Mode()) || !info.Mode().IsRegular() {
		return fmt.Errorf("refuse to compress unsafe diagnostic segment: %s", path)
	}
	source, err := openNoFollow(path, os.O_RDONLY, 0)
	if err != nil {
		return fmt.Errorf("open diagnostic segment for compression: %w", err)
	}
	defer source.Close()
	temp, err := os.CreateTemp(filepath.Dir(path), ".diagnostic-*.log.gz.tmp")
	if err != nil {
		return fmt.Errorf("create diagnostic compression temporary file: %w", err)
	}
	tempPath := temp.Name()
	removeTemp := true
	defer func() {
		if removeTemp {
			_ = os.Remove(tempPath)
		}
	}()
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return fmt.Errorf("restrict diagnostic compression temporary file: %w", err)
	}
	writer := gzip.NewWriter(temp)
	writer.Header.ModTime = now.UTC()
	if _, err := io.Copy(writer, source); err != nil {
		writer.Close()
		temp.Close()
		return fmt.Errorf("compress diagnostic segment: %w", err)
	}
	if err := writer.Close(); err != nil {
		temp.Close()
		return fmt.Errorf("finish diagnostic compression: %w", err)
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return fmt.Errorf("sync diagnostic compression: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close diagnostic compression: %w", err)
	}
	target := path + ".gz"
	if _, err := os.Lstat(target); err == nil {
		return fmt.Errorf("compressed diagnostic segment already exists: %s", target)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect compressed diagnostic segment: %w", err)
	}
	if err := os.Rename(tempPath, target); err != nil {
		return fmt.Errorf("publish compressed diagnostic segment: %w", err)
	}
	removeTemp = false
	if err := os.Chtimes(target, now, now); err != nil {
		return fmt.Errorf("timestamp compressed diagnostic segment: %w", err)
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove uncompressed diagnostic segment: %w", err)
	}
	return nil
}

func (s *Store) Cleanup() error {
	now := s.now().UTC()
	err := s.withTreeLock(func() error { return s.cleanupLocked(now) })
	if err != nil {
		s.mu.Lock()
		loggers := append([]*Logger(nil), s.loggers...)
		s.mu.Unlock()
		for _, logger := range loggers {
			logger.observeFailure(err, false, now)
		}
	}
	return err
}

func (s *Store) cleanupLocked(now time.Time) error {
	segments, err := discoverSegments(s.root)
	if err != nil {
		return err
	}
	for _, item := range segments {
		if item.closed {
			continue
		}
		started := segmentStart(item.path, item.modTime)
		if item.size > 0 && now.Sub(started) >= s.limits.SegmentAge {
			base := strings.TrimSuffix(filepath.Base(item.path), ".log")
			if err := s.closeAndCompress(item.path, base, now); err != nil {
				return err
			}
		}
	}
	segments, err = discoverSegments(s.root)
	if err != nil {
		return err
	}

	for _, item := range segments {
		if item.closed && now.Sub(item.modTime.UTC()) > s.limits.Retention {
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
	return s.rebalanceActiveSegments(now)
}

func (s *Store) rebalanceActiveSegments(now time.Time) error {
	for {
		segments, err := discoverSegments(s.root)
		if err != nil {
			return err
		}
		overStream := overBudgetStream(segments, s.limits.StreamBytes)
		globalOver := totalSegmentBytes(segments) > s.limits.GlobalBytes
		if overStream == "" && !globalOver {
			return nil
		}
		var active []segment
		for _, item := range segments {
			if item.closed {
				continue
			}
			if overStream == "" || item.stream == overStream {
				active = append(active, item)
			}
		}
		if len(active) == 0 {
			return fmt.Errorf("diagnostic closed segments exceed storage budget")
		}
		sort.Slice(active, func(i, j int) bool { return active[i].modTime.Before(active[j].modTime) })
		oldest := active[0]
		base := strings.TrimSuffix(filepath.Base(oldest.path), ".log")
		if err := s.closeAndCompress(oldest.path, base, now); err != nil {
			return err
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
	}
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
			return infoErr
		}
		if pathIsUnsafe(path, info.Mode()) {
			return fmt.Errorf("diagnostic tree contains symlink: %s", path)
		}
		if entry.IsDir() {
			return nil
		}
		name := entry.Name()
		if name == lockFileName {
			return nil
		}
		if strings.HasPrefix(name, ".diagnostic-") && strings.HasSuffix(name, ".tmp") {
			segments = append(segments, segment{path: path, stream: filepath.Join(filepath.Dir(path), ".temporary"), size: info.Size(), modTime: info.ModTime().UTC(), closed: true})
			return nil
		}
		active := strings.HasSuffix(name, ".log") && !isClosedName(name)
		closed := strings.HasSuffix(name, ".log.gz") || isClosedName(name)
		if !active && !closed {
			return nil
		}
		stream := streamKey(path, active)
		segments = append(segments, segment{path: path, stream: stream, size: info.Size(), modTime: info.ModTime().UTC(), closed: closed})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scan diagnostic tree: %w", err)
	}
	return segments, nil
}

func isClosedName(name string) bool {
	trimmed := strings.TrimSuffix(strings.TrimSuffix(name, ".gz"), ".log")
	parts := strings.Split(trimmed, ".")
	return len(parts) >= 3
}

func streamKey(path string, active bool) string {
	dir := filepath.Dir(path)
	name := filepath.Base(path)
	if active {
		return filepath.Join(dir, strings.TrimSuffix(name, ".log"))
	}
	trimmed := strings.TrimSuffix(strings.TrimSuffix(name, ".gz"), ".log")
	base, _, ok := strings.Cut(trimmed, ".")
	if !ok {
		base = trimmed
	}
	return filepath.Join(dir, base)
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
			if item.closed {
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
		if item.closed {
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
