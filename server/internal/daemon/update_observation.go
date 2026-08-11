package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

const (
	updateObservationSchemaVersion = 1
	updateObservationFileMode      = 0o600
	updateObservationErrorLimit    = 240
)

type persistedUpdateObservation struct {
	SchemaVersion int                              `json:"schema_version"`
	Observation   protocol.DaemonUpdateObservation `json:"observation"`
}

// updateObservationCoordinator is the only daemon-side writer for update
// eligibility and outcome truth. A transition is persisted before it becomes
// visible to heartbeat senders. Failed persistence therefore leaves both the
// in-memory snapshot and its revision unchanged.
type updateObservationCoordinator struct {
	mu      sync.RWMutex
	path    string
	current protocol.DaemonUpdateObservation
	durable bool
	changed *runtimeSetWatcher
	now     func() time.Time
	logger  *slog.Logger
}

func newUpdateObservationCoordinator(cfg Config, logger *slog.Logger) *updateObservationCoordinator {
	if logger == nil {
		logger = slog.Default()
	}
	c := &updateObservationCoordinator{
		path:    strings.TrimSpace(cfg.UpdateObservationPath),
		changed: newRuntimeSetWatcher(),
		now:     time.Now,
		logger:  logger,
	}
	c.current = c.initialObservation(cfg)
	if err := c.persist(c.current); err != nil {
		logger.Warn("auto-update observation: initial persistence failed", "error", err)
	} else {
		c.durable = true
	}
	return c
}

func (c *updateObservationCoordinator) initialObservation(cfg Config) protocol.DaemonUpdateObservation {
	now := c.now().UTC()
	// Automatic installation is retired, but release detection remains active.
	// Keep AutoUpdateEffectiveEnabled false so old clients never infer mutation;
	// the explicit auto_detect source and waiting phase describe the detector.
	source := strings.TrimSpace(cfg.ReleaseDetectionConfigSource)
	if source == "" {
		source = "auto_detect"
	}
	interval := cfg.ReleaseDetectionInterval
	if interval <= 0 {
		interval = DefaultReleaseDetectionInterval
	}
	next := protocol.DaemonUpdateObservation{
		SessionID:                  uuid.NewString(),
		Revision:                   1,
		ObservedAt:                 now.Format(time.RFC3339Nano),
		AutoUpdateEffectiveEnabled: false,
		ConfigSource:               source,
		IneligibleReason:           "",
		CheckIntervalSeconds:       max(int64(interval/time.Second), 1),
		Phase:                      "waiting",
		LastOutcome:                "never_checked",
	}

	previous, err := readPersistedUpdateObservation(c.path)
	if err != nil {
		if c.path != "" && !errors.Is(err, os.ErrNotExist) {
			c.logger.Warn("auto-update observation: ignoring unreadable predecessor state", "error", err)
		}
		return next
	}
	next.AttemptSource = previous.AttemptSource
	next.LastAttemptAt = previous.LastAttemptAt
	next.LastOutcome = previous.LastOutcome
	next.TargetVersion = previous.TargetVersion
	next.ErrorCode = previous.ErrorCode
	next.ErrorMessage = previous.ErrorMessage
	next.StagedVersion = previous.StagedVersion
	next.ActivationGeneration = previous.ActivationGeneration
	if previous.ConfigSource == "deprecated_noop" && previous.LastOutcome == "explicit_only" {
		next.AttemptSource = ""
		next.LastAttemptAt = ""
		next.LastOutcome = "never_checked"
		next.ErrorCode = ""
		next.ErrorMessage = ""
	}
	switch previous.Phase {
	case "checking", "updating":
		next.LastOutcome = "interrupted"
		next.ErrorCode = "daemon_restarted_during_update"
		next.ErrorMessage = "Daemon restarted before the update attempt reached a terminal result."
		next.LastAttemptAt = now.Format(time.RFC3339Nano)
	case "restart_pending":
		if previous.LastOutcome != "update_succeeded" {
			next.LastOutcome = "interrupted"
			next.ErrorCode = "daemon_restarted_during_update"
			next.ErrorMessage = "Daemon restarted before the update attempt reached a terminal result."
			next.LastAttemptAt = now.Format(time.RFC3339Nano)
		}
	}
	if next.LastOutcome == "" {
		next.LastOutcome = "never_checked"
	}
	return next
}

func (c *updateObservationCoordinator) Snapshot() protocol.DaemonUpdateObservation {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.current
}

// PublishedSnapshot returns only state that has crossed the local durability
// boundary. A broken local store therefore degrades the server projection to
// unknown instead of publishing an observation the daemon cannot replay.
func (c *updateObservationCoordinator) PublishedSnapshot() *protocol.DaemonUpdateObservation {
	if c == nil {
		return nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	if !c.durable {
		return nil
	}
	snapshot := c.current
	return &snapshot
}

func (c *updateObservationCoordinator) Subscribe() (<-chan struct{}, func()) {
	return c.changed.Subscribe()
}

// Transition applies one semantic change. The callback may freely mutate the
// copy it receives; validation, revision/time assignment, durable persistence,
// publication, and heartbeat notification happen as one coordinator boundary.
func (c *updateObservationCoordinator) Transition(apply func(*protocol.DaemonUpdateObservation)) error {
	if c == nil || apply == nil {
		return nil
	}
	c.mu.Lock()
	next := c.current
	apply(&next)
	normalizeUpdateObservation(&next)
	if updateObservationSemanticEqual(c.current, next) {
		c.mu.Unlock()
		return nil
	}
	next.Revision = c.current.Revision + 1
	next.ObservedAt = c.now().UTC().Format(time.RFC3339Nano)
	if err := validateDaemonUpdateObservation(next); err != nil {
		c.mu.Unlock()
		return err
	}
	if err := c.persist(next); err != nil {
		c.mu.Unlock()
		return err
	}
	c.current = next
	c.durable = true
	c.mu.Unlock()
	c.changed.notify()
	return nil
}

func (c *updateObservationCoordinator) persist(observation protocol.DaemonUpdateObservation) error {
	if c.path == "" {
		return nil
	}
	payload, err := json.Marshal(persistedUpdateObservation{
		SchemaVersion: updateObservationSchemaVersion,
		Observation:   observation,
	})
	if err != nil {
		return fmt.Errorf("marshal update observation: %w", err)
	}
	dir := filepath.Dir(c.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create update observation directory: %w", err)
	}
	temp, err := os.CreateTemp(dir, ".daemon-update-status-*")
	if err != nil {
		return fmt.Errorf("create update observation temp file: %w", err)
	}
	tempPath := temp.Name()
	cleanup := true
	defer func() {
		_ = temp.Close()
		if cleanup {
			_ = os.Remove(tempPath)
		}
	}()
	if err := temp.Chmod(updateObservationFileMode); err != nil {
		return fmt.Errorf("chmod update observation temp file: %w", err)
	}
	if _, err := temp.Write(payload); err != nil {
		return fmt.Errorf("write update observation temp file: %w", err)
	}
	if err := temp.Sync(); err != nil {
		return fmt.Errorf("sync update observation temp file: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close update observation temp file: %w", err)
	}
	if err := replaceUpdateObservationFile(tempPath, c.path); err != nil {
		return fmt.Errorf("publish update observation: %w", err)
	}
	if err := syncUpdateObservationDir(dir); err != nil {
		return fmt.Errorf("sync update observation directory: %w", err)
	}
	cleanup = false
	return nil
}

func readPersistedUpdateObservation(path string) (protocol.DaemonUpdateObservation, error) {
	if strings.TrimSpace(path) == "" {
		return protocol.DaemonUpdateObservation{}, os.ErrNotExist
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return protocol.DaemonUpdateObservation{}, err
	}
	var persisted persistedUpdateObservation
	if err := json.Unmarshal(data, &persisted); err != nil {
		return protocol.DaemonUpdateObservation{}, fmt.Errorf("decode update observation: %w", err)
	}
	if persisted.SchemaVersion != updateObservationSchemaVersion {
		return protocol.DaemonUpdateObservation{}, fmt.Errorf("unsupported update observation schema %d", persisted.SchemaVersion)
	}
	if err := validateDaemonUpdateObservation(persisted.Observation); err != nil {
		return protocol.DaemonUpdateObservation{}, err
	}
	return persisted.Observation, nil
}

func normalizeUpdateObservation(observation *protocol.DaemonUpdateObservation) {
	observation.ConfigSource = strings.TrimSpace(observation.ConfigSource)
	observation.IneligibleReason = strings.TrimSpace(observation.IneligibleReason)
	observation.Phase = strings.TrimSpace(observation.Phase)
	observation.AttemptSource = strings.TrimSpace(observation.AttemptSource)
	observation.LastOutcome = strings.TrimSpace(observation.LastOutcome)
	observation.TargetVersion = strings.TrimSpace(observation.TargetVersion)
	observation.ErrorCode = strings.TrimSpace(observation.ErrorCode)
	observation.ErrorMessage = sanitizeUpdateObservationError(observation.ErrorMessage)
	observation.StagedVersion = strings.TrimSpace(observation.StagedVersion)
}

func sanitizeUpdateObservationError(message string) string {
	message = strings.Join(strings.Fields(message), " ")
	if len(message) > updateObservationErrorLimit {
		message = message[:updateObservationErrorLimit]
	}
	return message
}

func validateDaemonUpdateObservation(observation protocol.DaemonUpdateObservation) error {
	if _, err := uuid.Parse(observation.SessionID); err != nil {
		return errors.New("auto-update observation session_id must be a UUID")
	}
	if observation.Revision <= 0 {
		return errors.New("auto-update observation revision must be positive")
	}
	if _, err := time.Parse(time.RFC3339Nano, observation.ObservedAt); err != nil {
		return errors.New("auto-update observation observed_at must be RFC3339")
	}
	if !oneOf(observation.ConfigSource, "official_host_default", "self_host_default", "env_enabled", "env_disabled", "cli_disabled", "deprecated_noop", "auto_detect") {
		return fmt.Errorf("invalid auto-update config_source %q", observation.ConfigSource)
	}
	if observation.IneligibleReason != "" && !oneOf(observation.IneligibleReason, "desktop_managed", "non_release_build", "explicit_only") {
		return fmt.Errorf("invalid auto-update ineligible_reason %q", observation.IneligibleReason)
	}
	if observation.CheckIntervalSeconds <= 0 {
		return errors.New("auto-update check_interval_seconds must be positive")
	}
	if !oneOf(observation.Phase, "disabled", "waiting", "checking", "updating", "restart_pending") {
		return fmt.Errorf("invalid auto-update phase %q", observation.Phase)
	}
	if observation.AttemptSource != "" && !oneOf(observation.AttemptSource, "auto", "server") {
		return fmt.Errorf("invalid auto-update attempt_source %q", observation.AttemptSource)
	}
	if observation.LastAttemptAt != "" {
		if _, err := time.Parse(time.RFC3339Nano, observation.LastAttemptAt); err != nil {
			return errors.New("auto-update last_attempt_at must be RFC3339")
		}
	}
	if !oneOf(observation.LastOutcome, "never_checked", "up_to_date", "update_available", "busy", "pinned", "fetch_failed", "update_failed", "verification_failed", "update_succeeded", "interrupted", "explicit_only") {
		return fmt.Errorf("invalid auto-update last_outcome %q", observation.LastOutcome)
	}
	if len(observation.ErrorCode) > 80 {
		return errors.New("auto-update error_code is too long")
	}
	if observation.ErrorCode != "" && !oneOf(
		observation.ErrorCode,
		"daemon_restarted_during_update",
		"release_fetch_failed",
		"download_update_failed",
		"updated_binary_verification_failed",
		"desktop_managed",
	) {
		return fmt.Errorf("invalid auto-update error_code %q", observation.ErrorCode)
	}
	if len(observation.ErrorMessage) > updateObservationErrorLimit {
		return errors.New("auto-update error_message is too long")
	}
	return nil
}

func updateObservationSemanticEqual(a, b protocol.DaemonUpdateObservation) bool {
	a.Revision, b.Revision = 0, 0
	a.ObservedAt, b.ObservedAt = "", ""
	if !equalOptionalUint64(a.ActivationGeneration, b.ActivationGeneration) {
		return false
	}
	a.ActivationGeneration, b.ActivationGeneration = nil, nil
	return a == b
}

func equalOptionalUint64(a, b *uint64) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func oneOf(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}

func (d *Daemon) beginUpdateObservation(source, phase, target string) bool {
	if d.updateObservation == nil {
		return true
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	err := d.updateObservation.Transition(func(observation *protocol.DaemonUpdateObservation) {
		continuingAttempt := observation.AttemptSource == source &&
			observation.LastAttemptAt != "" &&
			observation.Phase == "checking" &&
			phase == "updating"
		observation.Phase = phase
		observation.AttemptSource = source
		if !continuingAttempt {
			observation.LastAttemptAt = now
		}
		observation.TargetVersion = target
		observation.ErrorCode = ""
		observation.ErrorMessage = ""
	})
	if err != nil {
		d.logger.Error("auto-update observation: refusing attempt without durable transition", "source", source, "phase", phase, "error", err)
		return false
	}
	return true
}

func (d *Daemon) finishUpdateObservation(phase, outcome, target, errorCode, errorMessage string) bool {
	if d.updateObservation == nil {
		return true
	}
	err := d.updateObservation.Transition(func(observation *protocol.DaemonUpdateObservation) {
		observation.Phase = phase
		observation.LastOutcome = outcome
		if target != "" {
			observation.TargetVersion = target
		}
		observation.ErrorCode = errorCode
		observation.ErrorMessage = errorMessage
	})
	if err != nil {
		d.logger.Error("auto-update observation: refusing terminal action without durable transition", "phase", phase, "outcome", outcome, "error", err)
		return false
	}
	return true
}

func (d *Daemon) idleUpdateObservationPhase() string {
	if d.updateObservation == nil {
		return "waiting"
	}
	observation := d.updateObservation.Snapshot()
	if observation.AutoUpdateEffectiveEnabled || observation.ConfigSource == "auto_detect" {
		return "waiting"
	}
	return "disabled"
}
