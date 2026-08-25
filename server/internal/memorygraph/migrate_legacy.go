package memorygraph

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	legacyMigrationSchemaVersion = 1
	legacyMaxJudgeScore          = 1.0
	legacyMaxRounds              = 64
	legacyMaxAgentRuns           = 16
)

// LegacyMigrationResult reports the durable classification work performed by
// MigrateLegacyQueryLogs. Skipped includes already-marked and current-format
// records; Quarantined records are retained in the migration audit file.
type LegacyMigrationResult struct {
	Scanned     int
	Marked      int
	Skipped     int
	Quarantined int
}

type legacyMigrationCheckpoint struct {
	SchemaVersion int       `json:"schema_version"`
	CompletedAt   time.Time `json:"completed_at"`
}

type legacyMigrationQuarantine struct {
	Source    string `json:"source"`
	Line      int    `json:"line"`
	Reason    string `json:"reason"`
	Raw       string `json:"raw"`
	EntryHash string `json:"entry_hash"`
}

// MigrateLegacyQueryLogs marks flat pre-Dive query-log and regression records
// as audit-only. It is explicit rather than startup work: each source is
// atomically rewritten, then receives a checkpoint so repeated or interrupted
// runs safely rescan and converge without creating authoritative data.
func MigrateLegacyQueryLogs(store *Store) (LegacyMigrationResult, error) {
	store.mu.Lock()
	defer store.mu.Unlock()

	var result LegacyMigrationResult
	quarantined, err := store.legacyQuarantineHashesLocked()
	if err != nil {
		return result, err
	}
	windows, err := listIDFiles(store.queryLogDir(), ".jsonl")
	if err != nil {
		return result, fmt.Errorf("legacy migration: list query log windows: %w", err)
	}
	for _, window := range windows {
		if err := store.migrateLegacyQueryLogWindowLocked(window, &result, quarantined); err != nil {
			return result, err
		}
	}
	return result, nil
}

func (s *Store) migrateLegacyQueryLogWindowLocked(window string, result *LegacyMigrationResult, quarantined map[string]bool) error {
	path := s.queryLogPath(window)
	lines, err := readLegacyMigrationLines(path)
	if err != nil {
		return fmt.Errorf("legacy migration: read query log %s: %w", window, err)
	}
	entries := make([]*QueryLogEntry, 0, len(lines))
	for lineNumber, raw := range lines {
		result.Scanned++
		entry, reason := parseLegacyQueryLogEntry(raw)
		if reason != "" {
			if err := s.quarantineLegacyEntryLocked(quarantined, "query_log/"+window, lineNumber+1, reason, raw); err != nil {
				return err
			}
			result.Quarantined++
			continue
		}
		if entry.LegacyNonAuthoritative {
			result.Skipped++
			entries = append(entries, entry)
			continue
		}
		if isCurrentQueryLogEntry(entry) {
			result.Skipped++
			entries = append(entries, entry)
			continue
		}
		if !isFlatLegacyQueryLogEntry(entry) {
			if err := s.quarantineLegacyEntryLocked(quarantined, "query_log/"+window, lineNumber+1, "unknown_query_log_shape", raw); err != nil {
				return err
			}
			result.Quarantined++
			continue
		}
		if !legacyQueryNumbersInRange(entry) {
			if err := s.quarantineLegacyEntryLocked(quarantined, "query_log/"+window, lineNumber+1, "legacy_numeric_value_out_of_range", raw); err != nil {
				return err
			}
			result.Quarantined++
			continue
		}
		entry.LegacyNonAuthoritative = true
		result.Marked++
		entries = append(entries, entry)
	}
	if err := writeJSONLAtomically(path, entries); err != nil {
		return fmt.Errorf("legacy migration: rewrite query log %s: %w", window, err)
	}
	if err := s.writeLegacyMigrationCheckpointLocked(window); err != nil {
		return err
	}
	return nil
}

func parseLegacyQueryLogEntry(raw string) (*QueryLogEntry, string) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &fields); err != nil || len(fields) == 0 {
		return nil, "undecodable_query_log_entry"
	}
	if !hasAnyLegacyField(fields, "trace_id", "query", "judge_done", "judge_score", "info_items", "ledger_id", "trajectory_id", "legacy_non_authoritative") {
		return nil, "unknown_query_log_shape"
	}
	var entry QueryLogEntry
	if err := json.Unmarshal([]byte(raw), &entry); err != nil {
		return nil, "undecodable_query_log_entry"
	}
	return &entry, ""
}

func hasAnyLegacyField(fields map[string]json.RawMessage, names ...string) bool {
	for _, name := range names {
		if _, ok := fields[name]; ok {
			return true
		}
	}
	return false
}

func isCurrentQueryLogEntry(entry *QueryLogEntry) bool {
	return entry.TraceID != "" && (len(entry.InfoItems) > 0 || entry.LedgerID != "" || entry.TrajectoryID != "")
}

func isFlatLegacyQueryLogEntry(entry *QueryLogEntry) bool {
	return entry.JudgeDone && len(entry.InfoItems) == 0 && entry.LedgerID == "" && entry.TrajectoryID == ""
}

func legacyQueryNumbersInRange(entry *QueryLogEntry) bool {
	return entry.Version >= 0 && entry.Rounds >= 0 && entry.Rounds <= legacyMaxRounds &&
		entry.AgentRuns >= 0 && entry.AgentRuns <= legacyMaxAgentRuns &&
		!math.IsNaN(entry.JudgeScore) && !math.IsInf(entry.JudgeScore, 0) &&
		entry.JudgeScore >= 0 && entry.JudgeScore <= legacyMaxJudgeScore
}

func (s *Store) legacyMigrationDir() string {
	return filepath.Join(s.Root, "legacy_migration")
}

func (s *Store) writeLegacyMigrationCheckpointLocked(source string) error {
	path := filepath.Join(s.legacyMigrationDir(), source+".json")
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("legacy migration: stat checkpoint %s: %w", source, err)
	}
	checkpoint := legacyMigrationCheckpoint{SchemaVersion: legacyMigrationSchemaVersion, CompletedAt: time.Now().UTC()}
	if err := writeJSONLAtomically(path, []*legacyMigrationCheckpoint{&checkpoint}); err != nil {
		return fmt.Errorf("legacy migration: write checkpoint %s: %w", source, err)
	}
	return nil
}

func (s *Store) legacyQuarantineHashesLocked() (map[string]bool, error) {
	path := filepath.Join(s.legacyMigrationDir(), "quarantine.jsonl")
	var entries []*legacyMigrationQuarantine
	if err := readJSONL(path, &entries); err != nil {
		return nil, fmt.Errorf("legacy migration: read quarantine: %w", err)
	}
	hashes := make(map[string]bool, len(entries))
	for _, entry := range entries {
		if entry != nil && entry.EntryHash != "" {
			hashes[entry.EntryHash] = true
		}
	}
	return hashes, nil
}

func (s *Store) quarantineLegacyEntryLocked(hashes map[string]bool, source string, line int, reason, raw string) error {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%d\x00%s", source, line, raw)))
	hash := hex.EncodeToString(sum[:])
	if hashes[hash] {
		return nil
	}
	entry := &legacyMigrationQuarantine{Source: source, Line: line, Reason: reason, Raw: raw, EntryHash: hash}
	if err := appendJSONL(filepath.Join(s.legacyMigrationDir(), "quarantine.jsonl"), entry); err != nil {
		return fmt.Errorf("legacy migration: quarantine %s line %d: %w", source, line, err)
	}
	hashes[hash] = true
	return nil
}

func readLegacyMigrationLines(path string) ([]string, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var lines []string
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines, nil
}

func writeJSONLAtomically[T any](path string, entries []T) error {
	tmp := path + ".tmp"
	if err := writeJSONL(tmp, entries); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	return nil
}
