package daemon

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func newTestUpdateObservationCoordinator(t *testing.T, path string) *updateObservationCoordinator {
	t.Helper()
	return newUpdateObservationCoordinator(Config{
		ServerBaseURL:          "https://api.multica.ai",
		CLIVersion:             "v0.3.72",
		AutoUpdateEnabled:      true,
		AutoUpdateConfigSource: "env_enabled",
		UpdateObservationPath:  path,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func assertUpdateObservation(t *testing.T, coordinator *updateObservationCoordinator, phase, outcome string) {
	t.Helper()
	observation := coordinator.Snapshot()
	if observation.Phase != phase || observation.LastOutcome != outcome {
		t.Fatalf("update observation phase/outcome = %s/%s, want %s/%s: %+v", observation.Phase, observation.LastOutcome, phase, outcome, observation)
	}
}

func TestUpdateObservationCoordinatorPersistsBeforePublishing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon-update-status.json")
	coordinator := newTestUpdateObservationCoordinator(t, path)
	initial := coordinator.Snapshot()
	if initial.Revision != 1 || initial.Phase != "disabled" || initial.LastOutcome != "explicit_only" || initial.AutoUpdateEffectiveEnabled || initial.ConfigSource != "deprecated_noop" || initial.IneligibleReason != "explicit_only" {
		t.Fatalf("initial observation = %+v", initial)
	}

	if err := coordinator.Transition(func(observation *protocol.DaemonUpdateObservation) {
		observation.Phase = "checking"
		observation.AttemptSource = "auto"
		observation.LastAttemptAt = "2026-07-27T00:00:00Z"
	}); err != nil {
		t.Fatalf("transition: %v", err)
	}
	current := coordinator.Snapshot()
	if current.Revision != 2 || current.Phase != "checking" {
		t.Fatalf("current observation = %+v", current)
	}
	persisted, err := readPersistedUpdateObservation(path)
	if err != nil {
		t.Fatalf("read persisted observation: %v", err)
	}
	if persisted != current {
		t.Fatalf("persisted = %+v, current = %+v", persisted, current)
	}

	if err := coordinator.Transition(func(*protocol.DaemonUpdateObservation) {}); err != nil {
		t.Fatalf("duplicate transition: %v", err)
	}
	if got := coordinator.Snapshot().Revision; got != 2 {
		t.Fatalf("duplicate transition revision = %d, want 2", got)
	}
}

func TestUpdateObservationCoordinatorNormalizesInterruptedPredecessor(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon-update-status.json")
	first := newTestUpdateObservationCoordinator(t, path)
	if err := first.Transition(func(observation *protocol.DaemonUpdateObservation) {
		observation.Phase = "updating"
		observation.AttemptSource = "server"
		observation.LastAttemptAt = "2026-07-27T00:00:00Z"
		observation.TargetVersion = "v0.3.73"
	}); err != nil {
		t.Fatalf("persist updating state: %v", err)
	}

	successor := newTestUpdateObservationCoordinator(t, path).Snapshot()
	if successor.SessionID == first.Snapshot().SessionID {
		t.Fatal("successor reused predecessor session_id")
	}
	if successor.Revision != 1 || successor.Phase != "disabled" || successor.LastOutcome != "interrupted" {
		t.Fatalf("successor observation = %+v", successor)
	}
	if successor.ErrorCode != "daemon_restarted_during_update" || successor.TargetVersion != "v0.3.73" {
		t.Fatalf("successor interruption details = %+v", successor)
	}
}

func TestUpdateObservationCoordinatorReplaysRestartPendingSuccess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon-update-status.json")
	first := newTestUpdateObservationCoordinator(t, path)
	if err := first.Transition(func(observation *protocol.DaemonUpdateObservation) {
		observation.Phase = "restart_pending"
		observation.AttemptSource = "auto"
		observation.LastAttemptAt = "2026-07-27T00:00:00Z"
		observation.LastOutcome = "update_succeeded"
		observation.TargetVersion = "v0.3.73"
	}); err != nil {
		t.Fatalf("persist restart_pending state: %v", err)
	}

	successor := newTestUpdateObservationCoordinator(t, path).Snapshot()
	if successor.Phase != "disabled" ||
		successor.LastOutcome != "update_succeeded" ||
		successor.TargetVersion != "v0.3.73" {
		t.Fatalf("successor replay = %+v", successor)
	}
}

func TestUpdateObservationCoordinatorPersistenceFailureKeepsSnapshot(t *testing.T) {
	root := t.TempDir()
	parentFile := filepath.Join(root, "not-a-directory")
	if err := os.WriteFile(parentFile, []byte("x"), 0o600); err != nil {
		t.Fatalf("create parent file: %v", err)
	}
	coordinator := newTestUpdateObservationCoordinator(t, "")
	coordinator.path = filepath.Join(parentFile, "daemon-update-status.json")
	before := coordinator.Snapshot()

	err := coordinator.Transition(func(observation *protocol.DaemonUpdateObservation) {
		observation.Phase = "checking"
	})
	if err == nil {
		t.Fatal("transition succeeded despite persistence failure")
	}
	if after := coordinator.Snapshot(); after != before {
		t.Fatalf("snapshot changed after persistence failure: before=%+v after=%+v", before, after)
	}
}

func TestUpdateObservationCoordinatorDoesNotPublishUndurableInitialState(t *testing.T) {
	root := t.TempDir()
	parentFile := filepath.Join(root, "not-a-directory")
	if err := os.WriteFile(parentFile, []byte("x"), 0o600); err != nil {
		t.Fatalf("create parent file: %v", err)
	}
	coordinator := newTestUpdateObservationCoordinator(t, filepath.Join(parentFile, "daemon-update-status.json"))
	if snapshot := coordinator.PublishedSnapshot(); snapshot != nil {
		t.Fatalf("published undurable initial observation = %+v", snapshot)
	}
	if err := coordinator.Transition(func(observation *protocol.DaemonUpdateObservation) {
		observation.Phase = "checking"
	}); err == nil {
		t.Fatal("transition succeeded despite broken persistence path")
	}
	if snapshot := coordinator.PublishedSnapshot(); snapshot != nil {
		t.Fatalf("published transition after persistence failure = %+v", snapshot)
	}
}

func TestUpdateObservationCoordinatorRejectsUnknownErrorCode(t *testing.T) {
	coordinator := newTestUpdateObservationCoordinator(t, filepath.Join(t.TempDir(), "daemon-update-status.json"))
	before := coordinator.Snapshot()
	err := coordinator.Transition(func(observation *protocol.DaemonUpdateObservation) {
		observation.LastOutcome = "update_failed"
		observation.ErrorCode = "future_unreviewed_error"
	})
	if err == nil {
		t.Fatal("transition accepted an unknown error code")
	}
	if after := coordinator.Snapshot(); after != before {
		t.Fatalf("snapshot changed after invalid transition: before=%+v after=%+v", before, after)
	}
}

func TestWSHeartbeatCarriesCurrentUpdateObservation(t *testing.T) {
	coordinator := newTestUpdateObservationCoordinator(t, filepath.Join(t.TempDir(), "daemon-update-status.json"))
	if err := coordinator.Transition(func(observation *protocol.DaemonUpdateObservation) {
		observation.Phase = "checking"
		observation.AttemptSource = "auto"
		observation.LastAttemptAt = "2026-07-27T00:00:00Z"
	}); err != nil {
		t.Fatalf("transition: %v", err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	daemon := &Daemon{
		logger:            logger,
		updateObservation: coordinator,
		runtimeIndex:      map[string]Runtime{"runtime-a": {ID: "runtime-a"}},
	}
	writes := make(chan []byte, 2)
	daemon.sendWSHeartbeats(context.Background(), []string{"runtime-a"}, writes)

	var frame protocol.Message
	if err := json.Unmarshal(<-writes, &frame); err != nil {
		t.Fatalf("decode heartbeat frame: %v", err)
	}
	if frame.Type != protocol.EventDaemonHeartbeat {
		t.Fatalf("frame type = %q, want %q", frame.Type, protocol.EventDaemonHeartbeat)
	}
	var heartbeat protocol.DaemonHeartbeatRequestPayload
	if err := json.Unmarshal(frame.Payload, &heartbeat); err != nil {
		t.Fatalf("decode heartbeat payload: %v", err)
	}
	current := coordinator.Snapshot()
	if heartbeat.UpdateObservation == nil || *heartbeat.UpdateObservation != current {
		t.Fatalf("heartbeat observation = %+v, current = %+v", heartbeat.UpdateObservation, current)
	}
}

// TestUpdateObservationCoordinatorAcceptsPinnedOutcome verifies that "pinned"
// is a valid LastOutcome value. Task #57 (#1690) writes this outcome when the
// daemon is version-pinned; if it's missing from the validation whitelist,
// the observation write is silently rejected and the pinned status is
// invisible to operators.
func TestUpdateObservationCoordinatorAcceptsPinnedOutcome(t *testing.T) {
	coord := newTestUpdateObservationCoordinator(t, filepath.Join(t.TempDir(), "daemon-update-status.json"))

	err := coord.Transition(func(o *protocol.DaemonUpdateObservation) {
		o.Phase = "waiting"
		o.LastOutcome = "pinned"
		o.TargetVersion = "0.3.94"
		o.ErrorMessage = "This machine is pinned to version 0.3.94."
	})
	if err != nil {
		t.Fatalf("Transition with outcome 'pinned' was rejected: %v — 'pinned' must be in the valid outcomes whitelist", err)
	}

	snap := coord.Snapshot()
	if snap.LastOutcome != "pinned" {
		t.Fatalf("expected LastOutcome 'pinned', got %q", snap.LastOutcome)
	}
}
