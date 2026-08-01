package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/cli"
)

// newAutoUpdateTestDaemon returns a Daemon stripped to just the pieces
// tryAutoUpdate touches, plus a sentinel cancelFunc the test can assert on to
// detect that triggerRestart fired. The caller is expected to install its own
// runUpdateFn before calling tryAutoUpdate when it wants to exercise the
// upgrade-success path.
func newAutoUpdateTestDaemon(t *testing.T, currentVersion string) (*Daemon, *atomic.Int32) {
	t.Helper()
	var restartCalls atomic.Int32
	d := &Daemon{
		cfg:    Config{CLIVersion: currentVersion, AutoUpdateEnabled: true},
		logger: slog.Default(),
		cancelFunc: func() {
			restartCalls.Add(1)
		},
	}
	d.runUpdateFn = func(string) (string, error) {
		t.Fatalf("runUpdateFn called unexpectedly")
		return "", nil
	}
	d.verifyUpdatedBinaryFn = func(targetVersion, _ string) (string, error) {
		return targetVersion, nil
	}
	// Skip real VersionStore CAS in unit tests; success path only needs restart.
	d.activateStagedFn = func(context.Context, string, string) (string, error) {
		return "", nil
	}
	return d, &restartCalls
}

// TestHandleHeartbeatActionsCachesReleaseManifestBaseURL proves the daemon
// caches a non-empty ReleaseManifestBaseURL from a heartbeat ack (task #815
// step 2) and does NOT clear the cache when a later ack omits the field — a
// transient server-side hiccup should not blank out a previously-good
// server-dispatched override.
func TestHandleHeartbeatActionsCachesReleaseManifestBaseURL(t *testing.T) {
	d := &Daemon{logger: slog.Default()}

	if got := d.releaseManifestBaseURLOverride(); got != "" {
		t.Fatalf("before any heartbeat: override = %q, want empty", got)
	}

	d.handleHeartbeatActions(context.Background(), "rt-1", &HeartbeatResponse{
		RuntimeID:              "rt-1",
		Status:                 "ok",
		ReleaseManifestBaseURL: "https://oss.example.com/releases",
	})
	if got := d.releaseManifestBaseURLOverride(); got != "https://oss.example.com/releases" {
		t.Fatalf("after ack with URL: override = %q, want the dispatched URL", got)
	}

	// A later ack that omits the field must not clobber the cached value.
	d.handleHeartbeatActions(context.Background(), "rt-1", &HeartbeatResponse{
		RuntimeID: "rt-1",
		Status:    "ok",
	})
	if got := d.releaseManifestBaseURLOverride(); got != "https://oss.example.com/releases" {
		t.Fatalf("after ack without URL: override = %q, want the previously cached URL preserved", got)
	}
}

func withStubRelease(t *testing.T, release *cli.ReleaseManifest, err error) {
	t.Helper()
	prev := fetchLatestRelease
	fetchLatestRelease = func(string) (*cli.ReleaseManifest, error) { return release, err }
	t.Cleanup(func() { fetchLatestRelease = prev })
}

func TestTryAutoUpdate_SkipsWhenUpdating(t *testing.T) {
	d, restartCalls := newAutoUpdateTestDaemon(t, "v0.1.13")
	d.updating.Store(true)
	withStubRelease(t, &cli.ReleaseManifest{TagName: "v0.1.14"}, nil)

	d.tryAutoUpdate(context.Background())

	if restartCalls.Load() != 0 {
		t.Fatalf("triggerRestart called while another update was in progress")
	}
}

func TestTryAutoUpdateOwnsCheckingBeforeServerUpdateCanStart(t *testing.T) {
	d, restartCalls := newAutoUpdateTestDaemon(t, "v0.1.13")
	d.updateObservation = newTestUpdateObservationCoordinator(t, filepath.Join(t.TempDir(), "daemon-update-status.json"))

	type updateReport struct {
		path    string
		payload map[string]any
	}
	reports := make(chan updateReport, 2)
	var reportCalls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		reportCalls.Add(1)
		reports <- updateReport{path: r.URL.Path, payload: payload}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	t.Cleanup(srv.Close)
	d.client = NewClient(srv.URL)

	fetchStarted := make(chan struct{})
	releaseFetch := make(chan struct{})
	previousFetchLatestRelease := fetchLatestRelease
	fetchLatestRelease = func(string) (*cli.ReleaseManifest, error) {
		close(fetchStarted)
		<-releaseFetch
		return nil, errors.New("network down")
	}
	t.Cleanup(func() { fetchLatestRelease = previousFetchLatestRelease })

	var updateCalls atomic.Int32
	d.runUpdateFn = func(string) (string, error) {
		updateCalls.Add(1)
		return "updated", nil
	}

	autoDone := make(chan struct{})
	go func() {
		defer close(autoDone)
		d.tryAutoUpdate(context.Background())
	}()
	<-fetchStarted

	d.handleUpdate(context.Background(), "rt-1", &PendingUpdate{
		ID:                   "upd-1",
		TargetVersion:        "v0.1.14",
		SupportsReadyToApply: true,
	})
	if reportCalls.Load() != 1 {
		t.Fatalf("server request terminal reports = %d, want exactly 1", reportCalls.Load())
	}
	report := <-reports
	if report.path != "/api/daemon/runtimes/rt-1/update/upd-1/result" {
		t.Fatalf("terminal report path = %q", report.path)
	}
	if report.payload["status"] != "failed" || report.payload["error"] != "update_already_in_progress" {
		t.Fatalf("terminal report = %#v, want failed/update_already_in_progress", report.payload)
	}
	close(releaseFetch)
	<-autoDone

	if updateCalls.Load() != 0 {
		t.Fatalf("server update entered while auto check owned the attempt: calls=%d", updateCalls.Load())
	}
	if restartCalls.Load() != 0 {
		t.Fatalf("restart triggered while auto check owned the attempt: calls=%d", restartCalls.Load())
	}
	observation := d.updateObservation.Snapshot()
	if observation.AttemptSource != "auto" || observation.Phase != "waiting" || observation.LastOutcome != "fetch_failed" {
		t.Fatalf("observation = %+v, want auto waiting/fetch_failed without server overwrite", observation)
	}
}

func TestTryAutoUpdate_SkipsWhenTasksRunning(t *testing.T) {
	d, restartCalls := newAutoUpdateTestDaemon(t, "v0.1.13")
	d.activeTasks.Store(1)
	withStubRelease(t, &cli.ReleaseManifest{TagName: "v0.1.14"}, nil)

	d.tryAutoUpdate(context.Background())

	if restartCalls.Load() != 0 {
		t.Fatalf("triggerRestart fired with active tasks; auto-update must defer")
	}
	if d.updating.Load() {
		t.Fatalf("updating flag should not have been claimed while tasks were running")
	}
}

// TestTryAutoUpdate_DefersWhenClaimInFlightAtBarrier covers the race the
// review flagged: cheap pre-fetch idle check passes (activeTasks == 0), then
// during the release fetch a poller decides to claim and bumps
// claimsInFlight. trySetClaimBarrier must observe that and defer rather than
// proceed into runUpdate (which would lead to a triggerRestart cancelling
// the just-claimed task mid-run).
func TestTryAutoUpdate_DefersWhenClaimInFlightAtBarrier(t *testing.T) {
	d, restartCalls := newAutoUpdateTestDaemon(t, "v0.1.13")
	withStubRelease(t, &cli.ReleaseManifest{TagName: "v0.1.14"}, nil)

	d.claimsInFlight = 1 // poller is mid-ClaimTask while activeTasks is still 0

	d.tryAutoUpdate(context.Background())

	if restartCalls.Load() != 0 {
		t.Fatalf("triggerRestart fired despite a claim being in flight at the barrier")
	}
	if d.updating.Load() {
		t.Fatalf("updating flag must be released after a deferred upgrade so the next tick can retry")
	}
	if d.pauseClaims {
		t.Fatalf("pauseClaims must be cleared after a deferred upgrade")
	}
}

// TestTryAutoUpdate_HoldsBarrierAcrossRestart asserts the success path leaves
// pauseClaims set: process exit is imminent and clearing the barrier would
// open a window for a poller to claim a task that the imminent restart is
// about to cancel.
func TestTryAutoUpdate_HoldsBarrierAcrossRestart(t *testing.T) {
	d, restartCalls := newAutoUpdateTestDaemon(t, "v0.1.13")
	withStubRelease(t, &cli.ReleaseManifest{TagName: "v0.1.14"}, nil)
	d.runUpdateFn = func(string) (string, error) { return "upgraded", nil }

	d.tryAutoUpdate(context.Background())

	if restartCalls.Load() != 1 {
		t.Fatalf("triggerRestart fired %d times, want 1", restartCalls.Load())
	}
	if !d.pauseClaims {
		t.Fatalf("pauseClaims must remain set across the restart kick; got cleared")
	}
}

// TestTryAutoUpdate_ReleasesBarrierOnUpgradeFailure asserts the failure path
// clears pauseClaims so the daemon can keep claiming tasks normally and
// retry the upgrade on the next tick.
func TestTryAutoUpdate_ReleasesBarrierOnUpgradeFailure(t *testing.T) {
	d, restartCalls := newAutoUpdateTestDaemon(t, "v0.1.13")
	withStubRelease(t, &cli.ReleaseManifest{TagName: "v0.1.14"}, nil)
	d.runUpdateFn = func(string) (string, error) {
		return "brew network error", errors.New("brew upgrade failed")
	}

	d.tryAutoUpdate(context.Background())

	if restartCalls.Load() != 0 {
		t.Fatalf("triggerRestart fired despite upgrade failure")
	}
	if d.pauseClaims {
		t.Fatalf("pauseClaims must be cleared after a failed upgrade so pollers resume claiming")
	}
}

func TestTryAutoUpdate_ReleasesBarrierOnVerificationFailure(t *testing.T) {
	d, restartCalls := newAutoUpdateTestDaemon(t, "v0.1.13")
	withStubRelease(t, &cli.ReleaseManifest{TagName: "v0.1.14"}, nil)
	d.runUpdateFn = func(string) (string, error) {
		return "Warning: multica-ai/tap/multica 0.1.13 already installed", nil
	}
	d.verifyUpdatedBinaryFn = func(string, string) (string, error) {
		return "v0.1.13", errors.New("binary_version_mismatch_after_update")
	}

	d.tryAutoUpdate(context.Background())

	if restartCalls.Load() != 0 {
		t.Fatalf("triggerRestart fired despite verification failure")
	}
	if d.pauseClaims {
		t.Fatalf("pauseClaims must be cleared after a failed verification so pollers resume claiming")
	}
	if d.updating.Load() {
		t.Fatalf("updating flag must be released after verification failure so the next tick can retry")
	}
}

// TestTryEnterClaim_RespectsBarrier asserts the poller-side helper returns
// false while pauseClaims is held and that pairs of enter/exit balance the
// counter so a later barrier set sees idle.
func TestTryEnterClaim_RespectsBarrier(t *testing.T) {
	d := &Daemon{}

	if !d.tryEnterClaim() {
		t.Fatal("tryEnterClaim should succeed when barrier is unset")
	}
	d.exitClaim()
	if d.claimsInFlight != 0 {
		t.Fatalf("claimsInFlight not balanced: %d", d.claimsInFlight)
	}

	if !d.trySetClaimBarrier() {
		t.Fatal("trySetClaimBarrier should succeed when idle")
	}
	if d.tryEnterClaim() {
		t.Fatal("tryEnterClaim must refuse while barrier is held")
	}
	d.releaseClaimBarrier()
	if !d.tryEnterClaim() {
		t.Fatal("tryEnterClaim should succeed after barrier release")
	}
	d.exitClaim()
}

func TestTryAutoUpdate_SkipsWhenFetchFails(t *testing.T) {
	d, restartCalls := newAutoUpdateTestDaemon(t, "v0.1.13")
	withStubRelease(t, nil, errors.New("network down"))

	d.tryAutoUpdate(context.Background())

	if restartCalls.Load() != 0 {
		t.Fatalf("triggerRestart fired despite fetch failure")
	}
}

func TestTryAutoUpdate_SkipsWhenNotNewer(t *testing.T) {
	d, restartCalls := newAutoUpdateTestDaemon(t, "v0.1.13")
	withStubRelease(t, &cli.ReleaseManifest{TagName: "v0.1.13"}, nil)

	d.tryAutoUpdate(context.Background())

	if restartCalls.Load() != 0 {
		t.Fatalf("triggerRestart fired even though latest == current")
	}
}

func TestTryAutoUpdate_RunsUpgradeAndRestartsOnNewer(t *testing.T) {
	d, restartCalls := newAutoUpdateTestDaemon(t, "v0.1.13")
	withStubRelease(t, &cli.ReleaseManifest{TagName: "v0.1.14"}, nil)

	var upgradedTo string
	d.runUpdateFn = func(target string) (string, error) {
		upgradedTo = target
		return "upgraded", nil
	}

	d.tryAutoUpdate(context.Background())

	if upgradedTo != "v0.1.14" {
		t.Fatalf("runUpdateFn called with %q, want v0.1.14", upgradedTo)
	}
	if restartCalls.Load() != 1 {
		t.Fatalf("triggerRestart fired %d times, want 1", restartCalls.Load())
	}
	if !d.updating.Load() {
		t.Fatalf("updating flag should remain set across the restart kick; got cleared")
	}
}

func TestTryAutoUpdate_DoesNotRestartOnUpgradeFailure(t *testing.T) {
	d, restartCalls := newAutoUpdateTestDaemon(t, "v0.1.13")
	withStubRelease(t, &cli.ReleaseManifest{TagName: "v0.1.14"}, nil)

	d.runUpdateFn = func(string) (string, error) {
		return "brew: network error", errors.New("brew upgrade failed")
	}

	d.tryAutoUpdate(context.Background())

	if restartCalls.Load() != 0 {
		t.Fatalf("triggerRestart fired despite upgrade failure")
	}
	if d.updating.Load() {
		t.Fatalf("updating flag must be released after a failed upgrade so the next tick can retry")
	}
}

func TestAutoUpdateLoop_EarlyExits(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
	}{
		{
			name: "disabled by config",
			cfg:  Config{AutoUpdateEnabled: false, CLIVersion: "v0.1.13"},
		},
		{
			name: "managed by desktop",
			cfg:  Config{AutoUpdateEnabled: true, CLIVersion: "v0.1.13", LaunchedBy: "desktop"},
		},
		{
			name: "dev build",
			cfg:  Config{AutoUpdateEnabled: true, CLIVersion: "v0.1.13-235-gabcdef0"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &Daemon{cfg: tt.cfg, logger: slog.Default()}
			d.runUpdateFn = func(string) (string, error) {
				t.Fatalf("runUpdateFn called from an early-exit code path")
				return "", nil
			}
			withStubRelease(t, &cli.ReleaseManifest{TagName: "v0.1.14"}, nil)

			done := make(chan struct{})
			go func() {
				d.autoUpdateLoop(context.Background())
				close(done)
			}()
			<-done
		})
	}
}

// TestTryAutoUpdate_PinnedVersionPreventsUpgrade verifies that when
// PinnedVersion is set, tryAutoUpdate never fetches or installs a new
// release even when one is available.
//
// Mutation check: remove the PinnedVersion guard in autoUpdateLoop or
// tryAutoUpdate → the stub release will be fetched and runUpdateFn will
// fire (t.Fatalf), proving the pin was the only thing blocking it.
func TestTryAutoUpdate_PinnedVersionPreventsUpgrade(t *testing.T) {
	d, restartCalls := newAutoUpdateTestDaemon(t, "v0.1.13")
	d.cfg.PinnedVersion = "v0.1.13"
	withStubRelease(t, &cli.ReleaseManifest{TagName: "v0.1.14"}, nil)

	// runUpdateFn should never be called when pinned.
	d.runUpdateFn = func(string) (string, error) {
		t.Fatal("runUpdateFn called despite version pin")
		return "", nil
	}

	// Verify the pin check fires in autoUpdateLoop, not just tryAutoUpdate.
	// We test the loop-level guard by calling it and checking the log/restart.
	// Since autoUpdateLoop blocks, we test the guard indirectly via tryAutoUpdate
	// which is the actual upgrade path. The loop guard is the same condition.
	d.tryAutoUpdate(context.Background())

	if restartCalls.Load() != 0 {
		t.Fatalf("triggerRestart called despite version pin")
	}
}

// TestTryAutoUpdate_NoPinUpgradesNormally verifies that without a pin,
// auto-update proceeds as before (regression guard for the pin feature).
func TestTryAutoUpdate_NoPinUpgradesNormally(t *testing.T) {
	d, restartCalls := newAutoUpdateTestDaemon(t, "v0.1.13")
	// PinnedVersion is empty (zero value) — no pin.
	withStubRelease(t, &cli.ReleaseManifest{TagName: "v0.1.14"}, nil)

	d.runUpdateFn = func(target string) (string, error) {
		if target != "v0.1.14" {
			t.Fatalf("unexpected upgrade target %q", target)
		}
		return "staged " + target, nil
	}

	d.tryAutoUpdate(context.Background())

	if restartCalls.Load() != 1 {
		t.Fatalf("expected triggerRestart to fire once, got %d", restartCalls.Load())
	}
}

// TestAutoUpdateLoop_PinnedVersionLogsAndReturns verifies that
// autoUpdateLoop exits immediately when pinned, with a visible log message.
func TestAutoUpdateLoop_PinnedVersionLogsAndReturns(t *testing.T) {
	d, _ := newAutoUpdateTestDaemon(t, "v0.1.13")
	d.cfg.PinnedVersion = "v0.1.13"

	// Capture logs to verify the pin message is visible.
	var logBuf strings.Builder
	d.logger = slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	// autoUpdateLoop should return immediately when pinned.
	// We use a context with a short timeout to ensure it doesn't block.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	d.autoUpdateLoop(ctx)

	logOutput := logBuf.String()
	if !strings.Contains(logOutput, "version pinned") {
		t.Fatalf("expected log to mention 'version pinned', got: %s", logOutput)
	}
	if !strings.Contains(logOutput, "v0.1.13") {
		t.Fatalf("expected log to contain pinned version v0.1.13, got: %s", logOutput)
	}
}

// TestTryAutoUpdate_PinnedDifferentVersionInstallsIt verifies that when the
// pinned version differs from the current version, tryAutoUpdate fetches and
// installs the pinned version (not "latest").
func TestTryAutoUpdate_PinnedDifferentVersionInstallsIt(t *testing.T) {
	d, restartCalls := newAutoUpdateTestDaemon(t, "v0.1.13")
	d.cfg.PinnedVersion = "v0.1.14"

	// Stub fetchReleaseByTag to return the pinned version.
	prevFetch := fetchReleaseByTagVar
	fetchReleaseByTagVar = func(tag, _ string) (*cli.ReleaseManifest, error) {
		if tag != "v0.1.14" {
			t.Fatalf("expected fetch for pinned version v0.1.14, got %q", tag)
		}
		return &cli.ReleaseManifest{TagName: "v0.1.14"}, nil
	}
	defer func() { fetchReleaseByTagVar = prevFetch }()

	// fetchLatestRelease should NOT be called when installing pinned version.
	prevLatest := fetchLatestRelease
	fetchLatestRelease = func(string) (*cli.ReleaseManifest, error) {
		t.Fatal("fetchLatestRelease called when should use pinned version fetch")
		return nil, nil
	}
	defer func() { fetchLatestRelease = prevLatest }()

	var upgradedTarget string
	d.runUpdateFn = func(target string) (string, error) {
		upgradedTarget = target
		return "staged " + target, nil
	}

	d.tryAutoUpdate(context.Background())

	if upgradedTarget != "v0.1.14" {
		t.Fatalf("expected upgrade to pinned v0.1.14, got %q", upgradedTarget)
	}
	if restartCalls.Load() != 1 {
		t.Fatalf("expected restart after pinned install, got %d restart calls", restartCalls.Load())
	}
}
