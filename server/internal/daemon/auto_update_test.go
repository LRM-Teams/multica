package daemon

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/cli"
)

// #2379 regression guard: the detection path (autoUpdateLoop/checkForNewerRelease)
// and the legacy tryAutoUpdate helper have no authority to fetch, stage,
// activate, or restart a release. Detection only records a target version on
// the update observation; install belongs to a canonical explicit Machine
// Upgrade operation only.
func TestAutoUpdateDetectionNeverMutatesRelease(t *testing.T) {
	var mutations atomic.Int64
	d := &Daemon{
		cfg: Config{CLIVersion: "v9.9.9", AutoUpdateEnabled: true, AutoUpdateCheckInterval: time.Nanosecond},
		runUpdateFn: func(string) (string, error) {
			mutations.Add(1)
			return "", nil
		},
		verifyUpdatedBinaryFn: func(string, string) (string, error) {
			mutations.Add(1)
			return "", nil
		},
		activateStagedFn: func(context.Context, string, string) (string, error) {
			mutations.Add(1)
			return "", nil
		},
		cancelFunc: func() { mutations.Add(1) },
	}
	// updateObservation is nil, so the detection path returns without doing
	// anything (no manifest fetch, no observation write); tryAutoUpdate is a
	// no-op. Neither should ever touch the release-mutation seams.
	for range 100 {
		d.checkForNewerRelease(context.Background())
		d.tryAutoUpdate(context.Background())
	}
	if got := mutations.Load(); got != 0 {
		t.Fatalf("detection/legacy path mutated %d times", got)
	}
}

func TestAutoUpdateLoopSkipsWhenIntervalDisabled(t *testing.T) {
	// An unconfigured daemon (interval <= 0) must not start a detection
	// loop that blocks on a timer/ticker; it returns immediately.
	done := make(chan struct{})
	go func() {
		(&Daemon{cfg: Config{AutoUpdateCheckInterval: 0}}).autoUpdateLoop(context.Background())
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("autoUpdateLoop scheduled a delay/ticker when interval is disabled")
	}
}

// TestAutoUpdateDetectionRecordsNewerTarget covers the core detection semantic:
// with a real updateObservation and a fake release feed advertising a newer
// release, checkForNewerRelease must record that release as TargetVersion on the
// observation (which the heartbeat then reports to the server -> frontend).
func TestAutoUpdateDetectionRecordsNewerTarget(t *testing.T) {
	// Fake release feed serving a newer release than the running CLI version.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/metainfo.json" {
			http.Error(w, "wrong release document", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"schema_version":1,"environments":{"production":{"version":"9.9.10","tag":"v9.9.10","platforms":{}},"test":{"version":"9.9.11-alpha.1","tag":"v9.9.11-alpha.1","platforms":{}}}}`))
	}))
	defer srv.Close()
	t.Setenv(cli.ReleaseManifestBaseURLEnv, srv.URL)

	coordinator := newUpdateObservationCoordinator(Config{
		ServerBaseURL: "https://api.multica.ai",
		CLIVersion:    "v9.9.9",
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	d := &Daemon{
		cfg:               Config{CLIVersion: "v9.9.9"},
		updateObservation: coordinator,
		logger:            slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	d.checkForNewerRelease(context.Background())

	obs := coordinator.Snapshot()
	if obs.TargetVersion != "v9.9.10" {
		t.Fatalf("TargetVersion = %q, want v9.9.10 (recorded from fake feed)", obs.TargetVersion)
	}
}

func TestAutoUpdateDetectionReadsTestFromCanonicalMetainfo(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.URL.Path != "/metainfo.json" {
			http.Error(w, "wrong channel", http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(`{"schema_version":1,"environments":{"production":{"version":"9.9.9","tag":"v9.9.9","platforms":{}},"test":{"version":"10.0.0-alpha.2","tag":"v10.0.0-alpha.2","platforms":{}}}}`))
	}))
	defer srv.Close()
	t.Setenv(cli.ReleaseManifestBaseURLEnv, srv.URL)
	coordinator := newUpdateObservationCoordinator(Config{CLIVersion: "v10.0.0-alpha.1"}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	d := &Daemon{
		cfg:               Config{CLIVersion: "v10.0.0-alpha.1", ReleaseChannel: "alpha"},
		updateObservation: coordinator,
		logger:            slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	d.checkForNewerRelease(context.Background())
	if got := coordinator.Snapshot().TargetVersion; got != "v10.0.0-alpha.2" {
		t.Fatalf("TargetVersion = %q", got)
	}
	if len(paths) != 1 || paths[0] != "/metainfo.json" {
		t.Fatalf("paths = %v", paths)
	}
}
