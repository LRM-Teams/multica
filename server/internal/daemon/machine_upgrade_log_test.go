package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func overrideVersionStoreRoot(t *testing.T, root string) {
	t.Helper()
	previousRoot := versionStoreRootFn
	versionStoreRootFn = func() (string, error) { return root, nil }
	t.Cleanup(func() { versionStoreRootFn = previousRoot })
}

func readMachineUpgradeEvents(t *testing.T, root string) []machineUpgradeEvent {
	t.Helper()
	dir := filepath.Join(root, "machine-upgrades")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read machine-upgrades dir: %v", err)
	}
	var events []machineUpgradeEvent
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "upgrade-") || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			var event machineUpgradeEvent
			if err := json.Unmarshal([]byte(line), &event); err != nil {
				t.Fatalf("corrupt upgrade history line %q: %v", line, err)
			}
			events = append(events, event)
		}
	}
	return events
}

func TestMachineUpgradeEventLogAppendsOwnerOnlyRedactedHistory(t *testing.T) {
	root := t.TempDir()
	overrideVersionStoreRoot(t, root)
	now := time.Date(2026, time.August, 7, 12, 0, 0, 0, time.UTC)
	log := newMachineUpgradeEventLog(func() time.Time { return now })
	if err := log.Append(machineUpgradeEvent{
		Event:               machineUpgradeEventAccepted,
		UpgradeID:           "upgrade-1",
		Generation:          "generation-1",
		SourceVersion:       "v0.4.18",
		TargetVersion:       "v0.4.19",
		IncumbentGeneration: 3,
	}); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "machine-upgrades")
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("upgrade history files = %v, %v", entries, err)
	}
	if want := "upgrade-2026-08-07-000.jsonl"; entries[0].Name() != want {
		t.Fatalf("upgrade history file = %s, want %s", entries[0].Name(), want)
	}
	path := filepath.Join(dir, entries[0].Name())
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("upgrade history permissions = %o, want 0600", info.Mode().Perm())
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"prompt", "credential", "environment", "stderr", "token", "path"} {
		if strings.Contains(string(body), forbidden) {
			t.Fatalf("upgrade history leaked %q: %s", forbidden, body)
		}
	}
	events := readMachineUpgradeEvents(t, root)
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	event := events[0]
	if event.Event != "accepted" || event.UpgradeID != "upgrade-1" || event.SourceVersion != "v0.4.18" || event.TargetVersion != "v0.4.19" || event.IncumbentGeneration != 3 {
		t.Fatalf("event = %+v", event)
	}
	if event.At != now.Format(time.RFC3339Nano) {
		t.Fatalf("event at = %q", event.At)
	}
}

func TestMachineUpgradeEventLogDisabledWithoutRoot(t *testing.T) {
	previousRoot := versionStoreRootFn
	versionStoreRootFn = func() (string, error) { return "", fmt.Errorf("no home") }
	t.Cleanup(func() { versionStoreRootFn = previousRoot })
	log := newMachineUpgradeEventLog(nil)
	if err := log.Append(machineUpgradeEvent{Event: machineUpgradeEventFailed, ErrorCode: "stage_failed"}); err != nil {
		t.Fatalf("disabled log append must be a no-op, got %v", err)
	}
	if err := log.Cleanup(); err != nil {
		t.Fatalf("disabled log cleanup must be a no-op, got %v", err)
	}
}

func TestMachineUpgradeEventLogCleanupPreservesJournals(t *testing.T) {
	root := t.TempDir()
	overrideVersionStoreRoot(t, root)
	now := time.Date(2026, time.August, 10, 12, 0, 0, 0, time.UTC)
	dir := filepath.Join(root, "machine-upgrades")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	// A recovery journal plus over-cap history files, one of them current.
	journalPath := filepath.Join(dir, "upgrade-1.json")
	if err := os.WriteFile(journalPath, []byte(`{"id":"upgrade-1"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(dir, "upgrade-2026-08-01-000.jsonl")
	newer := filepath.Join(dir, "upgrade-2026-08-09-000.jsonl")
	current := filepath.Join(dir, "upgrade-2026-08-10-000.jsonl")
	for _, path := range []string{old, newer, current} {
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Truncate(path, machineUpgradeLogCapBytes/2+1); err != nil {
			t.Fatal(err)
		}
	}
	oldTime := now.Add(-8 * 24 * time.Hour)
	if err := os.Chtimes(old, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	log := newMachineUpgradeEventLog(func() time.Time { return now })
	log.writer.currentPath = current
	if err := log.Cleanup(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(journalPath); err != nil {
		t.Fatalf("recovery journal was deleted: %v", err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatalf("expired upgrade history remains: %v", err)
	}
	if _, err := os.Stat(current); err != nil {
		t.Fatalf("current writable upgrade history was deleted: %v", err)
	}
	if _, err := os.Stat(newer); !os.IsNotExist(err) {
		t.Fatalf("oldest over-cap upgrade history remains: %v", err)
	}
}

func TestMachineUpgradeEventLogBoundsError(t *testing.T) {
	root := t.TempDir()
	overrideVersionStoreRoot(t, root)
	log := newMachineUpgradeEventLog(nil)
	long := strings.Repeat("x ", 500)
	if err := log.Append(machineUpgradeEvent{Event: machineUpgradeEventFailed, ErrorCode: "stage_failed", Error: long}); err != nil {
		t.Fatal(err)
	}
	events := readMachineUpgradeEvents(t, root)
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	if len(events[0].Error) > updateObservationErrorLimit {
		t.Fatalf("error length = %d, want <= %d", len(events[0].Error), updateObservationErrorLimit)
	}
	if strings.Contains(events[0].Error, "\n") || strings.Contains(events[0].Error, "  ") {
		t.Fatalf("error was not whitespace-collapsed: %q", events[0].Error)
	}
}

func TestHandleMachineUpgradeAlreadyCurrentAppendsEvent(t *testing.T) {
	root := t.TempDir()
	overrideVersionStoreRoot(t, root)
	previousDetect := detectAgentVersion
	detectAgentVersion = func(context.Context, string) (string, error) { return "codex-cli 9.9.9", nil }
	t.Cleanup(func() { detectAgentVersion = previousDetect })
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/accept"):
			generation := "generation-1"
			_ = json.NewEncoder(w).Encode(MachineUpgradeReceipt{
				ID:                   "upgrade-1",
				Phase:                "converging",
				AcceptedGeneration:   &generation,
				AcceptedRuntimeIDs:   []string{"runtime-1"},
				AcceptedWorkspaceIDs: []string{"workspace-1"},
			})
		case r.URL.Path == "/api/daemon/register":
			_ = json.NewEncoder(w).Encode(RegisterResponse{Runtimes: []Runtime{{
				ID: "runtime-1", WorkspaceID: "workspace-1", Name: "Codex", Provider: "codex", Status: "online",
			}}})
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer server.Close()
	d := &Daemon{
		cfg: Config{
			CLIVersion: "v9.9.9", DaemonID: "computer-1",
			Agents: map[string]AgentEntry{"codex": {Path: "codex"}},
		},
		client:        NewClient(server.URL),
		logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		agentVersions: make(map[string]string),
		workspaces: map[string]*workspaceState{
			"workspace-1": newWorkspaceState("workspace-1", []string{"runtime-1"}),
		},
	}
	d.client.SetToken("test-token")
	d.machineUpgradeLog = newMachineUpgradeEventLog(nil)
	d.handleMachineUpgrade(context.Background(), "runtime-1", &PendingMachineUpgrade{ID: "upgrade-1", TargetVersion: "9.9.9"})
	events := readMachineUpgradeEvents(t, root)
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	if events[0].Event != machineUpgradeEventAlreadyCurrent || events[0].UpgradeID != "upgrade-1" || events[0].TargetVersion != "9.9.9" {
		t.Fatalf("event = %+v", events[0])
	}
}

func TestFailMachineUpgradeAppendsFailedEvent(t *testing.T) {
	root := t.TempDir()
	overrideVersionStoreRoot(t, root)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	d := &Daemon{
		cfg:    Config{CLIVersion: "v0.4.18"},
		client: NewClient(server.URL),
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	d.client.SetToken("test-token")
	d.machineUpgradeLog = newMachineUpgradeEventLog(nil)
	d.failMachineUpgrade(context.Background(), "runtime-1", "upgrade-1", "stage_failed", fmt.Errorf("stage release v0.4.19: invalid staged metadata"))
	events := readMachineUpgradeEvents(t, root)
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	event := events[0]
	if event.Event != machineUpgradeEventFailed || event.ErrorCode != "stage_failed" || event.SourceVersion != "v0.4.18" {
		t.Fatalf("event = %+v", event)
	}
	if !strings.Contains(event.Error, "invalid staged metadata") {
		t.Fatalf("event error = %q", event.Error)
	}
}

func TestMarkMachineUpgradeCandidateReadyAppendsEvent(t *testing.T) {
	root := t.TempDir()
	overrideVersionStoreRoot(t, root)
	d := &Daemon{
		cfg:    Config{CLIVersion: "v10.0.0"},
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	d.machineUpgradeLog = newMachineUpgradeEventLog(nil)
	journal := &machineUpgradeJournal{
		ID: "upgrade-1", Generation: "generation-successor", SourceVersion: "v9.9.9", TargetVersion: "v10.0.0", RuntimeIDs: []string{"runtime-1"}, Phase: "handoff",
	}
	if err := d.writeMachineUpgradeJournal(journal); err != nil {
		t.Fatal(err)
	}
	if err := d.markMachineUpgradeCandidateReady(); err != nil {
		t.Fatal(err)
	}
	events := readMachineUpgradeEvents(t, root)
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	if events[0].Event != machineUpgradeEventCandidateReady || events[0].Generation != "generation-successor" || events[0].TargetVersion != "v10.0.0" {
		t.Fatalf("event = %+v", events[0])
	}
}
