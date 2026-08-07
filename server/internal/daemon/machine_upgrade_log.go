package daemon

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

const (
	machineUpgradeLogFileBytes = 5 << 20
	machineUpgradeLogRetention = 7 * 24 * time.Hour
	machineUpgradeLogCapBytes  = 200 << 20
)

// Machine Upgrade lifecycle events recorded in the machine-local upgrade
// history. The set is deliberately closed: new transitions require a code
// change, which keeps the history greppable and free of ad-hoc payloads.
const (
	machineUpgradeEventAccepted        = "accepted"
	machineUpgradeEventStaged          = "staged"
	machineUpgradeEventHandoff         = "handoff"
	machineUpgradeEventActivated       = "activated"
	machineUpgradeEventCandidateReady  = "candidate_ready"
	machineUpgradeEventRollbackPending = "rollback_pending"
	machineUpgradeEventAlreadyCurrent  = "already_current"
	machineUpgradeEventFailed          = "failed"
)

// machineUpgradeEvent is one append-only record in the machine-local Machine
// Upgrade history. The type deliberately has no credential, prompt,
// environment, path, or arbitrary payload field, so those values cannot enter
// the history. It complements the per-upgrade recovery journal: the journal
// holds the latest restorable state of one operation; this log is the durable
// timeline across operations and process generations.
type machineUpgradeEvent struct {
	At                  string `json:"at"`
	Event               string `json:"event"`
	UpgradeID           string `json:"upgrade_id,omitempty"`
	Generation          string `json:"generation,omitempty"`
	SourceVersion       string `json:"source_version,omitempty"`
	TargetVersion       string `json:"target_version,omitempty"`
	IncumbentGeneration uint64 `json:"incumbent_generation,omitempty"`
	ErrorCode           string `json:"error_code,omitempty"`
	Error               string `json:"error,omitempty"`
}

// machineUpgradeEventLog appends Machine Upgrade lifecycle events next to the
// recovery journals (<version-store>/machine-upgrades/upgrade-<day>-<seq>.jsonl).
// Append and cleanup failures are returned for observability only and must
// never fail an upgrade transition.
type machineUpgradeEventLog struct {
	writer *rotatingJSONLWriter
	now    func() time.Time
}

// newMachineUpgradeEventLog resolves the machine-wide version-store root. When
// the root is unavailable the log is disabled rather than fatal: upgrade
// history must never block upgrades.
func newMachineUpgradeEventLog(now func() time.Time) *machineUpgradeEventLog {
	if now == nil {
		now = time.Now
	}
	log := &machineUpgradeEventLog{now: now}
	root, err := versionStoreRootFn()
	if err != nil || strings.TrimSpace(root) == "" {
		return log
	}
	log.writer = newRotatingJSONLWriter(filepath.Join(root, "machine-upgrades"), "upgrade-", rotatingJSONLLimits{
		fileBytes: machineUpgradeLogFileBytes,
		retention: machineUpgradeLogRetention,
		capBytes:  machineUpgradeLogCapBytes,
	}, now)
	return log
}

func (l *machineUpgradeEventLog) Append(event machineUpgradeEvent) error {
	if l == nil || l.writer == nil {
		return nil
	}
	if strings.TrimSpace(event.At) == "" {
		event.At = l.now().UTC().Format(time.RFC3339Nano)
	}
	event.Error = sanitizeUpdateObservationError(event.Error)
	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal machine upgrade event: %w", err)
	}
	return l.writer.appendLine(append(data, '\n'))
}

func (l *machineUpgradeEventLog) Cleanup() error {
	if l == nil || l.writer == nil {
		return nil
	}
	return l.writer.Cleanup()
}
