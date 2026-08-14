package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
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
// File mechanics (size/day rotation, retention, cap) live in the shared
// rotatingJSONLWriter.
type lifecycleDiagnosticWriter struct {
	*rotatingJSONLWriter
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
	return &lifecycleDiagnosticWriter{newRotatingJSONLWriter(dir, "lifecycle-", rotatingJSONLLimits{
		fileBytes: lifecycleDiagnosticFileBytes,
		retention: lifecycleDiagnosticRetention,
		capBytes:  lifecycleDiagnosticCapBytes,
	}, now)}
}

// Record is safe for an Agent Process Manager transition callback. Errors are
// returned only for local observability; callers must log and continue.
func (w *lifecycleDiagnosticWriter) Record(transition agentLifecycleTransition) error {
	if w == nil || w.rotatingJSONLWriter == nil || strings.TrimSpace(w.dir) == "" {
		return nil
	}
	record := lifecycleDiagnosticRecord{StateInstanceID: transition.StateInstanceID, LaunchID: transition.LaunchID, Sequence: transition.Sequence, Phase: transition.Phase, State: transition.State, Event: transition.Event, Result: transition.Result, At: transition.At.UTC()}
	encoded, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("marshal lifecycle diagnostic: %w", err)
	}
	return w.appendLine(append(encoded, '\n'))
}

// diagnosticsCleanupLoop repeats the writers' best-effort cleanup once a day.
// Diagnostic and upgrade-history retention is never allowed to block daemon
// startup, process management, or shutdown.
func (d *Daemon) diagnosticsCleanupLoop(ctx context.Context) {
	if d == nil || (d.lifecycleDiagnostics == nil && d.machineUpgradeLog == nil) {
		return
	}
	ticker := time.NewTicker(lifecycleDiagnosticCleanupInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if d.lifecycleDiagnostics != nil {
				if err := d.lifecycleDiagnostics.Cleanup(); err != nil && d.logger != nil {
					d.logger.Debug("lifecycle diagnostic cleanup failed", "error", err)
				}
			}
			if d.machineUpgradeLog != nil {
				if err := d.machineUpgradeLog.Cleanup(); err != nil && d.logger != nil {
					d.logger.Debug("machine upgrade log cleanup failed", "error", err)
				}
			}
		}
	}
}

func (d *Daemon) recordAgentLifecycleTransition(transition agentLifecycleTransition) {
	if d == nil {
		return
	}
	if d.bindingDiagnostics != nil {
		if err := d.bindingDiagnostics.recordLifecycle(transition); err != nil && d.logger != nil {
			d.logger.Debug("Host lifecycle diagnostic aggregation failed", "reason", "queue_unavailable")
		}
		return
	}
	if d.lifecycleDiagnostics == nil {
		return
	}
	if err := d.lifecycleDiagnostics.Record(transition); err != nil && d.logger != nil {
		// Local diagnostics are intentionally non-blocking for lifecycle.
		d.logger.Debug("agent lifecycle diagnostic write failed", "error", err)
	}
}
