package diagnosticlog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"gopkg.in/natefinch/lumberjack.v2"
)

const (
	failureRollupInterval = 5 * time.Minute
)

var safeTokenPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]{0,127}$`)
var safeIdentifierPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

type Store struct {
	root   string
	now    func() time.Time
	limits Limits

	mu        sync.Mutex
	cleanupMu sync.Mutex
	loggers   []*Logger
	byPath    map[string]*Logger
	cleanupCh chan struct{}
}

type destination struct {
	scope              Scope
	environment        Environment
	workspaceID        string
	runnerGeneration   string
	computerID         string
	computerGeneration string
	dir                string
	activePath         string
	base               string
}

type Logger struct {
	store *Store
	dest  destination
	sink  *lumberjack.Logger
	seq   atomic.Uint64

	writeMu          sync.Mutex
	segmentStartedAt time.Time

	healthMu sync.Mutex
	health   Health

	rateMu    sync.Mutex
	incidents map[string]*incident
}

type incident struct {
	firstAt    time.Time
	lastEmitAt time.Time
	attempts   int64
	suppressed int64
}

type rateMutation struct {
	key              string
	incident         *incident
	created          bool
	previousLastEmit time.Time
}

func DefaultRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve user home: %w", err)
	}
	return filepath.Join(home, ".multica", "computer", "logs"), nil
}

func Open(config Config) (*Store, error) {
	root := strings.TrimSpace(config.Root)
	if root == "" {
		var err error
		root, err = DefaultRoot()
		if err != nil {
			return nil, err
		}
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve diagnostic root: %w", err)
	}
	if err := ensurePrivateDir(root); err != nil {
		return nil, fmt.Errorf("prepare diagnostic root: %w", err)
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	store := &Store{
		root:      root,
		now:       now,
		limits:    config.Limits.normalized(),
		byPath:    make(map[string]*Logger),
		cleanupCh: make(chan struct{}, 1),
	}
	// Startup cleanup is best effort for product availability. There are no
	// registered sinks yet, so a later caller observes any persistent storage
	// failure on its first write.
	_ = store.Cleanup()
	return store, nil
}

func (s *Store) Root() string { return s.root }

func (s *Store) Service(options ServiceOptions) (*Logger, error) {
	if err := validateOptionalUUID("computer_id", options.ComputerID); err != nil {
		return nil, err
	}
	if err := validateRequiredToken("computer_generation", options.ComputerGeneration); err != nil {
		return nil, err
	}
	dest := destination{
		scope:              ScopeService,
		computerID:         options.ComputerID,
		computerGeneration: options.ComputerGeneration,
		dir:                s.root,
		activePath:         filepath.Join(s.root, "service.log"),
		base:               "service",
	}
	return s.newLogger(dest)
}

func (s *Store) Runner(options RunnerOptions) (*Logger, error) {
	if options.Environment != EnvironmentProduction && options.Environment != EnvironmentTest {
		return nil, fmt.Errorf("environment must be production or test")
	}
	if _, err := uuid.Parse(options.WorkspaceID); err != nil {
		return nil, fmt.Errorf("workspace_id must be an immutable UUID: %w", err)
	}
	if err := validateRequiredToken("runner_generation", options.RunnerGeneration); err != nil {
		return nil, err
	}
	if err := validateOptionalUUID("computer_id", options.ComputerID); err != nil {
		return nil, err
	}
	if err := validateOptionalToken("computer_generation", options.ComputerGeneration); err != nil {
		return nil, err
	}
	dir := filepath.Join(s.root, "runners", string(options.Environment))
	dest := destination{
		scope:              ScopeRunner,
		environment:        options.Environment,
		workspaceID:        options.WorkspaceID,
		runnerGeneration:   options.RunnerGeneration,
		computerID:         options.ComputerID,
		computerGeneration: options.ComputerGeneration,
		dir:                dir,
		activePath:         filepath.Join(dir, options.WorkspaceID+".log"),
		base:               options.WorkspaceID,
	}
	return s.newLogger(dest)
}

func (s *Store) newLogger(dest destination) (*Logger, error) {
	if err := ensureDestinationDir(s.root, dest); err != nil {
		return nil, err
	}
	maxBackups := int(s.limits.StreamBytes/s.limits.SegmentBytes) - 1
	if maxBackups < 1 {
		maxBackups = 1
	}
	logger := &Logger{
		store: s,
		dest:  dest,
		sink: &lumberjack.Logger{
			Filename:   dest.activePath,
			MaxSize:    bytesToMegabytes(s.limits.SegmentBytes),
			MaxAge:     durationToDays(s.limits.Retention),
			MaxBackups: maxBackups,
			LocalTime:  false,
			Compress:   true,
		},
		incidents: make(map[string]*incident),
		health: Health{
			Scope:       dest.scope,
			Environment: dest.environment,
			WorkspaceID: dest.workspaceID,
			Path:        dest.activePath,
			State:       SinkHealthy,
		},
	}
	s.mu.Lock()
	if s.byPath[dest.activePath] != nil {
		s.mu.Unlock()
		return nil, fmt.Errorf("diagnostic stream already registered: %s", dest.activePath)
	}
	s.byPath[dest.activePath] = logger
	s.loggers = append(s.loggers, logger)
	s.mu.Unlock()
	return logger, nil
}

func (l *Logger) Record(event Event) error {
	now := l.store.now().UTC()
	if err := validateEvent(event); err != nil {
		l.observeFailure(err, true, now)
		return err
	}
	if err := validateEventScope(l.dest.scope, event.Name); err != nil {
		l.observeFailure(err, true, now)
		return err
	}
	if _, err := validatedIdentity(event.Identity); err != nil {
		l.observeFailure(err, true, now)
		return err
	}
	if _, _, err := validatedFields(event.Fields); err != nil {
		l.observeFailure(err, true, now)
		return err
	}

	emit, recoveryKey, mutation := l.applyRateLimit(&event, now)
	if !emit {
		return nil
	}
	record, err := l.buildRecord(event, now)
	if err != nil {
		l.rollbackRate(mutation)
		l.observeFailure(err, true, now)
		return err
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		err = fmt.Errorf("encode diagnostic record: %w", err)
		l.rollbackRate(mutation)
		l.observeFailure(err, true, now)
		return err
	}
	encoded = append(encoded, '\n')
	if len(encoded) > MaxRecordBytes {
		err = fmt.Errorf("diagnostic record exceeds %d bytes after field bounds", MaxRecordBytes)
		l.rollbackRate(mutation)
		l.observeFailure(err, true, now)
		return err
	}

	written, rotated, err := l.append(encoded, now)
	if err != nil {
		l.rollbackRate(mutation)
		if written {
			l.observeSuccess(now)
		}
		l.observeFailure(err, !written, now)
		return err
	}
	if rotated {
		l.store.requestCleanup()
	}
	l.observeSuccess(now)
	if recoveryKey != "" {
		l.rateMu.Lock()
		delete(l.incidents, recoveryKey)
		l.rateMu.Unlock()
	}
	return nil
}

func (l *Logger) buildRecord(event Event, now time.Time) (wireRecord, error) {
	identity, err := validatedIdentity(event.Identity)
	if err != nil {
		return wireRecord{}, err
	}
	fields, redactionFailed, err := validatedFields(event.Fields)
	if err != nil {
		return wireRecord{}, err
	}
	diagnostic, stderr, truncated, evidenceRedactionFailed := sanitizeEvidence(event.Evidence)
	return wireRecord{
		SchemaVersion:      SchemaVersion,
		At:                 now.Format(time.RFC3339Nano),
		Level:              event.Level,
		Scope:              l.dest.scope,
		Event:              event.Name,
		Component:          event.Component,
		ComputerID:         l.dest.computerID,
		ComputerGeneration: l.dest.computerGeneration,
		Environment:        l.dest.environment,
		WorkspaceID:        l.dest.workspaceID,
		RunnerGeneration:   l.dest.runnerGeneration,
		StreamSeq:          l.seq.Add(1),
		AgentID:            identity.AgentID,
		RuntimeID:          identity.RuntimeID,
		TaskID:             identity.TaskID,
		SessionID:          identity.SessionID,
		MessageID:          identity.MessageID,
		DeliveryID:         identity.DeliveryID,
		RequestID:          identity.RequestID,
		TraceID:            identity.TraceID,
		ChannelID:          identity.ChannelID,
		ChatSessionID:      identity.ChatSessionID,
		ConversationID:     identity.ConversationID,
		SourceMessageID:    identity.SourceMessageID,
		ExecutionID:        identity.ExecutionID,
		From:               fields.From,
		To:                 fields.To,
		Trigger:            fields.Trigger,
		ReasonCode:         fields.ReasonCode,
		Outcome:            fields.Outcome,
		Status:             fields.Status,
		Provider:           fields.Provider,
		Model:              fields.Model,
		Phase:              fields.Phase,
		FailureReason:      fields.FailureReason,
		ResponseMode:       fields.ResponseMode,
		ServiceOrigin:      fields.ServiceOrigin,
		DurationMS:         fields.DurationMS,
		SeqFrom:            fields.SeqFrom,
		SeqTo:              fields.SeqTo,
		AckedSeq:           fields.AckedSeq,
		FoldedCount:        fields.FoldedCount,
		AttemptCount:       fields.AttemptCount,
		SuppressedCount:    fields.SuppressedCount,
		DroppedCount:       fields.DroppedCount,
		OutageDurationMS:   fields.OutageDurationMS,
		Diagnostic:         diagnostic,
		StderrTail:         stderr,
		RedactionFailed:    redactionFailed || evidenceRedactionFailed,
		TruncatedFields:    truncated,
	}, nil
}

func validateEvent(event Event) error {
	if !knownEvent(event.Name) {
		return fmt.Errorf("unsupported diagnostic event %q", event.Name)
	}
	switch event.Level {
	case LevelDebug, LevelInfo, LevelWarn, LevelError:
	default:
		return fmt.Errorf("unsupported diagnostic level %q", event.Level)
	}
	if err := validateRequiredToken("component", event.Component); err != nil {
		return err
	}
	return nil
}

func knownEvent(name EventName) bool {
	switch name {
	case EventComputerStateChanged, EventEnvironmentStateChanged, EventSessionStateChanged,
		EventWorkspaceRunnerStateChanged, EventUpgradeStateChanged, EventGenerationFenced,
		EventRunnerLogSinkDegraded, EventRunnerLogSinkRecovered, EventDiagnosticStorageEvicted,
		EventRunnerStateChanged, EventServerConnectionStateChanged, EventRuntimeDetected,
		EventAgentLifecycleRequested, EventAgentProcessStateChanged, EventDeliveryStateChanged,
		EventChatTurnCheckpoint, EventTaskStateChanged, EventToolStateChanged, EventProviderFailure:
		return true
	default:
		return false
	}
}

func validateEventScope(scope Scope, name EventName) error {
	service := false
	switch name {
	case EventComputerStateChanged, EventEnvironmentStateChanged, EventSessionStateChanged,
		EventWorkspaceRunnerStateChanged, EventUpgradeStateChanged, EventGenerationFenced,
		EventRunnerLogSinkDegraded, EventRunnerLogSinkRecovered, EventDiagnosticStorageEvicted:
		service = true
	}
	if (scope == ScopeService) != service {
		return fmt.Errorf("diagnostic event %q does not belong to %s scope", name, scope)
	}
	return nil
}

func validatedIdentity(identity Identity) (Identity, error) {
	values := []struct {
		name  string
		value string
	}{
		{"agent_id", identity.AgentID}, {"runtime_id", identity.RuntimeID}, {"task_id", identity.TaskID},
		{"session_id", identity.SessionID}, {"message_id", identity.MessageID}, {"delivery_id", identity.DeliveryID},
		{"request_id", identity.RequestID}, {"trace_id", identity.TraceID}, {"channel_id", identity.ChannelID},
		{"chat_session_id", identity.ChatSessionID}, {"conversation_id", identity.ConversationID},
		{"source_message_id", identity.SourceMessageID}, {"execution_id", identity.ExecutionID},
	}
	for _, item := range values {
		if err := validateOptionalIdentifier(item.name, item.value); err != nil {
			return Identity{}, err
		}
	}
	return identity, nil
}

func validatedFields(fields Fields) (Fields, bool, error) {
	tokens := []struct {
		name  string
		value string
	}{
		{"from", fields.From}, {"to", fields.To}, {"trigger", fields.Trigger},
		{"reason_code", fields.ReasonCode}, {"outcome", fields.Outcome}, {"status", fields.Status},
		{"provider", fields.Provider}, {"model", fields.Model}, {"phase", fields.Phase},
		{"failure_reason", fields.FailureReason}, {"response_mode", fields.ResponseMode},
	}
	for _, item := range tokens {
		if err := validateOptionalToken(item.name, item.value); err != nil {
			return Fields{}, false, err
		}
	}
	counts := []struct {
		name  string
		value int64
	}{
		{"duration_ms", fields.DurationMS}, {"seq_from", fields.SeqFrom}, {"seq_to", fields.SeqTo},
		{"acked_seq", fields.AckedSeq}, {"folded_count", fields.FoldedCount}, {"attempt_count", fields.AttemptCount},
		{"suppressed_count", fields.SuppressedCount}, {"dropped_count", fields.DroppedCount},
		{"outage_duration_ms", fields.OutageDurationMS},
	}
	for _, item := range counts {
		if item.value < 0 {
			return Fields{}, false, fmt.Errorf("%s cannot be negative", item.name)
		}
	}
	if fields.ServiceOrigin != "" {
		sanitized := sanitizeURL(fields.ServiceOrigin)
		if sanitized == "[url]" {
			fields.ServiceOrigin = ""
			return fields, true, nil
		}
		fields.ServiceOrigin = sanitized
	}
	return fields, false, nil
}

func validateOptionalIdentifier(name, value string) error {
	if value == "" {
		return nil
	}
	if !safeIdentifierPattern.MatchString(value) || strings.Contains(value, "://") || jwtPattern.MatchString(value) || credentialTokenPattern.MatchString(value) {
		return fmt.Errorf("%s is not a safe bounded identifier", name)
	}
	return nil
}

func validateOptionalToken(name, value string) error {
	if value == "" {
		return nil
	}
	return validateRequiredToken(name, value)
}

func validateRequiredToken(name, value string) error {
	if !safeTokenPattern.MatchString(value) || strings.Contains(value, "://") || jwtPattern.MatchString(value) || credentialTokenPattern.MatchString(value) {
		return fmt.Errorf("%s is not a safe bounded token", name)
	}
	return nil
}

func validateOptionalUUID(name, value string) error {
	if value == "" {
		return nil
	}
	if _, err := uuid.Parse(value); err != nil {
		return fmt.Errorf("%s must be a UUID: %w", name, err)
	}
	return nil
}

func (l *Logger) applyRateLimit(event *Event, now time.Time) (emit bool, recoveryKey string, mutation rateMutation) {
	key := l.incidentKey(*event)
	if event.Fields.Outcome == "recovered" {
		l.rateMu.Lock()
		defer l.rateMu.Unlock()
		if current := l.incidents[key]; current != nil {
			event.Fields.AttemptCount = current.attempts
			event.Fields.SuppressedCount = current.suppressed
			event.Fields.OutageDurationMS = now.Sub(current.firstAt).Milliseconds()
			return true, key, rateMutation{}
		}
		return true, "", rateMutation{}
	}
	if event.Level != LevelWarn && event.Level != LevelError && event.Fields.Outcome != "failed" {
		return true, "", rateMutation{}
	}

	l.rateMu.Lock()
	defer l.rateMu.Unlock()
	current := l.incidents[key]
	if current == nil {
		current = &incident{firstAt: now, lastEmitAt: now, attempts: 1}
		l.incidents[key] = current
		return true, "", rateMutation{key: key, incident: current, created: true}
	}
	current.attempts++
	if now.Sub(current.lastEmitAt) >= failureRollupInterval {
		previousLastEmit := current.lastEmitAt
		current.lastEmitAt = now
		event.Fields.AttemptCount = current.attempts
		event.Fields.SuppressedCount = current.suppressed
		event.Fields.OutageDurationMS = now.Sub(current.firstAt).Milliseconds()
		return true, "", rateMutation{key: key, incident: current, previousLastEmit: previousLastEmit}
	}
	current.suppressed++
	l.healthMu.Lock()
	l.health.SuppressedRecords++
	l.healthMu.Unlock()
	return false, "", rateMutation{}
}

func (l *Logger) rollbackRate(mutation rateMutation) {
	if mutation.incident == nil {
		return
	}
	l.rateMu.Lock()
	defer l.rateMu.Unlock()
	if l.incidents[mutation.key] != mutation.incident {
		return
	}
	if mutation.created {
		delete(l.incidents, mutation.key)
		return
	}
	mutation.incident.lastEmitAt = mutation.previousLastEmit
}

func (l *Logger) incidentKey(event Event) string {
	owner := event.Identity.AgentID
	if owner == "" {
		owner = event.Identity.RuntimeID
	}
	if owner == "" {
		owner = l.dest.workspaceID
	}
	if owner == "" {
		owner = l.dest.computerID
	}
	return strings.Join([]string{string(l.dest.scope), string(event.Name), event.Fields.ReasonCode, owner}, "|")
}

func (l *Logger) observeFailure(err error, dropped bool, now time.Time) {
	l.healthMu.Lock()
	defer l.healthMu.Unlock()
	l.health.State = SinkDegraded
	l.health.LastErrorClass = classifyError(err)
	l.health.LastErrorAt = now
	if dropped {
		l.health.DroppedRecords++
	}
}

func (l *Logger) observeSuccess(now time.Time) {
	l.healthMu.Lock()
	defer l.healthMu.Unlock()
	l.health.State = SinkHealthy
	l.health.LastSuccessfulWriteAt = now
}

func classifyError(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, fs.ErrPermission):
		return "permission_denied"
	case errors.Is(err, fs.ErrNotExist):
		return "path_missing"
	case strings.Contains(err.Error(), "symlink") || strings.Contains(err.Error(), "reparse"):
		return "unsafe_path"
	case strings.Contains(err.Error(), "encode") || strings.Contains(err.Error(), "record") || strings.Contains(err.Error(), "unsupported"):
		return "invalid_record"
	case strings.Contains(err.Error(), "compress"):
		return "compression_failed"
	case strings.Contains(err.Error(), "rotate") || strings.Contains(err.Error(), "rename"):
		return "rotation_failed"
	default:
		return "write_failed"
	}
}

func (l *Logger) Health() Health {
	l.healthMu.Lock()
	health := l.health
	l.healthMu.Unlock()
	bytes, oldest, newest, err := streamStats(l.dest)
	if err == nil {
		health.Bytes = bytes
		health.OldestRetainedAt = oldest
		health.NewestRetainedAt = newest
	}
	return health
}

func (s *Store) Health() []Health {
	s.mu.Lock()
	loggers := append([]*Logger(nil), s.loggers...)
	s.mu.Unlock()
	health := make([]Health, 0, len(loggers))
	for _, logger := range loggers {
		health = append(health, logger.Health())
	}
	sort.Slice(health, func(i, j int) bool { return health[i].Path < health[j].Path })
	return health
}

// Close releases all open rolling-file handles. It does not remove retained
// diagnostics and is safe to call during normal daemon shutdown.
func (s *Store) Close() error {
	s.mu.Lock()
	loggers := append([]*Logger(nil), s.loggers...)
	s.mu.Unlock()
	var closeErr error
	for _, logger := range loggers {
		logger.writeMu.Lock()
		err := logger.sink.Close()
		logger.writeMu.Unlock()
		if closeErr == nil && err != nil {
			closeErr = err
		}
	}
	return closeErr
}

func bytesToMegabytes(bytes int64) int {
	const megabyte = int64(1 << 20)
	megabytes := int((bytes + megabyte - 1) / megabyte)
	if megabytes < 1 {
		return 1
	}
	return megabytes
}

func durationToDays(duration time.Duration) int {
	days := int((duration + 24*time.Hour - 1) / (24 * time.Hour))
	if days < 1 {
		return 1
	}
	return days
}

// RunCleanup performs periodic age and budget enforcement until ctx is
// cancelled. Callers normally start one loop for the Machine Service. A failed
// pass is reflected in registered sink health and the loop keeps running.
func (s *Store) RunCleanup(ctx context.Context) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = s.Cleanup()
		case <-s.cleanupCh:
			// Lumberjack publishes a rotated file synchronously and compresses it
			// asynchronously. Debounce quota scans so they do not race its mill.
			timer := time.NewTimer(time.Second)
			select {
			case <-ctx.Done():
				if !timer.Stop() {
					<-timer.C
				}
				return
			case <-timer.C:
				_ = s.Cleanup()
			}
		}
	}
}

func (s *Store) requestCleanup() {
	select {
	case s.cleanupCh <- struct{}{}:
	default:
	}
}

func ensurePrivateDir(path string) error {
	info, err := os.Lstat(path)
	if err == nil {
		if pathIsUnsafe(path, info.Mode()) {
			return fmt.Errorf("diagnostic directory is a symlink: %s", path)
		}
		if !info.IsDir() {
			return fmt.Errorf("diagnostic directory is not a directory: %s", path)
		}
		return os.Chmod(path, 0o700)
	}
	if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	return os.Chmod(path, 0o700)
}

func ensurePrivateChildDir(root string, parts ...string) error {
	current := root
	if err := ensurePrivateDir(current); err != nil {
		return err
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." || filepath.Base(part) != part {
			return fmt.Errorf("invalid diagnostic path component %q", part)
		}
		current = filepath.Join(current, part)
		if err := ensurePrivateDir(current); err != nil {
			return err
		}
	}
	return nil
}

func ensureDestinationDir(root string, dest destination) error {
	if dest.scope == ScopeRunner {
		return ensurePrivateChildDir(root, "runners", string(dest.environment))
	}
	return ensurePrivateDir(root)
}
