package diagnosticlog

import (
	"bufio"
	"compress/gzip"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	testComputerID  = "11111111-1111-4111-8111-111111111111"
	testWorkspaceID = "22222222-2222-4222-8222-222222222222"
	testAgentID     = "33333333-3333-4333-8333-333333333333"
)

func TestServiceAndRunnerStreamsAreScopedAndVersioned(t *testing.T) {
	now := time.Date(2026, time.August, 10, 12, 0, 0, 123000000, time.UTC)
	root := filepath.Join(t.TempDir(), "logs")
	store := openTestStore(t, root, func() time.Time { return now }, Limits{})

	service, err := store.Service(ServiceOptions{
		ComputerID:         testComputerID,
		ComputerGeneration: "computer-generation-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	runner, err := store.Runner(RunnerOptions{
		Environment:        EnvironmentProduction,
		WorkspaceID:        testWorkspaceID,
		RunnerGeneration:   "runner-generation-1",
		ComputerID:         testComputerID,
		ComputerGeneration: "computer-generation-1",
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := service.Record(Event{
		Name:      EventComputerStateChanged,
		Level:     LevelInfo,
		Component: "machine_service",
		Fields:    Fields{From: "starting", To: "ready", Outcome: "succeeded"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := runner.Record(Event{
		Name:      EventChatTurnCheckpoint,
		Level:     LevelInfo,
		Component: "agent_inbox",
		Identity: Identity{
			AgentID:         testAgentID,
			TaskID:          "44444444-4444-4444-8444-444444444444",
			SourceMessageID: "55555555-5555-4555-8555-555555555555",
		},
		Fields: Fields{Phase: "lease_acquired", Outcome: "succeeded"},
	}); err != nil {
		t.Fatal(err)
	}

	servicePath := filepath.Join(root, "service.log")
	runnerPath := filepath.Join(root, "runners", string(EnvironmentProduction), testWorkspaceID+".log")
	serviceRecord := readOneRecord(t, servicePath)
	runnerRecord := readOneRecord(t, runnerPath)

	assertField(t, serviceRecord, "schema_version", float64(1))
	assertField(t, serviceRecord, "scope", "service")
	assertField(t, serviceRecord, "event", string(EventComputerStateChanged))
	assertField(t, serviceRecord, "computer_id", testComputerID)
	assertField(t, serviceRecord, "computer_generation", "computer-generation-1")
	assertField(t, serviceRecord, "stream_seq", float64(1))
	if _, exists := serviceRecord["workspace_id"]; exists {
		t.Fatalf("service record unexpectedly contains workspace_id: %#v", serviceRecord)
	}

	assertField(t, runnerRecord, "scope", "runner")
	assertField(t, runnerRecord, "environment", string(EnvironmentProduction))
	assertField(t, runnerRecord, "workspace_id", testWorkspaceID)
	assertField(t, runnerRecord, "runner_generation", "runner-generation-1")
	assertField(t, runnerRecord, "source_message_id", "55555555-5555-4555-8555-555555555555")

	for _, path := range []string{root, filepath.Dir(runnerPath)} {
		info, statErr := os.Stat(path)
		if statErr != nil {
			t.Fatal(statErr)
		}
		if got := info.Mode().Perm(); got != 0o700 {
			t.Fatalf("directory %s permissions = %o, want 0700", path, got)
		}
	}
	for _, path := range []string{servicePath, runnerPath} {
		info, statErr := os.Stat(path)
		if statErr != nil {
			t.Fatal(statErr)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Fatalf("file %s permissions = %o, want 0600", path, got)
		}
	}
}

func TestRunnerRejectsInvalidDestination(t *testing.T) {
	root := filepath.Join(t.TempDir(), "logs")
	store := openTestStore(t, root, time.Now, Limits{})
	if _, err := store.Service(ServiceOptions{}); err == nil {
		t.Fatal("Service accepted an empty computer_generation")
	}
	tests := []RunnerOptions{
		{Environment: "staging", WorkspaceID: testWorkspaceID, RunnerGeneration: "generation-1"},
		{Environment: EnvironmentProduction, WorkspaceID: "../../escape", RunnerGeneration: "generation-1"},
		{Environment: EnvironmentProduction, WorkspaceID: testWorkspaceID},
	}
	for _, options := range tests {
		if _, err := store.Runner(options); err == nil {
			t.Fatalf("Runner(%+v) succeeded, want validation error", options)
		}
	}
	service, err := store.Service(ServiceOptions{ComputerGeneration: "generation-1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Record(Event{Name: EventRunnerStateChanged, Level: LevelInfo, Component: "test"}); err == nil {
		t.Fatal("Service accepted a Runner-scoped event")
	}
	runner, err := store.Runner(RunnerOptions{Environment: EnvironmentProduction, WorkspaceID: testWorkspaceID, RunnerGeneration: "generation-1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := runner.Record(Event{Name: EventComputerStateChanged, Level: LevelInfo, Component: "test"}); err == nil {
		t.Fatal("Runner accepted a Service-scoped event")
	}
	if err := runner.Record(Event{
		Name:      EventDeliveryStateChanged,
		Level:     LevelInfo,
		Component: "test",
		Identity:  Identity{DeliveryID: "message:55555555-5555-4555-8555-555555555555:agent:" + testAgentID},
	}); err != nil {
		t.Fatalf("Runner rejected canonical Message delivery identity: %v", err)
	}
	if err := service.Record(Event{Name: EventComputerStateChanged, Level: LevelInfo, Component: "test", Identity: Identity{RequestID: "https://user:password@example.com"}}); err == nil {
		t.Fatal("Service accepted a URL-shaped identity")
	}
	if err := service.Record(Event{Name: EventComputerStateChanged, Level: LevelInfo, Component: "test", Fields: Fields{ReasonCode: "sk-1234567890abcdefghijklmnop"}}); err == nil {
		t.Fatal("Service accepted a credential-shaped reason code")
	}
	if err := service.Record(Event{
		Name:      EventComputerStateChanged,
		Level:     LevelInfo,
		Component: "test",
		Fields:    Fields{ServiceOrigin: "file:///Users/alice/private.sock"},
	}); err != nil {
		t.Fatal(err)
	}
	record := readOneRecord(t, filepath.Join(root, "service.log"))
	assertField(t, record, "redaction_failed", true)
	if _, exists := record["service_origin"]; exists {
		t.Fatalf("unsafe service_origin was not omitted: %#v", record)
	}
}

func TestLimitsCannotExpandTheStorageContract(t *testing.T) {
	defaults := DefaultLimits()
	store := openTestStore(t, filepath.Join(t.TempDir(), "logs"), time.Now, Limits{
		SegmentBytes: defaults.SegmentBytes + 1,
		SegmentAge:   defaults.SegmentAge + time.Second,
		Retention:    defaults.Retention + time.Second,
		StreamBytes:  defaults.StreamBytes + 1,
		GlobalBytes:  defaults.GlobalBytes + 1,
	})
	if store.limits != defaults {
		t.Fatalf("expanded limits = %+v, want defaults %+v", store.limits, defaults)
	}
}

func TestLoggerDelegatesRollingPolicyToLumberjack(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "logs"), time.Now, Limits{})
	logger, err := store.Service(ServiceOptions{ComputerGeneration: "generation-1"})
	if err != nil {
		t.Fatal(err)
	}
	if logger.sink == nil {
		t.Fatal("rolling sink is nil")
	}
	if logger.sink.MaxSize != 16 || logger.sink.MaxAge != 30 || logger.sink.MaxBackups != 7 || !logger.sink.Compress || logger.sink.LocalTime {
		t.Fatalf("lumberjack policy = %+v, want 16 MiB, 30 days, 7 backups, gzip, UTC", logger.sink)
	}
}

func TestRecordRedactsAndBoundsUntrustedEvidence(t *testing.T) {
	now := time.Date(2026, time.August, 10, 12, 0, 0, 0, time.UTC)
	root := filepath.Join(t.TempDir(), "logs")
	store := openTestStore(t, root, func() time.Time { return now }, Limits{})
	service, err := store.Service(ServiceOptions{ComputerID: testComputerID, ComputerGeneration: "generation-1"})
	if err != nil {
		t.Fatal(err)
	}

	secret := "canary-super-secret-value"
	detail := "Authorization: Bearer " + secret + " API_KEY=" + secret +
		" https://alice:" + secret + "@example.com/private?token=" + secret + "#fragment" +
		" /Users/alice/private/file.txt\x1b[31m\n sk-1234567890abcdefghijklmnop" +
		" AKIA1234567890ABCDEF ftp://alice:" + secret + "@example.com/private" +
		` {"token":"` + secret + `"} --password ` + secret + " " + strings.Repeat("oversized ", 4000)
	stderr := append([]byte("password="+secret+"\n"), []byte{0xff, 0xfe}...)
	stderr = append(stderr, []byte(strings.Repeat("x", 5000))...)
	if err := service.Record(Event{
		Name:      EventComputerStateChanged,
		Level:     LevelError,
		Component: "machine_service",
		Fields:    Fields{ReasonCode: "startup_failed", Outcome: "failed", ServiceOrigin: "HTTPS://alice:" + secret + "@example.com/private?token=" + secret},
		Evidence:  Evidence{Detail: detail, StderrTail: stderr},
	}); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(root, "service.log")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(body) > MaxRecordBytes {
		t.Fatalf("record bytes = %d, want <= %d", len(body), MaxRecordBytes)
	}
	for _, forbidden := range []string{secret, "alice:", "?token=", "#fragment", "/Users/alice/private", "\x1b[31m", "sk-1234567890abcdefghijklmnop", "AKIA1234567890ABCDEF", "ftp://", "\xff"} {
		if strings.Contains(string(body), forbidden) {
			t.Fatalf("record leaked %q: %s", forbidden, body)
		}
	}
	record := readOneRecord(t, path)
	if got := record["diagnostic"]; got == nil || !strings.Contains(got.(string), "https://example.com") {
		t.Fatalf("sanitized URL = %#v", got)
	}
	assertField(t, record, "service_origin", "https://example.com")
	truncated, ok := record["truncated_fields"].([]any)
	if !ok || len(truncated) == 0 {
		t.Fatalf("truncated_fields = %#v", record["truncated_fields"])
	}

	now = now.Add(25 * time.Hour)
	if err := service.Record(Event{Name: EventComputerStateChanged, Level: LevelInfo, Component: "machine_service"}); err != nil {
		t.Fatal(err)
	}
	waitForCompressedSegment(t, root, "service-")
	allBytes := readDiagnosticTree(t, root)
	for _, forbidden := range []string{secret, "alice:", "?token=", "#fragment", "/Users/alice/private", "sk-1234567890abcdefghijklmnop", "AKIA1234567890ABCDEF", "ftp://"} {
		if strings.Contains(string(allBytes), forbidden) {
			t.Fatalf("active or compressed diagnostics leaked %q", forbidden)
		}
	}
}

func TestRotationCompressesClosedSegmentAndRespectsAge(t *testing.T) {
	now := time.Date(2026, time.August, 10, 12, 0, 0, 0, time.UTC)
	root := filepath.Join(t.TempDir(), "logs")
	limits := Limits{SegmentBytes: 1 << 20, SegmentAge: time.Hour, Retention: 30 * 24 * time.Hour, StreamBytes: 4 << 20, GlobalBytes: 8 << 20}
	store := openTestStore(t, root, func() time.Time { return now }, limits)
	service, err := store.Service(ServiceOptions{ComputerGeneration: "generation-1"})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 600; i++ {
		if err := service.Record(Event{Name: EventComputerStateChanged, Level: LevelInfo, Component: "machine_service", Evidence: Evidence{Detail: strings.Repeat("a", 3000)}}); err != nil {
			t.Fatal(err)
		}
	}
	waitForCompressedSegment(t, root, "service-")

	now = now.Add(2 * time.Hour)
	if err := service.Record(Event{Name: EventComputerStateChanged, Level: LevelInfo, Component: "machine_service"}); err != nil {
		t.Fatal(err)
	}
	waitForCompressedSegment(t, root, "service-")
	if _, err := os.Stat(filepath.Join(root, "service.log")); err != nil {
		t.Fatalf("active service.log missing after rotation: %v", err)
	}
}

func TestCleanupEnforcesRetentionStreamAndGlobalBudgets(t *testing.T) {
	now := time.Date(2026, time.August, 10, 12, 0, 0, 0, time.UTC)
	root := filepath.Join(t.TempDir(), "logs")
	limits := Limits{SegmentBytes: 500, SegmentAge: time.Hour, Retention: 24 * time.Hour, StreamBytes: 900, GlobalBytes: 1400}
	store := openTestStore(t, root, func() time.Time { return now }, limits)
	service, err := store.Service(ServiceOptions{ComputerGeneration: "generation-1"})
	if err != nil {
		t.Fatal(err)
	}
	runner, err := store.Runner(RunnerOptions{Environment: EnvironmentProduction, WorkspaceID: testWorkspaceID, RunnerGeneration: "runner-generation-1"})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 12; i++ {
		writer := service
		if i%2 == 1 {
			writer = runner
		}
		eventName := EventComputerStateChanged
		if writer == runner {
			eventName = EventRunnerStateChanged
		}
		if err := writer.Record(Event{Name: eventName, Level: LevelInfo, Component: "test", Evidence: Evidence{Detail: strings.Repeat("budget-data-", 40)}}); err != nil {
			t.Fatal(err)
		}
		now = now.Add(10 * time.Minute)
	}

	old := filepath.Join(root, "service-2026-08-01T00-00-00.000.log.gz")
	if err := os.WriteFile(old, []byte(strings.Repeat("old", 100)), 0o600); err != nil {
		t.Fatal(err)
	}
	oldTime := now.Add(-48 * time.Hour)
	if err := os.Chtimes(old, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	if err := store.Cleanup(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatalf("expired segment remains: %v", err)
	}
	if got := treeBytes(t, root); got > limits.GlobalBytes {
		t.Fatalf("diagnostic tree bytes = %d, want <= %d", got, limits.GlobalBytes)
	}
	for _, health := range store.Health() {
		if health.Bytes > limits.StreamBytes {
			t.Fatalf("stream %s bytes = %d, want <= %d", health.Scope, health.Bytes, limits.StreamBytes)
		}
	}
}

// Lumberjack serializes concurrent writes through one logger. Production has
// exactly one Store owner because Daemon.Run acquires the Computer resident
// lease before initializing diagnostics.
func TestConcurrentLoggerWritesWholeJSONLines(t *testing.T) {
	root := filepath.Join(t.TempDir(), "logs")
	store := openTestStore(t, root, time.Now, Limits{})
	logger, err := store.Service(ServiceOptions{ComputerGeneration: "generation-a"})
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				if err := logger.Record(Event{Name: EventComputerStateChanged, Level: LevelInfo, Component: "concurrency", Fields: Fields{Status: "ok"}}); err != nil {
					t.Errorf("Record: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()

	file, err := os.Open(filepath.Join(root, "service.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	count := 0
	for scanner.Scan() {
		var record map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			t.Fatalf("invalid line %d: %v: %q", count+1, err, scanner.Bytes())
		}
		count++
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if count != 200 {
		t.Fatalf("line count = %d, want 200", count)
	}
}

func TestPartialTailIsQuarantinedBeforeNextRecord(t *testing.T) {
	root := filepath.Join(t.TempDir(), "logs")
	store := openTestStore(t, root, time.Now, Limits{})
	logger, err := store.Service(ServiceOptions{ComputerGeneration: "generation-1"})
	if err != nil {
		t.Fatal(err)
	}
	active := filepath.Join(root, "service.log")
	if err := os.WriteFile(active, []byte(`{"schema_version":1`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := logger.Record(Event{Name: EventComputerStateChanged, Level: LevelInfo, Component: "test"}); err != nil {
		t.Fatal(err)
	}
	if got := countLines(t, active); got != 1 {
		t.Fatalf("new active line count = %d, want 1", got)
	}
	record := readOneRecord(t, active)
	assertField(t, record, "event", string(EventComputerStateChanged))
	waitForCompressedSegment(t, root, "service-")
}

func TestSymlinkEscapeIsRejected(t *testing.T) {
	root := filepath.Join(t.TempDir(), "logs")
	outside := t.TempDir()
	store := openTestStore(t, root, time.Now, Limits{})
	if err := os.Symlink(outside, filepath.Join(root, "runners")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	if _, err := store.Runner(RunnerOptions{Environment: EnvironmentProduction, WorkspaceID: testWorkspaceID, RunnerGeneration: "generation-1"}); err == nil {
		t.Fatal("Runner succeeded through symlink, want rejection")
	}
	entries, err := os.ReadDir(outside)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("outside directory was modified: %v", entries)
	}

	outsideFile := filepath.Join(t.TempDir(), "outside.log")
	if err := os.WriteFile(outsideFile, []byte("unchanged"), 0o600); err != nil {
		t.Fatal(err)
	}
	service, err := store.Service(ServiceOptions{ComputerGeneration: "generation-1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsideFile, filepath.Join(root, "service.log")); err != nil {
		t.Skipf("file symlink unsupported: %v", err)
	}
	if err := service.Record(Event{Name: EventComputerStateChanged, Level: LevelInfo, Component: "test"}); err == nil {
		t.Fatal("Service write followed a symlink")
	}
	outsideBody, err := os.ReadFile(outsideFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(outsideBody) != "unchanged" {
		t.Fatalf("outside file modified: %q", outsideBody)
	}
}

func TestSinkFailureIsVisibleAndRecoveryIsNonFatal(t *testing.T) {
	root := filepath.Join(t.TempDir(), "logs")
	store := openTestStore(t, root, time.Now, Limits{})
	logger, err := store.Service(ServiceOptions{ComputerGeneration: "generation-1"})
	if err != nil {
		t.Fatal(err)
	}
	servicePath := filepath.Join(root, "service.log")
	if err := os.Mkdir(servicePath, 0o700); err != nil {
		t.Fatal(err)
	}
	failure := Event{Name: EventComputerStateChanged, Level: LevelWarn, Component: "machine_service", Fields: Fields{ReasonCode: "sink_test", Outcome: "failed"}}
	if err := logger.Record(failure); err == nil {
		t.Fatal("Record succeeded with directory at service.log")
	}
	health := logger.Health()
	if health.State != SinkDegraded || health.DroppedRecords != 1 || health.LastErrorClass == "" {
		t.Fatalf("degraded health = %+v", health)
	}
	if err := os.Remove(servicePath); err != nil {
		t.Fatal(err)
	}
	if err := logger.Record(failure); err != nil {
		t.Fatal(err)
	}
	if got := countLines(t, servicePath); got != 1 {
		t.Fatalf("recovered sink line count = %d, want 1", got)
	}
	health = logger.Health()
	if health.State != SinkHealthy || health.LastSuccessfulWriteAt.IsZero() {
		t.Fatalf("recovered health = %+v", health)
	}
}

func TestEquivalentFailuresAreSuppressedAndRecoverySummarizes(t *testing.T) {
	now := time.Date(2026, time.August, 10, 12, 0, 0, 0, time.UTC)
	root := filepath.Join(t.TempDir(), "logs")
	store := openTestStore(t, root, func() time.Time { return now }, Limits{})
	logger, err := store.Service(ServiceOptions{ComputerGeneration: "generation-1"})
	if err != nil {
		t.Fatal(err)
	}
	failure := Event{Name: EventComputerStateChanged, Level: LevelWarn, Component: "machine_service", Fields: Fields{ReasonCode: "connection_failed", Outcome: "failed"}}
	for i := 0; i < 3; i++ {
		if err := logger.Record(failure); err != nil {
			t.Fatal(err)
		}
	}
	if got := countLines(t, filepath.Join(root, "service.log")); got != 1 {
		t.Fatalf("failure lines = %d, want 1", got)
	}
	if got := logger.Health().SuppressedRecords; got != 2 {
		t.Fatalf("suppressed records = %d, want 2", got)
	}

	now = now.Add(5 * time.Minute)
	if err := logger.Record(failure); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)
	recovery := Event{Name: EventComputerStateChanged, Level: LevelInfo, Component: "machine_service", Fields: Fields{ReasonCode: "connection_failed", Outcome: "recovered"}}
	if err := logger.Record(recovery); err != nil {
		t.Fatal(err)
	}
	records := readRecords(t, filepath.Join(root, "service.log"))
	if len(records) != 3 {
		t.Fatalf("records = %d, want 3", len(records))
	}
	last := records[len(records)-1]
	if last["suppressed_count"] == nil || last["attempt_count"] == nil || last["outage_duration_ms"] == nil {
		t.Fatalf("recovery summary missing: %#v", last)
	}
}

func openTestStore(t *testing.T, root string, now func() time.Time, limits Limits) *Store {
	t.Helper()
	store, err := Open(Config{Root: root, Now: now, Limits: limits})
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func readOneRecord(t *testing.T, path string) map[string]any {
	t.Helper()
	records := readRecords(t, path)
	if len(records) != 1 {
		t.Fatalf("records in %s = %d, want 1", path, len(records))
	}
	return records[0]
}

func readRecords(t *testing.T, path string) []map[string]any {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var records []map[string]any
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var record map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			t.Fatalf("unmarshal record: %v: %q", err, scanner.Bytes())
		}
		records = append(records, record)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return records
}

func assertField(t *testing.T, record map[string]any, key string, want any) {
	t.Helper()
	if got := record[key]; got != want {
		t.Fatalf("%s = %#v, want %#v; record=%#v", key, got, want, record)
	}
}

func assertHasCompressedSegment(t *testing.T, root, prefix string) bool {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), prefix) && strings.HasSuffix(entry.Name(), ".log.gz") {
			info, infoErr := entry.Info()
			if infoErr != nil {
				t.Fatal(infoErr)
			}
			if got := info.Mode().Perm(); got != 0o600 {
				t.Fatalf("compressed segment %s permissions = %o, want 0600", entry.Name(), got)
			}
			file, openErr := os.Open(filepath.Join(root, entry.Name()))
			if openErr != nil {
				return false
			}
			reader, gzipErr := gzip.NewReader(file)
			if gzipErr != nil {
				file.Close()
				return false
			}
			if _, readErr := io.ReadAll(reader); readErr != nil {
				reader.Close()
				file.Close()
				return false
			}
			reader.Close()
			file.Close()
			return true
		}
	}
	return false
}

func waitForCompressedSegment(t *testing.T, root, prefix string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if assertHasCompressedSegment(t, root, prefix) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no compressed segment with prefix %q under %s", prefix, root)
}

func countLines(t *testing.T, path string) int {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	count := 0
	for scanner.Scan() {
		count++
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return count
}

func treeBytes(t *testing.T, root string) int64 {
	t.Helper()
	var total int64
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			total += info.Size()
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return total
}

func readDiagnosticTree(t *testing.T, root string) []byte {
	t.Helper()
	var combined []byte
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		var reader io.Reader = file
		var gzipReader *gzip.Reader
		if strings.HasSuffix(path, ".gz") {
			gzipReader, err = gzip.NewReader(file)
			if err != nil {
				file.Close()
				return err
			}
			reader = gzipReader
		}
		body, err := io.ReadAll(reader)
		if gzipReader != nil {
			_ = gzipReader.Close()
		}
		_ = file.Close()
		if err != nil {
			return err
		}
		combined = append(combined, body...)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return combined
}
